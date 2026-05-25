package types

import "time"

// Session represents a conversation session.
type Session struct {
	Key              string
	Messages         []map[string]any
	CreatedAt        time.Time
	UpdatedAt        time.Time
	Metadata         map[string]any
	LastConsolidated int
	LastSummary      string
}
