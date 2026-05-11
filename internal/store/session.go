package store

import (
	"database/sql"
	"fmt"
	"strings"
	"time"
)

func (s *Store) CreateSession(id, channel, mode string) error {
	now := time.Now()
	_, err := s.db.Exec(
		"INSERT INTO sessions (id, channel, mode, created_at, updated_at) VALUES (?, ?, ?, ?, ?)",
		id, channel, mode, now.Unix(), now.Unix(),
	)
	return fmt.Errorf("create session: %w", err)
}

func (s *Store) GetSession(id string) (*SessionMeta, error) {
	row := s.db.QueryRow(
		"SELECT id, channel, mode, created_at, updated_at FROM sessions WHERE id = ?",
		id,
	)
	var sm SessionMeta
	var createdSec, updatedSec float64
	if err := row.Scan(&sm.ID, &sm.Channel, &sm.Mode, &createdSec, &updatedSec); err != nil {
		if err == sql.ErrNoRows {
			return nil, nil
		}
		return nil, err
	}
	sm.CreatedAt = time.Unix(int64(createdSec), 0)
	sm.UpdatedAt = time.Unix(int64(updatedSec), 0)
	return &sm, nil
}

func (s *Store) GetLastSession(channel, mode string) (string, error) {
	sessions, err := s.listSessions(channel, mode, 1)
	if err != nil {
		return "", fmt.Errorf("get last session: %w", err)
	}
	if len(sessions) == 0 {
		return "", nil
	}
	return sessions[0].ID, nil
}

func (s *Store) ListSessions(channel string) ([]SessionMeta, error) {
	sessions, err := s.listSessions(channel, "", 0)
	if err != nil {
		return nil, fmt.Errorf("list sessions: %w", err)
	}
	return sessions, nil
}

func (s *Store) SessionExists(id string) (bool, error) {
	sess, err := s.GetSession(id)
	if err != nil {
		return false, fmt.Errorf("session exists: %w", err)
	}
	return sess != nil, nil
}

func (s *Store) TouchSession(id string) error {
	now := time.Now().Unix()
	_, err := s.db.Exec("UPDATE sessions SET updated_at = ? WHERE id = ?", now, id)
	return err
}

func (s *Store) listSessions(channel, mode string, limit int) ([]SessionMeta, error) {
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
	rows, err := s.db.Query(query, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var sessions []SessionMeta
	for rows.Next() {
		var sm SessionMeta
		var createdSec, updatedSec float64
		if err := rows.Scan(&sm.ID, &sm.Channel, &sm.Mode, &createdSec, &updatedSec); err != nil {
			return nil, err
		}
		sm.CreatedAt = time.Unix(int64(createdSec), 0)
		sm.UpdatedAt = time.Unix(int64(updatedSec), 0)
		sessions = append(sessions, sm)
	}
	return sessions, rows.Err()
}

func (s *Store) updateSession(id string, fields map[string]any) error {
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
	_, err := s.db.Exec(query, args...)
	return err
}
