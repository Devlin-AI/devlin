package main

import (
	"fmt"
	"strings"
	"time"

	"github.com/charmbracelet/bubbles/key"
	"github.com/charmbracelet/bubbles/textarea"
	"github.com/charmbracelet/bubbles/viewport"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/glamour"
	"github.com/charmbracelet/lipgloss"
	"github.com/charmbracelet/x/ansi"
	"github.com/devlin-ai/devlin/internal/tool"
	"github.com/gorilla/websocket"
)

type message struct {
	role       string
	toolID     string
	text       string
	thinking   string
	toolName   string
	display    tool.ToolDisplay
	rawContent string
	mdBody     string
}

type model struct {
	viewport         viewport.Model
	textarea         textarea.Model
	windowHeight     int
	messages         []message
	streaming        bool
	cancelPending    bool
	err              error
	conn             *websocket.Conn
	scrambleFrame    int
	mdRenderer       *glamour.TermRenderer
	mdWidth          int
	reconnecting     bool
	reconnectDots    int
	reconnectAttempt int
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
		if msg.String() == "esc" {
			if m.streaming {
				if !m.cancelPending {
					if m.textarea.Value() != "" {
						m.textarea.Reset()
						m.textarea.SetHeight(1)
						m.viewport.Height = m.windowHeight - m.textarea.Height() - dividerHeight
					}
					m.cancelPending = true
					m.textarea.Placeholder = "Press Esc again to cancel..."
					refreshView(&m)
					return m, tea.Tick(3*time.Second, func(t time.Time) tea.Msg {
						return cancelResetMsg{}
					})
				}
				m.cancelPending = false
				m.textarea.Placeholder = "Send a new message..."
				refreshView(&m)
				if m.conn != nil {
					return m, sendCancel(m.conn)
				}
			} else {
				if m.textarea.Value() != "" {
					m.textarea.Reset()
					m.textarea.SetHeight(1)
					m.viewport.Height = m.windowHeight - m.textarea.Height() - dividerHeight
					refreshView(&m)
				}
			}
			return m, nil
		}

		if m.cancelPending {
			m.cancelPending = false
			m.textarea.Placeholder = "Send a new message..."
		}

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

		newWidth := msg.Width - aiPrefixW
		if newWidth != m.mdWidth {
			m.mdWidth = newWidth
			m.mdRenderer = newMDRenderer(m.mdWidth)
			m.renderAllMarkdown(true)
		}

		refreshView(&m)
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
		if m.reconnecting {
			m.reconnecting = false
			m.reconnectDots = 0
			m.reconnectAttempt = 0
			m.textarea.Placeholder = "Send a new message..."
			m.textarea.Focus()
		}
		m.mdWidth = m.viewport.Width - aiPrefixW
		m.mdRenderer = newMDRenderer(m.mdWidth)
		return m, readNext(m.conn)

	case scrambleTickMsg:
		if !m.streaming {
			return m, nil
		}
		m.scrambleFrame++
		refreshView(&m)
		return m, scrambleTick()

	case wsThinkingMsg:
		if len(m.messages) > 0 && m.messages[len(m.messages)-1].role == "assistant" {
			m.messages[len(m.messages)-1].thinking += msg.text
		} else if len(m.messages) > 0 && m.messages[len(m.messages)-1].role == "tool" {
			m.messages = append(m.messages, message{role: "assistant", thinking: msg.text})
		}
		refreshView(&m)
		return m, readNext(m.conn)

	case wsTokenMsg:
		if len(m.messages) > 0 && m.messages[len(m.messages)-1].role == "assistant" {
			m.messages[len(m.messages)-1].text += msg.text
		} else if len(m.messages) > 0 && m.messages[len(m.messages)-1].role == "tool" {
			m.messages = append(m.messages, message{role: "assistant", text: msg.text})
		}
		refreshView(&m)
		return m, readNext(m.conn)

	case wsToolStartMsg:
		m.messages = append(m.messages, message{
			role:     "tool",
			toolID:   msg.toolID,
			toolName: msg.toolName,
			display:  msg.display,
		})
		refreshView(&m)
		return m, readNext(m.conn)
	case wsToolOutputMsg:
		if idx := m.findToolMsg(msg.toolID); idx >= 0 {
			m.messages[idx].rawContent += msg.content
			if msg.display.Title != "" || len(msg.display.Body) > 0 {
				m.messages[idx].display = msg.display
			}
		}
		refreshView(&m)
		return m, readNext(m.conn)
	case wsToolEndMsg:
		if idx := m.findToolMsg(msg.toolID); idx >= 0 {
			m.messages[idx].rawContent = ""
			if msg.toolName != "" {
				m.messages[idx].toolName = msg.toolName
			}
			if msg.display.Title != "" || len(msg.display.Body) > 0 {
				m.messages[idx].display = msg.display
			}
		}
		m.messages = append(m.messages, message{role: "assistant", text: ""})
		refreshView(&m)
		return m, readNext(m.conn)

	case wsCancelledMsg:
		m.streaming = false
		m.renderAllMarkdown(false)
		refreshView(&m)
		return m, readNext(m.conn)

	case wsDoneMsg:
		m.streaming = false
		m.renderAllMarkdown(false)
		refreshView(&m)
		return m, readNext(m.conn)

	case wsErrorMsg:
		if isConnectionError(msg.text) {
			m.conn = nil
			if !m.reconnecting {
				m.reconnecting = true
				m.reconnectDots = 0
				m.reconnectAttempt = 0
				m.streaming = false
				m.textarea.Blur()
				m.textarea.Placeholder = "Reconnecting."
				refreshView(&m)
				return m, tea.Batch(reconnectTick(), reconnectAttemptAfter(reconnectInitialDelay))
			}
			m.reconnectAttempt++
			delay := reconnectBackoff(m.reconnectAttempt)
			m.textarea.Placeholder = "Reconnecting."
			m.reconnectDots = 0
			refreshView(&m)
			return m, tea.Batch(reconnectTick(), reconnectAttemptAfter(delay))
		}
		m.streaming = false
		m.messages = append(m.messages, message{role: "error", text: msg.text})
		m.viewport.Height = m.windowHeight - m.textarea.Height() - dividerHeight
		refreshView(&m)
		return m, readNext(m.conn)

	case wsStatusMsg:
		if len(m.messages) > 0 && m.messages[len(m.messages)-1].role == "status" {
			m.messages[len(m.messages)-1].text = msg.text
		} else {
			m.messages = append(m.messages, message{role: "status", text: msg.text})
		}
		refreshView(&m)
		if m.conn != nil {
			return m, readNext(m.conn)
		}
		return m, nil

	case reconnectTickMsg:
		if !m.reconnecting {
			return m, nil
		}
		m.reconnectDots = (m.reconnectDots + 1) % 4
		m.textarea.Placeholder = "Reconnecting" + strings.Repeat(".", m.reconnectDots)
		refreshView(&m)
		return m, reconnectTick()

	case reconnectAttemptMsg:
		if !m.reconnecting {
			return m, nil
		}
		return m, dialGateway()

	case cancelResetMsg:
		m.cancelPending = false
		m.textarea.Placeholder = "Send a new message..."
		refreshView(&m)
		return m, nil
	}

	return m, tea.Batch(cmds...)
}

