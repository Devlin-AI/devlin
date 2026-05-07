package store

import (
	"database/sql"
	"fmt"
	"strings"
)

type repo struct {
	db *sql.DB
}

func (r *repo) migrate() error {
	_, err := r.db.Exec(`
		CREATE TABLE IF NOT EXISTS sessions (
			id TEXT PRIMARY KEY,
			channel TEXT NOT NULL,
			mode TEXT NOT NULL DEFAULT 'coding',
			created_at REAL NOT NULL,
			updated_at REAL NOT NULL
		);

		CREATE TABLE IF NOT EXISTS messages (
			id INTEGER PRIMARY KEY AUTOINCREMENT,
			session_id TEXT NOT NULL REFERENCES sessions(id),
			role TEXT NOT NULL,
			content TEXT,
			tool_calls TEXT,
			tool_call_id TEXT,
			tool_name TEXT,
			thinking TEXT,
			model TEXT,
			usage TEXT,
			timestamp REAL NOT NULL
		);

		CREATE TABLE IF NOT EXISTS branches (
			session_id    TEXT PRIMARY KEY REFERENCES sessions(id),
			parent_id     TEXT REFERENCES sessions(id),
			parent_msg_id INTEGER REFERENCES messages(id)
		);

		CREATE INDEX IF NOT EXISTS idx_messages_session ON messages(session_id, id);
	`)
	if err != nil {
		return err
	}

	r.db.Exec("ALTER TABLE sessions RENAME COLUMN platform TO channel")
	r.db.Exec("ALTER TABLE sessions ADD COLUMN mode TEXT NOT NULL DEFAULT 'coding'")

	return nil
}

func (r *repo) insertSession(s *SessionMeta) error {
	_, err := r.db.Exec(
		"INSERT INTO sessions (id, channel, mode, created_at, updated_at) VALUES (?, ?, ?, ?, ?)",
		s.ID, s.Channel, s.Mode, s.CreatedAt, s.UpdatedAt,
	)
	return err
}

func (r *repo) getSession(id string) (*SessionMeta, error) {
	row := r.db.QueryRow(
		"SELECT id, channel, mode, created_at, updated_at FROM sessions WHERE id = ?",
		id,
	)
	var s SessionMeta
	if err := row.Scan(&s.ID, &s.Channel, &s.Mode, &s.CreatedAt, &s.UpdatedAt); err != nil {
		if err == sql.ErrNoRows {
			return nil, nil
		}
		return nil, err
	}
	return &s, nil
}

func (r *repo) updateSession(id string, fields map[string]any) error {
	if len(fields) == 0 {
		return nil
	}
	var setClauses []string
	var args []any
	for k, v := range fields {
		setClauses = append(setClauses, fmt.Sprintf("%s = ?", k))
		args = append(args, v)
	}
	args = append(args, id)
	query := fmt.Sprintf("UPDATE sessions SET %s WHERE id = ?", strings.Join(setClauses, ", "))
	_, err := r.db.Exec(query, args...)
	return err
}

func (r *repo) deleteSession(id string) error {
	_, err := r.db.Exec("DELETE FROM sessions WHERE id = ?", id)
	return err
}

