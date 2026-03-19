package llm

import (
	"context"
	"errors"
	"strings"
	"sync"
	"testing"
	"time"

	"lcroom/internal/codexapp"
	"lcroom/internal/model"
)

type fakeCodexPromptHelper struct {
	mu         sync.Mutex
	runCount   int
	closeCount int
	requests   []codexapp.PromptHelperRequest
	response   codexapp.PromptHelperResponse
	err        error
}

func (f *fakeCodexPromptHelper) Run(_ context.Context, req codexapp.PromptHelperRequest) (codexapp.PromptHelperResponse, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.runCount++
	f.requests = append(f.requests, req)
	if f.err != nil {
		return codexapp.PromptHelperResponse{}, f.err
	}
	return f.response, nil
}

func (f *fakeCodexPromptHelper) Close() error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.closeCount++
	return nil
}

func TestPersistentCodexRunnerReusesHelperAcrossDistinctRequests(t *testing.T) {
	helper := &fakeCodexPromptHelper{
		response: codexapp.PromptHelperResponse{
			OutputText: `{"message":"hello"}`,
			Model:      "gpt-5.4-mini",
			Usage: model.LLMUsage{
				InputTokens:  10,
				OutputTokens: 2,
				TotalTokens:  12,
			},
		},
	}
	created := 0
	runner := NewPersistentCodexRunner(2*time.Second, nil)
	runner.helperFactory = func() (codexPromptHelper, error) {
		created++
		return helper, nil
	}
	runner.idleTimeout = 0
	runner.maxRequests = 8

	reqA := JSONSchemaRequest{Model: "gpt-5.4-mini", SystemText: "system", UserText: "user-a", SchemaName: "demo", Schema: map[string]any{"type": "object"}}
	reqB := JSONSchemaRequest{Model: "gpt-5.4-mini", SystemText: "system", UserText: "user-b", SchemaName: "demo", Schema: map[string]any{"type": "object"}}

	if _, err := runner.RunJSONSchema(context.Background(), reqA); err != nil {
		t.Fatalf("first RunJSONSchema() error = %v", err)
	}
	if _, err := runner.RunJSONSchema(context.Background(), reqB); err != nil {
		t.Fatalf("second RunJSONSchema() error = %v", err)
	}
	if created != 1 {
		t.Fatalf("helperFactory call count = %d, want 1 reused helper", created)
	}
	if helper.runCount != 2 {
		t.Fatalf("helper run count = %d, want 2 distinct requests", helper.runCount)
	}
}

func TestPersistentCodexRunnerRotatesHelperAfterMaxRequests(t *testing.T) {
	helpers := []*fakeCodexPromptHelper{
		{response: codexapp.PromptHelperResponse{OutputText: `{"message":"one"}`, Model: "gpt-5.4-mini"}},
		{response: codexapp.PromptHelperResponse{OutputText: `{"message":"two"}`, Model: "gpt-5.4-mini"}},
	}
	created := 0
	runner := NewPersistentCodexRunner(2*time.Second, nil)
	runner.helperFactory = func() (codexPromptHelper, error) {
		helper := helpers[created]
		created++
		return helper, nil
	}
	runner.idleTimeout = 0
	runner.maxRequests = 1

	reqA := JSONSchemaRequest{Model: "gpt-5.4-mini", SystemText: "system", UserText: "user-a", SchemaName: "demo", Schema: map[string]any{"type": "object"}}
	reqB := JSONSchemaRequest{Model: "gpt-5.4-mini", SystemText: "system", UserText: "user-b", SchemaName: "demo", Schema: map[string]any{"type": "object"}}

	if _, err := runner.RunJSONSchema(context.Background(), reqA); err != nil {
		t.Fatalf("first RunJSONSchema() error = %v", err)
	}
	if _, err := runner.RunJSONSchema(context.Background(), reqB); err != nil {
		t.Fatalf("second RunJSONSchema() error = %v", err)
	}
	if created != 2 {
		t.Fatalf("helperFactory call count = %d, want 2 helpers after rotation", created)
	}
	if helpers[0].closeCount != 1 {
		t.Fatalf("first helper close count = %d, want 1 after rotation", helpers[0].closeCount)
	}
}

func TestPersistentCodexRunnerDiscardsHelperOnError(t *testing.T) {
	first := &fakeCodexPromptHelper{err: errors.New("boom")}
	second := &fakeCodexPromptHelper{
		response: codexapp.PromptHelperResponse{OutputText: `{"message":"ok"}`, Model: "gpt-5.4-mini"},
	}
	created := 0
	runner := NewPersistentCodexRunner(2*time.Second, nil)
	runner.helperFactory = func() (codexPromptHelper, error) {
		created++
		if created == 1 {
			return first, nil
		}
		return second, nil
	}
	runner.idleTimeout = 0
	runner.maxRequests = 8

	req := JSONSchemaRequest{Model: "gpt-5.4-mini", SystemText: "system", UserText: "user", SchemaName: "demo", Schema: map[string]any{"type": "object"}}

	if _, err := runner.RunJSONSchema(context.Background(), req); err == nil {
		t.Fatalf("first RunJSONSchema() error = nil, want helper error")
	}
	if first.closeCount != 1 {
		t.Fatalf("failed helper close count = %d, want 1 after discard", first.closeCount)
	}
	if _, err := runner.RunJSONSchema(context.Background(), req); err != nil {
		t.Fatalf("second RunJSONSchema() error = %v", err)
	}
	if created != 2 {
		t.Fatalf("helperFactory call count = %d, want 2 after recreating helper", created)
	}
}

func TestPersistentCodexRunnerIncludesSchemaInPrompt(t *testing.T) {
	helper := &fakeCodexPromptHelper{
		response: codexapp.PromptHelperResponse{
			OutputText: `{"message":"hello"}`,
			Model:      "gpt-5.4-mini",
		},
	}
	runner := NewPersistentCodexRunner(2*time.Second, nil)
	runner.helperFactory = func() (codexPromptHelper, error) {
		return helper, nil
	}
	runner.idleTimeout = 0

	req := JSONSchemaRequest{
		Model:           "gpt-5.4-mini",
		SystemText:      "system",
		UserText:        "user",
		SchemaName:      "git_commit_message",
		ReasoningEffort: "low",
		Schema: map[string]any{
			"type": "object",
			"properties": map[string]any{
				"message": map[string]any{"type": "string"},
			},
			"required": []string{"message"},
		},
	}

	if _, err := runner.RunJSONSchema(context.Background(), req); err != nil {
		t.Fatalf("RunJSONSchema() error = %v", err)
	}
	if len(helper.requests) != 1 {
		t.Fatalf("helper request count = %d, want 1", len(helper.requests))
	}
	prompt := helper.requests[0].Prompt
	if !strings.Contains(prompt, `"message"`) {
		t.Fatalf("prompt = %q, want embedded schema field name", prompt)
	}
	if !strings.Contains(prompt, "Return only valid JSON that matches this schema exactly:") {
		t.Fatalf("prompt = %q, want explicit schema instructions", prompt)
	}
}
