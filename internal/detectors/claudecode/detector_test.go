package claudecode

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"slices"
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

func TestDetectPendingBackgroundTaskKeepsClaudeSessionInProgress(t *testing.T) {
	root := t.TempDir()
	t.Setenv("TMPDIR", root)

	projectPath := filepath.Join(root, "bg-project")
	if err := os.MkdirAll(projectPath, 0o755); err != nil {
		t.Fatal(err)
	}

	claudeHome := filepath.Join(root, ".claude")
	sessionID := "background-session-001"
	sessionFile, encodedPath := createClaudeProjectDirs(t, claudeHome, projectPath, sessionID)
	ts := time.Date(2026, 3, 29, 16, 30, 30, 0, time.UTC)
	taskID := "bsokqntg8"

	writeJSONLines(t, sessionFile, []map[string]any{
		{
			"type":      "user",
			"sessionId": sessionID,
			"cwd":       projectPath,
			"timestamp": ts.Format(time.RFC3339Nano),
			"uuid":      "u1",
			"message":   map[string]any{"role": "user", "content": "start training"},
		},
		{
			"type":      "assistant",
			"sessionId": sessionID,
			"cwd":       projectPath,
			"timestamp": ts.Add(3 * time.Second).Format(time.RFC3339Nano),
			"uuid":      "a1",
			"message": map[string]any{
				"role": "assistant",
				"content": []map[string]any{
					{
						"type":  "tool_use",
						"id":    "toolu_1",
						"name":  "Bash",
						"input": map[string]any{"command": "train", "run_in_background": true},
					},
				},
			},
		},
		{
			"type":      "user",
			"sessionId": sessionID,
			"cwd":       projectPath,
			"timestamp": ts.Add(4 * time.Second).Format(time.RFC3339Nano),
			"uuid":      "u2",
			"message": map[string]any{
				"role": "user",
				"content": []map[string]any{
					{
						"type":        "tool_result",
						"tool_use_id": "toolu_1",
						"content":     "Command running in background",
						"is_error":    false,
					},
				},
			},
			"toolUseResult": map[string]any{
				"stdout":           "",
				"stderr":           "",
				"backgroundTaskId": taskID,
			},
		},
		{
			"type":      "assistant",
			"sessionId": sessionID,
			"cwd":       projectPath,
			"timestamp": ts.Add(8 * time.Second).Format(time.RFC3339Nano),
			"uuid":      "a2",
			"message": map[string]any{
				"role": "assistant",
				"content": []map[string]any{
					{"type": "text", "text": "Training running. I'll report when it finishes."},
				},
			},
		},
		{
			"type":      "system",
			"subtype":   "turn_duration",
			"sessionId": sessionID,
			"cwd":       projectPath,
			"timestamp": ts.Add(9 * time.Second).Format(time.RFC3339Nano),
			"uuid":      "s1",
		},
	})
	setModTime(t, sessionFile, ts.Add(9*time.Second))

	taskFile := filepath.Join(root, "claude-501", encodedPath, sessionID, "tasks", taskID+".output")
	if err := os.MkdirAll(filepath.Dir(taskFile), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(taskFile, []byte("step 1\nstep 2\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	taskMTime := ts.Add(12 * time.Minute)
	setModTime(t, taskFile, taskMTime)

	d := New(claudeHome)
	results, err := d.Detect(context.Background(), scanner.NewPathScope([]string{root}, nil))
	if err != nil {
		t.Fatalf("Detect: %v", err)
	}

	entry := results[projectPath]
	if entry == nil {
		t.Fatalf("expected project %s in results", projectPath)
	}
	if len(entry.Sessions) != 1 {
		t.Fatalf("expected 1 session, got %d", len(entry.Sessions))
	}
	sess := entry.Sessions[0]
	if !sess.LatestTurnStateKnown {
		t.Fatalf("expected LatestTurnStateKnown = true")
	}
	if sess.LatestTurnCompleted {
		t.Fatalf("expected LatestTurnCompleted = false while background task is pending")
	}
	if !sess.LastEventAt.Equal(taskMTime) {
		t.Fatalf("LastEventAt = %s, want %s from task output mtime", sess.LastEventAt, taskMTime)
	}
}

func TestDetectCompletedBackgroundTaskRestoresCompletedTurnState(t *testing.T) {
	root := t.TempDir()
	t.Setenv("TMPDIR", root)

	projectPath := filepath.Join(root, "done-project")
	if err := os.MkdirAll(projectPath, 0o755); err != nil {
		t.Fatal(err)
	}

	claudeHome := filepath.Join(root, ".claude")
	sessionID := "background-session-002"
	sessionFile, encodedPath := createClaudeProjectDirs(t, claudeHome, projectPath, sessionID)
	ts := time.Date(2026, 3, 29, 17, 13, 32, 0, time.UTC)
	taskID := "done12345"
	notification := "<task-notification>\n<task-id>" + taskID + "</task-id>\n<status>completed</status>\n</task-notification>"

	writeJSONLines(t, sessionFile, []map[string]any{
		{
			"type":      "assistant",
			"sessionId": sessionID,
			"cwd":       projectPath,
			"timestamp": ts.Add(-10 * time.Second).Format(time.RFC3339Nano),
			"uuid":      "a1",
			"message": map[string]any{
				"role": "assistant",
				"content": []map[string]any{
					{
						"type":  "tool_use",
						"id":    "toolu_1",
						"name":  "Bash",
						"input": map[string]any{"command": "train", "run_in_background": true},
					},
				},
			},
		},
		{
			"type":      "user",
			"sessionId": sessionID,
			"cwd":       projectPath,
			"timestamp": ts.Add(-9 * time.Second).Format(time.RFC3339Nano),
			"uuid":      "u1",
			"message": map[string]any{
				"role": "user",
				"content": []map[string]any{
					{"type": "tool_result", "tool_use_id": "toolu_1", "content": "Command running in background", "is_error": false},
				},
			},
			"toolUseResult": map[string]any{
				"backgroundTaskId": taskID,
			},
		},
		{
			"type":      "queue-operation",
			"operation": "enqueue",
			"sessionId": sessionID,
			"timestamp": ts.Format(time.RFC3339Nano),
			"content":   notification,
		},
		{
			"type":      "user",
			"sessionId": sessionID,
			"cwd":       projectPath,
			"timestamp": ts.Add(2 * time.Second).Format(time.RFC3339Nano),
			"uuid":      "u2",
			"origin":    map[string]any{"kind": "task-notification"},
			"message":   map[string]any{"role": "user", "content": notification},
		},
		{
			"type":      "assistant",
			"sessionId": sessionID,
			"cwd":       projectPath,
			"timestamp": ts.Add(6 * time.Second).Format(time.RFC3339Nano),
			"uuid":      "a2",
			"message": map[string]any{
				"role": "assistant",
				"content": []map[string]any{
					{"type": "text", "text": "Training finished successfully."},
				},
			},
		},
		{
			"type":      "system",
			"subtype":   "turn_duration",
			"sessionId": sessionID,
			"cwd":       projectPath,
			"timestamp": ts.Add(7 * time.Second).Format(time.RFC3339Nano),
			"uuid":      "s1",
		},
	})
	setModTime(t, sessionFile, ts.Add(7*time.Second))

	taskFile := filepath.Join(root, "claude-501", encodedPath, sessionID, "tasks", taskID+".output")
	if err := os.MkdirAll(filepath.Dir(taskFile), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(taskFile, []byte("complete\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	setModTime(t, taskFile, ts.Add(1*time.Second))

	d := New(claudeHome)
	results, err := d.Detect(context.Background(), scanner.NewPathScope([]string{root}, nil))
	if err != nil {
		t.Fatalf("Detect: %v", err)
	}

	entry := results[projectPath]
	if entry == nil {
		t.Fatalf("expected project %s in results", projectPath)
	}
	sess := entry.Sessions[0]
	if !sess.LatestTurnStateKnown {
		t.Fatalf("expected LatestTurnStateKnown = true")
	}
	if !sess.LatestTurnCompleted {
		t.Fatalf("expected LatestTurnCompleted = true after completion notification and final turn")
	}
	if !sess.LastEventAt.Equal(ts.Add(7 * time.Second)) {
		t.Fatalf("LastEventAt = %s, want %s", sess.LastEventAt, ts.Add(7*time.Second))
	}
}

func TestDetectUsesSubagentActivityForIncompleteParentTurn(t *testing.T) {
	root := t.TempDir()
	t.Setenv("TMPDIR", root)

	projectPath := filepath.Join(root, "agent-project")
	if err := os.MkdirAll(projectPath, 0o755); err != nil {
		t.Fatal(err)
	}

	claudeHome := filepath.Join(root, ".claude")
	sessionID := "agent-session-001"
	sessionFile, _ := createClaudeProjectDirs(t, claudeHome, projectPath, sessionID)
	ts := time.Date(2026, 3, 29, 12, 45, 23, 0, time.UTC)

	writeJSONLines(t, sessionFile, []map[string]any{
		{
			"type":      "user",
			"sessionId": sessionID,
			"cwd":       projectPath,
			"timestamp": ts.Format(time.RFC3339Nano),
			"uuid":      "u1",
			"message":   map[string]any{"role": "user", "content": "launch async agent"},
		},
		{
			"type":      "assistant",
			"sessionId": sessionID,
			"cwd":       projectPath,
			"timestamp": ts.Add(2 * time.Second).Format(time.RFC3339Nano),
			"uuid":      "a1",
			"message": map[string]any{
				"role": "assistant",
				"content": []map[string]any{
					{
						"type":  "tool_use",
						"id":    "toolu_1",
						"name":  "Agent",
						"input": map[string]any{"description": "Explore repo"},
					},
				},
			},
		},
	})
	setModTime(t, sessionFile, ts.Add(2*time.Second))

	subagentFile := filepath.Join(filepath.Dir(sessionFile), sessionID, "subagents", "agent-abc123.jsonl")
	if err := os.MkdirAll(filepath.Dir(subagentFile), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(subagentFile, []byte("{}\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	subagentMTime := ts.Add(11 * time.Minute)
	setModTime(t, subagentFile, subagentMTime)

	d := New(claudeHome)
	results, err := d.Detect(context.Background(), scanner.NewPathScope([]string{root}, nil))
	if err != nil {
		t.Fatalf("Detect: %v", err)
	}

	entry := results[projectPath]
	if entry == nil {
		t.Fatalf("expected project %s in results", projectPath)
	}
	sess := entry.Sessions[0]
	if !sess.LatestTurnStateKnown {
		t.Fatalf("expected LatestTurnStateKnown = true")
	}
	if sess.LatestTurnCompleted {
		t.Fatalf("expected LatestTurnCompleted = false while parent is waiting on subagent")
	}
	if !sess.LastEventAt.Equal(subagentMTime) {
		t.Fatalf("LastEventAt = %s, want %s from subagent activity", sess.LastEventAt, subagentMTime)
	}
}

func TestDetectRefreshesCachedSessionWhenAuxiliaryArtifactsChange(t *testing.T) {
	root := t.TempDir()
	t.Setenv("TMPDIR", root)

	projectPath := filepath.Join(root, "cache-project")
	if err := os.MkdirAll(projectPath, 0o755); err != nil {
		t.Fatal(err)
	}

	claudeHome := filepath.Join(root, ".claude")
	sessionID := "cache-session-001"
	sessionFile, encodedPath := createClaudeProjectDirs(t, claudeHome, projectPath, sessionID)
	ts := time.Date(2026, 3, 29, 13, 56, 55, 0, time.UTC)
	taskID := "cachetask1"

	writeJSONLines(t, sessionFile, []map[string]any{
		{
			"type":      "assistant",
			"sessionId": sessionID,
			"cwd":       projectPath,
			"timestamp": ts.Format(time.RFC3339Nano),
			"uuid":      "a1",
			"message": map[string]any{
				"role": "assistant",
				"content": []map[string]any{
					{"type": "tool_use", "id": "toolu_1", "name": "Bash", "input": map[string]any{"command": "train", "run_in_background": true}},
				},
			},
		},
		{
			"type":      "user",
			"sessionId": sessionID,
			"cwd":       projectPath,
			"timestamp": ts.Add(1 * time.Second).Format(time.RFC3339Nano),
			"uuid":      "u1",
			"message": map[string]any{
				"role": "user",
				"content": []map[string]any{
					{"type": "tool_result", "tool_use_id": "toolu_1", "content": "Command running in background", "is_error": false},
				},
			},
			"toolUseResult": map[string]any{
				"backgroundTaskId": taskID,
			},
		},
		{
			"type":      "assistant",
			"sessionId": sessionID,
			"cwd":       projectPath,
			"timestamp": ts.Add(2 * time.Second).Format(time.RFC3339Nano),
			"uuid":      "a2",
			"message":   map[string]any{"role": "assistant", "content": []map[string]any{{"type": "text", "text": "Training running."}}},
		},
		{
			"type":      "system",
			"subtype":   "turn_duration",
			"sessionId": sessionID,
			"cwd":       projectPath,
			"timestamp": ts.Add(3 * time.Second).Format(time.RFC3339Nano),
			"uuid":      "s1",
		},
	})
	setModTime(t, sessionFile, ts.Add(3*time.Second))

	taskFile := filepath.Join(root, "claude-501", encodedPath, sessionID, "tasks", taskID+".output")
	if err := os.MkdirAll(filepath.Dir(taskFile), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(taskFile, []byte("first\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	firstTaskMTime := ts.Add(8 * time.Minute)
	setModTime(t, taskFile, firstTaskMTime)

	d := New(claudeHome)
	scope := scanner.NewPathScope([]string{root}, nil)
	first, err := d.Detect(context.Background(), scope)
	if err != nil {
		t.Fatalf("first Detect: %v", err)
	}
	if got := first[projectPath].Sessions[0].LastEventAt; !got.Equal(firstTaskMTime) {
		t.Fatalf("first LastEventAt = %s, want %s", got, firstTaskMTime)
	}

	secondTaskMTime := ts.Add(16 * time.Minute)
	if err := os.WriteFile(taskFile, []byte("second\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	setModTime(t, taskFile, secondTaskMTime)

	second, err := d.Detect(context.Background(), scope)
	if err != nil {
		t.Fatalf("second Detect: %v", err)
	}
	if got := second[projectPath].Sessions[0].LastEventAt; !got.Equal(secondTaskMTime) {
		t.Fatalf("second LastEventAt = %s, want %s after cache refresh", got, secondTaskMTime)
	}
}

func TestClaudeTaskRootCandidatesIncludePrivateTmpFallback(t *testing.T) {
	root := t.TempDir()
	customTmp := filepath.Join(root, "custom-tmp")
	if err := os.MkdirAll(customTmp, 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("TMPDIR", customTmp)

	roots := claudeTaskRootCandidates()
	if !slices.Contains(roots, filepath.Clean(customTmp)) {
		t.Fatalf("expected claude task roots to include TMPDIR %q, got %v", customTmp, roots)
	}
	if !slices.Contains(roots, "/private/tmp") {
		t.Fatalf("expected claude task roots to include /private/tmp fallback, got %v", roots)
	}
}

func createClaudeProjectDirs(t *testing.T, claudeHome, projectPath, sessionID string) (sessionFile string, encodedPath string) {
	t.Helper()
	encodedPath = encodeCCProjectPath(projectPath)
	projectDir := filepath.Join(claudeHome, "projects", encodedPath)
	if err := os.MkdirAll(projectDir, 0o755); err != nil {
		t.Fatal(err)
	}
	return filepath.Join(projectDir, sessionID+".jsonl"), encodedPath
}

func writeJSONLines(t *testing.T, path string, lines []map[string]any) {
	t.Helper()
	f, err := os.Create(path)
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()

	enc := json.NewEncoder(f)
	for _, line := range lines {
		if err := enc.Encode(line); err != nil {
			t.Fatal(err)
		}
	}
}

func setModTime(t *testing.T, path string, when time.Time) {
	t.Helper()
	if err := os.Chtimes(path, when, when); err != nil {
		t.Fatal(err)
	}
}
