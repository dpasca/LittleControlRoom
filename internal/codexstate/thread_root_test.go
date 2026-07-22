package codexstate

import (
	"database/sql"
	"os"
	"path/filepath"
	"strings"
	"testing"

	_ "modernc.org/sqlite"
)

func TestThreadRootIDUsesSessionTreeMetadata(t *testing.T) {
	codexHome := t.TempDir()
	rolloutPath := filepath.Join(codexHome, "sessions", "rollout-child.jsonl")
	if err := os.MkdirAll(filepath.Dir(rolloutPath), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(rolloutPath, []byte(`{"type":"session_meta","payload":{"session_id":"thread-root","id":"thread-child","parent_thread_id":"thread-root"}}`+"\n"), 0o600); err != nil {
		t.Fatal(err)
	}

	db, err := sql.Open("sqlite", filepath.Join(codexHome, "state_5.sqlite"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	if _, err := db.Exec(`CREATE TABLE threads (id TEXT PRIMARY KEY, rollout_path TEXT NOT NULL)`); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(`INSERT INTO threads(id, rollout_path) VALUES (?, ?)`, "thread-child", rolloutPath); err != nil {
		t.Fatal(err)
	}

	rootID, err := ThreadRootID(codexHome, "thread-child")
	if err != nil {
		t.Fatalf("ThreadRootID() error = %v", err)
	}
	if rootID != "thread-root" {
		t.Fatalf("ThreadRootID() = %q, want thread-root", rootID)
	}
}

func TestThreadRootIDKeepsLegacyThreadID(t *testing.T) {
	codexHome := t.TempDir()
	rolloutPath := filepath.Join(codexHome, "sessions", "rollout-root.jsonl")
	if err := os.MkdirAll(filepath.Dir(rolloutPath), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(rolloutPath, []byte(`{"type":"session_meta","payload":{"id":"thread-root","source":"cli"}}`+"\n"), 0o600); err != nil {
		t.Fatal(err)
	}

	db, err := sql.Open("sqlite", filepath.Join(codexHome, "state_5.sqlite"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	if _, err := db.Exec(`CREATE TABLE threads (id TEXT PRIMARY KEY, rollout_path TEXT NOT NULL)`); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(`INSERT INTO threads(id, rollout_path) VALUES (?, ?)`, "thread-root", rolloutPath); err != nil {
		t.Fatal(err)
	}

	rootID, err := ThreadRootID(codexHome, "thread-root")
	if err != nil {
		t.Fatalf("ThreadRootID() error = %v", err)
	}
	if rootID != "thread-root" {
		t.Fatalf("ThreadRootID() = %q, want thread-root", rootID)
	}
}

func TestRolloutIsRootThreadRejectsForkedSubagents(t *testing.T) {
	dir := t.TempDir()
	tests := []struct {
		name string
		line string
		root bool
	}{
		{
			name: "root",
			line: `{"type":"session_meta","payload":{"id":"thread-demo","source":"cli"}}`,
			root: true,
		},
		{
			name: "session tree child",
			line: `{"type":"session_meta","payload":{"id":"thread-demo","session_id":"thread-root"}}`,
		},
		{
			name: "thread spawn child",
			line: `{"type":"session_meta","payload":{"id":"thread-demo","source":{"subagent":{"thread_spawn":{"parent_thread_id":"thread-root"}}}}}`,
		},
		{
			name: "legacy fork child",
			line: `{"type":"session_meta","payload":{"id":"thread-demo","forked_from_id":"thread-root","agent_role":"worker"}}`,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			path := filepath.Join(dir, strings.ReplaceAll(tt.name, " ", "-")+".jsonl")
			if err := os.WriteFile(path, []byte(tt.line+"\n"), 0o600); err != nil {
				t.Fatal(err)
			}
			got, err := RolloutIsRootThread(path, "thread-demo")
			if err != nil {
				t.Fatalf("RolloutIsRootThread() error = %v", err)
			}
			if got != tt.root {
				t.Fatalf("RolloutIsRootThread() = %t, want %t", got, tt.root)
			}
		})
	}
}
