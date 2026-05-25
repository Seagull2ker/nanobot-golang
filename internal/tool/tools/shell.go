package tools

import (
	"context"
	"fmt"
	"regexp"
	"strings"
	"time"

	"github.com/Seagull2ker/nanobot-go/internal/tool"
)

type execTool struct {
	sandbox   *tool.LocalSandbox
	workspace string
}

func init() {
	tool.Register(&execTool{sandbox: tool.NewLocalSandbox(120 * time.Second)})
}

func (t *execTool) Name() string { return "exec" }

func (t *execTool) Description() string {
	return "Execute a shell command in a sandboxed environment. Returns stdout, stderr, and exit code."
}

func (t *execTool) Parameters() map[string]any {
	return map[string]any{
		"type": "object",
		"properties": map[string]any{
			"command": map[string]any{
				"type":        "string",
				"description": "The shell command to execute",
			},
			"workdir": map[string]any{
				"type":        "string",
				"description": "Working directory for the command (relative to workspace)",
			},
			"timeout": map[string]any{
				"type":        "integer",
				"description": "Timeout in seconds (default 120, max 600)",
				"minimum":     1,
				"maximum":     600,
			},
			"background": map[string]any{
				"type":        "boolean",
				"description": "Run command in background (returns immediately)",
			},
		},
		"required": []string{"command"},
	}
}

func (t *execTool) ReadOnly() bool        { return false }
func (t *execTool) ConcurrencySafe() bool { return false }
func (t *execTool) Exclusive() bool       { return true }

func (t *execTool) Execute(ctx context.Context, params map[string]any) (*tool.Result, error) {
	command, _ := params["command"].(string)
	if command == "" {
		return &tool.Result{Content: "Error: command is required"}, nil
	}

	if err := guardCommand(command); err != nil {
		return &tool.Result{Content: fmt.Sprintf("Error: %v", err)}, nil
	}

	workdir, _ := params["workdir"].(string)
	timeoutSec := 120
	if sec, ok := params["timeout"].(float64); ok {
		timeoutSec = int(sec)
	}
	if timeoutSec > 600 {
		timeoutSec = 600
	}

	cwd := t.workspace
	if workdir != "" {
		cwd = workdir
	}

	result, err := t.sandbox.Run(ctx, command, &tool.SandboxOptions{
		CWD:     cwd,
		Timeout: time.Duration(timeoutSec) * time.Second,
	})
	if err != nil {
		return &tool.Result{Content: fmt.Sprintf("Error: %v", err)}, nil
	}

	var parts []string
	if result.Stdout != "" {
		parts = append(parts, result.Stdout)
	}
	if result.Stderr != "" {
		parts = append(parts, fmt.Sprintf("[stderr]\n%s", result.Stderr))
	}
	if result.TimedOut {
		parts = append(parts, "[Process timed out]")
	} else if result.ExitCode != 0 {
		parts = append(parts, fmt.Sprintf("[Exit code: %d]", result.ExitCode))
	}

	return &tool.Result{Content: strings.Join(parts, "\n")}, nil
}

// denyPatterns blocks known-dangerous shell commands before execution.
//
// Design decision: regex blocklist rather than command allowlist.
// Allowlists are more secure but break legitimate workflows (e.g., package managers,
// build tools, git hooks). A blocklist catches the catastrophic cases while letting
// the agent remain useful. The Python nanobot implementation uses the same approach
// (base.py:_guard_command with allow_patterns/deny_patterns).
//
// The error message includes "hard policy boundary — do not retry with shell tricks"
// because LLMs will otherwise try encoding tricks (base64, xxd, eval) to bypass
// pattern matching. This phrasing has proven effective in practice at stopping retry loops.
var denyPatterns = []*regexp.Regexp{
	regexp.MustCompile(`(?i)\brm\s+-rf\b`),
	regexp.MustCompile(`(?i)\brm\s+-r\s+/`),
	regexp.MustCompile(`(?i)\bdel\s+/[fq]\b`),
	regexp.MustCompile(`(?i)\brmdir\s+/[sq]\b`),
	regexp.MustCompile(`(?i)\bformat\b`),
	regexp.MustCompile(`(?i)\bmkfs\.`),
	regexp.MustCompile(`(?i)\bdiskpart\b`),
	regexp.MustCompile(`(?i)\bdd\s+if=`),
	regexp.MustCompile(`(?i)>\s*/dev/sd`),
	regexp.MustCompile(`(?i)\bshutdown\b`),
	regexp.MustCompile(`(?i)\breboot\b`),
	regexp.MustCompile(`(?i)\bpoweroff\b`),
	regexp.MustCompile(`(?i):\(\)\s*\{`), // fork bomb
	regexp.MustCompile(`(?i)\bchmod\s+777\b`),
	regexp.MustCompile(`(?i)\bcurl.*\|\s*(ba)?sh\b`),
	regexp.MustCompile(`(?i)\bwget.*\|\s*(ba)?sh\b`),
}

// internalURLPattern catches private-address URLs embedded in shell commands.
// Separate from web.go's DNS-level SSRF: shell tools bypass DNS via raw IPs,
// so regex is the only pre-execution check available here.
var internalURLPattern = regexp.MustCompile(`https?://(localhost|127\.0\.0\.1|0\.0\.0\.0|\[::1\]|10\.\d+|172\.(1[6-9]|2\d|3[01])\.|192\.168\.|169\.254\.)`)

func guardCommand(command string) error {
	for _, p := range denyPatterns {
		if p.MatchString(command) {
			return fmt.Errorf("Command denied by security policy (matched: %s). This is a hard policy boundary — do not retry with shell tricks.", p.String())
		}
	}

	if internalURLPattern.MatchString(command) {
		return fmt.Errorf("Command contains a URL targeting an internal/private address. This is a security boundary.")
	}

	return nil
}
