package main

import (
	"encoding/json"
	"sync"

	"github.com/devlin-ai/devlin/internal/channel"
	"github.com/devlin-ai/devlin/internal/llm"
	"github.com/devlin-ai/devlin/internal/logger"
	"github.com/devlin-ai/devlin/internal/session"
	"github.com/gorilla/websocket"
)

type connState struct {
	conn     *websocket.Conn
	writeMu  sync.Mutex
	sess     *session.Session
	store    *session.Store
	provider llm.Provider
	model    string
	channel  string
}

func (cs *connState) send(msg channel.OutboundMessage) {
	cs.writeMu.Lock()
	defer cs.writeMu.Unlock()
	cs.conn.WriteJSON(msg)
}

func (cs *connState) handleNew(msg channel.InboundMessage) {
	sess, err := session.New(cs.provider, cs.store, msg.Channel, msg.Mode, cs.model, cs.send)
	if err != nil {
		logger.L().Error("failed to create session", "error", err)
		cs.send(channel.OutboundMessage{Type: "error", Content: err.Error()})
		return
	}
	cs.sess = sess
	cs.channel = msg.Channel
	cs.send(channel.OutboundMessage{
		Type:      "session_created",
		SessionID: sess.ID(),
		Mode:      sess.Mode(),
	})
}

func (cs *connState) handleContinue(msg channel.InboundMessage) {
	lastID, err := cs.store.GetLastSession(msg.Channel, msg.Mode)
	if err != nil {
		logger.L().Error("failed to get last session", "error", err)
		cs.send(channel.OutboundMessage{Type: "error", Content: err.Error()})
		return
	}

	if lastID == "" {
		cs.handleNew(msg)
		return
	}

	sess, err := session.Load(cs.provider, cs.store, lastID, cs.model, cs.send)
	if err != nil {
		logger.L().Error("failed to load session", "error", err)
		cs.send(channel.OutboundMessage{Type: "error", Content: err.Error()})
		return
	}
	cs.sess = sess
	cs.channel = msg.Channel
	cs.send(channel.OutboundMessage{
		Type:      "session_continued",
		SessionID: sess.ID(),
		Mode:      sess.Mode(),
	})
}

func (cs *connState) handleCancel(msg channel.InboundMessage) {
	if !cs.requireSession() {
		return
	}
	logger.L().Info("cancel requested")
	cs.sess.Cancel()
}

func (cs *connState) handleBranch(msg channel.InboundMessage) {
	if !cs.requireSession() {
		return
	}
	branch, err := cs.sess.Branch(msg.MessageID)
	if err != nil {
		logger.L().Error("branch failed", "error", err)
		cs.send(channel.OutboundMessage{Type: "error", Content: err.Error()})
		return
	}
	cs.sess = branch
	cs.sess.SetOnEvent(cs.send)
	cs.send(channel.OutboundMessage{
		Type:      "branch_created",
		SessionID: branch.ID(),
		MessageID: msg.MessageID,
	})
}

func (cs *connState) handleSwitchSession(msg channel.InboundMessage) {
	if !cs.requireSession() {
		return
	}
	switched, err := cs.sess.SwitchTo(msg.SessionID)
	if err != nil {
		logger.L().Error("switch session failed", "error", err)
		cs.send(channel.OutboundMessage{Type: "error", Content: err.Error()})
		return
	}
	cs.sess = switched
	cs.sess.SetOnEvent(cs.send)
	cs.send(channel.OutboundMessage{
		Type:      "session_switched",
		SessionID: switched.ID(),
	})
}

func (cs *connState) handleListSessions(msg channel.InboundMessage) {
	ch := cs.channel
	if ch == "" {
		ch = msg.Channel
	}
	if ch == "" {
		cs.send(channel.OutboundMessage{Type: "session_list"})
		return
	}
	sessionMetas, err := cs.store.ListSessions(ch)
	if err != nil {
		logger.L().Error("list sessions failed", "error", err)
		cs.send(channel.OutboundMessage{Type: "error", Content: err.Error()})
		return
	}
	infos := make([]channel.SessionInfo, len(sessionMetas))
	for i, sm := range sessionMetas {
		infos[i] = channel.SessionInfo{
			ID:        sm.ID,
			Channel:   sm.Channel,
			Mode:      sm.Mode,
			CreatedAt: sm.CreatedAt,
			UpdatedAt: sm.UpdatedAt,
		}
	}
	cs.send(channel.OutboundMessage{
		Type:     "session_list",
		Sessions: infos,
	})
}

