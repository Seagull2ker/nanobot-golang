package provider

import (
	"context"
	"sync"
	"time"

	"github.com/cloudwego/eino/components/model"
	"github.com/cloudwego/eino/schema"
	"github.com/Seagull2ker/nanobot-go/internal/errors"
)

// FactoryFunc creates a ChatModelAdapter from a spec name.
type FactoryFunc func(specName string) (ChatModelAdapter, error)

type fallbackAdapter struct {
	primary   ChatModelAdapter
	fallbacks []string // spec names of fallback providers
	factory   FactoryFunc

	mu               sync.Mutex
	consecutiveFails int
	cooldownUntil    time.Time
}

const (
	circuitBreakerThreshold = 3
	circuitBreakerCooldown  = 60 * time.Second
)

// WithFallback wraps a ChatModelAdapter with fallback capabilities.
func WithFallback(primary ChatModelAdapter, fallbackSpecs []string, factory FactoryFunc) ChatModelAdapter {
	return &fallbackAdapter{
		primary:   primary,
		fallbacks: fallbackSpecs,
		factory:   factory,
	}
}

func (f *fallbackAdapter) BindTools(tools []*schema.ToolInfo) error {
	return f.primary.BindTools(tools)
}

func (f *fallbackAdapter) GetDefaultModel() string {
	return f.primary.GetDefaultModel()
}

func (f *fallbackAdapter) SupportsThinking() bool {
	return f.primary.SupportsThinking()
}

func (f *fallbackAdapter) Generate(ctx context.Context, input []*schema.Message, opts ...model.Option) (*schema.Message, error) {
	resp, err := f.primary.Generate(ctx, input, opts...)
	if err == nil {
		f.recordSuccess()
		return resp, nil
	}

	if !f.shouldFallback(err) {
		return nil, err
	}

	f.recordFailure()

	for _, specName := range f.fallbacks {
		if !f.primaryAvailable() {
			continue
		}

		fallback, fbErr := f.factory(specName)
		if fbErr != nil {
			continue
		}

		resp, retryErr := fallback.Generate(ctx, input, opts...)
		if retryErr == nil {
			return resp, nil
		}

		if !fallbackable(retryErr) {
			return nil, retryErr
		}
	}

	return nil, err
}

func (f *fallbackAdapter) Stream(ctx context.Context, input []*schema.Message, opts ...model.Option) (*schema.StreamReader[*schema.Message], error) {
	return f.primary.Stream(ctx, input, opts...)
}

func (f *fallbackAdapter) recordSuccess() {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.consecutiveFails = 0
	f.cooldownUntil = time.Time{}
}

func (f *fallbackAdapter) recordFailure() {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.consecutiveFails++
	if f.consecutiveFails >= circuitBreakerThreshold {
		f.cooldownUntil = time.Now().Add(circuitBreakerCooldown)
	}
}

func (f *fallbackAdapter) primaryAvailable() bool {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.consecutiveFails >= circuitBreakerThreshold {
		if time.Now().Before(f.cooldownUntil) {
			return false
		}
		// Half-open: allow one probe.
		f.consecutiveFails = 0
	}
	return true
}

func (f *fallbackAdapter) shouldFallback(err error) bool {
	kind := errors.KindOf(err)
	switch kind {
	case errors.KindUnauthorized:
		return false
	case errors.KindNotFound:
		return false
	case errors.KindInvalid:
		return false
	default:
		return true
	}
}

func fallbackable(err error) bool {
	return errors.Retryable(err)
}
