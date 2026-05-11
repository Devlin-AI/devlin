package session

import (
	"fmt"

	"github.com/devlin-ai/devlin/internal/branch"
	"github.com/devlin-ai/devlin/internal/message"
	"github.com/devlin-ai/devlin/internal/store"
)

func Create(db *store.Store, id, channel, mode string) error {
	return db.CreateSession(id, channel, mode)
}

func Touch(db *store.Store, id string) error {
	return db.TouchSession(id)
}

func Exists(db *store.Store, id string) (bool, error) {
	return db.SessionExists(id)
}

func Get(db *store.Store, id string) (*SessionMeta, error) {
	s, err := db.GetSession(id)
	if err != nil {
		return nil, fmt.Errorf("get session: %w", err)
	}
	return s, nil
}

func GetLast(db *store.Store, channel, mode string) (string, error) {
	return db.GetLastSession(channel, mode)
}

func List(db *store.Store, channel string) ([]SessionMeta, error) {
	raw, err := db.ListSessions(channel)
	if err != nil {
		return nil, fmt.Errorf("list sessions: %w", err)
	}
	result := make([]SessionMeta, len(raw))
	for i, s := range raw {
		result[i] = s
	}
	return result, nil
}

func CreateMessage(db *store.Store, sessionID string, role string, content string, toolCallsJSON []byte, toolCallID string, toolName string, thinking string, model string, usageJSON []byte) (int64, error) {
	return db.CreateMessage(sessionID, role, content, toolCallsJSON, toolCallID, toolName, thinking, model, usageJSON)
}

func ListMessages(db *store.Store, sessionID string) ([]message.Message, error) {
	raw, err := db.ListMessages(sessionID)
	if err != nil {
		return nil, fmt.Errorf("list messages: %w", err)
	}
	result := make([]message.Message, len(raw))
	for i, m := range raw {
		result[i] = message.FromStore(m)
	}
	return result, nil
}

func ListMessagesUpToID(db *store.Store, sessionID string, upToMsgID int64) ([]message.Message, error) {
	raw, err := db.ListMessagesUpToID(sessionID, upToMsgID)
	if err != nil {
		return nil, fmt.Errorf("list messages up to id: %w", err)
	}
	result := make([]message.Message, len(raw))
	for i, m := range raw {
		result[i] = message.FromStore(m)
	}
	return result, nil
}

func GetFirstUserMessage(db *store.Store, sessionID string) (string, error) {
	return db.GetFirstUserMessage(sessionID)
}

func LoadFullHistory(db *store.Store, sessionID string) ([]message.Message, error) {
	msgs, err := ListMessages(db, sessionID)
	if err != nil {
		return nil, fmt.Errorf("load messages for %s: %w", sessionID, err)
	}
	allMsgs := msgs

	chain, err := branch.LoadChain(db, sessionID)
	if err != nil {
		return nil, err
	}
	for i := len(chain) - 1; i >= 0; i-- {
		meta := chain[i]
		parentMsgs, err := ListMessagesUpToID(db, meta.ParentID, meta.ParentMsgID)
		if err != nil {
			return nil, fmt.Errorf("load messages up to id for %s: %w", meta.ParentID, err)
		}
		allMsgs = append(parentMsgs, allMsgs...)
	}
	return allMsgs, nil
}
