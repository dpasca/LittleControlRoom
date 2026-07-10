package codexstate

import (
	"database/sql"
	"os"
	"path/filepath"
	"testing"

	_ "modernc.org/sqlite"
)

func TestThreadRolloutPath(t *testing.T) {
	codexHome := t.TempDir()
	db, err := sql.Open("sqlite", filepath.Join(codexHome, "state_5.sqlite"))
	if err != nil {
		t.Fatalf("open sqlite: %v", err)
	}
	if _, err := db.Exec(`CREATE TABLE threads (id TEXT PRIMARY KEY, rollout_path TEXT NOT NULL)`); err != nil {
		t.Fatalf("create threads table: %v", err)
	}
	rolloutPath := filepath.Join(codexHome, "sessions", "2026", "07", "10", "rollout-thread-demo.jsonl")
	if _, err := db.Exec(`INSERT INTO threads(id, rollout_path) VALUES (?, ?)`, "thread-demo", rolloutPath); err != nil {
		t.Fatalf("insert thread: %v", err)
	}
	if err := db.Close(); err != nil {
		t.Fatalf("close sqlite: %v", err)
	}

	got, err := ThreadRolloutPath(codexHome, "thread-demo")
	if err != nil {
		t.Fatalf("ThreadRolloutPath() error = %v", err)
	}
	if got != rolloutPath {
		t.Fatalf("ThreadRolloutPath() = %q, want %q", got, rolloutPath)
	}

	missing, err := ThreadRolloutPath(codexHome, "missing-thread")
	if err != nil {
		t.Fatalf("ThreadRolloutPath(missing) error = %v", err)
	}
	if missing != "" {
		t.Fatalf("ThreadRolloutPath(missing) = %q, want empty", missing)
	}
}

func TestThreadRolloutPathWithoutStateDatabase(t *testing.T) {
	codexHome := t.TempDir()
	if err := os.MkdirAll(filepath.Join(codexHome, "sessions"), 0o755); err != nil {
		t.Fatalf("mkdir sessions: %v", err)
	}

	got, err := ThreadRolloutPath(codexHome, "thread-demo")
	if err != nil {
		t.Fatalf("ThreadRolloutPath() error = %v", err)
	}
	if got != "" {
		t.Fatalf("ThreadRolloutPath() = %q, want empty", got)
	}
}
