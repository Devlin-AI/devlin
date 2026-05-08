package session

import (
	"context"
	"encoding/json"
	"fmt"
	"math/rand"
	"os"
	"sync"
	"time"

	"github.com/devlin-ai/devlin/internal/branch"
	"github.com/devlin-ai/devlin/internal/llm"
	"github.com/devlin-ai/devlin/internal/logger"
	"github.com/devlin-ai/devlin/internal/message"
	"github.com/devlin-ai/devlin/internal/process"
	"github.com/devlin-ai/devlin/internal/prompt"
	"github.com/devlin-ai/devlin/internal/store"
	"github.com/devlin-ai/devlin/internal/tool"
	"github.com/google/uuid"
)

// --- Session wrapper (service layer over store) ---

func Create(db *store.Store, id, channel, mode string) error {
	return db.CreateSession(id, channel, mode)
}

func Touch(db *store.Store, id string) error {
	return db.TouchSession(id)
}

func Exists(db *store.Store, id string) (bool, error) {
	return db.SessionExists(id)
}

func Get(db *store.Store, id string) (*SessionMeta, error) {
	s, err := db.GetSession(id)
	if err != nil {
		return nil, fmt.Errorf("get session: %w", err)
	}
	if s == nil {
		return nil, nil
	}
	meta := FromStoreMeta(*s)
	return &meta, nil
}

func GetLast(db *store.Store, channel, mode string) (string, error) {
	return db.GetLastSession(channel, mode)
}

func List(db *store.Store, channel string) ([]SessionMeta, error) {
	raw, err := db.ListSessions(channel)
	if err != nil {
		return nil, fmt.Errorf("list sessions: %w", err)
	}
	result := make([]SessionMeta, len(raw))
	for i, s := range raw {
		result[i] = FromStoreMeta(s)
	}
	return result, nil
}

func PersistMessage(db *store.Store, sessionID string, role string, content string, toolCallsJSON []byte, toolCallID string, toolName string, thinking string, model string, usageJSON []byte) (int64, error) {
	return db.PersistMessage(sessionID, role, content, toolCallsJSON, toolCallID, toolName, thinking, model, usageJSON)
}

func LoadMessagesForSession(db *store.Store, sessionID string) ([]message.Message, error) {
	raw, err := db.LoadMessagesForSession(sessionID)
	if err != nil {
		return nil, fmt.Errorf("load messages for session: %w", err)
	}
	result := make([]message.Message, len(raw))
	for i, m := range raw {
		result[i] = message.FromStore(m)
	}
	return result, nil
}

func LoadMessagesUpToID(db *store.Store, sessionID string, upToMsgID int64) ([]message.Message, error) {
	raw, err := db.LoadMessagesUpToID(sessionID, upToMsgID)
	if err != nil {
		return nil, fmt.Errorf("load messages up to id: %w", err)
	}
	result := make([]message.Message, len(raw))
	for i, m := range raw {
		result[i] = message.FromStore(m)
	}
	return result, nil
}

func GetFirstUserMessage(db *store.Store, sessionID string) (string, error) {
	return db.GetFirstUserMessage(sessionID)
}

func LoadFullHistory(db *store.Store, sessionID string) ([]message.Message, error) {
	msgs, err := LoadMessagesForSession(db, sessionID)
	if err != nil {
		return nil, fmt.Errorf("load messages for %s: %w", sessionID, err)
	}
	allMsgs := msgs

	err = branch.WalkUp(db, sessionID, func(meta branch.BranchMeta) error {
		parentMsgs, err := LoadMessagesUpToID(db, meta.ParentID, meta.ParentMsgID)
		if err != nil {
			return fmt.Errorf("load messages up to id for %s: %w", meta.ParentID, err)
		}
		allMsgs = append(parentMsgs, allMsgs...)
		return nil
	})
	if err != nil {
		return nil, err
	}
	return allMsgs, nil
}

// --- Session service ---

var defaultMaxDepth int

func SetDefaultMaxDepth(d int) {
	defaultMaxDepth = d
}

type Session struct {
	id           string
	channel      string
	mode         string
	provider     llm.Provider
	store        *store.Store
	model        string
	history      []message.Message
	systemPrompt string
	mu           sync.Mutex
	cancelMu     sync.Mutex
	historyMu    sync.Mutex
	onEvent      func(Event)
	cancel       context.CancelFunc
	parentID     string
	branchPoint  int64
	depth        int
	emitter      EventEmitter
	parentCtx    context.Context
}

