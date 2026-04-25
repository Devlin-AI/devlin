package session

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"sync"
	"time"

	"github.com/devlin-ai/devlin/internal/llm"
	"github.com/devlin-ai/devlin/internal/logger"
	"github.com/devlin-ai/devlin/internal/message"
	"github.com/devlin-ai/devlin/internal/prompt"
	"github.com/devlin-ai/devlin/internal/tool"
	"github.com/google/uuid"
)

type Session struct {
	id           string
	platform     string
	provider     llm.Provider
	store        *Store
	model        string
	history      []message.Message
	systemPrompt string
	mu           sync.Mutex
	cancelMu     sync.Mutex
	onEvent      func(Event)
	cancel       context.CancelFunc
}

func New(provider llm.Provider, store *Store, platform string, model string, onEvent func(Event)) (*Session, error) {
	id := uuid.New().String()

	if err := store.CreateSession(id, platform); err != nil {
		return nil, err
	}

	cwd, _ := os.Getwd()
	sysPrompt := prompt.Build(cwd, tool.All())

	s := &Session{
		id:           id,
		platform:     platform,
		provider:     provider,
		store:        store,
		model:        model,
		systemPrompt: sysPrompt,
		onEvent:      onEvent,
	}

	s.store.persistMessage(id, "tool_defs", string(marshalToolCalls(buildToolDefs())), nil, "", "", "", "", nil)

	return s, nil
}

func (s *Session) ID() string {
	return s.id
}

func (s *Session) IsExpired(timeout time.Duration) bool {
	return false
}

func (s *Session) Cancel() {
	s.cancelMu.Lock()
	defer s.cancelMu.Unlock()
	if s.cancel != nil {
		s.cancel()
	}
}

func (s *Session) setCancel(fn context.CancelFunc) {
	s.cancelMu.Lock()
	defer s.cancelMu.Unlock()
	s.cancel = fn
}

func (s *Session) ProcessMessage(content string) {
	s.mu.Lock()
	defer s.mu.Unlock()

	s.history = append(s.history, message.Message{
		Role:      message.RoleUser,
		Content:   content,
		Timestamp: time.Now(),
	})
	s.store.persistMessage(s.id, string(message.RoleUser), content, nil, "", "", "", "", nil)

	s.processLoop()
}

func (s *Session) sendEvent(evt Event) {
	if s.onEvent != nil {
		s.onEvent(evt)
	}
}

