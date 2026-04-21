package codexstate

import (
	"database/sql"
	"os"
	"path/filepath"
	"testing"

	_ "modernc.org/sqlite"
)

func TestSanitizeStateRolloutPaths(t *testing.T) {
	dataDir := t.TempDir()
	sourceHome := filepath.Join(t.TempDir(), ".codex")
	if err := os.MkdirAll(filepath.Join(sourceHome, "sessions", "2026", "04", "21"), 0o755); err != nil {
		t.Fatalf("mkdir source sessions: %v", err)
	}
	if err := os.MkdirAll(filepath.Join(sourceHome, "archived_sessions"), 0o755); err != nil {
		t.Fatalf("mkdir archived sessions: %v", err)
	}

	db, err := sql.Open("sqlite", filepath.Join(sourceHome, "state_5.sqlite"))
	if err != nil {
		t.Fatalf("open sqlite: %v", err)
	}
	defer db.Close()
	if _, err := db.Exec(`CREATE TABLE threads (rollout_path TEXT NOT NULL)`); err != nil {
		t.Fatalf("create threads table: %v", err)
	}

	staleSessionPath := filepath.Join(dataDir, "internal-workspaces", "lcroom-codex-home-123456", "sessions", "2026", "04", "21", "rollout-thread-a.jsonl")
	staleArchivedPath := filepath.Join(dataDir, "internal-workspaces", "lcroom-codex-home-654321", "archived_sessions", "rollout-thread-b.jsonl")
	unrelatedPath := filepath.Join(t.TempDir(), "rollout-thread-c.jsonl")
	for _, rolloutPath := range []string{staleSessionPath, staleArchivedPath, unrelatedPath} {
		if _, err := db.Exec(`INSERT INTO threads(rollout_path) VALUES (?)`, rolloutPath); err != nil {
			t.Fatalf("insert rollout path %q: %v", rolloutPath, err)
		}
	}

	result, err := SanitizeStateRolloutPaths(sourceHome)
	if err != nil {
		t.Fatalf("SanitizeStateRolloutPaths() error = %v", err)
	}
	if result.UpdatedRows != 2 {
		t.Fatalf("updated rows = %d, want 2", result.UpdatedRows)
	}

	rows, err := db.Query(`SELECT rollout_path FROM threads ORDER BY rowid`)
	if err != nil {
		t.Fatalf("query rollout paths: %v", err)
	}
	defer rows.Close()

	var got []string
	for rows.Next() {
		var rolloutPath string
		if err := rows.Scan(&rolloutPath); err != nil {
			t.Fatalf("scan rollout path: %v", err)
		}
		got = append(got, rolloutPath)
	}
	if err := rows.Err(); err != nil {
		t.Fatalf("rows error: %v", err)
	}

	want := []string{
		filepath.Join(sourceHome, "sessions", "2026", "04", "21", "rollout-thread-a.jsonl"),
		filepath.Join(sourceHome, "archived_sessions", "rollout-thread-b.jsonl"),
		unrelatedPath,
	}
	if len(got) != len(want) {
		t.Fatalf("rollout path count = %d, want %d (%v)", len(got), len(want), got)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("rollout path %d = %q, want %q", i, got[i], want[i])
		}
	}
}

func TestResolveHomeRootResolvesOverlayBackToSourceHome(t *testing.T) {
	sourceHome := t.TempDir()
	if err := os.WriteFile(filepath.Join(sourceHome, "config.toml"), []byte("model = \"gpt-5\""), 0o644); err != nil {
		t.Fatalf("write config: %v", err)
	}
	overlay := filepath.Join(t.TempDir(), "overlay")
	if err := os.MkdirAll(overlay, 0o755); err != nil {
		t.Fatalf("mkdir overlay: %v", err)
	}
	if err := os.Symlink(filepath.Join(sourceHome, "config.toml"), filepath.Join(overlay, "config.toml")); err != nil {
		t.Fatalf("symlink config: %v", err)
	}

	if got := ResolveHomeRoot(overlay); got != sourceHome {
		t.Fatalf("ResolveHomeRoot() = %q, want %q", got, sourceHome)
	}
}

func TestNormalizeRolloutPathCanonicalizesOverlayPath(t *testing.T) {
	sourceHome := filepath.Join(t.TempDir(), ".codex")
	rollout := filepath.Join(t.TempDir(), "internal-workspaces", "lcroom-codex-home-7890", "sessions", "2026", "04", "21", "rollout.jsonl")

	got := NormalizeRolloutPath(sourceHome, rollout)
	want := filepath.Join(sourceHome, "sessions", "2026", "04", "21", "rollout.jsonl")
	if got != want {
		t.Fatalf("NormalizeRolloutPath() = %q, want %q", got, want)
	}
}
