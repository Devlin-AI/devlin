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
	Action    string `json:"action"`
	Command   string `json:"command,omitempty"`
	TaskID    string `json:"task_id,omitempty"`
	SessionID string `json:"session_id,omitempty"`
	Input     string `json:"input,omitempty"`
	Timeout   int    `json:"timeout,omitempty"`

	Background        bool `json:"background,omitempty"`
	BackgroundTimeout int  `json:"background_timeout,omitempty"`
}

const processDescription = `Manages background tasks (bash commands and subagents).

Actions:
- spawn: Create a new background bash process. Returns task_id.
- write: Send input to a running bash process (for interactive prompts).
- read: Read current output from task buffer.
- wait: Wait for task to complete, return full output + exit code.
- kill: Terminate a running task.
- list: List all running, backgrounded, and recently finished tasks.

Use 'spawn' with background=true for long-running commands (dev servers).
Use 'write' to send input to interactive bash processes (passwords, y/n prompts).
Use 'wait' to block until a task completes.
Max 64 concurrent tasks. Finished tasks kept for 30 minutes.`

const processParameters = `{
	"type": "object",
	"properties": {
		"action": {
			"type": "string",
			"enum": ["spawn", "write", "read", "wait", "kill", "list"],
			"description": "Action to perform"
		},
		"command": { "type": "string", "description": "Command to execute (for spawn)" },
		"task_id": { "type": "string", "description": "Task ID (for write, read, wait, kill)" },
		"session_id": { "type": "string", "description": "Deprecated alias for task_id" },
		"input": { "type": "string", "description": "Input to send to bash process (for write)" },
		"timeout": { "type": "integer", "description": "Timeout in seconds for wait", "default": 120 },
		"background": { "type": "boolean", "description": "Spawn in background mode with auto-background timer", "default": true },
		"background_timeout": { "type": "integer", "description": "Auto-background after N seconds (0 uses global default)" }
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

	taskID := params.TaskID
	if taskID == "" && params.SessionID != "" {
		taskID = params.SessionID
	}

	switch params.Action {
	case "spawn":
		ps, err := reg.Spawn(params.Command, nil, process.WithAutoBackground(params.bgTimeout()))
		if err != nil {
			return "", err
		}

		output, _ := reg.Read(ps.ID)
		cleanOutput := stripANSI(output)

		result := map[string]interface{}{
			"task_id":    ps.ID,
			"type":       "bash",
			"command":    ps.Command,
			"status":     ps.Status,
			"pid":        ps.PID,
			"output":     cleanOutput,
			"output_raw": output,
		}
		b, _ := json.Marshal(result)
		return string(b), nil

	case "write":
		if taskID == "" {
			return "", fmt.Errorf("task_id required for write")
		}
		if err := reg.Write(taskID, params.Input); err != nil {
			return "", err
		}
		return fmt.Sprintf(`{"status": "written", "task_id": "%s"}`, taskID), nil

	case "read":
		if taskID == "" {
			return "", fmt.Errorf("task_id required for read")
		}
		output, err := reg.Read(taskID)
		if err != nil {
			return "", err
		}
		cleanOutput := stripANSI(output)
		return fmt.Sprintf(`{"output": %s}`, jsonString(cleanOutput)), nil

	case "wait":
		if taskID == "" {
			return "", fmt.Errorf("task_id required for wait")
		}
		ps, err := reg.Wait(taskID, params.timeout())
		if err != nil {
			return "", err
		}
		output, _ := reg.Read(ps.ID)
		cleanOutput := stripANSI(output)

		result := map[string]interface{}{
			"task_id":   ps.ID,
			"type":      string(ps.Type),
			"status":    ps.Status,
			"exit_code": ps.ExitCode,
			"output":    cleanOutput,
		}
		b, _ := json.Marshal(result)
		return string(b), nil

	case "kill":
		if taskID == "" {
			return "", fmt.Errorf("task_id required for kill")
		}
		if err := reg.Kill(taskID); err != nil {
			return "", err
		}
		return fmt.Sprintf(`{"status": "killed", "task_id": "%s"}`, taskID), nil

	case "list":
		list := reg.List()
		var items []map[string]interface{}
		for _, ps := range list {
			item := map[string]interface{}{
				"task_id":    ps.ID,
				"type":       string(ps.Type),
				"status":     ps.Status,
				"created_at": ps.CreatedAt.Unix(),
			}
			if ps.Type == process.TaskTypeBash {
				item["command"] = ps.Command
				item["pid"] = ps.PID
				item["exit_code"] = ps.ExitCode
			} else {
				item["description"] = ps.Description
			}
			items = append(items, item)
		}
		b, _ := json.Marshal(map[string]interface{}{"tasks": items})
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
	return "process — Manage background tasks with spawn, write, read, wait, kill, list"
}

func (ProcessTool) PromptGuidelines() []string {
	return []string{
		"Use 'spawn' for long-running commands (dev servers) — returns task_id",
		"Use 'write' to send input to interactive bash processes (passwords, y/n prompts)",
		"Use 'wait' to block until a task completes",
		"Use 'list' to see all running and backgrounded tasks",
		"Max 64 concurrent tasks",
	}
}

func (p processParams) timeout() time.Duration {
	if p.Timeout > 0 {
		return time.Duration(p.Timeout) * time.Second
	}
	return 120 * time.Second
}

func (p processParams) bgTimeout() time.Duration {
	if p.BackgroundTimeout > 0 {
		return time.Duration(p.BackgroundTimeout) * time.Second
	}
	return process.DefaultBackgroundTimeout
}

func jsonString(s string) string {
	b, _ := json.Marshal(s)
	return string(b)
}

func init() {
	Register(&ProcessTool{})
}
