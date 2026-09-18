package lcagent

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"lcroom/internal/lcagent/imagemedia"
	"lcroom/internal/lcagent/modeladapter"
	"lcroom/internal/model"
)

func TestContextCounterUsesProviderUsageAndCountsUnseenContent(t *testing.T) {
	messages := []modeladapter.Message{{Role: "user", Content: "Inspect 税理士送信"}}
	defs := []modeladapter.ToolDefinition{{Type: "function", Function: modeladapter.FunctionSpec{Name: "inspect"}}}
	response := modeladapter.Message{Role: "assistant", Content: "done", ReasoningContent: strings.Repeat("reasoning ", 100)}
	counter := &contextTokenCounter{}
	counter.Observe("deepseek-flash", messages, defs, modeladapter.Completion{
		Message:      response,
		UsageSummary: model.LLMUsage{InputTokens: 100_000, CachedInputTokens: 90_000, OutputTokens: 2000, ReasoningTokens: 1900},
	})
	messages = append(messages, response)
	if got := counter.Estimate("deepseek-flash", messages, defs); got.Tokens != 102_000 || got.Source != "provider" {
		t.Fatalf("provider input plus full output (cache included once) = %+v", got)
	}
	result := modeladapter.Message{Role: "tool", Content: "税理士送信"}
	messages = append(messages, result)
	got := counter.Estimate("deepseek-flash", messages, defs)
	if got.Tokens != 102_000+unmeasuredMessageTokens(result) || got.Source != "provider+estimate" {
		t.Fatalf("new tool output not included: %+v", got)
	}
	changedDefs := append(append([]modeladapter.ToolDefinition(nil), defs...), modeladapter.ToolDefinition{Function: modeladapter.FunctionSpec{Name: "new_tool", Description: strings.Repeat("schema ", 200)}})
	if next := counter.Estimate("deepseek-flash", messages, changedDefs); next.Tokens != got.Tokens+unmeasuredToolsTokens(changedDefs) {
		t.Fatalf("changed schema omitted: %+v", next)
	}
	counter.Observe("deepseek-flash", messages, defs, modeladapter.Completion{})
	if next := counter.Estimate("deepseek-flash", messages, defs); next != got {
		t.Fatalf("missing usage erased baseline: %+v != %+v", next, got)
	}
	if next := counter.Estimate("different-model", messages, defs); next.Source != "estimate" || next.Tokens == got.Tokens {
		t.Fatalf("reused another model's usage: %+v", next)
	}
}

func TestContextCounterFallbackIncludesReasoningImagesAndDuplicateMessages(t *testing.T) {
	message := modeladapter.Message{Role: "assistant", Content: "small", ReasoningContent: strings.Repeat("thought ", 1000)}
	base := unmeasuredMessageTokens(message)
	if base < int64(len(message.ReasoningContent)) {
		t.Fatal("reasoning missing from fallback")
	}
	message.Images = []imagemedia.Reference{{SHA256: "image"}}
	if got := unmeasuredMessageTokens(message); got != base+16384 {
		t.Fatalf("image allowance = %d, want %d", got, base+16384)
	}
	counter := &contextTokenCounter{}
	counter.Observe("model", []modeladapter.Message{message}, nil, modeladapter.Completion{Usage: json.RawMessage(`{"prompt_tokens":5000,"completion_tokens":10}`)})
	got := counter.Estimate("model", []modeladapter.Message{message, message}, nil)
	if got.Tokens != 5000+unmeasuredMessageTokens(message) {
		t.Fatalf("duplicate message incorrectly counted as already measured: %+v", got)
	}
	message.Content = "changed"
	if got := counter.Estimate("model", []modeladapter.Message{message}, nil); got.Tokens != 5000+unmeasuredMessageTokens(message) {
		t.Fatalf("changed message omitted: %+v", got)
	}
}

func TestContextCounterSurvivesCheckpointAndResume(t *testing.T) {
	messages := []modeladapter.Message{{Role: "user", Content: "continue work"}}
	store := newThreadStateStore(t.TempDir(), "lct_token_test", t.TempDir(), "run", time.Now())
	store.TokenCounter = &contextTokenCounter{}
	store.TokenCounter.Observe("model", messages, nil, modeladapter.Completion{UsageSummary: model.LLMUsage{InputTokens: 1234}})
	if err := store.SaveCheckpoint("test", messages, false); err != nil {
		t.Fatal(err)
	}
	state, ok, err := loadThreadState(store.DataDir, store.ThreadID, store.ProjectPath)
	if err != nil || !ok {
		t.Fatalf("load = %v, %v", ok, err)
	}
	resumed, err := resumeContextFromThreadState(state)
	if err != nil {
		t.Fatal(err)
	}
	if got := resumed.TokenCounter.Estimate("model", resumed.ExactMessages, nil); got.Tokens != 1234 || got.Source != "provider" {
		t.Fatalf("resume lost measured usage: %+v", got)
	}
}

