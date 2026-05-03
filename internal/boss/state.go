package boss

import (
	"context"
	"database/sql"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"lcroom/internal/config"
	"lcroom/internal/model"
	"lcroom/internal/service"
)

const (
	hotProjectLimit              = 8
	defaultAttentionProjectLimit = 4
	openAgentTaskLimit           = 4
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
	OpenAgentTasks         []AgentTaskBrief
}

type ProjectBrief struct {
	Name                 string
	Path                 string
	Kind                 model.ProjectKind
	Status               model.ProjectStatus
	AttentionScore       int
	LastActivity         time.Time
	WorktreeRootPath     string
	WorktreeKind         model.WorktreeKind
	RepoBranch           string
	RepoDirty            bool
	RepoConflict         bool
	RepoSyncStatus       model.RepoSyncStatus
	RepoAheadCount       int
	RepoBehindCount      int
	OpenTODOCount        int
	SnoozedUntil         *time.Time
	LatestFormat         string
	LatestSummary        string
	LatestCompleted      string
	LatestCategory       model.SessionCategory
	LatestCompletedKind  model.SessionCategory
	ClassificationStatus model.SessionClassificationStatus
	Reasons              []model.AttentionReason
}

type AgentTaskBrief struct {
	ID            string
	ParentTaskID  string
	Title         string
	EngineerName  string
	Kind          model.AgentTaskKind
	Status        model.AgentTaskStatus
	Summary       string
	Capabilities  []string
	Provider      model.SessionSource
	SessionID     string
	LastTouchedAt time.Time
	Resources     []model.AgentTaskResource
}

type StateSnapshotOptions struct {
	PrivacyMode     bool
	PrivacyPatterns []string
}

