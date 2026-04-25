package boss

import (
	"context"
	"database/sql"
	"fmt"
	"path/filepath"
	"strings"
	"time"

	"lcroom/internal/model"
	"lcroom/internal/service"
)

const hotProjectLimit = 8

type StateSnapshot struct {
	LoadedAt               time.Time
	TotalProjects          int
	ActiveProjects         int
	PossiblyStuckProjects  int
	DirtyProjects          int
	ConflictProjects       int
	PendingClassifications int
	HotProjects            []ProjectBrief
}

type ProjectBrief struct {
	Name                 string
	Path                 string
	Status               model.ProjectStatus
	AttentionScore       int
	LastActivity         time.Time
	RepoBranch           string
	RepoDirty            bool
	RepoConflict         bool
	RepoSyncStatus       model.RepoSyncStatus
	RepoAheadCount       int
	RepoBehindCount      int
	OpenTODOCount        int
	SnoozedUntil         *time.Time
	LatestSummary        string
	LatestCompleted      string
	LatestCategory       model.SessionCategory
	LatestCompletedKind  model.SessionCategory
	ClassificationStatus model.SessionClassificationStatus
	Reasons              []model.AttentionReason
}

func LoadStateSnapshot(ctx context.Context, svc *service.Service, now time.Time) (StateSnapshot, error) {
	if svc == nil || svc.Store() == nil {
		return StateSnapshot{}, fmt.Errorf("service store is not available")
	}
	if now.IsZero() {
		now = time.Now()
	}
	projects, err := svc.Store().ListProjects(ctx, false)
	if err != nil {
		return StateSnapshot{}, err
	}

	snapshot := StateSnapshot{
		LoadedAt:      now,
		TotalProjects: len(projects),
	}
	for _, project := range projects {
		switch project.Status {
		case model.StatusActive:
			snapshot.ActiveProjects++
		case model.StatusPossiblyStuck:
			snapshot.PossiblyStuckProjects++
		}
		if project.RepoDirty {
			snapshot.DirtyProjects++
		}
		if project.RepoConflict {
			snapshot.ConflictProjects++
		}
	}
	if counts, err := svc.Store().GetSessionClassificationCounts(ctx, true); err == nil {
		snapshot.PendingClassifications = counts[model.ClassificationPending] + counts[model.ClassificationRunning]
	}

	for _, project := range projects {
		if len(snapshot.HotProjects) >= hotProjectLimit {
			break
		}
		brief := projectBriefFromSummary(project)
		detail, err := svc.Store().GetProjectDetail(ctx, project.Path, 3)
		if err == nil {
			brief.Reasons = append([]model.AttentionReason(nil), detail.Reasons...)
		} else if err != sql.ErrNoRows {
			brief.Reasons = nil
		}
		snapshot.HotProjects = append(snapshot.HotProjects, brief)
	}
	return snapshot, nil
}

func projectBriefFromSummary(project model.ProjectSummary) ProjectBrief {
	return ProjectBrief{
		Name:                 displayProjectName(project),
		Path:                 strings.TrimSpace(project.Path),
		Status:               project.Status,
		AttentionScore:       project.AttentionScore,
		LastActivity:         project.LastActivity,
		RepoBranch:           strings.TrimSpace(project.RepoBranch),
		RepoDirty:            project.RepoDirty,
		RepoConflict:         project.RepoConflict,
		RepoSyncStatus:       project.RepoSyncStatus,
		RepoAheadCount:       project.RepoAheadCount,
		RepoBehindCount:      project.RepoBehindCount,
		OpenTODOCount:        project.OpenTODOCount,
		SnoozedUntil:         project.SnoozedUntil,
		LatestSummary:        strings.TrimSpace(project.LatestSessionSummary),
		LatestCompleted:      strings.TrimSpace(project.LatestCompletedSessionSummary),
		LatestCategory:       project.LatestSessionClassificationType,
		LatestCompletedKind:  project.LatestCompletedSessionClassificationType,
		ClassificationStatus: project.LatestSessionClassification,
	}
}

func BuildStateBrief(snapshot StateSnapshot, now time.Time) string {
	if now.IsZero() {
		now = time.Now()
	}
	lines := []string{
		"Current app state:",
		fmt.Sprintf("Visible projects: %d. Active: %d. Possibly stuck: %d. Dirty repos: %d. Conflicts: %d.",
			snapshot.TotalProjects,
			snapshot.ActiveProjects,
			snapshot.PossiblyStuckProjects,
			snapshot.DirtyProjects,
			snapshot.ConflictProjects,
		),
	}
	if snapshot.PendingClassifications > 0 {
		lines = append(lines, fmt.Sprintf("AI assessment queue: %d pending/running.", snapshot.PendingClassifications))
	}
	if len(snapshot.HotProjects) == 0 {
		lines = append(lines, "Hot projects: none loaded.")
		return strings.Join(lines, "\n")
	}

	lines = append(lines, "Hot projects:")
	for _, project := range snapshot.HotProjects {
		lines = append(lines, "- "+briefLine(project, now))
	}
	return strings.Join(lines, "\n")
}

