package modeladapter

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// Exercise both wire protocols, including providers whose models use the same
// Chat Completions adapter. Continuation must preserve current instructions and
// the shared schema's optional search filters, regardless of model selection.
func TestProviderToolContinuationContract(t *testing.T) {
	providers := []struct {
		name      string
		newClient func(OpenRouterConfig) (*Client, error)
		responses bool
	}{
		{"openai", NewOpenAIClient, true},
		{"deepseek", NewDeepSeekClient, false},
		{"openrouter", NewOpenRouterClient, false},
		{"moonshot", NewMoonshotClient, false},
		{"xiaomi", NewXiaomiClient, false},
		{"ollama", NewOllamaClient, false},
		{"mlx", NewMLXClient, false},
	}
	for _, provider := range providers {
		t.Run(provider.name, func(t *testing.T) {
			requests := 0
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				requests++
				var req struct {
					Instructions string           `json:"instructions"`
					PreviousID   string           `json:"previous_response_id"`
					Input        []map[string]any `json:"input"`
					Messages     []Message        `json:"messages"`
					Tools        []map[string]any `json:"tools"`
				}
				if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
					t.Fatal(err)
				}
				wantSystem := fmt.Sprintf("system instructions %d", requests)
				wantDeveloper := fmt.Sprintf("developer instructions %d", requests)
				if provider.responses {
					if r.URL.Path != "/responses" || req.Instructions != wantSystem+"\n\n"+wantDeveloper {
						t.Fatalf("request %d: path=%q instructions=%q", requests, r.URL.Path, req.Instructions)
					}
					if requests == 2 && (req.PreviousID != "resp_1" || len(req.Input) != 1 || req.Input[0]["call_id"] != "call_1") {
						t.Fatalf("tool continuation lost: previous=%q input=%#v", req.PreviousID, req.Input)
					}
				} else {
					if r.URL.Path != "/chat/completions" || req.PreviousID != "" || len(req.Messages) < 3 {
						t.Fatalf("request %d: path=%q messages=%#v", requests, r.URL.Path, req.Messages)
					}
					if req.Messages[0].Role != "system" || req.Messages[0].Content != wantSystem || req.Messages[1].Role != "developer" || req.Messages[1].Content != wantDeveloper {
						t.Fatalf("request %d lost current instructions: %#v", requests, req.Messages)
					}
					if requests == 2 && (len(req.Messages) != 5 || req.Messages[4].ToolCallID != "call_1") {
						t.Fatalf("tool continuation lost: %#v", req.Messages)
					}
				}
				foundSearch := false
				for _, tool := range req.Tools {
					function := tool
					if provider.responses {
						if strict, ok := tool["strict"].(bool); !ok || strict {
							t.Fatalf("Responses must explicitly preserve non-strict optional parameters: %#v", tool)
						}
					} else {
						function = tool["function"].(map[string]any)
					}
					if function["name"] != "web_search" {
						continue
					}
					foundSearch = true
					params := function["parameters"].(map[string]any)
					required := params["required"].([]any)
					if len(required) != 1 || required[0] != "query" {
						t.Fatalf("optional filters became required: %#v", required)
					}
					properties := params["properties"].(map[string]any)
					recency := properties["recency_days"].(map[string]any)
					if recency["minimum"] != float64(0) || !strings.Contains(recency["description"].(string), "0") {
						t.Fatalf("search has no explicit unrestricted date filter: %#v", recency)
					}
				}
				if !foundSearch {
					t.Fatal("missing web search tool")
				}
				if provider.responses {
					_, _ = w.Write([]byte(`{"id":"resp_1","status":"completed","output":[{"type":"function_call","call_id":"call_1","name":"web_search","arguments":"{\"query\":\"historical magazine\"}"}]}`))
				} else {
					_, _ = w.Write([]byte(`{"choices":[{"message":{"role":"assistant","tool_calls":[{"id":"call_1","type":"function","function":{"name":"web_search","arguments":"{\"query\":\"historical magazine\"}"}}]}}]}`))
				}
			}))
			defer server.Close()
			client, err := provider.newClient(OpenRouterConfig{APIKey: "test-key", BaseURL: server.URL, Model: "contract-model"})
			if err != nil {
				t.Fatal(err)
			}
			messages := []Message{
				{Role: "system", Content: "system instructions 1"},
				{Role: "developer", Content: "developer instructions 1"},
				{Role: "user", Content: "Find a historical magazine issue."},
			}
			defs := ToolsWithOptions(ToolOptions{WebSearchEnabled: true})
			first, err := client.Complete(context.Background(), messages, defs)
			if err != nil {
				t.Fatal(err)
			}
			messages[0].Content = "system instructions 2"
			messages[1].Content = "developer instructions 2"
			messages = append(messages, first.Message, Message{Role: "tool", ToolCallID: "call_1", Content: `{"success":true,"output":"No results."}`})
			if _, err := client.Complete(context.Background(), messages, defs); err != nil {
				t.Fatal(err)
			}
			if requests != 2 {
				t.Fatalf("requests=%d, want 2", requests)
			}
		})
	}
}
