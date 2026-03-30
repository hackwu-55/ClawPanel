package handler

import (
	"database/sql"
	"encoding/json"
	"net/http"
	"strings"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/zhaoxinyi02/ClawPanel/internal/config"
	"github.com/zhaoxinyi02/ClawPanel/internal/model"
	ws "github.com/zhaoxinyi02/ClawPanel/internal/websocket"
)

func wechatBridgeAuthorized(cfg *config.Config, c *gin.Context) bool {
	expected := strings.TrimSpace(toString(loadWechatConfigMap(cfg)["bridgeToken"]))
	if expected == "" {
		expected = defaultWechatBridgeToken
	}
	queryToken := strings.TrimSpace(c.Query("token"))
	if queryToken != "" {
		return queryToken == expected
	}
	auth := strings.TrimSpace(c.GetHeader("Authorization"))
	if strings.HasPrefix(strings.ToLower(auth), "bearer ") {
		auth = strings.TrimSpace(auth[7:])
	}
	if auth == "" {
		auth = strings.TrimSpace(c.GetHeader("X-Wechat-Bridge-Token"))
	}
	return auth != "" && auth == expected
}

func wechatBridgeString(raw map[string]interface{}, keys ...string) string {
	for _, key := range keys {
		if value := strings.TrimSpace(toString(raw[key])); value != "" {
			return value
		}
	}
	return ""
}

func wechatBridgeBool(raw map[string]interface{}, keys ...string) bool {
	for _, key := range keys {
		switch v := raw[key].(type) {
		case bool:
			return v
		case float64:
			return v != 0
		case string:
			switch strings.ToLower(strings.TrimSpace(v)) {
			case "1", "true", "yes":
				return true
			case "0", "false", "no":
				return false
			}
		}
	}
	return false
}

func wechatSourceName(raw interface{}) string {
	obj, ok := raw.(map[string]interface{})
	if !ok || obj == nil {
		return ""
	}
	if payload, ok := obj["payload"].(map[string]interface{}); ok && payload != nil {
		for _, key := range []string{"name", "topic", "alias", "id"} {
			if v := strings.TrimSpace(toString(payload[key])); v != "" {
				return v
			}
		}
	}
	for _, key := range []string{"name", "topic", "alias", "id"} {
		if v := strings.TrimSpace(toString(obj[key])); v != "" {
			return v
		}
	}
	return ""
}

func normalizeWechatInboundPayload(raw map[string]interface{}) map[string]interface{} {
	source, _ := raw["source"].(map[string]interface{})
	roomLabel := ""
	senderLabel := ""
	if source != nil {
		if room := source["room"]; room != nil {
			switch v := room.(type) {
			case string:
				roomLabel = strings.TrimSpace(v)
			default:
				roomLabel = wechatSourceName(v)
			}
		}
		senderLabel = wechatSourceName(source["from"])
	}
	payload := map[string]interface{}{
		"event":   strings.TrimSpace(wechatBridgeString(raw, "event", "type")),
		"talker":  strings.TrimSpace(wechatBridgeString(raw, "talker", "roomId", "roomid", "strTalker", "from")),
		"sender":  strings.TrimSpace(wechatBridgeString(raw, "sender", "senderWxid", "fromWxid", "from")),
		"content": strings.TrimSpace(wechatBridgeString(raw, "content", "strContent", "text")),
		"isRoom":  wechatBridgeBool(raw, "isRoom"),
		"isSelf":  wechatBridgeBool(raw, "isSelf", "isSender", "self"),
	}
	if roomLabel != "" {
		payload["talker"] = "room:" + roomLabel
		payload["isRoom"] = true
	}
	if senderLabel != "" {
		payload["sender"] = "user:" + senderLabel
	}
	if payload["event"] == "" {
		payload["event"] = "message"
	}
	talker := toString(payload["talker"])
	if talker == "" {
		talker = wechatBridgeString(raw, "conversationId")
		payload["talker"] = talker
	}
	if talker != "" && strings.HasSuffix(talker, "@chatroom") {
		payload["isRoom"] = true
	}
	sender := toString(payload["sender"])
	if sender == "" {
		if talker != "" && !wechatBridgeBool(payload, "isRoom") {
			sender = talker
		} else {
			sender = wechatBridgeString(raw, "fromUser", "wxid")
		}
		payload["sender"] = sender
	}
	content := toString(payload["content"])
	if wechatBridgeBool(payload, "isRoom") {
		if idx := strings.Index(content, ":\n"); idx > 0 {
			if sender == "" {
				payload["sender"] = strings.TrimSpace(content[:idx])
			}
			payload["content"] = strings.TrimSpace(content[idx+2:])
		}
	}
	payload["raw"] = raw
	return payload
}

