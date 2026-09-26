package claudecode

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"

	"lcroom/internal/claudeartifact"
	"lcroom/internal/model"
	"lcroom/internal/scanner"
)

func TestDetectSubagentWorktreesHaveIndependentIdentityAndActivity(t *testing.T) {
	root := t.TempDir()
	parentCWD := filepath.Join(root, "repo")
	home := filepath.Join(root, "claude")
	projectDir := filepath.Join(home, "projects", "encoded-parent")
	if err := os.MkdirAll(filepath.Join(projectDir, "parent", "subagents"), 0o755); err != nil {
		t.Fatal(err)
	}
	start := time.Date(2026, 9, 26, 12, 0, 0, 0, time.UTC)
	write := func(path, cwd, agentID, stop string, at time.Time) {
		t.Helper()
		writeJSONLines(t, path, []map[string]any{
			{"type": "user", "sessionId": "parent", "agentId": agentID, "isSidechain": agentID != "", "cwd": cwd, "timestamp": start.Format(time.RFC3339Nano), "message": map[string]any{"role": "user", "content": "Implement the assigned task"}},
			{"type": "assistant", "sessionId": "parent", "agentId": agentID, "isSidechain": agentID != "", "cwd": cwd, "timestamp": at.Format(time.RFC3339Nano), "message": map[string]any{"role": "assistant", "stop_reason": stop, "content": []map[string]any{{"type": "text", "text": "Task progress"}}}},
		})
		setModTime(t, path, at)
	}
	parentFile := filepath.Join(projectDir, "parent.jsonl")
	write(parentFile, parentCWD, "", "tool_use", start)
	paths := []string{filepath.Join(parentCWD, ".claude", "worktrees", "agent-a"), filepath.Join(root, "custom-worktree-b"), filepath.Join(root, "custom-worktree-c")}
	ids := []string{"a", "b", "c"}
	for i, path := range paths {
		stop := "tool_use"
		if i == 1 {
			stop = "end_turn"
		}
		write(filepath.Join(projectDir, "parent", "subagents", "agent-"+ids[i]+".jsonl"), path, ids[i], stop, start.Add(time.Duration(i+1)*time.Second))
	}
	write(filepath.Join(projectDir, "parent", "subagents", "agent-helper.jsonl"), parentCWD, "helper", "end_turn", start.Add(4*time.Second))
	// A path resembling a worktree, without structured child identity, is not evidence.
	write(filepath.Join(projectDir, "parent", "subagents", "agent-invalid.jsonl"), filepath.Join(root, "unrelated"), "", "tool_use", start)

	d := New(home)
	results, err := d.Detect(context.Background(), scanner.NewPathScope([]string{root}, nil))
	if err != nil {
		t.Fatal(err)
	}
	if len(results) != 4 || len(results[parentCWD].Sessions) != 1 || results[parentCWD].Sessions[0].ExternalID() != "parent" {
		t.Fatalf("parent or worktree ownership was lost: %#v", results)
	}
	for i, path := range paths {
		entry := results[path]
		if entry == nil || len(entry.Sessions) != 1 {
			t.Fatalf("missing child at %s: %#v", path, entry)
		}
		session := entry.Sessions[0]
		if session.Source != model.SessionSourceClaudeCode || session.ExternalID() != claudeartifact.SubagentSessionID("parent", ids[i]) || session.DetectedProjectPath != path {
			t.Fatalf("wrong child identity: %#v", session)
		}
		if !session.LatestTurnStateKnown || session.LatestTurnCompleted != (i == 1) || !session.LastEventAt.Equal(start.Add(time.Duration(i+1)*time.Second)) {
			t.Fatalf("child activity leaked between siblings: %#v", session)
		}
		if entry.Artifacts[0].Kind != "claude_code_subagent_jsonl" {
			t.Fatalf("missing subagent provenance: %#v", entry.Artifacts)
		}
		if _, _, ok := d.SessionFileForProject(path); ok {
			t.Fatal("delegated transcript must not be returned as a resumable CLI conversation")
		}
	}
	// Cache invalidation must retain sub-second changes even when the parent is quiet.
	updatedAt := start.Add(time.Second + 100*time.Millisecond)
	write(filepath.Join(projectDir, "parent", "subagents", "agent-a.jsonl"), paths[0], "a", "end_turn", updatedAt)
	scoped, err := d.Detect(context.Background(), scanner.NewPathScope([]string{paths[0]}, nil))
	if err != nil {
		t.Fatal(err)
	}
	if len(scoped) != 1 || !scoped[paths[0]].Sessions[0].LatestTurnCompleted || !scoped[paths[0]].LastActivity.Equal(updatedAt) {
		t.Fatalf("child-only scope or cache refresh failed: %#v", scoped)
	}
}
