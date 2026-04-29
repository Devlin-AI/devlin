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

func (z *ZaiProvider) Stream(ctx context.Context, messages []message.Message, tools []message.ToolDef) (<-chan message.StreamEvent, error) {
	body := map[string]interface{}{
		"model":    z.model,
		"messages": messages,
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

	return streamOpenAISSE(ctx, req)
}

func init() {
	Register("zai-coding-plan", func(apiKey, model, baseURL string) Provider {
		return NewZaiProvider(apiKey, model, baseURL)
	})
}
