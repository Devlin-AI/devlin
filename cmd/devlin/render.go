package main

import (
	"fmt"
	"regexp"
	"strings"

	"github.com/charmbracelet/glamour"
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

func renderToolDisplay(toolName string, d tool.ToolDisplay, bodyW int, prefixW int) string {
	var lines []string
	if h := buildToolHeader(toolName, d.Title); h != "" {
		lines = append(lines, h)
	}
	for _, block := range d.Body {
		lines = append(lines, renderBlock(block)...)
	}
	return renderLines(lines, toolBodyMaxLines, bodyW, prefixW)
}

func renderBlock(block tool.DisplayBlock) []string {
	switch block.Type {
	case tool.DisplayDiff:
		return renderDiffLines(block.Content)
	case tool.DisplayCode:
		raw := strings.Split(block.Content, "\n")
		lines := make([]string, len(raw))
		for i, l := range raw {
			lines[i] = toolCodeStyle.Render(l)
		}
		return lines
	default:
		raw := strings.Split(block.Content, "\n")
		lines := make([]string, len(raw))
		for i, l := range raw {
			lines[i] = toolBodyStyle.Render(l)
		}
		return lines
	}
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

	if len(lines) > maxLines {
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
	result := strings.Join(lines, "\n")
	wrapped := ansi.Wrap(result, bodyW, " ")
	return strings.Join(strings.Split(wrapped, "\n"), "\n"+indent)
}

func newMDRenderer(width int) *glamour.TermRenderer {
	r, err := glamour.NewTermRenderer(
		glamour.WithStandardStyle(mdStyle),
		glamour.WithWordWrap(width),
		glamour.WithStylesFromJSONBytes([]byte(mdStyleOverrides)),
	)
	if err != nil {
		return nil
	}
	return r
}

const mdStyleOverrides = `{
  "document": {
    "margin": 0,
    "block_prefix": "",
    "block_suffix": ""
  },
  "code_block": {
    "margin": 0
  },
  "code": {
    "prefix": " ",
    "suffix": " "
  }
}`
