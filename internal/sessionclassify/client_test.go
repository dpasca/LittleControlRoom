package sessionclassify

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"syscall"
	"testing"
	"time"

	"lcroom/internal/brand"
	"lcroom/internal/llm"
	"lcroom/internal/model"
)

type roundTripperFunc func(*http.Request) (*http.Response, error)

func (f roundTripperFunc) RoundTrip(req *http.Request) (*http.Response, error) {
	return f(req)
}

type fakeJSONSchemaRunner struct {
	run func(context.Context, llm.JSONSchemaRequest) (llm.JSONSchemaResponse, error)
}

func (f fakeJSONSchemaRunner) RunJSONSchema(ctx context.Context, req llm.JSONSchemaRequest) (llm.JSONSchemaResponse, error) {
	return f.run(ctx, req)
}

func TestNewCodexClientWithUsageTrackerDefaultsToLocalCompatibleModel(t *testing.T) {
	t.Setenv(brand.SessionClassifierModelEnvVar, "")

	client := NewCodexClientWithUsageTracker(nil)
	if client == nil {
		t.Fatalf("expected codex client")
	}
	if got := client.ModelName(); got != localRunnerDefaultModel {
		t.Fatalf("ModelName() = %q, want %q", got, localRunnerDefaultModel)
	}
}

func TestNewOpenCodeClientWithUsageTrackerDefaultsToLocalCompatibleModel(t *testing.T) {
	t.Setenv(brand.SessionClassifierModelEnvVar, "")

	client := NewOpenCodeClientWithUsageTracker(nil)
	if client == nil {
		t.Fatalf("expected opencode client")
	}
	if got := client.ModelName(); got != localRunnerDefaultModel {
		t.Fatalf("ModelName() = %q, want %q", got, localRunnerDefaultModel)
	}
}

func TestOpenAIClientClassifyRepairsStructuredOutputWhenFieldsAreMissing(t *testing.T) {
	t.Parallel()

	callCount := 0
	client := &OpenAIClient{
		model: "gpt-5.4-mini",
		responses: fakeJSONSchemaRunner{
			run: func(_ context.Context, req llm.JSONSchemaRequest) (llm.JSONSchemaResponse, error) {
				callCount++
				if callCount == 1 {
					if req.ReasoningEffort != classifierPrimaryReasoningEffort {
						t.Fatalf("first reasoning effort = %q, want %q", req.ReasoningEffort, classifierPrimaryReasoningEffort)
					}
					return llm.JSONSchemaResponse{
						Status:     "completed",
						Model:      "gpt-5.4-mini",
						OutputText: `{"classification":"completed","description":"Work is wrapped up."}`,
					}, nil
				}
				if req.SystemText != sessionClassificationRepairInstructions {
					t.Fatalf("second system prompt = %q, want repair prompt", req.SystemText)
				}
				if req.ReasoningEffort != classifierRepairReasoningEffort {
					t.Fatalf("second reasoning effort = %q, want %q", req.ReasoningEffort, classifierRepairReasoningEffort)
				}
				return llm.JSONSchemaResponse{
					Status:     "completed",
					Model:      "gpt-5.4-mini",
					OutputText: `{"category":"completed","summary":"Work is wrapped up."}`,
				}, nil
			},
		},
	}

	result, err := client.Classify(context.Background(), SessionSnapshot{
		ProjectPath:          "/tmp/demo",
		SessionID:            "ses_demo",
		SessionFormat:        "modern",
		LastEventAt:          time.Now().UTC().Format(time.RFC3339),
		LatestTurnStateKnown: true,
		LatestTurnCompleted:  true,
		Transcript: []TranscriptItem{
			{Role: "user", Text: "Ship the change."},
			{Role: "assistant", Text: "Done and verified."},
		},
	})
	if err != nil {
		t.Fatalf("Classify() error = %v", err)
	}
	if callCount != 2 {
		t.Fatalf("runner call count = %d, want 2 after repairing malformed output", callCount)
	}
	if result.Category != model.SessionCategoryCompleted {
		t.Fatalf("category = %s, want completed", result.Category)
	}
	if result.Summary != "Work is wrapped up." {
		t.Fatalf("summary = %q, want valid retried summary", result.Summary)
	}
}

