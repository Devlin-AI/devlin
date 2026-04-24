package tool

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"unicode/utf8"
)

type GrepTool struct{}

type grepParams struct {
	Pattern string `json:"pattern"`
	Path    string `json:"path,omitempty"`
	Include string `json:"include,omitempty"`
}

type grepOutput struct {
	Title     string `json:"title"`
	Output    string `json:"output"`
	Truncated bool   `json:"truncated"`
}

const grepDescription = `- Fast content search tool that works with any codebase size
- Searches file contents using regular expressions
- Supports full regex syntax (eg. "log.*Error", "function\s+\w+", etc.)
- Filter files by pattern with the include parameter (eg. "*.js", "*.{ts,tsx}")
- Returns file paths and line numbers with at least one match sorted by modification time
- Use this tool when you need to find files containing specific patterns
- If you need to identify/count the number of matches within files, use the Bash tool with rg (ripgrep) directly. Do NOT use 'grep'.
- When you are doing an open-ended search that may require multiple rounds of globbing and grepping, use the Task tool instead.`

const grepParameters = `{
	"type": "object",
	"properties": {
		"pattern": {
			"type": "string",
			"description": "The regex pattern to search for in file contents"
		},
		"path": {
			"type": "string",
			"description": "The directory to search in. Defaults to the current working directory."
		},
		"include": {
			"type": "string",
			"description": "File pattern to include in the search (e.g. \"*.js\", \"*.{ts,tsx}\")"
		}
	},
	"required": ["pattern"]
}`

const (
	grepMaxMatches    = 100
	grepMaxLineLength = 2000
)

func (GrepTool) Name() string        { return "grep" }
func (GrepTool) Description() string { return grepDescription }
func (GrepTool) Parameters() json.RawMessage {
	return json.RawMessage(grepParameters)
}

type rgSummary struct {
	Elapsed struct {
		Total struct {
			Human string `json:"human"`
		} `json:"total"`
	} `json:"elapsed"`
	Stats struct {
		Elapsed struct {
			Human string `json:"human"`
		} `json:"elapsed"`
		Searches    int `json:"searches"`
		SearchBytes int `json:"search_bytes"`
	} `json:"stats"`
}

type rgMatch struct {
	Type string `json:"type"`
	Data struct {
		Path struct {
			Text string `json:"text"`
		} `json:"path"`
		Lines struct {
			Text string `json:"text"`
		} `json:"lines"`
		LineNumber int `json:"line_number"`
	} `json:"data"`
}

func (GrepTool) Execute(ctx context.Context, args json.RawMessage) (string, error) {
	var params grepParams
	if err := json.Unmarshal(args, &params); err != nil {
		return "", err
	}

	searchDir := expandHome(params.Path)
	if searchDir == "" {
		cwd, err := filepath.Abs(".")
		if err != nil {
			return "", fmt.Errorf("getwd: %w", err)
		}
		searchDir = cwd
	}

	if !filepath.IsAbs(searchDir) {
		abs, err := filepath.Abs(searchDir)
		if err != nil {
			return "", fmt.Errorf("resolve path: %w", err)
		}
		searchDir = abs
	}

	if _, err := exec.LookPath("rg"); err != nil {
		return "", fmt.Errorf("ripgrep (rg) not found on PATH. Install with: apt install ripgrep (Linux), brew install ripgrep (macOS), or cargo install ripgrep")
	}

	rgArgs := []string{
		"--no-config",
		"--json",
		"--hidden",
		"--glob=!.git/*",
		"--no-messages",
	}

	if params.Include != "" {
		rgArgs = append(rgArgs, "--glob="+params.Include)
	}

	rgArgs = append(rgArgs, "--", params.Pattern, searchDir)

	cmd := exec.CommandContext(ctx, "rg", rgArgs...)

	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr

	err := cmd.Run()
	if err != nil && stdout.Len() == 0 {
		if exitErr, ok := err.(*exec.ExitError); ok && exitErr.ExitCode() == 2 {
			return "", fmt.Errorf("grep error: %s", strings.TrimSpace(stderr.String()))
		}
		if exitErr, ok := err.(*exec.ExitError); ok && exitErr.ExitCode() == 1 {
			return noMatchResult(params.Pattern, searchDir), nil
		}
		return "", fmt.Errorf("grep: %w", err)
	}

	var matches []rgMatch
	scanner := bufio.NewScanner(&stdout)
	for scanner.Scan() {
		line := scanner.Bytes()
		var m rgMatch
		if err := json.Unmarshal(line, &m); err != nil {
			continue
		}
		if m.Type == "match" {
			matches = append(matches, m)
		}
	}

	if len(matches) == 0 {
		return noMatchResult(params.Pattern, searchDir), nil
	}

	return formatMatches(matches, params.Pattern, searchDir)
}

