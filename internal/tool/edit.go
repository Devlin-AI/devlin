package tool

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"
)

type EditTool struct{}

type editParams struct {
	FilePath   string `json:"filePath"`
	OldString  string `json:"oldString"`
	NewString  string `json:"newString"`
	ReplaceAll bool   `json:"replaceAll,omitempty"`
}

type editOutput struct {
	Title   string `json:"title"`
	Output  string `json:"output"`
	Diff    string `json:"diff,omitempty"`
	Added   int    `json:"added,omitempty"`
	Removed int    `json:"removed,omitempty"`
}

const editDescription = `Performs exact string replacements in files. 

Usage:
- You must use your Read tool at least once in the conversation before editing. This tool will error if you attempt an edit without reading the file. 
- When editing text from Read tool output, ensure you preserve the exact indentation (tabs/spaces) as it appears AFTER the line number prefix. The line number prefix format is: line number + colon + space (e.g. "1: "). Everything after that space is the actual file content to match. Never include any part of the line number prefix in the oldString or newString.
- ALWAYS prefer editing existing files in the codebase. NEVER write new files unless explicitly required.
- Only use emojis if the user explicitly requests it. Avoid adding emojis to files unless asked.
- The edit will FAIL if oldString is not found in the file with an error "oldString not found in content".
- The edit will FAIL if oldString is found multiple times in the file with an error "Found multiple matches for oldString. Provide more surrounding lines in oldString to identify the correct match." Either provide a larger string with more surrounding context to make it unique or use replaceAll to change every instance of oldString. 
- Use replaceAll for replacing and renaming strings across the file. This parameter is useful if you want to rename a variable for instance.`

const editParameters = `{
	"type": "object",
	"properties": {
		"filePath": {
			"type": "string",
			"description": "The absolute path to the file to modify"
		},
		"oldString": {
			"type": "string",
			"description": "The text to replace"
		},
		"newString": {
			"type": "string",
			"description": "The text to replace it with (must be different from oldString)"
		},
		"replaceAll": {
			"type": "boolean",
			"description": "Replace all occurrences of oldString (default false)"
		}
	},
	"required": ["filePath", "oldString", "newString"]
}`

func (EditTool) Name() string        { return "edit" }
func (EditTool) Description() string { return editDescription }
func (EditTool) Parameters() json.RawMessage {
	return json.RawMessage(editParameters)
}

func (EditTool) Execute(ctx context.Context, args json.RawMessage) (string, error) {
	var params editParams
	if err := json.Unmarshal(args, &params); err != nil {
		return "", err
	}

	if params.FilePath == "" {
		return "", fmt.Errorf("filePath is required")
	}

	if params.OldString == params.NewString {
		return "", fmt.Errorf("No changes to apply: oldString and newString are identical.")
	}

	fp := expandHome(params.FilePath)
	if !filepath.IsAbs(fp) {
		cwd, err := os.Getwd()
		if err != nil {
			return "", fmt.Errorf("getwd: %w", err)
		}
		fp = filepath.Join(cwd, fp)
	}

	if params.OldString == "" {
		return createFile(fp, params.NewString)
	}

	read, stale := tracker.Check(fp)
	if !read {
		return "", fmt.Errorf("File has not been read yet. Read it first before writing to it.")
	}
	if stale {
		return "", fmt.Errorf("File has been modified since last read. Re-read the file before editing.")
	}

	info, err := os.Stat(fp)
	if err != nil {
		if os.IsNotExist(err) {
			return "", fmt.Errorf("File %s not found", fp)
		}
		return "", fmt.Errorf("stat %s: %w", fp, err)
	}
	if info.IsDir() {
		return "", fmt.Errorf("Path is a directory, not a file: %s", fp)
	}

	content, err := os.ReadFile(fp)
	if err != nil {
		return "", fmt.Errorf("read %s: %w", fp, err)
	}

	contentOld := string(content)
	ending := detectLineEnding(contentOld)
	old := convertLineEnding(normalizeLineEndings(params.OldString), ending)
	next := convertLineEnding(normalizeLineEndings(params.NewString), ending)

	contentNew, err := fuzzyReplace(contentOld, old, next, params.ReplaceAll)
	if err != nil {
		return "", err
	}

	if err := os.WriteFile(fp, []byte(contentNew), info.Mode()); err != nil {
		return "", fmt.Errorf("write %s: %w", fp, err)
	}

	diff := unifiedDiff(fp, contentOld, contentNew)
	added, removed := countDiffLines(contentOld, contentNew)

	out, err := json.Marshal(editOutput{
		Title:   relPath(fp),
		Output:  "Edit applied successfully.",
		Diff:    diff,
		Added:   added,
		Removed: removed,
	})
	if err != nil {
		return "", err
	}
	return string(out), nil
}

