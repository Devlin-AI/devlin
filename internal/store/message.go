package store

import (
	"database/sql"
	"fmt"
	"strings"
)

func (r *repo) insertMessage(sessionID, role, content string, toolCallsJSON []byte, toolCallID, toolName, thinking, model string, usageJSON []byte, ts float64) (int64, error) {
	result, err := r.db.Exec(
		`INSERT INTO messages (session_id, role, content, tool_calls, tool_call_id, tool_name, thinking, model, usage, timestamp)
		 VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		sessionID, role, content, toolCallsJSON, toolCallID, toolName, thinking, model, usageJSON, ts,
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
	if err := row.Scan(&msg.ID, &msg.SessionID, &msg.Role, &msg.Content, &msg.ToolCallsJSON, &msg.ToolCallID, &msg.ToolName, &msg.Thinking); err != nil {
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
		if err := rows.Scan(&msg.ID, &msg.SessionID, &msg.Role, &msg.Content, &msg.ToolCallsJSON, &msg.ToolCallID, &msg.ToolName, &msg.Thinking); err != nil {
			return nil, err
		}
		msgs = append(msgs, msg)
	}
	return msgs, rows.Err()
}
