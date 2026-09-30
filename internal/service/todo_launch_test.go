package service

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"lcroom/internal/config"
	"lcroom/internal/control"
	"lcroom/internal/events"
	"lcroom/internal/scanner"
	"lcroom/internal/store"
)

func TestTodoLaunchResumesEveryPartialWorktreeTrackingFailure(t *testing.T) {
	for _, stage := range []struct {
		name, trigger string
		directory     bool
	}{
		{"destination", `BEFORE INSERT ON todo_worktree_plans`, false},
		{"project", `BEFORE INSERT ON projects WHEN NEW.path <> '%s'`, true},
		{"category", `BEFORE INSERT ON category_assignments WHEN NEW.resource_id <> '%s'`, true},
		{"metadata", `BEFORE UPDATE ON projects WHEN NEW.worktree_origin_todo_id > 0`, true},
		{"ready", `BEFORE UPDATE ON todo_worktree_plans WHEN NEW.ready = 1`, true},
	} {
		t.Run(stage.name, func(t *testing.T) {
			ctx := context.Background()
			root := t.TempDir()
			project := filepath.Join(root, "repo")
			initGitRepo(t, project)
			dbPath := filepath.Join(t.TempDir(), "state.sqlite")
			st, err := store.Open(dbPath)
			if err != nil {
				t.Fatal(err)
			}
			defer st.Close()
			svc := New(config.Default(), st, events.NewBus(), nil)
			if _, err := svc.CreateOrAttachProject(ctx, CreateOrAttachProjectRequest{ParentPath: root, Name: "repo"}); err != nil {
				t.Fatal(err)
			}
			category, err := st.CreateProjectCategory(ctx, "Tasks")
			if err != nil {
				t.Fatal(err)
			}
			if err := st.SetProjectsCategory(ctx, []string{project}, category.ID); err != nil {
				t.Fatal(err)
			}
			input := control.TodoCreateWorktreeAndStartEngineerInput{RequestID: "request", ProjectPath: project, TodoText: "Recover this task", Provider: control.ProviderCodex, Prompt: "Do the original task"}
			todo, err := st.AddTodoForLaunch(ctx, input)
			if err != nil {
				t.Fatal(err)
			}
			raw, err := sql.Open("sqlite", dbPath)
			if err != nil {
				t.Fatal(err)
			}
			defer raw.Close()
			trigger := stage.trigger
			if strings.Contains(trigger, "%s") {
				trigger = fmt.Sprintf(trigger, strings.ReplaceAll(project, "'", "''"))
			}
			if _, err := raw.Exec(`CREATE TRIGGER fail_launch ` + trigger + ` BEGIN SELECT RAISE(ABORT, 'injected persistence failure'); END`); err != nil {
				t.Fatal(err)
			}
			result, err := svc.CreateTodoWorktree(ctx, CreateTodoWorktreeRequest{ProjectPath: project, TodoID: todo.ID})
			if err == nil {
				t.Fatal("expected injected failure")
			}
			worktrees, listErr := scanner.ListGitWorktrees(ctx, project)
			if listErr != nil {
				t.Fatal(listErr)
			}
			want := 1
			if stage.directory {
				want = 2
			}
			if len(worktrees) != want {
				t.Fatalf("worktrees=%d want %d (%v)", len(worktrees), want, err)
			}
			if stage.directory {
				plan, planErr := st.TodoWorktreePlan(ctx, todo.ID)
				if planErr != nil || plan.WorktreePath != result.WorktreePath || plan.Ready {
					t.Fatalf("plan=%+v err=%v", plan, planErr)
				}
				if !strings.Contains(err.Error(), "Retry launch") {
					t.Fatal(err)
				}
				if err := os.WriteFile(filepath.Join(result.WorktreePath, "keep.txt"), []byte("owner work"), 0600); err != nil {
					t.Fatal(err)
				}
			}
			if _, err := raw.Exec(`DROP TRIGGER fail_launch`); err != nil {
				t.Fatal(err)
			}
			// A new service has no in-memory knowledge of the failed attempt.
			svc = New(config.Default(), st, events.NewBus(), nil)
			retried, err := svc.CreateTodoWorktree(ctx, CreateTodoWorktreeRequest{ProjectPath: project, TodoID: todo.ID})
			if err != nil {
				t.Fatal(err)
			}
			if stage.directory {
				if retried.WorktreePath != result.WorktreePath {
					t.Fatalf("duplicate destination: %s vs %s", retried.WorktreePath, result.WorktreePath)
				}
				data, err := os.ReadFile(filepath.Join(retried.WorktreePath, "keep.txt"))
				if err != nil || string(data) != "owner work" {
					t.Fatalf("owner file=%q err=%v", data, err)
				}
			}
			again, err := svc.CreateTodoWorktree(ctx, CreateTodoWorktreeRequest{ProjectPath: project, TodoID: todo.ID})
			if err != nil || again.WorktreePath != retried.WorktreePath {
				t.Fatalf("idempotent retry=%+v err=%v", again, err)
			}
			worktrees, err = scanner.ListGitWorktrees(ctx, project)
			if err != nil || len(worktrees) != 2 {
				t.Fatalf("worktrees=%d err=%v", len(worktrees), err)
			}
			detail, err := st.GetTrackedProjectSummary(ctx, retried.WorktreePath)
			if err != nil || detail.WorktreeOriginTodoID != todo.ID || detail.CategoryID != category.ID {
				t.Fatalf("tracking=%+v err=%v", detail, err)
			}
		})
	}
}

func TestWorktreeRemovalWaitsForConcurrentDatabaseWriter(t *testing.T) {
	ctx := t.Context()
	root := t.TempDir()
	project := filepath.Join(root, "repo")
	initGitRepo(t, project)
	dbPath := filepath.Join(t.TempDir(), "state.sqlite")
	st, err := store.Open(dbPath)
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	svc := New(config.Default(), st, events.NewBus(), nil)
	if _, err := svc.CreateOrAttachProject(ctx, CreateOrAttachProjectRequest{ParentPath: root, Name: "repo"}); err != nil {
		t.Fatal(err)
	}
	todo, err := st.AddTodo(ctx, project, "Remove this worktree")
	if err != nil {
		t.Fatal(err)
	}
	result, err := svc.CreateTodoWorktree(ctx, CreateTodoWorktreeRequest{ProjectPath: project, TodoID: todo.ID})
	if err != nil {
		t.Fatal(err)
	}
	raw, err := sql.Open("sqlite", dbPath)
	if err != nil {
		t.Fatal(err)
	}
	defer raw.Close()
	raw.SetMaxOpenConns(1)
	if _, err := raw.Exec(`BEGIN IMMEDIATE`); err != nil {
		t.Fatal(err)
	}
	defer raw.Exec(`ROLLBACK`)
	done := make(chan error, 1)
	go func() { done <- svc.RemoveWorktree(ctx, result.WorktreePath, false) }()
	deadline := time.Now().Add(10 * time.Second)
	for {
		if _, err := os.Stat(result.WorktreePath); os.IsNotExist(err) {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("directory was not removed")
		}
		time.Sleep(10 * time.Millisecond)
	}
	select {
	case err := <-done:
		t.Fatalf("removal should wait for database writer: %v", err)
	case <-time.After(50 * time.Millisecond):
	}
	if _, err := raw.Exec(`COMMIT`); err != nil {
		t.Fatal(err)
	}
	if err := <-done; err != nil {
		t.Fatal(err)
	}
	if _, err := st.TodoWorktreePlan(ctx, todo.ID); !errors.Is(err, sql.ErrNoRows) {
		t.Fatalf("removed plan retained: %v", err)
	}
}
