package service

import (
	"context"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"lcroom/internal/config"
	"lcroom/internal/events"
	"lcroom/internal/model"
	"lcroom/internal/store"
)

func TestPreviewCloneProjectDerivesRepositoryNames(t *testing.T) {
	t.Parallel()

	parent := t.TempDir()
	tests := []struct {
		repository string
		want       string
	}{
		{repository: "https://github.com/acme/widget.git", want: "widget"},
		{repository: "ssh://git@github.com/acme/gadget.git", want: "gadget"},
		{repository: "git@github.com:acme/console.git", want: "console"},
		{repository: "/tmp/remotes/local.git", want: "local"},
		{repository: `C:\remotes\windows.git`, want: "windows"},
		{repository: "file:///tmp/remotes/with%20spaces.git", want: "with spaces"},
	}
	for _, tt := range tests {
		t.Run(tt.repository, func(t *testing.T) {
			preview, err := PreviewCloneProject(tt.repository, parent)
			if err != nil {
				t.Fatalf("PreviewCloneProject() error = %v", err)
			}
			if preview.ProjectName != tt.want {
				t.Fatalf("project name = %q, want %q", preview.ProjectName, tt.want)
			}
			if preview.ProjectPath != filepath.Join(parent, tt.want) {
				t.Fatalf("project path = %q, want %q", preview.ProjectPath, filepath.Join(parent, tt.want))
			}
		})
	}
}

func TestPreviewCloneProjectUsesNumberedCollisionSuffix(t *testing.T) {
	t.Parallel()

	parent := t.TempDir()
	for _, name := range []string{"widget", "widget-2"} {
		if err := os.Mkdir(filepath.Join(parent, name), 0o755); err != nil {
			t.Fatalf("mkdir %s: %v", name, err)
		}
	}
	preview, err := PreviewCloneProject("git@github.com:acme/widget.git", parent)
	if err != nil {
		t.Fatalf("PreviewCloneProject() error = %v", err)
	}
	if preview.ProjectName != "widget-3" || preview.ProjectPath != filepath.Join(parent, "widget-3") || !preview.Collision {
		t.Fatalf("preview = %#v, want widget-3 collision", preview)
	}
}

func TestCloneProjectReservesCollisionSafeDestinationAndRegisters(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	st, err := store.Open(filepath.Join(t.TempDir(), "little-control-room.sqlite"))
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	defer st.Close()

	parent := t.TempDir()
	if err := os.Mkdir(filepath.Join(parent, "widget"), 0o755); err != nil {
		t.Fatalf("mkdir collision: %v", err)
	}
	svc := New(config.Default(), st, events.NewBus(), nil)
	var gotRepository, gotPath string
	svc.gitRepoCloner = func(_ context.Context, repository, projectPath string) error {
		gotRepository = repository
		gotPath = projectPath
		return os.Mkdir(filepath.Join(projectPath, ".git"), 0o755)
	}

	result, err := svc.CloneProject(ctx, CloneProjectRequest{
		Repository:             "git@github.com:acme/widget.git",
		ParentPath:             parent,
		PreferredSessionSource: model.SessionSourceOpenCode,
		CategoryExplicit:       true,
	})
	if err != nil {
		t.Fatalf("CloneProject() error = %v", err)
	}
	wantPath := filepath.Join(parent, "widget-2")
	if gotRepository != "git@github.com:acme/widget.git" || gotPath != wantPath {
		t.Fatalf("cloner inputs = (%q, %q), want repository and %q", gotRepository, gotPath, wantPath)
	}
	if result.ProjectName != "widget-2" || result.ProjectPath != wantPath || !result.CollisionResolved || !result.Cloned || !result.Registered {
		t.Fatalf("result = %#v", result)
	}
	summary, err := st.GetProjectSummary(ctx, wantPath, false)
	if err != nil {
		t.Fatalf("GetProjectSummary() error = %v", err)
	}
	if summary.Name != "widget-2" || !summary.ManuallyAdded || summary.PreferredSessionSource != model.SessionSourceOpenCode {
		t.Fatalf("summary = %#v", summary)
	}
}

