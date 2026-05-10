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
	"time"
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
	descriptions := map[string]string{}
	for _, tool := range Tools() {
		names[tool.Function.Name] = true
		descriptions[tool.Function.Name] = tool.Function.Description
	}
	for _, want := range []string{"read_file", "file_outline", "module_outline", "list_files", "search", "load_skill", "run_command", "apply_patch", "update_plan", "final_response"} {
		if !names[want] {
			t.Fatalf("Tools() missing %s", want)
		}
	}
	if !strings.Contains(descriptions["apply_patch"], "*** Update File: README.md") {
		t.Fatalf("apply_patch description missing format example: %q", descriptions["apply_patch"])
	}
	var readFilePathDescription string
	for _, tool := range Tools() {
		if tool.Function.Name != "read_file" {
			continue
		}
		properties, _ := tool.Function.Parameters["properties"].(map[string]any)
		pathSpec, _ := properties["path"].(map[string]any)
		readFilePathDescription, _ = pathSpec["description"].(string)
	}
	if !strings.Contains(readFilePathDescription, "Workspace-relative") {
		t.Fatalf("read_file path description should mention relative paths: %q", readFilePathDescription)
	}
	if !strings.Contains(descriptions["file_outline"], "line ranges") {
		t.Fatalf("file_outline description should mention line ranges: %q", descriptions["file_outline"])
	}
	if !strings.Contains(descriptions["module_outline"], "many Go or Markdown files") {
		t.Fatalf("module_outline description should mention many files: %q", descriptions["module_outline"])
	}
}

func TestSystemPromptIncludesSkillMetadata(t *testing.T) {
	prompt := SystemPrompt("Available skills\n- demo [project]: Demo workflow", "Project instructions from AGENTS.md:\nRun tests.")
	if !strings.Contains(prompt, "call load_skill") || !strings.Contains(prompt, "demo [project]") || !strings.Contains(prompt, "Run tests.") || !strings.Contains(prompt, "*** Update File: path") || !strings.Contains(prompt, "workspace-relative paths") || !strings.Contains(prompt, "workspace-only") || !strings.Contains(prompt, "structured tool_calls") || !strings.Contains(prompt, "prefer file_outline") || !strings.Contains(prompt, "prefer module_outline") || !strings.Contains(prompt, "next_offset") || !strings.Contains(prompt, "summary must contain the full answer") {
		t.Fatalf("prompt missing skill guidance:\n%s", prompt)
	}
}

func TestSanitizeAssistantContentStripsProviderToolMarkup(t *testing.T) {
	content := "No filename matches.\n\n<\uff5cDSML\uff5ctool_calls><\uff5cDSML\uff5cinvoke name=\"run_command\">"
	got, stripped := SanitizeAssistantContent(content)
	if !stripped {
		t.Fatal("SanitizeAssistantContent() stripped = false, want true")
	}
	if got != "No filename matches." {
		t.Fatalf("sanitized content = %q", got)
	}
	if !ContainsProviderToolMarkup(content) {
		t.Fatal("ContainsProviderToolMarkup() = false, want true")
	}
	if clean, stripped := SanitizeAssistantContent("plain answer"); stripped || clean != "plain answer" {
		t.Fatalf("plain sanitize = %q/%v", clean, stripped)
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
		_, _ = w.Write([]byte(`{"model":"deepseek/deepseek-v4-pro","choices":[{"message":{"role":"assistant","tool_calls":[{"id":"call_1","type":"function","function":{"name":"run_command","arguments":"{\"command\":\"pwd\"}"}}]}}],"usage":{"prompt_tokens":1200,"prompt_tokens_details":{"cached_tokens":300},"completion_tokens":40,"completion_tokens_details":{"reasoning_tokens":7},"total_tokens":1240,"cost":0.0123}}`))
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
	if completion.UsageSummary.InputTokens != 1200 || completion.UsageSummary.OutputTokens != 40 || completion.UsageSummary.TotalTokens != 1240 || completion.UsageSummary.CachedInputTokens != 300 || completion.UsageSummary.ReasoningTokens != 7 {
		t.Fatalf("UsageSummary = %+v", completion.UsageSummary)
	}
	if completion.UsageSummary.EstimatedCostUSD != 0.0123 {
		t.Fatalf("EstimatedCostUSD = %f, want provider cost", completion.UsageSummary.EstimatedCostUSD)
	}
}

func TestOpenRouterClientSendsCompletionOptions(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var req struct {
			MaxCompletionTokens int `json:"max_completion_tokens"`
			Reasoning           struct {
				MaxTokens int `json:"max_tokens"`
			} `json:"reasoning"`
		}
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			t.Fatal(err)
		}
		if req.MaxCompletionTokens != 2000 {
			t.Fatalf("max_completion_tokens = %d, want 2000", req.MaxCompletionTokens)
		}
		if req.Reasoning.MaxTokens != 1024 {
			t.Fatalf("reasoning = %+v, want max_tokens 1024", req.Reasoning)
		}
		_, _ = w.Write([]byte(`{"choices":[{"finish_reason":"stop","message":{"role":"assistant","content":"done"}}]}`))
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
	_, err = client.CompleteWithOptions(context.Background(), []Message{{Role: "user", Content: "hi"}}, nil, CompletionOptions{
		MaxCompletionTokens: 2000,
		ReasoningMaxTokens:  1024,
	})
	if err != nil {
		t.Fatal(err)
	}
}

