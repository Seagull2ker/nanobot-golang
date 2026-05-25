package tools

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/Seagull2ker/nanobot-go/internal/tool"
)

// applyPatchTool applies structured multi-file edits.
// Each patch specifies a file_path, old_string, and new_string, similar to edit_file
// but batched so multiple files can be edited in a single tool call.
type applyPatchTool struct{}

func init() { tool.Register(&applyPatchTool{}) }

func (t *applyPatchTool) Name() string          { return "apply_patch" }
func (t *applyPatchTool) ReadOnly() bool        { return false }
func (t *applyPatchTool) ConcurrencySafe() bool { return false }
func (t *applyPatchTool) Exclusive() bool       { return false }

func (t *applyPatchTool) Description() string {
	return "Apply multiple file edits in a single call. Each edit specifies a file_path, old_string, and new_string."
}

func (t *applyPatchTool) Parameters() map[string]any {
	return map[string]any{
		"type": "object",
		"properties": map[string]any{
			"edits": map[string]any{
				"type":        "array",
				"description": "List of file edits to apply",
				"items": map[string]any{
					"type": "object",
					"properties": map[string]any{
						"file_path": map[string]any{
							"type":        "string",
							"description": "Path to the file to edit (relative to workspace)",
						},
						"old_string": map[string]any{
							"type":        "string",
							"description": "The exact text to replace",
						},
						"new_string": map[string]any{
							"type":        "string",
							"description": "The text to replace it with",
						},
					},
					"required": []string{"file_path", "old_string", "new_string"},
				},
			},
		},
		"required": []string{"edits"},
	}
}

func (t *applyPatchTool) Execute(ctx context.Context, params map[string]any) (*tool.Result, error) {
	editsRaw, ok := params["edits"].([]any)
	if !ok || len(editsRaw) == 0 {
		return &tool.Result{Content: "Error: edits array is required"}, nil
	}

	var results []string
	successes := 0

	for i, e := range editsRaw {
		edit, ok := e.(map[string]any)
		if !ok {
			results = append(results, fmt.Sprintf("[%d] Error: invalid edit format", i+1))
			continue
		}

		filePath, _ := edit["file_path"].(string)
		oldStr, _ := edit["old_string"].(string)
		newStr, _ := edit["new_string"].(string)

		if filePath == "" || oldStr == "" {
			results = append(results, fmt.Sprintf("[%d] Error: file_path and old_string required", i+1))
			continue
		}

		resolved, err := tool.ResolveFilePath(filePath, workspace)
		if err != nil {
			results = append(results, fmt.Sprintf("[%d] Error: %v", i+1, err))
			continue
		}
		if err := tool.EnsureWithinWorkspace(resolved, workspace); err != nil {
			results = append(results, fmt.Sprintf("[%d] Error: %v", i+1, err))
			continue
		}

		data, err := os.ReadFile(resolved)
		if err != nil {
			results = append(results, fmt.Sprintf("[%d] Error reading %s: %v", i+1, filePath, err))
			continue
		}

		content := string(data)

		// Try exact match first, then whitespace-tolerant.
		matched := oldStr
		if !strings.Contains(content, oldStr) {
			if m, ok := findWhitespaceMatch(content, oldStr); ok {
				matched = m
			} else {
				results = append(results, fmt.Sprintf("[%d] Error: old_string not found in %s", i+1, filePath))
				continue
			}
		}

		// Apply replacement.
		dir := filepath.Dir(resolved)
		if err := os.MkdirAll(dir, 0755); err != nil {
			results = append(results, fmt.Sprintf("[%d] Error: %v", i+1, err))
			continue
		}
		newContent := strings.Replace(content, matched, newStr, 1)
		if err := os.WriteFile(resolved, []byte(newContent), 0644); err != nil {
			results = append(results, fmt.Sprintf("[%d] Error writing: %v", i+1, err))
			continue
		}

		successes++
		results = append(results, fmt.Sprintf("[%d] Edited %s", i+1, filePath))
	}

	return &tool.Result{Content: fmt.Sprintf("Applied %d/%d edits:\n%s", successes, len(editsRaw), strings.Join(results, "\n"))}, nil
}
