package main

import (
	"encoding/json"
	"fmt"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/charmbracelet/bubbles/key"
	"github.com/charmbracelet/bubbles/textarea"
	"github.com/charmbracelet/bubbles/viewport"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
	"github.com/charmbracelet/x/ansi"
	"github.com/devlin-ai/devlin/internal/config"
	"github.com/devlin-ai/devlin/internal/logger"
	"github.com/gorilla/websocket"
)

var (
	promptStyle = lipgloss.NewStyle().Foreground(lipgloss.Color("63"))
	userStyle   = lipgloss.NewStyle().Foreground(lipgloss.Color("120"))
	aiStyle     = lipgloss.NewStyle().Foreground(lipgloss.Color("226"))
	errStyle    = lipgloss.NewStyle().Foreground(lipgloss.Color("196"))
	dimStyle    = lipgloss.NewStyle().Foreground(lipgloss.Color("240"))
)

const (
	textareaMaxHeight = 5
	dividerHeight     = 1

	userPrefix = "You: "
	aiPrefix   = "Devlin: "
	prompt     = "┃ "

	scrambleLen      = 4
	scrambleInterval = 60 * time.Millisecond
	scrambleChars    = "!@#$%^&*()_+-=[]{}|;:',.<>?/~0123456789abcdefghijklmnopqrstuvwxyzABCDEFGHIJKLMNOPQRSTUVWXYZ"
)

type message struct {
	role     string
	text     string
	thinking string
}

type wsMessage struct {
	Content string `json:"content"`
}

type wsEvent struct {
	Type    string `json:"type"`
	Content string `json:"content"`
}

type wsConnectedMsg struct{ conn *websocket.Conn }
type wsThinkingMsg struct{ text string }
type wsTokenMsg struct{ text string }
type wsDoneMsg struct{}
type wsErrorMsg struct{ text string }
type scrambleTickMsg struct{}

type model struct {
	viewport      viewport.Model
	textarea      textarea.Model
	windowHeight  int
	messages      []message
	streaming     bool
	err           error
	conn          *websocket.Conn
	scrambleFrame int
}

func initialModel() model {
	ta := textarea.New()
	ta.Placeholder = "Send a new message..."
	ta.Focus()
	// ta.CharLimit = 280
	ta.SetWidth(50)
	ta.SetHeight(1)
	ta.Prompt = prompt
	ta.ShowLineNumbers = false
	ta.FocusedStyle.CursorLine = lipgloss.NewStyle()
	ta.KeyMap.InsertNewline.SetEnabled(true)
	ta.KeyMap.InsertNewline.SetKeys("ctrl+j", "alt+enter", "shift+enter")
	vp := viewport.New(50, 10)

	return model{
		viewport: vp,
		textarea: ta,
		messages: []message{},
	}
}

func (m model) Init() tea.Cmd {
	return dialGateway()
}

func (m model) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	var cmds []tea.Cmd

	switch msg := msg.(type) {
	case tea.KeyMsg:
		switch msg.String() {
		case "ctrl+c":
			return m, tea.Quit
		case "enter":
			if !m.streaming && m.conn != nil && m.textarea.Value() != "" {
				m.messages = append(m.messages, message{role: "user", text: m.textarea.Value()})
				m.textarea.Reset()
				m.textarea.SetHeight(1)

				m.messages = append(m.messages, message{role: "assistant", text: ""})
				m.streaming = true

				m.viewport.Height = m.windowHeight - m.textarea.Height() - dividerHeight
				m.viewport.SetContent(m.renderMessages())
				m.viewport.GotoBottom()

				err := m.conn.WriteJSON(wsMessage{Content: m.messages[len(m.messages)-2].text})
				if err != nil {
					m.streaming = false
					m.messages[len(m.messages)-1] = message{role: "error", text: err.Error()}
					m.viewport.SetContent(m.renderMessages())
				}
				return m, scrambleTick()
			}
			return m, tea.Batch(cmds...)
		}

		if key.Matches(msg, m.textarea.KeyMap.InsertNewline) {
			m.textarea.SetHeight(min(visualLineCount(m.textarea)+1, textareaMaxHeight))
			m.viewport.Height = m.windowHeight - m.textarea.Height() - dividerHeight
		}

		var taCmd tea.Cmd
		m.textarea, taCmd = m.textarea.Update(msg)
		cmds = append(cmds, taCmd)

		desiredHeight := min(max(visualLineCount(m.textarea), 1), textareaMaxHeight)
		if desiredHeight != m.textarea.Height() {
			m.textarea.SetHeight(desiredHeight)
			m.viewport.Height = m.windowHeight - m.textarea.Height() - dividerHeight
		}
		return m, tea.Batch(cmds...)

	case tea.WindowSizeMsg:
		m.viewport.Width = msg.Width
		m.viewport.Height = msg.Height - m.textarea.Height() - dividerHeight

		m.textarea.SetWidth(msg.Width)
		m.windowHeight = msg.Height

		m.viewport.SetContent(m.renderMessages())
		if m.viewport.AtBottom() {
			m.viewport.GotoBottom()
		}
		return m, tea.Batch(cmds...)

	case tea.MouseMsg:
		textareaY := m.viewport.Height + dividerHeight

		if msg.Y < textareaY {
			var vpCmd tea.Cmd
			m.viewport, vpCmd = m.viewport.Update(msg)
			cmds = append(cmds, vpCmd)
		} else {
			var taCmd tea.Cmd
			if msg.Type == tea.MouseWheelUp {
				m.textarea, taCmd = m.textarea.Update(tea.KeyMsg{Type: tea.KeyUp})
			} else if msg.Type == tea.MouseWheelDown {
				m.textarea, taCmd = m.textarea.Update(tea.KeyMsg{Type: tea.KeyDown})
			}
			cmds = append(cmds, taCmd)
		}

		return m, tea.Batch(cmds...)

	case wsConnectedMsg:
		m.conn = msg.conn
		return m, readNext(m.conn)

	case scrambleTickMsg:
		if !m.streaming {
			return m, nil
		}
		m.scrambleFrame++
		atBottom := m.viewport.AtBottom()
		m.viewport.SetContent(m.renderMessages())
		if atBottom {
			m.viewport.GotoBottom()
		}
		return m, scrambleTick()

	case wsThinkingMsg:
		if len(m.messages) > 0 && m.messages[len(m.messages)-1].role == "assistant" {
			m.messages[len(m.messages)-1].thinking += msg.text
		}
		atBottom := m.viewport.AtBottom()
		m.viewport.SetContent(m.renderMessages())
		if atBottom {
			m.viewport.GotoBottom()
		}
		return m, readNext(m.conn)

	case wsTokenMsg:
		if len(m.messages) > 0 && m.messages[len(m.messages)-1].role == "assistant" {
			m.messages[len(m.messages)-1].text += msg.text
		}
		atBottom := m.viewport.AtBottom()
		m.viewport.SetContent(m.renderMessages())
		if atBottom {
			m.viewport.GotoBottom()
		}
		return m, readNext(m.conn)

	case wsDoneMsg:
		m.streaming = false
		atBottom := m.viewport.AtBottom()
		m.viewport.SetContent(m.renderMessages())
		if atBottom {
			m.viewport.GotoBottom()
		}
		return m, readNext(m.conn)

	case wsErrorMsg:
		m.streaming = false
		m.messages = append(m.messages, message{role: "error", text: msg.text})
		m.viewport.Height = m.windowHeight - m.textarea.Height() - dividerHeight
		atBottom := m.viewport.AtBottom()
		m.viewport.SetContent(m.renderMessages())
		if atBottom {
			m.viewport.GotoBottom()
		}
		return m, readNext(m.conn)
	}

	return m, tea.Batch(cmds...)
}

