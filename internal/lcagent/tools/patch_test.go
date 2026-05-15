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
	if !result.Denied || !strings.Contains(result.DenialReason, "--auto off") {
		t.Fatalf("denial metadata = denied %v reason %q", result.Denied, result.DenialReason)
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
	if result.PatchSummary == nil {
		t.Fatal("PatchSummary is nil")
	}
	if result.PatchSummary.TotalAddedLines != 2 || result.PatchSummary.TotalDeletedLines != 1 {
		t.Fatalf("patch totals = +%d -%d", result.PatchSummary.TotalAddedLines, result.PatchSummary.TotalDeletedLines)
	}
	if !strings.Contains(result.DiffSummary, "README.md: update +1 -1") || !strings.Contains(result.DiffSummary, "docs/note.txt: add +1 -0") {
		t.Fatalf("diff summary = %q", result.DiffSummary)
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
	if !result.Denied || !strings.Contains(result.DenialReason, "escapes workspace") {
		t.Fatalf("denial metadata = denied %v reason %q", result.Denied, result.DenialReason)
	}
}

func TestPatchApplierMalformedPatchReturnsFormatHint(t *testing.T) {
	root := t.TempDir()
	w, err := policy.NewWorkspace(root, policy.AutonomyLow)
	if err != nil {
		t.Fatal(err)
	}
	result := PatchApplier{Workspace: w}.Apply(`--- a/README.md
+++ b/README.md
@@ -1 +1 @@
-old
+new
`)
	if result.Success {
		t.Fatal("malformed patch succeeded, want error")
	}
	if !strings.Contains(result.Error, "*** Update File: README.md") {
		t.Fatalf("error missing apply_patch format hint: %q", result.Error)
	}
	if result.PatchFailure == nil || result.PatchFailure.Stage != "parse" || !strings.Contains(result.PatchFailure.Hint, "*** Begin Patch") {
		t.Fatalf("patch failure = %#v", result.PatchFailure)
	}
}

func TestPatchApplierContextFailureReturnsRecoveryHint(t *testing.T) {
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "README.md"), []byte("current\nkeep\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	w, err := policy.NewWorkspace(root, policy.AutonomyLow)
	if err != nil {
		t.Fatal(err)
	}
	result := PatchApplier{Workspace: w}.Apply(`*** Begin Patch
*** Update File: README.md
@@
-old
+new
 keep
*** End Patch
`)
	if result.Success {
		t.Fatal("stale-context patch succeeded, want error")
	}
	if result.PatchFailure == nil || result.PatchFailure.Stage != "apply" || result.PatchFailure.Path != "README.md" {
		t.Fatalf("patch failure = %#v", result.PatchFailure)
	}
	if len(result.PatchFailure.SuggestedReads) != 1 {
		t.Fatalf("suggested reads = %#v, want one read suggestion", result.PatchFailure.SuggestedReads)
	}
	suggestion := result.PatchFailure.SuggestedReads[0]
	if suggestion.Path != "README.md" || suggestion.Offset != 1 || suggestion.Limit != 2 {
		t.Fatalf("suggested read = %#v, want README.md lines 1-2", suggestion)
	}
	if !strings.Contains(result.Error, `read_file {"path":"README.md","offset":1,"limit":2}`) || !strings.Contains(result.Error, "smaller hunk") {
		t.Fatalf("error missing recovery hint: %q", result.Error)
	}
}
