package claudecode

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
	"time"

	"lcroom/internal/scanner"
)

func TestDetectFindsSessionFromJSONL(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	projectPath := filepath.Join(root, "myproject")
	if err := os.MkdirAll(projectPath, 0o755); err != nil {
		t.Fatal(err)
	}

	claudeHome := filepath.Join(root, ".claude")
	encodedPath := encodeCCProjectPath(projectPath)
	projectDir := filepath.Join(claudeHome, "projects", encodedPath)
	if err := os.MkdirAll(projectDir, 0o755); err != nil {
		t.Fatal(err)
	}

	sessionID := "test-session-001"
	ts := time.Date(2026, 3, 25, 12, 0, 0, 0, time.UTC)
	lines := []map[string]any{
		{
			"type":      "user",
			"sessionId": sessionID,
			"cwd":       projectPath,
			"timestamp": ts.Format(time.RFC3339Nano),
			"uuid":      "u1",
			"message":   map[string]any{"role": "user", "content": "hello"},
		},
		{
			"type":      "assistant",
			"sessionId": sessionID,
			"cwd":       projectPath,
			"timestamp": ts.Add(5 * time.Second).Format(time.RFC3339Nano),
			"uuid":      "a1",
			"message": map[string]any{
				"role": "assistant",
				"content": []map[string]any{
					{"type": "text", "text": "Hi there!"},
				},
			},
		},
		{
			"type":      "system",
			"subtype":   "turn_duration",
			"sessionId": sessionID,
			"cwd":       projectPath,
			"timestamp": ts.Add(10 * time.Second).Format(time.RFC3339Nano),
			"uuid":      "s1",
		},
	}

	sessionFile := filepath.Join(projectDir, sessionID+".jsonl")
	f, err := os.Create(sessionFile)
	if err != nil {
		t.Fatal(err)
	}
	enc := json.NewEncoder(f)
	for _, line := range lines {
		if err := enc.Encode(line); err != nil {
			t.Fatal(err)
		}
	}
	f.Close()

	d := New(claudeHome)
	ctx := context.Background()
	scope := scanner.NewPathScope([]string{root}, nil)

	results, err := d.Detect(ctx, scope)
	if err != nil {
		t.Fatalf("Detect: %v", err)
	}

	entry, ok := results[projectPath]
	if !ok {
		t.Fatalf("expected project %s in results, got %v", projectPath, results)
	}

	if entry.Source != "claude_code" {
		t.Errorf("source = %q, want %q", entry.Source, "claude_code")
	}
	if len(entry.Sessions) != 1 {
		t.Fatalf("expected 1 session, got %d", len(entry.Sessions))
	}
	sess := entry.Sessions[0]
	if sess.SessionID != sessionID {
		t.Errorf("session ID = %q, want %q", sess.SessionID, sessionID)
	}
	if sess.Format != "claude_code" {
		t.Errorf("format = %q, want %q", sess.Format, "claude_code")
	}
	if !sess.LatestTurnStateKnown {
		t.Error("expected LatestTurnStateKnown = true")
	}
	if !sess.LatestTurnCompleted {
		t.Error("expected LatestTurnCompleted = true (system/turn_duration was last)")
	}
}

func TestDetectActiveSessionOverridesTurnCompletion(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	projectPath := filepath.Join(root, "activeproject")
	if err := os.MkdirAll(projectPath, 0o755); err != nil {
		t.Fatal(err)
	}

	claudeHome := filepath.Join(root, ".claude")
	encodedPath := encodeCCProjectPath(projectPath)
	projectDir := filepath.Join(claudeHome, "projects", encodedPath)
	sessionsDir := filepath.Join(claudeHome, "sessions")
	for _, dir := range []string{projectDir, sessionsDir} {
		if err := os.MkdirAll(dir, 0o755); err != nil {
			t.Fatal(err)
		}
	}

	sessionID := "active-session-001"
	ts := time.Date(2026, 3, 25, 12, 0, 0, 0, time.UTC)

	// Write a session JSONL that looks completed (ends with system entry).
	lines := []map[string]any{
		{
			"type":      "user",
			"sessionId": sessionID,
			"cwd":       projectPath,
			"timestamp": ts.Format(time.RFC3339Nano),
			"uuid":      "u1",
			"message":   map[string]any{"role": "user", "content": "do something"},
		},
		{
			"type":      "system",
			"subtype":   "turn_duration",
			"sessionId": sessionID,
			"cwd":       projectPath,
			"timestamp": ts.Add(10 * time.Second).Format(time.RFC3339Nano),
			"uuid":      "s1",
		},
	}

	sessionFile := filepath.Join(projectDir, sessionID+".jsonl")
	f, err := os.Create(sessionFile)
	if err != nil {
		t.Fatal(err)
	}
	enc := json.NewEncoder(f)
	for _, line := range lines {
		if err := enc.Encode(line); err != nil {
			t.Fatal(err)
		}
	}
	f.Close()

	// Write a PID session file pointing to our own PID (which is alive).
	pidFile := filepath.Join(sessionsDir, "99999999.json")
	pidData, _ := json.Marshal(map[string]any{
		"pid":       os.Getpid(), // Our own PID — guaranteed alive.
		"sessionId": sessionID,
		"cwd":       projectPath,
		"startedAt": ts.UnixMilli(),
	})
	if err := os.WriteFile(pidFile, pidData, 0o644); err != nil {
		t.Fatal(err)
	}

	d := New(claudeHome)
	results, err := d.Detect(context.Background(), scanner.NewPathScope([]string{root}, nil))
	if err != nil {
		t.Fatalf("Detect: %v", err)
	}

	entry, ok := results[projectPath]
	if !ok {
		t.Fatalf("expected project %s in results", projectPath)
	}
	sess := entry.Sessions[0]
	if !sess.LatestTurnStateKnown {
		t.Error("expected LatestTurnStateKnown = true")
	}
	if sess.LatestTurnCompleted {
		t.Error("expected LatestTurnCompleted = false when active PID session exists")
	}
}

func TestEncodeCCProjectPath(t *testing.T) {
	t.Parallel()

	tests := []struct {
		input string
		want  string
	}{
		{"/Users/davide/dev/repos/Foo", "-Users-davide-dev-repos-Foo"},
		{"/tmp/test", "-tmp-test"},
	}
	for _, tt := range tests {
		got := encodeCCProjectPath(tt.input)
		if got != tt.want {
			t.Errorf("encodeCCProjectPath(%q) = %q, want %q", tt.input, got, tt.want)
		}
	}
}
