package message

import (
	"time"

	"github.com/devlin-ai/devlin/internal/store"
)

type Role = store.Role
type StreamEventType = store.StreamEventType
type ToolCall = store.ToolCall
type ToolDef = store.ToolDef
type FunctionDef = store.FunctionDef
type Usage = store.Usage

const (
	RoleUser      = store.RoleUser
	RoleAssistant = store.RoleAssistant
	RoleSystem    = store.RoleSystem
	RoleTool      = store.RoleTool
)

const (
	StreamEventToken      = store.StreamEventToken
	StreamEventThinking   = store.StreamEventThinking
	StreamEventDone       = store.StreamEventDone
	StreamEventError      = store.StreamEventError
	StreamEventToolStart  = store.StreamEventToolStart
	StreamEventToolOutput = store.StreamEventToolOutput
	StreamEventToolEnd    = store.StreamEventToolEnd
)

type Message struct {
	ID         int64          `json:"id"`
	SessionID  string         `json:"session_id"`
	Role       store.Role     `json:"role"`
	Content    string         `json:"content"`
	Timestamp  time.Time      `json:"timestamp"`
	ToolCalls  []store.ToolCall `json:"tool_calls,omitempty"`
	ToolCallID string         `json:"tool_call_id,omitempty"`
	ToolName   string         `json:"tool_name,omitempty"`
	Thinking   string         `json:"thinking,omitempty"`
}

type StreamEvent struct {
	SessionID  string              `json:"session_id"`
	Type       store.StreamEventType `json:"type"`
	Token      string              `json:"token,omitempty"`
	Error      string              `json:"error,omitempty"`
	ToolName   string              `json:"tool_name,omitempty"`
	ToolID     string              `json:"tool_id,omitempty"`
	StatusCode int                 `json:"status_code,omitempty"`
	Usage      *store.Usage        `json:"usage,omitempty"`
}
