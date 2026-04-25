package main

import (
	"strings"
	"time"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
	"github.com/muesli/termenv"
)

const (
	textareaMaxHeight         = 5
	dividerHeight             = 1
	toolBodyMaxLines          = 5
	toolBodyStreamingMaxLines = 10

	userPrefix = "You: "
	aiPrefix   = "Devlin: "
	toolPrefix = "Tool: "
	prompt     = "┃ "

	scrambleLen      = 4
	scrambleInterval = 60 * time.Millisecond
	scrambleChars    = "!@#$%^&*()_+-=[]{}|;:',.<>?/~0123456789abcdefghijklmnopqrstuvwxyzABCDEFGHIJKLMNOPQRSTUVWXYZ"

	reconnectInterval     = 500 * time.Millisecond
	reconnectInitialDelay = 1 * time.Second
	reconnectMaxDelay     = 30 * time.Second
)

var (
	promptStyle  = lipgloss.NewStyle().Foreground(lipgloss.Color("63"))
	userStyle    = lipgloss.NewStyle().Foreground(lipgloss.Color("120"))
	aiStyle      = lipgloss.NewStyle().Foreground(lipgloss.Color("226"))
	toolStyle    = lipgloss.NewStyle().Foreground(lipgloss.Color("39"))
	errStyle     = lipgloss.NewStyle().Foreground(lipgloss.Color("196"))
	dimStyle     = lipgloss.NewStyle().Foreground(lipgloss.Color("240"))
	diffAddStyle = lipgloss.NewStyle().Background(lipgloss.Color("28")).Foreground(lipgloss.Color("15"))
	diffDelStyle = lipgloss.NewStyle().Background(lipgloss.Color("160")).Foreground(lipgloss.Color("15"))

	toolNameStyle = lipgloss.NewStyle().Foreground(lipgloss.Color("252")).Bold(true)
	toolBodyStyle = lipgloss.NewStyle().Foreground(lipgloss.Color("244"))
	toolCodeStyle = lipgloss.NewStyle().Background(lipgloss.Color("236")).Foreground(lipgloss.Color("252"))

	aiPrefixW = lipgloss.Width(aiStyle.Render(aiPrefix))
	mdStyle   = "dark"
)

func init() {
	if !termenv.HasDarkBackground() {
		mdStyle = "light"
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

func reconnectBackoff(attempt int) time.Duration {
	delay := reconnectInitialDelay * time.Duration(1<<uint(attempt))
	if delay > reconnectMaxDelay {
		delay = reconnectMaxDelay
	}
	return delay
}
