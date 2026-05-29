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
	for _, want := range []string{"read_file", "file_outline", "module_outline", "repo_overview", "list_files", "search", "scout_files", "load_skill", "run_command", "apply_patch", "replace_text", "update_plan", "final_response"} {
		if !names[want] {
			t.Fatalf("Tools() missing %s", want)
		}
	}
	if !strings.Contains(descriptions["apply_patch"], "*** Update File: README.md") {
		t.Fatalf("apply_patch description missing format example: %q", descriptions["apply_patch"])
	}
	if !strings.Contains(descriptions["replace_text"], "exact literal text span") || !strings.Contains(descriptions["replace_text"], "not regex-based") {
		t.Fatalf("replace_text description missing exact-match guidance: %q", descriptions["replace_text"])
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
	if !strings.Contains(readFilePathDescription, "absolute path") || !strings.Contains(readFilePathDescription, "read-only inspection") {
		t.Fatalf("read_file path description should mention absolute read-only inspection: %q", readFilePathDescription)
	}
	if !strings.Contains(descriptions["file_outline"], "line ranges") {
		t.Fatalf("file_outline description should mention line ranges: %q", descriptions["file_outline"])
	}
	if !strings.Contains(descriptions["module_outline"], "many supported source or Markdown files") {
		t.Fatalf("module_outline description should mention many files: %q", descriptions["module_outline"])
	}
	if !strings.Contains(descriptions["repo_overview"], "deterministic repository overview") {
		t.Fatalf("repo_overview description should mention deterministic overview: %q", descriptions["repo_overview"])
	}
	if !strings.Contains(descriptions["search"], "literal substring") || !strings.Contains(descriptions["search"], "not a regex") {
		t.Fatalf("search description should explain literal matching: %q", descriptions["search"])
	}
	if !strings.Contains(descriptions["scout_files"], "utility/scout model") || !strings.Contains(descriptions["scout_files"], "read-only") {
		t.Fatalf("scout_files description should explain utility model routing: %q", descriptions["scout_files"])
	}
	if !strings.Contains(descriptions["update_plan"], "continue with the in_progress step") {
		t.Fatalf("update_plan description should keep plans tied to execution: %q", descriptions["update_plan"])
	}
	if names["web_search"] {
		t.Fatalf("Tools() should not expose web_search unless it is enabled")
	}
	for _, processTool := range []string{"start_process", "list_processes", "stop_process"} {
		if names[processTool] {
			t.Fatalf("Tools() should not expose %s outside managed-process sessions", processTool)
		}
	}
}

func TestToolsWithOptionsExposeWebSearchWhenEnabled(t *testing.T) {
	spec := toolSpec(t, ToolsWithOptions(ToolOptions{WebSearchEnabled: true}), "web_search")
	if !strings.Contains(spec.Description, "public web") {
		t.Fatalf("web_search description = %q", spec.Description)
	}
	props := spec.Parameters["properties"].(map[string]any)
	if _, ok := props["query"]; !ok {
		t.Fatalf("web_search missing query property: %#v", props)
	}
}

func TestToolsWithOptionsExposeManagedProcessesWhenEnabled(t *testing.T) {
	tools := ToolsWithOptions(ToolOptions{ManagedProcessesEnabled: true})
	startSpec := toolSpec(t, tools, "start_process")
	if !strings.Contains(startSpec.Description, "long-running managed background process") {
		t.Fatalf("start_process description = %q", startSpec.Description)
	}
	startProps := startSpec.Parameters["properties"].(map[string]any)
	if _, ok := startProps["name"]; !ok {
		t.Fatalf("start_process missing name property: %#v", startProps)
	}
	_ = toolSpec(t, tools, "list_processes")
	stopSpec := toolSpec(t, tools, "stop_process")
	stopProps := stopSpec.Parameters["properties"].(map[string]any)
	if _, ok := stopProps["process_id"]; !ok {
		t.Fatalf("stop_process missing process_id property: %#v", stopProps)
	}
}

