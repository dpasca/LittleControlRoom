package sessionclassify

import (
	"context"
	"errors"
	"os/exec"
	"strings"
	"time"

	"lcroom/internal/model"
)

type WorktreePublicationStatus string

const (
	WorktreePublicationUnknown   WorktreePublicationStatus = "unknown"
	WorktreePublicationPending   WorktreePublicationStatus = "unpublished"
	WorktreePublicationPublished WorktreePublicationStatus = "published"
)

// WorktreeIntegrationSnapshot is current repository evidence, independent of
// what the engineer knew at the end of its turn. Remote state uses local
// tracking refs; collecting an assessment never fetches or changes a repository.
type WorktreeIntegrationSnapshot struct {
	TargetBranch      string                    `json:"target_branch"`
	MergeStatus       model.WorktreeMergeStatus `json:"merge_status"`
	PublicationStatus WorktreePublicationStatus `json:"publication_status"`
}

// WithWorktree enriches a snapshot with integration and target publication.
// It may read Git with a bounded timeout; call only from background scan,
// refresh, or classifier work, never from a UI update or render path.
func (s GitStatusSnapshot) WithWorktree(ctx context.Context, path string, kind model.WorktreeKind, target string, status model.WorktreeMergeStatus) GitStatusSnapshot {
	if kind != model.WorktreeKindLinked {
		return s
	}
	s.Integration = &WorktreeIntegrationSnapshot{
		TargetBranch:      target,
		MergeStatus:       status,
		PublicationStatus: WorktreePublicationUnknown,
	}
	if status != model.WorktreeMergeStatusMerged || strings.TrimSpace(target) == "" {
		return s
	}
	ctx, cancel := context.WithTimeout(ctx, 2*time.Second)
	defer cancel()
	// Check this checkout's work against the target's upstream. The target's
	// ahead/behind counts include unrelated work and must not invalidate this
	// assessment whenever another worktree is merged or pushed.
	upstream := target + "@{upstream}"
	err := exec.CommandContext(ctx, "git", "-C", path, "merge-base", "--is-ancestor", "HEAD", upstream).Run()
	if err == nil {
		s.Integration.PublicationStatus = WorktreePublicationPublished
		return s
	}
	var exitErr *exec.ExitError
	if !errors.As(err, &exitErr) || exitErr.ExitCode() != 1 {
		return s
	}
	// Match the merge detector's support for rebased/cherry-picked work.
	// A failed read leaves publication unknown, never implicitly published.
	out, err := exec.CommandContext(ctx, "git", "-C", path, "cherry", upstream, "HEAD").Output()
	if err != nil {
		return s
	}
	for _, line := range strings.Split(strings.TrimSpace(string(out)), "\n") {
		fields := strings.Fields(line)
		if len(fields) == 0 {
			continue
		}
		if len(fields) != 2 || (fields[0] != "+" && fields[0] != "-") {
			return s
		}
		if fields[0] == "+" {
			s.Integration.PublicationStatus = WorktreePublicationPending
			return s
		}
	}
	s.Integration.PublicationStatus = WorktreePublicationPublished
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
