package main

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	"github.com/devlin-ai/devlin/internal/llm"
	"github.com/devlin-ai/devlin/internal/logger"
	"github.com/devlin-ai/devlin/internal/message"
	"github.com/devlin-ai/devlin/internal/tool"
	"github.com/gorilla/websocket"
)

type toolCall struct {
	ID   string
	Name string
	Args string
}

func processUserMessage(conn *websocket.Conn, provider llm.Provider, history *[]message.Message) {
	toolDefs := buildToolDefs()

	for {
		ch, err := provider.Stream(context.Background(), *history, toolDefs)
		if err != nil {
			logger.L().Error("stream failed", "error", err)
			conn.WriteJSON(outgoingEvent{
				Type:    "error",
				Content: err.Error(),
			})
			return
		}

		var assistantText string
		var toolCalls []toolCall

		for evt := range ch {
			switch evt.Type {
			case message.StreamEventToken:
				assistantText += evt.Token
				conn.WriteJSON(outgoingEvent{
					Type:    "token",
					Content: evt.Token,
				})
			case message.StreamEventThinking:
				conn.WriteJSON(outgoingEvent{
					Type:    "thinking",
					Content: evt.Token,
				})
			case message.StreamEventToolStart:
				conn.WriteJSON(outgoingEvent{
					Type:     "tool_start",
					Content:  evt.Token,
					ToolName: evt.ToolName,
					ToolID:   evt.ToolID,
					Display:  string(marshalDisplay(tool.ToolDisplay{Title: evt.ToolName})),
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
				logger.L().Error("stream event error", "error", evt.Error)
				conn.WriteJSON(outgoingEvent{
					Type:    "error",
					Content: evt.Error,
				})
				return
			}
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
		*history = append(*history, assistantMsg)

		if len(toolCalls) == 0 {
			conn.WriteJSON(outgoingEvent{
				Type: "done",
			})
			return
		}

		for _, tc := range toolCalls {
			t, ok := tool.Get(tc.Name)
			if !ok {
				output := fmt.Sprintf("error: unknown tool %q", tc.Name)
				completeToolCall(conn, tc, output, tool.ToolDisplay{Title: tc.Name, Body: []string{output}}, history)
				continue
			}

			if se, ok := t.(tool.StreamingExecutor); ok {
				pending := ""
				const flushAt = 100

				sendPending := func() {
					if pending == "" {
						return
					}
					conn.WriteJSON(outgoingEvent{
						Type:    "tool_output",
						Content: pending,
						ToolID:  tc.ID,
						Display: "",
					})
					pending = ""
				}

				finalJSON, err := se.StreamingExecute(
					context.Background(), json.RawMessage(tc.Args),
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

				completeToolCall(conn, tc, finalJSON, t.Display(tc.Args, finalJSON), history)
				continue
			}

			output, err := t.Execute(context.Background(), json.RawMessage(tc.Args))
			if err != nil {
				output = fmt.Sprintf("error: %v\n%s", err, output)
			}

			completeToolCall(conn, tc, output, t.Display(tc.Args, output), history)
		}
	}
}

func completeToolCall(conn *websocket.Conn, tc toolCall, output string, disp tool.ToolDisplay, history *[]message.Message) {
	conn.WriteJSON(outgoingEvent{
		Type:    "tool_output",
		Content: output,
		ToolID:  tc.ID,
		Display: string(marshalDisplay(disp)),
	})
	conn.WriteJSON(outgoingEvent{
		Type:   "tool_end",
		ToolID: tc.ID,
	})
	*history = append(*history, message.Message{
		Role:       message.RoleTool,
		Content:    output,
		ToolCallID: tc.ID,
		Timestamp:  time.Now(),
	})
}

func marshalDisplay(disp tool.ToolDisplay) []byte {
	b, err := json.Marshal(disp)
	if err != nil {
		logger.L().Error("failed to marshal tool display", "error", err)
		return []byte("{}")
	}
	return b
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
