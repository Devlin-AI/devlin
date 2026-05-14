package agent

import (
	"context"
	"fmt"
	"os"

	"github.com/devlin-ai/devlin/internal/branch"
	"github.com/devlin-ai/devlin/internal/logger"
	"github.com/devlin-ai/devlin/internal/message"
	"github.com/devlin-ai/devlin/internal/process"
	"github.com/devlin-ai/devlin/internal/prompt"
	"github.com/devlin-ai/devlin/internal/session"
	"github.com/google/uuid"
)

type SubagentEmitter struct {
	parent  func(Event)
	depth   int
	desc    string
	toolMap map[string]bool
}

func NewSubagentEmitter(parent func(Event), depth int, desc string) *SubagentEmitter {
	return &SubagentEmitter{
		parent:  parent,
		depth:   depth,
		desc:    desc,
		toolMap: make(map[string]bool),
	}
}

func (e *SubagentEmitter) SendEvent(evt Event) {
	evt.SubagentDepth = e.depth
	evt.SubagentDesc = e.desc
	e.parent(evt)
}

func (e *SubagentEmitter) SendToolStart(tc toolCall) {
	e.toolMap[tc.ID] = true
	e.SendEvent(Event{
		Type:     "tool_start",
		ToolID:   tc.ID,
		ToolName: tc.Name,
	})
}

func (s *Session) SpawnSubagent(ctx context.Context, description, taskPrompt string) (string, error) {
	child, err := s.createChildSession(ctx, description, taskPrompt)
	if err != nil {
		return "", err
	}

	child.ProcessMessage(taskPrompt)

	lastResponse := child.getLastAssistantResponse()
	if lastResponse == "" {
		return "", fmt.Errorf("subagent produced no output")
	}
	return lastResponse, nil
}

func (s *Session) SpawnSubagentAsync(ctx context.Context, description, taskPrompt string) (string, error) {
	child, err := s.createChildSession(ctx, description, taskPrompt)
	if err != nil {
		return "", err
	}

	onComplete := func(ps *process.ProcessSession) {
		ps.Result = child.getLastAssistantResponse()
	}

	runFunc := func(ctx context.Context) (string, error) {
		child.parentCtx = ctx
		child.ProcessMessage(taskPrompt)
		return child.getLastAssistantResponse(), nil
	}

	_, err = process.DefaultRegistry.SpawnAgent(description, taskPrompt, runFunc, process.WithOnComplete(onComplete))
	if err != nil {
		return "", err
	}

	return child.id, nil
}

func (s *Session) createChildSession(ctx context.Context, description, taskPrompt string) (*Session, error) {
	if defaultMaxDepth > 0 && s.depth >= defaultMaxDepth {
		return nil, fmt.Errorf("maximum subagent depth (%d) reached", defaultMaxDepth)
	}

	childID := uuid.New().String()

	if err := session.Create(s.store, childID, s.channel, s.mode); err != nil {
		return nil, fmt.Errorf("create subagent session: %w", err)
	}

	if err := branch.Create(s.store, childID, s.id, 0); err != nil {
		return nil, fmt.Errorf("create subagent branch: %w", err)
	}

	subTools := buildSubagentTools(s.depth + 1)
	cwd, _ := os.Getwd()
	subPrompt := prompt.Build(cwd, subTools)

	subEmitter := NewSubagentEmitter(s.onEvent, s.depth+1, description)

	child := &Session{
		id:           childID,
		channel:      s.channel,
		mode:         s.mode,
		provider:     s.provider,
		store:        s.store,
		model:        s.model,
		systemPrompt: subPrompt,
		onEvent:      subEmitter.SendEvent,
		parentID:     s.id,
		depth:        s.depth + 1,
		parentCtx:    ctx,
	}
	child.emitter = subEmitter

	if _, err := session.CreateMessage(s.store, childID, "tool_defs", string(message.MarshalToolDefs(buildToolDefsWithTools(subTools))), nil, "", "", "", "", nil); err != nil {
		logger.Default().Error("failed to persist subagent tool_defs", "session_id", childID, "error", err)
	}

	if _, err := session.CreateMessage(s.store, childID, "system_prompt", subPrompt, nil, "", "", "", "", nil); err != nil {
		logger.Default().Error("failed to persist subagent system_prompt", "session_id", childID, "error", err)
	}

	return child, nil
}

func (s *Session) getLastAssistantResponse() string {
	s.historyMu.Lock()
	defer s.historyMu.Unlock()
	for i := len(s.history) - 1; i >= 0; i-- {
		if s.history[i].Role == message.RoleAssistant {
			return s.history[i].Content
		}
	}
	return ""
}
