package lcagent

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"lcroom/internal/lcagent/modeladapter"
	"lcroom/internal/lcagent/policy"
	"lcroom/internal/lcagent/script"
	"lcroom/internal/lcagent/session"
	"lcroom/internal/lcagent/tools"
)

func TestSearchConvergenceSurvivesLoopCompaction(t *testing.T) {
	var requests, searches int
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		if r.URL.Path == "/search" {
			searches++
			if r.URL.Query().Has("time_range") {
				t.Fatal("omitted recency_days added a date filter")
			}
			_ = json.NewEncoder(w).Encode(map[string]any{"results": []any{map[string]any{
				"title": "Magazine archive", "url": "https://example.com/archive",
				"content": strings.Repeat("An archive entry requiring inspection. ", 250),
			}}})
			return
		}
		if r.URL.Path != "/chat/completions" {
			t.Fatalf("unexpected path %q", r.URL.Path)
		}
		requests++
		var body struct {
			Messages []modeladapter.Message `json:"messages"`
		}
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			t.Fatal(err)
		}
		var note string
		for _, msg := range body.Messages {
			note += msg.Content + "\n"
		}
		if requests == 3 {
			for _, want := range []string{"phase: consolidation", "web search calls for the current request: 12", "update the user"} {
				if !strings.Contains(note, want) {
					t.Fatalf("post-compaction request missing %q:\n%s", want, note)
				}
			}
			report := testProgressReport()
			report.Objective = "Identify the historical magazine issue."
			report.UserUpdate = "The archive exists, but the issue number remains unverified."
			report.Decision = "finish"
			replyHarnessTest(w, "deepseek", requests, "", harnessTestCall(progressCheckpointTool, report))
			return
		}
		if requests == 4 {
			replyHarnessTest(w, "deepseek", requests, "", harnessTestCall("final_response", map[string]any{"summary": "The archive exists, but the issue number remains unverified.", "outcome": "partial", "files_changed": []string{}, "verification": []string{}}))
			return
		}
		if requests > 4 {
			t.Fatalf("unexpected model request %d", requests)
		}
		var calls []any
		for i := 0; i < 6; i++ {
			args, _ := json.Marshal(map[string]any{"query": fmt.Sprintf("magazine issue variant %d-%d", requests, i)})
			calls = append(calls, map[string]any{
				"id": fmt.Sprintf("call_%d_%d", requests, i), "type": "function",
				"function": map[string]any{"name": "web_search", "arguments": string(args)},
			})
		}
		_ = json.NewEncoder(w).Encode(map[string]any{"choices": []any{map[string]any{
			"message": map[string]any{"role": "assistant", "content": strings.Repeat("Search evidence to retain. ", 2000), "tool_calls": calls},
		}}})
	}))
	defer server.Close()

	var stream bytes.Buffer
	writer, sessionID, err := session.NewWriter(t.TempDir(), time.Now(), &stream)
	if err != nil {
		t.Fatal(err)
	}
	defer writer.Close()
	workspace, err := policy.NewWorkspace(t.TempDir(), policy.AutonomyMedium)
	if err != nil {
		t.Fatal(err)
	}
	limits := tools.FileLimitsForProfile(tools.FileProfileBalanced)
	runner := script.Runner{
		Session: writer, SessionID: sessionID, Prompt: "Find a historical magazine issue.",
		Command: tools.CommandRunner{Workspace: workspace}, Patch: tools.PatchApplier{Workspace: workspace},
		Files:       tools.FileTools{Workspace: workspace, Limits: limits},
		WebSearchOn: true, WebSearch: tools.WebSearchRunner{Backend: tools.WebSearchBackendSearXNG, URL: server.URL, HTTPClient: server.Client()},
	}
	err = runChatLoop(context.Background(), writer, runner, nil, "", nil,
		modeladapter.OpenRouterConfig{APIKey: "test-key", BaseURL: server.URL, Model: "test-model", MaxTurns: 160},
		modeladapter.OpenRouterConfig{}, modeladapter.OpenRouterConfig{},
		"deepseek", "off", "off", script.DefaultSearchRefineMinBytes, tools.FileProfileBalanced, limits,
		openRouterContextOptions{LoopCompactionTokenBudget: 40000, LoopCompactionCharThreshold: 160000, LoopCompactionTranscriptChars: 2000}, false, true, false)
	if err != nil {
		t.Fatalf("runChatLoop: %v\n%s", err, stream.String())
	}
	if requests != 4 || searches != 12 || !strings.Contains(stream.String(), `"type":"context_compacted"`) || !strings.Contains(stream.String(), `"reason":"work_budget"`) {
		t.Fatalf("requests=%d searches=%d; expected compaction and search checkpoint\n%s", requests, searches, stream.String())
	}
}
