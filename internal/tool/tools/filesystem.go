package tools

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/Seagull2ker/nanobot-go/internal/tool"
)

var workspace string

func SetWorkspace(ws string) { workspace = ws }

// ---------- ReadFileTool ----------

type readFileTool struct{}

func init() { tool.Register(&readFileTool{}) }

func (t *readFileTool) Name() string         { return "read_file" }
func (t *readFileTool) ReadOnly() bool        { return true }
func (t *readFileTool) ConcurrencySafe() bool { return true }
func (t *readFileTool) Exclusive() bool       { return false }

func (t *readFileTool) Description() string {
	return "Read a file from the workspace. Returns line-numbered output with pagination support."
}

func (t *readFileTool) Parameters() map[string]any {
	return map[string]any{
		"type": "object",
		"properties": map[string]any{
			"file_path": map[string]any{
				"type":        "string",
				"description": "Path to the file to read (relative to workspace)",
			},
			"offset": map[string]any{
				"type":        "integer",
				"description": "Line number to start reading from (1-based, default 1)",
			},
			"limit": map[string]any{
				"type":        "integer",
				"description": "Maximum number of lines to read (default 2000)",
			},
		},
		"required": []string{"file_path"},
	}
}

// 128K upper bound prevents a single read_file from consuming the entire context window.
// The continuation hint tells the LLM to paginate with offset=, avoiding truncation loops.
// Chosen empirically: large enough for most source files, small enough to leave room
// for conversation history + system prompt within a typical 64K-128K context window.
const maxReadChars = 128 * 1024

func (t *readFileTool) Execute(ctx context.Context, params map[string]any) (*tool.Result, error) {
	filePath, _ := params["file_path"].(string)
	if filePath == "" {
		return &tool.Result{Content: "Error: file_path is required"}, nil
	}

	resolved, err := tool.ResolveFilePath(filePath, workspace)
	if err != nil {
		return &tool.Result{Content: fmt.Sprintf("Error: %v", err)}, nil
	}
	if err := tool.EnsureWithinWorkspace(resolved, workspace); err != nil {
		return &tool.Result{Content: fmt.Sprintf("Error: %v", err)}, nil
	}

	data, err := os.ReadFile(resolved)
	if err != nil {
		return &tool.Result{Content: fmt.Sprintf("Error reading file: %v", err)}, nil
	}

	offset := 1
	if o, ok := params["offset"].(float64); ok && int(o) > 0 {
		offset = int(o)
	}

	defaultLimit := 2000
	limit := defaultLimit
	if l, ok := params["limit"].(float64); ok && int(l) > 0 {
		limit = int(l)
	}

	lines := strings.Split(string(data), "\n")
	if offset > len(lines) {
		return &tool.Result{Content: fmt.Sprintf("Error: offset %d exceeds file length %d", offset, len(lines))}, nil
	}

	if offset-1+limit > len(lines) {
		limit = len(lines) - offset + 1
	}

	var b strings.Builder
	chars := 0
	truncated := false

	for i := offset - 1; i < offset-1+limit && i < len(lines); i++ {
		line := fmt.Sprintf("%6d| %s\n", i+1, lines[i])
		if chars+len(line) > maxReadChars {
			truncated = true
			break
		}
		b.WriteString(line)
		chars += len(line)
	}

	result := b.String()
	if truncated {
		totalLines := len(lines)
		result += fmt.Sprintf("\n[File truncated at %d chars. %d of %d lines shown. Use offset=%d to continue reading.]",
			maxReadChars, offset+limit-1, totalLines, offset+limit)
	}

	return &tool.Result{Content: strings.TrimSuffix(result, "\n")}, nil
}

// ---------- WriteFileTool ----------

type writeFileTool struct{}

func init() { tool.Register(&writeFileTool{}) }

func (t *writeFileTool) Name() string          { return "write_file" }
func (t *writeFileTool) ReadOnly() bool         { return false }
func (t *writeFileTool) ConcurrencySafe() bool  { return false }
func (t *writeFileTool) Exclusive() bool        { return false }

func (t *writeFileTool) Description() string {
	return "Write content to a file in the workspace. Creates parent directories if needed."
}