func TestCloneProjectRemovesIncompleteCloneAfterFailure(t *testing.T) {
	t.Parallel()

	st, err := store.Open(filepath.Join(t.TempDir(), "little-control-room.sqlite"))
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	defer st.Close()

	parent := t.TempDir()
	svc := New(config.Default(), st, events.NewBus(), nil)
	svc.gitRepoCloner = func(_ context.Context, _, projectPath string) error {
		if err := os.WriteFile(filepath.Join(projectPath, "partial"), []byte("partial"), 0o644); err != nil {
			return err
		}
		return errors.New("authentication failed")
	}

	result, err := svc.CloneProject(context.Background(), CloneProjectRequest{
		Repository: "https://github.com/acme/widget.git",
		ParentPath: parent,
	})
	if err == nil || !strings.Contains(err.Error(), "authentication failed") {
		t.Fatalf("CloneProject() error = %v, want authentication failure", err)
	}
	if result.Cloned || result.Registered {
		t.Fatalf("result = %#v, want unsuccessful clone", result)
	}
	if _, statErr := os.Stat(filepath.Join(parent, "widget")); !errors.Is(statErr, os.ErrNotExist) {
		t.Fatalf("incomplete clone still exists, stat err = %v", statErr)
	}
}

func TestCloneProjectPreservesSuccessfulCloneWhenRegistrationFails(t *testing.T) {
	t.Parallel()

	st, err := store.Open(filepath.Join(t.TempDir(), "little-control-room.sqlite"))
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	svc := New(config.Default(), st, events.NewBus(), nil)
	svc.gitRepoCloner = func(_ context.Context, _, projectPath string) error {
		return os.Mkdir(filepath.Join(projectPath, ".git"), 0o755)
	}
	if err := st.Close(); err != nil {
		t.Fatalf("close store: %v", err)
	}

	parent := t.TempDir()
	result, err := svc.CloneProject(context.Background(), CloneProjectRequest{
		Repository: "https://github.com/acme/widget.git",
		ParentPath: parent,
	})
	if err == nil || !strings.Contains(err.Error(), "registration") {
		t.Fatalf("CloneProject() error = %v, want registration failure", err)
	}
	if !result.Cloned || result.Registered {
		t.Fatalf("result = %#v, want preserved unregistered clone", result)
	}
	if _, statErr := os.Stat(filepath.Join(result.ProjectPath, ".git")); statErr != nil {
		t.Fatalf("successful clone was not preserved: %v", statErr)
	}
}

func TestCloneProjectCancellationCleansReservedDestination(t *testing.T) {
	t.Parallel()

	st, err := store.Open(filepath.Join(t.TempDir(), "little-control-room.sqlite"))
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	defer st.Close()
	svc := New(config.Default(), st, events.NewBus(), nil)
	svc.gitRepoCloner = func(ctx context.Context, _, _ string) error {
		return ctx.Err()
	}

	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	parent := t.TempDir()
	_, err = svc.CloneProject(ctx, CloneProjectRequest{
		Repository: "https://github.com/acme/widget.git",
		ParentPath: parent,
	})
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("CloneProject() error = %v, want context canceled", err)
	}
	if _, statErr := os.Stat(filepath.Join(parent, "widget")); !errors.Is(statErr, os.ErrNotExist) {
		t.Fatalf("canceled clone destination still exists, stat err = %v", statErr)
	}
}

func TestRunGitCloneClonesIntoReservedDirectory(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	repository := filepath.Join(root, "source.git")
	cmd := exec.Command("git", "init", "--bare", "--quiet", repository)
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("git init --bare: %v: %s", err, strings.TrimSpace(string(out)))
	}
	destination := filepath.Join(root, "destination")
	if err := os.Mkdir(destination, 0o755); err != nil {
		t.Fatalf("mkdir destination: %v", err)
	}
	if err := runGitClone(context.Background(), repository, destination); err != nil {
		t.Fatalf("runGitClone() error = %v", err)
	}
	if _, err := os.Stat(filepath.Join(destination, ".git")); err != nil {
		t.Fatalf("cloned .git missing: %v", err)
	}
}
