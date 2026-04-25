package tool

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"sort"
	"strings"
	"unicode/utf8"

	"github.com/devlin-ai/devlin/internal/logger"
)

var worktree string

func init() {
	if cwd, err := os.Getwd(); err == nil {
		worktree = cwd
	}
}

type ReadTool struct{}

type readParams struct {
	FilePath string `json:"filePath"`
	Offset   int    `json:"offset,omitempty"`
	Limit    int    `json:"limit,omitempty"`
}

type readOutput struct {
	Title     string `json:"title"`
	Output    string `json:"output"`
	Truncated bool   `json:"truncated"`
}

const readDescription = `Read a file or directory from the local filesystem. If the path does not exist, an error is returned.

Usage:
- The filePath parameter should be an absolute path.
- By default, this tool returns up to 2000 lines from the start of the file.
- The offset parameter is the line number to start from (1-indexed).
- To read later sections, call this tool again with a larger offset.
- Use the grep tool to find specific content in large files or files with long lines.
- If you are unsure of the correct file path, use the glob tool to look up filenames by glob pattern.
- Contents are returned with each line prefixed by its line number as '<line>: <content>'. For example, if a file has contents "foo\n", you will receive "1: foo\n". For directories, entries are returned one per line (without line numbers) with a trailing '/' for subdirectories.
- Any line longer than 2000 characters is truncated.
- Output is capped at 50 KB. If the file is larger, use offset to read beyond the cap.
- Call this tool in parallel when you know there are multiple files you want to read.
- Avoid tiny repeated slices (30 line chunks). If you need more context, read a larger window.`

const readParameters = `{
	"type": "object",
	"properties": {
		"filePath": {
			"type": "string",
			"description": "The absolute path to the file or directory to read"
		},
		"offset": {
			"type": "number",
			"description": "The line number to start reading from (1-indexed)"
		},
		"limit": {
			"type": "number",
			"description": "The maximum number of lines to read (defaults to 2000)"
		}
	},
	"required": ["filePath"]
}`

const (
	defaultLimit  = 2000
	maxLineLength = 2000
	maxLineSuffix = "... (line truncated to 2000 chars)"
	maxBytes      = 50 * 1024
	maxBytesLabel = "50 KB"
	sampleBytes   = 4096
)

func (ReadTool) Name() string        { return "read" }
func (ReadTool) Description() string { return readDescription }
func (ReadTool) Parameters() json.RawMessage {
	return json.RawMessage(readParameters)
}

func (ReadTool) Execute(ctx context.Context, args json.RawMessage) (string, error) {
	var params readParams
	if err := json.Unmarshal(args, &params); err != nil {
		return "", err
	}

	if params.Offset != 0 && params.Offset < 1 {
		return "", fmt.Errorf("offset must be greater than or equal to 1")
	}

	fp := expandHome(params.FilePath)
	if !filepath.IsAbs(fp) {
		cwd, err := os.Getwd()
		if err != nil {
			return "", fmt.Errorf("getwd: %w", err)
		}
		fp = filepath.Join(cwd, fp)
	}

	if runtime.GOOS == "windows" {
		fp = strings.ReplaceAll(fp, "/", "\\")
	}

	stat, err := os.Stat(fp)
	if err != nil {
		if os.IsNotExist(err) {
			return suggestSimilarPath(fp)
		}
		return "", fmt.Errorf("stat %s: %w", fp, err)
	}

	if stat.IsDir() {
		return readDirectory(fp, params.Offset, params.Limit)
	}

	if isBinary(fp) {
		return "", fmt.Errorf("cannot read binary file: %s", fp)
	}

	tracker.Store(fp, stat.ModTime().Unix())

	return readFile(fp, params.Offset, params.Limit)
}

func (ReadTool) Display(args, output string) ToolDisplay {
	var rp readParams
	if err := json.Unmarshal([]byte(args), &rp); err != nil {
		return ToolDisplay{Title: "read", Body: []string{output}}
	}

	var out readOutput
	if err := json.Unmarshal([]byte(output), &out); err != nil {
		return ToolDisplay{Title: "read", Body: []string{output}}
	}

	disp := ToolDisplay{Title: out.Title}
	if out.Output != "" {
		disp.Body = strings.Split(out.Output, "\n")
	}
	return disp
}

