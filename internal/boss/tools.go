package boss

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"path/filepath"
	"strings"
	"time"

	"lcroom/internal/model"
)

const (
	bossActionAnswer                 = "answer"
	bossActionListProjects           = "list_projects"
	bossActionProjectDetail          = "project_detail"
	bossActionSessionClassifications = "session_classifications"
	bossActionTodoReport             = "todo_report"
	bossActionCurrentTUI             = "current_tui"
	bossActionAssessmentQueue        = "assessment_queue"

	bossToolResultLimit = 8000
)

type bossAction struct {
	Kind              string `json:"kind"`
	Answer            string `json:"answer"`
	Target            string `json:"target"`
	ProjectPath       string `json:"project_path"`
	ProjectName       string `json:"project_name"`
	SessionID         string `json:"session_id"`
	IncludeHistorical bool   `json:"include_historical"`
	Limit             int    `json:"limit"`
	Reason            string `json:"reason"`
}

type bossToolResult struct {
	Name string
	Text string
}

type bossStoreReader interface {
	ListProjects(ctx context.Context, includeHistorical bool) ([]model.ProjectSummary, error)
	GetProjectSummary(ctx context.Context, projectPath string, includeHistorical bool) (model.ProjectSummary, error)
	GetProjectDetail(ctx context.Context, path string, eventLimit int) (model.ProjectDetail, error)
	ListSessionClassifications(ctx context.Context, projectPath, sessionID string) ([]model.SessionClassification, error)
	GetSessionClassificationCounts(ctx context.Context, inScopeOnly bool) (map[model.SessionClassificationStatus]int, error)
}

type QueryExecutor struct {
	store bossStoreReader
	nowFn func() time.Time
}

type ViewContext struct {
	Active              bool
	Embedded            bool
	Loading             bool
	AllProjectCount     int
	VisibleProjectCount int
	SelectedIndex       int
	SelectedProject     ProjectViewContext
	FocusedPane         string
	SortMode            string
	Visibility          string
	Filter              string
	Status              string
	DetailProjectPath   string
	DetailOpenTODOCount int
	DetailReasonCount   int
	DetailSessionCount  int
	DetailRecentEvents  int
	DetailLatestSummary string
}

type ProjectViewContext struct {
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
	LatestSummary        string
	LatestCompleted      string
	LatestCategory       model.SessionCategory
	ClassificationStatus model.SessionClassificationStatus
}

func ProjectViewFromSummary(project model.ProjectSummary) ProjectViewContext {
	return ProjectViewContext{
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
		LatestSummary:        strings.TrimSpace(project.LatestSessionSummary),
		LatestCompleted:      strings.TrimSpace(project.LatestCompletedSessionSummary),
		LatestCategory:       project.LatestSessionClassificationType,
		ClassificationStatus: project.LatestSessionClassification,
	}
}

func newQueryExecutor(store bossStoreReader) *QueryExecutor {
	if store == nil {
		return nil
	}
	return &QueryExecutor{
		store: store,
		nowFn: time.Now,
	}
}

func (e *QueryExecutor) Execute(ctx context.Context, action bossAction, snapshot StateSnapshot, view ViewContext) (bossToolResult, error) {
	if e == nil || e.store == nil {
		return bossToolResult{}, errors.New("boss query tools are not connected to the project store")
	}
	kind := normalizeBossActionKind(action.Kind)
	switch kind {
	case bossActionListProjects:
		return e.listProjects(ctx, action)
	case bossActionProjectDetail:
		return e.projectDetail(ctx, action, view)
	case bossActionSessionClassifications:
		return e.sessionClassifications(ctx, action, view)
	case bossActionTodoReport:
		return e.todoReport(ctx, action, view)
	case bossActionCurrentTUI:
		return bossToolResult{Name: bossActionCurrentTUI, Text: e.currentTUI(snapshot, view)}, nil
	case bossActionAssessmentQueue:
		return e.assessmentQueue(ctx)
	default:
		return bossToolResult{}, fmt.Errorf("unknown boss action kind %q", strings.TrimSpace(action.Kind))
	}
}

