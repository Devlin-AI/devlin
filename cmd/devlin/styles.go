package main

import (
	"strings"
	"time"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
)

const (
	textareaMaxHeight = 5
	dividerHeight     = 1

	userPrefix = "You: "
	aiPrefix   = "Devlin: "
	toolPrefix = "Tool: "
	prompt     = "┃ "

	scrambleLen      = 4
	scrambleInterval = 60 * time.Millisecond
	scrambleChars    = "!@#$%^&*()_+-=[]{}|;:',.<>?/~0123456789abcdefghijklmnopqrstuvwxyzABCDEFGHIJKLMNOPQRSTUVWXYZ"
)

var (
	promptStyle = lipgloss.NewStyle().Foreground(lipgloss.Color("63"))
	userStyle   = lipgloss.NewStyle().Foreground(lipgloss.Color("120"))
	aiStyle     = lipgloss.NewStyle().Foreground(lipgloss.Color("226"))
	toolStyle   = lipgloss.NewStyle().Foreground(lipgloss.Color("39"))
	errStyle    = lipgloss.NewStyle().Foreground(lipgloss.Color("196"))
	dimStyle    = lipgloss.NewStyle().Foreground(lipgloss.Color("240"))
)

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
