package tui

import (
	"errors"
	"testing"

	"lcroom/internal/codexapp"
	"lcroom/internal/control"
	"lcroom/internal/model"

	tea "github.com/charmbracelet/bubbletea"
)

func TestProjectMessageResolvesCurrentSessionAfterWaiting(t *testing.T) {
	svc := newControlTestService(t)
	path := t.TempDir()
	project := model.ProjectSummary{Path: path, PresentOnDisk: true, LatestSessionID: "stale-disk-session", LatestSessionFormat: "modern"}
	live := &fakeCodexSession{projectPath: path, snapshot: codexapp.Snapshot{Provider: codexapp.ProviderOpenCode, ThreadID: "first", Started: true, Busy: true, ActiveTurnID: "turn"}}
	manager := codexapp.NewManagerWithFactory(func(codexapp.LaunchRequest, func()) (codexapp.Session, error) { return live, nil })
	m := Model{ctx: t.Context(), svc: svc, codexManager: manager, allProjects: []model.ProjectSummary{project}}
	queued := m.createEngineerMessageCmd(control.Invocation{}, control.EngineerSendPromptInput{SessionMode: control.SessionModeResumeOrNew, FollowCurrentSession: true}, project, codexapp.ProviderOpenCode, "Project update")().(engineerMessageQueuedMsg)
	if queued.err != nil {
		t.Fatal(queued.err)
	}
	message := queued.message
	if message.TargetSessionID != "" || !message.FollowCurrentSession {
		t.Fatalf("premature binding: %#v", message)
	}
	if d := m.engineerMessageDisposition(message); !d.wait || d.deliver || d.failure != nil {
		t.Fatalf("missing live recipient: %#v", d)
	}
	if _, _, err := manager.Open(codexapp.LaunchRequest{ProjectPath: path, Provider: codexapp.ProviderOpenCode}); err != nil {
		t.Fatal(err)
	}
	if d := m.engineerMessageDisposition(message); !d.wait || d.failure != nil {
		t.Fatalf("busy recipient: %#v", d)
	}
	live.snapshot.ThreadID = "replacement"
	live.snapshot.Busy = false
	live.snapshot.ActiveTurnID = ""
	d := m.engineerMessageDisposition(message)
	if !d.deliver || d.failure != nil || d.bindSessionID != "replacement" {
		t.Fatalf("replacement recipient: %#v", d)
	}
	// An interrupted submission is uncertain, so even current-session messages
	// must retain that bound recipient rather than duplicate into another thread.
	message.TargetSessionID = "first"
	if d := m.engineerMessageDisposition(message); !errors.Is(d.failure, codexapp.ErrSessionChanged) {
		t.Fatalf("bound retry crossed recipients: %#v", d)
	}
}

func TestCurrentRecipientRaceRequeuesOnlyBeforeSubmission(t *testing.T) {
	for _, tc := range []struct {
		name        string
		deliveryErr error
		wantState   control.EngineerMessageState
		wantTarget  string
		uncertain   bool
	}{
		{"changed before submission", codexapp.ErrCurrentSessionUnavailable, control.EngineerMessageQueued, "", false},
		{"uncertain failure", errors.New("transport disconnected"), control.EngineerMessageFailed, "original", false},
		{"delivered", nil, control.EngineerMessageDelivered, "original", false},
		{"restart followed by replacement", codexapp.ErrCurrentSessionUnavailable, control.EngineerMessageFailed, "original", true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			svc := newControlTestService(t)
			message, err := svc.Store().CreateEngineerMessage(t.Context(), control.EngineerMessage{ProjectPath: t.TempDir(), Provider: control.ProviderCodex, SessionMode: control.SessionModeResumeOrNew, FollowCurrentSession: true, Prompt: "Project update"})
			if err != nil {
				t.Fatal(err)
			}
			message, _, err = svc.Store().ClaimEngineerMessage(t.Context(), message.ID, "original")
			if err != nil {
				t.Fatal(err)
			}
			if tc.uncertain {
				if err := svc.Store().RequeueDeliveringEngineerMessages(t.Context()); err != nil {
					t.Fatal(err)
				}
				message, _, err = svc.Store().ClaimEngineerMessage(t.Context(), message.ID, "original")
				if err != nil {
					t.Fatal(err)
				}
			}
			m := Model{ctx: t.Context(), svc: svc}
			cmd := m.engineerMessageDeliveryCmd(message, func() tea.Msg {
				return codexSessionOpenedMsg{projectPath: message.ProjectPath, err: tc.deliveryErr, snapshot: codexapp.Snapshot{ThreadID: "original"}}
			})
			var receipt *engineerMessageDeliveryRecordedMsg
			for _, msg := range collectCmdMsgs(cmd) {
				if recorded, ok := msg.(engineerMessageDeliveryRecordedMsg); ok {
					receipt = &recorded
				}
				if opened, ok := msg.(codexSessionOpenedMsg); ok && tc.wantState == control.EngineerMessageQueued {
					if !opened.deliveryDeferred {
						t.Fatal("queued delivery still reports a session-open failure")
					}
					pending := m
					pending.codexPendingOpen = &codexPendingOpenState{projectPath: message.ProjectPath}
					updated, _ := pending.applyCodexSessionOpenedMsg(opened)
					got := updated.(Model)
					if len(got.errorLogEntries) != 0 || got.codexPendingOpen != nil {
						t.Fatalf("deferred delivery left a failure or loading state: %#v", got.codexPendingOpen)
					}
				}
			}
			if receipt == nil || receipt.recordErr != nil {
				t.Fatalf("receipt: %#v", receipt)
			}
			if receipt.message.State != tc.wantState || receipt.message.TargetSessionID != tc.wantTarget {
				t.Fatalf("receipt: %#v", receipt.message)
			}
		})
	}
}
