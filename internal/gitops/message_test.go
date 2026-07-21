package gitops

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strings"
	"testing"
	"time"

	"lcroom/internal/llm"
)

type scriptedJSONSchemaRunner struct {
	requests []llm.JSONSchemaRequest
	results  []scriptedJSONSchemaResult
}

type scriptedJSONSchemaResult struct {
	response llm.JSONSchemaResponse
	err      error
}

func (r *scriptedJSONSchemaRunner) RunJSONSchema(_ context.Context, req llm.JSONSchemaRequest) (llm.JSONSchemaResponse, error) {
	r.requests = append(r.requests, req)
	if len(r.results) == 0 {
		return llm.JSONSchemaResponse{}, errors.New("unexpected RunJSONSchema call")
	}
	result := r.results[0]
	r.results = r.results[1:]
	return result.response, result.err
}

func TestOpenAICommitMessageClientSuggest(t *testing.T) {
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
		if req.Reasoning.Effort != "low" {
			t.Fatalf("reasoning effort = %q, want %q", req.Reasoning.Effort, "low")
		}
		if req.MaxOutputTokens != nil {
			t.Fatalf("max_output_tokens = %v, want omitted field", *req.MaxOutputTokens)
		}

		userText := req.Input[1].Content[0].Text
		prefix := "Draft a git commit subject for this coding task snapshot:\n\n"
		if !strings.HasPrefix(userText, prefix) {
			t.Fatalf("unexpected commit prompt: %q", userText)
		}
		var input CommitMessageInput
		if err := json.Unmarshal([]byte(strings.TrimPrefix(userText, prefix)), &input); err != nil {
			t.Fatalf("decode embedded input: %v", err)
		}
		if input.StageMode != "staged_only" {
			t.Fatalf("stage mode = %q, want staged_only", input.StageMode)
		}
		if !strings.Contains(input.DiffStat, "README.md") {
			t.Fatalf("diff stat = %q, want README.md", input.DiffStat)
		}

		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{
			"status":"completed",
			"model":"gpt-5.4-mini-2026-03-17",
			"usage":{
				"input_tokens":1200,
				"input_tokens_details":{"cached_tokens":100},
				"output_tokens":12,
				"output_tokens_details":{"reasoning_tokens":3},
				"total_tokens":1212
			},
			"output":[
				{
					"type":"message",
					"role":"assistant",
					"content":[
						{
							"type":"output_text",
							"text":"{\"message\":\"Improve command palette scrolling\"}"
						}
					]
				}
			]
		}`))
	}))
	defer server.Close()

	usageTracker := llm.NewUsageTracker()
	client := &OpenAICommitMessageClient{
		apiKey:   "test-key",
		model:    "gpt-5.4-mini",
		endpoint: server.URL,
		httpClient: &http.Client{
			Timeout: 5 * time.Second,
		},
		responses: llm.NewResponsesClientWithHTTPClient("test-key", server.URL, &http.Client{
			Timeout: 5 * time.Second,
		}, usageTracker),
	}

	suggestion, err := client.Suggest(context.Background(), CommitMessageInput{
		Intent:        "commit",
		ProjectName:   "Little Control Room",
		Branch:        "master",
		StageMode:     "staged_only",
		IncludedFiles: []string{"README.md"},
		DiffStat:      " README.md | 3 ++-\n 1 file changed, 2 insertions(+), 1 deletion(-)",
	})
	if err != nil {
		t.Fatalf("suggest: %v", err)
	}
	if suggestion.Message != "Improve command palette scrolling" {
		t.Fatalf("message = %q, want expected subject", suggestion.Message)
	}
	if suggestion.Model != "gpt-5.4-mini-2026-03-17" {
		t.Fatalf("model = %q, want response model", suggestion.Model)
	}
	usage := usageTracker.Snapshot(true)
	if usage.Completed != 1 || usage.Failed != 0 {
		t.Fatalf("usage counters = %+v, want one successful tracked call", usage)
	}
	if usage.Totals.EstimatedCostUSD <= 0 {
		t.Fatalf("usage estimated cost = %f, want positive commit cost", usage.Totals.EstimatedCostUSD)
	}
}

func TestDecodeJSONObjectOutput(t *testing.T) {
	t.Parallel()

	type commitPayload struct {
		Message string `json:"message"`
	}

	tests := []struct {
		name string
		text string
	}{
		{
			name: "plain json",
			text: `{"message":"Improve commit message parsing"}`,
		},
		{
			name: "json fenced output",
			text: "```json\n{\"message\":\"Improve commit message parsing\"}\n```",
		},
		{
			name: "fenced output with suffix",
			text: "```json\n{\"message\":\"Improve commit message parsing\"}\n```\n",
		},
		{
			name: "json embedded in prose",
			text: "Here is the payload:\n{\"message\":\"Improve commit message parsing\"}",
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			var decoded commitPayload
			if err := llm.DecodeJSONObjectOutput(tc.text, &decoded); err != nil {
				t.Fatalf("DecodeJSONObjectOutput: %v", err)
			}
			if decoded.Message != "Improve commit message parsing" {
				t.Fatalf("message = %q, want recovered subject", decoded.Message)
			}
		})
	}
}

