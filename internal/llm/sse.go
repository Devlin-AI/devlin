package llm

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"

	"github.com/devlin-ai/devlin/internal/logger"
	"github.com/devlin-ai/devlin/internal/message"
)

const userAgent = "Devlin/1.0"

var httpClient = &http.Client{
	Transport: &http.Transport{},
}

func buildBaseURL(raw string) string {
	return strings.TrimRight(raw, "/")
}

func streamOpenAISSE(ctx context.Context, req *http.Request) (<-chan message.StreamEvent, error) {
	ch := make(chan message.StreamEvent)

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
			body, _ := io.ReadAll(resp.Body)
			resp.Body.Close()
			log.Error("unexpected status code", "status", resp.StatusCode, "body", string(body))
			ch <- message.StreamEvent{
				Type:       message.StreamEventError,
				Error:      fmt.Sprintf("HTTP %d: %s", resp.StatusCode, string(body)),
				StatusCode: resp.StatusCode,
			}
			return
		}

		scanner := bufio.NewScanner(resp.Body)
		var streamUsage *message.Usage
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
					Type:  message.StreamEventDone,
					Usage: streamUsage,
				}
				return
			}

		var chunk struct {
			Usage *struct {
				PromptTokens     int `json:"prompt_tokens"`
				CompletionTokens int `json:"completion_tokens"`
				TotalTokens      int `json:"total_tokens"`
			} `json:"usage"`
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
			if chunk.Usage != nil {
				streamUsage = &message.Usage{
					PromptTokens:     chunk.Usage.PromptTokens,
					CompletionTokens: chunk.Usage.CompletionTokens,
					TotalTokens:      chunk.Usage.TotalTokens,
				}
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
