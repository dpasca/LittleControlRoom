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
	if !strings.Contains(read.Output, "total_lines: 3") || !strings.Contains(read.Output, "has_more: true") || !strings.Contains(read.Output, "next_offset: 3") {
		t.Fatalf("read metadata missing:\n%s", read.Output)
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

	searchWithContext := files.SearchContext("needle", "README.md", "", 20, 1, 1)
	if !searchWithContext.Success {
		t.Fatalf("search with context failed: %s", searchWithContext.Error)
	}
	for _, want := range []string{"README.md:2: beta needle", "  1 | alpha", "> 2 | beta needle", "  3 | gamma"} {
		if !strings.Contains(searchWithContext.Output, want) {
			t.Fatalf("search context missing %q:\n%s", want, searchWithContext.Output)
		}
	}

	binary := files.Read("image.bin", 1, 20)
	if binary.Success || !binary.Binary || !strings.Contains(binary.Error, "binary file suppressed") {
		t.Fatalf("binary read = %#v, want suppressed failure", binary)
	}
}

func TestFileToolsOutlineGoAndMarkdown(t *testing.T) {
	root := t.TempDir()
	goBody := `package demo

import (
	"context"
	"fmt"
)

type Runner struct{}

const statusReady = "ready"

func Run(ctx context.Context) error {
	return nil
}

func (r *Runner) String() string {
	return fmt.Sprint(statusReady)
}
`
	if err := os.WriteFile(filepath.Join(root, "demo.go"), []byte(goBody), 0o644); err != nil {
		t.Fatal(err)
	}
	mdBody := "# Title\n\nIntro\n\n## Details\n\nBody\n\n### Deep\n\nMore\n"
	if err := os.WriteFile(filepath.Join(root, "README.md"), []byte(mdBody), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(root, "internal", "mod"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "internal", "mod", "worker.go"), []byte("package mod\n\nfunc Work() {}\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(root, "vendor"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "vendor", "skip.go"), []byte("package vendor\n\nfunc Skip() {}\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	w, err := policy.NewWorkspace(root, policy.AutonomyOff)
	if err != nil {
		t.Fatal(err)
	}
	files := FileTools{Workspace: w}

	goOutline := files.Outline("demo.go")
	if !goOutline.Success {
		t.Fatalf("go outline failed: %s", goOutline.Error)
	}
	for _, want := range []string{"type: go", "package: demo", "imports: 2", "type Runner lines", "const statusReady lines", "func Run lines", "method *Runner.String lines"} {
		if !strings.Contains(goOutline.Output, want) {
			t.Fatalf("go outline missing %q:\n%s", want, goOutline.Output)
		}
	}

	mdOutline := files.Outline("README.md")
	if !mdOutline.Success {
		t.Fatalf("markdown outline failed: %s", mdOutline.Error)
	}
	for _, want := range []string{"type: markdown", "h1 lines 1-11: Title", "h2 lines 5-11: Details", "h3 lines 9-11: Deep"} {
		if !strings.Contains(mdOutline.Output, want) {
			t.Fatalf("markdown outline missing %q:\n%s", want, mdOutline.Output)
		}
	}

	moduleOutline := files.ModuleOutline(".", "*.go", 10)
	if !moduleOutline.Success {
		t.Fatalf("module outline failed: %s", moduleOutline.Error)
	}
	for _, want := range []string{"path: .", "files: 2", "file: demo.go", "file: internal/mod/worker.go", "func Work lines"} {
		if !strings.Contains(moduleOutline.Output, want) {
			t.Fatalf("module outline missing %q:\n%s", want, moduleOutline.Output)
		}
	}
	if strings.Contains(moduleOutline.Output, "vendor/skip.go") {
		t.Fatalf("module outline should skip vendor:\n%s", moduleOutline.Output)
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