func TestOpenRouterClientSendsProviderRoutingAndTemperature(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var req struct {
			Temperature float64 `json:"temperature"`
			Provider    struct {
				Only              []string `json:"only"`
				AllowFallbacks    bool     `json:"allow_fallbacks"`
				RequireParameters bool     `json:"require_parameters"`
			} `json:"provider"`
		}
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			t.Fatal(err)
		}
		if req.Temperature != 0.4 {
			t.Fatalf("temperature = %f, want 0.4", req.Temperature)
		}
		if strings.Join(req.Provider.Only, ",") != "anthropic,minimax" {
			t.Fatalf("provider.only = %#v", req.Provider.Only)
		}
		if req.Provider.AllowFallbacks {
			t.Fatalf("provider.allow_fallbacks = true, want false")
		}
		if !req.Provider.RequireParameters {
			t.Fatalf("provider.require_parameters = false, want true")
		}
		_, _ = w.Write([]byte(`{"choices":[{"finish_reason":"stop","message":{"role":"assistant","content":"done"}}]}`))
	}))
	defer server.Close()

	temperature := 0.4
	client, err := NewOpenRouterClient(OpenRouterConfig{
		APIKey:       "key",
		BaseURL:      server.URL,
		Model:        "anthropic/claude-sonnet-4.6",
		Temperature:  &temperature,
		ProviderOnly: []string{"anthropic", "", "minimax"},
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := client.Complete(context.Background(), []Message{{Role: "user", Content: "hi"}}, nil); err != nil {
		t.Fatal(err)
	}
}

func TestOpenAIClientUsesDirectEndpointShape(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/responses" {
			t.Fatalf("path = %s", r.URL.Path)
		}
		if r.Header.Get("Authorization") != "Bearer openai-key" {
			t.Fatalf("Authorization = %q", r.Header.Get("Authorization"))
		}
		if got := r.Header.Get("HTTP-Referer"); got != "" {
			t.Fatalf("OpenAI request should not send OpenRouter referer header: %q", got)
		}
		var req struct {
			Model               string `json:"model"`
			MaxOutputTokens     int    `json:"max_output_tokens"`
			MaxCompletionTokens int    `json:"max_completion_tokens"`
			ReasoningEffort     string `json:"reasoning_effort"`
			Temperature         *float64
			Reasoning           struct {
				Effort string `json:"effort"`
			} `json:"reasoning"`
			Tools []struct {
				Type        string         `json:"type"`
				Name        string         `json:"name"`
				Description string         `json:"description"`
				Parameters  map[string]any `json:"parameters"`
			} `json:"tools"`
			ToolChoice         string           `json:"tool_choice"`
			Instructions       string           `json:"instructions"`
			Input              []map[string]any `json:"input"`
			PreviousResponseID string           `json:"previous_response_id"`
		}
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			t.Fatal(err)
		}
		if req.Model != "gpt-5.5" {
			t.Fatalf("model = %q", req.Model)
		}
		if req.MaxOutputTokens != 777 || req.MaxCompletionTokens != 0 {
			t.Fatalf("token fields max_output_tokens=%d max_completion_tokens=%d", req.MaxOutputTokens, req.MaxCompletionTokens)
		}
		if req.Reasoning.Effort != "low" || req.ReasoningEffort != "" {
			t.Fatalf("reasoning fields reasoning=%+v reasoning_effort=%q", req.Reasoning, req.ReasoningEffort)
		}
		if req.Temperature != nil {
			t.Fatalf("openai request should omit temperature, got %f", *req.Temperature)
		}
		if len(req.Tools) == 0 || req.Tools[0].Type != "function" || req.Tools[0].Name == "" {
			t.Fatalf("tools = %#v", req.Tools)
		}
		if req.ToolChoice != "auto" {
			t.Fatalf("tool_choice = %q", req.ToolChoice)
		}
		if !strings.Contains(req.Instructions, "system guidance") {
			t.Fatalf("instructions = %q", req.Instructions)
		}
		if len(req.Input) != 1 || req.Input[0]["role"] != "user" || req.Input[0]["content"] != "hi" {
			t.Fatalf("input = %#v", req.Input)
		}
		if req.PreviousResponseID != "" {
			t.Fatalf("previous_response_id = %q", req.PreviousResponseID)
		}
		_, _ = w.Write([]byte(`{
			"id":"openai_resp",
			"model":"gpt-5.5",
			"status":"completed",
			"output":[{"type":"function_call","call_id":"call_1","name":"search","arguments":"{\"query\":\"needle\"}"}],
			"usage":{"input_tokens":1000,"input_tokens_details":{"cached_tokens":250},"output_tokens":20,"output_tokens_details":{"reasoning_tokens":3},"total_tokens":1020}
		}`))
	}))
	defer server.Close()

	client, err := NewOpenAIClient(OpenRouterConfig{
		APIKey:  "openai-key",
		BaseURL: server.URL,
		Model:   "gpt-5.5",
	})
	if err != nil {
		t.Fatal(err)
	}
	completion, err := client.CompleteWithOptions(context.Background(), []Message{{Role: "system", Content: "system guidance"}, {Role: "user", Content: "hi"}}, Tools(), CompletionOptions{
		MaxCompletionTokens: 777,
		ReasoningEffort:     "low",
	})
	if err != nil {
		t.Fatal(err)
	}
	if completion.ID != "openai_resp" || completion.Model != "gpt-5.5" {
		t.Fatalf("completion identity = %q %q", completion.ID, completion.Model)
	}
	if completion.UsageSummary.InputTokens != 1000 || completion.UsageSummary.CachedInputTokens != 250 || completion.UsageSummary.OutputTokens != 20 || completion.UsageSummary.ReasoningTokens != 3 {
		t.Fatalf("UsageSummary = %+v", completion.UsageSummary)
	}
	msg := completion.Message
	if len(msg.ToolCalls) != 1 || msg.ToolCalls[0].Function.Name != "search" {
		t.Fatalf("tool calls = %#v", msg.ToolCalls)
	}
	args, err := NormalizeArguments(msg.ToolCalls[0].Function.Arguments)
	if err != nil {
		t.Fatal(err)
	}
	if string(args) != `{"query":"needle"}` {
		t.Fatalf("args = %s", args)
	}
}

