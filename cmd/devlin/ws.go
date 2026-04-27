package main

import (
	"encoding/json"
	"fmt"
	"net/url"
	"strings"
	"time"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/devlin-ai/devlin/internal/channel"
	"github.com/devlin-ai/devlin/internal/config"
	"github.com/devlin-ai/devlin/internal/tool"
	"github.com/gorilla/websocket"
)

type wsConnectedMsg struct{ conn *websocket.Conn }
type wsThinkingMsg struct{ text string }
type wsTokenMsg struct{ text string }
type wsDoneMsg struct{}

type wsToolStartMsg struct {
	toolID   string
	toolName string
	display  tool.ToolDisplay
}
type wsToolOutputMsg struct {
	toolID  string
	content string
	display tool.ToolDisplay
}
type wsToolEndMsg struct {
	toolID   string
	toolName string
	display  tool.ToolDisplay
}

type wsCancelledMsg struct{}

type wsStatusMsg struct{ text string }
type cancelResetMsg struct{}
type reconnectTickMsg struct{}
type reconnectAttemptMsg struct{}

type wsErrorMsg struct{ text string }
type scrambleTickMsg struct{}

type wsBranchCreatedMsg struct {
	sessionID string
	messageID int64
}
type wsSessionCreatedMsg struct {
	sessionID string
}
type wsSessionSwitchedMsg struct {
	sessionID string
}
type wsBranchListMsg struct {
	branches []channel.BranchInfo
}
type wsSessionListMsg struct{}

func sendBranch(conn *websocket.Conn, messageID int64) tea.Cmd {
	return func() tea.Msg {
		err := conn.WriteJSON(channel.InboundMessage{Type: "branch", MessageID: messageID})
		if err != nil {
			return wsErrorMsg{text: err.Error()}
		}
		return nil
	}
}

func sendSwitchSession(conn *websocket.Conn, sessionID string) tea.Cmd {
	return func() tea.Msg {
		err := conn.WriteJSON(channel.InboundMessage{Type: "switch_session", SessionID: sessionID})
		if err != nil {
			return wsErrorMsg{text: err.Error()}
		}
		return nil
	}
}

func sendListBranches(conn *websocket.Conn) tea.Cmd {
	return func() tea.Msg {
		err := conn.WriteJSON(channel.InboundMessage{Type: "list_branches"})
		if err != nil {
			return wsErrorMsg{text: err.Error()}
		}
		return nil
	}
}

func sendListSessions(conn *websocket.Conn) tea.Cmd {
	return func() tea.Msg {
		err := conn.WriteJSON(channel.InboundMessage{Type: "list_sessions"})
		if err != nil {
			return wsErrorMsg{text: err.Error()}
		}
		return nil
	}
}

func sendCancel(conn *websocket.Conn) tea.Cmd {
	return func() tea.Msg {
		err := conn.WriteJSON(channel.InboundMessage{Type: "cancel"})
		if err != nil {
			return wsErrorMsg{text: err.Error()}
		}
		return nil
	}
}

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

func isConnectionError(errText string) bool {
	if websocket.IsCloseError(nil, websocket.CloseGoingAway, websocket.CloseNormalClosure, websocket.CloseAbnormalClosure) {
		return false
	}
	lower := strings.ToLower(errText)
	return strings.Contains(lower, "use of closed network connection") ||
		strings.Contains(lower, "websocket: close") ||
		strings.Contains(lower, "connection reset by peer") ||
		strings.Contains(lower, "broken pipe") ||
		strings.Contains(lower, "eof") ||
		strings.Contains(lower, "dial:")
}

func reconnectTick() tea.Cmd {
	return tea.Tick(reconnectInterval, func(time.Time) tea.Msg { return reconnectTickMsg{} })
}

func reconnectAttemptAfter(delay time.Duration) tea.Cmd {
	return tea.Tick(delay, func(time.Time) tea.Msg { return reconnectAttemptMsg{} })
}

func readNext(conn *websocket.Conn) tea.Cmd {
	return func() tea.Msg {
		_, raw, err := conn.ReadMessage()
		if err != nil {
			return wsErrorMsg{text: err.Error()}
		}

		var evt channel.OutboundMessage
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
		case "cancelled":
			return wsCancelledMsg{}
		case "tool_start":
			var disp tool.ToolDisplay
			if evt.Display != "" {
				json.Unmarshal([]byte(evt.Display), &disp)
			}
			return wsToolStartMsg{toolID: evt.ToolID, toolName: evt.ToolName, display: disp}
		case "tool_output":
			var disp tool.ToolDisplay
			if evt.Display != "" {
				json.Unmarshal([]byte(evt.Display), &disp)
			}
			return wsToolOutputMsg{toolID: evt.ToolID, content: evt.Content, display: disp}
		case "tool_end":
			var disp tool.ToolDisplay
			if evt.Display != "" {
				json.Unmarshal([]byte(evt.Display), &disp)
			}
			return wsToolEndMsg{toolID: evt.ToolID, toolName: evt.ToolName, display: disp}
		case "error":
			return wsErrorMsg{text: evt.Content}
		case "status":
			return wsStatusMsg{text: evt.Content}
		case "session_created":
			return wsSessionCreatedMsg{sessionID: evt.SessionID}
		case "branch_created":
			return wsBranchCreatedMsg{sessionID: evt.SessionID, messageID: evt.MessageID}
		case "session_switched":
			return wsSessionSwitchedMsg{sessionID: evt.SessionID}
		case "branch_list":
			return wsBranchListMsg{branches: evt.Branches}
		case "session_list":
			return wsSessionListMsg{}
		default:
			return wsErrorMsg{text: "unknown event: " + evt.Type}
		}
	}
}
