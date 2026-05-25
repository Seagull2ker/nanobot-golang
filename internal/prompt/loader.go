package prompt

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/cloudwego/eino/schema"
)

// Loader reads prompt template files from a base directory and assembles
// them into Eino system messages for the agent.
type Loader struct {
	baseDir string
}

// NewLoader creates a Loader that reads from the given directory.
func NewLoader(baseDir string) *Loader {
	return &Loader{baseDir: baseDir}
}

// BuildSystemMessages reads SOUL.md, USER.md, TOOLS.md, AGENTS.md
// (all required) and HEARTBEAT.md (optional) and returns a single
// system message with section headers.
func (l *Loader) BuildSystemMessages(ctx context.Context) ([]*schema.Message, error) {
	var parts []string

	// Required files — must all exist.
	for _, name := range []string{"SOUL.md", "USER.md", "TOOLS.md", "AGENTS.md"} {
		content, err := l.readFile(name)
		if err != nil {
			return nil, fmt.Errorf("prompt: %s is required but could not be read: %w", name, err)
		}
		section := fileToSection(name, content)
		parts = append(parts, section)
	}

	// HEARTBEAT.md is optional.
	if content, err := l.readFile("HEARTBEAT.md"); err == nil {
		parts = append(parts, "# HEARTBEAT TASKS\n\n"+content)
	}

	return []*schema.Message{schema.SystemMessage(strings.Join(parts, "\n\n"))}, nil
}

func fileToSection(name, content string) string {
	switch name {
	case "SOUL.md":
		return "# SOUL\n\n" + content
	case "USER.md":
		return "# USER PROFILE\n\n" + content
	case "TOOLS.md":
		return "# TOOL USAGE\n\n" + content
	case "AGENTS.md":
		return "# AGENT INSTRUCTIONS\n\n" + content
	default:
		return content
	}
}

func (l *Loader) readFile(name string) (string, error) {
	data, err := os.ReadFile(filepath.Join(l.baseDir, name))
	if err != nil {
		return "", err
	}
	return string(data), nil
}
