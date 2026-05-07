package main

import (
	"fmt"
	"regexp"
	"strings"

	"github.com/charmbracelet/glamour"
	"github.com/charmbracelet/glamour/styles"
	"github.com/charmbracelet/lipgloss"
	xansi "github.com/charmbracelet/x/ansi"
	"github.com/devlin-ai/devlin/internal/protocol"
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

func buildToolHeader(toolName, title string) string {
	if toolName != "" {
		h := toolNameStyle.Render(toolName)
		if title != "" {
			h += " " + dimStyle.Render(title)
		}
		return h
	}
	if title != "" {
		return dimStyle.Render(title)
	}
	return ""
}

func renderToolDisplay(toolName string, d tool.ToolDisplay, bodyW int, prefixW int, maxLines int) string {
	var lines []string
	if h := buildToolHeader(toolName, d.Title); h != "" {
		lines = append(lines, h)
	}
	for _, block := range d.Body {
		lines = append(lines, renderBlock(block, bodyW)...)
	}
	return renderLines(lines, maxLines, bodyW, prefixW)
}

func renderBlock(block tool.DisplayBlock, bodyW int) []string {
	content := processCarriageReturns(block.Content)
	content = strings.TrimRight(content, "\n")
	if content == "" {
		return nil
	}
	raw := strings.Split(content, "\n")
	switch block.Type {
	case tool.DisplayDiff:
		return borderLines(renderDiffLines(strings.Join(raw, "\n")), bodyW)
	case tool.DisplayCode:
		lines := make([]string, len(raw))
		for i, l := range raw {
			lines[i] = toolCodeStyle.Render(l)
		}
		return borderLines(lines, bodyW)
	default:
		lines := make([]string, len(raw))
		for i, l := range raw {
			lines[i] = toolBodyStyle.Render(l)
		}
		return lines
	}
}

func borderLines(lines []string, bodyW int) []string {
	if len(lines) == 0 {
		return lines
	}
	top := blockBorderStyle.Render("┌" + strings.Repeat("─", bodyW-2) + "┐")
	bot := blockBorderStyle.Render("└" + strings.Repeat("─", bodyW-2) + "┘")
	bordered := make([]string, len(lines))
	for i, l := range lines {
		pad := bodyW - 2 - lipgloss.Width(l)
		if pad < 0 {
			pad = 0
		}
		bordered[i] = blockBorderStyle.Render("│") + l + strings.Repeat(" ", pad) + blockBorderStyle.Render("│")
	}
	return append(append([]string{top}, bordered...), bot)
}

func renderDiffLines(diff string) []string {
	rawLines := strings.Split(diff, "\n")
	var lines []string
	for _, line := range rawLines {
		if line == "" {
			continue
		}
		if strings.HasPrefix(line, "--- ") || strings.HasPrefix(line, "+++ ") {
			lines = append(lines, dimStyle.Render(line))
		} else if strings.HasPrefix(line, "+") {
			lines = append(lines, diffAddStyle.Render(line))
		} else if strings.HasPrefix(line, "-") {
			lines = append(lines, diffDelStyle.Render(line))
		} else {
			lines = append(lines, dimStyle.Render(line))
		}
	}
	return lines
}

func renderStreamingTool(toolName string, title string, raw string, bodyW int, prefixW int) string {
	cleaned := processCarriageReturns(stripANSI(raw))
	rawLines := strings.Split(cleaned, "\n")

	for len(rawLines) > 0 && rawLines[len(rawLines)-1] == "" {
		rawLines = rawLines[:len(rawLines)-1]
	}

	if len(rawLines) == 0 {
		return ""
	}

	lines := make([]string, len(rawLines))
	for i, l := range rawLines {
		lines[i] = toolBodyStyle.Render(l)
	}

	if h := buildToolHeader(toolName, title); h != "" {
		lines = append([]string{h}, lines...)
	}

	return renderLines(lines, toolBodyStreamingMaxLines, bodyW, prefixW)
}

func renderLines(lines []string, maxLines int, bodyW int, prefixW int) string {
	if len(lines) == 0 {
		return ""
	}

	if maxLines > 0 && len(lines) > maxLines {
		tailLines := maxLines - 2
		if tailLines < 1 {
			tailLines = 1
		}
		first := lines[0]
		hidden := len(lines) - 1 - tailLines
		tail := lines[len(lines)-tailLines:]
		lines = append([]string{first, dimStyle.Render(fmt.Sprintf("... (%d more lines)", hidden))}, tail...)
	}

	indent := strings.Repeat(" ", prefixW)
	wrapped := make([]string, len(lines))
	for i, l := range lines {
		wrapped[i] = xansi.Wrap(l, bodyW, " ")
	}
	return strings.Join(wrapped, "\n"+indent)
}

func newMDRenderer(width int) *glamour.TermRenderer {
	cfg := styles.DarkStyleConfig
	if mdStyle == "light" {
		cfg = styles.LightStyleConfig
	}
	cfg.Document.Margin = ptrUint(0)
	cfg.Document.BlockPrefix = ""
	cfg.Document.BlockSuffix = ""
	cfg.CodeBlock.Margin = ptrUint(0)
	cfg.CodeBlock.Chroma = nil
	cfg.Code.Prefix = " "
	cfg.Code.Suffix = " "

	r, err := glamour.NewTermRenderer(
		glamour.WithStyles(cfg),
		glamour.WithWordWrap(width),
	)
	if err != nil {
		return nil
	}
	return r
}

func ptrUint(v uint) *uint { return &v }

func renderBranchDivider(w int, shortID string) string {
	label := "branch " + shortID
	total := len(label) + 2
	if total >= w {
		return dimStyle.Render(label)
	}
	side := (w - total) / 2
	line := strings.Repeat("─", side)
	return dimStyle.Render(line + " " + label + " " + strings.Repeat("─", w-total-side))
}

func truncID(id string) string {
	if len(id) > 7 {
		return id[:7]
	}
	return id
}

func renderBranchTree(branches []protocol.BranchInfo, w int) string {
	var s string
	for i, b := range branches {
		connector := "├─ "
		if i == len(branches)-1 {
			connector = "└─ "
		}
		label := truncID(b.SessionID)
		if b.FirstMessage != "" {
			firstLine := b.FirstMessage
			if idx := strings.IndexByte(firstLine, '\n'); idx >= 0 {
				firstLine = firstLine[:idx]
			}
			truncLen := w - len(connector) - len(label) - 4
			if truncLen < 10 {
				truncLen = 10
			}
			if len(firstLine) > truncLen {
				firstLine = firstLine[:truncLen-3] + "..."
			}
			label += ": " + firstLine
		}
		s += dimStyle.Render(connector+label) + "\n"
	}
	return strings.TrimRight(s, "\n")
}
