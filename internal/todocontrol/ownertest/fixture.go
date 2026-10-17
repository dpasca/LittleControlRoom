// Package ownertest provides isolated Git/SQLite fixtures for owner regressions.
package ownertest

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"lcroom/internal/model"
	"lcroom/internal/store"
)

type Fixture struct {
	Root, Caller, Worker, DBPath string
	Store                        *store.Store
}

func Git(t *testing.T, path string, args ...string) string {
	t.Helper()
	cmd := exec.Command("git", append([]string{"-C", path}, args...)...)
	cmd.Env = append(os.Environ(), "GIT_CONFIG_NOSYSTEM=1", "GIT_CONFIG_GLOBAL=/dev/null", "GIT_TERMINAL_PROMPT=0")
	output, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("git %v: %v: %s", args, err, output)
	}
	return strings.TrimSpace(string(output))
}

func New(t *testing.T) Fixture {
	t.Helper()
	base := t.TempDir()
	f := Fixture{Root: filepath.Join(base, "repo"), Caller: filepath.Join(base, "repo--release"), Worker: filepath.Join(base, "repo--worker"), DBPath: filepath.Join(base, "state.sqlite")}
	if err := os.MkdirAll(f.Root, 0700); err != nil {
		t.Fatal(err)
	}
	Git(t, f.Root, "init", "-b", "master")
	Git(t, f.Root, "config", "user.email", "test@example.com")
	Git(t, f.Root, "config", "user.name", "Test")
	if err := os.WriteFile(filepath.Join(f.Root, "keep.txt"), []byte("original\n"), 0600); err != nil {
		t.Fatal(err)
	}
	Git(t, f.Root, "add", "keep.txt")
	Git(t, f.Root, "commit", "-m", "fixture")
	Git(t, f.Root, "worktree", "add", "-b", "release", f.Caller)
	Git(t, f.Root, "worktree", "add", "-b", "worker", f.Worker)
	var err error
	f.Store, err = store.Open(f.DBPath)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { f.Store.Close() })
	for _, path := range []string{f.Root, f.Caller, f.Worker} {
		state := model.ProjectState{Path: path, Name: filepath.Base(path), PresentOnDisk: true, InScope: true, UpdatedAt: time.Now()}
		if path != f.Root {
			state.WorktreeKind, state.WorktreeRootPath = model.WorktreeKindLinked, f.Root
		}
		if err := f.Store.UpsertProjectState(t.Context(), state); err != nil {
			t.Fatal(err)
		}
	}
	return f
}
