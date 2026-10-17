package service

import (
	"context"
	"database/sql"
	"os"
	"path/filepath"
	"reflect"
	"testing"
	"time"

	"lcroom/internal/control"
	"lcroom/internal/events"
	"lcroom/internal/model"
	"lcroom/internal/todocontrol"
	"lcroom/internal/todocontrol/ownertest"
)

func TestPlainTodoAddUsesRepositoryOwnerAndRetainsAttachments(t *testing.T) {
	f := ownertest.New(t)
	svc := newOwnerTestService(t, f.Store)
	refreshed := make(chan string, 8)
	svc.refreshProjectStatusFn = func(_ context.Context, path string) error { refreshed <- path; return nil }
	eventCh, unsubscribe := svc.bus.Subscribe(16)
	defer unsubscribe()
	plain, err := svc.AddTodo(t.Context(), f.Caller, "linked checkout task")
	if err != nil || plain.ProjectPath != f.Root {
		t.Fatalf("plain=%+v err=%v", plain, err)
	}
	paths := []string{filepath.Join(f.Caller, "first.png"), filepath.Join(f.Caller, "second.png")}
	attachments := make([]model.TodoAttachment, len(paths))
	for i, path := range paths {
		if err := os.WriteFile(path, []byte("keep attachment bytes"), 0600); err != nil {
			t.Fatal(err)
		}
		attachments[i] = model.TodoAttachment{Kind: model.TodoAttachmentLocalImage, Path: path, Position: i, CreatedAt: time.Unix(123+int64(i), 0)}
	}
	before := append([]model.TodoAttachment(nil), attachments...)
	added, err := svc.AddTodoWithAttachments(t.Context(), f.Caller, "task with evidence", attachments)
	if err != nil || added.ProjectPath != f.Root {
		t.Fatalf("added=%+v err=%v", added, err)
	}
	stored, err := f.Store.GetTodo(t.Context(), added.ID)
	if err != nil || stored.ProjectPath != f.Root || len(stored.Attachments) != len(attachments) {
		t.Fatalf("stored=%+v err=%v", stored, err)
	}
	for i, attachment := range stored.Attachments {
		if attachment.ID == 0 || attachment.TodoID != added.ID || attachment.Path != paths[i] || attachment.Kind != before[i].Kind || attachment.Position != i || !attachment.CreatedAt.Equal(before[i].CreatedAt) {
			t.Fatalf("attachment=%+v want=%+v", attachment, before[i])
		}
		bytes, err := os.ReadFile(attachment.Path)
		if err != nil || string(bytes) != "keep attachment bytes" {
			t.Fatalf("attachment bytes=%q err=%v", bytes, err)
		}
	}
	if !reflect.DeepEqual(before, attachments) {
		t.Fatal("caller attachment metadata changed")
	}
	root, _, _ := f.Store.ListOpenTodosForReview(t.Context(), f.Root)
	linked, _, _ := f.Store.ListOpenTodosForReview(t.Context(), f.Caller)
	if len(root) != 2 || len(linked) != 0 {
		t.Fatalf("root=%+v linked=%+v", root, linked)
	}
	waitCtx, cancel := context.WithTimeout(t.Context(), 5*time.Second)
	defer cancel()
	if err := svc.WaitForAsyncProjectRefreshes(waitCtx); err != nil {
		t.Fatal(err)
	}
	close(refreshed)
	refreshCount := 0
	for path := range refreshed {
		refreshCount++
		if path != f.Root {
			t.Fatalf("refreshed checkout instead of owner: %s", path)
		}
	}
	if refreshCount == 0 {
		t.Fatal("owner was never refreshed")
	}
	addEvents := 0
	for len(eventCh) > 0 {
		event := <-eventCh
		if event.Type == events.ActionApplied && event.Payload["action"] == "add_todo" {
			addEvents++
			if event.ProjectPath != f.Root {
				t.Fatalf("event owner=%s", event.ProjectPath)
			}
		}
	}
	if addEvents != 2 {
		t.Fatalf("add events=%d", addEvents)
	}
}

func TestPlainTodoAddPreservesNonGitProject(t *testing.T) {
	f := ownertest.New(t)
	svc := newOwnerTestService(t, f.Store)
	path := t.TempDir()
	if err := f.Store.UpsertProjectState(t.Context(), model.ProjectState{Path: path, Name: "non-Git notes", InScope: true, PresentOnDisk: true, UpdatedAt: time.Now()}); err != nil {
		t.Fatal(err)
	}
	for _, attached := range []bool{false, true} {
		var item model.TodoItem
		var err error
		if attached {
			item, err = svc.AddTodoWithAttachments(t.Context(), path, "non-Git evidence", []model.TodoAttachment{{Path: filepath.Join(path, "evidence.png")}})
		} else {
			item, err = svc.AddTodo(t.Context(), path, "non-Git task")
		}
		if err != nil || item.ProjectPath != path {
			t.Fatalf("item=%+v err=%v", item, err)
		}
		stored, err := f.Store.GetTodo(t.Context(), item.ID)
		if err != nil || stored.ProjectPath != path || (attached && len(stored.Attachments) != 1) {
			t.Fatalf("stored=%+v err=%v", stored, err)
		}
	}
}