func TestOpenAIClientResponsesContinuationUsesPreviousResponseID(t *testing.T) {
	requests := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests++
		var req struct {
			Instructions       string           `json:"instructions"`
			Input              []map[string]any `json:"input"`
			PreviousResponseID string           `json:"previous_response_id"`
			Tools              []struct {
				Name string `json:"name"`
			} `json:"tools"`
		}
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			t.Fatal(err)
		}
		switch requests {
		case 1:
			if req.PreviousResponseID != "" {
				t.Fatalf("first previous_response_id = %q", req.PreviousResponseID)
			}
			if !strings.Contains(req.Instructions, "system guidance") {
				t.Fatalf("first instructions = %q", req.Instructions)
			}
			if len(req.Input) != 1 || req.Input[0]["role"] != "user" {
				t.Fatalf("first input = %#v", req.Input)
			}
			_, _ = w.Write([]byte(`{"id":"resp_1","model":"gpt-5.5","status":"completed","output":[{"type":"function_call","call_id":"call_1","name":"search","arguments":"{\"query\":\"needle\"}"}]}`))
		case 2:
			if req.PreviousResponseID != "resp_1" {
				t.Fatalf("second previous_response_id = %q", req.PreviousResponseID)
			}
			if req.Instructions != "" {
				t.Fatalf("second instructions = %q, want omitted for previous_response_id continuation", req.Instructions)
			}
			if len(req.Input) != 2 {
				t.Fatalf("second input = %#v", req.Input)
			}
			if req.Input[0]["type"] != "function_call_output" || req.Input[0]["call_id"] != "call_1" {
				t.Fatalf("second tool output input = %#v", req.Input)
			}
			if req.Input[1]["role"] != "user" || !strings.Contains(req.Input[1]["content"].(string), "continue") {
				t.Fatalf("second user input = %#v", req.Input)
			}
			_, _ = w.Write([]byte(`{"id":"resp_2","model":"gpt-5.5","status":"completed","output":[{"type":"message","role":"assistant","content":[{"type":"output_text","text":"done"}]}]}`))
		default:
			t.Fatalf("unexpected request %d", requests)
		}
	}))
	defer server.Close()

	client, err := NewOpenAIClient(OpenRouterConfig{
		APIKey:  "openai-key",
		BaseURL: server.URL,
		Model:   "gpt-5.5",
	})
	if err != nil {
		t.Fatal(err)
	}
	messages := []Message{{Role: "system", Content: "system guidance"}, {Role: "user", Content: "hi"}}
	first, err := client.CompleteWithOptions(context.Background(), messages, Tools(), CompletionOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if len(first.Message.ToolCalls) != 1 {
		t.Fatalf("first tool calls = %#v", first.Message.ToolCalls)
	}
	messages = append(messages, first.Message, Message{Role: "tool", ToolCallID: "call_1", Content: `{"ok":true}`}, Message{Role: "user", Content: "continue"})
	second, err := client.CompleteWithOptions(context.Background(), messages, Tools(), CompletionOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if second.Message.Content != "done" {
		t.Fatalf("second content = %q", second.Message.Content)
	}
}

func TestOpenAIClientResponsesCompactedContextDoesNotUsePreviousResponseID(t *testing.T) {
	requests := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests++
		var req struct {
			Instructions       string           `json:"instructions"`
			Input              []map[string]any `json:"input"`
			PreviousResponseID string           `json:"previous_response_id"`
		}
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			t.Fatal(err)
		}
		switch requests {
		case 1:
			_, _ = w.Write([]byte(`{"id":"resp_1","model":"gpt-5.5","status":"completed","output":[{"type":"function_call","call_id":"call_1","name":"search","arguments":"{\"query\":\"needle\"}"}]}`))
		case 2:
			if req.PreviousResponseID != "" {
				t.Fatalf("previous_response_id = %q, want omitted for compacted standalone context", req.PreviousResponseID)
			}
			if !strings.Contains(req.Instructions, "system guidance") {
				t.Fatalf("instructions = %q", req.Instructions)
			}
			if len(req.Input) != 2 || req.Input[0]["content"] != "original request" || req.Input[1]["content"] != "compacted transcript" {
				t.Fatalf("input = %#v", req.Input)
			}
			_, _ = w.Write([]byte(`{"id":"resp_2","model":"gpt-5.5","status":"completed","output":[{"type":"message","role":"assistant","content":[{"type":"output_text","text":"done"}]}]}`))
		default:
			t.Fatalf("unexpected request %d", requests)
		}
	}))
	defer server.Close()

	client, err := NewOpenAIClient(OpenRouterConfig{
		APIKey:  "openai-key",
		BaseURL: server.URL,
		Model:   "gpt-5.5",
	})
	if err != nil {
		t.Fatal(err)
	}
	_, err = client.CompleteWithOptions(context.Background(), []Message{{Role: "system", Content: "system guidance"}, {Role: "user", Content: "hi"}}, Tools(), CompletionOptions{})
	if err != nil {
		t.Fatal(err)
	}
	second, err := client.CompleteWithOptions(context.Background(), []Message{{Role: "system", Content: "system guidance"}, {Role: "user", Content: "original request"}, {Role: "user", Content: "compacted transcript"}}, Tools(), CompletionOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if second.Message.Content != "done" {
		t.Fatalf("second content = %q", second.Message.Content)
	}
}

