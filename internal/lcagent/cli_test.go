package lcagent

import (
	"bytes"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"lcroom/internal/lcagent/sessionmetrics"
)

func TestRunExecScriptedStreamJSON(t *testing.T) {
	isolateSkillHomes(t)
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "README.md"), []byte("old\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "AGENTS.md"), []byte("Run the scripted checks.\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	scriptPath := filepath.Join(t.TempDir(), "script.jsonl")
	script := `{"type":"tool_call","tool":"apply_patch","args":{"patch":"*** Begin Patch\n*** Update File: README.md\n@@\n-old\n+new\n*** End Patch\n"}}
{"type":"final_response","summary":"done","files_changed":["README.md"],"verification":["scripted"]}
`
	if err := os.WriteFile(scriptPath, []byte(script), 0o600); err != nil {
		t.Fatal(err)
	}
	var stdout, stderr bytes.Buffer
	code := Run([]string{"exec", "--cwd", root, "--data-dir", t.TempDir(), "--auto", "low", "--output", "stream-json", "--script", scriptPath, "patch it"}, &stdout, &stderr)
	if code != 0 {
		t.Fatalf("code = %d stderr=%s", code, stderr.String())
	}
	data, err := os.ReadFile(filepath.Join(root, "README.md"))
	if err != nil {
		t.Fatal(err)
	}
	if string(data) != "new\n" {
		t.Fatalf("README = %q", data)
	}
	if !strings.Contains(stdout.String(), `"type":"session_meta"`) || !strings.Contains(stdout.String(), `"type":"project_instructions"`) || !strings.Contains(stdout.String(), `"type":"turn_complete"`) {
		t.Fatalf("stdout missing events:\n%s", stdout.String())
	}
}

func TestRunExecOpenRouterEmitsModelResponseUsage(t *testing.T) {
	isolateSkillHomes(t)
	root := t.TempDir()
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/chat/completions" {
			t.Fatalf("request path = %s, want /chat/completions", r.URL.Path)
		}
		if got := r.Header.Get("Authorization"); got != "Bearer test-key" {
			t.Fatalf("authorization = %q, want bearer test-key", got)
		}
		var body map[string]any
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			t.Fatalf("decode request body: %v", err)
		}
		if body["model"] != "deepseek/test-model" {
			t.Fatalf("model = %q, want deepseek/test-model", body["model"])
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{
			"id":"resp_test",
			"model":"deepseek/test-model",
			"choices":[{
				"finish_reason":"stop",
				"message":{"role":"assistant","content":"done from model"}
			}],
			"usage":{"prompt_tokens":7,"completion_tokens":3,"total_tokens":10}
		}`))
	}))
	defer server.Close()

	t.Setenv("OPENROUTER_API_KEY", "test-key")
	t.Setenv("OPENROUTER_BASE_URL", server.URL)

	var stdout, stderr bytes.Buffer
	code := Run([]string{
		"exec",
		"--cwd", root,
		"--data-dir", t.TempDir(),
		"--auto", "off",
		"--output", "stream-json",
		"--provider", "openrouter",
		"--model", "deepseek/test-model",
		"--max-turns", "2",
		"answer directly",
	}, &stdout, &stderr)
	if code != 0 {
		t.Fatalf("code = %d stderr=%s stdout=%s", code, stderr.String(), stdout.String())
	}
	text := stdout.String()
	for _, want := range []string{
		`"type":"model_response"`,
		`"response_id":"resp_test"`,
		`"finish_reason":"stop"`,
		`"prompt_tokens":7`,
		`"usage_summary"`,
		`"input_tokens":7`,
		`"output_tokens":3`,
		`"type":"turn_complete"`,
		`"summary":"done from model"`,
	} {
		if !strings.Contains(text, want) {
			t.Fatalf("stdout missing %q:\n%s", want, text)
		}
	}
	if got := strings.Count(text, `"type":"assistant_message"`); got != 1 {
		t.Fatalf("assistant_message count = %d, want 1:\n%s", got, text)
	}
}

func TestRunExecOpenRouterRetriesTransientProviderFailure(t *testing.T) {
	isolateSkillHomes(t)
	root := t.TempDir()
	requests := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests++
		w.Header().Set("Content-Type", "application/json")
		if requests == 1 {
			w.WriteHeader(http.StatusTooManyRequests)
			_, _ = w.Write([]byte(`{"error":{"message":"rate limit exceeded","type":"rate_limit_exceeded"}}`))
			return
		}
		_, _ = w.Write([]byte(`{
			"id":"resp_retry",
			"model":"deepseek/test-model",
			"choices":[{
				"finish_reason":"stop",
				"message":{"role":"assistant","content":"done after retry"}
			}]
		}`))
	}))
	defer server.Close()

	t.Setenv("OPENROUTER_API_KEY", "test-key")
	t.Setenv("OPENROUTER_BASE_URL", server.URL)

	var stdout, stderr bytes.Buffer
	code := Run([]string{
		"exec",
		"--cwd", root,
		"--data-dir", t.TempDir(),
		"--auto", "off",
		"--output", "stream-json",
		"--provider", "openrouter",
		"--model", "deepseek/test-model",
		"--max-turns", "2",
		"answer directly",
	}, &stdout, &stderr)
	if code != 0 {
		t.Fatalf("code = %d stderr=%s stdout=%s", code, stderr.String(), stdout.String())
	}
	if requests != 2 {
		t.Fatalf("requests = %d, want retry once", requests)
	}
	text := stdout.String()
	for _, want := range []string{
		`"type":"provider_failure"`,
		`"kind":"rate_limited"`,
		`"retrying":true`,
		`"type":"provider_retry"`,
		`"attempt":2`,
		`"type":"provider_retry_succeeded"`,
		`"summary":"done after retry"`,
	} {
		if !strings.Contains(text, want) {
			t.Fatalf("stdout missing %q:\n%s", want, text)
		}
	}
}

func TestRunPresetsListsCodingRoutes(t *testing.T) {
	var stdout, stderr bytes.Buffer
	code := Run([]string{"presets", "--output", "json"}, &stdout, &stderr)
	if code != 0 {
		t.Fatalf("code = %d stderr=%s stdout=%s", code, stderr.String(), stdout.String())
	}
	var presets []struct {
		Name     string `json:"Name"`
		Provider string `json:"Provider"`
		Model    string `json:"Model"`
	}
	if err := json.Unmarshal(stdout.Bytes(), &presets); err != nil {
		t.Fatalf("decode presets json: %v\n%s", err, stdout.String())
	}
	got := map[string]string{}
	for _, preset := range presets {
		got[preset.Name] = preset.Provider + "/" + preset.Model
	}
	for _, name := range []string{"balanced", "quality", "cheap-scout"} {
		if got[name] == "/" {
			t.Fatalf("preset %s missing provider/model: %#v", name, presets)
		}
		if _, ok := got[name]; !ok {
			t.Fatalf("missing preset %s in %#v", name, presets)
		}
	}
}

func TestRunExecRoutePresetAppliesCodingDefaults(t *testing.T) {
	isolateSkillHomes(t)
	root := t.TempDir()
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/responses" {
			t.Fatalf("request path = %s, want /responses", r.URL.Path)
		}
		if got := r.Header.Get("Authorization"); got != "Bearer test-openai-key" {
			t.Fatalf("authorization = %q, want bearer test key", got)
		}
		var body map[string]any
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			t.Fatalf("decode request body: %v", err)
		}
		if body["model"] != "gpt-5.5" {
			t.Fatalf("model = %q, want gpt-5.5", body["model"])
		}
		reasoning, _ := body["reasoning"].(map[string]any)
		if reasoning["effort"] != "low" {
			t.Fatalf("reasoning = %#v, want low effort", body["reasoning"])
		}
		if _, ok := body["temperature"]; ok {
			t.Fatalf("quality preset should omit temperature for direct OpenAI: %#v", body["temperature"])
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{
			"id":"resp_quality",
			"model":"gpt-5.5",
			"status":"completed",
			"output":[{
				"type":"message",
				"content":[{"type":"output_text","text":"done from quality route"}]
			}]
		}`))
	}))
	defer server.Close()

	t.Setenv("OPENAI_API_KEY", "test-openai-key")
	t.Setenv("OPENAI_BASE_URL", server.URL)

	var stdout, stderr bytes.Buffer
	code := Run([]string{
		"exec",
		"--cwd", root,
		"--data-dir", t.TempDir(),
		"--route-preset", "quality",
		"--output", "stream-json",
		"--max-turns", "2",
		"answer directly",
	}, &stdout, &stderr)
	if code != 0 {
		t.Fatalf("code = %d stderr=%s stdout=%s", code, stderr.String(), stdout.String())
	}
	text := stdout.String()
	for _, want := range []string{
		`"type":"session_meta"`,
		`"provider":"openai"`,
		`"model":"gpt-5.5"`,
		`"type":"route_preset"`,
		`"name":"quality"`,
		`"context_profile":"large"`,
		`"reasoning_effort":"low"`,
		`"summary":"done from quality route"`,
	} {
		if !strings.Contains(text, want) {
			t.Fatalf("stdout missing %q:\n%s", want, text)
		}
	}
}

