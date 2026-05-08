package modeladapter

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestLoadEnvFileDoesNotOverrideExistingEnv(t *testing.T) {
	t.Setenv("OPENROUTER_API_KEY", "existing")
	path := filepath.Join(t.TempDir(), ".env")
	if err := os.WriteFile(path, []byte("OPENROUTER_API_KEY=file\nOPENROUTER_BASE_URL=http://example.test\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := LoadEnvFile(path); err != nil {
		t.Fatal(err)
	}
	if got := os.Getenv("OPENROUTER_API_KEY"); got != "existing" {
		t.Fatalf("OPENROUTER_API_KEY = %q", got)
	}
	if got := os.Getenv("OPENROUTER_BASE_URL"); got != "http://example.test" {
		t.Fatalf("OPENROUTER_BASE_URL = %q", got)
	}
}

func TestToolsExposeReadOnlyInspectionTools(t *testing.T) {
	names := map[string]bool{}
	for _, tool := range Tools() {
		names[tool.Function.Name] = true
	}
	for _, want := range []string{"read_file", "list_files", "search", "load_skill", "run_command", "apply_patch", "update_plan", "final_response"} {
		if !names[want] {
			t.Fatalf("Tools() missing %s", want)
		}
	}
}

func TestSystemPromptIncludesSkillMetadata(t *testing.T) {
	prompt := SystemPrompt("Available skills\n- demo [project]: Demo workflow")
	if !strings.Contains(prompt, "call load_skill") || !strings.Contains(prompt, "demo [project]") {
		t.Fatalf("prompt missing skill guidance:\n%s", prompt)
	}
}

func TestOpenRouterClientSendsToolRequestAndParsesResponse(t *testing.T) {
	var gotModel string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/chat/completions" {
			t.Fatalf("path = %s", r.URL.Path)
		}
		if r.Header.Get("Authorization") != "Bearer key" {
			t.Fatalf("Authorization = %q", r.Header.Get("Authorization"))
		}
		var req struct {
			Model string           `json:"model"`
			Tools []ToolDefinition `json:"tools"`
		}
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			t.Fatal(err)
		}
		gotModel = req.Model
		if len(req.Tools) == 0 {
			t.Fatal("tools empty")
		}
		_, _ = w.Write([]byte(`{"choices":[{"message":{"role":"assistant","tool_calls":[{"id":"call_1","type":"function","function":{"name":"run_command","arguments":"{\"command\":\"pwd\"}"}}]}}]}`))
	}))
	defer server.Close()

	client, err := NewOpenRouterClient(OpenRouterConfig{
		APIKey:  "key",
		BaseURL: server.URL,
		Model:   "deepseek/deepseek-v4-pro",
	})
	if err != nil {
		t.Fatal(err)
	}
	completion, err := client.Complete(context.Background(), []Message{{Role: "user", Content: "hi"}}, Tools())
	if err != nil {
		t.Fatal(err)
	}
	if gotModel != "deepseek/deepseek-v4-pro" {
		t.Fatalf("model = %q", gotModel)
	}
	msg := completion.Message
	if len(msg.ToolCalls) != 1 || msg.ToolCalls[0].Function.Name != "run_command" {
		t.Fatalf("tool calls = %#v", msg.ToolCalls)
	}
	args, err := NormalizeArguments(msg.ToolCalls[0].Function.Arguments)
	if err != nil {
		t.Fatal(err)
	}
	if string(args) != `{"command":"pwd"}` {
		t.Fatalf("args = %s", args)
	}
}

func TestOpenRouterClientReportsHTTPStatusForNonJSONError(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, "upstream temporarily unavailable", http.StatusBadGateway)
	}))
	defer server.Close()

	client, err := NewOpenRouterClient(OpenRouterConfig{
		APIKey:  "key",
		BaseURL: server.URL,
		Model:   "deepseek/deepseek-v4-pro",
	})
	if err != nil {
		t.Fatal(err)
	}
	_, err = client.Complete(context.Background(), []Message{{Role: "user", Content: "hi"}}, Tools())
	if err == nil {
		t.Fatal("expected OpenRouter HTTP error")
	}
	if got := err.Error(); !strings.Contains(got, "HTTP 502") || !strings.Contains(got, "upstream temporarily unavailable") {
		t.Fatalf("error = %q, want status and body snippet", got)
	}
}