func TestPrepareContextUsesTokensAndPreservesRequestNotes(t *testing.T) {
	messages := []modeladapter.Message{
		{Role: "system", Content: "system"},
		{Role: "user", Content: "actual objective"},
		{Role: "assistant", Content: strings.Repeat("evidence ", 1000)},
	}
	tail := []modeladapter.Message{{Role: "user", Content: "transient harness instructions"}}
	opts := openRouterContextOptionsForProfileAndModel(openRouterContextProfileBalanced, "deepseek", "deepseek-flash")
	counter := &contextTokenCounter{}
	counter.Observe("deepseek-flash", messages, nil, modeladapter.Completion{UsageSummary: model.LLMUsage{InputTokens: 710_000}})
	packed, _, compacted, err := prepareContextRequest(counter, "deepseek-flash", messages, tail, nil, nil, opts)
	if err != nil || !compacted {
		t.Fatalf("high reported usage did not compact short text: compacted=%v err=%v", compacted, err)
	}
	if packed[1].Content != "actual objective" || packed[len(packed)-1].Content != tail[0].Content {
		t.Fatal("packing lost the active request or transient instructions")
	}
	if counter.InputTokens != 0 || counter.Estimate("deepseek-flash", packed, nil).Tokens >= 700_000 {
		t.Fatal("compaction retained the old usage baseline or still exceeds budget")
	}
	// A known low token count must win over even a very large byte count.
	messages[2].Content = strings.Repeat("large but already measured ", 120000)
	counter.Observe("deepseek-flash", messages, nil, modeladapter.Completion{UsageSummary: model.LLMUsage{InputTokens: 100_000}})
	if _, _, compacted, err := prepareContextRequest(counter, "deepseek-flash", messages, nil, nil, nil, opts); err != nil || compacted {
		t.Fatalf("ignored measured low usage: compacted=%v err=%v", compacted, err)
	}
}

func TestPrepareContextRejectsIrreducibleRequestAndSchemas(t *testing.T) {
	opts := openRouterContextOptionsForProfileAndModel(openRouterContextProfileBalanced, "deepseek", "deepseek-flash")
	for _, tt := range []struct {
		name    string
		message string
		schema  string
	}{
		{"request", strings.Repeat("x", 800_000), ""},
		{"schema", "small request", strings.Repeat("x", 800_000)},
	} {
		t.Run(tt.name, func(t *testing.T) {
			messages := []modeladapter.Message{{Role: "user", Content: tt.message}}
			defs := []modeladapter.ToolDefinition{{Function: modeladapter.FunctionSpec{Name: "tool", Description: tt.schema}}}
			if _, _, _, err := prepareContextRequest(&contextTokenCounter{}, "deepseek-flash", messages, nil, defs, nil, opts); err == nil {
				t.Fatal("oversized request was allowed through")
			}
		})
	}
}

func TestProviderTokenUsageTriggersLoopCompaction(t *testing.T) {
	isolateSkillHomes(t)
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "README.md"), []byte("small file"), 0600); err != nil {
		t.Fatal(err)
	}
	requests := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests++
		var body struct {
			Messages []modeladapter.Message `json:"messages"`
		}
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			t.Error(err)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		if requests == 1 {
			_ = json.NewEncoder(w).Encode(map[string]any{
				"model": "deepseek-flash",
				"usage": map[string]int{"prompt_tokens": 710000, "completion_tokens": 100},
				"choices": []any{map[string]any{"finish_reason": "tool_calls", "message": map[string]any{
					"role": "assistant", "reasoning_content": strings.Repeat("retained reasoning ", 1000),
					"tool_calls": []any{harnessTestCall("read_file", map[string]any{"path": "README.md"})},
				}}},
			})
			return
		}
		found := false
		for _, message := range body.Messages {
			if strings.Contains(message.Content, loopCompactedContextPrefix) {
				found = true
			}
			if message.Role == "tool" {
				t.Error("raw tool history survived required compaction")
			}
		}
		if !found {
			t.Error("provider usage did not trigger compaction")
		}
		_, _ = w.Write([]byte(`{"model":"deepseek-flash","usage":{"prompt_tokens":35000,"completion_tokens":100},"choices":[{"finish_reason":"stop","message":{"role":"assistant","content":"Done."}}]}`))
	}))
	defer server.Close()
	t.Setenv("OPENROUTER_API_KEY", "test-key")
	t.Setenv("OPENROUTER_BASE_URL", server.URL)
	var stdout, stderr bytes.Buffer
	code := Run([]string{"exec", "--cwd", root, "--data-dir", t.TempDir(), "--provider", "openrouter", "--model", "deepseek/deepseek-flash", "--auto", "off", "--output", "stream-json", "--max-turns", "3", "Inspect README."}, &stdout, &stderr)
	if code != 0 || requests != 2 {
		t.Fatalf("code=%d requests=%d stderr=%s", code, requests, stderr.String())
	}
	for _, want := range []string{`"type":"context_compacted"`, `"context_tokens":710100`, `"context_tokens":35100`} {
		if !strings.Contains(stdout.String(), want) {
			t.Errorf("missing trace evidence %s", want)
		}
	}
}
