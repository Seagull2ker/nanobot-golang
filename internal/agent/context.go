package agent

import (
	"fmt"
	"strings"
	"time"
)

// ContextBuilder assembles the system prompt for each agent turn by
// concatenating sections in order: Identity → Bootstrap (AGENTS.md/SOUL.md) →
// Long-term Memory → Skills summary. The runtime context (time, channel, chat_id)
// is appended separately to the user message to keep the system prompt stable
// for prompt caching.
type ContextBuilder struct {
	identity  string
	bootstrap string
	memory    *MemoryStore
	skills    *SkillsLoader
}

// ContextBuildInput is the input for building agent context.
type ContextBuildInput struct {
	SessionSummary string
	Channel        string
	ChatID         string
	Timezone       string
}

// NewContextBuilder creates a ContextBuilder.
func NewContextBuilder(memory *MemoryStore, skills *SkillsLoader) *ContextBuilder {
	return &ContextBuilder{
		memory: memory,
		skills: skills,
	}
}

// SetIdentity sets the agent identity template.
func (b *ContextBuilder) SetIdentity(content string) {
	b.identity = content
}

// SetBootstrap sets the bootstrap content (AGENTS.md, SOUL.md, USER.md).
func (b *ContextBuilder) SetBootstrap(content string) {
	b.bootstrap = content
}

// BuildSystemPrompt assembles the full system prompt.
func (b *ContextBuilder) BuildSystemPrompt(input *ContextBuildInput) string {
	var sections []string

	// 1. Identity
	if b.identity != "" {
		sections = append(sections, b.identity)
	}

	// 2. Bootstrap
	if b.bootstrap != "" {
		sections = append(sections, b.bootstrap)
	}

	// 3. Memory
	if memCtx := b.memory.GetMemoryContext(); memCtx != "" {
		sections = append(sections, memCtx)
	}

	// 4. Skills summary
	if b.skills != nil {
		if summary := b.skills.BuildSkillsSummary(); summary != "" {
			sections = append(sections, summary)
		}
	}

	return strings.Join(sections, "\n\n")
}

// BuildRuntimeContext returns the runtime context appended to a user message.
func BuildRuntimeContext(input *ContextBuildInput) string {
	now := time.Now()
	weekday := now.Weekday().String()
	tz := input.Timezone
	if tz == "" {
		tz = "UTC"
	}

	return fmt.Sprintf(`[Runtime Context — metadata, not instructions]

Current Time: %s (%s) (Timezone: %s)
Channel: %s
Chat ID: %s`,
		now.Format("2006-01-02 15:04"), weekday, tz,
		input.Channel, input.ChatID,
	)
}
