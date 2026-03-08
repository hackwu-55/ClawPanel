package handler

import (
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"

	"github.com/gin-gonic/gin"
	"github.com/goccy/go-yaml"
	"github.com/zhaoxinyi02/ClawPanel/internal/config"
)

type skillInfo struct {
	ID          string                 `json:"id"`
	Name        string                 `json:"name"`
	Description string                 `json:"description"`
	Version     string                 `json:"version,omitempty"`
	Enabled     bool                   `json:"enabled"`
	Path        string                 `json:"path"`
	SkillKey    string                 `json:"skillKey,omitempty"`
	Source      string                 `json:"source,omitempty"`
	Metadata    map[string]interface{} `json:"metadata,omitempty"`
	Requires    map[string]interface{} `json:"requires,omitempty"`
}

type pluginInfo struct {
	ID          string `json:"id"`
	Name        string `json:"name"`
	Description string `json:"description"`
	Version     string `json:"version,omitempty"`
	Enabled     bool   `json:"enabled"`
	Source      string `json:"source,omitempty"`
	Path        string `json:"path"`
}

type skillDiscoveryRoot struct {
	Dir    string
	Source string
}

type pluginDiscoveryCandidate struct {
	ID     string
	Path   string
	Source string
}

const (
	maxSkillScanDepth = 3
	maxSkillFileSize  = 2 * 1024 * 1024
	maxSkillsPerRoot  = 256
)

var skillFrontmatterRegexp = regexp.MustCompile(`(?s)\A---\r?\n(.*?)\r?\n---\r?\n?`)

// GetSkills returns installed skills and plugins from OpenClaw.
func GetSkills(cfg *config.Config) gin.HandlerFunc {
	return func(c *gin.Context) {
		agentID, err := resolveRequestedAgentID(cfg, c.Query("agentId"))
		if err != nil {
			c.JSON(http.StatusBadRequest, gin.H{"ok": false, "error": err.Error()})
			return
		}

		ocConfig, _ := cfg.ReadOpenClawJSON()
		if ocConfig == nil {
			ocConfig = map[string]interface{}{}
		}

		skillsCfg := asMapAny(ocConfig["skills"])
		skillEntries := asMapAny(skillsCfg["entries"])
		legacyBlocklist := readLegacySkillBlocklist(skillsCfg)
		pluginEntries := asMapAny(asMapAny(ocConfig["plugins"])["entries"])
		pluginInstalls := asMapAny(asMapAny(ocConfig["plugins"])["installs"])

		plugins := discoverPlugins(cfg, pluginEntries, pluginInstalls)
		sort.Slice(plugins, func(i, j int) bool {
			return strings.ToLower(plugins[i].Name) < strings.ToLower(plugins[j].Name)
		})

		roots := resolveSkillDiscoveryRoots(cfg, ocConfig, plugins, agentID)
		skills := discoverSkills(roots, skillEntries, legacyBlocklist)

		c.JSON(http.StatusOK, gin.H{
			"ok":        true,
			"agentId":   agentID,
			"workspace": resolveSkillsWorkspace(cfg, agentID),
			"skills":    skills,
			"plugins":   plugins,
		})
	}
}

// ToggleSkill toggles a skill by writing skills.entries.<key>.enabled.
func ToggleSkill(cfg *config.Config) gin.HandlerFunc {
	type reqBody struct {
		Enabled bool     `json:"enabled"`
		Aliases []string `json:"aliases,omitempty"`
	}
	return func(c *gin.Context) {
		key := strings.TrimSpace(c.Param("id"))
		if key == "" {
			c.JSON(http.StatusBadRequest, gin.H{"ok": false, "error": "missing skill id"})
			return
		}
		var req reqBody
		if err := c.ShouldBindJSON(&req); err != nil {
			c.JSON(http.StatusBadRequest, gin.H{"ok": false, "error": err.Error()})
			return
		}

		ocConfig, _ := cfg.ReadOpenClawJSON()
		if ocConfig == nil {
			ocConfig = map[string]interface{}{}
		}
		skillsCfg := asMapAny(ocConfig["skills"])
		entries := asMapAny(skillsCfg["entries"])
		entry := asMapAny(entries[key])
		entry["enabled"] = req.Enabled
		entries[key] = entry
		skillsCfg["entries"] = entries
		removeLegacyBlocklistEntries(skillsCfg, append([]string{key}, req.Aliases...)...)
		ocConfig["skills"] = skillsCfg
		if err := cfg.WriteOpenClawJSON(ocConfig); err != nil {
			c.JSON(http.StatusInternalServerError, gin.H{"ok": false, "error": err.Error()})
			return
		}
		c.JSON(http.StatusOK, gin.H{"ok": true})
	}
}