func (t *writeFileTool) Parameters() map[string]any {
	return map[string]any{
		"type": "object",
		"properties": map[string]any{
			"file_path": map[string]any{
				"type":        "string",
				"description": "Path to the file to write (relative to workspace)",
			},
			"content": map[string]any{
				"type":        "string",
				"description": "Content to write to the file",
			},
		},
		"required": []string{"file_path", "content"},
	}
}

func (t *writeFileTool) Execute(ctx context.Context, params map[string]any) (*tool.Result, error) {
	filePath, _ := params["file_path"].(string)
	content, _ := params["content"].(string)

	resolved, err := tool.ResolveFilePath(filePath, workspace)
	if err != nil {
		return &tool.Result{Content: fmt.Sprintf("Error: %v", err)}, nil
	}
	if err := tool.EnsureWithinWorkspace(resolved, workspace); err != nil {
		return &tool.Result{Content: fmt.Sprintf("Error: %v", err)}, nil
	}

	if err := os.MkdirAll(filepath.Dir(resolved), 0755); err != nil {
		return &tool.Result{Content: fmt.Sprintf("Error creating directory: %v", err)}, nil
	}
	if err := os.WriteFile(resolved, []byte(content), 0644); err != nil {
		return &tool.Result{Content: fmt.Sprintf("Error writing file: %v", err)}, nil
	}

	info, _ := os.Stat(resolved)
	return &tool.Result{Content: fmt.Sprintf("Successfully wrote %d bytes to %s", info.Size(), filePath)}, nil
}

// ---------- EditFileTool ----------

type editFileTool struct{}

func init() { tool.Register(&editFileTool{}) }

func (t *editFileTool) Name() string          { return "edit_file" }
func (t *editFileTool) ReadOnly() bool         { return false }
func (t *editFileTool) ConcurrencySafe() bool  { return false }
func (t *editFileTool) Exclusive() bool        { return false }

func (t *editFileTool) Description() string {
	return "Edit a file by replacing a string. Supports whitespace-tolerant matching and provides diagnostics on failure."
}

func (t *editFileTool) Parameters() map[string]any {
	return map[string]any{
		"type": "object",
		"properties": map[string]any{
			"file_path":   map[string]any{"type": "string", "description": "Path to the file to edit (relative to workspace)"},
			"old_string":  map[string]any{"type": "string", "description": "The exact text to replace"},
			"new_string":  map[string]any{"type": "string", "description": "The text to replace it with"},
			"replace_all": map[string]any{"type": "boolean", "description": "Replace all occurrences (default: false)"},
		},
		"required": []string{"file_path", "old_string", "new_string"},
	}
}

func (t *editFileTool) Execute(ctx context.Context, params map[string]any) (*tool.Result, error) {
	filePath, _ := params["file_path"].(string)
	oldStr, _ := params["old_string"].(string)
	newStr, _ := params["new_string"].(string)
	replaceAll, _ := params["replace_all"].(bool)

	resolved, err := tool.ResolveFilePath(filePath, workspace)
	if err != nil {
		return &tool.Result{Content: fmt.Sprintf("Error: %v", err)}, nil
	}
	if err := tool.EnsureWithinWorkspace(resolved, workspace); err != nil {
		return &tool.Result{Content: fmt.Sprintf("Error: %v", err)}, nil
	}

	data, err := os.ReadFile(resolved)
	if err != nil {
		return &tool.Result{Content: fmt.Sprintf("Error reading file: %v", err)}, nil
	}
	content := string(data)

	// Three-tier matching strategy (exact → whitespace-tolerant → diagnostic).
	// LLMs frequently reproduce code with minor whitespace drift (tab vs space,
	// trailing whitespace stripped by editors). Exact match fails for these cases,
	// causing the agent to give up or hallucinate a fix. Whitespace-tolerant match
	// normalizes both sides before comparison so the edit succeeds.
	count := strings.Count(content, oldStr)
	if count == 0 {
		match, matched := findWhitespaceMatch(content, oldStr)
		if !matched {
			return &tool.Result{Content: notFoundDiagnostic(oldStr, content, filePath)}, nil
		}
		oldStr = match
		count = strings.Count(content, oldStr)
	}
	if count > 1 && !replaceAll {
		return &tool.Result{Content: fmt.Sprintf(
			"Error: old_string found %d times. Use replace_all=true to replace all, or provide more context to make it unique.", count)}, nil
	}

	newContent := strings.ReplaceAll(content, oldStr, newStr)
	if err := os.WriteFile(resolved, []byte(newContent), 0644); err != nil {
		return &tool.Result{Content: fmt.Sprintf("Error writing file: %v", err)}, nil
	}

	if replaceAll {
		return &tool.Result{Content: fmt.Sprintf("Successfully replaced %d occurrences in %s", count, filePath)}, nil
	}
	return &tool.Result{Content: fmt.Sprintf("Successfully edited %s", filePath)}, nil
}

