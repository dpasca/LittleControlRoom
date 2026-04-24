package todoworktree

import (
	"context"
	"database/sql"
	"path/filepath"
	"testing"
	"time"

	"lcroom/internal/events"
	"lcroom/internal/model"
	"lcroom/internal/store"
)

type fakeSuggester struct {
	result Result
	err    error
}

func (f fakeSuggester) Suggest(context.Context, Request) (Result, error) {
	return f.result, f.err
}

func (f fakeSuggester) ModelName() string {
	return "gpt-5.4-mini"
}

func TestManagerGeneratesReadySuggestion(t *testing.T) {
	t.Parallel()
	ctx := context.Background()

	st, err := store.Open(filepath.Join(t.TempDir(), "little-control-room.sqlite"))
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	defer st.Close()

	now := time.Now()
	if err := st.UpsertProjectState(ctx, model.ProjectState{
		Path:           "/tmp/demo",
		Name:           "demo",
		Status:         model.StatusIdle,
		AttentionScore: 25,
		InScope:        true,
		UpdatedAt:      now,
	}); err != nil {
		t.Fatalf("upsert project: %v", err)
	}
	item, err := st.AddTodo(ctx, "/tmp/demo", "Fix TODO dialog spacing")
	if err != nil {
		t.Fatalf("add todo: %v", err)
	}

	manager := NewManager(st, events.NewBus(), Options{
		Client: fakeSuggester{
			result: Result{
				BranchName:     "fix/todo-dialog-spacing",
				WorktreeSuffix: "fix-todo-dialog-spacing",
				Kind:           "bugfix",
				Reason:         "The task corrects layout behavior in an existing dialog.",
				Confidence:     0.9,
			},
		},
		Workers:    1,
		Debounce:   time.Second,
		StaleAfter: time.Minute,
	})
	manager.debounce = 0

	if queued, err := manager.QueueTodo(ctx, item.ID); err != nil || !queued {
		t.Fatalf("QueueTodo() = (%t, %v), want (true, nil)", queued, err)
	}
	if err := manager.processOne(ctx); err != nil {
		t.Fatalf("processOne() error = %v", err)
	}

	suggestion, err := st.GetTodoWorktreeSuggestion(ctx, item.ID)
	if err != nil {
		t.Fatalf("final GetTodoWorktreeSuggestion() error = %v", err)
	}
	if suggestion.Status != model.TodoWorktreeSuggestionReady {
		t.Fatalf("final status = %q, want %q", suggestion.Status, model.TodoWorktreeSuggestionReady)
	}
	if suggestion.BranchName != "fix/todo-dialog-spacing" {
		t.Fatalf("branch = %q, want %q", suggestion.BranchName, "fix/todo-dialog-spacing")
	}
}

func TestManagerStartProcessesQueuedSuggestionsInBackground(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	runCtx, cancel := context.WithTimeout(context.Background(), 6*time.Second)
	defer cancel()

	st, err := store.Open(filepath.Join(t.TempDir(), "little-control-room.sqlite"))
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	defer st.Close()

	now := time.Now()
	if err := st.UpsertProjectState(ctx, model.ProjectState{
		Path:           "/tmp/demo",
		Name:           "demo",
		Status:         model.StatusIdle,
		AttentionScore: 25,
		InScope:        true,
		UpdatedAt:      now,
	}); err != nil {
		t.Fatalf("upsert project: %v", err)
	}
	item, err := st.AddTodo(ctx, "/tmp/demo", "Wire TODO suggestions into the dialog")
	if err != nil {
		t.Fatalf("add todo: %v", err)
	}

	manager := NewManager(st, events.NewBus(), Options{
		Client: fakeSuggester{
			result: Result{
				BranchName:     "feat/todo-dialog-suggestions",
				WorktreeSuffix: "feat-todo-dialog-suggestions",
				Kind:           "feature",
				Reason:         "The task adds a new TODO suggestion flow to the existing dialog.",
				Confidence:     0.88,
			},
		},
		Workers:    1,
		Debounce:   1100 * time.Millisecond,
		StaleAfter: time.Minute,
	})

	if queued, err := manager.QueueTodo(ctx, item.ID); err != nil || !queued {
		t.Fatalf("QueueTodo() = (%t, %v), want (true, nil)", queued, err)
	}
	manager.Start(runCtx)
	time.Sleep(2500 * time.Millisecond)

	suggestion, err := st.GetTodoWorktreeSuggestion(ctx, item.ID)
	if err != nil {
		t.Fatalf("GetTodoWorktreeSuggestion() error = %v", err)
	}
	if suggestion.Status != model.TodoWorktreeSuggestionReady {
		t.Fatalf("background status = %q, want %q", suggestion.Status, model.TodoWorktreeSuggestionReady)
	}
}