func visualLineCount(ta textarea.Model) int {
	width := ta.Width() - lipgloss.Width(ta.Prompt)
	if width <= 0 {
		return ta.LineCount()
	}

	total := 0
	for _, line := range strings.Split(ta.Value(), "\n") {
		wrapped := ansi.Wrap(line, width, " ")
		total += strings.Count(wrapped, "\n") + 1
	}

	return total
}

func (m model) renderMessages() string {
	var s string
	w := m.viewport.Width

	for i, msg := range m.messages {
		var prefix string
		if msg.role == "user" {
			prefix = userStyle.Render(userPrefix)
		} else if msg.role == "assistant" {
			prefix = aiStyle.Render(aiPrefix)
		} else if msg.role == "error" {
			prefix = errStyle.Render("Error: ")
		}

		prefixW := lipgloss.Width(prefix)
		bodyW := w - prefixW

		var body string
		if msg.role == "assistant" && msg.thinking != "" && msg.text == "" {
			wrapped := ansi.Wrap(msg.thinking, bodyW, " ")
			lines := strings.Split(wrapped, "\n")
			for j, line := range lines {
				lines[j] = dimStyle.Render(line)
			}
			body = strings.Join(lines, "\n"+strings.Repeat(" ", prefixW))
		} else {
			wrapped := ansi.Wrap(msg.text, bodyW, " ")
			body = strings.Join(strings.Split(wrapped, "\n"), "\n"+strings.Repeat(" ", prefixW))
		}

		if m.streaming && i == len(m.messages)-1 && msg.role == "assistant" {
			body += dimStyle.Render(scramble(m.scrambleFrame))
		}

		s += prefix + body

		if i < len(m.messages)-1 {
			s += "\n"
		}
	}
	return s
}

func (m model) View() string {
	divider := strings.Repeat("—", m.viewport.Width)
	return fmt.Sprintf("%s\n%s\n%s", m.viewport.View(), dimStyle.Render(divider), promptStyle.Render(m.textarea.View()))
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
		case "error":
			return wsErrorMsg{text: evt.Content}
		default:
			return wsErrorMsg{text: "unknown event: " + evt.Type}
		}
	}
}

func scramble(frame int) string {
	var b strings.Builder
	for i := 0; i < scrambleLen; i++ {
		b.WriteByte(scrambleChars[(frame*(i+1))%len(scrambleChars)])
	}
	return b.String()
}

func scrambleTick() tea.Cmd {
	return tea.Tick(scrambleInterval, func(time.Time) tea.Msg { return scrambleTickMsg{} })
}

func main() {
	home, _ := os.UserHomeDir()
	logPath := filepath.Join(home, ".devlin", "devlin.log")

	logFile, err := os.OpenFile(logPath, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0644)
	if err != nil {
		fmt.Fprintf(os.Stderr, "failed to open log file: %v\n", err)
		os.Exit(1)
	}
	defer logFile.Close()

	logger.Init(logger.WithOutput(logFile))

	p := tea.NewProgram(initialModel(), tea.WithAltScreen(), tea.WithMouseCellMotion())
	p.Run()
}
