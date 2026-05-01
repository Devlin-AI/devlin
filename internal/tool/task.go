package tool

import (
	"context"
	"encoding/json"
	"fmt"
	"sync"
)

type TaskTool struct{}

type taskParams struct {
	Description string       `json:"description"`
	Prompt      string       `json:"prompt"`
	Tasks       []taskItem   `json:"tasks,omitempty"`
}

type taskItem struct {
	Description string `json:"description"`
	Prompt      string `json:"prompt"`
}

const taskDescription = `Launch a new agent that is given a task to perform. The agent operates in a subagent session with its own conversation history and tool access.

Use this tool when you want to spawn a subagent to handle a specific, scoped task. The subagent has access to the same tools (except task nesting beyond depth limits).

When sending multiple independent tasks, use the "tasks" array parameter to run them in parallel.`

const taskParameters = `{
	"type": "object",
	"properties": {
		"description": {
			"type": "string",
			"description": "A short 3-5 word description of the task"
		},
		"prompt": {
			"type": "string",
			"description": "The full task instruction for the subagent"
		},
		"tasks": {
			"type": "array",
			"description": "Multiple independent tasks to run in parallel. If provided, 'description' and 'prompt' are ignored.",
			"items": {
				"type": "object",
				"properties": {
					"description": {
						"type": "string",
						"description": "A short 3-5 word description of the task"
					},
					"prompt": {
						"type": "string",
						"description": "The full task instruction for the subagent"
					}
				},
				"required": ["description", "prompt"]
			}
		}
	},
	"required": ["description", "prompt"]
}`

func (TaskTool) Name() string        { return "task" }
func (TaskTool) Description() string { return taskDescription }
func (TaskTool) Parameters() json.RawMessage {
	return json.RawMessage(taskParameters)
}

func (TaskTool) Execute(ctx context.Context, args json.RawMessage) (string, error) {
	var params taskParams
	if err := json.Unmarshal(args, &params); err != nil {
		return "", err
	}

	spawner := SpawnerFromContext(ctx)
	if spawner == nil {
		return "", fmt.Errorf("no session available for task execution")
	}

	if len(params.Tasks) > 1 {
		return executeBatchParallel(ctx, spawner, params.Tasks)
	}

	prompt := params.Prompt
	desc := params.Description
	if len(params.Tasks) == 1 {
		prompt = params.Tasks[0].Prompt
		desc = params.Tasks[0].Description
	}

	result, err := spawner.SpawnSubagent(ctx, desc, prompt)
	if err != nil {
		return fmt.Sprintf("error: %v", err), nil
	}

	return result, nil
}

func executeBatchParallel(ctx context.Context, spawner SessionSpawner, tasks []taskItem) (string, error) {
	type taskResult struct {
		description string
		result      string
		err         error
	}

	results := make([]taskResult, len(tasks))
	var wg sync.WaitGroup

	for i, t := range tasks {
		wg.Add(1)
		go func(idx int, task taskItem) {
			defer wg.Done()
			result, err := spawner.SpawnSubagent(ctx, task.Description, task.Prompt)
			results[idx] = taskResult{
				description: task.Description,
				result:      result,
				err:         err,
			}
		}(i, t)
	}

	wg.Wait()

	var output string
	for _, r := range results {
		if r.err != nil {
			output += fmt.Sprintf("## %s\nError: %v\n\n", r.description, r.err)
		} else {
			output += fmt.Sprintf("## %s\n%s\n\n", r.description, r.result)
		}
	}

	return output, nil
}

func (TaskTool) StreamingExecute(ctx context.Context, args json.RawMessage, onChunk func(chunk string)) (string, error) {
	return (TaskTool{}).Execute(ctx, args)
}

func (TaskTool) ConcurrencySafe() bool { return false }

func (TaskTool) Display(args, output string) ToolDisplay {
	var params taskParams
	if err := json.Unmarshal([]byte(args), &params); err != nil {
		return ToolDisplay{Body: []DisplayBlock{{Type: DisplayText, Content: output}}}
	}

	title := params.Description
	if title == "" && len(params.Tasks) > 0 {
		title = fmt.Sprintf("%d tasks in parallel", len(params.Tasks))
	}

	return ToolDisplay{
		Title:    title,
		Subtitle: "task",
		Body:     []DisplayBlock{{Type: DisplayText, Content: output}},
	}
}

func (TaskTool) Core() bool { return true }
func (TaskTool) PromptSnippet() string {
	return "task — Launch a subagent to handle a specific, scoped task. Supports parallel execution."
}
func (TaskTool) PromptGuidelines() []string {
	return []string{
		"Use task to delegate focused work to a subagent with its own context",
		"Each subagent has full tool access but limited nesting depth",
		"For multiple independent tasks, use the tasks array for parallel execution",
	}
}

func init() {
	Register(&TaskTool{})
}