func (e *QueryExecutor) listProjects(ctx context.Context, action bossAction) (bossToolResult, error) {
	projects, err := e.store.ListProjects(ctx, action.IncludeHistorical)
	if err != nil {
		return bossToolResult{}, err
	}
	limit := clampBossLimit(action.Limit, 12, 40)
	now := e.now()
	lines := []string{fmt.Sprintf("Project list: showing %d of %d projects.", minInt(limit, len(projects)), len(projects))}
	if action.IncludeHistorical {
		lines[0] += " Historical/out-of-scope projects are included."
	}
	for i, project := range projects {
		if i >= limit {
			break
		}
		brief := projectBriefFromSummary(project)
		lines = append(lines, fmt.Sprintf("%d. path: %s | %s", i+1, brief.Path, briefLine(brief, now)))
	}
	if len(projects) == 0 {
		lines = append(lines, "No projects matched the current store query.")
	}
	return clippedToolResult(bossActionListProjects, strings.Join(lines, "\n")), nil
}

func (e *QueryExecutor) projectDetail(ctx context.Context, action bossAction, view ViewContext) (bossToolResult, error) {
	path, note, err := e.resolveProjectPath(ctx, action, view)
	if err != nil {
		return bossToolResult{}, err
	}
	detail, err := e.store.GetProjectDetail(ctx, path, clampBossLimit(action.Limit, 8, 30))
	if errors.Is(err, sql.ErrNoRows) {
		return clippedToolResult(bossActionProjectDetail, "Project detail not found for path: "+path), nil
	}
	if err != nil {
		return bossToolResult{}, err
	}

	now := e.now()
	brief := projectBriefFromSummary(detail.Summary)
	if len(detail.Reasons) > 0 {
		brief.Reasons = append([]model.AttentionReason(nil), detail.Reasons...)
	}
	lines := []string{
		"Project detail:",
		fmt.Sprintf("- name: %s", brief.Name),
		fmt.Sprintf("- path: %s", brief.Path),
		fmt.Sprintf("- state: %s", briefLine(brief, now)),
	}
	if note != "" {
		lines = append(lines, "- target note: "+note)
	}
	if len(detail.Reasons) > 0 {
		lines = append(lines, "Attention reasons:")
		for _, reason := range detail.Reasons {
			if text := strings.TrimSpace(reason.Text); text != "" {
				lines = append(lines, fmt.Sprintf("- %s (weight %d)", clipText(text, 220), reason.Weight))
			}
		}
	}
	openTodos := openTodosOnly(detail.Todos)
	if len(openTodos) > 0 {
		lines = append(lines, fmt.Sprintf("Open TODOs (%d):", len(openTodos)))
		for i, item := range openTodos {
			if i >= clampBossLimit(action.Limit, 8, 20) {
				break
			}
			lines = append(lines, fmt.Sprintf("- #%d %s", item.ID, clipText(item.Text, 240)))
		}
	}
	if detail.LatestSessionClassification != nil {
		c := detail.LatestSessionClassification
		lines = append(lines,
			"Latest assessment:",
			fmt.Sprintf("- status: %s; category: %s; confidence: %.2f", c.Status, c.Category, c.Confidence),
		)
		if c.Summary != "" {
			lines = append(lines, "- summary: "+clipText(c.Summary, 260))
		}
		if c.LastError != "" {
			lines = append(lines, "- last error: "+clipText(c.LastError, 260))
		}
	}
	if len(detail.Sessions) > 0 {
		lines = append(lines, "Recent sessions:")
		for i, session := range detail.Sessions {
			if i >= 6 {
				break
			}
			line := fmt.Sprintf("- %s %s, last event %s", session.Source, session.ExternalID(), relativeAge(now, session.LastEventAt))
			if session.LatestTurnStateKnown {
				if session.LatestTurnCompleted {
					line += ", latest turn completed"
				} else {
					line += ", latest turn may still be open"
				}
			}
			lines = append(lines, line)
		}
	}
	if len(detail.RecentEvents) > 0 {
		lines = append(lines, "Recent events:")
		for i, event := range detail.RecentEvents {
			if i >= 6 {
				break
			}
			lines = append(lines, fmt.Sprintf("- %s %s: %s", relativeAge(now, event.At), event.Type, clipText(event.Payload, 220)))
		}
	}
	return clippedToolResult(bossActionProjectDetail, strings.Join(lines, "\n")), nil
}

