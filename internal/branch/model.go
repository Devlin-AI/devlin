package branch

import "github.com/devlin-ai/devlin/internal/store"

type BranchMeta struct {
	SessionID   string
	ParentID    string
	ParentMsgID int64
}

func FromStore(b store.BranchMeta) BranchMeta {
	return BranchMeta{
		SessionID:   b.SessionID,
		ParentID:    b.ParentID,
		ParentMsgID: b.ParentMsgID,
	}
}
