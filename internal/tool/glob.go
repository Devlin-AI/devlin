package tool

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/bmatcuk/doublestar/v4"
)

type GlobTool struct{}

type globParams struct {
	Pattern string `json:"pattern"`
	Path    string `json:"path,omitempty"`
}

type globOutput struct {
	Title     string `json:"title"`
	Output    string `json:"output"`
	Truncated bool   `json:"truncated"`
}

type globEntry struct {
	path  string
	mtime time.Time
}

const globDescription = `- Fast file pattern matching tool that works with any codebase size
- Supports glob patterns like "**/*.go" or "src/**/*.ts"
- Returns matching file paths sorted by modification time (newest first)
- Use this tool when you need to find files by name patterns
- When you are doing an open-ended search that may require multiple rounds of globbing and grepping, use the Task tool instead.`

const globParameters = `{
	"type": "object",
	"properties": {
		"pattern": {
			"type": "string",
			"description": "The glob pattern to match files against (e.g., \"**/*.go\", \"src/**/*.ts\", \"*.md\")"
		},
		"path": {
			"type": "string",
			"description": "The directory to search in. If not specified, the current working directory will be used. IMPORTANT: Omit this field to use the default directory. DO NOT enter \"undefined\" or \"null\" - simply omit it for the default behavior. Must be a valid directory path if provided."
		}
	},
	"required": ["pattern"]
}`

const (
	globMaxResults = 100
	globTimeout    = 30 * time.Second
)

func (GlobTool) Name() string        { return "glob" }
func (GlobTool) Description() string { return globDescription }
func (GlobTool) Parameters() json.RawMessage {
	return json.RawMessage(globParameters)
}

func (GlobTool) Execute(ctx context.Context, args json.RawMessage) (string, error) {
	var params globParams
	if err := json.Unmarshal(args, &params); err != nil {
		return "", err
	}

	searchDir := expandHome(params.Path)
	if searchDir == "" {
		cwd, err := os.Getwd()
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

	stat, err := os.Stat(searchDir)
	if err != nil {
		if os.IsNotExist(err) {
			return "", fmt.Errorf("directory not found: %s", searchDir)
		}
		return "", fmt.Errorf("stat %s: %w", searchDir, err)
	}
	if !stat.IsDir() {
		return "", fmt.Errorf("glob path must be a directory: %s", searchDir)
	}

	ctx, cancel := context.WithTimeout(ctx, globTimeout)
	defer cancel()

	fsys := os.DirFS(searchDir)

	var matches []globEntry
	totalMatches := 0

	err = doublestar.GlobWalk(fsys, params.Pattern, func(p string, d os.DirEntry) error {
		select {
		case <-ctx.Done():
			return ctx.Err()
		default:
		}

		if d.IsDir() {
			return nil
		}

		totalMatches++

		absPath := filepath.Join(searchDir, p)
		info, statErr := d.Info()
		if statErr != nil {
			return nil
		}

		matches = append(matches, globEntry{path: absPath, mtime: info.ModTime()})

		if len(matches) >= globMaxResults+1 {
			return fmt.Errorf("max reached")
		}

		return nil
	})

	if ctx.Err() != nil {
		return "", fmt.Errorf("glob search timed out")
	}
	if err != nil {
		return "", fmt.Errorf("glob walk: %w", err)
	}

	sort.Slice(matches, func(i, j int) bool {
		return matches[i].mtime.After(matches[j].mtime)
	})

	truncated := len(matches) > globMaxResults
	if truncated {
		matches = matches[:globMaxResults]
	}

	relDir, _ := filepath.Rel(worktree, searchDir)
	if relDir == "." {
		relDir = searchDir
	}

	var sb strings.Builder
	if len(matches) == 0 {
		sb.WriteString("No files found")
	} else {
		for _, m := range matches {
			sb.WriteString(m.path)
			sb.WriteString("\n")
		}
		if truncated {
			sb.WriteString(fmt.Sprintf("\n(Results truncated: showing first %d of %d matches. Use a more specific pattern.)", globMaxResults, totalMatches))
		}
	}

	result := globOutput{
		Title:     fmt.Sprintf("%s %s", relDir, params.Pattern),
		Output:    sb.String(),
		Truncated: truncated,
	}

	out, err := json.Marshal(result)
	if err != nil {
		return "", err
	}
	return string(out), nil
}

func (GlobTool) Display(args, output string) ToolDisplay {
	var gp globParams
	if err := json.Unmarshal([]byte(args), &gp); err != nil {
		return ToolDisplay{Title: "glob", Body: []string{output}}
	}

	var out globOutput
	if err := json.Unmarshal([]byte(output), &out); err != nil {
		return ToolDisplay{Title: "glob", Body: []string{output}}
	}

	disp := ToolDisplay{Title: out.Title}
	if out.Output != "" {
		disp.Body = strings.Split(out.Output, "\n")
	}
	return disp
}

func init() {
	Register(&GlobTool{})
}
