package tools

import (
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"lcroom/internal/lcagent/policy"
)

func TestFileToolsReadListAndSearch(t *testing.T) {
	root := t.TempDir()
	if err := os.MkdirAll(filepath.Join(root, "docs"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "README.md"), []byte("alpha\nbeta needle\ngamma\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "docs", "note.txt"), []byte("needle in docs\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "image.bin"), []byte{0, 1, 2}, 0o644); err != nil {
		t.Fatal(err)
	}
	w, err := policy.NewWorkspace(root, policy.AutonomyOff)
	if err != nil {
		t.Fatal(err)
	}
	files := FileTools{Workspace: w}

	read := files.Read("README.md", 2, 1)
	if !read.Success {
		t.Fatalf("read failed: %s", read.Error)
	}
	if !strings.Contains(read.Output, "lines: 2-2") || !strings.Contains(read.Output, "2 | beta needle") {
		t.Fatalf("read output = %q", read.Output)
	}

	list := files.List(".", "*.md", 20)
	if !list.Success {
		t.Fatalf("list failed: %s", list.Error)
	}
	if !strings.Contains(list.Output, "README.md") || strings.Contains(list.Output, "note.txt") {
		t.Fatalf("list output = %q", list.Output)
	}

	search := files.Search("needle", ".", "*.txt", 20)
	if !search.Success {
		t.Fatalf("search failed: %s", search.Error)
	}
	if !strings.Contains(search.Output, "docs/note.txt:1: needle in docs") || strings.Contains(search.Output, "README.md") {
		t.Fatalf("search output = %q", search.Output)
	}

	binary := files.Read("image.bin", 1, 20)
	if binary.Success || !binary.Binary || !strings.Contains(binary.Error, "binary file suppressed") {
		t.Fatalf("binary read = %#v, want suppressed failure", binary)
	}
}

func TestFileToolsDenyWorkspaceEscape(t *testing.T) {
	root := t.TempDir()
	w, err := policy.NewWorkspace(root, policy.AutonomyOff)
	if err != nil {
		t.Fatal(err)
	}
	files := FileTools{Workspace: w}
	if result := files.Read("../outside.txt", 1, 20); result.Success {
		t.Fatalf("read outside workspace succeeded: %#v", result)
	}
	if result := files.List("../outside", "", 20); result.Success {
		t.Fatalf("list outside workspace succeeded: %#v", result)
	}
	if result := files.Search("needle", "../outside", "", 20); result.Success {
		t.Fatalf("search outside workspace succeeded: %#v", result)
	}
}

func TestFileToolsDenySymlinkEscape(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("symlink setup requires elevated privileges on some Windows hosts")
	}
	root := t.TempDir()
	outside := t.TempDir()
	if err := os.WriteFile(filepath.Join(outside, "secret.txt"), []byte("needle\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(outside, filepath.Join(root, "outside")); err != nil {
		t.Fatal(err)
	}
	w, err := policy.NewWorkspace(root, policy.AutonomyOff)
	if err != nil {
		t.Fatal(err)
	}
	files := FileTools{Workspace: w}
	if result := files.Read("outside/secret.txt", 1, 20); result.Success {
		t.Fatalf("read through symlink escape succeeded: %#v", result)
	}
}