func TestOpenAIClientClassifyFallsBackAfterRepairFailure(t *testing.T) {
	t.Parallel()

	callCount := 0
	client := &OpenAIClient{
		model: "gpt-5.4-mini",
		responses: fakeJSONSchemaRunner{
			run: func(_ context.Context, req llm.JSONSchemaRequest) (llm.JSONSchemaResponse, error) {
				callCount++
				switch callCount {
				case 1:
					if req.SystemText != sessionClassificationInstructions {
						t.Fatalf("first system prompt = %q, want classify prompt", req.SystemText)
					}
					if req.ReasoningEffort != classifierPrimaryReasoningEffort {
						t.Fatalf("first reasoning effort = %q, want %q", req.ReasoningEffort, classifierPrimaryReasoningEffort)
					}
					return llm.JSONSchemaResponse{
						Status:     "completed",
						Model:      "gpt-5.4-mini",
						OutputText: `{"classification":"completed","description":"Work is wrapped up."}`,
					}, nil
				case 2:
					if req.SystemText != sessionClassificationRepairInstructions {
						t.Fatalf("second system prompt = %q, want repair prompt", req.SystemText)
					}
					if req.ReasoningEffort != classifierRepairReasoningEffort {
						t.Fatalf("second reasoning effort = %q, want %q", req.ReasoningEffort, classifierRepairReasoningEffort)
					}
					return llm.JSONSchemaResponse{
						Status:     "completed",
						Model:      "gpt-5.4-mini",
						OutputText: `{"classification":"completed"}`,
					}, nil
				case 3:
					if req.SystemText != sessionClassificationInstructions {
						t.Fatalf("third system prompt = %q, want classify prompt", req.SystemText)
					}
					if req.ReasoningEffort != classifierFallbackReasoningEffort {
						t.Fatalf("third reasoning effort = %q, want %q", req.ReasoningEffort, classifierFallbackReasoningEffort)
					}
					return llm.JSONSchemaResponse{
						Status:     "completed",
						Model:      "gpt-5.4-mini",
						OutputText: `{"category":"completed","summary":"Work is wrapped up."}`,
					}, nil
				default:
					t.Fatalf("unexpected runner call %d", callCount)
					return llm.JSONSchemaResponse{}, nil
				}
			},
		},
	}

	result, err := client.Classify(context.Background(), SessionSnapshot{
		ProjectPath:          "/tmp/demo",
		SessionID:            "ses_demo",
		SessionFormat:        "modern",
		LastEventAt:          time.Now().UTC().Format(time.RFC3339),
		LatestTurnStateKnown: true,
		LatestTurnCompleted:  true,
		Transcript: []TranscriptItem{
			{Role: "user", Text: "Ship the change."},
			{Role: "assistant", Text: "Done and verified."},
		},
	})
	if err != nil {
		t.Fatalf("Classify() error = %v", err)
	}
	if callCount != 3 {
		t.Fatalf("runner call count = %d, want 3 after repair then fallback attempt", callCount)
	}
	if result.Category != model.SessionCategoryCompleted {
		t.Fatalf("category = %s, want completed", result.Category)
	}
}

