package opencodesqlite

import (
	"context"
	"database/sql"
	"path/filepath"
	"testing"
	"time"
)

func TestOpenConfiguresBusyTimeoutAndQueryOnly(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	dbPath := filepath.Join(t.TempDir(), "opencode.db")

	seed, err := sql.Open("sqlite", dbPath)
	if err != nil {
		t.Fatalf("open seed db: %v", err)
	}
	if _, err := seed.ExecContext(ctx, `CREATE TABLE demo(id TEXT PRIMARY KEY);`); err != nil {
		_ = seed.Close()
		t.Fatalf("seed db: %v", err)
	}
	if err := seed.Close(); err != nil {
		t.Fatalf("close seed db: %v", err)
	}

	db, err := Open(dbPath)
	if err != nil {
		t.Fatalf("open helper db: %v", err)
	}
	defer db.Close()

	var busy int
	if err := db.QueryRowContext(ctx, `PRAGMA busy_timeout`).Scan(&busy); err != nil {
		t.Fatalf("query busy_timeout: %v", err)
	}
	if busy != int(busyTimeout/time.Millisecond) {
		t.Fatalf("busy_timeout = %d, want %d", busy, busyTimeout/time.Millisecond)
	}

	var queryOnly int
	if err := db.QueryRowContext(ctx, `PRAGMA query_only`).Scan(&queryOnly); err != nil {
		t.Fatalf("query query_only: %v", err)
	}
	if queryOnly != 1 {
		t.Fatalf("query_only = %d, want 1", queryOnly)
	}
}
