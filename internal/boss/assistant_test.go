package boss

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"strings"
	"testing"

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

func TestBossPromptsPreferExecutiveBriefAndSearchBeforeUnknown(t *testing.T) {
	t.Parallel()

	directPrompt := bossAssistantSystemPrompt()
	for _, want := range []string{
		"executive-brief assistant",
		"extension of the active Codex, OpenCode, or Claude Code sessions",
		"ongoing coworker chat",
		"skip onboarding",
		"highest-level read first",
		"operational takeaway",
		"latest meaningful work",
		"concrete next validation, decision, or risk",
		"Do not start with mapping phrases",
		"Treat codenames as shared coworker context",
		"sharp spoken update to a busy owner",
		"latest session evidence",
		"Minimize redundant information",
		"repo hygiene, counts, scores, branches, freshness, or board stats only when they explain a real blocker or decision",
		"dirty working tree, ahead commits, and the current branch are normal background state",
		"Use reference metadata internally",
		"Prefer verbs from the evidence",
	} {
		if !strings.Contains(directPrompt, want) {
			t.Fatalf("assistant prompt missing %q:\n%s", want, directPrompt)
		}
	}

	plannerPrompt := bossActionPlannerSystemPrompt()
	for _, want := range []string{
		"Do not answer that a concrete term is unknown until search_context has been tried.",
		"extension of the active Codex, OpenCode, or Claude Code sessions",
		"after it finds one project path, inspect project_detail before answering",
		"live assistant session context",
		"concise executive brief",
		"turn tool output into judgment",
		"answer the operational substance rather than reciting the lookup",
		"codenames and aliases as shared coworker context",
		"latest meaningful work plus the immediate validation, decision, or risk",
		"Minimize redundant information",
		"Do not include mappings, paths, dirty/ahead state, branch names, ages, attention scores, confidence, queue, or classification telemetry unless it materially changes what the user should do.",
		"Treat repo hygiene as material only for conflicts",
		"Use reference metadata only to disambiguate targets and detect blockers",
		"Do not hedge a single clear match",
		"avoid capability pitches or optional menus",
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

func encodedBossAction(t *testing.T, action bossAction) string {
	t.Helper()
	raw, err := json.Marshal(action)
	if err != nil {
		t.Fatalf("marshal action: %v", err)
	}
	return string(raw)
}