func TestOpenAIClientClassifyCapturesUsage(t *testing.T) {
	t.Parallel()

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			t.Fatalf("method = %s, want POST", r.Method)
		}
		if got := r.Header.Get("Authorization"); got != "Bearer test-key" {
			t.Fatalf("authorization = %q, want bearer token", got)
		}
		body, err := io.ReadAll(r.Body)
		if err != nil {
			t.Fatalf("read request body: %v", err)
		}
		var req struct {
			Input []struct {
				Content []struct {
					Text string `json:"text"`
				} `json:"content"`
			} `json:"input"`
			Reasoning struct {
				Effort string `json:"effort"`
			} `json:"reasoning"`
			MaxOutputTokens *int `json:"max_output_tokens"`
		}
		if err := json.Unmarshal(body, &req); err != nil {
			t.Fatalf("decode request body: %v", err)
		}
		if len(req.Input) < 2 || len(req.Input[1].Content) == 0 {
			t.Fatalf("unexpected request structure: %s", string(body))
		}
		if req.Reasoning.Effort != "medium" {
			t.Fatalf("reasoning effort = %q, want %q", req.Reasoning.Effort, "medium")
		}
		if req.MaxOutputTokens != nil {
			t.Fatalf("max_output_tokens = %v, want omitted field", *req.MaxOutputTokens)
		}
		userText := req.Input[1].Content[0].Text
		prefix := "Classify this latest coding-session snapshot:\n\n"
		if !strings.HasPrefix(userText, prefix) {
			t.Fatalf("unexpected classifier prompt: %q", userText)
		}
		var snapshot SessionSnapshot
		if err := json.Unmarshal([]byte(strings.TrimPrefix(userText, prefix)), &snapshot); err != nil {
			t.Fatalf("decode embedded snapshot: %v", err)
		}
		if snapshot.GitStatus.RemoteStatus != "ahead" {
			t.Fatalf("git remote status = %q, want %q", snapshot.GitStatus.RemoteStatus, "ahead")
		}
		if !snapshot.GitStatus.WorktreeDirty {
			t.Fatalf("expected git dirty flag in snapshot")
		}
		if snapshot.GitStatus.AheadCount != 2 {
			t.Fatalf("git ahead count = %d, want %d", snapshot.GitStatus.AheadCount, 2)
		}
		if !snapshot.LatestTurnStateKnown || !snapshot.LatestTurnCompleted {
			t.Fatalf("expected lifecycle flags in snapshot, got known=%v completed=%v", snapshot.LatestTurnStateKnown, snapshot.LatestTurnCompleted)
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{
			"status":"completed",
			"model":"gpt-5-mini-2026-03-01",
			"usage":{
				"input_tokens":345,
				"input_tokens_details":{"cached_tokens":21},
				"output_tokens":67,
				"output_tokens_details":{"reasoning_tokens":5},
				"total_tokens":412
			},
			"output":[
				{
					"type":"message",
					"role":"assistant",
					"content":[
						{
							"type":"output_text",
							"text":"{\"category\":\"completed\",\"summary\":\"Work is wrapped up.\"}"
						}
					]
				}
			]
		}`))
	}))
	defer server.Close()

	client := &OpenAIClient{
		apiKey:   "test-key",
		model:    "gpt-5-mini",
		endpoint: server.URL,
		httpClient: &http.Client{
			Timeout: 5 * time.Second,
		},
	}

	result, err := client.Classify(context.Background(), SessionSnapshot{
		ProjectPath:          "/tmp/demo",
		SessionID:            "ses_demo",
		LatestTurnStateKnown: true,
		LatestTurnCompleted:  true,
		GitStatus: GitStatusSnapshot{
			WorktreeDirty: true,
			RemoteStatus:  "ahead",
			AheadCount:    2,
		},
	})
	if err != nil {
		t.Fatalf("classify: %v", err)
	}
	if result.Category != model.SessionCategoryCompleted {
		t.Fatalf("category = %s, want completed", result.Category)
	}
	if result.Model != "gpt-5-mini-2026-03-01" {
		t.Fatalf("model = %q, want response model", result.Model)
	}
	if result.Usage.InputTokens != 345 || result.Usage.OutputTokens != 67 || result.Usage.TotalTokens != 412 {
		t.Fatalf("unexpected usage totals: %+v", result.Usage)
	}
	if result.Usage.CachedInputTokens != 21 || result.Usage.ReasoningTokens != 5 {
		t.Fatalf("unexpected usage detail totals: %+v", result.Usage)
	}
}

func TestOpenAIClientClassifyRequestsImplicitAssistantPOVSummaries(t *testing.T) {
	t.Parallel()

	var (
		systemText         string
		summaryDescription string
	)

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, err := io.ReadAll(r.Body)
		if err != nil {
			t.Fatalf("read request body: %v", err)
		}
		var req struct {
			Input []struct {
				Content []struct {
					Text string `json:"text"`
				} `json:"content"`
			} `json:"input"`
			Text struct {
				Format struct {
					Schema struct {
						Properties struct {
							Summary struct {
								Description string `json:"description"`
							} `json:"summary"`
						} `json:"properties"`
					} `json:"schema"`
				} `json:"format"`
			} `json:"text"`
		}
		if err := json.Unmarshal(body, &req); err != nil {
			t.Fatalf("decode request body: %v", err)
		}
		if len(req.Input) == 0 || len(req.Input[0].Content) == 0 {
			t.Fatalf("unexpected request structure: %s", string(body))
		}
		systemText = req.Input[0].Content[0].Text
		summaryDescription = req.Text.Format.Schema.Properties.Summary.Description

		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{
			"status":"completed",
			"model":"gpt-5-mini-2026-03-01",
			"output":[
				{
					"type":"message",
					"role":"assistant",
					"content":[
						{
							"type":"output_text",
							"text":"{\"category\":\"in_progress\",\"summary\":\"Actively working on the runtime cleanup.\"}"
						}
					]
				}
			]
		}`))
	}))
	defer server.Close()

	client := &OpenAIClient{
		apiKey:   "test-key",
		model:    "gpt-5-mini",
		endpoint: server.URL,
		httpClient: &http.Client{
			Timeout: 5 * time.Second,
		},
	}

	_, err := client.Classify(context.Background(), SessionSnapshot{
		ProjectPath: "/tmp/demo",
		SessionID:   "ses_demo",
		Transcript: []TranscriptItem{
			{Role: "user", Text: "Please summarize the current state."},
			{Role: "assistant", Text: "I am still working through the runtime cleanup."},
		},
	})
	if err != nil {
		t.Fatalf("classify: %v", err)
	}
	if !strings.Contains(systemText, `Omit leading scaffolding like "Assistant is" or "The assistant is".`) {
		t.Fatalf("system prompt = %q, want prefix-free summary guidance", systemText)
	}
	if !strings.Contains(systemText, "Return exactly one JSON object that matches the response schema.") {
		t.Fatalf("system prompt = %q, want explicit JSON-only guidance", systemText)
	}
	if !strings.Contains(systemText, "Do not include any prose before or after the JSON object.") {
		t.Fatalf("system prompt = %q, want no-prose JSON-only guidance", systemText)
	}
	if !strings.Contains(systemText, "Write from the implicit assistant point of view rather than naming the assistant as the subject.") {
		t.Fatalf("system prompt = %q, want implicit point-of-view guidance", systemText)
	}
	if !strings.Contains(systemText, "Do not force a stock opener; choose the most direct wording that fits the evidence.") {
		t.Fatalf("system prompt = %q, want anti-template guidance", systemText)
	}
	if !strings.Contains(systemText, "Treat optional follow-up offers like") {
		t.Fatalf("system prompt = %q, want optional-follow-up guidance", systemText)
	}
	if !strings.Contains(systemText, "If the latest assistant message asks the user to choose between options, confirm a proposed plan, approve a next step, or answer a direct implementation question, prefer waiting_for_user over completed.") {
		t.Fatalf("system prompt = %q, want explicit proposal-handoff guidance", systemText)
	}
	if !strings.Contains(systemText, "Proposal handoffs count as waiting_for_user when the next meaningful action depends on the user's choice, even if the assistant includes a recommendation like “I’d go with 2”.") {
		t.Fatalf("system prompt = %q, want option-selection recommendation guidance", systemText)
	}
	if !strings.Contains(systemText, "Use completed only when the assistant can stop without a reply from the user; if the assistant is clearly waiting for the user's answer before proceeding, do not mark completed.") {
		t.Fatalf("system prompt = %q, want completed-vs-waiting guidance", systemText)
	}
	if !strings.Contains(systemText, "Reasoning/tool transcript items can reflect earlier planning; when they conflict with a later user-visible assistant message, trust the latest user-visible assistant message.") {
		t.Fatalf("system prompt = %q, want latest-visible-message guidance", systemText)
	}
	if !strings.Contains(systemText, "If the latest assistant message says requested repo actions already happened") {
		t.Fatalf("system prompt = %q, want completed-repo-actions guidance", systemText)
	}
	if !strings.Contains(summaryDescription, "brief fragments are fine") {
		t.Fatalf("summary schema description = %q, want brief-fragment guidance", summaryDescription)
	}
	if !strings.Contains(summaryDescription, "implicit assistant point of view") {
		t.Fatalf("summary schema description = %q, want implicit point-of-view guidance", summaryDescription)
	}
	if !strings.Contains(summaryDescription, "omit prefixes like 'Assistant is'") {
		t.Fatalf("summary schema description = %q, want prefix omission guidance", summaryDescription)
	}
}

