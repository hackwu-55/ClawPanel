package handler

import (
	"encoding/json"
	"fmt"
	"net"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/zhaoxinyi02/ClawPanel/internal/config"
)

// ConfigIssue represents a detected configuration problem
type ConfigIssue struct {
	ID          string `json:"id"`
	Severity    string `json:"severity"` // error, warning, info
	Component   string `json:"component"` // napcat, openclaw
	Title       string `json:"title"`
	Description string `json:"description"`
	Fixable     bool   `json:"fixable"`
	CurrentVal  string `json:"currentValue,omitempty"`
	ExpectedVal string `json:"expectedValue,omitempty"`
	FilePath    string `json:"filePath,omitempty"`
}

// ConfigCheckResult is the response for config check API
type ConfigCheckResult struct {
	OK       bool          `json:"ok"`
	Issues   []ConfigIssue `json:"issues"`
	Checked  int           `json:"checked"`
	Problems int           `json:"problems"`
}

// CheckConfig scans OpenClaw and NapCat configuration files for common issues
func CheckConfig(cfg *config.Config) gin.HandlerFunc {
	return func(c *gin.Context) {
		var issues []ConfigIssue
		checked := 0

		// === NapCat onebot11.json checks ===
		napcatIssues, napcatChecked := checkNapCatConfig()
		issues = append(issues, napcatIssues...)
		checked += napcatChecked

		// === NapCat webui.json checks ===
		webuiIssues, webuiChecked := checkNapCatWebUI()
		issues = append(issues, webuiIssues...)
		checked += webuiChecked

		// === OpenClaw config checks ===
		ocIssues, ocChecked := checkOpenClawConfig(cfg)
		issues = append(issues, ocIssues...)
		checked += ocChecked

		// === Port conflict checks ===
		portIssues, portChecked := checkPortConflicts()
		issues = append(issues, portIssues...)
		checked += portChecked

		problems := 0
		for _, iss := range issues {
			if iss.Severity == "error" || iss.Severity == "warning" {
				problems++
			}
		}

		c.JSON(http.StatusOK, ConfigCheckResult{
			OK:       true,
			Issues:   issues,
			Checked:  checked,
			Problems: problems,
		})
	}
}

// FixConfig applies automatic fixes for known configuration issues
func FixConfig(cfg *config.Config) gin.HandlerFunc {
	return func(c *gin.Context) {
		var req struct {
			IssueIDs []string `json:"issueIds"`
		}
		if err := c.ShouldBindJSON(&req); err != nil {
			c.JSON(http.StatusBadRequest, gin.H{"ok": false, "error": "参数错误"})
			return
		}

		fixed := []string{}
		failed := []string{}

		for _, id := range req.IssueIDs {
			err := fixIssue(id, cfg)
			if err != nil {
				failed = append(failed, fmt.Sprintf("%s: %s", id, err.Error()))
			} else {
				fixed = append(fixed, id)
			}
		}

		// If any NapCat config was fixed, restart the container
		napcatRestart := false
		for _, id := range fixed {
			if strings.HasPrefix(id, "napcat-") {
				napcatRestart = true
				break
			}
		}
		if napcatRestart {
			go exec.Command("docker", "restart", "openclaw-qq").Run()
		}

		c.JSON(http.StatusOK, gin.H{
			"ok":             true,
			"fixed":          fixed,
			"failed":         failed,
			"napcatRestart":  napcatRestart,
		})
	}
}

// --- NapCat onebot11.json ---

func readNapCatOneBot11() (map[string]interface{}, string, error) {
	// Try reading from Docker container
	out, err := exec.Command("docker", "exec", "openclaw-qq", "cat", "/app/napcat/config/onebot11.json").Output()
	if err != nil {
		return nil, "", fmt.Errorf("无法读取 NapCat onebot11.json: %v", err)
	}
	var data map[string]interface{}
	if err := json.Unmarshal(out, &data); err != nil {
		return nil, "", fmt.Errorf("onebot11.json 解析失败: %v", err)
	}
	return data, "/app/napcat/config/onebot11.json (Docker: openclaw-qq)", nil
}

