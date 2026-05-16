package agent

import (
	"context"
	"fmt"
	"math/rand"
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

func (s *Session) ProcessMessage(content string, onEvent func(Event)) {
	s.sessionMu.Lock()
	defer s.sessionMu.Unlock()

	s.onEvent = onEvent

	s.history = append(s.history, message.Message{
		Role:      message.RoleUser,
		Content:   content,
		Timestamp: time.Now(),
	})
	if _, err := session.CreateMessage(s.store, s.id, string(message.RoleUser), content, nil, "", "", "", "", nil); err != nil {
		logger.Default().Error("failed to persist user message", "session_id", s.id, "error", err)
	}
	if err := session.Touch(s.store, s.id); err != nil {
		logger.Default().Error("failed to touch session", "session_id", s.id, "error", err)
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
		s.refreshSystemPrompt()

		ctx, cancel := context.WithCancel(ctx)
		s.setCancel(cancel)

		messages := s.buildMessages()

		var stallRetries int
		var streamErr error
		var result *streamResult
		var err error

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

			result, err = s.runProviderAttempt(ctx, messages, toolDefs)
			if err != nil {
				if ctx.Err() != nil {
					s.emitter.SendEvent(Event{Type: "cancelled"})
					s.history = s.history[:len(s.history)-1]
				} else {
					logger.Default().Error("stream failed", "error", err)
					s.emitter.SendEvent(Event{Type: "error", Content: err.Error()})
				}
				s.setCancel(nil)
				return
			}

			if result.isStall {
				if stallRetries < maxStallRetries {
					stallRetries++
					logger.Default().Warn("stream stall, retrying", "attempt", stallRetries, "max", maxStallRetries)
					s.emitter.SendEvent(Event{Type: "status", Content: fmt.Sprintf("Stream stalled, retrying... attempt %d/%d", stallRetries, maxStallRetries)})
					select {
					case <-ctx.Done():
					case <-time.After(retryBackoff(stallRetries - 1)):
					}
					attempt = -1
					continue
				}
				if result.hasPartial {
					logger.Default().Warn("stream stall with partial content")
					result.assistantText += "\n\n[Warning: Stream stalled — returning partial response]"
					if len(result.toolCalls) > 0 {
						result.assistantText += fmt.Sprintf(" (%d tool call(s) dropped)", len(result.toolCalls))
						result.toolCalls = nil
					}
				} else {
					logger.Default().Error("stream stall retries exhausted", "retries", maxStallRetries)
					s.emitter.SendEvent(Event{Type: "error", Content: "Stream stalled repeatedly with no response"})
					s.setCancel(nil)
					return
				}
			}

			if result.isProviderError {
				streamErr = fmt.Errorf("HTTP %d: %s", result.statusCode, result.errorStr)
				continue
			}

			if result.errorStr != "" {
				logger.Default().Error("stream event error", "error", result.errorStr, "status", result.statusCode)
				s.emitter.SendEvent(Event{Type: "error", Content: result.errorStr})
				s.setCancel(nil)
				return
			}

			break
		}

		if streamErr != nil {
			logger.Default().Error("provider retries exhausted", "error", streamErr, "retries", maxProviderRetries)
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

		assistantMsgID := s.persistAssistantMessage(result.assistantText, result.thinkingText, result.toolCalls, result.usage)
		if assistantMsgID == 0 {
			s.emitter.SendEvent(Event{Type: "error", Content: "Failed to save response"})
			s.setCancel(nil)
			return
		}

		if len(result.toolCalls) == 0 {
			s.emitter.SendEvent(Event{Type: "done", MessageID: assistantMsgID})
			return
		}

		s.executePartitionedTools(ctx, result.toolCalls)
	}
}

func (s *Session) buildMessages() []message.Message {
	messages := s.history
	if s.systemPrompt != "" {
		messages = append([]message.Message{
			{
				Role:    message.RoleSystem,
				Content: s.systemPrompt,
			},
		}, messages...)
	}
	return messages
}

func (s *Session) refreshSystemPrompt() {
	newPrompt := prompt.Build(s.cwd, tool.All())
	if newPrompt != s.systemPrompt {
		s.systemPrompt = newPrompt
		if _, err := session.CreateMessage(s.store, s.id, "system_prompt", newPrompt, nil, "", "", "", "", nil); err != nil {
			logger.Default().Error("failed to persist system_prompt", "session_id", s.id, "error", err)
		}
	}
}

func (s *Session) runProviderAttempt(ctx context.Context, messages []message.Message, toolDefs []message.ToolDef) (*streamResult, error) {
	ch, err := s.provider.Stream(ctx, messages, toolDefs)
	if err != nil {
		return nil, err
	}

	result := &streamResult{}
	var tokensReceived bool

	for evt := range ch {
		switch evt.Type {
		case message.StreamEventToken:
			result.assistantText += evt.Token
			s.emitter.SendEvent(Event{Type: "token", Content: evt.Token})
			tokensReceived = true
		case message.StreamEventThinking:
			result.thinkingText += evt.Token
			s.emitter.SendEvent(Event{Type: "thinking", Content: evt.Token})
			tokensReceived = true
		case message.StreamEventDone:
			if evt.Usage != nil {
				result.usage = evt.Usage
			}
		case message.StreamEventToolStart:
			if evt.ToolID != "" {
				if len(result.toolCalls) > 0 {
					s.emitter.SendToolStart(result.toolCalls[len(result.toolCalls)-1])
				}
				result.toolCalls = append(result.toolCalls, toolCall{
					ID:   evt.ToolID,
					Name: evt.ToolName,
					Args: evt.Token,
				})
			} else if len(result.toolCalls) > 0 {
				result.toolCalls[len(result.toolCalls)-1].Args += evt.Token
			}
		case message.StreamEventError:
			if ctx.Err() != nil {
				s.emitter.SendEvent(Event{Type: "cancelled"})
				s.history = s.history[:len(s.history)-1]
				return nil, ctx.Err()
			}
			if evt.StatusCode == stallStatusCode {
				if !tokensReceived {
					result.isStall = true
					return result, nil
				}
				result.isStall = true
				result.hasPartial = true
				return result, nil
			}
			if evt.StatusCode != 0 && isRetryableStatus(evt.StatusCode) {
				result.isProviderError = true
				result.statusCode = evt.StatusCode
				result.errorStr = evt.Error
				return result, nil
			}
			result.errorStr = evt.Error
			result.statusCode = evt.StatusCode
			return result, nil
		}
		if result.isProviderError {
			break
		}
	}

	return result, nil
}

func (s *Session) persistAssistantMessage(assistantText, thinkingText string, toolCalls []toolCall, usage *message.Usage) int64 {
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

	s.historyMu.Lock()
	s.history = append(s.history, assistantMsg)
	s.historyMu.Unlock()
	assistantMsgID, err := session.CreateMessage(
		s.store,
		s.id,
		string(message.RoleAssistant),
		assistantText,
		message.MarshalToolCalls(assistantMsg.ToolCalls),
		"", "",
		thinkingText,
		s.model,
		message.MarshalUsage(usage),
	)
	if err != nil {
		logger.Default().Error("failed to persist assistant message", "session_id", s.id, "error", err)
	}
	if err := session.Touch(s.store, s.id); err != nil {
		logger.Default().Error("failed to touch session", "session_id", s.id, "error", err)
	}
	return assistantMsgID
}

func (s *Session) executePartitionedTools(ctx context.Context, toolCalls []toolCall) {
	if len(toolCalls) > 0 {
		s.emitter.SendToolStart(toolCalls[len(toolCalls)-1])
	}

	groups := s.partitionToolCalls(toolCalls)
	for _, g := range groups {
		if ctx.Err() != nil {
			s.historyMu.Lock()
			s.history = s.history[:len(s.history)-1]
			s.historyMu.Unlock()
			s.emitter.SendEvent(Event{Type: "cancelled"})
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
			s.historyMu.Lock()
			s.history = s.history[:len(s.history)-1]
			s.historyMu.Unlock()
			s.emitter.SendEvent(Event{Type: "cancelled"})
			return
		}
	}
}