func TestOpenAIClientClassifyRetriesIncompleteWithFallback(t *testing.T) {
	t.Parallel()

	attempts := make([]struct {
		Effort string
	}, 0, 2)

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, err := io.ReadAll(r.Body)
		if err != nil {
			t.Fatalf("read request body: %v", err)
		}
		var req struct {
			Reasoning struct {
				Effort string `json:"effort"`
			} `json:"reasoning"`
			MaxOutputTokens *int `json:"max_output_tokens"`
		}
		if err := json.Unmarshal(body, &req); err != nil {
			t.Fatalf("decode request body: %v", err)
		}
		if req.MaxOutputTokens != nil {
			t.Fatalf("max_output_tokens = %v, want omitted field", *req.MaxOutputTokens)
		}
		attempts = append(attempts, struct {
			Effort string
		}{
			Effort: req.Reasoning.Effort,
		})

		w.Header().Set("Content-Type", "application/json")
		if len(attempts) == 1 {
			_, _ = w.Write([]byte(`{
				"status":"incomplete",
				"model":"gpt-5-mini-2026-03-01",
				"max_output_tokens":1024,
				"incomplete_details":{"reason":"max_output_tokens"},
				"usage":{
					"input_tokens":288,
					"input_tokens_details":{"cached_tokens":0},
					"output_tokens":1024,
					"output_tokens_details":{"reasoning_tokens":1009},
					"total_tokens":1312
				},
				"output":[]
			}`))
			return
		}

		_, _ = w.Write([]byte(`{
			"status":"completed",
			"model":"gpt-5-mini-2026-03-01",
			"usage":{
				"input_tokens":288,
				"input_tokens_details":{"cached_tokens":0},
				"output_tokens":63,
				"output_tokens_details":{"reasoning_tokens":8},
				"total_tokens":351
			},
			"output":[
				{
					"type":"message",
					"role":"assistant",
					"content":[
						{
							"type":"output_text",
							"text":"{\"category\":\"needs_follow_up\",\"summary\":\"One more repo step remains.\"}"
						}
					]
				}
			]
		}`))
	}))
	defer server.Close()

	client := &OpenAIClient{
		apiKey:   "test-key",
		model:    "gpt-5-mini",
		endpoint: server.URL,
		httpClient: &http.Client{
			Timeout: 5 * time.Second,
		},
	}

	result, err := client.Classify(context.Background(), SessionSnapshot{
		ProjectPath: "/tmp/demo",
		SessionID:   "ses_demo",
		Transcript: []TranscriptItem{
			{Role: "user", Text: "Please verify whether this is finished."},
			{Role: "assistant", Text: "I still need to run one last step."},
		},
	})
	if err != nil {
		t.Fatalf("classify: %v", err)
	}
	if result.Category != model.SessionCategoryNeedsFollowUp {
		t.Fatalf("category = %s, want needs_follow_up", result.Category)
	}
	if len(attempts) != 2 {
		t.Fatalf("attempts = %d, want 2", len(attempts))
	}
	if attempts[0].Effort != classifierPrimaryReasoningEffort {
		t.Fatalf("first attempt = %+v, want primary classifier settings", attempts[0])
	}
	if attempts[1].Effort != classifierFallbackReasoningEffort {
		t.Fatalf("second attempt = %+v, want fallback classifier settings", attempts[1])
	}
}

