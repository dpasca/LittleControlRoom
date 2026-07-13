package boss

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"testing"
	"time"

	"lcroom/internal/agentcontext"
	"lcroom/internal/bossrun"
	"lcroom/internal/control"
	"lcroom/internal/llm"
	"lcroom/internal/model"
)

type fakeTextRunner struct {
	req  llm.TextRequest
	resp llm.TextResponse
	err  error
}

func (r *fakeTextRunner) RunText(_ context.Context, req llm.TextRequest) (llm.TextResponse, error) {
	r.req = req
	return r.resp, r.err
}

type fakeStreamingTextRunner struct {
	req    llm.TextRequest
	resp   llm.TextResponse
	err    error
	deltas []string
}

func (r *fakeStreamingTextRunner) RunText(_ context.Context, req llm.TextRequest) (llm.TextResponse, error) {
	r.req = req
	return r.resp, r.err
}

func (r *fakeStreamingTextRunner) RunTextStream(_ context.Context, req llm.TextRequest, handle llm.TextStreamHandler) (llm.TextResponse, error) {
	r.req = req
	if r.err != nil {
		return llm.TextResponse{}, r.err
	}
	for _, delta := range r.deltas {
		if err := handle(llm.TextStreamEvent{Delta: delta}); err != nil {
			return llm.TextResponse{}, err
		}
	}
	return r.resp, nil
}

type fakeJSONSchemaRunner struct {
	reqs  []llm.JSONSchemaRequest
	resp  []llm.JSONSchemaResponse
	err   error
	index int
}

func (r *fakeJSONSchemaRunner) RunJSONSchema(_ context.Context, req llm.JSONSchemaRequest) (llm.JSONSchemaResponse, error) {
	r.reqs = append(r.reqs, req)
	if r.err != nil {
		return llm.JSONSchemaResponse{}, r.err
	}
	if r.index >= len(r.resp) {
		return llm.JSONSchemaResponse{}, errors.New("unexpected structured action request")
	}
	resp := r.resp[r.index]
	r.index++
	return resp, nil
}

func bossTextMessagesContent(messages []llm.TextMessage) string {
	var b strings.Builder
	for _, message := range messages {
		b.WriteString(message.Role)
		b.WriteString(": ")
		b.WriteString(message.Content)
		b.WriteString("\n")
	}
	return b.String()
}

type fakeBossStore struct {
	projects        []model.ProjectSummary
	details         map[string]model.ProjectDetail
	classifications []model.SessionClassification
	counts          map[model.SessionClassificationStatus]int
	searchResults   []model.ContextSearchResult
	sessionSamples  []model.SessionContextSample
	excerpt         model.SessionContextExcerpt
	excerptErr      error
	agentTasks      []model.AgentTask
	goalRuns        []bossrun.GoalRecord
}

func (s *fakeBossStore) ListProjects(context.Context, bool) ([]model.ProjectSummary, error) {
	return append([]model.ProjectSummary(nil), s.projects...), nil
}

func (s *fakeBossStore) GetProjectSummary(_ context.Context, path string, _ bool) (model.ProjectSummary, error) {
	for _, project := range s.projects {
		if project.Path == path {
			return project, nil
		}
	}
	return model.ProjectSummary{}, sql.ErrNoRows
}

func (s *fakeBossStore) GetProjectDetail(_ context.Context, path string, _ int) (model.ProjectDetail, error) {
	if detail, ok := s.details[path]; ok {
		return detail, nil
	}
	return model.ProjectDetail{}, sql.ErrNoRows
}

func (s *fakeBossStore) ListSessionClassifications(context.Context, string, string) ([]model.SessionClassification, error) {
	return append([]model.SessionClassification(nil), s.classifications...), nil
}

func (s *fakeBossStore) GetSessionClassificationCounts(context.Context, bool) (map[model.SessionClassificationStatus]int, error) {
	out := map[model.SessionClassificationStatus]int{}
	for status, count := range s.counts {
		out[status] = count
	}
	return out, nil
}

func (s *fakeBossStore) SearchContext(context.Context, model.ContextSearchRequest) ([]model.ContextSearchResult, error) {
	return append([]model.ContextSearchResult(nil), s.searchResults...), nil
}

func (s *fakeBossStore) SampleProjectSessionContext(context.Context, string, int) ([]model.SessionContextSample, error) {
	return append([]model.SessionContextSample(nil), s.sessionSamples...), nil
}

func (s *fakeBossStore) GetSessionContextExcerpt(context.Context, model.SessionContextExcerptRequest) (model.SessionContextExcerpt, error) {
	if s.excerptErr != nil {
		return model.SessionContextExcerpt{}, s.excerptErr
	}
	return s.excerpt, nil
}

func (s *fakeBossStore) GetAgentTask(_ context.Context, id string) (model.AgentTask, error) {
	for _, task := range s.agentTasks {
		if task.ID == id {
			return task, nil
		}
	}
	return model.AgentTask{}, sql.ErrNoRows
}

func (s *fakeBossStore) ListAgentTasks(_ context.Context, filter model.AgentTaskFilter) ([]model.AgentTask, error) {
	statuses := map[model.AgentTaskStatus]struct{}{}
	for _, status := range filter.Statuses {
		statuses[model.NormalizeAgentTaskStatus(status)] = struct{}{}
	}
	out := make([]model.AgentTask, 0, len(s.agentTasks))
	for _, task := range s.agentTasks {
		task.Kind = model.NormalizeAgentTaskKind(task.Kind)
		task.Status = model.NormalizeAgentTaskStatus(task.Status)
		if filter.Kind != "" && task.Kind != model.NormalizeAgentTaskKind(filter.Kind) {
			continue
		}
		if len(statuses) > 0 {
			if _, ok := statuses[task.Status]; !ok {
				continue
			}
		} else if !filter.IncludeArchived && task.Status == model.AgentTaskStatusArchived {
			continue
		}
		out = append(out, task)
		if filter.Limit > 0 && len(out) >= filter.Limit {
			break
		}
	}
	return out, nil
}

func (s *fakeBossStore) ListGoalRuns(context.Context, int) ([]bossrun.GoalRecord, error) {
	return append([]bossrun.GoalRecord(nil), s.goalRuns...), nil
}

func (s *fakeBossStore) GetGoalRun(_ context.Context, id string) (bossrun.GoalRecord, error) {
	for _, record := range s.goalRuns {
		if record.Proposal.Run.ID == id {
			return record, nil
		}
	}
	return bossrun.GoalRecord{}, sql.ErrNoRows
}

func TestAssistantReplyIncludesStateBriefAndRecentChat(t *testing.T) {
	t.Parallel()

	if bossAssistantReasoningEffort != "high" {
		t.Fatalf("boss reasoning effort = %q, want high", bossAssistantReasoningEffort)
	}

	runner := &fakeTextRunner{
		resp: llm.TextResponse{Model: "gpt-test", OutputText: "Look at Alpha first."},
	}
	assistant := &Assistant{runner: runner, model: "gpt-test"}
	resp, err := assistant.Reply(context.Background(), AssistantRequest{
		StateBrief: "Visible projects: 2.",
		Messages: []ChatMessage{
			{Role: "user", Content: "What should I do?"},
			{Role: "assistant", Content: "Let me check."},
			{Role: "user", Content: "Be brief."},
		},
	})
	if err != nil {
		t.Fatalf("Reply() error = %v", err)
	}
	if resp.Content != "Look at Alpha first." {
		t.Fatalf("Content = %q", resp.Content)
	}
	if runner.req.Model != "gpt-test" || runner.req.ReasoningEffort != bossAssistantReasoningEffort {
		t.Fatalf("unexpected request config: %+v", runner.req)
	}
	if len(runner.req.Messages) != 4 {
		t.Fatalf("messages len = %d, want 4", len(runner.req.Messages))
	}
	if !strings.Contains(runner.req.Messages[0].Content, "Current exchange time:") {
		t.Fatalf("exchange timestamp missing from first message: %+v", runner.req.Messages[0])
	}
	if !strings.Contains(runner.req.Messages[0].Content, "Visible projects: 2.") {
		t.Fatalf("state brief missing from first message: %+v", runner.req.Messages[0])
	}
	if runner.req.Messages[2].Role != "assistant" {
		t.Fatalf("assistant history role = %q", runner.req.Messages[2].Role)
	}
	if !strings.Contains(runner.req.SystemText, "Little Control Room") {
		t.Fatalf("system prompt missing product context: %q", runner.req.SystemText)
	}
}

func TestQueryExecutorGoalRunReport(t *testing.T) {
	t.Parallel()

	store := &fakeBossStore{
		goalRuns: []bossrun.GoalRecord{{
			Proposal: bossrun.GoalProposal{
				Run: bossrun.GoalRun{
					ID:        "goal_demo",
					Kind:      bossrun.GoalKindAgentTaskCleanup,
					Title:     "Clear stale delegated agents",
					Objective: "Archive stale delegated agent task records.",
					Status:    bossrun.GoalStatusCompleted,
				},
			},
			Result: bossrun.GoalResult{
				Summary:         "Archived 2 delegated agent task records and verified the selected tasks are out of the active set.",
				ArchivedTaskIDs: []string{"agt_one", "agt_two"},
				Verified:        true,
			},
			Trace: []bossrun.TraceEntry{{StepID: "archive-agent-tasks", Status: "completed"}},
		}},
	}
	executor := newQueryExecutor(store)
	result, err := executor.Execute(context.Background(), bossAction{Kind: bossActionGoalRunReport}, StateSnapshot{}, ViewContext{})
	if err != nil {
		t.Fatalf("Execute(goal_run_report) error = %v", err)
	}
	if result.Name != bossActionGoalRunReport {
		t.Fatalf("result name = %q, want goal_run_report", result.Name)
	}
	for _, want := range []string{"Recent LCR goal runs", "goal_demo", "Archived 2 delegated agent task records", "trace entries: 1"} {
		if !strings.Contains(result.Text, want) {
			t.Fatalf("goal run report missing %q:\n%s", want, result.Text)
		}
	}
}

func TestQueryExecutorGoalRunReportSpecificRun(t *testing.T) {
	t.Parallel()

	store := &fakeBossStore{
		goalRuns: []bossrun.GoalRecord{{
			Proposal: bossrun.GoalProposal{
				Run: bossrun.GoalRun{
					ID:              "goal_demo",
					Kind:            bossrun.GoalKindAgentTaskCleanup,
					Title:           "Clear stale delegated agents",
					Objective:       "Archive stale delegated agent task records.",
					SuccessCriteria: "Selected records leave the active set.",
					Status:          bossrun.GoalStatusCompleted,
				},
			},
			Result: bossrun.GoalResult{
				Summary:  "Archived 2 delegated agent task records and verified the selected tasks are out of the active set.",
				Verified: true,
			},
			Trace: []bossrun.TraceEntry{
				{StepID: "archive-agent-tasks", ResourceID: "agt_one", Status: "completed", Summary: "Archived agent task record."},
				{StepID: "verify-active-set", Status: "completed", Summary: "Refreshed open agent-task state."},
			},
		}},
	}
	executor := newQueryExecutor(store)
	result, err := executor.Execute(context.Background(), bossAction{Kind: bossActionGoalRunReport, Query: "goal_demo"}, StateSnapshot{}, ViewContext{})
	if err != nil {
		t.Fatalf("Execute(goal_run_report specific) error = %v", err)
	}
	for _, want := range []string{"LCR goal run", "goal_demo", "success criteria", "archive-agent-tasks agt_one [completed]", "verify-active-set [completed]"} {
		if !strings.Contains(result.Text, want) {
			t.Fatalf("specific goal report missing %q:\n%s", want, result.Text)
		}
	}
}

func TestAssistantReplyStructuredGoalRunHandleBypassesModels(t *testing.T) {
	t.Parallel()

	store := &fakeBossStore{
		goalRuns: []bossrun.GoalRecord{{
			Proposal: bossrun.GoalProposal{
				Run: bossrun.GoalRun{
					ID:              "goal_demo",
					Kind:            bossrun.GoalKindAgentTaskCleanup,
					Title:           "Clear stale delegated agents",
					SuccessCriteria: "Selected records leave the active set.",
					Status:          bossrun.GoalStatusCompleted,
				},
			},
			Result: bossrun.GoalResult{
				Summary:  "Archived 2 delegated agent task records and verified the selected tasks are out of the active set.",
				Verified: true,
			},
			Trace: []bossrun.TraceEntry{
				{StepID: "archive-agent-tasks", ResourceID: "agt_one", Status: "completed", Summary: "Archived agent task record."},
				{StepID: "verify-active-set", Status: "completed", Summary: "Refreshed open agent-task state."},
			},
		}},
	}
	router := &fakeJSONSchemaRunner{}
	planner := &fakeJSONSchemaRunner{}
	assistant := &Assistant{
		planner:     planner,
		queryRouter: router,
		query:       newQueryExecutor(store),
		model:       "gpt-test",
	}

	resp, err := assistant.Reply(context.Background(), AssistantRequest{
		StateBrief: "Recent LCR goal runs:\n- Clear stale delegated agents (goal_demo/completed); inspect: goal_run_report query=goal_demo",
		Snapshot: StateSnapshot{RecentGoalRuns: []GoalRunBrief{{
			ID:         "goal_demo",
			Kind:       bossrun.GoalKindAgentTaskCleanup,
			Title:      "Clear stale delegated agents",
			Status:     bossrun.GoalStatusCompleted,
			TraceCount: 2,
		}}},
		Messages: []ChatMessage{{Role: "user", Content: "inspect the goal-run trace for goal_demo"}},
	})
	if err != nil {
		t.Fatalf("Reply() error = %v", err)
	}
	if len(router.reqs) != 0 || len(planner.reqs) != 0 {
		t.Fatalf("model requests router/planner = %d/%d, want none", len(router.reqs), len(planner.reqs))
	}
	for _, want := range []string{"LCR goal run", "goal_demo", "archive-agent-tasks agt_one [completed]", "verify-active-set [completed]"} {
		if !strings.Contains(resp.Content, want) {
			t.Fatalf("structured handle report missing %q:\n%s", want, resp.Content)
		}
	}
}

func TestAssistantReplyStructuredGoalRunHandleCanResolveFromStore(t *testing.T) {
	t.Parallel()

	store := &fakeBossStore{
		goalRuns: []bossrun.GoalRecord{{
			Proposal: bossrun.GoalProposal{
				Run: bossrun.GoalRun{ID: "goal_demo", Kind: bossrun.GoalKindAgentTaskCleanup, Title: "Goal demo", Status: bossrun.GoalStatusCompleted},
			},
			Result: bossrun.GoalResult{Summary: "Verified the selected task left the active set.", Verified: true},
			Trace:  []bossrun.TraceEntry{{StepID: "verify-active-set", Status: "completed", Summary: "Refreshed open agent-task state."}},
		}},
	}
	router := &fakeJSONSchemaRunner{}
	planner := &fakeJSONSchemaRunner{}
	assistant := &Assistant{
		planner:     planner,
		queryRouter: router,
		query:       newQueryExecutor(store),
		model:       "gpt-test",
	}

	resp, err := assistant.Reply(context.Background(), AssistantRequest{
		StateBrief: "Visible projects: 1.",
		Messages:   []ChatMessage{{Role: "user", Content: "inspect the goal-run trace for goal_demo"}},
	})
	if err != nil {
		t.Fatalf("Reply() error = %v", err)
	}
	if len(router.reqs) != 0 || len(planner.reqs) != 0 {
		t.Fatalf("model requests router/planner = %d/%d, want none", len(router.reqs), len(planner.reqs))
	}
	if !strings.Contains(resp.Content, "verify-active-set [completed]") {
		t.Fatalf("store-resolved goal report = %q", resp.Content)
	}
}