func TestOpenAIClientDefaultsFromEnv(t *testing.T) {
	t.Setenv("OPENAI_API_KEY", "key")
	t.Setenv("OPENAI_BASE_URL", "https://example.openai.test")
	client, err := NewOpenAIClient(OpenRouterConfig{})
	if err != nil {
		t.Fatal(err)
	}
	if client.Model() != DefaultOpenAIModel {
		t.Fatalf("Model() = %q", client.Model())
	}
}

func TestDeepSeekClientUsesDirectEndpointShape(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/chat/completions" {
			t.Fatalf("path = %s", r.URL.Path)
		}
		if r.Header.Get("Authorization") != "Bearer deepseek-key" {
			t.Fatalf("Authorization = %q", r.Header.Get("Authorization"))
		}
		if got := r.Header.Get("HTTP-Referer"); got != "" {
			t.Fatalf("DeepSeek request should not send OpenRouter referer header: %q", got)
		}
		var req struct {
			Model     string `json:"model"`
			MaxTokens int    `json:"max_tokens"`
			OpenAI    int    `json:"max_completion_tokens"`
			Thinking  struct {
				Type string `json:"type"`
			} `json:"thinking"`
			Tools []ToolDefinition `json:"tools"`
		}
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			t.Fatal(err)
		}
		if req.Model != "deepseek-v4-pro" {
			t.Fatalf("model = %q", req.Model)
		}
		if req.MaxTokens != 777 || req.OpenAI != 0 {
			t.Fatalf("token fields max_tokens=%d max_completion_tokens=%d", req.MaxTokens, req.OpenAI)
		}
		if req.Thinking.Type != "disabled" {
			t.Fatalf("thinking.type = %q, want disabled", req.Thinking.Type)
		}
		if len(req.Tools) == 0 {
			t.Fatal("tools empty")
		}
		_, _ = w.Write([]byte(`{
			"id":"deepseek_resp",
			"model":"deepseek-v4-pro",
			"choices":[{
				"finish_reason":"tool_calls",
				"message":{"role":"assistant","tool_calls":[{"id":"call_1","type":"function","function":{"name":"search","arguments":"{\"query\":\"needle\"}"}}]}
			}],
			"usage":{"prompt_tokens":1000,"prompt_cache_hit_tokens":250,"completion_tokens":20,"completion_tokens_details":{"reasoning_tokens":3},"total_tokens":1020}
		}`))
	}))
	defer server.Close()

	client, err := NewDeepSeekClient(OpenRouterConfig{
		APIKey:  "deepseek-key",
		BaseURL: server.URL,
		Model:   "deepseek-v4-pro",
	})
	if err != nil {
		t.Fatal(err)
	}
	completion, err := client.CompleteWithOptions(context.Background(), []Message{{Role: "user", Content: "hi"}}, Tools(), CompletionOptions{
		MaxCompletionTokens: 777,
		DisableThinking:     true,
	})
	if err != nil {
		t.Fatal(err)
	}
	if completion.ID != "deepseek_resp" || completion.Model != "deepseek-v4-pro" {
		t.Fatalf("completion identity = %q %q", completion.ID, completion.Model)
	}
	if completion.UsageSummary.InputTokens != 1000 || completion.UsageSummary.CachedInputTokens != 250 || completion.UsageSummary.OutputTokens != 20 || completion.UsageSummary.ReasoningTokens != 3 {
		t.Fatalf("UsageSummary = %+v", completion.UsageSummary)
	}
}