func checkNapCatConfig() ([]ConfigIssue, int) {
	var issues []ConfigIssue
	checked := 0

	data, filePath, err := readNapCatOneBot11()
	if err != nil {
		// Container might not exist
		if isDockerContainerExists("openclaw-qq") {
			issues = append(issues, ConfigIssue{
				ID: "napcat-onebot11-missing", Severity: "error", Component: "napcat",
				Title: "onebot11.json 配置文件不存在或无法读取",
				Description: err.Error(), Fixable: true, FilePath: filePath,
			})
		}
		return issues, 1
	}

	network, _ := data["network"].(map[string]interface{})
	if network == nil {
		issues = append(issues, ConfigIssue{
			ID: "napcat-no-network", Severity: "error", Component: "napcat",
			Title: "缺少 network 配置块",
			Description: "onebot11.json 中没有 network 字段，WS/HTTP 服务无法启动",
			Fixable: true, FilePath: filePath,
		})
		return issues, 1
	}

	// Check WebSocket servers
	checked++
	wsServers, _ := network["websocketServers"].([]interface{})
	if len(wsServers) == 0 {
		issues = append(issues, ConfigIssue{
			ID: "napcat-no-ws", Severity: "error", Component: "napcat",
			Title: "WebSocket 服务未配置",
			Description: "未配置 websocketServers，ClawPanel 无法接收消息事件",
			Fixable: true, FilePath: filePath,
		})
	} else {
		for i, ws := range wsServers {
			wsMap, _ := ws.(map[string]interface{})
			if wsMap == nil {
				continue
			}

			// Check enable
			checked++
			enabled, _ := wsMap["enable"].(bool)
			if !enabled {
				issues = append(issues, ConfigIssue{
					ID: fmt.Sprintf("napcat-ws-%d-disabled", i), Severity: "error", Component: "napcat",
					Title: fmt.Sprintf("WebSocket 服务 #%d 未启用", i+1),
					Description: "websocketServers[" + fmt.Sprint(i) + "].enable = false",
					Fixable: true, CurrentVal: "false", ExpectedVal: "true", FilePath: filePath,
				})
			}

			// Check port
			checked++
			port, _ := wsMap["port"].(float64)
			if port == 0 {
				issues = append(issues, ConfigIssue{
					ID: fmt.Sprintf("napcat-ws-%d-noport", i), Severity: "error", Component: "napcat",
					Title: fmt.Sprintf("WebSocket 服务 #%d 端口未设置", i+1),
					Description: "端口为 0 或未设置",
					Fixable: true, CurrentVal: "0", ExpectedVal: "3001", FilePath: filePath,
				})
			}

			// Check reportSelfMessage
			checked++
			reportSelf, reportSelfExists := wsMap["reportSelfMessage"].(bool)
			if !reportSelfExists || !reportSelf {
				issues = append(issues, ConfigIssue{
					ID: fmt.Sprintf("napcat-ws-%d-no-self-msg", i), Severity: "warning", Component: "napcat",
					Title: "reportSelfMessage 未启用",
					Description: "Bot 发送的消息不会转发到 WebSocket，活动日志中将看不到 Bot 回复",
					Fixable: true, CurrentVal: "false", ExpectedVal: "true", FilePath: filePath,
				})
			}
		}
	}

	// Check HTTP servers
	checked++
	httpServers, _ := network["httpServers"].([]interface{})
	if len(httpServers) == 0 {
		issues = append(issues, ConfigIssue{
			ID: "napcat-no-http", Severity: "warning", Component: "napcat",
			Title: "HTTP API 服务未配置",
			Description: "未配置 httpServers，部分 Bot 操作（发消息、获取群列表等）将不可用",
			Fixable: true, FilePath: filePath,
		})
	} else {
		for i, hs := range httpServers {
			hsMap, _ := hs.(map[string]interface{})
			if hsMap == nil {
				continue
			}
			checked++
			enabled, _ := hsMap["enable"].(bool)
			if !enabled {
				issues = append(issues, ConfigIssue{
					ID: fmt.Sprintf("napcat-http-%d-disabled", i), Severity: "warning", Component: "napcat",
					Title: fmt.Sprintf("HTTP API 服务 #%d 未启用", i+1),
					Description: "httpServers[" + fmt.Sprint(i) + "].enable = false",
					Fixable: true, CurrentVal: "false", ExpectedVal: "true", FilePath: filePath,
				})
			}
		}
	}

	return issues, checked
}