func TestOpenAIClientClassifyRetriesTransientServerError(t *testing.T) {
	t.Parallel()

	var attempts int
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		attempts++
		if attempts == 1 {
			w.Header().Set("Content-Type", "application/json")
			w.Header().Set("Retry-After", "0")
			w.WriteHeader(http.StatusInternalServerError)
			_, _ = w.Write([]byte(`{"error":{"message":"temporary upstream failure"}}`))
			return
		}

		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{
			"status":"completed",
			"model":"gpt-5-mini-2026-03-01",
			"output":[
				{
					"type":"message",
					"role":"assistant",
					"content":[
						{
							"type":"output_text",
							"text":"{\"category\":\"completed\",\"summary\":\"Everything is wrapped up.\"}"
						}
					]
				}
			]
		}`))
	}))
	defer server.Close()

	client := &OpenAIClient{
		apiKey:   "test-key",
		model:    "gpt-5-mini",
		endpoint: server.URL,
		httpClient: &http.Client{
			Timeout: 5 * time.Second,
		},
	}

	result, err := client.Classify(context.Background(), SessionSnapshot{
		ProjectPath: "/tmp/demo",
		SessionID:   "ses_demo",
		Transcript: []TranscriptItem{
			{Role: "user", Text: "Please verify whether this is finished."},
			{Role: "assistant", Text: "Yes, the requested work is complete."},
		},
	})
	if err != nil {
		t.Fatalf("classify: %v", err)
	}
	if result.Category != model.SessionCategoryCompleted {
		t.Fatalf("category = %s, want completed", result.Category)
	}
	if attempts != 2 {
		t.Fatalf("attempts = %d, want 2", attempts)
	}
}

func TestOpenAIClientClassifyReportsIncompleteReason(t *testing.T) {
	t.Parallel()

	var attempts int
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		attempts++
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{
			"status":"incomplete",
			"model":"gpt-5-mini-2026-03-01",
			"max_output_tokens":320,
			"incomplete_details":{"reason":"max_output_tokens"},
			"usage":{
				"input_tokens":288,
				"input_tokens_details":{"cached_tokens":0},
				"output_tokens":320,
				"output_tokens_details":{"reasoning_tokens":309},
				"total_tokens":608
			},
			"output":[]
		}`))
	}))
	defer server.Close()

	client := &OpenAIClient{
		apiKey:   "test-key",
		model:    "gpt-5-mini",
		endpoint: server.URL,
		httpClient: &http.Client{
			Timeout: 5 * time.Second,
		},
	}

	_, err := client.Classify(context.Background(), SessionSnapshot{
		ProjectPath: "/tmp/demo",
		SessionID:   "ses_demo",
		Transcript: []TranscriptItem{
			{Role: "user", Text: "Please verify whether this is finished."},
			{Role: "assistant", Text: "I still need to run one last step."},
		},
	})
	if err == nil {
		t.Fatalf("expected classify to fail after both attempts")
	}
	if !strings.Contains(err.Error(), "reason=max_output_tokens") {
		t.Fatalf("error = %q, want incomplete reason", err.Error())
	}
	if !strings.Contains(err.Error(), "reasoning_tokens=309") {
		t.Fatalf("error = %q, want reasoning token detail", err.Error())
	}
	if attempts != 2 {
		t.Fatalf("attempts = %d, want 2", attempts)
	}
}

