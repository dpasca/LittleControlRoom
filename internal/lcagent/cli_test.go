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
		`"type":"turn_complete"`,
		`"summary":"done from model"`,
	} {
		if !strings.Contains(text, want) {
			t.Fatalf("stdout missing %q:\n%s", want, text)
		}
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

func isolateSkillHomes(t *testing.T) {
	t.Helper()
	t.Setenv("CODEX_HOME", filepath.Join(t.TempDir(), "codex"))
	t.Setenv("AGENTS_HOME", filepath.Join(t.TempDir(), "agents"))
}
