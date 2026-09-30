package claudeartifact

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func progressRecord(id, parent, typ string, at time.Time, message any) string {
	data, _ := json.Marshal(map[string]any{"type": typ, "timestamp": at, "isSidechain": true,
		"agentId": id, "sessionId": parent, "message": message})
	return string(data) + "\n"
}

func progressTool(description string) any {
	return map[string]any{"stop_reason": "tool_use", "content": []any{
		map[string]any{"type": "tool_use", "name": "Bash", "input": map[string]any{"description": description}},
	}}
}

func writeProgressFile(t *testing.T, path, text string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(text), 0o600); err != nil {
		t.Fatal(err)
	}
}

func TestSubagentProgressQuietParentAndIndependentChildRefresh(t *testing.T) {
	parent := filepath.Join(t.TempDir(), "parent.jsonl")
	dir := strings.TrimSuffix(parent, ".jsonl") + "/subagents"
	now := time.Now().UTC()
	since := now.Add(-5 * time.Hour)
	worker := filepath.Join(dir, "agent-worker.jsonl")
	icon := filepath.Join(dir, "agent-icon.jsonl")
	writeProgressFile(t, worker, progressRecord("worker", "parent", "assistant", now.Add(-time.Minute), progressTool("Compare graphics tiers")))
	writeProgressFile(t, strings.TrimSuffix(worker, ".jsonl")+".meta.json", `{"description":"Phone profiling","toolUseId":"launch-worker","requestShape":"foreground"}`)
	writeProgressFile(t, icon, progressRecord("icon", "parent", "assistant", since.Add(20*time.Minute), map[string]any{"stop_reason": "end_turn"}))
	writeProgressFile(t, filepath.Join(dir, "agent-old.jsonl"), progressRecord("old", "parent", "assistant", since.Add(-time.Hour), progressTool("Old task")))
	writeProgressFile(t, filepath.Join(dir, "agent-unrelated.jsonl"), progressRecord("unrelated", "other-parent", "assistant", now, progressTool("Wrong parent")))
	r := SubagentProgressReader{}
	got, err := r.Read(parent, "parent", since)
	if err != nil || len(got) != 2 {
		t.Fatalf("Read = %#v, %v", got, err)
	}
	if got[0].Description != "Phone profiling" || got[0].ToolUseID != "launch-worker" || got[0].LatestAction != "Bash: Compare graphics tiers" || got[0].State(now) != "active" || !got[1].Completed {
		t.Fatalf("missing child progress: %#v", got)
	}
	// Rewrite a child without changing its parent. mtime precision matters.
	writeProgressFile(t, worker, progressRecord("worker", "parent", "assistant", now, progressTool("Analyze results")))
	got, err = r.Read(parent, "parent", since)
	if err != nil || got[0].LatestAction != "Bash: Analyze results" {
		t.Fatalf("stale progress: %#v, %v", got, err)
	}
	// A cached child ages from the event timestamp, never from a queue write.
	file, err := os.OpenFile(worker, os.O_APPEND|os.O_WRONLY, 0o600)
	if err != nil {
		t.Fatal(err)
	}
	_, err = file.WriteString(progressRecord("worker", "parent", "queue-operation", now.Add(time.Hour), nil))
	file.Close()
	if err != nil {
		t.Fatal(err)
	}
	got, err = r.Read(parent, "parent", since)
	if err != nil || !got[0].UpdatedAt.Equal(now) || got[0].State(now.Add(time.Hour)) != "no recent activity" || got[0].Completed {
		t.Fatalf("queue bookkeeping revived worker: %#v, %v", got, err)
	}
	// Metadata arriving independently of log writes still updates its label.
	writeProgressFile(t, strings.TrimSuffix(worker, ".jsonl")+".meta.json", `{"description":"Updated task name"}`)
	got, _ = r.Read(parent, "parent", since)
	if got[0].Description != "Updated task name" {
		t.Fatalf("metadata stayed stale: %#v", got)
	}
	got, err = r.Read(parent, "parent", now.Add(time.Second))
	if err != nil || len(got) != 0 {
		t.Fatalf("old turn leaked: %#v, %v", got, err)
	}
}

func TestSubagentProgressBoundsLargeLogsAndIgnoresPartialRecords(t *testing.T) {
	parent := filepath.Join(t.TempDir(), "parent.jsonl")
	path := strings.TrimSuffix(parent, ".jsonl") + "/subagents/agent-worker.jsonl"
	now := time.Now().UTC()
	oversized := progressRecord("worker", "parent", "user", now.Add(-time.Second), map[string]any{"content": strings.Repeat("x", 2*subagentProgressTailBytes)})
	complete := progressRecord("worker", "parent", "assistant", now, progressTool("Measure phone"))
	partial := progressRecord("worker", "parent", "assistant", now.Add(time.Second), map[string]any{"stop_reason": "end_turn"})
	writeProgressFile(t, path, oversized+complete+strings.TrimSuffix(partial, "\n"))
	r := SubagentProgressReader{}
	got, err := r.Read(parent, "parent", now.Add(-time.Hour))
	if err != nil || len(got) != 1 || got[0].Completed || got[0].LatestAction != "Bash: Measure phone" {
		t.Fatalf("bounded read: %#v, %v", got, err)
	}
	writeProgressFile(t, path, oversized+complete+partial)
	got, err = r.Read(parent, "parent", now.Add(-time.Hour))
	if err != nil || len(got) != 1 || !got[0].Completed {
		t.Fatalf("completion: %#v, %v", got, err)
	}
	// If one oversized record fills the tail, keep the known child visible
	// with an explicit read limitation instead of silently dropping it.
	writeProgressFile(t, path, complete+oversized)
	got, err = r.Read(parent, "parent", now.Add(-time.Hour))
	if err != nil || len(got) != 1 || !got[0].Unavailable || got[0].State(now) != "activity unavailable" {
		t.Fatalf("lost child behind oversized record: %#v, %v", got, err)
	}
}

func TestSubagentProgressMissingAndUnreadableDirectory(t *testing.T) {
	parent := filepath.Join(t.TempDir(), "parent.jsonl")
	r := SubagentProgressReader{}
	if got, err := r.Read(parent, "parent", time.Now()); err != nil || len(got) != 0 {
		t.Fatalf("missing directory: %#v, %v", got, err)
	}
	writeProgressFile(t, strings.TrimSuffix(parent, ".jsonl")+"/subagents", "not a directory")
	if _, err := r.Read(parent, "parent", time.Now()); err == nil {
		t.Fatal("unreadable activity must surface an error")
	}
}
