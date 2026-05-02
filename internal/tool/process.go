package tool

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	"github.com/devlin-ai/devlin/internal/process"
)

type ProcessTool struct{}

type processParams struct {
	Action     string `json:"action"`
	Command    string `json:"command,omitempty"`
	SessionID  string `json:"session_id,omitempty"`
	Input      string `json:"input,omitempty"`
	Background bool   `json:"background,omitempty"`
	Timeout    int    `json:"timeout,omitempty"`
}

const processDescription = `Manages background processes with interactive capabilities.

Actions:
- spawn: Create a new background process. Returns session_id. If background=false, waits for completion.
- write: Send input to a running process (for interactive prompts, passwords, y/n).
- read: Read current output from process buffer.
- wait: Wait for process to complete, return full output + exit code.
- kill: Terminate a running process.
- list: List all running and recently finished processes.

For interactive prompts (passwords, y/n), use 'write' action.
For long-running commands (dev servers), use background=true and poll/wait to check status.
Max 64 concurrent processes. Finished processes kept for 30 minutes.`

const processParameters = `{
	"type": "object",
	"properties": {
		"action": {
			"type": "string",
			"enum": ["spawn", "write", "read", "wait", "kill", "list"],
			"description": "Action to perform"
		},
		"command": { "type": "string", "description": "Command to execute (for spawn)" },
		"session_id": { "type": "string", "description": "Process session ID" },
		"input": { "type": "string", "description": "Input to send to process (for write)" },
		"background": { "type": "boolean", "description": "Run in background, return immediately", "default": false },
		"timeout": { "type": "integer", "description": "Timeout in seconds for wait/spawn", "default": 120 }
	},
	"required": ["action"]
}`

func (ProcessTool) Name() string { return "process" }

func (ProcessTool) Description() string { return processDescription }

func (ProcessTool) Parameters() json.RawMessage {
	return json.RawMessage(processParameters)
}

func (p ProcessTool) Execute(ctx context.Context, args json.RawMessage) (string, error) {
	var params processParams
	if err := json.Unmarshal(args, &params); err != nil {
		return "", err
	}

	_ = ctx

	reg := process.DefaultRegistry

	switch params.Action {
	case "spawn":
		ps, err := reg.Spawn(params.Command, nil)
		if err != nil {
			return "", err
		}

		if !params.Background {
			timeout := params.timeout()
			if timeout > 0 {
				var cancel context.CancelFunc
				ctx, cancel = context.WithTimeout(ctx, timeout)
				defer cancel()
			}
			ps, err = reg.Wait(ps.ID, params.timeout())
			if err != nil {
				return "", err
			}
		}

		output, _ := reg.Read(ps.ID)
		cleanOutput := stripANSI(output)

		result := map[string]interface{}{
			"session_id": ps.ID,
			"command":    ps.Command,
			"status":     ps.Status,
			"exit_code":  ps.ExitCode,
			"output":     cleanOutput,
			"output_raw": output,
		}
		b, _ := json.Marshal(result)
		return string(b), nil

	case "write":
		if params.SessionID == "" {
			return "", fmt.Errorf("session_id required for write")
		}
		if err := reg.Write(params.SessionID, params.Input); err != nil {
			return "", err
		}
		return fmt.Sprintf(`{"status": "written", "session_id": "%s"}`, params.SessionID), nil

	case "read":
		if params.SessionID == "" {
			return "", fmt.Errorf("session_id required for read")
		}
		output, err := reg.Read(params.SessionID)
		if err != nil {
			return "", err
		}
		cleanOutput := stripANSI(output)
		return fmt.Sprintf(`{"output": %s}`, jsonString(cleanOutput)), nil

	case "wait":
		if params.SessionID == "" {
			return "", fmt.Errorf("session_id required for wait")
		}
		ps, err := reg.Wait(params.SessionID, params.timeout())
		if err != nil {
			return "", err
		}
		output, _ := reg.Read(ps.ID)
		cleanOutput := stripANSI(output)

		result := map[string]interface{}{
			"session_id": ps.ID,
			"status":     ps.Status,
			"exit_code":  ps.ExitCode,
			"output":     cleanOutput,
		}
		b, _ := json.Marshal(result)
		return string(b), nil

	case "kill":
		if params.SessionID == "" {
			return "", fmt.Errorf("session_id required for kill")
		}
		if err := reg.Kill(params.SessionID); err != nil {
			return "", err
		}
		return fmt.Sprintf(`{"status": "killed", "session_id": "%s"}`, params.SessionID), nil

	case "list":
		list := reg.List()
		var items []map[string]interface{}
		for _, ps := range list {
			items = append(items, map[string]interface{}{
				"session_id": ps.ID,
				"command":    ps.Command,
				"pid":        ps.PID,
				"status":     ps.Status,
				"exit_code":  ps.ExitCode,
				"created_at": ps.CreatedAt.Unix(),
			})
		}
		b, _ := json.Marshal(map[string]interface{}{"processes": items})
		return string(b), nil

	default:
		return "", fmt.Errorf("unknown action: %s", params.Action)
	}
}

func (p ProcessTool) Display(args, output string) ToolDisplay {
	var params processParams
	json.Unmarshal([]byte(args), &params)

	disp := ToolDisplay{Title: "process " + params.Action}

	if params.Action == "spawn" {
		disp.Title = params.Command
	}

	if output != "" {
		disp.Body = []DisplayBlock{{Type: DisplayText, Content: output}}
	}

	return disp
}

func (ProcessTool) Core() bool { return true }

func (ProcessTool) PromptSnippet() string {
	return "process — Manage background processes with spawn, write, read, wait, kill, list"
}

func (ProcessTool) PromptGuidelines() []string {
	return []string{
		"Use 'spawn' with background=true for long-running commands (dev servers)",
		"Use 'write' to send input to interactive processes (passwords, y/n prompts)",
		"Use 'wait' to get full output after process completes",
		"Max 64 concurrent processes",
	}
}

func (p processParams) timeout() time.Duration {
	if p.Timeout > 0 {
		return time.Duration(p.Timeout) * time.Second
	}
	return 120 * time.Second
}

func jsonString(s string) string {
	b, _ := json.Marshal(s)
	return string(b)
}

func (p ProcessTool) StreamingExecute(ctx context.Context, args json.RawMessage, onChunk func(chunk string)) (string, error) {
	var params processParams
	if err := json.Unmarshal(args, &params); err != nil {
		return "", err
	}

	if params.Action != "spawn" {
		return p.Execute(ctx, args)
	}

	reg := process.DefaultRegistry

	ps, err := reg.Spawn(params.Command, onChunk)
	if err != nil {
		return "", err
	}

	if !params.Background {
		ps, err = reg.Wait(ps.ID, params.timeout())
		if err != nil {
			return "", err
		}
	}

	output, _ := reg.Read(ps.ID)
	cleanOutput := stripANSI(output)

	result := map[string]interface{}{
		"session_id": ps.ID,
		"command":    ps.Command,
		"status":     ps.Status,
		"exit_code":  ps.ExitCode,
		"output":     cleanOutput,
		"output_raw": output,
	}
	b, _ := json.Marshal(result)
	return string(b), nil
}

func init() {
	Register(&ProcessTool{})
}
