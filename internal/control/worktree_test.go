package control

import (
	"encoding/json"
	"strings"
	"testing"
)

func TestWorktreeRemoveRequiresExactPathAndConfirmation(t *testing.T) {
	listed, err := ListReport("worktree", AuthorityScopePortfolio, true)
	if err != nil {
		t.Fatal(err)
	}
	capabilities := listed["capabilities"].([]CapabilitySummary)
	if len(capabilities) != 1 || capabilities[0].Name != CapabilityWorktreeRemove || capabilities[0].Risk != RiskDestructive || capabilities[0].Confirmation != ConfirmationRequired || !capabilities[0].Async || !capabilities[0].RequiresHost {
		t.Fatalf("unsafe removal catalog: %#v", capabilities)
	}
	for _, args := range []string{
		`{}`, `null`, `{"worktree_path":""}`, `{"worktree_path":"../repo"}`,
		`{"worktree_path":"/"}`, `{"worktree_path":"/repo/.."}`,
		`{"worktree_path":"/repo--task","force":true}`,
		`{"worktree_path":"/repo--task","delete_branch":true}`,
		`{"worktree_path":"/repo--task","request_id":"different"}`,
	} {
		if _, err := ValidateInvocation(Invocation{RequestID: "remove-1", Capability: CapabilityWorktreeRemove, Args: json.RawMessage(args)}); err == nil {
			t.Fatalf("accepted invalid removal: %s", args)
		}
	}
	inv, err := BuildProposedInvocation("remove-1", CapabilityWorktreeRemove, json.RawMessage(`{"worktree_path":" /repos/task/ "}`))
	if err != nil || !strings.Contains(string(inv.Args), `"worktree_path":"/repos/task"`) || inv.RequestID != "remove-1" {
		t.Fatalf("normalized invocation = %#v, err = %v", inv, err)
	}
	if _, err := DescribeReport("worktree.remove", AuthorityScopeProject, "propose_control_operation"); err == nil {
		t.Fatal("project-only authority exposed portfolio deletion")
	}
}
