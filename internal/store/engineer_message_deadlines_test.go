package store

import (
	"path/filepath"
	"testing"
	"time"

	"lcroom/internal/control"
	"lcroom/internal/model"
)

func TestEngineerMessageDeadlineAndRequestSurviveRestart(t *testing.T) {
	path := filepath.Join(t.TempDir(), "deadline.sqlite")
	st, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	ctx := t.Context()
	request := control.EngineerMessage{OperationID: "durable-deadline", ProjectPath: "/destination", Provider: control.ProviderCodex, SessionMode: control.SessionModeResumeOrNew, TargetSessionID: "exact-worker", Prompt: "Preserve this request", CreatedAt: time.Now().Add(-4 * time.Hour).Truncate(time.Second)}
	message, err := st.CreateEngineerMessage(ctx, request)
	if err != nil {
		t.Fatal(err)
	}
	deadline := request.CreatedAt.Add(EngineerMessageDeliveryDeadline)
	if !message.ExpiresAt.Equal(deadline) {
		t.Fatalf("deadline %s, want %s", message.ExpiresAt, deadline)
	}
	if _, claimed, err := st.ClaimEngineerMessage(ctx, message.ID, "exact-worker"); err != nil || !claimed {
		t.Fatalf("claim: %t %v", claimed, err)
	}
	if err := st.Close(); err != nil {
		t.Fatal(err)
	}
	st, err = Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	if err := st.RequeueDeliveringEngineerMessages(ctx); err != nil {
		t.Fatal(err)
	}
	retried, err := st.CreateEngineerMessage(ctx, request)
	if err != nil || retried.ID != message.ID || !retried.ExpiresAt.Equal(deadline) || retried.TargetSessionID != "exact-worker" || !retried.SubmissionUncertain {
		t.Fatalf("restart changed the request: %+v %v", retried, err)
	}
	// A legacy queued row gains a deadline from its original timestamp.
	if _, err := st.db.ExecContext(ctx, `UPDATE engineer_messages SET expires_at = 0 WHERE id = ?`, message.ID); err != nil {
		t.Fatal(err)
	}
	for range 2 {
		if err := st.RequeueDeliveringEngineerMessages(ctx); err != nil {
			t.Fatal(err)
		}
	}
	legacy, err := st.GetEngineerMessage(ctx, message.ID)
	if err != nil || !legacy.ExpiresAt.Equal(deadline) {
		t.Fatalf("legacy deadline renewed: %+v %v", legacy, err)
	}
}

func TestFirstObservedWorktreeBranchRemainsStable(t *testing.T) {
	st, err := Open(filepath.Join(t.TempDir(), "branches.sqlite"))
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	state := model.ProjectState{Path: "/repo--perf", WorktreeKind: model.WorktreeKindLinked, WorktreeRootPath: "/repo", RepoBranch: "perf/tfa-android-vulkan-60fps", PresentOnDisk: true, InScope: true}
	if err := st.UpsertProjectState(t.Context(), state); err != nil {
		t.Fatal(err)
	}
	state.RepoBranch = "perf/tfa-ground-water2"
	if err := st.UpsertProjectState(t.Context(), state); err != nil {
		t.Fatal(err)
	}
	summary, err := st.GetProjectSummary(t.Context(), state.Path, true)
	if err != nil || !summary.WorktreeBranchChanged() || summary.WorktreeInitialBranch != "perf/tfa-android-vulkan-60fps" {
		t.Fatalf("branch switch silently adopted: %+v %v", summary, err)
	}
}
