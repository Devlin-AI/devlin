package store

import (
	"database/sql"
	"fmt"
	"os"
	"path/filepath"

	_ "modernc.org/sqlite"
)

type Store struct {
	db *sql.DB
}

func NewStore(dbPath string) (*Store, error) {
	if err := os.MkdirAll(filepath.Dir(dbPath), 0o755); err != nil {
		return nil, fmt.Errorf("create db directory: %w", err)
	}

	db, err := openDB(dbPath)
	if err != nil {
		return nil, err
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

func openDB(dbPath string) (*sql.DB, error) {
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
	return db, nil
}

func (s *Store) migrate() error {
	_, err := s.db.Exec(`
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

	s.db.Exec("ALTER TABLE sessions RENAME COLUMN platform TO channel")
	s.db.Exec("ALTER TABLE sessions ADD COLUMN mode TEXT NOT NULL DEFAULT 'coding'")

	return nil
}