func New(provider llm.Provider, db *store.Store, ch string, mode string, model string, onEvent func(Event)) (*Session, error) {
	id := uuid.New().String()

	if err := Create(db, id, ch, mode); err != nil {
		return nil, err
	}

	cwd, _ := os.Getwd()
	sysPrompt := prompt.Build(cwd, tool.All())

	s := &Session{
		id:           id,
		channel:      ch,
		mode:         mode,
		provider:     provider,
		store:        db,
		model:        model,
		systemPrompt: sysPrompt,
		onEvent:      onEvent,
	}
	s.emitter = s

	if _, err := PersistMessage(db, id, "tool_defs", string(message.MarshalToolDefs(buildToolDefs())), nil, "", "", "", "", nil); err != nil {
		logger.L().Error("failed to persist tool_defs", "session_id", id, "error", err)
	}

	if _, err := PersistMessage(db, id, "system_prompt", sysPrompt, nil, "", "", "", "", nil); err != nil {
		logger.L().Error("failed to persist system_prompt", "session_id", id, "error", err)
	}

	return s, nil
}

func Load(provider llm.Provider, db *store.Store, sessionID string, model string, onEvent func(Event)) (*Session, error) {
	history, err := LoadFullHistory(db, sessionID)
	if err != nil {
		return nil, err
	}

	meta, err := branch.LoadMeta(db, sessionID)
	if err != nil {
		return nil, err
	}

	var parentID string
	var branchPoint int64
	if meta != nil {
		parentID = meta.ParentID
		branchPoint = meta.ParentMsgID
	}

	depth, err := branch.ComputeDepth(db, sessionID)
	if err != nil {
		logger.L().Warn("failed to compute depth", "session_id", sessionID, "error", err)
		depth = 0
	}

	cwd, _ := os.Getwd()
	sysPrompt := prompt.Build(cwd, tool.All())

	s := &Session{
		id:           sessionID,
		provider:     provider,
		store:        db,
		model:        model,
		history:      history,
		systemPrompt: sysPrompt,
		onEvent:      onEvent,
		parentID:     parentID,
		branchPoint:  branchPoint,
		depth:        depth,
	}
	s.emitter = s

	sess, err := Get(db, sessionID)
	if err != nil {
		return nil, fmt.Errorf("load session meta: %w", err)
	}
	if sess == nil {
		return nil, fmt.Errorf("session not found: %s", sessionID)
	}
	s.channel = sess.Channel
	s.mode = sess.Mode

	return s, nil
}

func (s *Session) ID() string {
	return s.id
}

func (s *Session) Channel() string {
	return s.channel
}

func (s *Session) Mode() string {
	return s.mode
}

func (s *Session) SetOnEvent(fn func(Event)) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.onEvent = fn
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

func (s *Session) ParentID() string {
	return s.parentID
}

func (s *Session) BranchPoint() int64 {
	return s.branchPoint
}

func (s *Session) Branch(msgID int64) (*Session, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	branchID := uuid.New().String()

	if err := Create(s.store, branchID, s.channel, s.mode); err != nil {
		return nil, err
	}

	if err := branch.Create(s.store, branchID, s.id, msgID); err != nil {
		return nil, err
	}

	historyCopy, err := LoadMessagesUpToID(s.store, s.id, msgID)
	if err != nil {
		return nil, err
	}

	br := &Session{
		id:           branchID,
		channel:      s.channel,
		mode:         s.mode,
		provider:     s.provider,
		store:        s.store,
		model:        s.model,
		history:      historyCopy,
		systemPrompt: s.systemPrompt,
		onEvent:      s.onEvent,
		parentID:     s.id,
		branchPoint:  msgID,
	}

	return br, nil
}

func (s *Session) SwitchTo(sessionID string) (*Session, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	exists, err := Exists(s.store, sessionID)
	if err != nil {
		return nil, err
	}
	if !exists {
		return nil, fmt.Errorf("session %s not found", sessionID)
	}

	history, err := LoadFullHistory(s.store, sessionID)
	if err != nil {
		return nil, err
	}

	meta, err := branch.LoadMeta(s.store, sessionID)
	if err != nil {
		return nil, err
	}

	var parentID string
	var branchPoint int64
	if meta != nil {
		parentID = meta.ParentID
		branchPoint = meta.ParentMsgID
	}

	target := &Session{
		id:           sessionID,
		channel:      s.channel,
		mode:         s.mode,
		provider:     s.provider,
		store:        s.store,
		model:        s.model,
		history:      history,
		systemPrompt: s.systemPrompt,
		onEvent:      s.onEvent,
		parentID:     parentID,
		branchPoint:  branchPoint,
	}

	return target, nil
}

func (s *Session) ListBranches() ([]branch.BranchMeta, error) {
	return branch.ListChildren(s.store, s.id)
}

func (s *Session) GetParentBranch() (*branch.BranchMeta, error) {
	return branch.GetParent(s.store, s.id)
}

func (s *Session) MaxDepth() int {
	return defaultMaxDepth
}

func (s *Session) Depth() int {
	return s.depth
}

