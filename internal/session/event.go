package session

type Event struct {
	Type     string `json:"type"`
	Content  string `json:"content"`
	ToolName string `json:"tool_name,omitempty"`
	ToolID   string `json:"tool_id,omitempty"`
	Display  string `json:"display,omitempty"`
}
