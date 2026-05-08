package store

import (
	"database/sql"
	"fmt"
	"strings"
	"time"
)

func (r *repo) insertSession(s *SessionMeta) error {
	_, err := r.db.Exec(
		"INSERT INTO sessions (id, channel, mode, created_at, updated_at) VALUES (?, ?, ?, ?, ?)",
		s.ID, s.Channel, s.Mode, s.CreatedAt.Unix(), s.UpdatedAt.Unix(),
	)
	return err
}

func (r *repo) getSession(id string) (*SessionMeta, error) {
	row := r.db.QueryRow(
		"SELECT id, channel, mode, created_at, updated_at FROM sessions WHERE id = ?",
		id,
	)
	var s SessionMeta
	var createdSec, updatedSec float64
	if err := row.Scan(&s.ID, &s.Channel, &s.Mode, &createdSec, &updatedSec); err != nil {
		if err == sql.ErrNoRows {
			return nil, nil
		}
		return nil, err
	}
	s.CreatedAt = time.Unix(int64(createdSec), 0)
	s.UpdatedAt = time.Unix(int64(updatedSec), 0)
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
		var createdSec, updatedSec float64
		if err := rows.Scan(&s.ID, &s.Channel, &s.Mode, &createdSec, &updatedSec); err != nil {
			return nil, err
		}
		s.CreatedAt = time.Unix(int64(createdSec), 0)
		s.UpdatedAt = time.Unix(int64(updatedSec), 0)
		sessions = append(sessions, s)
	}
	return sessions, rows.Err()
}
