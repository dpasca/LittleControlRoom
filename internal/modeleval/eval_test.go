package modeleval

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"lcroom/internal/config"
)

func TestRunOllamaModelEval(t *testing.T) {
	t.Parallel()

	var generateRequests []map[string]any
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch r.URL.Path {
		case "/v1/models":
			_, _ = w.Write([]byte(`{"data":[{"id":"gemma4:12b-mlx"}]}`))
		case "/api/show":
			_, _ = w.Write([]byte(`{
				"details":{"parameter_size":"13.0B","quantization_level":"nvfp4"},
				"model_info":{"gemma4_unified.context_length":131072,"general.architecture":"gemma4_unified"}
			}`))
		case "/api/generate":
			var req map[string]any
			if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
				t.Fatalf("decode request: %v", err)
			}
			generateRequests = append(generateRequests, req)
			response := `Little Control Room can use this model for summaries.`
			if _, structured := req["format"]; structured {
				switch len(generateRequests) {
				case 2:
					response = `{"summary":"Gemma can summarize local model testing.","category":"completed","confidence":0.88}`
				case 3:
					response = `{"summary":"Concrete demo milestone should be implemented next.","category":"needs_follow_up","confidence":0.84}`
				default:
					response = `{"message":"Add Ollama model eval diagnostics"}`
				}
			}
			_, _ = w.Write([]byte(`{
				"model":"gemma4:12b-mlx",
				"response":` + strconvQuote(response) + `,
				"done":true,
				"done_reason":"stop",
				"prompt_eval_count":24,
				"eval_count":12
			}`))
		default:
			t.Fatalf("unexpected path %q", r.URL.Path)
		}
	}))
	defer server.Close()

	report, err := Run(context.Background(), Options{
		Backend: config.AIBackendOllama,
		BaseURL: server.URL + "/v1",
		Model:   "gemma4:12b-mlx",
		Timeout: time.Second,
	})
	if err != nil {
		t.Fatalf("Run() error = %v", err)
	}
	if !report.Passed() {
		t.Fatalf("report should pass: %+v", report)
	}
	if report.ContextWindow != 131072 {
		t.Fatalf("ContextWindow = %d, want 131072", report.ContextWindow)
	}
	if len(report.Cases) != 4 {
		t.Fatalf("cases = %d, want 4", len(report.Cases))
	}
	if !reportContainsCase(report, "session_assessment_advice_followup_json") {
		t.Fatalf("report missing advice-follow-up case: %+v", report.Cases)
	}
	for _, req := range generateRequests {
		if req["think"] != false {
			t.Fatalf("ollama request think = %#v, want false", req["think"])
		}
	}
}

func strconvQuote(value string) string {
	raw, err := json.Marshal(value)
	if err != nil {
		panic(err)
	}
	return string(raw)
}

func reportContainsCase(report Report, name string) bool {
	for _, c := range report.Cases {
		if c.Name == name {
			return true
		}
	}
	return false
}

func TestReportMarshalJSONIncludesPassState(t *testing.T) {
	t.Parallel()

	report := Report{
		Backend:  "ollama",
		Model:    "gemma4:12b-mlx",
		Duration: 1200 * time.Millisecond,
		Cases: []CaseResult{
			{Name: "plain_text_generation", Passed: true},
		},
	}
	raw, err := json.Marshal(report)
	if err != nil {
		t.Fatalf("MarshalJSON() error = %v", err)
	}
	text := string(raw)
	if !strings.Contains(text, `"passed":true`) || !strings.Contains(text, `"duration_ms":1200`) {
		t.Fatalf("json report missing pass/duration: %s", text)
	}
}
