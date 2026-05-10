package branch

import (
	"fmt"

	"github.com/devlin-ai/devlin/internal/store"
)

func Create(db *store.Store, sessionID, parentID string, parentMsgID int64) error {
	return db.CreateBranch(sessionID, parentID, parentMsgID)
}

func LoadMeta(db *store.Store, sessionID string) (*BranchMeta, error) {
	b, err := db.LoadBranchMeta(sessionID)
	if err != nil {
		return nil, fmt.Errorf("load branch meta: %w", err)
	}
	if b == nil {
		return nil, nil
	}
	return b, nil
}

func ListChildren(db *store.Store, parentID string) ([]BranchMeta, error) {
	raw, err := db.ListBranches(parentID)
	if err != nil {
		return nil, fmt.Errorf("list branches: %w", err)
	}
	result := make([]BranchMeta, len(raw))
	for i, b := range raw {
		result[i] = b
	}
	return result, nil
}

func LoadChain(db *store.Store, sessionID string) ([]BranchMeta, error) {
	chain, err := db.LoadBranchChain(sessionID)
	if err != nil {
		return nil, err
	}
	for i, j := 0, len(chain)-1; i < j; i, j = i+1, j-1 {
		chain[i], chain[j] = chain[j], chain[i]
	}
	return chain, nil
}

func ComputeDepth(db *store.Store, sessionID string) (int, error) {
	chain, err := LoadChain(db, sessionID)
	return len(chain), err
}

func GetParent(db *store.Store, sessionID string) (*BranchMeta, error) {
	return db.LoadBranchMeta(sessionID)
}
