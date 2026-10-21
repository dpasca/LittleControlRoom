package service

import (
	"errors"
	"strings"
	"testing"
	"time"

	"lcroom/internal/control"
	"lcroom/internal/events"
	"lcroom/internal/model"
	"lcroom/internal/store"
)

func TestEngineerDeliveryNoticesSurviveRestartAndNotifyExactCaller(t *testing.T) {
	for _, provider := range []model.SessionSource{model.SessionSourceCodex, model.SessionSourceOpenCode, model.SessionSourceClaudeCode, model.SessionSourceLCAgent} {
		for _, outcome := range []string{"failed", "expired", "waiting", "before queue"} {
			t.Run(string(provider)+"/"+outcome, func(t *testing.T) {
				svc, st := newEngineerDispatchTestService(t)
				ctx := t.Context()
				op := createDispatchTestOperation(t, st, "lcrop_delivery_notice", dispatchCallerPath, string(provider), dispatchCallerKey,
					control.CapabilityEngineerSendPrompt, control.EngineerSendPromptInput{ProjectPath: dispatchWorkerPath, Provider: control.ProviderCodex, SessionMode: control.SessionModeResumeOrNew, Prompt: "Implement the task"})
				request := control.EngineerMessage{OperationID: op.ID, ProjectPath: dispatchWorkerPath, Provider: control.ProviderCodex, SessionMode: control.SessionModeResumeOrNew, FollowCurrentSession: true, Prompt: "Implement the task"}
				if outcome == "expired" {
					request.CreatedAt = time.Now().Add(-25 * time.Hour)
				}
				var message control.EngineerMessage
				var err error
				if outcome != "before queue" {
					message, err = st.CreateEngineerMessage(ctx, request)
					if err != nil {
						t.Fatal(err)
					}
				}
				switch outcome {
				case "failed":
					_, err = st.RecordEngineerMessageState(ctx, message.ID, control.EngineerMessageFailed, "", "Delivery failed: replaced target", errors.New("replaced target"))
				case "expired":
					if _, claimed, claimErr := st.ClaimEngineerMessage(ctx, message.ID, "worker"); claimErr != nil || claimed {
						t.Fatalf("expired message claimed: %t %v", claimed, claimErr)
					}
					_, err = st.ExpireOverdueEngineerMessages(ctx, time.Now())
				case "waiting":
					for range 2 {
						_, err = st.RecordEngineerMessageWaiting(ctx, message.ID, "no-current-session", "Waiting for a current engineer session")
						if err != nil {
							t.Fatal(err)
						}
					}
				case "before queue":
					_, err = st.UpdateControlOperationStatus(ctx, op.ID, control.OperationFailed, nil, errors.New("destination unavailable"))
				}
				if err != nil {
					t.Fatal(err)
				}
				// The notice is durable even if the host dies before queueing it,
				// and the caller has not announced a provider identity yet.
				if err := st.Close(); err != nil {
					t.Fatal(err)
				}
				st, err = store.Open(svc.cfg.DBPath)
				if err != nil {
					t.Fatal(err)
				}
				t.Cleanup(func() { st.Close() })
				svc = New(svc.cfg, st, events.NewBus(), nil)
				if err := svc.QueuePendingEngineerDispatchReplies(ctx, 32); err != nil {
					t.Fatal(err)
				}
				if err := st.BindEngineerSession(ctx, dispatchCallerPath, provider, dispatchCallerKey, "exact-caller"); err != nil {
					t.Fatal(err)
				}
				for range 2 {
					if err := svc.QueuePendingEngineerDispatchReplies(ctx, 32); err != nil {
						t.Fatal(err)
					}
				}
				queued, err := st.ListQueuedEngineerMessages(ctx, 10)
				if err != nil {
					t.Fatal(err)
				}
				var notices []control.EngineerMessage
				for _, queued := range queued {
					if queued.ID != message.ID {
						notices = append(notices, queued)
					}
				}
				if len(notices) != 1 || notices[0].TargetSessionID != "exact-caller" || notices[0].FollowCurrentSession || notices[0].Provider != control.Provider(provider) {
					t.Fatalf("notice lost or duplicated: %#v", notices)
				}
				if outcome == "waiting" && !strings.Contains(notices[0].Prompt, "session_mode new") {
					t.Fatal("waiting notice omitted the offer to start a session")
				}
				// Callback failure must not create an endless return-message loop.
				if _, err := st.RecordEngineerMessageState(ctx, notices[0].ID, control.EngineerMessageFailed, "", "Caller was replaced", errors.New("session changed")); err != nil {
					t.Fatal(err)
				}
				ready, err := st.ListReadyEngineerDispatches(ctx, 32)
				if err != nil || len(ready) != 0 {
					t.Fatalf("recursive notices: %+v %v", ready, err)
				}
			})
		}
	}
}