func TestRunExecRoutePresetAllowsExplicitOverrides(t *testing.T) {
	isolateSkillHomes(t)
	root := t.TempDir()
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var body map[string]any
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			t.Fatalf("decode request body: %v", err)
		}
		if body["model"] != "deepseek/custom" {
			t.Fatalf("model = %q, want explicit override", body["model"])
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{
			"id":"resp_override",
			"model":"deepseek/custom",
			"choices":[{
				"finish_reason":"stop",
				"message":{"role":"assistant","content":"done from override"}
			}]
		}`))
	}))
	defer server.Close()

	t.Setenv("OPENROUTER_API_KEY", "test-key")
	t.Setenv("OPENROUTER_BASE_URL", server.URL)

	var stdout, stderr bytes.Buffer
	code := Run([]string{
		"exec",
		"--cwd", root,
		"--data-dir", t.TempDir(),
		"--route-preset", "quality",
		"--provider", "openrouter",
		"--model", "deepseek/custom",
		"--context-profile", "balanced",
		"--output", "stream-json",
		"--max-turns", "2",
		"answer directly",
	}, &stdout, &stderr)
	if code != 0 {
		t.Fatalf("code = %d stderr=%s stdout=%s", code, stderr.String(), stdout.String())
	}
	text := stdout.String()
	for _, want := range []string{
		`"provider":"openrouter"`,
		`"model":"deepseek/custom"`,
		`"name":"quality"`,
		`"context_profile":"balanced"`,
		`"summary":"done from override"`,
	} {
		if !strings.Contains(text, want) {
			t.Fatalf("stdout missing %q:\n%s", want, text)
		}
	}
}

func TestRunExecOpenRouterSendsResumeContext(t *testing.T) {
	isolateSkillHomes(t)
	root := t.TempDir()
	dataDir := t.TempDir()
	sessionID := "lca_resume_source"
	started := time.Date(2026, 5, 12, 8, 0, 0, 0, time.UTC)
	writeLCAgentCLITestArtifact(t, dataDir, started, sessionID, []map[string]any{
		{
			"type":       "session_meta",
			"id":         sessionID,
			"cwd":        root,
			"started_at": started.Format(time.RFC3339Nano),
		},
		{
			"type":       "user_message",
			"session_id": sessionID,
			"timestamp":  started.Add(time.Second).Format(time.RFC3339Nano),
			"message":    "implement the first half",
		},
		{
			"type":       "patch_diff_summary",
			"session_id": sessionID,
			"timestamp":  started.Add(2 * time.Second).Format(time.RFC3339Nano),
			"summary":    "README.md: +2 -1",
			"files":      []string{"README.md"},
		},
		{
			"type":                "verification_summary",
			"session_id":          sessionID,
			"timestamp":           started.Add(3 * time.Second).Format(time.RFC3339Nano),
			"status":              "reported",
			"files_changed":       []string{"README.md"},
			"verification_checks": []string{"go test ./internal/lcagent/..."},
		},
		{
			"type":       "turn_complete",
			"session_id": sessionID,
			"timestamp":  started.Add(4 * time.Second).Format(time.RFC3339Nano),
			"summary":    "first half complete",
			"files_changed": []string{
				"README.md",
			},
			"verification":        []string{"go test ./internal/lcagent/..."},
			"verification_status": "reported",
		},
	})

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var body struct {
			Messages []struct {
				Role    string `json:"role"`
				Content string `json:"content"`
			} `json:"messages"`
		}
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			t.Fatalf("decode request body: %v", err)
		}
		if len(body.Messages) < 2 {
			t.Fatalf("request missing messages: %#v", body.Messages)
		}
		systemPrompt := body.Messages[0].Content
		for _, want := range []string{
			"Previous LCAgent session context",
			sessionID,
			"implement the first half",
			"first half complete",
			"README.md: +2 -1",
			"go test ./internal/lcagent/...",
			"Previous verification status: reported",
		} {
			if !strings.Contains(systemPrompt, want) {
				t.Fatalf("system prompt missing resume context %q:\n%s", want, systemPrompt)
			}
		}
		foundCurrentPrompt := false
		for _, msg := range body.Messages {
			if msg.Role == "user" && msg.Content == "continue from previous work" {
				foundCurrentPrompt = true
			}
		}
		if !foundCurrentPrompt {
			t.Fatalf("request missing current continuation prompt: %#v", body.Messages)
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{
			"id":"resp_resume",
			"model":"deepseek/test-model",
			"choices":[{
				"finish_reason":"stop",
				"message":{"role":"assistant","content":"continued with context"}
			}]
		}`))
	}))
	defer server.Close()

	t.Setenv("OPENROUTER_API_KEY", "test-key")
	t.Setenv("OPENROUTER_BASE_URL", server.URL)

	var stdout, stderr bytes.Buffer
	code := Run([]string{
		"exec",
		"--cwd", root,
		"--data-dir", dataDir,
		"--auto", "off",
		"--output", "stream-json",
		"--provider", "openrouter",
		"--model", "deepseek/test-model",
		"--resume", sessionID,
		"--max-turns", "2",
		"continue from previous work",
	}, &stdout, &stderr)
	if code != 0 {
		t.Fatalf("code = %d stderr=%s stdout=%s", code, stderr.String(), stdout.String())
	}
	text := stdout.String()
	for _, want := range []string{
		`"type":"resume_context"`,
		`"source_session_id":"` + sessionID + `"`,
		`"source_path":`,
		`"summary":"source ` + sessionID,
		`"summary":"continued with context"`,
	} {
		if !strings.Contains(text, want) {
			t.Fatalf("stdout missing %q:\n%s", want, text)
		}
	}
}

func TestRunExecOpenRouterUsesGenerousToolProfile(t *testing.T) {
	isolateSkillHomes(t)
	root := t.TempDir()
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var body struct {
			Messages []struct {
				Role    string `json:"role"`
				Content string `json:"content"`
			} `json:"messages"`
			Tools []struct {
				Function struct {
					Name        string         `json:"name"`
					Description string         `json:"description"`
					Parameters  map[string]any `json:"parameters"`
				} `json:"function"`
			} `json:"tools"`
		}
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			t.Fatalf("decode request body: %v", err)
		}
		if len(body.Messages) == 0 || !strings.Contains(body.Messages[0].Content, "read_file defaults to 400 lines") {
			t.Fatalf("system prompt missing larger read-limit guidance: %#v", body.Messages)
		}
		if strings.Contains(body.Messages[0].Content, "tool profile") {
			t.Fatalf("system prompt should not expose benchmark profile labels: %#v", body.Messages)
		}
		if strings.Contains(body.Messages[0].Content, "context profile") {
			t.Fatalf("system prompt should not expose context profile labels: %#v", body.Messages)
		}
		var readMax, searchContextMax string
		var readDescription string
		for _, tool := range body.Tools {
			props, _ := tool.Function.Parameters["properties"].(map[string]any)
			switch tool.Function.Name {
			case "read_file":
				readDescription = tool.Function.Description
				readMax = fmt.Sprint(props["limit"].(map[string]any)["maximum"])
			case "search":
				searchContextMax = fmt.Sprint(props["context_before"].(map[string]any)["maximum"])
			}
		}
		if readMax != "2500" || searchContextMax != "16" || !strings.Contains(readDescription, "Larger read limits") {
			t.Fatalf("generous tool schema mismatch: readMax=%s searchContextMax=%s readDescription=%q", readMax, searchContextMax, readDescription)
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{
			"id":"resp_test",
			"model":"deepseek/test-model",
			"choices":[{
				"finish_reason":"stop",
				"message":{"role":"assistant","content":"done"}
			}]
		}`))
	}))
	defer server.Close()

	t.Setenv("OPENROUTER_API_KEY", "test-key")
	t.Setenv("OPENROUTER_BASE_URL", server.URL)

	var stdout, stderr bytes.Buffer
	code := Run([]string{
		"exec",
		"--cwd", root,
		"--data-dir", t.TempDir(),
		"--auto", "off",
		"--output", "stream-json",
		"--provider", "openrouter",
		"--model", "deepseek/test-model",
		"--tool-profile", "generous",
		"--context-profile", "large",
		"--max-turns", "2",
		"answer directly",
	}, &stdout, &stderr)
	if code != 0 {
		t.Fatalf("code = %d stderr=%s stdout=%s", code, stderr.String(), stdout.String())
	}
	if !strings.Contains(stdout.String(), `"type":"tool_profile"`) || !strings.Contains(stdout.String(), `"profile":"generous"`) {
		t.Fatalf("stdout missing generous tool profile event:\n%s", stdout.String())
	}
	if !strings.Contains(stdout.String(), `"type":"context_profile"`) || !strings.Contains(stdout.String(), `"profile":"large"`) {
		t.Fatalf("stdout missing large context profile event:\n%s", stdout.String())
	}
}

func TestRunExecDeepSeekUsesDirectProviderEnv(t *testing.T) {
	isolateSkillHomes(t)
	root := t.TempDir()
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/chat/completions" {
			t.Fatalf("request path = %s, want /chat/completions", r.URL.Path)
		}
		if got := r.Header.Get("Authorization"); got != "Bearer test-deepseek-key" {
			t.Fatalf("authorization = %q, want bearer test key", got)
		}
		if got := r.Header.Get("HTTP-Referer"); got != "" {
			t.Fatalf("deepseek request should not send OpenRouter referer header: %q", got)
		}
		var body map[string]any
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			t.Fatalf("decode request body: %v", err)
		}
		if body["model"] != "deepseek-v4-pro" {
			t.Fatalf("model = %q, want deepseek-v4-pro", body["model"])
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{
			"id":"resp_deepseek",
			"model":"deepseek-v4-pro",
			"choices":[{
				"finish_reason":"stop",
				"message":{"role":"assistant","content":"done from direct deepseek"}
			}],
			"usage":{"prompt_tokens":7,"prompt_cache_hit_tokens":2,"completion_tokens":3,"total_tokens":10}
		}`))
	}))
	defer server.Close()

	t.Setenv("DEEPSEEK_API_KEY", "test-deepseek-key")
	t.Setenv("DEEPSEEK_BASE_URL", server.URL)

	var stdout, stderr bytes.Buffer
	code := Run([]string{
		"exec",
		"--cwd", root,
		"--data-dir", t.TempDir(),
		"--auto", "off",
		"--output", "stream-json",
		"--provider", "deepseek",
		"--max-turns", "2",
		"answer directly",
	}, &stdout, &stderr)
	if code != 0 {
		t.Fatalf("code = %d stderr=%s stdout=%s", code, stderr.String(), stdout.String())
	}
	text := stdout.String()
	for _, want := range []string{
		`"provider":"deepseek"`,
		`"model":"deepseek-v4-pro"`,
		`"response_id":"resp_deepseek"`,
		`"cached_input_tokens":2`,
		`"summary":"done from direct deepseek"`,
	} {
		if !strings.Contains(text, want) {
			t.Fatalf("stdout missing %q:\n%s", want, text)
		}
	}
}