func TestAssistantReplyStructuredGoalRunHandleUsesBoundaries(t *testing.T) {
	t.Parallel()

	store := &fakeBossStore{
		goalRuns: []bossrun.GoalRecord{{
			Proposal: bossrun.GoalProposal{
				Run: bossrun.GoalRun{ID: "goal_demo", Kind: bossrun.GoalKindAgentTaskCleanup, Title: "Goal demo", Status: bossrun.GoalStatusCompleted},
			},
		}},
	}
	router := &fakeJSONSchemaRunner{
		resp: []llm.JSONSchemaResponse{{
			OutputText: encodedReadOnlyRoute(t, bossReadOnlyRoute{Kind: bossReadOnlyRoutePass}),
			Usage:      model.LLMUsage{TotalTokens: 3},
		}},
	}
	planner := &fakeJSONSchemaRunner{
		resp: []llm.JSONSchemaResponse{{
			OutputText: encodedBossAction(t, bossAction{Kind: bossActionAnswer, Answer: "No exact goal handle was selected."}),
			Usage:      model.LLMUsage{TotalTokens: 7},
		}},
	}
	assistant := &Assistant{
		planner:     planner,
		queryRouter: router,
		query:       newQueryExecutor(store),
		model:       "gpt-test",
	}

	resp, err := assistant.Reply(context.Background(), AssistantRequest{
		StateBrief: "Recent LCR goal runs:\n- Goal demo (goal_demo/completed)",
		Snapshot: StateSnapshot{RecentGoalRuns: []GoalRunBrief{{
			ID:     "goal_demo",
			Kind:   bossrun.GoalKindAgentTaskCleanup,
			Title:  "Goal demo",
			Status: bossrun.GoalStatusCompleted,
		}}},
		Messages: []ChatMessage{{Role: "user", Content: "inspect goal_demo_suffix"}},
	})
	if err != nil {
		t.Fatalf("Reply() error = %v", err)
	}
	if resp.Content != "No exact goal handle was selected." {
		t.Fatalf("Content = %q", resp.Content)
	}
	if len(router.reqs) != 1 || len(planner.reqs) != 1 {
		t.Fatalf("model requests router/planner = %d/%d, want fallback", len(router.reqs), len(planner.reqs))
	}
}

func TestAssistantReplyStreamStructuredGoalRunHandleEmitsToolEvents(t *testing.T) {
	t.Parallel()

	store := &fakeBossStore{
		goalRuns: []bossrun.GoalRecord{{
			Proposal: bossrun.GoalProposal{
				Run: bossrun.GoalRun{ID: "goal_demo", Kind: bossrun.GoalKindAgentTaskCleanup, Title: "Goal demo", Status: bossrun.GoalStatusCompleted},
			},
			Result: bossrun.GoalResult{Summary: "Verified the selected task left the active set.", Verified: true},
			Trace:  []bossrun.TraceEntry{{StepID: "verify-active-set", Status: "completed", Summary: "Refreshed open agent-task state."}},
		}},
	}
	router := &fakeJSONSchemaRunner{}
	planner := &fakeJSONSchemaRunner{}
	assistant := &Assistant{
		planner:     planner,
		queryRouter: router,
		query:       newQueryExecutor(store),
		model:       "gpt-test",
	}

	var events []AssistantStreamEvent
	resp, err := assistant.ReplyStream(context.Background(), AssistantRequest{
		StateBrief: "Recent LCR goal runs:\n- Goal demo (goal_demo/completed); inspect: goal_run_report query=goal_demo",
		Snapshot: StateSnapshot{RecentGoalRuns: []GoalRunBrief{{
			ID:         "goal_demo",
			Kind:       bossrun.GoalKindAgentTaskCleanup,
			Title:      "Goal demo",
			Status:     bossrun.GoalStatusCompleted,
			TraceCount: 1,
		}}},
		Messages: []ChatMessage{{Role: "user", Content: "inspect the goal-run trace for goal_demo"}},
	}, func(event AssistantStreamEvent) {
		events = append(events, event)
	})
	if err != nil {
		t.Fatalf("ReplyStream() error = %v", err)
	}
	if len(router.reqs) != 0 || len(planner.reqs) != 0 {
		t.Fatalf("model requests router/planner = %d/%d, want none", len(router.reqs), len(planner.reqs))
	}
	var toolStates []string
	var text string
	for _, event := range events {
		if event.Kind == AssistantStreamToolCall {
			toolStates = append(toolStates, event.ToolState+":"+event.ToolCall)
		}
		if event.Kind == AssistantStreamTextDelta {
			text += event.Delta
		}
	}
	if strings.Join(toolStates, "|") != "running:goal_run_report goal_demo|done:goal_run_report goal_demo" {
		t.Fatalf("tool events = %#v", toolStates)
	}
	if text != resp.Content || !strings.Contains(text, "verify-active-set [completed]") {
		t.Fatalf("streamed text = %q, response = %q", text, resp.Content)
	}
}

func TestAssistantReplyFastRoutesGoalRunReport(t *testing.T) {
	t.Parallel()

	store := &fakeBossStore{
		goalRuns: []bossrun.GoalRecord{{
			Proposal: bossrun.GoalProposal{
				Run: bossrun.GoalRun{
					ID:              "goal_demo",
					Kind:            bossrun.GoalKindAgentTaskCleanup,
					Title:           "Clear stale delegated agents",
					Objective:       "Archive stale delegated agent task records.",
					SuccessCriteria: "Selected records leave the active set.",
					Status:          bossrun.GoalStatusCompleted,
				},
			},
			Result: bossrun.GoalResult{
				Summary:  "Archived 2 delegated agent task records and verified the selected tasks are out of the active set.",
				Verified: true,
			},
			Trace: []bossrun.TraceEntry{
				{StepID: "archive-agent-tasks", ResourceID: "agt_one", Status: "completed", Summary: "Archived agent task record."},
				{StepID: "verify-active-set", Status: "completed", Summary: "Refreshed open agent-task state."},
			},
		}},
	}
	router := &fakeJSONSchemaRunner{
		resp: []llm.JSONSchemaResponse{{
			Model:      "gpt-test",
			OutputText: encodedReadOnlyRoute(t, bossReadOnlyRoute{Kind: bossActionGoalRunReport, Query: "goal_demo", Reason: "The user asked to inspect a goal-run trace."}),
			Usage:      model.LLMUsage{TotalTokens: 11},
		}},
	}
	planner := &fakeJSONSchemaRunner{}
	assistant := &Assistant{
		planner:     planner,
		queryRouter: router,
		query:       newQueryExecutor(store),
		model:       "gpt-test",
	}

	resp, err := assistant.Reply(context.Background(), AssistantRequest{
		StateBrief: "Recent LCR goal runs:\n- Clear stale delegated agents (goal_demo/completed); inspect: goal_run_report query=goal_demo",
		Messages:   []ChatMessage{{Role: "user", Content: "inspect the latest goal-run trace"}},
	})
	if err != nil {
		t.Fatalf("Reply() error = %v", err)
	}
	if len(planner.reqs) != 0 {
		t.Fatalf("heavy planner requests = %d, want none", len(planner.reqs))
	}
	if resp.Usage.TotalTokens != 11 {
		t.Fatalf("usage total = %d, want router-only usage", resp.Usage.TotalTokens)
	}
	for _, want := range []string{"LCR goal run", "goal_demo", "archive-agent-tasks agt_one [completed]", "verify-active-set [completed]"} {
		if !strings.Contains(resp.Content, want) {
			t.Fatalf("fast goal report missing %q:\n%s", want, resp.Content)
		}
	}
	if len(router.reqs) != 1 {
		t.Fatalf("router requests = %d, want one", len(router.reqs))
	}
	if router.reqs[0].SchemaName != "boss_read_only_query_route" ||
		router.reqs[0].ReasoningEffort != bossReadOnlyRouterReasoningEffort {
		t.Fatalf("router request = %+v", router.reqs[0])
	}
}

func TestAssistantReplyStreamFastRoutesGoalRunReport(t *testing.T) {
	t.Parallel()

	store := &fakeBossStore{
		goalRuns: []bossrun.GoalRecord{{
			Proposal: bossrun.GoalProposal{
				Run: bossrun.GoalRun{
					ID:              "goal_demo",
					Kind:            bossrun.GoalKindAgentTaskCleanup,
					Title:           "Clear stale delegated agents",
					SuccessCriteria: "Selected records leave the active set.",
					Status:          bossrun.GoalStatusCompleted,
				},
			},
			Result: bossrun.GoalResult{
				Summary:  "Archived 2 delegated agent task records and verified the selected tasks are out of the active set.",
				Verified: true,
			},
			Trace: []bossrun.TraceEntry{
				{StepID: "archive-agent-tasks", ResourceID: "agt_one", Status: "completed", Summary: "Archived agent task record."},
				{StepID: "verify-active-set", Status: "completed", Summary: "Refreshed open agent-task state."},
			},
		}},
	}
	router := &fakeJSONSchemaRunner{
		resp: []llm.JSONSchemaResponse{{
			Model:      "gpt-test",
			OutputText: encodedReadOnlyRoute(t, bossReadOnlyRoute{Kind: bossActionGoalRunReport, Query: "goal_demo"}),
		}},
	}
	planner := &fakeJSONSchemaRunner{}
	assistant := &Assistant{
		planner:     planner,
		queryRouter: router,
		query:       newQueryExecutor(store),
		model:       "gpt-test",
	}

	var events []AssistantStreamEvent
	resp, err := assistant.ReplyStream(context.Background(), AssistantRequest{
		StateBrief: "Recent LCR goal runs:\n- Clear stale delegated agents (goal_demo/completed); inspect: goal_run_report query=goal_demo",
		Messages:   []ChatMessage{{Role: "user", Content: "inspect the latest goal-run trace"}},
	}, func(event AssistantStreamEvent) {
		events = append(events, event)
	})
	if err != nil {
		t.Fatalf("ReplyStream() error = %v", err)
	}
	if len(planner.reqs) != 0 {
		t.Fatalf("heavy planner requests = %d, want none", len(planner.reqs))
	}
	if !strings.Contains(resp.Content, "archive-agent-tasks agt_one [completed]") ||
		!strings.Contains(resp.Content, "verify-active-set [completed]") {
		t.Fatalf("streamed goal report content = %q", resp.Content)
	}
	var toolStates []string
	var text string
	for _, event := range events {
		if event.Kind == AssistantStreamToolCall {
			toolStates = append(toolStates, event.ToolState+":"+event.ToolCall)
		}
		if event.Kind == AssistantStreamTextDelta {
			text += event.Delta
		}
	}
	if strings.Join(toolStates, "|") != "running:goal_run_report goal_demo|done:goal_run_report goal_demo" {
		t.Fatalf("tool events = %#v", toolStates)
	}
	if text != resp.Content {
		t.Fatalf("streamed text = %q, want response content", text)
	}
}

func TestAssistantReplyReadOnlyRoutePassFallsBackToPlanner(t *testing.T) {
	t.Parallel()

	router := &fakeJSONSchemaRunner{
		resp: []llm.JSONSchemaResponse{{
			OutputText: encodedReadOnlyRoute(t, bossReadOnlyRoute{Kind: bossReadOnlyRoutePass, Reason: "The user is confirming a prior plan."}),
			Usage:      model.LLMUsage{TotalTokens: 5},
		}},
	}
	planner := &fakeJSONSchemaRunner{
		resp: []llm.JSONSchemaResponse{{
			OutputText: encodedBossAction(t, bossAction{Kind: bossActionAnswer, Answer: "Okay, moving on with the plan."}),
			Usage:      model.LLMUsage{TotalTokens: 13},
		}},
	}
	assistant := &Assistant{
		planner:     planner,
		queryRouter: router,
		query:       newQueryExecutor(&fakeBossStore{}),
		model:       "gpt-test",
	}

	resp, err := assistant.Reply(context.Background(), AssistantRequest{
		StateBrief: "Visible projects: 1.",
		Messages:   []ChatMessage{{Role: "user", Content: "Okay, let's do it"}},
	})
	if err != nil {
		t.Fatalf("Reply() error = %v", err)
	}
	if resp.Content != "Okay, moving on with the plan." {
		t.Fatalf("Content = %q", resp.Content)
	}
	if len(planner.reqs) != 1 {
		t.Fatalf("planner requests = %d, want one", len(planner.reqs))
	}
	if got, want := router.reqs[0].Model, "gpt-test"; got != want {
		t.Fatalf("router model = %q, want fallback to helm %q", got, want)
	}
	if resp.Usage.TotalTokens != 18 {
		t.Fatalf("usage total = %d, want router plus planner usage", resp.Usage.TotalTokens)
	}
}

func TestAssistantReplyRepairsPlainTextPlannerOutputAsFinalAnswer(t *testing.T) {
	t.Parallel()

	const answer = "Here's where things stand: the airplane view zoom and release branch checks are already in flight."
	router := &fakeJSONSchemaRunner{
		resp: []llm.JSONSchemaResponse{{
			OutputText: encodedReadOnlyRoute(t, bossReadOnlyRoute{Kind: bossReadOnlyRoutePass, Reason: "Needs helm judgment."}),
			Usage:      model.LLMUsage{TotalTokens: 5},
		}},
	}
	planner := &fakeJSONSchemaRunner{
		resp: []llm.JSONSchemaResponse{
			{
				OutputText: answer,
				Usage:      model.LLMUsage{TotalTokens: 13},
			},
			{
				OutputText: encodedBossAction(t, bossAction{Kind: bossActionAnswer, Answer: answer}),
				Usage:      model.LLMUsage{TotalTokens: 7},
			},
		},
	}
	assistant := &Assistant{
		planner:     planner,
		queryRouter: router,
		query:       newQueryExecutor(&fakeBossStore{}),
		model:       "gpt-test",
	}

	resp, err := assistant.Reply(context.Background(), AssistantRequest{
		StateBrief: "Visible projects: 3.",
		Messages:   []ChatMessage{{Role: "user", Content: "Which projects require my attention right now? which one would you work on?"}},
	})
	if err != nil {
		t.Fatalf("Reply() error = %v", err)
	}
	if resp.Content != answer {
		t.Fatalf("Content = %q, want plain planner answer", resp.Content)
	}
	if resp.Usage.TotalTokens != 25 {
		t.Fatalf("usage total = %d, want router plus planner and repair usage", resp.Usage.TotalTokens)
	}
	if len(planner.reqs) != 2 || planner.reqs[1].SchemaName != "boss_next_action" {
		t.Fatalf("planner requests = %#v, want original action plus structured repair", planner.reqs)
	}
	if !strings.Contains(planner.reqs[1].UserText, answer) || !strings.Contains(planner.reqs[1].SystemText, "Repair one invalid") {
		t.Fatalf("repair request should include the malformed output and repair instructions: %+v", planner.reqs[1])
	}
}

