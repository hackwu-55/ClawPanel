package model

import (
	"database/sql"
	"fmt"
	"os"
	"path/filepath"
	"time"

	_ "modernc.org/sqlite"
)

// InitDB 初始化 SQLite 数据库
func InitDB(dataDir string) (*sql.DB, error) {
	if err := os.MkdirAll(dataDir, 0755); err != nil {
		return nil, fmt.Errorf("创建数据目录失败: %w", err)
	}

	dbPath := filepath.Join(dataDir, "clawpanel.db")
	db, err := sql.Open("sqlite", dbPath+"?_pragma=journal_mode(WAL)&_pragma=busy_timeout(5000)")
	if err != nil {
		return nil, fmt.Errorf("打开数据库失败: %w", err)
	}

	// 设置连接池
	db.SetMaxOpenConns(1) // SQLite 单写
	db.SetMaxIdleConns(1)
	db.SetConnMaxLifetime(time.Hour)

	// 创建表
	if err := migrate(db); err != nil {
		return nil, fmt.Errorf("数据库迁移失败: %w", err)
	}

	return db, nil
}

func migrate(db *sql.DB) error {
	schema := `
	CREATE TABLE IF NOT EXISTS events (
		id INTEGER PRIMARY KEY AUTOINCREMENT,
		time INTEGER NOT NULL,
		source TEXT NOT NULL DEFAULT 'system',
		type TEXT NOT NULL DEFAULT 'info',
		summary TEXT NOT NULL,
		detail TEXT DEFAULT '',
		created_at DATETIME DEFAULT CURRENT_TIMESTAMP
	);
	CREATE INDEX IF NOT EXISTS idx_events_time ON events(time DESC);
	CREATE INDEX IF NOT EXISTS idx_events_source ON events(source);

	CREATE TABLE IF NOT EXISTS settings (
		key TEXT PRIMARY KEY,
		value TEXT NOT NULL,
		updated_at DATETIME DEFAULT CURRENT_TIMESTAMP
	);

	CREATE TABLE IF NOT EXISTS workflow_templates (
		id TEXT PRIMARY KEY,
		name TEXT NOT NULL,
		description TEXT NOT NULL DEFAULT '',
		category TEXT NOT NULL DEFAULT '',
		status TEXT NOT NULL DEFAULT 'ready',
		trigger_mode TEXT NOT NULL DEFAULT 'manual',
		settings_json TEXT NOT NULL DEFAULT '{}',
		definition_json TEXT NOT NULL DEFAULT '{}',
		created_at INTEGER NOT NULL,
		updated_at INTEGER NOT NULL
	);
	CREATE INDEX IF NOT EXISTS idx_workflow_templates_status ON workflow_templates(status);

	CREATE TABLE IF NOT EXISTS workflow_runs (
		id TEXT PRIMARY KEY,
		short_id TEXT NOT NULL,
		template_id TEXT NOT NULL DEFAULT '',
		name TEXT NOT NULL,
		status TEXT NOT NULL,
		channel_id TEXT NOT NULL DEFAULT '',
		conversation_id TEXT NOT NULL DEFAULT '',
		user_id TEXT NOT NULL DEFAULT '',
		source_message TEXT NOT NULL DEFAULT '',
		settings_json TEXT NOT NULL DEFAULT '{}',
		context_json TEXT NOT NULL DEFAULT '{}',
		last_message TEXT NOT NULL DEFAULT '',
		created_at INTEGER NOT NULL,
		updated_at INTEGER NOT NULL
	);
	CREATE INDEX IF NOT EXISTS idx_workflow_runs_status ON workflow_runs(status);
	CREATE INDEX IF NOT EXISTS idx_workflow_runs_conversation ON workflow_runs(channel_id, conversation_id, user_id, updated_at DESC);

	CREATE TABLE IF NOT EXISTS workflow_steps (
		id TEXT PRIMARY KEY,
		run_id TEXT NOT NULL,
		step_key TEXT NOT NULL,
		title TEXT NOT NULL,
		step_type TEXT NOT NULL,
		status TEXT NOT NULL,
		order_index INTEGER NOT NULL,
		needs_approval INTEGER NOT NULL DEFAULT 0,
		input_json TEXT NOT NULL DEFAULT '{}',
		output_text TEXT NOT NULL DEFAULT '',
		error_text TEXT NOT NULL DEFAULT '',
		created_at INTEGER NOT NULL,
		updated_at INTEGER NOT NULL
	);
	CREATE INDEX IF NOT EXISTS idx_workflow_steps_run ON workflow_steps(run_id, order_index ASC);

	CREATE TABLE IF NOT EXISTS workflow_events (
		id INTEGER PRIMARY KEY AUTOINCREMENT,
		run_id TEXT NOT NULL,
		step_id TEXT NOT NULL DEFAULT '',
		event_type TEXT NOT NULL,
		message TEXT NOT NULL,
		payload_json TEXT NOT NULL DEFAULT '{}',
		created_at INTEGER NOT NULL
	);
	CREATE INDEX IF NOT EXISTS idx_workflow_events_run ON workflow_events(run_id, created_at ASC);

	CREATE TABLE IF NOT EXISTS panel_chat_participants (
		id INTEGER PRIMARY KEY AUTOINCREMENT,
		session_id TEXT NOT NULL,
		agent_id TEXT NOT NULL,
		role_type TEXT NOT NULL DEFAULT 'assistant',
		order_index INTEGER NOT NULL DEFAULT 0,
		auto_reply INTEGER NOT NULL DEFAULT 1,
		is_summary INTEGER NOT NULL DEFAULT 0,
		enabled INTEGER NOT NULL DEFAULT 1,
		openclaw_session_id TEXT NOT NULL DEFAULT '',
		created_at INTEGER NOT NULL,
		updated_at INTEGER NOT NULL,
		UNIQUE(session_id, agent_id)
	);
	CREATE INDEX IF NOT EXISTS idx_panel_chat_participants_session ON panel_chat_participants(session_id, order_index ASC);
	`
	_, err := db.Exec(schema)
	return err
}