func TestRunExecMoonshotUsesDirectProviderEnv(t *testing.T) {
	isolateSkillHomes(t)
	root := t.TempDir()
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/chat/completions" {
			t.Fatalf("request path = %s, want /chat/completions", r.URL.Path)
		}
		if got := r.Header.Get("Authorization"); got != "Bearer test-moonshot-key" {
			t.Fatalf("authorization = %q, want bearer test key", got)
		}
		if got := r.Header.Get("HTTP-Referer"); got != "" {
			t.Fatalf("moonshot request should not send OpenRouter referer header: %q", got)
		}
		var body map[string]any
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			t.Fatalf("decode request body: %v", err)
		}
		if body["model"] != "kimi-k2.6" {
			t.Fatalf("model = %q, want kimi-k2.6", body["model"])
		}
		for _, key := range []string{"temperature", "max_completion_tokens", "max_tokens", "thinking"} {
			if _, ok := body[key]; ok {
				t.Fatalf("moonshot request should not send %s by default: %#v", key, body[key])
			}
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{
			"id":"resp_moonshot",
			"model":"kimi-k2.6",
			"choices":[{
				"finish_reason":"stop",
				"message":{"role":"assistant","content":"done from direct moonshot"}
			}],
			"usage":{"prompt_tokens":7,"cached_tokens":2,"completion_tokens":3,"total_tokens":10}
		}`))
	}))
	defer server.Close()

	t.Setenv("MOONSHOT_API_KEY", "test-moonshot-key")
	t.Setenv("MOONSHOT_BASE_URL", server.URL)

	var stdout, stderr bytes.Buffer
	code := Run([]string{
		"exec",
		"--cwd", root,
		"--data-dir", t.TempDir(),
		"--auto", "off",
		"--output", "stream-json",
		"--provider", "moonshot",
		"--max-turns", "2",
		"answer directly",
	}, &stdout, &stderr)
	if code != 0 {
		t.Fatalf("code = %d stderr=%s stdout=%s", code, stderr.String(), stdout.String())
	}
	text := stdout.String()
	for _, want := range []string{
		`"provider":"moonshot"`,
		`"model":"kimi-k2.6"`,
		`"response_id":"resp_moonshot"`,
		`"cached_input_tokens":2`,
		`"summary":"done from direct moonshot"`,
	} {
		if !strings.Contains(text, want) {
			t.Fatalf("stdout missing %q:\n%s", want, text)
		}
	}
}

func TestRunExecOpenRouterPassesReasoningEffort(t *testing.T) {
	isolateSkillHomes(t)
	root := t.TempDir()
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var body map[string]any
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			t.Fatalf("decode request body: %v", err)
		}
		reasoning, _ := body["reasoning"].(map[string]any)
		if reasoning["effort"] != "low" {
			t.Fatalf("reasoning = %#v, want effort=low", body["reasoning"])
		}
		if _, ok := body["max_completion_tokens"]; ok {
			t.Fatalf("request should not set max_completion_tokens with reasoning effort: %#v", body["max_completion_tokens"])
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{
			"id":"resp_reasoning",
			"model":"openai/gpt-5.5",
			"choices":[{
				"finish_reason":"stop",
				"message":{"role":"assistant","content":"done with low reasoning"}
			}]
		}`))
	}))
	defer server.Close()

	t.Setenv("OPENROUTER_API_KEY", "test-key")
	t.Setenv("OPENROUTER_BASE_URL", server.URL)

	var stdout, stderr bytes.Buffer
	code := Run([]string{
		"exec",
		"--cwd", root,
		"--data-dir", t.TempDir(),
		"--auto", "off",
		"--output", "stream-json",
		"--provider", "openrouter",
		"--model", "openai/gpt-5.5",
		"--reasoning-effort", "low",
		"--max-turns", "2",
		"answer directly",
	}, &stdout, &stderr)
	if code != 0 {
		t.Fatalf("code = %d stderr=%s stdout=%s", code, stderr.String(), stdout.String())
	}
	if !strings.Contains(stdout.String(), `"summary":"done with low reasoning"`) {
		t.Fatalf("stdout missing final summary:\n%s", stdout.String())
	}
}

func TestRunExecOpenRouterPassesProviderOnlyAndTemperature(t *testing.T) {
	isolateSkillHomes(t)
	root := t.TempDir()
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var body struct {
			Temperature float64 `json:"temperature"`
			Provider    struct {
				Only              []string `json:"only"`
				AllowFallbacks    bool     `json:"allow_fallbacks"`
				RequireParameters bool     `json:"require_parameters"`
			} `json:"provider"`
		}
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			t.Fatalf("decode request body: %v", err)
		}
		if body.Temperature != 0.4 {
			t.Fatalf("temperature = %f, want 0.4", body.Temperature)
		}
		if strings.Join(body.Provider.Only, ",") != "anthropic,minimax" {
			t.Fatalf("provider.only = %#v", body.Provider.Only)
		}
		if body.Provider.AllowFallbacks {
			t.Fatalf("provider.allow_fallbacks = true, want false")
		}
		if !body.Provider.RequireParameters {
			t.Fatalf("provider.require_parameters = false, want true")
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{
			"id":"resp_routing",
			"model":"anthropic/claude-sonnet-4.6",
			"choices":[{
				"finish_reason":"stop",
				"message":{"role":"assistant","content":"done with provider pin"}
			}]
		}`))
	}))
	defer server.Close()

	t.Setenv("OPENROUTER_API_KEY", "test-key")
	t.Setenv("OPENROUTER_BASE_URL", server.URL)

	var stdout, stderr bytes.Buffer
	code := Run([]string{
		"exec",
		"--cwd", root,
		"--data-dir", t.TempDir(),
		"--auto", "off",
		"--output", "stream-json",
		"--provider", "openrouter",
		"--model", "anthropic/claude-sonnet-4.6",
		"--openrouter-provider-only", "anthropic, minimax",
		"--temperature", "0.4",
		"--max-turns", "2",
		"answer directly",
	}, &stdout, &stderr)
	if code != 0 {
		t.Fatalf("code = %d stderr=%s stdout=%s", code, stderr.String(), stdout.String())
	}
	if !strings.Contains(stdout.String(), `"summary":"done with provider pin"`) {
		t.Fatalf("stdout missing final summary:\n%s", stdout.String())
	}
}

func TestRunExecOpenRouterCanOmitTemperature(t *testing.T) {
	isolateSkillHomes(t)
	root := t.TempDir()
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var body map[string]any
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			t.Fatalf("decode request body: %v", err)
		}
		if _, ok := body["temperature"]; ok {
			t.Fatalf("temperature should be omitted: %#v", body["temperature"])
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{
			"id":"resp_no_temperature",
			"model":"anthropic/claude-opus-4.7",
			"choices":[{
				"finish_reason":"stop",
				"message":{"role":"assistant","content":"done without temperature"}
			}]
		}`))
	}))
	defer server.Close()

	t.Setenv("OPENROUTER_API_KEY", "test-key")
	t.Setenv("OPENROUTER_BASE_URL", server.URL)

	var stdout, stderr bytes.Buffer
	code := Run([]string{
		"exec",
		"--cwd", root,
		"--data-dir", t.TempDir(),
		"--auto", "off",
		"--output", "stream-json",
		"--provider", "openrouter",
		"--model", "anthropic/claude-opus-4.7",
		"--openrouter-provider-only", "anthropic",
		"--temperature", "omitted",
		"--max-turns", "2",
		"answer directly",
	}, &stdout, &stderr)
	if code != 0 {
		t.Fatalf("code = %d stderr=%s stdout=%s", code, stderr.String(), stdout.String())
	}
	if !strings.Contains(stdout.String(), `"summary":"done without temperature"`) {
		t.Fatalf("stdout missing final summary:\n%s", stdout.String())
	}
}

