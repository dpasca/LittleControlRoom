package store

import (
	"encoding/json"
	"path/filepath"
	"testing"
	"time"

	"lcroom/internal/model"
)

func TestCommitTodoCandidatesPersistAndFenceChangedTodos(t *testing.T) {
	st, err := Open(filepath.Join(t.TempDir(), "test.sqlite"))
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	ctx := t.Context()
	path := t.TempDir()
	if err := st.UpsertProjectState(ctx, model.ProjectState{Path: path, UpdatedAt: time.Now()}); err != nil {
		t.Fatal(err)
	}
	original, err := st.AddTodo(ctx, path, "original requirement")
	if err != nil {
		t.Fatal(err)
	}
	check := model.CommitTodoCheck{ProjectPath: path, HeadHash: "head"}
	if _, err := st.QueueCommitTodoCheck(ctx, check); err != nil {
		t.Fatal(err)
	}
	claimed, err := st.ClaimNextQueuedCommitTodoCheck(ctx, time.Minute)
	if err != nil {
		t.Fatal(err)
	}
	var candidates []model.CommitTodoCandidate
	if err := json.Unmarshal([]byte(claimed.CandidatesJSON), &candidates); err != nil {
		t.Fatal(err)
	}
	if len(candidates) != 1 || candidates[0].ID != original.ID || candidates[0].Text != original.Text {
		t.Fatalf("candidates = %#v", candidates)
	}
	if err := st.UpdateTodo(ctx, original.ID, "edited during inference"); err != nil {
		t.Fatal(err)
	}
	if changed, err := st.CompleteCommitTodoCandidate(ctx, claimed, candidates[0]); err != nil || changed {
		t.Fatalf("edited candidate completed: %v, %v", changed, err)
	}
	if _, err := st.AddTodo(ctx, path, "new after queue"); err != nil {
		t.Fatal(err)
	}
	if _, err := st.FailCommitTodoCheck(ctx, claimed, "retry", 0); err != nil {
		t.Fatal(err)
	}
	if _, err := st.QueueCommitTodoCheck(ctx, check); err != nil {
		t.Fatal(err)
	}
	retried, err := st.ClaimNextQueuedCommitTodoCheck(ctx, time.Minute)
	if err != nil {
		t.Fatal(err)
	}
	if retried.CandidatesJSON != claimed.CandidatesJSON {
		t.Fatalf("retry replaced snapshot: %s", retried.CandidatesJSON)
	}
	// Return to the original text and revision to exercise claim fencing alone.
	if _, err := st.db.ExecContext(ctx, "UPDATE project_todos SET text = ?, updated_at = ? WHERE id = ?", original.Text, candidates[0].UpdatedAt, original.ID); err != nil {
		t.Fatal(err)
	}
	if changed, err := st.CompleteCommitTodoCandidate(ctx, claimed, candidates[0]); err != nil || changed {
		t.Fatalf("stale worker completed: %v, %v", changed, err)
	}
	if changed, err := st.CompleteCommitTodoCandidate(ctx, retried, candidates[0]); err != nil || !changed {
		t.Fatalf("current worker completion: %v, %v", changed, err)
	}
	if changed, err := st.CompleteCommitTodoCandidate(ctx, retried, candidates[0]); err != nil || changed {
		t.Fatalf("completed twice: %v, %v", changed, err)
	}
}
