package boss

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"time"

	"lcroom/internal/codexskills"
	"lcroom/internal/codexstate"
	"lcroom/internal/config"
	"lcroom/internal/control"
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
	bossActionProcessReport          = "process_report"
	bossActionSearchContext          = "search_context"
	bossActionSearchBossSessions     = "search_boss_sessions"
	bossActionContextCommand         = "context_command"
	bossActionSkillsInventory        = "skills_inventory"
	bossActionProposeControl         = "propose_control"

	bossToolResultLimit = 8000
)

type bossAction struct {
	Kind              string                `json:"kind"`
	Answer            string                `json:"answer"`
	Target            string                `json:"target"`
	Query             string                `json:"query"`
	Command           string                `json:"command"`
	ProjectPath       string                `json:"project_path"`
	ProjectName       string                `json:"project_name"`
	SessionID         string                `json:"session_id"`
	ControlCapability string                `json:"control_capability"`
	RequestID         string                `json:"request_id"`
	TaskID            string                `json:"task_id"`
	TaskTitle         string                `json:"task_title"`
	TaskKind          string                `json:"task_kind"`
	ParentTaskID      string                `json:"parent_task_id"`
	TaskCloseStatus   string                `json:"task_close_status"`
	TaskSummary       string                `json:"task_summary"`
	EngineerProvider  string                `json:"engineer_provider"`
	SessionMode       string                `json:"session_mode"`
	Prompt            string                `json:"prompt"`
	Reveal            bool                  `json:"reveal"`
	CloseSession      bool                  `json:"close_session"`
	Capabilities      []string              `json:"capabilities"`
	Resources         []control.ResourceRef `json:"resources"`
	IncludeHistorical bool                  `json:"include_historical"`
	Limit             int                   `json:"limit"`
	Reason            string                `json:"reason"`
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
	GetSessionContextExcerpt(ctx context.Context, req model.SessionContextExcerptRequest) (model.SessionContextExcerpt, error)
}

type bossAgentTaskReader interface {
	GetAgentTask(ctx context.Context, id string) (model.AgentTask, error)
	ListAgentTasks(ctx context.Context, filter model.AgentTaskFilter) ([]model.AgentTask, error)
}

type bossCandidateExcerptReader interface {
	GetSessionContextExcerptFromCandidate(ctx context.Context, req model.SessionContextExcerptCandidateRequest) (model.SessionContextExcerpt, error)
}

type QueryExecutor struct {
	store              bossStoreReader
	bossSessions       *bossSessionStore
	processReporter    processReportFunc
	nowFn              func() time.Time
	codexHome          string
	codexHomeFallbacks []string
	openCodeHome       string
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
	SystemNotices       []ViewSystemNotice
}

type ViewSystemNotice struct {
	Code     string
	Severity string
	Summary  string
	Count    int
}

func newQueryExecutor(store bossStoreReader) *QueryExecutor {
	return newQueryExecutorWithBossSessions(store, nil)
}

