package agent

import (
	"context"
	"time"
)

// DreamConfig configures the dream processing.
type DreamConfig struct {
	IntervalH        int  // Hours between dream runs (default 2)
	MaxBatchSize     int  // Max events per dream run (default 20)
	MaxIterations    int  // Max tool iterations for phase 2 (default 15)
	AnnotateLineAges bool // Whether to annotate MEMORY.md line ages
}

func DefaultDreamConfig() DreamConfig {
	return DreamConfig{
		IntervalH:        2,
		MaxBatchSize:     20,
		MaxIterations:    15,
		AnnotateLineAges: true,
	}
}

// Dream handles two-stage memory processing.
type Dream struct {
	store      *MemoryStore
	config     DreamConfig
	lastCursor int
	lastRun    time.Time
}

func NewDream(store *MemoryStore, cfg DreamConfig) *Dream {
	return &Dream{store: store, config: cfg}
}

// ShouldRun checks if enough time has passed since last dream run.
func (d *Dream) ShouldRun() bool {
	if d.config.IntervalH <= 0 {
		return false
	}
	return time.Since(d.lastRun) >= time.Duration(d.config.IntervalH)*time.Hour
}

// ProcessPhase1 reads the current MEMORY.md content for analysis.
func (d *Dream) ProcessPhase1(ctx context.Context) (string, error) {
	d.lastRun = time.Now()
	return d.store.ReadMemory(), nil
}

// ApplyMemory writes updated memory content after dream processing.
func (d *Dream) ApplyMemory(content string) error {
	return d.store.WriteMemory(content)
}
