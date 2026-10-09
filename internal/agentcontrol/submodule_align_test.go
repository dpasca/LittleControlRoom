package agentcontrol

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"lcroom/internal/control"
	"lcroom/internal/store"
	"lcroom/internal/submodulealign"
	"lcroom/internal/submodulealign/aligntest"
)

func submoduleAlignExecutor(t *testing.T, scope control.AuthorityScope, origin string) (*Executor, *store.Store) {
	t.Helper()
	st, err := store.Open(filepath.Join(t.TempDir(), "lcr.sqlite"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { st.Close() })
	executor, err := NewExecutor(Options{Store: st, OriginProjectPath: origin, Scope: scope, Source: "mcp", Provider: "claude_code", SessionKey: "session"})
	if err != nil {
		t.Fatal(err)
	}
	return executor, st
}

func alignArgs(f aligntest.Fixture) json.RawMessage {
	return json.RawMessage(`{"parent_path":"` + f.Task + `","submodule_path":"asset"}`)
}

func TestSubmoduleAlignProposalReportsAncestryAndAsksTheOperator(t *testing.T) {
	aligntest.IsolateEnv(t)
	f := aligntest.New(t)
	f.Pin(t, f.C2, f.C1)
	executor, _ := submoduleAlignExecutor(t, control.AuthorityScopeProject, f.Task)

	report, err := executor.Propose(t.Context(), string(control.CapabilityGitSubmoduleAlign), alignArgs(f), "align")
	if err != nil {
		t.Fatal(err)
	}
	plan, ok := report["preflight"].(*submodulealign.Plan)
	if !ok || plan.Ancestry != submodulealign.AncestryFastForward || plan.TargetCommit != f.C2 || plan.CurrentHead != f.C1 {
		t.Fatalf("preflight = %#v", report["preflight"])
	}
	if summary, _ := report["preflight_summary"].(string); !strings.Contains(summary, "fast-forward") {
		t.Fatalf("summary = %q", summary)
	}
	if report["automatic_delivery"] == true {
		t.Fatalf("no saved permission exists, yet delivery is automatic: %#v", report)
	}
	if report["requires_new_user_turn"] != true {
		t.Fatalf("agent was not told to wait: %#v", report)
	}
}

func TestSubmoduleAlignProposalRefusalsAreImmediateAndPrecise(t *testing.T) {
	aligntest.IsolateEnv(t)
	f := aligntest.New(t)
	f.Pin(t, f.C2, f.C1)
	executor, st := submoduleAlignExecutor(t, control.AuthorityScopeProject, f.Task)

	if err := os.WriteFile(filepath.Join(f.TaskSub, "scratch.txt"), []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	_, err := executor.Propose(t.Context(), string(control.CapabilityGitSubmoduleAlign), alignArgs(f), "dirty")
	if err == nil || !strings.Contains(err.Error(), submodulealign.RefusalDirty) || !strings.Contains(err.Error(), "scratch.txt") {
		t.Fatalf("dirty refusal = %v", err)
	}
	if _, found, err := st.ClaimNextControlOperation(t.Context()); err != nil || found {
		t.Fatalf("a refused proposal still queued an operation: found=%v err=%v", found, err)
	}

	// A project-scoped agent may align only its own checkout, never a sibling's.
	sibling, _ := submoduleAlignExecutor(t, control.AuthorityScopeProject, f.Canonical)
	_, err = sibling.Propose(t.Context(), string(control.CapabilityGitSubmoduleAlign), alignArgs(f), "sibling")
	if err == nil || !strings.Contains(err.Error(), "own checkout") || !strings.Contains(err.Error(), f.Canonical) {
		t.Fatalf("cross-project error = %v", err)
	}
	unknown, _ := submoduleAlignExecutor(t, control.AuthorityScopeProject, "")
	if _, err := unknown.Propose(t.Context(), string(control.CapabilityGitSubmoduleAlign), alignArgs(f), "unknown"); err == nil {
		t.Fatal("accepted a project-scoped caller with no known project")
	}
}

func TestSavedPermissionCoversOnlyCleanFastForwardAlignments(t *testing.T) {
	aligntest.IsolateEnv(t)
	f := aligntest.New(t)
	f.Pin(t, f.C2, f.C1)
	executor, st := submoduleAlignExecutor(t, control.AuthorityScopeProject, f.Task)
	ctx := t.Context()

	first, err := executor.Propose(ctx, string(control.CapabilityGitSubmoduleAlign), alignArgs(f), "first")
	if err != nil {
		t.Fatal(err)
	}
	if _, _, err := st.ClaimNextControlOperation(ctx); err != nil {
		t.Fatal(err)
	}
	if _, err := st.ApproveControlPermission(ctx, first["operation"].(control.Operation).ID, false); err != nil {
		t.Fatal(err)
	}

	next, err := executor.Propose(ctx, string(control.CapabilityGitSubmoduleAlign), alignArgs(f), "routine")
	if err != nil {
		t.Fatal(err)
	}
	if next["automatic_delivery"] != true || next["requires_new_user_turn"] != false {
		t.Fatalf("clean fast-forward not covered by the saved permission: %#v", next)
	}
	nextOp := next["operation"].(control.Operation)
	if !StandingPermissionApplies(ctx, nextOp) {
		t.Fatal("StandingPermissionApplies rejected a clean fast-forward")
	}

	// The same grant must not cover a backward move, even though the typed
	// arguments are identical.
	f.Pin(t, f.C1, f.C2)
	backward, err := executor.Get(ctx, nextOp.ID)
	if err != nil {
		t.Fatal(err)
	}
	if backward["automatic_delivery"] == true || backward["requires_new_user_turn"] != true {
		t.Fatalf("saved permission covered a backward move: %#v", backward)
	}
	if StandingPermissionApplies(ctx, nextOp) {
		t.Fatal("StandingPermissionApplies accepted a backward move")
	}

	// Nor an unrelated capability's gate being affected.
	if !StandingPermissionApplies(ctx, control.Operation{Capability: control.CapabilityTodoAdd}) {
		t.Fatal("the gate interfered with another capability")
	}
}