func (m model) View() string {
	divider := strings.Repeat("—", m.viewport.Width)
	input := promptStyle.Render(m.textarea.View())
	if m.reconnecting {
		input = dimStyle.Render(prompt + m.textarea.Placeholder)
	}
	return fmt.Sprintf("%s\n%s\n%s", m.viewport.View(), dimStyle.Render(divider), input)
}

func (m model) renderMessages() string {
	var s string
	w := m.viewport.Width

	for i, msg := range m.messages {
		if msg.role == "assistant" && msg.text == "" && msg.mdBody == "" && i != len(m.messages)-1 {
			continue
		}

		var prefix string
		if msg.role == "user" {
			prefix = userStyle.Render(userPrefix)
		} else if msg.role == "assistant" {
			prefix = aiStyle.Render(aiPrefix)
		} else if msg.role == "tool" {
			prefix = toolStyle.Render(toolPrefix)
		} else if msg.role == "error" {
			prefix = errStyle.Render("Error: ")
		} else if msg.role == "status" {
			prefix = dimStyle.Render("Status: ")
		}

		prefixW := lipgloss.Width(prefix)
		bodyW := w - prefixW

		var body string
		if msg.role == "assistant" && msg.mdBody != "" {
			body = strings.Join(strings.Split(msg.mdBody, "\n"), "\n"+strings.Repeat(" ", prefixW))
		} else if msg.role == "assistant" && msg.thinking != "" && msg.text == "" {
			wrapped := ansi.Wrap(msg.thinking, bodyW, " ")
			lines := strings.Split(wrapped, "\n")
			for j, line := range lines {
				lines[j] = dimStyle.Render(line)
			}
			body = strings.Join(lines, "\n"+strings.Repeat(" ", prefixW))
		} else if msg.role == "tool" {
			if msg.rawContent != "" {
				body = renderStreamingTool(msg.toolName, msg.display.Title, msg.rawContent, bodyW, prefixW)
			} else {
				body = renderToolDisplay(msg.toolName, msg.display, bodyW, prefixW)
			}
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

func (m model) findToolMsg(toolID string) int {
	for i := len(m.messages) - 1; i >= 0; i-- {
		if m.messages[i].role == "tool" && m.messages[i].toolID == toolID {
			return i
		}
	}
	return -1
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

func refreshView(m *model) {
	atBottom := m.viewport.AtBottom()
	m.viewport.SetContent(m.renderMessages())
	if atBottom {
		m.viewport.GotoBottom()
	}
}

func (m *model) renderAllMarkdown(force bool) {
	if m.mdRenderer == nil {
		return
	}
	for i := range m.messages {
		if m.messages[i].role != "assistant" || m.messages[i].text == "" {
			continue
		}
		if !force && m.messages[i].mdBody != "" {
			continue
		}
		rendered, err := m.mdRenderer.Render(m.messages[i].text)
		if err != nil {
			continue
		}
		m.messages[i].mdBody = strings.TrimRight(rendered, "\n")
	}
}