func TestAssistantReplyRepairsToolProtocolIntoHelpChatControlProposal(t *testing.T) {
	t.Parallel()

	const toolProtocol = `<tool_call>
<function=propose_control>
<parameter=control_capability>engineer.send_prompt</parameter>
<parameter=project_name>ChatNext3</parameter>
<parameter=session_mode>new</parameter>
<parameter=provider>auto</parameter>
<parameter=prompt>Fix Help Chat wrapping and use the full response width.</parameter>
</function>
</tool_call>`
	planner := &fakeJSONSchemaRunner{
		resp: []llm.JSONSchemaResponse{
			{OutputText: toolProtocol, Usage: model.LLMUsage{TotalTokens: 17}},
			{
				OutputText: encodedBossAction(t, bossAction{
					Kind:              bossActionProposeControl,
					ControlCapability: string(control.CapabilityEngineerSendPrompt),
					ProjectName:       "ChatNext3",
					EngineerProvider:  string(control.ProviderAuto),
					SessionMode:       string(control.SessionModeNew),
					Prompt:            "Fix Help Chat wrapping and use the full response width.",
				}),
				Usage: model.LLMUsage{TotalTokens: 9},
			},
		},
	}
	assistant := &Assistant{
		planner: planner,
		query:   newQueryExecutor(&fakeBossStore{}),
		model:   "mimo-v2.5-pro",
	}

	resp, err := assistant.Reply(context.Background(), AssistantRequest{
		HelpChat:   true,
		StateBrief: "Visible projects: ChatNext3.",
		Messages:   []ChatMessage{{Role: "user", Content: "Please fix Help Chat wrapping and response width."}},
	})
	if err != nil {
		t.Fatalf("Reply() error = %v", err)
	}
	if resp.ControlInvocation == nil {
		t.Fatalf("ControlInvocation = nil, want a confirmable engineer handoff")
	}
	if resp.ControlInvocation.Capability != control.CapabilityEngineerSendPrompt {
		t.Fatalf("capability = %q, want %q", resp.ControlInvocation.Capability, control.CapabilityEngineerSendPrompt)
	}
	if strings.Contains(resp.Content, "<tool_call>") || !strings.Contains(resp.Content, "Enter sends") {
		t.Fatalf("proposal content = %q, want a confirmation preview without leaked protocol", resp.Content)
	}
	if len(planner.reqs) != 2 ||
		!strings.Contains(planner.reqs[1].UserText, "<tool_call>") ||
		!strings.Contains(planner.reqs[1].UserText, "engineer.send_prompt") ||
		!strings.Contains(planner.reqs[1].UserText, "Fix Help Chat wrapping") {
		t.Fatalf("planner requests = %#v, want one structured repair containing the malformed protocol", planner.reqs)
	}
	if resp.Usage.TotalTokens != 26 {
		t.Fatalf("usage total = %d, want original plus repair usage", resp.Usage.TotalTokens)
	}
}

func TestAssistantReplyStillErrorsForMalformedPlannerJSON(t *testing.T) {
	t.Parallel()

	router := &fakeJSONSchemaRunner{
		resp: []llm.JSONSchemaResponse{{
			OutputText: encodedReadOnlyRoute(t, bossReadOnlyRoute{Kind: bossReadOnlyRoutePass}),
		}},
	}
	planner := &fakeJSONSchemaRunner{
		resp: []llm.JSONSchemaResponse{
			{OutputText: `{"kind":"answer","answer":`},
			{OutputText: `still not JSON`},
		},
	}
	assistant := &Assistant{
		planner:     planner,
		queryRouter: router,
		query:       newQueryExecutor(&fakeBossStore{}),
		model:       "gpt-test",
	}

	_, err := assistant.Reply(context.Background(), AssistantRequest{
		StateBrief: "Visible projects: 1.",
		Messages:   []ChatMessage{{Role: "user", Content: "What is up?"}},
	})
	if err == nil {
		t.Fatalf("Reply() error = nil, want malformed JSON error")
	}
	if !strings.Contains(err.Error(), "decode Help chat action") || !strings.Contains(err.Error(), "repair") {
		t.Fatalf("Reply() error = %q, want structured repair failure", err.Error())
	}
}

func TestAssistantReadOnlyRouteUsesUtilityModel(t *testing.T) {
	t.Parallel()

	router := &fakeJSONSchemaRunner{
		resp: []llm.JSONSchemaResponse{{
			OutputText: encodedReadOnlyRoute(t, bossReadOnlyRoute{Kind: bossReadOnlyRoutePass, Reason: "Routine routing."}),
			Usage:      model.LLMUsage{TotalTokens: 5},
		}},
	}
	planner := &fakeJSONSchemaRunner{
		resp: []llm.JSONSchemaResponse{{
			OutputText: encodedBossAction(t, bossAction{Kind: bossActionAnswer, Answer: "Done."}),
			Usage:      model.LLMUsage{TotalTokens: 13},
		}},
	}
	assistant := &Assistant{
		planner:      planner,
		queryRouter:  router,
		query:        newQueryExecutor(&fakeBossStore{}),
		model:        "gpt-5.5",
		utilityModel: "gpt-5.4-mini",
	}

	_, err := assistant.Reply(context.Background(), AssistantRequest{
		StateBrief: "Visible projects: 1.",
		Messages:   []ChatMessage{{Role: "user", Content: "What is up?"}},
	})
	if err != nil {
		t.Fatalf("Reply() error = %v", err)
	}
	if got, want := router.reqs[0].Model, "gpt-5.4-mini"; got != want {
		t.Fatalf("router model = %q, want utility model %q", got, want)
	}
	if got, want := planner.reqs[0].Model, "gpt-5.5"; got != want {
		t.Fatalf("planner model = %q, want helm model %q", got, want)
	}
}

func TestAssistantReadOnlyRouteErrorFallsBackToHelmPlanner(t *testing.T) {
	t.Parallel()

	router := &fakeJSONSchemaRunner{err: errors.New("utility model unavailable")}
	planner := &fakeJSONSchemaRunner{
		resp: []llm.JSONSchemaResponse{{
			OutputText: encodedBossAction(t, bossAction{Kind: bossActionAnswer, Answer: "The helm planner handled it."}),
			Usage:      model.LLMUsage{TotalTokens: 13},
		}},
	}
	assistant := &Assistant{
		planner:      planner,
		queryRouter:  router,
		query:        newQueryExecutor(&fakeBossStore{}),
		model:        "gpt-5.5",
		utilityModel: "gpt-5.4-mini",
	}

	resp, err := assistant.Reply(context.Background(), AssistantRequest{
		StateBrief: "Visible projects: 1.",
		Messages:   []ChatMessage{{Role: "user", Content: "What should I check now?"}},
	})
	if err != nil {
		t.Fatalf("Reply() error = %v", err)
	}
	if got, want := resp.Content, "The helm planner handled it."; got != want {
		t.Fatalf("Content = %q, want %q", got, want)
	}
	if got, want := router.reqs[0].Model, "gpt-5.4-mini"; got != want {
		t.Fatalf("router model = %q, want utility model %q", got, want)
	}
	if len(planner.reqs) != 1 {
		t.Fatalf("planner requests = %d, want one fallback request", len(planner.reqs))
	}
	if got, want := planner.reqs[0].Model, "gpt-5.5"; got != want {
		t.Fatalf("planner model = %q, want helm model %q", got, want)
	}
}

func TestAssistantStreamUsesPlannerAnswerAfterUtilityRoute(t *testing.T) {
	t.Parallel()

	router := &fakeJSONSchemaRunner{
		resp: []llm.JSONSchemaResponse{{
			Model:      "gpt-5.4-mini",
			OutputText: encodedReadOnlyRoute(t, bossReadOnlyRoute{Kind: bossReadOnlyRoutePass, Reason: "Needs helm judgment."}),
			Usage:      model.LLMUsage{TotalTokens: 5},
		}},
	}
	planner := &fakeJSONSchemaRunner{
		resp: []llm.JSONSchemaResponse{{
			Model:      "gpt-5.5",
			OutputText: encodedBossAction(t, bossAction{Kind: bossActionAnswer, Answer: "Planner draft."}),
			Usage:      model.LLMUsage{TotalTokens: 13},
		}},
	}
	runner := &fakeStreamingTextRunner{err: errors.New("final text runner should not be called")}
	assistant := &Assistant{
		runner:       runner,
		planner:      planner,
		queryRouter:  router,
		query:        newQueryExecutor(&fakeBossStore{}),
		model:        "gpt-5.5",
		utilityModel: "gpt-5.4-mini",
	}

	resp, err := assistant.ReplyStream(context.Background(), AssistantRequest{
		StateBrief: "Visible projects: 1.",
		Messages:   []ChatMessage{{Role: "user", Content: "What should I test now?"}},
	}, nil)
	if err != nil {
		t.Fatalf("ReplyStream() error = %v", err)
	}
	if got, want := router.reqs[0].Model, "gpt-5.4-mini"; got != want {
		t.Fatalf("router model = %q, want utility model %q", got, want)
	}
	if got, want := planner.reqs[0].Model, "gpt-5.5"; got != want {
		t.Fatalf("planner model = %q, want helm model %q", got, want)
	}
	if runner.req.Model != "" {
		t.Fatalf("final text runner was called with model %q", runner.req.Model)
	}
	if got, want := resp.Content, "Planner draft."; got != want {
		t.Fatalf("Content = %q, want %q", got, want)
	}
	if got, want := resp.Usage.TotalTokens, int64(18); got != want {
		t.Fatalf("usage total = %d, want router plus planner usage", got)
	}
}

func TestAssistantLabelShowsHelmAndUtilityModels(t *testing.T) {
	t.Parallel()

	assistant := &Assistant{
		runner:       &fakeTextRunner{},
		model:        "gpt-5.5",
		utilityModel: "gpt-5.4-mini",
	}

	if got, want := assistant.Label(), "(gpt-5.5/gpt-5.4-mini)"; got != want {
		t.Fatalf("Label() = %q, want %q", got, want)
	}
}

func TestAssistantReplyLimitsChatHistory(t *testing.T) {
	t.Parallel()

	runner := &fakeTextRunner{resp: llm.TextResponse{OutputText: "ok"}}
	assistant := &Assistant{runner: runner, model: "gpt-test"}
	var messages []ChatMessage
	for i := 0; i < 25; i++ {
		messages = append(messages, ChatMessage{Role: "user", Content: "message"})
	}
	if _, err := assistant.Reply(context.Background(), AssistantRequest{StateBrief: "state", Messages: messages}); err != nil {
		t.Fatalf("Reply() error = %v", err)
	}
	if len(runner.req.Messages) != 17 {
		t.Fatalf("messages len = %d, want state brief plus 16 history messages", len(runner.req.Messages))
	}
}

func TestBossPromptHistorySkipsFlowMessages(t *testing.T) {
	t.Parallel()

	req := AssistantRequest{
		StateBrief: "state",
		Messages: []ChatMessage{{
			Role:    "user",
			Content: "Keep the Alpha release topic.",
		}, {
			Role:    "assistant",
			Content: "Work on the noisy flow is ready for review.",
			Kind:    ChatMessageKindFlow,
		}, {
			Role:    "assistant",
			Content: "Alpha answer from Boss.",
		}},
	}
	directMessages := bossDirectMessages(req)
	var directText []string
	for _, message := range directMessages {
		directText = append(directText, message.Content)
	}
	for label, text := range map[string]string{
		"planner": strings.Join([]string{
			bossActionPlannerUserText(req, nil, false),
			bossReadOnlyRouterUserText(req),
		}, "\n"),
		"direct": strings.Join(directText, "\n"),
	} {
		if strings.Contains(text, "noisy flow") {
			t.Fatalf("%s prompt should not include flow transcript chatter:\n%s", label, text)
		}
		for _, want := range []string{"Keep the Alpha release topic.", "Alpha answer from Boss."} {
			if !strings.Contains(text, want) {
				t.Fatalf("%s prompt missing conversational message %q:\n%s", label, want, text)
			}
		}
	}
}

func TestBossPromptContextCompactsLongHistory(t *testing.T) {
	t.Parallel()

	runner := &fakeTextRunner{resp: llm.TextResponse{
		OutputText: "ok",
		Model:      "gpt-main",
		Usage:      model.LLMUsage{TotalTokens: 7},
	}}
	router := &fakeJSONSchemaRunner{resp: []llm.JSONSchemaResponse{{
		OutputText: `{"summary":"The user wants Help Chat context to stay compact while preserving the commit-routing discussion."}`,
		Model:      "gpt-utility",
		Usage:      model.LLMUsage{TotalTokens: 5},
	}}}
	assistant := &Assistant{
		runner:       runner,
		queryRouter:  router,
		model:        "gpt-main",
		utilityModel: "gpt-utility",
		dataDir:      t.TempDir(),
	}
	var messages []ChatMessage
	for i := 0; i < 25; i++ {
		messages = append(messages, ChatMessage{Role: "user", Content: fmt.Sprintf("chat turn %02d", i)})
	}

	resp, err := assistant.Reply(context.Background(), AssistantRequest{
		StateBrief: "state",
		Messages:   messages,
		SessionID:  "boss_test_context",
	})
	if err != nil {
		t.Fatalf("Reply() error = %v", err)
	}
	if resp.PromptContext.ContextMode != agentcontext.ContextModeCompacted {
		t.Fatalf("context mode = %q, want compacted", resp.PromptContext.ContextMode)
	}
	if resp.PromptContext.SummaryMessageCount != 13 {
		t.Fatalf("summary count = %d, want 13", resp.PromptContext.SummaryMessageCount)
	}
	if got, want := resp.Usage.TotalTokens, int64(12); got != want {
		t.Fatalf("usage total = %d, want compaction+answer %d", got, want)
	}
	if len(router.reqs) != 1 || router.reqs[0].SchemaName != "boss_chat_context_compaction" {
		t.Fatalf("router requests = %#v, want one compaction request", router.reqs)
	}
	joined := bossTextMessagesContent(runner.req.Messages)
	if !strings.Contains(joined, "Compacted prior Help Chat summary") ||
		!strings.Contains(joined, "commit-routing discussion") {
		t.Fatalf("direct prompt missing compacted summary:\n%s", joined)
	}
	if strings.Contains(joined, "chat turn 00") {
		t.Fatalf("direct prompt should not keep the earliest raw turn after compaction:\n%s", joined)
	}
	if !strings.Contains(joined, "chat turn 24") {
		t.Fatalf("direct prompt missing latest turn:\n%s", joined)
	}
	state, ok, err := agentcontext.LoadState[ChatMessage, bossContextNoToolCall](assistant.dataDir, bossContextNamespace, "boss_test_context", "", nil)
	if err != nil || !ok {
		t.Fatalf("load compacted state ok=%v err=%v", ok, err)
	}
	if state.ContextMode != agentcontext.ContextModeCompacted || state.SummaryCount != 13 {
		t.Fatalf("stored context state = mode %q summary %d", state.ContextMode, state.SummaryCount)
	}
}

