package agent

import (
	"fmt"

	"github.com/devlin-ai/devlin/internal/branch"
	"github.com/devlin-ai/devlin/internal/session"
	"github.com/google/uuid"
)

func (s *Session) Branch(msgID int64) (*Session, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	branchID := uuid.New().String()

	if err := session.Create(s.store, branchID, s.channel, s.mode); err != nil {
		return nil, err
	}

	if err := branch.Create(s.store, branchID, s.id, msgID); err != nil {
		return nil, err
	}

	historyCopy, err := session.ListMessagesUpToID(s.store, s.id, msgID)
	if err != nil {
		return nil, err
	}

	br := &Session{
		id:           branchID,
		channel:      s.channel,
		mode:         s.mode,
		provider:     s.provider,
		store:        s.store,
		model:        s.model,
		history:      historyCopy,
		systemPrompt: s.systemPrompt,
		onEvent:      s.onEvent,
		parentID:     s.id,
		branchPoint:  msgID,
	}

	return br, nil
}

func (s *Session) SwitchTo(sessionID string) (*Session, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	exists, err := session.Exists(s.store, sessionID)
	if err != nil {
		return nil, err
	}
	if !exists {
		return nil, fmt.Errorf("session %s not found", sessionID)
	}

	history, err := session.LoadFullHistory(s.store, sessionID)
	if err != nil {
		return nil, err
	}

	meta, err := branch.GetMeta(s.store, sessionID)
	if err != nil {
		return nil, err
	}

	var parentID string
	var branchPoint int64
	if meta != nil {
		parentID = meta.ParentID
		branchPoint = meta.ParentMsgID
	}

	target := &Session{
		id:           sessionID,
		channel:      s.channel,
		mode:         s.mode,
		provider:     s.provider,
		store:        s.store,
		model:        s.model,
		history:      history,
		systemPrompt: s.systemPrompt,
		onEvent:      s.onEvent,
		parentID:     parentID,
		branchPoint:  branchPoint,
	}

	return target, nil
}

func (s *Session) ListBranches() ([]branch.BranchMeta, error) {
	return branch.ListChildren(s.store, s.id)
}

func (s *Session) GetParentBranch() (*branch.BranchMeta, error) {
	return branch.GetParent(s.store, s.id)
}
