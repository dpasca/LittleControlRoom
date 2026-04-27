package boss

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"path/filepath"
	"strings"
	"time"

	"lcroom/internal/config"
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
	bossActionSearchContext          = "search_context"
	bossActionSearchBossSessions     = "search_boss_sessions"

	bossToolResultLimit = 8000
)

type bossAction struct {
	Kind              string `json:"kind"`
	Answer            string `json:"answer"`
	Target            string `json:"target"`
	Query             string `json:"query"`
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
	SearchContext(ctx context.Context, req model.ContextSearchRequest) ([]model.ContextSearchResult, error)
	SampleProjectSessionContext(ctx context.Context, projectPath string, limit int) ([]model.SessionContextSample, error)
}

type QueryExecutor struct {
	store        bossStoreReader
	bossSessions *bossSessionStore
	nowFn        func() time.Time
}

type ViewContext struct {
	Active              bool
	Embedded            bool
	Loading             bool
	AllProjectCount     int
	VisibleProjectCount int
	FocusedPane         string
	SortMode            string
	Visibility          string
	Filter              string
	Status              string
	PrivacyMode         bool
	PrivacyPatterns     []string
}

func newQueryExecutor(store bossStoreReader) *QueryExecutor {
	return newQueryExecutorWithBossSessions(store, nil)
}

func newQueryExecutorWithBossSessions(store bossStoreReader, bossSessions *bossSessionStore) *QueryExecutor {
	if store == nil && bossSessions == nil {
		return nil
	}
	return &QueryExecutor{
		store:        store,
		bossSessions: bossSessions,
		nowFn:        time.Now,
	}
}

func (e *QueryExecutor) Execute(ctx context.Context, action bossAction, snapshot StateSnapshot, view ViewContext) (bossToolResult, error) {
	if e == nil {
		return bossToolResult{}, errors.New("boss query tools are not connected to the project store")
	}
	kind := normalizeBossActionKind(action.Kind)
	if kind == bossActionSearchBossSessions {
		return e.searchBossSessions(ctx, action)
	}
	if e.store == nil {
		return bossToolResult{}, errors.New("boss query tools are not connected to the project store")
	}
	switch kind {
	case bossActionListProjects:
		return e.listProjects(ctx, action, view)
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
	case bossActionSearchContext:
		return e.searchContext(ctx, action, view)
	default:
		return bossToolResult{}, fmt.Errorf("unknown boss action kind %q", strings.TrimSpace(action.Kind))
	}
}

func (e *QueryExecutor) searchBossSessions(ctx context.Context, action bossAction) (bossToolResult, error) {
	if e == nil || e.bossSessions == nil {
		return clippedToolResult(bossActionSearchBossSessions, `<boss_session_search matches="0"><note>Boss chat session search is not connected.</note></boss_session_search>`), nil
	}
	query := strings.TrimSpace(action.Query)
	if query == "" {
		query = strings.TrimSpace(action.Target)
	}
	if query == "" {
		return clippedToolResult(bossActionSearchBossSessions, `<boss_session_search matches="0"><note>Boss chat session search needs a non-empty query.</note></boss_session_search>`), nil
	}
	results, err := e.bossSessions.searchSessions(ctx, query, clampBossLimit(action.Limit, 6, 16))
	if err != nil {
		return bossToolResult{}, err
	}
	return clippedToolResult(bossActionSearchBossSessions, formatBossSessionSearchXML(query, results, e.now())), nil
}

func (e *QueryExecutor) listProjects(ctx context.Context, action bossAction, view ViewContext) (bossToolResult, error) {
	projects, err := e.store.ListProjects(ctx, action.IncludeHistorical)
	if err != nil {
		return bossToolResult{}, err
	}
	projects = filterProjectSummariesForBossPrivacy(projects, view)
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
		lines = append(lines, fmt.Sprintf("%d. %s", i+1, operationalProjectLine(brief, now)))
		if metadata := projectReferenceMetadata(brief, now); metadata != "" {
			lines = append(lines, "   reference metadata: "+metadata)
		}
	}
	if len(projects) == 0 {
		lines = append(lines, "No projects matched the current store query.")
	}
	return clippedToolResult(bossActionListProjects, strings.Join(lines, "\n")), nil
}