func TestBossPromptsPreferCoworkerBriefAndSearchBeforeUnknown(t *testing.T) {
	t.Parallel()

	directPrompt := bossAssistantSystemPrompt()
	for _, want := range []string{
		"top-level Help Chat helper",
		"engineer threads",
		"ordinary coworker coordination",
		"Help Chat is the top-level conversation",
		"linked task/thread records",
		"Open agent tasks are delegated engineer work items",
		"one tracked task with its linked engineer thread",
		"separate from project TODOs",
		"Delegated agent tasks can be archived",
		"do not route that request to project TODO cleanup",
		"Scratch-task projects are project records with kind=scratch_task",
		"separate from both project TODOs and delegated agent tasks",
		"the AI assistant",
		"high-level coordinator",
		"Do not assign human names or personas to AI work sessions",
		"Do not explain a missing task detail as the engineer having no persistent memory",
		"Be proactive about finding facts",
		"Do not answer commit, deploy, release, migration, schema, storage, or API-shape safety questions from summaries alone",
		"Never say a deploy needs no DB migration unless direct evidence explicitly covers migrations, schema, storage, or the current diff",
		"Cached engineer transcripts and Help Chat recall are for context, not fresh evidence",
		"propose continuing that same task with the specific question",
		"ongoing coworker chat",
		"skip onboarding",
		"highest-level read first",
		"in-the-know coworker",
		"what we have working or learned",
		"what we still need to validate, decide, or watch",
		"Do not start with mapping phrases",
		"Treat codenames as shared coworker context",
		"Alias resolution is private routing",
		"do not say '<alias> is in <project/repo>'",
		"sharp but casual coworker update",
		"compact Markdown link label",
		"Use we/us naturally",
		"actively being worked",
		"Do not lead with 'X is actively being worked'",
		"latest session evidence",
		"Minimize redundant information",
		"repo hygiene, counts, scores, branches, freshness, or board stats only when they explain a real blocker or decision",
		"dirty working tree, ahead commits, and the current branch are normal background state",
		"Use reference metadata internally",
		"Prefer verbs from the evidence",
		"structured control action",
		"user must confirm",
		"Do not say agent work will be done",
		"Help Chat can propose opening the normal TUI commit preview through git.prepare_commit",
		"operator must still confirm in that dialog",
		"recommend one next move",
	} {
		if !strings.Contains(directPrompt, want) {
			t.Fatalf("assistant prompt missing %q:\n%s", want, directPrompt)
		}
	}

	plannerPrompt := bossActionPlannerSystemPrompt()
	for _, want := range []string{
		"Do not answer that a concrete term is unknown until search_context has been tried.",
		"high-level coordinator",
		"linked task/thread records",
		"do not imply a fresh unrelated session",
		"Do not explain a missing task detail as the engineer having no persistent memory",
		"Be proactive about finding facts",
		"routine coworker coordination",
		"Do not answer commit, deploy, release, migration, schema, storage, or API-shape safety questions from summaries alone",
		"Never say a deploy needs no DB migration unless direct evidence explicitly covers migrations, schema, storage, or the current diff",
		"fresh external research",
		"propose_control",
		"agent_task.create",
		"project.set_archive_state",
		"scratch_task.archive",
		"engineer.send_prompt control proposal sends to exactly one loaded project",
		"do not silently pick one and drop the rest",
		"agent_task_report",
		"Use agent_task_report when the user asks about open, active, completed, archived, historical, or delegated agent tasks",
		"include_historical=true on agent_task_report",
		"project TODOs are separate from delegated agent tasks",
		"Do not answer that there are no open agent tasks",
		"Use engineer.send_prompt only for explicit project/repo work",
		"git.prepare_commit",
		"Use git.prepare_commit for a simple commit or commit-and-push request",
		"operator still confirms Enter for commit or Alt+Enter for commit and push",
		"A git.prepare_commit control proposal opens exactly one loaded project preview",
		"do not silently pick one and drop the rest",
		"Do not use engineer.send_prompt merely to create a git commit",
		"todo.create_worktree_and_start_engineer",
		"idle engineer turn does not mean its existing task is finished",
		"Use todo.add only when the user explicitly asks",
		"Use tracked worktrees for unrelated new work",
		"Use session_mode=resume_or_new only when",
		"operator note meant to steer that work",
		"The host will steer the active Codex turn when possible",
		"Active work alone is not enough reason to resume",
		"Use agent_task.create for temporary delegated work",
		"Use project.set_archive_state when project metadata identifies an in-scope regular loaded project",
		"This control does not add out-of-scope projects back to scope.",
		"external web/product/market research",
		"do not encode special domains as task kinds",
		"Use scratch_task.archive when project metadata identifies kind=scratch_task",
		"task_close_status=archived",
		"Do not use waiting for cleanup/removal requests",
		"fresh read-only evidence resolves it with no remaining work",
		"A status or situation question is enough to close a review/waiting agent task",
		"treat that as a request to manage those agent tasks",
		"propose exactly one agent_task.continue",
		"remove multiple delegated agent tasks",
		"assents to a prior Help Chat plan",
		"Do not answer with only a priority order",
		"Do not use the Little Control Room project or another unrelated active engineer session as a proxy venue",
		"leave answer empty unless a short scope note is needed",
		"user confirmation",
		"context_command",
		"recall/context, not fresh research",
		"ctx search engineer",
		"current product, market, web, or source question",
		"ctx show",
		"ctx show agent_task",
		"output or result of an open agent task",
		"details of a visible review/waiting agent task",
		"propose agent_task.continue with a precise follow-up",
		`vague request like "please try again"`,
		"Use ctx search boss",
		"search_boss_sessions",
		"process_report",
		"skills_inventory",
		"help_reference",
		"Use help_reference when the user asks how to use Little Control Room",
		"suspicious PIDs",
		"XML-like boss_session and turn snippets",
		"after it finds one project path, inspect project_detail before answering",
		"live engineer work context",
		"concise coworker update",
		"turn tool output into judgment",
		"compact Markdown link label",
		"Do not describe UI mechanics",
		"lossless reframing",
		"fill intent_excerpt",
		"preserved_meaning",
		"success_condition",
		"source/metric mismatch",
		"meaningful result and what still needs attention",
		"recommend one next move",
		"answer the operational substance rather than reciting the lookup",
		"codenames and aliases as shared coworker context",
		"Alias resolution is private routing",
		"avoid phrasing like '<alias> is in <project>'",
		"what we have working or learned, plus what we still need to validate, decide, or watch",
		"Use we/us naturally",
		"Do not lead with 'X is actively being worked'",
		"Minimize redundant information",
		"Do not include mappings, paths, dirty/ahead state, branch names, ages, attention scores, confidence, queue, or classification telemetry unless it materially changes what the user should do.",
		"Treat repo hygiene as material only for conflicts",
		"Use reference metadata only to disambiguate targets and detect blockers",
		"Do not hedge a single clear match",
		"avoid capability pitches or optional menus",
		"Read-only query tools are report-only; control actions are proposals",
	} {
		if !strings.Contains(plannerPrompt, want) {
			t.Fatalf("planner prompt missing %q:\n%s", want, plannerPrompt)
		}
	}

	routerPrompt := bossReadOnlyRouterSystemPrompt()
	for _, want := range []string{
		"fast read-only query router",
		"Choose pass for requests to change state",
		"fresh/current external or web research",
		"Cached engineer snippets are not fresh evidence",
		"Use help_reference for questions about how to use Little Control Room",
		"Use goal_run_report when the user asks what LCR goal runs happened",
		"put only that id in query",
		"Do not answer the user",
	} {
		if !strings.Contains(routerPrompt, want) {
			t.Fatalf("read-only router prompt missing %q:\n%s", want, routerPrompt)
		}
	}
}

func TestHelpChatPromptsAvoidUnrequestedStatusReports(t *testing.T) {
	t.Parallel()

	req := AssistantRequest{HelpChat: true}
	for label, prompt := range map[string]string{
		"direct":  bossAssistantSystemPromptForRequest(req),
		"planner": bossActionPlannerSystemPromptForRequest(req),
		"router":  bossReadOnlyRouterSystemPromptForRequest(req),
	} {
		for _, want := range []string{
			"greetings",
			"casual",
			"status report",
		} {
			if !strings.Contains(prompt, want) {
				t.Fatalf("%s help prompt missing %q:\n%s", label, want, prompt)
			}
		}
	}

	plannerPrompt := bossActionPlannerSystemPromptForRequest(req)
	for _, want := range []string{
		`choose kind="answer" immediately`,
		"Never answer a casual turn with a snapshot",
		"Do not turn casual input into a project, task, queue, process, or attention report.",
		"current Help Chat transcript",
		"Do not claim to have searched files, the web, or all previous conversations",
	} {
		if !strings.Contains(plannerPrompt, want) {
			t.Fatalf("planner help prompt missing casual answer guard %q:\n%s", want, plannerPrompt)
		}
	}
	directPrompt := bossAssistantSystemPromptForRequest(req)
	if !strings.Contains(directPrompt, "When asked how you know a personal detail") {
		t.Fatalf("direct help prompt missing personal context boundary:\n%s", directPrompt)
	}
}

func TestHelpChatPromptsCoordinateWorkWithoutAgentNames(t *testing.T) {
	t.Parallel()

	prompt := bossAssistantSystemPromptForRequest(AssistantRequest{HelpChat: true})
	for _, want := range []string{
		"decide what deserves attention across coding projects",
		"Do not assign human names or personas to AI work sessions",
		"Work on X is underway",
	} {
		if !strings.Contains(prompt, want) {
			t.Fatalf("Help Chat prompt missing %q:\n%s", want, prompt)
		}
	}
	for _, unwanted := range []string{"Use those names", "Ada or Grace", "visible named engineer"} {
		if strings.Contains(prompt, unwanted) {
			t.Fatalf("Help Chat prompt retained agent-name guidance %q:\n%s", unwanted, prompt)
		}
	}
}

func TestHelpChatCasualRouterAnswerSkipsPlanner(t *testing.T) {
	t.Parallel()

	router := &fakeJSONSchemaRunner{
		resp: []llm.JSONSchemaResponse{{
			Model:      "utility-test",
			OutputText: encodedReadOnlyRoute(t, bossReadOnlyRoute{Kind: bossActionAnswer, Answer: "Hey! What can I help with?"}),
			Usage:      model.LLMUsage{TotalTokens: 3},
		}},
	}
	planner := &fakeJSONSchemaRunner{}
	assistant := &Assistant{
		planner:     planner,
		queryRouter: router,
		query:       newQueryExecutor(&fakeBossStore{}),
		model:       "gpt-test",
	}

	resp, err := assistant.Reply(context.Background(), AssistantRequest{
		HelpChat: true,
		Messages: []ChatMessage{{
			Role:    "user",
			Content: "hey",
		}},
	})
	if err != nil {
		t.Fatalf("Reply() error = %v", err)
	}
	if resp.Content != "Hey! What can I help with?" {
		t.Fatalf("content = %q, want casual router answer", resp.Content)
	}
	if resp.Model != "utility-test" {
		t.Fatalf("model = %q, want utility router model", resp.Model)
	}
	if got := len(router.reqs); got != 1 {
		t.Fatalf("router calls = %d, want 1", got)
	}
	if got := len(planner.reqs); got != 0 {
		t.Fatalf("planner calls = %d, want none", got)
	}
	if got, want := resp.Usage.TotalTokens, int64(3); got != want {
		t.Fatalf("usage total = %d, want %d", got, want)
	}
}

func TestHelpChatFastAnswerUsesRouterBeforePlanner(t *testing.T) {
	t.Parallel()

	router := &fakeJSONSchemaRunner{
		resp: []llm.JSONSchemaResponse{{
			Model:      "utility-test",
			OutputText: encodedReadOnlyRoute(t, bossReadOnlyRoute{Kind: bossActionAnswer, Answer: "Hey."}),
			Usage:      model.LLMUsage{InputTokens: 7, OutputTokens: 2, TotalTokens: 9},
		}},
	}
	assistant := &Assistant{
		queryRouter: router,
		model:       "gpt-test",
	}

	resp, handled, err := assistant.tryHelpChatFastAnswer(context.Background(), AssistantRequest{
		HelpChat: true,
		Messages: []ChatMessage{{
			Role:    "user",
			Content: "hey",
		}},
	}, nil)
	if err != nil {
		t.Fatalf("tryHelpChatFastAnswer() error = %v", err)
	}
	if !handled {
		t.Fatalf("tryHelpChatFastAnswer() handled = false, want true")
	}
	if resp.Content != "Hey." {
		t.Fatalf("content = %q, want router answer", resp.Content)
	}
	if resp.Model != "utility-test" {
		t.Fatalf("model = %q, want utility model", resp.Model)
	}
	if got := len(router.reqs); got != 1 {
		t.Fatalf("router calls = %d, want 1", got)
	}
	if got, want := resp.Usage.TotalTokens, int64(9); got != want {
		t.Fatalf("usage total = %d, want %d", got, want)
	}
}

func TestAssistantPlannerUserTextSteersFreshExternalResearchToEngineer(t *testing.T) {
	t.Parallel()

	req := AssistantRequest{
		StateBrief: "Open delegated agent tasks (separate from project TODOs):\n- Research Garmin 3 used watch status (agt_garmin); kind/status: agent/review; show: agent_task:agt_garmin",
		Messages: []ChatMessage{
			{Role: "assistant", Content: "The older notes say the used-market picture was unclear."},
			{Role: "user", Content: "Can you ask the engineer to check the current used status for the Garmin 3 watch again?"},
		},
	}
	normal := bossActionPlannerUserText(req, []bossToolResult{{
		Name: bossActionContextCommand,
		Text: "Task output: Prior notes mention used status but did not run a fresh web search today.",
	}}, false)
	forced := bossActionPlannerUserText(req, []bossToolResult{{
		Name: bossActionContextCommand,
		Text: "Task output: Prior notes mention used status but did not run a fresh web search today.",
	}}, true)
	for _, got := range []string{normal, forced} {
		for _, want := range []string{
			"fresh/current external web, product, market, or source research",
			"needs an engineer to newly search",
			`control_capability="agent_task.continue"`,
			`control_capability="agent_task.create"`,
			"cached transcript snippets are not enough",
		} {
			if !strings.Contains(got, want) {
				t.Fatalf("planner user text missing fresh-research handoff guidance %q:\n%s", want, got)
			}
		}
	}
}

func TestAssistantPlannerUserTextAllowsClosingResolvedReviewTasks(t *testing.T) {
	t.Parallel()

	req := AssistantRequest{
		StateBrief: "Open delegated agent tasks (separate from project TODOs):\n- Diff duplicate Codex skills (agt_diff); kind/status: agent/review; show: agent_task:agt_diff",
		Messages:   []ChatMessage{{Role: "user", Content: "what's the situation with the skills?"}},
	}
	normal := bossActionPlannerUserText(req, nil, false)
	forced := bossActionPlannerUserText(req, []bossToolResult{{
		Name: bossActionSkillsInventory,
		Text: "Codex skills inventory: clean. No duplicate or metadata issues.",
	}}, true)
	for _, got := range []string{normal, forced} {
		if !strings.Contains(got, "resolves a visible review/waiting agent task") ||
			!strings.Contains(got, `control_capability="agent_task.close"`) {
			t.Fatalf("planner user text should steer resolved review tasks toward close proposals:\n%s", got)
		}
	}
}

