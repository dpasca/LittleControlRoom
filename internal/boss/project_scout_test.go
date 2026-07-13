package boss

import (
	"context"
	"errors"
	"path/filepath"
	"strings"
	"testing"

	"lcroom/internal/lcagent"
	"lcroom/internal/model"
)

type fakeProjectScout struct {
	result  lcagent.ScoutResult
	err     error
	request lcagent.ScoutRequest
}

func (s *fakeProjectScout) Scout(_ context.Context, req lcagent.ScoutRequest) (lcagent.ScoutResult, error) {
	s.request = req
	return s.result, s.err
}

func TestQueryExecutorProjectScoutReturnsEvidenceAndUserReceipt(t *testing.T) {
	root := t.TempDir()
	plan := filepath.Join(root, "docs", "MVP.md")
	trace := filepath.Join(t.TempDir(), "scout.jsonl")
	scout := &fakeProjectScout{result: lcagent.ScoutResult{
		Summary:          "The MVP plan exists and lists first-run guidance next.",
		Route:            lcagent.ScoutRoute{Source: "chat_utility", Description: "inherited Chat utility model", Provider: "deepseek", Model: "deepseek-v4-flash"},
		ResolvedProvider: "deepseek",
		ResolvedModel:    "deepseek-v4-flash",
		SessionID:        "lca_test",
		ArtifactPath:     trace,
		Evidence:         []lcagent.ScoutEvidence{{Path: plan, StartLine: 12, EndLine: 24, TotalLines: 80}},
		Attempts:         []lcagent.ScoutAttempt{{Source: "chat_utility", Status: "used"}},
	}}
	executor := newQueryExecutor(&fakeBossStore{projects: []model.ProjectSummary{{Path: root, Name: "Alpha"}}})
	executor.projectScout = scout
	executor.dataDir = "/tmp/lcr-data"

	result, err := executor.Execute(context.Background(), bossAction{
		Kind:        bossActionProjectScout,
		ProjectPath: root,
		Query:       "Do we have an MVP plan?",
	}, StateSnapshot{}, ViewContext{})
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"Fresh read-only repository Scout result", "The MVP plan exists", "Mechanically recorded read evidence", "MVP.md", "inherited Chat utility model", "LCAgent Scout trace"} {
		if !strings.Contains(result.Text, want) {
			t.Fatalf("result missing %q:\n%s", want, result.Text)
		}
	}
	if !strings.Contains(result.UserReceipt, "Repository inspection:") || !strings.Contains(result.UserReceipt, "evidence") || !strings.Contains(result.UserReceipt, "trace") {
		t.Fatalf("receipt = %q", result.UserReceipt)
	}
	if scout.request.WorkspaceRoot != root || scout.request.Question != "Do we have an MVP plan?" || scout.request.DataDir != "/tmp/lcr-data" {
		t.Fatalf("Scout request = %+v", scout.request)
	}
	answer := appendBossToolReceipts("Yes, the plan exists.", []bossToolResult{result})
	if !strings.Contains(answer, "Yes, the plan exists.") || !strings.Contains(answer, result.UserReceipt) {
		t.Fatalf("answer receipt not appended:\n%s", answer)
	}
}

func TestQueryExecutorProjectScoutFailureDoesNotBecomeAbsenceClaim(t *testing.T) {
	root := t.TempDir()
	attempts := []lcagent.ScoutAttempt{{
		Source:      "lcagent_override",
		Description: "explicit LCAgent route",
		Provider:    "openai",
		Model:       "gpt-test",
		Status:      "failed",
		Error:       "missing provider credential",
	}}
	scoutErr := &lcagent.ScoutUnavailableError{Attempts: attempts}
	scout := &fakeProjectScout{result: lcagent.ScoutResult{Attempts: attempts}, err: scoutErr}
	executor := newQueryExecutor(&fakeBossStore{projects: []model.ProjectSummary{{Path: root, Name: "Alpha"}}})
	executor.projectScout = scout

	result, err := executor.Execute(context.Background(), bossAction{
		Kind:        bossActionProjectScout,
		ProjectPath: root,
		Query:       "Is there no plan?",
	}, StateSnapshot{}, ViewContext{})
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"Repository inspection unavailable", "Do not infer that repository content is absent", "missing provider credential"} {
		if !strings.Contains(result.Text, want) {
			t.Fatalf("result missing %q:\n%s", want, result.Text)
		}
	}
	if !strings.Contains(result.UserReceipt, "no absence claim should be inferred") {
		t.Fatalf("receipt = %q", result.UserReceipt)
	}
}

func TestQueryExecutorProjectScoutRequiresQuestion(t *testing.T) {
	root := t.TempDir()
	executor := newQueryExecutor(&fakeBossStore{projects: []model.ProjectSummary{{Path: root, Name: "Alpha"}}})
	executor.projectScout = &fakeProjectScout{err: errors.New("should not run")}
	_, err := executor.Execute(context.Background(), bossAction{Kind: bossActionProjectScout, ProjectPath: root}, StateSnapshot{}, ViewContext{})
	if err == nil || !strings.Contains(err.Error(), "needs the user's repository question") {
		t.Fatalf("error = %v", err)
	}
}

func TestQueryExecutorProjectScoutRejectsUntrackedExactPath(t *testing.T) {
	root := t.TempDir()
	scout := &fakeProjectScout{}
	executor := newQueryExecutor(&fakeBossStore{})
	executor.projectScout = scout
	result, err := executor.Execute(context.Background(), bossAction{
		Kind:        bossActionProjectScout,
		ProjectPath: root,
		Query:       "What is in this directory?",
	}, StateSnapshot{}, ViewContext{})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(result.Text, "only reads tracked projects") || !strings.Contains(result.UserReceipt, "inspection unavailable") {
		t.Fatalf("result should explain tracked-project boundary: %+v", result)
	}
	if scout.request.WorkspaceRoot != "" {
		t.Fatalf("Scout unexpectedly ran for untracked path: %+v", scout.request)
	}
}