func TestMoonshotClientUsesDirectEndpointShape(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/chat/completions" {
			t.Fatalf("path = %s", r.URL.Path)
		}
		if r.Header.Get("Authorization") != "Bearer moonshot-key" {
			t.Fatalf("Authorization = %q", r.Header.Get("Authorization"))
		}
		if got := r.Header.Get("HTTP-Referer"); got != "" {
			t.Fatalf("Moonshot request should not send OpenRouter referer header: %q", got)
		}
		var req struct {
			Model               string   `json:"model"`
			MaxCompletionTokens int      `json:"max_completion_tokens"`
			MaxTokens           int      `json:"max_tokens"`
			Temperature         *float64 `json:"temperature"`
			Thinking            struct {
				Type string `json:"type"`
			} `json:"thinking"`
			Tools    []ToolDefinition `json:"tools"`
			Messages []Message        `json:"messages"`
		}
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			t.Fatal(err)
		}
		if req.Model != "kimi-k2.6" {
			t.Fatalf("model = %q", req.Model)
		}
		if req.MaxCompletionTokens != 777 || req.MaxTokens != 0 {
			t.Fatalf("token fields max_completion_tokens=%d max_tokens=%d", req.MaxCompletionTokens, req.MaxTokens)
		}
		if req.Temperature != nil {
			t.Fatalf("moonshot request should omit temperature, got %f", *req.Temperature)
		}
		if req.Thinking.Type != "disabled" {
			t.Fatalf("thinking.type = %q, want disabled", req.Thinking.Type)
		}
		if len(req.Tools) == 0 {
			t.Fatal("tools empty")
		}
		if len(req.Messages) == 0 || req.Messages[0].ReasoningContent != "keep me" {
			t.Fatalf("reasoning content was not preserved in request: %#v", req.Messages)
		}
		_, _ = w.Write([]byte(`{
			"id":"moonshot_resp",
			"model":"kimi-k2.6",
			"choices":[{
				"finish_reason":"tool_calls",
				"message":{
					"role":"assistant",
					"reasoning_content":"internal reasoning to preserve",
					"tool_calls":[{"id":"call_1","type":"function","function":{"name":"search","arguments":"{\"query\":\"needle\"}"}}]
				}
			}],
			"usage":{"prompt_tokens":1000,"cached_tokens":250,"completion_tokens":20,"completion_tokens_details":{"reasoning_tokens":3},"total_tokens":1020}
		}`))
	}))
	defer server.Close()

	client, err := NewMoonshotClient(OpenRouterConfig{
		APIKey:  "moonshot-key",
		BaseURL: server.URL,
		Model:   "kimi-k2.6",
	})
	if err != nil {
		t.Fatal(err)
	}
	completion, err := client.CompleteWithOptions(context.Background(), []Message{{Role: "assistant", ReasoningContent: "keep me"}}, Tools(), CompletionOptions{
		MaxCompletionTokens: 777,
		DisableThinking:     true,
	})
	if err != nil {
		t.Fatal(err)
	}
	if completion.ID != "moonshot_resp" || completion.Model != "kimi-k2.6" {
		t.Fatalf("completion identity = %q %q", completion.ID, completion.Model)
	}
	if completion.Message.ReasoningContent != "internal reasoning to preserve" {
		t.Fatalf("ReasoningContent = %q", completion.Message.ReasoningContent)
	}
	if completion.UsageSummary.InputTokens != 1000 || completion.UsageSummary.CachedInputTokens != 250 || completion.UsageSummary.OutputTokens != 20 || completion.UsageSummary.ReasoningTokens != 3 {
		t.Fatalf("UsageSummary = %+v", completion.UsageSummary)
	}
}

