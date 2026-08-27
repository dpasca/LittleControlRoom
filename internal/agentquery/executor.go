package agentquery

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"time"

	"lcroom/internal/bossrun"
	"lcroom/internal/control"
	"lcroom/internal/demorecord"
	"lcroom/internal/model"
)

const (
	defaultPageLimit    = 20
	maximumPageLimit    = 50
	defaultTodoLimit    = 20
	defaultSessionLimit = 10
)

type Reader interface {
	ListProjects(context.Context, bool) ([]model.ProjectSummary, error)
	GetProjectSummary(context.Context, string, bool) (model.ProjectSummary, error)
	GetProjectDetail(context.Context, string, int) (model.ProjectDetail, error)
	ListSessionClassifications(context.Context, string, string) ([]model.SessionClassification, error)
	ListAgentTasks(context.Context, model.AgentTaskFilter) ([]model.AgentTask, error)
	GetAgentTask(context.Context, string) (model.AgentTask, error)
	ListGoalRuns(context.Context, int) ([]bossrun.GoalRecord, error)
	GetGoalRun(context.Context, string) (bossrun.GoalRecord, error)
}

type DemoRecordingReader interface {
	Latest(context.Context) (demorecord.Resource, bool, error)
}

type DisclosurePolicy string

const (
	// DisclosureOriginProject is the default embedded-agent policy: private
	// projects are hidden except for the caller's own originating project.
	DisclosureOriginProject DisclosurePolicy = "origin_project"
	// DisclosureHidePrivate is for a host-level surface with privacy mode on:
	// every private-category project is hidden and there is no origin exception.
	DisclosureHidePrivate DisclosurePolicy = "hide_private"
	// DisclosureHost is for an explicitly trusted host surface with privacy mode
	// off. It may inspect private-category projects under host UI policy.
	DisclosureHost DisclosurePolicy = "host"
)

type Executor struct {
	reader            Reader
	originProjectPath string
	scope             Scope
	disclosure        DisclosurePolicy
	demoRecordings    DemoRecordingReader
	recordingGrants   []DemoRecordingPathGrant
	nowFn             func() time.Time
}

type Options struct {
	Reader            Reader
	OriginProjectPath string
	Scope             Scope
	Disclosure        DisclosurePolicy
	DemoRecordings    DemoRecordingReader
	RecordingGrants   []DemoRecordingPathGrant
	Now               func() time.Time
}

func NewExecutor(options Options) (*Executor, error) {
	if options.Reader == nil {
		return nil, errors.New("LCR query reader is required")
	}
	originProjectPath := cleanPath(options.OriginProjectPath)
	disclosure := options.Disclosure
	if disclosure == "" {
		disclosure = DisclosureOriginProject
	}
	switch disclosure {
	case DisclosureOriginProject:
		// Embedded sessions need a trusted origin for the one private-project
		// exception in their disclosure contract.
	case DisclosureHidePrivate, DisclosureHost:
		// Host surfaces intentionally have no originating project.
	default:
		return nil, fmt.Errorf("unsupported LCR query disclosure policy %q", disclosure)
	}
	if disclosure == DisclosureOriginProject && originProjectPath == "" {
		return nil, errors.New("origin project path is required")
	}
	scope := NormalizeScope(string(options.Scope))
	if scope == "" {
		scope = ScopeProject
	}
	nowFn := options.Now
	if nowFn == nil {
		nowFn = time.Now
	}
	recordingGrants, err := normalizeDemoRecordingPathGrants(options.RecordingGrants)
	if err != nil {
		return nil, err
	}
	return &Executor{
		reader:            options.Reader,
		originProjectPath: originProjectPath,
		scope:             scope,
		disclosure:        disclosure,
		demoRecordings:    options.DemoRecordings,
		recordingGrants:   recordingGrants,
		nowFn:             nowFn,
	}, nil
}

func (e *Executor) Scope() Scope {
	if e == nil {
		return ""
	}
	return e.scope
}

