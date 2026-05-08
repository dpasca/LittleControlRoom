package tools

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"lcroom/internal/lcagent/policy"
)

func TestPatchApplierDeniesAutoOff(t *testing.T) {
	root := t.TempDir()
	w, err := policy.NewWorkspace(root, policy.AutonomyOff)
	if err != nil {
		t.Fatal(err)
	}
	result := PatchApplier{Workspace: w}.Apply(`*** Begin Patch
*** Add File: a.txt
+hello
*** End Patch
`)
	if result.Success {
		t.Fatal("patch succeeded with auto off, want denial")
	}
}

func TestPatchApplierAppliesAddAndUpdate(t *testing.T) {
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "README.md"), []byte("old\nkeep\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	w, err := policy.NewWorkspace(root, policy.AutonomyLow)
	if err != nil {
		t.Fatal(err)
	}
	patch := `*** Begin Patch
*** Update File: README.md
@@
-old
+new
 keep
*** Add File: docs/note.txt
+created
*** End Patch
`
	result := PatchApplier{Workspace: w}.Apply(patch)
	if !result.Success {
		t.Fatalf("patch failed: %s", result.Error)
	}
	data, err := os.ReadFile(filepath.Join(root, "README.md"))
	if err != nil {
		t.Fatal(err)
	}
	if string(data) != "new\nkeep\n" {
		t.Fatalf("README = %q", data)
	}
	note, err := os.ReadFile(filepath.Join(root, "docs", "note.txt"))
	if err != nil {
		t.Fatal(err)
	}
	if string(note) != "created\n" {
		t.Fatalf("note = %q", note)
	}
	if got := strings.Join(result.FilesTouched, ","); got != "README.md,docs/note.txt" {
		t.Fatalf("FilesTouched = %v", result.FilesTouched)
	}
}

func TestPatchApplierDeniesOutsidePath(t *testing.T) {
	root := t.TempDir()
	w, err := policy.NewWorkspace(root, policy.AutonomyLow)
	if err != nil {
		t.Fatal(err)
	}
	result := PatchApplier{Workspace: w}.Apply(`*** Begin Patch
*** Add File: ../outside.txt
+nope
*** End Patch
`)
	if result.Success {
		t.Fatal("outside patch succeeded, want denial")
	}
}