func TestAssistantPlannerUserTextSteersDelegatedTaskRemovalToArchive(t *testing.T) {
	t.Parallel()

	req := AssistantRequest{
		StateBrief: "Open delegated agent tasks: none visible.",
		Messages:   []ChatMessage{{Role: "user", Content: "remove the task regarding the Hex accessibility issue"}},
	}
	normal := bossActionPlannerUserText(req, nil, false)
	for _, want := range []string{
		"agent_task_report with include_historical=true",
		"manage/continue/solve/archive/remove an agent task",
		`task_close_status="archived"`,
	} {
		if !strings.Contains(normal, want) {
			t.Fatalf("planner user text missing %q:\n%s", want, normal)
		}
	}

	forced := bossActionPlannerUserText(req, []bossToolResult{{
		Name: bossActionAgentTaskReport,
		Text: "Agent task report: 1 delegated agent task.\n- Hex accessibility issue (agt_hex); kind/status: agent/done",
	}}, true)
	for _, want := range []string{
		`control_capability="agent_task.close"`,
		`task_close_status="archived"`,
	} {
		if !strings.Contains(forced, want) {
			t.Fatalf("forced planner user text missing %q:\n%s", want, forced)
		}
	}
}

func TestAssistantPlannerUserTextSteersOpenTaskCleanupToArchive(t *testing.T) {
	t.Parallel()

	req := AssistantRequest{
		StateBrief: "Open delegated agent tasks (separate from project TODOs):\n- Investigate and reduce CPU usage (agt_cpu); kind/status: agent/review; show: agent_task:agt_cpu",
		Messages:   []ChatMessage{{Role: "user", Content: "let's close those about CPU usage"}},
	}
	normal := bossActionPlannerUserText(req, nil, false)
	for _, want := range []string{
		"any delegated task the user wants gone from the active record",
		"multiple tasks the user wants removed",
		`task_close_status="archived"`,
	} {
		if !strings.Contains(normal, want) {
			t.Fatalf("planner user text missing %q:\n%s", want, normal)
		}
	}

	forced := bossActionPlannerUserText(req, []bossToolResult{{
		Name: bossActionAgentTaskReport,
		Text: "Agent task report: 1 open delegated agent task.\n- Investigate and reduce CPU usage (agt_cpu); kind/status: agent/review",
	}}, true)
	for _, want := range []string{
		`control_capability="agent_task.close"`,
		`task_close_status="archived"`,
		`do not use task_close_status="waiting" for cleanup`,
	} {
		if !strings.Contains(forced, want) {
			t.Fatalf("forced planner user text missing %q:\n%s", want, forced)
		}
	}
}

func TestAssistantPlannerUserTextSteersMultiProjectHandoffScope(t *testing.T) {
	t.Parallel()

	req := AssistantRequest{
		StateBrief: "Hot projects:\n- talk-alpha; latest work: needs source verification.\n- talk-beta; latest work: needs source verification.",
		Messages:   []ChatMessage{{Role: "user", Content: "ask the two talk projects to check whether their source assumptions still hold"}},
	}
	for _, got := range []string{
		bossActionPlannerUserText(req, nil, false),
		bossActionPlannerUserText(req, nil, true),
	} {
		for _, want := range []string{
			"do not silently collapse the request to one target",
			"Help Chat can prepare one handoff at a time",
			"scope note naming the remaining targets",
		} {
			if !strings.Contains(got, want) {
				t.Fatalf("planner user text missing %q:\n%s", want, got)
			}
		}
	}
}

func TestAssistantPlannerUserTextSteersScratchTaskRemovalToArchive(t *testing.T) {
	t.Parallel()

	req := AssistantRequest{
		StateBrief: "Hot projects:\n- Hex accessibility issue; latest work: Accessibility check completed.",
		Messages:   []ChatMessage{{Role: "user", Content: "remove the Hex accessibility task"}},
	}
	normal := bossActionPlannerUserText(req, nil, false)
	for _, want := range []string{
		"choose project_detail before answering",
		"archive/remove a scratch task whose project metadata says kind=scratch_task",
		`control_capability="scratch_task.archive"`,
	} {
		if !strings.Contains(normal, want) {
			t.Fatalf("planner user text missing %q:\n%s", want, normal)
		}
	}

	forced := bossActionPlannerUserText(req, []bossToolResult{{
		Name: bossActionProjectDetail,
		Text: "Reference metadata (use only for disambiguation/blockers):\n- name=Hex accessibility issue; kind=scratch_task; path=/tmp/tasks/hex",
	}}, true)
	if !strings.Contains(forced, `control_capability="scratch_task.archive"`) {
		t.Fatalf("forced planner user text should allow scratch task archive:\n%s", forced)
	}
}

func TestAssistantPlannerUserTextRequiresFreshDeployEvidence(t *testing.T) {
	t.Parallel()

	req := AssistantRequest{
		StateBrief: "Visible projects: 1.\n- ChatNext3: latest summary says image preview was verified.",
		Messages: []ChatMessage{
			{Role: "user", Content: "what's the situation with chatnext3?"},
			{Role: "assistant", Content: "The preview path is verified."},
			{Role: "user", Content: "if we deploy, do we need to think about db migration?"},
		},
	}
	normal := bossActionPlannerUserText(req, nil, false)
	forced := bossActionPlannerUserText(req, []bossToolResult{{
		Name: bossActionProjectDetail,
		Text: "Project detail: latest summary says image preview was verified. No diff inspection is included.",
	}}, true)
	for _, got := range []string{normal, forced} {
		for _, want := range []string{
			"do not answer from summaries",
			"current-diff evidence",
			`control_capability="todo.create_worktree_and_start_engineer"`,
		} {
			if !strings.Contains(got, want) {
				t.Fatalf("planner user text missing %q:\n%s", want, got)
			}
		}
	}
}

func TestAssistantPlannerUserTextSteersMissingTaskDetailToSameTask(t *testing.T) {
	t.Parallel()

	req := AssistantRequest{
		StateBrief: "Open delegated agent tasks (separate from project TODOs):\n- Diff duplicate Codex skills (agt_diff); kind/status: agent/review; show: agent_task:agt_diff",
		Messages:   []ChatMessage{{Role: "user", Content: "I was curious to know what were the differences"}},
	}
	normal := bossActionPlannerUserText(req, []bossToolResult{{
		Name: bossActionContextCommand,
		Text: "Task output: Current state: there are no longer two live imagegen copies. No line-by-line diff is included.",
	}}, false)
	forced := bossActionPlannerUserText(req, []bossToolResult{{
		Name: bossActionContextCommand,
		Text: "Task output: Current state: there are no longer two live imagegen copies. No line-by-line diff is included.",
	}}, true)
	for _, got := range []string{normal, forced} {
		if !strings.Contains(got, "lacks the detail the user asked for") ||
			!strings.Contains(got, `control_capability="agent_task.continue"`) ||
			!strings.Contains(got, "same task") {
			t.Fatalf("planner user text should steer missing task details back to the same task:\n%s", got)
		}
	}
}

func TestAssistantReplyUsesStructuredToolLoop(t *testing.T) {
	t.Parallel()

	store := &fakeBossStore{
		projects: []model.ProjectSummary{{
			Path:           "/tmp/alpha",
			Name:           "Alpha",
			Status:         model.StatusPossiblyStuck,
			AttentionScore: 44,
			OpenTODOCount:  1,
		}},
		details: map[string]model.ProjectDetail{
			"/tmp/alpha": {
				Summary: model.ProjectSummary{
					Path:           "/tmp/alpha",
					Name:           "Alpha",
					Status:         model.StatusPossiblyStuck,
					AttentionScore: 44,
					OpenTODOCount:  1,
				},
				Reasons: []model.AttentionReason{{Text: "Latest session is waiting for the user", Weight: 15}},
				Todos:   []model.TodoItem{{ID: 7, ProjectPath: "/tmp/alpha", Text: "Decide rollout shape"}},
			},
		},
	}
	planner := &fakeJSONSchemaRunner{
		resp: []llm.JSONSchemaResponse{
			{
				Model:      "gpt-test",
				OutputText: encodedBossAction(t, bossAction{Kind: bossActionProjectDetail, ProjectPath: "/tmp/alpha", Limit: 8, Reason: "Need Alpha detail"}),
				Usage:      model.LLMUsage{InputTokens: 10, OutputTokens: 3, TotalTokens: 13},
			},
			{
				Model:      "gpt-test",
				OutputText: encodedBossAction(t, bossAction{Kind: bossActionAnswer, Answer: "Focus Alpha first; it is waiting on your rollout decision."}),
				Usage:      model.LLMUsage{InputTokens: 20, OutputTokens: 5, TotalTokens: 25},
			},
		},
	}
	assistant := &Assistant{
		planner: planner,
		query:   newQueryExecutor(store),
		model:   "gpt-test",
	}

	resp, err := assistant.Reply(context.Background(), AssistantRequest{
		StateBrief: "Visible projects: 1.",
		View:       ViewContext{Active: true},
		Messages:   []ChatMessage{{Role: "user", Content: "What about /tmp/alpha?"}},
	})
	if err != nil {
		t.Fatalf("Reply() error = %v", err)
	}
	if resp.Content != "Focus Alpha first; it is waiting on your rollout decision." {
		t.Fatalf("Content = %q", resp.Content)
	}
	if resp.Usage.TotalTokens != 38 {
		t.Fatalf("total usage = %d, want 38", resp.Usage.TotalTokens)
	}
	if len(planner.reqs) != 2 {
		t.Fatalf("structured calls = %d, want 2", len(planner.reqs))
	}
	if !strings.Contains(planner.reqs[0].UserText, "Current TUI view") {
		t.Fatalf("first planner request missing TUI context:\n%s", planner.reqs[0].UserText)
	}
	if !strings.Contains(planner.reqs[1].UserText, "[project_detail]") || !strings.Contains(planner.reqs[1].UserText, "Decide rollout shape") {
		t.Fatalf("second planner request missing tool result:\n%s", planner.reqs[1].UserText)
	}
}

func TestAssistantReplyCanReportAgentTasks(t *testing.T) {
	t.Parallel()

	now := time.Unix(1_800_000_000, 0)
	store := &fakeBossStore{
		agentTasks: []model.AgentTask{{
			ID:            "agt_cursor",
			Title:         "Revoke Cursor GitHub access",
			Kind:          model.AgentTaskKindAgent,
			Status:        model.AgentTaskStatusActive,
			Summary:       "Cursor OAuth removal is waiting on the browser-side GitHub settings check.",
			Capabilities:  []string{"browser.inspect"},
			Provider:      model.SessionSourceCodex,
			SessionID:     "019deb93",
			LastTouchedAt: now.Add(-5 * time.Minute),
		}},
	}
	query := newQueryExecutor(store)
	query.nowFn = func() time.Time { return now }
	planner := &fakeJSONSchemaRunner{
		resp: []llm.JSONSchemaResponse{
			{OutputText: encodedBossAction(t, bossAction{Kind: bossActionAgentTaskReport, Limit: 8})},
			{OutputText: encodedBossAction(t, bossAction{Kind: bossActionAnswer, Answer: "We have the Cursor/GitHub delegated agent task open; it is waiting on the browser-side settings check."})},
		},
	}
	assistant := &Assistant{
		planner: planner,
		query:   query,
		model:   "gpt-test",
	}

	resp, err := assistant.Reply(context.Background(), AssistantRequest{
		StateBrief: "Open delegated agent tasks (separate from project TODOs):\n- Revoke Cursor GitHub access (agt_cursor)",
		Messages:   []ChatMessage{{Role: "user", Content: "what about the agents?"}},
	})
	if err != nil {
		t.Fatalf("Reply() error = %v", err)
	}
	if !strings.Contains(resp.Content, "Cursor/GitHub delegated agent task") {
		t.Fatalf("Content = %q", resp.Content)
	}
	if len(planner.reqs) != 2 {
		t.Fatalf("structured calls = %d, want 2", len(planner.reqs))
	}
	second := planner.reqs[1].UserText
	for _, want := range []string{
		"[agent_task_report]",
		"Agent task report: 1 open delegated agent task.",
		"Revoke Cursor GitHub access",
		"show: agent_task:agt_cursor",
	} {
		if !strings.Contains(second, want) {
			t.Fatalf("second planner request missing %q:\n%s", want, second)
		}
	}
}

func TestAssistantReplyCanProposeEngineerSendPromptControl(t *testing.T) {
	t.Parallel()

	planner := &fakeJSONSchemaRunner{
		resp: []llm.JSONSchemaResponse{{
			Model: "gpt-test",
			OutputText: encodedBossAction(t, bossAction{
				Kind:              bossActionProposeControl,
				ControlCapability: "engineer.send_prompt",
				ProjectPath:       "/tmp/alpha",
				EngineerProvider:  "opencode",
				SessionMode:       "new",
				Prompt:            "Please fix the failing tests and report what changed.",
				Reveal:            false,
				Reason:            "The user asked to delegate the fix.",
			}),
			Usage: model.LLMUsage{TotalTokens: 17},
		}},
	}
	assistant := &Assistant{
		planner: planner,
		query:   newQueryExecutor(&fakeBossStore{}),
		model:   "gpt-test",
	}

	resp, err := assistant.Reply(context.Background(), AssistantRequest{
		StateBrief: "Visible projects: 1.",
		HelpChat:   true,
		Messages:   []ChatMessage{{Role: "user", Content: "Tell OpenCode to fix Alpha's tests"}},
	})
	if err != nil {
		t.Fatalf("Reply() error = %v", err)
	}
	if resp.ControlInvocation == nil {
		t.Fatalf("ControlInvocation = nil, want engineer.send_prompt proposal")
	}
	if resp.ControlInvocation.Capability != "engineer.send_prompt" {
		t.Fatalf("capability = %q", resp.ControlInvocation.Capability)
	}
	if !strings.Contains(resp.Content, "Send this to OpenCode") ||
		!strings.Contains(resp.Content, "start a fresh session") ||
		!strings.Contains(resp.Content, "Enter sends") {
		t.Fatalf("proposal content = %q, want confirmation preview", resp.Content)
	}
	var input control.EngineerSendPromptInput
	if err := json.Unmarshal(resp.ControlInvocation.Args, &input); err != nil {
		t.Fatalf("decode invocation args: %v", err)
	}
	if input.Provider != control.ProviderOpenCode ||
		input.SessionMode != control.SessionModeNew ||
		!strings.Contains(input.Prompt, "Help Chat lossless task packet:") ||
		!strings.Contains(input.Prompt, "Tell OpenCode to fix Alpha's tests") ||
		!strings.Contains(input.Prompt, "Please fix the failing tests and report what changed.") {
		t.Fatalf("invocation args = %s", resp.ControlInvocation.Args)
	}
	if resp.Usage.TotalTokens != 17 {
		t.Fatalf("usage total = %d, want 17", resp.Usage.TotalTokens)
	}
}

