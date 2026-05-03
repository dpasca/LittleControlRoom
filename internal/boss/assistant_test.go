package boss

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"strings"
	"testing"
	"time"

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

func TestBossPromptsPreferCoworkerBriefAndSearchBeforeUnknown(t *testing.T) {
	t.Parallel()

	directPrompt := bossAssistantSystemPrompt()
	for _, want := range []string{
		"unnamed Boss Chat helper",
		"the user is the boss",
		"engineer sessions",
		"Boss Chat messages",
		"Open agent tasks are delegated engineer work items",
		"separate from project TODOs",
		"the AI assistant",
		"extension of the active engineer sessions",
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
	} {
		if !strings.Contains(directPrompt, want) {
			t.Fatalf("assistant prompt missing %q:\n%s", want, directPrompt)
		}
	}

	plannerPrompt := bossActionPlannerSystemPrompt()
	for _, want := range []string{
		"Do not answer that a concrete term is unknown until search_context has been tried.",
		"extension of the active engineer sessions",
		"propose_control",
		"agent_task.create",
		"agent_task_report",
		"Use agent_task_report when the user asks about open or active agent tasks",
		"project TODOs are separate from delegated agent tasks",
		"Do not answer that there are no open agent tasks",
		"Use engineer.send_prompt only for explicit project/repo work",
		"Use agent_task.create for temporary delegated work",
		"do not encode special domains as task kinds",
		"Do not use the Little Control Room project or another unrelated active engineer session as a proxy venue",
		"user confirmation",
		"context_command",
		"ctx search engineer",
		"ctx show",
		"ctx show agent_task",
		"output or result of an open agent task",
		`vague request like "please try again"`,
		"Use ctx search boss",
		"search_boss_sessions",
		"process_report",
		"skills_inventory",
		"suspicious PIDs",
		"XML-like boss_session and turn snippets",
		"after it finds one project path, inspect project_detail before answering",
		"live engineer session context",
		"concise coworker update",
		"turn tool output into judgment",
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
				SessionMode:       "resume_or_new",
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
	if !strings.Contains(resp.Content, "Send this to OpenCode") || !strings.Contains(resp.Content, "Enter confirms") {
		t.Fatalf("proposal content = %q, want confirmation preview", resp.Content)
	}
	if !strings.Contains(string(resp.ControlInvocation.Args), `"provider":"opencode"`) ||
		!strings.Contains(string(resp.ControlInvocation.Args), `"prompt":"Please fix the failing tests and report what changed."`) {
		t.Fatalf("invocation args = %s", resp.ControlInvocation.Args)
	}
	if resp.Usage.TotalTokens != 17 {
		t.Fatalf("usage total = %d, want 17", resp.Usage.TotalTokens)
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
	runner := &fakeStreamingTextRunner{
		deltas: []string{"Focus ", "Alpha first."},
		resp: llm.TextResponse{
			Model:      "gpt-test",
			OutputText: "Focus Alpha first.",
			Usage:      model.LLMUsage{InputTokens: 4, OutputTokens: 3, TotalTokens: 7},
		},
	}
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
	if resp.Usage.TotalTokens != 45 {
		t.Fatalf("total usage = %d, want 45", resp.Usage.TotalTokens)
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
	if !strings.Contains(runner.req.Messages[len(runner.req.Messages)-1].Content, "Decide rollout shape") {
		t.Fatalf("final text request missing tool result:\n%s", runner.req.Messages[len(runner.req.Messages)-1].Content)
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
				Answer: "That was from the engineer session, not Boss Chat: the flash was the attention-row update cue.",
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