func (e *Executor) Execute(ctx context.Context, name Name, arguments json.RawMessage) (map[string]any, error) {
	capability, ok := CapabilityByName(name)
	if !ok {
		return nil, fmt.Errorf("unknown LCR query %q", strings.TrimSpace(string(name)))
	}
	if !ScopeAllows(e.scope, capability.Scope) {
		return nil, fmt.Errorf("LCR query %q requires %s scope; this embedded session has %s scope", capability.Name, capability.Scope, e.scope)
	}
	if len(bytes.TrimSpace(arguments)) == 0 {
		arguments = json.RawMessage(`{}`)
	}

	var result map[string]any
	var err error
	switch capability.Name {
	case QueryPortfolioOverview:
		result, err = e.portfolioOverview(ctx, arguments)
	case QueryProjectList:
		result, err = e.projectList(ctx, arguments)
	case QueryProjectSearch:
		result, err = e.projectSearch(ctx, arguments)
	case QueryProjectDetail:
		result, err = e.projectDetail(ctx, arguments)
	case QueryTodoList:
		result, err = e.todoList(ctx, arguments)
	case QuerySessionList:
		result, err = e.sessionList(ctx, arguments)
	case QueryAssessmentList:
		result, err = e.assessmentList(ctx, arguments)
	case QueryAgentTaskList:
		result, err = e.agentTaskList(ctx, arguments)
	case QueryAgentTaskGet:
		result, err = e.agentTaskGet(ctx, arguments)
	case QueryGoalRunList:
		result, err = e.goalRunList(ctx, arguments)
	case QueryGoalRunGet:
		result, err = e.goalRunGet(ctx, arguments)
	case QueryDemoRecordingLatest:
		result, err = e.demoRecordingLatest(ctx, arguments)
	default:
		err = fmt.Errorf("LCR query %q has no executor", capability.Name)
	}
	if err != nil {
		return nil, err
	}

	result["success"] = true
	result["query"] = capability.Name
	result["as_of"] = formatTime(e.nowFn())
	result["freshness"] = capability.Freshness
	result["scope"] = e.scope
	if e.originProjectPath != "" {
		result["origin_project_path"] = e.originProjectPath
	}
	result["privacy_filter"] = e.PrivacyContract()
	if _, ok := result["truncated"]; !ok {
		result["truncated"] = false
	}
	return result, nil
}

func (e *Executor) PrivacyContract() string {
	if e == nil {
		return "unavailable"
	}
	switch e.disclosure {
	case DisclosureHost:
		return "host_visibility_private_categories_included"
	case DisclosureHidePrivate:
		return "private_categories_hidden"
	default:
		return "private_categories_hidden_except_origin_project"
	}
}

type portfolioOverviewArgs struct {
	IncludeHistorical bool `json:"include_historical"`
	AttentionLimit    int  `json:"attention_limit"`
}

func (e *Executor) portfolioOverview(ctx context.Context, raw json.RawMessage) (map[string]any, error) {
	var args portfolioOverviewArgs
	if err := decodeStrict(raw, &args); err != nil {
		return nil, err
	}
	limit, err := boundedLimit(args.AttentionLimit, 10, 20)
	if err != nil {
		return nil, fmt.Errorf("attention_limit: %w", err)
	}
	projects, err := e.visibleProjects(ctx, args.IncludeHistorical)
	if err != nil {
		return nil, err
	}

	statuses := map[string]int{}
	kinds := map[string]int{}
	categories := map[string]int{}
	archived := 0
	outOfScope := 0
	dirty := 0
	conflicted := 0
	openTodos := 0
	attention := make([]map[string]any, 0, min(limit, len(projects)))
	for _, project := range projects {
		statuses[string(project.Status)]++
		kinds[string(model.NormalizeProjectKind(project.Kind))]++
		category := strings.TrimSpace(project.CategoryName)
		if category == "" {
			category = "uncategorized"
		}
		categories[category]++
		if project.Archived {
			archived++
		}
		if !project.InScope {
			outOfScope++
		}
		if project.RepoDirty {
			dirty++
		}
		if project.RepoConflict {
			conflicted++
		}
		openTodos += project.OpenTODOCount
		if len(attention) < limit && project.AttentionScore > 0 {
			attention = append(attention, projectSummaryRecord(project))
		}
	}
	return map[string]any{
		"include_historical":  args.IncludeHistorical,
		"project_count":       len(projects),
		"archived_count":      archived,
		"out_of_scope_count":  outOfScope,
		"repo_dirty_count":    dirty,
		"repo_conflict_count": conflicted,
		"open_todo_count":     openTodos,
		"status_counts":       statuses,
		"kind_counts":         kinds,
		"category_counts":     categories,
		"attention_projects":  attention,
	}, nil
}

type projectListArgs struct {
	IncludeHistorical bool   `json:"include_historical"`
	Limit             int    `json:"limit"`
	Cursor            string `json:"cursor"`
}

