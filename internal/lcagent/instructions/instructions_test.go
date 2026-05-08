package instructions

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestLoadWorkspaceReadsRootAgentsFile(t *testing.T) {
	root := t.TempDir()
	path := filepath.Join(root, "AGENTS.md")
	if err := os.WriteFile(path, []byte("Always run tests.\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	got, err := LoadWorkspace(root)
	if err != nil {
		t.Fatalf("load instructions: %v", err)
	}
	if got.Path != path || got.Body != "Always run tests." || got.Truncated {
		t.Fatalf("instructions = %#v", got)
	}
	section := got.PromptSection()
	if !strings.Contains(section, "Project instructions from") || !strings.Contains(section, "Always run tests.") {
		t.Fatalf("prompt section = %q", section)
	}
}

func TestLoadWorkspaceMissingAgentsFileIsEmpty(t *testing.T) {
	got, err := LoadWorkspace(t.TempDir())
	if err != nil {
		t.Fatalf("load missing instructions: %v", err)
	}
	if got.Body != "" || got.PromptSection() != "" {
		t.Fatalf("missing instructions = %#v", got)
	}
}
