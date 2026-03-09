package handler

import (
	"encoding/json"
	"net/http"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/gin-gonic/gin"
	"github.com/zhaoxinyi02/ClawPanel/internal/config"
)

type FeishuDMDiagnosis struct {
	ConfiguredDMScope         string                         `json:"configuredDmScope,omitempty"`
	EffectiveDMScope          string                         `json:"effectiveDmScope"`
	RecommendedDMScope        string                         `json:"recommendedDmScope"`
	DefaultAgent              string                         `json:"defaultAgent"`
	ScannedAgentIDs           []string                       `json:"scannedAgentIds,omitempty"`
	AccountCount              int                            `json:"accountCount"`
	AccountIDs                []string                       `json:"accountIds,omitempty"`
	DefaultAccount            string                         `json:"defaultAccount,omitempty"`
	DMPolicy                  string                         `json:"dmPolicy,omitempty"`
	ThreadSession             bool                           `json:"threadSession"`
	UnsupportedChannelDMScope string                         `json:"unsupportedChannelDmScope,omitempty"`
	SessionFilePath           string                         `json:"sessionFilePath"`
	SessionIndexExists        bool                           `json:"sessionIndexExists"`
	FeishuSessionCount        int                            `json:"feishuSessionCount"`
	FeishuSessionKeys         []string                       `json:"feishuSessionKeys,omitempty"`
	HasSharedMainSessionKey   bool                           `json:"hasSharedMainSessionKey"`
	MainSessionKey            string                         `json:"mainSessionKey"`
	CredentialsDir            string                         `json:"credentialsDir,omitempty"`
	PairingStorePath          string                         `json:"pairingStorePath,omitempty"`
	PendingPairingCount       int                            `json:"pendingPairingCount"`
	AuthorizedSenderCount     int                            `json:"authorizedSenderCount"`
	AuthorizedSenders         []FeishuAuthorizedSenderBucket `json:"authorizedSenders,omitempty"`
}

type FeishuAuthorizedSenderBucket struct {
	AccountID         string   `json:"accountId"`
	AccountConfigured bool     `json:"accountConfigured"`
	SenderCount       int      `json:"senderCount"`
	SenderIDs         []string `json:"senderIds,omitempty"`
	SourceFiles       []string `json:"sourceFiles,omitempty"`
}

type feishuAllowFromStore struct {
	AllowFrom []interface{} `json:"allowFrom"`
}

type feishuPairingStore struct {
	Requests []json.RawMessage `json:"requests"`
}

func GetFeishuDMDiagnosis(cfg *config.Config) gin.HandlerFunc {
	return func(c *gin.Context) {
		diagnosis := buildFeishuDMDiagnosis(cfg)
		c.JSON(http.StatusOK, gin.H{"ok": true, "diagnosis": diagnosis})
	}
}

