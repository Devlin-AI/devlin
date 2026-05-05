package llm

import (
	"context"
	"time"

	"github.com/devlin-ai/devlin/internal/message"
)

const defaultStallTimeout = 60 * time.Second

var DefaultStallTimeout = defaultStallTimeout

func SetDefaultStallTimeout(d time.Duration) {
	DefaultStallTimeout = d
}

type Provider interface {
	Name() string
	Stream(ctx context.Context, messages []message.Message, tools []message.ToolDef) (<-chan message.StreamEvent, error)
}
