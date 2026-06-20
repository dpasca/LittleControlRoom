package lcagent

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"lcroom/internal/lcagent/modeladapter"
	"lcroom/internal/lcagent/policy"
	"lcroom/internal/lcagent/script"
	"lcroom/internal/lcagent/session"
	"lcroom/internal/lcagent/sessionmetrics"
	skillcatalog "lcroom/internal/lcagent/skills"
	"lcroom/internal/lcagent/tools"
)

func TestRunVersion(t *testing.T) {
	var stdout, stderr bytes.Buffer
	code := Run([]string{"--version"}, &stdout, &stderr)
	if code != 0 {
		t.Fatalf("code = %d stderr=%s", code, stderr.String())
	}
	if got, want := strings.TrimSpace(stdout.String()), "lcagent dev"; got != want {
		t.Fatalf("version output = %q, want %q", got, want)
	}
}

func TestShouldDeferSynthesisForUnverifiedChanges(t *testing.T) {
	guidance := openRouterProgressGuidance{
		Turn:           40,
		MaxTurns:       48,
		TurnsRemaining: 8,
		Phase:          "synthesis",
		ForceSynthesis: true,
	}
	if !shouldDeferSynthesisForUnverifiedChanges(guidance, 3, 2) {
		t.Fatal("expected synthesis to defer while file changes are newer than passed verification")
	}
	if shouldDeferSynthesisForUnverifiedChanges(guidance, 3, 3) {
		t.Fatal("did not expect synthesis deferral after current changes have passed verification")
	}
	guidance.TurnsRemaining = 0
	if shouldDeferSynthesisForUnverifiedChanges(guidance, 4, 3) {
		t.Fatal("did not expect synthesis deferral at the hard turn limit")
	}
}

func TestOpenRouterLoopProgressTrackerForcesSynthesisAfterRepeatedEvidence(t *testing.T) {
	tracker := newOpenRouterLoopProgressTracker(nil, script.Runner{})
	messages := []modeladapter.Message{openRouterLoopProgressTestToolResult(t, "same evidence", time.Millisecond)}
	tracker.Observe(messages, script.Runner{})
	if tracker.NoProgressTurns() != 0 {
		t.Fatalf("no progress turns after first evidence = %d, want 0", tracker.NoProgressTurns())
	}
	for i := 0; i < openRouterStallSynthesisAfterTurns; i++ {
		messages = append(messages, openRouterLoopProgressTestToolResult(t, "same evidence", time.Duration(i+2)*time.Millisecond))
		tracker.Observe(messages, script.Runner{})
	}
	guidance := openRouterProgressGuidance{
		Turn:           openRouterMinimumTurnBeforeStallCheck,
		MaxTurns:       modeladapter.DefaultOpenRouterMaxTurns,
		TurnsRemaining: modeladapter.DefaultOpenRouterMaxTurns - openRouterMinimumTurnBeforeStallCheck,
		Phase:          "consolidation",
	}
	if !tracker.ShouldForceSynthesis(guidance) {
		t.Fatalf("expected repeated identical evidence to force stall synthesis after %d no-progress turns", tracker.NoProgressTurns())
	}
	messages = append(messages, openRouterLoopProgressTestToolResult(t, "new evidence", time.Millisecond))
	tracker.Observe(messages, script.Runner{})
	if tracker.NoProgressTurns() != 0 {
		t.Fatalf("no progress turns after new evidence = %d, want reset", tracker.NoProgressTurns())
	}
	if tracker.ShouldForceSynthesis(guidance) {
		t.Fatal("did not expect stall synthesis immediately after new evidence")
	}
}

func openRouterLoopProgressTestToolResult(t *testing.T, output string, duration time.Duration) modeladapter.Message {
	t.Helper()
	result := tools.ToolResult{Success: true, Output: output, Duration: duration}
	data, err := json.Marshal(result)
	if err != nil {
		t.Fatal(err)
	}
	return modeladapter.Message{Role: "tool", Content: string(data)}
}

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
	if !strings.Contains(stdout.String(), `"type":"session_meta"`) || !strings.Contains(stdout.String(), `"host_os":`) || !strings.Contains(stdout.String(), `"host_arch":`) || !strings.Contains(stdout.String(), `"type":"project_instructions"`) || !strings.Contains(stdout.String(), `"type":"turn_complete"`) {
		t.Fatalf("stdout missing events:\n%s", stdout.String())
	}
}

func TestRunExecEmitsManagedBrowserCapability(t *testing.T) {
	isolateSkillHomes(t)
	root := t.TempDir()
	scriptPath := filepath.Join(t.TempDir(), "script.jsonl")
	script := `{"type":"tool_call","tool":"load_skill","args":{"name":"playwright"}}
{"type":"final_response","summary":"done","files_changed":[],"verification":[]}
`
	if err := os.WriteFile(scriptPath, []byte(script), 0o600); err != nil {
		t.Fatal(err)
	}
	var stdout, stderr bytes.Buffer
	code := Run([]string{
		"exec",
		"--cwd", root,
		"--data-dir", t.TempDir(),
		"--auto", "low",
		"--output", "stream-json",
		"--script", scriptPath,
		"--browser-control", "managed",
		"--browser-session-key", "session-demo",
		"--browser-profile-key", "profile-demo",
		"--browser-launch-mode", "background",
		"browser capable",
	}, &stdout, &stderr)
	if code != 0 {
		t.Fatalf("code = %d stderr=%s", code, stderr.String())
	}
	text := stdout.String()
	for _, want := range []string{
		`"type":"browser_capability"`,
		`"enabled":true`,
		`"launch_mode":"background"`,
		`"session_key":"session-demo"`,
		`"profile_key":"profile-demo"`,
		`This LCAgent run has native browser tools`,
		`Use browser_navigate`,
		`Do not run npx`,
	} {
		if !strings.Contains(text, want) {
			t.Fatalf("stdout missing %q:\n%s", want, text)
		}
	}
}