func TestRunExecOpenRouterCanUseReadOnlyTool(t *testing.T) {
	isolateSkillHomes(t)
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "README.md"), []byte("alpha\nbeta needle\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "AGENTS.md"), []byte("Always prefer the project instructions.\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	skillPath := filepath.Join(root, ".agents", "skills", "demo", "SKILL.md")
	if err := os.MkdirAll(filepath.Dir(skillPath), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(skillPath, []byte("---\nname: demo\ndescription: Demo workflow\n---\n# Demo\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	requests := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests++
		var body struct {
			Messages []struct {
				Role    string `json:"role"`
				Content string `json:"content"`
			} `json:"messages"`
		}
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			t.Fatalf("decode request body: %v", err)
		}
		w.Header().Set("Content-Type", "application/json")
		if requests == 1 {
			if len(body.Messages) == 0 || !strings.Contains(body.Messages[0].Content, "demo [project]: Demo workflow") {
				t.Fatalf("system prompt missing skill metadata: %#v", body.Messages)
			}
			if !strings.Contains(body.Messages[0].Content, "Always prefer the project instructions.") {
				t.Fatalf("system prompt missing project instructions: %#v", body.Messages)
			}
			_, _ = w.Write([]byte(`{
				"id":"resp_tool",
				"model":"deepseek/test-model",
				"choices":[{
					"finish_reason":"tool_calls",
					"message":{"role":"assistant","tool_calls":[{"id":"call_read","type":"function","function":{"name":"read_file","arguments":"{\"path\":\"README.md\",\"offset\":2,\"limit\":1}"}}]}
				}]
			}`))
			return
		}
		foundToolOutput := false
		for _, msg := range body.Messages {
			if msg.Role == "tool" && strings.Contains(msg.Content, "2 | beta needle") {
				foundToolOutput = true
			}
		}
		if !foundToolOutput {
			t.Fatalf("second request missing read_file tool output: %#v", body.Messages)
		}
		_, _ = w.Write([]byte(`{
			"id":"resp_final",
			"model":"deepseek/test-model",
			"choices":[{
				"finish_reason":"stop",
				"message":{"role":"assistant","content":"read the file"}
			}]
		}`))
	}))
	defer server.Close()

	t.Setenv("OPENROUTER_API_KEY", "test-key")
	t.Setenv("OPENROUTER_BASE_URL", server.URL)

	var stdout, stderr bytes.Buffer
	code := Run([]string{
		"exec",
		"--cwd", root,
		"--data-dir", t.TempDir(),
		"--auto", "off",
		"--output", "stream-json",
		"--provider", "openrouter",
		"--model", "deepseek/test-model",
		"--max-turns", "3",
		"read README",
	}, &stdout, &stderr)
	if code != 0 {
		t.Fatalf("code = %d stderr=%s stdout=%s", code, stderr.String(), stdout.String())
	}
	text := stdout.String()
	for _, want := range []string{
		`"tool":"read_file"`,
		`2 | beta needle`,
		`"summary":"read the file"`,
	} {
		if !strings.Contains(text, want) {
			t.Fatalf("stdout missing %q:\n%s", want, text)
		}
	}
	if requests != 2 {
		t.Fatalf("requests = %d, want 2", requests)
	}
}

func TestRunExecOpenRouterCompactsLargeToolHistoryBeforeNextRequest(t *testing.T) {
	isolateSkillHomes(t)
	root := t.TempDir()
	var big strings.Builder
	for i := 1; i <= 1000; i++ {
		fmt.Fprintf(&big, "line %04d with enough repeated context to force compaction before the next provider request abcdefghijklmnopqrstuvwxyz abcdefghijklmnopqrstuvwxyz\n", i)
	}
	if err := os.WriteFile(filepath.Join(root, "BIG.md"), []byte(big.String()), 0o644); err != nil {
		t.Fatal(err)
	}

	requests := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests++
		var body struct {
			Messages []struct {
				Role    string `json:"role"`
				Content string `json:"content"`
			} `json:"messages"`
			Tools []any `json:"tools"`
		}
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			t.Fatalf("decode request body: %v", err)
		}
		w.Header().Set("Content-Type", "application/json")
		if requests == 1 {
			if len(body.Tools) == 0 {
				t.Fatalf("first request missing tools: %#v", body)
			}
			_, _ = w.Write([]byte(`{
				"id":"resp_tool",
				"model":"deepseek/test-model",
				"choices":[{
					"finish_reason":"tool_calls",
					"message":{"role":"assistant","tool_calls":[{"id":"call_read","type":"function","function":{"name":"read_file","arguments":"{\"path\":\"BIG.md\",\"limit\":1000}"}}]}
				}]
			}`))
			return
		}
		if len(body.Tools) == 0 {
			t.Fatalf("compacted continuation request should still include tools: %#v", body)
		}
		originalRequestSeen := false
		compactedContextSeen := false
		readLedgerSeen := false
		for _, msg := range body.Messages {
			if msg.Role == "tool" {
				t.Fatalf("compacted request should not contain raw tool messages: %#v", body.Messages)
			}
			if msg.Role == "user" && msg.Content == "read the big file" {
				originalRequestSeen = true
			}
			if strings.Contains(msg.Content, loopCompactedContextPrefix) && strings.Contains(msg.Content, "tool_result: read_file") {
				compactedContextSeen = true
			}
			if strings.Contains(msg.Content, "Read ledger") && strings.Contains(msg.Content, "- BIG.md: lines 1-1000 of 1000") {
				readLedgerSeen = true
			}
			if strings.Contains(msg.Content, "line 0500 with enough repeated context") {
				t.Fatalf("compacted request kept middle of large file output: %#v", body.Messages)
			}
		}
		if !originalRequestSeen || !compactedContextSeen || !readLedgerSeen {
			t.Fatalf("compacted request missing original=%v compacted=%v ledger=%v messages=%#v", originalRequestSeen, compactedContextSeen, readLedgerSeen, body.Messages)
		}
		_, _ = w.Write([]byte(`{
			"id":"resp_final",
			"model":"deepseek/test-model",
			"choices":[{
				"finish_reason":"stop",
				"message":{"role":"assistant","content":"done after compaction"}
			}]
		}`))
	}))
	defer server.Close()

	t.Setenv("OPENROUTER_API_KEY", "test-key")
	t.Setenv("OPENROUTER_BASE_URL", server.URL)

	var stdout, stderr bytes.Buffer
	code := Run([]string{
		"exec",
		"--cwd", root,
		"--data-dir", t.TempDir(),
		"--auto", "off",
		"--output", "stream-json",
		"--provider", "openrouter",
		"--model", "deepseek/test-model",
		"--max-turns", "3",
		"read the big file",
	}, &stdout, &stderr)
	if code != 0 {
		t.Fatalf("code = %d stderr=%s stdout=%s", code, stderr.String(), stdout.String())
	}
	text := stdout.String()
	for _, want := range []string{`"type":"context_compacted"`, `"type":"turn_complete"`, `"summary":"done after compaction"`} {
		if !strings.Contains(text, want) {
			t.Fatalf("stdout missing %q:\n%s", want, text)
		}
	}
	if requests != 2 {
		t.Fatalf("requests = %d, want 2", requests)
	}
}

