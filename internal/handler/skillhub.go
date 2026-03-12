package handler

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"regexp"
	"strings"
	"sync"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/zhaoxinyi02/ClawPanel/internal/config"
)

// --- S1: Types + Cache ---

type skillHubCatalog struct {
	Total       int                 `json:"total"`
	GeneratedAt string              `json:"generated_at"`
	Featured    []string            `json:"featured"`
	Categories  map[string][]string `json:"categories"`
	Skills      []skillHubSkillItem `json:"skills"`
}

type skillHubSkillItem struct {
	Slug          string   `json:"slug"`
	Name          string   `json:"name"`
	Description   string   `json:"description"`
	DescriptionZh string   `json:"description_zh"`
	Version       string   `json:"version"`
	Homepage      string   `json:"homepage"`
	Tags          []string `json:"tags"`
	Downloads     int      `json:"downloads"`
	Stars         int      `json:"stars"`
	Installs      int      `json:"installs"`
	UpdatedAt     int64    `json:"updated_at"`
	Score         float64  `json:"score"`
	Owner         string   `json:"owner"`
}

// trimmed item for API response (drop homepage, installs)
type skillHubSkillTrimmed struct {
	Slug          string   `json:"slug"`
	Name          string   `json:"name"`
	Description   string   `json:"description"`
	DescriptionZh string   `json:"description_zh"`
	Version       string   `json:"version"`
	Tags          []string `json:"tags"`
	Downloads     int      `json:"downloads"`
	Stars         int      `json:"stars"`
	UpdatedAt     int64    `json:"updated_at"`
	Score         float64  `json:"score"`
	Owner         string   `json:"owner"`
}

var (
	skillHubCache           *skillHubCatalog
	skillHubCacheTime       time.Time
	skillHubCacheMu         sync.Mutex
	skillHubLastGoodURL     string
	skillHubNextRetryTime   time.Time
	skillHubLastErr         string
	skillHubRefreshInFlight bool
	skillHubRefreshDone     chan struct{}
)

const (
	skillHubCacheTTL     = 1 * time.Hour
	skillHubBootstrapURL = "https://cloudcache.tencentcs.com/qcloud/tea/app/data/skills.805f4f80.json"
	skillHubHomepage     = "https://skillhub.tencent.com/"
	skillHubMaxBodyBytes = 16 << 20 // 16MB
	skillHubFetchTimeout = 25 * time.Second
	skillHubRetryBackoff = 5 * time.Minute
	skillHubCDNBase      = "https://cloudcache.tencentcs.com/qcloud/tea/app/data/"
)

var skillHubJSONHashRe = regexp.MustCompile(`skills\.([0-9a-f]+)\.json`)

var skillHubHTTPClient = &http.Client{Timeout: skillHubFetchTimeout}

// --- S2: URL Discovery + Handler ---