func OnMyDeskText(snapshot StateSnapshot, now time.Time) string {
	if now.IsZero() {
		now = time.Now()
	}
	if len(snapshot.HotProjects) == 0 {
		return "Nothing on the desk yet.\nRun a scan or wait for project state to load."
	}
	lines := make([]string, 0, 5)
	for i, project := range snapshot.HotProjects {
		if i >= 4 {
			break
		}
		label := fmt.Sprintf("%s: %s", project.Name, shortProjectState(project))
		if project.AttentionScore > 0 {
			label = fmt.Sprintf("%s (%d)", label, project.AttentionScore)
		}
		if !project.LastActivity.IsZero() {
			label += ", " + relativeAge(now, project.LastActivity)
		}
		lines = append(lines, label)
	}
	return strings.Join(lines, "\n")
}

func NotebookText(snapshot StateSnapshot) string {
	lines := []string{
		"Chat is the control surface.",
		"Panels are stationary assistant notes.",
		"Classic TUI stays available for detail work.",
	}
	if snapshot.PendingClassifications > 0 {
		lines = append(lines, fmt.Sprintf("Assessment queue: %d running/pending.", snapshot.PendingClassifications))
	}
	return strings.Join(lines, "\n")
}

func briefLine(project ProjectBrief, now time.Time) string {
	parts := []string{
		fmt.Sprintf("%s: %s", project.Name, shortProjectState(project)),
	}
	if project.AttentionScore > 0 {
		parts = append(parts, fmt.Sprintf("attention %d", project.AttentionScore))
	}
	if project.OpenTODOCount > 0 {
		parts = append(parts, fmt.Sprintf("%d open TODOs", project.OpenTODOCount))
	}
	if !project.LastActivity.IsZero() {
		parts = append(parts, "last activity "+relativeAge(now, project.LastActivity))
	}
	if summary := bestProjectSummary(project); summary != "" {
		parts = append(parts, "latest: "+clipText(summary, 120))
	}
	if len(project.Reasons) > 0 {
		reasons := make([]string, 0, minInt(2, len(project.Reasons)))
		for i, reason := range project.Reasons {
			if i >= 2 {
				break
			}
			if text := strings.TrimSpace(reason.Text); text != "" {
				reasons = append(reasons, text)
			}
		}
		if len(reasons) > 0 {
			parts = append(parts, "reasons: "+clipText(strings.Join(reasons, "; "), 120))
		}
	}
	return strings.Join(parts, "; ")
}

func shortProjectState(project ProjectBrief) string {
	parts := []string{string(project.Status)}
	if project.RepoConflict {
		parts = append(parts, "conflict")
	}
	if project.RepoDirty {
		parts = append(parts, "dirty")
	}
	if sync := repoSyncLabel(project.RepoSyncStatus, project.RepoAheadCount, project.RepoBehindCount); sync != "" {
		parts = append(parts, sync)
	}
	if branch := strings.TrimSpace(project.RepoBranch); branch != "" {
		parts = append(parts, branch)
	}
	if project.SnoozedUntil != nil {
		parts = append(parts, "snoozed")
	}
	return strings.Join(parts, ", ")
}

func repoSyncLabel(status model.RepoSyncStatus, ahead, behind int) string {
	switch status {
	case model.RepoSyncAhead:
		return fmt.Sprintf("ahead +%d", ahead)
	case model.RepoSyncBehind:
		return fmt.Sprintf("behind -%d", behind)
	case model.RepoSyncDiverged:
		return fmt.Sprintf("diverged +%d/-%d", ahead, behind)
	default:
		return ""
	}
}

func bestProjectSummary(project ProjectBrief) string {
	if summary := strings.TrimSpace(project.LatestSummary); summary != "" {
		return summary
	}
	return strings.TrimSpace(project.LatestCompleted)
}

func displayProjectName(project model.ProjectSummary) string {
	if name := strings.TrimSpace(project.Name); name != "" {
		return name
	}
	if path := strings.TrimSpace(project.Path); path != "" {
		return filepath.Base(path)
	}
	return "untitled project"
}

func relativeAge(now, at time.Time) string {
	if at.IsZero() {
		return "unknown"
	}
	if now.IsZero() {
		now = time.Now()
	}
	d := now.Sub(at)
	if d < 0 {
		d = -d
	}
	switch {
	case d < time.Minute:
		return "just now"
	case d < time.Hour:
		return fmt.Sprintf("%dm ago", int(d.Minutes()))
	case d < 24*time.Hour:
		return fmt.Sprintf("%dh ago", int(d.Hours()))
	default:
		return fmt.Sprintf("%dd ago", int(d.Hours()/24))
	}
}

func clipText(text string, limit int) string {
	text = strings.Join(strings.Fields(strings.TrimSpace(text)), " ")
	if limit <= 0 {
		return ""
	}
	runes := []rune(text)
	if len(runes) <= limit {
		return text
	}
	if limit <= 3 {
		return string(runes[:limit])
	}
	return string(runes[:limit-3]) + "..."
}

func minInt(a, b int) int {
	if a < b {
		return a
	}
	return b
}
