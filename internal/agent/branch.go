package agent

import (
	"github.com/devlin-ai/devlin/internal/branch"
	"github.com/devlin-ai/devlin/internal/session"
	"github.com/google/uuid"
)

func (s *Session) Branch(msgID int64) (string, error) {
	s.sessionMu.Lock()
	defer s.sessionMu.Unlock()

	branchID := uuid.New().String()

	if err := session.Create(s.store, branchID, s.channel, s.mode); err != nil {
		return "", err
	}

	if err := branch.Create(s.store, branchID, s.id, msgID); err != nil {
		return "", err
	}

	return branchID, nil
}

func (s *Session) ListBranches() ([]branch.BranchMeta, error) {
	return branch.ListChildren(s.store, s.id)
}

func (s *Session) GetParentBranch() (*branch.BranchMeta, error) {
	return branch.GetParent(s.store, s.id)
}