func (e *Executor) projectList(ctx context.Context, raw json.RawMessage) (map[string]any, error) {
	var args projectListArgs
	if err := decodeStrict(raw, &args); err != nil {
		return nil, err
	}
	projects, err := e.visibleProjects(ctx, args.IncludeHistorical)
	if err != nil {
		return nil, err
	}
	records := make([]map[string]any, 0, len(projects))
	for _, project := range projects {
		records = append(records, projectSummaryRecord(project))
	}
	return pageResult("projects", records, args.Cursor, args.Limit)
}

type projectSearchArgs struct {
	Query             string `json:"query"`
	IncludeHistorical bool   `json:"include_historical"`
	Limit             int    `json:"limit"`
	Cursor            string `json:"cursor"`
}

func (e *Executor) projectSearch(ctx context.Context, raw json.RawMessage) (map[string]any, error) {
	var args projectSearchArgs
	if err := decodeStrict(raw, &args); err != nil {
		return nil, err
	}
	query := strings.ToLower(strings.TrimSpace(args.Query))
	if query == "" {
		return nil, errors.New("query is required")
	}
	projects, err := e.visibleProjects(ctx, args.IncludeHistorical)
	if err != nil {
		return nil, err
	}
	records := make([]map[string]any, 0)
	for _, project := range projects {
		haystack := strings.ToLower(strings.Join([]string{
			project.Name,
			project.Path,
			project.CategoryName,
			project.LatestSessionSummary,
			project.LatestCompletedSessionSummary,
		}, "\n"))
		if strings.Contains(haystack, query) {
			records = append(records, projectSummaryRecord(project))
		}
	}
	result, err := pageResult("projects", records, args.Cursor, args.Limit)
	if err != nil {
		return nil, err
	}
	result["search_query"] = strings.TrimSpace(args.Query)
	return result, nil
}

type projectDetailArgs struct {
	ProjectPath      string `json:"project_path"`
	IncludeCompleted bool   `json:"include_completed"`
	TodoLimit        int    `json:"todo_limit"`
	SessionLimit     int    `json:"session_limit"`
}

func (e *Executor) projectDetail(ctx context.Context, raw json.RawMessage) (map[string]any, error) {
	var args projectDetailArgs
	if err := decodeStrict(raw, &args); err != nil {
		return nil, err
	}
	todoLimit, err := boundedLimit(args.TodoLimit, defaultTodoLimit, maximumPageLimit)
	if err != nil {
		return nil, fmt.Errorf("todo_limit: %w", err)
	}
	sessionLimit, err := boundedLimit(args.SessionLimit, defaultSessionLimit, 30)
	if err != nil {
		return nil, fmt.Errorf("session_limit: %w", err)
	}
	path, _, err := e.resolveProject(ctx, args.ProjectPath)
	if err != nil {
		return nil, err
	}
	detail, err := e.reader.GetProjectDetail(ctx, path, 1)
	if err != nil {
		return nil, err
	}
	if !e.projectVisible(detail.Summary) {
		return nil, errors.New("project became hidden by the private-category disclosure policy")
	}

	todos := todoRecords(detail.Todos, args.IncludeCompleted)
	todos, todosTruncated := take(todos, todoLimit)
	sessions := sessionRecords(detail.Sessions)
	sessions, sessionsTruncated := take(sessions, sessionLimit)
	reasons := make([]map[string]any, 0, len(detail.Reasons))
	for _, reason := range detail.Reasons {
		reasons = append(reasons, map[string]any{
			"code":   reason.Code,
			"text":   clip(reason.Text, 600),
			"weight": reason.Weight,
		})
	}
	result := map[string]any{
		"project":            projectSummaryRecord(detail.Summary),
		"run_command":        clip(detail.Summary.RunCommand, 1000),
		"attention_reasons":  reasons,
		"todos":              todos,
		"todos_truncated":    todosTruncated,
		"sessions":           sessions,
		"sessions_truncated": sessionsTruncated,
		"truncated":          todosTruncated || sessionsTruncated,
	}
	if detail.LatestSessionClassification != nil {
		result["latest_assessment"] = assessmentRecord(*detail.LatestSessionClassification)
	}
	return result, nil
}

type projectPageArgs struct {
	ProjectPath      string `json:"project_path"`
	IncludeCompleted bool   `json:"include_completed"`
	Limit            int    `json:"limit"`
	Cursor           string `json:"cursor"`
}

type projectSessionPageArgs struct {
	ProjectPath string `json:"project_path"`
	Limit       int    `json:"limit"`
	Cursor      string `json:"cursor"`
}

