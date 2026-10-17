package store

import (
	"path/filepath"
	"testing"

	"lcroom/internal/control"
)

func TestCurrentSessionMessageBindingAndRestart(t *testing.T) {
	path := filepath.Join(t.TempDir(), "messages.sqlite")
	st, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { st.Close() })
	ctx := t.Context()
	request := control.EngineerMessage{OperationID: "current-message", ProjectPath: "/project", Provider: control.ProviderCodex, SessionMode: control.SessionModeResumeOrNew, FollowCurrentSession: true, Prompt: "Checkpoint"}
	message, err := st.CreateEngineerMessage(ctx, request)
	if err != nil {
		t.Fatal(err)
	}
	if !message.FollowCurrentSession || message.TargetSessionID != "" || message.RequestedTargetSessionID != "" {
		t.Fatalf("premature binding: %#v", message)
	}
	if _, claimed, err := st.ClaimEngineerMessage(ctx, message.ID, ""); err != nil || claimed {
		t.Fatalf("claimed without a recipient: %v %v", claimed, err)
	}
	message, claimed, err := st.ClaimEngineerMessage(ctx, message.ID, "first")
	if err != nil || !claimed {
		t.Fatalf("claim: %v %v", claimed, err)
	}
	// A proven pre-submission refusal permits selecting the new current session.
	message, err = st.RecordEngineerMessageState(ctx, message.ID, control.EngineerMessageQueued, "", "recipient changed before submission", nil)
	if err != nil || message.TargetSessionID != "" {
		t.Fatalf("release: %#v %v", message, err)
	}
	message, claimed, err = st.ClaimEngineerMessage(ctx, message.ID, "second")
	if err != nil || !claimed || message.TargetSessionID != "second" {
		t.Fatalf("reclaim: %#v %v", message, err)
	}
	if err := st.Close(); err != nil {
		t.Fatal(err)
	}
	st, err = Open(path)
	if err != nil {
		t.Fatal(err)
	}
	if err := st.RequeueDeliveringEngineerMessages(ctx); err != nil {
		t.Fatal(err)
	}
	message, claimed, err = st.ClaimEngineerMessage(ctx, message.ID, "third")
	if err != nil || !claimed || message.TargetSessionID != "second" || !message.FollowCurrentSession || !message.SubmissionUncertain {
		t.Fatalf("uncertain restart delivery crossed recipients: %#v %v", message, err)
	}
	message, err = st.RecordEngineerMessageState(ctx, message.ID, control.EngineerMessageQueued, "", "recipient disappeared again", nil)
	if err != nil || message.TargetSessionID != "second" {
		t.Fatalf("uncertain submission lost its recipient: %#v %v", message, err)
	}
	if _, err := st.CreateEngineerMessage(ctx, request); err != nil {
		t.Fatalf("idempotent retry: %v", err)
	}
	request.FollowCurrentSession = false
	if _, err := st.CreateEngineerMessage(ctx, request); err == nil {
		t.Fatal("idempotent retry changed addressing policy")
	}
}

func TestCurrentSessionMessageSchemaMigrationPreservesExactTargets(t *testing.T) {
	st, err := Open(filepath.Join(t.TempDir(), "migration.sqlite"))
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	ctx := t.Context()
	message, err := st.CreateEngineerMessage(ctx, control.EngineerMessage{ProjectPath: "/project", Provider: control.ProviderCodex, SessionMode: control.SessionModeResumeOrNew, TargetSessionID: "caller", Prompt: "Reply"})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := st.db.ExecContext(ctx, `ALTER TABLE engineer_messages DROP COLUMN follow_current_session`); err != nil {
		t.Fatal(err)
	}
	if _, err := st.db.ExecContext(ctx, `ALTER TABLE engineer_messages DROP COLUMN submission_uncertain`); err != nil {
		t.Fatal(err)
	}
	if err := st.initSchema(ctx); err != nil {
		t.Fatal(err)
	}
	got, err := st.GetEngineerMessage(ctx, message.ID)
	if err != nil || got.FollowCurrentSession || got.TargetSessionID != "caller" || got.RequestedTargetSessionID != "caller" {
		t.Fatalf("migration changed exact target: %#v %v", got, err)
	}
}