func (EditTool) Display(args, output string) ToolDisplay {
	var ep editParams
	if err := json.Unmarshal([]byte(args), &ep); err != nil {
		return ToolDisplay{Title: "edit", Body: []string{output}}
	}

	var out editOutput
	if err := json.Unmarshal([]byte(output), &out); err != nil {
		return ToolDisplay{Title: "edit", Body: []string{output}}
	}

	disp := ToolDisplay{Title: out.Title}
	if out.Output != "" {
		disp.Body = []string{out.Output}
	}
	if out.Diff != "" {
		disp.Body = append(disp.Body, out.Diff)
	}
	return disp
}

func (EditTool) Core() bool { return true }
func (EditTool) PromptSnippet() string {
	return "edit — Replace exact text in existing files. Supports replaceAll for renames."
}
func (EditTool) PromptGuidelines() []string {
	return []string{
		"oldString must match the file content exactly (preserve indentation)",
		"Prefer edit over write for changing existing files",
		"Use replaceAll to rename a variable across a file",
		"If multiple matches, add more surrounding context to oldString",
	}
}

func init() {
	Register(&EditTool{})
}

func createFile(fp string, content string) (string, error) {
	dir := filepath.Dir(fp)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return "", fmt.Errorf("mkdir %s: %w", dir, err)
	}

	if err := os.WriteFile(fp, []byte(content), 0o644); err != nil {
		return "", fmt.Errorf("write %s: %w", fp, err)
	}

	diff := unifiedDiff(fp, "", content)
	added, _ := countDiffLines("", content)

	out, err := json.Marshal(editOutput{
		Title:  relPath(fp),
		Output: "Edit applied successfully.",
		Diff:   diff,
		Added:  added,
	})
	if err != nil {
		return "", err
	}
	return string(out), nil
}

func normalizeLineEndings(text string) string {
	return strings.ReplaceAll(text, "\r\n", "\n")
}

func detectLineEnding(text string) string {
	if strings.Contains(text, "\r\n") {
		return "\r\n"
	}
	return "\n"
}

func convertLineEnding(text string, ending string) string {
	if ending == "\n" {
		return text
	}
	return strings.ReplaceAll(text, "\n", "\r\n")
}

type replacerFunc func(content string, find string) []string

func fuzzyReplace(content string, oldString string, newString string, replaceAll bool) (string, error) {
	notFound := true

	replacers := []replacerFunc{
		simpleReplacer,
		lineTrimmedReplacer,
		blockAnchorReplacer,
		whitespaceNormalizedReplacer,
		indentationFlexibleReplacer,
		escapeNormalizedReplacer,
		trimmedBoundaryReplacer,
		contextAwareReplacer,
		multiOccurrenceReplacer,
	}

	for _, replacer := range replacers {
		for _, search := range replacer(content, oldString) {
			idx := strings.Index(content, search)
			if idx == -1 {
				continue
			}
			notFound = false
			if replaceAll {
				return strings.ReplaceAll(content, search, newString), nil
			}
			lastIdx := strings.LastIndex(content, search)
			if idx != lastIdx {
				continue
			}
			return content[:idx] + newString + content[idx+len(search):], nil
		}
	}

	if notFound {
		return "", fmt.Errorf("oldString not found in content")
	}
	return "", fmt.Errorf("Found multiple matches for oldString. Provide more surrounding lines in oldString to identify the correct match.")
}

func simpleReplacer(_ string, find string) []string {
	return []string{find}
}

