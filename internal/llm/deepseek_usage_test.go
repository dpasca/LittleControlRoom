package llm

import (
	"context"
	"fmt"
	"math"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"lcroom/internal/model"
)

func TestChatCompletionsDeepSeekCostIncludesCacheReads(t *testing.T) {
	t.Parallel()

	for _, mode := range []string{"structured", "text", "stream"} {
		t.Run(mode, func(t *testing.T) {
			for _, cacheFields := range []string{
				`"prompt_cache_hit_tokens":900000`,
				`"prompt_cache_hit_tokens":900000,"prompt_tokens_details":{"cached_tokens":900000}`,
			} {
				usageJSON := fmt.Sprintf(`{"prompt_tokens":1000000,"completion_tokens":10000,"completion_tokens_details":{"reasoning_tokens":500},"total_tokens":1010000,%s}`, cacheFields)
				server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
					if mode == "stream" {
						w.Header().Set("Content-Type", "text/event-stream")
						fmt.Fprint(w, "data: {\"model\":\"deepseek-flash\",\"choices\":[{\"delta\":{\"content\":\"Done.\"},\"finish_reason\":\"stop\"}]}\n\n")
						fmt.Fprintf(w, "data: {\"model\":\"deepseek-flash\",\"choices\":[],\"usage\":%s}\n\ndata: [DONE]\n\n", usageJSON)
						return
					}
					w.Header().Set("Content-Type", "application/json")
					fmt.Fprintf(w, `{"model":"deepseek-flash","choices":[{"message":{"content":"{\"ok\":true}"},"finish_reason":"stop"}],"usage":%s}`, usageJSON)
				}))
				t.Cleanup(server.Close)

				tracker := NewUsageTracker()
				var usage model.LLMUsage
				var err error
				if mode == "structured" {
					client := NewOpenAICompatibleChatCompletionsClientWithBaseURL("test-key", server.URL, time.Second, tracker)
					resp, callErr := client.RunJSONSchema(context.Background(), JSONSchemaRequest{
						Model:      "deepseek-flash",
						UserText:   "Assess the session.",
						SchemaName: "result",
						Schema:     map[string]any{"type": "object"},
					})
					usage, err = resp.Usage, callErr
				} else {
					client := NewOpenAICompatibleChatTextClientWithBaseURL("test-key", server.URL, time.Second, tracker)
					req := TextRequest{Model: "deepseek-flash", Messages: []TextMessage{{Role: "user", Content: "Assess the session."}}}
					var resp TextResponse
					if mode == "stream" {
						resp, err = client.RunTextStream(context.Background(), req, func(TextStreamEvent) error { return nil })
					} else {
						resp, err = client.RunText(context.Background(), req)
					}
					usage = resp.Usage
				}
				if err != nil {
					t.Fatal(err)
				}
				if usage.CachedInputTokens != 900_000 || usage.ReasoningTokens != 500 || math.Abs(usage.EstimatedCostUSD-0.0474) > 1e-12 {
					t.Fatalf("DeepSeek usage = %#v, want 900000 cached tokens and $0.0474", usage)
				}
				snapshot := tracker.Snapshot(true)
				if snapshot.Model != "deepseek-flash" || snapshot.Completed != 1 || snapshot.Totals != usage {
					t.Fatalf("usage tracker did not preserve the priced response: %#v", snapshot)
				}
			}
		})
	}
}
