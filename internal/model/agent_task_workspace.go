package model

import (
	"path/filepath"
	"strings"
)

// AgentTaskMatchesRemovedWorktree uses saved task ownership only. It performs
// no filesystem reads, so the TUI can drop the same rows after verified removal.
func AgentTaskMatchesRemovedWorktree(task AgentTask, removedPath string) bool {
	clean := func(path string) string {
		path = strings.TrimSpace(path)
		if !filepath.IsAbs(path) {
			return ""
		}
		return filepath.Clean(path)
	}
	removedPath = clean(removedPath)
	if removedPath == "" || removedPath == string(filepath.Separator) {
		return false
	}
	if clean(task.OriginWorktreePath) == removedPath {
		return true
	}
	workspace := clean(task.WorkspacePath)
	if workspace == "" {
		return false
	}
	if workspace == removedPath || strings.HasPrefix(workspace, removedPath+string(filepath.Separator)) {
		return true
	}
	// A repository lease can refer to a shared checkout. Only an isolated root
	// is ownership evidence for removing the task itself.
	if task.Repository.Isolated && clean(task.Repository.Root) == removedPath {
		return true
	}
	// Before automatic isolated checkouts, workers commonly placed their linked
	// checkout at workspace/repo. Limit compatibility to this exact old layout
	// and tasks without a recorded repository boundary, never arbitrary children.
	return task.Repository.Root == "" && !task.Repository.Isolated &&
		filepath.Join(workspace, "repo") == removedPath
}