func (e *QueryExecutor) sessionClassifications(ctx context.Context, action bossAction, view ViewContext) (bossToolResult, error) {
	path := strings.TrimSpace(action.ProjectPath)
	note := ""
	if (path == "" && strings.TrimSpace(action.ProjectName) != "") || strings.EqualFold(strings.TrimSpace(action.Target), "selected") {
		resolved, resolvedNote, err := e.resolveProjectPath(ctx, action, view)
		if err != nil {
			return bossToolResult{}, err
		}
		path = resolved
		note = resolvedNote
	}
	items, err := e.store.ListSessionClassifications(ctx, path, strings.TrimSpace(action.SessionID))
	if err != nil {
		return bossToolResult{}, err
	}
	limit := clampBossLimit(action.Limit, 10, 40)
	lines := []string{fmt.Sprintf("Session assessments: showing %d of %d.", minInt(limit, len(items)), len(items))}
	if path != "" {
		lines[0] += " Project path: " + path + "."
	}
	if note != "" {
		lines = append(lines, "Target note: "+note)
	}
	for i, item := range items {
		if i >= limit {
			break
		}
		line := fmt.Sprintf("- %s | project: %s | status: %s", item.ExternalID(), item.ProjectPath, item.Status)
		if item.Category != "" {
			line += " | category: " + string(item.Category)
		}
		if item.Stage != "" {
			line += " | stage: " + string(item.Stage)
		}
		if item.Summary != "" {
			line += " | " + clipText(item.Summary, 180)
		}
		if item.LastError != "" {
			line += " | error: " + clipText(item.LastError, 180)
		}
		lines = append(lines, line)
	}
	if len(items) == 0 {
		lines = append(lines, "No session assessments matched this query.")
	}
	return clippedToolResult(bossActionSessionClassifications, strings.Join(lines, "\n")), nil
}

func (e *QueryExecutor) todoReport(ctx context.Context, action bossAction, view ViewContext) (bossToolResult, error) {
	projects, err := e.store.ListProjects(ctx, action.IncludeHistorical)
	if err != nil {
		return bossToolResult{}, err
	}
	limit := clampBossLimit(action.Limit, 8, 20)
	lines := []string{"TODO report:"}
	shown := 0
	for _, project := range projects {
		if project.OpenTODOCount <= 0 {
			continue
		}
		lines = append(lines, fmt.Sprintf("- %s | path: %s | %d open TODOs | attention %d", displayProjectName(project), project.Path, project.OpenTODOCount, project.AttentionScore))
		shown++
		if shown >= limit {
			break
		}
	}
	if shown == 0 {
		lines = append(lines, "No open TODOs found in the visible project set.")
	}

	hasTarget := strings.TrimSpace(action.ProjectPath) != "" ||
		strings.TrimSpace(action.ProjectName) != "" ||
		strings.EqualFold(strings.TrimSpace(action.Target), "selected") ||
		strings.TrimSpace(view.SelectedProject.Path) != ""
	path, note, err := e.resolveProjectPath(ctx, action, view)
	if err == nil && path != "" {
		detail, detailErr := e.store.GetProjectDetail(ctx, path, 0)
		if detailErr == nil {
			openTodos := openTodosOnly(detail.Todos)
			lines = append(lines, "", fmt.Sprintf("Selected/target project TODOs: %s (%s)", displayProjectName(detail.Summary), path))
			if note != "" {
				lines = append(lines, "Target note: "+note)
			}
			if len(openTodos) == 0 {
				lines = append(lines, "- no open TODOs")
			}
			for i, item := range openTodos {
				if i >= limit {
					break
				}
				lines = append(lines, fmt.Sprintf("- #%d %s", item.ID, clipText(item.Text, 240)))
			}
		} else if hasTarget {
			lines = append(lines, "", "Target project TODO detail unavailable: "+detailErr.Error())
		}
	} else if hasTarget && err != nil {
		lines = append(lines, "", "Target project TODO detail unavailable: "+err.Error())
	}
	return clippedToolResult(bossActionTodoReport, strings.Join(lines, "\n")), nil
}

