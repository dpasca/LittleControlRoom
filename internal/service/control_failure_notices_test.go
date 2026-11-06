package service

import (
	"encoding/json"
	"errors"
	"strings"
	"testing"

	"lcroom/internal/control"
	"lcroom/internal/events"
	"lcroom/internal/model"
	"lcroom/internal/store"
)

func TestWaitingControlFailureReceiptsSurviveRestartAndReachExactCaller(t *testing.T) {
	for _, provider := range []model.SessionSource{model.SessionSourceCodex, model.SessionSourceOpenCode, model.SessionSourceClaudeCode, model.SessionSourceLCAgent} {
		t.Run(string(provider), func(t *testing.T) {
			ctx := t.Context()
			svc, st := newEngineerDispatchTestService(t)
			args, _ := json.Marshal(control.AgentTaskContinueInput{TaskID: "copy-worker", SessionMode: control.SessionModeResumeOrNew, Prompt: "Continue on existing edits"})
			op, err := st.CreateControlOperation(ctx, control.Operation{
				ID: "lcrop_waiting_failure", Source: string(provider), Provider: string(provider),
				SessionKey: dispatchCallerKey, ProjectPath: dispatchCallerPath, ResumeOnSuccess: true,
				Invocation: control.Invocation{Capability: control.CapabilityAgentTaskContinue, Args: args},
			})
			if err != nil {
				t.Fatal(err)
			}
			// The asynchronous operation can fail after an auto-approved caller
			// has ended its turn, even before its provider binding is recorded.
			for range 2 {
				if _, err := st.UpdateControlOperationStatus(ctx, op.ID, control.OperationFailed, nil, errors.New("repository write preflight requires a clean checkout")); err != nil {
					t.Fatal(err)
				}
			}
			if err := st.Close(); err != nil {
				t.Fatal(err)
			}
			st, err = store.Open(svc.cfg.DBPath)
			if err != nil {
				t.Fatal(err)
			}
			defer st.Close()
			svc = New(svc.cfg, st, events.NewBus(), nil)
			if err := st.BindEngineerSession(ctx, dispatchCallerPath, provider, "unrelated-key", "replacement-caller"); err != nil {
				t.Fatal(err)
			}
			if err := svc.QueuePendingEngineerDispatchReplies(ctx, 32); err != nil {
				t.Fatal(err)
			}
			queued, err := st.ListQueuedEngineerMessages(ctx, 32)
			if err != nil || len(queued) != 0 {
				t.Fatalf("failure receipt guessed a recipient: %+v, %v", queued, err)
			}
			if err := st.BindEngineerSession(ctx, dispatchCallerPath, provider, dispatchCallerKey, "exact-caller"); err != nil {
				t.Fatal(err)
			}
			for range 2 {
				if err := svc.QueuePendingEngineerDispatchReplies(ctx, 32); err != nil {
					t.Fatal(err)
				}
			}
			queued, err = st.ListQueuedEngineerMessages(ctx, 32)
			if err != nil || len(queued) != 1 {
				t.Fatalf("lost or duplicate receipt: %+v, %v", queued, err)
			}
			receipt := queued[0]
			if receipt.TargetSessionID != "exact-caller" || receipt.FollowCurrentSession || receipt.Provider != control.Provider(provider) ||
				!strings.Contains(receipt.Prompt, op.ID) || !strings.Contains(receipt.Prompt, "clean checkout") || !strings.Contains(receipt.Prompt, "do not retry") {
				t.Fatalf("incorrect failure receipt: %+v", receipt)
			}
			if _, err := st.RecordEngineerMessageState(ctx, receipt.ID, control.EngineerMessageFailed, "", "caller unavailable", errors.New("caller unavailable")); err != nil {
				t.Fatal(err)
			}
			ready, err := st.ListReadyEngineerDispatches(ctx, 32)
			if err != nil || len(ready) != 0 {
				t.Fatalf("receipt failure created a callback loop: %+v, %v", ready, err)
			}
		})
	}
}

func TestControlFailureReceiptRequiresAnExplicitWait(t *testing.T) {
	for _, scenario := range []string{"no wait", "canceled", "success", "callback"} {
		t.Run(scenario, func(t *testing.T) {
			ctx := t.Context()
			svc, st := newEngineerDispatchTestService(t)
			id := "lcrop_wait"
			if scenario == "callback" {
				id = "engineer-dispatch-reply:1:1"
			}
			args, _ := json.Marshal(control.TodoAddInput{ProjectPath: dispatchCallerPath, Text: "Follow up"})
			_, err := st.CreateControlOperation(ctx, control.Operation{
				ID: id, Source: "lcagent", Provider: "lcagent", SessionKey: dispatchCallerKey,
				ProjectPath: dispatchCallerPath, ResumeOnSuccess: scenario != "no wait",
				Invocation: control.Invocation{Capability: control.CapabilityTodoAdd, Args: args},
			})
			if err != nil {
				t.Fatal(err)
			}
			if err := st.BindEngineerSession(ctx, dispatchCallerPath, model.SessionSourceLCAgent, dispatchCallerKey, "caller"); err != nil {
				t.Fatal(err)
			}
			status := control.OperationFailed
			if scenario == "canceled" {
				status = control.OperationCanceled
			}
			if scenario == "success" {
				status = control.OperationCompleted
			}
			if _, err := st.UpdateControlOperationStatus(ctx, id, status, nil, nil); err != nil {
				t.Fatal(err)
			}
			if err := svc.QueuePendingEngineerDispatchReplies(ctx, 32); err != nil {
				t.Fatal(err)
			}
			queued, err := st.ListQueuedEngineerMessages(ctx, 32)
			if err != nil || len(queued) != 0 {
				t.Fatalf("unexpected failure receipt: %+v, %v", queued, err)
			}
		})
	}
}