// --- NapCat webui.json ---

func checkNapCatWebUI() ([]ConfigIssue, int) {
	var issues []ConfigIssue
	checked := 0

	if !isDockerContainerExists("openclaw-qq") {
		return issues, 0
	}

	out, err := exec.Command("docker", "exec", "openclaw-qq", "cat", "/app/napcat/config/webui.json").Output()
	if err != nil {
		return issues, 0
	}

	var data map[string]interface{}
	if err := json.Unmarshal(out, &data); err != nil {
		return issues, 0
	}

	// Check token
	checked++
	token, _ := data["token"].(string)
	if token == "" {
		issues = append(issues, ConfigIssue{
			ID: "napcat-webui-no-token", Severity: "warning", Component: "napcat",
			Title: "NapCat WebUI Token 为空",
			Description: "WebUI 没有设置访问令牌，ClawPanel 可能无法调用 NapCat 管理 API",
			Fixable: true, CurrentVal: "(空)", ExpectedVal: "clawpanel-qq",
			FilePath: "/app/napcat/config/webui.json (Docker: openclaw-qq)",
		})
	}

	return issues, checked
}

// --- OpenClaw config ---

func checkOpenClawConfig(cfg *config.Config) ([]ConfigIssue, int) {
	var issues []ConfigIssue
	checked := 0

	ocConfig, err := cfg.ReadOpenClawJSON()
	if err != nil {
		if !os.IsNotExist(err) {
			issues = append(issues, ConfigIssue{
				ID: "openclaw-config-error", Severity: "error", Component: "openclaw",
				Title: "openclaw.json 读取失败",
				Description: err.Error(), Fixable: false,
				FilePath: filepath.Join(cfg.OpenClawDir, "openclaw.json"),
			})
		}
		return issues, 1
	}

	// Check models configuration
	checked++
	models, _ := ocConfig["models"].(map[string]interface{})
	if models == nil {
		issues = append(issues, ConfigIssue{
			ID: "openclaw-no-models", Severity: "warning", Component: "openclaw",
			Title: "未配置模型提供商",
			Description: "openclaw.json 中没有 models 配置，AI 助手将无法工作",
			Fixable: false,
			FilePath: filepath.Join(cfg.OpenClawDir, "openclaw.json"),
		})
	} else {
		providers, _ := models["providers"].(map[string]interface{})
		if providers == nil || len(providers) == 0 {
			issues = append(issues, ConfigIssue{
				ID: "openclaw-no-providers", Severity: "warning", Component: "openclaw",
				Title: "未配置任何模型提供商",
				Description: "models.providers 为空，请在系统配置中添加至少一个模型服务商",
				Fixable: false,
				FilePath: filepath.Join(cfg.OpenClawDir, "openclaw.json"),
			})
		} else {
			// Check each provider has apiKey
			for pid, prov := range providers {
				checked++
				provMap, _ := prov.(map[string]interface{})
				if provMap == nil {
					continue
				}
				apiKey, _ := provMap["apiKey"].(string)
				if apiKey == "" {
					issues = append(issues, ConfigIssue{
						ID: "openclaw-provider-" + pid + "-no-key", Severity: "warning", Component: "openclaw",
						Title: fmt.Sprintf("模型提供商 %s 缺少 API Key", pid),
						Description: fmt.Sprintf("providers.%s.apiKey 为空，该提供商的模型将无法使用", pid),
						Fixable: false,
						FilePath: filepath.Join(cfg.OpenClawDir, "openclaw.json"),
					})
				}
			}
		}
	}

	// Check primary model
	checked++
	agents, _ := ocConfig["agents"].(map[string]interface{})
	if agents != nil {
		defaults, _ := agents["defaults"].(map[string]interface{})
		if defaults != nil {
			model, _ := defaults["model"].(map[string]interface{})
			if model != nil {
				primary, _ := model["primary"].(string)
				if primary == "" {
					issues = append(issues, ConfigIssue{
						ID: "openclaw-no-primary-model", Severity: "warning", Component: "openclaw",
						Title: "未设置主模型",
						Description: "agents.defaults.model.primary 为空，请在系统配置中选择一个主模型",
						Fixable: false,
						FilePath: filepath.Join(cfg.OpenClawDir, "openclaw.json"),
					})
				}
			}
		}
	}

	// Check channels configuration
	checked++
	channels, _ := ocConfig["channels"].(map[string]interface{})
	if channels != nil {
		for chID, ch := range channels {
			chMap, _ := ch.(map[string]interface{})
			if chMap == nil {
				continue
			}
			enabled, _ := chMap["enabled"].(bool)
			if !enabled {
				continue
			}
			// For QQ channel, check wsUrl config
			if chID == "qq" {
				checked++
				wsUrl, _ := chMap["wsUrl"].(string)
				if wsUrl == "" {
					issues = append(issues, ConfigIssue{
						ID: "openclaw-qq-no-wsurl", Severity: "warning", Component: "openclaw",
						Title: "QQ 通道缺少 WebSocket 地址",
						Description: "channels.qq.wsUrl 未配置，QQ 通道将无法连接 NapCat",
						Fixable: false,
						FilePath: filepath.Join(cfg.OpenClawDir, "openclaw.json"),
					})
				}
			}
		}
	}

	return issues, checked
}