func lineTrimmedReplacer(content string, find string) []string {
	originalLines := strings.Split(content, "\n")
	searchLines := strings.Split(find, "\n")

	if len(searchLines) > 0 && searchLines[len(searchLines)-1] == "" {
		searchLines = searchLines[:len(searchLines)-1]
	}
	if len(searchLines) == 0 {
		return nil
	}

	var results []string
	for i := 0; i <= len(originalLines)-len(searchLines); i++ {
		match := true
		for j := 0; j < len(searchLines); j++ {
			if strings.TrimSpace(originalLines[i+j]) != strings.TrimSpace(searchLines[j]) {
				match = false
				break
			}
		}
		if match {
			start := lineIndexToOffset(originalLines, i)
			end := lineIndexToOffset(originalLines, i+len(searchLines))
			results = append(results, content[start:end])
		}
	}
	return results
}

func blockAnchorReplacer(content string, find string) []string {
	originalLines := strings.Split(content, "\n")
	searchLines := strings.Split(find, "\n")

	if len(searchLines) > 0 && searchLines[len(searchLines)-1] == "" {
		searchLines = searchLines[:len(searchLines)-1]
	}
	if len(searchLines) < 3 {
		return nil
	}

	firstLine := strings.TrimSpace(searchLines[0])
	lastLine := strings.TrimSpace(searchLines[len(searchLines)-1])

	type candidate struct {
		startLine int
		endLine   int
	}

	var candidates []candidate
	for i := 0; i < len(originalLines); i++ {
		if strings.TrimSpace(originalLines[i]) != firstLine {
			continue
		}
		for j := i + 2; j < len(originalLines); j++ {
			if strings.TrimSpace(originalLines[j]) == lastLine {
				candidates = append(candidates, candidate{i, j})
				break
			}
		}
	}

	if len(candidates) == 0 {
		return nil
	}

	if len(candidates) == 1 {
		c := candidates[0]
		similarity := blockAnchorSimilarity(originalLines, searchLines, c.startLine, c.endLine)
		if similarity >= 0.0 {
			return []string{extractBlock(originalLines, c.startLine, c.endLine)}
		}
		return nil
	}

	bestIdx := -1
	maxSim := -1.0
	for i, c := range candidates {
		sim := blockAnchorSimilarity(originalLines, searchLines, c.startLine, c.endLine)
		if sim > maxSim {
			maxSim = sim
			bestIdx = i
		}
	}

	if maxSim >= 0.3 && bestIdx >= 0 {
		c := candidates[bestIdx]
		return []string{extractBlock(originalLines, c.startLine, c.endLine)}
	}
	return nil
}

func blockAnchorSimilarity(originalLines, searchLines []string, startLine, endLine int) float64 {
	actualBlockSize := endLine - startLine + 1
	searchBlockSize := len(searchLines)
	linesToCheck := min(searchBlockSize-2, actualBlockSize-2)

	if linesToCheck <= 0 {
		return 1.0
	}

	totalSim := 0.0
	for j := 1; j < searchBlockSize-1 && j < actualBlockSize-1; j++ {
		origLine := strings.TrimSpace(originalLines[startLine+j])
		searchLine := strings.TrimSpace(searchLines[j])
		maxLen := float64(max(len(origLine), len(searchLine)))
		if maxLen == 0 {
			continue
		}
		dist := levenshtein(origLine, searchLine)
		totalSim += (1.0 - float64(dist)/maxLen) / float64(linesToCheck)
		if totalSim >= 0.0 {
			break
		}
	}
	return totalSim
}

func whitespaceNormalizedReplacer(content string, find string) []string {
	normalizeWS := func(text string) string {
		re := regexp.MustCompile(`\s+`)
		return strings.TrimSpace(re.ReplaceAllString(text, " "))
	}

	normalizedFind := normalizeWS(find)
	lines := strings.Split(content, "\n")
	var results []string

	for _, line := range lines {
		if normalizeWS(line) == normalizedFind {
			results = append(results, line)
		} else {
			nl := normalizeWS(line)
			if strings.Contains(nl, normalizedFind) {
				words := strings.Fields(find)
				if len(words) > 0 {
					pattern := ""
					for i, w := range words {
						if i > 0 {
							pattern += `\s+`
						}
						pattern += regexp.QuoteMeta(w)
					}
					re, err := regexp.Compile(pattern)
					if err == nil {
						if match := re.FindString(line); match != "" {
							results = append(results, match)
						}
					}
				}
			}
		}
	}

	findLines := strings.Split(find, "\n")
	if len(findLines) > 1 {
		for i := 0; i <= len(lines)-len(findLines); i++ {
			block := strings.Join(lines[i:i+len(findLines)], "\n")
			if normalizeWS(block) == normalizedFind {
				results = append(results, block)
			}
		}
	}

	return results
}

