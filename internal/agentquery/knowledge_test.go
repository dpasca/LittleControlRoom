package agentquery

import (
	"encoding/json"
	"strings"
	"testing"
)

func TestKnowledgeDiscoveryAndReadWithoutProjectData(t *testing.T) {
	executor, err := NewExecutor(Options{Reader: newFakeReader(nil), OriginProjectPath: "/unloaded/project", Scope: ScopeProject})
	if err != nil {
		t.Fatal(err)
	}
	report, err := ListReport("knowledge", ScopeProject, true)
	if err != nil || len(report["queries"].([]CapabilitySummary)) != 2 {
		t.Fatalf("knowledge discovery = %#v, %v", report, err)
	}
	list, err := executor.Execute(t.Context(), QueryKnowledgeList, nil)
	if err != nil {
		t.Fatal(err)
	}
	topics := list["topics"].([]knowledgeTopic)
	for _, topic := range topics {
		args, _ := json.Marshal(map[string]string{"topic": topic.ID})
		result, err := executor.Execute(t.Context(), QueryKnowledgeGet, args)
		if err != nil {
			t.Fatal(err)
		}
		if result["freshness"] != "built_in_documentation" || result["truncated"] != false {
			t.Fatalf("knowledge envelope = %#v", result)
		}
		for _, want := range []string{"extensions.worktreeConfig=true", "config.worktree", "--git-dir", "shared repository config"} {
			if !strings.Contains(result["markdown"].(string), want) {
				t.Fatalf("topic %s missing diagnostic guidance %q", topic.ID, want)
			}
		}
	}
	for _, tc := range []struct {
		name Name
		args string
	}{
		{QueryKnowledgeList, `{"project_path":"/private"}`},
		{QueryKnowledgeGet, `{}`},
		{QueryKnowledgeGet, `{"topic":"../../etc/passwd"}`},
		{QueryKnowledgeGet, `{"topic":"submodule-worktrees","path":"/private"}`},
	} {
		if _, err := executor.Execute(t.Context(), tc.name, json.RawMessage(tc.args)); err == nil {
			t.Fatalf("accepted invalid knowledge args %s", tc.args)
		}
	}
}
