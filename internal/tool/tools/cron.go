package tools

import (
	"context"
	"fmt"
	"strings"

	"github.com/Seagull2ker/nanobot-go/internal/tool"
)

// CronService is the minimal interface the cron tool needs.
type CronService interface {
	AddJob(schedule CronSchedule) (*CronJob, error)
	ListJobs() []*CronJob
	RemoveJob(id string) error
}

// CronSchedule matches the cron package's Schedule.
type CronSchedule struct {
	Kind         string `json:"kind"`
	EverySeconds int    `json:"every_seconds,omitempty"`
	CronExpr     string `json:"cron_expr,omitempty"`
	At           string `json:"at,omitempty"`
	TZ           string `json:"tz,omitempty"`
	Message      string `json:"message"`
	Name         string `json:"name,omitempty"`
	Channel      string `json:"channel,omitempty"`
	ChatID       string `json:"chat_id,omitempty"`
	Deliver      bool   `json:"deliver"`
}

// CronJob represents a registered cron job.
type CronJob struct {
	ID       string       `json:"id"`
	Schedule CronSchedule `json:"schedule"`
}

var cronSvc CronService

// SetCronService sets the backend used by the cron tool.
func SetCronService(svc CronService) {
	cronSvc = svc
}

type cronTool struct{}

func init() { tool.Register(&cronTool{}) }

func (t *cronTool) Name() string          { return "cron" }
func (t *cronTool) ReadOnly() bool         { return false }
func (t *cronTool) ConcurrencySafe() bool  { return true }
func (t *cronTool) Exclusive() bool        { return false }

func (t *cronTool) Description() string {
	return "Manage scheduled tasks. Actions: add (schedule a job), list (show all jobs), remove (delete a job by ID)."
}

func (t *cronTool) Parameters() map[string]any {
	return map[string]any{
		"type": "object",
		"properties": map[string]any{
			"action": map[string]any{
				"type":        "string",
				"description": "Action: add, list, or remove",
				"enum":        []string{"add", "list", "remove"},
			},
			"message": map[string]any{
				"type":        "string",
				"description": "Message to deliver (for add action)",
			},
			"every_seconds": map[string]any{
				"type":        "integer",
				"description": "Repeat interval in seconds (for add action)",
			},
			"cron_expr": map[string]any{
				"type":        "string",
				"description": "Cron expression for scheduling (for add action)",
			},
			"at": map[string]any{
				"type":        "string",
				"description": "ISO 8601 datetime for one-shot job (for add action)",
			},
			"job_id": map[string]any{
				"type":        "string",
				"description": "Job ID to remove (for remove action; use list first to find IDs)",
			},
		},
		"required": []string{"action"},
	}
}

func (t *cronTool) Execute(ctx context.Context, params map[string]any) (*tool.Result, error) {
	action, _ := params["action"].(string)
	switch action {
	case "list":
		return t.listJobs()
	case "add":
		return t.addJob(params)
	case "remove":
		return t.removeJob(params)
	default:
		return &tool.Result{Content: "Error: action must be add, list, or remove"}, nil
	}
}

func (t *cronTool) listJobs() (*tool.Result, error) {
	if cronSvc == nil {
		return &tool.Result{Content: "Cron service not configured"}, nil
	}
	jobs := cronSvc.ListJobs()
	if len(jobs) == 0 {
		return &tool.Result{Content: "No scheduled jobs."}, nil
	}
	var lines []string
	for _, j := range jobs {
		lines = append(lines, fmt.Sprintf("- %s: %s (kind=%s)", j.ID, j.Schedule.Message, j.Schedule.Kind))
	}
	return &tool.Result{Content: strings.Join(lines, "\n")}, nil
}

func (t *cronTool) addJob(params map[string]any) (*tool.Result, error) {
	if cronSvc == nil {
		return &tool.Result{Content: "Cron service not configured"}, nil
	}

	msg, _ := params["message"].(string)
	if msg == "" {
		return &tool.Result{Content: "Error: message is required for add action"}, nil
	}

	schedule := CronSchedule{
		Message: msg,
		Deliver: true,
	}

	if sec, ok := params["every_seconds"].(float64); ok {
		schedule.Kind = "every"
		schedule.EverySeconds = int(sec)
	} else if expr, ok := params["cron_expr"].(string); ok && expr != "" {
		schedule.Kind = "cron"
		schedule.CronExpr = expr
	} else if at, ok := params["at"].(string); ok && at != "" {
		schedule.Kind = "at"
		schedule.At = at
	} else {
		return &tool.Result{Content: "Error: specify every_seconds, cron_expr, or at for scheduling"}, nil
	}

	job, err := cronSvc.AddJob(schedule)
	if err != nil {
		return &tool.Result{Content: fmt.Sprintf("Error: %v", err)}, nil
	}
	return &tool.Result{Content: fmt.Sprintf("Job scheduled: %s", job.ID)}, nil
}

func (t *cronTool) removeJob(params map[string]any) (*tool.Result, error) {
	if cronSvc == nil {
		return &tool.Result{Content: "Cron service not configured"}, nil
	}

	jobID, _ := params["job_id"].(string)
	if jobID == "" {
		return &tool.Result{Content: "Error: job_id is required for remove action. Use list first to find IDs."}, nil
	}

	if err := cronSvc.RemoveJob(jobID); err != nil {
		return &tool.Result{Content: fmt.Sprintf("Error: %v", err)}, nil
	}
	return &tool.Result{Content: fmt.Sprintf("Job %s removed", jobID)}, nil
}
