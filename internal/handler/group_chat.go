package handler

import (
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/zhaoxinyi02/ClawPanel/internal/config"
)

type groupChatSession struct {
	ID                  string            `json:"id"`
	ChatType            string            `json:"chatType"`
	AgentID             string            `json:"agentId"`
	ControllerAgentID   string            `json:"controllerAgentId"`
	PreferredAgentID    string            `json:"preferredAgentId,omitempty"`
	Title               string            `json:"title"`
	HostAgentID         string            `json:"hostAgentId"`
	ParticipantAgentIDs []string          `json:"participantAgentIds"`
	AgentSessionIDs     map[string]string `json:"agentSessionIds,omitempty"`
	Status              string            `json:"status"`
	LastSummary         string            `json:"lastSummary,omitempty"`
	CreatedAt           int64             `json:"createdAt"`
	UpdatedAt           int64             `json:"updatedAt"`
}

func groupChatSessionsPath(cfg *config.Config) string {
	return filepath.Join(cfg.DataDir, "group-chat", "sessions.json")
}

func groupChatMessagesPath(cfg *config.Config, sessionID string) string {
	return filepath.Join(cfg.DataDir, "group-chat", "sessions", sessionID+".messages.json")
}

func loadGroupChatSessions(cfg *config.Config) ([]groupChatSession, error) {
	data, err := os.ReadFile(groupChatSessionsPath(cfg))
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return []groupChatSession{}, nil
		}
		return nil, err
	}
	var sessions []groupChatSession
	if len(strings.TrimSpace(string(data))) == 0 {
		return []groupChatSession{}, nil
	}
	if err := json.Unmarshal(data, &sessions); err != nil {
		return nil, err
	}
	for i := range sessions {
		if strings.TrimSpace(sessions[i].ChatType) == "" {
			sessions[i].ChatType = "group"
		}
		if strings.TrimSpace(sessions[i].AgentID) == "" {
			sessions[i].AgentID = sessions[i].HostAgentID
		}
		if strings.TrimSpace(sessions[i].ControllerAgentID) == "" {
			sessions[i].ControllerAgentID = sessions[i].HostAgentID
		}
		if strings.TrimSpace(sessions[i].PreferredAgentID) == "" {
			sessions[i].PreferredAgentID = sessions[i].HostAgentID
		}
	}
	return sessions, nil
}

func saveGroupChatSessions(cfg *config.Config, sessions []groupChatSession) error {
	path := groupChatSessionsPath(cfg)
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	data, err := json.MarshalIndent(sessions, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(path, append(data, '\n'), 0o644)
}

func loadGroupChatMessages(cfg *config.Config, sessionID string) ([]map[string]interface{}, error) {
	path := groupChatMessagesPath(cfg, sessionID)
	data, err := os.ReadFile(path)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return []map[string]interface{}{}, nil
		}
		return nil, err
	}
	var messages []map[string]interface{}
	if len(strings.TrimSpace(string(data))) == 0 {
		return []map[string]interface{}{}, nil
	}
	if err := json.Unmarshal(data, &messages); err != nil {
		return nil, err
	}
	return messages, nil
}

