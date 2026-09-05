package integrations

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"reflect"
	"strings"
	"testing"
	"time"
)

func TestHTTPProbeNegotiatesSessionAndNeverCallsTools(t *testing.T) {
	t.Setenv("LCR_TEST_INTEGRATION_TOKEN", "test-secret")
	methods := make(chan string, 8)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "test-secret" {
			t.Error("missing header environment reference")
		}
		if r.Method == http.MethodDelete {
			methods <- "DELETE"
			if r.Header.Get("Mcp-Session-Id") != "test-session" {
				t.Error("missing cleanup session")
			}
			w.WriteHeader(http.StatusNoContent)
			return
		}
		var request struct {
			ID     int    `json:"id"`
			Method string `json:"method"`
		}
		if err := json.NewDecoder(r.Body).Decode(&request); err != nil {
			t.Error(err)
			w.WriteHeader(400)
			return
		}
		methods <- request.Method
		if request.Method != "initialize" && (r.Header.Get("Mcp-Session-Id") != "test-session" || r.Header.Get("MCP-Protocol-Version") != "2025-03-26") {
			t.Error("negotiated session/protocol not retained")
		}
		switch request.Method {
		case "initialize":
			w.Header().Set("Mcp-Session-Id", "test-session")
			w.Header().Set("Content-Type", "application/json")
			fmt.Fprintf(w, `{"jsonrpc":"2.0","id":%d,"result":{"protocolVersion":"2025-03-26","capabilities":{"tools":{}}}}`, request.ID)
		case "notifications/initialized":
			w.WriteHeader(http.StatusAccepted)
		case "tools/list":
			w.Header().Set("Content-Type", "text/event-stream")
			fmt.Fprintf(w, "event: message\ndata: {\"jsonrpc\":\"2.0\",\"id\":%d,\"result\":{\"tools\":[{\"name\":\"example\"}]}}\n\n", request.ID)
		default:
			t.Errorf("unexpected MCP method: %s", request.Method)
			w.WriteHeader(400)
		}
	}))
	defer server.Close()
	names, err := probeHTTP(t.Context(), map[string]any{"url": server.URL, "env_http_headers": map[string]any{"Authorization": "LCR_TEST_INTEGRATION_TOKEN"}})
	if err != nil || !reflect.DeepEqual(names, []string{"example"}) {
		t.Fatalf("probe: %v %v", names, err)
	}
	for _, expected := range []string{"initialize", "notifications/initialized", "tools/list", "DELETE"} {
		select {
		case method := <-methods:
			if method != expected {
				t.Fatalf("method %s, want %s", method, expected)
			}
		default:
			t.Fatalf("missing method %s", expected)
		}
	}
}

func TestHTTPProbeAuthAndRedirectDoNotLeakDetails(t *testing.T) {
	for _, code := range []int{401, 403, 302, 500} {
		t.Run(fmt.Sprint(code), func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				w.Header().Set("Location", "https://example.invalid/secret")
				w.WriteHeader(code)
				fmt.Fprint(w, "do-not-disclose")
			}))
			defer server.Close()
			_, err := probeHTTP(t.Context(), map[string]any{"url": server.URL})
			if err == nil || strings.Contains(err.Error(), "do-not-disclose") || strings.Contains(err.Error(), "secret") {
				t.Fatalf("unsafe/missing error: %v", err)
			}
			if (code == 401 || code == 403) != errors.Is(err, errAuthenticationRequired) {
				t.Fatalf("incorrect auth classification: %v", err)
			}
		})
	}
}

func TestStdioProbeProcess(t *testing.T) {
	if os.Getenv("LCR_TEST_MCP_HELPER") == "1" {
		decoder := json.NewDecoder(bufio.NewReader(os.Stdin))
		encoder := json.NewEncoder(os.Stdout)
		for {
			var request struct {
				ID     int    `json:"id"`
				Method string `json:"method"`
			}
			if err := decoder.Decode(&request); err != nil {
				os.Exit(0)
			}
			switch request.Method {
			case "initialize":
				_ = encoder.Encode(map[string]any{"id": request.ID, "result": map[string]any{"protocolVersion": "2025-06-18"}})
			case "notifications/initialized":
			case "tools/list":
				_ = encoder.Encode(map[string]any{"id": request.ID, "result": map[string]any{"tools": []map[string]any{{"name": "example"}}}})
			default:
				os.Exit(2)
			}
		}
	}
	ctx, cancel := context.WithTimeout(t.Context(), 5*time.Second)
	defer cancel()
	names, err := probeStdio(ctx, map[string]any{"command": []string{os.Args[0], "-test.run=^TestStdioProbeProcess$"}, "env": map[string]any{"LCR_TEST_MCP_HELPER": "1"}}, t.TempDir())
	if err != nil || !reflect.DeepEqual(names, []string{"example"}) {
		t.Fatalf("stdio probe: %v %v", names, err)
	}
}

func TestProbeMissingEnvironmentAndBoundedNames(t *testing.T) {
	t.Setenv("LCR_TEST_MISSING_TOKEN", "")
	_, err := probeHTTP(t.Context(), map[string]any{"url": "https://example.invalid/mcp", "env_http_headers": map[string]any{"Authorization": "LCR_TEST_MISSING_TOKEN"}})
	if err == nil || !strings.Contains(err.Error(), "environment variable") {
		t.Fatalf("missing environment error: %v", err)
	}
	var entries []map[string]any
	for i := 0; i < 110; i++ {
		entries = append(entries, map[string]any{"name": fmt.Sprintf("tool_%03d", i)})
	}
	raw, _ := json.Marshal(map[string]any{"tools": entries})
	names, err := toolNames(raw)
	if err != nil || len(names) != 100 {
		t.Fatalf("unbounded tool names: %d %v", len(names), err)
	}
}
