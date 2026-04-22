package main

import (
	"encoding/json"
	"fmt"
	"net/url"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/devlin-ai/devlin/internal/config"
	"github.com/devlin-ai/devlin/internal/tool"
	"github.com/gorilla/websocket"
)

type wsMessage struct {
	Content string `json:"content"`
}

type wsEvent struct {
	Type     string `json:"type"`
	Content  string `json:"content"`
	ToolName string `json:"tool_name,omitempty"`
	ToolID   string `json:"tool_id,omitempty"`
	Display  string `json:"display,omitempty"`
}

type wsConnectedMsg struct{ conn *websocket.Conn }
type wsThinkingMsg struct{ text string }
type wsTokenMsg struct{ text string }
type wsDoneMsg struct{}

type wsToolStartMsg struct {
	display tool.ToolDisplay
}
type wsToolOutputMsg struct {
	content string
	display tool.ToolDisplay
}
type wsToolEndMsg struct{}

type wsErrorMsg struct{ text string }
type scrambleTickMsg struct{}

func dialGateway() tea.Cmd {
	cfg, err := config.Load()
	if err != nil {
		return func() tea.Msg { return wsErrorMsg{text: "config: " + err.Error()} }
	}

	host := fmt.Sprintf("127.0.0.1:%d", cfg.Gateway.Port)
	u := url.URL{Scheme: "ws", Host: host, Path: "/ws"}

	return func() tea.Msg {
		conn, _, err := websocket.DefaultDialer.Dial(u.String(), nil)
		if err != nil {
			return wsErrorMsg{text: "dial: " + err.Error()}
		}
		return wsConnectedMsg{conn: conn}
	}
}

func readNext(conn *websocket.Conn) tea.Cmd {
	return func() tea.Msg {
		_, raw, err := conn.ReadMessage()
		if err != nil {
			return wsErrorMsg{text: err.Error()}
		}

		var evt wsEvent
		if err := json.Unmarshal(raw, &evt); err != nil {
			return wsErrorMsg{text: err.Error()}
		}

		switch evt.Type {
		case "token":
			return wsTokenMsg{text: evt.Content}
		case "thinking":
			return wsThinkingMsg{text: evt.Content}
		case "done":
			return wsDoneMsg{}
		case "tool_start":
			var disp tool.ToolDisplay
			if evt.Display != "" {
				json.Unmarshal([]byte(evt.Display), &disp)
			} else {
				disp = tool.ToolDisplay{Title: evt.ToolName}
			}
			return wsToolStartMsg{display: disp}
		case "tool_output":
			var disp tool.ToolDisplay
			if evt.Display != "" {
				json.Unmarshal([]byte(evt.Display), &disp)
			}
			return wsToolOutputMsg{content: evt.Content, display: disp}
		case "tool_end":
			return wsToolEndMsg{}
		case "error":
			return wsErrorMsg{text: evt.Content}
		default:
			return wsErrorMsg{text: "unknown event: " + evt.Type}
		}
	}
}
