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

const (
	hotProjectLimit              = 8
	defaultAttentionProjectLimit = 4
)

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
		fmt.Sprintf("Visible projects: %d. Active: %d. Possibly stuck: %d. Conflicts: %d.",
			snapshot.TotalProjects,
			snapshot.ActiveProjects,
			snapshot.PossiblyStuckProjects,
			snapshot.ConflictProjects,
		),
		"Routine dirty/ahead/branch state is reference metadata; treat it as background unless a blocker is listed or the user asks repo-health.",
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
		lines = append(lines, "- "+operationalProjectLine(project, now))
		if metadata := projectReferenceMetadata(project, now); metadata != "" {
			lines = append(lines, "  Reference metadata (use only for disambiguation/blockers): "+metadata)
		}
	}
	return strings.Join(lines, "\n")
}

func AttentionText(snapshot StateSnapshot, now time.Time) string {
	return AttentionTextWithLimit(snapshot, now, defaultAttentionProjectLimit)
}

func AttentionTextWithLimit(snapshot StateSnapshot, now time.Time, limit int) string {
	if now.IsZero() {
		now = time.Now()
	}
	limit = clampInt(limit, 1, hotProjectLimit)
	if len(snapshot.HotProjects) == 0 {
		return "Nothing needs attention yet.\nRun a scan or wait for project state to load."
	}
	lines := make([]string, 0, minInt(limit, len(snapshot.HotProjects)))
	for i, project := range snapshot.HotProjects {
		if i >= limit {
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

func NotesText(snapshot StateSnapshot) string {
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

func operationalProjectLine(project ProjectBrief, _ time.Time) string {
	return operationalProjectLineFor(project, true)
}

func operationalProjectSubstanceLine(project ProjectBrief, _ time.Time) string {
	return operationalProjectLineFor(project, false)
}

func operationalProjectLineFor(project ProjectBrief, includeName bool) string {
	name := strings.TrimSpace(project.Name)
	if name == "" {
		name = "untitled project"
	}
	var parts []string
	if includeName {
		parts = append(parts, name)
	}
	hasSummary := false
	if summary := bestProjectSummary(project); summary != "" {
		hasSummary = true
		parts = append(parts, "latest work: "+clipText(summary, 160))
	}
	if state := operationalStatusLabel(project, hasSummary); state != "" {
		parts = append(parts, "state: "+state)
	}
	if repoBlockers := materialRepoStatus(project); len(repoBlockers) > 0 {
		parts = append(parts, "repo blocker: "+strings.Join(repoBlockers, ", "))
	}
	if project.OpenTODOCount > 0 {
		parts = append(parts, fmt.Sprintf("open TODOs: %d", project.OpenTODOCount))
	}
	if reasons := projectReasonTexts(project, 2); len(reasons) > 0 {
		parts = append(parts, "signals: "+clipText(strings.Join(reasons, "; "), 180))
	}
	if (includeName && len(parts) == 1) || (!includeName && len(parts) == 0) {
		parts = append(parts, "no recent summary loaded")
	}
	return strings.Join(parts, "; ")
}

func operationalStatusLabel(project ProjectBrief, hasSummary bool) string {
	switch project.Status {
	case model.StatusPossiblyStuck:
		return "possibly stuck"
	case model.StatusActive:
		if !hasSummary {
			return "active work in progress"
		}
	case model.StatusIdle:
		if !hasSummary {
			return "idle"
		}
	}
	if project.SnoozedUntil != nil {
		return "snoozed"
	}
	return ""
}

func materialRepoStatus(project ProjectBrief) []string {
	var parts []string
	if project.RepoConflict {
		parts = append(parts, "conflict")
	}
	switch project.RepoSyncStatus {
	case model.RepoSyncBehind:
		parts = append(parts, fmt.Sprintf("behind -%d", project.RepoBehindCount))
	case model.RepoSyncDiverged:
		parts = append(parts, fmt.Sprintf("diverged +%d/-%d", project.RepoAheadCount, project.RepoBehindCount))
	}
	if project.RepoDirty && project.Status != model.StatusActive {
		parts = append(parts, "dirty without active work context")
	}
	return parts
}

func projectReferenceMetadata(project ProjectBrief, now time.Time) string {
	var parts []string
	if path := strings.TrimSpace(project.Path); path != "" {
		parts = append(parts, "path="+path)
	}
	if project.Status != "" {
		parts = append(parts, "status="+string(project.Status))
	}
	if branch := strings.TrimSpace(project.RepoBranch); branch != "" {
		parts = append(parts, "branch="+branch)
	}
	repoParts := projectRepoReferenceParts(project)
	if len(repoParts) > 0 {
		parts = append(parts, "repo="+strings.Join(repoParts, ", "))
	}
	if project.AttentionScore > 0 {
		parts = append(parts, fmt.Sprintf("attention=%d", project.AttentionScore))
	}
	if !project.LastActivity.IsZero() {
		parts = append(parts, "last_activity="+relativeAge(now, project.LastActivity))
	}
	if project.SnoozedUntil != nil {
		parts = append(parts, "snoozed=true")
	}
	return strings.Join(parts, "; ")
}

func projectRepoReferenceParts(project ProjectBrief) []string {
	var parts []string
	if project.RepoDirty {
		parts = append(parts, "dirty")
	}
	if project.RepoConflict {
		parts = append(parts, "conflict")
	}
	if sync := repoSyncLabel(project.RepoSyncStatus, project.RepoAheadCount, project.RepoBehindCount); sync != "" {
		parts = append(parts, sync)
	}
	return parts
}

func projectReasonTexts(project ProjectBrief, limit int) []string {
	if limit <= 0 {
		return nil
	}
	reasons := make([]string, 0, minInt(limit, len(project.Reasons)))
	for _, reason := range project.Reasons {
		if len(reasons) >= limit {
			break
		}
		if text := strings.TrimSpace(reason.Text); text != "" {
			reasons = append(reasons, text)
		}
	}
	return reasons
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

func ageAtTime(now, at time.Time) string {
	if at.IsZero() {
		return "unknown"
	}
	if now.IsZero() {
		now = time.Now()
	}
	d := now.Sub(at)
	prefix := ""
	if d < 0 {
		d = -d
		prefix = "-"
	}
	switch {
	case d < time.Minute:
		return prefix + "under 1m"
	case d < time.Hour:
		return fmt.Sprintf("%s%dm", prefix, int(d.Minutes()))
	case d < 24*time.Hour:
		return fmt.Sprintf("%s%dh", prefix, int(d.Hours()))
	default:
		return fmt.Sprintf("%s%dd", prefix, int(d.Hours()/24))
	}
}

func formatBossTimestamp(t time.Time) string {
	if t.IsZero() {
		return "unknown"
	}
	return t.Format(time.RFC3339)
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
