package llm

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"net/http"

	"github.com/devlin-ai/devlin/internal/logger"
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

func (z *ZaiProvider) Stream(ctx context.Context, messages []message.Message, tools []message.ToolDef) (<-chan message.StreamEvent, error) {
	ch := make(chan message.StreamEvent)

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

	go func() {
		defer close(ch)
		log := logger.L()

		resp, err := httpClient.Do(req)

		if err != nil {
			log.Error("http request failed", "error", err)
			ch <- message.StreamEvent{
				Type:  message.StreamEventError,
				Error: err.Error(),
			}
			return
		}

		go func() {
			<-ctx.Done()
			resp.Body.Close()
		}()

		if resp.StatusCode != http.StatusOK {
			log.Error("unexpected status code", "status", resp.StatusCode)
		}

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
						ToolCalls        []struct {
							ID       string `json:"id"`
							Function struct {
								Name      string `json:"name"`
								Arguments string `json:"arguments"`
							} `json:"function"`
						} `json:"tool_calls"`
					} `json:"delta"`
				} `json:"choices"`
			}
			if err := json.Unmarshal([]byte(data), &chunk); err != nil {
				log.Warn("failed to unmarshal SSE chunk", "data", data, "error", err)
				continue
			}
			if len(chunk.Choices) == 0 {
				continue
			}

			if len(chunk.Choices[0].Delta.ToolCalls) > 0 {
				for _, tc := range chunk.Choices[0].Delta.ToolCalls {
					ch <- message.StreamEvent{
						Type:     message.StreamEventToolStart,
						ToolName: tc.Function.Name,
						ToolID:   tc.ID,
						Token:    tc.Function.Arguments,
					}
				}
				// continue
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