func filterProjectSummariesForBossPrivacy(projects []model.ProjectSummary, view ViewContext) []model.ProjectSummary {
	if !view.PrivacyMode {
		return projects
	}
	return filterProjectSummariesByPrivacy(projects, view.PrivacyPatterns)
}

func (e *QueryExecutor) projectDetail(ctx context.Context, action bossAction, view ViewContext) (bossToolResult, error) {
	path, _, err := e.resolveProjectPath(ctx, action, view)
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
	lines := []string{"Project detail:"}
	if samples, err := e.store.SampleProjectSessionContext(ctx, path, 1); err == nil && len(samples) > 0 {
		lines = append(lines, "Live assistant session context:")
		for _, sample := range samples {
			line := fmt.Sprintf("- %s %s", sample.Source, sample.ExternalID())
			if !sample.UpdatedAt.IsZero() {
				line += ", sampled artifact updated " + relativeAge(now, sample.UpdatedAt)
			}
			if sample.LatestTurnStateKnown && !sample.LatestTurnCompleted {
				line += ", latest turn open"
			} else if sample.ArtifactUpdatedAfterScan {
				line += ", artifact moved since last scan"
			}
			lines = append(lines, line)
			for _, excerptLine := range liveSessionSampleLines(sample.Text) {
				lines = append(lines, "  "+clipText(excerptLine, 360))
			}
		}
	}
	lines = append(lines, "Operational snapshot:", "- "+operationalProjectSubstanceLine(brief, now))
	if detail.LatestSessionClassification != nil {
		c := detail.LatestSessionClassification
		lines = append(lines, "Assessment evidence:")
		if c.Summary != "" {
			lines = append(lines, "- "+clipText(c.Summary, 260))
		}
		if c.LastError != "" {
			lines = append(lines, "- last error: "+clipText(c.LastError, 260))
		}
		lines = append(lines, fmt.Sprintf("- reference metadata: status=%s category=%s confidence=%.2f", c.Status, c.Category, c.Confidence))
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
	reference := "name=" + brief.Name
	if metadata := projectReferenceMetadata(brief, now); metadata != "" {
		reference += "; " + metadata
	}
	lines = append(lines,
		"Reference metadata (use only for disambiguation/blockers):",
		"- "+reference,
	)
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

func liveSessionSampleLines(text string) []string {
	rawLines := strings.Split(strings.TrimSpace(strings.ReplaceAll(text, "\r\n", "\n")), "\n")
	lines := make([]string, 0, len(rawLines))
	for _, line := range rawLines {
		if line = strings.TrimSpace(line); line != "" {
			lines = append(lines, line)
		}
	}
	return lines
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
	if view.PrivacyMode && path == "" {
		privatePaths, err := e.privateProjectPaths(ctx, action.IncludeHistorical, view)
		if err != nil {
			return bossToolResult{}, err
		}
		items = filterSessionClassificationsForBossPrivacy(items, privatePaths)
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
	projects = filterProjectSummariesForBossPrivacy(projects, view)
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
		strings.EqualFold(strings.TrimSpace(action.Target), "selected")
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

func (e *QueryExecutor) searchContext(ctx context.Context, action bossAction, view ViewContext) (bossToolResult, error) {
	query := strings.TrimSpace(action.Query)
	if query == "" {
		query = strings.TrimSpace(action.Target)
	}
	if query == "" {
		return clippedToolResult(bossActionSearchContext, "Context search needs a non-empty query."), nil
	}

	path := strings.TrimSpace(action.ProjectPath)
	note := ""
	if path != "" || strings.TrimSpace(action.ProjectName) != "" || strings.EqualFold(strings.TrimSpace(action.Target), "selected") {
		resolved, resolvedNote, err := e.resolveProjectPath(ctx, action, view)
		if err != nil {
			return bossToolResult{}, err
		}
		path = resolved
		note = resolvedNote
	}

	results, err := e.store.SearchContext(ctx, model.ContextSearchRequest{
		Query:             query,
		ProjectPath:       path,
		IncludeHistorical: action.IncludeHistorical,
		Limit:             clampBossLimit(action.Limit, 8, 24),
	})
	if err != nil {
		return bossToolResult{}, err
	}
	if view.PrivacyMode {
		privatePaths, err := e.privateProjectPaths(ctx, action.IncludeHistorical, view)
		if err != nil {
			return bossToolResult{}, err
		}
		results = filterContextSearchResultsForBossPrivacy(results, view, privatePaths)
	}

	now := e.now()
	lines := []string{
		fmt.Sprintf("Context search for %q: %d matches.", query, len(results)),
		"Internal routing note: use matches to choose project/context; do not present alias-to-project/repo mappings unless the user asks what or where the alias is.",
		"Query time: " + formatBossTimestamp(now) + ".",
	}
	if action.IncludeHistorical {
		lines[0] += " Historical/out-of-scope projects are included."
	}
	if path != "" {
		lines[0] += " Reference project path: " + path + "."
	}
	if note != "" {
		lines = append(lines, "Target note: "+note)
	}
	for i, result := range results {
		label := result.Source
		if label == "" {
			label = "context"
		}
		line := fmt.Sprintf("%d. [%s] internal match", i+1, label)
		metadata := []string{}
		if project := strings.TrimSpace(firstNonEmpty(result.ProjectName, result.Title, result.ProjectPath)); project != "" {
			metadata = append(metadata, "project="+project)
		}
		if path := strings.TrimSpace(result.ProjectPath); path != "" {
			metadata = append(metadata, "path="+path)
		}
		if result.SessionID != "" {
			metadata = append(metadata, "session="+result.SessionID)
		}
		if !result.UpdatedAt.IsZero() {
			metadata = append(metadata, "updated_at="+formatBossTimestamp(result.UpdatedAt))
			metadata = append(metadata, "age_at_query="+ageAtTime(now, result.UpdatedAt))
		}
		lines = append(lines, line)
		if len(metadata) > 0 {
			lines = append(lines, "   reference metadata: "+strings.Join(metadata, "; "))
		}
		if title := strings.TrimSpace(result.Title); title != "" && title != result.ProjectName {
			lines = append(lines, "   title: "+clipText(title, 220))
		}
		if snippet := strings.TrimSpace(result.Snippet); snippet != "" {
			lines = append(lines, "   snippet: "+clipText(snippet, 420))
		}
	}
	if len(results) == 0 {
		lines = append(lines, "No project summaries, assessments, or cached session transcripts matched that query.")
	}
	return clippedToolResult(bossActionSearchContext, strings.Join(lines, "\n")), nil
}

func (e *QueryExecutor) resolveProjectPath(ctx context.Context, action bossAction, view ViewContext) (string, string, error) {
	if strings.EqualFold(strings.TrimSpace(action.Target), "selected") {
		return "", "", errors.New("boss chat cannot use the hidden classic TUI selection; ask for a project name or path")
	}
	if path := strings.TrimSpace(action.ProjectPath); path != "" {
		path = filepath.Clean(path)
		project, err := e.store.GetProjectSummary(ctx, path, true)
		if err != nil {
			if errors.Is(err, sql.ErrNoRows) {
				return path, "exact path supplied by boss chat, but no project summary matched", nil
			}
			return "", "", err
		}
		if bossProjectHiddenByPrivacy(project, view) {
			return "", "", fmt.Errorf("project is hidden while privacy mode is enabled")
		}
		return path, "exact project path supplied by boss chat", nil
	}
	if name := strings.TrimSpace(action.ProjectName); name != "" {
		projects, err := e.store.ListProjects(ctx, true)
		if err != nil {
			return "", "", err
		}
		projects = filterProjectSummariesForBossPrivacy(projects, view)
		var matches []model.ProjectSummary
		for _, project := range projects {
			if strings.EqualFold(strings.TrimSpace(project.Name), name) || strings.EqualFold(displayProjectName(project), name) {
				matches = append(matches, project)
			}
		}
		switch len(matches) {
		case 0:
			return e.resolveProjectPathFromContextSearch(ctx, name, view)
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
	return "", "", errors.New("project query needs a project_path or exact project_name")
}

func (e *QueryExecutor) resolveProjectPathFromContextSearch(ctx context.Context, name string, view ViewContext) (string, string, error) {
	results, err := e.store.SearchContext(ctx, model.ContextSearchRequest{
		Query:             name,
		IncludeHistorical: true,
		Limit:             8,
	})
	if err != nil {
		return "", "", err
	}
	if view.PrivacyMode {
		privatePaths, err := e.privateProjectPaths(ctx, true, view)
		if err != nil {
			return "", "", err
		}
		results = filterContextSearchResultsForBossPrivacy(results, view, privatePaths)
	}

	paths := make([]string, 0, len(results))
	seen := map[string]struct{}{}
	for _, result := range results {
		path := strings.TrimSpace(result.ProjectPath)
		if path == "" {
			continue
		}
		if _, ok := seen[path]; ok {
			continue
		}
		seen[path] = struct{}{}
		paths = append(paths, path)
	}

	switch len(paths) {
	case 0:
		return "", "", fmt.Errorf("no project exactly matched name %q and context search found no project", name)
	case 1:
		return paths[0], fmt.Sprintf("resolved %q through context search", name), nil
	default:
		return "", "", fmt.Errorf("project name %q did not exactly match; context search matched multiple projects: %s", name, strings.Join(paths, ", "))
	}
}

func (e *QueryExecutor) privateProjectPaths(ctx context.Context, includeHistorical bool, view ViewContext) (map[string]struct{}, error) {
	privatePaths := map[string]struct{}{}
	if !view.PrivacyMode || len(view.PrivacyPatterns) == 0 {
		return privatePaths, nil
	}
	projects, err := e.store.ListProjects(ctx, includeHistorical)
	if err != nil {
		return nil, err
	}
	for _, project := range projects {
		if bossProjectHiddenByPrivacy(project, view) {
			privatePaths[filepath.Clean(strings.TrimSpace(project.Path))] = struct{}{}
		}
	}
	return privatePaths, nil
}

func bossProjectHiddenByPrivacy(project model.ProjectSummary, view ViewContext) bool {
	return view.PrivacyMode && config.MatchesPrivacyPattern(project.Name, view.PrivacyPatterns)
}

func filterSessionClassificationsForBossPrivacy(items []model.SessionClassification, privatePaths map[string]struct{}) []model.SessionClassification {
	if len(items) == 0 || len(privatePaths) == 0 {
		return items
	}
	filtered := make([]model.SessionClassification, 0, len(items))
	for _, item := range items {
		if _, private := privatePaths[filepath.Clean(strings.TrimSpace(item.ProjectPath))]; !private {
			filtered = append(filtered, item)
		}
	}
	return filtered
}

func filterContextSearchResultsForBossPrivacy(results []model.ContextSearchResult, view ViewContext, privatePaths map[string]struct{}) []model.ContextSearchResult {
	if len(results) == 0 || (!view.PrivacyMode && len(privatePaths) == 0) {
		return results
	}
	filtered := make([]model.ContextSearchResult, 0, len(results))
	for _, result := range results {
		if config.MatchesPrivacyPattern(result.ProjectName, view.PrivacyPatterns) ||
			config.MatchesPrivacyPattern(result.Title, view.PrivacyPatterns) {
			continue
		}
		if _, private := privatePaths[filepath.Clean(strings.TrimSpace(result.ProjectPath))]; private {
			continue
		}
		filtered = append(filtered, result)
	}
	return filtered
}

func BuildViewContextBrief(view ViewContext, now time.Time) string {
	if now.IsZero() {
		now = time.Now()
	}
	if !view.Active && view.AllProjectCount == 0 && view.VisibleProjectCount == 0 {
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
	if view.PrivacyMode {
		lines = append(lines, "- privacy mode: enabled")
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
	return strings.Join(lines, "\n")
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
