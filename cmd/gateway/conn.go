package main

import (
	"encoding/json"
	"sync"

	"github.com/devlin-ai/devlin/internal/agent"
	"github.com/devlin-ai/devlin/internal/protocol"
	"github.com/devlin-ai/devlin/internal/logger"
	"github.com/devlin-ai/devlin/internal/session"
	"github.com/gorilla/websocket"
)

type connState struct {
	conn    *websocket.Conn
	writeMu sync.Mutex
	gw      *gateway
}

func (cs *connState) send(msg protocol.OutboundMessage) {
	cs.writeMu.Lock()
	defer cs.writeMu.Unlock()
	cs.conn.WriteJSON(msg)
}

func (cs *connState) handleNew(msg protocol.InboundMessage) {
	sess, err := agent.New(cs.gw.provider, cs.gw.store, msg.Channel, msg.Mode, cs.gw.model)
	if err != nil {
		logger.Default().Error("failed to create session", "error", err)
		cs.send(protocol.OutboundMessage{Type: "error", Content: err.Error()})
		return
	}
	cs.gw.sessions.Store(sess.ID(), sess)
	cs.send(protocol.OutboundMessage{
		Type:      "session_created",
		SessionID: sess.ID(),
		Mode:      sess.Mode(),
	})
}

func (cs *connState) handleContinue(msg protocol.InboundMessage) {
	lastID, err := session.GetLast(cs.gw.store, msg.Channel, msg.Mode)
	if err != nil {
		logger.Default().Error("failed to get last session", "error", err)
		cs.send(protocol.OutboundMessage{Type: "error", Content: err.Error()})
		return
	}

	if lastID == "" {
		cs.handleNew(msg)
		return
	}

	sess, err := cs.gw.resolve(lastID)
	if err != nil {
		logger.Default().Error("failed to load session", "error", err)
		cs.send(protocol.OutboundMessage{Type: "error", Content: err.Error()})
		return
	}
	cs.send(protocol.OutboundMessage{
		Type:      "session_continued",
		SessionID: sess.ID(),
		Mode:      sess.Mode(),
	})
}

func (cs *connState) handleListSessions(msg protocol.InboundMessage) {
	if msg.Channel == "" {
		cs.send(protocol.OutboundMessage{Type: "session_list"})
		return
	}
	sessionMetas, err := session.List(cs.gw.store, msg.Channel)
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

		if msg.SessionID == "" {
			continue
		}
		sess, err := cs.gw.resolve(msg.SessionID)
		if err != nil {
			cs.send(protocol.OutboundMessage{Type: "error", Content: err.Error()})
			continue
		}

		switch msg.Type {
		case "cancel":
			sess.Cancel()
		case "branch":
			branchID, err := sess.Branch(msg.MessageID)
			if err != nil {
				logger.Default().Error("branch failed", "error", err)
				cs.send(protocol.OutboundMessage{Type: "error", Content: err.Error()})
				break
			}
			cs.send(protocol.OutboundMessage{
				Type:      "branch_created",
				SessionID: branchID,
				MessageID: msg.MessageID,
			})
		case "session_state":
			cs.handleHistory(msg)
		default:
			go sess.ProcessMessage(msg.Content, cs.send)
		}
	}
}
