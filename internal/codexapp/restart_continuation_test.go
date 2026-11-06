package codexapp

import (
	"context"
	"encoding/json"
	"fmt"
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
	if got := strings.Join(calls, ","); got != "thread/read,turn/interrupt,thread/read,thread/read,turn/start" {
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

func TestContinueInterruptedCodexTurnStartsForLatestInterruptedTurn(t *testing.T) {
	calls := []string{}
	s := &appServerSession{
		threadID:   "thread-demo",
		started:    true,
		entryIndex: make(map[string]int),
		notify:     func() {},
		rpcCallHook: func(_ context.Context, method string, _ any) (json.RawMessage, error) {
			calls = append(calls, method)
			switch method {
			case "thread/read":
				return json.RawMessage(`{"thread":{"id":"thread-demo","turns":[{"id":"old","status":"interrupted"}]}}`), nil
			case "turn/start":
				return json.RawMessage(`{"turn":{"id":"new"}}`), nil
			default:
				t.Fatalf("unexpected RPC method %q", method)
				return nil, nil
			}
		},
	}
	if err := s.continueInterruptedTurn("old", Submission{Text: "continue"}); err != nil {
		t.Fatal(err)
	}
	if got := strings.Join(calls, ","); got != "thread/read,turn/start" {
		t.Fatalf("RPC calls = %q", got)
	}
}

func TestContinueInterruptedCodexTurnRejectsStaleOrUnverifiableIntent(t *testing.T) {
	for _, tc := range []struct {
		name       string
		capturedID string
		turns      []resumedTurn
		wantError  bool
	}{
		{"newer completed", "old", []resumedTurn{{ID: "old", Status: "interrupted"}, {ID: "new", Status: "completed"}}, false},
		{"newer active", "old", []resumedTurn{{ID: "old", Status: "interrupted"}, {ID: "new", Status: "inProgress"}}, true},
		{"newer interrupted", "old", []resumedTurn{{ID: "old", Status: "interrupted"}, {ID: "new", Status: "interrupted"}}, true},
		{"newer failed", "old", []resumedTurn{{ID: "old", Status: "interrupted"}, {ID: "new", Status: "failed"}}, true},
		{"completed captured with newer interrupted", "old", []resumedTurn{{ID: "old", Status: "completed"}, {ID: "new", Status: "interrupted"}}, true},
		{"missing captured", "missing", []resumedTurn{{ID: "new", Status: "completed"}}, true},
		{"empty captured", "", []resumedTurn{{ID: "old", Status: "interrupted"}}, true},
		{"empty history", "old", nil, true},
		{"unknown status", "old", []resumedTurn{{ID: "old", Status: "unknown"}}, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			s := &appServerSession{
				threadID:   "thread-demo",
				started:    true,
				entryIndex: make(map[string]int),
				notify:     func() {},
				rpcCallHook: func(_ context.Context, method string, _ any) (json.RawMessage, error) {
					if method != "thread/read" {
						t.Fatalf("unexpected side effect: %s", method)
					}
					return json.Marshal(threadReadResponse{Thread: resumedThread{ID: "thread-demo", Turns: tc.turns}})
				},
			}
			if err := s.continueInterruptedTurn(tc.capturedID, Submission{Text: "continue"}); (err != nil) != tc.wantError {
				t.Fatalf("continuation error = %v, wantError = %v", err, tc.wantError)
			}
		})
	}
}

func TestContinueInterruptedCodexTurnRevalidatesAfterInterrupt(t *testing.T) {
	for _, interruptFails := range []bool{false, true} {
		for _, tc := range []struct {
			name   string
			status string
			newer  bool
		}{
			{"captured completed", "completed", false},
			{"newer completed", "completed", true},
			{"newer active", "inProgress", true},
			{"newer interrupted", "interrupted", true},
			{"newer failed", "failed", true},
		} {
			t.Run(fmt.Sprintf("interruptFails=%v/%s", interruptFails, tc.name), func(t *testing.T) {
				reads := 0
				interrupts := 0
				s := &appServerSession{
					threadID:   "thread-demo",
					started:    true,
					entryIndex: make(map[string]int),
					notify:     func() {},
					rpcCallHook: func(_ context.Context, method string, _ any) (json.RawMessage, error) {
						switch method {
						case "thread/read":
							reads++
							turns := []resumedTurn{{ID: "old", Status: "inProgress"}}
							if reads > 1 {
								turns[0].Status = "interrupted"
								// The wait helper sees idle; the final re-read must
								// still catch a new turn racing with that check.
								if interruptFails || reads > 2 {
									if tc.newer {
										turns = append(turns, resumedTurn{ID: "new", Status: tc.status})
									} else {
										turns[0].Status = tc.status
									}
								}
							}
							return json.Marshal(threadReadResponse{Thread: resumedThread{ID: "thread-demo", Turns: turns}})
						case "turn/interrupt":
							interrupts++
							if interruptFails {
								return nil, fmt.Errorf("turn already finished")
							}
							return json.RawMessage(`{}`), nil
						default:
							t.Fatalf("unexpected side effect: %s", method)
							return nil, nil
						}
					},
				}
				err := s.continueInterruptedTurn("old", Submission{Text: "continue"})
				if (err != nil) != (tc.status != "completed") {
					t.Fatalf("continuation error = %v for status %s", err, tc.status)
				}
				if interrupts != 1 {
					t.Fatalf("interrupts = %d, want 1", interrupts)
				}
			})
		}
	}
}

func TestContinueInterruptedCodexTurnRestoresCompleteHistoryInChronologicalOrder(t *testing.T) {
	turns := make([]resumedTurn, 0, 13)
	for i := 1; i <= 13; i++ {
		status := "completed"
		if i == 13 {
			status = "inProgress"
		}
		turns = append(turns, resumedTurn{
			ID:     fmt.Sprintf("turn-%02d", i),
			Status: status,
			Items: []map[string]json.RawMessage{{
				"id":      json.RawMessage(fmt.Sprintf(`"user-%02d"`, i)),
				"type":    json.RawMessage(`"userMessage"`),
				"content": json.RawMessage(fmt.Sprintf(`[{"type":"text","text":"request %02d"}]`, i)),
			}},
		})
	}

	recent := resumedThread{
		ID:                "thread-demo",
		Status:            resumedThreadStatus{Type: "active"},
		Turns:             append([]resumedTurn(nil), turns[5:]...),
		HistoryTruncated:  true,
		HistoryNextCursor: "older-turns",
	}
	s := &appServerSession{
		projectPath: "/tmp/demo",
		threadID:    "thread-demo",
		started:     true,
		entryIndex:  make(map[string]int),
		notify:      func() {},
	}
	s.initializeHistoryPagination(recent)
	s.hydrateResumedThread(recent)
	readCalls := 0
	s.rpcCallHook = func(_ context.Context, method string, params any) (json.RawMessage, error) {
		switch method {
		case "thread/read":
			_ = params.(threadReadParams)
			readCalls++
			thread := resumedThread{
				ID:     "thread-demo",
				Status: resumedThreadStatus{Type: "active"},
				Turns:  append([]resumedTurn(nil), turns...),
			}
			if readCalls > 1 {
				thread.Status = resumedThreadStatus{Type: "idle"}
				thread.Turns[len(thread.Turns)-1].Status = "interrupted"
			}
			response, err := json.Marshal(threadReadResponse{Thread: thread})
			if err != nil {
				t.Fatalf("marshal thread/read response: %v", err)
			}
			return response, nil
		case "turn/interrupt":
			return json.RawMessage(`{}`), nil
		case "turn/start":
			return json.RawMessage(`{"turn":{"id":"turn-new"}}`), nil
		default:
			t.Fatalf("unexpected RPC method %q", method)
			return nil, nil
		}
	}

	if err := s.continueInterruptedTurn("turn-13", Submission{Text: "continue safely"}); err != nil {
		t.Fatalf("continueInterruptedTurn() error = %v", err)
	}

	snapshot := s.Snapshot()
	if snapshot.HistoryHasMore {
		t.Fatal("complete interrupted-turn refresh should settle older-history pagination")
	}
	want := 1
	for _, entry := range snapshot.Entries {
		if entry.Kind != TranscriptUser || !strings.HasPrefix(entry.Text, "request ") {
			continue
		}
		wantText := fmt.Sprintf("request %02d", want)
		if entry.Text != wantText {
			t.Fatalf("restored request %d = %q, want %q; entries = %#v", want, entry.Text, wantText, snapshot.Entries)
		}
		want++
	}
	if want != 14 {
		t.Fatalf("restored %d requests, want 13; entries = %#v", want-1, snapshot.Entries)
	}
}