func appendWechatEvent(db *sql.DB, hub *ws.Hub, source, eventType, summary, detail string) {
	e := &model.Event{
		Time:    time.Now().UnixMilli(),
		Source:  source,
		Type:    eventType,
		Summary: summary,
		Detail:  detail,
	}
	id, err := model.AddEvent(db, e)
	if err != nil {
		return
	}
	if hub == nil {
		return
	}
	entry := map[string]interface{}{
		"id":      id,
		"time":    e.Time,
		"source":  e.Source,
		"type":    e.Type,
		"summary": e.Summary,
		"detail":  e.Detail,
	}
	if payload, err := json.Marshal(map[string]interface{}{"type": "log-entry", "data": entry}); err == nil {
		hub.Broadcast(payload)
	}
}

func WechatBridgeCallback(db *sql.DB, hub *ws.Hub, rt *workflowRuntime, cfg *config.Config) gin.HandlerFunc {
	return func(c *gin.Context) {
		if !wechatBridgeAuthorized(cfg, c) {
			c.JSON(http.StatusUnauthorized, gin.H{"ok": false, "error": "unauthorized"})
			return
		}
		raw := map[string]interface{}{}
		contentType := strings.ToLower(strings.TrimSpace(c.ContentType()))
		if strings.Contains(contentType, "json") {
			if err := c.ShouldBindJSON(&raw); err != nil {
				c.JSON(http.StatusBadRequest, gin.H{"ok": false, "error": err.Error()})
				return
			}
		} else {
			raw["type"] = c.PostForm("type")
			raw["content"] = c.PostForm("content")
			raw["isMentioned"] = c.PostForm("isMentioned")
			raw["isMsgFromSelf"] = c.PostForm("isMsgFromSelf")
			raw["isSystemEvent"] = c.PostForm("isSystemEvent")
			if sourceJSON := strings.TrimSpace(c.PostForm("source")); sourceJSON != "" {
				var source map[string]interface{}
				if err := json.Unmarshal([]byte(sourceJSON), &source); err == nil {
					raw["source"] = source
				} else {
					raw["sourceRaw"] = sourceJSON
				}
			}
			if file, err := c.FormFile("content"); err == nil && file != nil {
				raw["content"] = "[文件] " + strings.TrimSpace(file.Filename)
			}
		}
		payload := normalizeWechatInboundPayload(raw)
		eventType := strings.TrimSpace(toString(payload["event"]))
		switch eventType {
		case "system_event_login", "system_event_logout", "system_event_error", "system_event_push_notify":
			appendWechatEvent(db, hub, "wechat", eventType, "微信系统事件", strings.TrimSpace(toString(payload["content"])))
			c.JSON(http.StatusOK, gin.H{"success": true})
			return
		}
		talker := strings.TrimSpace(toString(payload["talker"]))
		sender := strings.TrimSpace(toString(payload["sender"]))
		content := strings.TrimSpace(toString(payload["content"]))
		isRoom := wechatBridgeBool(payload, "isRoom")
		isSelf := wechatBridgeBool(payload, "isSelf")

		scope := "私聊"
		if isRoom {
			scope = "群聊"
		}
		if content == "" {
			content = "[非文本消息]"
		}
		summary := "微信" + scope + "消息"
		if sender != "" {
			summary += " · " + sender
		}
		appendWechatEvent(db, hub, "wechat", eventType, summary, content)

		if !isSelf && rt != nil && strings.TrimSpace(content) != "" {
			conversationID := talker
			if conversationID == "" {
				conversationID = sender
			}
			extra := map[string]interface{}{
				"scope":    scope,
				"isRoom":   isRoom,
				"talker":   talker,
				"sender":   sender,
				"provider": "wechat",
			}
			handled, reply, reason := rt.interceptInboundMessage("wechat", conversationID, sender, content, extra)
			if handled && strings.TrimSpace(reply) != "" {
				c.JSON(http.StatusOK, gin.H{
					"success": true,
					"data": gin.H{
						"type":    "text",
						"content": reply,
					},
					"reason": reason,
				})
				return
			}
			c.JSON(http.StatusOK, gin.H{"success": true, "handled": handled, "reason": reason})
			return
		}
		c.JSON(http.StatusOK, gin.H{"success": true, "handled": false})
	}
}
