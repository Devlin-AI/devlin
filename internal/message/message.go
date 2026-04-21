package message

import (
	"encoding/json"
	"time"
)

type Role string
type StreamEventType string

const (
	RoleUser      Role = "user"
	RoleAssistant Role = "assistant"
	RoleSystem    Role = "system"
	RoleTool      Role = "tool"
)

const (
	StreamEventToken      StreamEventType = "token"
	StreamEventThinking   StreamEventType = "thinking"
	StreamEventDone       StreamEventType = "done"
	StreamEventError      StreamEventType = "error"
	StreamEventToolStart  StreamEventType = "tool_start"
	StreamEventToolOutput StreamEventType = "tool_output"
	StreamEventToolEnd    StreamEventType = "tool_end"
)

type ToolCall struct {
	ID       string `json:"id"`
	Type     string `json:"type"`
	Function struct {
		Name      string `json:"name"`
		Arguments string `json:"arguments"`
	} `json:"function"`
}

type ToolDef struct {
	Type     string      `json:"type"`
	Function FunctionDef `json:"function"`
}

type FunctionDef struct {
	Name        string          `json:"name"`
	Description string          `json:"description"`
	Parameters  json.RawMessage `json:"parameters"`
}

type Message struct {
	ID         string     `json:"id"`
	SessionID  string     `json:"session_id"`
	Role       Role       `json:"role"`
	Content    string     `json:"content"`
	Timestamp  time.Time  `json:"timestamp"`
	ToolCalls  []ToolCall `json:"tool_calls,omitempty"`
	ToolCallID string     `json:"tool_call_id,omitempty"`
}

type StreamEvent struct {
	SessionID string          `json:"session_id"`
	Type      StreamEventType `json:"type"`
	Token     string          `json:"token,omitempty"`
	Error     string          `json:"error,omitempty"`
	ToolName  string          `json:"tool_name,omitempty"`
	ToolID    string          `json:"tool_id,omitempty"`
}
