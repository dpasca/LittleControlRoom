package service

import (
	"context"
	"fmt"
	"time"

	"lcroom/internal/codexstate"
)

// A broad audit can take time. Recheck selected index rows and file metadata at
// the deletion boundary so activity or pins changed during that audit fail closed.
func revalidateStaleCleanupFiles(ctx context.Context, home string, group CodexCleanupWorktreeGroup) error {
	threads, err := codexstate.ListThreadsIncludingUnknownCWD(ctx, home)
	if err != nil {
		return fmt.Errorf("recheck stale session index: %w", err)
	}
	byID := make(map[string]codexstate.Thread, len(threads))
	count := 0
	for _, thread := range threads {
		byID[thread.ID] = thread
		if normalizeCleanupPath(thread.CWD) == group.WorktreePath {
			count++
		}
	}
	if count != group.TotalThreadCount {
		return fmt.Errorf("project session count changed during audit; refresh before deleting")
	}
	if group.InactiveDays != 7 && group.InactiveDays != 14 && group.InactiveDays != 30 && group.InactiveDays != 90 {
		return fmt.Errorf("invalid stale session inactivity threshold")
	}
	cutoff := time.Now().Add(-time.Duration(group.InactiveDays) * 24 * time.Hour)
	for _, tree := range group.Threads {
		for _, file := range tree.RolloutFiles {
			thread, exists := byID[file.ThreadID]
			if !exists || !thread.PinnedKnown || thread.Pinned ||
				normalizeCleanupPath(thread.CWD) != group.WorktreePath || thread.LastActivity.IsZero() || thread.LastActivity.After(cutoff) {
				return fmt.Errorf("selected session changed during audit; refresh before deleting")
			}
			fresh, err := inspectCleanupRolloutFile(home, thread.ID, thread.RolloutPath)
			if err != nil || fresh.Path != file.Path || fresh.Size != file.Size || !fresh.ModTime.Equal(file.ModTime) {
				return fmt.Errorf("selected rollout changed during audit; refresh before deleting")
			}
		}
	}
	return nil
}
