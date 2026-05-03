package boss

import "testing"

func TestEngineerNameForKeyIsStableAndReadable(t *testing.T) {
	t.Parallel()

	first := EngineerNameForKey("agent_task", "agt_cursor")
	second := EngineerNameForKey("agent_task", "agt_cursor")
	if first == "" || first == "Engineer" {
		t.Fatalf("EngineerNameForKey() = %q, want a readable assigned name", first)
	}
	if second != first {
		t.Fatalf("EngineerNameForKey() not stable: %q then %q", first, second)
	}
}
