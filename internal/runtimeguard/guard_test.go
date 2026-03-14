package runtimeguard

import (
	"path/filepath"
	"testing"
)

func TestAcquireBlocksSecondLeaseForSameDB(t *testing.T) {
	t.Parallel()

	dbPath := filepath.Join(t.TempDir(), "little-control-room.sqlite")
	first, owner, err := Acquire(dbPath, "tui")
	if err != nil {
		t.Fatalf("Acquire(first) error = %v", err)
	}
	if owner != nil {
		t.Fatalf("expected first acquire owner hint to be nil, got %+v", owner)
	}
	defer first.Close()

	second, owner, err := Acquire(dbPath, "serve")
	if err != ErrBusy {
		t.Fatalf("Acquire(second) error = %v, want ErrBusy", err)
	}
	if second != nil {
		t.Fatalf("expected second lease to be nil")
	}
	if owner == nil {
		t.Fatalf("expected owner hint for busy lease")
	}
	if owner.PID == 0 {
		t.Fatalf("expected owner PID to be recorded")
	}
	if owner.Mode != "tui" {
		t.Fatalf("owner mode = %q, want tui", owner.Mode)
	}
	if owner.DBPath != filepath.Clean(dbPath) {
		t.Fatalf("owner db path = %q, want %q", owner.DBPath, filepath.Clean(dbPath))
	}
}

func TestAcquireAllowsDifferentDBPaths(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	first, _, err := Acquire(filepath.Join(dir, "one.sqlite"), "tui")
	if err != nil {
		t.Fatalf("Acquire(first) error = %v", err)
	}
	defer first.Close()

	second, owner, err := Acquire(filepath.Join(dir, "two.sqlite"), "tui")
	if err != nil {
		t.Fatalf("Acquire(second) error = %v owner=%+v", err, owner)
	}
	defer second.Close()
}

func TestParseGuardedRuntimeCommand(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name     string
		command  string
		wantOK   bool
		wantMode string
		wantDB   string
	}{
		{
			name:     "compiled binary",
			command:  "/tmp/lcroom tui --db /tmp/demo.sqlite",
			wantOK:   true,
			wantMode: "tui",
			wantDB:   "/tmp/demo.sqlite",
		},
		{
			name:     "go run parent",
			command:  "go run ./cmd/lcroom classify --config /tmp/config.toml --db /tmp/demo.sqlite",
			wantOK:   true,
			wantMode: "classify",
			wantDB:   "/tmp/demo.sqlite",
		},
		{
			name:    "other command",
			command: "python app.py --db /tmp/demo.sqlite",
			wantOK:  false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			mode, dbPath, ok := parseGuardedRuntimeCommand(tt.command)
			if ok != tt.wantOK {
				t.Fatalf("parseGuardedRuntimeCommand() ok = %v, want %v", ok, tt.wantOK)
			}
			if !tt.wantOK {
				return
			}
			if mode != tt.wantMode {
				t.Fatalf("mode = %q, want %q", mode, tt.wantMode)
			}
			if dbPath != tt.wantDB {
				t.Fatalf("dbPath = %q, want %q", dbPath, tt.wantDB)
			}
		})
	}
}
