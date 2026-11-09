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
	wantByTopic := map[string][]string{
		"submodule-worktrees": {"extensions.worktreeConfig=true", "config.worktree", "--git-dir", "shared repository config", "git.submodule_align", "fast-forward, backward, or diverged", "standing"},
		"director-coordination": {
			"reads its own owner-answer state directly",
			"conditional",
			"get_control_operation",
			"An explicit operator stop or cancellation holds",
			"`end_turn: true` on an operation you proposed with `resume_on_success`",
			"That is not a stop.",
			"A control operation that still needs operator confirmation holds",
			"stale exact target fails unless LCR holds a recorded",
			"original 24-hour expiry still applies",
			"LCR has no subscription on a project's board",
			"By itself it is not authorization for",
			"Who owns what",
			"engineer.board_watch_register",
			"it never fires for note edits",
			"Do not build a proxy consent",
			"cannot guarantee a wake during sleep",
			"keywords or names",
		},
		"task-merge-continue": {
			"commit exactly the accepted edits",
			"Commit inside each changed submodule worktree first",
			"`repository.handoff_content`",
			"descending from `repository.base_head`",
			"Any content the reviewer did not accept fails closed",
			"can be continued only in their exact uncommitted state",
			"Never commit in a checkout a worker currently holds",
		},
	}
	if len(topics) != len(wantByTopic) {
		t.Fatalf("topics = %#v, want exactly %d with expectations", topics, len(wantByTopic))
	}
	for _, topic := range topics {
		args, _ := json.Marshal(map[string]string{"topic": topic.ID})
		result, err := executor.Execute(t.Context(), QueryKnowledgeGet, args)
		if err != nil {
			t.Fatal(err)
		}
		if result["freshness"] != "built_in_documentation" || result["truncated"] != false {
			t.Fatalf("knowledge envelope = %#v", result)
		}
		normalized := strings.Join(strings.Fields(result["markdown"].(string)), " ")
		for _, want := range wantByTopic[topic.ID] {
			if !strings.Contains(normalized, want) {
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
		{QueryKnowledgeGet, `{"topic":"director-coordination","path":"/private"}`},
		{QueryKnowledgeGet, `{"topic":"Director-Coordination"}`},
	} {
		if _, err := executor.Execute(t.Context(), tc.name, json.RawMessage(tc.args)); err == nil {
			t.Fatalf("accepted invalid knowledge args %s", tc.args)
		}
	}
}

func TestKnowledgeInstructionsPointToDirectorCoordination(t *testing.T) {
	for _, want := range []string{
		"director-coordination",
		"saved answers and pending operation IDs are reconciled before resending",
		"explicit stops and required confirmations hold",
		"end_turn with a requested continuation is not a stop",
		"messaging or collaboration permission is not itself authorization",
		"task-merge-continue",
	} {
		if !strings.Contains(KnowledgeInstructions, want) {
			t.Fatalf("KnowledgeInstructions missing %q", want)
		}
	}
	if len(KnowledgeInstructions) > 1500 {
		t.Fatalf("KnowledgeInstructions grew to %d bytes; keep startup context short", len(KnowledgeInstructions))
	}
}
