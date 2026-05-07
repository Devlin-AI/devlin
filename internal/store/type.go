package store

import "encoding/json"

type Role string

const (
	RoleUser      Role = "user"
	RoleAssistant Role = "assistant"
	RoleSystem    Role = "system"
	RoleTool      Role = "tool"
)

type StreamEventType string

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

type Usage struct {
	PromptTokens     int `json:"prompt_tokens"`
	CompletionTokens int `json:"completion_tokens"`
	TotalTokens      int `json:"total_tokens"`
}
