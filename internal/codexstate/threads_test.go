package codexstate

import (
	"context"
	"database/sql"
	"os"
	"path/filepath"
	"testing"
	"time"

	_ "modernc.org/sqlite"
)

func TestListThreadsReturnsRecoveryMetadata(t *testing.T) {
	codexHome := t.TempDir()
	db, err := sql.Open("sqlite", filepath.Join(codexHome, "state_5.sqlite"))
	if err != nil {
		t.Fatalf("open sqlite: %v", err)
	}
	if _, err := db.Exec(`
		CREATE TABLE threads (
			id TEXT PRIMARY KEY,
			rollout_path TEXT,
			cwd TEXT,
			title TEXT,
			git_sha TEXT,
			git_branch TEXT,
			git_origin_url TEXT,
			source TEXT,
			agent_role TEXT,
			archived INTEGER,
			has_user_event INTEGER,
			created_at INTEGER,
			updated_at INTEGER,
			recency_at_ms INTEGER
		)
	`); err != nil {
		t.Fatalf("create threads: %v", err)
	}
	started := time.Date(2026, 7, 20, 8, 0, 0, 0, time.UTC)
	active := started.Add(3 * time.Hour)
	rolloutPath := filepath.Join(codexHome, "sessions", "2026", "07", "20", "rollout-thread-demo.jsonl")
	if _, err := db.Exec(`
		INSERT INTO threads(
			id, rollout_path, cwd, title, git_sha, git_branch, git_origin_url,
			source, agent_role, archived, has_user_event, created_at, updated_at, recency_at_ms
		) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
	`,
		"thread-demo", rolloutPath, "/repos/demo--feature", " Restore the feature ", "abc123",
		"feature/demo", "git@example.com:demo.git", "cli", "", 1, 1,
		started.Unix(), started.Unix(), active.UnixMilli(),
	); err != nil {
		t.Fatalf("insert thread: %v", err)
	}
	if err := db.Close(); err != nil {
		t.Fatalf("close sqlite: %v", err)
	}

	threads, err := ListThreads(context.Background(), codexHome)
	if err != nil {
		t.Fatalf("ListThreads() error = %v", err)
	}
	if len(threads) != 1 {
		t.Fatalf("thread count = %d, want 1", len(threads))
	}
	got := threads[0]
	if got.ID != "thread-demo" || got.CWD != "/repos/demo--feature" || got.GitBranch != "feature/demo" || got.GitSHA != "abc123" {
		t.Fatalf("thread identity = %#v", got)
	}
	if got.Title != "Restore the feature" || got.GitOriginURL != "git@example.com:demo.git" || !got.Archived || !got.HasUserEvent {
		t.Fatalf("thread metadata = %#v", got)
	}
	if !got.StartedAt.Equal(started) || !got.LastActivity.Equal(active) {
		t.Fatalf("thread times = (%s, %s), want (%s, %s)", got.StartedAt, got.LastActivity, started, active)
	}
}

func TestListThreadsSupportsOlderOptionalColumnSet(t *testing.T) {
	codexHome := t.TempDir()
	db, err := sql.Open("sqlite", filepath.Join(codexHome, "state_5.sqlite"))
	if err != nil {
		t.Fatalf("open sqlite: %v", err)
	}
	if _, err := db.Exec(`CREATE TABLE threads (id TEXT PRIMARY KEY, cwd TEXT NOT NULL, updated_at INTEGER)`); err != nil {
		t.Fatalf("create minimal threads: %v", err)
	}
	updated := time.Date(2025, 12, 1, 2, 3, 4, 0, time.UTC)
	if _, err := db.Exec(`INSERT INTO threads(id, cwd, updated_at) VALUES (?, ?, ?)`, "thread-old", "/repos/demo--old", updated.Unix()); err != nil {
		t.Fatalf("insert minimal thread: %v", err)
	}
	if err := db.Close(); err != nil {
		t.Fatalf("close sqlite: %v", err)
	}

	threads, err := ListThreads(context.Background(), codexHome)
	if err != nil {
		t.Fatalf("ListThreads() error = %v", err)
	}
	if len(threads) != 1 || threads[0].ID != "thread-old" || !threads[0].LastActivity.Equal(updated) {
		t.Fatalf("threads = %#v", threads)
	}
}

func TestListThreadsWithoutStateDatabase(t *testing.T) {
	codexHome := t.TempDir()
	if err := os.MkdirAll(filepath.Join(codexHome, "sessions"), 0o755); err != nil {
		t.Fatalf("mkdir sessions: %v", err)
	}
	threads, err := ListThreads(context.Background(), codexHome)
	if err != nil {
		t.Fatalf("ListThreads() error = %v", err)
	}
	if len(threads) != 0 {
		t.Fatalf("threads = %#v, want empty", threads)
	}
}
