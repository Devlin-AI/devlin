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

const bashDescription = `Execute a bash command in a persistent shell session. Returns stdout, stderr, and exit code.`

const bashParameters = `{
	"type": "object",
	"properties": {
		"command": {
			"type": "string",
			"description": "The bash command to execute"
		},
		"timeout": {
			"type": "integer",
			"description": "Optional timeout in seconds (default 120)"
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
