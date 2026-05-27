package trace

import (
	"fmt"
	"log/slog"

	"github.com/Seagull2ker/nanobot-go/internal/config"
	"github.com/cloudwego/eino-ext/callbacks/langfuse"
	"github.com/cloudwego/eino/callbacks"
)

// Init initializes Langfuse tracing. Returns a shutdown function to be called before exit.
// If cfg.Enabled is false, returns a no-op shutdown.
func Init(cfg config.TracingConfig) (shutdown func(), err error) {
	if !cfg.Enabled {
		return func() {}, nil
	}

	if cfg.Endpoint == "" || cfg.PublicKey == "" || cfg.SecretKey == "" {
		return nil, fmt.Errorf("trace: enabled but missing required fields (endpoint, publicKey, secretKey)")
	}

	handler, flusher := langfuse.NewLangfuseHandler(&langfuse.Config{
		Host:      cfg.Endpoint,
		PublicKey: cfg.PublicKey,
		SecretKey: cfg.SecretKey,
	})

	callbacks.AppendGlobalHandlers(handler)

	slog.Info("Tracing enabled", "module", "trace", "endpoint", cfg.Endpoint)

	return flusher, nil
}