// GetCronJobs returns cron jobs from openclaw.json cron.jobs.
func GetCronJobs(cfg *config.Config) gin.HandlerFunc {
	return func(c *gin.Context) {
		oc, _ := cfg.ReadOpenClawJSON()
		jobs := make([]interface{}, 0)
		if oc != nil {
			if cronCfg, ok := oc["cron"].(map[string]interface{}); ok {
				if arr, ok := cronCfg["jobs"].([]interface{}); ok {
					jobs = arr
				}
			}
		}
		if len(jobs) == 0 {
			if raw, err := os.ReadFile(filepath.Join(cfg.OpenClawDir, "cron", "jobs.json")); err == nil {
				var saved map[string]interface{}
				if json.Unmarshal(raw, &saved) == nil {
					if arr, ok := saved["jobs"].([]interface{}); ok {
						jobs = arr
					}
				}
			}
		}
		c.JSON(http.StatusOK, gin.H{"ok": true, "jobs": jobs})
	}
}

// SaveCronJobs replaces cron.jobs in openclaw.json.
func SaveCronJobs(cfg *config.Config) gin.HandlerFunc {
	type reqBody struct {
		Jobs []map[string]interface{} `json:"jobs"`
	}
	return func(c *gin.Context) {
		var req reqBody
		if err := c.ShouldBindJSON(&req); err != nil {
			c.JSON(http.StatusBadRequest, gin.H{"ok": false, "error": err.Error()})
			return
		}
		oc, _ := cfg.ReadOpenClawJSON()
		if oc == nil {
			oc = map[string]interface{}{}
		}
		if err := validateCronJobsSessionTargets(cfg, req.Jobs); err != nil {
			c.JSON(http.StatusBadRequest, gin.H{"ok": false, "error": err.Error()})
			return
		}
		cronCfg := asMapAny(oc["cron"])
		list := make([]interface{}, 0, len(req.Jobs))
		for _, job := range req.Jobs {
			copyJob := make(map[string]interface{}, len(job))
			for k, v := range job {
				copyJob[k] = v
			}
			if strings.TrimSpace(fmt.Sprint(copyJob["sessionTarget"])) == "" {
				copyJob["sessionTarget"] = loadDefaultAgentID(cfg)
			}
			list = append(list, copyJob)
		}
		cronCfg["jobs"] = list
		oc["cron"] = cronCfg
		if err := cfg.WriteOpenClawJSON(oc); err != nil {
			c.JSON(http.StatusInternalServerError, gin.H{"ok": false, "error": err.Error()})
			return
		}
		if err := writeCronJobsFile(cfg, list); err != nil {
			c.JSON(http.StatusInternalServerError, gin.H{"ok": false, "error": err.Error()})
			return
		}
		c.JSON(http.StatusOK, gin.H{"ok": true})
	}
}

func scanPluginDir(path, id string, pluginEntries, installs map[string]interface{}) (pluginInfo, bool) {
	pj := filepath.Join(path, "openclaw.plugin.json")
	b, err := os.ReadFile(pj)
	if err != nil {
		return pluginInfo{}, false
	}
	var meta map[string]interface{}
	if err := json.Unmarshal(b, &meta); err != nil {
		return pluginInfo{}, false
	}
	entry := asMapAny(pluginEntries[id])
	enabled := true
	if v, ok := entry["enabled"].(bool); ok {
		enabled = v
	} else if v, ok := installs[id].(bool); ok {
		enabled = v
	}
	install := asMapAny(installs[id])
	version := trimmedString(meta["version"], trimmedString(install["version"], ""))
	return pluginInfo{
		ID:          id,
		Name:        trimmedString(meta["name"], id),
		Description: trimmedString(meta["description"], ""),
		Version:     version,
		Enabled:     enabled,
		Path:        path,
	}, true
}