// --- Port conflicts ---

func checkPortConflicts() ([]ConfigIssue, int) {
	var issues []ConfigIssue
	checked := 0

	ports := map[int]string{
		3000: "NapCat HTTP API",
		3001: "NapCat WebSocket",
		6099: "NapCat WebUI",
	}

	for port, name := range ports {
		checked++
		if !isPortListening(port) && isDockerContainerExists("openclaw-qq") {
			issues = append(issues, ConfigIssue{
				ID:          fmt.Sprintf("port-%d-not-listening", port),
				Severity:    "warning",
				Component:   "napcat",
				Title:       fmt.Sprintf("端口 %d (%s) 未监听", port, name),
				Description: fmt.Sprintf("NapCat 容器已存在但端口 %d 未响应，可能服务未启动或端口映射错误", port),
				Fixable:     false,
			})
		}
	}

	return issues, checked
}

// --- Fix logic ---

func fixIssue(issueID string, cfg *config.Config) error {
	switch {
	case issueID == "napcat-onebot11-missing" || issueID == "napcat-no-network" || issueID == "napcat-no-ws" || issueID == "napcat-no-http":
		return writeDefaultOneBot11Config()

	case strings.HasPrefix(issueID, "napcat-ws-") && strings.HasSuffix(issueID, "-disabled"):
		return fixNapCatWSField("enable", true)
	case strings.HasPrefix(issueID, "napcat-ws-") && strings.HasSuffix(issueID, "-noport"):
		return fixNapCatWSField("port", float64(3001))
	case strings.HasPrefix(issueID, "napcat-ws-") && strings.HasSuffix(issueID, "-no-self-msg"):
		return fixNapCatWSField("reportSelfMessage", true)

	case strings.HasPrefix(issueID, "napcat-http-") && strings.HasSuffix(issueID, "-disabled"):
		return fixNapCatHTTPField("enable", true)

	case issueID == "napcat-webui-no-token":
		return fixNapCatWebUIToken()

	default:
		return fmt.Errorf("该问题不支持自动修复")
	}
}