func TestToolsWithOptionsExposeConfiguredFileLimits(t *testing.T) {
	tools := ToolsWithOptions(ToolOptions{
		ToolProfile:             "generous",
		DefaultReadLineLimit:    400,
		MaxReadLineLimit:        2500,
		DefaultListEntryLimit:   400,
		MaxListEntryLimit:       2000,
		DefaultSearchMaxMatch:   100,
		MaxSearchMaxMatch:       500,
		MaxSearchContextLines:   16,
		DefaultOutlineFileLimit: 60,
		MaxOutlineFileLimit:     160,
	})

	spec := toolSpec(t, tools, "read_file")
	if !strings.Contains(spec.Description, "Larger read limits") {
		t.Fatalf("read_file description missing generous guidance: %q", spec.Description)
	}
	readProps := spec.Parameters["properties"].(map[string]any)
	limitSpec := readProps["limit"].(map[string]any)
	if got := limitSpec["maximum"]; got != 2500 {
		t.Fatalf("read_file max = %#v, want 2500", got)
	}
	if !strings.Contains(limitSpec["description"].(string), "Defaults to 400") {
		t.Fatalf("read_file limit description = %q", limitSpec["description"])
	}

	searchSpec := toolSpec(t, tools, "search")
	searchProps := searchSpec.Parameters["properties"].(map[string]any)
	querySpec := searchProps["query"].(map[string]any)
	if !strings.Contains(querySpec["description"].(string), "Do not use regex") {
		t.Fatalf("search query description = %q", querySpec["description"])
	}
	if got := searchProps["max_matches"].(map[string]any)["maximum"]; got != 500 {
		t.Fatalf("search max_matches max = %#v, want 500", got)
	}
	if got := searchProps["context_before"].(map[string]any)["maximum"]; got != 16 {
		t.Fatalf("search context max = %#v, want 16", got)
	}
	if _, ok := searchProps["intent"].(map[string]any); !ok {
		t.Fatalf("search missing intent property: %#v", searchProps)
	}
	outputMode := searchProps["output_mode"].(map[string]any)
	if !strings.Contains(outputMode["description"].(string), "compact") {
		t.Fatalf("search output_mode description = %q", outputMode["description"])
	}

	outlineSpec := toolSpec(t, tools, "module_outline")
	outlineProps := outlineSpec.Parameters["properties"].(map[string]any)
	if got := outlineProps["max_files"].(map[string]any)["maximum"]; got != 160 {
		t.Fatalf("module_outline max_files max = %#v, want 160", got)
	}

	overviewSpec := toolSpec(t, tools, "repo_overview")
	overviewProps := overviewSpec.Parameters["properties"].(map[string]any)
	if got := overviewProps["max_files"].(map[string]any)["maximum"]; got != 500 {
		t.Fatalf("repo_overview max_files max = %#v, want 500", got)
	}

	scoutSpec := toolSpec(t, tools, "scout_files")
	scoutProps := scoutSpec.Parameters["properties"].(map[string]any)
	if got := scoutProps["max_lines_per_file"].(map[string]any)["maximum"]; got != 2500 {
		t.Fatalf("scout_files max_lines_per_file max = %#v, want 2500", got)
	}
}

func TestToolsWithOptionsDescribeAdminWritePaths(t *testing.T) {
	tools := ToolsWithOptions(ToolOptions{AdminWrite: true})
	replaceSpec := toolSpec(t, tools, "replace_text")
	props := replaceSpec.Parameters["properties"].(map[string]any)
	pathSpec := props["path"].(map[string]any)
	if !strings.Contains(pathSpec["description"].(string), "admin-write") || !strings.Contains(pathSpec["description"].(string), "absolute path") {
		t.Fatalf("replace_text path description = %q", pathSpec["description"])
	}
	patchSpec := toolSpec(t, tools, "apply_patch")
	if !strings.Contains(patchSpec.Description, "admin-write mode is enabled") {
		t.Fatalf("apply_patch description = %q", patchSpec.Description)
	}
}

func TestSystemPromptIncludesSkillMetadata(t *testing.T) {
	prompt := SystemPrompt("Available skills\n- demo [project]: Demo workflow", "Project instructions from AGENTS.md:\nRun tests.")
	if !strings.Contains(prompt, "call load_skill") || !strings.Contains(prompt, "demo [project]") || !strings.Contains(prompt, "Run tests.") || !strings.Contains(prompt, "*** Update File: path") || !strings.Contains(prompt, "workspace-relative paths") || !strings.Contains(prompt, "absolute paths") || !strings.Contains(prompt, "read-only file inspection") || !strings.Contains(prompt, "workspace-only") || !strings.Contains(prompt, "structured tool_calls") || !strings.Contains(prompt, "prefer file_outline") || !strings.Contains(prompt, "prefer repo_overview") || !strings.Contains(prompt, "prefer module_outline") || !strings.Contains(prompt, "literal substrings") || !strings.Contains(prompt, "intent sentence") || !strings.Contains(prompt, "next_offset") || !strings.Contains(prompt, "summary must contain the full answer") || !strings.Contains(prompt, "toolchain probes") || !strings.Contains(prompt, "corepack enable") || !strings.Contains(prompt, "process group") || !strings.Contains(prompt, "long-running process is still running") {
		t.Fatalf("prompt missing skill guidance:\n%s", prompt)
	}
	if strings.Contains(prompt, "start_process") {
		t.Fatalf("default prompt should not mention managed-process tools when they are disabled:\n%s", prompt)
	}
}

