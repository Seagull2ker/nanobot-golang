package tools

import (
	"context"
	"fmt"

	"github.com/Seagull2ker/nanobot-go/internal/tool"
)

// SubagentSpawner is the interface the subagent manager must satisfy.
type SubagentSpawner interface {
	Spawn(ctx context.Context, task, label, channel, chatID, sessionKey string) (string, error)
}

var spawner SubagentSpawner

// SetSubagentSpawner sets the backend for the spawn tool.
func SetSubagentSpawner(s SubagentSpawner) {
	spawner = s
}

type spawnTool struct{}

func init() { tool.Register(&spawnTool{}) }

func (t *spawnTool) Name() string          { return "spawn" }
func (t *spawnTool) ReadOnly() bool         { return false }
func (t *spawnTool) ConcurrencySafe() bool  { return true }
func (t *spawnTool) Exclusive() bool        { return false }

func (t *spawnTool) Description() string {
	return "Spawn a background subagent to work on a task independently. The subagent will report back when done."
}

func (t *spawnTool) Parameters() map[string]any {
	return map[string]any{
		"type": "object",
		"properties": map[string]any{
			"task": map[string]any{
				"type":        "string",
				"description": "The task description for the subagent",
			},
			"label": map[string]any{
				"type":        "string",
				"description": "Short label for the task (max 60 chars)",
			},
		},
		"required": []string{"task"},
	}
}

func (t *spawnTool) Execute(ctx context.Context, params map[string]any) (*tool.Result, error) {
	if spawner == nil {
		return &tool.Result{Content: "Subagent spawner not configured"}, nil
	}

	task, _ := params["task"].(string)
	if task == "" {
		return &tool.Result{Content: "Error: task is required"}, nil
	}

	label, _ := params["label"].(string)
	if label == "" {
		if len(task) > 60 {
			label = task[:60] + "..."
		} else {
			label = task
		}
	}
	if len(label) > 60 {
		label = label[:60]
	}

	taskID, err := spawner.Spawn(ctx, task, label, "", "", "")
	if err != nil {
		return &tool.Result{Content: fmt.Sprintf("Error spawning subagent: %v", err)}, nil
	}

	return &tool.Result{Content: fmt.Sprintf("Subagent spawned: %s", taskID)}, nil
}