func TestManagerStartDoesNotQueueOpenTodosSpeculatively(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	runCtx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()

	st, err := store.Open(filepath.Join(t.TempDir(), "little-control-room.sqlite"))
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	defer st.Close()

	now := time.Now()
	if err := st.UpsertProjectState(ctx, model.ProjectState{
		Path:           "/tmp/demo",
		Name:           "demo",
		Status:         model.StatusIdle,
		AttentionScore: 25,
		InScope:        true,
		UpdatedAt:      now,
	}); err != nil {
		t.Fatalf("upsert project: %v", err)
	}
	item, err := st.AddTodo(ctx, "/tmp/demo", "Only create a worktree when explicitly requested")
	if err != nil {
		t.Fatalf("add todo: %v", err)
	}

	manager := NewManager(st, events.NewBus(), Options{
		Client: fakeSuggester{
			result: Result{
				BranchName:     "feat/on-demand-worktree-suggestion",
				WorktreeSuffix: "feat-on-demand-worktree-suggestion",
				Kind:           "feature",
				Reason:         "The task enables on-demand worktree suggestion generation.",
				Confidence:     0.9,
			},
		},
		Workers:    1,
		Debounce:   0,
		StaleAfter: time.Minute,
	})

	manager.Start(runCtx)
	time.Sleep(1500 * time.Millisecond)

	if _, err := st.GetTodoWorktreeSuggestion(ctx, item.ID); err == nil || err != sql.ErrNoRows {
		t.Fatalf("GetTodoWorktreeSuggestion() error = %v, want sql.ErrNoRows", err)
	}
}

func TestManagerProcessesQueuedSuggestionAfterClientConfigured(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	runCtx, cancel := context.WithTimeout(context.Background(), 4*time.Second)
	defer cancel()

	st, err := store.Open(filepath.Join(t.TempDir(), "little-control-room.sqlite"))
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	defer st.Close()

	now := time.Now()
	if err := st.UpsertProjectState(ctx, model.ProjectState{
		Path:           "/tmp/demo",
		Name:           "demo",
		Status:         model.StatusIdle,
		AttentionScore: 25,
		InScope:        true,
		UpdatedAt:      now,
	}); err != nil {
		t.Fatalf("upsert project: %v", err)
	}
	item, err := st.AddTodo(ctx, "/tmp/demo", "Enable TODO suggester after setup")
	if err != nil {
		t.Fatalf("add todo: %v", err)
	}
	if queued, err := st.ForceQueueTodoWorktreeSuggestion(ctx, item.ID); err != nil || !queued {
		t.Fatalf("ForceQueueTodoWorktreeSuggestion() = (%t, %v), want (true, nil)", queued, err)
	}

	manager := NewManager(st, events.NewBus(), Options{
		Workers:    1,
		Debounce:   0,
		StaleAfter: time.Minute,
	})
	manager.Start(runCtx)
	manager.ConfigureClient(fakeSuggester{
		result: Result{
			BranchName:     "chore/enable-todo-suggester-after-setup",
			WorktreeSuffix: "chore-enable-todo-suggester-after-setup",
			Kind:           "chore",
			Reason:         "The task wires late setup configuration into the TODO suggester lifecycle.",
			Confidence:     0.86,
		},
	})

	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		suggestion, err := st.GetTodoWorktreeSuggestion(ctx, item.ID)
		if err != nil {
			t.Fatalf("GetTodoWorktreeSuggestion() error = %v", err)
		}
		if suggestion.Status == model.TodoWorktreeSuggestionReady {
			return
		}
		time.Sleep(25 * time.Millisecond)
	}

	suggestion, err := st.GetTodoWorktreeSuggestion(ctx, item.ID)
	if err != nil {
		t.Fatalf("final GetTodoWorktreeSuggestion() error = %v", err)
	}
	t.Fatalf("final status = %q, want %q", suggestion.Status, model.TodoWorktreeSuggestionReady)
}