func (e *Executor) todoList(ctx context.Context, raw json.RawMessage) (map[string]any, error) {
	var args projectPageArgs
	if err := decodeStrict(raw, &args); err != nil {
		return nil, err
	}
	path, project, err := e.resolveProject(ctx, args.ProjectPath)
	if err != nil {
		return nil, err
	}
	detail, err := e.reader.GetProjectDetail(ctx, path, 1)
	if err != nil {
		return nil, err
	}
	if !e.projectVisible(detail.Summary) {
		return nil, errors.New("project became hidden by the private-category disclosure policy")
	}
	result, err := pageResult("todos", todoRecords(detail.Todos, args.IncludeCompleted), args.Cursor, args.Limit)
	if err != nil {
		return nil, err
	}
	result["project"] = projectReference(project)
	result["include_completed"] = args.IncludeCompleted
	return result, nil
}

func (e *Executor) sessionList(ctx context.Context, raw json.RawMessage) (map[string]any, error) {
	var args projectSessionPageArgs
	if err := decodeStrict(raw, &args); err != nil {
		return nil, err
	}
	path, project, err := e.resolveProject(ctx, args.ProjectPath)
	if err != nil {
		return nil, err
	}
	detail, err := e.reader.GetProjectDetail(ctx, path, 1)
	if err != nil {
		return nil, err
	}
	if !e.projectVisible(detail.Summary) {
		return nil, errors.New("project became hidden by the private-category disclosure policy")
	}
	result, err := pageResult("sessions", sessionRecords(detail.Sessions), args.Cursor, args.Limit)
	if err != nil {
		return nil, err
	}
	result["project"] = projectReference(project)
	return result, nil
}

type assessmentListArgs struct {
	ProjectPath       string `json:"project_path"`
	SessionID         string `json:"session_id"`
	IncludeHistorical bool   `json:"include_historical"`
	Limit             int    `json:"limit"`
	Cursor            string `json:"cursor"`
}

func (e *Executor) assessmentList(ctx context.Context, raw json.RawMessage) (map[string]any, error) {
	var args assessmentListArgs
	if err := decodeStrict(raw, &args); err != nil {
		return nil, err
	}
	path := cleanPath(args.ProjectPath)
	if path == "" && e.scope == ScopeProject {
		path = e.originProjectPath
	}
	if path != "" {
		var err error
		path, _, err = e.resolveProject(ctx, path)
		if err != nil {
			return nil, err
		}
	}
	classifications, err := e.reader.ListSessionClassifications(ctx, path, strings.TrimSpace(args.SessionID))
	if err != nil {
		return nil, err
	}
	visible, err := e.visibleProjectMap(ctx, args.IncludeHistorical)
	if err != nil {
		return nil, err
	}
	records := make([]map[string]any, 0, len(classifications))
	for _, classification := range classifications {
		if _, ok := visible[cleanPath(classification.ProjectPath)]; !ok {
			continue
		}
		records = append(records, assessmentRecord(classification))
	}
	sort.SliceStable(records, func(i, j int) bool {
		return fmt.Sprint(records[i]["updated_at"]) > fmt.Sprint(records[j]["updated_at"])
	})
	result, err := pageResult("assessments", records, args.Cursor, args.Limit)
	if err != nil {
		return nil, err
	}
	if path != "" {
		result["project_path"] = path
	}
	return result, nil
}

type workListArgs struct {
	IncludeHistorical bool   `json:"include_historical"`
	Limit             int    `json:"limit"`
	Cursor            string `json:"cursor"`
}

type pageArgs struct {
	Limit  int    `json:"limit"`
	Cursor string `json:"cursor"`
}

func (e *Executor) agentTaskList(ctx context.Context, raw json.RawMessage) (map[string]any, error) {
	var args workListArgs
	if err := decodeStrict(raw, &args); err != nil {
		return nil, err
	}
	filter := model.AgentTaskFilter{IncludeArchived: args.IncludeHistorical, Limit: maximumPageLimit}
	if !args.IncludeHistorical {
		filter.Statuses = []model.AgentTaskStatus{model.AgentTaskStatusActive, model.AgentTaskStatusWaiting}
	}
	tasks, err := e.reader.ListAgentTasks(ctx, filter)
	if err != nil {
		return nil, err
	}
	records := make([]map[string]any, 0, len(tasks))
	for _, task := range tasks {
		if !e.taskVisible(task) {
			continue
		}
		records = append(records, agentTaskRecord(task))
	}
	return pageResult("agent_tasks", records, args.Cursor, args.Limit)
}

