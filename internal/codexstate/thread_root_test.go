package codexstate

import (
	"database/sql"
	"os"
	"path/filepath"
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
	if err := os.WriteFile(rolloutPath, []byte(`{"type":"session_meta","payload":{"id":"thread-root"}}`+"\n"), 0o600); err != nil {
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
