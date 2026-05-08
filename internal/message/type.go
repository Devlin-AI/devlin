package message

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