func TestAssistantReplyCanProposeGitPrepareCommitControl(t *testing.T) {
	t.Parallel()

	planner := &fakeJSONSchemaRunner{
		resp: []llm.JSONSchemaResponse{{
			Model: "gpt-test",
			OutputText: encodedBossAction(t, bossAction{
				Kind:              bossActionProposeControl,
				ControlCapability: "git.prepare_commit",
				ProjectPath:       "/tmp/talk",
				ProjectName:       "talk_gamedev_lessons",
				CommitMessage:     "Publish talk cleanup",
				PushAfterCommit:   true,
				Reason:            "The user asked for a normal commit-and-push on the loaded talk project.",
			}),
			Usage: model.LLMUsage{TotalTokens: 16},
		}},
	}
	assistant := &Assistant{
		planner: planner,
		query:   newQueryExecutor(&fakeBossStore{}),
		model:   "gpt-test",
	}

	resp, err := assistant.Reply(context.Background(), AssistantRequest{
		StateBrief: "Visible projects: 1.\n- talk_gamedev_lessons at /tmp/talk",
		Messages:   []ChatMessage{{Role: "user", Content: "commit and push the talk gamedev lessons work"}},
	})
	if err != nil {
		t.Fatalf("Reply() error = %v", err)
	}
	if resp.ControlInvocation == nil {
		t.Fatalf("ControlInvocation = nil, want git.prepare_commit proposal")
	}
	if resp.ControlInvocation.Capability != control.CapabilityGitPrepareCommit {
		t.Fatalf("capability = %q", resp.ControlInvocation.Capability)
	}
	if !strings.Contains(resp.Content, "Open the commit & push preview for talk_gamedev_lessons?") ||
		!strings.Contains(resp.Content, "Message seed: Publish talk cleanup") ||
		!strings.Contains(resp.Content, "normal commit dialog") ||
		!strings.Contains(resp.Content, "Alt+Enter") ||
		!strings.Contains(resp.Content, "Enter opens") {
		t.Fatalf("proposal content = %q, want commit-preview confirmation", resp.Content)
	}
	var input control.GitPrepareCommitInput
	if err := json.Unmarshal(resp.ControlInvocation.Args, &input); err != nil {
		t.Fatalf("decode invocation args: %v", err)
	}
	if input.ProjectPath != "/tmp/talk" ||
		input.ProjectName != "talk_gamedev_lessons" ||
		input.Message != "Publish talk cleanup" ||
		!input.PushAfterCommit {
		t.Fatalf("invocation args = %#v", input)
	}
}

func TestAssistantReplyIncludesControlProposalScopeNote(t *testing.T) {
	t.Parallel()

	planner := &fakeJSONSchemaRunner{
		resp: []llm.JSONSchemaResponse{{
			Model: "gpt-test",
			OutputText: encodedBossAction(t, bossAction{
				Kind:              bossActionProposeControl,
				Answer:            "This prepares the Alpha handoff only; Beta still needs its own project handoff after this confirmation.",
				ControlCapability: "engineer.send_prompt",
				ProjectPath:       "/tmp/alpha",
				ProjectName:       "Alpha",
				EngineerProvider:  "auto",
				SessionMode:       "new",
				Prompt:            "Check whether Alpha's source assumptions still hold and report what needs follow-up.",
				Reason:            "The user asked for work across two projects; this proposal covers the first target explicitly.",
			}),
		}},
	}
	assistant := &Assistant{
		planner: planner,
		query:   newQueryExecutor(&fakeBossStore{}),
		model:   "gpt-test",
	}

	resp, err := assistant.Reply(context.Background(), AssistantRequest{
		StateBrief: "Hot projects:\n- Alpha\n- Beta",
		Messages:   []ChatMessage{{Role: "user", Content: "ask Alpha and Beta to check their assumptions"}},
	})
	if err != nil {
		t.Fatalf("Reply() error = %v", err)
	}
	if resp.ControlInvocation == nil {
		t.Fatalf("ControlInvocation = nil, want engineer.send_prompt proposal")
	}
	if !strings.HasPrefix(resp.Content, "This prepares the Alpha handoff only; Beta still needs its own project handoff") ||
		!strings.Contains(resp.Content, "Send this to the preferred engineer session for Alpha.") ||
		!strings.Contains(resp.Content, "Enter sends") {
		t.Fatalf("proposal content = %q, want scope note plus confirmation", resp.Content)
	}
}

func TestAssistantReplyCanProposeTodoAddControl(t *testing.T) {
	t.Parallel()

	planner := &fakeJSONSchemaRunner{
		resp: []llm.JSONSchemaResponse{{
			Model: "gpt-test",
			OutputText: encodedBossAction(t, bossAction{
				Kind:              bossActionProposeControl,
				ControlCapability: "todo.add",
				ProjectPath:       "/tmp/alpha",
				ProjectName:       "Alpha",
				TodoText:          "Add a Boss Desk TODO section for pending project backlog items.",
				Reason:            "The user asked to enqueue the work instead of starting it now.",
			}),
			Usage: model.LLMUsage{TotalTokens: 18},
		}},
	}
	assistant := &Assistant{
		planner: planner,
		query:   newQueryExecutor(&fakeBossStore{}),
		model:   "gpt-test",
	}

	resp, err := assistant.Reply(context.Background(), AssistantRequest{
		StateBrief: "Visible projects: 1.\nOpen project TODOs: none visible.",
		Messages:   []ChatMessage{{Role: "user", Content: "enqueue this for Alpha: add a Boss Desk TODO section"}},
	})
	if err != nil {
		t.Fatalf("Reply() error = %v", err)
	}
	if resp.ControlInvocation == nil {
		t.Fatalf("ControlInvocation = nil, want todo.add proposal")
	}
	if resp.ControlInvocation.Capability != control.CapabilityTodoAdd {
		t.Fatalf("capability = %q", resp.ControlInvocation.Capability)
	}
	if !strings.Contains(resp.Content, "Add this TODO to Alpha?") ||
		!strings.Contains(resp.Content, "Boss Desk TODO section") ||
		!strings.Contains(resp.Content, "Enter confirms") {
		t.Fatalf("proposal content = %q, want TODO confirmation preview", resp.Content)
	}
	var input control.TodoAddInput
	if err := json.Unmarshal(resp.ControlInvocation.Args, &input); err != nil {
		t.Fatalf("decode invocation args: %v", err)
	}
	if input.ProjectPath != "/tmp/alpha" || input.ProjectName != "Alpha" ||
		input.Text != "Add a Boss Desk TODO section for pending project backlog items." {
		t.Fatalf("invocation args = %#v", input)
	}
}

func TestAssistantTodoAddPolicyConvertsImplicitBacklogToFreshEngineerHandoff(t *testing.T) {
	t.Parallel()

	router := &fakeJSONSchemaRunner{
		resp: []llm.JSONSchemaResponse{
			{OutputText: encodedReadOnlyRoute(t, bossReadOnlyRoute{Kind: bossReadOnlyRoutePass}), Usage: model.LLMUsage{TotalTokens: 3}},
			{OutputText: encodedTodoAddPolicyReview(t, bossTodoAddPolicyReview{
				AllowTodoAdd:                 false,
				ReplacementControlCapability: string(control.CapabilityTodoCreateWorktreeAndStartEngineer),
				Reason:                       "The user asked for project work now; it should use a tracked worktree.",
			}), Usage: model.LLMUsage{TotalTokens: 5}},
		},
	}
	planner := &fakeJSONSchemaRunner{
		resp: []llm.JSONSchemaResponse{{
			Model: "gpt-test",
			OutputText: encodedBossAction(t, bossAction{
				Kind:              bossActionProposeControl,
				ControlCapability: "todo.add",
				ProjectPath:       "/tmp/lcr",
				ProjectName:       "Little Control Room",
				TodoText:          "Change Help Chat so project change requests start a fresh engineer handoff instead of becoming TODOs.",
				Reason:            "The project has an open engineer session.",
			}),
			Usage: model.LLMUsage{TotalTokens: 17},
		}},
	}
	assistant := &Assistant{
		planner:      planner,
		queryRouter:  router,
		query:        newQueryExecutor(&fakeBossStore{}),
		model:        "gpt-test",
		utilityModel: "gpt-utility",
	}

	resp, err := assistant.Reply(context.Background(), AssistantRequest{
		StateBrief: "Visible projects: 1.\nCurrent TUI view:\n- classic TUI status: Embedded Codex session is already open in the background.",
		Messages:   []ChatMessage{{Role: "user", Content: "change Little Control Room so Help chat does not turn project change requests into TODOs just because a session is open"}},
	})
	if err != nil {
		t.Fatalf("Reply() error = %v", err)
	}
	if resp.ControlInvocation == nil {
		t.Fatalf("ControlInvocation = nil, want tracked worktree proposal")
	}
	if resp.ControlInvocation.Capability != control.CapabilityTodoCreateWorktreeAndStartEngineer {
		t.Fatalf("capability = %q, want tracked worktree launch", resp.ControlInvocation.Capability)
	}
	var input control.TodoCreateWorktreeAndStartEngineerInput
	if err := json.Unmarshal(resp.ControlInvocation.Args, &input); err != nil {
		t.Fatalf("decode invocation args: %v", err)
	}
	if input.ProjectPath != "/tmp/lcr" ||
		input.TodoText == "" ||
		!strings.Contains(input.Prompt, "project change requests start a fresh engineer handoff") {
		t.Fatalf("invocation args = %#v", input)
	}
	if !strings.Contains(resp.Content, "dedicated worktree") || !strings.Contains(resp.Content, "q adds the TODO") {
		t.Fatalf("proposal content = %q, want tracked-worktree confirmation", resp.Content)
	}
	if len(router.reqs) != 2 {
		t.Fatalf("router requests = %d, want route plus todo policy review", len(router.reqs))
	}
	if router.reqs[1].SchemaName != "boss_todo_add_policy_review" || router.reqs[1].Model != "gpt-utility" {
		t.Fatalf("policy review request = %+v", router.reqs[1])
	}
	if resp.Usage.TotalTokens != 25 {
		t.Fatalf("usage total = %d, want route+planner+policy usage", resp.Usage.TotalTokens)
	}
}

func TestAssistantTodoAddPolicyAllowsExplicitBacklog(t *testing.T) {
	t.Parallel()

	router := &fakeJSONSchemaRunner{
		resp: []llm.JSONSchemaResponse{
			{OutputText: encodedReadOnlyRoute(t, bossReadOnlyRoute{Kind: bossReadOnlyRoutePass})},
			{OutputText: encodedTodoAddPolicyReview(t, bossTodoAddPolicyReview{
				AllowTodoAdd: true,
				Reason:       "The user explicitly asked to enqueue the work.",
			})},
		},
	}
	planner := &fakeJSONSchemaRunner{
		resp: []llm.JSONSchemaResponse{{
			Model: "gpt-test",
			OutputText: encodedBossAction(t, bossAction{
				Kind:              bossActionProposeControl,
				ControlCapability: "todo.add",
				ProjectPath:       "/tmp/alpha",
				ProjectName:       "Alpha",
				TodoText:          "Add a Boss Desk TODO section.",
			}),
		}},
	}
	assistant := &Assistant{
		planner:     planner,
		queryRouter: router,
		query:       newQueryExecutor(&fakeBossStore{}),
		model:       "gpt-test",
	}

	resp, err := assistant.Reply(context.Background(), AssistantRequest{
		StateBrief: "Visible projects: 1.",
		Messages:   []ChatMessage{{Role: "user", Content: "enqueue this for Alpha: add a Boss Desk TODO section"}},
	})
	if err != nil {
		t.Fatalf("Reply() error = %v", err)
	}
	if resp.ControlInvocation == nil || resp.ControlInvocation.Capability != control.CapabilityTodoAdd {
		t.Fatalf("ControlInvocation = %#v, want todo.add", resp.ControlInvocation)
	}
	if len(router.reqs) != 2 || router.reqs[1].SchemaName != "boss_todo_add_policy_review" {
		t.Fatalf("router requests = %+v, want route plus todo policy review", router.reqs)
	}
}

func TestAssistantReplyCanProposeTodoCompleteControl(t *testing.T) {
	t.Parallel()

	planner := &fakeJSONSchemaRunner{
		resp: []llm.JSONSchemaResponse{{
			Model: "gpt-test",
			OutputText: encodedBossAction(t, bossAction{
				Kind:              bossActionProposeControl,
				ControlCapability: "todo.complete",
				ProjectPath:       "/tmp/alpha",
				ProjectName:       "Alpha",
				TodoID:            42,
				TodoLabel:         "boss todo tracking",
				TodoText:          "Add Boss-managed TODO tracking.",
				TodoEvidence:      "The engineer reported the prompt, badge, and confirmation flow are implemented and tests pass.",
				Reason:            "The user asked to mark the tracked TODO done.",
			}),
			Usage: model.LLMUsage{TotalTokens: 19},
		}},
	}
	assistant := &Assistant{
		planner: planner,
		query:   newQueryExecutor(&fakeBossStore{}),
		model:   "gpt-test",
	}

	resp, err := assistant.Reply(context.Background(), AssistantRequest{
		StateBrief: "Visible projects: 1.\nOpen project TODOs (pending backlog, not delegated agent tasks):\n- #42; Alpha: boss todo tracking; text=Add Boss-managed TODO tracking.; project_path=/tmp/alpha",
		Messages:   []ChatMessage{{Role: "user", Content: "ok mark TODO 42 done"}},
	})
	if err != nil {
		t.Fatalf("Reply() error = %v", err)
	}
	if resp.ControlInvocation == nil {
		t.Fatalf("ControlInvocation = nil, want todo.complete proposal")
	}
	if resp.ControlInvocation.Capability != control.CapabilityTodoComplete {
		t.Fatalf("capability = %q", resp.ControlInvocation.Capability)
	}
	if !strings.Contains(resp.Content, "Mark TODO #42 boss todo tracking complete in Alpha?") ||
		!strings.Contains(resp.Content, "engineer reported") ||
		!strings.Contains(resp.Content, "Enter confirms") {
		t.Fatalf("proposal content = %q, want TODO complete confirmation preview", resp.Content)
	}
	var input control.TodoCompleteInput
	if err := json.Unmarshal(resp.ControlInvocation.Args, &input); err != nil {
		t.Fatalf("decode invocation args: %v", err)
	}
	if input.ProjectPath != "/tmp/alpha" || input.ProjectName != "Alpha" ||
		input.TodoID != 42 || input.TodoLabel != "boss todo tracking" || input.TodoText != "Add Boss-managed TODO tracking." ||
		!strings.Contains(input.Evidence, "tests pass") {
		t.Fatalf("invocation args = %#v", input)
	}
}

