package gitops

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

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
			MaxOutputTokens *int `json:"max_output_tokens"`
		}
		if err := json.Unmarshal(body, &req); err != nil {
			t.Fatalf("decode request body: %v", err)
		}
		if len(req.Input) < 2 || len(req.Input[1].Content) == 0 {
			t.Fatalf("unexpected request structure: %s", string(body))
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
			"model":"gpt-5-mini-2026-03-07",
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

	client := &OpenAICommitMessageClient{
		apiKey:   "test-key",
		model:    "gpt-5-mini",
		endpoint: server.URL,
		httpClient: &http.Client{
			Timeout: 5 * time.Second,
		},
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
	if suggestion.Model != "gpt-5-mini-2026-03-07" {
		t.Fatalf("model = %q, want response model", suggestion.Model)
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
			MaxOutputTokens *int `json:"max_output_tokens"`
		}
		if err := json.Unmarshal(body, &req); err != nil {
			t.Fatalf("decode request body: %v", err)
		}
		if len(req.Input) < 2 || len(req.Input[1].Content) == 0 {
			t.Fatalf("unexpected request structure: %s", string(body))
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
			"model":"gpt-5-mini-2026-03-07",
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

	client := &OpenAICommitMessageClient{
		apiKey:   "test-key",
		model:    "gpt-5-mini",
		endpoint: server.URL,
		httpClient: &http.Client{
			Timeout: 5 * time.Second,
		},
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
	if suggestion.Model != "gpt-5-mini-2026-03-07" {
		t.Fatalf("model = %q, want response model", suggestion.Model)
	}
}