func TestPlainTodoAddRefusesUntrustedLinkedOwnersWithoutWrites(t *testing.T) {
	for _, scenario := range []string{"missing-root", "missing-checkout", "missing-git-pointer", "stale-mapping", "unloaded-root", "ambiguous-root"} {
		t.Run(scenario, func(t *testing.T) {
			f := ownertest.New(t)
			svc := newOwnerTestService(t, f.Store)
			switch scenario {
			case "missing-root", "missing-checkout", "missing-git-pointer":
				path := map[string]string{"missing-root": f.Root, "missing-checkout": f.Caller, "missing-git-pointer": filepath.Join(f.Caller, ".git")}[scenario]
				if err := os.Rename(path, path+"-unavailable"); err != nil {
					t.Fatal(err)
				}
			case "stale-mapping":
				other := ownertest.New(t)
				if err := f.Store.UpsertProjectState(t.Context(), model.ProjectState{Path: f.Caller, Name: "caller", InScope: true, PresentOnDisk: true, WorktreeKind: model.WorktreeKindLinked, WorktreeRootPath: other.Root, UpdatedAt: time.Now()}); err != nil {
					t.Fatal(err)
				}
			case "unloaded-root":
				db, err := sql.Open("sqlite", f.DBPath)
				if err != nil {
					t.Fatal(err)
				}
				defer db.Close()
				if _, err := db.Exec(`DELETE FROM projects WHERE path=?`, f.Root); err != nil {
					t.Fatal(err)
				}
			case "ambiguous-root":
				alias := filepath.Join(filepath.Dir(f.Root), "alias")
				if err := os.Symlink(f.Root, alias); err != nil {
					t.Fatal(err)
				}
				if err := f.Store.UpsertProjectState(t.Context(), model.ProjectState{Path: alias, Name: "alias", InScope: true, PresentOnDisk: true, UpdatedAt: time.Now()}); err != nil {
					t.Fatal(err)
				}
			}
			if _, err := svc.AddTodo(t.Context(), f.Caller, "must refuse"); err == nil {
				t.Fatal("plain add accepted unsafe owner")
			}
			if _, err := svc.AddTodoWithAttachments(t.Context(), f.Caller, "must refuse with attachment", []model.TodoAttachment{{Path: "retained.png"}}); err == nil {
				t.Fatal("attachment add accepted unsafe owner")
			}
			for _, path := range []string{f.Root, f.Caller} {
				items, _, err := f.Store.ListOpenTodosForReview(t.Context(), path)
				if err != nil || len(items) != 0 {
					t.Fatalf("partial write at %s: %+v err=%v", path, items, err)
				}
			}
		})
	}
}

func TestPlainTodoControlRefusesOwnerChangedAfterReview(t *testing.T) {
	f := ownertest.New(t)
	svc := newOwnerTestService(t, f.Store)
	input := control.TodoAddInput{ProjectPath: f.Caller, Text: "reviewed add"}
	plan, err := todocontrol.InspectAdd(t.Context(), f.Store, input)
	if err != nil {
		t.Fatal(err)
	}
	input.TodoOwnerPath = plan.OwnerPath
	// The same path now names a different valid non-Git project. It must not
	// turn a previously reviewed root write into a checkout-owned TODO.
	if err := os.Rename(f.Caller, f.Caller+"-retained"); err != nil {
		t.Fatal(err)
	}
	if err := os.Mkdir(f.Caller, 0700); err != nil {
		t.Fatal(err)
	}
	if err := f.Store.UpsertProjectState(t.Context(), model.ProjectState{Path: f.Caller, Name: "replacement", InScope: true, PresentOnDisk: true, UpdatedAt: time.Now()}); err != nil {
		t.Fatal(err)
	}
	if _, err := svc.AddTodoForControl(t.Context(), input); err == nil {
		t.Fatal("reviewed owner silently changed")
	}
	for _, path := range []string{f.Root, f.Caller} {
		items, _, err := f.Store.ListOpenTodosForReview(t.Context(), path)
		if err != nil || len(items) != 0 {
			t.Fatalf("unexpected write: %+v err=%v", items, err)
		}
	}
}
