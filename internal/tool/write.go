package tool

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
)

type WriteTool struct{}

type writeParams struct {
	FilePath string `json:"filePath"`
	Content  string `json:"content"`
}

type writeOutput struct {
	Title   string `json:"title"`
	Output  string `json:"output"`
	Diff    string `json:"diff,omitempty"`
	Added   int    `json:"added,omitempty"`
	Removed int    `json:"removed,omitempty"`
}

const writeDescription = `Writes a file to the local filesystem.

Usage:
- This tool will overwrite the existing file if there is one at the provided path.
- If this is an existing file, you MUST use the Read tool first to read the file's contents. This tool will fail if you did not read the file first.
- ALWAYS prefer editing existing files in the codebase. NEVER write new files unless explicitly required.
- NEVER proactively create documentation files (*.md) or README files. Only create documentation files if explicitly requested by the User.
- Only use emojis if the user explicitly requests it. Avoid writing emojis to files unless asked.`

const writeParameters = `{
	"type": "object",
	"properties": {
		"content": {
			"type": "string",
			"description": "The content to write to the file"
		},
		"filePath": {
			"type": "string",
			"description": "The absolute path to the file to write (must be absolute, not relative)"
		}
	},
	"required": ["content", "filePath"]
}`

func (WriteTool) Name() string        { return "write" }
func (WriteTool) Description() string { return writeDescription }
func (WriteTool) Parameters() json.RawMessage {
	return json.RawMessage(writeParameters)
}

func (WriteTool) Execute(ctx context.Context, args json.RawMessage) (string, error) {
	var params writeParams
	if err := json.Unmarshal(args, &params); err != nil {
		return "", err
	}

	if params.FilePath == "" {
		return "", fmt.Errorf("filePath is required")
	}

	fp := expandHome(params.FilePath)
	if !filepath.IsAbs(fp) {
		cwd, err := os.Getwd()
		if err != nil {
			return "", fmt.Errorf("getwd: %w", err)
		}
		fp = filepath.Join(cwd, fp)
	}

	exists := false
	info, err := os.Stat(fp)
	if err == nil && !info.IsDir() {
		exists = true
	}

	if exists {
		read, stale := tracker.Check(fp)
		if !read {
			return "", fmt.Errorf("File has not been read yet. Read it first before writing to it.")
		}
		if stale {
			return "", fmt.Errorf("File has been modified since last read. Re-read the file before writing.")
		}
	}

	dir := filepath.Dir(fp)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return "", fmt.Errorf("mkdir %s: %w", dir, err)
	}

	var contentOld string
	if exists {
		data, readErr := os.ReadFile(fp)
		if readErr == nil {
			contentOld = string(data)
		}
	}

	if err := os.WriteFile(fp, []byte(params.Content), 0o644); err != nil {
		return "", fmt.Errorf("write %s: %w", fp, err)
	}

	diff := unifiedDiff(fp, contentOld, params.Content)
	added, removed := countDiffLines(contentOld, params.Content)

	out, err := json.Marshal(writeOutput{
		Title:   relPath(fp),
		Output:  "Wrote file successfully.",
		Diff:    diff,
		Added:   added,
		Removed: removed,
	})
	if err != nil {
		return "", err
	}
	return string(out), nil
}

func (WriteTool) Display(args, output string) ToolDisplay {
	var wp writeParams
	if err := json.Unmarshal([]byte(args), &wp); err != nil {
		return ToolDisplay{Body: []DisplayBlock{{Type: DisplayText, Content: output}}}
	}

	var out writeOutput
	if err := json.Unmarshal([]byte(output), &out); err != nil {
		return ToolDisplay{Title: wp.FilePath, Body: []DisplayBlock{{Type: DisplayText, Content: output}}}
	}

	disp := ToolDisplay{Title: out.Title}
	if out.Output != "" {
		disp.Body = []DisplayBlock{{Type: DisplayText, Content: out.Output}}
	}
	if out.Diff != "" {
		disp.Body = append(disp.Body, DisplayBlock{Type: DisplayDiff, Content: out.Diff})
	}
	return disp
}

func (WriteTool) Core() bool { return true }
func (WriteTool) PromptSnippet() string {
	return "write — Create or completely overwrite a file. Must read existing files first."
}
func (WriteTool) PromptGuidelines() []string {
	return []string{
		"Only use write for new files or complete rewrites — prefer edit for changes",
		"You MUST read an existing file before overwriting it",
		"NEVER write new files unless explicitly required",
	}
}

func init() {
	Register(&WriteTool{})
}
