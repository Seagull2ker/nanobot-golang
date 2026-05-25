package tool

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// ResolveFilePath resolves a file path relative to workspace, enforcing containment.
func ResolveFilePath(filePath, workspace string) (string, error) {
	p := filepath.Clean(filePath)

	if !filepath.IsAbs(p) {
		p = filepath.Join(workspace, p)
	}

	resolved, err := filepath.Abs(p)
	if err != nil {
		return "", fmt.Errorf("resolve path: %w", err)
	}

	return resolved, nil
}

// ValidatePathSafety checks that a resolved path is within the workspace.
func ValidatePathSafety(path, workspace string) error {
	absPath, err := filepath.Abs(path)
	if err != nil {
		return fmt.Errorf("abs path: %w", err)
	}

	absWS, err := filepath.Abs(workspace)
	if err != nil {
		return fmt.Errorf("abs workspace: %w", err)
	}

	rel, err := filepath.Rel(absWS, absPath)
	if err != nil {
		return fmt.Errorf("path outside workspace: %s", path)
	}
	if strings.HasPrefix(rel, "..") {
		return fmt.Errorf("path outside workspace: %s", path)
	}

	return nil
}

// EnsureWithinWorkspace validates that path is under workspace.
func EnsureWithinWorkspace(path, workspace string) error {
	return ValidatePathSafety(path, workspace)
}

// FileExists checks if a file exists and is not a directory.
func FileExists(path string) bool {
	info, err := os.Stat(path)
	return err == nil && !info.IsDir()
}

// DirExists checks if a directory exists.
func DirExists(path string) bool {
	info, err := os.Stat(path)
	return err == nil && info.IsDir()
}
