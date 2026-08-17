package agentquery

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"
	"time"

	"lcroom/internal/bossrun"
	"lcroom/internal/model"
)

func TestCapabilityCatalogFiltersByScopeAndDefersSchemas(t *testing.T) {
	projectQueries := CapabilitySummaries(DomainProject, ScopeProject)
	if len(projectQueries) == 0 {
		t.Fatal("project query catalog is empty")
	}
	for _, query := range projectQueries {
		if query.Name == QueryProjectSearch {
			t.Fatalf("project-scoped catalog exposed portfolio query: %#v", query)
		}
	}
	if capability, ok := CapabilityByName(QueryProjectDetail); !ok || capability.InputSchema == nil || capability.OutputSchema == nil {
		t.Fatalf("project.detail = %#v, %t; want full schemas", capability, ok)
	}
}

func TestExecutorPortfolioListHidesPrivateProjectsAndPaginates(t *testing.T) {
	origin := "/repos/private-origin"
	reader := newFakeReader([]model.ProjectSummary{
		{Path: origin, Name: "Origin", InScope: true, CategoryPrivate: true, AttentionScore: 40},
		{Path: "/repos/private-other", Name: "Other secret", InScope: true, CategoryPrivate: true, AttentionScore: 30},
		{Path: "/repos/public-a", Name: "Public A", InScope: true, AttentionScore: 20},
		{Path: "/repos/public-b", Name: "Public B", InScope: true, AttentionScore: 10},
	})
	executor := mustExecutor(t, reader, origin, ScopePortfolio)

	first, err := executor.Execute(t.Context(), QueryProjectList, json.RawMessage(`{"limit":1}`))
	if err != nil {
		t.Fatal(err)
	}
	if first["total"] != 3 || first["truncated"] != true {
		t.Fatalf("first page = %#v, want three visible projects and truncation", first)
	}
	projects := first["projects"].([]map[string]any)
	if len(projects) != 1 || projects[0]["path"] != origin {
		t.Fatalf("first projects = %#v, want private originating project", projects)
	}
	if strings.Contains(mustMarshal(t, first), "private-other") {
		t.Fatalf("private non-origin project leaked: %#v", first)
	}

	secondArgs, _ := json.Marshal(map[string]any{"limit": 2, "cursor": first["next_cursor"]})
	second, err := executor.Execute(t.Context(), QueryProjectList, secondArgs)
	if err != nil {
		t.Fatal(err)
	}
	if second["count"] != 2 || second["truncated"] != false {
		t.Fatalf("second page = %#v, want final two visible projects", second)
	}
}

func TestExecutorProjectScopeAndPrivateDisclosureBoundary(t *testing.T) {
	origin := "/repos/origin"
	other := "/repos/other"
	reader := newFakeReader([]model.ProjectSummary{
		{Path: origin, Name: "Origin", InScope: true, CategoryPrivate: true},
		{Path: other, Name: "Other", InScope: true},
		{Path: "/repos/secret", Name: "Secret", InScope: true, CategoryPrivate: true},
	})
	reader.details[origin] = model.ProjectDetail{Summary: reader.projects[origin]}
	executor := mustExecutor(t, reader, origin, ScopeProject)

	listed, err := executor.Execute(t.Context(), QueryProjectList, nil)
	if err != nil {
		t.Fatal(err)
	}
	if listed["total"] != 1 {
		t.Fatalf("project-scoped list = %#v, want origin only", listed)
	}
	if _, err := executor.Execute(t.Context(), QueryProjectDetail, json.RawMessage(`{"project_path":"/repos/other"}`)); err == nil || !strings.Contains(err.Error(), "outside") {
		t.Fatalf("cross-project detail error = %v, want scope rejection", err)
	}
	if _, err := executor.Execute(t.Context(), QueryPortfolioOverview, nil); err == nil || !strings.Contains(err.Error(), "requires portfolio") {
		t.Fatalf("portfolio overview error = %v, want scope rejection", err)
	}
	if _, err := executor.Execute(t.Context(), QuerySessionList, json.RawMessage(`{"include_completed":true}`)); err == nil || !strings.Contains(err.Error(), "unknown field") {
		t.Fatalf("schema mismatch error = %v, want strict unknown-field rejection", err)
	}
}

func TestExecutorAssessmentsFilterPrivateProjectContent(t *testing.T) {
	origin := "/repos/origin"
	reader := newFakeReader([]model.ProjectSummary{
		{Path: origin, Name: "Origin", InScope: true},
		{Path: "/repos/public", Name: "Public", InScope: true},
		{Path: "/repos/private", Name: "Private", InScope: true, CategoryPrivate: true},
	})
	reader.assessments = []model.SessionClassification{
		{ProjectPath: "/repos/public", SessionID: "public-session", Summary: "public result", UpdatedAt: time.Unix(20, 0)},
		{ProjectPath: "/repos/private", SessionID: "private-session", Summary: "private result", UpdatedAt: time.Unix(30, 0)},
	}
	executor := mustExecutor(t, reader, origin, ScopePortfolio)
	result, err := executor.Execute(t.Context(), QueryAssessmentList, nil)
	if err != nil {
		t.Fatal(err)
	}
	if result["total"] != 1 || strings.Contains(mustMarshal(t, result), "private result") {
		t.Fatalf("assessment result = %#v, want only public content", result)
	}
}