func TestOpenAIClientClassifyRetriesTransientTransportError(t *testing.T) {
	t.Parallel()

	attempts := make([]string, 0, len(classifierAttemptPlan))
	client := &OpenAIClient{
		apiKey:   "test-key",
		model:    "gpt-5-mini",
		endpoint: "https://api.openai.com/v1/responses",
		httpClient: &http.Client{
			Timeout: 5 * time.Second,
			Transport: roundTripperFunc(func(req *http.Request) (*http.Response, error) {
				body, err := io.ReadAll(req.Body)
				if err != nil {
					t.Fatalf("read request body: %v", err)
				}
				var payload struct {
					Reasoning struct {
						Effort string `json:"effort"`
					} `json:"reasoning"`
				}
				if err := json.Unmarshal(body, &payload); err != nil {
					t.Fatalf("decode request body: %v", err)
				}
				attempts = append(attempts, payload.Reasoning.Effort)

				if len(attempts) == 1 {
					return nil, &os.SyscallError{Syscall: "read", Err: syscall.EADDRNOTAVAIL}
				}

				return &http.Response{
					StatusCode: http.StatusOK,
					Status:     "200 OK",
					Header:     make(http.Header),
					Body: io.NopCloser(strings.NewReader(`{
						"status":"completed",
						"model":"gpt-5-mini-2026-03-01",
						"output":[
							{
								"type":"message",
								"role":"assistant",
								"content":[
									{
									"type":"output_text",
										"text":"{\"category\":\"completed\",\"summary\":\"Everything is wrapped up.\"}"
									}
								]
							}
						]
					}`)),
				}, nil
			}),
		},
	}

	result, err := client.Classify(context.Background(), SessionSnapshot{
		ProjectPath: "/tmp/demo",
		SessionID:   "ses_demo",
		Transcript: []TranscriptItem{
			{Role: "user", Text: "Please verify whether this is finished."},
			{Role: "assistant", Text: "Yes, the requested work is complete."},
		},
	})
	if err != nil {
		t.Fatalf("classify: %v", err)
	}
	if result.Category != model.SessionCategoryCompleted {
		t.Fatalf("category = %s, want completed", result.Category)
	}
	if len(attempts) != 2 {
		t.Fatalf("attempts = %d, want 2", len(attempts))
	}
	if attempts[0] != classifierPrimaryReasoningEffort {
		t.Fatalf("first attempt effort = %q, want %q", attempts[0], classifierPrimaryReasoningEffort)
	}
	if attempts[1] != classifierFallbackReasoningEffort {
		t.Fatalf("second attempt effort = %q, want %q", attempts[1], classifierFallbackReasoningEffort)
	}
}