func TestRunExecOpenRouterFinalResponseToolIsCanonical(t *testing.T) {
	isolateSkillHomes(t)
	root := t.TempDir()
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{
			"id":"resp_final_tool",
			"model":"deepseek/test-model",
			"choices":[{
				"finish_reason":"tool_calls",
				"message":{
					"role":"assistant",
					"content":"ignore this prose wrapper",
					"tool_calls":[{
						"id":"call_final",
						"type":"function",
						"function":{
							"name":"final_response",
							"arguments":{"summary":"canonical final","files_changed":[],"verification":[]}
						}
					}]
				}
			}]
		}`))
	}))
	defer server.Close()

	t.Setenv("OPENROUTER_API_KEY", "test-key")
	t.Setenv("OPENROUTER_BASE_URL", server.URL)

	var stdout, stderr bytes.Buffer
	code := Run([]string{
		"exec",
		"--cwd", root,
		"--data-dir", t.TempDir(),
		"--auto", "off",
		"--output", "stream-json",
		"--provider", "openrouter",
		"--model", "deepseek/test-model",
		"--max-turns", "2",
		"finish directly",
	}, &stdout, &stderr)
	if code != 0 {
		t.Fatalf("code = %d stderr=%s stdout=%s", code, stderr.String(), stdout.String())
	}
	text := stdout.String()
	if got := strings.Count(text, `"type":"assistant_message"`); got != 1 {
		t.Fatalf("assistant_message count = %d, want 1:\n%s", got, text)
	}
	if !strings.Contains(text, `"summary":"canonical final"`) {
		t.Fatalf("stdout missing canonical final summary:\n%s", text)
	}
	if strings.Contains(text, "ignore this prose wrapper") {
		t.Fatalf("stdout should not include wrapper content when final_response is present:\n%s", text)
	}
}

func TestRunExecOpenRouterFeedsFailedVerificationBackToModel(t *testing.T) {
	isolateSkillHomes(t)
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "go.mod"), []byte("module demo\n\ngo 1.25\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "demo_test.go"), []byte("package demo\n\nimport \"testing\"\n\nfunc TestDemo(t *testing.T) { t.Fatal(\"boom\") }\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	requests := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests++
		var body struct {
			Messages []struct {
				Role    string `json:"role"`
				Content string `json:"content"`
			} `json:"messages"`
		}
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			t.Fatalf("decode request body: %v", err)
		}
		w.Header().Set("Content-Type", "application/json")
		if requests == 1 {
			_, _ = w.Write([]byte(`{
				"id":"resp_verify",
				"model":"deepseek/test-model",
				"choices":[{
					"finish_reason":"tool_calls",
					"message":{"role":"assistant","tool_calls":[{"id":"call_verify","type":"function","function":{"name":"run_command","arguments":{"argv":["go","test","./..."],"shell":false,"purpose":"verify","timeout_ms":120000}}}]}
				}]
			}`))
			return
		}
		feedbackSeen := false
		for _, msg := range body.Messages {
			if msg.Role == "user" && strings.Contains(msg.Content, "Verification feedback: go test ./... failed") && strings.Contains(msg.Content, "rerun a purpose=verify check") {
				feedbackSeen = true
			}
		}
		if !feedbackSeen {
			t.Fatalf("second request missing verification feedback: %#v", body.Messages)
		}
		_, _ = w.Write([]byte(`{
			"id":"resp_final",
			"model":"deepseek/test-model",
			"choices":[{
				"finish_reason":"tool_calls",
				"message":{"role":"assistant","tool_calls":[{"id":"call_final","type":"function","function":{"name":"final_response","arguments":{"summary":"verification failed and needs a fix","files_changed":[],"verification":["go test ./... failed"]}}}]}
			}]
		}`))
	}))
	defer server.Close()

	t.Setenv("OPENROUTER_API_KEY", "test-key")
	t.Setenv("OPENROUTER_BASE_URL", server.URL)

	var stdout, stderr bytes.Buffer
	code := Run([]string{
		"exec",
		"--cwd", root,
		"--data-dir", t.TempDir(),
		"--auto", "low",
		"--output", "stream-json",
		"--provider", "openrouter",
		"--model", "deepseek/test-model",
		"--max-turns", "3",
		"run verification",
	}, &stdout, &stderr)
	if code != 0 {
		t.Fatalf("code = %d stderr=%s stdout=%s", code, stderr.String(), stdout.String())
	}
	text := stdout.String()
	for _, want := range []string{
		`"type":"verification_check"`,
		`"status":"failed"`,
		`"type":"verification_feedback"`,
		`"verification_status":"failed"`,
	} {
		if !strings.Contains(text, want) {
			t.Fatalf("stdout missing %q:\n%s", want, text)
		}
	}
	if requests != 2 {
		t.Fatalf("requests = %d, want 2", requests)
	}
}

func TestRunExecOpenRouterBouncesFinalAfterChangedFilesWithoutActualVerification(t *testing.T) {
	isolateSkillHomes(t)
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "README.md"), []byte("hello\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	requests := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests++
		var body struct {
			Messages []struct {
				Role    string `json:"role"`
				Content string `json:"content"`
			} `json:"messages"`
		}
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			t.Fatalf("decode request body: %v", err)
		}
		w.Header().Set("Content-Type", "application/json")
		if requests == 1 {
			_, _ = w.Write([]byte(`{
				"id":"resp_early_final",
				"model":"deepseek/test-model",
				"choices":[{
					"finish_reason":"tool_calls",
					"message":{"role":"assistant","tool_calls":[{"id":"call_final_1","type":"function","function":{"name":"final_response","arguments":{"summary":"changed README","files_changed":["README.md"],"verification":["go test ./..."]}}}]}
				}]
			}`))
			return
		}
		feedbackSeen := false
		for _, msg := range body.Messages {
			if strings.Contains(msg.Content, "final_response reported verification") && strings.Contains(msg.Content, "purpose=verify") {
				feedbackSeen = true
			}
		}
		if !feedbackSeen {
			t.Fatalf("second request missing final verification feedback: %#v", body.Messages)
		}
		_, _ = w.Write([]byte(`{
			"id":"resp_final",
			"model":"deepseek/test-model",
			"choices":[{
				"finish_reason":"tool_calls",
				"message":{"role":"assistant","tool_calls":[{"id":"call_final_2","type":"function","function":{"name":"final_response","arguments":{"summary":"verification blocked after one reminder","files_changed":["README.md"],"verification":["not run: no applicable check"]}}}]}
			}]
		}`))
	}))
	defer server.Close()

	t.Setenv("OPENROUTER_API_KEY", "test-key")
	t.Setenv("OPENROUTER_BASE_URL", server.URL)

	var stdout, stderr bytes.Buffer
	code := Run([]string{
		"exec",
		"--cwd", root,
		"--data-dir", t.TempDir(),
		"--auto", "low",
		"--output", "stream-json",
		"--provider", "openrouter",
		"--model", "deepseek/test-model",
		"--max-turns", "3",
		"patch README",
	}, &stdout, &stderr)
	if code != 0 {
		t.Fatalf("code = %d stderr=%s stdout=%s", code, stderr.String(), stdout.String())
	}
	text := stdout.String()
	for _, want := range []string{
		`"type":"verification_feedback"`,
		`"status":"reported_only"`,
		`"summary":"verification blocked after one reminder"`,
	} {
		if !strings.Contains(text, want) {
			t.Fatalf("stdout missing %q:\n%s", want, text)
		}
	}
	if requests != 2 {
		t.Fatalf("requests = %d, want 2", requests)
	}
}

func TestRunExecOpenRouterFeedsPatchFailureBackToModel(t *testing.T) {
	isolateSkillHomes(t)
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "README.md"), []byte("current\nkeep\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	requests := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests++
		var body struct {
			Messages []struct {
				Role    string `json:"role"`
				Content string `json:"content"`
			} `json:"messages"`
		}
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			t.Fatalf("decode request body: %v", err)
		}
		w.Header().Set("Content-Type", "application/json")
		if requests == 1 {
			_, _ = w.Write([]byte(`{
				"id":"resp_patch",
				"model":"deepseek/test-model",
				"choices":[{
					"finish_reason":"tool_calls",
					"message":{"role":"assistant","tool_calls":[{"id":"call_patch","type":"function","function":{"name":"apply_patch","arguments":{"patch":"*** Begin Patch\n*** Update File: README.md\n@@\n-old\n+new\n keep\n*** End Patch\n"}}}]}
				}]
			}`))
			return
		}
		feedbackSeen := false
		for _, msg := range body.Messages {
			if msg.Role == "user" &&
				strings.Contains(msg.Content, "Patch feedback: README.md failed during apply") &&
				strings.Contains(msg.Content, `read_file {"path":"README.md","offset":1,"limit":2}`) {
				feedbackSeen = true
			}
		}
		if !feedbackSeen {
			t.Fatalf("second request missing patch feedback: %#v", body.Messages)
		}
		_, _ = w.Write([]byte(`{
			"id":"resp_final",
			"model":"deepseek/test-model",
			"choices":[{
				"finish_reason":"tool_calls",
				"message":{"role":"assistant","tool_calls":[{"id":"call_final","type":"function","function":{"name":"final_response","arguments":{"summary":"patch failed; need to re-read README.md","files_changed":[],"verification":[]}}}]}
			}]
		}`))
	}))
	defer server.Close()

	t.Setenv("OPENROUTER_API_KEY", "test-key")
	t.Setenv("OPENROUTER_BASE_URL", server.URL)

	var stdout, stderr bytes.Buffer
	code := Run([]string{
		"exec",
		"--cwd", root,
		"--data-dir", t.TempDir(),
		"--auto", "low",
		"--output", "stream-json",
		"--provider", "openrouter",
		"--model", "deepseek/test-model",
		"--max-turns", "3",
		"patch README",
	}, &stdout, &stderr)
	if code != 0 {
		t.Fatalf("code = %d stderr=%s stdout=%s", code, stderr.String(), stdout.String())
	}
	text := stdout.String()
	for _, want := range []string{
		`"type":"patch_feedback"`,
		`"stage":"apply"`,
		`"path":"README.md"`,
		`"suggested_reads":[{"path":"README.md","offset":1,"limit":2`,
		`"summary":"patch failed; need to re-read README.md"`,
	} {
		if !strings.Contains(text, want) {
			t.Fatalf("stdout missing %q:\n%s", want, text)
		}
	}
	if requests != 2 {
		t.Fatalf("requests = %d, want 2", requests)
	}
}

func TestRunExecOpenRouterSuppressesDuplicatePatchFeedback(t *testing.T) {
	isolateSkillHomes(t)
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "README.md"), []byte("current\nkeep\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	stalePatch := "*** Begin Patch\n*** Update File: README.md\n@@\n-old\n+new\n keep\n*** End Patch\n"
	requests := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests++
		var body struct {
			Messages []struct {
				Role    string `json:"role"`
				Content string `json:"content"`
			} `json:"messages"`
		}
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			t.Fatalf("decode request body: %v", err)
		}
		w.Header().Set("Content-Type", "application/json")
		switch requests {
		case 1, 2:
			_, _ = fmt.Fprintf(w, `{
				"id":"resp_patch_%d",
				"model":"deepseek/test-model",
				"choices":[{
					"finish_reason":"tool_calls",
					"message":{"role":"assistant","tool_calls":[{"id":"call_patch_%d","type":"function","function":{"name":"apply_patch","arguments":{"patch":%q}}}]}
				}]
			}`, requests, requests, stalePatch)
		default:
			feedbackMessages := 0
			for _, msg := range body.Messages {
				if msg.Role == "user" && strings.Contains(msg.Content, "Patch feedback: README.md failed during apply") {
					feedbackMessages++
				}
			}
			if feedbackMessages != 1 {
				t.Fatalf("third request patch feedback user messages = %d, want one: %#v", feedbackMessages, body.Messages)
			}
			_, _ = w.Write([]byte(`{
				"id":"resp_final",
				"model":"deepseek/test-model",
				"choices":[{
					"finish_reason":"tool_calls",
					"message":{"role":"assistant","tool_calls":[{"id":"call_final","type":"function","function":{"name":"final_response","arguments":{"summary":"patch retry blocked after duplicate feedback","files_changed":[],"verification":[]}}}]}
				}]
			}`))
		}
	}))
	defer server.Close()

	t.Setenv("OPENROUTER_API_KEY", "test-key")
	t.Setenv("OPENROUTER_BASE_URL", server.URL)

	var stdout, stderr bytes.Buffer
	code := Run([]string{
		"exec",
		"--cwd", root,
		"--data-dir", t.TempDir(),
		"--auto", "low",
		"--output", "stream-json",
		"--provider", "openrouter",
		"--model", "deepseek/test-model",
		"--max-turns", "4",
		"patch README",
	}, &stdout, &stderr)
	if code != 0 {
		t.Fatalf("code = %d stderr=%s stdout=%s", code, stderr.String(), stdout.String())
	}
	text := stdout.String()
	if strings.Count(text, `"type":"patch_feedback"`) != 1 {
		t.Fatalf("stdout patch feedback count mismatch:\n%s", text)
	}
	for _, want := range []string{
		`"type":"repair_feedback_suppressed"`,
		`"kind":"patch"`,
		`"reason":"duplicate feedback already sent to model"`,
		`"summary":"patch retry blocked after duplicate feedback"`,
	} {
		if !strings.Contains(text, want) {
			t.Fatalf("stdout missing %q:\n%s", want, text)
		}
	}
	if requests != 3 {
		t.Fatalf("requests = %d, want 3", requests)
	}
}

func TestRunExecOpenRouterStripsProviderToolMarkupFromToolTurn(t *testing.T) {
	isolateSkillHomes(t)
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "README.md"), []byte("needle\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	requests := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests++
		var body struct {
			Messages []struct {
				Content string `json:"content"`
			} `json:"messages"`
		}
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			t.Fatalf("decode request body: %v", err)
		}
		for _, msg := range body.Messages {
			if strings.Contains(msg.Content, "<\uff5cDSML") {
				t.Fatalf("request history leaked provider markup: %#v", body.Messages)
			}
		}
		w.Header().Set("Content-Type", "application/json")
		if requests == 1 {
			_, _ = w.Write([]byte(`{
				"id":"resp_tool",
				"model":"deepseek/test-model",
				"choices":[{
					"finish_reason":"tool_calls",
					"message":{
						"role":"assistant",
						"content":"Let me read it.\n\n<\uff5cDSML\uff5ctool_calls><\uff5cDSML\uff5cinvoke name=\"read_file\">",
						"tool_calls":[{"id":"call_read","type":"function","function":{"name":"read_file","arguments":{"path":"README.md","limit":1}}}]
					}
				}]
			}`))
			return
		}
		_, _ = w.Write([]byte(`{
			"id":"resp_final",
			"model":"deepseek/test-model",
			"choices":[{
				"finish_reason":"stop",
				"message":{"role":"assistant","content":"done"}
			}]
		}`))
	}))
	defer server.Close()

	t.Setenv("OPENROUTER_API_KEY", "test-key")
	t.Setenv("OPENROUTER_BASE_URL", server.URL)

	var stdout, stderr bytes.Buffer
	code := Run([]string{
		"exec",
		"--cwd", root,
		"--data-dir", t.TempDir(),
		"--auto", "off",
		"--output", "stream-json",
		"--provider", "openrouter",
		"--model", "deepseek/test-model",
		"--max-turns", "3",
		"read README",
	}, &stdout, &stderr)
	if code != 0 {
		t.Fatalf("code = %d stderr=%s stdout=%s", code, stderr.String(), stdout.String())
	}
	text := stdout.String()
	if strings.Contains(text, "DSML") {
		t.Fatalf("stdout should not include provider markup:\n%s", text)
	}
	if !strings.Contains(text, "Let me read it.") || !strings.Contains(text, `"tool":"read_file"`) || !strings.Contains(text, `"summary":"done"`) {
		t.Fatalf("stdout missing sanitized tool flow:\n%s", text)
	}
	if requests != 2 {
		t.Fatalf("requests = %d, want 2", requests)
	}
}

func TestRunExecOpenRouterAbortsProviderMarkupWithoutStructuredToolCalls(t *testing.T) {
	isolateSkillHomes(t)
	root := t.TempDir()
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{
			"id":"resp_bad_markup",
			"model":"deepseek/test-model",
			"choices":[{
				"finish_reason":"tool_calls",
				"message":{
					"role":"assistant",
					"content":"<\uff5cDSML\uff5ctool_calls><\uff5cDSML\uff5cinvoke name=\"run_command\">"
				}
			}]
		}`))
	}))
	defer server.Close()

	t.Setenv("OPENROUTER_API_KEY", "test-key")
	t.Setenv("OPENROUTER_BASE_URL", server.URL)

	var stdout, stderr bytes.Buffer
	code := Run([]string{
		"exec",
		"--cwd", root,
		"--data-dir", t.TempDir(),
		"--auto", "off",
		"--output", "stream-json",
		"--provider", "openrouter",
		"--model", "deepseek/test-model",
		"--max-turns", "2",
		"search files",
	}, &stdout, &stderr)
	if code == 0 {
		t.Fatalf("code = 0, want failure stdout=%s", stdout.String())
	}
	text := stdout.String()
	if strings.Contains(text, "DSML") {
		t.Fatalf("stdout should not include provider markup:\n%s", text)
	}
	for _, want := range []string{
		`"type":"turn_aborted"`,
		`provider tool-call markup`,
	} {
		if !strings.Contains(text, want) {
			t.Fatalf("stdout missing %q:\n%s", want, text)
		}
	}
	if !strings.Contains(stderr.String(), "provider tool-call markup") {
		t.Fatalf("stderr missing abort reason: %s", stderr.String())
	}
}