func TestRunExecShadowsPlaywrightSkillWhenBrowserUnavailable(t *testing.T) {
	isolateSkillHomes(t)
	root := t.TempDir()
	codexHome := os.Getenv("CODEX_HOME")
	if err := os.MkdirAll(filepath.Join(codexHome, "skills", "playwright"), 0o755); err != nil {
		t.Fatal(err)
	}
	staleBody := "Run playwright_cli.sh from the terminal."
	if err := os.WriteFile(filepath.Join(codexHome, "skills", "playwright", "SKILL.md"), []byte("---\nname: playwright\ndescription: stale browser workflow\n---\n"+staleBody+"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	scriptPath := filepath.Join(t.TempDir(), "script.jsonl")
	script := `{"type":"tool_call","tool":"load_skill","args":{"name":"playwright"}}
{"type":"final_response","summary":"done","files_changed":[],"verification":[]}
`
	if err := os.WriteFile(scriptPath, []byte(script), 0o600); err != nil {
		t.Fatal(err)
	}

	var stdout, stderr bytes.Buffer
	code := Run([]string{
		"exec",
		"--cwd", root,
		"--data-dir", t.TempDir(),
		"--auto", "low",
		"--output", "stream-json",
		"--script", scriptPath,
		"browser unavailable",
	}, &stdout, &stderr)
	if code != 0 {
		t.Fatalf("code = %d stderr=%s", code, stderr.String())
	}
	text := stdout.String()
	for _, want := range []string{
		`"type":"browser_capability"`,
		`"enabled":false`,
		`Browser control is not available in this LCAgent run`,
		`Do not run Playwright CLI`,
	} {
		if !strings.Contains(text, want) {
			t.Fatalf("stdout missing %q:\n%s", want, text)
		}
	}
	if strings.Contains(text, staleBody) {
		t.Fatalf("stdout kept stale playwright skill body:\n%s", text)
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
	dataDir := t.TempDir()
	code := Run([]string{
		"exec",
		"--cwd", root,
		"--data-dir", dataDir,
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
	sessionID := lcagentCLITestSessionIDFromStream(t, text)
	path, err := resolveResumeContextPath(dataDir, sessionID)
	if err != nil {
		t.Fatalf("resolve session artifact: %v", err)
	}
	artifact, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read session artifact: %v", err)
	}
	if !strings.Contains(string(artifact), `"type":"model_context_snapshot"`) {
		t.Fatalf("artifact missing private model context snapshot:\n%s", string(artifact))
	}
	if got := strings.Count(text, `"type":"assistant_message"`); got != 1 {
		t.Fatalf("assistant_message count = %d, want 1:\n%s", got, text)
	}
}

func TestRunExecOpenRouterEmitsModelRequestProgress(t *testing.T) {
	isolateSkillHomes(t)
	root := t.TempDir()
	previousInterval := modelRequestProgressInterval
	modelRequestProgressInterval = 10 * time.Millisecond
	t.Cleanup(func() {
		modelRequestProgressInterval = previousInterval
	})
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/chat/completions" {
			t.Fatalf("request path = %s, want /chat/completions", r.URL.Path)
		}
		time.Sleep(60 * time.Millisecond)
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{
			"id":"resp_slow",
			"model":"deepseek/test-model",
			"choices":[{
				"finish_reason":"stop",
				"message":{"role":"assistant","content":"done after slow model request"}
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
		`"type":"model_request_started"`,
		`"type":"model_request_progress"`,
		`"elapsed_ms"`,
		`"phase":"tool_loop"`,
		`"turn":1`,
		`"attempt":1`,
		`"type":"model_response"`,
		`"summary":"done after slow model request"`,
	} {
		if !strings.Contains(text, want) {
			t.Fatalf("stdout missing %q:\n%s", want, text)
		}
	}
}

func TestRunExecOpenRouterRequiresFinalResponseTool(t *testing.T) {
	isolateSkillHomes(t)
	root := t.TempDir()
	requests := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests++
		if r.URL.Path != "/chat/completions" {
			t.Fatalf("request path = %s, want /chat/completions", r.URL.Path)
		}
		var body map[string]any
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			t.Fatalf("decode request body: %v", err)
		}
		w.Header().Set("Content-Type", "application/json")
		if requests == 2 {
			_, _ = w.Write([]byte(`{
				"id":"resp_final",
				"model":"deepseek/test-model",
				"choices":[{
					"finish_reason":"tool_calls",
					"message":{"role":"assistant","tool_calls":[{"id":"call_final","type":"function","function":{"name":"final_response","arguments":{"summary":"done from model","outcome":"completed","files_changed":[],"verification":[]}}}]}
				}],
				"usage":{"prompt_tokens":8,"completion_tokens":4,"total_tokens":12}
			}`))
			return
		}
		_, _ = w.Write([]byte(`{
			"id":"resp_plain",
			"model":"deepseek/test-model",
			"choices":[{
				"finish_reason":"stop",
				"message":{"role":"assistant","content":"plain final"}
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
		"--require-final-response-tool",
		"answer directly",
	}, &stdout, &stderr)
	if code != 0 {
		t.Fatalf("code = %d stderr=%s stdout=%s", code, stderr.String(), stdout.String())
	}
	text := stdout.String()
	for _, want := range []string{
		`"type":"final_response_feedback"`,
		`"response_id":"resp_final"`,
		`"type":"turn_complete"`,
		`"summary":"done from model"`,
		`"final_outcome":"completed"`,
	} {
		if !strings.Contains(text, want) {
			t.Fatalf("stdout missing %q:\n%s", want, text)
		}
	}
	if requests != 2 {
		t.Fatalf("requests = %d, want 2", requests)
	}
}

func TestRunExecOpenRouterQualityCheckpointFlagIsNoOp(t *testing.T) {
	isolateSkillHomes(t)
	root := t.TempDir()
	requests := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests++
		if r.URL.Path != "/chat/completions" {
			t.Fatalf("request path = %s, want /chat/completions", r.URL.Path)
		}
		var body map[string]any
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			t.Fatalf("decode request body: %v", err)
		}
		w.Header().Set("Content-Type", "application/json")
		if requests != 1 {
			t.Fatalf("unexpected request %d", requests)
		}
		if body["model"] != "deepseek/test-model" {
			t.Fatalf("request model = %q, want lead model", body["model"])
		}
		_ = json.NewEncoder(w).Encode(map[string]any{
			"id":    "resp_first_final",
			"model": "deepseek/test-model",
			"choices": []any{map[string]any{
				"finish_reason": "tool_calls",
				"message": map[string]any{
					"role": "assistant",
					"tool_calls": []any{map[string]any{
						"id":   "call_first_final",
						"type": "function",
						"function": map[string]any{
							"name": "final_response",
							"arguments": map[string]any{
								"summary":       "first answer",
								"outcome":       "completed",
								"files_changed": []any{},
								"verification":  []any{},
							},
						},
					}},
				},
			}},
			"usage": map[string]any{"prompt_tokens": 8, "completion_tokens": 4, "total_tokens": 12},
		})
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
		"--quality-checkpoint-passes", "1",
		"--max-turns", "3",
		"answer directly",
	}, &stdout, &stderr)
	if code != 0 {
		t.Fatalf("code = %d stderr=%s stdout=%s", code, stderr.String(), stdout.String())
	}
	if requests != 1 {
		t.Fatalf("requests = %d, want 1\nstdout=%s", requests, stdout.String())
	}
	text := stdout.String()
	for _, want := range []string{
		`"type":"quality_checkpoint_profile"`,
		`"type":"turn_complete"`,
		`"summary":"first answer"`,
	} {
		if !strings.Contains(text, want) {
			t.Fatalf("stdout missing %q:\n%s", want, text)
		}
	}
	for _, unwanted := range []string{`"type":"quality_checkpoint_started"`, `"type":"quality_checkpoint_feedback"`} {
		if strings.Contains(text, unwanted) {
			t.Fatalf("stdout unexpectedly contains %q:\n%s", unwanted, text)
		}
	}
}

func TestRunExecPlanningPreflightRequiresQualityPlanForSizableWork(t *testing.T) {
	isolateSkillHomes(t)
	root := t.TempDir()
	requests := 0
	sawPlanningLeadMessage := false
	sawMissingPlanFeedback := false
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests++
		if r.URL.Path != "/chat/completions" {
			t.Fatalf("request path = %s, want /chat/completions", r.URL.Path)
		}
		var body struct {
			Model    string                 `json:"model"`
			Messages []modeladapter.Message `json:"messages"`
		}
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			t.Fatalf("decode request body: %v", err)
		}
		w.Header().Set("Content-Type", "application/json")
		switch requests {
		case 1:
			if body.Model != "deepseek/test-model" {
				t.Fatalf("preflight model = %q, want lead model", body.Model)
			}
			_ = json.NewEncoder(w).Encode(map[string]any{
				"id":    "resp_preflight",
				"model": "deepseek/test-model",
				"choices": []any{map[string]any{
					"finish_reason": "stop",
					"message": map[string]any{
						"role":    "assistant",
						"content": `{"scope":"sizable","needs_preplan":true,"artifact_type":"document","requires_runtime_verification":false,"requires_visual_verification":false,"reason":"substantial generated artifact benefits from sequencing","suggested_phases":[{"name":"structure","acceptance":["outline exists"]},{"name":"polish","acceptance":["final pass complete"]}]}`,
					},
				}},
				"usage": map[string]any{"prompt_tokens": 8, "completion_tokens": 4, "total_tokens": 12},
			})
		case 2:
			for _, msg := range body.Messages {
				if strings.Contains(msg.Content, "planning preflight classified this request as sizable") {
					sawPlanningLeadMessage = true
				}
			}
			_ = json.NewEncoder(w).Encode(map[string]any{
				"id":    "resp_no_plan_final",
				"model": "deepseek/test-model",
				"choices": []any{map[string]any{
					"finish_reason": "tool_calls",
					"message": map[string]any{
						"role": "assistant",
						"tool_calls": []any{map[string]any{
							"id":   "call_no_plan_final",
							"type": "function",
							"function": map[string]any{
								"name": "final_response",
								"arguments": map[string]any{
									"summary":       "done too soon",
									"outcome":       "completed",
									"files_changed": []any{},
									"verification":  []any{},
								},
							},
						}},
					},
				}},
				"usage": map[string]any{"prompt_tokens": 9, "completion_tokens": 4, "total_tokens": 13},
			})
		case 3:
			for _, msg := range body.Messages {
				if strings.Contains(msg.Content, "no quality plan has been recorded") {
					sawMissingPlanFeedback = true
				}
			}
			_ = json.NewEncoder(w).Encode(map[string]any{
				"id":    "resp_plan_started",
				"model": "deepseek/test-model",
				"choices": []any{map[string]any{
					"finish_reason": "tool_calls",
					"message": map[string]any{
						"role": "assistant",
						"tool_calls": []any{map[string]any{
							"id":   "call_plan_started",
							"type": "function",
							"function": map[string]any{
								"name": "update_quality_plan",
								"arguments": map[string]any{
									"artifact_type":                 "document",
									"requires_runtime_verification": false,
									"requires_visual_verification":  false,
									"phases": []any{map[string]any{
										"name":       "structure",
										"status":     "in_progress",
										"acceptance": []any{"outline exists"},
									}},
								},
							},
						}},
					},
				}},
				"usage": map[string]any{"prompt_tokens": 10, "completion_tokens": 5, "total_tokens": 15},
			})
		case 4:
			_ = json.NewEncoder(w).Encode(map[string]any{
				"id":    "resp_plan_verified_final",
				"model": "deepseek/test-model",
				"choices": []any{map[string]any{
					"finish_reason": "tool_calls",
					"message": map[string]any{
						"role": "assistant",
						"tool_calls": []any{
							map[string]any{
								"id":   "call_plan_verified",
								"type": "function",
								"function": map[string]any{
									"name": "update_quality_plan",
									"arguments": map[string]any{
										"artifact_type":                 "document",
										"requires_runtime_verification": false,
										"requires_visual_verification":  false,
										"phases": []any{map[string]any{
											"name":       "structure",
											"status":     "verified",
											"acceptance": []any{"outline exists"},
											"evidence":   []any{"created and reviewed final structure"},
										}},
									},
								},
							},
							map[string]any{
								"id":   "call_final_after_plan",
								"type": "function",
								"function": map[string]any{
									"name": "final_response",
									"arguments": map[string]any{
										"summary":       "finished with plan evidence",
										"outcome":       "completed",
										"files_changed": []any{},
										"verification":  []any{},
									},
								},
							},
						},
					},
				}},
				"usage": map[string]any{"prompt_tokens": 11, "completion_tokens": 6, "total_tokens": 17},
			})
		default:
			t.Fatalf("unexpected request %d", requests)
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
		"--auto", "off",
		"--output", "stream-json",
		"--provider", "openrouter",
		"--model", "deepseek/test-model",
		"--max-turns", "4",
		"--require-final-response-tool",
		"--planning-preflight",
		"write a substantial project brief",
	}, &stdout, &stderr)
	if code != 0 {
		t.Fatalf("code = %d stderr=%s stdout=%s", code, stderr.String(), stdout.String())
	}
	if requests != 4 {
		t.Fatalf("requests = %d, want 4\nstdout=%s", requests, stdout.String())
	}
	if !sawPlanningLeadMessage {
		t.Fatalf("lead request did not include planning preflight guidance")
	}
	if !sawMissingPlanFeedback {
		t.Fatalf("lead request did not receive missing-plan audit feedback")
	}
	text := stdout.String()
	for _, want := range []string{
		`"type":"planning_preflight_profile"`,
		`"type":"planning_preflight_result"`,
		`"scope":"sizable"`,
		`"code":"quality_plan_required_missing"`,
		`"type":"quality_plan_update"`,
		`"type":"turn_complete"`,
		`"summary":"finished with plan evidence"`,
	} {
		if !strings.Contains(text, want) {
			t.Fatalf("stdout missing %q:\n%s", want, text)
		}
	}
}

func TestRunExecOpenRouterCriticProviderDoesNotAutoReviewFinal(t *testing.T) {
	isolateSkillHomes(t)
	root := t.TempDir()
	requests := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests++
		if r.URL.Path != "/chat/completions" {
			t.Fatalf("request path = %s, want /chat/completions", r.URL.Path)
		}
		var body map[string]any
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			t.Fatalf("decode request body: %v", err)
		}
		w.Header().Set("Content-Type", "application/json")
		switch requests {
		case 1:
			if body["model"] != "deepseek/test-model" {
				t.Fatalf("request 1 model = %q, want lead model", body["model"])
			}
			_ = json.NewEncoder(w).Encode(map[string]any{
				"id":    "resp_write_file",
				"model": "deepseek/test-model",
				"choices": []any{map[string]any{
					"finish_reason": "tool_calls",
					"message": map[string]any{
						"role": "assistant",
						"tool_calls": []any{map[string]any{
							"id":   "call_create_file",
							"type": "function",
							"function": map[string]any{
								"name": "create_file",
								"arguments": map[string]any{
									"path":    "answer.txt",
									"content": "wrong answer\n",
								},
							},
						}},
					},
				}},
				"usage": map[string]any{"prompt_tokens": 8, "completion_tokens": 4, "total_tokens": 12},
			})
		case 2:
			if body["model"] != "deepseek/test-model" {
				t.Fatalf("request 2 model = %q, want lead model", body["model"])
			}
			_ = json.NewEncoder(w).Encode(map[string]any{
				"id":    "resp_bad_final",
				"model": "deepseek/test-model",
				"choices": []any{map[string]any{
					"finish_reason": "tool_calls",
					"message": map[string]any{
						"role": "assistant",
						"tool_calls": []any{map[string]any{
							"id":   "call_bad_final",
							"type": "function",
							"function": map[string]any{
								"name": "final_response",
								"arguments": map[string]any{
									"summary":       "wrong answer",
									"outcome":       "completed",
									"files_changed": []any{},
									"verification":  []any{},
								},
							},
						}},
					},
				}},
				"usage": map[string]any{"prompt_tokens": 9, "completion_tokens": 4, "total_tokens": 13},
			})
		default:
			t.Fatalf("unexpected request %d", requests)
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
		"--critic-provider", "openrouter",
		"--critic-model", "critic/test-model",
		"--critic-reasoning-effort", "low",
		"--max-turns", "5",
		"write an answer file",
	}, &stdout, &stderr)
	if code != 0 {
		t.Fatalf("code = %d stderr=%s stdout=%s", code, stderr.String(), stdout.String())
	}
	if requests != 2 {
		t.Fatalf("requests = %d, want 2\nstdout=%s", requests, stdout.String())
	}
	text := stdout.String()
	for _, want := range []string{
		`"type":"turn_complete"`,
		`"summary":"wrong answer"`,
	} {
		if !strings.Contains(text, want) {
			t.Fatalf("stdout missing %q:\n%s", want, text)
		}
	}
	for _, unwanted := range []string{`"type":"critic_review_started"`, `"type":"critic_lead_feedback"`} {
		if strings.Contains(text, unwanted) {
			t.Fatalf("stdout unexpectedly contains %q:\n%s", unwanted, text)
		}
	}
}

func TestRunExecOpenRouterDeprecatedQualityRepairDoesNotRunCritic(t *testing.T) {
	isolateSkillHomes(t)
	root := t.TempDir()
	requests := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests++
		if r.URL.Path != "/chat/completions" {
			t.Fatalf("request path = %s, want /chat/completions", r.URL.Path)
		}
		var body map[string]any
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			t.Fatalf("decode request body: %v", err)
		}
		w.Header().Set("Content-Type", "application/json")
		switch requests {
		case 1:
			_ = json.NewEncoder(w).Encode(map[string]any{
				"id":    "resp_create_file",
				"model": "deepseek/test-model",
				"choices": []any{map[string]any{
					"finish_reason": "tool_calls",
					"message": map[string]any{
						"role": "assistant",
						"tool_calls": []any{map[string]any{
							"id":   "call_create_file",
							"type": "function",
							"function": map[string]any{
								"name": "create_file",
								"arguments": map[string]any{
									"path":    "game.txt",
									"content": "rough visual game placeholder\n",
								},
							},
						}},
					},
				}},
				"usage": map[string]any{"prompt_tokens": 8, "completion_tokens": 4, "total_tokens": 12},
			})
		case 2:
			if body["model"] != "deepseek/test-model" {
				t.Fatalf("request 2 model = %q, want lead model", body["model"])
			}
			_ = json.NewEncoder(w).Encode(map[string]any{
				"id":    "resp_verify_file",
				"model": "deepseek/test-model",
				"choices": []any{map[string]any{
					"finish_reason": "tool_calls",
					"message": map[string]any{
						"role": "assistant",
						"tool_calls": []any{map[string]any{
							"id":   "call_verify_file",
							"type": "function",
							"function": map[string]any{
								"name": "run_command",
								"arguments": map[string]any{
									"argv":    []any{"test", "-f", "game.txt"},
									"purpose": "verify",
								},
							},
						}},
					},
				}},
				"usage": map[string]any{"prompt_tokens": 8, "completion_tokens": 4, "total_tokens": 12},
			})
		case 3:
			if body["model"] != "deepseek/test-model" {
				t.Fatalf("request 3 model = %q, want lead model", body["model"])
			}
			_ = json.NewEncoder(w).Encode(map[string]any{
				"id":    "resp_bad_final",
				"model": "deepseek/test-model",
				"choices": []any{map[string]any{
					"finish_reason": "tool_calls",
					"message": map[string]any{
						"role": "assistant",
						"tool_calls": []any{map[string]any{
							"id":   "call_bad_final",
							"type": "function",
							"function": map[string]any{
								"name": "final_response",
								"arguments": map[string]any{
									"summary":       "visual game is excellent",
									"outcome":       "completed",
									"files_changed": []any{"game.txt"},
									"verification":  []any{},
								},
							},
						}},
					},
				}},
				"usage": map[string]any{"prompt_tokens": 8, "completion_tokens": 4, "total_tokens": 12},
			})
		default:
			t.Fatalf("unexpected request %d", requests)
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
		"--auto", "medium",
		"--output", "stream-json",
		"--provider", "openrouter",
		"--model", "deepseek/test-model",
		"--critic-provider", "openrouter",
		"--critic-model", "critic/test-model",
		"--quality-repair-passes", "3",
		"--max-turns", "7",
		"make a visual game",
	}, &stdout, &stderr)
	if code != 0 {
		t.Fatalf("code = %d stderr=%s stdout=%s", code, stderr.String(), stdout.String())
	}
	if requests != 3 {
		t.Fatalf("requests = %d, want 3\nstdout=%s", requests, stdout.String())
	}
	text := stdout.String()
	for _, want := range []string{
		`"type":"quality_repair_profile"`,
		`"enabled":false`,
		`"summary":"visual game is excellent"`,
		`"final_outcome":"completed"`,
	} {
		if !strings.Contains(text, want) {
			t.Fatalf("stdout missing %q:\n%s", want, text)
		}
	}
	for _, unwanted := range []string{`"type":"quality_repair_feedback"`, `"type":"critic_review_started"`, `"type":"critic_lead_feedback"`} {
		if strings.Contains(text, unwanted) {
			t.Fatalf("stdout unexpectedly contains %q:\n%s", unwanted, text)
		}
	}
}

func TestRunExecOpenRouterDeprecatedQualityRepairDoesNotReviewPlainFinalWithoutChanges(t *testing.T) {
	isolateSkillHomes(t)
	root := t.TempDir()
	requests := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests++
		if r.URL.Path != "/chat/completions" {
			t.Fatalf("request path = %s, want /chat/completions", r.URL.Path)
		}
		var body map[string]any
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			t.Fatalf("decode request body: %v", err)
		}
		w.Header().Set("Content-Type", "application/json")
		switch requests {
		case 1:
			_ = json.NewEncoder(w).Encode(map[string]any{
				"id":    "resp_plain_final",
				"model": "deepseek/test-model",
				"choices": []any{map[string]any{
					"finish_reason": "stop",
					"message": map[string]any{
						"role":    "assistant",
						"content": "The visual game is complete and looks excellent.",
					},
				}},
				"usage": map[string]any{"prompt_tokens": 8, "completion_tokens": 4, "total_tokens": 12},
			})
		default:
			t.Fatalf("unexpected request %d", requests)
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
		"--auto", "off",
		"--output", "stream-json",
		"--provider", "openrouter",
		"--model", "deepseek/test-model",
		"--critic-provider", "openrouter",
		"--critic-model", "critic/test-model",
		"--quality-repair-passes", "3",
		"--max-turns", "4",
		"make a visual game",
	}, &stdout, &stderr)
	if code != 0 {
		t.Fatalf("code = %d stderr=%s stdout=%s", code, stderr.String(), stdout.String())
	}
	if requests != 1 {
		t.Fatalf("requests = %d, want 1\nstdout=%s", requests, stdout.String())
	}
	text := stdout.String()
	for _, want := range []string{
		`"type":"quality_repair_profile"`,
		`"enabled":false`,
		`"summary":"The visual game is complete and looks excellent."`,
	} {
		if !strings.Contains(text, want) {
			t.Fatalf("stdout missing %q:\n%s", want, text)
		}
	}
	if strings.Contains(text, `"type":"quality_repair_feedback"`) || strings.Contains(text, `"type":"critic_review_started"`) {
		t.Fatalf("deprecated repair path unexpectedly reviewed plain final:\n%s", text)
	}
}

func TestRunExecOpenRouterCriticRetriesInvalidJSONOnce(t *testing.T) {
	isolateSkillHomes(t)
	root := t.TempDir()
	dataDir := t.TempDir()
	requests := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests++
		if r.URL.Path != "/chat/completions" {
			t.Fatalf("request path = %s, want /chat/completions", r.URL.Path)
		}
		var body map[string]any
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			t.Fatalf("decode request body: %v", err)
		}
		w.Header().Set("Content-Type", "application/json")
		switch requests {
		case 1:
			if body["model"] != "deepseek/test-model" {
				t.Fatalf("request 1 model = %q, want lead model", body["model"])
			}
			_ = json.NewEncoder(w).Encode(map[string]any{
				"id":    "resp_consult",
				"model": "deepseek/test-model",
				"choices": []any{map[string]any{
					"finish_reason": "tool_calls",
					"message": map[string]any{
						"role": "assistant",
						"tool_calls": []any{map[string]any{
							"id":   "call_consult",
							"type": "function",
							"function": map[string]any{
								"name": "consult_critic",
								"arguments": map[string]any{
									"kind":      "plan",
									"question":  "Is this candidate plan safe enough?",
									"candidate": "candidate answer",
								},
							},
						}},
					},
				}},
				"usage": map[string]any{"prompt_tokens": 8, "completion_tokens": 4, "total_tokens": 12},
			})
		case 2:
			if body["model"] != "critic/test-model" {
				t.Fatalf("request 2 model = %q, want critic model", body["model"])
			}
			reasoning, _ := body["reasoning"].(map[string]any)
			if reasoning["effort"] != "low" {
				t.Fatalf("critic reasoning = %#v, want low effort", body["reasoning"])
			}
			_ = json.NewEncoder(w).Encode(map[string]any{
				"id":    "resp_critic_invalid",
				"model": "critic/test-model",
				"choices": []any{map[string]any{
					"finish_reason": "stop",
					"message": map[string]any{
						"role":    "assistant",
						"content": "This is not JSON",
					},
				}},
				"usage": map[string]any{"prompt_tokens": 13, "completion_tokens": 5, "total_tokens": 18},
			})
		case 3:
			if body["model"] != "critic/test-model" {
				t.Fatalf("request 3 model = %q, want critic model", body["model"])
			}
			messagesJSON, _ := json.Marshal(body["messages"])
			if !strings.Contains(string(messagesJSON), "Your previous critic response was not valid JSON") || !strings.Contains(string(messagesJSON), "This is not JSON") {
				t.Fatalf("critic retry request missing repair context:\n%s", messagesJSON)
			}
			_ = json.NewEncoder(w).Encode(map[string]any{
				"id":    "resp_critic_clean",
				"model": "critic/test-model",
				"choices": []any{map[string]any{
					"finish_reason": "stop",
					"message": map[string]any{
						"role":    "assistant",
						"content": `{"status":"clean","confidence":0.88,"summary":"no material issues","findings":[],"lead_instruction":"","human_prompt":"","proposed_user_message":""}`,
					},
				}},
				"usage": map[string]any{"prompt_tokens": 14, "completion_tokens": 6, "total_tokens": 20},
			})
		case 4:
			if body["model"] != "deepseek/test-model" {
				t.Fatalf("request 4 model = %q, want lead model", body["model"])
			}
			_ = json.NewEncoder(w).Encode(map[string]any{
				"id":    "resp_final",
				"model": "deepseek/test-model",
				"choices": []any{map[string]any{
					"finish_reason": "tool_calls",
					"message": map[string]any{
						"role": "assistant",
						"tool_calls": []any{map[string]any{
							"id":   "call_final",
							"type": "function",
							"function": map[string]any{
								"name": "final_response",
								"arguments": map[string]any{
									"summary":       "candidate answer",
									"outcome":       "completed",
									"files_changed": []any{},
									"verification":  []any{},
								},
							},
						}},
					},
				}},
				"usage": map[string]any{"prompt_tokens": 9, "completion_tokens": 4, "total_tokens": 13},
			})
		default:
			t.Fatalf("unexpected request %d", requests)
		}
	}))
	defer server.Close()

	t.Setenv("OPENROUTER_API_KEY", "test-key")
	t.Setenv("OPENROUTER_BASE_URL", server.URL)

	var stdout, stderr bytes.Buffer
	code := Run([]string{
		"exec",
		"--cwd", root,
		"--data-dir", dataDir,
		"--auto", "low",
		"--output", "stream-json",
		"--provider", "openrouter",
		"--model", "deepseek/test-model",
		"--critic-provider", "openrouter",
		"--critic-model", "critic/test-model",
		"--critic-reasoning-effort", "low",
		"--max-turns", "4",
		"write an answer file",
	}, &stdout, &stderr)
	if code != 0 {
		t.Fatalf("code = %d stderr=%s stdout=%s", code, stderr.String(), stdout.String())
	}
	if requests != 4 {
		t.Fatalf("requests = %d, want 4\nstdout=%s", requests, stdout.String())
	}
	text := stdout.String()
	for _, want := range []string{
		`"type":"critic_model_response_invalid"`,
		`"type":"critic_review_retry"`,
		`"type":"critic_consult_result"`,
		`"status":"clean"`,
		`"summary":"candidate answer"`,
	} {
		if !strings.Contains(text, want) {
			t.Fatalf("stdout missing %q:\n%s", want, text)
		}
	}
	if strings.Contains(text, `"type":"critic_review_failed"`) || strings.Contains(text, "This is not JSON") {
		t.Fatalf("stdout leaked raw invalid response or failure:\n%s", text)
	}

	privateTrace := readSingleLCAgentSessionTraceForTest(t, dataDir)
	if !strings.Contains(privateTrace, `"type":"critic_model_response_invalid_raw"`) || !strings.Contains(privateTrace, "This is not JSON") {
		t.Fatalf("private trace missing raw invalid critic output:\n%s", privateTrace)
	}
}

func readSingleLCAgentSessionTraceForTest(t *testing.T, dataDir string) string {
	t.Helper()
	var matches []string
	root := filepath.Join(dataDir, "lcagent", "sessions")
	if err := filepath.WalkDir(root, func(path string, entry os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if entry == nil || entry.IsDir() || filepath.Ext(path) != ".jsonl" {
			return nil
		}
		matches = append(matches, path)
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	if len(matches) != 1 {
		t.Fatalf("session traces = %v, want exactly one", matches)
	}
	body, err := os.ReadFile(matches[0])
	if err != nil {
		t.Fatal(err)
	}
	return string(body)
}

func TestRunChatLoopUsesManagedProcessToolsWhenAvailable(t *testing.T) {
	isolateSkillHomes(t)
	root := t.TempDir()
	dataDir := t.TempDir()
	var providerRequests int
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		providerRequests++
		if r.URL.Path != "/chat/completions" {
			t.Fatalf("request path = %s, want /chat/completions", r.URL.Path)
		}
		var body map[string]any
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			t.Fatalf("decode request body: %v", err)
		}
		if providerRequests == 1 {
			toolsRaw, _ := body["tools"].([]any)
			if !lcagentCLITestRequestHasTool(toolsRaw, "start_process") || !lcagentCLITestRequestHasTool(toolsRaw, "list_processes") {
				t.Fatalf("first request missing managed process tools: %#v", toolsRaw)
			}
		}
		w.Header().Set("Content-Type", "application/json")
		switch providerRequests {
		case 1:
			_, _ = w.Write([]byte(`{
				"id":"resp_start",
				"model":"deepseek/test-model",
				"choices":[{
					"finish_reason":"tool_calls",
					"message":{"role":"assistant","tool_calls":[{"id":"call_start","type":"function","function":{"name":"start_process","arguments":{"command":"npm run dev","name":"dev-server"}}}]}
				}]
			}`))
		case 2:
			_, _ = w.Write([]byte(`{
				"id":"resp_list",
				"model":"deepseek/test-model",
				"choices":[{
					"finish_reason":"tool_calls",
					"message":{"role":"assistant","tool_calls":[{"id":"call_list","type":"function","function":{"name":"list_processes","arguments":{}}}]}
				}]
			}`))
		case 3:
			_, _ = w.Write([]byte(`{
				"id":"resp_final",
				"model":"deepseek/test-model",
				"choices":[{
					"finish_reason":"tool_calls",
					"message":{"role":"assistant","tool_calls":[{"id":"call_final","type":"function","function":{"name":"final_response","arguments":{"summary":"Started dev-server under Little Control Room and confirmed it is still running on port 4173.","outcome":"partial","files_changed":[],"verification":["list_processes reported dev-server running on port 4173"]}}}]}
				}]
			}`))
		default:
			t.Fatalf("unexpected provider request %d", providerRequests)
		}
	}))
	defer server.Close()

	t.Setenv("OPENROUTER_API_KEY", "test-key")
	t.Setenv("OPENROUTER_BASE_URL", server.URL)

	var stream bytes.Buffer
	writer, sessionID, err := session.NewWriter(dataDir, time.Now(), &stream)
	if err != nil {
		t.Fatal(err)
	}
	defer writer.Close()
	workspace, err := policy.NewWorkspace(root, policy.AutonomyMedium)
	if err != nil {
		t.Fatal(err)
	}
	processes := &lcagentCLITestProcessBroker{}
	runner := script.Runner{
		Session:   writer,
		Command:   tools.CommandRunner{Workspace: workspace},
		Patch:     tools.PatchApplier{Workspace: workspace},
		Files:     tools.FileTools{Workspace: workspace, Limits: tools.FileLimitsForProfile(tools.FileProfileBalanced)},
		Processes: processes,
		Skills:    skillcatalog.Catalog{},
		SessionID: sessionID,
		Prompt:    "Start the dev server and leave it running under Little Control Room management.",
	}
	err = runChatLoop(context.Background(), writer, runner, nil, "", nil,
		modeladapter.OpenRouterConfig{Model: "deepseek/test-model", MaxTurns: 4, RequestTimeout: time.Minute},
		modeladapter.OpenRouterConfig{Model: "deepseek/test-model", MaxTurns: 1, RequestTimeout: time.Minute},
		modeladapter.OpenRouterConfig{MaxTurns: 1, RequestTimeout: time.Minute},
		modeladapter.OpenRouterConfig{MaxTurns: 1, RequestTimeout: time.Minute},
		"openrouter", "main", "off", "off", script.DefaultSearchRefineMinBytes, tools.FileProfileBalanced, tools.FileLimitsForProfile(tools.FileProfileBalanced), openRouterContextOptions{}, true, false, 0, 0, false)
	if err != nil {
		t.Fatalf("runChatLoop error: %v\nstream:\n%s", err, stream.String())
	}
	if len(processes.requests) != 2 {
		t.Fatalf("process requests = %#v, want start then list", processes.requests)
	}
	if processes.requests[0].Action != script.ProcessActionStart || processes.requests[0].Command != "npm run dev" || processes.requests[0].Name != "dev-server" {
		t.Fatalf("start request = %#v", processes.requests[0])
	}
	if processes.requests[1].Action != script.ProcessActionList {
		t.Fatalf("list request = %#v", processes.requests[1])
	}
	text := stream.String()
	for _, want := range []string{
		`"type":"tool_call"`,
		`"tool":"start_process"`,
		`"tool":"list_processes"`,
		`"type":"operational_action"`,
		`"process_id":"rt_dev"`,
		`"final_outcome":"partial"`,
	} {
		if !strings.Contains(text, want) {
			t.Fatalf("stream missing %q:\n%s", want, text)
		}
	}
	if strings.Contains(text, `"tool":"run_command"`) {
		t.Fatalf("managed process replay should not use run_command:\n%s", text)
	}
}

func TestRunChatLoopRequiresVerificationAfterManagedProcessCompletion(t *testing.T) {
	isolateSkillHomes(t)
	root := t.TempDir()
	dataDir := t.TempDir()
	var providerRequests int
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		providerRequests++
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
		switch providerRequests {
		case 1:
			_, _ = w.Write([]byte(`{
				"id":"resp_start",
				"model":"deepseek/test-model",
				"choices":[{
					"finish_reason":"tool_calls",
					"message":{"role":"assistant","tool_calls":[{"id":"call_start","type":"function","function":{"name":"start_process","arguments":{"command":"npm run dev","name":"dev-server"}}}]}
				}]
			}`))
		case 2:
			_, _ = w.Write([]byte(`{
				"id":"resp_bad_final",
				"model":"deepseek/test-model",
				"choices":[{
					"finish_reason":"tool_calls",
					"message":{"role":"assistant","tool_calls":[{"id":"call_final_bad","type":"function","function":{"name":"final_response","arguments":{"summary":"started and done","outcome":"completed","files_changed":[],"verification":["dev server is running"]}}}]}
				}]
			}`))
		case 3:
			feedbackSeen := false
			for _, msg := range body.Messages {
				if msg.Role == "user" && strings.Contains(msg.Content, "managed process action") && strings.Contains(msg.Content, "no later run_command check marked purpose=verify") {
					feedbackSeen = true
				}
			}
			if !feedbackSeen {
				t.Fatalf("third request missing managed-process verification feedback: %#v", body.Messages)
			}
			_, _ = w.Write([]byte(`{
				"id":"resp_verify",
				"model":"deepseek/test-model",
				"choices":[{
					"finish_reason":"tool_calls",
					"message":{"role":"assistant","tool_calls":[{"id":"call_verify","type":"function","function":{"name":"run_command","arguments":{"argv":["sh","-c","exit 0"],"shell":false,"purpose":"verify","timeout_ms":120000}}}]}
				}]
			}`))
		case 4:
			_, _ = w.Write([]byte(`{
				"id":"resp_final",
				"model":"deepseek/test-model",
				"choices":[{
					"finish_reason":"tool_calls",
					"message":{"role":"assistant","tool_calls":[{"id":"call_final","type":"function","function":{"name":"final_response","arguments":{"summary":"Started dev-server under Little Control Room and verified it with a separate probe.","outcome":"completed","files_changed":[],"verification":["sh -c exit 0"]}}}]}
				}]
			}`))
		default:
			t.Fatalf("unexpected provider request %d", providerRequests)
		}
	}))
	defer server.Close()

	t.Setenv("OPENROUTER_API_KEY", "test-key")
	t.Setenv("OPENROUTER_BASE_URL", server.URL)

	var stream bytes.Buffer
	writer, sessionID, err := session.NewWriter(dataDir, time.Now(), &stream)
	if err != nil {
		t.Fatal(err)
	}
	defer writer.Close()
	workspace, err := policy.NewWorkspace(root, policy.AutonomyMedium)
	if err != nil {
		t.Fatal(err)
	}
	processes := &lcagentCLITestProcessBroker{}
	runner := script.Runner{
		Session:   writer,
		Command:   tools.CommandRunner{Workspace: workspace},
		Patch:     tools.PatchApplier{Workspace: workspace},
		Files:     tools.FileTools{Workspace: workspace, Limits: tools.FileLimitsForProfile(tools.FileProfileBalanced)},
		Processes: processes,
		Skills:    skillcatalog.Catalog{},
		SessionID: sessionID,
		Prompt:    "Start the dev server, verify it, and report completion only after verification.",
	}
	err = runChatLoop(context.Background(), writer, runner, nil, "", nil,
		modeladapter.OpenRouterConfig{Model: "deepseek/test-model", MaxTurns: 5, RequestTimeout: time.Minute},
		modeladapter.OpenRouterConfig{Model: "deepseek/test-model", MaxTurns: 1, RequestTimeout: time.Minute},
		modeladapter.OpenRouterConfig{MaxTurns: 1, RequestTimeout: time.Minute},
		modeladapter.OpenRouterConfig{MaxTurns: 1, RequestTimeout: time.Minute},
		"openrouter", "main", "off", "off", script.DefaultSearchRefineMinBytes, tools.FileProfileBalanced, tools.FileLimitsForProfile(tools.FileProfileBalanced), openRouterContextOptions{}, true, false, 0, 0, false)
	if err != nil {
		t.Fatalf("runChatLoop error: %v\nstream:\n%s", err, stream.String())
	}
	text := stream.String()
	for _, want := range []string{
		`"tool":"start_process"`,
		`"type":"final_response_audit"`,
		`"outcome":"block"`,
		`managed process action \"start\" has no later run_command check marked purpose=verify`,
		`"type":"verification_feedback"`,
		`"tool":"run_command"`,
		`"type":"verification_check"`,
		`"status":"passed"`,
		`"outcome":"pass"`,
		`"final_outcome":"completed"`,
		`"summary":"Started dev-server under Little Control Room and verified it with a separate probe."`,
	} {
		if !strings.Contains(text, want) {
			t.Fatalf("stream missing %q:\n%s", want, text)
		}
	}
	if providerRequests != 4 {
		t.Fatalf("provider requests = %d, want 4", providerRequests)
	}
}

type lcagentCLITestProcessBroker struct {
	requests []script.ProcessRequest
}

func (b *lcagentCLITestProcessBroker) RequestProcess(_ context.Context, request script.ProcessRequest) (tools.ToolResult, error) {
	b.requests = append(b.requests, request)
	evidence := tools.ManagedProcessEvidence{
		Action:       string(request.Action),
		ProcessID:    "rt_dev",
		Name:         "dev-server",
		Command:      "npm run dev",
		PID:          4242,
		PGID:         4242,
		Running:      true,
		Ports:        []int{4173},
		URLs:         []string{"http://127.0.0.1:4173"},
		RecentOutput: []string{"listening on 4173"},
	}
	switch request.Action {
	case script.ProcessActionStart:
		return tools.ToolResult{
			Success:        true,
			Output:         "started dev-server",
			Command:        request.Command,
			CWD:            request.CWD,
			ManagedProcess: &evidence,
		}, nil
	case script.ProcessActionList:
		return tools.ToolResult{
			Success:          true,
			Output:           "dev-server running",
			ManagedProcesses: []tools.ManagedProcessEvidence{evidence},
		}, nil
	default:
		return tools.ToolResult{Success: false, Error: "unexpected process action"}, nil
	}
}

func lcagentCLITestRequestHasTool(toolsRaw []any, name string) bool {
	for _, raw := range toolsRaw {
		toolMap, _ := raw.(map[string]any)
		function, _ := toolMap["function"].(map[string]any)
		if function["name"] == name {
			return true
		}
	}
	return false
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
	dataDir := t.TempDir()
	code := Run([]string{
		"exec",
		"--cwd", root,
		"--data-dir", dataDir,
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

func TestRunExecOpenRouterRetriesEmptyProviderCompletion(t *testing.T) {
	isolateSkillHomes(t)
	root := t.TempDir()
	requests := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests++
		w.Header().Set("Content-Type", "application/json")
		if requests == 1 {
			_, _ = w.Write([]byte(`{
				"id":"resp_empty",
				"model":"deepseek/test-model",
				"choices":[{
					"finish_reason":"stop",
					"message":{"role":"assistant","content":""}
				}],
				"usage":{"prompt_tokens":11,"completion_tokens":2,"total_tokens":13}
			}`))
			return
		}
		_, _ = w.Write([]byte(`{
			"id":"resp_after_empty",
			"model":"deepseek/test-model",
			"choices":[{
				"finish_reason":"stop",
				"message":{"role":"assistant","content":"done after empty retry"}
			}]
		}`))
	}))
	defer server.Close()

	t.Setenv("OPENROUTER_API_KEY", "test-key")
	t.Setenv("OPENROUTER_BASE_URL", server.URL)

	var stdout, stderr bytes.Buffer
	dataDir := t.TempDir()
	code := Run([]string{
		"exec",
		"--cwd", root,
		"--data-dir", dataDir,
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
		`"response_id":"resp_empty"`,
		`"invalid":true`,
		`"prompt_tokens":11`,
		`"type":"provider_failure"`,
		`"kind":"malformed_response"`,
		`"retrying":true`,
		`"type":"provider_retry"`,
		`"attempt":2`,
		`"type":"provider_retry_succeeded"`,
		`"summary":"done after empty retry"`,
	} {
		if !strings.Contains(text, want) {
			t.Fatalf("stdout missing %q:\n%s", want, text)
		}
	}
	if strings.Contains(text, `"type":"turn_aborted"`) {
		t.Fatalf("stdout should not abort after empty completion retry:\n%s", text)
	}
}

func TestRunPresetsListsCodingRoutes(t *testing.T) {
	var stdout, stderr bytes.Buffer
	code := Run([]string{"presets", "--output", "json"}, &stdout, &stderr)
	if code != 0 {
		t.Fatalf("code = %d stderr=%s stdout=%s", code, stderr.String(), stdout.String())
	}
	var presets []struct {
		Name            string `json:"Name"`
		Provider        string `json:"Provider"`
		Model           string `json:"Model"`
		ReasoningEffort string `json:"ReasoningEffort"`
	}
	if err := json.Unmarshal(stdout.Bytes(), &presets); err != nil {
		t.Fatalf("decode presets json: %v\n%s", err, stdout.String())
	}
	got := map[string]string{}
	for _, preset := range presets {
		got[preset.Name] = preset.Provider + "/" + preset.Model
		if preset.Name == "balanced" && preset.ReasoningEffort != "high" {
			t.Fatalf("balanced reasoning effort = %q, want high: %#v", preset.ReasoningEffort, presets)
		}
	}
	for _, name := range []string{"balanced", "quality", "mimo-2.5-pro-low", "mimo-2.5-pro-high", "mimo-2.5-pro-max", "cheap-scout"} {
		if got[name] == "/" {
			t.Fatalf("preset %s missing provider/model: %#v", name, presets)
		}
		if _, ok := got[name]; !ok {
			t.Fatalf("missing preset %s in %#v", name, presets)
		}
	}
}

func TestLiveEvalRoutePresetAppliesBalancedReasoning(t *testing.T) {
	preset, ok := lcagentRoutePresetByName("balanced")
	if !ok {
		t.Fatal("balanced preset missing")
	}
	provider := "openrouter"
	model := ""
	reasoningEffort := ""
	autoRaw := "low"
	toolProfile := "balanced"
	contextProfile := "balanced"
	var requestTimeout time.Duration

	applyLiveEvalRoutePreset(preset, map[string]bool{}, &provider, &model, &reasoningEffort, &autoRaw, &toolProfile, &contextProfile, &requestTimeout)

	if provider != "deepseek" || model != "deepseek-v4-pro" || reasoningEffort != "high" || autoRaw != "low" || toolProfile != "balanced" || contextProfile != "balanced" || requestTimeout != 10*time.Minute {
		t.Fatalf("live eval preset result provider=%q model=%q reasoning=%q auto=%q tool=%q context=%q timeout=%s", provider, model, reasoningEffort, autoRaw, toolProfile, contextProfile, requestTimeout)
	}

	reasoningEffort = "low"
	applyLiveEvalRoutePreset(preset, map[string]bool{"reasoning-effort": true}, &provider, &model, &reasoningEffort, &autoRaw, &toolProfile, &contextProfile, &requestTimeout)
	if reasoningEffort != "low" {
		t.Fatalf("explicit reasoning effort was overwritten: %q", reasoningEffort)
	}
}

func TestRunExecRoutePresetAppliesMimoMaxDirectXiaomi(t *testing.T) {
	isolateSkillHomes(t)
	root := t.TempDir()
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/chat/completions" {
			t.Fatalf("request path = %s, want /chat/completions", r.URL.Path)
		}
		if got := r.Header.Get("api-key"); got != "test-xiaomi-key" {
			t.Fatalf("api-key = %q, want Xiaomi test key", got)
		}
		var body struct {
			Model       string  `json:"model"`
			Temperature float64 `json:"temperature"`
			Thinking    struct {
				Type            string `json:"type"`
				ReasoningEffort string `json:"reasoning_effort"`
			} `json:"thinking"`
		}
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			t.Fatalf("decode request body: %v", err)
		}
		if body.Model != "mimo-v2.5-pro" {
			t.Fatalf("model = %q, want Xiaomi MiMo", body.Model)
		}
		if body.Temperature != 0.2 {
			t.Fatalf("temperature = %f, want 0.2", body.Temperature)
		}
		if body.Thinking.Type != "enabled" || body.Thinking.ReasoningEffort != "xhigh" {
			t.Fatalf("thinking = %#v, want xhigh enabled", body.Thinking)
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{
			"id":"resp_mimo",
			"model":"mimo-v2.5-pro",
			"choices":[{
				"finish_reason":"stop",
				"message":{"role":"assistant","content":"done from mimo route"}
			}]
		}`))
	}))
	defer server.Close()

	t.Setenv("XIAOMI_API_KEY", "test-xiaomi-key")
	t.Setenv("XIAOMI_BASE_URL", server.URL)

	var stdout, stderr bytes.Buffer
	code := Run([]string{
		"exec",
		"--cwd", root,
		"--data-dir", t.TempDir(),
		"--route-preset", "mimo-2.5-pro-max",
		"--output", "stream-json",
		"--max-turns", "2",
		"answer directly",
	}, &stdout, &stderr)
	if code != 0 {
		t.Fatalf("code = %d stderr=%s stdout=%s", code, stderr.String(), stdout.String())
	}
	text := stdout.String()
	for _, want := range []string{
		`"provider":"xiaomi"`,
		`"model":"mimo-v2.5-pro"`,
		`"type":"route_preset"`,
		`"name":"mimo-2.5-pro-max"`,
		`"context_profile":"large"`,
		`"reasoning_effort":"xhigh"`,
		`"summary":"done from mimo route"`,
	} {
		if !strings.Contains(text, want) {
			t.Fatalf("stdout missing %q:\n%s", want, text)
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

func TestRunScoutUsesCheapScoutDefaultsAndPromptContract(t *testing.T) {
	isolateSkillHomes(t)
	root := t.TempDir()
	var capturedPrompt string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/chat/completions" {
			t.Fatalf("request path = %s, want /chat/completions", r.URL.Path)
		}
		var body map[string]any
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			t.Fatalf("decode request body: %v", err)
		}
		if body["model"] != "deepseek-v4-flash" {
			t.Fatalf("model = %q, want cheap scout flash model", body["model"])
		}
		messages, _ := body["messages"].([]any)
		for _, raw := range messages {
			msg, _ := raw.(map[string]any)
			if msg["role"] == "user" {
				content, _ := msg["content"].(string)
				if strings.Contains(content, "Scout task.") {
					capturedPrompt = content
				}
			}
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{
			"id":"resp_scout",
			"model":"deepseek/deepseek-v4-flash",
			"choices":[{
				"finish_reason":"stop",
				"message":{"role":"assistant","content":"Findings\n- mapped repo\nRelevant files\n- README.md\nSuggested next steps\n- implement\nRisks or unknowns\n- none"}
			}]
		}`))
	}))
	defer server.Close()

	t.Setenv("DEEPSEEK_API_KEY", "test-key")
	t.Setenv("DEEPSEEK_BASE_URL", server.URL)

	var stdout, stderr bytes.Buffer
	code := Run([]string{
		"scout",
		"--cwd", root,
		"--data-dir", t.TempDir(),
		"--output", "stream-json",
		"map the repo",
	}, &stdout, &stderr)
	if code != 0 {
		t.Fatalf("code = %d stderr=%s stdout=%s", code, stderr.String(), stdout.String())
	}
	for _, want := range []string{
		"Scout task.",
		"do not modify files",
		"Return a compact handoff",
		"User request:\nmap the repo",
	} {
		if !strings.Contains(capturedPrompt, want) {
			t.Fatalf("scout prompt missing %q:\n%s", want, capturedPrompt)
		}
	}
	text := stdout.String()
	for _, want := range []string{
		`"provider":"deepseek"`,
		`"model":"deepseek-v4-flash"`,
		`"type":"route_preset"`,
		`"name":"cheap-scout"`,
		`"type":"delegation_mode"`,
		`"mode":"cheap_scout"`,
		`"summary":"Findings\n- mapped repo`,
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

func TestRunExecRejectsLegacySessionContinuationWithoutThreadState(t *testing.T) {
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

	var stdout, stderr bytes.Buffer
	code := Run([]string{
		"exec",
		"--cwd", root,
		"--data-dir", dataDir,
		"--auto", "off",
		"--output", "stream-json",
		"--provider", "openrouter",
		"--model", "deepseek/test-model",
		"--continue-from", sessionID,
		"--max-turns", "2",
		"continue from previous work",
	}, &stdout, &stderr)
	if code == 0 {
		t.Fatalf("code = 0, want failure")
	}
	if stdout.Len() != 0 {
		t.Fatalf("stdout = %s, want empty", stdout.String())
	}
	if !strings.Contains(stderr.String(), "thread state not found") {
		t.Fatalf("stderr missing thread-state error: %s", stderr.String())
	}
}

func TestRunExecOpenRouterPrefersExactContinuationSnapshot(t *testing.T) {
	isolateSkillHomes(t)
	root := t.TempDir()
	dataDir := t.TempDir()
	threadID := "lct_exact_source"
	started := time.Date(2026, 5, 12, 9, 0, 0, 0, time.UTC)
	exactMessages := []modeladapter.Message{
		{Role: "system", Content: "previous system prompt"},
		{Role: "user", Content: "first request"},
		{Role: "assistant", ToolCalls: []modeladapter.ToolCall{{
			ID:   "call_read",
			Type: "function",
			Function: modeladapter.FunctionCall{
				Name:      "read_file",
				Arguments: json.RawMessage(`{"path":"README.md","limit":1}`),
			},
		}}},
		{Role: "tool", ToolCallID: "call_read", Content: `{"success":true,"output":"file: README.md\ntotal_lines: 1\nhas_more: false\nlines: 1-1\n\n1 | hello\n"}`},
		{Role: "assistant", Content: "first answer"},
	}
	store := newThreadStateStore(dataDir, threadID, root, "lca_exact_source_run", started)
	if err := store.SaveCheckpoint("final_response", exactMessages, false); err != nil {
		t.Fatalf("write thread state: %v", err)
	}

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var body struct {
			Messages []modeladapter.Message `json:"messages"`
		}
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			t.Fatalf("decode request body: %v", err)
		}
		if len(body.Messages) < len(exactMessages)+1 {
			t.Fatalf("request missing replay messages: %#v", body.Messages)
		}
		if body.Messages[0].Role != "system" || !strings.Contains(body.Messages[0].Content, "Capability status for this run") {
			t.Fatalf("first message = %#v, want refreshed current system prompt", body.Messages[0])
		}
		if !strings.Contains(body.Messages[0].Content, "Host environment for this run") || !strings.Contains(body.Messages[0].Content, "operating system:") {
			t.Fatalf("refreshed system prompt missing host environment:\n%s", body.Messages[0].Content)
		}
		if !strings.Contains(body.Messages[0].Content, "Previous LCAgent session context") || !strings.Contains(body.Messages[0].Content, "latest current user request below is authoritative") {
			t.Fatalf("refreshed system prompt missing resume boundary:\n%s", body.Messages[0].Content)
		}
		if body.Messages[1].Role != "user" || body.Messages[1].Content != "first request" {
			t.Fatalf("previous user message = %#v", body.Messages[1])
		}
		if len(body.Messages[2].ToolCalls) != 1 || body.Messages[2].ToolCalls[0].ID != "call_read" {
			t.Fatalf("previous assistant tool call = %#v", body.Messages[2])
		}
		if body.Messages[3].Role != "tool" || body.Messages[3].ToolCallID != "call_read" || !strings.Contains(body.Messages[3].Content, "hello") {
			t.Fatalf("previous tool result = %#v", body.Messages[3])
		}
		foundCurrentPrompt := false
		for _, msg := range body.Messages {
			if msg.Role == "user" && msg.Content == "continue exactly" {
				foundCurrentPrompt = true
			}
		}
		if !foundCurrentPrompt {
			t.Fatalf("request missing current prompt: %#v", body.Messages)
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{
			"id":"resp_exact",
			"model":"deepseek/test-model",
			"choices":[{
				"finish_reason":"stop",
				"message":{"role":"assistant","content":"continued from exact context"}
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
		"--continue-from", threadID,
		"--max-turns", "2",
		"continue exactly",
	}, &stdout, &stderr)
	if code != 0 {
		t.Fatalf("code = %d stderr=%s stdout=%s", code, stderr.String(), stdout.String())
	}
	text := stdout.String()
	for _, want := range []string{
		`"context_mode":"exact"`,
		`"exact_message_count":5`,
		`"summary":"continued from exact context"`,
	} {
		if !strings.Contains(text, want) {
			t.Fatalf("stdout missing %q:\n%s", want, text)
		}
	}
}

func TestRunExecOpenRouterResumesCanonicalThreadState(t *testing.T) {
	isolateSkillHomes(t)
	root := t.TempDir()
	dataDir := t.TempDir()
	requests := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests++
		var body struct {
			Messages []modeladapter.Message `json:"messages"`
		}
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			t.Fatalf("decode request body: %v", err)
		}
		w.Header().Set("Content-Type", "application/json")
		switch requests {
		case 1:
			_, _ = w.Write([]byte(`{
				"id":"resp_thread_first",
				"model":"deepseek/test-model",
				"choices":[{
					"finish_reason":"stop",
					"message":{"role":"assistant","content":"first answer"}
				}]
			}`))
		case 2:
			for _, want := range []modeladapter.Message{
				{Role: "user", Content: "first prompt"},
				{Role: "assistant", Content: "first answer"},
				{Role: "user", Content: "second prompt"},
			} {
				found := false
				for _, msg := range body.Messages {
					if msg.Role == want.Role && msg.Content == want.Content {
						found = true
						break
					}
				}
				if !found {
					t.Fatalf("resumed request missing %#v in %#v", want, body.Messages)
				}
			}
			if body.Messages[0].Role != "system" || !strings.Contains(body.Messages[0].Content, "Previous LCAgent session context") {
				t.Fatalf("thread resume should refresh the system prompt with a resume boundary: %#v", body.Messages)
			}
			_, _ = w.Write([]byte(`{
				"id":"resp_thread_second",
				"model":"deepseek/test-model",
				"choices":[{
					"finish_reason":"stop",
					"message":{"role":"assistant","content":"second answer"}
				}]
			}`))
		default:
			t.Fatalf("unexpected request %d", requests)
		}
	}))
	defer server.Close()

	t.Setenv("OPENROUTER_API_KEY", "test-key")
	t.Setenv("OPENROUTER_BASE_URL", server.URL)

	var firstStdout, firstStderr bytes.Buffer
	code := Run([]string{
		"exec",
		"--cwd", root,
		"--data-dir", dataDir,
		"--auto", "off",
		"--output", "stream-json",
		"--provider", "openrouter",
		"--model", "deepseek/test-model",
		"--max-turns", "2",
		"first prompt",
	}, &firstStdout, &firstStderr)
	if code != 0 {
		t.Fatalf("first code = %d stderr=%s stdout=%s", code, firstStderr.String(), firstStdout.String())
	}
	threadID := lcagentCLITestThreadIDFromStream(t, firstStdout.String())
	state, ok, err := loadThreadState(dataDir, threadID, root)
	if err != nil || !ok {
		t.Fatalf("load thread state ok=%v err=%v", ok, err)
	}
	if state.Status != threadStateStatusStable || state.ContextMode != threadContextModeExact {
		t.Fatalf("state status/mode = %q/%q", state.Status, state.ContextMode)
	}
	if state.ThreadID != threadID || !sameCleanPath(state.ProjectPath, root) || len(state.Messages) < 3 {
		t.Fatalf("state = %#v", state)
	}

	var secondStdout, secondStderr bytes.Buffer
	code = Run([]string{
		"exec",
		"--cwd", root,
		"--data-dir", dataDir,
		"--auto", "off",
		"--output", "stream-json",
		"--provider", "openrouter",
		"--model", "deepseek/test-model",
		"--continue-from", threadID,
		"--max-turns", "2",
		"second prompt",
	}, &secondStdout, &secondStderr)
	if code != 0 {
		t.Fatalf("second code = %d stderr=%s stdout=%s", code, secondStderr.String(), secondStdout.String())
	}
	for _, want := range []string{
		`"thread_id":"` + threadID + `"`,
		`"source_thread_id":"` + threadID + `"`,
		`"context_mode":"exact"`,
		`"summary":"second answer"`,
	} {
		if !strings.Contains(secondStdout.String(), want) {
			t.Fatalf("second stdout missing %q:\n%s", want, secondStdout.String())
		}
	}
}

