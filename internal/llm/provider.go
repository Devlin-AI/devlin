package llm

import (
	"context"
	"time"

	"github.com/devlin-ai/devlin/internal/message"
)

type StreamOptions struct {
	StallTimeout time.Duration
}

type Provider interface {
	Name() string
	Stream(ctx context.Context, messages []message.Message, tools []message.ToolDef, opts StreamOptions) (<-chan message.StreamEvent, error)
}
