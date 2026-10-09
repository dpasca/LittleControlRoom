package control

import (
	"encoding/json"
	"strings"
	"testing"
)

func TestGitSubmoduleAlignCapabilityIsGuardedAndProjectScoped(t *testing.T) {
	listed, err := ListReport("git", AuthorityScopeProject, true)
	if err != nil {
		t.Fatal(err)
	}
	var found *CapabilitySummary
	for _, c := range listed["capabilities"].([]CapabilitySummary) {
		if c.Name == CapabilityGitSubmoduleAlign {
			c := c
			found = &c
		}
	}
	if found == nil || found.Risk != RiskWrite || found.Confirmation != ConfirmationRequired || !found.RequiresHost || !found.Async {
		t.Fatalf("catalog entry = %#v", found)
	}
	described, err := DescribeReport("git.submodule_align", AuthorityScopeProject, "propose_control_operation")
	if err != nil {
		t.Fatalf("project-scoped agents must be able to describe it: %v", err)
	}
	encoded, _ := json.Marshal(described)
	for _, want := range []string{"checkout --detach", "never writes Git configuration", "standing permission", "fetch_if_missing", "ancestry"} {
		if !strings.Contains(string(encoded), want) {
			t.Fatalf("description missing %q", want)
		}
	}
}

func TestGitSubmoduleAlignValidation(t *testing.T) {
	good := strings.Repeat("ab", 20)
	for _, args := range []string{
		``, `{}`, `null`,
		`{"parent_path":"/repo"}`,
		`{"submodule_path":"asset"}`,
		`{"parent_path":"repo","submodule_path":"asset"}`,
		`{"parent_path":"/repo","submodule_path":"../asset"}`,
		`{"parent_path":"/repo","submodule_path":"/asset"}`,
		`{"parent_path":"/repo","submodule_path":"asset","target_commit":"main"}`,
		`{"parent_path":"/repo","submodule_path":"asset","target_commit":"abc"}`,
		`{"parent_path":"/repo","submodule_path":"asset","force":true}`,
		`{"parent_path":"/repo","submodule_path":"asset","config":{"core.worktree":"/x"}}`,
		`{"parent_path":"/repo","submodule_path":"asset","request_id":"different"}`,
	} {
		if _, err := ValidateInvocation(Invocation{RequestID: "align-1", Capability: CapabilityGitSubmoduleAlign, Args: json.RawMessage(args)}); err == nil {
			t.Fatalf("accepted invalid alignment: %s", args)
		}
	}
	inv, err := BuildProposedInvocation("align-1", CapabilityGitSubmoduleAlign,
		json.RawMessage(`{"parent_path":" /repos/task/ ","submodule_path":" asset-source/ ","target_commit":"`+strings.ToUpper(good)+`","fetch_if_missing":true}`))
	if err != nil || inv.RequestID != "align-1" {
		t.Fatalf("invocation = %#v, err = %v", inv, err)
	}
	var input GitSubmoduleAlignInput
	if err := json.Unmarshal(inv.Args, &input); err != nil {
		t.Fatal(err)
	}
	if input.ParentPath != "/repos/task" || input.SubmodulePath != "asset-source" || input.TargetCommit != good || !input.FetchIfMissing {
		t.Fatalf("normalized input = %#v", input)
	}
}

func TestGitSubmoduleAlignPermissionIsScopedToCallerParentAndSubmodule(t *testing.T) {
	op := func(origin, args string) Operation {
		return Operation{ProjectPath: origin, Provider: "claude_code", SessionKey: "session", Invocation: Invocation{RequestID: "op", Capability: CapabilityGitSubmoduleAlign, Args: json.RawMessage(args)}}
	}
	base, ok := PermissionForOperation(op("/repos/task", `{"parent_path":"/repos/task","submodule_path":"asset"}`))
	if !ok || base.Target != "/repos/task" || base.Capability != CapabilityGitSubmoduleAlign || base.Origin != "/repos/task" {
		t.Fatalf("permission = %#v, ok = %v", base, ok)
	}
	// The commit and the fetch choice are chosen by the caller per request, so
	// they must not narrow or widen a grant; the host gates them on live state.
	varied, ok := PermissionForOperation(op("/repos/task", `{"parent_path":"/repos/task","submodule_path":"asset","fetch_if_missing":true,"target_commit":"`+strings.Repeat("a", 40)+`"}`))
	if !ok || varied.Constraints != base.Constraints {
		t.Fatalf("grant keyed on per-request choices: %#v vs %#v", varied, base)
	}
	other, ok := PermissionForOperation(op("/repos/task", `{"parent_path":"/repos/task","submodule_path":"other"}`))
	if !ok || other.Constraints == base.Constraints {
		t.Fatalf("a grant covers a different submodule: %#v", other)
	}
	if _, ok := PermissionForOperation(op("", `{"parent_path":"/repos/task","submodule_path":"asset"}`)); ok {
		t.Fatal("grant created without a caller project")
	}
}
