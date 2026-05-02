package llm

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"

	"github.com/devlin-ai/devlin/internal/message"
)

type ZaiProvider struct {
	apiKey  string
	model   string
	baseURL string
}

func NewZaiProvider(apiKey, model, baseURL string) *ZaiProvider {
	z := &ZaiProvider{
		apiKey: apiKey,
		model:  model,
	}
	if baseURL != "" {
		z.baseURL = buildBaseURL(baseURL)
	} else {
		z.baseURL = "https://api.z.ai/api/coding/paas/v4"
	}
	return z
}

func (z *ZaiProvider) Name() string {
	return "Zai"
}

func (z *ZaiProvider) Stream(ctx context.Context, messages []message.Message, tools []message.ToolDef, opts StreamOptions) (<-chan message.StreamEvent, error) {
	apiMessages := make([]interface{}, len(messages))
	for i, msg := range messages {
		m := map[string]interface{}{
			"role":    string(msg.Role),
			"content": msg.Content,
		}
		if msg.Role == message.RoleAssistant && msg.Thinking != "" {
			m["reasoning_content"] = msg.Thinking
		}
		if len(msg.ToolCalls) > 0 {
			m["tool_calls"] = msg.ToolCalls
		}
		if msg.ToolCallID != "" {
			m["tool_call_id"] = msg.ToolCallID
		}
		apiMessages[i] = m
	}

	body := map[string]interface{}{
		"model":    z.model,
		"messages": apiMessages,
		"stream":   true,
	}

	if len(tools) > 0 {
		body["tools"] = tools
	}

	jsonBody, err := json.Marshal(body)
	if err != nil {
		return nil, err
	}

	req, err := http.NewRequestWithContext(ctx, "POST", z.baseURL+"/chat/completions", bytes.NewReader(jsonBody))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+z.apiKey)
	req.Header.Set("User-Agent", userAgent)

	return streamOpenAISSE(ctx, req, opts.StallTimeout)
}

func init() {
	Register("zai-coding-plan", func(apiKey, model, baseURL string) Provider {
		return NewZaiProvider(apiKey, model, baseURL)
	})
}
