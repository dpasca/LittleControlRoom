package codexapp

import (
	"context"
	"encoding/json"
	"fmt"
	"testing"
	"time"
)

func TestInterruptWhileFollowUpSubmissionIsPending(t *testing.T) {
	steering := make(chan struct{})
	releaseSteer := make(chan struct{})
	interrupted := make(chan turnInterruptParams, 1)
	s := &appServerSession{
		threadID: "thread-live", activeTurnID: "turn-live", busy: true,
		entryIndex: make(map[string]int), notify: func() {},
		rpcCallHook: func(ctx context.Context, method string, params any) (json.RawMessage, error) {
			switch method {
			case "turn/steer":
				close(steering)
				select {
				case <-releaseSteer:
					return json.RawMessage(`{"turnId":"turn-live"}`), nil
				case <-ctx.Done():
					return nil, ctx.Err()
				}
			case "turn/interrupt":
				interrupted <- params.(turnInterruptParams)
				return json.RawMessage(`{}`), nil
			default:
				return nil, fmt.Errorf("unexpected RPC %s", method)
			}
		},
	}
	finished := make(chan struct{})
	defer func() {
		close(releaseSteer)
		select {
		case <-finished:
		case <-time.After(5 * time.Second):
			t.Error("follow-up did not finish after release")
		}
	}()
	submitted := make(chan error, 1)
	go func() {
		defer close(finished)
		submitted <- s.Submit("follow-up")
	}()
	select {
	case <-steering:
	case err := <-submitted:
		t.Fatalf("submission returned before steering: %v", err)
	case <-time.After(5 * time.Second):
		t.Fatal("follow-up did not reach the provider")
	}
	stopped := make(chan error, 1)
	go func() { stopped <- s.Interrupt() }()
	select {
	case err := <-stopped:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("interrupt waited for the pending follow-up")
	}
	if request := <-interrupted; request.ThreadID != "thread-live" || request.TurnID != "turn-live" {
		t.Fatalf("wrong interrupt target: %+v", request)
	}
}