func discoverPlugins(cfg *config.Config, pluginEntries, pluginInstalls map[string]interface{}) []pluginInfo {
	candidates := collectPluginDiscoveryCandidates(cfg, pluginInstalls)
	plugins := make([]pluginInfo, 0, len(candidates))
	seenPaths := map[string]bool{}
	seenIDs := map[string]bool{}
	for _, candidate := range candidates {
		candidate.Path = filepath.Clean(candidate.Path)
		if candidate.Path == "" || seenPaths[candidate.Path] {
			continue
		}
		seenPaths[candidate.Path] = true
		id := strings.TrimSpace(candidate.ID)
		if id == "" {
			id = filepath.Base(candidate.Path)
		}
		plugin, ok := scanPluginDir(candidate.Path, id, pluginEntries, pluginInstalls)
		if !ok || seenIDs[plugin.ID] {
			continue
		}
		plugin.Source = candidate.Source
		seenIDs[plugin.ID] = true
		plugins = append(plugins, plugin)
	}
	for pluginID, raw := range pluginEntries {
		if seenIDs[pluginID] {
			continue
		}
		entry := asMapAny(raw)
		install := asMapAny(pluginInstalls[pluginID])
		enabled := true
		if value, ok := entry["enabled"].(bool); ok {
			enabled = value
		}
		plugins = append(plugins, pluginInfo{
			ID:          pluginID,
			Name:        trimmedString(entry["name"], pluginID),
			Description: trimmedString(entry["description"], ""),
			Version:     trimmedString(install["version"], ""),
			Enabled:     enabled,
			Source:      "config",
			Path:        trimmedString(install["installPath"], ""),
		})
	}
	return plugins
}

func collectPluginDiscoveryCandidates(cfg *config.Config, pluginInstalls map[string]interface{}) []pluginDiscoveryCandidate {
	candidates := make([]pluginDiscoveryCandidate, 0)
	added := map[string]bool{}
	addCandidate := func(id, path, source string) {
		path = strings.TrimSpace(path)
		if path == "" {
			return
		}
		if !filepath.IsAbs(path) {
			path = expandSkillPath(filepath.Dir(cfg.OpenClawDir), path)
		}
		path = filepath.Clean(path)
		if added[path] {
			return
		}
		added[path] = true
		candidates = append(candidates, pluginDiscoveryCandidate{ID: id, Path: path, Source: source})
	}

	for pluginID, raw := range pluginInstalls {
		install := asMapAny(raw)
		installPath := trimmedString(install["installPath"], "")
		addCandidate(pluginID, installPath, normalizePluginSource(trimmedString(install["source"], ""), installPath))
	}

	roots := []string{
		filepath.Join(cfg.OpenClawDir, "extensions"),
		filepath.Join(cfg.OpenClawDir, "plugins"),
		filepath.Join(cfg.OpenClawDir, "node_modules"),
	}
	if appRoot := strings.TrimSpace(cfg.OpenClawApp); appRoot != "" {
		roots = append(roots,
			filepath.Join(appRoot, "extensions"),
			filepath.Join(appRoot, "plugins"),
			filepath.Join(appRoot, "node_modules"),
		)
	}
	roots = uniqueStrings(roots)
	for _, root := range roots {
		if root == "." || root == "" {
			continue
		}
		for _, candidate := range listPluginCandidatesUnderRoot(root) {
			addCandidate(candidate.ID, candidate.Path, candidate.Source)
		}
	}
	return candidates
}

func listPluginCandidatesUnderRoot(root string) []pluginDiscoveryCandidate {
	info, err := os.Stat(root)
	if err != nil || !info.IsDir() {
		return nil
	}
	entries, err := os.ReadDir(root)
	if err != nil {
		return nil
	}
	candidates := make([]pluginDiscoveryCandidate, 0)
	for _, entry := range entries {
		if !entry.IsDir() {
			continue
		}
		name := entry.Name()
		child := filepath.Join(root, name)
		if strings.HasPrefix(name, "@") {
			scopedEntries, err := os.ReadDir(child)
			if err != nil {
				continue
			}
			for _, scoped := range scopedEntries {
				if !scoped.IsDir() {
					continue
				}
				candidates = append(candidates, pluginDiscoveryCandidate{
					ID:     name + "/" + scoped.Name(),
					Path:   filepath.Join(child, scoped.Name()),
					Source: pluginCandidateSource(root),
				})
			}
			continue
		}
		candidates = append(candidates, pluginDiscoveryCandidate{ID: name, Path: child, Source: pluginCandidateSource(root)})
	}
	return candidates
}