func formatMatches(matches []rgMatch, pattern, searchDir string) (string, error) {
	type fileInfo struct {
		path  string
		mtime int64
	}
	fileMtimes := &sync.Map{}
	var wg sync.WaitGroup

	seen := make(map[string]bool)
	var uniqueFiles []string
	for _, m := range matches {
		p := m.Data.Path.Text
		if !seen[p] {
			seen[p] = true
			uniqueFiles = append(uniqueFiles, p)
			wg.Add(1)
			go func(fp string) {
				defer wg.Done()
				var mt int64
				if info, err := os.Stat(fp); err == nil {
					mt = info.ModTime().UnixNano()
				}
				fileMtimes.Store(fp, mt)
			}(p)
		}
	}
	wg.Wait()

	sort.Slice(uniqueFiles, func(i, j int) bool {
		mi, _ := fileMtimes.Load(uniqueFiles[i])
		mj, _ := fileMtimes.Load(uniqueFiles[j])
		return mi.(int64) > mj.(int64)
	})

	fileOrder := make(map[string]int)
	for i, f := range uniqueFiles {
		fileOrder[f] = i
	}

	sorted := make([]rgMatch, len(matches))
	copy(sorted, matches)
	sort.SliceStable(sorted, func(i, j int) bool {
		pi := sorted[i].Data.Path.Text
		pj := sorted[j].Data.Path.Text
		if pi != pj {
			return fileOrder[pi] < fileOrder[pj]
		}
		return sorted[i].Data.LineNumber < sorted[j].Data.LineNumber
	})

	truncated := len(sorted) > grepMaxMatches
	if truncated {
		sorted = sorted[:grepMaxMatches]
	}

	lastFile := ""
	var sb strings.Builder

	for _, m := range sorted {
		filePath := m.Data.Path.Text

		if filePath != lastFile {
			if lastFile != "" {
				sb.WriteString("\n")
			}
			sb.WriteString(filePath)
			sb.WriteString(":\n")
			lastFile = filePath
		}

		text := strings.TrimRight(m.Data.Lines.Text, "\n\r")
		if utf8.RuneCountInString(text) > grepMaxLineLength {
			text = string([]rune(text)[:grepMaxLineLength]) + "..."
		}

		sb.WriteString(fmt.Sprintf("  Line %d: %s\n", m.Data.LineNumber, text))
	}

	if truncated {
		sb.WriteString(fmt.Sprintf("\n(Results truncated: showing %d of %d matches. Use a more specific path or pattern.)", grepMaxMatches, len(matches)))
	}

	relDir, _ := filepath.Rel(worktree, searchDir)
	if relDir == "." {
		relDir = searchDir
	}

	result := grepOutput{
		Title:     fmt.Sprintf("%s \"%s\"", relDir, pattern),
		Output:    sb.String(),
		Truncated: truncated,
	}

	out, err := json.Marshal(result)
	if err != nil {
		return "", err
	}
	return string(out), nil
}

func noMatchResult(pattern, searchDir string) string {
	relDir, _ := filepath.Rel(worktree, searchDir)
	if relDir == "." {
		relDir = searchDir
	}

	result := grepOutput{
		Title:     fmt.Sprintf("%s \"%s\"", relDir, pattern),
		Output:    "No files found",
		Truncated: false,
	}

	out, _ := json.Marshal(result)
	return string(out)
}

func (GrepTool) Display(args, output string) ToolDisplay {
	var gp grepParams
	if err := json.Unmarshal([]byte(args), &gp); err != nil {
		return ToolDisplay{Title: "grep", Body: []string{output}}
	}

	var out grepOutput
	if err := json.Unmarshal([]byte(output), &out); err != nil {
		return ToolDisplay{Title: "grep", Body: []string{output}}
	}

	disp := ToolDisplay{Title: out.Title}
	if out.Output != "" {
		disp.Body = strings.Split(out.Output, "\n")
	}
	return disp
}

func init() {
	Register(&GrepTool{})
}
