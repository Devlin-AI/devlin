package tool

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"strings"
	"sync"
	"syscall"
	"time"

	"github.com/creack/pty"
)

type BashTool struct{}

type bashParams struct {
	Command string `json:"command"`
	Timeout int    `json:"timeout,omitempty"`
}

type bashOutput struct {
	ExitCode int    `json:"exit_code"`
	Stdout   string `json:"stdout"`
	Stderr   string `json:"stderr"`
	TimedOut bool   `json:"timed_out"`
}

const bashDescription = `Executes a given bash command in a persistent shell session with optional timeout, ensuring proper handling and security measures.

Be aware: OS: linux, Shell: bash

All commands run in the current working directory by default.

IMPORTANT: This tool is for terminal operations like git, npm, docker, etc. DO NOT use it for file operations (reading, writing, editing, searching, finding files) - use the specialized tools for this instead.

Before executing the command, please follow these steps:

1. Directory Verification:
   - If the command will create new directories or files, first use 'ls' to verify the parent directory exists and is the correct location
   - For example, before running "mkdir foo/bar", first use 'ls foo' to check that "foo" exists and is the intended parent directory

2. Command Execution:
   - Always quote file paths that contain spaces with double quotes (e.g., rm "path with spaces/file.txt")
   - Examples of proper quoting:
     - mkdir "/Users/name/My Documents" (correct)
     - mkdir /Users/name/My Documents (incorrect - will fail)
     - python "/path/with spaces/script.py" (correct)
     - python /path/with spaces/script.py (incorrect - will fail)
   - After ensuring proper quoting, execute the command.
   - Capture the output of the command.

Usage notes:
  - The command argument is required.
  - You can specify an optional timeout in seconds. If not specified, commands will time out after 120s (2 minutes).
  - Avoid using Bash with the 'find', 'grep', 'cat', 'head', 'tail', 'sed', 'awk', or 'echo' commands, unless explicitly instructed or when these commands are truly necessary for the task. Instead, always prefer using the dedicated tools for these commands:
    - File search: Use Glob (NOT find or ls)
    - Content search: Use Grep (NOT grep or rg)
    - Read files: Use Read (NOT cat/head/tail)
    - Communication: Output text directly (NOT echo/printf)
  - When issuing multiple commands:
    - If the commands are independent and can run in parallel, make multiple Bash tool calls in a single message. For example, if you need to run "git status" and "git diff", send a single message with two Bash tool calls in parallel.
    - If the commands depend on each other and must run sequentially, use a single Bash call with '&&' to chain them together (e.g., git add . && git commit -m "message" && git push). For instance, if one operation must complete before another starts (like mkdir before cp, Read before Bash for git operations, or git add before git commit), run these operations sequentially instead.
    - Use ';' only when you need to run commands sequentially but don't care if earlier commands fail
    - DO NOT use newlines to separate commands (newlines are ok in quoted strings)

# Committing changes with git

Only create commits when requested by the user. If unclear, ask first. When the user asks you to create a new git commit, follow these steps carefully:

Git Safety Protocol:
- NEVER update the git config
- NEVER run destructive/irreversible git commands (like push --force, hard reset, etc) unless the user explicitly requests them
- NEVER skip hooks (--no-verify, --no-gpg-sign, etc) unless the user explicitly requests it
- NEVER run force push to main/master, warn the user if they request it
- Avoid git commit --amend. ONLY use --amend when ALL conditions are met:
  (1) User explicitly requested amend, OR commit SUCCEEDED but pre-commit hook auto-modified files that need including
  (2) HEAD commit was created by you in this conversation (verify: git log -1 --format='%an %ae')
  (3) Commit has NOT been pushed to remote (verify: git status shows "Your branch is ahead")
- CRITICAL: If commit FAILED or was REJECTED by hook, NEVER amend - fix the issue and create a NEW commit
- CRITICAL: If you already pushed to remote, NEVER amend unless user explicitly requests it (requires force push)
- NEVER commit changes unless the user explicitly asks you to. It is VERY IMPORTANT to only commit when explicitly asked, otherwise the user will feel that you are being too proactive.

1. You can call multiple tools in a single response. When multiple independent pieces of information are requested and all commands are likely to succeed, run multiple tool calls in parallel for optimal performance. run the following bash commands in parallel, each using the Bash tool:
   - Run a git status command to see all untracked files.
   - Run a git diff command to see both staged and unstaged changes that will be committed.
   - Run a git log command to see recent commit messages, so that you can follow this repository's commit message style.
2. Analyze all staged changes (both previously staged and newly added) and draft a commit message:
   - Summarize the nature of the changes (eg. new feature, enhancement to an existing feature, bug fix, refactoring, test, docs, etc.). Ensure the message accurately reflects the changes and their purpose (i.e. "add" means a wholly new feature, "update" means an enhancement to an existing feature, "fix" means a bug fix, etc.).
   - Do not commit files that likely contain secrets (.env, credentials.json, etc.). Warn the user if they specifically request to commit those files
   - Draft a concise (1-2 sentences) commit message that focuses on the "why" rather than the "what"
   - Ensure it accurately reflects the changes and their purpose
3. You can call multiple tools in a single response. When multiple independent pieces of information are requested and all commands are likely to succeed, run multiple tool calls in parallel for optimal performance. run the following commands:
   - Add relevant untracked files to the staging area.
   - Create the commit with a message
   - Run git status after the commit completes to verify success.
   Note: git status depends on the commit completing, so run it sequentially after the commit.
4. If the commit fails due to pre-commit hook, fix the issue and create a NEW commit (see amend rules above)

Important notes:
- NEVER run additional commands to read or explore code, besides git bash commands
- NEVER use the TodoWrite or Task tools
- DO NOT push to the remote repository unless the user explicitly asks you to do so
- IMPORTANT: Never use git commands with the -i flag (like git rebase -i or git add -i) since they require interactive input which is not supported.
- If there are no changes to commit (i.e., no untracked files and no modifications), do not create an empty commit

# Creating pull requests
Use the gh command via the Bash tool for ALL GitHub-related tasks including working with issues, pull requests, checks, and releases. If given a GitHub URL use the gh command to get the information needed.

IMPORTANT: When the user asks you to create a pull request, follow these steps carefully:

1. You can call multiple tools in a single response. When multiple independent pieces of information are requested and all commands are likely to succeed, run multiple tool calls in parallel for optimal performance. run the following bash commands in parallel using the Bash tool, in order to understand the current state of the branch since it diverged from the main branch:
   - Run a git status command to see all untracked files
   - Run a git diff command to see both staged and unstaged changes that will be committed
   - Check if the current branch tracks a remote branch and is up to date with the remote, so you know if you need to push to the remote
   - Run a git log command and 'git diff [base-branch]...HEAD' to understand the full commit history for the current branch (from the time it diverged from the base branch)
2. Analyze all changes that will be included in the pull request, making sure to look at all relevant commits (NOT just the latest commit, but ALL commits that will be included in the pull request!!!), and draft a pull request summary
3. You can call multiple tools in a single response. When multiple independent pieces of information are requested and all commands are likely to succeed, run multiple tool calls in parallel for optimal performance. run the following commands in parallel:
   - Create new branch if needed
   - Push to remote with -u flag if needed
   - Create PR using gh pr create with the format below. Use a HEREDOC to pass the body to ensure correct formatting.
<example>
gh pr create --title "the pr title" --body "$(cat <<'EOF'
## Summary
<1-3 bullet points>
</example>

Important:
- DO NOT use the TodoWrite or Task tools
- Return the PR URL when you're done, so the user can see it

# Other common operations
- View comments on a GitHub PR: gh api repos/foo/bar/pulls/123/comments`

