package message

import "time"

type Role string
type StreamEventType string

const (
	RoleUser      Role = "user"
	RoleAssistant Role = "assistant"
	RoleSystem    Role = "system"
)

const (
	StreamEventToken    StreamEventType = "token"
	StreamEventThinking StreamEventType = "thinking"
	StreamEventDone     StreamEventType = "done"
	StreamEventError    StreamEventType = "error"
)

type Message struct {
	ID        string    `json:"id"`
	SessionID string    `json:"session_id"`
	Role      Role      `json:"role"`
	Content   string    `json:"content"`
	Timestamp time.Time `json:"timestamp"`
}

type StreamEvent struct {
	SessionID string          `json:"session_id"`
	Type      StreamEventType `json:"type"`
	Token     string          `json:"token,omitempty"`
	Error     string          `json:"error,omitempty"`
}