func TestRunExecOpenRouterFinalizesGracefullyAtMaxTurns(t *testing.T) {
	isolateSkillHomes(t)
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "README.md"), []byte("alpha\nbeta\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	requests := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests++
		var body map[string]any
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			t.Fatalf("decode request body: %v", err)
		}
		w.Header().Set("Content-Type", "application/json")
		if requests <= 2 {
			if _, ok := body["tools"]; !ok {
				t.Fatalf("tool loop request %d missing tools: %#v", requests, body)
			}
			_, _ = w.Write([]byte(`{
				"id":"resp_tool",
				"model":"deepseek/test-model",
				"choices":[{
					"finish_reason":"tool_calls",
					"message":{"role":"assistant","tool_calls":[{"id":"call_read","type":"function","function":{"name":"read_file","arguments":"{\"path\":\"README.md\",\"limit\":1}"}}]}
				}]
			}`))
			return
		}
		if _, ok := body["tools"]; ok {
			t.Fatalf("final handoff request should not include tools: %#v", body)
		}
		messages, _ := body["messages"].([]any)
		if len(messages) == 0 || !strings.Contains(fmt.Sprint(messages[len(messages)-1]), "Do not call more tools") {
			t.Fatalf("final handoff request missing no-tools prompt: %#v", body)
		}
		if !strings.Contains(fmt.Sprint(messages[len(messages)-1]), "Compact transcript of work so far") {
			t.Fatalf("final handoff request was not compacted: %#v", body)
		}
		_, _ = w.Write([]byte(`{
			"id":"resp_handoff",
			"model":"deepseek/test-model",
			"choices":[{
				"finish_reason":"stop",
				"message":{"role":"assistant","content":"Turn budget reached. I read README.md and found alpha. No files changed. Verification not run. Ask me to continue from README.md."}
			}]
		}`))
	}))
	defer server.Close()

	t.Setenv("OPENROUTER_API_KEY", "test-key")
	t.Setenv("OPENROUTER_BASE_URL", server.URL)

	var stdout, stderr bytes.Buffer
	code := Run([]string{
		"exec",
		"--cwd", root,
		"--data-dir", t.TempDir(),
		"--auto", "off",
		"--output", "stream-json",
		"--provider", "openrouter",
		"--model", "deepseek/test-model",
		"--max-turns", "2",
		"keep reading",
	}, &stdout, &stderr)
	if code != 0 {
		t.Fatalf("code = %d stderr=%s stdout=%s", code, stderr.String(), stdout.String())
	}
	text := stdout.String()
	for _, want := range []string{
		`"type":"turn_complete"`,
		`"type":"final_handoff_compacted"`,
		`Turn budget reached`,
		`"turn":3`,
	} {
		if !strings.Contains(text, want) {
			t.Fatalf("stdout missing %q:\n%s", want, text)
		}
	}
	if strings.Contains(text, `"type":"turn_aborted"`) || strings.Contains(stderr.String(), "maximum turns") {
		t.Fatalf("max turns should finalize, not abort; stderr=%s stdout=%s", stderr.String(), text)
	}
	if requests != 3 {
		t.Fatalf("requests = %d, want 3", requests)
	}
}