func TestEngineerTurnReportDistinguishesEndingFromWaiting(t *testing.T) {
	for _, tc := range []struct {
		completion EngineerTurnCompletion
		want       string
	}{
		{EngineerTurnCompletion{Summary: "Implementation ready"}, "Turn state: ended."},
		{EngineerTurnCompletion{WaitingForInput: true}, "Turn state: waiting for input."},
		{EngineerTurnCompletion{Interrupted: true}, "Turn state: interrupted."},
		{EngineerTurnCompletion{Problem: "transport failure"}, "Turn state: failed."},
	} {
		if got := engineerDispatchReplyPrompt(model.EngineerDispatch{WorkerProjectPath: dispatchWorkerPath, WorkerProvider: model.SessionSourceCodex}, "worker", "task", tc.completion); !strings.Contains(got, tc.want) {
			t.Fatalf("report missing %q: %s", tc.want, got)
		}
	}
}

func TestEngineerDeliveryNoticeCannotBecomeACompletionReturnAddress(t *testing.T) {
	svc, st := newEngineerDispatchTestService(t)
	ctx := t.Context()
	if err := st.BindEngineerSession(ctx, dispatchCallerPath, model.SessionSourceCodex, dispatchCallerKey, "exact-caller"); err != nil {
		t.Fatal(err)
	}
	createDispatchTestOperation(t, st, "lcrop_prior_turn", dispatchCallerPath, "codex", dispatchCallerKey,
		control.CapabilityEngineerSendPrompt, control.EngineerSendPromptInput{ProjectPath: dispatchWorkerPath, Provider: control.ProviderCodex, SessionMode: control.SessionModeResumeOrNew, Prompt: "First turn"})
	deliverDispatchTestMessage(t, st, "lcrop_prior_turn", "First turn")
	op := createDispatchTestOperation(t, st, "lcrop_waiting_turn", dispatchCallerPath, "codex", dispatchCallerKey,
		control.CapabilityEngineerSendPrompt, control.EngineerSendPromptInput{ProjectPath: dispatchWorkerPath, Provider: control.ProviderCodex, SessionMode: control.SessionModeResumeOrNew, Prompt: "Next turn"})
	message, err := st.CreateEngineerMessage(ctx, control.EngineerMessage{OperationID: op.ID, ProjectPath: dispatchWorkerPath, Provider: control.ProviderCodex, SessionMode: control.SessionModeResumeOrNew, TargetSessionID: "worker-thread", Prompt: "Next turn"})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := st.RecordEngineerMessageWaiting(ctx, message.ID, "project-unloaded", "Waiting for project reload"); err != nil {
		t.Fatal(err)
	}
	if err := svc.QueuePendingEngineerDispatchReplies(ctx, 32); err != nil {
		t.Fatal(err)
	}
	if _, err := st.RecordEngineerMessageState(ctx, message.ID, control.EngineerMessageDelivered, "worker-thread", "delivered", nil); err != nil {
		t.Fatal(err)
	}
	dispatches, err := svc.RouteEngineerTurnCompletion(ctx, EngineerTurnCompletion{ProjectPath: dispatchWorkerPath, Provider: model.SessionSourceCodex, SessionID: "worker-thread", Summary: "Done"})
	if err != nil || len(dispatches) != 1 {
		t.Fatalf("waiting notice duplicated the turn report: %+v %v", dispatches, err)
	}
}
