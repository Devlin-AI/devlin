package main

import (
	"encoding/json"
	"sync"

	"github.com/devlin-ai/devlin/internal/agent"
	"github.com/devlin-ai/devlin/internal/protocol"
	"github.com/devlin-ai/devlin/internal/llm"
	"github.com/devlin-ai/devlin/internal/logger"
	"github.com/devlin-ai/devlin/internal/session"
	"github.com/devlin-ai/devlin/internal/store"
	"github.com/gorilla/websocket"
)

type connState struct {
	conn     *websocket.Conn
	writeMu  sync.Mutex
	sess     *agent.Session
	store    *store.Store
	provider llm.Provider
	model    string
	channel  string
}

func (cs *connState) send(msg protocol.OutboundMessage) {
	cs.writeMu.Lock()
	defer cs.writeMu.Unlock()
	cs.conn.WriteJSON(msg)
}

func (cs *connState) handleNew(msg protocol.InboundMessage) {
	sess, err := agent.New(cs.provider, cs.store, msg.Channel, msg.Mode, cs.model, cs.send)
	if err != nil {
		logger.Default().Error("failed to create session", "error", err)
		cs.send(protocol.OutboundMessage{Type: "error", Content: err.Error()})
		return
	}
	cs.sess = sess
	cs.channel = msg.Channel
	cs.send(protocol.OutboundMessage{
		Type:      "session_created",
		SessionID: sess.ID(),
		Mode:      sess.Mode(),
	})
}

func (cs *connState) handleContinue(msg protocol.InboundMessage) {
	lastID, err := session.GetLast(cs.store, msg.Channel, msg.Mode)
	if err != nil {
		logger.Default().Error("failed to get last session", "error", err)
		cs.send(protocol.OutboundMessage{Type: "error", Content: err.Error()})
		return
	}

	if lastID == "" {
		cs.handleNew(msg)
		return
	}

	sess, err := agent.Load(cs.provider, cs.store, lastID, cs.model, cs.send)
	if err != nil {
		logger.Default().Error("failed to load session", "error", err)
		cs.send(protocol.OutboundMessage{Type: "error", Content: err.Error()})
		return
	}
	cs.sess = sess
	cs.channel = msg.Channel
	cs.send(protocol.OutboundMessage{
		Type:      "session_continued",
		SessionID: sess.ID(),
		Mode:      sess.Mode(),
	})
}

func (cs *connState) handleCancel(msg protocol.InboundMessage) {
	logger.Default().Info("cancel requested")
	cs.sess.Cancel()
}

func (cs *connState) handleBranch(msg protocol.InboundMessage) {
	branch, err := cs.sess.Branch(msg.MessageID)
	if err != nil {
		logger.Default().Error("branch failed", "error", err)
		cs.send(protocol.OutboundMessage{Type: "error", Content: err.Error()})
		return
	}
	cs.sess = branch
	cs.sess.SetOnEvent(cs.send)
	cs.send(protocol.OutboundMessage{
		Type:      "branch_created",
		SessionID: branch.ID(),
		MessageID: msg.MessageID,
	})
}

func (cs *connState) handleSwitchSession(msg protocol.InboundMessage) {
	switched, err := cs.sess.SwitchTo(msg.SessionID)
	if err != nil {
		logger.Default().Error("switch session failed", "error", err)
		cs.send(protocol.OutboundMessage{Type: "error", Content: err.Error()})
		return
	}
	cs.sess = switched
	cs.sess.SetOnEvent(cs.send)
	cs.send(protocol.OutboundMessage{
		Type:      "session_switched",
		SessionID: switched.ID(),
	})
}

func (cs *connState) handleListSessions(msg protocol.InboundMessage) {
	ch := cs.channel
	if ch == "" {
		ch = msg.Channel
	}
	if ch == "" {
		cs.send(protocol.OutboundMessage{Type: "session_list"})
		return
	}
	sessionMetas, err := session.List(cs.store, ch)
	if err != nil {
		logger.Default().Error("list sessions failed", "error", err)
		cs.send(protocol.OutboundMessage{Type: "error", Content: err.Error()})
		return
	}
	infos := make([]protocol.SessionInfo, len(sessionMetas))
	for i, sm := range sessionMetas {
		infos[i] = protocol.SessionInfo{
			ID:        sm.ID,
			Channel:   sm.Channel,
			Mode:      sm.Mode,
			CreatedAt: sm.CreatedAt.Unix(),
			UpdatedAt: sm.UpdatedAt.Unix(),
		}
	}
	cs.send(protocol.OutboundMessage{
		Type:     "session_list",
		Sessions: infos,
	})
}

func (cs *connState) handleConnection() {
	for {
		_, raw, err := cs.conn.ReadMessage()
		if err != nil {
			return
		}

		var msg protocol.InboundMessage
		if err := json.Unmarshal(raw, &msg); err != nil {
			continue
		}

		switch msg.Type {
		case "new":
			cs.handleNew(msg)
			continue
		case "continue":
			cs.handleContinue(msg)
			continue
		case "list_sessions":
			cs.handleListSessions(msg)
			continue
		}

		if cs.sess == nil {
			continue
		}

		switch msg.Type {
		case "cancel":
			cs.handleCancel(msg)
		case "branch":
			cs.handleBranch(msg)
		case "switch_session":
			cs.handleSwitchSession(msg)
		case "session_state":
			cs.handleHistory(msg)
		default:
			go cs.sess.ProcessMessage(msg.Content)
		}
	}
}