// Whitespace-tolerant line-by-line matching. Each line pair is compared after
// TrimSpace, so "  foo  " matches "foo". This preserves the original file content
// for the actual replacement (returning the matched window as-is, not the target).
func findWhitespaceMatch(content, target string) (string, bool) {
	targetLines := strings.Split(target, "\n")
	contentLines := strings.Split(content, "\n")

	for i := 0; i <= len(contentLines)-len(targetLines); i++ {
		if linesMatch(currentWindow(contentLines, i, len(targetLines)), targetLines) {
			return strings.Join(currentWindow(contentLines, i, len(targetLines)), "\n"), true
		}
	}
	return "", false
}

func currentWindow(lines []string, start, n int) []string {
	end := start + n
	if end > len(lines) {
		end = len(lines)
	}
	return lines[start:end]
}

func linesMatch(window, target []string) bool {
	if len(window) != len(target) {
		return false
	}
	for i := range window {
		if strings.TrimSpace(window[i]) != strings.TrimSpace(target[i]) {
			return false
		}
	}
	return true
}

// LCS-based similarity diagnostic. When the edit target isn't found, instead of
// a bare "not found" error, show the closest matching line with similarity percentage.
// Uses longest common subsequence ratio (not Levenshtein) because LCS handles
// insertions/deletions better for code — the LLM often adds or removes a parameter
// while keeping the surrounding structure intact.
func notFoundDiagnostic(oldStr, content, path string) string {
	var b strings.Builder
	b.WriteString("Error: old_string not found in file.\n\n")

	// Find best matching line.
	bestLine, bestSim := 0, 0.0
	contentLines := strings.Split(content, "\n")
	oldLines := strings.Split(oldStr, "\n")
	firstOld := oldLines[0]

	for i, line := range contentLines {
		sim := lineSimilarity(firstOld, line)
		if sim > bestSim {
			bestSim = sim
			bestLine = i + 1
		}
	}

	if bestSim > 0.3 {
		start := bestLine - 3
		if start < 1 {
			start = 1
		}
		end := bestLine + 2
		if end > len(contentLines) {
			end = len(contentLines)
		}

		b.WriteString(fmt.Sprintf("Closest match at line %d (%.0f%% similarity):\n", bestLine, bestSim*100))
		for i := start; i <= end; i++ {
			marker := "  "
			if i == bestLine {
				marker = "> "
			}
			b.WriteString(fmt.Sprintf("%s%6d| %s\n", marker, i, contentLines[i-1]))
		}
	}

	b.WriteString("\nTip: Use read_file to view the file content, then copy the exact text to replace.")
	return b.String()
}

func lineSimilarity(a, b string) float64 {
	ta := strings.TrimSpace(a)
	tb := strings.TrimSpace(b)
	if ta == "" && tb == "" {
		return 1.0
	}
	if ta == "" || tb == "" {
		return 0.0
	}
	return longestCommonSubsequence(ta, tb) / float64(max(len(ta), len(tb)))
}

func longestCommonSubsequence(a, b string) float64 {
	na, nb := len(a), len(b)
	if na == 0 || nb == 0 {
		return 0
	}

	dp := make([][]int, na+1)
	for i := range dp {
		dp[i] = make([]int, nb+1)
	}

	for i := 1; i <= na; i++ {
		for j := 1; j <= nb; j++ {
			if a[i-1] == b[j-1] {
				dp[i][j] = dp[i-1][j-1] + 1
			} else {
				dp[i][j] = max(dp[i-1][j], dp[i][j-1])
			}
		}
	}

	return float64(dp[na][nb])
}

