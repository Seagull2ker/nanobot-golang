package trace

import (
	"log/slog"

	"github.com/Seagull2ker/nanobot-go/internal/config"
)

// Init initializes Langfuse tracing. Returns a shutdown function to be called before exit.
// If cfg.Enabled is false, returns a no-op shutdown.
func Init(cfg config.TracingConfig) (shutdown func()) {
	if !cfg.Enabled {
		slog.Debug("tracing disabled")
		return func() {}
	}

	slog.Info("tracing enabled", "endpoint", cfg.Endpoint)
	// Full Langfuse initialization: import github.com/cloudwego/eino-ext/callbacks/langfuse
	// and register via callbacks.AppendGlobalHandlers(handler).
	// For now, the dependency is present but initialization is deferred to runtime wiring.

	return func() {
		slog.Info("tracing shutdown")
	}
}