func (s *Session) processLoop() {
	toolDefs := buildToolDefs()

	for {
		ctx, cancel := context.WithCancel(context.Background())
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

		ch, err := s.provider.Stream(ctx, messages, toolDefs)
		if err != nil {
			if ctx.Err() != nil {
				s.sendEvent(Event{Type: "cancelled"})
				s.history = s.history[:len(s.history)-1]
			} else {
				logger.L().Error("stream failed", "error", err)
				s.sendEvent(Event{Type: "error", Content: err.Error()})
			}
			s.setCancel(nil)
			return
		}

		var assistantText string
		var thinkingText string
		var toolCalls []toolCall

		for evt := range ch {
			switch evt.Type {
			case message.StreamEventToken:
				assistantText += evt.Token
				s.sendEvent(Event{Type: "token", Content: evt.Token})
			case message.StreamEventThinking:
				thinkingText += evt.Token
				s.sendEvent(Event{Type: "thinking", Content: evt.Token})
			case message.StreamEventToolStart:
				display := string(marshalToolCallDisplay(tool.ToolDisplay{Title: evt.ToolName}))
				s.sendEvent(Event{
					Type:     "tool_start",
					Content:  evt.Token,
					ToolName: evt.ToolName,
					ToolID:   evt.ToolID,
					Display:  display,
				})

				if evt.ToolID != "" {
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
					s.sendEvent(Event{Type: "cancelled"})
					s.history = s.history[:len(s.history)-1]
				} else {
					logger.L().Error("stream event error", "error", evt.Error)
					s.sendEvent(Event{Type: "error", Content: evt.Error})
				}
				s.setCancel(nil)
				return
			}
		}

		s.setCancel(nil)

		if ctx.Err() != nil {
			s.sendEvent(Event{Type: "cancelled"})
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
		s.store.persistMessage(
			s.id,
			string(message.RoleAssistant),
			assistantText,
			marshalToolCalls(assistantMsg.ToolCalls),
			"", "",
			thinkingText,
			s.model,
			nil,
		)

		if len(toolCalls) == 0 {
			s.sendEvent(Event{Type: "done"})
			return
		}

		for _, tc := range toolCalls {
			if ctx.Err() != nil {
				s.sendEvent(Event{Type: "cancelled"})
				s.history = s.history[:len(s.history)-1]
				return
			}

			t, ok := tool.Get(tc.Name)
			if !ok {
				output := fmt.Sprintf("error: unknown tool %q", tc.Name)
				s.completeToolCall(tc, output, tool.ToolDisplay{Title: tc.Name, Body: []string{output}})
				continue
			}

			if se, ok := t.(tool.StreamingExecutor); ok {
				pending := ""
				const flushAt = 100

				sendPending := func() {
					if pending == "" {
						return
					}
					s.sendEvent(Event{
						Type:    "tool_output",
						Content: pending,
						ToolID:  tc.ID,
					})
					pending = ""
				}

				finalJSON, err := se.StreamingExecute(
					ctx, json.RawMessage(tc.Args),
					func(chunk string) {
						pending += chunk
						if len(pending) >= flushAt {
							sendPending()
						}
					},
				)
				sendPending()

				if err != nil {
					finalJSON = fmt.Sprintf("error: %v", err)
				}

				s.completeToolCall(tc, finalJSON, t.Display(tc.Args, finalJSON))

				if ctx.Err() != nil {
					s.sendEvent(Event{Type: "cancelled"})
					s.history = s.history[:len(s.history)-1]
					return
				}
				continue
			}

			output, err := t.Execute(ctx, json.RawMessage(tc.Args))
			if err != nil {
				output = fmt.Sprintf("error: %v\n%s", err, output)
			}

			s.completeToolCall(tc, output, t.Display(tc.Args, output))

			if ctx.Err() != nil {
				s.sendEvent(Event{Type: "cancelled"})
				s.history = s.history[:len(s.history)-1]
				return
			}
		}
	}
}

func (s *Session) completeToolCall(tc toolCall, output string, disp tool.ToolDisplay) {
	s.sendEvent(Event{
		Type:    "tool_output",
		Content: output,
		ToolID:  tc.ID,
		Display: string(marshalToolCallDisplay(disp)),
	})
	s.sendEvent(Event{
		Type:   "tool_end",
		ToolID: tc.ID,
	})

	toolMsg := message.Message{
		Role:       message.RoleTool,
		Content:    output,
		ToolCallID: tc.ID,
		Timestamp:  time.Now(),
	}
	s.history = append(s.history, toolMsg)
	s.store.persistMessage(
		s.id,
		string(message.RoleTool),
		output,
		nil,
		tc.ID,
		tc.Name,
		"", "", nil,
	)
}

type toolCall struct {
	ID   string
	Name string
	Args string
}

func buildToolDefs() []message.ToolDef {
	tools := tool.All()
	defs := make([]message.ToolDef, 0, len(tools))
	for _, t := range tools {
		defs = append(defs, message.ToolDef{
			Type: "function",
			Function: message.FunctionDef{
				Name:        t.Name(),
				Description: t.Description(),
				Parameters:  t.Parameters(),
			},
		})
	}
	return defs
}

func marshalToolCallDisplay(disp tool.ToolDisplay) []byte {
	b, err := json.Marshal(disp)
	if err != nil {
		logger.L().Error("failed to marshal tool display", "error", err)
		return []byte("{}")
	}
	return b
}
