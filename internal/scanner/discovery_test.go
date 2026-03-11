package scanner

import (
	"os"
	"path/filepath"
	"testing"
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