func indentationFlexibleReplacer(content string, find string) []string {
	removeIndent := func(text string) string {
		lines := strings.Split(text, "\n")
		var nonEmpty []string
		for _, l := range lines {
			if strings.TrimSpace(l) != "" {
				nonEmpty = append(nonEmpty, l)
			}
		}
		if len(nonEmpty) == 0 {
			return text
		}
		minIndent := len(nonEmpty[0])
		for _, l := range nonEmpty {
			trimmed := len(l) - len(strings.TrimLeft(l, " \t"))
			if trimmed < minIndent {
				minIndent = trimmed
			}
		}
		result := make([]string, len(lines))
		for i, l := range lines {
			if strings.TrimSpace(l) == "" {
				result[i] = l
			} else if len(l) >= minIndent {
				result[i] = l[minIndent:]
			} else {
				result[i] = l
			}
		}
		return strings.Join(result, "\n")
	}

	normalizedFind := removeIndent(find)
	contentLines := strings.Split(content, "\n")
	findLines := strings.Split(find, "\n")

	var results []string
	for i := 0; i <= len(contentLines)-len(findLines); i++ {
		block := strings.Join(contentLines[i:i+len(findLines)], "\n")
		if removeIndent(block) == normalizedFind {
			results = append(results, block)
		}
	}
	return results
}

func escapeNormalizedReplacer(content string, find string) []string {
	unescape := func(s string) string {
		re := regexp.MustCompile(`\\(.)`)
		return re.ReplaceAllStringFunc(s, func(match string) string {
			if len(match) < 2 {
				return match
			}
			ch := match[1]
			switch ch {
			case 'n':
				return "\n"
			case 't':
				return "\t"
			case 'r':
				return "\r"
			default:
				return string(ch)
			}
		})
	}

	unescapedFind := unescape(find)
	var results []string

	if strings.Contains(content, unescapedFind) {
		results = append(results, unescapedFind)
	}

	lines := strings.Split(content, "\n")
	findLines := strings.Split(unescapedFind, "\n")

	for i := 0; i <= len(lines)-len(findLines); i++ {
		block := strings.Join(lines[i:i+len(findLines)], "\n")
		if unescape(block) == unescapedFind {
			results = append(results, block)
		}
	}

	return results
}

func trimmedBoundaryReplacer(content string, find string) []string {
	trimmedFind := strings.TrimSpace(find)
	if trimmedFind == find {
		return nil
	}

	var results []string
	if strings.Contains(content, trimmedFind) {
		results = append(results, trimmedFind)
	}

	lines := strings.Split(content, "\n")
	findLines := strings.Split(find, "\n")

	for i := 0; i <= len(lines)-len(findLines); i++ {
		block := strings.Join(lines[i:i+len(findLines)], "\n")
		if strings.TrimSpace(block) == trimmedFind {
			results = append(results, block)
		}
	}
	return results
}

func contextAwareReplacer(content string, find string) []string {
	findLines := strings.Split(find, "\n")
	if len(findLines) > 0 && findLines[len(findLines)-1] == "" {
		findLines = findLines[:len(findLines)-1]
	}
	if len(findLines) < 3 {
		return nil
	}

	contentLines := strings.Split(content, "\n")
	firstLine := strings.TrimSpace(findLines[0])
	lastLine := strings.TrimSpace(findLines[len(findLines)-1])

	for i := 0; i < len(contentLines); i++ {
		if strings.TrimSpace(contentLines[i]) != firstLine {
			continue
		}
		for j := i + 2; j < len(contentLines); j++ {
			if strings.TrimSpace(contentLines[j]) == lastLine {
				blockLines := contentLines[i : j+1]
				if len(blockLines) == len(findLines) {
					matching := 0
					total := 0
					for k := 1; k < len(blockLines)-1; k++ {
						bl := strings.TrimSpace(blockLines[k])
						fl := strings.TrimSpace(findLines[k])
						if len(bl) > 0 || len(fl) > 0 {
							total++
							if bl == fl {
								matching++
							}
						}
					}
					if total == 0 || float64(matching)/float64(total) >= 0.5 {
						block := strings.Join(blockLines, "\n")
						return []string{block}
					}
				}
				break
			}
		}
	}
	return nil
}