func (ReadTool) Core() bool { return true }
func (ReadTool) PromptSnippet() string {
	return "read — Read file or directory contents. Supports offset/limit for large files."
}
func (ReadTool) PromptGuidelines() []string {
	return []string{
		"Use read instead of cat/head/tail to examine files",
		"Read files in parallel when you need multiple files",
		"Use offset to read beyond default 2000 lines",
		"You MUST read a file before editing or overwriting it",
	}
}

func init() {
	Register(&ReadTool{})
}

func isBinary(path string) bool {
	ext := strings.ToLower(filepath.Ext(path))

	binaryExts := []string{
		".zip", ".tar", ".gz", ".exe", ".dll", ".so", ".class", ".jar", ".war",
		".7z", ".doc", ".docx", ".xls", ".xlsx", ".ppt", ".pptx", ".odt",
		".ods", ".odp", ".bin", ".dat", ".obj", ".o", ".a", ".lib", ".wasm",
		".pyc", ".pyo", ".png", ".jpg", ".jpeg", ".gif", ".ico", ".webp",
		".pdf", ".svg", ".bmp", ".tiff", ".tif", ".avif", ".mp3", ".mp4",
		".avi", ".mov", ".wav", ".flac", ".ogg", ".webm",
	}

	for _, e := range binaryExts {
		if ext == e {
			return true
		}
	}

	file, err := os.Open(path)
	if err != nil {
		return false
	}
	defer file.Close()

	buf := make([]byte, sampleBytes)
	n, _ := file.Read(buf)
	if n == 0 {
		return false
	}

	nonPrintable := 0
	for i := 0; i < n; i++ {
		if buf[i] == 0 {
			return true
		}
		if buf[i] < 9 || (buf[i] > 13 && buf[i] < 32) {
			nonPrintable++
		}
	}

	return float64(nonPrintable)/float64(n) > 0.3
}

type fileResult struct {
	lines  []string
	count  int
	cut    bool
	more   bool
	offset int
}

func scanFile(path string, offset, limit int) (*fileResult, error) {
	if offset < 1 {
		offset = 1
	}
	if limit <= 0 {
		limit = defaultLimit
	}

	file, err := os.Open(path)
	if err != nil {
		return nil, fmt.Errorf("open %s: %w", path, err)
	}
	defer file.Close()

	scanner := bufio.NewScanner(file)
	scanner.Split(bufio.ScanLines)

	var lines []string
	lineNum := 1
	totalBytes := 0
	cut := false
	more := false

	for scanner.Scan() {
		if len(lines) >= limit {
			more = true
			lineNum++
			for scanner.Scan() {
				lineNum++
			}
			break
		}

		lineNum++

		if lineNum-1 < offset {
			continue
		}

		text := scanner.Text()
		if utf8.RuneCountInString(text) > maxLineLength {
			text = string([]rune(text)[:maxLineLength]) + maxLineSuffix
		}

		lineBytes := len(text)
		if len(lines) > 0 {
			lineBytes++
		}

		if totalBytes+lineBytes > maxBytes {
			cut = true
			more = true
			lineNum--
			for scanner.Scan() {
				lineNum++
			}
			break
		}

		lines = append(lines, text)
		totalBytes += lineBytes
	}

	if err := scanner.Err(); err != nil {
		logger.L().Error("read file scanner error", "error", err)
	}

	return &fileResult{
		lines:  lines,
		count:  lineNum - 1,
		cut:    cut,
		more:   more,
		offset: offset,
	}, nil
}

