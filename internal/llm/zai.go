package llm

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"net/http"

	"github.com/devlin-ai/devlin/internal/message"
)

const userAgent = "Devlin/1.0"

var httpClient = &http.Client{
	Transport: &http.Transport{},
}

type ZaiProvider struct {
	apiKey  string
	model   string
	baseURL string
}

func NewZaiProvider(apiKey, model string) *ZaiProvider {
	return &ZaiProvider{
		apiKey:  apiKey,
		model:   model,
		baseURL: "https://api.z.ai/api/coding/paas/v4",
	}
}

func (z *ZaiProvider) Name() string {
	return "Zai"
}

func (z *ZaiProvider) Stream(ctx context.Context, messages []message.Message) (<-chan message.StreamEvent, error) {
	ch := make(chan message.StreamEvent)

	body := map[string]interface{}{
		"model":    z.model,
		"messages": messages,
		"stream":   true,
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

	go func() {
		defer close(ch)

		resp, err := httpClient.Do(req)

		if err != nil {
			ch <- message.StreamEvent{
				Type:  message.StreamEventError,
				Error: err.Error(),
			}
			return
		}

		defer resp.Body.Close()

		scanner := bufio.NewScanner(resp.Body)
		for scanner.Scan() {
			line := scanner.Text()

			if line == "" {
				continue
			}

			if len(line) < 6 || line[:6] != "data: " {
				continue
			}
			data := line[6:]

			if data == "[DONE]" {
				ch <- message.StreamEvent{
					Type: message.StreamEventDone,
				}
				return
			}

			var chunk struct {
				Choices []struct {
					Delta struct {
						Content          string `json:"content"`
						ReasoningContent string `json:"reasoning_content"`
					} `json:"delta"`
				} `json:"choices"`
			}
			if err := json.Unmarshal([]byte(data), &chunk); err != nil {
				continue
			}
			if len(chunk.Choices) == 0 {
				continue
			}

			if chunk.Choices[0].Delta.ReasoningContent != "" {
				ch <- message.StreamEvent{
					Type:  message.StreamEventThinking,
					Token: chunk.Choices[0].Delta.ReasoningContent,
				}
			}
			if chunk.Choices[0].Delta.Content != "" {
				ch <- message.StreamEvent{
					Type:  message.StreamEventToken,
					Token: chunk.Choices[0].Delta.Content,
				}
			}
		}
	}()

	return ch, nil
}

func init() {
	Register("zai-coding-plan", func(apiKey, model string) Provider {
		return NewZaiProvider(apiKey, model)
	})
}
