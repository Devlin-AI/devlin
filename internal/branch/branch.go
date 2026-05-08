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
	meta := FromStore(*b)
	return &meta, nil
}

func ListChildren(db *store.Store, parentID string) ([]BranchMeta, error) {
	raw, err := db.ListBranches(parentID)
	if err != nil {
		return nil, fmt.Errorf("list branches: %w", err)
	}
	result := make([]BranchMeta, len(raw))
	for i, b := range raw {
		result[i] = FromStore(b)
	}
	return result, nil
}

func LoadChain(db *store.Store, sessionID string) ([]BranchMeta, error) {
	var chain []BranchMeta
	err := WalkUp(db, sessionID, func(meta BranchMeta) error {
		chain = append(chain, meta)
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

func ComputeDepth(db *store.Store, sessionID string) (int, error) {
	depth := 0
	err := WalkUp(db, sessionID, func(meta BranchMeta) error {
		depth++
		return nil
	})
	return depth, err
}

func GetParent(db *store.Store, sessionID string) (*BranchMeta, error) {
	b, err := db.LoadBranchMeta(sessionID)
	if err != nil {
		return nil, err
	}
	if b == nil {
		return nil, nil
	}
	meta := FromStore(*b)
	return &meta, nil
}

func WalkUp(db *store.Store, sessionID string, fn func(BranchMeta) error) error {
	currentID := sessionID
	for currentID != "" {
		meta, err := db.LoadBranchMeta(currentID)
		if err != nil {
			return fmt.Errorf("load branch meta for %s: %w", currentID, err)
		}
		if meta == nil {
			break
		}
		if err := fn(FromStore(*meta)); err != nil {
			return err
		}
		currentID = meta.ParentID
	}
	return nil
}