func saveGroupChatMessages(cfg *config.Config, sessionID string, messages []map[string]interface{}) error {
	path := groupChatMessagesPath(cfg, sessionID)
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	data, err := json.MarshalIndent(messages, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(path, append(data, '\n'), 0o644)
}

func sortGroupChatSessions(sessions []groupChatSession) {
	sort.Slice(sessions, func(i, j int) bool { return sessions[i].UpdatedAt > sessions[j].UpdatedAt })
}

func findGroupChatSession(sessions []groupChatSession, id string) (int, *groupChatSession) {
	for i := range sessions {
		if sessions[i].ID == id {
			return i, &sessions[i]
		}
	}
	return -1, nil
}

func appendGroupChatMessage(sessionID, role, kind, content, agentID string) map[string]interface{} {
	msg := map[string]interface{}{
		"id":        fmt.Sprintf("%s-%d", role, time.Now().UnixNano()),
		"role":      role,
		"kind":      kind,
		"content":   strings.TrimSpace(content),
		"timestamp": time.Now().UTC().Format(time.RFC3339),
		"sessionId": sessionID,
	}
	if agentID != "" {
		msg["agentId"] = agentID
	}
	return msg
}

func groupChatShouldAsk(agentID, message string) bool {
	text := strings.ToLower(message)
	switch agentID {
	case "coding":
		return strings.Contains(text, "系统") || strings.Contains(text, "功能") || strings.Contains(text, "规则") || strings.Contains(text, "流程") || strings.Contains(text, "架构") || strings.Contains(text, "技术") || strings.Contains(text, "算法") || strings.Contains(text, "代码") || strings.Contains(text, "实现") || strings.Contains(text, "方案") || strings.Contains(text, "模块") || strings.Contains(text, "设计") || strings.Contains(text, "活动")
	case "writer":
		return strings.Contains(text, "文案") || strings.Contains(text, "宣传") || strings.Contains(text, "公告") || strings.Contains(text, "报告") || strings.Contains(text, "结构") || strings.Contains(text, "新闻稿") || strings.Contains(text, "标题") || strings.Contains(text, "对外")
	case "reviewer":
		return strings.Contains(text, "风险") || strings.Contains(text, "检查") || strings.Contains(text, "评估") || strings.Contains(text, "是否可行") || strings.Contains(text, "验收") || strings.Contains(text, "回滚") || strings.Contains(text, "灰度")
	default:
		return false
	}
}

func buildGroupDiscussionPrompt(session groupChatSession, userMessage, agentID string) string {
	roleHint := map[string]string{
		"coding":   "从实现、规则、流程、系统设计角度给建议。",
		"writer":   "从文案结构、表达方式、宣传材料角度给建议。",
		"reviewer": "从风险、验收、回滚、可交付性角度给建议。",
	}
	return fmt.Sprintf("你正在参加一个轻量多智能体讨论室。你的 agentId 是 %s，主控是 %s。\n用户问题：\n%s\n\n你的发言方向：%s\n\n请只从你的专业角度给出 3-5 条简洁观点，不要展开成长篇任务执行，不要输出 thinking 或内部标签。", agentID, session.HostAgentID, userMessage, roleHint[agentID])
}

func buildGroupSummaryPrompt(session groupChatSession, userMessage string, discussion []string) string {
	return fmt.Sprintf("你是讨论室主控智能体 %s。\n用户问题：\n%s\n\n以下是各参与智能体观点：\n%s\n\n请基于这些观点给用户一个轻量、清晰、可执行的总结，不要把它写成正式任务交付文档。", session.HostAgentID, userMessage, strings.Join(discussion, "\n\n"))
}

func buildGroupFallbackReply(agentID, userMessage string) string {
	switch agentID {
	case "coding":
		return "从实现角度建议先把活动拆成报名、签到、现场流程和数据统计 4 个模块，先明确每个模块的输入输出，再决定系统支持方式。"
	case "writer":
		return "从文案角度建议先整理 3 层结构：活动主题口号、参与方式说明、现场亮点与时间地点信息，方便后续同步到海报、推文和通知。"
	case "reviewer":
		return "从风险角度建议重点检查报名高峰期并发、签到识别准确率、现场秩序分流以及宣传口径是否一致。"
	default:
		return fmt.Sprintf("我从 %s 的角度建议先把问题拆清楚再讨论执行细节。", agentID)
	}
}

func buildGroupHostFallbackSummary(userMessage string, discussion []string) string {
	return fmt.Sprintf("先给你一个轻量结论：这个问题适合先按职责分 3 块推进——实现方案、文案结构、风险检查。\n\n当前讨论摘要：\n%s\n\n建议下一步先确认活动目标和时间范围，再分别展开设计。", strings.Join(discussion, "\n\n"))
}

func buildGroupHostDirectOpinion(userMessage string) string {
	return fmt.Sprintf("我先给你一个主控视角的快速判断：这个问题适合从实现方案、表达结构和风险控制三个角度讨论。你可以继续细化其中任意一块，我会再组织对应智能体展开。\n\n当前问题：%s", userMessage)
}

func ListGroupChatSessions(cfg *config.Config) gin.HandlerFunc {
	return func(c *gin.Context) {
		sessions, err := loadGroupChatSessions(cfg)
		if err != nil {
			c.JSON(http.StatusInternalServerError, gin.H{"ok": false, "error": err.Error()})
			return
		}
		sortGroupChatSessions(sessions)
		c.JSON(http.StatusOK, gin.H{"ok": true, "sessions": sessions})
	}
}

func CreateGroupChatSession(cfg *config.Config) gin.HandlerFunc {
	return func(c *gin.Context) {
		var req struct {
			Title               string   `json:"title"`
			HostAgentID         string   `json:"hostAgentId"`
			ParticipantAgentIDs []string `json:"participantAgentIds"`
		}
		_ = c.ShouldBindJSON(&req)
		participants := normalizePanelChatAgentIDs(req.ParticipantAgentIDs)
		host := strings.TrimSpace(req.HostAgentID)
		if host == "" {
			host = "main"
		}
		if !containsPanelChatAgent(participants, host) {
			participants = append([]string{host}, participants...)
			participants = normalizePanelChatAgentIDs(participants)
		}
		now := time.Now().UnixMilli()
		session := groupChatSession{ID: fmt.Sprintf("group-%d", now), ChatType: "group", AgentID: host, ControllerAgentID: host, PreferredAgentID: host, Title: buildPanelChatTitle(req.Title), HostAgentID: host, ParticipantAgentIDs: participants, AgentSessionIDs: map[string]string{}, Status: "active", CreatedAt: now, UpdatedAt: now}
		sessions, err := loadGroupChatSessions(cfg)
		if err != nil {
			c.JSON(http.StatusInternalServerError, gin.H{"ok": false, "error": err.Error()})
			return
		}
		sessions = append(sessions, session)
		sortGroupChatSessions(sessions)
		if err := saveGroupChatSessions(cfg, sessions); err != nil {
			c.JSON(http.StatusInternalServerError, gin.H{"ok": false, "error": err.Error()})
			return
		}
		c.JSON(http.StatusOK, gin.H{"ok": true, "session": session})
	}
}

func GetGroupChatSessionDetail(cfg *config.Config) gin.HandlerFunc {
	return func(c *gin.Context) {
		sessions, err := loadGroupChatSessions(cfg)
		if err != nil {
			c.JSON(http.StatusInternalServerError, gin.H{"ok": false, "error": err.Error()})
			return
		}
		_, session := findGroupChatSession(sessions, c.Param("id"))
		if session == nil {
			c.JSON(http.StatusNotFound, gin.H{"ok": false, "error": "会话不存在"})
			return
		}
		messages, err := loadGroupChatMessages(cfg, session.ID)
		if err != nil {
			c.JSON(http.StatusInternalServerError, gin.H{"ok": false, "error": err.Error()})
			return
		}
		c.JSON(http.StatusOK, gin.H{"ok": true, "session": session, "messages": messages})
	}
}

func RenameGroupChatSession(cfg *config.Config) gin.HandlerFunc {
	return func(c *gin.Context) {
		var req struct {
			Title string `json:"title"`
		}
		_ = c.ShouldBindJSON(&req)
		sessions, err := loadGroupChatSessions(cfg)
		if err != nil {
			c.JSON(http.StatusInternalServerError, gin.H{"ok": false, "error": err.Error()})
			return
		}
		idx, session := findGroupChatSession(sessions, c.Param("id"))
		if session == nil {
			c.JSON(http.StatusNotFound, gin.H{"ok": false, "error": "会话不存在"})
			return
		}
		sessions[idx].Title = buildPanelChatTitle(req.Title)
		sessions[idx].UpdatedAt = time.Now().UnixMilli()
		sortGroupChatSessions(sessions)
		if err := saveGroupChatSessions(cfg, sessions); err != nil {
			c.JSON(http.StatusInternalServerError, gin.H{"ok": false, "error": err.Error()})
			return
		}
		c.JSON(http.StatusOK, gin.H{"ok": true, "session": sessions[0]})
	}
}

func DeleteGroupChatSession(cfg *config.Config) gin.HandlerFunc {
	return func(c *gin.Context) {
		sessions, err := loadGroupChatSessions(cfg)
		if err != nil {
			c.JSON(http.StatusInternalServerError, gin.H{"ok": false, "error": err.Error()})
			return
		}
		idx, session := findGroupChatSession(sessions, c.Param("id"))
		if session == nil {
			c.JSON(http.StatusNotFound, gin.H{"ok": false, "error": "会话不存在"})
			return
		}
		sessions = append(sessions[:idx], sessions[idx+1:]...)
		if err := saveGroupChatSessions(cfg, sessions); err != nil {
			c.JSON(http.StatusInternalServerError, gin.H{"ok": false, "error": err.Error()})
			return
		}
		_ = os.Remove(groupChatMessagesPath(cfg, session.ID))
		c.JSON(http.StatusOK, gin.H{"ok": true})
	}
}

func SendGroupChatMessage(cfg *config.Config) gin.HandlerFunc {
	return func(c *gin.Context) {
		var req struct {
			Message string `json:"message"`
		}
		if err := c.ShouldBindJSON(&req); err != nil || strings.TrimSpace(req.Message) == "" {
			c.JSON(http.StatusBadRequest, gin.H{"ok": false, "error": "message required"})
			return
		}
		sessions, err := loadGroupChatSessions(cfg)
		if err != nil {
			c.JSON(http.StatusInternalServerError, gin.H{"ok": false, "error": err.Error()})
			return
		}
		idx, session := findGroupChatSession(sessions, c.Param("id"))
		if session == nil {
			c.JSON(http.StatusNotFound, gin.H{"ok": false, "error": "会话不存在"})
			return
		}
		messages, err := loadGroupChatMessages(cfg, session.ID)
		if err != nil {
			c.JSON(http.StatusInternalServerError, gin.H{"ok": false, "error": err.Error()})
			return
		}
		userMessage := strings.TrimSpace(req.Message)
		messages = append(messages, appendGroupChatMessage(session.ID, "user", "user", userMessage, ""))
		discussionBlocks := make([]string, 0, 4)
		for _, agentID := range session.ParticipantAgentIDs {
			if agentID == session.HostAgentID || !groupChatShouldAsk(agentID, userMessage) {
				continue
			}
			sessionKey := strings.TrimSpace(session.AgentSessionIDs[agentID])
			if sessionKey == "" {
				sessionKey = fmt.Sprintf("%s-%s", session.ID, agentID)
			}
			reply, actualSessionID, execErr := runPanelChatMessage(c.Request.Context(), cfg, panelChatSession{ID: session.ID, AgentID: agentID, OpenClawSessionID: sessionKey}, buildGroupDiscussionPrompt(*session, userMessage, agentID))
			if execErr != nil {
				reply = buildGroupFallbackReply(agentID, userMessage)
			}
			if strings.TrimSpace(actualSessionID) != "" {
				session.AgentSessionIDs[agentID] = strings.TrimSpace(actualSessionID)
			}
			reply = strings.TrimSpace(reply)
			if reply == "" {
				reply = buildGroupFallbackReply(agentID, userMessage)
			}
			if reply == "" {
				continue
			}
			messages = append(messages, appendGroupChatMessage(session.ID, "assistant", "discussion", reply, agentID))
			discussionBlocks = append(discussionBlocks, fmt.Sprintf("[%s]\n%s", agentID, summarizePanelChatArtifact(reply)))
		}
		hostKey := strings.TrimSpace(session.AgentSessionIDs[session.HostAgentID])
		if hostKey == "" {
			hostKey = fmt.Sprintf("%s-%s", session.ID, session.HostAgentID)
		}
		summary := ""
		var actualSessionID string
		var execErr error
		if len(discussionBlocks) == 0 {
			summary = buildGroupHostDirectOpinion(userMessage)
		} else {
			summaryPrompt := buildGroupSummaryPrompt(*session, userMessage, discussionBlocks)
			summary, actualSessionID, execErr = runPanelChatMessage(c.Request.Context(), cfg, panelChatSession{ID: session.ID, AgentID: session.HostAgentID, OpenClawSessionID: hostKey}, summaryPrompt)
			if execErr != nil {
				summary = buildGroupHostFallbackSummary(userMessage, discussionBlocks)
			}
		}
		if strings.TrimSpace(actualSessionID) != "" {
			session.AgentSessionIDs[session.HostAgentID] = strings.TrimSpace(actualSessionID)
		}
		summary = strings.TrimSpace(summary)
		if summary == "" {
			summary = buildGroupHostFallbackSummary(userMessage, discussionBlocks)
		}
		if summary != "" {
			messages = append(messages, appendGroupChatMessage(session.ID, "assistant", "summary", summary, session.HostAgentID))
			session.LastSummary = summarizePanelChatArtifact(summary)
		}
		session.UpdatedAt = time.Now().UnixMilli()
		sessions[idx] = *session
		sortGroupChatSessions(sessions)
		if err := saveGroupChatSessions(cfg, sessions); err != nil {
			c.JSON(http.StatusInternalServerError, gin.H{"ok": false, "error": err.Error()})
			return
		}
		if err := saveGroupChatMessages(cfg, session.ID, messages); err != nil {
			c.JSON(http.StatusInternalServerError, gin.H{"ok": false, "error": err.Error()})
			return
		}
		c.JSON(http.StatusOK, gin.H{"ok": true, "session": *session, "messages": messages, "reply": summary})
	}
}