func TestOpenAIClientClassifyRetriesCodexStreamDisconnect(t *testing.T) {
	t.Parallel()

	attempts := make([]string, 0, len(classifierAttemptPlan))
	client := &OpenAIClient{
		model: "gpt-5.4-mini",
		responses: fakeJSONSchemaRunner{
			run: func(_ context.Context, req llm.JSONSchemaRequest) (llm.JSONSchemaResponse, error) {
				attempts = append(attempts, req.ReasoningEffort)
				if len(attempts) == 1 {
					return llm.JSONSchemaResponse{}, errors.New("stream disconnected before completion: error sending request for url (https://chatgpt.com/backend-api/codex/responses)")
				}
				return llm.JSONSchemaResponse{
					Status:     "completed",
					Model:      "gpt-5.4-mini",
					OutputText: `{"category":"completed","summary":"Everything is wrapped up."}`,
				}, nil
			},
		},
	}

	result, err := client.Classify(context.Background(), SessionSnapshot{
		ProjectPath: "/tmp/demo",
		SessionID:   "ses_demo",
		Transcript: []TranscriptItem{
			{Role: "user", Text: "Please verify whether this is finished."},
			{Role: "assistant", Text: "Yes, the requested work is complete."},
		},
	})
	if err != nil {
		t.Fatalf("classify: %v", err)
	}
	if result.Category != model.SessionCategoryCompleted {
		t.Fatalf("category = %s, want completed", result.Category)
	}
	if len(attempts) != 2 {
		t.Fatalf("attempts = %d, want 2", len(attempts))
	}
	if attempts[0] != classifierPrimaryReasoningEffort {
		t.Fatalf("first attempt effort = %q, want %q", attempts[0], classifierPrimaryReasoningEffort)
	}
	if attempts[1] != classifierFallbackReasoningEffort {
		t.Fatalf("second attempt effort = %q, want %q", attempts[1], classifierFallbackReasoningEffort)
	}
}

func TestOpenAIClientClassifyTransportRetriesRemainBounded(t *testing.T) {
	t.Parallel()

	var attempts int
	client := &OpenAIClient{
		apiKey:   "test-key",
		model:    "gpt-5-mini",
		endpoint: "https://api.openai.com/v1/responses",
		httpClient: &http.Client{
			Timeout: 5 * time.Second,
			Transport: roundTripperFunc(func(req *http.Request) (*http.Response, error) {
				attempts++
				return nil, &os.SyscallError{Syscall: "read", Err: syscall.EADDRNOTAVAIL}
			}),
		},
	}

	_, err := client.Classify(context.Background(), SessionSnapshot{
		ProjectPath: "/tmp/demo",
		SessionID:   "ses_demo",
		Transcript: []TranscriptItem{
			{Role: "user", Text: "Please verify whether this is finished."},
			{Role: "assistant", Text: "Yes, the requested work is complete."},
		},
	})
	if err == nil {
		t.Fatalf("expected classify to fail after bounded retries")
	}
	if attempts != classifierTransientRetryAttempts {
		t.Fatalf("attempts = %d, want %d", attempts, classifierTransientRetryAttempts)
	}
	if !strings.Contains(err.Error(), "can't assign requested address") {
		t.Fatalf("error = %q, want transport detail", err.Error())
	}
}

func TestClassificationFailureMetadataTreatsCodexStreamDisconnectAsConnectionFailure(t *testing.T) {
	t.Parallel()

	kind, diagnosis := classificationFailureMetadata(errors.New("stream disconnected before completion: error sending request for url (https://chatgpt.com/backend-api/codex/responses)"))
	if kind != classificationFailureKindConnectionFailed {
		t.Fatalf("kind = %q, want %q", kind, classificationFailureKindConnectionFailed)
	}
	if diagnosis == "" {
		t.Fatalf("expected connection failure diagnosis")
	}
}

