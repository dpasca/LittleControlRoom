package store

import (
	"context"
	"errors"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"lcroom/internal/model"
)

func TestAddTodoReviewedSerializesConcurrentExactRetriesAcrossStores(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	dbPath := filepath.Join(t.TempDir(), "review.sqlite")
	first := openReviewTestStore(t, dbPath)
	defer first.Close()
	second := openReviewTestStore(t, dbPath)
	defer second.Close()
	projectPath := filepath.Join(t.TempDir(), "project")
	insertReviewTestProject(t, first, projectPath)

	_, revision, err := first.ListOpenTodosForReview(ctx, projectPath)
	if err != nil {
		t.Fatalf("list review snapshot: %v", err)
	}
	stores := []*Store{first, second}
	results := make([]ReviewedTodoResult, len(stores))
	errs := make([]error, len(stores))
	var wg sync.WaitGroup
	for i, st := range stores {
		wg.Add(1)
		go func() {
			defer wg.Done()
			results[i], errs[i] = st.AddTodoReviewed(ctx, projectPath, "Add keyboard navigation", revision)
		}()
	}
	wg.Wait()

	dispositions := map[ReviewedTodoDisposition]int{}
	for i := range results {
		if errs[i] != nil {
			t.Fatalf("concurrent add %d: %v", i, errs[i])
		}
		dispositions[results[i].Disposition]++
	}
	if dispositions[ReviewedTodoCreated] != 1 || dispositions[ReviewedTodoExisting] != 1 {
		t.Fatalf("dispositions = %#v, want one created and one existing", dispositions)
	}
	todos, _, err := first.ListOpenTodosForReview(ctx, projectPath)
	if err != nil {
		t.Fatalf("list final TODOs: %v", err)
	}
	if len(todos) != 1 || todos[0].Text != "Add keyboard navigation" {
		t.Fatalf("final TODOs = %#v", todos)
	}
}

func TestAddTodoReviewedRechecksRuntimePolicyInsideWriteTransaction(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	st := openReviewTestStore(t, filepath.Join(t.TempDir(), "review.sqlite"))
	defer st.Close()
	projectPath := filepath.Join(t.TempDir(), "project")
	insertReviewTestProject(t, st, projectPath)
	if err := st.SetRuntimeSetting(ctx, "capture_mode", "explicit_only"); err != nil {
		t.Fatal(err)
	}
	_, revision, err := st.ListOpenTodosForReview(ctx, projectPath)
	if err != nil {
		t.Fatal(err)
	}
	if err := st.SetRuntimeSetting(ctx, "capture_mode", "off"); err != nil {
		t.Fatal(err)
	}
	_, err = st.AddTodoReviewedWithRuntimePolicy(ctx, projectPath, "Add keyboard navigation", revision, "capture_mode", []string{"explicit_only"})
	if !errors.Is(err, ErrRuntimePolicyDenied) {
		t.Fatalf("policy error = %v, want ErrRuntimePolicyDenied", err)
	}
	todos, _, err := st.ListOpenTodosForReview(ctx, projectPath)
	if err != nil || len(todos) != 0 {
		t.Fatalf("TODOs after revoked write = %#v, err = %v", todos, err)
	}
}

func TestAddTodoReviewedChecksExactRetryBeforeStaleRevision(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	st := openReviewTestStore(t, filepath.Join(t.TempDir(), "review.sqlite"))
	defer st.Close()
	projectPath := filepath.Join(t.TempDir(), "project")
	insertReviewTestProject(t, st, projectPath)

	_, originalRevision, err := st.ListOpenTodosForReview(ctx, projectPath)
	if err != nil {
		t.Fatal(err)
	}
	created, err := st.AddTodoReviewed(ctx, projectPath, "Add keyboard navigation", originalRevision)
	if err != nil || created.Disposition != ReviewedTodoCreated {
		t.Fatalf("created result = %#v, err = %v", created, err)
	}
	stale, err := st.AddTodoReviewed(ctx, projectPath, "Document the shortcut", originalRevision)
	if err != nil || stale.Disposition != ReviewedTodoStale {
		t.Fatalf("stale result = %#v, err = %v", stale, err)
	}
	retry, err := st.AddTodoReviewed(ctx, projectPath, "Add keyboard navigation", originalRevision)
	if err != nil || retry.Disposition != ReviewedTodoExisting {
		t.Fatalf("exact retry result = %#v, err = %v", retry, err)
	}
	if retry.Todo.ID != created.Todo.ID {
		t.Fatalf("retry TODO id = %d, want %d", retry.Todo.ID, created.Todo.ID)
	}
}

func TestTodoReviewRevisionIsBoundToProjectScope(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	st := openReviewTestStore(t, filepath.Join(t.TempDir(), "review.sqlite"))
	defer st.Close()
	firstProject := filepath.Join(t.TempDir(), "first")
	secondProject := filepath.Join(t.TempDir(), "second")
	insertReviewTestProject(t, st, firstProject)
	insertReviewTestProject(t, st, secondProject)

	_, firstRevision, err := st.ListOpenTodosForReview(ctx, firstProject)
	if err != nil {
		t.Fatal(err)
	}
	_, secondRevision, err := st.ListOpenTodosForReview(ctx, secondProject)
	if err != nil {
		t.Fatal(err)
	}
	if firstRevision == secondRevision {
		t.Fatalf("empty TODO revisions were reused across projects: %s", firstRevision)
	}
	result, err := st.AddTodoReviewed(ctx, secondProject, "Scoped work", firstRevision)
	if err != nil {
		t.Fatal(err)
	}
	if result.Disposition != ReviewedTodoStale {
		t.Fatalf("cross-project review disposition = %q, want %q", result.Disposition, ReviewedTodoStale)
	}
}

func openReviewTestStore(t *testing.T, dbPath string) *Store {
	t.Helper()
	st, err := Open(dbPath)
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	return st
}

func insertReviewTestProject(t *testing.T, st *Store, projectPath string) {
	t.Helper()
	if err := st.UpsertProjectState(context.Background(), model.ProjectState{
		Path:      projectPath,
		Name:      filepath.Base(projectPath),
		Status:    model.StatusIdle,
		InScope:   true,
		UpdatedAt: time.Now(),
	}); err != nil {
		t.Fatalf("upsert project: %v", err)
	}
}
