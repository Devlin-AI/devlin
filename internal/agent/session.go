package agent

import (
	"context"
	"fmt"
	"os"
	"sync"

	"github.com/devlin-ai/devlin/internal/branch"
	"github.com/devlin-ai/devlin/internal/llm"
	"github.com/devlin-ai/devlin/internal/logger"
	"github.com/devlin-ai/devlin/internal/message"
	"github.com/devlin-ai/devlin/internal/prompt"
	"github.com/devlin-ai/devlin/internal/protocol"
	"github.com/devlin-ai/devlin/internal/session"
	"github.com/devlin-ai/devlin/internal/store"
	"github.com/devlin-ai/devlin/internal/tool"
	"github.com/google/uuid"
)

type Event = protocol.OutboundMessage

type EventEmitter interface {
	SendEvent(Event)
	SendToolStart(toolCall)
}

type toolCall struct {
	ID   string
	Name string
	Args string
}

var defaultMaxDepth int

func SetDefaultMaxDepth(d int) {
	defaultMaxDepth = d
}

type Session struct {
	mu        sync.Mutex
	cancelMu  sync.Mutex
	historyMu sync.Mutex

	id           string
	channel      string
	mode         string
	provider     llm.Provider
	store        *store.Store
	model        string
	history      []message.Message
	systemPrompt string
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

	if err := session.Create(db, id, ch, mode); err != nil {
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

	if _, err := session.CreateMessage(db, id, "tool_defs", string(message.MarshalToolDefs(buildToolDefsWithTools(tool.All()))), nil, "", "", "", "", nil); err != nil {
		logger.L().Error("failed to persist tool_defs", "session_id", id, "error", err)
	}

	if _, err := session.CreateMessage(db, id, "system_prompt", sysPrompt, nil, "", "", "", "", nil); err != nil {
		logger.L().Error("failed to persist system_prompt", "session_id", id, "error", err)
	}

	return s, nil
}

func Load(provider llm.Provider, db *store.Store, sessionID string, model string, onEvent func(Event)) (*Session, error) {
	history, err := session.LoadFullHistory(db, sessionID)
	if err != nil {
		return nil, err
	}

	meta, err := branch.GetMeta(db, sessionID)
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

	sess, err := session.Get(db, sessionID)
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

func (s *Session) MaxDepth() int {
	return defaultMaxDepth
}

func (s *Session) Depth() int {
	return s.depth
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
