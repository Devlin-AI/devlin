package agent

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	"github.com/devlin-ai/devlin/internal/logger"
	"github.com/devlin-ai/devlin/internal/message"
	"github.com/devlin-ai/devlin/internal/session"
	"github.com/devlin-ai/devlin/internal/tool"
)

type toolGroup struct {
	calls []toolCall
	safe  bool
}

func (s *Session) partitionToolCalls(calls []toolCall) []toolGroup {
	if len(calls) == 0 {
		return nil
	}
	var groups []toolGroup
	safe := isToolSafe(calls[0])
	current := toolGroup{calls: []toolCall{calls[0]}, safe: safe}
	for _, tc := range calls[1:] {
		ts := isToolSafe(tc)
		if ts == current.safe {
			current.calls = append(current.calls, tc)
		} else {
			groups = append(groups, current)
			current = toolGroup{calls: []toolCall{tc}, safe: ts}
		}
	}
	groups = append(groups, current)
	return groups
}

func isToolSafe(tc toolCall) bool {
	t, ok := tool.Get(tc.Name)
	if !ok {
		return false
	}
	cs, ok := t.(tool.ConcurrencySafe)
	if !ok {
		return false
	}
	return cs.ConcurrencySafe()
}

func buildSubagentTools(depth int) map[string]tool.Tool {
	all := tool.All()
	filtered := make(map[string]tool.Tool, len(all))
	for name, t := range all {
		if name == "task" && defaultMaxDepth > 0 && depth >= defaultMaxDepth {
			continue
		}
		filtered[name] = t
	}
	return filtered
}

func buildToolDefsWithTools(tools map[string]tool.Tool) []message.ToolDef {
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

func (s *Session) executeTool(ctx context.Context, tc toolCall) {
	t, ok := tool.Get(tc.Name)
	if !ok {
		output := fmt.Sprintf("error: unknown tool %q", tc.Name)
		s.completeToolCall(tc, output, tool.ToolDisplay{Body: []tool.DisplayBlock{{Type: tool.DisplayText, Content: output}}})
		return
	}

	if se, ok := t.(tool.StreamingExecutor); ok {
		pending := ""
		const flushAt = 100

		sendPending := func() {
			if pending == "" {
				return
			}
			s.emitter.SendEvent(Event{
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
		return
	}

	output, err := t.Execute(ctx, json.RawMessage(tc.Args))
	if err != nil {
		output = fmt.Sprintf("error: %v\n%s", err, output)
	}

	s.completeToolCall(tc, output, t.Display(tc.Args, output))
}

func (s *Session) completeToolCall(tc toolCall, output string, disp tool.ToolDisplay) {
	s.emitter.SendEvent(Event{
		Type:    "tool_output",
		Content: output,
		ToolID:  tc.ID,
	})
	s.emitter.SendEvent(Event{
		Type:     "tool_end",
		ToolID:   tc.ID,
		ToolName: tc.Name,
		Display:  string(marshalToolCallDisplay(disp)),
	})

	toolMsg := message.Message{
		Role:       message.RoleTool,
		Content:    output,
		ToolCallID: tc.ID,
		Timestamp:  time.Now(),
	}
	s.historyMu.Lock()
	s.history = append(s.history, toolMsg)
	s.historyMu.Unlock()
	if _, err := session.CreateMessage(
		s.store,
		s.id,
		string(message.RoleTool),
		output,
		nil,
		tc.ID,
		tc.Name,
		"", "", nil,
	); err != nil {
		logger.L().Error("failed to persist tool message", "session_id", s.id, "tool", tc.Name, "error", err)
	}
}