func TestDecodeClassifierOutput(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name  string
		input string
	}{
		{
			name:  "plain json",
			input: `{"category":"completed","summary":"done"}`,
		},
		{
			name:  "json fenced output",
			input: "```json\n{\"category\":\"completed\",\"summary\":\"done\"}\n```",
		},
		{
			name:  "json embedded in prose",
			input: "Here is the assessment:\n{\"category\":\"completed\",\"summary\":\"done\"}",
		},
		{
			name:  "json with trailing prose",
			input: "{\"category\":\"completed\",\"summary\":\"done\"}\nThat is the result.",
		},
		{
			name:  "json embedded after reasoning text with braces",
			input: "Reasoning: keep {notes} private.\n{\"category\":\"completed\",\"summary\":\"done\"}\nTrailing note.",
		},
		{
			name:  "thinking block before json",
			input: "<think>\nNeed to classify carefully.\n</think>\n{\"category\":\"completed\",\"summary\":\"done\"}",
		},
		{
			name:  "thinking block before fenced json",
			input: "<think>\nNeed to classify carefully.\n</think>\n```json\n{\"category\":\"completed\",\"summary\":\"done\"}\n```",
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			var result Result
			if err := decodeClassifierOutput(tc.input, &result); err != nil {
				t.Fatalf("decodeClassifierOutput() error = %v", err)
			}
			if result.Category != model.SessionCategoryCompleted {
				t.Fatalf("category = %q, want completed", result.Category)
			}
			if result.Summary != "done" {
				t.Fatalf("summary = %q, want done", result.Summary)
			}
			if result.Confidence != 0 {
				t.Fatalf("confidence = %v, want 0 default", result.Confidence)
			}
		})
	}
}

func TestDecodeClassifierOutputIncludesPreviewOnFailure(t *testing.T) {
	t.Parallel()

	var result Result
	err := decodeClassifierOutput("classification=completed", &result)
	if err == nil {
		t.Fatalf("expected decodeClassifierOutput() to fail")
	}
	if !strings.Contains(err.Error(), `preview="classification=completed"`) {
		t.Fatalf("error = %q, want preview", err.Error())
	}
	if !strings.Contains(err.Error(), "invalid character") {
		t.Fatalf("error = %q, want JSON parse cause", err.Error())
	}
}

func TestStripMarkdownCodeBlock(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name  string
		input string
		want  string
	}{
		{
			name:  "plain json",
			input: `{"category": "completed", "summary": "done"}`,
			want:  `{"category": "completed", "summary": "done"}`,
		},
		{
			name:  "json in code block",
			input: "```json\n{\"category\": \"completed\", \"summary\": \"done\"}\n```",
			want:  `{"category": "completed", "summary": "done"}`,
		},
		{
			name:  "json in code block without language",
			input: "```\n{\"category\": \"completed\", \"summary\": \"done\"}\n```",
			want:  `{"category": "completed", "summary": "done"}`,
		},
		{
			name:  "json in code block with extra whitespace",
			input: "```json\n  {\"category\": \"completed\", \"summary\": \"done\"}  \n```",
			want:  `{"category": "completed", "summary": "done"}`,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := llm.StripMarkdownCodeBlock(tt.input)
			if got != tt.want {
				t.Errorf("StripMarkdownCodeBlock() = %q, want %q", got, tt.want)
			}
		})
	}
}

func TestStripThinkingBlocks(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name  string
		input string
		want  string
	}{
		{
			name:  "plain json",
			input: `{"category": "completed", "summary": "done"}`,
			want:  `{"category": "completed", "summary": "done"}`,
		},
		{
			name:  "leading thinking block",
			input: "<think>\ninternal reasoning\n</think>\n{\"category\": \"completed\", \"summary\": \"done\"}",
			want:  `{"category": "completed", "summary": "done"}`,
		},
		{
			name:  "multiple leading thinking blocks",
			input: "<think>\nfirst\n</think>\n<think>\nsecond\n</think>\n{\"category\": \"completed\", \"summary\": \"done\"}",
			want:  `{"category": "completed", "summary": "done"}`,
		},
		{
			name:  "missing closing tag leaves text alone",
			input: "<think>\ninternal reasoning\n{\"category\": \"completed\", \"summary\": \"done\"}",
			want:  "<think>\ninternal reasoning\n{\"category\": \"completed\", \"summary\": \"done\"}",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := llm.StripThinkingBlocks(tt.input)
			if got != tt.want {
				t.Errorf("StripThinkingBlocks() = %q, want %q", got, tt.want)
			}
		})
	}
}
