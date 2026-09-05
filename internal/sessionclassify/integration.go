package sessionclassify

import (
	"context"
	"fmt"
	"os/exec"
	"strings"
	"time"

	"lcroom/internal/model"
)

// WorktreeIntegrationSnapshot is current repository evidence, independent of
// what the engineer knew at the end of its turn. Remote state uses local
// tracking refs; collecting an assessment never fetches or changes a repository.
type WorktreeIntegrationSnapshot struct {
	TargetBranch       string                    `json:"target_branch"`
	MergeStatus        model.WorktreeMergeStatus `json:"merge_status"`
	TargetRemoteStatus model.RepoSyncStatus      `json:"target_remote_status"`
	TargetAheadCount   int                       `json:"target_ahead_count,omitempty"`
	TargetBehindCount  int                       `json:"target_behind_count,omitempty"`
}

// WithWorktree enriches a snapshot with integration and target publication.
// It may read Git with a bounded timeout; call only from background scan,
// refresh, or classifier work, never from a UI update or render path.
func (s GitStatusSnapshot) WithWorktree(ctx context.Context, path string, kind model.WorktreeKind, target string, status model.WorktreeMergeStatus) GitStatusSnapshot {
	if kind != model.WorktreeKindLinked {
		return s
	}
	s.Integration = &WorktreeIntegrationSnapshot{
		TargetBranch:       target,
		MergeStatus:        status,
		TargetRemoteStatus: "unknown",
	}
	if status != model.WorktreeMergeStatusMerged || strings.TrimSpace(target) == "" {
		return s
	}
	ctx, cancel := context.WithTimeout(ctx, 2*time.Second)
	defer cancel()
	// Use the recorded target ref even when the primary checkout is on another
	// branch. A linked branch's own upstream does not describe merge publication.
	ref := "refs/heads/" + target
	out, err := exec.CommandContext(ctx, "git", "-C", path, "rev-list", "--left-right", "--count", "--end-of-options", ref+"..."+target+"@{upstream}").Output()
	if err != nil {
		return s
	}
	var ahead, behind int
	if n, err := fmt.Sscanf(string(out), "%d %d", &ahead, &behind); err != nil || n != 2 || ahead < 0 || behind < 0 {
		return s
	}
	s.Integration.TargetAheadCount = ahead
	s.Integration.TargetBehindCount = behind
	switch {
	case ahead > 0 && behind > 0:
		s.Integration.TargetRemoteStatus = model.RepoSyncDiverged
	case ahead > 0:
		s.Integration.TargetRemoteStatus = model.RepoSyncAhead
	case behind > 0:
		s.Integration.TargetRemoteStatus = model.RepoSyncBehind
	default:
		s.Integration.TargetRemoteStatus = model.RepoSyncSynced
	}
	return s
}

func GitStatusForState(ctx context.Context, state model.ProjectState) GitStatusSnapshot {
	return NewGitStatusSnapshot(state.RepoDirty, state.RepoSyncStatus, state.RepoAheadCount, state.RepoBehindCount).
		WithWorktree(ctx, state.Path, state.WorktreeKind, state.WorktreeParentBranch, state.WorktreeMergeStatus)
}

func GitStatusForSummary(ctx context.Context, summary model.ProjectSummary) GitStatusSnapshot {
	return NewGitStatusSnapshot(summary.RepoDirty, summary.RepoSyncStatus, summary.RepoAheadCount, summary.RepoBehindCount).
		WithWorktree(ctx, summary.Path, summary.WorktreeKind, summary.WorktreeParentBranch, summary.WorktreeMergeStatus)
}
