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

func testProgressReport() progressCheckpointReport {
	return progressCheckpointReport{
		Objective: "Scan both sides and save a named document.", UserUpdate: "The destination is known. I will avoid foreground UI while you use the computer.",
		NewEvidence: []string{"Destination folder located."}, Constraints: []string{"Do not interfere with the user's desktop."},
		Blocker: "The document must be flipped.", Decision: "continue", NextAction: "Request the physical flip before another acquisition.",
	}
}

func harnessTestCall(name string, args any) modeladapter.ToolCall {
	body, _ := json.Marshal(args)
	return modeladapter.ToolCall{ID: "call_" + name, Type: "function", Function: modeladapter.FunctionCall{Name: name, Arguments: body}}
}

func replyHarnessTest(w http.ResponseWriter, provider string, turn int, text string, calls ...modeladapter.ToolCall) {
	w.Header().Set("Content-Type", "application/json")
	if provider == "openai" {
		var output []any
		for i, call := range calls {
			output = append(output, map[string]any{"type": "function_call", "id": fmt.Sprintf("fc_%d_%d", turn, i), "call_id": fmt.Sprintf("call_%d_%d", turn, i), "name": call.Function.Name, "arguments": string(call.Function.Arguments)})
		}
		if text != "" {
			output = append(output, map[string]any{"type": "message", "role": "assistant", "content": []any{map[string]any{"type": "output_text", "text": text}}})
		}
		_ = json.NewEncoder(w).Encode(map[string]any{"id": fmt.Sprintf("resp_%d", turn), "status": "completed", "model": "test-model", "output": output})
		return
	}
	_ = json.NewEncoder(w).Encode(map[string]any{"id": fmt.Sprintf("resp_%d", turn), "model": "test-model", "choices": []any{map[string]any{"finish_reason": "stop", "message": modeladapter.Message{Role: "assistant", Content: text, ToolCalls: calls}}}})
}

func assertHarnessTools(t *testing.T, body map[string]json.RawMessage, want string) {
	t.Helper()
	var defs []struct {
		Name     string `json:"name"`
		Function struct {
			Name string `json:"name"`
		} `json:"function"`
	}
	if err := json.Unmarshal(body["tools"], &defs); err != nil {
		t.Error(err)
	}
	if len(defs) != 1 || (defs[0].Name != want && defs[0].Function.Name != want) {
		t.Errorf("tools=%s, want only %s", body["tools"], want)
	}
}

func runHarnessTest(t *testing.T, provider, url string, steer <-chan string) (string, error) {
	t.Helper()
	var stream bytes.Buffer
	writer, id, err := session.NewWriter(t.TempDir(), time.Now(), &stream)
	if err != nil {
		t.Fatal(err)
	}
	defer writer.Close()
	workspace, err := policy.NewWorkspace(t.TempDir(), policy.AutonomyMedium)
	if err != nil {
		t.Fatal(err)
	}
	limits := tools.FileLimitsForProfile(tools.FileProfileBalanced)
	runner := script.Runner{Session: writer, SessionID: id, Prompt: "Scan both sides and save a named document.", SteerMessages: steer,
		Command: tools.CommandRunner{Workspace: workspace}, Patch: tools.PatchApplier{Workspace: workspace}, Files: tools.FileTools{Workspace: workspace, Limits: limits}}
	err = runChatLoop(context.Background(), writer, runner, nil, "", nil,
		modeladapter.OpenRouterConfig{APIKey: "test-key", BaseURL: url, Model: "test-model", MaxTurns: 12},
		modeladapter.OpenRouterConfig{}, modeladapter.OpenRouterConfig{}, provider, "off", "off", script.DefaultSearchRefineMinBytes,
		tools.FileProfileBalanced, limits, openRouterContextOptions{}, true, false, false)
	return stream.String(), err
}