func TestRunExecOpenRouterRequestsSynthesisBeforeLongRunMaxTurns(t *testing.T) {
	isolateSkillHomes(t)
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "README.md"), []byte("alpha\nbeta\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	requests := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests++
		var body map[string]any
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			t.Fatalf("decode request body: %v", err)
		}
		w.Header().Set("Content-Type", "application/json")
		if requests < openRouterMinimumTurnBeforeSynthesis {
			if body["model"] != "deepseek/test-model" {
				t.Fatalf("tool loop request %d model = %#v", requests, body["model"])
			}
			if _, ok := body["tools"]; !ok {
				t.Fatalf("tool loop request %d missing tools: %#v", requests, body)
			}
			_, _ = w.Write([]byte(`{
				"id":"resp_tool",
				"model":"deepseek/test-model",
				"choices":[{
					"finish_reason":"tool_calls",
					"message":{"role":"assistant","tool_calls":[{"id":"call_read","type":"function","function":{"name":"read_file","arguments":"{\"path\":\"README.md\",\"limit\":1}"}}]}
				}]
			}`))
			return
		}
		if body["model"] != "deepseek/final-model" {
			t.Fatalf("synthesis request model = %#v, want final model", body["model"])
		}
		if _, ok := body["tools"]; ok {
			t.Fatalf("synthesis request should not include tools: %#v", body)
		}
		if _, ok := body["max_completion_tokens"]; ok {
			t.Fatalf("synthesis request should not cap max_completion_tokens: %#v", body["max_completion_tokens"])
		}
		if _, ok := body["max_tokens"]; ok {
			t.Fatalf("synthesis request should not cap max_tokens: %#v", body["max_tokens"])
		}
		if _, ok := body["reasoning"]; ok {
			t.Fatalf("synthesis request should not force reasoning options on the final model: %#v", body["reasoning"])
		}
		if _, ok := body["thinking"]; ok {
			t.Fatalf("synthesis request should not disable thinking on the final model: %#v", body["thinking"])
		}
		messages, _ := body["messages"].([]any)
		if len(messages) == 0 {
			t.Fatalf("synthesis request missing messages: %#v", body)
		}
		last := fmt.Sprint(messages[len(messages)-1])
		for _, want := range []string{
			"Original user request",
			"keep reading until synthesis",
			"Compact transcript of work so far",
			"planned synthesis checkpoint",
			"Tools are unavailable",
			"not missing merely because there is no same-named file",
		} {
			if !strings.Contains(last, want) {
				t.Fatalf("synthesis request missing %q in last message: %s", want, last)
			}
		}
		_, _ = w.Write([]byte(`{
			"id":"resp_synthesis",
			"model":"deepseek/final-model",
			"choices":[{
				"finish_reason":"stop",
				"message":{"role":"assistant","content":"synthesized before the hard cap"}
			}]
		}`))
	}))
	defer server.Close()

	t.Setenv("OPENROUTER_API_KEY", "test-key")
	t.Setenv("OPENROUTER_BASE_URL", server.URL)

	var stdout, stderr bytes.Buffer
	code := Run([]string{
		"exec",
		"--cwd", root,
		"--data-dir", t.TempDir(),
		"--auto", "off",
		"--output", "stream-json",
		"--provider", "openrouter",
		"--model", "deepseek/test-model",
		"--final-model", "deepseek/final-model",
		"--max-turns", "28",
		"keep reading until synthesis",
	}, &stdout, &stderr)
	if code != 0 {
		t.Fatalf("code = %d stderr=%s stdout=%s", code, stderr.String(), stdout.String())
	}
	text := stdout.String()
	for _, want := range []string{
		`"type":"synthesis_requested"`,
		`"final_model":"deepseek/final-model"`,
		`"force_synthesis":true`,
		`"model":"deepseek/final-model"`,
		`"summary":"synthesized before the hard cap"`,
	} {
		if !strings.Contains(text, want) {
			t.Fatalf("stdout missing %q:\n%s", want, text)
		}
	}
	if strings.Contains(text, `"type":"final_handoff_compacted"`) {
		t.Fatalf("synthesis should complete inside the normal loop, not final handoff:\n%s", text)
	}
	if requests != openRouterMinimumTurnBeforeSynthesis {
		t.Fatalf("requests = %d, want %d", requests, openRouterMinimumTurnBeforeSynthesis)
	}
}

func TestRunMetricsSummarizesSessionArtifact(t *testing.T) {
	path := filepath.Join(t.TempDir(), "session.jsonl")
	body := `{"type":"session_meta","id":"lca_metrics","cwd":"/repo"}
{"type":"tool_profile","profile":"balanced"}
{"type":"context_profile","profile":"large"}
{"type":"model_response","model":"deepseek/test","usage":{"prompt_tokens":12,"prompt_tokens_details":{"cached_tokens":4},"completion_tokens":3,"total_tokens":15,"cost":0.01}}
{"type":"tool_call","tool":"read_file","args":{"path":"README.md"}}
{"type":"tool_result","tool":"read_file","result":{"success":true,"output":"file: README.md\ntotal_lines: 2\nhas_more: false\nlines: 1-2\n\n1 | hello\n2 | world\n"}}
{"type":"turn_complete","summary":"done"}
`
	if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
	var stdout, stderr bytes.Buffer
	code := Run([]string{"metrics", path}, &stdout, &stderr)
	if code != 0 {
		t.Fatalf("code = %d stderr=%s", code, stderr.String())
	}
	text := stdout.String()
	for _, want := range []string{`"sessions": 1`, `"tool_profiles": {`, `"balanced": 1`, `"context_profiles": {`, `"large": 1`, `"read_file_calls": 1`, `"read_file_lines": 2`, `"input_tokens": 12`, `"cached_input_tokens": 4`, `"trace_quality": {`, `"grade": "mixed"`} {
		if !strings.Contains(text, want) {
			t.Fatalf("metrics output missing %q:\n%s", want, text)
		}
	}
}

