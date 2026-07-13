package lcagent

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestRunScoutWithRouteUsesReadOnlyHarnessAndReturnsEvidence(t *testing.T) {
	isolateSkillHomes(t)
	root := t.TempDir()
	planPath := filepath.Join(root, "docs", "MVP.md")
	if err := os.MkdirAll(filepath.Dir(planPath), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(planPath, []byte("# MVP\n\nNext: ship the briefing flow.\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "AGENTS.md"), []byte("Read docs/MVP.md before answering planning questions.\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	requests := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests++
		var body struct {
			Tools []struct {
				Function struct {
					Name string `json:"name"`
				} `json:"function"`
			} `json:"tools"`
		}
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			t.Fatal(err)
		}
		for _, tool := range body.Tools {
			switch tool.Function.Name {
			case "run_command", "apply_patch", "create_file", "load_skill":
				t.Fatalf("read-only Scout exposed %s", tool.Function.Name)
			}
		}
		w.Header().Set("Content-Type", "application/json")
		switch requests {
		case 1:
			_, _ = w.Write([]byte(`{
				"id":"scout_read",
				"model":"test-scout",
				"choices":[{"finish_reason":"tool_calls","message":{"role":"assistant","tool_calls":[{"id":"call_read","type":"function","function":{"name":"read_file","arguments":"{\"path\":\"docs/MVP.md\",\"offset\":1,\"limit\":20}"}}]}}],
				"usage":{"prompt_tokens":100,"completion_tokens":20,"total_tokens":120}
			}`))
		case 2:
			_, _ = w.Write([]byte(`{
				"id":"scout_done",
				"model":"test-scout",
				"choices":[{"finish_reason":"stop","message":{"role":"assistant","content":"The MVP plan exists and says the briefing flow is next."}}],
				"usage":{"prompt_tokens":150,"completion_tokens":30,"total_tokens":180}
			}`))
		default:
			t.Fatalf("unexpected Scout provider request %d", requests)
		}
	}))
	defer server.Close()

	result, err := RunScoutWithRoute(context.Background(), ScoutRequest{
		WorkspaceRoot: root,
		Question:      "Do we have an MVP plan, and what is next?",
		DataDir:       t.TempDir(),
	}, ScoutRoute{
		Source:         "chat_utility",
		Description:    "inherited Chat utility model",
		Provider:       "openrouter",
		Model:          "test-scout",
		APIKey:         "test-key",
		BaseURL:        server.URL,
		RequestTimeout: time.Second,
	})
	if err != nil {
		t.Fatalf("RunScoutWithRoute() error = %v", err)
	}
	if requests != 2 {
		t.Fatalf("provider requests = %d, want 2", requests)
	}
	if !strings.Contains(result.Summary, "MVP plan exists") {
		t.Fatalf("summary = %q", result.Summary)
	}
	if result.ResolvedProvider != "openrouter" || result.ResolvedModel != "test-scout" {
		t.Fatalf("resolved route = %s/%s", result.ResolvedProvider, result.ResolvedModel)
	}
	if result.SessionID == "" || result.ArtifactPath == "" {
		t.Fatalf("missing trace receipt: %+v", result)
	}
	if len(result.Evidence) != 1 {
		t.Fatalf("evidence = %+v, want one read range", result.Evidence)
	}
	evidence := result.Evidence[0]
	if evidence.Path != planPath || evidence.StartLine != 1 || evidence.EndLine != 3 {
		t.Fatalf("evidence = %+v, want %s lines 1-3", evidence, planPath)
	}
	if result.Usage.TotalTokens != 300 {
		t.Fatalf("usage = %+v, want 300 total tokens", result.Usage)
	}
	trace, err := os.ReadFile(result.ArtifactPath)
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{`"type":"inference_route"`, `"source":"chat_utility"`, `"type":"project_instructions"`, `"read_only":true`} {
		if !strings.Contains(string(trace), want) {
			t.Fatalf("trace missing %q:\n%s", want, trace)
		}
	}
}

func TestScoutServiceFallsBackAndReportsAttempts(t *testing.T) {
	isolateSkillHomes(t)
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "PLAN.md"), []byte("# Plan\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	requests := 0
	working := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		requests++
		w.Header().Set("Content-Type", "application/json")
		if requests == 1 {
			_, _ = w.Write([]byte(`{"id":"read","model":"fallback-model","choices":[{"finish_reason":"tool_calls","message":{"role":"assistant","tool_calls":[{"id":"read","type":"function","function":{"name":"read_file","arguments":"{\"path\":\"PLAN.md\",\"limit\":10}"}}]}}]}`))
			return
		}
		_, _ = w.Write([]byte(`{"id":"done","model":"fallback-model","choices":[{"finish_reason":"stop","message":{"role":"assistant","content":"Plan found."}}]}`))
	}))
	defer working.Close()

	service := ScoutService{Routes: []ScoutRoute{
		{Source: "lcagent_override", Description: "explicit override", Provider: "unsupported", Model: "broken", RequestTimeout: time.Second},
		{Source: "chat_utility", Description: "inherited Chat utility model", Provider: "openrouter", Model: "fallback-model", APIKey: "key", BaseURL: working.URL, RequestTimeout: time.Second},
	}}
	result, err := service.Scout(context.Background(), ScoutRequest{WorkspaceRoot: root, Question: "Find the plan", DataDir: t.TempDir()})
	if err != nil {
		t.Fatal(err)
	}
	if len(result.Attempts) != 2 || result.Attempts[0].Status != "failed" || result.Attempts[1].Status != "used" {
		t.Fatalf("attempts = %+v", result.Attempts)
	}
	if result.Route.Source != "chat_utility" {
		t.Fatalf("route source = %q, want chat_utility", result.Route.Source)
	}
}