func pluginCandidateSource(root string) string {
	root = filepath.Clean(root)
	switch filepath.Base(root) {
	case "node_modules":
		return "config-ext"
	default:
		return "installed"
	}
}

func normalizePluginSource(source, path string) string {
	switch strings.TrimSpace(source) {
	case "config", "config-ext", "installed":
		return strings.TrimSpace(source)
	}
	if filepath.Base(filepath.Dir(path)) == "node_modules" || filepath.Base(path) == "node_modules" {
		return "config-ext"
	}
	return "installed"
}

func resolveSkillDiscoveryRoots(cfg *config.Config, ocConfig map[string]interface{}, plugins []pluginInfo, agentID string) []skillDiscoveryRoot {
	workspace := resolveSkillsWorkspace(cfg, agentID)
	baseDir := filepath.Dir(cfg.OpenClawDir)
	roots := make([]skillDiscoveryRoot, 0, 8)

	for _, dir := range uniqueStrings(resolveExtraSkillDirs(cfg, ocConfig, workspace)) {
		roots = append(roots, skillDiscoveryRoot{Dir: dir, Source: "extra-dir"})
	}
	roots = append(roots, resolvePluginSkillDirs(baseDir, plugins)...)
	bundled := filepath.Join(resolveBundledSkillsBase(cfg), "skills")
	roots = append(roots, skillDiscoveryRoot{Dir: bundled, Source: "app-skill"})
	managed := filepath.Join(cfg.OpenClawDir, "skills")
	roots = append(roots, skillDiscoveryRoot{Dir: managed, Source: "managed"})
	globalAgent := expandSkillPath(baseDir, "~/.agents/skills")
	roots = append(roots, skillDiscoveryRoot{Dir: globalAgent, Source: "global-agent"})
	if workspace != "" {
		roots = append(roots,
			skillDiscoveryRoot{Dir: filepath.Join(workspace, ".agents", "skills"), Source: "workspace-agent"},
			skillDiscoveryRoot{Dir: filepath.Join(workspace, "skills"), Source: "workspace"},
		)
	}
	return roots
}

func resolveSkillsWorkspace(cfg *config.Config, agentID string) string {
	if workspace := resolveAgentWorkspacePath(cfg, agentID); workspace != "" {
		return workspace
	}
	if cfg.OpenClawWork != "" {
		return cfg.OpenClawWork
	}
	return filepath.Join(filepath.Dir(cfg.OpenClawDir), "work")
}

func resolveRequestedAgentID(cfg *config.Config, requested string) (string, error) {
	requested = strings.TrimSpace(requested)
	if requested == "" {
		requested = loadDefaultAgentID(cfg)
		if requested == "" {
			requested = "main"
		}
	}
	if strings.Contains(requested, "/") || strings.Contains(requested, "\\") || strings.Contains(requested, "..") {
		return "", fmt.Errorf("invalid agentId %q", requested)
	}
	_, agentSet := loadAgentIDs(cfg)
	if len(agentSet) == 0 {
		agentSet = map[string]struct{}{"main": {}}
	}
	if _, ok := agentSet[requested]; !ok {
		return "", fmt.Errorf("agentId %q 不存在于当前 Agent 列表", requested)
	}
	return requested, nil
}

func resolveBundledSkillsBase(cfg *config.Config) string {
	if strings.TrimSpace(cfg.OpenClawApp) != "" {
		return cfg.OpenClawApp
	}
	return filepath.Join(filepath.Dir(cfg.OpenClawDir), "app")
}

func resolveExtraSkillDirs(cfg *config.Config, ocConfig map[string]interface{}, workspace string) []string {
	baseDir := filepath.Dir(cfg.OpenClawDir)
	roots := make([]string, 0)
	for _, raw := range asStringSlice(asMapAny(asMapAny(ocConfig["skills"])["load"])["extraDirs"]) {
		if dir := expandSkillPath(baseDir, raw); dir != "" {
			roots = append(roots, dir)
		}
	}
	if workspace != "" {
		for _, raw := range asStringSlice(asMapAny(asMapAny(ocConfig["skill"])["load"])["extraDirs"]) {
			if dir := expandSkillPath(workspace, raw); dir != "" {
				roots = append(roots, dir)
			}
		}
	}
	return roots
}