func TestMoonshotClientDefaultsFromEnv(t *testing.T) {
	t.Setenv("MOONSHOT_API_KEY", "key")
	t.Setenv("MOONSHOT_BASE_URL", "https://example.moonshot.test")
	client, err := NewMoonshotClient(OpenRouterConfig{})
	if err != nil {
		t.Fatal(err)
	}
	if client.Model() != DefaultMoonshotModel {
		t.Fatalf("Model() = %q", client.Model())
	}
}

func TestDeepSeekClientDefaultsFromEnv(t *testing.T) {
	t.Setenv("DEEPSEEK_API_KEY", "key")
	t.Setenv("DEEPSEEK_BASE_URL", "https://example.deepseek.test")
	client, err := NewDeepSeekClient(OpenRouterConfig{})
	if err != nil {
		t.Fatal(err)
	}
	if client.Model() != DefaultDeepSeekModel {
		t.Fatalf("Model() = %q", client.Model())
	}
}

func TestOpenRouterClientRejectsConflictingReasoningOptions(t *testing.T) {
	client, err := NewOpenRouterClient(OpenRouterConfig{
		APIKey:  "key",
		BaseURL: "http://127.0.0.1:1",
		Model:   "deepseek/deepseek-v4-pro",
	})
	if err != nil {
		t.Fatal(err)
	}
	_, err = client.CompleteWithOptions(context.Background(), []Message{{Role: "user", Content: "hi"}}, nil, CompletionOptions{
		ReasoningMaxTokens: 1024,
		ReasoningEffort:    "low",
	})
	if err == nil || !strings.Contains(err.Error(), "mutually exclusive") {
		t.Fatalf("err = %v, want mutually exclusive reasoning options", err)
	}
}

