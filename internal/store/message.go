package store

import (
	"database/sql"
	"fmt"
	"strings"
	"time"
)

func (s *Store) CreateMessage(sessionID, role, content string, toolCallsJSON []byte, toolCallID, toolName, thinking, model string, usageJSON []byte) (int64, error) {
	ts := float64(time.Now().UnixNano()) / 1e9
	result, err := s.db.Exec(
		`INSERT INTO messages (session_id, role, content, tool_calls, tool_call_id, tool_name, thinking, model, usage, timestamp)
		 VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		sessionID, role, content, toolCallsJSON, toolCallID, toolName, thinking, model, usageJSON, ts,
	)
	if err != nil {
		return 0, fmt.Errorf("create message: %w", err)
	}
	return result.LastInsertId()
}

func (s *Store) ListMessages(sessionID string) ([]Message, error) {
	msgs, err := s.findMessages(sessionID, 0, []string{"system", "tool_defs", "system_prompt"}, "", 0)
	if err != nil {
		return nil, fmt.Errorf("list messages: %w", err)
	}
	return msgs, nil
}

func (s *Store) ListMessagesUpToID(sessionID string, upToMsgID int64) ([]Message, error) {
	msgs, err := s.findMessages(sessionID, upToMsgID, []string{"system", "tool_defs", "system_prompt"}, "", 0)
	if err != nil {
		return nil, fmt.Errorf("list messages up to id: %w", err)
	}
	return msgs, nil
}

func (s *Store) GetFirstUserMessage(sessionID string) (string, error) {
	msgs, err := s.findMessages(sessionID, 0, nil, "user", 1)
	if err != nil {
		return "", fmt.Errorf("get first user message: %w", err)
	}
	if len(msgs) == 0 {
		return "", nil
	}
	return msgs[0].Content, nil
}

func (s *Store) findMessages(sessionID string, upToID int64, excludeRoles []string, role string, limit int) ([]Message, error) {
	query := "SELECT id, session_id, role, content, tool_calls, tool_call_id, tool_name, thinking, model, usage, timestamp FROM messages WHERE 1=1"
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
	rows, err := s.db.Query(query, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var msgs []Message
	for rows.Next() {
		var msg Message
		var ts float64
		if err := rows.Scan(&msg.ID, &msg.SessionID, &msg.Role, &msg.Content, &msg.ToolCallsJSON, &msg.ToolCallID, &msg.ToolName, &msg.Thinking, &msg.Model, &msg.UsageJSON, &ts); err != nil {
			return nil, err
		}
		msg.Timestamp = time.Unix(int64(ts), 0)
		msgs = append(msgs, msg)
	}
	return msgs, rows.Err()
}

func (s *Store) getMessage(id int64) (*Message, error) {
	row := s.db.QueryRow(
		"SELECT id, session_id, role, content, tool_calls, tool_call_id, tool_name, thinking, model, usage, timestamp FROM messages WHERE id = ?",
		id,
	)
	var msg Message
	var ts float64
	if err := row.Scan(&msg.ID, &msg.SessionID, &msg.Role, &msg.Content, &msg.ToolCallsJSON, &msg.ToolCallID, &msg.ToolName, &msg.Thinking, &msg.Model, &msg.UsageJSON, &ts); err != nil {
		if err == sql.ErrNoRows {
			return nil, nil
		}
		return nil, err
	}
	msg.Timestamp = time.Unix(int64(ts), 0)
	return &msg, nil
}

func (s *Store) updateMessage(id int64, fields map[string]any) error {
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
	_, err := s.db.Exec(query, args...)
	return err
}

func (s *Store) deleteMessage(id int64) error {
	_, err := s.db.Exec("DELETE FROM messages WHERE id = ?", id)
	return err
}