func TestSystemPromptIncludesProactiveExecutionGuidance(t *testing.T) {
	prompt := SystemPrompt("", "")
	for _, want := range []string{
		"asks you to carry out a proposed plan or selected option",
		"start executing it within the current autonomy and tool policy",
		"continue with the in_progress step",
	} {
		if !strings.Contains(prompt, want) {
			t.Fatalf("prompt missing %q:\n%s", want, prompt)
		}
	}
}

func TestSystemPromptIncludesGenerousToolProfile(t *testing.T) {
	prompt := SystemPromptWithOptions("", "", SystemPromptOptions{
		ToolProfile:          "generous",
		DefaultReadLineLimit: 400,
		MaxReadLineLimit:     2500,
	})
	for _, want := range []string{"For this run", "defaults to 400 lines", "permits up to 2500 lines", "outline/search identifies a central file"} {
		if !strings.Contains(prompt, want) {
			t.Fatalf("prompt missing %q:\n%s", want, prompt)
		}
	}
	if strings.Contains(prompt, "tool profile") {
		t.Fatalf("prompt should not expose benchmark profile labels to the model:\n%s", prompt)
	}
}

func TestSystemPromptIncludesManagedProcessGuidanceWhenEnabled(t *testing.T) {
	prompt := SystemPromptWithOptions("", "", SystemPromptOptions{ManagedProcessesEnabled: true})
	for _, want := range []string{"call start_process first", "list_processes", "stop_process"} {
		if !strings.Contains(prompt, want) {
			t.Fatalf("managed-process prompt missing %q:\n%s", want, prompt)
		}
	}
}

func TestSystemPromptIncludesAdminWriteMode(t *testing.T) {
	prompt := SystemPromptWithOptions("", "", SystemPromptOptions{AdminWrite: true})
	if !strings.Contains(prompt, "admin-write enabled") || !strings.Contains(prompt, "absolute paths outside the workspace") {
		t.Fatalf("prompt missing admin-write guidance:\n%s", prompt)
	}
}

