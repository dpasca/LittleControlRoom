package codexapp

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
)

func TestContinueInterruptedCodexTurnInterruptsStaleTurnThenStartsContinuation(t *testing.T) {
	calls := []string{}
	readCalls := 0
	s := &appServerSession{
		projectPath:  "/tmp/demo",
		threadID:     "thread-demo",
		started:      true,
		busy:         true,
		busyExternal: true,
		activeTurnID: "turn-old",
		entryIndex:   make(map[string]int),
		notify:       func() {},
		rpcCallHook: func(_ context.Context, method string, params any) (json.RawMessage, error) {
			calls = append(calls, method)
			switch method {
			case "thread/read":
				readCalls++
				if readCalls == 1 {
					return json.RawMessage(`{"thread":{"id":"thread-demo","status":{"type":"active"},"turns":[{"id":"turn-old","status":"inProgress"}]}}`), nil
				}
				return json.RawMessage(`{"thread":{"id":"thread-demo","status":{"type":"idle"},"turns":[{"id":"turn-old","status":"interrupted"}]}}`), nil
			case "turn/interrupt":
				request := params.(turnInterruptParams)
				if request.ThreadID != "thread-demo" || request.TurnID != "turn-old" {
					t.Fatalf("interrupt request = %#v", request)
				}
				return json.RawMessage(`{}`), nil
			case "turn/start":
				request := params.(turnStartParams)
				if request.ThreadID != "thread-demo" || len(request.Input) != 1 || request.Input[0].Text != "continue safely" {
					t.Fatalf("turn start request = %#v", request)
				}
				return json.RawMessage(`{"turn":{"id":"turn-new"}}`), nil
			default:
				t.Fatalf("unexpected RPC method %q", method)
				return nil, nil
			}
		},
	}

	if err := s.continueInterruptedTurn("turn-old", Submission{Text: "continue safely"}); err != nil {
		t.Fatalf("continueInterruptedTurn() error = %v", err)
	}
	if got := strings.Join(calls, ","); got != "thread/read,turn/interrupt,thread/read,turn/start" {
		t.Fatalf("RPC calls = %q", got)
	}
	snapshot := s.Snapshot()
	if !snapshot.Busy || snapshot.BusyExternal || snapshot.ActiveTurnID != "turn-new" {
		t.Fatalf("continued snapshot = %+v", snapshot)
	}
}

func TestContinueInterruptedCodexTurnDoesNotDuplicateCompletedTurn(t *testing.T) {
	calls := []string{}
	s := &appServerSession{
		projectPath: "/tmp/demo",
		threadID:    "thread-demo",
		started:     true,
		entryIndex:  make(map[string]int),
		notify:      func() {},
		rpcCallHook: func(_ context.Context, method string, params any) (json.RawMessage, error) {
			calls = append(calls, method)
			if method != "thread/read" {
				t.Fatalf("unexpected RPC method %q", method)
			}
			return json.RawMessage(`{"thread":{"id":"thread-demo","status":{"type":"idle"},"turns":[{"id":"turn-old","status":"completed"}]}}`), nil
		},
	}

	if err := s.continueInterruptedTurn("turn-old", Submission{Text: "continue safely"}); err != nil {
		t.Fatalf("continueInterruptedTurn() error = %v", err)
	}
	if got := strings.Join(calls, ","); got != "thread/read" {
		t.Fatalf("RPC calls = %q, want only thread/read", got)
	}
	if snapshot := s.Snapshot(); snapshot.Busy {
		t.Fatalf("completed captured turn should remain idle: %+v", snapshot)
	}
}