func buildFeishuDMDiagnosis(cfg *config.Config) FeishuDMDiagnosis {
	ocConfig, _ := cfg.ReadOpenClawJSON()
	if ocConfig == nil {
		ocConfig = map[string]interface{}{}
	}

	feishuCfg, _ := getNestedMapValue(ocConfig, "channels", "feishu").(map[string]interface{})
	if feishuCfg == nil {
		feishuCfg = map[string]interface{}{}
	}

	accountIDs := listFeishuAccountIDsFromConfig(feishuCfg)
	defaultAccount := strings.TrimSpace(toString(feishuCfg["defaultAccount"]))
	if len(accountIDs) == 0 {
		if strings.TrimSpace(toString(feishuCfg["appId"])) != "" || strings.TrimSpace(toString(feishuCfg["appSecret"])) != "" {
			fallback := defaultAccount
			if fallback == "" {
				fallback = "default"
			}
			accountIDs = []string{fallback}
		}
	}

	configuredDMScope := strings.TrimSpace(toString(getNestedMapValue(ocConfig, "session", "dmScope")))
	effectiveDMScope := configuredDMScope
	if effectiveDMScope == "" {
		effectiveDMScope = "main"
	}

	recommendedDMScope := "per-channel-peer"
	if len(accountIDs) > 1 {
		recommendedDMScope = "per-account-channel-peer"
	}

	defaultAgent := strings.TrimSpace(loadDefaultAgentID(cfg))
	if defaultAgent == "" {
		defaultAgent = "main"
	}
	mainSessionKey := "agent:" + defaultAgent + ":main"
	sessionFilePath := resolveAgentPath(cfg, defaultAgent, "sessions", "sessions.json")
	credentialsDir := filepath.Join(cfg.OpenClawDir, "credentials")
	pairingStorePath := filepath.Join(credentialsDir, "feishu-pairing.json")
	authorizedSenders, authorizedSenderCount := collectFeishuAuthorizedSenders(credentialsDir, accountIDs, defaultAccount)

	diagnosis := FeishuDMDiagnosis{
		ConfiguredDMScope:         configuredDMScope,
		EffectiveDMScope:          effectiveDMScope,
		RecommendedDMScope:        recommendedDMScope,
		DefaultAgent:              defaultAgent,
		ScannedAgentIDs:           nil,
		AccountCount:              len(accountIDs),
		AccountIDs:                accountIDs,
		DefaultAccount:            defaultAccount,
		DMPolicy:                  strings.TrimSpace(toString(feishuCfg["dmPolicy"])),
		ThreadSession:             asBool(feishuCfg["threadSession"]),
		UnsupportedChannelDMScope: strings.TrimSpace(toString(feishuCfg["dmScope"])),
		SessionFilePath:           sessionFilePath,
		MainSessionKey:            mainSessionKey,
		CredentialsDir:            credentialsDir,
		PairingStorePath:          pairingStorePath,
		PendingPairingCount:       readFeishuPendingPairingCount(pairingStorePath),
		AuthorizedSenderCount:     authorizedSenderCount,
		AuthorizedSenders:         authorizedSenders,
	}

	agentIDs, _ := loadAgentIDs(cfg)
	if len(agentIDs) == 0 {
		agentIDs = []string{defaultAgent}
	}

	allKeys := make([]string, 0)
	for _, agentID := range agentIDs {
		path := resolveAgentPath(cfg, agentID, "sessions", "sessions.json")
		raw, err := os.ReadFile(path)
		if err != nil {
			continue
		}
		var sessionIndex map[string]interface{}
		if err := json.Unmarshal(raw, &sessionIndex); err != nil {
			continue
		}
		diagnosis.SessionIndexExists = true
		diagnosis.ScannedAgentIDs = append(diagnosis.ScannedAgentIDs, agentID)
		allKeys = append(allKeys, collectFeishuSessionKeys(sessionIndex)...)
	}
	sort.Strings(allKeys)
	diagnosis.FeishuSessionKeys = allKeys
	diagnosis.FeishuSessionCount = len(allKeys)
	for _, key := range allKeys {
		if isSharedMainSessionKey(key) {
			diagnosis.HasSharedMainSessionKey = true
			diagnosis.MainSessionKey = key
			break
		}
	}
	if diagnosis.MainSessionKey == "" {
		diagnosis.MainSessionKey = mainSessionKey
	}
	return diagnosis
}

func listFeishuAccountIDsFromConfig(ch map[string]interface{}) []string {
	accounts, _ := ch["accounts"].(map[string]interface{})
	if accounts == nil {
		return nil
	}
	out := make([]string, 0, len(accounts))
	for id := range accounts {
		id = strings.TrimSpace(id)
		if id == "" {
			continue
		}
		out = append(out, id)
	}
	sort.Strings(out)
	return out
}

func collectFeishuSessionKeys(sessionIndex map[string]interface{}) []string {
	if sessionIndex == nil {
		return nil
	}
	keys := make([]string, 0)
	for key, raw := range sessionIndex {
		record, _ := raw.(map[string]interface{})
		if record == nil {
			continue
		}
		if strings.TrimSpace(toString(getNestedMapValue(record, "deliveryContext", "channel"))) == "feishu" {
			keys = append(keys, key)
			continue
		}
		if strings.TrimSpace(toString(getNestedMapValue(record, "origin", "provider"))) == "feishu" {
			keys = append(keys, key)
		}
	}
	sort.Strings(keys)
	return keys
}

func isSharedMainSessionKey(key string) bool {
	parts := strings.Split(key, ":")
	return len(parts) == 3 && parts[0] == "agent" && parts[2] == "main"
}