func newQueryExecutorWithBossSessions(store bossStoreReader, bossSessions *bossSessionStore) *QueryExecutor {
	if store == nil && bossSessions == nil {
		return nil
	}
	return &QueryExecutor{
		store:           store,
		bossSessions:    bossSessions,
		processReporter: defaultProcessReporter,
		nowFn:           time.Now,
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
	if kind == bossActionContextCommand {
		return e.contextCommand(ctx, action, view)
	}
	if kind == bossActionSkillsInventory {
		return e.skillsInventory(ctx, action)
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
	case bossActionProcessReport:
		return e.processReport(ctx, action, view)
	case bossActionSearchContext:
		return e.searchContext(ctx, action, view)
	default:
		return bossToolResult{}, fmt.Errorf("unknown boss action kind %q", strings.TrimSpace(action.Kind))
	}
}

func (e *QueryExecutor) skillsInventory(ctx context.Context, action bossAction) (bossToolResult, error) {
	inv, err := codexskills.LoadInventory(ctx, e.codexHome, e.now())
	if err != nil {
		return bossToolResult{}, err
	}
	return clippedToolResult(bossActionSkillsInventory, codexskills.FormatInventoryReport(inv, clampBossLimit(action.Limit, 20, 40))), nil
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
		lines = append(lines, "Live engineer session context:")
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
				lines = append(lines, "  "+clipText(engineerRoleLine(excerptLine), 360))
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
	if familyLines, err := e.worktreeFamilyActivity(ctx, detail.Summary, action.IncludeHistorical, view, now, 4); err == nil && len(familyLines) > 0 {
		lines = append(lines, "Worktree family activity:")
		lines = append(lines, familyLines...)
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

func (e *QueryExecutor) worktreeFamilyActivity(ctx context.Context, project model.ProjectSummary, includeHistorical bool, view ViewContext, now time.Time, limit int) ([]string, error) {
	if e == nil || e.store == nil {
		return nil, nil
	}
	rootPath := projectWorktreeRootPath(project)
	if rootPath == "" {
		return nil, nil
	}
	projects, err := e.store.ListProjects(ctx, includeHistorical)
	if err != nil {
		return nil, err
	}
	projects = filterProjectSummariesForBossPrivacy(projects, view)
	selectedPath := filepath.Clean(strings.TrimSpace(project.Path))
	family := make([]model.ProjectSummary, 0, len(projects))
	for _, candidate := range projects {
		if filepath.Clean(strings.TrimSpace(candidate.Path)) == selectedPath {
			continue
		}
		if projectWorktreeRootPath(candidate) == rootPath {
			family = append(family, candidate)
		}
	}
	if len(family) == 0 {
		return nil, nil
	}
	sort.SliceStable(family, func(i, j int) bool {
		left := family[i]
		right := family[j]
		switch {
		case left.LastActivity.IsZero() != right.LastActivity.IsZero():
			return !left.LastActivity.IsZero()
		case !left.LastActivity.Equal(right.LastActivity):
			return left.LastActivity.After(right.LastActivity)
		default:
			return displayProjectName(left) < displayProjectName(right)
		}
	})
	limit = clampBossLimit(limit, 3, 8)
	lines := make([]string, 0, minInt(limit, len(family))*2)
	for i, member := range family {
		if i >= limit {
			break
		}
		brief := projectBriefFromSummary(member)
		label := "worktree"
		switch member.WorktreeKind {
		case model.WorktreeKindMain:
			label = "root"
		case model.WorktreeKindLinked:
			label = "linked"
		}
		lines = append(lines, "- "+label+": "+operationalProjectLine(brief, now))
		if metadata := projectReferenceMetadata(brief, now); metadata != "" {
			lines = append(lines, "  reference metadata: "+metadata)
		}
	}
	return lines, nil
}

func projectWorktreeRootPath(project model.ProjectSummary) string {
	rootPath := strings.TrimSpace(project.WorktreeRootPath)
	if rootPath == "" {
		rootPath = strings.TrimSpace(project.Path)
	}
	if rootPath == "" {
		return ""
	}
	return filepath.Clean(rootPath)
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

type parsedContextCommand struct {
	Verb              string
	Domain            string
	Query             string
	Handle            string
	Project           string
	Limit             int
	BeforeTurns       int
	AfterTurns        int
	MaxChars          int
	IncludeHistorical bool
}

func (e *QueryExecutor) contextCommand(ctx context.Context, action bossAction, view ViewContext) (bossToolResult, error) {
	command := strings.TrimSpace(action.Command)
	if command == "" {
		command = strings.TrimSpace(action.Query)
	}
	parsed, err := parseContextCommand(command)
	if err != nil {
		return clippedToolResult(bossActionContextCommand, "Context command error: "+err.Error()+"\nAllowed forms:\n- ctx search engineer \"query\" --project <name-or-path> --limit 5\n- ctx show engineer:<session-id> --query \"query\" --before 1 --after 2 --max-chars 6000\n- ctx show agent_task:<task-id> --before 1 --after 4 --max-chars 6000\n- ctx recent engineer --project <name-or-path> --limit 5\n- ctx search boss \"query\" --limit 5"), nil
	}
	switch parsed.Verb {
	case "search":
		switch normalizeContextCommandDomain(parsed.Domain) {
		case "boss":
			return e.contextCommandSearchBoss(ctx, parsed)
		case "engineer":
			return e.contextCommandSearchEngineer(ctx, parsed, view)
		default:
			return clippedToolResult(bossActionContextCommand, "Context command error: search domain must be boss or engineer."), nil
		}
	case "show":
		return e.contextCommandShow(ctx, parsed, view)
	case "recent":
		if normalizeContextCommandDomain(parsed.Domain) != "engineer" {
			return clippedToolResult(bossActionContextCommand, "Context command error: recent currently supports only engineer sessions."), nil
		}
		return e.contextCommandRecentEngineer(ctx, parsed, view)
	default:
		return clippedToolResult(bossActionContextCommand, "Context command error: unknown verb "+parsed.Verb+"."), nil
	}
}

func (e *QueryExecutor) contextCommandSearchBoss(ctx context.Context, parsed parsedContextCommand) (bossToolResult, error) {
	if e == nil || e.bossSessions == nil {
		return clippedToolResult(bossActionContextCommand, "Boss Chat transcript search is not connected."), nil
	}
	query := strings.TrimSpace(parsed.Query)
	if query == "" {
		return clippedToolResult(bossActionContextCommand, "Context command error: ctx search boss needs a query."), nil
	}
	results, err := e.bossSessions.searchSessions(ctx, query, clampBossLimit(parsed.Limit, 5, 12))
	if err != nil {
		return bossToolResult{}, err
	}
	return clippedToolResult(bossActionContextCommand, formatBossSessionSearchXML(query, results, e.now())), nil
}

func (e *QueryExecutor) contextCommandSearchEngineer(ctx context.Context, parsed parsedContextCommand, view ViewContext) (bossToolResult, error) {
	if e == nil || e.store == nil {
		return clippedToolResult(bossActionContextCommand, "Engineer transcript search is not connected."), nil
	}
	query := strings.TrimSpace(parsed.Query)
	if query == "" {
		return clippedToolResult(bossActionContextCommand, "Context command error: ctx search engineer needs a query."), nil
	}
	projectPath, note, err := e.contextCommandProjectPath(ctx, parsed, view)
	if err != nil {
		return bossToolResult{}, err
	}
	results, err := e.store.SearchContext(ctx, model.ContextSearchRequest{
		Query:             query,
		ProjectPath:       projectPath,
		IncludeHistorical: parsed.IncludeHistorical,
		Limit:             clampBossLimit(parsed.Limit, 8, 24),
	})
	if err != nil {
		return bossToolResult{}, err
	}
	if view.PrivacyMode {
		privatePaths, err := e.privateProjectPaths(ctx, parsed.IncludeHistorical, view)
		if err != nil {
			return bossToolResult{}, err
		}
		results = filterContextSearchResultsForBossPrivacy(results, view, privatePaths)
	}
	sessionResults := make([]model.ContextSearchResult, 0, len(results))
	for _, result := range results {
		if strings.EqualFold(strings.TrimSpace(result.Source), "session") && strings.TrimSpace(result.SessionID) != "" {
			sessionResults = append(sessionResults, result)
		}
	}
	limit := clampBossLimit(parsed.Limit, 5, 12)
	if len(sessionResults) > limit {
		sessionResults = sessionResults[:limit]
	}

	now := e.now()
	lines := []string{
		fmt.Sprintf("ctx search engineer %q: %d matches.", query, len(sessionResults)),
		"Engineer means Codex, OpenCode, or Claude Code work-session transcripts. Boss Chat transcripts are separate.",
		"Use the handle with ctx show to fetch a bounded nearby exchange before quoting or correcting details.",
	}
	if projectPath != "" {
		lines[0] += " Project path: " + projectPath + "."
	}
	if note != "" {
		lines = append(lines, "Target note: "+note)
	}
	for i, result := range sessionResults {
		handle := contextEngineerHandle(result.SessionID)
		lines = append(lines, fmt.Sprintf("%d. handle: %s", i+1, handle))
		metadata := []string{}
		if project := strings.TrimSpace(firstNonEmpty(result.ProjectName, result.ProjectPath)); project != "" {
			metadata = append(metadata, "project="+project)
		}
		if path := strings.TrimSpace(result.ProjectPath); path != "" {
			metadata = append(metadata, "path="+path)
		}
		if !result.UpdatedAt.IsZero() {
			metadata = append(metadata, "updated_at="+formatBossTimestamp(result.UpdatedAt))
			metadata = append(metadata, "age_at_query="+ageAtTime(now, result.UpdatedAt))
		}
		if len(metadata) > 0 {
			lines = append(lines, "   reference metadata: "+strings.Join(metadata, "; "))
		}
		if snippet := strings.TrimSpace(result.Snippet); snippet != "" {
			lines = append(lines, "   snippet: "+clipText(snippet, 420))
		}
		lines = append(lines, "   show: "+formatContextShowCommand(handle, query))
	}
	if len(sessionResults) == 0 {
		lines = append(lines, "No engineer transcript matches found.")
	}
	return clippedToolResult(bossActionContextCommand, strings.Join(lines, "\n")), nil
}

func (e *QueryExecutor) contextCommandShow(ctx context.Context, parsed parsedContextCommand, view ViewContext) (bossToolResult, error) {
	if e == nil || e.store == nil {
		return clippedToolResult(bossActionContextCommand, "Engineer exchange lookup is not connected."), nil
	}
	handle := strings.TrimSpace(parsed.Handle)
	if taskID, ok := strings.CutPrefix(handle, "agent_task:"); ok {
		if strings.TrimSpace(taskID) == "" {
			return clippedToolResult(bossActionContextCommand, "Context command error: ctx show needs an agent_task:<task-id> handle."), nil
		}
		return e.contextCommandShowAgentTask(ctx, taskID, parsed, view)
	}
	sessionID, ok := strings.CutPrefix(handle, "engineer:")
	if !ok || strings.TrimSpace(sessionID) == "" {
		return clippedToolResult(bossActionContextCommand, "Context command error: ctx show needs an engineer:<session-id> or agent_task:<task-id> handle."), nil
	}
	excerpt, err := e.store.GetSessionContextExcerpt(ctx, model.SessionContextExcerptRequest{
		SessionID:   sessionID,
		Query:       parsed.Query,
		BeforeTurns: clampContextCommandCount(parsed.BeforeTurns, 1, 6),
		AfterTurns:  clampContextCommandCount(parsed.AfterTurns, 2, 6),
		MaxChars:    clampContextCommandCount(parsed.MaxChars, 6000, 12000),
	})
	if err != nil {
		var fallbackErr error
		excerpt, fallbackErr = e.contextCommandShowEngineerViaAgentTask(ctx, sessionID, parsed, view)
		if fallbackErr != nil {
			return bossToolResult{}, fallbackErr
		}
	}
	if e.excerptHiddenByPrivacy(ctx, excerpt, view) {
		return clippedToolResult(bossActionContextCommand, "Engineer exchange is hidden while privacy mode is enabled."), nil
	}
	return clippedToolResult(bossActionContextCommand, formatEngineerExcerpt(excerpt, e.now())), nil
}

func (e *QueryExecutor) contextCommandShowAgentTask(ctx context.Context, taskID string, parsed parsedContextCommand, view ViewContext) (bossToolResult, error) {
	excerpt, err := e.agentTaskContextExcerpt(ctx, strings.TrimSpace(taskID), parsed, view)
	if err != nil {
		return bossToolResult{}, err
	}
	return clippedToolResult(bossActionContextCommand, formatEngineerExcerpt(excerpt, e.now())), nil
}

func (e *QueryExecutor) contextCommandShowEngineerViaAgentTask(ctx context.Context, sessionID string, parsed parsedContextCommand, view ViewContext) (model.SessionContextExcerpt, error) {
	if view.PrivacyMode {
		return model.SessionContextExcerpt{}, errors.New("engineer exchange is hidden while privacy mode is enabled")
	}
	taskReader, ok := e.store.(bossAgentTaskReader)
	if !ok {
		return model.SessionContextExcerpt{}, errors.New("agent task lookup is not connected")
	}
	tasks, err := taskReader.ListAgentTasks(ctx, model.AgentTaskFilter{IncludeArchived: true, Limit: 100})
	if err != nil {
		return model.SessionContextExcerpt{}, err
	}
	for _, task := range tasks {
		provider, matchedSessionID, ok := agentTaskMatchingEngineerSession(task, sessionID)
		if !ok {
			continue
		}
		return e.agentTaskContextExcerptForSession(ctx, task, provider, matchedSessionID, parsed, view)
	}
	return model.SessionContextExcerpt{}, fmt.Errorf("engineer session not found: %s", strings.TrimSpace(sessionID))
}

func (e *QueryExecutor) agentTaskContextExcerpt(ctx context.Context, taskID string, parsed parsedContextCommand, view ViewContext) (model.SessionContextExcerpt, error) {
	if view.PrivacyMode {
		return model.SessionContextExcerpt{}, errors.New("agent task exchange is hidden while privacy mode is enabled")
	}
	taskReader, ok := e.store.(bossAgentTaskReader)
	if !ok {
		return model.SessionContextExcerpt{}, errors.New("agent task lookup is not connected")
	}
	task, err := taskReader.GetAgentTask(ctx, taskID)
	if err != nil {
		return model.SessionContextExcerpt{}, err
	}
	provider, sessionID, ok := primaryAgentTaskEngineerSession(task)
	if !ok {
		return model.SessionContextExcerpt{}, fmt.Errorf("agent task %s has no tracked engineer session yet", task.ID)
	}
	return e.agentTaskContextExcerptForSession(ctx, task, provider, sessionID, parsed, view)
}

func (e *QueryExecutor) agentTaskContextExcerptForSession(ctx context.Context, task model.AgentTask, provider model.SessionSource, sessionID string, parsed parsedContextCommand, view ViewContext) (model.SessionContextExcerpt, error) {
	if view.PrivacyMode {
		return model.SessionContextExcerpt{}, errors.New("agent task exchange is hidden while privacy mode is enabled")
	}
	excerptReader, ok := e.store.(bossCandidateExcerptReader)
	if !ok {
		return model.SessionContextExcerpt{}, errors.New("agent task transcript lookup is not connected")
	}
	source, canonicalSessionID, rawSessionID := model.NormalizeSessionIdentity(provider, agentTaskSessionFormat(provider), sessionID, "")
	sessionFile := e.agentTaskSessionFile(source, rawSessionID, task)
	if strings.TrimSpace(sessionFile) == "" {
		return model.SessionContextExcerpt{}, fmt.Errorf("agent task %s has %s session %s, but no transcript artifact was found", task.ID, source, rawSessionID)
	}
	return excerptReader.GetSessionContextExcerptFromCandidate(ctx, model.SessionContextExcerptCandidateRequest{
		Source:        source,
		SessionID:     canonicalSessionID,
		RawSessionID:  rawSessionID,
		ProjectPath:   task.WorkspacePath,
		ProjectName:   task.Title,
		SessionFile:   sessionFile,
		SessionFormat: agentTaskSessionFormat(source),
		Query:         parsed.Query,
		BeforeTurns:   clampContextCommandCount(parsed.BeforeTurns, 1, 6),
		AfterTurns:    clampContextCommandCount(parsed.AfterTurns, 2, 6),
		MaxChars:      clampContextCommandCount(parsed.MaxChars, 6000, 12000),
	})
}

func (e *QueryExecutor) contextCommandRecentEngineer(ctx context.Context, parsed parsedContextCommand, view ViewContext) (bossToolResult, error) {
	if e == nil || e.store == nil {
		return clippedToolResult(bossActionContextCommand, "Recent engineer session lookup is not connected."), nil
	}
	projectPath, note, err := e.contextCommandProjectPath(ctx, parsed, view)
	if err != nil {
		return bossToolResult{}, err
	}
	if projectPath == "" {
		return clippedToolResult(bossActionContextCommand, "Context command error: ctx recent engineer needs --project <name-or-path>."), nil
	}
	samples, err := e.store.SampleProjectSessionContext(ctx, projectPath, clampBossLimit(parsed.Limit, 3, 6))
	if err != nil {
		return bossToolResult{}, err
	}
	now := e.now()
	lines := []string{
		fmt.Sprintf("ctx recent engineer: %d recent sessions for %s.", len(samples), projectPath),
		"Engineer means Codex, OpenCode, or Claude Code work-session transcripts.",
	}
	if note != "" {
		lines = append(lines, "Target note: "+note)
	}
	for i, sample := range samples {
		handle := contextEngineerHandle(sample.SessionID)
		line := fmt.Sprintf("%d. handle: %s", i+1, handle)
		line += fmt.Sprintf(" | source=%s | updated=%s", sample.Source, ageAtTime(now, sample.UpdatedAt))
		if sample.LatestTurnStateKnown && !sample.LatestTurnCompleted {
			line += " | latest turn open"
		}
		lines = append(lines, line)
		for _, excerptLine := range liveSessionSampleLines(sample.Text) {
			lines = append(lines, "   "+clipText(engineerRoleLine(excerptLine), 360))
		}
		lines = append(lines, "   show: "+formatContextShowCommand(handle, ""))
	}
	if len(samples) == 0 {
		lines = append(lines, "No recent live engineer session samples found for that project.")
	}
	return clippedToolResult(bossActionContextCommand, strings.Join(lines, "\n")), nil
}

func (e *QueryExecutor) contextCommandProjectPath(ctx context.Context, parsed parsedContextCommand, view ViewContext) (string, string, error) {
	project := strings.TrimSpace(parsed.Project)
	if project == "" {
		return "", "", nil
	}
	action := bossAction{}
	if strings.HasPrefix(project, "/") || strings.HasPrefix(project, ".") {
		action.ProjectPath = project
	} else {
		action.ProjectName = project
	}
	return e.resolveProjectPath(ctx, action, view)
}

func primaryAgentTaskEngineerSession(task model.AgentTask) (model.SessionSource, string, bool) {
	provider := model.NormalizeSessionSource(task.Provider)
	sessionID := strings.TrimSpace(task.SessionID)
	if provider != model.SessionSourceUnknown && sessionID != "" {
		return provider, sessionID, true
	}
	for _, resource := range task.Resources {
		if model.NormalizeAgentTaskResourceKind(resource.Kind) != model.AgentTaskResourceEngineerSession {
			continue
		}
		provider := model.NormalizeSessionSource(resource.Provider)
		sessionID := strings.TrimSpace(resource.SessionID)
		if sessionID == "" {
			sessionID = strings.TrimSpace(resource.RefID)
		}
		if provider != model.SessionSourceUnknown && sessionID != "" {
			return provider, sessionID, true
		}
	}
	return model.SessionSourceUnknown, "", false
}

func agentTaskMatchingEngineerSession(task model.AgentTask, wantedSessionID string) (model.SessionSource, string, bool) {
	wantedSource, _, wantedRaw := model.NormalizeSessionIdentity(model.SessionSourceUnknown, "", wantedSessionID, "")
	matches := func(provider model.SessionSource, sessionID string) bool {
		source, canonical, raw := model.NormalizeSessionIdentity(provider, agentTaskSessionFormat(provider), sessionID, "")
		if canonical == "" && raw == "" {
			return false
		}
		if wantedSource != model.SessionSourceUnknown && source != wantedSource {
			return false
		}
		return strings.TrimSpace(wantedSessionID) == canonical || wantedRaw == raw || strings.TrimSpace(wantedSessionID) == raw
	}
	if provider := model.NormalizeSessionSource(task.Provider); provider != model.SessionSourceUnknown && strings.TrimSpace(task.SessionID) != "" {
		if matches(provider, task.SessionID) {
			return provider, strings.TrimSpace(task.SessionID), true
		}
	}
	for _, resource := range task.Resources {
		if model.NormalizeAgentTaskResourceKind(resource.Kind) != model.AgentTaskResourceEngineerSession {
			continue
		}
		provider := model.NormalizeSessionSource(resource.Provider)
		sessionID := strings.TrimSpace(resource.SessionID)
		if sessionID == "" {
			sessionID = strings.TrimSpace(resource.RefID)
		}
		if provider != model.SessionSourceUnknown && sessionID != "" && matches(provider, sessionID) {
			return provider, sessionID, true
		}
	}
	return model.SessionSourceUnknown, "", false
}

func (e *QueryExecutor) agentTaskSessionFile(source model.SessionSource, rawSessionID string, task model.AgentTask) string {
	source = model.NormalizeSessionSource(source)
	rawSessionID = strings.TrimSpace(rawSessionID)
	if rawSessionID == "" {
		return ""
	}
	switch source {
	case model.SessionSourceOpenCode:
		if home := strings.TrimSpace(e.openCodeHome); home != "" {
			return filepath.Join(home, "opencode.db") + "#session:" + rawSessionID
		}
	case model.SessionSourceCodex:
		for _, codexHome := range e.bossCodexHomes() {
			if sessionFile := resolveBossCodexSessionFile(codexHome, rawSessionID, task.CreatedAt, task.LastTouchedAt, task.UpdatedAt); sessionFile != "" {
				return sessionFile
			}
		}
	}
	return ""
}

func (e *QueryExecutor) bossCodexHomes() []string {
	if e == nil {
		return nil
	}
	homes := make([]string, 0, 1+len(e.codexHomeFallbacks))
	seen := map[string]struct{}{}
	add := func(home string) {
		home = normalizeBossCodexHome(home)
		if home == "" {
			return
		}
		if _, ok := seen[home]; ok {
			return
		}
		seen[home] = struct{}{}
		homes = append(homes, home)
	}
	add(e.codexHome)
	for _, fallback := range e.codexHomeFallbacks {
		add(fallback)
	}
	return homes
}

func bossDefaultCodexHomeFallbacks(primary string) []string {
	defaultHome := normalizeBossCodexHome(config.Default().CodexHome)
	if defaultHome == "" || defaultHome == normalizeBossCodexHome(primary) {
		return nil
	}
	return []string{defaultHome}
}

func normalizeBossCodexHome(home string) string {
	home = strings.TrimSpace(home)
	if home == "" {
		return ""
	}
	home = strings.TrimSpace(codexstate.ResolveHomeRoot(home))
	if home == "" || home == "." {
		return ""
	}
	return filepath.Clean(home)
}

func agentTaskSessionFormat(source model.SessionSource) string {
	switch model.NormalizeSessionSource(source) {
	case model.SessionSourceOpenCode:
		return "opencode_db"
	case model.SessionSourceClaudeCode:
		return "claude_code"
	default:
		return "modern"
	}
}

func resolveBossCodexSessionFile(codexHome, sessionID string, times ...time.Time) string {
	codexHome = codexstate.ResolveHomeRoot(codexHome)
	sessionID = strings.TrimSpace(sessionID)
	if codexHome == "" || sessionID == "" {
		return ""
	}
	for _, root := range []string{"sessions", "archived_sessions"} {
		for _, day := range bossCodexSessionDateCandidates(times...) {
			pattern := filepath.Join(
				codexHome,
				root,
				day.Format("2006"),
				day.Format("01"),
				day.Format("02"),
				"*"+sessionID+"*.jsonl",
			)
			matches, err := filepath.Glob(pattern)
			if err != nil || len(matches) == 0 {
				continue
			}
			sort.Strings(matches)
			return matches[len(matches)-1]
		}
	}
	return ""
}

func bossCodexSessionDateCandidates(times ...time.Time) []time.Time {
	seen := map[string]struct{}{}
	out := make([]time.Time, 0, len(times)*3+1)
	add := func(t time.Time) {
		if t.IsZero() {
			return
		}
		day := time.Date(t.UTC().Year(), t.UTC().Month(), t.UTC().Day(), 0, 0, 0, 0, time.UTC)
		key := day.Format("2006-01-02")
		if _, ok := seen[key]; ok {
			return
		}
		seen[key] = struct{}{}
		out = append(out, day)
	}
	for _, t := range times {
		add(t)
	}
	now := time.Now().UTC()
	add(now)
	add(now.Add(-24 * time.Hour))
	add(now.Add(24 * time.Hour))
	return out
}

func (e *QueryExecutor) excerptHiddenByPrivacy(ctx context.Context, excerpt model.SessionContextExcerpt, view ViewContext) bool {
	if !view.PrivacyMode {
		return false
	}
	if config.MatchesPrivacyPattern(excerpt.ProjectName, view.PrivacyPatterns) {
		return true
	}
	privatePaths, err := e.privateProjectPaths(ctx, true, view)
	if err != nil {
		return true
	}
	_, private := privatePaths[filepath.Clean(strings.TrimSpace(excerpt.ProjectPath))]
	return private
}

func parseContextCommand(command string) (parsedContextCommand, error) {
	tokens, err := contextCommandTokens(command)
	if err != nil {
		return parsedContextCommand{}, err
	}
	if len(tokens) < 2 || strings.ToLower(tokens[0]) != "ctx" {
		return parsedContextCommand{}, errors.New(`command must start with "ctx"`)
	}
	parsed := parsedContextCommand{Verb: strings.ToLower(tokens[1])}
	switch parsed.Verb {
	case "search":
		if len(tokens) < 4 {
			return parsedContextCommand{}, errors.New("ctx search needs a domain and query")
		}
		parsed.Domain = normalizeContextCommandDomain(tokens[2])
		parsed.Query = tokens[3]
		if err := parseContextCommandFlags(tokens[4:], &parsed); err != nil {
			return parsedContextCommand{}, err
		}
	case "show":
		if len(tokens) < 3 {
			return parsedContextCommand{}, errors.New("ctx show needs a handle")
		}
		parsed.Handle = tokens[2]
		if err := parseContextCommandFlags(tokens[3:], &parsed); err != nil {
			return parsedContextCommand{}, err
		}
	case "recent":
		if len(tokens) < 3 {
			return parsedContextCommand{}, errors.New("ctx recent needs a domain")
		}
		parsed.Domain = normalizeContextCommandDomain(tokens[2])
		if err := parseContextCommandFlags(tokens[3:], &parsed); err != nil {
			return parsedContextCommand{}, err
		}
	default:
		return parsedContextCommand{}, fmt.Errorf("unknown ctx verb %q", parsed.Verb)
	}
	return parsed, nil
}

func parseContextCommandFlags(tokens []string, parsed *parsedContextCommand) error {
	for i := 0; i < len(tokens); i++ {
		token := strings.TrimSpace(tokens[i])
		switch token {
		case "--project":
			value, next, err := contextCommandFlagValue(tokens, i)
			if err != nil {
				return err
			}
			parsed.Project = value
			i = next
		case "--query":
			value, next, err := contextCommandFlagValue(tokens, i)
			if err != nil {
				return err
			}
			parsed.Query = value
			i = next
		case "--limit":
			value, next, err := contextCommandIntFlag(tokens, i)
			if err != nil {
				return err
			}
			parsed.Limit = value
			i = next
		case "--before":
			value, next, err := contextCommandIntFlag(tokens, i)
			if err != nil {
				return err
			}
			parsed.BeforeTurns = value
			i = next
		case "--after":
			value, next, err := contextCommandIntFlag(tokens, i)
			if err != nil {
				return err
			}
			parsed.AfterTurns = value
			i = next
		case "--max-chars":
			value, next, err := contextCommandIntFlag(tokens, i)
			if err != nil {
				return err
			}
			parsed.MaxChars = value
			i = next
		case "--historical", "--include-historical":
			parsed.IncludeHistorical = true
		default:
			return fmt.Errorf("unknown ctx flag %q", token)
		}
	}
	return nil
}

func contextCommandFlagValue(tokens []string, i int) (string, int, error) {
	if i+1 >= len(tokens) || strings.HasPrefix(tokens[i+1], "--") {
		return "", i, fmt.Errorf("ctx flag %s needs a value", tokens[i])
	}
	return strings.TrimSpace(tokens[i+1]), i + 1, nil
}

func contextCommandIntFlag(tokens []string, i int) (int, int, error) {
	value, next, err := contextCommandFlagValue(tokens, i)
	if err != nil {
		return 0, i, err
	}
	parsed, err := strconv.Atoi(value)
	if err != nil {
		return 0, i, fmt.Errorf("ctx flag %s needs an integer value", tokens[i])
	}
	return parsed, next, nil
}

func contextCommandTokens(command string) ([]string, error) {
	command = strings.TrimSpace(command)
	if command == "" {
		return nil, errors.New("empty context command")
	}
	tokens := []string{}
	var b strings.Builder
	var quote rune
	escaped := false
	for _, r := range command {
		switch {
		case escaped:
			b.WriteRune(r)
			escaped = false
		case r == '\\' && quote != 0:
			escaped = true
		case quote != 0:
			if r == quote {
				quote = 0
			} else {
				b.WriteRune(r)
			}
		case r == '"' || r == '\'':
			quote = r
		case r == ' ' || r == '\t' || r == '\n':
			if b.Len() > 0 {
				tokens = append(tokens, b.String())
				b.Reset()
			}
		default:
			b.WriteRune(r)
		}
	}
	if escaped {
		b.WriteRune('\\')
	}
	if quote != 0 {
		return nil, errors.New("unterminated quote in context command")
	}
	if b.Len() > 0 {
		tokens = append(tokens, b.String())
	}
	return tokens, nil
}

func normalizeContextCommandDomain(domain string) string {
	switch strings.ToLower(strings.TrimSpace(domain)) {
	case "engineer", "engineers", "work", "agent", "agents", "assistant", "assistants", "ai":
		return "engineer"
	case "boss", "boss-chat", "boss_chat", "chat", "desk":
		return "boss"
	default:
		return strings.ToLower(strings.TrimSpace(domain))
	}
}

func contextEngineerHandle(sessionID string) string {
	return "engineer:" + strings.TrimSpace(sessionID)
}

func formatContextShowCommand(handle, query string) string {
	command := "ctx show " + shellQuoteContextToken(handle)
	if strings.TrimSpace(query) != "" {
		command += " --query " + shellQuoteContextToken(query)
	}
	return command + " --before 1 --after 2 --max-chars 6000"
}

func shellQuoteContextToken(value string) string {
	value = strings.TrimSpace(value)
	if value == "" {
		return `""`
	}
	if !strings.ContainsAny(value, " \t\n\"'") {
		return value
	}
	return `"` + strings.ReplaceAll(value, `"`, `\"`) + `"`
}

func formatEngineerExcerpt(excerpt model.SessionContextExcerpt, now time.Time) string {
	lines := []string{
		"Engineer exchange:",
		fmt.Sprintf("- handle: %s", contextEngineerHandle(excerpt.SessionID)),
		fmt.Sprintf("- source: %s", excerpt.Source),
	}
	if project := strings.TrimSpace(firstNonEmpty(excerpt.ProjectName, excerpt.ProjectPath)); project != "" {
		lines = append(lines, "- project: "+project)
	}
	if path := strings.TrimSpace(excerpt.ProjectPath); path != "" {
		lines = append(lines, "- path: "+path)
	}
	if !excerpt.UpdatedAt.IsZero() {
		lines = append(lines, "- updated: "+ageAtTime(now, excerpt.UpdatedAt))
	}
	if query := strings.TrimSpace(excerpt.Query); query != "" {
		if excerpt.AnchorMatched {
			lines = append(lines, fmt.Sprintf("- anchor: turn %d matched %q", excerpt.AnchorIndex, query))
		} else {
			lines = append(lines, fmt.Sprintf("- anchor: latest turn; query %q was not found inside fetched transcript text", query))
		}
	}
	lines = append(lines, "Transcript excerpt:")
	for _, turn := range excerpt.Turns {
		role := engineerTranscriptRole(turn.Role)
		lines = append(lines, fmt.Sprintf("<turn index=\"%d\" role=\"%s\"><![CDATA[%s]]></turn>", turn.Index, role, cdataSafe(turn.Text)))
	}
	if excerpt.Truncated {
		lines = append(lines, "<note>Excerpt truncated by max-chars.</note>")
	}
	return strings.Join(lines, "\n")
}

func engineerTranscriptRole(role string) string {
	switch strings.ToLower(strings.TrimSpace(role)) {
	case "assistant":
		return "engineer"
	case "user":
		return "boss"
	default:
		return strings.ToLower(strings.TrimSpace(role))
	}
}

func engineerRoleLine(line string) string {
	line = strings.TrimSpace(line)
	if strings.HasPrefix(line, "assistant:") {
		return "engineer:" + strings.TrimPrefix(line, "assistant:")
	}
	if strings.HasPrefix(line, "user:") {
		return "boss:" + strings.TrimPrefix(line, "user:")
	}
	return line
}

func cdataSafe(text string) string {
	return strings.ReplaceAll(text, "]]>", "]]]]><![CDATA[>")
}

func clampContextCommandCount(value, defaultValue, maxValue int) int {
	if value <= 0 {
		value = defaultValue
	}
	if value > maxValue {
		value = maxValue
	}
	return value
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
	noticeLines := []string{}
	if len(view.SystemNotices) > 0 {
		limit := minInt(len(view.SystemNotices), 5)
		for i := 0; i < limit; i++ {
			notice := view.SystemNotices[i]
			summary := strings.TrimSpace(notice.Summary)
			if summary == "" {
				continue
			}
			severity := strings.TrimSpace(notice.Severity)
			if severity == "" {
				severity = "notice"
			}
			code := strings.TrimSpace(notice.Code)
			if code == "" {
				code = "system"
			}
			count := ""
			if notice.Count > 0 {
				count = fmt.Sprintf(" count=%d", notice.Count)
			}
			noticeLines = append(noticeLines, fmt.Sprintf("  - %s/%s%s: %s", severity, code, count, clipText(summary, 220)))
		}
		if len(view.SystemNotices) > limit {
			noticeLines = append(noticeLines, fmt.Sprintf("  - ... %d more", len(view.SystemNotices)-limit))
		}
	}
	if len(noticeLines) > 0 {
		lines = append(lines, "- system notices:")
		lines = append(lines, noticeLines...)
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
