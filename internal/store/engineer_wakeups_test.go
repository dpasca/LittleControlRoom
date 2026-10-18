package store

import (
	"context"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"lcroom/internal/control"
)

func wakeupMessage(project string, deliverAt time.Time) control.EngineerMessage {
	return control.EngineerMessage{
		ProjectPath:     project,
		Provider:        control.ProviderCodex,
		SessionMode:     control.SessionModeResumeOrNew,
		TargetSessionID: "session-1",
		Prompt:          "Verify the repost.",
		DeliverAt:       deliverAt,
		ExpiresAt:       deliverAt.Add(time.Hour),
		Unattended:      &control.UnattendedGrant{FileChanges: true, Tools: []string{"mcp__playwright__*"}, Notes: "no new posts"},
	}
}

func TestScheduledEngineerMessageWaitsForDeliveryTime(t *testing.T) {
	st, err := Open(filepath.Join(t.TempDir(), "wakeup.sqlite"))
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	ctx := context.Background()
	project := t.TempDir()

	created, err := st.CreateEngineerMessage(ctx, wakeupMessage(project, time.Now().Add(time.Hour)))
	if err != nil {
		t.Fatal(err)
	}
	if !created.Scheduled() || created.Unattended == nil || !created.Unattended.FileChanges || created.Unattended.ValidMinutes != control.DefaultGrantValidMinutes {
		t.Fatalf("created wakeup lost its schedule or grant: %#v", created)
	}
	queued, err := st.ListQueuedEngineerMessages(ctx, 10)
	if err != nil || len(queued) != 0 {
		t.Fatalf("future wakeup listed as deliverable: %v err=%v", queued, err)
	}
	if _, claimed, err := st.ClaimEngineerMessage(ctx, created.ID, "session-1"); err != nil || claimed {
		t.Fatalf("future wakeup claimed early: claimed=%t err=%v", claimed, err)
	}
	scheduled, err := st.ListScheduledEngineerMessages(ctx, 10)
	if err != nil || len(scheduled) != 1 || scheduled[0].ID != created.ID {
		t.Fatalf("scheduled list = %v err=%v", scheduled, err)
	}

	due, err := st.CreateEngineerMessage(ctx, wakeupMessage(project, time.Now().Add(-time.Minute)))
	if err != nil {
		t.Fatal(err)
	}
	queued, err = st.ListQueuedEngineerMessages(ctx, 10)
	if err != nil || len(queued) != 1 || queued[0].ID != due.ID {
		t.Fatalf("due wakeup not deliverable: %v err=%v", queued, err)
	}
	if _, claimed, err := st.ClaimEngineerMessage(ctx, due.ID, "session-1"); err != nil || !claimed {
		t.Fatalf("due wakeup not claimable: claimed=%t err=%v", claimed, err)
	}
}

func TestExpireOverdueEngineerMessagesReportsMissedWakeups(t *testing.T) {
	st, err := Open(filepath.Join(t.TempDir(), "wakeup-expire.sqlite"))
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	ctx := context.Background()
	project := t.TempDir()

	missed, err := st.CreateEngineerMessage(ctx, wakeupMessage(project, time.Now().Add(-3*time.Hour)))
	if err != nil {
		t.Fatal(err)
	}
	fresh, err := st.CreateEngineerMessage(ctx, wakeupMessage(project, time.Now().Add(-5*time.Minute)))
	if err != nil {
		t.Fatal(err)
	}
	expired, err := st.ExpireOverdueEngineerMessages(ctx, time.Now())
	if err != nil || len(expired) != 1 || expired[0].ID != missed.ID {
		t.Fatalf("expired = %v err=%v", expired, err)
	}
	if expired[0].State != control.EngineerMessageFailed || !strings.Contains(expired[0].LastError, "Missed wakeup") {
		t.Fatalf("missed wakeup state = %s error=%q", expired[0].State, expired[0].LastError)
	}
	got, err := st.GetEngineerMessage(ctx, fresh.ID)
	if err != nil || got.State != control.EngineerMessageQueued {
		t.Fatalf("in-grace wakeup = %s err=%v", got.State, err)
	}
}

func TestCancelScheduledEngineerMessage(t *testing.T) {
	st, err := Open(filepath.Join(t.TempDir(), "wakeup-cancel.sqlite"))
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	ctx := context.Background()
	project := t.TempDir()

	created, err := st.CreateEngineerMessage(ctx, wakeupMessage(project, time.Now().Add(time.Hour)))
	if err != nil {
		t.Fatal(err)
	}
	canceled, ok, err := st.CancelScheduledEngineerMessage(ctx, created.ID)
	if err != nil || !ok || canceled.State != control.EngineerMessageFailed {
		t.Fatalf("cancel = %#v ok=%t err=%v", canceled, ok, err)
	}
	if _, ok, _ := st.CancelScheduledEngineerMessage(ctx, created.ID); ok {
		t.Fatal("canceled a wakeup twice")
	}
	ordinary, err := st.CreateEngineerMessage(ctx, control.EngineerMessage{
		ProjectPath: project, Provider: control.ProviderCodex, SessionMode: control.SessionModeResumeOrNew, Prompt: "hello",
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, ok, _ := st.CancelScheduledEngineerMessage(ctx, ordinary.ID); ok {
		t.Fatal("cancel must only withdraw scheduled wakeups")
	}
}

func TestScheduledEngineerMessageValidation(t *testing.T) {
	st, err := Open(filepath.Join(t.TempDir(), "wakeup-validate.sqlite"))
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	ctx := context.Background()
	project := t.TempDir()
	when := time.Now().Add(time.Hour)

	for name, edit := range map[string]func(*control.EngineerMessage){
		"no exact target":    func(m *control.EngineerMessage) { m.TargetSessionID = "" },
		"follow current":     func(m *control.EngineerMessage) { m.TargetSessionID = ""; m.FollowCurrentSession = true },
		"no expiry":          func(m *control.EngineerMessage) { m.ExpiresAt = time.Time{} },
		"grant without time": func(m *control.EngineerMessage) { m.DeliverAt = time.Time{}; m.ExpiresAt = time.Time{} },
	} {
		message := wakeupMessage(project, when)
		edit(&message)
		if _, err := st.CreateEngineerMessage(ctx, message); err == nil {
			t.Errorf("%s: wakeup was accepted", name)
		}
	}
}
