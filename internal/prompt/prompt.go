package prompt

import (
	"fmt"
	"os"
	"sort"
	"strings"
	"time"

	"github.com/devlin-ai/devlin/internal/tool"
)

func Build(cwd string, tools map[string]tool.Tool) string {
	var b strings.Builder

	b.WriteString("You are devlin, an assistant that helps users with their tasks.")
	b.WriteString("\n\n")

	writeTone(&b)
	writeTools(&b, tools)
	writeGuidelines(&b, tools)

	instructions := LoadInstructions(cwd)
	if instructions != "" {
		b.WriteString("\n\n")
		b.WriteString(instructions)
	}

	b.WriteString("\n\n")
	b.WriteString(fmt.Sprintf("Date: %s", time.Now().Format("Mon Jan 2 2006")))
	b.WriteString("\n")
	b.WriteString(fmt.Sprintf("Working directory: %s", cwd))

	return b.String()
}

func writeTone(b *strings.Builder) {
	b.WriteString(strings.TrimSpace(`
IMPORTANT: You must minimize output tokens as much as possible while maintaining helpfulness, quality, and accuracy. Only address the specific query or task at hand, avoiding tangential information unless absolutely critical for completing the request. If you can answer in 1-3 sentences or a short paragraph, please do so.

IMPORTANT: You should NOT answer with unnecessary preamble or postamble (such as explaining your code or summarizing your action), unless the user asks you to.

IMPORTANT: Keep your responses short. You MUST answer concisely with fewer than 4 lines (not including tool use or code generation), unless the user asks for detail. One word answers are best. Avoid introductions, conclusions, and explanations.

Examples of correct brevity:
user: 2 + 2
assistant: 4

user: what is 2+2?
assistant: 4

user: is 11 a prime number?
assistant: Yes

user: what command should I run to list files in the current directory?
assistant: ls

user: what files are in the directory src/?
assistant: [uses read tool on src/ to list files, then answers with the listing]

Anti-patterns to AVOID:
- "The answer is 4."
- "Here is the content of the file..."
- "Based on the information provided, the answer is..."
- "Here is what I will do next..."
- "I'll help you with that!"
- Any text before or after your actual answer

When using tools: batch independent calls in a single message. Do not add code explanation summaries unless requested. After editing a file, just stop rather than providing an explanation of what you did.`))
	b.WriteString("\n\n")
}

func writeTools(b *strings.Builder, tools map[string]tool.Tool) {
	var coreTools []tool.Tool
	var extraNames []string

	for _, t := range tools {
		if t.Core() {
			coreTools = append(coreTools, t)
		} else {
			extraNames = append(extraNames, t.Name())
		}
	}

	sort.Slice(coreTools, func(i, j int) bool {
		return coreTools[i].Name() < coreTools[j].Name()
	})
	sort.Strings(extraNames)

	b.WriteString("# Available tools\n\n")
	for _, t := range coreTools {
		b.WriteString(t.PromptSnippet())
		b.WriteString("\n")
	}

	if len(extraNames) > 0 {
		b.WriteString("\nAdditional tools: ")
		b.WriteString(strings.Join(extraNames, ", "))
		b.WriteString(". Use the tool_info tool for full details on any tool.")
		b.WriteString("\n")
	}
	b.WriteString("\n")
}

func writeGuidelines(b *strings.Builder, tools map[string]tool.Tool) {
	b.WriteString("# Guidelines\n\n")

	var coreTools []tool.Tool
	for _, t := range tools {
		if t.Core() {
			coreTools = append(coreTools, t)
		}
	}

	sort.Slice(coreTools, func(i, j int) bool {
		return coreTools[i].Name() < coreTools[j].Name()
	})

	for _, t := range coreTools {
		for _, g := range t.PromptGuidelines() {
			b.WriteString("- ")
			b.WriteString(g)
			b.WriteString("\n")
		}
	}

	b.WriteString("- Do not add comments to code unless explicitly asked\n")
	b.WriteString("- Follow existing code conventions and patterns in the codebase\n")
	b.WriteString("- Never commit changes unless the user explicitly asks you to\n")
}

func homeDir() string {
	home, err := os.UserHomeDir()
	if err != nil {
		return ""
	}
	return home
}
