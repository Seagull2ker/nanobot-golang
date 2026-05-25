package workspace

import (
	"embed"
	"log/slog"
	"os"
	"path/filepath"
)

//go:embed templates/*.md
var embeddedTemplates embed.FS

// TemplateFiles lists the prompt template files synced to the prompts directory.
var TemplateFiles = []string{
	"SOUL.md",
	"USER.md",
	"TOOLS.md",
	"AGENTS.md",
	"HEARTBEAT.md",
}

// SyncTemplates ensures all template files exist in targetDir.
// Existing files are never overwritten — this preserves user customizations.
// Missing files are created from the embedded templates baked into the binary.
func SyncTemplates(targetDir string) error {
	if err := os.MkdirAll(targetDir, 0755); err != nil {
		return err
	}

	for _, name := range TemplateFiles {
		dst := filepath.Join(targetDir, name)
		if _, err := os.Stat(dst); err == nil {
			continue // never overwrite
		}

		content, err := embeddedTemplates.ReadFile("templates/" + name)
		if err != nil {
			slog.Warn("embedded template missing", "file", name)
			continue
		}

		if err := os.WriteFile(dst, content, 0644); err != nil {
			return err
		}
		slog.Info("synced template", "file", dst)
	}
	return nil
}

// MemoryTemplates returns default content for memory files.
type MemoryTemplates struct {
	MEMORY  string
	History string
}

// DefaultMemoryTemplates returns initial content for MEMORY.md and HISTORY.md.
func DefaultMemoryTemplates() MemoryTemplates {
	return MemoryTemplates{
		MEMORY: "# Nanobot's Long-term Memory\n\n" +
			"Your long-term memory file. The agent will read this on every session\n" +
			"and update it with important facts it learns about you and your projects.\n\n" +
			"## About You\n\n(Edit this section with information about yourself)\n\n" +
			"## Projects\n\n(Describe your current projects)\n\n" +
			"## Preferences\n\n(Your preferences for how the agent should work)\n",
		History: "# History\n\n" +
			"Append-only event log. Entries are added by the memory consolidation system.\n\n",
	}
}

// InitMemoryFiles creates MEMORY.md and HISTORY.md in the memory directory
// if they do not already exist.
func InitMemoryFiles(memoryDir string) error {
	if err := os.MkdirAll(memoryDir, 0755); err != nil {
		return err
	}

	tmpl := DefaultMemoryTemplates()

	memoryPath := filepath.Join(memoryDir, "MEMORY.md")
	if _, err := os.Stat(memoryPath); os.IsNotExist(err) {
		if err := os.WriteFile(memoryPath, []byte(tmpl.MEMORY), 0644); err != nil {
			return err
		}
	}

	historyPath := filepath.Join(memoryDir, "HISTORY.md")
	if _, err := os.Stat(historyPath); os.IsNotExist(err) {
		if err := os.WriteFile(historyPath, []byte(tmpl.History), 0644); err != nil {
			return err
		}
	}

	return nil
}
