package message

import (
	"encoding/json"
	"time"

	"github.com/devlin-ai/devlin/internal/store"
)

type Role = store.Role
type StreamEventType = store.StreamEventType

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

type Usage struct {
	PromptTokens     int `json:"prompt_tokens"`
	CompletionTokens int `json:"completion_tokens"`
	TotalTokens      int `json:"total_tokens"`
}

func FromStore(m store.Message) Message {
	return Message{
		ID:         m.ID,
		SessionID:  m.SessionID,
		Role:       store.Role(m.Role),
		Content:    m.Content,
		ToolCallID: m.ToolCallID,
		ToolName:   m.ToolName,
		Thinking:   m.Thinking,
		Timestamp:  m.Timestamp,
		ToolCalls:  UnmarshalToolCalls(m.ToolCallsJSON),
	}
}

func UnmarshalToolCalls(raw []byte) []ToolCall {
	if len(raw) == 0 {
		return nil
	}
	var calls []ToolCall
	json.Unmarshal(raw, &calls)
	return calls
}

func MarshalToolCalls(calls []ToolCall) []byte {
	if calls == nil {
		return nil
	}
	b, err := json.Marshal(calls)
	if err != nil {
		return nil
	}
	return b
}

func MarshalToolDefs(defs []ToolDef) []byte {
	if defs == nil {
		return nil
	}
	b, err := json.Marshal(defs)
	if err != nil {
		return nil
	}
	return b
}

func MarshalUsage(u *Usage) []byte {
	if u == nil {
		return nil
	}
	b, err := json.Marshal(u)
	if err != nil {
		return nil
	}
	return b
}

type Message struct {
	ID         int64       `json:"id"`
	SessionID  string      `json:"session_id"`
	Role       store.Role  `json:"role"`
	Content    string      `json:"content"`
	Timestamp  time.Time   `json:"timestamp"`
	ToolCalls  []ToolCall  `json:"tool_calls,omitempty"`
	ToolCallID string      `json:"tool_call_id,omitempty"`
	ToolName   string      `json:"tool_name,omitempty"`
	Thinking   string      `json:"thinking,omitempty"`
}

type StreamEvent struct {
	SessionID  string              `json:"session_id"`
	Type       store.StreamEventType `json:"type"`
	Token      string              `json:"token,omitempty"`
	Error      string              `json:"error,omitempty"`
	ToolName   string              `json:"tool_name,omitempty"`
	ToolID     string              `json:"tool_id,omitempty"`
	StatusCode int                 `json:"status_code,omitempty"`
	Usage      *Usage              `json:"usage,omitempty"`
}