const bashParameters = `{
	"type": "object",
	"properties": {
		"command": {
			"type": "string",
			"description": "The command to execute"
		},
		"timeout": {
			"type": "integer",
			"description": "Optional timeout in seconds"
		}
	},
	"required": ["command"]
}`

func (BashTool) Name() string {
	return "bash"
}

func (BashTool) Description() string {
	return bashDescription
}

func (BashTool) Parameters() json.RawMessage {
	return json.RawMessage(bashParameters)
}

func (BashTool) Execute(ctx context.Context, args json.RawMessage) (string, error) {
	var params bashParams
	if err := json.Unmarshal(args, &params); err != nil {
		return "", err
	}

	ctx, cancel := context.WithTimeout(ctx, params.timeout())
	defer cancel()

	cmd := exec.CommandContext(ctx, "bash", "-c", params.Command)
	var stdout, stderr []byte
	outPipe, err := cmd.StdoutPipe()
	if err != nil {
		return "", err
	}
	errPipe, err := cmd.StderrPipe()
	if err != nil {
		return "", err
	}

	if err := cmd.Start(); err != nil {
		return "", err
	}

	done := make(chan struct{})
	go func() {
		buf := make([]byte, 4096)
		for {
			n, err := outPipe.Read(buf)
			if n > 0 {
				stdout = append(stdout, buf[:n]...)
			}
			if err != nil {
				break
			}
		}
		buf2 := make([]byte, 4096)
		for {
			n, err := errPipe.Read(buf2)
			if n > 0 {
				stderr = append(stderr, buf2[:n]...)
			}
			if err != nil {
				break
			}
		}
		close(done)
	}()

	waitErr := cmd.Wait()
	<-done

	result := bashOutput{
		ExitCode: -1,
		Stdout:   string(stdout),
		Stderr:   string(stderr),
	}

	if ctx.Err() == context.DeadlineExceeded {
		result.TimedOut = true
	}

	if waitErr != nil {
		if exitErr, ok := waitErr.(*exec.ExitError); ok {
			result.ExitCode = exitErr.ExitCode()
		}
	} else {
		result.ExitCode = 0
	}

	out, _ := json.Marshal(result)
	return string(out), nil
}