func (s *Session) SpawnSubagent(ctx context.Context, description, taskPrompt string) (string, error) {
	if defaultMaxDepth > 0 && s.depth >= defaultMaxDepth {
		return "", fmt.Errorf("maximum subagent depth (%d) reached", defaultMaxDepth)
	}

	childID := uuid.New().String()

	if err := Create(s.store, childID, s.channel, s.mode); err != nil {
		return "", fmt.Errorf("create subagent session: %w", err)
	}

	if err := branch.Create(s.store, childID, s.id, 0); err != nil {
		return "", fmt.Errorf("create subagent branch: %w", err)
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

	if _, err := PersistMessage(s.store, childID, "tool_defs", string(message.MarshalToolDefs(buildToolDefsWithTools(subTools))), nil, "", "", "", "", nil); err != nil {
		logger.L().Error("failed to persist subagent tool_defs", "session_id", childID, "error", err)
	}

	if _, err := PersistMessage(s.store, childID, "system_prompt", subPrompt, nil, "", "", "", "", nil); err != nil {
		logger.L().Error("failed to persist subagent system_prompt", "session_id", childID, "error", err)
	}

	child.ProcessMessage(taskPrompt)

	for i := len(child.history) - 1; i >= 0; i-- {
		if child.history[i].Role == message.RoleAssistant {
			return child.history[i].Content, nil
		}
	}

	return "", fmt.Errorf("subagent produced no output")
}

func (s *Session) SpawnSubagentAsync(ctx context.Context, description, taskPrompt string) (string, error) {
	if defaultMaxDepth > 0 && s.depth >= defaultMaxDepth {
		return "", fmt.Errorf("maximum subagent depth (%d) reached", defaultMaxDepth)
	}

	childID := uuid.New().String()

	if err := Create(s.store, childID, s.channel, s.mode); err != nil {
		return "", fmt.Errorf("create subagent session: %w", err)
	}

	if err := branch.Create(s.store, childID, s.id, 0); err != nil {
		return "", fmt.Errorf("create subagent branch: %w", err)
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
	}
	child.emitter = subEmitter

	if _, err := PersistMessage(s.store, childID, "tool_defs", string(message.MarshalToolDefs(buildToolDefsWithTools(subTools))), nil, "", "", "", "", nil); err != nil {
		logger.L().Error("failed to persist subagent tool_defs", "session_id", childID, "error", err)
	}

	if _, err := PersistMessage(s.store, childID, "system_prompt", subPrompt, nil, "", "", "", "", nil); err != nil {
		logger.L().Error("failed to persist subagent system_prompt", "session_id", childID, "error", err)
	}

	onComplete := func(ps *process.ProcessSession) {
		ps.Result = child.getLastAssistantResponse()
	}

	runFunc := func(ctx context.Context) (string, error) {
		child.parentCtx = ctx
		child.ProcessMessage(taskPrompt)
		return child.getLastAssistantResponse(), nil
	}

	_, err := process.DefaultRegistry.SpawnAgent(description, taskPrompt, runFunc, process.WithOnComplete(onComplete))
	if err != nil {
		return "", err
	}

	return childID, nil
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

func (s *Session) ProcessMessage(content string) {
	s.mu.Lock()
	defer s.mu.Unlock()

	s.history = append(s.history, message.Message{
		Role:      message.RoleUser,
		Content:   content,
		Timestamp: time.Now(),
	})
	if _, err := PersistMessage(s.store, s.id, string(message.RoleUser), content, nil, "", "", "", "", nil); err != nil {
		logger.L().Error("failed to persist user message", "session_id", s.id, "error", err)
	}

	s.processLoop()
}

func (s *Session) SendEvent(evt Event) {
	if s.onEvent != nil {
		s.onEvent(evt)
	}
}

func (s *Session) SendToolStart(tc toolCall) {
	t, ok := tool.Get(tc.Name)
	if !ok {
		return
	}
	disp := t.Display(tc.Args, "")
	disp.Body = nil
	s.emitter.SendEvent(Event{
		Type:     "tool_start",
		ToolName: tc.Name,
		ToolID:   tc.ID,
		Display:  string(marshalToolCallDisplay(disp)),
	})
}

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

func (s *Session) processLoop() {
	parentCtx := s.parentCtx
	if parentCtx == nil {
		parentCtx = context.Background()
	}
	ctx := tool.ContextWithSpawner(parentCtx, s)
	toolDefs := buildToolDefs()

	for {
		cwd, _ := os.Getwd()
		newPrompt := prompt.Build(cwd, tool.All())
		if newPrompt != s.systemPrompt {
			s.systemPrompt = newPrompt
			if _, err := PersistMessage(s.store, s.id, "system_prompt", newPrompt, nil, "", "", "", "", nil); err != nil {
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
		assistantMsgID, err := PersistMessage(
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
	if _, err := PersistMessage(
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
