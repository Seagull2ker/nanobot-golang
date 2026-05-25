package app

import (
	"context"
	"log/slog"
	"os"
	"os/signal"
	"sync"
	"time"
)

// RuntimeComponents collects stoppable subsystems for orderly shutdown.
type RuntimeComponents struct {
	Feishu interface {
		Stop(ctx context.Context) error
	}
	Heartbeat interface{ Stop() }
	Cron      interface{ Stop() }
	API       interface {
		Stop(ctx context.Context) error
	}

	ComponentStopTimeout time.Duration
}

// GracefulShutdownConfig configures the multi-phase shutdown sequence.
type GracefulShutdownConfig struct {
	SigCh <-chan os.Signal

	CancelRoot         context.CancelFunc
	CancelAgentTasks   interface{ CancelAll() int }
	CancelSubagentTask interface{ CancelAll() int }
	CloseInbound       func()

	WaitGroup       *sync.WaitGroup
	ShutdownTimeout time.Duration
	Components      RuntimeComponents
}

// NewSignalChannel creates a buffered channel subscribed to SIGINT and SIGTERM.
func NewSignalChannel() chan os.Signal {
	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, os.Interrupt)
	return sigCh
}

// StartGracefulShutdown runs the multi-phase shutdown sequence in a goroutine.
// Returns a channel that closes when shutdown is complete.
func StartGracefulShutdown(cfg GracefulShutdownConfig) <-chan struct{} {
	shutdownDone := make(chan struct{})
	go func() {
		defer close(shutdownDone)

		sig := <-cfg.SigCh
		slog.Info("shutdown signal received", "signal", sig.String())

		// Phase 1: Cancel root context.
		if cfg.CancelRoot != nil {
			cfg.CancelRoot()
		}

		// Phase 2: Cancel all agent and subagent tasks.
		agentCancelled := 0
		if cfg.CancelAgentTasks != nil {
			agentCancelled = cfg.CancelAgentTasks.CancelAll()
		}
		subCancelled := 0
		if cfg.CancelSubagentTask != nil {
			subCancelled = cfg.CancelSubagentTask.CancelAll()
		}
		slog.Info("tasks cancelled", "agent", agentCancelled, "subagent", subCancelled)

		// Phase 3: Close inbound to stop new message processing.
		if cfg.CloseInbound != nil {
			cfg.CloseInbound()
		}

		// Phase 4: Wait for in-flight requests with timeout.
		done := make(chan struct{})
		go func() {
			if cfg.WaitGroup != nil {
				cfg.WaitGroup.Wait()
			}
			close(done)
		}()

		select {
		case <-done:
			slog.Info("in-flight requests completed")
		case <-time.After(cfg.ShutdownTimeout):
			slog.Warn("shutdown timed out, forcing exit")
		}

		// Phase 5: Stop runtime components.
		stopTimeout := cfg.Components.ComponentStopTimeout
		if stopTimeout == 0 {
			stopTimeout = 5 * time.Second
		}

		stopComponents(cfg.Components, stopTimeout)
		slog.Info("gateway shutdown complete")
	}()
	return shutdownDone
}

func stopComponents(c RuntimeComponents, timeout time.Duration) {
	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()

	if c.API != nil {
		if err := c.API.Stop(ctx); err != nil {
			slog.Warn("api stop", "error", err)
		}
	}
	if c.Feishu != nil {
		if err := c.Feishu.Stop(ctx); err != nil {
			slog.Warn("feishu stop", "error", err)
		}
	}
	if c.Heartbeat != nil {
		c.Heartbeat.Stop()
	}
	if c.Cron != nil {
		c.Cron.Stop()
	}
}