func (e *QueryExecutor) currentTUI(snapshot StateSnapshot, view ViewContext) string {
	parts := []string{BuildStateBrief(snapshot, e.now())}
	if brief := BuildViewContextBrief(view, e.now()); brief != "" {
		parts = append(parts, brief)
	}
	return strings.Join(parts, "\n\n")
}

func (e *QueryExecutor) assessmentQueue(ctx context.Context) (bossToolResult, error) {
	counts, err := e.store.GetSessionClassificationCounts(ctx, true)
	if err != nil {
		return bossToolResult{}, err
	}
	lines := []string{
		"AI assessment queue:",
		fmt.Sprintf("- pending: %d", counts[model.ClassificationPending]),
		fmt.Sprintf("- running: %d", counts[model.ClassificationRunning]),
		fmt.Sprintf("- failed: %d", counts[model.ClassificationFailed]),
		fmt.Sprintf("- completed: %d", counts[model.ClassificationCompleted]),
	}
	return clippedToolResult(bossActionAssessmentQueue, strings.Join(lines, "\n")), nil
}

func (e *QueryExecutor) resolveProjectPath(ctx context.Context, action bossAction, view ViewContext) (string, string, error) {
	if strings.EqualFold(strings.TrimSpace(action.Target), "selected") {
		if path := strings.TrimSpace(view.SelectedProject.Path); path != "" {
			return path, "using the project selected in the classic TUI", nil
		}
		return "", "", errors.New("boss chat asked for the selected project, but the current TUI selection is unavailable")
	}
	if path := strings.TrimSpace(action.ProjectPath); path != "" {
		path = filepath.Clean(path)
		if _, err := e.store.GetProjectSummary(ctx, path, true); err != nil {
			if errors.Is(err, sql.ErrNoRows) {
				return path, "exact path supplied by boss chat, but no project summary matched", nil
			}
			return "", "", err
		}
		return path, "exact project path supplied by boss chat", nil
	}
	if name := strings.TrimSpace(action.ProjectName); name != "" {
		projects, err := e.store.ListProjects(ctx, true)
		if err != nil {
			return "", "", err
		}
		var matches []model.ProjectSummary
		for _, project := range projects {
			if strings.EqualFold(strings.TrimSpace(project.Name), name) || strings.EqualFold(displayProjectName(project), name) {
				matches = append(matches, project)
			}
		}
		switch len(matches) {
		case 0:
			return "", "", fmt.Errorf("no project exactly matched name %q", name)
		case 1:
			return strings.TrimSpace(matches[0].Path), fmt.Sprintf("resolved exact project name %q", name), nil
		default:
			paths := make([]string, 0, len(matches))
			for _, match := range matches {
				paths = append(paths, strings.TrimSpace(match.Path))
			}
			return "", "", fmt.Errorf("project name %q is ambiguous; matching paths: %s", name, strings.Join(paths, ", "))
		}
	}
	if path := strings.TrimSpace(view.SelectedProject.Path); path != "" {
		return path, "no explicit target supplied, so using the current TUI selection", nil
	}
	return "", "", errors.New("project query needs a project_path, exact project_name, or selected TUI project")
}