func LoadStateSnapshot(ctx context.Context, svc *service.Service, now time.Time, options ...StateSnapshotOptions) (StateSnapshot, error) {
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
	opts := stateSnapshotOptionsForService(svc)
	if len(options) > 0 {
		opts = options[0]
	}
	if opts.PrivacyMode {
		projects = filterProjectSummariesByPrivacy(projects, opts.PrivacyPatterns)
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
	if tasks, err := svc.ListOpenAgentTasks(ctx, openAgentTaskLimit); err == nil {
		if opts.PrivacyMode {
			tasks = filterAgentTasksForBossPrivacy(tasks, opts.PrivacyPatterns)
		}
		for _, task := range tasks {
			snapshot.OpenAgentTasks = append(snapshot.OpenAgentTasks, agentTaskBriefFromTask(task))
		}
	}

	for _, project := range selectRecentAttentionProjectsWithPresence(projects, hotProjectLimit, projectCurrentlyPresent) {
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

func stateSnapshotOptionsForService(svc *service.Service) StateSnapshotOptions {
	if svc == nil {
		return StateSnapshotOptions{}
	}
	cfg := svc.Config()
	return StateSnapshotOptions{
		PrivacyMode:     cfg.PrivacyMode,
		PrivacyPatterns: append([]string(nil), cfg.PrivacyPatterns...),
	}
}

func projectBriefFromSummary(project model.ProjectSummary) ProjectBrief {
	return ProjectBrief{
		Name:                 displayProjectName(project),
		Path:                 strings.TrimSpace(project.Path),
		Kind:                 model.NormalizeProjectKind(project.Kind),
		Status:               project.Status,
		AttentionScore:       project.AttentionScore,
		LastActivity:         project.LastActivity,
		WorktreeRootPath:     strings.TrimSpace(project.WorktreeRootPath),
		WorktreeKind:         project.WorktreeKind,
		RepoBranch:           strings.TrimSpace(project.RepoBranch),
		RepoDirty:            project.RepoDirty,
		RepoConflict:         project.RepoConflict,
		RepoSyncStatus:       project.RepoSyncStatus,
		RepoAheadCount:       project.RepoAheadCount,
		RepoBehindCount:      project.RepoBehindCount,
		OpenTODOCount:        project.OpenTODOCount,
		SnoozedUntil:         project.SnoozedUntil,
		LatestFormat:         strings.TrimSpace(project.LatestSessionFormat),
		LatestSummary:        strings.TrimSpace(project.LatestSessionSummary),
		LatestCompleted:      strings.TrimSpace(project.LatestCompletedSessionSummary),
		LatestCategory:       project.LatestSessionClassificationType,
		LatestCompletedKind:  project.LatestCompletedSessionClassificationType,
		ClassificationStatus: project.LatestSessionClassification,
	}
}

func agentTaskBriefFromTask(task model.AgentTask) AgentTaskBrief {
	resources := append([]model.AgentTaskResource(nil), task.Resources...)
	if len(resources) > 4 {
		resources = resources[:4]
	}
	return AgentTaskBrief{
		ID:            strings.TrimSpace(task.ID),
		ParentTaskID:  strings.TrimSpace(task.ParentTaskID),
		Title:         strings.TrimSpace(task.Title),
		EngineerName:  EngineerNameForKey("agent_task", task.ID),
		Kind:          model.NormalizeAgentTaskKind(task.Kind),
		Status:        model.NormalizeAgentTaskStatus(task.Status),
		Summary:       strings.TrimSpace(task.Summary),
		Capabilities:  append([]string(nil), task.Capabilities...),
		Provider:      model.NormalizeSessionSource(task.Provider),
		SessionID:     strings.TrimSpace(task.SessionID),
		LastTouchedAt: task.LastTouchedAt,
		Resources:     resources,
	}
}

func filterAgentTasksForBossPrivacy(tasks []model.AgentTask, patterns []string) []model.AgentTask {
	if len(tasks) == 0 || len(patterns) == 0 {
		return tasks
	}
	filtered := make([]model.AgentTask, 0, len(tasks))
	for _, task := range tasks {
		if !agentTaskHiddenByPrivacy(task, patterns) {
			filtered = append(filtered, task)
		}
	}
	return filtered
}

func filterAgentTaskBriefsForBossPrivacy(tasks []AgentTaskBrief, patterns []string) []AgentTaskBrief {
	if len(tasks) == 0 || len(patterns) == 0 {
		return tasks
	}
	filtered := make([]AgentTaskBrief, 0, len(tasks))
	for _, task := range tasks {
		if !agentTaskBriefHiddenByPrivacy(task, patterns) {
			filtered = append(filtered, task)
		}
	}
	return filtered
}

func agentTaskHiddenByPrivacy(task model.AgentTask, patterns []string) bool {
	if len(patterns) == 0 {
		return false
	}
	if bossPrivacyMatchesAny(patterns, task.Title, task.Summary, task.WorkspacePath) {
		return true
	}
	for _, resource := range task.Resources {
		if bossPrivacyMatchesAny(patterns, resource.ProjectPath, resource.Path, resource.Label, resource.RefID) {
			return true
		}
	}
	return false
}

func agentTaskBriefHiddenByPrivacy(task AgentTaskBrief, patterns []string) bool {
	if len(patterns) == 0 {
		return false
	}
	if bossPrivacyMatchesAny(patterns, task.Title, task.Summary) {
		return true
	}
	for _, resource := range task.Resources {
		if bossPrivacyMatchesAny(patterns, resource.ProjectPath, resource.Path, resource.Label, resource.RefID) {
			return true
		}
	}
	return false
}

func bossPrivacyMatchesAny(patterns []string, values ...string) bool {
	for _, value := range values {
		if config.MatchesPrivacyPattern(value, patterns) {
			return true
		}
	}
	return false
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
	if len(snapshot.OpenAgentTasks) > 0 {
		lines = append(lines, "Open delegated agent tasks (separate from project TODOs):")
		for _, task := range snapshot.OpenAgentTasks {
			lines = append(lines, "- "+operationalAgentTaskLine(task, now))
		}
	} else {
		lines = append(lines, "Open delegated agent tasks: none visible.")
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
	if len(snapshot.OpenAgentTasks) == 0 && len(snapshot.HotProjects) == 0 {
		return "Nothing needs attention yet.\nRun a scan or wait for project state to load."
	}
	lines := make([]string, 0, minInt(limit, len(snapshot.OpenAgentTasks)+len(snapshot.HotProjects)))
	for _, task := range snapshot.OpenAgentTasks {
		if len(lines) >= limit {
			break
		}
		lines = append(lines, compactAttentionAgentTaskKind(task)+" | "+compactAttentionAgentTaskLine(task, now))
	}
	for _, project := range snapshot.HotProjects {
		if len(lines) >= limit {
			break
		}
		parts := []string{shortAttentionState(project)}
		if repo := compactRepoFlag(project); repo != "" {
			parts = append(parts, repo)
		}
		if summary := bestProjectSummary(project); summary != "" {
			parts = append(parts, clipText(summary, 120))
		} else if !project.LastActivity.IsZero() {
			parts = append(parts, relativeAge(now, project.LastActivity))
		}
		lines = append(lines, strings.Join(parts, " | "))
	}
	return strings.Join(lines, "\n")
}

func compactAttentionAgentTaskKind(task AgentTaskBrief) string {
	switch model.NormalizeAgentTaskStatus(task.Status) {
	case model.AgentTaskStatusWaiting:
		return "review"
	default:
		return "agent"
	}
}

func compactAttentionAgentTaskLine(task AgentTaskBrief, now time.Time) string {
	parts := []string{strings.TrimSpace(task.Title)}
	if parts[0] == "" {
		parts[0] = strings.TrimSpace(task.ID)
	}
	if parts[0] == "" {
		parts[0] = "agent task"
	}
	engineerName := strings.TrimSpace(task.EngineerName)
	if engineerName != "" {
		parts = append(parts, engineerName)
	}
	status := model.NormalizeAgentTaskStatus(task.Status)
	if status == model.AgentTaskStatusWaiting {
		parts = append(parts, agentTaskDecisionQuestion(engineerName))
	} else if status != "" {
		parts = append(parts, agentTaskBriefStatusLabel(status))
	}
	if summary := strings.TrimSpace(task.Summary); summary != "" {
		parts = append(parts, clipText(cleanHandoffSummary(summary), 120))
	} else if status == model.AgentTaskStatusWaiting {
		if !task.LastTouchedAt.IsZero() {
			parts = append(parts, "touched "+relativeAge(now, task.LastTouchedAt))
		}
	} else if task.Provider != "" || strings.TrimSpace(task.SessionID) != "" {
		provider := string(model.NormalizeSessionSource(task.Provider))
		session := strings.TrimSpace(task.SessionID)
		parts = append(parts, strings.TrimSpace(provider+" "+session))
	} else if !task.LastTouchedAt.IsZero() {
		parts = append(parts, relativeAge(now, task.LastTouchedAt))
	}
	filtered := make([]string, 0, len(parts))
	for _, part := range parts {
		if text := strings.TrimSpace(part); text != "" {
			filtered = append(filtered, text)
		}
	}
	return strings.Join(filtered, " | ")
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

func operationalAgentTaskLine(task AgentTaskBrief, now time.Time) string {
	title := strings.TrimSpace(task.Title)
	if title == "" {
		title = "untitled task"
	}
	taskID := strings.TrimSpace(task.ID)
	parts := []string{fmt.Sprintf("%s (%s)", title, taskID)}
	if name := strings.TrimSpace(task.EngineerName); name != "" {
		parts = append(parts, "assigned: "+name)
	}
	if taskID != "" {
		parts = append(parts, "show: agent_task:"+taskID)
	}
	if parent := strings.TrimSpace(task.ParentTaskID); parent != "" {
		parts = append(parts, "parent: "+parent)
	}
	parts = append(parts, fmt.Sprintf("kind/status: %s/%s", task.Kind, agentTaskBriefStatusLabel(model.NormalizeAgentTaskStatus(task.Status))))
	if !task.LastTouchedAt.IsZero() {
		parts = append(parts, "touched "+relativeAge(now, task.LastTouchedAt))
	}
	if task.Provider != "" || strings.TrimSpace(task.SessionID) != "" {
		session := strings.TrimSpace(task.SessionID)
		if session == "" {
			session = "none"
		}
		provider := string(task.Provider)
		if provider == "" {
			provider = "none"
		}
		parts = append(parts, "engineer: "+provider+" "+session)
	}
	if summary := strings.TrimSpace(task.Summary); summary != "" {
		parts = append(parts, "brief: "+clipText(cleanHandoffSummary(summary), 140))
	}
	if len(task.Capabilities) > 0 {
		parts = append(parts, "capabilities: "+clipText(strings.Join(task.Capabilities, ", "), 100))
	}
	if resources := compactAgentTaskResources(task.Resources); resources != "" {
		parts = append(parts, "resources: "+resources)
	}
	return strings.Join(parts, "; ")
}

func agentTaskBriefStatusLabel(status model.AgentTaskStatus) string {
	switch model.NormalizeAgentTaskStatus(status) {
	case model.AgentTaskStatusWaiting:
		return "review"
	case model.AgentTaskStatusCompleted:
		return "done"
	case model.AgentTaskStatusArchived:
		return "archived"
	default:
		return "active"
	}
}

func compactAgentTaskResources(resources []model.AgentTaskResource) string {
	if len(resources) == 0 {
		return ""
	}
	parts := make([]string, 0, minInt(len(resources), 4))
	for _, resource := range resources {
		label := compactAgentTaskResource(resource)
		if label == "" {
			continue
		}
		parts = append(parts, label)
		if len(parts) >= 4 {
			break
		}
	}
	return strings.Join(parts, ", ")
}

func compactAgentTaskResource(resource model.AgentTaskResource) string {
	switch model.NormalizeAgentTaskResourceKind(resource.Kind) {
	case model.AgentTaskResourceProject:
		return "project " + firstNonEmpty(resource.ProjectPath, resource.Path, resource.Label)
	case model.AgentTaskResourceProcess:
		if resource.PID > 0 {
			return strings.TrimSpace(fmt.Sprintf("pid %d %s", resource.PID, strings.TrimSpace(resource.Label)))
		}
	case model.AgentTaskResourcePort:
		if resource.Port > 0 {
			return strings.TrimSpace(fmt.Sprintf("port %d %s", resource.Port, strings.TrimSpace(resource.Label)))
		}
	case model.AgentTaskResourceFile:
		return "file " + firstNonEmpty(resource.Path, resource.Label)
	case model.AgentTaskResourceAgentTask:
		return "task " + firstNonEmpty(resource.RefID, resource.Label)
	case model.AgentTaskResourceEngineerSession:
		session := strings.TrimSpace(resource.SessionID)
		if session == "" {
			return ""
		}
		provider := string(model.NormalizeSessionSource(resource.Provider))
		if provider == "" {
			return "session " + session
		}
		return provider + " session " + session
	}
	return strings.TrimSpace(resource.Label)
}

func selectRecentAttentionProjects(projects []model.ProjectSummary, limit int) []model.ProjectSummary {
	return selectRecentAttentionProjectsWithPresence(projects, limit, func(project model.ProjectSummary) bool {
		return project.PresentOnDisk
	})
}

func selectRecentAttentionProjectsWithPresence(projects []model.ProjectSummary, limit int, present func(model.ProjectSummary) bool) []model.ProjectSummary {
	if limit <= 0 || len(projects) == 0 {
		return nil
	}
	candidates := make([]model.ProjectSummary, 0, len(projects))
	for _, project := range projects {
		if present != nil && present(project) {
			candidates = append(candidates, project)
		}
	}
	if len(candidates) == 0 {
		candidates = projects
	}
	selected := append([]model.ProjectSummary(nil), candidates...)
	sort.SliceStable(selected, func(i, j int) bool {
		left := selected[i]
		right := selected[j]
		switch {
		case left.LastActivity.IsZero() != right.LastActivity.IsZero():
			return !left.LastActivity.IsZero()
		case !left.LastActivity.Equal(right.LastActivity):
			return left.LastActivity.After(right.LastActivity)
		case left.AttentionScore != right.AttentionScore:
			return left.AttentionScore > right.AttentionScore
		default:
			return displayProjectName(left) < displayProjectName(right)
		}
	})
	if len(selected) > limit {
		selected = selected[:limit]
	}
	return selected
}

func projectCurrentlyPresent(project model.ProjectSummary) bool {
	if !project.PresentOnDisk {
		return false
	}
	path := strings.TrimSpace(project.Path)
	if path == "" {
		return true
	}
	info, err := os.Stat(path)
	return err == nil && info.IsDir()
}

func filterProjectSummariesByPrivacy(projects []model.ProjectSummary, privacyPatterns []string) []model.ProjectSummary {
	if len(projects) == 0 || len(privacyPatterns) == 0 {
		return projects
	}
	filtered := make([]model.ProjectSummary, 0, len(projects))
	for _, project := range projects {
		if !config.MatchesPrivacyPattern(project.Name, privacyPatterns) {
			filtered = append(filtered, project)
		}
	}
	return filtered
}

func compactProjectName(project ProjectBrief) string {
	name := strings.TrimSpace(project.Name)
	if name == "" {
		name = "untitled project"
	}
	if model.NormalizeProjectKind(project.Kind) == model.ProjectKindScratchTask {
		return "[T] " + name
	}
	return name
}

func shortAttentionState(project ProjectBrief) string {
	state := string(project.Status)
	switch project.Status {
	case model.StatusPossiblyStuck:
		state = "stuck"
	case model.StatusActive:
		state = "active"
	case model.StatusIdle:
		state = "idle"
	case "":
		state = "unknown"
	}
	if project.AttentionScore > 0 {
		return fmt.Sprintf("%s %d", state, project.AttentionScore)
	}
	return state
}

func compactRepoFlag(project ProjectBrief) string {
	var parts []string
	if project.RepoConflict {
		parts = append(parts, "conflict")
	}
	if project.RepoDirty {
		parts = append(parts, "dirty")
	}
	if sync := repoSyncFlag(project.RepoSyncStatus, project.RepoAheadCount, project.RepoBehindCount); sync != "" {
		parts = append(parts, sync)
	}
	return strings.Join(parts, ", ")
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
	if project.WorktreeKind != "" {
		parts = append(parts, "worktree="+string(project.WorktreeKind))
		if root := strings.TrimSpace(project.WorktreeRootPath); root != "" && filepath.Clean(root) != filepath.Clean(project.Path) {
			parts = append(parts, "worktree_root="+root)
		}
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

func repoSyncFlag(status model.RepoSyncStatus, ahead, behind int) string {
	switch status {
	case model.RepoSyncNoUpstream:
		return "no upstream"
	default:
		return repoSyncLabel(status, ahead, behind)
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

func cleanHandoffSummary(text string) string {
	text = strings.Join(strings.Fields(strings.TrimSpace(text)), " ")
	text = strings.TrimRight(text, " \t\r\n:;,")
	text = strings.TrimSpace(text)
	if text == "" || strings.HasSuffix(text, "...") || strings.HasSuffix(text, "…") {
		return text
	}
	last := text[len(text)-1]
	if last == '.' || last == '!' || last == '?' {
		return text
	}
	return text + "."
}

func minInt(a, b int) int {
	if a < b {
		return a
	}
	return b
}
