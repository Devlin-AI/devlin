package store

import "time"

type BranchMeta struct {
	SessionID   string
	ParentID    string
	ParentMsgID int64
}

type SessionMeta struct {
	ID        string
	Channel   string
	Mode      string
	CreatedAt time.Time
	UpdatedAt time.Time
}

type Message struct {
	ID             int64
	SessionID      string
	Role           string
	Content        string
	ToolCallsJSON  []byte
	ToolCallID     string
	ToolName       string
	Thinking       string
	Model          string
	UsageJSON      []byte
	Timestamp      time.Time
}