func BuildViewContextBrief(view ViewContext, now time.Time) string {
	if now.IsZero() {
		now = time.Now()
	}
	if !view.Active && view.AllProjectCount == 0 && view.VisibleProjectCount == 0 && strings.TrimSpace(view.SelectedProject.Path) == "" {
		return "Current TUI view: no embedded classic TUI context was supplied."
	}
	mode := "standalone boss mode"
	if view.Embedded {
		mode = "embedded over classic TUI"
	}
	lines := []string{
		"Current TUI view:",
		"- mode: " + mode,
		fmt.Sprintf("- project list: %d visible of %d known", view.VisibleProjectCount, view.AllProjectCount),
	}
	if view.SortMode != "" || view.Visibility != "" || view.Filter != "" {
		lines = append(lines, fmt.Sprintf("- list controls: sort=%s visibility=%s filter=%q", emptyLabel(view.SortMode), emptyLabel(view.Visibility), strings.TrimSpace(view.Filter)))
	}
	if view.FocusedPane != "" {
		lines = append(lines, "- focused pane before boss mode: "+view.FocusedPane)
	}
	if view.Loading {
		lines = append(lines, "- classic TUI was loading when boss mode opened")
	}
	if status := strings.TrimSpace(view.Status); status != "" {
		lines = append(lines, "- classic TUI status: "+clipText(status, 220))
	}
	if strings.TrimSpace(view.SelectedProject.Path) != "" {
		selected := view.SelectedProject.toBrief()
		lines = append(lines, fmt.Sprintf("- selected project #%d: %s | path: %s | %s", view.SelectedIndex+1, selected.Name, selected.Path, briefLine(selected, now)))
	}
	if strings.TrimSpace(view.DetailProjectPath) != "" {
		lines = append(lines, fmt.Sprintf("- detail panel path: %s; reasons=%d open_todos=%d sessions=%d recent_events=%d", view.DetailProjectPath, view.DetailReasonCount, view.DetailOpenTODOCount, view.DetailSessionCount, view.DetailRecentEvents))
		if summary := strings.TrimSpace(view.DetailLatestSummary); summary != "" {
			lines = append(lines, "- detail latest: "+clipText(summary, 220))
		}
	}
	return strings.Join(lines, "\n")
}

func (project ProjectViewContext) toBrief() ProjectBrief {
	path := strings.TrimSpace(project.Path)
	name := strings.TrimSpace(project.Name)
	if name == "" && path != "" {
		name = filepath.Base(path)
	}
	if name == "" {
		name = "untitled project"
	}
	return ProjectBrief{
		Name:                 name,
		Path:                 path,
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
		LatestSummary:        strings.TrimSpace(project.LatestSummary),
		LatestCompleted:      strings.TrimSpace(project.LatestCompleted),
		LatestCategory:       project.LatestCategory,
		ClassificationStatus: project.ClassificationStatus,
	}
}

func openTodosOnly(items []model.TodoItem) []model.TodoItem {
	out := make([]model.TodoItem, 0, len(items))
	for _, item := range items {
		if !item.Done {
			out = append(out, item)
		}
	}
	return out
}

func clippedToolResult(name, text string) bossToolResult {
	return bossToolResult{Name: name, Text: clipToolText(text, bossToolResultLimit)}
}

func clampBossLimit(value, defaultValue, maxValue int) int {
	if value <= 0 {
		value = defaultValue
	}
	if maxValue > 0 && value > maxValue {
		value = maxValue
	}
	if value < 1 {
		value = 1
	}
	return value
}

func normalizeBossActionKind(kind string) string {
	return strings.TrimSpace(strings.ToLower(kind))
}

func emptyLabel(value string) string {
	value = strings.TrimSpace(value)
	if value == "" {
		return "unset"
	}
	return value
}

func clipToolText(text string, limit int) string {
	text = strings.TrimSpace(strings.ReplaceAll(text, "\r\n", "\n"))
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

func (e *QueryExecutor) now() time.Time {
	if e != nil && e.nowFn != nil {
		return e.nowFn()
	}
	return time.Now()
}