func toolSpec(t *testing.T, tools []ToolDefinition, name string) FunctionSpec {
	t.Helper()
	for _, tool := range tools {
		if tool.Function.Name == name {
			return tool.Function
		}
	}
	t.Fatalf("missing tool %s", name)
	return FunctionSpec{}
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

func TestOpenRouterClientListModels(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/models" {
			t.Fatalf("path = %s, want /models", r.URL.Path)
		}
		if r.Header.Get("Authorization") != "Bearer key" {
			t.Fatalf("Authorization = %q", r.Header.Get("Authorization"))
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"data":[{"id":"deepseek/deepseek-v4-pro","name":"DeepSeek V4 Pro","description":"coding route"},{"id":"openai/gpt-5.5"},{"id":"deepseek/deepseek-v4-pro"}]}`))
	}))
	defer server.Close()

	client, err := NewOpenRouterClient(OpenRouterConfig{
		APIKey:  "key",
		BaseURL: server.URL,
	})
	if err != nil {
		t.Fatal(err)
	}
	models, err := client.ListModels(context.Background())
	if err != nil {
		t.Fatalf("ListModels() error = %v", err)
	}
	if len(models) != 2 {
		t.Fatalf("ListModels() returned %d models, want 2: %#v", len(models), models)
	}
	if models[0].ID != "deepseek/deepseek-v4-pro" || models[0].Name != "DeepSeek V4 Pro" || models[0].Description != "coding route" {
		t.Fatalf("first model = %#v", models[0])
	}
	if models[1].ID != "openai/gpt-5.5" {
		t.Fatalf("second model = %#v", models[1])
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

func TestOpenRouterClientCanOmitTemperature(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var req map[string]any
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			t.Fatal(err)
		}
		if _, ok := req["temperature"]; ok {
			t.Fatalf("temperature should be omitted: %#v", req["temperature"])
		}
		_, _ = w.Write([]byte(`{"choices":[{"finish_reason":"stop","message":{"role":"assistant","content":"done"}}]}`))
	}))
	defer server.Close()

	client, err := NewOpenRouterClient(OpenRouterConfig{
		APIKey:          "key",
		BaseURL:         server.URL,
		Model:           "anthropic/claude-opus-4.7",
		OmitTemperature: true,
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := client.Complete(context.Background(), []Message{{Role: "system", Content: "stable system"}, {Role: "user", Content: "hi"}}, nil); err != nil {
		t.Fatal(err)
	}
}

func TestOpenRouterClientEnablesAnthropicPromptCacheForClaudeModels(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var req struct {
			Model        string        `json:"model"`
			CacheControl *CacheControl `json:"cache_control"`
			Messages     []struct {
				Role    string          `json:"role"`
				Content json.RawMessage `json:"content"`
			} `json:"messages"`
		}
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			t.Fatal(err)
		}
		if req.Model != "anthropic/claude-sonnet-4.6" {
			t.Fatalf("model = %q", req.Model)
		}
		if req.CacheControl != nil {
			t.Fatalf("top-level cache_control should be omitted for explicit prompt-cache breakpoint: %+v", req.CacheControl)
		}
		if len(req.Messages) == 0 || req.Messages[0].Role != "system" {
			t.Fatalf("messages = %#v, want system message first", req.Messages)
		}
		var blocks []struct {
			Type         string `json:"type"`
			Text         string `json:"text"`
			CacheControl struct {
				Type string `json:"type"`
			} `json:"cache_control"`
		}
		if err := json.Unmarshal(req.Messages[0].Content, &blocks); err != nil {
			t.Fatalf("decode cached system content: %v; raw=%s", err, req.Messages[0].Content)
		}
		if len(blocks) != 1 || blocks[0].Type != "text" || !strings.Contains(blocks[0].Text, "stable system") || blocks[0].CacheControl.Type != "ephemeral" {
			t.Fatalf("cached system content = %#v, want explicit ephemeral cache breakpoint", blocks)
		}
		_, _ = w.Write([]byte(`{"choices":[{"finish_reason":"stop","message":{"role":"assistant","content":"done"}}]}`))
	}))
	defer server.Close()

	client, err := NewOpenRouterClient(OpenRouterConfig{
		APIKey:  "key",
		BaseURL: server.URL,
		Model:   "anthropic/claude-sonnet-4.6",
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := client.Complete(context.Background(), []Message{{Role: "system", Content: "stable system"}, {Role: "user", Content: "hi"}}, nil); err != nil {
		t.Fatal(err)
	}
}

func TestOpenRouterClientDoesNotEnableAnthropicPromptCacheForOtherModels(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var req map[string]any
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			t.Fatal(err)
		}
		if _, ok := req["cache_control"]; ok {
			t.Fatalf("cache_control should be omitted for non-Anthropic model: %#v", req["cache_control"])
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

func TestXiaomiClientUsesAPIKeyHeader(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/chat/completions" {
			t.Fatalf("path = %s", r.URL.Path)
		}
		if got := r.Header.Get("api-key"); got != "xiaomi-key" {
			t.Fatalf("api-key = %q, want xiaomi-key", got)
		}
		if got := r.Header.Get("Authorization"); got != "" {
			t.Fatalf("Authorization = %q, want empty", got)
		}
		var req struct {
			Model     string `json:"model"`
			MaxTokens int    `json:"max_tokens"`
			Thinking  struct {
				Type string `json:"type"`
			} `json:"thinking"`
		}
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			t.Fatal(err)
		}
		if req.Model != "mimo-v2.5-pro" {
			t.Fatalf("model = %q", req.Model)
		}
		if req.MaxTokens != 321 {
			t.Fatalf("max_tokens = %d, want 321", req.MaxTokens)
		}
		if req.Thinking.Type != "disabled" {
			t.Fatalf("thinking.type = %q, want disabled", req.Thinking.Type)
		}
		_, _ = w.Write([]byte(`{
			"id":"xiaomi_resp",
			"model":"mimo-v2.5-pro",
			"choices":[{"message":{"role":"assistant","content":"done"}}]
		}`))
	}))
	defer server.Close()

	client, err := NewXiaomiClient(OpenRouterConfig{
		APIKey:  "xiaomi-key",
		BaseURL: server.URL,
		Model:   "xiaomi/mimo-v2.5-pro",
	})
	if err != nil {
		t.Fatal(err)
	}
	completion, err := client.CompleteWithOptions(context.Background(), []Message{{Role: "user", Content: "hi"}}, nil, CompletionOptions{
		MaxCompletionTokens: 321,
		DisableThinking:     true,
	})
	if err != nil {
		t.Fatal(err)
	}
	if completion.ID != "xiaomi_resp" || completion.Model != "mimo-v2.5-pro" || completion.Message.Content != "done" {
		t.Fatalf("completion = %+v", completion)
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

func TestDirectProviderClientNormalizesProviderPrefixedModel(t *testing.T) {
	t.Setenv("DEEPSEEK_API_KEY", "key")
	t.Setenv("DEEPSEEK_BASE_URL", "https://example.deepseek.test")
	client, err := NewDeepSeekClient(OpenRouterConfig{
		Model: "deepseek/deepseek-v4-flash",
	})
	if err != nil {
		t.Fatal(err)
	}
	if client.Model() != "deepseek-v4-flash" {
		t.Fatalf("Model() = %q, want deepseek-v4-flash", client.Model())
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

func TestUsageFromRawSupportsAnthropicCacheShape(t *testing.T) {
	usage := UsageFromRaw(json.RawMessage(`{
		"input_tokens":50,
		"cache_read_input_tokens":1000,
		"cache_creation_input_tokens":200,
		"output_tokens":67
	}`), "anthropic/claude-sonnet-4.6")
	if usage.InputTokens != 1250 || usage.OutputTokens != 67 || usage.TotalTokens != 1317 || usage.CachedInputTokens != 1000 {
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

func TestMaxTurnsForRequestTimeout(t *testing.T) {
	tests := []struct {
		name    string
		timeout time.Duration
		want    int
	}{
		{name: "default", timeout: 0, want: DefaultOpenRouterMaxTurns},
		{name: "short", timeout: 10 * time.Minute, want: DefaultOpenRouterMaxTurns},
		{name: "medium", timeout: 20 * time.Minute, want: 96},
		{name: "hour", timeout: time.Hour, want: 128},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := MaxTurnsForRequestTimeout(tt.timeout); got != tt.want {
				t.Fatalf("MaxTurnsForRequestTimeout(%s) = %d, want %d", tt.timeout, got, tt.want)
			}
		})
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
	providerErr, ok := AsProviderError(err)
	if !ok {
		t.Fatalf("error type = %T, want ProviderError: %v", err, err)
	}
	if providerErr.Kind != ProviderFailureTransientHTTP || !providerErr.Retryable || providerErr.StatusCode != http.StatusBadGateway {
		t.Fatalf("provider error = %#v, want retryable transient HTTP 502", providerErr)
	}
	if got := err.Error(); !strings.Contains(got, "HTTP 502") || !strings.Contains(got, "upstream temporarily unavailable") {
		t.Fatalf("error = %q, want status and body snippet", got)
	}
}

func TestOpenRouterClientClassifiesProviderFailures(t *testing.T) {
	tests := []struct {
		name       string
		statusCode int
		body       string
		wantKind   ProviderFailureKind
		retryable  bool
	}{
		{
			name:       "rate limit",
			statusCode: http.StatusTooManyRequests,
			body:       `{"error":{"message":"slow down","type":"rate_limit_exceeded"}}`,
			wantKind:   ProviderFailureRateLimited,
			retryable:  true,
		},
		{
			name:       "auth",
			statusCode: http.StatusUnauthorized,
			body:       `{"error":{"message":"bad key","type":"invalid_api_key"}}`,
			wantKind:   ProviderFailureAuth,
			retryable:  false,
		},
		{
			name:       "quota",
			statusCode: http.StatusPaymentRequired,
			body:       `{"error":{"message":"insufficient credits","type":"quota"}}`,
			wantKind:   ProviderFailureQuota,
			retryable:  false,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				w.Header().Set("Content-Type", "application/json")
				w.Header().Set("Retry-After", "1")
				w.WriteHeader(tt.statusCode)
				_, _ = w.Write([]byte(tt.body))
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
				t.Fatal("expected provider error")
			}
			providerErr, ok := AsProviderError(err)
			if !ok {
				t.Fatalf("error type = %T, want ProviderError: %v", err, err)
			}
			if providerErr.Kind != tt.wantKind || providerErr.Retryable != tt.retryable || providerErr.StatusCode != tt.statusCode {
				t.Fatalf("provider error = %#v, want kind=%s retryable=%v status=%d", providerErr, tt.wantKind, tt.retryable, tt.statusCode)
			}
			if tt.retryable && providerErr.RetryAfter != time.Second {
				t.Fatalf("retry-after = %s, want 1s", providerErr.RetryAfter)
			}
		})
	}
}

func TestOpenRouterClientClassifiesMalformedResponse(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`not-json`))
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
		t.Fatal("expected decode error")
	}
	providerErr, ok := AsProviderError(err)
	if !ok {
		t.Fatalf("error type = %T, want ProviderError: %v", err, err)
	}
	if providerErr.Kind != ProviderFailureMalformedResponse || providerErr.Retryable {
		t.Fatalf("provider error = %#v, want non-retryable malformed_response", providerErr)
	}
}
