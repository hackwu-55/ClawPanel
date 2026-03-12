package handler

import (
	"errors"
	"io"
	"net/http"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

func TestLoadSkillHubCatalogDoesNotPoisonLastGoodURLWhenDiscoveredURLFails(t *testing.T) {
	restore := resetSkillHubTestState(t)
	defer restore()

	badDynamicURL := skillHubCDNBase + "skills.deadbeef.json"
	skillHubHTTPClient = &http.Client{Transport: roundTripFunc(func(req *http.Request) (*http.Response, error) {
		switch req.URL.String() {
		case skillHubHomepage:
			return newHTTPResponse(http.StatusOK, `<script src="/assets/main.js"></script>skills.deadbeef.json`), nil
		case badDynamicURL:
			return newHTTPResponse(http.StatusNotFound, `not found`), nil
		case skillHubBootstrapURL:
			return newHTTPResponse(http.StatusOK, validSkillHubCatalogJSON("bootstrap-skill")), nil
		default:
			return nil, errors.New("unexpected url: " + req.URL.String())
		}
	})}

	catalog, err := loadSkillHubCatalog()
	if err != nil {
		t.Fatalf("loadSkillHubCatalog error: %v", err)
	}
	if catalog == nil || len(catalog.Skills) != 1 || catalog.Skills[0].Slug != "bootstrap-skill" {
		t.Fatalf("expected bootstrap catalog, got %#v", catalog)
	}
	if skillHubLastGoodURL != skillHubBootstrapURL {
		t.Fatalf("expected lastGoodURL %q, got %q", skillHubBootstrapURL, skillHubLastGoodURL)
	}
}

func TestLoadSkillHubCatalogReturnsStaleCacheWhenRefreshFails(t *testing.T) {
	restore := resetSkillHubTestState(t)
	defer restore()

	stale := &skillHubCatalog{
		Total:       1,
		GeneratedAt: "2026-03-01T13:44:23Z",
		Featured:    []string{"cached-skill"},
		Categories:  map[string][]string{"AI 智能": []string{"ai"}},
		Skills:      []skillHubSkillItem{{Slug: "cached-skill", Name: "Cached Skill"}},
	}
	skillHubCache = stale
	skillHubCacheTime = time.Now().Add(-2 * skillHubCacheTTL)
	skillHubLastGoodURL = skillHubCDNBase + "skills.cached.json"

	skillHubHTTPClient = &http.Client{Transport: roundTripFunc(func(req *http.Request) (*http.Response, error) {
		switch req.URL.String() {
		case skillHubHomepage:
			return nil, errors.New("homepage unavailable")
		case skillHubLastGoodURL, skillHubBootstrapURL:
			return nil, errors.New("upstream unavailable")
		default:
			return nil, errors.New("unexpected url: " + req.URL.String())
		}
	})}

	got, err := loadSkillHubCatalog()
	if err != nil {
		t.Fatalf("expected stale cache fallback, got error: %v", err)
	}
	if got != stale {
		t.Fatalf("expected stale cache pointer, got %#v", got)
	}
}

func TestLoadSkillHubCatalogErrorsWithoutAnySuccessfulCatalog(t *testing.T) {
	restore := resetSkillHubTestState(t)
	defer restore()

	skillHubHTTPClient = &http.Client{Transport: roundTripFunc(func(req *http.Request) (*http.Response, error) {
		switch req.URL.String() {
		case skillHubHomepage:
			return nil, errors.New("homepage unavailable")
		case skillHubBootstrapURL:
			return nil, errors.New("bootstrap unavailable")
		default:
			return nil, errors.New("unexpected url: " + req.URL.String())
		}
	})}

	if _, err := loadSkillHubCatalog(); err == nil {
		t.Fatalf("expected error when no cache and upstream unavailable")
	}
}

func TestLoadSkillHubCatalogSkipsRefreshDuringRetryBackoff(t *testing.T) {
	restore := resetSkillHubTestState(t)
	defer restore()

	stale := &skillHubCatalog{
		Total:       1,
		GeneratedAt: "2026-03-01T13:44:23Z",
		Featured:    []string{"cached-skill"},
		Categories:  map[string][]string{"AI 智能": []string{"ai"}},
		Skills:      []skillHubSkillItem{{Slug: "cached-skill", Name: "Cached Skill"}},
	}
	skillHubCache = stale
	skillHubCacheTime = time.Now().Add(-2 * skillHubCacheTTL)
	skillHubNextRetryTime = time.Now().Add(2 * time.Minute)

	calls := 0
	skillHubHTTPClient = &http.Client{Transport: roundTripFunc(func(req *http.Request) (*http.Response, error) {
		calls++
		return nil, errors.New("network should not be used during retry backoff")
	})}

	got, err := loadSkillHubCatalog()
	if err != nil {
		t.Fatalf("expected stale cache during retry backoff, got error: %v", err)
	}
	if got != stale {
		t.Fatalf("expected stale cache pointer, got %#v", got)
	}
	if calls != 0 {
		t.Fatalf("expected no upstream calls during retry backoff, got %d", calls)
	}
}

func TestLoadSkillHubCatalogRetriesWithoutCacheEvenIfRetryWindowIsSet(t *testing.T) {
	restore := resetSkillHubTestState(t)
	defer restore()

	skillHubNextRetryTime = time.Now().Add(2 * time.Minute)
	skillHubLastErr = "temporary failure"

	calls := 0
	skillHubHTTPClient = &http.Client{Transport: roundTripFunc(func(req *http.Request) (*http.Response, error) {
		calls++
		switch req.URL.String() {
		case skillHubHomepage:
			return nil, errors.New("homepage unavailable")
		case skillHubBootstrapURL:
			return newHTTPResponse(http.StatusOK, validSkillHubCatalogJSON("recovered-skill")), nil
		default:
			return nil, errors.New("unexpected url: " + req.URL.String())
		}
	})}

	got, err := loadSkillHubCatalog()
	if err != nil {
		t.Fatalf("expected successful retry without cache, got error: %v", err)
	}
	if got == nil || len(got.Skills) != 1 || got.Skills[0].Slug != "recovered-skill" {
		t.Fatalf("expected recovered catalog, got %#v", got)
	}
	if calls == 0 {
		t.Fatalf("expected upstream retry when no cache exists")
	}
}

func TestLoadSkillHubCatalogReturnsStaleImmediatelyWhileRefreshInFlight(t *testing.T) {
	restore := resetSkillHubTestState(t)
	defer restore()

	stale := &skillHubCatalog{
		Total:       1,
		GeneratedAt: "2026-03-01T13:44:23Z",
		Featured:    []string{"cached-skill"},
		Categories:  map[string][]string{"AI 智能": []string{"ai"}},
		Skills:      []skillHubSkillItem{{Slug: "cached-skill", Name: "Cached Skill"}},
	}
	skillHubCache = stale
	skillHubCacheTime = time.Now().Add(-2 * skillHubCacheTTL)

	homepageStarted := make(chan struct{})
	releaseHomepage := make(chan struct{})
	var started int32
	skillHubHTTPClient = &http.Client{Transport: roundTripFunc(func(req *http.Request) (*http.Response, error) {
		switch req.URL.String() {
		case skillHubHomepage:
			if atomic.CompareAndSwapInt32(&started, 0, 1) {
				close(homepageStarted)
			}
			<-releaseHomepage
			return nil, errors.New("homepage unavailable")
		case skillHubBootstrapURL:
			return nil, errors.New("bootstrap unavailable")
		default:
			return nil, errors.New("unexpected url: " + req.URL.String())
		}
	})}

	done := make(chan struct{})
	go func() {
		defer close(done)
		_, _ = loadSkillHubCatalog()
	}()

	select {
	case <-homepageStarted:
	case <-time.After(2 * time.Second):
		t.Fatalf("timed out waiting for refresh to start")
	}

	start := time.Now()
	got, err := loadSkillHubCatalog()
	if err != nil {
		t.Fatalf("expected stale cache while refresh in flight, got error: %v", err)
	}
	if got != stale {
		t.Fatalf("expected stale cache pointer, got %#v", got)
	}
	if elapsed := time.Since(start); elapsed > 200*time.Millisecond {
		t.Fatalf("expected stale response without waiting for refresh, took %v", elapsed)
	}

	close(releaseHomepage)
	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatalf("timed out waiting for refresh goroutine to exit")
	}
}