// discoverSkillHubJSONURL fetches the SkillHub homepage and extracts the
// current JSON data URL from embedded script/asset references.
func discoverSkillHubJSONURL() (string, error) {
	resp, err := skillHubHTTPClient.Get(skillHubHomepage)
	if err != nil {
		return "", fmt.Errorf("fetch skillhub homepage: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("skillhub homepage returned %d", resp.StatusCode)
	}
	body, err := io.ReadAll(io.LimitReader(resp.Body, 512*1024)) // 512KB max for HTML
	if err != nil {
		return "", fmt.Errorf("read skillhub homepage: %w", err)
	}
	matches := skillHubJSONHashRe.FindAllStringSubmatch(string(body), -1)
	if len(matches) == 0 {
		return "", fmt.Errorf("no skills JSON hash found in homepage")
	}
	// use the last match (usually in main JS bundle near bottom)
	filename := matches[len(matches)-1][0]
	return skillHubCDNBase + filename, nil
}

// resolveSkillHubJSONURLs returns candidate URLs in priority order without
// mutating the last-good state. lastGoodURL is only updated after a successful
// JSON fetch and decode.
func resolveSkillHubJSONURLs(lastGoodURL string) []string {
	urls := make([]string, 0, 3)
	seen := make(map[string]struct{}, 3)
	appendURL := func(url string) {
		if url == "" {
			return
		}
		if _, ok := seen[url]; ok {
			return
		}
		seen[url] = struct{}{}
		urls = append(urls, url)
	}
	url, err := discoverSkillHubJSONURL()
	if err == nil && url != "" {
		appendURL(url)
	}
	appendURL(lastGoodURL)
	appendURL(skillHubBootstrapURL)
	return urls
}

func fetchSkillHubCatalog(url string) (*skillHubCatalog, error) {
	resp, err := skillHubHTTPClient.Get(url)
	if err != nil {
		return nil, fmt.Errorf("fetch skillhub JSON: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("skillhub JSON returned %d", resp.StatusCode)
	}

	reader := io.LimitReader(resp.Body, skillHubMaxBodyBytes)
	var catalog skillHubCatalog
	dec := json.NewDecoder(reader)
	if err := dec.Decode(&catalog); err != nil {
		return nil, fmt.Errorf("parse skillhub JSON: %w", err)
	}
	if catalog.Skills == nil {
		return nil, fmt.Errorf("skillhub JSON missing skills list")
	}
	if catalog.Featured == nil {
		catalog.Featured = []string{}
	}
	if catalog.Categories == nil {
		catalog.Categories = map[string][]string{}
	}
	return &catalog, nil
}

func refreshSkillHubCatalog(lastGoodURL string) (*skillHubCatalog, string, error) {
	var lastErr error
	for _, jsonURL := range resolveSkillHubJSONURLs(lastGoodURL) {
		catalog, err := fetchSkillHubCatalog(jsonURL)
		if err != nil {
			lastErr = err
			continue
		}
		return catalog, jsonURL, nil
	}
	if lastErr == nil {
		lastErr = fmt.Errorf("failed to resolve skillhub catalog URL")
	}
	return nil, "", lastErr
}

func loadSkillHubCatalog() (*skillHubCatalog, error) {
	for {
		now := time.Now()

		skillHubCacheMu.Lock()
		if skillHubCache != nil && now.Sub(skillHubCacheTime) < skillHubCacheTTL {
			cached := skillHubCache
			skillHubCacheMu.Unlock()
			return cached, nil
		}
		if skillHubCache != nil && !skillHubNextRetryTime.IsZero() && now.Before(skillHubNextRetryTime) {
			cached := skillHubCache
			skillHubCacheMu.Unlock()
			return cached, nil
		}
		if skillHubRefreshInFlight {
			waitCh := skillHubRefreshDone
			cached := skillHubCache
			skillHubCacheMu.Unlock()

			if cached != nil {
				return cached, nil
			}
			if waitCh != nil {
				<-waitCh
			}

			skillHubCacheMu.Lock()
			cached = skillHubCache
			lastErr := skillHubLastErr
			skillHubCacheMu.Unlock()
			if cached != nil {
				return cached, nil
			}
			if lastErr != "" {
				return nil, fmt.Errorf("%s", lastErr)
			}
			continue
		}

		staleCache := skillHubCache
		lastGoodURL := skillHubLastGoodURL
		doneCh := make(chan struct{})
		skillHubRefreshInFlight = true
		skillHubRefreshDone = doneCh
		skillHubCacheMu.Unlock()

		catalog, jsonURL, err := refreshSkillHubCatalog(lastGoodURL)

		skillHubCacheMu.Lock()
		skillHubRefreshInFlight = false
		close(doneCh)
		skillHubRefreshDone = nil

		if err == nil {
			skillHubLastGoodURL = jsonURL
			skillHubCache = catalog
			skillHubCacheTime = time.Now()
			skillHubNextRetryTime = time.Time{}
			skillHubLastErr = ""
			skillHubCacheMu.Unlock()
			return catalog, nil
		}

		skillHubLastErr = err.Error()
		if staleCache != nil {
			skillHubNextRetryTime = time.Now().Add(skillHubRetryBackoff)
			cached := skillHubCache
			if cached == nil {
				cached = staleCache
			}
			skillHubCacheMu.Unlock()
			return cached, nil
		}
		skillHubNextRetryTime = time.Time{}
		skillHubCacheMu.Unlock()
		return nil, err
	}
}

func trimSkillHubSkills(skills []skillHubSkillItem) []skillHubSkillTrimmed {
	out := make([]skillHubSkillTrimmed, len(skills))
	for i, s := range skills {
		out[i] = skillHubSkillTrimmed{
			Slug:          s.Slug,
			Name:          s.Name,
			Description:   s.Description,
			DescriptionZh: s.DescriptionZh,
			Version:       s.Version,
			Tags:          s.Tags,
			Downloads:     s.Downloads,
			Stars:         s.Stars,
			UpdatedAt:     s.UpdatedAt,
			Score:         s.Score,
			Owner:         s.Owner,
		}
	}
	return out
}

// GetSkillHubCatalog returns the SkillHub catalog data.
func GetSkillHubCatalog(cfg *config.Config) gin.HandlerFunc {
	return func(c *gin.Context) {
		catalog, err := loadSkillHubCatalog()
		if err != nil {
			errMsg := err.Error()
			// sanitize internal URLs from error message
			if strings.Contains(errMsg, "cloudcache.tencentcs.com") {
				errMsg = "failed to load SkillHub data from upstream"
			}
			c.JSON(http.StatusBadGateway, gin.H{"ok": false, "error": errMsg})
			return
		}

		c.JSON(http.StatusOK, gin.H{
			"ok":          true,
			"total":       catalog.Total,
			"generatedAt": catalog.GeneratedAt,
			"featured":    catalog.Featured,
			"categories":  catalog.Categories,
			"skills":      trimSkillHubSkills(catalog.Skills),
		})
	}
}
