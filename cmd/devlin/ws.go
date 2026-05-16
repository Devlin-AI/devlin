package main

import (
	"encoding/json"
	"fmt"
	"net/url"
	"strings"
	"time"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/devlin-ai/devlin/internal/protocol"
	"github.com/devlin-ai/devlin/internal/config"
	"github.com/devlin-ai/devlin/internal/tool"
	"github.com/gorilla/websocket"
)

type wsConnectedMsg struct{ conn *websocket.Conn }
type sentMsg struct{}
type wsTokenMsg struct {
	text          string
	subagentDepth int
	subagentDesc  string
}
type wsThinkingMsg struct {
	text          string
	subagentDepth int
	subagentDesc  string
}
type wsDoneMsg struct {
	messageID int64
}

type wsToolStartMsg struct {
	toolID        string
	toolName      string
	display       tool.ToolDisplay
	subagentDepth int
	subagentDesc  string
}
type wsToolOutputMsg struct {
	toolID        string
	content       string
	display       tool.ToolDisplay
	subagentDepth int
	subagentDesc  string
}
type wsToolEndMsg struct {
	toolID        string
	toolName      string
	display       tool.ToolDisplay
	subagentDepth int
	subagentDesc  string
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
	mode      string
}
type wsSessionContinuedMsg struct {
	sessionID string
	mode      string
}

type wsSessionListMsg struct {
	sessions []protocol.SessionInfo
}
type wsSessionStateMsg struct {
	sessionID    string
	messages     []protocol.HistoryMessage
	branchPoints []protocol.BranchPoint
	parent       *protocol.BranchInfo
	children     []protocol.BranchInfo
	siblings     []protocol.BranchInfo
	siblingIdx   int
}

func sendNew(conn *websocket.Conn) tea.Cmd {
	return func() tea.Msg {
		err := conn.WriteJSON(protocol.InboundMessage{
			Type:    "new",
			Channel: "tui",
			Mode:    protocol.ModeCoding,
		})
		if err != nil {
			return wsErrorMsg{text: err.Error()}
		}
		return sentMsg{}
	}
}

func sendBranch(conn *websocket.Conn, sessionID string, messageID int64) tea.Cmd {
	return func() tea.Msg {
		err := conn.WriteJSON(protocol.InboundMessage{Type: "branch", SessionID: sessionID, MessageID: messageID})
		if err != nil {
			return wsErrorMsg{text: err.Error()}
		}
		return sentMsg{}
	}
}

func sendSessionState(conn *websocket.Conn, sessionID string) tea.Cmd {
	return func() tea.Msg {
		if err := conn.WriteJSON(protocol.InboundMessage{Type: "session_state", SessionID: sessionID}); err != nil {
			return wsErrorMsg{text: err.Error()}
		}
		return sentMsg{}
	}
}

func sendListSessions(conn *websocket.Conn) tea.Cmd {
	return func() tea.Msg {
		err := conn.WriteJSON(protocol.InboundMessage{Type: "list_sessions", Channel: "tui"})
		if err != nil {
			return wsErrorMsg{text: err.Error()}
		}
		return sentMsg{}
	}
}

func sendCancel(conn *websocket.Conn, sessionID string) tea.Cmd {
	return func() tea.Msg {
		err := conn.WriteJSON(protocol.InboundMessage{Type: "cancel", SessionID: sessionID})
		if err != nil {
			return wsErrorMsg{text: err.Error()}
		}
		return sentMsg{}
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

		var evt protocol.OutboundMessage
		if err := json.Unmarshal(raw, &evt); err != nil {
			return wsErrorMsg{text: err.Error()}
		}

		switch evt.Type {
		case "token":
			return wsTokenMsg{text: evt.Content, subagentDepth: evt.SubagentDepth, subagentDesc: evt.SubagentDesc}
		case "thinking":
			return wsThinkingMsg{text: evt.Content, subagentDepth: evt.SubagentDepth, subagentDesc: evt.SubagentDesc}
		case "done":
			return wsDoneMsg{messageID: evt.MessageID}
		case "cancelled":
			return wsCancelledMsg{}
		case "tool_start":
			var disp tool.ToolDisplay
			if evt.Display != "" {
				json.Unmarshal([]byte(evt.Display), &disp)
			}
			return wsToolStartMsg{toolID: evt.ToolID, toolName: evt.ToolName, display: disp, subagentDepth: evt.SubagentDepth, subagentDesc: evt.SubagentDesc}
		case "tool_output":
			var disp tool.ToolDisplay
			if evt.Display != "" {
				json.Unmarshal([]byte(evt.Display), &disp)
			}
			return wsToolOutputMsg{toolID: evt.ToolID, content: evt.Content, display: disp, subagentDepth: evt.SubagentDepth, subagentDesc: evt.SubagentDesc}
		case "tool_end":
			var disp tool.ToolDisplay
			if evt.Display != "" {
				json.Unmarshal([]byte(evt.Display), &disp)
			}
			return wsToolEndMsg{toolID: evt.ToolID, toolName: evt.ToolName, display: disp, subagentDepth: evt.SubagentDepth, subagentDesc: evt.SubagentDesc}
		case "error":
			return wsErrorMsg{text: evt.Content}
		case "status":
			return wsStatusMsg{text: evt.Content}
		case "session_created":
			return wsSessionCreatedMsg{sessionID: evt.SessionID, mode: evt.Mode}
		case "session_continued":
			return wsSessionContinuedMsg{sessionID: evt.SessionID, mode: evt.Mode}
		case "branch_created":
			return wsBranchCreatedMsg{sessionID: evt.SessionID, messageID: evt.MessageID}
		case "session_list":
			return wsSessionListMsg{sessions: evt.Sessions}
		case "session_state":
			return wsSessionStateMsg{
				sessionID:    evt.SessionID,
				messages:     evt.Messages,
				branchPoints: evt.BranchPoints,
				parent:       evt.Parent,
				children:     evt.Branches,
				siblings:     evt.Siblings,
				siblingIdx:   evt.SiblingIdx,
			}
		default:
			return wsErrorMsg{text: "unknown event: " + evt.Type}
		}
	}
}