func collectFeishuAuthorizedSenders(credentialsDir string, configuredAccountIDs []string, defaultAccount string) ([]FeishuAuthorizedSenderBucket, int) {
	configuredSet := make(map[string]struct{}, len(configuredAccountIDs))
	buckets := make(map[string]*FeishuAuthorizedSenderBucket, len(configuredAccountIDs))
	for _, accountID := range configuredAccountIDs {
		accountID = strings.TrimSpace(accountID)
		if accountID == "" {
			continue
		}
		configuredSet[accountID] = struct{}{}
		buckets[accountID] = &FeishuAuthorizedSenderBucket{
			AccountID:         accountID,
			AccountConfigured: true,
			SourceFiles:       []string{},
			SenderIDs:         []string{},
		}
	}

	defaultAccountID := resolveFeishuDefaultAccountID(configuredAccountIDs, defaultAccount)
	entries, err := os.ReadDir(credentialsDir)
	if err == nil {
		for _, entry := range entries {
			if entry.IsDir() {
				continue
			}
			accountID, ok := parseFeishuAllowFromAccountID(entry.Name(), defaultAccountID)
			if !ok {
				continue
			}
			bucket, exists := buckets[accountID]
			if !exists {
				_, configured := configuredSet[accountID]
				bucket = &FeishuAuthorizedSenderBucket{
					AccountID:         accountID,
					AccountConfigured: configured,
					SourceFiles:       []string{},
					SenderIDs:         []string{},
				}
				buckets[accountID] = bucket
			}
			filePath := filepath.Join(credentialsDir, entry.Name())
			bucket.SourceFiles = append(bucket.SourceFiles, filePath)
			bucket.SenderIDs = mergeUniqueStrings(bucket.SenderIDs, readFeishuAllowFromEntries(filePath))
		}
	}

	accountIDs := make([]string, 0, len(buckets))
	for accountID := range buckets {
		accountIDs = append(accountIDs, accountID)
	}
	sort.Strings(accountIDs)

	totalAuthorized := 0
	result := make([]FeishuAuthorizedSenderBucket, 0, len(accountIDs))
	for _, accountID := range accountIDs {
		bucket := buckets[accountID]
		if bucket == nil {
			continue
		}
		bucket.SourceFiles = mergeUniqueStrings(nil, bucket.SourceFiles)
		bucket.SenderCount = len(bucket.SenderIDs)
		totalAuthorized += bucket.SenderCount
		result = append(result, *bucket)
	}
	return result, totalAuthorized
}

func resolveFeishuDefaultAccountID(configuredAccountIDs []string, defaultAccount string) string {
	defaultAccount = strings.TrimSpace(defaultAccount)
	if defaultAccount != "" {
		return defaultAccount
	}
	for _, accountID := range configuredAccountIDs {
		if strings.TrimSpace(accountID) == "default" {
			return "default"
		}
	}
	if len(configuredAccountIDs) == 1 {
		return strings.TrimSpace(configuredAccountIDs[0])
	}
	return "default"
}

func parseFeishuAllowFromAccountID(fileName string, defaultAccountID string) (string, bool) {
	if fileName == "feishu-allowFrom.json" {
		return defaultAccountID, true
	}
	if !strings.HasPrefix(fileName, "feishu-") || !strings.HasSuffix(fileName, "-allowFrom.json") {
		return "", false
	}
	accountID := strings.TrimSuffix(strings.TrimPrefix(fileName, "feishu-"), "-allowFrom.json")
	accountID = strings.TrimSpace(accountID)
	if accountID == "" {
		return "", false
	}
	return accountID, true
}

func readFeishuAllowFromEntries(path string) []string {
	raw, err := os.ReadFile(path)
	if err != nil {
		return nil
	}
	var store feishuAllowFromStore
	if err := json.Unmarshal(raw, &store); err != nil {
		return nil
	}
	entries := make([]string, 0, len(store.AllowFrom))
	for _, value := range store.AllowFrom {
		entry := strings.TrimSpace(toString(value))
		if entry == "" {
			continue
		}
		entries = append(entries, entry)
	}
	return mergeUniqueStrings(nil, entries)
}

func readFeishuPendingPairingCount(path string) int {
	raw, err := os.ReadFile(path)
	if err != nil {
		return 0
	}
	var store feishuPairingStore
	if err := json.Unmarshal(raw, &store); err != nil {
		return 0
	}
	return len(store.Requests)
}

func mergeUniqueStrings(base []string, incoming []string) []string {
	seen := make(map[string]struct{}, len(base)+len(incoming))
	out := make([]string, 0, len(base)+len(incoming))
	for _, value := range base {
		value = strings.TrimSpace(value)
		if value == "" {
			continue
		}
		if _, exists := seen[value]; exists {
			continue
		}
		seen[value] = struct{}{}
		out = append(out, value)
	}
	for _, value := range incoming {
		value = strings.TrimSpace(value)
		if value == "" {
			continue
		}
		if _, exists := seen[value]; exists {
			continue
		}
		seen[value] = struct{}{}
		out = append(out, value)
	}
	return out
}
