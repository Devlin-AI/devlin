package store

type BranchMeta struct {
	SessionID   string
	ParentID    string
	ParentMsgID int64
}

type SessionMeta struct {
	ID        string
	Channel   string
	Mode      string
	CreatedAt int64
	UpdatedAt int64
}

type Message struct {
	ID         int64
	SessionID  string
	Role       string
	Content    string
	ToolCalls  string
	ToolCallID string
	ToolName   string
	Thinking   string
	Model      string
	Usage      string
	Timestamp  float64
}
