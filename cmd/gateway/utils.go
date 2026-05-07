package main

import (
	"encoding/json"

	"github.com/devlin-ai/devlin/internal/protocol"
	"github.com/devlin-ai/devlin/internal/logger"
	"github.com/devlin-ai/devlin/internal/store"
)

func (cs *connState) branchInfos(metas []store.BranchMeta) []protocol.BranchInfo {
	infos := make([]protocol.BranchInfo, len(metas))
	for i, m := range metas {
		firstMsg, err := cs.store.GetFirstUserMessage(m.SessionID)
		if err != nil {
			logger.L().Error("get first user message failed", "session_id", m.SessionID, "error", err)
		}
		infos[i] = protocol.BranchInfo{
			SessionID:    m.SessionID,
			ParentMsgID:  m.ParentMsgID,
			FirstMessage: firstMsg,
		}
	}
	return infos
}

func (cs *connState) loadSiblingInfo(sessionID string) (*protocol.BranchInfo, []protocol.BranchInfo, int) {
	currentMeta, err := cs.store.LoadBranchMeta(sessionID)
	if err != nil {
		logger.L().Error("load branch meta failed", "session_id", sessionID, "error", err)
		return nil, nil, 0
	}
	if currentMeta == nil || currentMeta.ParentID == "" {
		return nil, nil, 0
	}

	parent := &protocol.BranchInfo{
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

func (cs *connState) handleHistory(msg protocol.InboundMessage) {
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
		cs.send(protocol.OutboundMessage{Type: "error", Content: err.Error()})
		return
	}

	toolCallArgs := make(map[string]string)
	for _, m := range msgs {
		if m.Role == "assistant" {
			var calls []store.ToolCall
			json.Unmarshal([]byte(m.ToolCalls), &calls)
			for _, tc := range calls {
				toolCallArgs[tc.ID] = tc.Function.Arguments
			}
		}
	}

	histMsgs := make([]protocol.HistoryMessage, 0, len(msgs))
	for _, m := range msgs {
		var toolCallsJSON string
		if m.ToolCalls != "" {
			var calls []store.ToolCall
			if err := json.Unmarshal([]byte(m.ToolCalls), &calls); err == nil {
				if b, err := json.Marshal(calls); err == nil {
					toolCallsJSON = string(b)
				}
			}
		}
		hm := protocol.HistoryMessage{
			ID:        m.ID,
			Role:      m.Role,
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
		cs.send(protocol.OutboundMessage{Type: "error", Content: err.Error()})
		return
	}
	points := make([]protocol.BranchPoint, 0, len(chain))
	for _, c := range chain {
		points = append(points, protocol.BranchPoint{
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

	cs.send(protocol.OutboundMessage{
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
