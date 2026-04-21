package tool

import (
	"context"
	"encoding/json"
	"fmt"
	"os/exec"
	"time"
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

	timeout := time.Duration(120) * time.Second
	if params.Timeout > 0 {
		timeout = time.Duration(params.Timeout) * time.Second
	}

	ctx, cancel := context.WithTimeout(ctx, timeout)
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