func resolvePluginSkillDirs(baseDir string, plugins []pluginInfo) []skillDiscoveryRoot {
	roots := make([]skillDiscoveryRoot, 0)
	seen := map[string]bool{}
	for _, plugin := range plugins {
		if !plugin.Enabled {
			continue
		}
		manifestPath := filepath.Join(plugin.Path, "openclaw.plugin.json")
		manifestBytes, err := os.ReadFile(manifestPath)
		if err != nil {
			continue
		}
		var manifest map[string]interface{}
		if err := json.Unmarshal(manifestBytes, &manifest); err != nil {
			continue
		}
		skillDirs := asStringSlice(manifest["skills"])
		if len(skillDirs) == 0 {
			fallback := filepath.Join(plugin.Path, "skills")
			if info, err := os.Stat(fallback); err == nil && info.IsDir() {
				skillDirs = []string{"skills"}
			}
		}
		for _, raw := range skillDirs {
			raw = strings.TrimSpace(raw)
			if raw == "" || filepath.IsAbs(raw) {
				continue
			}
			resolved := filepath.Clean(filepath.Join(plugin.Path, raw))
			rel, err := filepath.Rel(plugin.Path, resolved)
			if err != nil || rel == ".." || strings.HasPrefix(rel, ".."+string(os.PathSeparator)) {
				continue
			}
			if resolved == "" || seen[resolved] {
				continue
			}
			seen[resolved] = true
			roots = append(roots, skillDiscoveryRoot{Dir: resolved, Source: "plugin-skill"})
		}
		_ = baseDir
	}
	return roots
}

func discoverSkills(roots []skillDiscoveryRoot, skillEntries map[string]interface{}, legacyBlocklist map[string]bool) []skillInfo {
	skills := make([]skillInfo, 0)
	positions := map[string]int{}
	seenRoots := map[string]bool{}
	for _, root := range roots {
		if root.Dir == "" {
			continue
		}
		resolved := filepath.Clean(root.Dir)
		if seenRoots[resolved] {
			continue
		}
		seenRoots[resolved] = true
		scanSkillRoot(resolved, root.Source, &skills, positions, skillEntries, legacyBlocklist)
	}
	sort.Slice(skills, func(i, j int) bool {
		left := strings.ToLower(skills[i].Name)
		right := strings.ToLower(skills[j].Name)
		if left == right {
			return skills[i].SkillKey < skills[j].SkillKey
		}
		return left < right
	})
	return skills
}

func scanSkillRoot(root, source string, skills *[]skillInfo, positions map[string]int, skillEntries map[string]interface{}, legacyBlocklist map[string]bool) {
	info, err := os.Stat(root)
	if err != nil || !info.IsDir() {
		return
	}
	count := 0
	if _, err := os.Stat(filepath.Join(root, "SKILL.md")); err == nil {
		if skill, ok := parseSkillInfo(root, source, skillEntries, legacyBlocklist); ok {
			upsertSkill(skills, positions, skill)
			count++
		}
	}
	if count >= maxSkillsPerRoot {
		return
	}
	scanSkillDirRecursive(root, source, 0, &count, skills, positions, skillEntries, legacyBlocklist)
}

func scanSkillDirRecursive(dir, source string, depth int, count *int, skills *[]skillInfo, positions map[string]int, skillEntries map[string]interface{}, legacyBlocklist map[string]bool) {
	if depth >= maxSkillScanDepth || *count >= maxSkillsPerRoot {
		return
	}
	entries, err := os.ReadDir(dir)
	if err != nil {
		return
	}
	childNames := make([]string, 0, len(entries))
	for _, entry := range entries {
		if !entry.IsDir() {
			continue
		}
		name := entry.Name()
		if shouldSkipSkillDir(name) {
			continue
		}
		childNames = append(childNames, name)
	}
	sort.Strings(childNames)
	for _, name := range childNames {
		if *count >= maxSkillsPerRoot {
			return
		}
		child := filepath.Join(dir, name)
		if _, err := os.Stat(filepath.Join(child, "SKILL.md")); err == nil {
			if skill, ok := parseSkillInfo(child, source, skillEntries, legacyBlocklist); ok {
				upsertSkill(skills, positions, skill)
				*count++
			}
			continue
		}
		scanSkillDirRecursive(child, source, depth+1, count, skills, positions, skillEntries, legacyBlocklist)
	}
}

