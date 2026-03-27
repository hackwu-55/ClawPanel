package model

import (
	"database/sql"
	"time"
)

type PanelChatParticipant struct {
	ID                int64  `json:"id"`
	SessionID         string `json:"sessionId"`
	AgentID           string `json:"agentId"`
	RoleType          string `json:"roleType"`
	OrderIndex        int    `json:"orderIndex"`
	AutoReply         bool   `json:"autoReply"`
	IsSummary         bool   `json:"isSummary"`
	Enabled           bool   `json:"enabled"`
	OpenClawSessionID string `json:"openclawSessionId"`
	CreatedAt         int64  `json:"createdAt"`
	UpdatedAt         int64  `json:"updatedAt"`
}

func ListPanelChatParticipants(db *sql.DB, sessionID string) ([]PanelChatParticipant, error) {
	rows, err := db.Query(`SELECT id, session_id, agent_id, role_type, order_index, auto_reply, is_summary, enabled, openclaw_session_id, created_at, updated_at FROM panel_chat_participants WHERE session_id = ? ORDER BY order_index ASC, id ASC`, sessionID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	items := make([]PanelChatParticipant, 0)
	for rows.Next() {
		var item PanelChatParticipant
		var autoReply, isSummary, enabled int
		if err := rows.Scan(&item.ID, &item.SessionID, &item.AgentID, &item.RoleType, &item.OrderIndex, &autoReply, &isSummary, &enabled, &item.OpenClawSessionID, &item.CreatedAt, &item.UpdatedAt); err != nil {
			return nil, err
		}
		item.AutoReply = autoReply == 1
		item.IsSummary = isSummary == 1
		item.Enabled = enabled == 1
		items = append(items, item)
	}
	if items == nil {
		items = []PanelChatParticipant{}
	}
	return items, nil
}

func ReplacePanelChatParticipants(db *sql.DB, sessionID string, items []PanelChatParticipant) error {
	now := time.Now().UnixMilli()
	tx, err := db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if _, err := tx.Exec(`DELETE FROM panel_chat_participants WHERE session_id = ?`, sessionID); err != nil {
		return err
	}
	for i, item := range items {
		if item.CreatedAt == 0 {
			item.CreatedAt = now
		}
		item.UpdatedAt = now
		if item.RoleType == "" {
			item.RoleType = "assistant"
		}
		if _, err := tx.Exec(`INSERT INTO panel_chat_participants (session_id, agent_id, role_type, order_index, auto_reply, is_summary, enabled, openclaw_session_id, created_at, updated_at) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
			sessionID, item.AgentID, item.RoleType, i, boolToInt(item.AutoReply), boolToInt(item.IsSummary), boolToInt(item.Enabled), item.OpenClawSessionID, item.CreatedAt, item.UpdatedAt); err != nil {
			return err
		}
	}
	return tx.Commit()
}

func UpdatePanelChatParticipantOpenClawSession(db *sql.DB, sessionID, agentID, openClawSessionID string) error {
	_, err := db.Exec(`UPDATE panel_chat_participants SET openclaw_session_id = ?, updated_at = ? WHERE session_id = ? AND agent_id = ?`, openClawSessionID, time.Now().UnixMilli(), sessionID, agentID)
	return err
}

func DeletePanelChatParticipants(db *sql.DB, sessionID string) error {
	_, err := db.Exec(`DELETE FROM panel_chat_participants WHERE session_id = ?`, sessionID)
	return err
}