func (BashTool) Display(args, output string) ToolDisplay {
	var bp bashParams
	if err := json.Unmarshal([]byte(args), &bp); err != nil {
		return ToolDisplay{Title: "bash", Body: []string{output}}
	}

	disp := ToolDisplay{
		Title: fmt.Sprintf("$ %s", bp.Command),
	}

	var out bashOutput
	if err := json.Unmarshal([]byte(output), &out); err != nil {
		if output != "" {
			disp.Body = []string{output}
		}
		return disp
	}

	if out.Stdout != "" {
		disp.Body = append(disp.Body, out.Stdout)
	}
	if out.Stderr != "" {
		disp.Body = append(disp.Body, out.Stderr)
	}
	if len(disp.Body) == 0 {
		if out.ExitCode != 0 {
			disp.Body = append(disp.Body, fmt.Sprintf("(exit code %d)", out.ExitCode))
		} else {
			disp.Body = append(disp.Body, "(no output)")
		}
	}

	return disp
}

func (BashTool) Core() bool { return true }
func (BashTool) PromptSnippet() string {
	return "bash — Execute shell commands with optional timeout. Persistent session, chains with &&/;."
}
func (BashTool) PromptGuidelines() []string {
	return []string{
		"Use bash for terminal operations (git, npm, docker, build, test), NOT for file reading/writing/editing/searching",
		"Chain dependent commands with &&, run independent commands in parallel tool calls",
		"Always quote paths with spaces. Verify parent dirs exist before mkdir",
		"Only commit when explicitly asked. Only push when explicitly asked",
	}
}

func init() {
	Register(&BashTool{})
}

func (p bashParams) timeout() time.Duration {
	if p.Timeout > 0 {
		return time.Duration(p.Timeout) * time.Second
	}
	return 120 * time.Second
}

func (BashTool) StreamingExecute(ctx context.Context, args json.RawMessage, onChunk func(chunk string)) (string, error) {
	var params bashParams
	if err := json.Unmarshal(args, &params); err != nil {
		return "", err
	}

	ctx, cancel := context.WithTimeout(ctx, params.timeout())
	defer cancel()

	env := os.Environ()
	found := false
	for i, e := range env {
		if strings.HasPrefix(e, "TERM=") {
			env[i] = "TERM=xterm-256color"
			found = true
			break
		}
	}
	if !found {
		env = append(env, "TERM=xterm-256color")
	}

	cmd := exec.Command("bash", "-c", params.Command)
	cmd.Env = env

	ptmx, err := pty.Start(cmd)
	if err != nil {
		return "", fmt.Errorf("pty.Start: %w", err)
	}
	defer ptmx.Close()

	var stdout []byte
	var wg sync.WaitGroup

	buf := make([]byte, 4096)
	wg.Add(1)
	go func() {
		defer wg.Done()
		for {
			n, err := ptmx.Read(buf)
			if n > 0 {
				chunk := string(buf[:n])
				onChunk(chunk)
				stdout = append(stdout, buf[:n]...)
			}
			if err != nil {
				break
			}
		}
	}()

	done := make(chan struct{})
	go func() {
		cmd.Wait()
		close(done)
	}()

	select {
	case <-ctx.Done():
		cmd.Process.Kill()
		<-done
	case <-done:
	}

	wg.Wait()

	result := bashOutput{
		ExitCode: -1,
		Stdout:   string(stdout),
	}

	if cmd.ProcessState != nil {
		if ws, ok := cmd.ProcessState.Sys().(syscall.WaitStatus); ok {
			result.ExitCode = ws.ExitStatus()
		}
	}

	if ctx.Err() == context.DeadlineExceeded {
		result.TimedOut = true
	}

	out, _ := json.Marshal(result)
	return string(out), nil
}