type idArgs struct {
	TaskID string `json:"task_id"`
}

func (e *Executor) agentTaskGet(ctx context.Context, raw json.RawMessage) (map[string]any, error) {
	var args idArgs
	if err := decodeStrict(raw, &args); err != nil {
		return nil, err
	}
	taskID := strings.TrimSpace(args.TaskID)
	if taskID == "" {
		return nil, errors.New("task_id is required")
	}
	task, err := e.reader.GetAgentTask(ctx, taskID)
	if err != nil {
		return nil, err
	}
	if !e.taskVisible(task) {
		return nil, errors.New("agent task is hidden by the private-category disclosure policy")
	}
	return map[string]any{"agent_task": agentTaskRecord(task)}, nil
}

func (e *Executor) goalRunList(ctx context.Context, raw json.RawMessage) (map[string]any, error) {
	var args pageArgs
	if err := decodeStrict(raw, &args); err != nil {
		return nil, err
	}
	runs, err := e.reader.ListGoalRuns(ctx, maximumPageLimit)
	if err != nil {
		return nil, err
	}
	records := make([]map[string]any, 0, len(runs))
	for _, run := range runs {
		visible, err := e.goalVisible(ctx, run)
		if err != nil {
			return nil, err
		}
		if visible {
			records = append(records, goalRunRecord(run, false, 0))
		}
	}
	return pageResult("goal_runs", records, args.Cursor, args.Limit)
}

type goalGetArgs struct {
	RunID      string `json:"run_id"`
	TraceLimit int    `json:"trace_limit"`
}

func (e *Executor) goalRunGet(ctx context.Context, raw json.RawMessage) (map[string]any, error) {
	var args goalGetArgs
	if err := decodeStrict(raw, &args); err != nil {
		return nil, err
	}
	runID := strings.TrimSpace(args.RunID)
	if runID == "" {
		return nil, errors.New("run_id is required")
	}
	traceLimit, err := boundedLimit(args.TraceLimit, 20, maximumPageLimit)
	if err != nil {
		return nil, fmt.Errorf("trace_limit: %w", err)
	}
	run, err := e.reader.GetGoalRun(ctx, runID)
	if err != nil {
		return nil, err
	}
	visible, err := e.goalVisible(ctx, run)
	if err != nil {
		return nil, err
	}
	if !visible {
		return nil, errors.New("goal run is hidden by the private-category disclosure policy")
	}
	truncated := len(run.Trace) > traceLimit
	return map[string]any{
		"goal_run":  goalRunRecord(run, true, traceLimit),
		"truncated": truncated,
	}, nil
}

func (e *Executor) visibleProjects(ctx context.Context, includeHistorical bool) ([]model.ProjectSummary, error) {
	projects, err := e.reader.ListProjects(ctx, includeHistorical)
	if err != nil {
		return nil, err
	}
	visible := make([]model.ProjectSummary, 0, len(projects))
	for _, project := range projects {
		if !e.projectVisible(project) {
			continue
		}
		if e.scope == ScopeProject && cleanPath(project.Path) != e.originProjectPath {
			continue
		}
		visible = append(visible, project)
	}
	return visible, nil
}

func (e *Executor) visibleProjectMap(ctx context.Context, includeHistorical bool) (map[string]model.ProjectSummary, error) {
	projects, err := e.visibleProjects(ctx, includeHistorical)
	if err != nil {
		return nil, err
	}
	byPath := make(map[string]model.ProjectSummary, len(projects))
	for _, project := range projects {
		byPath[cleanPath(project.Path)] = project
	}
	return byPath, nil
}

func (e *Executor) resolveProject(ctx context.Context, requestedPath string) (string, model.ProjectSummary, error) {
	path := cleanPath(requestedPath)
	if path == "" {
		path = e.originProjectPath
	}
	if e.scope == ScopeProject && path != e.originProjectPath {
		return "", model.ProjectSummary{}, errors.New("project path is outside this embedded session's query scope")
	}
	project, err := e.reader.GetProjectSummary(ctx, path, true)
	if err != nil {
		return "", model.ProjectSummary{}, err
	}
	if !e.projectVisible(project) {
		return "", model.ProjectSummary{}, errors.New("project is hidden by the private-category disclosure policy")
	}
	return path, project, nil
}

func (e *Executor) projectVisible(project model.ProjectSummary) bool {
	switch e.disclosure {
	case DisclosureHost:
		return true
	case DisclosureHidePrivate:
		return !project.CategoryPrivate
	default:
		return !project.CategoryPrivate || cleanPath(project.Path) == e.originProjectPath
	}
}

