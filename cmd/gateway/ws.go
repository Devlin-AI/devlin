package main

import (
	"encoding/json"
	"net/http"

	"github.com/devlin-ai/devlin/internal/channel"
	"github.com/gorilla/websocket"
)

var upgrader = websocket.Upgrader{
	CheckOrigin: func(r *http.Request) bool {
		return true
	},
}

type incomingMessage struct {
	Type      string `json:"type"`
	Content   string `json:"content"`
	MessageID int64  `json:"message_id"`
	SessionID string `json:"session_id"`
}

type branchListItem struct {
	SessionID   string `json:"session_id"`
	ParentMsgID int64  `json:"parent_msg_id"`
}

type outgoingEvent struct {
	Type      string           `json:"type"`
	Content   string           `json:"content"`
	ToolName  string           `json:"tool_name,omitempty"`
	ToolID    string           `json:"tool_id,omitempty"`
	Display   string           `json:"display,omitempty"`
	SessionID string           `json:"session_id,omitempty"`
	MessageID int64            `json:"message_id,omitempty"`
	Branches  []branchListItem `json:"branches,omitempty"`
}

type wsAdapter struct {
	conn      *websocket.Conn
	sessionID string
}

func (a *wsAdapter) Name() string {
	return "tui"
}

func (a *wsAdapter) Receive() (<-chan channel.InboundMessage, error) {
	ch := make(chan channel.InboundMessage, 32)
	go func() {
		defer close(ch)
		for {
			_, raw, err := a.conn.ReadMessage()
			if err != nil {
				return
			}
			var msg incomingMessage
			if err := json.Unmarshal(raw, &msg); err != nil {
				continue
			}
			ch <- channel.InboundMessage{
				Type:      msg.Type,
				Content:   msg.Content,
				MessageID: msg.MessageID,
				SessionID: msg.SessionID,
			}
		}
	}()
	return ch, nil
}

func (a *wsAdapter) Send(msg channel.OutboundMessage) error {
	evt := outgoingEvent{
		Type:      msg.Type,
		Content:   msg.Content,
		SessionID: msg.SessionID,
		MessageID: msg.MessageID,
	}
	for _, b := range msg.Branches {
		evt.Branches = append(evt.Branches, branchListItem{
			SessionID:   b.SessionID,
			ParentMsgID: b.ParentMsgID,
		})
	}
	return a.conn.WriteJSON(evt)
}

func (a *wsAdapter) SessionID() string {
	return a.sessionID
}

func (a *wsAdapter) SetSessionID(id string) {
	a.sessionID = id
}
