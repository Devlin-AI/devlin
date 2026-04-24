package session

import (
	"database/sql"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"time"

	"github.com/devlin-ai/devlin/internal/logger"
	_ "modernc.org/sqlite"
)

type Store struct {
	db *sql.DB
}

func NewStore(dbPath string) (*Store, error) {
	if err := os.MkdirAll(filepath.Dir(dbPath), 0o755); err != nil {
		return nil, fmt.Errorf("create db directory: %w", err)
	}

	db, err := sql.Open("sqlite", dbPath)
	if err != nil {
		return nil, fmt.Errorf("open db: %w", err)
	}

	db.SetMaxOpenConns(1)

	if _, err := db.Exec("PRAGMA journal_mode=WAL"); err != nil {
		db.Close()
		return nil, fmt.Errorf("set WAL mode: %w", err)
	}

	if _, err := db.Exec("PRAGMA foreign_keys=ON"); err != nil {
		db.Close()
		return nil, fmt.Errorf("enable foreign keys: %w", err)
	}

	s := &Store{db: db}
	if err := s.migrate(); err != nil {
		db.Close()
		return nil, fmt.Errorf("migrate: %w", err)
	}

	return s, nil
}

func (s *Store) Close() error {
	return s.db.Close()
}

func (s *Store) migrate() error {
	_, err := s.db.Exec(`
		CREATE TABLE IF NOT EXISTS sessions (
			id TEXT PRIMARY KEY,
			platform TEXT NOT NULL,
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

		CREATE INDEX IF NOT EXISTS idx_messages_session ON messages(session_id, id);
	`)
	return err
}

func (s *Store) CreateSession(id, platform string) error {
	now := time.Now().Unix()
	_, err := s.db.Exec(
		"INSERT INTO sessions (id, platform, created_at, updated_at) VALUES (?, ?, ?, ?)",
		id, platform, now, now,
	)
	if err != nil {
		return fmt.Errorf("create session: %w", err)
	}
	return nil
}

func (s *Store) InsertMessage(sessionID string, role string, content string, toolCalls []byte, toolCallID string, toolName string, thinking string, model string, usage []byte, ts float64) error {
	_, err := s.db.Exec(
		`INSERT INTO messages (session_id, role, content, tool_calls, tool_call_id, tool_name, thinking, model, usage, timestamp)
		 VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		sessionID, role, content, toolCalls, toolCallID, toolName, thinking, model, usage, ts,
	)
	if err != nil {
		return fmt.Errorf("insert message: %w", err)
	}
	return nil
}

func (s *Store) TouchSession(id string) error {
	now := time.Now().Unix()
	_, err := s.db.Exec("UPDATE sessions SET updated_at = ? WHERE id = ?", now, id)
	if err != nil {
		return fmt.Errorf("touch session: %w", err)
	}
	return nil
}

func (s *Store) persistMessage(sessionID string, role string, content string, toolCallsJSON []byte, toolCallID string, toolName string, thinking string, model string, usageJSON []byte) {
	ts := float64(time.Now().UnixNano()) / 1e9
	if err := s.InsertMessage(sessionID, role, content, toolCallsJSON, toolCallID, toolName, thinking, model, usageJSON, ts); err != nil {
		logger.L().Error("failed to persist message", "session_id", sessionID, "role", role, "error", err)
	}
	if err := s.TouchSession(sessionID); err != nil {
		logger.L().Error("failed to touch session", "session_id", sessionID, "error", err)
	}
}

func marshalToolCalls(v interface{}) []byte {
	if v == nil {
		return nil
	}
	b, err := json.Marshal(v)
	if err != nil {
		logger.L().Error("failed to marshal tool calls", "error", err)
		return nil
	}
	return b
}

func marshalUsage(v interface{}) []byte {
	if v == nil {
		return nil
	}
	b, err := json.Marshal(v)
	if err != nil {
		logger.L().Error("failed to marshal usage", "error", err)
		return nil
	}
	return b
}
