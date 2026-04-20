package llm

import (
	"context"

	"github.com/devlin-ai/devlin/internal/message"
)

type Provider interface {
	Name() string
	Stream(ctx context.Context, messages []message.Message, tools []message.ToolDef) (<-chan message.StreamEvent, error)
}
