package scanner

import (
	"os"
	"path/filepath"
	"testing"

	"lcroom/internal/appfs"
)

func TestDiscoverGitProjects(t *testing.T) {
	root := t.TempDir()
	projectA := filepath.Join(root, "a")
	projectB := filepath.Join(root, "nested", "b")
	if err := os.MkdirAll(filepath.Join(projectA, ".git"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(projectB, ".git"), 0o755); err != nil {
		t.Fatal(err)
	}

	got, err := DiscoverGitProjects(Discovery{Roots: []string{root}, MaxDepth: 4})
	if err != nil {
		t.Fatalf("DiscoverGitProjects() error = %v", err)
	}

	if len(got) != 2 {
		t.Fatalf("expected 2 projects, got %d (%v)", len(got), got)
	}
}

func TestDiscoverGitProjectsSkipsManagedInternalRoots(t *testing.T) {
	root := t.TempDir()
	project := filepath.Join(root, "repo")
	internalDataDir := filepath.Join(root, ".little-control-room")
	internalRoot := appfs.InternalWorkspaceRoot(internalDataDir)
	internalProject := filepath.Join(internalRoot, "lcroom-codex-helper-demo")

	for _, path := range []string{
		filepath.Join(project, ".git"),
		filepath.Join(internalProject, ".git"),
	} {
		if err := os.MkdirAll(path, 0o755); err != nil {
			t.Fatal(err)
		}
	}

	got, err := DiscoverGitProjects(Discovery{
		Roots:     []string{root},
		MaxDepth:  6,
		SkipPaths: []string{internalRoot},
	})
	if err != nil {
		t.Fatalf("DiscoverGitProjects() error = %v", err)
	}
	if len(got) != 1 || got[0] != project {
		t.Fatalf("DiscoverGitProjects() = %v, want only %q", got, project)
	}
}