func TestAssistantReplyBuildsLosslessEngineerTaskPacket(t *testing.T) {
	t.Parallel()

	planner := &fakeJSONSchemaRunner{
		resp: []llm.JSONSchemaResponse{{
			Model: "gpt-test",
			OutputText: encodedBossAction(t, bossAction{
				Kind:              bossActionProposeControl,
				ControlCapability: "engineer.send_prompt",
				ProjectPath:       "/tmp/oyk-aso",
				EngineerProvider:  "codex",
				SessionMode:       "resume_or_new",
				Prompt:            "Check Fractal Strike's current Appfigures ranking and report the date, category, and rank.",
				IntentExcerpt:     "ranking in appfigures, not the order in terms of sales among our games",
				PreservedMeaning:  "Source must be Appfigures; metric must be external store ranking; do not substitute Google Play earnings, Play Pass, or internal sales order among our games.",
				SuccessCondition:  "Return the Appfigures source/date/category/rank, or say Appfigures was not checked or unavailable and name any different source that was checked.",
				Reveal:            false,
			}),
		}},
	}
	assistant := &Assistant{
		planner: planner,
		query:   newQueryExecutor(&fakeBossStore{}),
		model:   "gpt-test",
	}

	resp, err := assistant.Reply(context.Background(), AssistantRequest{
		StateBrief: "Visible projects: 1.",
		Messages: []ChatMessage{
			{Role: "user", Content: "look also in the relative oyk-aso session. I was asking about ranking in appfigures, not the order in terms of sales among our games"},
		},
	})
	if err != nil {
		t.Fatalf("Reply() error = %v", err)
	}
	if resp.ControlInvocation == nil {
		t.Fatalf("ControlInvocation = nil, want engineer.send_prompt proposal")
	}
	var input control.EngineerSendPromptInput
	if err := json.Unmarshal(resp.ControlInvocation.Args, &input); err != nil {
		t.Fatalf("decode invocation args: %v", err)
	}
	for _, want := range []string{
		"Help Chat lossless task packet:",
		"Original user wording to preserve:",
		"ranking in appfigures, not the order in terms of sales among our games",
		"Reframed executable task:",
		"Check Fractal Strike's current Appfigures ranking",
		"Preserved meaning:",
		"Source must be Appfigures",
		"do not substitute Google Play earnings",
		"Success condition:",
		"Return the Appfigures source/date/category/rank",
		"If the requested evidence is unavailable or a different source was checked",
	} {
		if !strings.Contains(input.Prompt, want) {
			t.Fatalf("lossless prompt missing %q:\n%s", want, input.Prompt)
		}
	}
}

func TestAssistantReplyCanProposeAgentTaskCreateControl(t *testing.T) {
	t.Parallel()

	planner := &fakeJSONSchemaRunner{
		resp: []llm.JSONSchemaResponse{{
			Model: "gpt-test",
			OutputText: encodedBossAction(t, bossAction{
				Kind:              bossActionProposeControl,
				ControlCapability: "agent_task.create",
				TaskTitle:         "Clean suspicious local processes",
				TaskKind:          "agent",
				EngineerProvider:  "codex",
				Prompt:            "Inspect the suspicious PIDs and terminate only clearly stale processes.",
				Capabilities:      []string{"process.inspect", "process.terminate"},
				Resources: []control.ResourceRef{
					{Kind: control.ResourceProcess, PID: 93624, Label: "hot python"},
					{Kind: control.ResourcePort, Port: 9229, Label: "debug listener"},
				},
				Reason: "The user asked Boss to delegate temporary machine work.",
			}),
			Usage: model.LLMUsage{TotalTokens: 19},
		}},
	}
	assistant := &Assistant{
		planner: planner,
		query:   newQueryExecutor(&fakeBossStore{}),
		model:   "gpt-test",
	}

	resp, err := assistant.Reply(context.Background(), AssistantRequest{
		StateBrief: "Open delegated agent tasks: none visible.",
		Messages:   []ChatMessage{{Role: "user", Content: "Clean up those stale processes"}},
	})
	if err != nil {
		t.Fatalf("Reply() error = %v", err)
	}
	if resp.ControlInvocation == nil {
		t.Fatalf("ControlInvocation = nil, want agent_task.create proposal")
	}
	if resp.ControlInvocation.Capability != control.CapabilityAgentTaskCreate {
		t.Fatalf("capability = %q", resp.ControlInvocation.Capability)
	}
	if !strings.Contains(resp.Content, "Create agent task") || !strings.Contains(resp.Content, "process.terminate") {
		t.Fatalf("proposal content = %q, want agent task confirmation preview", resp.Content)
	}
	if !strings.Contains(string(resp.ControlInvocation.Args), `"title":"Clean suspicious local processes"`) ||
		!strings.Contains(string(resp.ControlInvocation.Args), `"kind":"agent"`) ||
		!strings.Contains(string(resp.ControlInvocation.Args), `"capabilities":["`) {
		t.Fatalf("invocation args = %s", resp.ControlInvocation.Args)
	}
}

func TestAssistantReplyCanProposeAgentTaskContinueControl(t *testing.T) {
	t.Parallel()

	planner := &fakeJSONSchemaRunner{
		resp: []llm.JSONSchemaResponse{{
			Model: "gpt-test",
			OutputText: encodedBossAction(t, bossAction{
				Kind:              bossActionProposeControl,
				ControlCapability: "agent_task.continue",
				TaskID:            "agt_roguellm",
				EngineerProvider:  "codex",
				SessionMode:       "resume_or_new",
				Prompt:            "Continue the stale roguellm dev-server cleanup. Verify whether the server process is still running, terminate only clearly stale project-local processes, and report the result.",
				Reason:            "The user asked to solve the open agent tasks, starting with the stalest one.",
			}),
			Usage: model.LLMUsage{TotalTokens: 21},
		}},
	}
	assistant := &Assistant{
		planner: planner,
		query:   newQueryExecutor(&fakeBossStore{}),
		model:   "gpt-test",
	}

	resp, err := assistant.Reply(context.Background(), AssistantRequest{
		StateBrief: "Open delegated agent tasks (separate from project TODOs):\n- Kill stale roguellm dev server (agt_roguellm); show: agent_task:agt_roguellm; touched 9h ago",
		Messages: []ChatMessage{
			{Role: "assistant", Content: "We should clear the stale roguellm dev server first, then the duplicate Codex skills, then Cursor."},
			{Role: "user", Content: "cool"},
		},
	})
	if err != nil {
		t.Fatalf("Reply() error = %v", err)
	}
	if resp.ControlInvocation == nil {
		t.Fatalf("ControlInvocation = nil, want agent_task.continue proposal")
	}
	if resp.ControlInvocation.Capability != control.CapabilityAgentTaskContinue {
		t.Fatalf("capability = %q", resp.ControlInvocation.Capability)
	}
	if !strings.Contains(resp.Content, "Continue agent task agt_roguellm.") || !strings.Contains(resp.Content, "Enter sends") {
		t.Fatalf("proposal content = %q, want agent task continuation confirmation", resp.Content)
	}
	var input control.AgentTaskContinueInput
	if err := json.Unmarshal(resp.ControlInvocation.Args, &input); err != nil {
		t.Fatalf("decode invocation args: %v", err)
	}
	if input.TaskID != "agt_roguellm" ||
		!strings.Contains(input.Prompt, "Help Chat lossless task packet:") ||
		!strings.Contains(input.Prompt, "cool") ||
		!strings.Contains(input.Prompt, "Continue the stale roguellm dev-server cleanup.") {
		t.Fatalf("invocation args = %s", resp.ControlInvocation.Args)
	}
}

func TestAssistantReplyCanProposeAgentTaskCloseControl(t *testing.T) {
	t.Parallel()

	planner := &fakeJSONSchemaRunner{
		resp: []llm.JSONSchemaResponse{{
			Model: "gpt-test",
			OutputText: encodedBossAction(t, bossAction{
				Kind:              bossActionProposeControl,
				ControlCapability: "agent_task.close",
				TaskID:            "agt_diff",
				TaskCloseStatus:   "completed",
				TaskSummary:       "Skills inventory is clean; no duplicate or stale skill metadata remains.",
				CloseSession:      true,
				Reason:            "The status check resolved the review task.",
			}),
			Usage: model.LLMUsage{TotalTokens: 23},
		}},
	}
	assistant := &Assistant{
		planner: planner,
		query:   newQueryExecutor(&fakeBossStore{}),
		model:   "gpt-test",
	}

	resp, err := assistant.Reply(context.Background(), AssistantRequest{
		StateBrief: "Open delegated agent tasks (separate from project TODOs):\n- Diff duplicate Codex skills (agt_diff); kind/status: agent/review; show: agent_task:agt_diff",
		Messages:   []ChatMessage{{Role: "user", Content: "what's the situation with the skills?"}},
	})
	if err != nil {
		t.Fatalf("Reply() error = %v", err)
	}
	if resp.ControlInvocation == nil {
		t.Fatalf("ControlInvocation = nil, want agent_task.close proposal")
	}
	if resp.ControlInvocation.Capability != control.CapabilityAgentTaskClose {
		t.Fatalf("capability = %q", resp.ControlInvocation.Capability)
	}
	if !strings.Contains(resp.Content, "Mark agent task agt_diff as completed?") ||
		!strings.Contains(resp.Content, "Skills inventory is clean") {
		t.Fatalf("proposal content = %q, want close-task confirmation", resp.Content)
	}
	if !strings.Contains(string(resp.ControlInvocation.Args), `"task_id":"agt_diff"`) ||
		!strings.Contains(string(resp.ControlInvocation.Args), `"status":"completed"`) ||
		!strings.Contains(string(resp.ControlInvocation.Args), `"close_session":true`) {
		t.Fatalf("invocation args = %s", resp.ControlInvocation.Args)
	}
	if resp.Usage.TotalTokens != 23 {
		t.Fatalf("usage total = %d, want 23", resp.Usage.TotalTokens)
	}
}

func TestAssistantReplyCanArchiveOpenAgentTaskForCleanup(t *testing.T) {
	t.Parallel()

	planner := &fakeJSONSchemaRunner{
		resp: []llm.JSONSchemaResponse{{
			Model: "gpt-test",
			OutputText: encodedBossAction(t, bossAction{
				Kind:              bossActionProposeControl,
				ControlCapability: "agent_task.close",
				TaskID:            "agt_cpu",
				TaskCloseStatus:   "archived",
				TaskSummary:       "Stray CPU investigation task removed from the active record.",
				CloseSession:      false,
				Reason:            "The user asked to close the stray CPU usage task record.",
			}),
			Usage: model.LLMUsage{TotalTokens: 23},
		}},
	}
	assistant := &Assistant{
		planner: planner,
		query:   newQueryExecutor(&fakeBossStore{}),
		model:   "gpt-test",
	}

	resp, err := assistant.Reply(context.Background(), AssistantRequest{
		StateBrief: "Open delegated agent tasks (separate from project TODOs):\n- Investigate and reduce CPU usage (agt_cpu); kind/status: agent/review; show: agent_task:agt_cpu",
		Messages:   []ChatMessage{{Role: "user", Content: "let's close those about CPU usage"}},
	})
	if err != nil {
		t.Fatalf("Reply() error = %v", err)
	}
	if resp.ControlInvocation == nil {
		t.Fatalf("ControlInvocation = nil, want agent_task.close proposal")
	}
	if resp.ControlInvocation.Capability != control.CapabilityAgentTaskClose {
		t.Fatalf("capability = %q", resp.ControlInvocation.Capability)
	}
	if !strings.Contains(resp.Content, "Mark agent task agt_cpu as archived?") ||
		!strings.Contains(resp.Content, "Stray CPU investigation task removed") {
		t.Fatalf("proposal content = %q, want archive-task confirmation", resp.Content)
	}
	if !strings.Contains(string(resp.ControlInvocation.Args), `"task_id":"agt_cpu"`) ||
		!strings.Contains(string(resp.ControlInvocation.Args), `"status":"archived"`) ||
		!strings.Contains(string(resp.ControlInvocation.Args), `"close_session":false`) {
		t.Fatalf("invocation args = %s", resp.ControlInvocation.Args)
	}
}

func TestAssistantReplyCanProposeAgentTaskCleanupGoal(t *testing.T) {
	t.Parallel()

	planner := &fakeJSONSchemaRunner{
		resp: []llm.JSONSchemaResponse{{
			Model: "gpt-test",
			OutputText: encodedBossAction(t, bossAction{
				Kind:                bossActionProposeGoal,
				GoalKind:            bossrun.GoalKindAgentTaskCleanup,
				GoalTitle:           "Clear stale delegated agents",
				GoalObjective:       "Archive stale delegated agent task records that have served their scope.",
				GoalSuccessCriteria: "Selected tasks no longer appear in the active delegated agent task set.",
				GoalResources: []control.ResourceRef{
					{Kind: control.ResourceAgentTask, ID: "agt_one", Label: "old review"},
					{Kind: control.ResourceAgentTask, ID: "agt_two", Label: "old follow-up"},
				},
				GoalAllowedCapabilities:  []string{"agent_task.close"},
				GoalForbiddenSideEffects: []string{"close live engineer sessions", "delete files or workspaces"},
				GoalMaxRisk:              "write",
				Reason:                   "The user asked to clear multiple stale delegated agents.",
			}),
			Usage: model.LLMUsage{TotalTokens: 31},
		}},
	}
	assistant := &Assistant{
		planner: planner,
		query:   newQueryExecutor(&fakeBossStore{}),
		model:   "gpt-test",
	}

	resp, err := assistant.Reply(context.Background(), AssistantRequest{
		StateBrief: "Open delegated agent tasks:\n- old review (agt_one)\n- old follow-up (agt_two)",
		Messages:   []ChatMessage{{Role: "user", Content: "we have some stale agents that have served their scope. Let's remove them now"}},
	})
	if err != nil {
		t.Fatalf("Reply() error = %v", err)
	}
	if resp.ControlInvocation != nil {
		t.Fatalf("ControlInvocation = %#v, want goal proposal instead of one control", resp.ControlInvocation)
	}
	if resp.GoalProposal == nil {
		t.Fatalf("GoalProposal = nil, want agent_task_cleanup proposal")
	}
	if resp.GoalProposal.Run.Kind != bossrun.GoalKindAgentTaskCleanup {
		t.Fatalf("goal kind = %q, want agent_task_cleanup", resp.GoalProposal.Run.Kind)
	}
	if ids := bossrun.AgentTaskResourceIDs(resp.GoalProposal.Authority.Resources); len(ids) != 2 || ids[0] != "agt_one" || ids[1] != "agt_two" {
		t.Fatalf("goal task ids = %#v, want both stale tasks", ids)
	}
	if !strings.Contains(resp.Content, "Archive 2 delegated agent task records?") ||
		!strings.Contains(resp.Content, "Forbidden side effects") {
		t.Fatalf("goal proposal content = %q, want scoped confirmation preview", resp.Content)
	}
}