func TestUsageFromRawSupportsResponsesShape(t *testing.T) {
	usage := UsageFromRaw(json.RawMessage(`{
		"input_tokens":345,
		"input_tokens_details":{"cached_tokens":21},
		"output_tokens":67,
		"output_tokens_details":{"reasoning_tokens":5},
		"total_tokens":412
	}`), "unknown-model")
	if usage.InputTokens != 345 || usage.OutputTokens != 67 || usage.TotalTokens != 412 || usage.CachedInputTokens != 21 || usage.ReasoningTokens != 5 {
		t.Fatalf("UsageFromRaw() = %+v", usage)
	}
}

func TestOpenRouterClientDefaultsMaxTurns(t *testing.T) {
	t.Setenv("OPENROUTER_API_KEY", "key")
	client, err := NewOpenRouterClient(OpenRouterConfig{})
	if err != nil {
		t.Fatal(err)
	}
	if got := client.MaxTurns(); got != DefaultOpenRouterMaxTurns {
		t.Fatalf("MaxTurns() = %d, want %d", got, DefaultOpenRouterMaxTurns)
	}
	if got := (*Client)(nil).MaxTurns(); got != DefaultOpenRouterMaxTurns {
		t.Fatalf("nil MaxTurns() = %d, want %d", got, DefaultOpenRouterMaxTurns)
	}
}

func TestOpenRouterClientUsesConfiguredRequestTimeout(t *testing.T) {
	client, err := NewOpenRouterClient(OpenRouterConfig{
		APIKey:         "key",
		RequestTimeout: 7 * time.Minute,
	})
	if err != nil {
		t.Fatal(err)
	}
	if client.httpClient == nil || client.httpClient.Timeout != 7*time.Minute {
		t.Fatalf("Timeout = %v, want 7m", client.httpClient.Timeout)
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
