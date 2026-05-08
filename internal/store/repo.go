package store

import (
	"database/sql"
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