func TestAssistantReplyCanProposeScratchTaskArchiveControl(t *testing.T) {
	t.Parallel()

	planner := &fakeJSONSchemaRunner{
		resp: []llm.JSONSchemaResponse{{
			Model: "gpt-test",
			OutputText: encodedBossAction(t, bossAction{
				Kind:              bossActionProposeControl,
				ControlCapability: "scratch_task.archive",
				ProjectPath:       "/tmp/tasks/hex-accessibility",
				ProjectName:       "Hex accessibility issue",
				Reason:            "The completed scratch task should be archived out of the dashboard.",
			}),
			Usage: model.LLMUsage{TotalTokens: 17},
		}},
	}
	assistant := &Assistant{
		planner: planner,
		query:   newQueryExecutor(&fakeBossStore{}),
		model:   "gpt-test",
	}

	resp, err := assistant.Reply(context.Background(), AssistantRequest{
		StateBrief: "Hot projects:\n- Hex accessibility issue; latest work: Accessibility check completed.\n  Reference metadata (use only for disambiguation/blockers): kind=scratch_task; path=/tmp/tasks/hex-accessibility",
		Messages:   []ChatMessage{{Role: "user", Content: "remove the Hex accessibility task"}},
	})
	if err != nil {
		t.Fatalf("Reply() error = %v", err)
	}
	if resp.ControlInvocation == nil {
		t.Fatalf("ControlInvocation = nil, want scratch_task.archive proposal")
	}
	if resp.ControlInvocation.Capability != control.CapabilityScratchTaskArchive {
		t.Fatalf("capability = %q", resp.ControlInvocation.Capability)
	}
	if !strings.Contains(resp.Content, "Archive scratch task Hex accessibility issue?") {
		t.Fatalf("proposal content = %q, want scratch task archive confirmation", resp.Content)
	}
	var input control.ScratchTaskArchiveInput
	if err := json.Unmarshal(resp.ControlInvocation.Args, &input); err != nil {
		t.Fatalf("decode invocation args: %v", err)
	}
	if input.ProjectPath != "/tmp/tasks/hex-accessibility" || input.ProjectName != "Hex accessibility issue" {
		t.Fatalf("invocation args = %#v", input)
	}
}

func TestAssistantReplyCanProposeProjectArchiveControl(t *testing.T) {
	t.Parallel()

	planner := &fakeJSONSchemaRunner{
		resp: []llm.JSONSchemaResponse{{
			Model: "gpt-test",
			OutputText: encodedBossAction(t, bossAction{
				Kind:                 bossActionProposeControl,
				ControlCapability:    "project.set_archive_state",
				ProjectPath:          "/tmp/repos/regular-project",
				ProjectName:          "regular-project",
				ProjectArchiveAction: "archive",
				Reason:               "The user asked to hide the regular project from Active.",
			}),
			Usage: model.LLMUsage{TotalTokens: 17},
		}},
	}
	assistant := &Assistant{
		planner: planner,
		query:   newQueryExecutor(&fakeBossStore{}),
		model:   "gpt-test",
	}

	resp, err := assistant.Reply(context.Background(), AssistantRequest{
		StateBrief: "Hot projects:\n- regular-project; latest work: Done.\n  Reference metadata (use only for disambiguation/blockers): kind=project; path=/tmp/repos/regular-project",
		Messages:   []ChatMessage{{Role: "user", Content: "archive regular-project"}},
	})
	if err != nil {
		t.Fatalf("Reply() error = %v", err)
	}
	if resp.ControlInvocation == nil {
		t.Fatalf("ControlInvocation = nil, want project.set_archive_state proposal")
	}
	if resp.ControlInvocation.Capability != control.CapabilityProjectArchive {
		t.Fatalf("capability = %q", resp.ControlInvocation.Capability)
	}
	if !strings.Contains(resp.Content, "Archive project regular-project?") ||
		!strings.Contains(resp.Content, "without changing files on disk") {
		t.Fatalf("proposal content = %q, want project archive confirmation", resp.Content)
	}
	var input control.ProjectArchiveInput
	if err := json.Unmarshal(resp.ControlInvocation.Args, &input); err != nil {
		t.Fatalf("decode invocation args: %v", err)
	}
	if input.ProjectPath != "/tmp/repos/regular-project" ||
		input.ProjectName != "regular-project" ||
		input.Action != control.ProjectArchiveActionArchive {
		t.Fatalf("invocation args = %#v", input)
	}
}

func TestAssistantReplyCanProposeBatchProjectArchiveControl(t *testing.T) {
	t.Parallel()

	planner := &fakeJSONSchemaRunner{
		resp: []llm.JSONSchemaResponse{{
			Model: "gpt-test",
			OutputText: encodedBossAction(t, bossAction{
				Kind:                 bossActionProposeControl,
				ControlCapability:    "project.set_archive_state",
				ProjectArchiveAction: "archive",
				Resources: []control.ResourceRef{
					{Kind: control.ResourceProject, ProjectPath: "/tmp/repos/quickgame_01", Label: "quickgame_01"},
					{Kind: control.ResourceProject, ProjectPath: "/tmp/repos/quickgame_02", Label: "quickgame_02"},
				},
				Reason: "The user asked to archive all matching regular projects.",
			}),
			Usage: model.LLMUsage{TotalTokens: 17},
		}},
	}
	assistant := &Assistant{
		planner: planner,
		query:   newQueryExecutor(&fakeBossStore{}),
		model:   "gpt-test",
	}

	resp, err := assistant.Reply(context.Background(), AssistantRequest{
		StateBrief: "Context search exact project matches include quickgame_01 and quickgame_02.",
		Messages:   []ChatMessage{{Role: "user", Content: "archive all quickgame projects"}},
	})
	if err != nil {
		t.Fatalf("Reply() error = %v", err)
	}
	if resp.ControlInvocation == nil {
		t.Fatalf("ControlInvocation = nil, want project.set_archive_state proposal")
	}
	if resp.ControlInvocation.Capability != control.CapabilityProjectArchive {
		t.Fatalf("capability = %q", resp.ControlInvocation.Capability)
	}
	if !strings.Contains(resp.Content, "Archive 2 projects?") ||
		!strings.Contains(resp.Content, "quickgame_01") {
		t.Fatalf("proposal content = %q, want batch project archive confirmation", resp.Content)
	}
	var input control.ProjectArchiveInput
	if err := json.Unmarshal(resp.ControlInvocation.Args, &input); err != nil {
		t.Fatalf("decode invocation args: %v", err)
	}
	if input.ProjectPath != "" || input.ProjectName != "" ||
		input.Action != control.ProjectArchiveActionArchive ||
		len(input.Resources) != 2 ||
		input.Resources[0].ProjectPath != "/tmp/repos/quickgame_01" {
		t.Fatalf("invocation args = %#v", input)
	}
}

func TestAssistantReplyReportsInvalidControlProposalAsActionError(t *testing.T) {
	t.Parallel()

	planner := &fakeJSONSchemaRunner{
		resp: []llm.JSONSchemaResponse{{
			Model: "gpt-test",
			OutputText: encodedBossAction(t, bossAction{
				Kind:              bossActionProposeControl,
				ControlCapability: "engineer.send_prompt",
				EngineerProvider:  "codex",
				SessionMode:       "resume_or_new",
				Prompt:            "Kill the runaway processes.",
			}),
		}},
	}
	assistant := &Assistant{
		planner: planner,
		query:   newQueryExecutor(&fakeBossStore{}),
		model:   "gpt-test",
	}

	_, err := assistant.Reply(context.Background(), AssistantRequest{
		StateBrief: "Visible projects: 2.",
		Messages:   []ChatMessage{{Role: "user", Content: "Let's do it"}},
	})
	if err == nil {
		t.Fatalf("Reply() error = nil, want invalid control proposal")
	}
	var proposalErr controlProposalError
	if !errors.As(err, &proposalErr) {
		t.Fatalf("Reply() error = %T %v, want controlProposalError", err, err)
	}
	if !strings.Contains(err.Error(), "project_path or project_name is required") {
		t.Fatalf("Reply() error = %q, want project target detail", err.Error())
	}
}

func TestAssistantReplyStreamEmitsToolCallsAndTextDeltas(t *testing.T) {
	t.Parallel()

	store := &fakeBossStore{
		projects: []model.ProjectSummary{{
			Path:   "/tmp/alpha",
			Name:   "Alpha",
			Status: model.StatusPossiblyStuck,
		}},
		details: map[string]model.ProjectDetail{
			"/tmp/alpha": {
				Summary: model.ProjectSummary{
					Path:   "/tmp/alpha",
					Name:   "Alpha",
					Status: model.StatusPossiblyStuck,
				},
				Todos: []model.TodoItem{{ID: 7, ProjectPath: "/tmp/alpha", Text: "Decide rollout shape"}},
			},
		},
	}
	planner := &fakeJSONSchemaRunner{
		resp: []llm.JSONSchemaResponse{
			{
				Model:      "gpt-test",
				OutputText: encodedBossAction(t, bossAction{Kind: bossActionProjectDetail, ProjectPath: "/tmp/alpha", Limit: 8, Reason: "Need Alpha detail"}),
				Usage:      model.LLMUsage{InputTokens: 10, OutputTokens: 3, TotalTokens: 13},
			},
			{
				Model:      "gpt-test",
				OutputText: encodedBossAction(t, bossAction{Kind: bossActionAnswer, Answer: "Focus Alpha first."}),
				Usage:      model.LLMUsage{InputTokens: 20, OutputTokens: 5, TotalTokens: 25},
			},
		},
	}
	runner := &fakeStreamingTextRunner{err: errors.New("final text runner should not be called")}
	assistant := &Assistant{
		runner:  runner,
		planner: planner,
		query:   newQueryExecutor(store),
		model:   "gpt-test",
	}

	var events []AssistantStreamEvent
	resp, err := assistant.ReplyStream(context.Background(), AssistantRequest{
		StateBrief: "Visible projects: 1.",
		Messages:   []ChatMessage{{Role: "user", Content: "What about /tmp/alpha?"}},
	}, func(event AssistantStreamEvent) {
		events = append(events, event)
	})
	if err != nil {
		t.Fatalf("ReplyStream() error = %v", err)
	}
	if resp.Content != "Focus Alpha first." {
		t.Fatalf("Content = %q", resp.Content)
	}
	if runner.req.Model != "" {
		t.Fatalf("final text runner was called with model %q", runner.req.Model)
	}
	if resp.Usage.TotalTokens != 38 {
		t.Fatalf("total usage = %d, want 38", resp.Usage.TotalTokens)
	}
	var toolStates []string
	var text string
	for _, event := range events {
		if event.Kind == AssistantStreamToolCall {
			toolStates = append(toolStates, event.ToolState+":"+event.ToolCall)
		}
		if event.Kind == AssistantStreamTextDelta {
			text += event.Delta
		}
	}
	if strings.Join(toolStates, "|") != "running:project_detail /tmp/alpha|done:project_detail /tmp/alpha" {
		t.Fatalf("tool events = %#v", toolStates)
	}
	if text != "Focus Alpha first." {
		t.Fatalf("streamed text = %q", text)
	}
	if len(planner.reqs) < 2 {
		t.Fatalf("planner requests = %d, want follow-up after tool result", len(planner.reqs))
	}
	if !strings.Contains(planner.reqs[1].UserText, "Decide rollout shape") {
		t.Fatalf("planner follow-up request missing tool result:\n%s", planner.reqs[1].UserText)
	}
}

func TestAssistantReplyCanSearchBossSessions(t *testing.T) {
	t.Parallel()

	sessionStore := newBossSessionStore(t.TempDir())
	now := time.Date(2026, 4, 27, 10, 0, 0, 0, time.UTC)
	session, err := sessionStore.createSession(context.Background(), now)
	if err != nil {
		t.Fatalf("createSession() error = %v", err)
	}
	if err := sessionStore.appendMessage(context.Background(), session.SessionID, ChatMessage{Role: "user", Content: "The launch notes said wait for the API key.", At: now.Add(time.Minute)}); err != nil {
		t.Fatalf("appendMessage() error = %v", err)
	}

	planner := &fakeJSONSchemaRunner{
		resp: []llm.JSONSchemaResponse{
			{OutputText: encodedBossAction(t, bossAction{Kind: bossActionSearchBossSessions, Query: "launch notes", Limit: 4})},
			{OutputText: encodedBossAction(t, bossAction{Kind: bossActionAnswer, Answer: "We said to wait for the API key before moving on."})},
		},
	}
	assistant := &Assistant{
		planner: planner,
		query:   newQueryExecutorWithBossSessions(nil, sessionStore),
		model:   "gpt-test",
	}

	resp, err := assistant.Reply(context.Background(), AssistantRequest{
		StateBrief: "Visible projects: 1.",
		Messages:   []ChatMessage{{Role: "user", Content: "What did we say about launch notes?"}},
	})
	if err != nil {
		t.Fatalf("Reply() error = %v", err)
	}
	if resp.Content != "We said to wait for the API key before moving on." {
		t.Fatalf("Content = %q", resp.Content)
	}
	if len(planner.reqs) != 2 {
		t.Fatalf("structured calls = %d, want 2", len(planner.reqs))
	}
	if !strings.Contains(planner.reqs[1].UserText, "[search_boss_sessions]") || !strings.Contains(planner.reqs[1].UserText, "<boss_session") {
		t.Fatalf("second planner request missing boss session XML result:\n%s", planner.reqs[1].UserText)
	}
}

func TestAssistantReplyCanUseContextCommandForEngineerTranscripts(t *testing.T) {
	t.Parallel()

	store := &fakeBossStore{
		searchResults: []model.ContextSearchResult{{
			Source:      "session",
			ProjectPath: "/tmp/lcr",
			ProjectName: "LittleControlRoom",
			SessionID:   "codex:ses_engineer",
			Snippet:     `assistant: The summary flash was the boss attention row update cue.`,
			UpdatedAt:   time.Unix(1_800_000_000, 0),
		}},
	}
	planner := &fakeJSONSchemaRunner{
		resp: []llm.JSONSchemaResponse{
			{OutputText: encodedBossAction(t, bossAction{
				Kind:    bossActionContextCommand,
				Command: `ctx search engineer "summary flash" --project LittleControlRoom --limit 5`,
			})},
			{OutputText: encodedBossAction(t, bossAction{
				Kind:   bossActionAnswer,
				Answer: "That was from the engineer session, not Help Chat: the flash was the attention-row update cue.",
			})},
		},
	}
	assistant := &Assistant{
		planner: planner,
		query:   newQueryExecutor(store),
		model:   "gpt-test",
	}

	resp, err := assistant.Reply(context.Background(), AssistantRequest{
		StateBrief: "Visible projects: 1.",
		Messages:   []ChatMessage{{Role: "user", Content: "What did the AI assistant say about summary flash?"}},
	})
	if err != nil {
		t.Fatalf("Reply() error = %v", err)
	}
	if !strings.Contains(resp.Content, "engineer session") {
		t.Fatalf("Content = %q", resp.Content)
	}
	if len(planner.reqs) != 2 {
		t.Fatalf("structured calls = %d, want 2", len(planner.reqs))
	}
	if !strings.Contains(planner.reqs[1].UserText, "[context_command]") ||
		!strings.Contains(planner.reqs[1].UserText, "handle: engineer:codex:ses_engineer") {
		t.Fatalf("second planner request missing context command result:\n%s", planner.reqs[1].UserText)
	}
}

func encodedBossAction(t *testing.T, action bossAction) string {
	t.Helper()
	raw, err := json.Marshal(action)
	if err != nil {
		t.Fatalf("marshal action: %v", err)
	}
	return string(raw)
}

func encodedReadOnlyRoute(t *testing.T, route bossReadOnlyRoute) string {
	t.Helper()
	raw, err := json.Marshal(route)
	if err != nil {
		t.Fatalf("marshal read-only route: %v", err)
	}
	return string(raw)
}

func encodedTodoAddPolicyReview(t *testing.T, review bossTodoAddPolicyReview) string {
	t.Helper()
	raw, err := json.Marshal(review)
	if err != nil {
		t.Fatalf("marshal todo.add policy review: %v", err)
	}
	return string(raw)
}