func (cs *connState) branchInfos(metas []session.BranchMeta) []channel.BranchInfo {
	infos := make([]channel.BranchInfo, len(metas))
	for i, m := range metas {
		firstMsg, err := cs.store.GetFirstUserMessage(m.SessionID)
		if err != nil {
			logger.L().Error("get first user message failed", "session_id", m.SessionID, "error", err)
		}
		infos[i] = channel.BranchInfo{
			SessionID:    m.SessionID,
			ParentMsgID:  m.ParentMsgID,
			FirstMessage: firstMsg,
		}
	}
	return infos
}

func (cs *connState) loadSiblingInfo(sessionID string) (*channel.BranchInfo, []channel.BranchInfo, int) {
	currentMeta, err := cs.store.LoadBranchMeta(sessionID)
	if err != nil {
		logger.L().Error("load branch meta failed", "session_id", sessionID, "error", err)
		return nil, nil, 0
	}
	if currentMeta == nil || currentMeta.ParentID == "" {
		return nil, nil, 0
	}

	parent := &channel.BranchInfo{
		SessionID:   currentMeta.ParentID,
		ParentMsgID: currentMeta.ParentMsgID,
	}

	metas, err := cs.store.ListBranches(currentMeta.ParentID)
	if err != nil {
		logger.L().Error("list parent branches failed", "parent_id", currentMeta.ParentID, "error", err)
		return parent, nil, 0
	}

	siblings := cs.branchInfos(metas)
	idx := 0
	for i, s := range siblings {
		if s.SessionID == sessionID {
			idx = i
			break
		}
	}
	return parent, siblings, idx
}

func (cs *connState) handleHistory(msg channel.InboundMessage) {
	if !cs.requireSession() {
		return
	}
	targetID := msg.SessionID
	if targetID == "" {
		targetID = cs.sess.ID()
	}
	msgs, err := cs.store.LoadFullHistory(targetID)
	if err != nil {
		logger.L().Error("load history failed", "error", err)
		cs.send(channel.OutboundMessage{Type: "error", Content: err.Error()})
		return
	}

	toolCallArgs := make(map[string]string)
	for _, m := range msgs {
		if m.Role == "assistant" {
			for _, tc := range m.ToolCalls {
				toolCallArgs[tc.ID] = tc.Function.Arguments
			}
		}
	}

	histMsgs := make([]channel.HistoryMessage, 0, len(msgs))
	for _, m := range msgs {
		var toolCallsJSON string
		if len(m.ToolCalls) > 0 {
			if b, err := json.Marshal(m.ToolCalls); err == nil {
				toolCallsJSON = string(b)
			}
		}
		hm := channel.HistoryMessage{
			ID:        m.ID,
			Role:      string(m.Role),
			Content:   m.Content,
			ToolName:  m.ToolName,
			ToolCalls: toolCallsJSON,
		}
		if m.Role == "tool" {
			hm.ToolArgs = toolCallArgs[m.ToolCallID]
		}
		histMsgs = append(histMsgs, hm)
	}

	chain, err := cs.store.LoadBranchChain(targetID)
	if err != nil {
		logger.L().Error("load branch chain failed", "error", err)
		cs.send(channel.OutboundMessage{Type: "error", Content: err.Error()})
		return
	}
	points := make([]channel.BranchPoint, 0, len(chain))
	for _, c := range chain {
		points = append(points, channel.BranchPoint{
			MsgID:     c.ParentMsgID,
			SessionID: c.SessionID,
		})
	}

	parent, siblings, siblingIdx := cs.loadSiblingInfo(targetID)

	childMetas, err := cs.store.ListBranches(targetID)
	if err != nil {
		logger.L().Error("list child branches failed", "session_id", targetID, "error", err)
	}
	children := cs.branchInfos(childMetas)

	cs.send(channel.OutboundMessage{
		Type:         "session_state",
		SessionID:    targetID,
		Messages:     histMsgs,
		BranchPoints: points,
		Parent:       parent,
		Branches:     children,
		Siblings:     siblings,
		SiblingIdx:   siblingIdx,
	})
}

func (cs *connState) handleConnection() {
	for {
		_, raw, err := cs.conn.ReadMessage()
		if err != nil {
			return
		}

		var msg channel.InboundMessage
		if err := json.Unmarshal(raw, &msg); err != nil {
			continue
		}

		switch msg.Type {
		case "new":
			cs.handleNew(msg)
		case "continue":
			cs.handleContinue(msg)
		case "cancel":
			cs.handleCancel(msg)
		case "branch":
			cs.handleBranch(msg)
		case "switch_session":
			cs.handleSwitchSession(msg)
		case "session_state":
			cs.handleHistory(msg)
		case "list_sessions":
			cs.handleListSessions(msg)
		default:
			if !cs.requireSession() {
				continue
			}
			go cs.sess.ProcessMessage(msg.Content)
		}
	}
}

func (cs *connState) requireSession() bool {
	return cs.sess != nil
}
