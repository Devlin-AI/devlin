package channel

import "time"

type InboundMessage struct {
	Type      string `json:"type"`
	Content   string `json:"content"`
	MessageID int64  `json:"message_id,omitempty"`
	SessionID string `json:"session_id,omitempty"`
	Timstamp  time.Time
}

type OutboundMessage struct {
	Type      string       `json:"type"`
	Content   string       `json:"content"`
	SessionID string       `json:"session_id,omitempty"`
	MessageID int64        `json:"message_id,omitempty"`
	Branches  []BranchInfo `json:"branches,omitempty"`
}

type BranchInfo struct {
	SessionID   string `json:"session_id"`
	ParentMsgID int64  `json:"parent_msg_id"`
	CreatedAt   int64  `json:"created_at"`
}

type Adapter interface {
	Name() string
	Receive() (<-chan InboundMessage, error)
	Send(OutboundMessage) error
	SessionID() string
	SetSessionID(id string)
}
