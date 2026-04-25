package prompt

import (
	"os"
	"path/filepath"
	"strings"
)

func LoadInstructions(cwd string) string {
	var parts []string

	for dir := cwd; dir != ""; dir = parentDir(dir) {
		content, err := os.ReadFile(filepath.Join(dir, "AGENTS.md"))
		if err == nil {
			parts = append(parts, string(content))
		}
		if dir == "/" {
			break
		}
	}

	home := homeDir()
	if home != "" && home != cwd && !strings.HasPrefix(cwd, home) {
		content, err := os.ReadFile(filepath.Join(home, ".devlin", "AGENTS.md"))
		if err == nil {
			parts = append(parts, string(content))
		}
	}

	if len(parts) == 0 {
		return ""
	}

	var b strings.Builder
	b.WriteString("# Instructions from AGENTS.md\n")
	for i := len(parts) - 1; i >= 0; i-- {
		b.WriteString("\n")
		b.WriteString(parts[i])
	}

	return b.String()
}

func parentDir(dir string) string {
	parent := filepath.Dir(dir)
	if parent == dir {
		return ""
	}
	return parent
}
