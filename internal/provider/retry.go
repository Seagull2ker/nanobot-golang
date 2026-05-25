package provider

import (
	"context"
	"time"

	"github.com/Seagull2ker/nanobot-go/internal/errors"
	"github.com/cloudwego/eino/components/model"
	"github.com/cloudwego/eino/schema"
)

type retryAdapter struct {
	inner ChatModelAdapter
	mode  string // "standard" | "persistent"
}

var (
	retryDelays              = []time.Duration{1 * time.Second, 2 * time.Second, 4 * time.Second}
	persistentMaxDelay       = 60 * time.Second
	persistentIdenticalLimit = 10
)

// WithRetry wraps a ChatModelAdapter with retry logic.
func WithRetry(adapter ChatModelAdapter, mode string) ChatModelAdapter {
	if mode != "standard" && mode != "persistent" {
		mode = "standard"
	}
	return &retryAdapter{inner: adapter, mode: mode}
}

func (r *retryAdapter) BindTools(tools []*schema.ToolInfo) error {
	return r.inner.BindTools(tools)
}

func (r *retryAdapter) GetDefaultModel() string {
	return r.inner.GetDefaultModel()
}

func (r *retryAdapter) SupportsThinking() bool {
	return r.inner.SupportsThinking()
}

func (r *retryAdapter) Generate(ctx context.Context, input []*schema.Message, opts ...model.Option) (*schema.Message, error) {
	var lastErr error

	attempts := 1 + len(retryDelays) // 4 tries for standard
	delayIdx := 0
	identicalCount := 0

	for i := 0; i < attempts || r.mode == "persistent"; i++ {
		resp, err := r.inner.Generate(ctx, input, opts...)
		if err == nil {
			return resp, nil
		}

		if !retryable(err) {
			return nil, err
		}

		if lastErr != nil && err.Error() == lastErr.Error() {
			identicalCount++
		} else {
			identicalCount = 0
		}
		lastErr = err

		if r.mode == "persistent" && identicalCount >= persistentIdenticalLimit {
			return nil, err
		}

		delay := time.Duration(0)
		if r.mode == "standard" {
			if delayIdx < len(retryDelays) {
				delay = retryDelays[delayIdx]
				delayIdx++
			} else {
				return nil, err
			}
		} else {
			delay = time.Duration(1<<uint(i)) * time.Second
			if delay > persistentMaxDelay {
				delay = persistentMaxDelay
			}
		}

		select {
		case <-ctx.Done():
			return nil, ctx.Err()
		case <-time.After(delay):
		}
	}

	return nil, lastErr
}

func (r *retryAdapter) Stream(ctx context.Context, input []*schema.Message, opts ...model.Option) (*schema.StreamReader[*schema.Message], error) {
	return r.inner.Stream(ctx, input, opts...)
}

func retryable(err error) bool {
	kind := errors.KindOf(err)
	switch kind {
	case errors.KindRateLimited, errors.KindUnavailable, errors.KindTimeout, errors.KindNetwork:
		return true
	default:
		return false
	}
}
