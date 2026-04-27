package channel

type InboundMessage struct {
	Type      string `json:"type"`
	Content   string `json:"content"`
	MessageID int64  `json:"message_id,omitempty"`
	SessionID string `json:"session_id,omitempty"`
	Channel   string `json:"channel,omitempty"`
	Mode      string `json:"mode,omitempty"`
}

type OutboundMessage struct {
	Type      string        `json:"type"`
	Content   string        `json:"content"`
	ToolName  string        `json:"tool_name,omitempty"`
	ToolID    string        `json:"tool_id,omitempty"`
	Display   string        `json:"display,omitempty"`
	SessionID string        `json:"session_id,omitempty"`
	MessageID int64         `json:"message_id,omitempty"`
	Mode      string        `json:"mode,omitempty"`
	Parent    *BranchInfo   `json:"parent,omitempty"`
	Branches  []BranchInfo  `json:"branches,omitempty"`
	Sessions  []SessionInfo `json:"sessions,omitempty"`
}

type BranchInfo struct {
	SessionID   string `json:"session_id"`
	ParentMsgID int64  `json:"parent_msg_id"`
	CreatedAt   int64  `json:"created_at"`
}

type SessionInfo struct {
	ID        string `json:"id"`
	Channel   string `json:"channel"`
	Mode      string `json:"mode"`
	CreatedAt int64  `json:"created_at"`
	UpdatedAt int64  `json:"updated_at"`
}