func TestRunExecRejectsUnstableThreadState(t *testing.T) {
	isolateSkillHomes(t)
	root := t.TempDir()
	dataDir := t.TempDir()
	threadID := "lct_unstable"
	store := newThreadStateStore(dataDir, threadID, root, "lca_interrupted", time.Now())
	if err := store.MarkInFlight("model_request", []modeladapter.Message{
		{Role: "system", Content: "system"},
		{Role: "user", Content: "pending prompt"},
	}, false); err != nil {
		t.Fatalf("write unstable state: %v", err)
	}

	var stdout, stderr bytes.Buffer
	code := Run([]string{
		"exec",
		"--cwd", root,
		"--data-dir", dataDir,
		"--auto", "off",
		"--output", "stream-json",
		"--provider", "openrouter",
		"--continue-from", threadID,
		"continue",
	}, &stdout, &stderr)
	if code == 0 {
		t.Fatalf("code = 0, want failure")
	}
	if stdout.Len() != 0 {
		t.Fatalf("stdout = %s, want empty", stdout.String())
	}
	for _, want := range []string{"not resumable", threadStateStatusInFlight} {
		if !strings.Contains(stderr.String(), want) {
			t.Fatalf("stderr missing %q: %s", want, stderr.String())
		}
	}
}

func TestRunExecRejectsConflictingContinuationFlags(t *testing.T) {
	isolateSkillHomes(t)
	var stdout, stderr bytes.Buffer
	code := Run([]string{
		"exec",
		"--cwd", t.TempDir(),
		"--data-dir", t.TempDir(),
		"--resume", "lca_one",
		"--continue-from", "lca_two",
		"continue",
	}, &stdout, &stderr)
	if code == 0 {
		t.Fatalf("code = 0, want failure")
	}
	if !strings.Contains(stderr.String(), "--resume and --continue-from refer to different threads") {
		t.Fatalf("stderr missing conflict error: %s", stderr.String())
	}
	if stdout.Len() != 0 {
		t.Fatalf("stdout = %s, want empty", stdout.String())
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
		"--model", "deepseek/deepseek-v4-pro",
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

func TestSearchRefineProfileNormalizesDirectDeepSeekUtilityModel(t *testing.T) {
	t.Setenv("DEEPSEEK_API_KEY", "test-deepseek-key")

	profile := newSearchRefineProfile("deepseek", modeladapter.OpenRouterConfig{
		Model: "deepseek/deepseek-v4-flash",
	}, 1, "openrouter", "deepseek/deepseek-v4-pro")
	if !profile.Enabled {
		t.Fatalf("search refine profile disabled: %v", profile.DisabledErr)
	}
	if profile.Model != "deepseek-v4-flash" {
		t.Fatalf("utility model = %q, want deepseek-v4-flash", profile.Model)
	}
}

func TestSearchRefineProfileUsesXiaomiUtilityDefaultForSameAsMain(t *testing.T) {
	t.Setenv("XIAOMI_API_KEY", "test-xiaomi-key")

	profile := newSearchRefineProfile("main", modeladapter.OpenRouterConfig{}, 1, "xiaomi", "mimo-v2.5-pro")
	if !profile.Enabled {
		t.Fatalf("search refine profile disabled: %v", profile.DisabledErr)
	}
	if profile.Model != modeladapter.DefaultXiaomiUtilityModel {
		t.Fatalf("utility model = %q, want %q", profile.Model, modeladapter.DefaultXiaomiUtilityModel)
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
		if body["model"] != "kimi-k2.7-code" {
			t.Fatalf("model = %q, want kimi-k2.7-code", body["model"])
		}
		for _, key := range []string{"temperature", "max_completion_tokens", "max_tokens", "thinking"} {
			if _, ok := body[key]; ok {
				t.Fatalf("moonshot request should not send %s by default: %#v", key, body[key])
			}
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{
			"id":"resp_moonshot",
			"model":"kimi-k2.7-code",
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
		`"model":"kimi-k2.7-code"`,
		`"response_id":"resp_moonshot"`,
		`"cached_input_tokens":2`,
		`"summary":"done from direct moonshot"`,
	} {
		if !strings.Contains(text, want) {
			t.Fatalf("stdout missing %q:\n%s", want, text)
		}
	}
}

func TestRunExecMoonshotSkipsUnsupportedReasoningEffort(t *testing.T) {
	isolateSkillHomes(t)
	root := t.TempDir()
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/chat/completions" {
			t.Fatalf("request path = %s, want /chat/completions", r.URL.Path)
		}
		if got := r.Header.Get("Authorization"); got != "Bearer test-moonshot-key" {
			t.Fatalf("authorization = %q, want bearer test key", got)
		}
		var body map[string]any
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			t.Fatalf("decode request body: %v", err)
		}
		if body["model"] != "kimi-k2.7-code" {
			t.Fatalf("model = %q, want kimi-k2.7-code", body["model"])
		}
		if _, ok := body["reasoning"]; ok {
			t.Fatalf("moonshot request should not include reasoning: %#v", body["reasoning"])
		}
		if _, ok := body["thinking"]; ok {
			t.Fatalf("moonshot request should not include thinking: %#v", body["thinking"])
		}
		if _, ok := body["temperature"]; ok {
			t.Fatalf("moonshot request should not include temperature: %#v", body["temperature"])
		}
		if _, ok := body["max_tokens"]; ok {
			t.Fatalf("moonshot request should not include max_tokens: %#v", body["max_tokens"])
		}
		if _, ok := body["max_completion_tokens"]; ok {
			t.Fatalf("moonshot request should not include max_completion_tokens: %#v", body["max_completion_tokens"])
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{
			"id":"resp_moonshot_skip_reasoning",
			"model":"kimi-k2.7-code",
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
		"--reasoning-effort", "low",
		"--max-turns", "2",
		"answer directly",
	}, &stdout, &stderr)
	if code != 0 {
		t.Fatalf("code = %d stderr=%s stdout=%s", code, stderr.String(), stdout.String())
	}
	text := stdout.String()
	if !strings.Contains(text, `"response_id":"resp_moonshot_skip_reasoning"`) {
		t.Fatalf("stdout missing response id:\n%s", text)
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
	dataDir := t.TempDir()
	code := Run([]string{
		"exec",
		"--cwd", root,
		"--data-dir", dataDir,
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
	sessionID := lcagentCLITestSessionIDFromStream(t, text)
	path, err := resolveResumeContextPath(dataDir, sessionID)
	if err != nil {
		t.Fatalf("resolve session artifact: %v", err)
	}
	artifact, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read session artifact: %v", err)
	}
	if !strings.Contains(string(artifact), `"source":"tool_result"`) {
		t.Fatalf("artifact missing tool-result context snapshot:\n%s", string(artifact))
	}
}

func TestRunExecOpenRouterCompactsLargeToolHistoryBeforeNextRequest(t *testing.T) {
	isolateSkillHomes(t)
	root := t.TempDir()
	var big strings.Builder
	for i := 1; i <= 1000; i++ {
		fmt.Fprintf(&big, "line %04d with enough repeated context to force compaction before the next provider request %s\n", i, strings.Repeat("abcdefghijklmnopqrstuvwxyz ", 8))
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

func TestRunExecOpenRouterCompactionKeepsCurrentPromptAfterResume(t *testing.T) {
	isolateSkillHomes(t)
	root := t.TempDir()
	dataDir := t.TempDir()
	var big strings.Builder
	for i := 1; i <= 1000; i++ {
		fmt.Fprintf(&big, "line %04d with enough repeated context to force compaction after resume %s\n", i, strings.Repeat("abcdefghijklmnopqrstuvwxyz ", 8))
	}
	if err := os.WriteFile(filepath.Join(root, "BIG.md"), []byte(big.String()), 0o644); err != nil {
		t.Fatal(err)
	}

	threadID := "lct_compact_resume"
	store := newThreadStateStore(dataDir, threadID, root, "lca_previous", time.Date(2026, 5, 31, 1, 0, 0, 0, time.UTC))
	if err := store.SaveCheckpoint("final_response", []modeladapter.Message{
		{Role: "system", Content: "previous system prompt"},
		{Role: "user", Content: "please check the handoff.md"},
		{Role: "assistant", Content: "handoff summary"},
	}, false); err != nil {
		t.Fatalf("write thread state: %v", err)
	}

	currentPrompt := "build and run original FF, then FF with the new sprites"
	requests := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests++
		var body struct {
			Messages []modeladapter.Message `json:"messages"`
			Tools    []any                  `json:"tools"`
		}
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			t.Fatalf("decode request body: %v", err)
		}
		w.Header().Set("Content-Type", "application/json")
		switch requests {
		case 1:
			foundCurrent := false
			for _, msg := range body.Messages {
				if msg.Role == "user" && msg.Content == currentPrompt {
					foundCurrent = true
				}
			}
			if !foundCurrent {
				t.Fatalf("first resumed request missing current prompt: %#v", body.Messages)
			}
			_, _ = w.Write([]byte(`{
				"id":"resp_tool",
				"model":"deepseek/test-model",
				"choices":[{
					"finish_reason":"tool_calls",
					"message":{"role":"assistant","tool_calls":[{"id":"call_read","type":"function","function":{"name":"read_file","arguments":"{\"path\":\"BIG.md\",\"limit\":1000}"}}]}
				}]
			}`))
		case 2:
			if len(body.Tools) == 0 {
				t.Fatalf("compacted continuation request should still include tools: %#v", body)
			}
			currentStandalone := false
			staleStandalone := false
			compactedContextSeen := false
			for _, msg := range body.Messages {
				if msg.Role == "tool" {
					t.Fatalf("compacted resumed request should not contain raw tool messages: %#v", body.Messages)
				}
				if msg.Role == "user" && msg.Content == currentPrompt {
					currentStandalone = true
				}
				if msg.Role == "user" && msg.Content == "please check the handoff.md" {
					staleStandalone = true
				}
				if strings.Contains(msg.Content, loopCompactedContextPrefix) && strings.Contains(msg.Content, "latest real user request above") {
					compactedContextSeen = true
				}
			}
			if !currentStandalone || staleStandalone || !compactedContextSeen {
				t.Fatalf("compacted resumed request current=%v stale=%v compacted=%v messages=%#v", currentStandalone, staleStandalone, compactedContextSeen, body.Messages)
			}
			_, _ = w.Write([]byte(`{
				"id":"resp_final",
				"model":"deepseek/test-model",
				"choices":[{
					"finish_reason":"stop",
					"message":{"role":"assistant","content":"continued current prompt after compaction"}
				}]
			}`))
		default:
			t.Fatalf("unexpected request %d", requests)
		}
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
		"--continue-from", threadID,
		"--max-turns", "3",
		currentPrompt,
	}, &stdout, &stderr)
	if code != 0 {
		t.Fatalf("code = %d stderr=%s stdout=%s", code, stderr.String(), stdout.String())
	}
	if requests != 2 {
		t.Fatalf("requests = %d, want 2", requests)
	}
	if !strings.Contains(stdout.String(), `"type":"context_compacted"`) || !strings.Contains(stdout.String(), `"summary":"continued current prompt after compaction"`) {
		t.Fatalf("stdout missing compaction/final evidence:\n%s", stdout.String())
	}
}

func TestRunExecPreflightCompactsOversizedResumeForSelectedModel(t *testing.T) {
	isolateSkillHomes(t)
	root := t.TempDir()
	dataDir := t.TempDir()
	threadID := "lct_model_switch_compact"
	store := newThreadStateStore(dataDir, threadID, root, "lca_previous", time.Date(2026, 5, 31, 1, 0, 0, 0, time.UTC))
	largePriorAnswer := "old answer " + strings.Repeat("model switch context ", 30000)
	if err := store.SaveCheckpoint("assistant_message", []modeladapter.Message{
		{Role: "system", Content: "previous system prompt"},
		{Role: "user", Content: "old request that should become packed background"},
		{Role: "assistant", Content: largePriorAnswer},
	}, false); err != nil {
		t.Fatalf("write thread state: %v", err)
	}

	currentPrompt := "continue with the smaller model"
	requests := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests++
		var body struct {
			Messages []modeladapter.Message `json:"messages"`
			Tools    []any                  `json:"tools"`
		}
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			t.Fatalf("decode request body: %v", err)
		}
		if requests != 1 {
			t.Fatalf("unexpected request %d", requests)
		}
		currentStandalone := false
		staleStandalone := false
		compactedContextSeen := false
		for _, msg := range body.Messages {
			if msg.Role == "user" && msg.Content == currentPrompt {
				currentStandalone = true
			}
			if msg.Role == "user" && msg.Content == "old request that should become packed background" {
				staleStandalone = true
			}
			if strings.Contains(msg.Content, loopCompactedContextPrefix) && strings.Contains(msg.Content, "old request that should become packed background") {
				compactedContextSeen = true
			}
			if strings.Contains(msg.Content, strings.Repeat("model switch context ", 2000)) {
				t.Fatalf("preflight compacted request kept raw oversized prior answer")
			}
		}
		if !currentStandalone || staleStandalone || !compactedContextSeen {
			t.Fatalf("preflight request current=%v stale=%v compacted=%v messages=%#v", currentStandalone, staleStandalone, compactedContextSeen, body.Messages)
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{
			"id":"resp_final",
			"model":"provider/small-128k-model",
			"choices":[{
				"finish_reason":"stop",
				"message":{"role":"assistant","content":"continued on smaller context budget"}
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
		"--model", "provider/small-128k-model",
		"--continue-from", threadID,
		"--max-turns", "2",
		currentPrompt,
	}, &stdout, &stderr)
	if code != 0 {
		t.Fatalf("code = %d stderr=%s stdout=%s", code, stderr.String(), stdout.String())
	}
	if requests != 1 {
		t.Fatalf("requests = %d, want 1", requests)
	}
	text := stdout.String()
	for _, want := range []string{`"type":"context_compacted"`, `"reason":"continuation_preflight"`, `"threshold_tokens":102400`, `"summary":"continued on smaller context budget"`} {
		if !strings.Contains(text, want) {
			t.Fatalf("stdout missing %q:\n%s", want, text)
		}
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
			if msg.Role == "user" && strings.Contains(msg.Content, "Verification feedback: go test ./...") && strings.Contains(msg.Content, " failed.") && strings.Contains(msg.Content, "rerun a purpose=verify check") {
				feedbackSeen = true
			}
		}
		if !feedbackSeen {
			t.Fatalf("second request missing verification feedback: %#v", body.Messages)
		}
		if requests == 2 {
			_, _ = w.Write([]byte(`{
				"id":"resp_bad_final",
				"model":"deepseek/test-model",
				"choices":[{
					"finish_reason":"tool_calls",
					"message":{"role":"assistant","tool_calls":[{"id":"call_final_bad","type":"function","function":{"name":"final_response","arguments":{"summary":"verification failed but marking complete","outcome":"completed","files_changed":[],"verification":["go test ./... failed"]}}}]}
				}]
			}`))
			return
		}
		finalOutcomeFeedbackSeen := false
		for _, msg := range body.Messages {
			if msg.Role == "user" && strings.Contains(msg.Content, "outcome was completed") && strings.Contains(msg.Content, "verification evidence did not pass") {
				finalOutcomeFeedbackSeen = true
			}
		}
		if !finalOutcomeFeedbackSeen {
			t.Fatalf("third request missing final outcome feedback: %#v", body.Messages)
		}
		_, _ = w.Write([]byte(`{
			"id":"resp_final",
			"model":"deepseek/test-model",
			"choices":[{
				"finish_reason":"tool_calls",
				"message":{"role":"assistant","tool_calls":[{"id":"call_final","type":"function","function":{"name":"final_response","arguments":{"summary":"verification failed and needs a fix","outcome":"failed","files_changed":[],"verification":["go test ./... failed"]}}}]}
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
		`"type":"final_response_audit"`,
		`"outcome":"block"`,
		`"final_outcome":"completed"`,
		`"verification_status":"failed"`,
		`"summary":"verification failed and needs a fix"`,
	} {
		if !strings.Contains(text, want) {
			t.Fatalf("stdout missing %q:\n%s", want, text)
		}
	}
	if requests != 3 {
		t.Fatalf("requests = %d, want 3", requests)
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
					"message":{"role":"assistant","tool_calls":[{"id":"call_final_1","type":"function","function":{"name":"final_response","arguments":{"summary":"changed README","outcome":"completed","files_changed":["README.md"],"verification":["go test ./..."]}}}]}
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
				"message":{"role":"assistant","tool_calls":[{"id":"call_final_2","type":"function","function":{"name":"final_response","arguments":{"summary":"verification blocked after one reminder","outcome":"blocked","files_changed":["README.md"],"verification":["not run: no applicable check"]}}}]}
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
		`"type":"final_response_audit"`,
		`"outcome":"block"`,
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

func TestShouldBounceFinalAuditAlwaysBouncesBrowserWaitRequirement(t *testing.T) {
	audit := script.FinalResponseAudit{Blocking: true, Code: "browser_wait_required"}
	if !shouldBounceFinalAudit(audit, 0) {
		t.Fatal("browser wait audit should bounce before any feedback")
	}
	if !shouldBounceFinalAudit(audit, 3) {
		t.Fatal("browser wait audit should keep bouncing even after prior final feedback")
	}
	if shouldBounceFinalAudit(script.FinalResponseAudit{Blocking: false, Code: "browser_wait_required"}, 0) {
		t.Fatal("non-blocking audit should not bounce")
	}
	if !shouldBounceFinalAudit(script.FinalResponseAudit{Blocking: true}, 0) {
		t.Fatal("ordinary blocking audit should bounce once")
	}
	if shouldBounceFinalAudit(script.FinalResponseAudit{Blocking: true}, 1) {
		t.Fatal("ordinary blocking audit should keep the existing one-feedback limit")
	}
	if shouldBounceFinalAudit(script.FinalResponseAudit{Blocking: false, Code: "quality_plan_partial_unfinished"}, 3) {
		t.Fatal("non-blocking quality plan partial handoff should not bounce")
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
			guidanceMessages := 0
			for _, msg := range body.Messages {
				if msg.Role == "user" && strings.Contains(msg.Content, "Patch feedback: README.md failed during apply") {
					feedbackMessages++
				}
				if msg.Role == "user" && strings.Contains(msg.Content, "Patch retry guidance:") {
					guidanceMessages++
				}
			}
			if feedbackMessages != 1 {
				t.Fatalf("third request patch feedback user messages = %d, want one: %#v", feedbackMessages, body.Messages)
			}
			if guidanceMessages != 1 {
				t.Fatalf("third request patch retry guidance messages = %d, want one: %#v", guidanceMessages, body.Messages)
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
		`"type":"repair_guidance"`,
		`Patch retry guidance`,
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

func TestRunExecOpenRouterFinalHandoffPreservesFilesTouched(t *testing.T) {
	isolateSkillHomes(t)
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "README.md"), []byte("old\n"), 0o644); err != nil {
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
		switch requests {
		case 1:
			if _, ok := body["tools"]; !ok {
				t.Fatalf("tool loop request missing tools: %#v", body)
			}
			_, _ = w.Write([]byte(`{
				"id":"resp_replace",
				"model":"deepseek/test-model",
				"choices":[{
					"finish_reason":"tool_calls",
					"message":{"role":"assistant","tool_calls":[{"id":"call_replace","type":"function","function":{"name":"replace_text","arguments":"{\"path\":\"README.md\",\"old_text\":\"old\\n\",\"new_text\":\"new\\n\",\"expected_replacements\":1}"}}]}
				}]
			}`))
		default:
			if _, ok := body["tools"]; ok {
				t.Fatalf("final handoff request should not include tools: %#v", body)
			}
			messages, _ := body["messages"].([]any)
			if len(messages) == 0 || !strings.Contains(fmt.Sprint(messages[len(messages)-1]), "Files touched by edit tools: README.md") {
				t.Fatalf("final handoff request missing files touched state: %#v", body)
			}
			_, _ = w.Write([]byte(`{
				"id":"resp_handoff",
				"model":"deepseek/test-model",
				"choices":[{
					"finish_reason":"stop",
					"message":{"role":"assistant","content":"Turn budget reached. README.md was changed from old to new. Verification not run. Continue by running checks."}
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
		"--max-turns", "1",
		"patch README",
	}, &stdout, &stderr)
	if code != 0 {
		t.Fatalf("code = %d stderr=%s stdout=%s", code, stderr.String(), stdout.String())
	}
	text := stdout.String()
	for _, want := range []string{
		`"type":"final_handoff_compacted"`,
		`"files_changed":["README.md"]`,
		`"verification_status":"missing_after_changes"`,
	} {
		if !strings.Contains(text, want) {
			t.Fatalf("stdout missing %q:\n%s", want, text)
		}
	}
	data, err := os.ReadFile(filepath.Join(root, "README.md"))
	if err != nil {
		t.Fatal(err)
	}
	if string(data) != "new\n" {
		t.Fatalf("README.md = %q, want new", string(data))
	}
	if requests != 2 {
		t.Fatalf("requests = %d, want 2", requests)
	}
}

func TestRunExecOpenRouterFinalHandoffToolCallFallsBackToPartial(t *testing.T) {
	isolateSkillHomes(t)
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "README.md"), []byte("alpha\n"), 0o644); err != nil {
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
		switch requests {
		case 1:
			_, _ = w.Write([]byte(`{
				"id":"resp_read",
				"model":"deepseek/test-model",
				"choices":[{
					"finish_reason":"tool_calls",
					"message":{"role":"assistant","tool_calls":[{"id":"call_read","type":"function","function":{"name":"read_file","arguments":"{\"path\":\"README.md\",\"limit\":1}"}}]}
				}]
			}`))
		case 2:
			if _, ok := body["tools"]; ok {
				t.Fatalf("final handoff request should not include tools: %#v", body)
			}
			_, _ = w.Write([]byte(`{
				"id":"resp_bad_handoff",
				"model":"deepseek/test-model",
				"choices":[{
					"finish_reason":"tool_calls",
					"message":{"role":"assistant","tool_calls":[{"id":"call_read_again","type":"function","function":{"name":"read_file","arguments":"{\"path\":\"README.md\",\"limit\":1}"}}]}
				}]
			}`))
		default:
			t.Fatalf("unexpected request %d: %#v", requests, body)
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
		"--auto", "off",
		"--output", "stream-json",
		"--provider", "openrouter",
		"--model", "deepseek/test-model",
		"--max-turns", "1",
		"keep reading",
	}, &stdout, &stderr)
	if code != 0 {
		t.Fatalf("code = %d stderr=%s stdout=%s", code, stderr.String(), stdout.String())
	}
	text := stdout.String()
	for _, want := range []string{
		`"type":"final_handoff_fallback"`,
		`final handoff tried to call tools`,
		`"final_outcome":"partial"`,
		`"type":"turn_complete"`,
	} {
		if !strings.Contains(text, want) {
			t.Fatalf("stdout missing %q:\n%s", want, text)
		}
	}
	if strings.Contains(text, `"type":"turn_aborted"`) {
		t.Fatalf("final handoff fallback should not abort:\n%s", text)
	}
	if requests != 2 {
		t.Fatalf("requests = %d, want 2", requests)
	}
}

func TestRunExecOpenRouterContinuesFromMaxTurnHandoff(t *testing.T) {
	isolateSkillHomes(t)
	root := t.TempDir()
	dataDir := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "README.md"), []byte("old\n"), 0o644); err != nil {
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
		switch requests {
		case 1:
			if len(body.Tools) == 0 {
				t.Fatalf("first tool-loop request missing tools: %#v", body)
			}
			_, _ = w.Write([]byte(`{
				"id":"resp_replace",
				"model":"deepseek/test-model",
				"choices":[{
					"finish_reason":"tool_calls",
					"message":{"role":"assistant","tool_calls":[{"id":"call_replace","type":"function","function":{"name":"replace_text","arguments":"{\"path\":\"README.md\",\"old_text\":\"old\\n\",\"new_text\":\"new\\n\",\"expected_replacements\":1}"}}]}
				}]
			}`))
		case 2:
			if len(body.Tools) != 0 {
				t.Fatalf("max-turn handoff request should not include tools: %#v", body)
			}
			if len(body.Messages) == 0 || !strings.Contains(body.Messages[len(body.Messages)-1].Content, "Files touched by edit tools: README.md") {
				t.Fatalf("max-turn handoff prompt missing touched-file state: %#v", body)
			}
			_, _ = w.Write([]byte(`{
				"id":"resp_handoff",
				"model":"deepseek/test-model",
				"choices":[{
					"finish_reason":"stop",
					"message":{"role":"assistant","content":"Turn budget reached. README.md was changed. Verification was not run. Continue by verifying README.md."}
				}]
			}`))
		case 3:
			if len(body.Messages) == 0 {
				t.Fatalf("continuation request missing messages: %#v", body)
			}
			if body.Messages[0].Role != "system" || !strings.Contains(body.Messages[0].Content, "Previous LCAgent session context") {
				t.Fatalf("continuation request missing refreshed resume boundary:\n%#v", body.Messages)
			}
			joined := fmt.Sprint(body.Messages)
			for _, want := range []string{
				"Current user request",
				"patch README then continue later",
				"Turn budget reached. README.md was changed",
				"continue from the handoff",
			} {
				if !strings.Contains(joined, want) {
					t.Fatalf("continuation exact replay missing %q:\n%#v", want, body.Messages)
				}
			}
			_, _ = w.Write([]byte(`{
				"id":"resp_continue",
				"model":"deepseek/test-model",
				"choices":[{
					"finish_reason":"stop",
					"message":{"role":"assistant","content":"continued after handoff"}
				}]
			}`))
		default:
			t.Fatalf("unexpected provider request %d: %#v", requests, body)
		}
	}))
	defer server.Close()

	t.Setenv("OPENROUTER_API_KEY", "test-key")
	t.Setenv("OPENROUTER_BASE_URL", server.URL)

	var firstStdout, firstStderr bytes.Buffer
	code := Run([]string{
		"exec",
		"--cwd", root,
		"--data-dir", dataDir,
		"--auto", "low",
		"--output", "stream-json",
		"--provider", "openrouter",
		"--model", "deepseek/test-model",
		"--max-turns", "1",
		"patch README then continue later",
	}, &firstStdout, &firstStderr)
	if code != 0 {
		t.Fatalf("first code = %d stderr=%s stdout=%s", code, firstStderr.String(), firstStdout.String())
	}
	sourceThreadID := lcagentCLITestThreadIDFromStream(t, firstStdout.String())
	firstText := firstStdout.String()
	for _, want := range []string{
		`"type":"final_handoff_compacted"`,
		`"files_changed":["README.md"]`,
		`"verification_status":"missing_after_changes"`,
	} {
		if !strings.Contains(firstText, want) {
			t.Fatalf("first stdout missing %q:\n%s", want, firstText)
		}
	}

	var secondStdout, secondStderr bytes.Buffer
	code = Run([]string{
		"exec",
		"--cwd", root,
		"--data-dir", dataDir,
		"--auto", "off",
		"--output", "stream-json",
		"--provider", "openrouter",
		"--model", "deepseek/test-model",
		"--continue-from", sourceThreadID,
		"--max-turns", "2",
		"continue from the handoff",
	}, &secondStdout, &secondStderr)
	if code != 0 {
		t.Fatalf("second code = %d stderr=%s stdout=%s", code, secondStderr.String(), secondStdout.String())
	}
	secondText := secondStdout.String()
	for _, want := range []string{
		`"type":"continuation"`,
		`"thread_id":"` + sourceThreadID + `"`,
		`"parent_session_id":"` + sourceThreadID + `"`,
		`"handoff_source":"final_handoff"`,
		`"context_mode":"compacted"`,
		`"summary":"continued after handoff"`,
	} {
		if !strings.Contains(secondText, want) {
			t.Fatalf("second stdout missing %q:\n%s", want, secondText)
		}
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
		if requests < openRouterMinimumTurnBeforeStallCheck {
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
			"Current user request",
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
	if requests != openRouterMinimumTurnBeforeStallCheck {
		t.Fatalf("requests = %d, want %d", requests, openRouterMinimumTurnBeforeStallCheck)
	}
}

func TestRunExecOpenRouterSynthesisWithRequiredFinalResponseExposesOnlyFinalTool(t *testing.T) {
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
		if requests < openRouterMinimumTurnBeforeStallCheck {
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
		toolsValue, ok := body["tools"].([]any)
		if !ok || len(toolsValue) != 1 {
			t.Fatalf("synthesis request tools = %#v, want only final_response", body["tools"])
		}
		tool, _ := toolsValue[0].(map[string]any)
		function, _ := tool["function"].(map[string]any)
		if function["name"] != "final_response" {
			t.Fatalf("synthesis tool = %#v, want final_response", toolsValue[0])
		}
		messages, _ := body["messages"].([]any)
		if len(messages) == 0 {
			t.Fatalf("synthesis request missing messages: %#v", body)
		}
		last := fmt.Sprint(messages[len(messages)-1])
		for _, want := range []string{
			"Only final_response is available",
			"Set final_response.outcome honestly",
			"keep reading until synthesis",
		} {
			if !strings.Contains(last, want) {
				t.Fatalf("synthesis request missing %q in last message: %s", want, last)
			}
		}
		_, _ = w.Write([]byte(`{
			"id":"resp_synthesis_final",
			"model":"deepseek/final-model",
			"choices":[{
				"finish_reason":"tool_calls",
				"message":{"role":"assistant","tool_calls":[{"id":"call_final","type":"function","function":{"name":"final_response","arguments":{"summary":"structured synthesis","outcome":"completed","files_changed":[],"verification":[]}}}]}
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
		"--require-final-response-tool",
		"keep reading until synthesis",
	}, &stdout, &stderr)
	if code != 0 {
		t.Fatalf("code = %d stderr=%s stdout=%s", code, stderr.String(), stdout.String())
	}
	text := stdout.String()
	for _, want := range []string{
		`"type":"synthesis_requested"`,
		`"model":"deepseek/final-model"`,
		`"summary":"structured synthesis"`,
		`"final_outcome":"completed"`,
	} {
		if !strings.Contains(text, want) {
			t.Fatalf("stdout missing %q:\n%s", want, text)
		}
	}
	if requests != openRouterMinimumTurnBeforeStallCheck {
		t.Fatalf("requests = %d, want %d", requests, openRouterMinimumTurnBeforeStallCheck)
	}
}

func TestRunExecOpenRouterSynthesisToolCallFallsBackToToolLoop(t *testing.T) {
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
		if requests < openRouterMinimumTurnBeforeStallCheck {
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
		if requests == openRouterMinimumTurnBeforeStallCheck {
			if body["model"] != "deepseek/final-model" {
				t.Fatalf("synthesis request model = %#v, want final model", body["model"])
			}
			toolsValue, ok := body["tools"].([]any)
			if !ok || len(toolsValue) != 1 {
				t.Fatalf("synthesis request tools = %#v, want only final_response", body["tools"])
			}
			tool, _ := toolsValue[0].(map[string]any)
			function, _ := tool["function"].(map[string]any)
			if function["name"] != "final_response" {
				t.Fatalf("synthesis tool = %#v, want final_response", toolsValue[0])
			}
			_, _ = w.Write([]byte(`{
				"id":"resp_bad_synthesis_tool",
				"model":"deepseek/final-model",
				"choices":[{
					"finish_reason":"tool_calls",
					"message":{"role":"assistant","tool_calls":[{"id":"call_read_after_synthesis","type":"function","function":{"name":"read_file","arguments":"{\"path\":\"README.md\",\"limit\":1}"}}]}
				}]
			}`))
			return
		}
		if requests == openRouterMinimumTurnBeforeStallCheck+1 {
			if body["model"] != "deepseek/test-model" {
				t.Fatalf("fallback request model = %#v, want lead model", body["model"])
			}
			toolsValue, ok := body["tools"].([]any)
			if !ok {
				t.Fatalf("fallback request tools = %#v, want tool list", body["tools"])
			}
			if !lcagentCLITestRequestHasTool(toolsValue, "read_file") {
				t.Fatalf("fallback request missing read_file tool: %#v", body["tools"])
			}
			messagesText := fmt.Sprint(body["messages"])
			if !strings.Contains(messagesText, "Synthesis feedback: this planned synthesis checkpoint could not accept the attempted tool call") {
				t.Fatalf("fallback request missing synthesis feedback: %s", messagesText)
			}
			_, _ = w.Write([]byte(`{
				"id":"resp_final_after_fallback",
				"model":"deepseek/test-model",
				"choices":[{
					"finish_reason":"tool_calls",
					"message":{"role":"assistant","tool_calls":[{"id":"call_final","type":"function","function":{"name":"final_response","arguments":{"summary":"finished after synthesis fallback","outcome":"completed","files_changed":[],"verification":[]}}}]}
				}]
			}`))
			return
		}
		t.Fatalf("unexpected request %d: %#v", requests, body)
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
		"--require-final-response-tool",
		"keep reading until synthesis",
	}, &stdout, &stderr)
	if code != 0 {
		t.Fatalf("code = %d stderr=%s stdout=%s", code, stderr.String(), stdout.String())
	}
	text := stdout.String()
	for _, want := range []string{
		`"type":"synthesis_requested"`,
		`"type":"synthesis_tool_call_rejected"`,
		`"attempted_tools":["read_file"]`,
		`"summary":"finished after synthesis fallback"`,
	} {
		if !strings.Contains(text, want) {
			t.Fatalf("stdout missing %q:\n%s", want, text)
		}
	}
	if strings.Contains(text, `"type":"turn_aborted"`) {
		t.Fatalf("synthesis fallback should not abort:\n%s", text)
	}
	if requests != openRouterMinimumTurnBeforeStallCheck+1 {
		t.Fatalf("requests = %d, want %d", requests, openRouterMinimumTurnBeforeStallCheck+1)
	}
}

func TestRunExecOpenRouterSynthesisToolCallAtHardLimitFallsBackToPartialFinal(t *testing.T) {
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
		if requests < openRouterMinimumTurnBeforeStallCheck {
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
		toolsValue, ok := body["tools"].([]any)
		if !ok || len(toolsValue) != 1 {
			t.Fatalf("synthesis request tools = %#v, want only final_response", body["tools"])
		}
		_, _ = w.Write([]byte(`{
			"id":"resp_bad_synthesis_tool",
			"model":"deepseek/final-model",
			"choices":[{
				"finish_reason":"tool_calls",
				"message":{"role":"assistant","tool_calls":[{"id":"call_read_at_hard_limit","type":"function","function":{"name":"read_file","arguments":"{\"path\":\"README.md\",\"limit\":1}"}}]}
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
		"--max-turns", fmt.Sprint(openRouterMinimumTurnBeforeStallCheck),
		"--require-final-response-tool",
		"keep reading until hard-limit synthesis",
	}, &stdout, &stderr)
	if code != 0 {
		t.Fatalf("code = %d stderr=%s stdout=%s", code, stderr.String(), stdout.String())
	}
	text := stdout.String()
	for _, want := range []string{
		`"type":"synthesis_requested"`,
		`"type":"synthesis_tool_call_rejected"`,
		`"attempted_tools":["read_file"]`,
		`"type":"final_handoff_fallback"`,
		`hard turn limit`,
		`"final_outcome":"partial"`,
	} {
		if !strings.Contains(text, want) {
			t.Fatalf("stdout missing %q:\n%s", want, text)
		}
	}
	if strings.Contains(text, `"type":"turn_aborted"`) {
		t.Fatalf("hard-limit synthesis fallback should not abort:\n%s", text)
	}
	if requests != openRouterMinimumTurnBeforeStallCheck {
		t.Fatalf("requests = %d, want %d", requests, openRouterMinimumTurnBeforeStallCheck)
	}
}

func TestRunExecOpenRouterMalformedFinalResponseArgumentsReturnToolResult(t *testing.T) {
	isolateSkillHomes(t)
	root := t.TempDir()
	requests := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests++
		var body map[string]any
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			t.Fatalf("decode request body: %v", err)
		}
		w.Header().Set("Content-Type", "application/json")
		switch requests {
		case 1:
			_, _ = w.Write([]byte(`{
				"id":"resp_bad_final_args",
				"model":"deepseek/test-model",
				"choices":[{
					"finish_reason":"tool_calls",
					"message":{"role":"assistant","tool_calls":[{"type":"function","function":{"name":"final_response","arguments":{"summary":123,"outcome":"completed","files_changed":[],"verification":[]}}}]}
				}]
			}`))
		case 2:
			messages, _ := body["messages"].([]any)
			var assistantToolCallID string
			var toolResultCallID string
			var toolResultContent string
			for _, raw := range messages {
				msg, _ := raw.(map[string]any)
				switch msg["role"] {
				case "assistant":
					toolCalls, _ := msg["tool_calls"].([]any)
					for _, rawCall := range toolCalls {
						call, _ := rawCall.(map[string]any)
						function, _ := call["function"].(map[string]any)
						if function["name"] == "final_response" {
							assistantToolCallID, _ = call["id"].(string)
						}
					}
				case "tool":
					if content, _ := msg["content"].(string); strings.Contains(content, "invalid final_response arguments") {
						toolResultCallID, _ = msg["tool_call_id"].(string)
						toolResultContent = content
					}
				}
			}
			if assistantToolCallID == "" {
				t.Fatalf("fallback request missing synthesized assistant tool call id: %#v", messages)
			}
			if toolResultCallID != assistantToolCallID {
				t.Fatalf("tool result call id = %q, want assistant id %q; messages=%#v", toolResultCallID, assistantToolCallID, messages)
			}
			if !strings.Contains(toolResultContent, "cannot unmarshal number") {
				t.Fatalf("tool result content missing decode detail: %s", toolResultContent)
			}
			_, _ = w.Write([]byte(`{
				"id":"resp_valid_final",
				"model":"deepseek/test-model",
				"choices":[{
					"finish_reason":"tool_calls",
					"message":{"role":"assistant","tool_calls":[{"id":"call_final","type":"function","function":{"name":"final_response","arguments":{"summary":"recovered after malformed final_response arguments","outcome":"completed","files_changed":[],"verification":[]}}}]}
				}]
			}`))
		default:
			t.Fatalf("unexpected request %d: %#v", requests, body)
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
		"--auto", "off",
		"--output", "stream-json",
		"--provider", "openrouter",
		"--model", "deepseek/test-model",
		"--max-turns", "3",
		"--require-final-response-tool",
		"finish with malformed args first",
	}, &stdout, &stderr)
	if code != 0 {
		t.Fatalf("code = %d stderr=%s stdout=%s", code, stderr.String(), stdout.String())
	}
	text := stdout.String()
	for _, want := range []string{
		`"type":"tool_result"`,
		`invalid final_response arguments`,
		`"summary":"recovered after malformed final_response arguments"`,
	} {
		if !strings.Contains(text, want) {
			t.Fatalf("stdout missing %q:\n%s", want, text)
		}
	}
	if strings.Contains(text, `"type":"turn_aborted"`) {
		t.Fatalf("malformed final_response arguments should not abort:\n%s", text)
	}
	if requests != 2 {
		t.Fatalf("requests = %d, want 2", requests)
	}
}

func TestRunExecLongRequestTimeoutExpandsDefaultMaxTurns(t *testing.T) {
	isolateSkillHomes(t)
	root := t.TempDir()
	requests := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests++
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{
			"id":"resp_done",
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
		"--request-timeout", "1h",
		"finish immediately",
	}, &stdout, &stderr)
	if code != 0 {
		t.Fatalf("code = %d stderr=%s stdout=%s", code, stderr.String(), stdout.String())
	}
	text := stdout.String()
	for _, want := range []string{
		`"request_timeout":"1h0m0s"`,
		`"max_turns":128`,
		`"summary":"done"`,
	} {
		if !strings.Contains(text, want) {
			t.Fatalf("stdout missing %q:\n%s", want, text)
		}
	}
	if requests != 1 {
		t.Fatalf("requests = %d, want 1", requests)
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
	if !report.Passed || len(report.Cases) != 12 {
		t.Fatalf("eval report = %#v, want twelve passing cases", report)
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
		report.Summary.VerificationCheckStatuses["denied"] < 1 ||
		report.Summary.VerificationCheckStatuses["timed_out"] < 1 ||
		report.Summary.ToolFailures["start_process"] < 1 ||
		report.Summary.OperationalActions < 1 ||
		report.Summary.OperationalActionStatuses["start.failed"] < 1 ||
		report.Summary.FinalResponseAudits < 1 ||
		report.Summary.FinalResponseAuditOutcomes["pass"] < 1 {
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
	if len(listed.Cases) != 10 {
		t.Fatalf("live eval cases = %d, want 10", len(listed.Cases))
	}
	byName := map[string]liveEvalTask{}
	for _, tc := range listed.Cases {
		byName[tc.Name] = tc
	}
	for _, want := range []string{"readme_edit_verify", "go_bug_fix", "feature_slice", "js_package_script_fix", "python_unittest_fix", "rust_cargo_fix", "repo_orientation", "managed_process_unavailable_handoff", "current_diff_review", "multi_file_price_refactor"} {
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
	if byName["managed_process_unavailable_handoff"].ExpectedVerificationStatus != "reported_only" || len(byName["managed_process_unavailable_handoff"].ExpectedFinalOutcomes) == 0 || !byName["managed_process_unavailable_handoff"].RequireFinalResponseTool {
		t.Fatalf("managed_process_unavailable_handoff list entry missing outcome contract: %#v", byName["managed_process_unavailable_handoff"])
	}
	if byName["js_package_script_fix"].Category != "js_ts" || strings.Join(byName["js_package_script_fix"].VerifyCommand, " ") != "npm test" {
		t.Fatalf("js_package_script_fix list entry missing JS verification contract: %#v", byName["js_package_script_fix"])
	}
	if byName["python_unittest_fix"].Category != "python" || strings.Join(byName["python_unittest_fix"].VerifyCommand, " ") != "python3 -m unittest" {
		t.Fatalf("python_unittest_fix list entry missing Python verification contract: %#v", byName["python_unittest_fix"])
	}
	if byName["rust_cargo_fix"].Category != "rust" || strings.Join(byName["rust_cargo_fix"].VerifyCommand, " ") != "cargo test" {
		t.Fatalf("rust_cargo_fix list entry missing Rust verification contract: %#v", byName["rust_cargo_fix"])
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
{"type":"turn_complete","summary":"done","final_outcome":"blocked","files_changed":["README.md"],"verification":["go test ./..."]}
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
	if !changed["calc.go"] || !changed["README.md"] {
		t.Fatalf("changed files = %#v", changed)
	}
	outcomes, err := liveEvalFinalOutcomes(path)
	if err != nil {
		t.Fatalf("final outcomes: %v", err)
	}
	if !liveEvalAllowedFinalOutcome(outcomes, []string{"blocked"}) {
		t.Fatalf("final outcomes = %#v, want blocked", outcomes)
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

func lcagentCLITestSessionIDFromStream(t *testing.T, stream string) string {
	t.Helper()
	for _, line := range strings.Split(stream, "\n") {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		var event map[string]json.RawMessage
		if err := json.Unmarshal([]byte(line), &event); err != nil {
			continue
		}
		var eventType string
		_ = json.Unmarshal(event["type"], &eventType)
		if strings.TrimSpace(eventType) == "session_meta" {
			var id string
			_ = json.Unmarshal(event["id"], &id)
			id = strings.TrimSpace(id)
			if id == "" {
				t.Fatalf("session_meta missing id in stream:\n%s", stream)
			}
			return id
		}
	}
	t.Fatalf("stream missing session_meta:\n%s", stream)
	return ""
}

func lcagentCLITestThreadIDFromStream(t *testing.T, stream string) string {
	t.Helper()
	for _, line := range strings.Split(stream, "\n") {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		var event map[string]json.RawMessage
		if err := json.Unmarshal([]byte(line), &event); err != nil {
			continue
		}
		var eventType string
		_ = json.Unmarshal(event["type"], &eventType)
		if strings.TrimSpace(eventType) == "session_meta" {
			var id string
			_ = json.Unmarshal(event["thread_id"], &id)
			id = strings.TrimSpace(id)
			if id == "" {
				t.Fatalf("session_meta missing thread_id in stream:\n%s", stream)
			}
			return id
		}
	}
	t.Fatalf("stream missing session_meta:\n%s", stream)
	return ""
}

func isolateSkillHomes(t *testing.T) {
	t.Helper()
	t.Setenv("CODEX_HOME", filepath.Join(t.TempDir(), "codex"))
	t.Setenv("AGENTS_HOME", filepath.Join(t.TempDir(), "agents"))
}
