package codexapp

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
)

func TestManagedBrowserTurnContextRequiresPlaywrightAndRuntimeMCP(t *testing.T) {
	tests := []struct {
		name       string
		playwright bool
		runtime    bool
		want       bool
	}{
		{name: "both available", playwright: true, runtime: true, want: true},
		{name: "playwright only", playwright: true, runtime: false},
		{name: "runtime only", playwright: false, runtime: true},
		{name: "neither available"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			session := &appServerSession{
				playwrightMCPExpected: tt.playwright,
				runtimeMCPExpected:    tt.runtime,
			}
			contextEntries := session.managedBrowserTurnContext()
			if got := len(contextEntries) > 0; got != tt.want {
				t.Fatalf("managedBrowserTurnContext() present = %v, want %v", got, tt.want)
			}
			if !tt.want {
				return
			}
			assertManagedBrowserTurnContext(t, contextEntries)
		})
	}
}

func TestStartTurnWithInputAddsCurrentManagedBrowserContext(t *testing.T) {
	session := &appServerSession{
		playwrightMCPExpected: true,
		runtimeMCPExpected:    true,
		notify:                func() {},
	}
	session.rpcCallHook = func(_ context.Context, method string, raw any) (json.RawMessage, error) {
		if method != "turn/start" {
			t.Fatalf("method = %q, want turn/start", method)
		}
		params, ok := raw.(turnStartParams)
		if !ok {
			t.Fatalf("params type = %T, want turnStartParams", raw)
		}
		assertManagedBrowserTurnContext(t, params.AdditionalContext)
		if len(params.Input) != 1 || params.Input[0].Text != "continue the browser task" {
			t.Fatalf("turn input = %#v", params.Input)
		}
		return json.RawMessage(`{"turn":{"id":"turn_browser_context"}}`), nil
	}

	err := session.startTurnWithInput(
		context.Background(),
		"thread_resumed_with_old_skill",
		Submission{Text: "continue the browser task"},
		"",
		"",
		"gpt-5",
		"high",
	)
	if err != nil {
		t.Fatalf("startTurnWithInput() error = %v", err)
	}
}

func TestSteerTurnAddsCurrentManagedBrowserContext(t *testing.T) {
	session := &appServerSession{
		playwrightMCPExpected: true,
		runtimeMCPExpected:    true,
	}
	session.rpcCallHook = func(_ context.Context, method string, raw any) (json.RawMessage, error) {
		if method != "turn/steer" {
			t.Fatalf("method = %q, want turn/steer", method)
		}
		params, ok := raw.(turnSteerParams)
		if !ok {
			t.Fatalf("params type = %T, want turnSteerParams", raw)
		}
		assertManagedBrowserTurnContext(t, params.AdditionalContext)
		return json.RawMessage(`{"turnId":"turn_browser_context"}`), nil
	}

	if _, err := session.steerTurn(
		context.Background(),
		"thread_resumed_with_old_skill",
		"turn_browser_context",
		Submission{Text: "the login is finished"},
	); err != nil {
		t.Fatalf("steerTurn() error = %v", err)
	}
}

func assertManagedBrowserTurnContext(t *testing.T, entries map[string]additionalContextEntry) {
	t.Helper()
	entry, ok := entries[managedBrowserContextSource]
	if !ok {
		t.Fatalf("additional context = %#v, want %q", entries, managedBrowserContextSource)
	}
	if entry.Kind != applicationContextKind {
		t.Fatalf("additional context kind = %q, want %q", entry.Kind, applicationContextKind)
	}
	for _, required := range []string{
		"lcr_runtime/request_browser_attention",
		"attention dialog and Browser sidebar",
		"stop the turn immediately",
		"Do not poll, wait, or use another tool",
	} {
		if !strings.Contains(entry.Value, required) {
			t.Fatalf("additional context missing %q: %q", required, entry.Value)
		}
	}
}