func parseSkillInfo(skillPath, source string, skillEntries map[string]interface{}, legacyBlocklist map[string]bool) (skillInfo, bool) {
	id := filepath.Base(skillPath)
	mdPath := filepath.Join(skillPath, "SKILL.md")
	mdBytes, err := os.ReadFile(mdPath)
	if err != nil || len(mdBytes) > maxSkillFileSize {
		return skillInfo{}, false
	}
	body := string(mdBytes)
	frontmatter := map[string]interface{}{}
	if match := skillFrontmatterRegexp.FindStringSubmatch(body); len(match) == 2 {
		_ = yaml.Unmarshal([]byte(match[1]), &frontmatter)
		body = body[len(match[0]):]
	}

	pkg := map[string]interface{}{}
	if packageBytes, err := os.ReadFile(filepath.Join(skillPath, "package.json")); err == nil {
		_ = json.Unmarshal(packageBytes, &pkg)
	}

	openClawMeta := resolveOpenClawMetadata(frontmatter)
	skillKey := trimmedString(openClawMeta["skillKey"], id)
	if skillKey == "" {
		skillKey = id
	}

	requiresMap := asMapAny(openClawMeta["requires"])
	requires := map[string]interface{}{}
	if env := asStringSlice(requiresMap["env"]); len(env) > 0 {
		requires["env"] = env
	}
	if bins := asStringSlice(requiresMap["bins"]); len(bins) > 0 {
		requires["bins"] = bins
	}
	if anyBins := asStringSlice(requiresMap["anyBins"]); len(anyBins) > 0 {
		requires["anyBins"] = anyBins
	}
	if configKeys := asStringSlice(requiresMap["config"]); len(configKeys) > 0 {
		requires["config"] = configKeys
	}

	metadata := map[string]interface{}{}
	if len(openClawMeta) > 0 {
		metadata["openclaw"] = openClawMeta
	}

	enabled := resolveSkillEnabled(skillEntries, legacyBlocklist, skillKey, id)
	skill := skillInfo{
		ID:          id,
		Name:        trimmedString(frontmatter["name"], trimmedString(pkg["name"], id)),
		Description: resolveSkillDescription(frontmatter, pkg, body),
		Version:     resolveLocalSkillVersion(pkg, skillPath),
		Enabled:     enabled,
		Path:        skillPath,
		SkillKey:    skillKey,
		Source:      source,
	}
	if len(metadata) > 0 {
		skill.Metadata = metadata
	}
	if len(requires) > 0 {
		skill.Requires = requires
	}
	return skill, true
}

func resolveOpenClawMetadata(frontmatter map[string]interface{}) map[string]interface{} {
	metadata := asMapAny(frontmatter["metadata"])
	for _, key := range []string{"openclaw", "clawdbot", "clawdis"} {
		if meta := asMapAny(metadata[key]); len(meta) > 0 {
			return meta
		}
	}
	for _, key := range []string{"openclaw", "clawdbot", "clawdis"} {
		if meta := asMapAny(frontmatter[key]); len(meta) > 0 {
			return meta
		}
	}
	return map[string]interface{}{}
}

func resolveSkillDescription(frontmatter, pkg map[string]interface{}, body string) string {
	if desc := trimmedString(frontmatter["description"], ""); desc != "" {
		return desc
	}
	if desc := trimmedString(pkg["description"], ""); desc != "" {
		return desc
	}
	for _, line := range strings.Split(body, "\n") {
		line = strings.TrimSpace(line)
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		return line
	}
	return ""
}

func resolveLocalSkillVersion(pkg map[string]interface{}, skillPath string) string {
	if version := trimmedString(pkg["version"], ""); version != "" {
		return version
	}
	raw, err := os.ReadFile(filepath.Join(skillPath, ".clawhub", "origin.json"))
	if err != nil {
		return ""
	}
	var origin clawHubOriginFile
	if err := json.Unmarshal(raw, &origin); err != nil {
		return ""
	}
	return strings.TrimSpace(origin.InstalledVersion)
}

func resolveSkillEnabled(skillEntries map[string]interface{}, legacyBlocklist map[string]bool, keys ...string) bool {
	for _, key := range keys {
		if key == "" {
			continue
		}
		if entry := asMapAny(skillEntries[key]); len(entry) > 0 {
			if enabled, ok := entry["enabled"].(bool); ok {
				return enabled
			}
		}
	}
	for _, key := range keys {
		if key != "" && legacyBlocklist[key] {
			return false
		}
	}
	return true
}

