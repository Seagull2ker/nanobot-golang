package tool

import (
	"context"
	"os/exec"
	"runtime"
	"time"
)

// SandboxOptions configures sandbox execution.
type SandboxOptions struct {
	CWD     string
	Env     []string
	Timeout time.Duration
}

// SandboxResult is the result of a sandboxed command execution.
type SandboxResult struct {
	Stdout   string
	Stderr   string
	ExitCode int
	TimedOut bool
}

func shellCommand() (string, string) {
	if runtime.GOOS == "windows" {
		return "cmd", "/c"
	}
	return "sh", "-c"
}

// LocalSandbox runs commands via os/exec with timeout.
type LocalSandbox struct {
	DefaultTimeout time.Duration
}

// NewLocalSandbox creates a LocalSandbox with an optional default timeout.
func NewLocalSandbox(timeout time.Duration) *LocalSandbox {
	if timeout == 0 {
		timeout = 120 * time.Second
	}
	return &LocalSandbox{DefaultTimeout: timeout}
}

// Run executes a command with the given options.
func (s *LocalSandbox) Run(ctx context.Context, command string, opts *SandboxOptions) (*SandboxResult, error) {
	if opts == nil {
		opts = &SandboxOptions{}
	}

	timeout := opts.Timeout
	if timeout == 0 {
		timeout = s.DefaultTimeout
	}

	ctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	shell, shellArg := shellCommand()
	cmd := exec.CommandContext(ctx, shell, shellArg, command)
	if opts.CWD != "" {
		cmd.Dir = opts.CWD
	}
	if len(opts.Env) > 0 {
		cmd.Env = opts.Env
	}

	stdout, err := cmd.Output()
	result := &SandboxResult{
		Stdout: string(stdout),
	}

	if err != nil {
		if ctx.Err() == context.DeadlineExceeded {
			result.TimedOut = true
			result.ExitCode = -1
			return result, nil
		}
		if exitErr, ok := err.(*exec.ExitError); ok {
			result.ExitCode = exitErr.ExitCode()
			result.Stderr = string(exitErr.Stderr)
			return result, nil
		}
		return nil, err
	}

	return result, nil
}