func TestRunEvalReportsPassingRegressionLane(t *testing.T) {
	isolateSkillHomes(t)
	var stdout, stderr bytes.Buffer
	code := Run([]string{"eval", "--output", "json"}, &stdout, &stderr)
	if code != 0 {
		t.Fatalf("code = %d stderr=%s stdout=%s", code, stderr.String(), stdout.String())
	}
	var report evalReport
	if err := json.Unmarshal(stdout.Bytes(), &report); err != nil {
		t.Fatalf("decode eval report: %v\n%s", err, stdout.String())
	}
	if !report.Passed || len(report.Cases) != 8 {
		t.Fatalf("eval report = %#v, want eight passing cases", report)
	}
	if report.Summary.PatchDiffSummaries < 3 ||
		report.Summary.PatchFeedback < 1 ||
		report.Summary.PermissionDenials < 1 ||
		report.Summary.ResumeContexts < 1 ||
		report.Summary.VerificationChecks < 3 ||
		report.Summary.VerificationStatuses["reported_only"] < 1 ||
		report.Summary.VerificationStatuses["verified"] < 1 ||
		report.Summary.VerificationStatuses["missing_after_changes"] < 1 ||
		report.Summary.VerificationCheckStatuses["passed"] < 1 ||
		report.Summary.VerificationCheckStatuses["failed"] < 1 ||
		report.Summary.VerificationCheckStatuses["denied"] < 1 {
		t.Fatalf("eval summary missing expected trace metrics: %#v", report.Summary)
	}
	if report.Summary.TraceQuality.Score == 0 || report.Summary.TraceQuality.ToolFailures == 0 {
		t.Fatalf("eval summary missing trace quality calibration: %#v", report.Summary.TraceQuality)
	}
}

func TestRunLiveEvalListsFixedSuite(t *testing.T) {
	var stdout, stderr bytes.Buffer
	code := Run([]string{"live-eval", "--list", "--output", "json"}, &stdout, &stderr)
	if code != 0 {
		t.Fatalf("code = %d stderr=%s stdout=%s", code, stderr.String(), stdout.String())
	}
	var listed struct {
		Cases []liveEvalTask `json:"cases"`
	}
	if err := json.Unmarshal(stdout.Bytes(), &listed); err != nil {
		t.Fatalf("decode live eval list: %v\n%s", err, stdout.String())
	}
	if len(listed.Cases) != 6 {
		t.Fatalf("live eval cases = %d, want 6", len(listed.Cases))
	}
	byName := map[string]liveEvalTask{}
	for _, tc := range listed.Cases {
		byName[tc.Name] = tc
	}
	for _, want := range []string{"readme_edit_verify", "go_bug_fix", "feature_slice", "repo_orientation", "current_diff_review", "multi_file_price_refactor"} {
		if _, ok := byName[want]; !ok {
			t.Fatalf("live eval list missing %s: %#v", want, listed.Cases)
		}
	}
	if byName["go_bug_fix"].Category != "bug_fix" || len(byName["go_bug_fix"].VerifyCommand) == 0 {
		t.Fatalf("go_bug_fix list entry missing category/verification: %#v", byName["go_bug_fix"])
	}
	if byName["current_diff_review"].ExpectedVerificationStatus != "failed" || !byName["current_diff_review"].ExpectNoEdits {
		t.Fatalf("current_diff_review list entry missing review contract: %#v", byName["current_diff_review"])
	}
}

func TestRunLiveEvalRejectsUnknownCaseBeforeProviderUse(t *testing.T) {
	var stdout, stderr bytes.Buffer
	code := Run([]string{"live-eval", "--case", "missing_case", "--output", "json"}, &stdout, &stderr)
	if code == 0 {
		t.Fatalf("code = 0, want failure; stdout=%s", stdout.String())
	}
	if !strings.Contains(stderr.String(), `unknown live-eval case "missing_case"`) {
		t.Fatalf("stderr missing unknown case error: %s", stderr.String())
	}
}

func TestLiveEvalArtifactScoringHelpers(t *testing.T) {
	path := filepath.Join(t.TempDir(), "session.jsonl")
	body := `{"type":"session_meta","id":"lca_live_eval"}
{"type":"tool_result","tool":"read_file","result":{"success":true,"output":"ok"}}
{"type":"tool_result","tool":"run_command","result":{"success":false,"error":"failed"}}
{"type":"final_response","summary":"done","files_changed":["calc.go"],"verification":["go test ./..."]}
`
	if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
	failed, err := liveEvalFailedToolResults(path)
	if err != nil {
		t.Fatalf("failed tool results: %v", err)
	}
	if failed != 1 {
		t.Fatalf("failed tool results = %d, want 1", failed)
	}
	changed, err := liveEvalFinalFilesChanged(path)
	if err != nil {
		t.Fatalf("files changed: %v", err)
	}
	if !changed["calc.go"] || changed["README.md"] {
		t.Fatalf("changed files = %#v", changed)
	}
}

func TestWriteLiveEvalWorkspaceSeedsUncommittedFiles(t *testing.T) {
	workspace := t.TempDir()
	base := map[string]string{
		"go.mod": "module seeded-diff\n\ngo 1.22\n",
		"state.go": `package seeded

func State() string {
	return "clean"
}
`,
	}
	dirty := map[string]string{
		"state.go": `package seeded

func State() string {
	return "dirty"
}
`,
	}
	if err := writeLiveEvalWorkspace(workspace, base, dirty); err != nil {
		t.Fatal(err)
	}
	diff, err := liveEvalWorkspaceDiff(workspace)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(diff, `return "clean"`) || !strings.Contains(diff, `return "dirty"`) {
		t.Fatalf("seeded diff missing expected content:\n%s", diff)
	}
	dirtyState, err := liveEvalWorkspaceDirty(workspace)
	if err != nil {
		t.Fatal(err)
	}
	if !dirtyState {
		t.Fatal("workspace is clean, want seeded uncommitted diff")
	}
}

func TestCheckLiveEvalTaskAllowsSeededReadOnlyDiff(t *testing.T) {
	workspace := t.TempDir()
	files := map[string]string{
		"go.mod": "module readonly-diff\n\ngo 1.22\n",
		"tasks.go": `package tasks

func Ready(value int) bool {
	return value <= 10
}
`,
	}
	uncommitted := map[string]string{
		"tasks.go": `package tasks

func Ready(value int) bool {
	return value < 10
}
`,
	}
	if err := writeLiveEvalWorkspace(workspace, files, uncommitted); err != nil {
		t.Fatal(err)
	}
	initialDiff, err := liveEvalWorkspaceDiff(workspace)
	if err != nil {
		t.Fatal(err)
	}
	artifact := filepath.Join(t.TempDir(), "session.jsonl")
	body := `{"type":"final_response","summary":"reviewed","files_changed":[],"verification":["go test ./... failed"]}
`
	if err := os.WriteFile(artifact, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
	task := liveEvalTask{
		Name:                       "current_diff_review",
		ExpectedVerificationStatus: "failed",
		ExpectNoEdits:              true,
	}
	result := liveEvalCaseResult{
		Artifact: artifact,
		Metrics:  sessionSummaryForLiveEvalTest(map[string]int{"failed": 1}),
		Score:    liveEvalScore{ExpectedFilesTouched: true},
	}
	if err := checkLiveEvalTask(workspace, task, initialDiff, &result); err != nil {
		t.Fatalf("check seeded read-only diff: %v", err)
	}
	if !result.Score.ExpectedVerificationSeen {
		t.Fatal("expected verification status was not recorded in score")
	}
	if err := os.WriteFile(filepath.Join(workspace, "extra.txt"), []byte("unexpected\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := checkLiveEvalTask(workspace, task, initialDiff, &result); err == nil {
		t.Fatal("check succeeded after extra edit, want failure")
	}
}

func sessionSummaryForLiveEvalTest(statuses map[string]int) sessionmetrics.Summary {
	return sessionmetrics.Summary{VerificationStatuses: statuses}
}

func writeLCAgentCLITestArtifact(t *testing.T, dataDir string, started time.Time, sessionID string, events []map[string]any) string {
	t.Helper()
	path := filepath.Join(dataDir, "lcagent", "sessions", started.Format("2006"), started.Format("01"), started.Format("02"), sessionID+".jsonl")
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatalf("mkdir lcagent artifact dir: %v", err)
	}
	file, err := os.Create(path)
	if err != nil {
		t.Fatalf("create lcagent artifact: %v", err)
	}
	defer file.Close()
	encoder := json.NewEncoder(file)
	for _, event := range events {
		if err := encoder.Encode(event); err != nil {
			t.Fatalf("encode lcagent artifact: %v", err)
		}
	}
	return path
}

func isolateSkillHomes(t *testing.T) {
	t.Helper()
	t.Setenv("CODEX_HOME", filepath.Join(t.TempDir(), "codex"))
	t.Setenv("AGENTS_HOME", filepath.Join(t.TempDir(), "agents"))
}