func readFile(path string, offset, limit int) (string, error) {
	result, err := scanFile(path, offset, limit)
	if err != nil {
		return "", err
	}

	if result.count < result.offset && !(result.count == 0 && result.offset == 1) {
		return "", fmt.Errorf("offset %d is out of range for this file (%d lines)", result.offset, result.count)
	}

	relPath := relPath(path)

	var sb strings.Builder
	sb.WriteString(fmt.Sprintf("<path>%s</path>\n", path))
	sb.WriteString("<type>file</type>\n")
	sb.WriteString("<content>\n")

	for i, line := range result.lines {
		sb.WriteString(fmt.Sprintf("%d: %s\n", result.offset+i, line))
	}

	last := result.offset + len(result.lines) - 1
	next := last + 1

	if result.cut {
		sb.WriteString(fmt.Sprintf("\n(Output capped at %s. Showing lines %d-%d. Use offset=%d to continue.)", maxBytesLabel, result.offset, last, next))
	} else if result.more {
		sb.WriteString(fmt.Sprintf("\n(Showing lines %d-%d of %d. Use offset=%d to continue.)", result.offset, last, result.count, next))
	} else {
		sb.WriteString(fmt.Sprintf("\n(End of file - total %d lines)", result.count))
	}
	sb.WriteString("\n</content>")

	out, err := json.Marshal(readOutput{
		Title:     relPath,
		Output:    sb.String(),
		Truncated: result.more || result.cut,
	})
	if err != nil {
		return "", err
	}
	return string(out), nil
}

func readDirectory(path string, offset, limit int) (string, error) {
	if offset < 1 {
		offset = 1
	}
	if limit <= 0 {
		limit = defaultLimit
	}

	entries, err := os.ReadDir(path)
	if err != nil {
		return "", fmt.Errorf("readdir %s: %w", path, err)
	}

	var names []string
	for _, e := range entries {
		name := e.Name()
		if e.IsDir() {
			name += "/"
		}
		names = append(names, name)
	}

	sort.Slice(names, func(i, j int) bool {
		return names[i] < names[j]
	})

	limitApplied := limit
	if limitApplied <= 0 {
		limitApplied = defaultLimit
	}

	start := offset - 1
	end := start + limitApplied
	if end > len(names) {
		end = len(names)
	}

	sliced := names[start:end]

	relPath := relPath(path)

	var sb strings.Builder
	sb.WriteString(fmt.Sprintf("<path>%s</path>\n", path))
	sb.WriteString("<type>directory</type>\n")
	sb.WriteString("<entries>\n")

	for _, name := range sliced {
		sb.WriteString(name + "\n")
	}

	totalEntries := len(names)
	lastEntry := start + len(sliced)

	if lastEntry < totalEntries {
		sb.WriteString(fmt.Sprintf("\n(Showing %d of %d entries. Use offset=%d to continue.)", len(sliced), totalEntries, lastEntry+1))
	} else {
		sb.WriteString(fmt.Sprintf("\n(%d entries)", totalEntries))
	}
	sb.WriteString("\n</entries>")

	out, err := json.Marshal(readOutput{
		Title:     relPath,
		Output:    sb.String(),
		Truncated: lastEntry < totalEntries,
	})
	if err != nil {
		return "", err
	}
	return string(out), nil
}

func suggestSimilarPath(path string) (string, error) {
	dir := filepath.Dir(path)
	base := filepath.Base(path)

	entries, err := os.ReadDir(dir)
	if err != nil {
		return "", fmt.Errorf("file not found: %s", path)
	}

	var suggestions []string
	lowerBase := strings.ToLower(base)
	for _, e := range entries {
		eName := strings.ToLower(e.Name())
		if strings.Contains(eName, lowerBase) || strings.Contains(lowerBase, eName) {
			suggestions = append(suggestions, filepath.Join(dir, e.Name()))
			if len(suggestions) >= 3 {
				break
			}
		}
	}

	if len(suggestions) > 0 {
		return "", fmt.Errorf("File not found: %s\n\nDid you mean one of these?\n%s", path, strings.Join(suggestions, "\n"))
	}

	return "", fmt.Errorf("File not found: %s", path)
}

func expandHome(fp string) string {
	if fp == "~" {
		if home, err := os.UserHomeDir(); err == nil {
			return home
		}
	}
	if strings.HasPrefix(fp, "~/") {
		if home, err := os.UserHomeDir(); err == nil {
			return filepath.Join(home, fp[2:])
		}
	}
	return fp
}

func relPath(path string) string {
	if worktree != "" {
		if rel, err := filepath.Rel(worktree, path); err == nil {
			return rel
		}
	}
	return path
}
