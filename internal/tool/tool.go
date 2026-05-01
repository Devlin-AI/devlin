package tool

import (
	"context"
	"encoding/json"
)

type contextKey struct{}

type DisplayType string

const (
	DisplayText DisplayType = "text"
	DisplayDiff DisplayType = "diff"
	DisplayCode DisplayType = "code"
)

type DisplayBlock struct {
	Type    DisplayType `json:"type"`
	Content string      `json:"content"`
	Lang    string      `json:"lang,omitempty"`
}

type ToolDisplay struct {
	Title    string         `json:"title"`
	Subtitle string         `json:"subtitle,omitempty"`
	Body     []DisplayBlock `json:"body,omitempty"`
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

type ConcurrencySafe interface {
	ConcurrencySafe() bool
}

type SessionSpawner interface {
	SpawnSubagent(ctx context.Context, description, prompt string) (string, error)
	MaxDepth() int
	Depth() int
}

func ContextWithSpawner(ctx context.Context, spawner SessionSpawner) context.Context {
	return context.WithValue(ctx, contextKey{}, spawner)
}

func SpawnerFromContext(ctx context.Context) SessionSpawner {
	if spawner, ok := ctx.Value(contextKey{}).(SessionSpawner); ok {
		return spawner
	}
	return nil
}
