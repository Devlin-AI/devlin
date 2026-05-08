package message

import (
	"encoding/json"
	"time"
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

type Message struct {
	ID         int64      `json:"id"`
	SessionID  string     `json:"session_id"`
	Role       Role       `json:"role"`
	Content    string     `json:"content"`
	Timestamp  time.Time  `json:"timestamp"`
	ToolCalls  []ToolCall `json:"tool_calls,omitempty"`
	ToolCallID string     `json:"tool_call_id,omitempty"`
	ToolName   string     `json:"tool_name,omitempty"`
	Thinking   string     `json:"thinking,omitempty"`
}

type StreamEvent struct {
	SessionID  string          `json:"session_id"`
	Type       StreamEventType `json:"type"`
	Token      string          `json:"token,omitempty"`
	Error      string          `json:"error,omitempty"`
	ToolName   string          `json:"tool_name,omitempty"`
	ToolID     string          `json:"tool_id,omitempty"`
	StatusCode int             `json:"status_code,omitempty"`
	Usage      *Usage          `json:"usage,omitempty"`
}
