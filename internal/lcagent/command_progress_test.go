package lcagent

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"lcroom/internal/lcagent/policy"
	"lcroom/internal/lcagent/tools"
)

func TestCommandProgressUsesRawEvidenceAcrossPresentationChanges(t *testing.T) {
	workspace, err := policy.NewWorkspace(t.TempDir(), policy.AutonomyMedium)
	if err != nil {
		t.Fatal(err)
	}
	runner := tools.CommandRunner{Workspace: workspace, ArtifactDir: t.TempDir()}
	key := func(result tools.ToolResult) string {
		body, _ := json.Marshal(result)
		return openRouterToolResultProgressKey(string(body))
	}
	first := runner.RunSpec(context.Background(), tools.CommandSpec{Argv: []string{"printf", "same evidence"}})
	second := runner.RunSpec(context.Background(), tools.CommandSpec{Argv: []string{"printf", "%s", "same evidence"}})
	if !first.Success || !second.Success || first.EvidenceHash == "" || !strings.Contains(first.Output, "[exit:") {
		t.Fatalf("real command results: first=%+v second=%+v", first, second)
	}
	if key(first) != key(second) {
		t.Fatal("same raw evidence changed with command spelling/timing")
	}
	second.ExitCode = 1
	second.Success = false
	if key(first) == key(second) {
		t.Fatal("failed exit was hidden")
	}
	// Tail changes beyond the inline limit must remain distinguishable, even
	// though the presenter stores them in timestamped artifact files.
	longOutput := strings.Repeat("x", 25*1024)
	one := runner.RunSpec(context.Background(), tools.CommandSpec{Argv: []string{"printf", longOutput + "A"}})
	two := runner.RunSpec(context.Background(), tools.CommandSpec{Argv: []string{"printf", longOutput + "A"}})
	three := runner.RunSpec(context.Background(), tools.CommandSpec{Argv: []string{"printf", longOutput + "B"}})
	if !one.Success || !one.Truncated || !two.Truncated || !three.Truncated {
		t.Fatal("expected truncated successful results")
	}
	if key(one) != key(two) {
		t.Fatal("artifact filenames changed evidence key")
	}
	if key(one) == key(three) {
		t.Fatal("truncated evidence change was lost")
	}
}