func TestOpenAICommitMessageClientRecommendUntracked(t *testing.T) {
	t.Parallel()

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			t.Fatalf("method = %s, want POST", r.Method)
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
		if req.Reasoning.Effort != "low" {
			t.Fatalf("reasoning effort = %q, want %q", req.Reasoning.Effort, "low")
		}
		if req.MaxOutputTokens != nil {
			t.Fatalf("max_output_tokens = %v, want omitted field", *req.MaxOutputTokens)
		}

		userText := req.Input[1].Content[0].Text
		prefix := "Review these untracked file candidates for a proposed git commit:\n\n"
		if !strings.HasPrefix(userText, prefix) {
			t.Fatalf("unexpected untracked prompt: %q", userText)
		}
		var input UntrackedFileRecommendationInput
		if err := json.Unmarshal([]byte(strings.TrimPrefix(userText, prefix)), &input); err != nil {
			t.Fatalf("decode embedded input: %v", err)
		}
		if len(input.Candidates) != 2 {
			t.Fatalf("candidate files = %d, want 2", len(input.Candidates))
		}
		if input.Candidates[0].Path != "notes.txt" {
			t.Fatalf("first candidate path = %q, want notes.txt", input.Candidates[0].Path)
		}
		if !strings.Contains(input.StagedDiffStat, "README.md") {
			t.Fatalf("staged diff stat = %q, want README.md", input.StagedDiffStat)
		}

		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{
			"status":"completed",
			"model":"gpt-5.4-mini-2026-03-17",
			"usage":{
				"input_tokens":1400,
				"input_tokens_details":{"cached_tokens":200},
				"output_tokens":24,
				"output_tokens_details":{"reasoning_tokens":5},
				"total_tokens":1424
			},
			"output":[
				{
					"type":"message",
					"role":"assistant",
					"content":[
						{
							"type":"output_text",
							"text":"{\"files\":[{\"path\":\"notes.txt\",\"include\":true,\"confidence\":0.93,\"reason\":\"notes.txt matches the staged work.\"},{\"path\":\"scratch.txt\",\"include\":false,\"confidence\":0.14,\"reason\":\"scratch.txt looks unrelated.\"}]}"
						}
					]
				}
			]
		}`))
	}))
	defer server.Close()

	usageTracker := llm.NewUsageTracker()
	client := &OpenAICommitMessageClient{
		apiKey:   "test-key",
		model:    "gpt-5.4-mini",
		endpoint: server.URL,
		httpClient: &http.Client{
			Timeout: 5 * time.Second,
		},
		responses: llm.NewResponsesClientWithHTTPClient("test-key", server.URL, &http.Client{
			Timeout: 5 * time.Second,
		}, usageTracker),
	}

	suggestion, err := client.RecommendUntracked(context.Background(), UntrackedFileRecommendationInput{
		ProjectName:    "Little Control Room",
		Branch:         "master",
		StagedFiles:    []string{"README.md"},
		StagedDiffStat: " README.md | 3 ++-\n 1 file changed, 2 insertions(+), 1 deletion(-)",
		Candidates: []UntrackedFileCandidate{
			{Path: "notes.txt", Kind: "file", ByteSize: 24, Preview: "Add notes for the new workflow."},
			{Path: "scratch.txt", Kind: "file", ByteSize: 18, Preview: "temporary reminder"},
		},
	})
	if err != nil {
		t.Fatalf("recommend untracked: %v", err)
	}
	if len(suggestion.Files) != 2 {
		t.Fatalf("files = %#v, want two model decisions", suggestion.Files)
	}
	if !suggestion.Files[0].Include || suggestion.Files[0].Path != "notes.txt" {
		t.Fatalf("first decision = %#v, want notes.txt included", suggestion.Files[0])
	}
	if suggestion.Files[1].Include || suggestion.Files[1].Path != "scratch.txt" {
		t.Fatalf("second decision = %#v, want scratch.txt excluded", suggestion.Files[1])
	}
	if suggestion.Model != "gpt-5.4-mini-2026-03-17" {
		t.Fatalf("model = %q, want response model", suggestion.Model)
	}
	usage := usageTracker.Snapshot(true)
	if usage.Completed != 1 || usage.Failed != 0 {
		t.Fatalf("usage counters = %+v, want one successful tracked call", usage)
	}
	if usage.Totals.EstimatedCostUSD <= 0 {
		t.Fatalf("usage estimated cost = %f, want positive untracked-review cost", usage.Totals.EstimatedCostUSD)
	}
}

func TestOpenAICommitMessageClientSuggestRetriesRetryableHTTPError(t *testing.T) {
	t.Parallel()

	runner := &scriptedJSONSchemaRunner{
		results: []scriptedJSONSchemaResult{
			{
				err: &llm.HTTPStatusError{
					StatusCode: http.StatusInternalServerError,
					Status:     "500 Internal Server Error",
					Body:       `{"error":"warmup"}`,
					RetryAfter: "0",
				},
			},
			{
				response: llm.JSONSchemaResponse{
					Status:     "completed",
					Model:      "gpt-5.4-mini",
					OutputText: `{"message":"Improve commit preview resilience"}`,
				},
			},
		},
	}

	client := &OpenAICommitMessageClient{
		model:     "gpt-5.4-mini",
		responses: runner,
	}

	suggestion, err := client.Suggest(context.Background(), CommitMessageInput{
		Intent:        "commit",
		ProjectName:   "Little Control Room",
		Branch:        "master",
		StageMode:     "staged_only",
		IncludedFiles: []string{"README.md"},
	})
	if err != nil {
		t.Fatalf("suggest: %v", err)
	}
	if suggestion.Message != "Improve commit preview resilience" {
		t.Fatalf("message = %q, want retried suggestion", suggestion.Message)
	}
	if len(runner.requests) != 2 {
		t.Fatalf("requests = %d, want retry", len(runner.requests))
	}
	if runner.requests[0].BypassCache || !runner.requests[1].BypassCache {
		t.Fatalf("retry cache bypass = first:%v second:%v, want false/true", runner.requests[0].BypassCache, runner.requests[1].BypassCache)
	}
}

func TestOpenAICommitMessageClientSuggestAcceptsCommitSubjectAlias(t *testing.T) {
	t.Parallel()

	runner := &scriptedJSONSchemaRunner{
		results: []scriptedJSONSchemaResult{
			{
				response: llm.JSONSchemaResponse{
					Status:     "completed",
					Model:      "gemma4:12b-mlx",
					OutputText: `{"commit_subject":"Add Ollama model eval diagnostics"}`,
				},
			},
		},
	}

	client := &OpenAICommitMessageClient{
		model:     "gemma4:12b-mlx",
		responses: runner,
	}

	suggestion, err := client.Suggest(context.Background(), CommitMessageInput{
		Intent:        "commit",
		ProjectName:   "Little Control Room",
		Branch:        "master",
		StageMode:     "staged_only",
		IncludedFiles: []string{"internal/llm/ollama.go"},
	})
	if err != nil {
		t.Fatalf("suggest: %v", err)
	}
	if suggestion.Message != "Add Ollama model eval diagnostics" {
		t.Fatalf("message = %q, want commit_subject alias", suggestion.Message)
	}
}

func TestOpenAICommitMessageClientSuggestRetriesEmptyMessage(t *testing.T) {
	t.Parallel()

	runner := &scriptedJSONSchemaRunner{
		results: []scriptedJSONSchemaResult{
			{
				response: llm.JSONSchemaResponse{
					Status:     "completed",
					Model:      "gpt-5.4-mini",
					OutputText: `{"message":""}`,
				},
			},
			{
				response: llm.JSONSchemaResponse{
					Status:     "completed",
					Model:      "gpt-5.4-mini",
					OutputText: `{"message":"Refresh LCAgent replay activity"}`,
				},
			},
		},
	}

	client := &OpenAICommitMessageClient{
		model:     "gpt-5.4-mini",
		responses: runner,
	}

	suggestion, err := client.Suggest(context.Background(), CommitMessageInput{
		Intent:        "commit",
		ProjectName:   "Little Control Room",
		Branch:        "master",
		StageMode:     "staged_only",
		IncludedFiles: []string{"internal/codexapp/lcagent_session.go"},
	})
	if err != nil {
		t.Fatalf("suggest: %v", err)
	}
	if suggestion.Message != "Refresh LCAgent replay activity" {
		t.Fatalf("message = %q, want retried suggestion", suggestion.Message)
	}
	if len(runner.requests) != 2 {
		t.Fatalf("requests = %d, want retry", len(runner.requests))
	}
	props, ok := runner.requests[0].Schema["properties"].(map[string]any)
	if !ok {
		t.Fatalf("schema properties = %#v", runner.requests[0].Schema["properties"])
	}
	messageSchema, ok := props["message"].(map[string]any)
	if !ok {
		t.Fatalf("message schema = %#v", props["message"])
	}
	if messageSchema["minLength"] != 1 {
		t.Fatalf("message minLength = %#v, want 1", messageSchema["minLength"])
	}
}

func TestOpenAICommitMessageClientSuggestAcceptsSubjectField(t *testing.T) {
	t.Parallel()

	runner := &scriptedJSONSchemaRunner{
		results: []scriptedJSONSchemaResult{
			{
				response: llm.JSONSchemaResponse{
					Status:     "completed",
					Model:      "mimo-v2.5",
					OutputText: `{"subject":"Enhance LCAgent model picker"}`,
				},
			},
		},
	}

	client := &OpenAICommitMessageClient{
		model:     "mimo-v2.5",
		responses: runner,
	}

	suggestion, err := client.Suggest(context.Background(), CommitMessageInput{
		Intent:        "commit",
		ProjectName:   "Little Control Room",
		Branch:        "master",
		StageMode:     "staged_only",
		IncludedFiles: []string{"internal/tui/settings_lcagent_model_picker.go"},
	})
	if err != nil {
		t.Fatalf("suggest: %v", err)
	}
	if suggestion.Message != "Enhance LCAgent model picker" {
		t.Fatalf("message = %q, want subject fallback", suggestion.Message)
	}
	if len(runner.requests) != 1 {
		t.Fatalf("requests = %d, want no retry", len(runner.requests))
	}
}

func TestOpenAICommitMessageClientSelectCommitTodoEvidenceUsesCommitModel(t *testing.T) {
	t.Parallel()

	runner := &scriptedJSONSchemaRunner{
		results: []scriptedJSONSchemaResult{{
			response: llm.JSONSchemaResponse{
				Status: "completed",
				Model:  "utility-model-actual",
				OutputText: `{"selected_evidence":[{` +
					`"todo_ids":[17],` +
					`"commit_hash":"abc123",` +
					`"files":["renderer/startup.go"],` +
					`"reason":"This commit changes startup rendering."` +
					`}]}`,
			},
		}},
	}

	client := &OpenAICommitMessageClient{
		model:     "commit-model",
		responses: runner,
	}

	selection, err := client.SelectCommitTodoEvidence(context.Background(), CommitTodoEvidenceSelectionInput{
		ProjectName:      "renderer",
		Branch:           "master",
		BaseHash:         "base123",
		HeadHash:         "abc123",
		BypassModelCache: true,
		OpenTodos: []CommitTodoRef{{
			ID:   17,
			Text: "Fix the startup black screen",
		}},
		Commits: []CommitTodoEvidenceCommit{{
			Hash:         "abc123",
			Parents:      []string{"base123"},
			Subject:      "Build Vulkan startup shaders",
			ChangedFiles: []string{"renderer/startup.go", "renderer/shaders.bin"},
		}},
	})
	if err != nil {
		t.Fatalf("SelectCommitTodoEvidence() error = %v", err)
	}
	if selection.Model != "utility-model-actual" || len(selection.Items) != 1 {
		t.Fatalf("selection = %#v", selection)
	}
	if got := selection.Items[0]; got.CommitHash != "abc123" || !reflect.DeepEqual(got.TodoIDs, []int64{17}) || !reflect.DeepEqual(got.Files, []string{"renderer/startup.go"}) {
		t.Fatalf("selection item = %#v", got)
	}
	if len(runner.requests) != 1 {
		t.Fatalf("requests = %d, want 1", len(runner.requests))
	}
	req := runner.requests[0]
	if req.Model != "commit-model" || req.SchemaName != "git_commit_todo_evidence_selection" {
		t.Fatalf("request model/schema = %q/%q", req.Model, req.SchemaName)
	}
	if !req.BypassCache {
		t.Fatal("evidence selection retry did not bypass the runner cache")
	}
	if !strings.Contains(req.SystemText, "Do not use keyword or regex matching") {
		t.Fatalf("selection instructions omitted semantic-selection guardrail: %q", req.SystemText)
	}
	properties, ok := req.Schema["properties"].(map[string]any)
	if !ok {
		t.Fatalf("schema properties = %#v", req.Schema["properties"])
	}
	selectedSchema, ok := properties["selected_evidence"].(map[string]any)
	if !ok {
		t.Fatalf("selected_evidence schema = %#v", properties["selected_evidence"])
	}
	itemSchema, ok := selectedSchema["items"].(map[string]any)
	if !ok {
		t.Fatalf("selection item schema = %#v", selectedSchema["items"])
	}
	itemProperties, ok := itemSchema["properties"].(map[string]any)
	if !ok {
		t.Fatalf("selection item properties = %#v", itemSchema["properties"])
	}
	hashSchema, ok := itemProperties["commit_hash"].(map[string]any)
	if !ok || !reflect.DeepEqual(hashSchema["enum"], []any{"abc123"}) {
		t.Fatalf("commit hash schema = %#v, want supplied hash enum", itemProperties["commit_hash"])
	}
}

func TestOpenAICommitMessageClientRecommendUntrackedRetriesRetryableHTTPError(t *testing.T) {
	t.Parallel()

	runner := &scriptedJSONSchemaRunner{
		results: []scriptedJSONSchemaResult{
			{
				err: &llm.HTTPStatusError{
					StatusCode: http.StatusTooManyRequests,
					Status:     "429 Too Many Requests",
					Body:       `{"error":"retry later"}`,
					RetryAfter: "0",
				},
			},
			{
				response: llm.JSONSchemaResponse{
					Status:     "completed",
					Model:      "gpt-5.4-mini",
					OutputText: `{"files":[{"path":"notes.txt","include":true,"confidence":0.9,"reason":"notes.txt matches the staged work."}]}`,
				},
			},
		},
	}

	client := &OpenAICommitMessageClient{
		model:     "gpt-5.4-mini",
		responses: runner,
	}

	suggestion, err := client.RecommendUntracked(context.Background(), UntrackedFileRecommendationInput{
		ProjectName: "Little Control Room",
		Branch:      "master",
		StagedFiles: []string{"README.md"},
		Candidates: []UntrackedFileCandidate{
			{Path: "notes.txt", Kind: "file"},
		},
	})
	if err != nil {
		t.Fatalf("recommend untracked: %v", err)
	}
	if len(suggestion.Files) != 1 || suggestion.Files[0].Path != "notes.txt" || !suggestion.Files[0].Include {
		t.Fatalf("files = %#v, want retried decision for notes.txt", suggestion.Files)
	}
	if len(runner.requests) != 2 {
		t.Fatalf("requests = %d, want retry", len(runner.requests))
	}
}
