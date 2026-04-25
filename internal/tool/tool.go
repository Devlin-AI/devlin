package tool

import (
	"context"
	"encoding/json"
)

type ToolDisplay struct {
	Title    string   `json:"title"`
	Subtitle string   `json:"subtitle,omitempty"`
	Body     []string `json:"body,omitempty"`
}

type Tool interface {
	Name() string
	Description() string
	Parameters() json.RawMessage
	Execute(ctx context.Context, args json.RawMessage) (string, error)
	Display(args, output string) ToolDisplay

	Core() bool
	PromptSnippet() string
	PromptGuidelines() []string
}

type StreamingExecutor interface {
	StreamingExecute(ctx context.Context, args json.RawMessage, onChunk func(chunk string)) (string, error)
}