func TestHarnessCheckpointAndHandoffAcrossProviders(t *testing.T) {
	for _, provider := range []string{"openai", "deepseek", "openrouter", "moonshot", "xiaomi", "ollama", "mlx"} {
		t.Run(provider, func(t *testing.T) {
			requests := 0
			const handoff = "Please flip the document and tell me when it is ready. Only the front side has been acquired."
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				requests++
				var body map[string]json.RawMessage
				if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
					t.Error(err)
					return
				}
				switch requests {
				case 1:
					var calls []modeladapter.ToolCall
					for i := 0; i < progressCheckpointCalls; i++ {
						call := harnessTestCall("run_command", map[string]any{"argv": []string{"printf", fmt.Sprintf("evidence %d", i)}, "purpose": "verify"})
						call.ID = fmt.Sprintf("probe_%d", i)
						calls = append(calls, call)
					}
					replyHarnessTest(w, provider, requests, "", calls...)
				case 2:
					assertHarnessTools(t, body, progressCheckpointTool)
					replyHarnessTest(w, provider, requests, "", harnessTestCall(progressCheckpointTool, testProgressReport()))
				case 3:
					replyHarnessTest(w, provider, requests, handoff)
				case 4:
					assertHarnessTools(t, body, "final_response")
					// Deliberately omit the human action in conversion. The host must
					// retain it without detecting question words or task keywords.
					replyHarnessTest(w, provider, requests, "", harnessTestCall("final_response", map[string]any{"summary": "A scan was attempted.", "outcome": "partial", "files_changed": []string{}, "verification": []string{}}))
				default:
					t.Errorf("unexpected request %d", requests)
					http.Error(w, "unexpected request", http.StatusBadRequest)
				}
			}))
			defer server.Close()
			trace, err := runHarnessTest(t, provider, server.URL, nil)
			if err != nil {
				t.Fatalf("loop failed: %v\n%s", err, trace)
			}
			for _, want := range []string{`"type":"progress_checkpoint"`, `"reason":"work_budget"`, `"message":"` + testProgressReport().UserUpdate + `"`, `"summary":"` + handoff + `"`} {
				if !strings.Contains(trace, want) {
					t.Errorf("trace missing %s", want)
				}
			}
			if requests != 4 {
				t.Errorf("requests=%d, want 4", requests)
			}
		})
	}
}

func TestSteeringRequiresAssessmentBeforeFurtherExecution(t *testing.T) {
	steer := make(chan string, 1)
	requests := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests++
		var body map[string]json.RawMessage
		_ = json.NewDecoder(r.Body).Decode(&body)
		switch requests {
		case 1:
			steer <- "I need to use the computer. Avoid the foreground UI."
			replyHarnessTest(w, "deepseek", requests, "", harnessTestCall("run_command", map[string]any{"argv": []string{"printf", "device detected"}}))
		case 2:
			assertHarnessTools(t, body, progressCheckpointTool)
			if !bytes.Contains(body["messages"], []byte("Scan both sides")) || !bytes.Contains(body["messages"], []byte("Avoid the foreground UI")) {
				t.Error("checkpoint lost original task or steering")
			}
			// Even a valid report cannot smuggle an action into the checkpoint.
			replyHarnessTest(w, "deepseek", requests, "", harnessTestCall(progressCheckpointTool, testProgressReport()), harnessTestCall("run_command", map[string]any{"argv": []string{"printf", "must not execute"}}))
		case 3:
			assertHarnessTools(t, body, progressCheckpointTool)
			report := testProgressReport()
			report.Decision = "finish"
			replyHarnessTest(w, "deepseek", requests, "", harnessTestCall(progressCheckpointTool, report))
		case 4:
			assertHarnessTools(t, body, "final_response")
			replyHarnessTest(w, "deepseek", requests, "", harnessTestCall("final_response", map[string]any{"summary": "Please flip the document when ready.", "outcome": "partial", "files_changed": []string{}, "verification": []string{}}))
		default:
			t.Errorf("unexpected request %d", requests)
			http.Error(w, "unexpected request", http.StatusBadRequest)
		}
	}))
	defer server.Close()
	trace, err := runHarnessTest(t, "deepseek", server.URL, steer)
	if err != nil {
		t.Fatalf("loop: %v\n%s", err, trace)
	}
	if !strings.Contains(trace, `"reason":"user_correction"`) || strings.Contains(trace, `"output":"must not execute`) {
		t.Fatalf("bad steering trace:\n%s", trace)
	}
}

func TestProgressCheckpointClockAndSchema(t *testing.T) {
	now := time.Now()
	s := progressCheckpointState{last: now.Add(-time.Minute)}
	if s.reason(now) != "" {
		t.Fatal("idle state scheduled a checkpoint")
	}
	s.calls = 1
	if s.reason(now) != "elapsed_time" {
		t.Fatal("elapsed work missed checkpoint")
	}
	s.steered = true
	if s.reason(now) != "user_correction" {
		t.Fatal("correction must take priority")
	}
	for _, args := range []string{`{}`, `null`, `{"objective":"x","user_update":"x","new_evidence":[],"constraints":[],"blocker":"","decision":"guess","next_action":"x"}`} {
		if _, err := decodeProgressCheckpoint(json.RawMessage(args)); err == nil {
			t.Errorf("accepted invalid report %s", args)
		}
	}
}