func TestExecutorRechecksProjectPrivacyOnDetailRead(t *testing.T) {
	origin := "/repos/origin"
	other := "/repos/other"
	reader := newFakeReader([]model.ProjectSummary{
		{Path: origin, Name: "Origin", InScope: true},
		{Path: other, Name: "Other", InScope: true},
	})
	reader.details[other] = model.ProjectDetail{
		Summary: model.ProjectSummary{Path: other, Name: "Other", InScope: true, CategoryPrivate: true},
		Todos:   []model.TodoItem{{ProjectPath: other, Text: "newly private content"}},
	}
	executor := mustExecutor(t, reader, origin, ScopePortfolio)

	for _, query := range []Name{QueryProjectDetail, QueryTodoList, QuerySessionList} {
		_, err := executor.Execute(t.Context(), query, json.RawMessage(`{"project_path":"/repos/other"}`))
		if err == nil || !strings.Contains(err.Error(), "became hidden") {
			t.Fatalf("%s error = %v, want fail-closed privacy recheck", query, err)
		}
	}
}

type fakeReader struct {
	projects    map[string]model.ProjectSummary
	projectList []model.ProjectSummary
	details     map[string]model.ProjectDetail
	assessments []model.SessionClassification
	tasks       map[string]model.AgentTask
	goalRuns    map[string]bossrun.GoalRecord
}

func newFakeReader(projects []model.ProjectSummary) *fakeReader {
	reader := &fakeReader{
		projects:    map[string]model.ProjectSummary{},
		projectList: append([]model.ProjectSummary(nil), projects...),
		details:     map[string]model.ProjectDetail{},
		tasks:       map[string]model.AgentTask{},
		goalRuns:    map[string]bossrun.GoalRecord{},
	}
	for _, project := range projects {
		reader.projects[project.Path] = project
		reader.details[project.Path] = model.ProjectDetail{Summary: project}
	}
	return reader
}

func (r *fakeReader) ListProjects(_ context.Context, includeHistorical bool) ([]model.ProjectSummary, error) {
	projects := make([]model.ProjectSummary, 0, len(r.projectList))
	for _, project := range r.projectList {
		if !includeHistorical && (!project.InScope || project.Archived) {
			continue
		}
		projects = append(projects, project)
	}
	return projects, nil
}

func (r *fakeReader) GetProjectSummary(_ context.Context, path string, _ bool) (model.ProjectSummary, error) {
	project, ok := r.projects[path]
	if !ok {
		return model.ProjectSummary{}, errors.New("project not found")
	}
	return project, nil
}

func (r *fakeReader) GetProjectDetail(_ context.Context, path string, _ int) (model.ProjectDetail, error) {
	detail, ok := r.details[path]
	if !ok {
		return model.ProjectDetail{}, errors.New("project not found")
	}
	return detail, nil
}

func (r *fakeReader) ListSessionClassifications(_ context.Context, projectPath, sessionID string) ([]model.SessionClassification, error) {
	items := make([]model.SessionClassification, 0, len(r.assessments))
	for _, assessment := range r.assessments {
		if projectPath != "" && assessment.ProjectPath != projectPath {
			continue
		}
		if sessionID != "" && assessment.SessionID != sessionID && assessment.RawSessionID != sessionID {
			continue
		}
		items = append(items, assessment)
	}
	return items, nil
}

func (r *fakeReader) ListAgentTasks(_ context.Context, _ model.AgentTaskFilter) ([]model.AgentTask, error) {
	items := make([]model.AgentTask, 0, len(r.tasks))
	for _, task := range r.tasks {
		items = append(items, task)
	}
	return items, nil
}

func (r *fakeReader) GetAgentTask(_ context.Context, id string) (model.AgentTask, error) {
	task, ok := r.tasks[id]
	if !ok {
		return model.AgentTask{}, errors.New("task not found")
	}
	return task, nil
}

func (r *fakeReader) ListGoalRuns(_ context.Context, _ int) ([]bossrun.GoalRecord, error) {
	items := make([]bossrun.GoalRecord, 0, len(r.goalRuns))
	for _, run := range r.goalRuns {
		items = append(items, run)
	}
	return items, nil
}

func (r *fakeReader) GetGoalRun(_ context.Context, id string) (bossrun.GoalRecord, error) {
	run, ok := r.goalRuns[id]
	if !ok {
		return bossrun.GoalRecord{}, errors.New("goal run not found")
	}
	return run, nil
}

func mustExecutor(t *testing.T, reader Reader, origin string, scope Scope) *Executor {
	t.Helper()
	executor, err := NewExecutor(Options{
		Reader:            reader,
		OriginProjectPath: origin,
		Scope:             scope,
		Now:               func() time.Time { return time.Unix(100, 0) },
	})
	if err != nil {
		t.Fatal(err)
	}
	return executor
}

func mustMarshal(t *testing.T, value any) string {
	t.Helper()
	encoded, err := json.Marshal(value)
	if err != nil {
		t.Fatal(err)
	}
	return string(encoded)
}
