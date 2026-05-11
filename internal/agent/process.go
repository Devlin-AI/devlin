package agent

import (
	"context"
	"fmt"
	"math/rand"
	"os"
	"sync"
	"time"

	"github.com/devlin-ai/devlin/internal/logger"
	"github.com/devlin-ai/devlin/internal/message"
	"github.com/devlin-ai/devlin/internal/prompt"
	"github.com/devlin-ai/devlin/internal/session"
	"github.com/devlin-ai/devlin/internal/tool"
)

const maxProviderRetries = 8
const maxStallRetries = 3
const stallStatusCode = 999

func isRetryableStatus(code int) bool {
	return code == 429 || code == 500 || code == 502 || code == 503
}

func retryBackoff(attempt int) time.Duration {
	base := 2 * time.Second
	delay := base * time.Duration(1<<uint(attempt))
	jitter := time.Duration(float64(delay) * 0.2 * rand.Float64())
	if delay > 256*time.Second {
		delay = 256 * time.Second
	}
	return delay + jitter
}

func (s *Session) ProcessMessage(content string) {
	s.mu.Lock()
	defer s.mu.Unlock()

	s.history = append(s.history, message.Message{
		Role:      message.RoleUser,
		Content:   content,
		Timestamp: time.Now(),
	})
	if _, err := session.CreateMessage(s.store, s.id, string(message.RoleUser), content, nil, "", "", "", "", nil); err != nil {
		logger.L().Error("failed to persist user message", "session_id", s.id, "error", err)
	}
	if err := session.Touch(s.store, s.id); err != nil {
		logger.L().Error("failed to touch session", "session_id", s.id, "error", err)
	}

	s.processLoop()
}