func (e *Executor) taskVisible(task model.AgentTask) bool {
	switch e.disclosure {
	case DisclosureHost:
		return true
	case DisclosureHidePrivate:
		return !task.CategoryPrivate
	}
	if !task.CategoryPrivate {
		return true
	}
	for _, resource := range task.Resources {
		if cleanPath(resource.ProjectPath) == e.originProjectPath {
			return true
		}
	}
	return false
}

func (e *Executor) goalVisible(ctx context.Context, run bossrun.GoalRecord) (bool, error) {
	for _, resource := range goalResources(run.Proposal) {
		if path := cleanPath(resource.ProjectPath); path != "" {
			project, err := e.reader.GetProjectSummary(ctx, path, true)
			if err != nil || !e.projectVisible(project) {
				return false, nil
			}
		}
		if resource.Kind == control.ResourceAgentTask && strings.TrimSpace(resource.ID) != "" {
			task, err := e.reader.GetAgentTask(ctx, resource.ID)
			if err != nil || !e.taskVisible(task) {
				return false, nil
			}
		}
	}
	return true, nil
}

func goalResources(proposal bossrun.GoalProposal) []control.ResourceRef {
	resources := append([]control.ResourceRef(nil), proposal.Authority.Resources...)
	resources = append(resources, proposal.ArchiveResources...)
	resources = append(resources, proposal.KeepResources...)
	resources = append(resources, proposal.ReviewResources...)
	for _, step := range proposal.Plan.Steps {
		resources = append(resources, step.Resources...)
	}
	return resources
}

func pageResult(key string, records []map[string]any, cursor string, requestedLimit int) (map[string]any, error) {
	limit, err := boundedLimit(requestedLimit, defaultPageLimit, maximumPageLimit)
	if err != nil {
		return nil, fmt.Errorf("limit: %w", err)
	}
	offset, err := decodeCursor(cursor)
	if err != nil {
		return nil, err
	}
	if offset > len(records) {
		return nil, errors.New("cursor is past the end of the current result set")
	}
	end := min(offset+limit, len(records))
	page := append([]map[string]any(nil), records[offset:end]...)
	result := map[string]any{
		key:         page,
		"count":     len(page),
		"total":     len(records),
		"truncated": end < len(records),
	}
	if end < len(records) {
		result["next_cursor"] = encodeCursor(end)
	}
	return result, nil
}

func boundedLimit(value, defaultValue, maximum int) (int, error) {
	if value == 0 {
		return defaultValue, nil
	}
	if value < 1 || value > maximum {
		return 0, fmt.Errorf("must be between 1 and %d", maximum)
	}
	return value, nil
}

func encodeCursor(offset int) string {
	return base64.RawURLEncoding.EncodeToString([]byte(strconv.Itoa(offset)))
}

func decodeCursor(cursor string) (int, error) {
	cursor = strings.TrimSpace(cursor)
	if cursor == "" {
		return 0, nil
	}
	decoded, err := base64.RawURLEncoding.DecodeString(cursor)
	if err != nil {
		return 0, errors.New("cursor is invalid")
	}
	offset, err := strconv.Atoi(string(decoded))
	if err != nil || offset < 0 {
		return 0, errors.New("cursor is invalid")
	}
	return offset, nil
}

func decodeStrict(raw json.RawMessage, destination any) error {
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(destination); err != nil {
		return fmt.Errorf("arguments do not match the described input schema: %w", err)
	}
	var trailing any
	if err := decoder.Decode(&trailing); !errors.Is(err, io.EOF) {
		return errors.New("arguments must contain exactly one JSON object")
	}
	return nil
}

func cleanPath(path string) string {
	path = strings.TrimSpace(path)
	if path == "" {
		return ""
	}
	return filepath.Clean(path)
}

func clip(value string, maxRunes int) string {
	value = strings.TrimSpace(value)
	if maxRunes <= 0 {
		return ""
	}
	runes := []rune(value)
	if len(runes) <= maxRunes {
		return value
	}
	return strings.TrimSpace(string(runes[:maxRunes])) + "…"
}

func formatTime(value time.Time) string {
	if value.IsZero() {
		return ""
	}
	return value.UTC().Format(time.RFC3339Nano)
}

func take(records []map[string]any, limit int) ([]map[string]any, bool) {
	if len(records) <= limit {
		return records, false
	}
	return records[:limit], true
}