func max(a, b int) int {
	if a > b {
		return a
	}
	return b
}

// ---------- ListDirTool ----------

type listDirTool struct{}

func init() { tool.Register(&listDirTool{}) }

func (t *listDirTool) Name() string          { return "list_dir" }
func (t *listDirTool) ReadOnly() bool         { return true }
func (t *listDirTool) ConcurrencySafe() bool  { return true }
func (t *listDirTool) Exclusive() bool        { return false }

func (t *listDirTool) Description() string {
	return "List files and directories in the workspace."
}

func (t *listDirTool) Parameters() map[string]any {
	return map[string]any{
		"type": "object",
		"properties": map[string]any{
			"path": map[string]any{
				"type":        "string",
				"description": "Subdirectory within workspace to list (default: workspace root)",
			},
			"recursive": map[string]any{
				"type":        "boolean",
				"description": "List directories recursively (default: false)",
			},
			"max_entries": map[string]any{
				"type":        "integer",
				"description": "Maximum entries to return (default: 200)",
			},
		},
	}
}

// Noise directories skipped by list_dir to keep output manageable.
// These are build artifacts, dependency caches, and tool internals that
// the agent rarely needs to inspect. Skipping them prevents the LLM from
// wasting context window on thousands of irrelevant entries.
// Python/pytest/ruff/coverage entries mirror the Python nanobot convention.
var ignoreDirs = map[string]bool{
	".git": true, "node_modules": true, "__pycache__": true, ".venv": true, "venv": true,
	"dist": true, "build": true, ".tox": true, ".mypy_cache": true, ".pytest_cache": true,
	".ruff_cache": true, ".coverage": true, "htmlcov": true, ".next": true, ".cache": true,
}

func (t *listDirTool) Execute(ctx context.Context, params map[string]any) (*tool.Result, error) {
	subPath, _ := params["path"].(string)
	recursive, _ := params["recursive"].(bool)
	maxEntries := 200
	if m, ok := params["max_entries"].(float64); ok && int(m) > 0 {
		maxEntries = int(m)
	}

	searchDir := workspace
	if subPath != "" {
		searchDir = filepath.Join(workspace, subPath)
	}

	var entries []string
	var walkFn filepath.WalkFunc

	if recursive {
		walkFn = func(path string, info os.FileInfo, err error) error {
			if err != nil || len(entries) >= maxEntries {
				return filepath.SkipDir
			}
			rel, _ := filepath.Rel(workspace, path)
			if rel == "." {
				return nil
			}
			for _, part := range strings.Split(rel, string(filepath.Separator)) {
				if ignoreDirs[part] {
					if info.IsDir() {
						return filepath.SkipDir
					}
					return nil
				}
			}
			if info.IsDir() {
				entries = append(entries, rel+"/")
			} else {
				entries = append(entries, fmt.Sprintf("%s (%d bytes)", rel, info.Size()))
			}
			return nil
		}
		filepath.Walk(searchDir, walkFn)
	} else {
		dirEntries, err := os.ReadDir(searchDir)
		if err != nil {
			return &tool.Result{Content: fmt.Sprintf("Error: %v", err)}, nil
		}
		for _, e := range dirEntries {
			if len(entries) >= maxEntries {
				break
			}
			name := e.Name()
			if ignoreDirs[name] && e.IsDir() {
				continue
			}
			if e.IsDir() {
				entries = append(entries, name+"/")
			} else {
				info, _ := e.Info()
				entries = append(entries, fmt.Sprintf("%s (%d bytes)", name, info.Size()))
			}
		}
	}

	sort.Strings(entries)

	if len(entries) == 0 {
		return &tool.Result{Content: fmt.Sprintf("No entries in %s", subPath)}, nil
	}

	result := strings.Join(entries, "\n")
	if len(entries) >= maxEntries {
		result += fmt.Sprintf("\n\n[Showing %d of more entries. Use path= to narrow or max_entries= to increase.]", len(entries))
	}

	return &tool.Result{Content: result}, nil
}