func TestInvalidCheckpointStopsWithoutExecutingTools(t *testing.T) {
	steer := make(chan string, 1)
	steer <- "Avoid foreground interaction."
	requests := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests++
		replyHarnessTest(w, "deepseek", requests, "", harnessTestCall("run_command", map[string]any{"argv": []string{"printf", "unwanted action"}}))
	}))
	defer server.Close()
	trace, err := runHarnessTest(t, "deepseek", server.URL, steer)
	if err == nil || !strings.Contains(err.Error(), "checkpoint failed twice") || requests != 2 {
		t.Fatalf("err=%v requests=%d", err, requests)
	}
	if !strings.Contains(trace, `"type":"turn_aborted"`) || strings.Contains(trace, `"output":"unwanted action`) {
		t.Fatalf("invalid checkpoint executed or hid failure:\n%s", trace)
	}
}

func TestFinalizationRetainsAuditRepairPath(t *testing.T) {
	requests := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests++
		switch requests {
		case 1:
			replyHarnessTest(w, "deepseek", requests, "", harnessTestCall("run_command", map[string]any{"argv": []string{"false"}, "purpose": "verify"}))
		case 2:
			replyHarnessTest(w, "deepseek", requests, "Incorrect initial completion claim.")
		case 3:
			replyHarnessTest(w, "deepseek", requests, "", harnessTestCall("final_response", map[string]any{"summary": "done", "outcome": "completed", "files_changed": []string{}, "verification": []string{"verification passed"}}))
		case 4:
			replyHarnessTest(w, "deepseek", requests, "", harnessTestCall("run_command", map[string]any{"argv": []string{"printf", "verified result"}, "purpose": "verify"}))
		case 5:
			replyHarnessTest(w, "deepseek", requests, "", harnessTestCall("final_response", map[string]any{"summary": "Corrected answer after verification.", "outcome": "completed", "files_changed": []string{}, "verification": []string{"printf verified result passed"}}))
		default:
			http.Error(w, "unexpected request", http.StatusBadRequest)
		}
	}))
	defer server.Close()
	trace, err := runHarnessTest(t, "deepseek", server.URL, nil)
	if err != nil {
		t.Fatalf("loop: %v\n%s", err, trace)
	}
	if requests != 5 || !strings.Contains(trace, `"summary":"Corrected answer after verification."`) || !strings.Contains(trace, `"type":"verification_feedback"`) {
		t.Fatalf("failed audit did not reopen repair:\n%s", trace)
	}
}

func TestMalformedCheckpointArgumentsRemainAuditable(t *testing.T) {
	var stream bytes.Buffer
	writer, id, err := session.NewWriter(t.TempDir(), time.Now(), &stream)
	if err != nil {
		t.Fatal(err)
	}
	defer writer.Close()
	call := harnessTestCall(progressCheckpointTool, nil)
	call.Function.Arguments = json.RawMessage(`{"objective":`)
	messages, report, err := acceptProgressCheckpoint(writer, id, nil, modeladapter.Message{Role: "assistant", ToolCalls: []modeladapter.ToolCall{call}})
	if err != nil || report != nil || len(messages) != 3 || messages[1].ToolCallID != call.ID || !strings.Contains(stream.String(), `"raw_args"`) {
		t.Fatalf("report=%+v messages=%+v err=%v trace=%s", report, messages, err, stream.String())
	}
}

func TestFinalizationRejectsAdditionalExecutionWithoutLosingHandoff(t *testing.T) {
	requests := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests++
		switch requests {
		case 1:
			replyHarnessTest(w, "openai", requests, "", harnessTestCall("run_command", map[string]any{"argv": []string{"printf", "ready"}, "purpose": "verify"}))
		case 2:
			replyHarnessTest(w, "openai", requests, "Please flip the document.")
		case 3:
			replyHarnessTest(w, "openai", requests, "", harnessTestCall("run_command", map[string]any{"argv": []string{"printf", "unwanted execution"}}))
		case 4:
			replyHarnessTest(w, "openai", requests, "", harnessTestCall("final_response", map[string]any{"summary": "partial result", "outcome": "partial", "files_changed": []string{}, "verification": []string{}}))
		default:
			http.Error(w, "unexpected request", http.StatusBadRequest)
		}
	}))
	defer server.Close()
	trace, err := runHarnessTest(t, "openai", server.URL, nil)
	if err != nil {
		t.Fatalf("loop: %v\n%s", err, trace)
	}
	if requests != 4 || strings.Contains(trace, `"output":"unwanted execution`) || !strings.Contains(trace, `"summary":"Please flip the document."`) || !strings.Contains(trace, "This tool was not executed") {
		t.Fatalf("finalization did not preserve handoff and block execution:\n%s", trace)
	}
}
