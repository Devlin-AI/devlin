package session

import (
	"database/sql"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"time"

	"github.com/devlin-ai/devlin/internal/logger"
	"github.com/devlin-ai/devlin/internal/message"
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
			channel TEXT NOT NULL,
			mode TEXT NOT NULL DEFAULT 'agentic',
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
	s.db.Exec("ALTER TABLE sessions ADD COLUMN mode TEXT NOT NULL DEFAULT 'agentic'")

	return nil
}

func (s *Store) CreateSession(id, channel, mode string) error {
	now := time.Now().Unix()
	_, err := s.db.Exec(
		"INSERT INTO sessions (id, channel, mode, created_at, updated_at) VALUES (?, ?, ?, ?, ?)",
		id, channel, mode, now, now,
	)
	if err != nil {
		return fmt.Errorf("create session: %w", err)
	}
	return nil
}

func (s *Store) InsertMessage(sessionID string, role string, content string, toolCalls []byte, toolCallID string, toolName string, thinking string, model string, usage []byte, ts float64) (int64, error) {
	result, err := s.db.Exec(
		`INSERT INTO messages (session_id, role, content, tool_calls, tool_call_id, tool_name, thinking, model, usage, timestamp)
		 VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		sessionID, role, content, toolCalls, toolCallID, toolName, thinking, model, usage, ts,
	)
	if err != nil {
		return 0, fmt.Errorf("insert message: %w", err)
	}
	id, err := result.LastInsertId()
	if err != nil {
		return 0, fmt.Errorf("last insert id: %w", err)
	}
	return id, nil
}

func (s *Store) TouchSession(id string) error {
	now := time.Now().Unix()
	_, err := s.db.Exec("UPDATE sessions SET updated_at = ? WHERE id = ?", now, id)
	if err != nil {
		return fmt.Errorf("touch session: %w", err)
	}
	return nil
}

func (s *Store) persistMessage(sessionID string, role string, content string, toolCallsJSON []byte, toolCallID string, toolName string, thinking string, model string, usageJSON []byte) int64 {
	ts := float64(time.Now().UnixNano()) / 1e9
	id, err := s.InsertMessage(sessionID, role, content, toolCallsJSON, toolCallID, toolName, thinking, model, usageJSON, ts)
	if err != nil {
		logger.L().Error("failed to persist message", "session_id", sessionID, "role", role, "error", err)
	}
	if err := s.TouchSession(sessionID); err != nil {
		logger.L().Error("failed to touch session", "session_id", sessionID, "error", err)
	}
	return id
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

type BranchMeta struct {
	SessionID   string
	ParentID    string
	ParentMsgID int64
}

func (s *Store) CreateBranch(sessionID, parentID string, parentMsgID int64) error {
	_, err := s.db.Exec(
		"INSERT INTO branches (session_id, parent_id, parent_msg_id) VALUES (?, ?, ?)",
		sessionID, parentID, parentMsgID,
	)
	if err != nil {
		return fmt.Errorf("create branch: %w", err)
	}
	return nil
}

func (s *Store) LoadBranchMeta(sessionID string) (*BranchMeta, error) {
	row := s.db.QueryRow(
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
		return nil, fmt.Errorf("load branch meta: %w", err)
	}
	if parentID.Valid {
		b.ParentID = parentID.String
	}
	if parentMsgID.Valid {
		b.ParentMsgID = parentMsgID.Int64
	}
	return &b, nil
}

func (s *Store) LoadMessagesForSession(sessionID string) ([]message.Message, error) {
	rows, err := s.db.Query(
		"SELECT id, session_id, role, content, tool_calls, tool_call_id, tool_name FROM messages WHERE session_id = ? AND role NOT IN ('system', 'tool_defs') ORDER BY id",
		sessionID,
	)
	if err != nil {
		return nil, fmt.Errorf("load messages for session: %w", err)
	}
	defer rows.Close()

	var msgs []message.Message
	for rows.Next() {
		var msg message.Message
		var toolCallsJSON []byte
		if err := rows.Scan(&msg.ID, &msg.SessionID, &msg.Role, &msg.Content, &toolCallsJSON, &msg.ToolCallID, &msg.ToolName); err != nil {
			return nil, fmt.Errorf("scan message: %w", err)
		}
		if toolCallsJSON != nil {
			json.Unmarshal(toolCallsJSON, &msg.ToolCalls)
		}
		msgs = append(msgs, msg)
	}
	return msgs, rows.Err()
}

func (s *Store) LoadMessagesUpToID(sessionID string, upToMsgID int64) ([]message.Message, error) {
	rows, err := s.db.Query(
		"SELECT id, session_id, role, content, tool_calls, tool_call_id, tool_name FROM messages WHERE session_id = ? AND id <= ? AND role NOT IN ('system', 'tool_defs') ORDER BY id",
		sessionID, upToMsgID,
	)
	if err != nil {
		return nil, fmt.Errorf("load messages up to id: %w", err)
	}
	defer rows.Close()

	var msgs []message.Message
	for rows.Next() {
		var msg message.Message
		var toolCallsJSON []byte
		if err := rows.Scan(&msg.ID, &msg.SessionID, &msg.Role, &msg.Content, &toolCallsJSON, &msg.ToolCallID, &msg.ToolName); err != nil {
			return nil, fmt.Errorf("scan message: %w", err)
		}
		if toolCallsJSON != nil {
			json.Unmarshal(toolCallsJSON, &msg.ToolCalls)
		}
		msgs = append(msgs, msg)
	}
	return msgs, rows.Err()
}

func (s *Store) walkBranchUp(sessionID string, fn func(*BranchMeta) error) error {
	currentID := sessionID
	for currentID != "" {
		meta, err := s.LoadBranchMeta(currentID)
		if err != nil {
			return fmt.Errorf("load branch meta for %s: %w", currentID, err)
		}
		if meta == nil {
			break
		}
		if err := fn(meta); err != nil {
			return err
		}
		currentID = meta.ParentID
	}
	return nil
}

func (s *Store) LoadFullHistory(sessionID string) ([]message.Message, error) {
	msgs, err := s.LoadMessagesForSession(sessionID)
	if err != nil {
		return nil, fmt.Errorf("load messages for %s: %w", sessionID, err)
	}
	allMsgs := msgs

	err = s.walkBranchUp(sessionID, func(meta *BranchMeta) error {
		parentMsgs, err := s.LoadMessagesUpToID(meta.ParentID, meta.ParentMsgID)
		if err != nil {
			return fmt.Errorf("load messages up to id for %s: %w", meta.ParentID, err)
		}
		allMsgs = append(parentMsgs, allMsgs...)
		return nil
	})
	if err != nil {
		return nil, err
	}
	return allMsgs, nil
}

func (s *Store) LoadBranchChain(sessionID string) ([]BranchMeta, error) {
	var chain []BranchMeta
	err := s.walkBranchUp(sessionID, func(meta *BranchMeta) error {
		chain = append(chain, *meta)
		return nil
	})
	if err != nil {
		return nil, err
	}
	for i, j := 0, len(chain)-1; i < j; i, j = i+1, j-1 {
		chain[i], chain[j] = chain[j], chain[i]
	}
	return chain, nil
}

func (s *Store) ListBranches(sessionID string) ([]BranchMeta, error) {
	rows, err := s.db.Query(
		"SELECT b.session_id, b.parent_id, b.parent_msg_id FROM branches b WHERE b.parent_id = ?",
		sessionID,
	)
	if err != nil {
		return nil, fmt.Errorf("list branches: %w", err)
	}
	defer rows.Close()

	var branches []BranchMeta
	for rows.Next() {
		var b BranchMeta
		if err := rows.Scan(&b.SessionID, &b.ParentID, &b.ParentMsgID); err != nil {
			return nil, fmt.Errorf("scan branch: %w", err)
		}
		branches = append(branches, b)
	}
	return branches, rows.Err()
}

func (s *Store) SessionExists(id string) (bool, error) {
	var exists bool
	err := s.db.QueryRow("SELECT 1 FROM sessions WHERE id = ?", id).Scan(&exists)
	if err == sql.ErrNoRows {
		return false, nil
	}
	return exists, err
}

type SessionMeta struct {
	ID        string
	Channel   string
	Mode      string
	CreatedAt int64
	UpdatedAt int64
}

func (s *Store) GetLastSession(channel, mode string) (string, error) {
	var id string
	err := s.db.QueryRow(
		"SELECT id FROM sessions WHERE channel = ? AND mode = ? ORDER BY updated_at DESC LIMIT 1",
		channel, mode,
	).Scan(&id)
	if err == sql.ErrNoRows {
		return "", nil
	}
	if err != nil {
		return "", fmt.Errorf("get last session: %w", err)
	}
	return id, nil
}

func (s *Store) GetParentBranch(sessionID string) (*BranchMeta, error) {
	return s.LoadBranchMeta(sessionID)
}

func (s *Store) GetFirstUserMessage(sessionID string) (string, error) {
	var content string
	err := s.db.QueryRow(
		"SELECT content FROM messages WHERE session_id = ? AND role = 'user' ORDER BY id ASC LIMIT 1",
		sessionID,
	).Scan(&content)
	if err == sql.ErrNoRows {
		return "", nil
	}
	return content, err
}

func (s *Store) ListSessions(channel string) ([]SessionMeta, error) {
	rows, err := s.db.Query(
		"SELECT id, channel, mode, created_at, updated_at FROM sessions WHERE channel = ? ORDER BY updated_at DESC",
		channel,
	)
	if err != nil {
		return nil, fmt.Errorf("list sessions: %w", err)
	}
	defer rows.Close()

	var sessions []SessionMeta
	for rows.Next() {
		var sm SessionMeta
		if err := rows.Scan(&sm.ID, &sm.Channel, &sm.Mode, &sm.CreatedAt, &sm.UpdatedAt); err != nil {
			return nil, fmt.Errorf("scan session: %w", err)
		}
		sessions = append(sessions, sm)
	}
	return sessions, rows.Err()
}
