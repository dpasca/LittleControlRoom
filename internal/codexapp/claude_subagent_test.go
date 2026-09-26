package codexapp

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"lcroom/internal/claudeartifact"
)

func TestClaudeSubagentViewerRefreshesAndRemainsReadOnly(t *testing.T) {
	home := t.TempDir()
	projectPath := filepath.Join(home, "worktree")
	path := filepath.Join(home, "projects", "parent-project", "parent", "subagents", "agent-worker.jsonl")
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	start := time.Now().Add(-time.Minute).UTC()
	write := func(stopReason string) {
		t.Helper()
		var data []byte
		for _, entry := range []map[string]any{
			{"type": "user", "cwd": projectPath, "sessionId": "parent", "agentId": "worker", "isSidechain": true, "timestamp": start.Format(time.RFC3339Nano), "message": map[string]any{"role": "user", "content": "Implement the task"}},
			{"type": "assistant", "timestamp": start.Add(time.Second).Format(time.RFC3339Nano), "message": map[string]any{"role": "assistant", "model": "claude-test-model", "stop_reason": stopReason, "content": []map[string]any{{"type": "text", "text": "Progress report"}}}},
		} {
			line, err := json.Marshal(entry)
			if err != nil {
				t.Fatal(err)
			}
			data = append(data, append(line, '\n')...)
		}
		if err := os.WriteFile(path, data, 0o600); err != nil {
			t.Fatal(err)
		}
	}
	write("tool_use")
	req := LaunchRequest{Provider: ProviderClaudeCode, ProjectPath: projectPath, ResumeID: claudeartifact.SubagentSessionID("parent", "worker")}
	opened, err := newClaudeSubagentSession(req, home, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer opened.Close()
	session := opened.(*claudeCodeSession)
	snapshot := session.Snapshot()
	if !snapshot.IsClaudeSubagent() || !snapshot.BusyExternal || !snapshot.Busy || snapshot.LatestTurnCompleted || !snapshot.BusySince.Equal(start) {
		t.Fatalf("unexpected child activity: %#v", snapshot)
	}
	if !strings.Contains(snapshot.Transcript, "Progress report") || !strings.Contains(snapshot.LastSystemNotice, "parent session parent") || snapshot.ReportedModel != "claude-test-model" {
		t.Fatalf("missing child transcript/provenance/model: %#v", snapshot)
	}
	if session.cmd != nil || session.approvalServer != nil || session.planUsageReader != nil {
		t.Fatal("viewing a subagent must not start provider or account integrations")
	}
	if snapshot.PermissionLevel != "" {
		t.Fatal("read-only observer cannot claim the external agent's permission mode")
	}
	if err := session.Submit("continue"); err == nil || !strings.Contains(err.Error(), "read-only") {
		t.Fatalf("subagent input must fail closed: %v", err)
	}
	// Same-length rewrites must refresh too (both stop reasons are eight bytes).
	write("end_turn")
	if err := os.Chtimes(path, start.Add(2*time.Second), start.Add(2*time.Second)); err != nil {
		t.Fatal(err)
	}
	if err := session.RefreshBusyElsewhere(); err != nil {
		t.Fatal(err)
	}
	snapshot = session.Snapshot()
	if snapshot.Busy || !snapshot.BusyExternal || !snapshot.LatestTurnCompleted || !strings.Contains(snapshot.Status, "completed") {
		t.Fatalf("completed child must stay read-only: %#v", snapshot)
	}
	if err := session.Submit("continue"); err == nil || !strings.Contains(err.Error(), "read-only") {
		t.Fatalf("completed subagent must never resume its parent: %v", err)
	}
	if err := session.Compact(); err == nil {
		t.Fatal("subagent compaction must be blocked")
	}
	if err := session.Interrupt(); err == nil {
		t.Fatal("subagent viewer must not stop the parent")
	}
	wrong := req
	wrong.ProjectPath = filepath.Join(home, "other-project")
	if _, err := newClaudeSubagentSession(wrong, home, nil); !errors.Is(err, ErrSessionChanged) {
		t.Fatalf("wrong-project resume must fail: %v", err)
	}
	withPrompt := req
	withPrompt.Prompt = "continue"
	if _, err := newClaudeSubagentSession(withPrompt, home, nil); err == nil {
		t.Fatal("launch with a prompt must fail")
	}
	if err := session.Close(); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(path); err != nil {
		t.Fatalf("closing the viewer altered the external transcript: %v", err)
	}
}
