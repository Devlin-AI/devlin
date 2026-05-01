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
	Type           string           `json:"type"`
	Content        string           `json:"content"`
	ToolName       string           `json:"tool_name,omitempty"`
	ToolID         string           `json:"tool_id,omitempty"`
	Display        string           `json:"display,omitempty"`
	SessionID      string           `json:"session_id,omitempty"`
	MessageID      int64            `json:"message_id,omitempty"`
	Mode           string           `json:"mode,omitempty"`
	Parent         *BranchInfo      `json:"parent,omitempty"`
	Branches       []BranchInfo     `json:"branches,omitempty"`
	Sessions       []SessionInfo    `json:"sessions,omitempty"`
	Messages       []HistoryMessage `json:"messages,omitempty"`
	BranchPoints   []BranchPoint    `json:"branch_points,omitempty"`
	Siblings       []BranchInfo     `json:"siblings,omitempty"`
	SiblingIdx     int              `json:"sibling_idx,omitempty"`
	SubagentDepth  int              `json:"subagent_depth,omitempty"`
	SubagentDesc   string           `json:"subagent_desc,omitempty"`
}

type BranchInfo struct {
	SessionID    string `json:"session_id"`
	ParentMsgID  int64  `json:"parent_msg_id"`
	CreatedAt    int64  `json:"created_at"`
	FirstMessage string `json:"first_message,omitempty"`
}

type SessionInfo struct {
	ID        string `json:"id"`
	Channel   string `json:"channel"`
	Mode      string `json:"mode"`
	CreatedAt int64  `json:"created_at"`
	UpdatedAt int64  `json:"updated_at"`
}

type HistoryMessage struct {
	ID        int64  `json:"id"`
	Role      string `json:"role"`
	Content   string `json:"content"`
	ToolName  string `json:"tool_name,omitempty"`
	ToolCalls string `json:"tool_calls,omitempty"`
	ToolArgs  string `json:"tool_args,omitempty"`
}

type BranchPoint struct {
	MsgID     int64  `json:"msg_id"`
	SessionID string `json:"session_id"`
}