func (r *repo) findSessions(channel, mode string, limit int) ([]SessionMeta, error) {
	query := "SELECT id, channel, mode, created_at, updated_at FROM sessions WHERE 1=1"
	var args []any
	if channel != "" {
		query += " AND channel = ?"
		args = append(args, channel)
	}
	if mode != "" {
		query += " AND mode = ?"
		args = append(args, mode)
	}
	query += " ORDER BY updated_at DESC"
	if limit > 0 {
		query += " LIMIT ?"
		args = append(args, limit)
	}
	rows, err := r.db.Query(query, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var sessions []SessionMeta
	for rows.Next() {
		var s SessionMeta
		if err := rows.Scan(&s.ID, &s.Channel, &s.Mode, &s.CreatedAt, &s.UpdatedAt); err != nil {
			return nil, err
		}
		sessions = append(sessions, s)
	}
	return sessions, rows.Err()
}

func (r *repo) insertMessage(sessionID, role, content string, toolCalls []byte, toolCallID, toolName, thinking, model string, usage []byte, ts float64) (int64, error) {
	result, err := r.db.Exec(
		`INSERT INTO messages (session_id, role, content, tool_calls, tool_call_id, tool_name, thinking, model, usage, timestamp)
		 VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		sessionID, role, content, toolCalls, toolCallID, toolName, thinking, model, usage, ts,
	)
	if err != nil {
		return 0, err
	}
	return result.LastInsertId()
}

func (r *repo) getMessage(id int64) (*Message, error) {
	row := r.db.QueryRow(
		"SELECT id, session_id, role, content, tool_calls, tool_call_id, tool_name, thinking FROM messages WHERE id = ?",
		id,
	)
	var msg Message
	if err := row.Scan(&msg.ID, &msg.SessionID, &msg.Role, &msg.Content, &msg.ToolCalls, &msg.ToolCallID, &msg.ToolName, &msg.Thinking); err != nil {
		if err == sql.ErrNoRows {
			return nil, nil
		}
		return nil, err
	}
	return &msg, nil
}

func (r *repo) updateMessage(id int64, fields map[string]any) error {
	if len(fields) == 0 {
		return nil
	}
	var setClauses []string
	var args []any
	for k, v := range fields {
		setClauses = append(setClauses, fmt.Sprintf("%s = ?", k))
		args = append(args, v)
	}
	args = append(args, id)
	query := fmt.Sprintf("UPDATE messages SET %s WHERE id = ?", strings.Join(setClauses, ", "))
	_, err := r.db.Exec(query, args...)
	return err
}

func (r *repo) deleteMessage(id int64) error {
	_, err := r.db.Exec("DELETE FROM messages WHERE id = ?", id)
	return err
}

func (r *repo) findMessages(sessionID string, upToID int64, excludeRoles []string, role string, limit int) ([]Message, error) {
	query := "SELECT id, session_id, role, content, tool_calls, tool_call_id, tool_name, thinking FROM messages WHERE 1=1"
	var args []any
	if sessionID != "" {
		query += " AND session_id = ?"
		args = append(args, sessionID)
	}
	if upToID > 0 {
		query += " AND id <= ?"
		args = append(args, upToID)
	}
	if len(excludeRoles) > 0 {
		placeholders := make([]string, len(excludeRoles))
		for i, r := range excludeRoles {
			placeholders[i] = "?"
			args = append(args, r)
		}
		query += fmt.Sprintf(" AND role NOT IN (%s)", strings.Join(placeholders, ","))
	}
	if role != "" {
		query += " AND role = ?"
		args = append(args, role)
	}
	query += " ORDER BY id"
	if limit > 0 {
		query += " LIMIT ?"
		args = append(args, limit)
	}
	rows, err := r.db.Query(query, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var msgs []Message
	for rows.Next() {
		var msg Message
		if err := rows.Scan(&msg.ID, &msg.SessionID, &msg.Role, &msg.Content, &msg.ToolCalls, &msg.ToolCallID, &msg.ToolName, &msg.Thinking); err != nil {
			return nil, err
		}
		msgs = append(msgs, msg)
	}
	return msgs, rows.Err()
}

func (r *repo) insertBranch(sessionID, parentID string, parentMsgID int64) error {
	_, err := r.db.Exec(
		"INSERT INTO branches (session_id, parent_id, parent_msg_id) VALUES (?, ?, ?)",
		sessionID, parentID, parentMsgID,
	)
	return err
}

func (r *repo) getBranch(sessionID string) (*BranchMeta, error) {
	row := r.db.QueryRow(
		"SELECT session_id, parent_id, parent_msg_id FROM branches WHERE session_id = ?",
		sessionID,
	)
	var b BranchMeta
	var parentID sql.NullString
	var parentMsgID sql.NullInt64
	if err := row.Scan(&b.SessionID, &parentID, &parentMsgID); err != nil {
		if err == sql.ErrNoRows {
			return nil, nil
		}
		return nil, err
	}
	if parentID.Valid {
		b.ParentID = parentID.String
	}
	if parentMsgID.Valid {
		b.ParentMsgID = parentMsgID.Int64
	}
	return &b, nil
}

func (r *repo) updateBranch(sessionID string, fields map[string]any) error {
	if len(fields) == 0 {
		return nil
	}
	var setClauses []string
	var args []any
	for k, v := range fields {
		setClauses = append(setClauses, fmt.Sprintf("%s = ?", k))
		args = append(args, v)
	}
	args = append(args, sessionID)
	query := fmt.Sprintf("UPDATE branches SET %s WHERE session_id = ?", strings.Join(setClauses, ", "))
	_, err := r.db.Exec(query, args...)
	return err
}

func (r *repo) deleteBranch(sessionID string) error {
	_, err := r.db.Exec("DELETE FROM branches WHERE session_id = ?", sessionID)
	return err
}

func (r *repo) findBranches(parentID string) ([]BranchMeta, error) {
	rows, err := r.db.Query(
		"SELECT session_id, parent_id, parent_msg_id FROM branches WHERE parent_id = ?",
		parentID,
	)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var branches []BranchMeta
	for rows.Next() {
		var b BranchMeta
		if err := rows.Scan(&b.SessionID, &b.ParentID, &b.ParentMsgID); err != nil {
			return nil, err
		}
		branches = append(branches, b)
	}
	return branches, rows.Err()
}
