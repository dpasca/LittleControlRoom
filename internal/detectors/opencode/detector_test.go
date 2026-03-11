package opencode

import (
	"context"
	"database/sql"
	"os"
	"path/filepath"
	"testing"

	"lcroom/internal/scanner"

	_ "modernc.org/sqlite"
)

func TestDetectUsesMessagePartActivityForLastEventAt(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	root := t.TempDir()
	projectPath := filepath.Join(root, "demo")
	opencodeHome := filepath.Join(root, "opencode-home")
	dbPath := filepath.Join(opencodeHome, "opencode.db")

	if err := sqlSeedOpenCodeFixture(dbPath, projectPath); err != nil {
		t.Fatalf("seed opencode fixture: %v", err)
	}

	detected, err := New(opencodeHome).Detect(ctx, scanner.NewPathScope([]string{root}, nil))
	if err != nil {
		t.Fatalf("detect: %v", err)
	}

	entry, ok := detected[projectPath]
	if !ok {
		t.Fatalf("expected project %s in detection results: %#v", projectPath, detected)
	}
	if len(entry.Sessions) != 1 {
		t.Fatalf("expected 1 session, got %#v", entry.Sessions)
	}

	want := unixToTimeFlexible(220_000)
	if got := entry.Sessions[0].LastEventAt; !got.Equal(want) {
		t.Fatalf("last_event_at = %s, want %s", got, want)
	}
	if got := entry.LastActivity; !got.Equal(want) {
		t.Fatalf("last_activity = %s, want %s", got, want)
	}
}

func sqlSeedOpenCodeFixture(dbPath, projectPath string) error {
	if err := os.MkdirAll(filepath.Dir(dbPath), 0o755); err != nil {
		return err
	}

	db, err := sql.Open("sqlite", dbPath)
	if err != nil {
		return err
	}
	defer db.Close()
	db.SetMaxOpenConns(1)

	_, err = db.Exec(`
		CREATE TABLE project (
			id TEXT PRIMARY KEY,
			worktree TEXT NOT NULL
		);
		CREATE TABLE session (
			id TEXT PRIMARY KEY,
			project_id TEXT NOT NULL,
			directory TEXT NOT NULL,
			time_created INTEGER NOT NULL,
			time_updated INTEGER NOT NULL,
			time_archived INTEGER
		);
		CREATE TABLE message (
			id TEXT PRIMARY KEY,
			session_id TEXT NOT NULL,
			time_updated INTEGER NOT NULL
		);
		CREATE TABLE part (
			id TEXT PRIMARY KEY,
			session_id TEXT NOT NULL,
			time_updated INTEGER NOT NULL
		);
		INSERT INTO project(id, worktree) VALUES ('proj_1', ?);
		INSERT INTO session(id, project_id, directory, time_created, time_updated, time_archived)
		VALUES ('ses_1', 'proj_1', ?, 100000, 900000, NULL);
		INSERT INTO message(id, session_id, time_updated) VALUES ('msg_1', 'ses_1', 180000);
		INSERT INTO part(id, session_id, time_updated) VALUES ('part_1', 'ses_1', 220000);
	`, projectPath, projectPath)
	return err
}
