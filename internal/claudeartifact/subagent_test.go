package claudeartifact

import "testing"

func TestSubagentIdentityCannotCollideWithParentOrSibling(t *testing.T) {
	id := SubagentSessionID("parent", "worker")
	parent, agent, ok := ParseSubagentSessionID(id)
	if !ok || parent != "parent" || agent != "worker" || id == "parent" || id == SubagentSessionID("parent", "other") {
		t.Fatalf("invalid child identity: %q -> %q, %q, %v", id, parent, agent, ok)
	}
	for _, invalid := range []string{"parent", "parent/agent-", "../agent-worker", "parent/agent-../other", "parent/agent-..", "parent/agent-a\\b"} {
		if _, _, ok := ParseSubagentSessionID(invalid); ok {
			t.Fatalf("accepted invalid child identity %q", invalid)
		}
	}
}
