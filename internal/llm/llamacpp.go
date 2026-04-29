package llm

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"

	"github.com/devlin-ai/devlin/internal/message"
)

type LlamaCppProvider struct {
	apiKey  string
	model   string
	baseURL string
}

func NewLlamaCppProvider(apiKey, model, baseURL string) *LlamaCppProvider {
	return &LlamaCppProvider{
		apiKey:  apiKey,
		model:   model,
		baseURL: buildBaseURL(baseURL),
	}
}

func (p *LlamaCppProvider) Name() string {
	return "LlamaCpp"
}

func (p *LlamaCppProvider) Stream(ctx context.Context, messages []message.Message, tools []message.ToolDef) (<-chan message.StreamEvent, error) {
	body := map[string]interface{}{
		"model":    p.model,
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

	req, err := http.NewRequestWithContext(ctx, "POST", p.baseURL+"/chat/completions", bytes.NewReader(jsonBody))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("User-Agent", userAgent)
	if p.apiKey != "" {
		req.Header.Set("Authorization", "Bearer "+p.apiKey)
	}

	return streamOpenAISSE(ctx, req)
}

func init() {
	Register("llamacpp", func(apiKey, model, baseURL string) Provider {
		return NewLlamaCppProvider(apiKey, model, baseURL)
	})
}