// Event 事件日志
type Event struct {
	ID      int64  `json:"id"`
	Time    int64  `json:"time"`
	Source  string `json:"source"`
	Type    string `json:"type"`
	Summary string `json:"summary"`
	Detail  string `json:"detail"`
}

// AddEvent 添加事件
func AddEvent(db *sql.DB, e *Event) (int64, error) {
	if e.Time == 0 {
		e.Time = time.Now().UnixMilli()
	}
	result, err := db.Exec(
		"INSERT INTO events (time, source, type, summary, detail) VALUES (?, ?, ?, ?, ?)",
		e.Time, e.Source, e.Type, e.Summary, e.Detail,
	)
	if err != nil {
		return 0, err
	}
	return result.LastInsertId()
}

// GetEvents 获取事件列表
func GetEvents(db *sql.DB, limit, offset int, source, search string) ([]Event, int, error) {
	// 构建查询
	where := "1=1"
	args := []interface{}{}
	if source != "" {
		where += " AND source = ?"
		args = append(args, source)
	}
	if search != "" {
		where += " AND (summary LIKE ? OR detail LIKE ?)"
		args = append(args, "%"+search+"%", "%"+search+"%")
	}

	// 总数
	var total int
	countArgs := make([]interface{}, len(args))
	copy(countArgs, args)
	err := db.QueryRow("SELECT COUNT(*) FROM events WHERE "+where, countArgs...).Scan(&total)
	if err != nil {
		return nil, 0, err
	}

	// 查询
	query := fmt.Sprintf("SELECT id, time, source, type, summary, detail FROM events WHERE %s ORDER BY time DESC LIMIT ? OFFSET ?", where)
	args = append(args, limit, offset)
	rows, err := db.Query(query, args...)
	if err != nil {
		return nil, 0, err
	}
	defer rows.Close()

	var events []Event
	for rows.Next() {
		var e Event
		if err := rows.Scan(&e.ID, &e.Time, &e.Source, &e.Type, &e.Summary, &e.Detail); err != nil {
			continue
		}
		events = append(events, e)
	}
	if events == nil {
		events = []Event{}
	}
	return events, total, nil
}

// ClearEvents 清空事件
func ClearEvents(db *sql.DB) error {
	_, err := db.Exec("DELETE FROM events")
	return err
}

// GetSetting 获取设置
func GetSetting(db *sql.DB, key string) (string, error) {
	var value string
	err := db.QueryRow("SELECT value FROM settings WHERE key = ?", key).Scan(&value)
	return value, err
}

// SetSetting 设置
func SetSetting(db *sql.DB, key, value string) error {
	_, err := db.Exec(
		"INSERT INTO settings (key, value, updated_at) VALUES (?, ?, CURRENT_TIMESTAMP) ON CONFLICT(key) DO UPDATE SET value = ?, updated_at = CURRENT_TIMESTAMP",
		key, value, value,
	)
	return err
}
