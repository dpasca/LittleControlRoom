package lcagent

import (
	"bytes"
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"

	"lcroom/internal/lcagent/modeladapter"
)

type contextCatalogStub struct {
	models []modeladapter.ListedModel
	err    error
}

func (s contextCatalogStub) ListModels(context.Context) ([]modeladapter.ListedModel, error) {
	return s.models, s.err
}

func TestCatalogContextWindowsOverrideGuessesAndFollowFinalModel(t *testing.T) {
	windows, err := loadModelContextWindows(t.Context(), contextCatalogStub{models: []modeladapter.ListedModel{
		{ID: "new/model", ContextLength: 32768},
		{ID: "final/model", ContextLength: 1000000},
		{ID: "bad/model", ContextLength: -1},
		{ID: "overflow/model", ContextLength: 1 << 62},
	}})
	if err != nil {
		t.Fatal(err)
	}
	opts := defaultOpenRouterContextOptions()
	opts.ModelContextWindows = windows
	main := contextOptionsForModel(opts, "openrouter", "new/model")
	if main.ModelContextWindowTokens != 32768 || main.LoopCompactionTokenBudget != 32768*85/100 {
		t.Fatalf("main budget=%#v", main)
	}
	final := contextOptionsForModel(main, "openrouter", "final/model")
	if final.ModelContextWindowTokens != 1000000 || final.LoopCompactionTokenBudget != 700000 {
		t.Fatalf("final budget=%#v", final)
	}
	unknown := contextOptionsForModel(main, "openrouter", "missing/model")
	if unknown.ModelContextWindowTokens != defaultHostedModelWindowTokens {
		t.Fatalf("fallback budget=%#v", unknown)
	}
	if len(windows) != 2 {
		t.Fatalf("invalid capacities accepted: %#v", windows)
	}
	if windows, err := loadModelContextWindows(t.Context(), contextCatalogStub{err: errors.New("offline")}); err == nil || windows != nil {
		t.Fatalf("failure=%#v, %v", windows, err)
	}
}

// Existing conversation fixtures exercise chat requests, independently of the
// optional startup catalog. Return an unavailable catalog without consuming a
// scripted completion or incrementing its request count.
func newModelCatalogTestServer(handler http.Handler) *httptest.Server {
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/models" {
			http.NotFound(w, r)
			return
		}
		handler.ServeHTTP(w, r)
	}))
}

func TestRunExecUsesCatalogCapacityOnce(t *testing.T) {
	isolateSkillHomes(t)
	var requests atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		if r.URL.Path == "/models" {
			requests.Add(1)
			_, _ = w.Write([]byte(`{"data":[{"id":"catalog/model","context_length":80000}]}`))
			return
		}
		_, _ = w.Write([]byte(`{"id":"answer","model":"catalog/model","choices":[{"finish_reason":"stop","message":{"role":"assistant","content":"done"}}]}`))
	}))
	defer server.Close()
	t.Setenv("OPENROUTER_API_KEY", "test-key")
	t.Setenv("OPENROUTER_BASE_URL", server.URL)
	var stdout, stderr bytes.Buffer
	code := Run([]string{"exec", "--cwd", t.TempDir(), "--data-dir", t.TempDir(), "--auto", "off", "--output", "stream-json", "--provider", "openrouter", "--model", "catalog/model", "--max-turns", "2", "answer directly"}, &stdout, &stderr)
	if code != 0 {
		t.Fatalf("exit=%d stderr=%s", code, stderr.String())
	}
	if requests.Load() != 1 {
		t.Fatalf("catalog requests=%d", requests.Load())
	}
	if !strings.Contains(stdout.String(), `"compaction_token_budget":68000`) || !strings.Contains(stdout.String(), `"context_window":80000`) {
		t.Fatalf("catalog budget missing: %s", stdout.String())
	}
}