func multiOccurrenceReplacer(content string, find string) []string {
	var results []string
	start := 0
	for {
		idx := strings.Index(content[start:], find)
		if idx == -1 {
			break
		}
		results = append(results, find)
		start += idx + len(find)
	}
	return results
}

func levenshtein(a, b string) int {
	aRunes := []rune(a)
	bRunes := []rune(b)
	aLen := len(aRunes)
	bLen := len(bRunes)

	if aLen == 0 {
		return bLen
	}
	if bLen == 0 {
		return aLen
	}

	prev := make([]int, bLen+1)
	curr := make([]int, bLen+1)

	for j := 0; j <= bLen; j++ {
		prev[j] = j
	}

	for i := 1; i <= aLen; i++ {
		curr[0] = i
		for j := 1; j <= bLen; j++ {
			cost := 1
			if aRunes[i-1] == bRunes[j-1] {
				cost = 0
			}
			curr[j] = min(
				prev[j]+1,
				curr[j-1]+1,
				prev[j-1]+cost,
			)
		}
		prev, curr = curr, prev
	}
	return prev[bLen]
}

func lineIndexToOffset(lines []string, lineIdx int) int {
	offset := 0
	for i := 0; i < lineIdx && i < len(lines); i++ {
		offset += len(lines[i]) + 1
	}
	return offset
}

func extractBlock(lines []string, startLine, endLine int) string {
	return strings.Join(lines[startLine:endLine+1], "\n")
}

func unifiedDiff(fp string, oldContent string, newContent string) string {
	oldLines := strings.Split(oldContent, "\n")
	newLines := strings.Split(newContent, "\n")

	var sb strings.Builder
	sb.WriteString(fmt.Sprintf("--- %s\n", fp))
	sb.WriteString(fmt.Sprintf("+++ %s\n", fp))

	for _, op := range diffOps(oldLines, newLines) {
		for _, line := range op.lines {
			sb.WriteString(string(op.prefix) + line + "\n")
		}
	}

	return sb.String()
}

type diffOp struct {
	prefix byte
	lines  []string
}

func diffOps(old, new []string) []diffOp {
	var ops []diffOp
	li, ri := 0, 0

	for li < len(old) && ri < len(new) {
		if old[li] == new[ri] {
			ops = append(ops, diffOp{' ', []string{old[li]}})
			li++
			ri++
		} else {
			j := ri + 1
			for j < len(new) && new[j] != old[li] {
				j++
			}
			if j < len(new) {
				for k := ri; k < j; k++ {
					ops = append(ops, diffOp{'+', []string{new[k]}})
				}
				ri = j
			} else {
				ops = append(ops, diffOp{'-', []string{old[li]}})
				li++
			}
		}
	}

	for ; li < len(old); li++ {
		ops = append(ops, diffOp{'-', []string{old[li]}})
	}
	for ; ri < len(new); ri++ {
		ops = append(ops, diffOp{'+', []string{new[ri]}})
	}

	return ops
}

func countDiffLines(oldContent, newContent string) (added, removed int) {
	oldLines := strings.Split(oldContent, "\n")
	newLines := strings.Split(newContent, "\n")

	oldSet := make(map[string]int)
	for _, l := range oldLines {
		oldSet[l]++
	}

	newSet := make(map[string]int)
	for _, l := range newLines {
		newSet[l]++
	}

	for l, cnt := range newSet {
		if oldCnt, ok := oldSet[l]; ok {
			if cnt > oldCnt {
				added += cnt - oldCnt
			}
		} else {
			added += cnt
		}
	}

	for l, cnt := range oldSet {
		if newCnt, ok := newSet[l]; ok {
			if cnt > newCnt {
				removed += cnt - newCnt
			}
		} else {
			removed += cnt
		}
	}

	return added, removed
}
