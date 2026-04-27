package channel

type InboundMessage struct {
	Type      string `json:"type"`
	Content   string `json:"content"`
	MessageID int64  `json:"message_id,omitempty"`
	SessionID string `json:"session_id,omitempty"`
}

type OutboundMessage struct {
	Type      string       `json:"type"`
	Content   string       `json:"content"`
	ToolName  string       `json:"tool_name,omitempty"`
	ToolID    string       `json:"tool_id,omitempty"`
	Display   string       `json:"display,omitempty"`
	SessionID string       `json:"session_id,omitempty"`
	MessageID int64        `json:"message_id,omitempty"`
	Branches  []BranchInfo `json:"branches,omitempty"`
}

type BranchInfo struct {
	SessionID   string `json:"session_id"`
	ParentMsgID int64  `json:"parent_msg_id"`
	CreatedAt   int64  `json:"created_at"`
}