func upsertSkill(skills *[]skillInfo, positions map[string]int, skill skillInfo) {
	key := skill.SkillKey
	if key == "" {
		key = skill.ID
	}
	if idx, ok := positions[key]; ok {
		(*skills)[idx] = skill
		return
	}
	positions[key] = len(*skills)
	*skills = append(*skills, skill)
}

func shouldSkipSkillDir(name string) bool {
	if strings.HasPrefix(name, ".") {
		return true
	}
	switch name {
	case "node_modules", "dist", "build", "coverage", "vendor":
		return true
	default:
		return false
	}
}

func readLegacySkillBlocklist(skillsCfg map[string]interface{}) map[string]bool {
	blocked := map[string]bool{}
	for _, value := range asStringSlice(skillsCfg["blocklist"]) {
		blocked[value] = true
	}
	return blocked
}

func removeLegacyBlocklistEntries(skillsCfg map[string]interface{}, keys ...string) {
	blocklist := asStringSlice(skillsCfg["blocklist"])
	if len(blocklist) == 0 {
		return
	}
	removeSet := map[string]struct{}{}
	for _, key := range keys {
		key = strings.TrimSpace(key)
		if key != "" {
			removeSet[key] = struct{}{}
		}
	}
	filtered := make([]string, 0, len(blocklist))
	for _, item := range blocklist {
		if _, ok := removeSet[item]; !ok {
			filtered = append(filtered, item)
		}
	}
	if len(filtered) == 0 {
		delete(skillsCfg, "blocklist")
		return
	}
	values := make([]interface{}, 0, len(filtered))
	for _, item := range filtered {
		values = append(values, item)
	}
	skillsCfg["blocklist"] = values
}

func validateCronJobsSessionTargets(cfg *config.Config, jobs []map[string]interface{}) error {
	_, agentSet := loadAgentIDs(cfg)
	if len(agentSet) == 0 {
		agentSet = map[string]struct{}{"main": {}}
	}
	for _, job := range jobs {
		target := strings.TrimSpace(fmt.Sprint(job["sessionTarget"]))
		if target == "" {
			continue
		}
		if _, ok := agentSet[target]; !ok {
			return fmt.Errorf("sessionTarget %q 不存在于当前 Agent 列表", target)
		}
	}
	return nil
}

func writeCronJobsFile(cfg *config.Config, jobs []interface{}) error {
	cronDir := filepath.Join(cfg.OpenClawDir, "cron")
	if err := os.MkdirAll(cronDir, 0755); err != nil {
		return err
	}
	raw, err := json.MarshalIndent(map[string]interface{}{"jobs": jobs}, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(filepath.Join(cronDir, "jobs.json"), raw, 0644)
}

func expandSkillPath(baseDir, raw string) string {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return ""
	}
	if strings.HasPrefix(raw, "~/") || raw == "~" {
		home, err := os.UserHomeDir()
		if err == nil {
			if raw == "~" {
				return home
			}
			return filepath.Join(home, raw[2:])
		}
	}
	if filepath.IsAbs(raw) {
		return filepath.Clean(raw)
	}
	if baseDir == "" {
		return filepath.Clean(raw)
	}
	return filepath.Clean(filepath.Join(baseDir, raw))
}

func uniqueStrings(items []string) []string {
	seen := map[string]bool{}
	out := make([]string, 0, len(items))
	for _, item := range items {
		if item == "" || seen[item] {
			continue
		}
		seen[item] = true
		out = append(out, item)
	}
	return out
}

func asMapAny(value interface{}) map[string]interface{} {
	if typed, ok := value.(map[string]interface{}); ok && typed != nil {
		return typed
	}
	return map[string]interface{}{}
}

func asStringSlice(value interface{}) []string {
	switch typed := value.(type) {
	case []string:
		out := make([]string, 0, len(typed))
		for _, item := range typed {
			item = strings.TrimSpace(item)
			if item != "" {
				out = append(out, item)
			}
		}
		return out
	case []interface{}:
		out := make([]string, 0, len(typed))
		for _, item := range typed {
			value := strings.TrimSpace(fmt.Sprint(item))
			if value != "" {
				out = append(out, value)
			}
		}
		return out
	default:
		return nil
	}
}

func trimmedString(value interface{}, fallback string) string {
	if text := strings.TrimSpace(fmt.Sprint(value)); text != "" && text != "<nil>" {
		return text
	}
	return fallback
}
