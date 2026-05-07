package store

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
	r *repo
}

func NewStore(dbPath string) (*Store, error) {
	if err := os.MkdirAll(filepath.Dir(dbPath), 0o755); err != nil {
		return nil, fmt.Errorf("create db directory: %w", err)
	}

	db, err := openDB(dbPath)
	if err != nil {
		return nil, err
	}

	r := &repo{db: db}
	if err := r.migrate(); err != nil {
		db.Close()
		return nil, fmt.Errorf("migrate: %w", err)
	}

	return &Store{r: r}, nil
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

func (s *Store) Close() error {
	return s.r.db.Close()
}

func (s *Store) CreateSession(id, channel, mode string) error {
	now := time.Now().Unix()
	return s.r.insertSession(&SessionMeta{
		ID:        id,
		Channel:   channel,
		Mode:      mode,
		CreatedAt: now,
		UpdatedAt: now,
	})
}

func (s *Store) TouchSession(id string) error {
	now := time.Now().Unix()
	return s.r.updateSession(id, map[string]any{"updated_at": now})
}

func (s *Store) SessionExists(id string) (bool, error) {
	sess, err := s.r.getSession(id)
	if err != nil {
		return false, err
	}
	return sess != nil, nil
}

func (s *Store) GetLastSession(channel, mode string) (string, error) {
	sessions, err := s.r.findSessions(channel, mode, 1)
	if err != nil {
		return "", fmt.Errorf("get last session: %w", err)
	}
	if len(sessions) == 0 {
		return "", nil
	}
	return sessions[0].ID, nil
}

func (s *Store) GetSession(id string) (*SessionMeta, error) {
	sess, err := s.r.getSession(id)
	if err != nil {
		return nil, fmt.Errorf("get session: %w", err)
	}
	return sess, nil
}

func (s *Store) ListSessions(channel string) ([]SessionMeta, error) {
	sessions, err := s.r.findSessions(channel, "", 0)
	if err != nil {
		return nil, fmt.Errorf("list sessions: %w", err)
	}
	return sessions, nil
}

func (s *Store) InsertMessage(sessionID string, role string, content string, toolCalls []byte, toolCallID string, toolName string, thinking string, model string, usage []byte, ts float64) (int64, error) {
	id, err := s.r.insertMessage(sessionID, role, content, toolCalls, toolCallID, toolName, thinking, model, usage, ts)
	if err != nil {
		return 0, fmt.Errorf("insert message: %w", err)
	}
	return id, nil
}

func (s *Store) PersistMessage(sessionID string, role string, content string, toolCallsJSON []byte, toolCallID string, toolName string, thinking string, model string, usageJSON []byte) (int64, error) {
	ts := float64(time.Now().UnixNano()) / 1e9
	id, err := s.r.insertMessage(sessionID, role, content, toolCallsJSON, toolCallID, toolName, thinking, model, usageJSON, ts)
	if err != nil {
		return 0, fmt.Errorf("persist message: %w", err)
	}
	if err := s.TouchSession(sessionID); err != nil {
		logger.L().Error("failed to touch session", "session_id", sessionID, "error", err)
	}
	return id, nil
}

func (s *Store) LoadMessagesForSession(sessionID string) ([]message.Message, error) {
	msgs, err := s.r.findMessages(sessionID, 0, []string{"system", "tool_defs", "system_prompt"}, "", 0)
	if err != nil {
		return nil, fmt.Errorf("load messages for session: %w", err)
	}
	return ToMessages(msgs), nil
}

func (s *Store) LoadMessagesUpToID(sessionID string, upToMsgID int64) ([]message.Message, error) {
	msgs, err := s.r.findMessages(sessionID, upToMsgID, []string{"system", "tool_defs", "system_prompt"}, "", 0)
	if err != nil {
		return nil, fmt.Errorf("load messages up to id: %w", err)
	}
	return ToMessages(msgs), nil
}

func (s *Store) GetFirstUserMessage(sessionID string) (string, error) {
	msgs, err := s.r.findMessages(sessionID, 0, nil, "user", 1)
	if err != nil {
		return "", err
	}
	if len(msgs) == 0 {
		return "", nil
	}
	return msgs[0].Content, nil
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

func (s *Store) CreateBranch(sessionID, parentID string, parentMsgID int64) error {
	if err := s.r.insertBranch(sessionID, parentID, parentMsgID); err != nil {
		return fmt.Errorf("create branch: %w", err)
	}
	return nil
}

func (s *Store) LoadBranchMeta(sessionID string) (*BranchMeta, error) {
	b, err := s.r.getBranch(sessionID)
	if err != nil {
		return nil, fmt.Errorf("load branch meta: %w", err)
	}
	return b, nil
}

func (s *Store) ListBranches(sessionID string) ([]BranchMeta, error) {
	branches, err := s.r.findBranches(sessionID)
	if err != nil {
		return nil, fmt.Errorf("list branches: %w", err)
	}
	return branches, nil
}

func (s *Store) walkBranchUp(sessionID string, fn func(*BranchMeta) error) error {
	currentID := sessionID
	for currentID != "" {
		meta, err := s.r.getBranch(currentID)
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

func (s *Store) ComputeDepth(sessionID string) (int, error) {
	depth := 0
	err := s.walkBranchUp(sessionID, func(meta *BranchMeta) error {
		depth++
		return nil
	})
	return depth, err
}

func (s *Store) GetParentBranch(sessionID string) (*BranchMeta, error) {
	return s.r.getBranch(sessionID)
}

func (m *Message) ToMessage() *message.Message {
	out := &message.Message{
		ID:         m.ID,
		SessionID:  m.SessionID,
		Role:       message.Role(m.Role),
		Content:    m.Content,
		ToolCallID: m.ToolCallID,
		ToolName:   m.ToolName,
		Thinking:   m.Thinking,
		Timestamp:  time.Unix(int64(m.Timestamp), 0),
	}
	if m.ToolCalls != "" {
		json.Unmarshal([]byte(m.ToolCalls), &out.ToolCalls)
	}
	return out
}

func ToMessages(models []Message) []message.Message {
	out := make([]message.Message, len(models))
	for i := range models {
		out[i] = *models[i].ToMessage()
	}
	return out
}

func MarshalToolCalls(v interface{}) []byte {
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

func MarshalUsage(v interface{}) []byte {
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