func (s *Session) processLoop() {
	parentCtx := s.parentCtx
	if parentCtx == nil {
		parentCtx = context.Background()
	}
	ctx := tool.ContextWithSpawner(parentCtx, s)
	toolDefs := buildToolDefsWithTools(tool.All())

	for {
		cwd, _ := os.Getwd()
		newPrompt := prompt.Build(cwd, tool.All())
		if newPrompt != s.systemPrompt {
			s.systemPrompt = newPrompt
			if _, err := session.CreateMessage(s.store, s.id, "system_prompt", newPrompt, nil, "", "", "", "", nil); err != nil {
				logger.L().Error("failed to persist system_prompt", "session_id", s.id, "error", err)
			}
		}

		ctx, cancel := context.WithCancel(ctx)
		s.setCancel(cancel)

		messages := s.history
		if s.systemPrompt != "" {
			messages = append([]message.Message{
				{
					Role:    message.RoleSystem,
					Content: s.systemPrompt,
				},
			}, messages...)
		}

		var assistantText string
		var thinkingText string
		var toolCalls []toolCall
		var streamErr error
		var streamUsage *message.Usage

		var stallRetries int

	attemptLoop:
		for attempt := 0; attempt <= maxProviderRetries; attempt++ {
			if attempt > 0 {
				s.emitter.SendEvent(Event{Type: "status", Content: fmt.Sprintf("Retrying... attempt %d/%d", attempt, maxProviderRetries)})
				select {
				case <-ctx.Done():
				case <-time.After(retryBackoff(attempt - 1)):
				}
			}

			if ctx.Err() != nil {
				s.emitter.SendEvent(Event{Type: "cancelled"})
				s.history = s.history[:len(s.history)-1]
				s.setCancel(nil)
				return
			}

			ch, err := s.provider.Stream(ctx, messages, toolDefs)
			if err != nil {
				if ctx.Err() != nil {
					s.emitter.SendEvent(Event{Type: "cancelled"})
					s.history = s.history[:len(s.history)-1]
				} else {
					logger.L().Error("stream failed", "error", err)
					s.emitter.SendEvent(Event{Type: "error", Content: err.Error()})
				}
				s.setCancel(nil)
				return
			}

			assistantText = ""
			thinkingText = ""
			toolCalls = nil
			streamErr = nil
			streamUsage = nil

			var retryNeeded bool
			var tokensReceived bool
			for evt := range ch {
				switch evt.Type {
				case message.StreamEventToken:
					assistantText += evt.Token
					s.emitter.SendEvent(Event{Type: "token", Content: evt.Token})
					tokensReceived = true
				case message.StreamEventThinking:
					thinkingText += evt.Token
					s.emitter.SendEvent(Event{Type: "thinking", Content: evt.Token})
					tokensReceived = true
				case message.StreamEventDone:
					if evt.Usage != nil {
						streamUsage = evt.Usage
					}
				case message.StreamEventToolStart:
					if evt.ToolID != "" {
						if len(toolCalls) > 0 {
							s.emitter.SendToolStart(toolCalls[len(toolCalls)-1])
						}
						toolCalls = append(toolCalls, toolCall{
							ID:   evt.ToolID,
							Name: evt.ToolName,
							Args: evt.Token,
						})
					} else if len(toolCalls) > 0 {
						toolCalls[len(toolCalls)-1].Args += evt.Token
					}
				case message.StreamEventError:
					if ctx.Err() != nil {
						s.emitter.SendEvent(Event{Type: "cancelled"})
						s.history = s.history[:len(s.history)-1]
						s.setCancel(nil)
						return
					}
					if evt.StatusCode == stallStatusCode {
						if !tokensReceived && stallRetries < maxStallRetries {
							stallRetries++
							logger.L().Warn("stream stall, retrying", "attempt", stallRetries, "max", maxStallRetries)
							s.emitter.SendEvent(Event{Type: "status", Content: fmt.Sprintf("Stream stalled, retrying... attempt %d/%d", stallRetries, maxStallRetries)})
							select {
							case <-ctx.Done():
							case <-time.After(retryBackoff(stallRetries - 1)):
							}
							attempt = -1
							continue attemptLoop
						}
						if tokensReceived {
							logger.L().Warn("stream stall with partial content")
							assistantText += "\n\n[Warning: Stream stalled — returning partial response]"
							if len(toolCalls) > 0 {
								assistantText += fmt.Sprintf(" (%d tool call(s) dropped)", len(toolCalls))
								toolCalls = nil
							}
						} else {
							logger.L().Error("stream stall retries exhausted", "retries", maxStallRetries)
							s.emitter.SendEvent(Event{Type: "error", Content: "Stream stalled repeatedly with no response"})
							s.setCancel(nil)
							return
						}
						break
					}
					if evt.StatusCode != 0 && isRetryableStatus(evt.StatusCode) && attempt < maxProviderRetries {
						logger.L().Warn("retryable provider error", "status", evt.StatusCode, "attempt", attempt+1, "max", maxProviderRetries)
						retryNeeded = true
						streamErr = fmt.Errorf("HTTP %d: %s", evt.StatusCode, evt.Error)
					} else {
						logger.L().Error("stream event error", "error", evt.Error, "status", evt.StatusCode)
						s.emitter.SendEvent(Event{Type: "error", Content: evt.Error})
						s.setCancel(nil)
						return
					}
				}
				if retryNeeded {
					break
				}
			}

			if !retryNeeded {
				break
			}
		}

		if streamErr != nil {
			logger.L().Error("provider retries exhausted", "error", streamErr, "retries", maxProviderRetries)
			s.emitter.SendEvent(Event{Type: "error", Content: fmt.Sprintf("Failed after %d retries: %s", maxProviderRetries, streamErr.Error())})
			s.setCancel(nil)
			return
		}

		s.setCancel(nil)

		if ctx.Err() != nil {
			s.emitter.SendEvent(Event{Type: "cancelled"})
			s.history = s.history[:len(s.history)-1]
			return
		}

		assistantMsg := message.Message{
			Role:      message.RoleAssistant,
			Content:   assistantText,
			Timestamp: time.Now(),
		}
		for _, tc := range toolCalls {
			assistantMsg.ToolCalls = append(assistantMsg.ToolCalls, message.ToolCall{
				ID:   tc.ID,
				Type: "function",
				Function: struct {
					Name      string `json:"name"`
					Arguments string `json:"arguments"`
				}{
					Name:      tc.Name,
					Arguments: tc.Args,
				},
			})
		}

		s.history = append(s.history, assistantMsg)
		assistantMsgID, err := session.CreateMessage(
			s.store,
			s.id,
			string(message.RoleAssistant),
			assistantText,
			message.MarshalToolCalls(assistantMsg.ToolCalls),
			"", "",
			thinkingText,
			s.model,
			message.MarshalUsage(streamUsage),
		)
		if err != nil {
			logger.L().Error("failed to persist assistant message", "session_id", s.id, "error", err)
		}
		if err := session.Touch(s.store, s.id); err != nil {
			logger.L().Error("failed to touch session", "session_id", s.id, "error", err)
		}

		if len(toolCalls) == 0 {
			s.emitter.SendEvent(Event{Type: "done", MessageID: assistantMsgID})
			return
		}

		if len(toolCalls) > 0 {
			s.emitter.SendToolStart(toolCalls[len(toolCalls)-1])
		}

		groups := s.partitionToolCalls(toolCalls)
		for _, g := range groups {
			if ctx.Err() != nil {
				s.emitter.SendEvent(Event{Type: "cancelled"})
				s.history = s.history[:len(s.history)-1]
				return
			}

			if g.safe && len(g.calls) > 1 {
				var wg sync.WaitGroup
				for _, tc := range g.calls {
					wg.Add(1)
					go func(tc toolCall) {
						defer wg.Done()
						s.executeTool(ctx, tc)
					}(tc)
				}
				wg.Wait()
			} else {
				for _, tc := range g.calls {
					s.executeTool(ctx, tc)
				}
			}

			if ctx.Err() != nil {
				s.emitter.SendEvent(Event{Type: "cancelled"})
				s.history = s.history[:len(s.history)-1]
				return
			}
		}
	}
}