func resetSkillHubTestState(t *testing.T) func() {
	t.Helper()
	origClient := skillHubHTTPClient
	origCache := skillHubCache
	origCacheTime := skillHubCacheTime
	origLastGood := skillHubLastGoodURL
	origNextRetry := skillHubNextRetryTime
	origLastErr := skillHubLastErr
	origRefreshInFlight := skillHubRefreshInFlight
	origRefreshDone := skillHubRefreshDone

	skillHubCache = nil
	skillHubCacheTime = time.Time{}
	skillHubLastGoodURL = ""
	skillHubNextRetryTime = time.Time{}
	skillHubLastErr = ""
	skillHubRefreshInFlight = false
	skillHubRefreshDone = nil

	return func() {
		skillHubHTTPClient = origClient
		skillHubCache = origCache
		skillHubCacheTime = origCacheTime
		skillHubLastGoodURL = origLastGood
		skillHubNextRetryTime = origNextRetry
		skillHubLastErr = origLastErr
		skillHubRefreshInFlight = origRefreshInFlight
		skillHubRefreshDone = origRefreshDone
	}
}

func newHTTPResponse(status int, body string) *http.Response {
	return &http.Response{
		StatusCode: status,
		Body:       io.NopCloser(strings.NewReader(body)),
		Header:     make(http.Header),
	}
}

func validSkillHubCatalogJSON(slug string) string {
	return `{"total":1,"generated_at":"2026-03-01T13:44:23Z","featured":["` + slug + `"],"categories":{"AI 智能":["ai"]},"skills":[{"slug":"` + slug + `","name":"` + slug + `","description":"desc","description_zh":"描述","version":"1.0.0","tags":["ai"],"downloads":1,"stars":1,"installs":1,"updated_at":1772065840450,"score":1.2,"owner":"clawhub"}]}`
}