func writeDefaultOneBot11Config() error {
	defaultConfig := `{
  "network": {
    "websocketServers": [{
      "name": "ws-server",
      "enable": true,
      "host": "0.0.0.0",
      "port": 3001,
      "token": "",
      "reportSelfMessage": true,
      "enableForcePushEvent": true,
      "messagePostFormat": "array",
      "debug": false,
      "heartInterval": 30000
    }],
    "httpServers": [{
      "name": "http-api",
      "enable": true,
      "host": "0.0.0.0",
      "port": 3000,
      "token": ""
    }],
    "httpSseServers": [],
    "httpClients": [],
    "websocketClients": [],
    "plugins": []
  },
  "musicSignUrl": "",
  "enableLocalFile2Url": true,
  "parseMultMsg": true,
  "imageDownloadProxy": ""
}`
	cmd := exec.Command("docker", "exec", "openclaw-qq", "bash", "-c",
		fmt.Sprintf("cat > /app/napcat/config/onebot11.json << 'FIXEOF'\n%s\nFIXEOF", defaultConfig))
	return cmd.Run()
}

func fixNapCatWSField(field string, value interface{}) error {
	data, _, err := readNapCatOneBot11()
	if err != nil {
		return err
	}

	network, _ := data["network"].(map[string]interface{})
	if network == nil {
		return writeDefaultOneBot11Config()
	}

	wsServers, _ := network["websocketServers"].([]interface{})
	if len(wsServers) == 0 {
		return writeDefaultOneBot11Config()
	}

	for _, ws := range wsServers {
		wsMap, _ := ws.(map[string]interface{})
		if wsMap != nil {
			wsMap[field] = value
		}
	}

	return writeNapCatOneBot11(data)
}

func fixNapCatHTTPField(field string, value interface{}) error {
	data, _, err := readNapCatOneBot11()
	if err != nil {
		return err
	}

	network, _ := data["network"].(map[string]interface{})
	if network == nil {
		return writeDefaultOneBot11Config()
	}

	httpServers, _ := network["httpServers"].([]interface{})
	if len(httpServers) == 0 {
		return writeDefaultOneBot11Config()
	}

	for _, hs := range httpServers {
		hsMap, _ := hs.(map[string]interface{})
		if hsMap != nil {
			hsMap[field] = value
		}
	}

	return writeNapCatOneBot11(data)
}

func writeNapCatOneBot11(data map[string]interface{}) error {
	jsonBytes, err := json.MarshalIndent(data, "", "  ")
	if err != nil {
		return err
	}
	cmd := exec.Command("docker", "exec", "openclaw-qq", "bash", "-c",
		fmt.Sprintf("cat > /app/napcat/config/onebot11.json << 'FIXEOF'\n%s\nFIXEOF", string(jsonBytes)))
	return cmd.Run()
}

func fixNapCatWebUIToken() error {
	out, err := exec.Command("docker", "exec", "openclaw-qq", "cat", "/app/napcat/config/webui.json").Output()
	if err != nil {
		return err
	}
	var data map[string]interface{}
	if err := json.Unmarshal(out, &data); err != nil {
		return err
	}
	data["token"] = "clawpanel-qq"
	jsonBytes, _ := json.MarshalIndent(data, "", "  ")
	cmd := exec.Command("docker", "exec", "openclaw-qq", "bash", "-c",
		fmt.Sprintf("cat > /app/napcat/config/webui.json << 'FIXEOF'\n%s\nFIXEOF", string(jsonBytes)))
	return cmd.Run()
}

// --- Helpers ---

func isDockerContainerExists(name string) bool {
	out, err := exec.Command("docker", "inspect", "--format", "{{.State.Status}}", name).Output()
	return err == nil && strings.TrimSpace(string(out)) != ""
}

func isPortListening(port int) bool {
	conn, err := net.DialTimeout("tcp", fmt.Sprintf("127.0.0.1:%d", port), 2*time.Second)
	if err != nil {
		return false
	}
	conn.Close()
	return true
}
