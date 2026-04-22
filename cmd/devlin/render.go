package main

import (
	"fmt"
	"regexp"
	"strings"

	"github.com/charmbracelet/x/ansi"
	"github.com/devlin-ai/devlin/internal/tool"
)

var ansiRe = regexp.MustCompile(`\x1b\[[0-9;]*[a-zA-Z]`)

func stripANSI(s string) string {
	return ansiRe.ReplaceAllString(s, "")
}

func processCarriageReturns(s string) string {
	s = strings.ReplaceAll(s, "\r\n", "\n")
	lines := strings.Split(s, "\n")
	for i, line := range lines {
		if idx := strings.LastIndex(line, "\r"); idx >= 0 {
			lines[i] = line[idx+1:]
		}
	}
	return strings.Join(lines, "\n")
}

func renderToolDisplay(d tool.ToolDisplay, bodyW int, prefixW int) string {
	var lines []string
	if d.Title != "" {
		lines = append(lines, dimStyle.Render(d.Title))
	}
	for _, entry := range d.Body {
		lines = append(lines, strings.Split(entry, "\n")...)
	}
	return renderLines(lines, toolBodyMaxLines, bodyW, prefixW)
}

func renderStreamingTool(title string, raw string, bodyW int, prefixW int) string {
	cleaned := processCarriageReturns(stripANSI(raw))
	lines := strings.Split(cleaned, "\n")

	for len(lines) > 0 && lines[len(lines)-1] == "" {
		lines = lines[:len(lines)-1]
	}

	if len(lines) == 0 {
		return ""
	}

	if title != "" {
		lines = append([]string{dimStyle.Render(title)}, lines...)
	}

	return renderLines(lines, toolBodyStreamingMaxLines, bodyW, prefixW)
}

func renderLines(lines []string, maxLines int, bodyW int, prefixW int) string {
	if len(lines) == 0 {
		return ""
	}

	if len(lines) > maxLines {
		trimmed := lines[len(lines)-maxLines:]
		trimmed[0] = dimStyle.Render(fmt.Sprintf("... (%d more lines)", len(lines)-maxLines)) + "\n" + trimmed[0]
		lines = trimmed
	}

	indent := strings.Repeat(" ", prefixW)
	result := strings.Join(lines, "\n")
	wrapped := ansi.Wrap(result, bodyW, " ")
	return strings.Join(strings.Split(wrapped, "\n"), "\n"+indent)
}
