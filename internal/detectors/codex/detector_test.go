package codex

import (
	"context"
	"database/sql"
	"os"
	"path/filepath"
	"testing"
	"time"

	"lcroom/internal/scanner"

	_ "modernc.org/sqlite"
)

func TestDetectorParsesModernAndLegacyFixtures(t *testing.T) {
	codexHome := filepath.Join("..", "..", "..", "testdata", "codex_footprint")
	d := New(codexHome)

	scope := scanner.NewPathScope([]string{"/workspaces/repos"}, nil)
	got, err := d.Detect(context.Background(), scope)
	if err != nil {
		t.Fatalf("Detect() error = %v", err)
	}

	if len(got) != 3 {
		t.Fatalf("expected 3 detected projects, got %d", len(got))
	}

	modernPath := "/workspaces/repos/LittleControlRoom"
	modern := got[modernPath]
	if modern == nil {
		t.Fatalf("missing modern project %s", modernPath)
	}
	if modern.ErrorCount != 1 {
		t.Fatalf("expected modern error_count=1, got %d", modern.ErrorCount)
	}
	if len(modern.Sessions) == 0 || modern.Sessions[0].Format != "modern" {
		t.Fatalf("expected modern session evidence, got %#v", modern.Sessions)
	}
	if !modern.Sessions[0].LatestTurnStateKnown || !modern.Sessions[0].LatestTurnCompleted {
		t.Fatalf("expected modern latest turn to be completed, got known=%v completed=%v", modern.Sessions[0].LatestTurnStateKnown, modern.Sessions[0].LatestTurnCompleted)
	}
	if !modern.Sessions[0].LatestTurnStartedAt.IsZero() {
		t.Fatalf("expected completed modern turn to clear turn start time, got %v", modern.Sessions[0].LatestTurnStartedAt)
	}

	legacyPath := "/workspaces/repos/legacy-demo"
	legacy := got[legacyPath]
	if legacy == nil {
		t.Fatalf("missing legacy project %s", legacyPath)
	}
	if len(legacy.Sessions) == 0 || legacy.Sessions[0].Format != "legacy" {
		t.Fatalf("expected legacy session evidence, got %#v", legacy.Sessions)
	}

	archivedPath := "/workspaces/repos/archived-demo"
	if got[archivedPath] == nil {
		t.Fatalf("missing archived project %s", archivedPath)
	}
}

func TestExtractLegacyCWD(t *testing.T) {
	text := `{"type":"message","content":[{"text":"<environment_context>\nCurrent working directory: /tmp/demo\nApproval policy: on-request\n</environment_context>"}]}`
	cwd := extractLegacyCWD(text)
	if cwd != "/tmp/demo" {
		t.Fatalf("extractLegacyCWD() = %q, want %q", cwd, "/tmp/demo")
	}
}

func TestCountNonZeroExitCodes(t *testing.T) {
	in := "Process exited with code 0\nProcess exited with code 1\nProcess exited with code 2"
	if got := countNonZeroExitCodes(in); got != 2 {
		t.Fatalf("countNonZeroExitCodes() = %d, want 2", got)
	}
}

func TestExtractTurnLifecycle(t *testing.T) {
	startLine := `{"timestamp":"2026-03-05T09:04:00.238Z","type":"event_msg","payload":{"type":"task_started"}}`
	event, ok := extractTurnLifecycle(startLine)
	if !ok || event.completed {
		t.Fatalf("task_started parse = (%#v, ok=%v), want completed=false, ok=true", event, ok)
	}
	if event.timestamp.IsZero() {
		t.Fatalf("task_started timestamp = zero, want parsed timestamp")
	}

	completeLine := `{"timestamp":"2026-03-05T09:04:10.657Z","type":"event_msg","payload":{"type":"task_complete"}}`
	event, ok = extractTurnLifecycle(completeLine)
	if !ok || !event.completed {
		t.Fatalf("task_complete parse = (%#v, ok=%v), want completed=true, ok=true", event, ok)
	}
	if event.timestamp.IsZero() {
		t.Fatalf("task_complete timestamp = zero, want parsed timestamp")
	}

	abortedLine := `{"timestamp":"2026-03-05T09:04:12.000Z","type":"event_msg","payload":{"type":"turn_aborted","reason":"interrupted"}}`
	event, ok = extractTurnLifecycle(abortedLine)
	if !ok || !event.completed {
		t.Fatalf("turn_aborted parse = (%#v, ok=%v), want completed=true, ok=true", event, ok)
	}
	if event.timestamp.IsZero() {
		t.Fatalf("turn_aborted timestamp = zero, want parsed timestamp")
	}
}

func TestDetectTurnStateFromTailIgnoresControlOnlyTaskStart(t *testing.T) {
	path := filepath.Join(t.TempDir(), "rollout.jsonl")
	content := "" +
		"{\"timestamp\":\"2026-03-05T09:04:12.000Z\",\"type\":\"event_msg\",\"payload\":{\"type\":\"task_complete\"}}\n" +
		"{\"timestamp\":\"2026-03-05T09:05:00.000Z\",\"type\":\"event_msg\",\"payload\":{\"type\":\"task_started\"}}\n" +
		"{\"timestamp\":\"2026-03-05T09:05:00.001Z\",\"type\":\"turn_context\",\"payload\":{\"turn_id\":\"turn_control\"}}\n" +
		"{\"timestamp\":\"2026-03-05T09:05:00.002Z\",\"type\":\"response_item\",\"payload\":{\"type\":\"message\",\"role\":\"developer\",\"content\":[{\"type\":\"input_text\",\"text\":\"model switch\"}]}}\n" +
		"{\"timestamp\":\"2026-03-05T09:05:00.003Z\",\"type\":\"event_msg\",\"payload\":{\"type\":\"token_count\"}}\n"
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatalf("write rollout: %v", err)
	}

	d := New("")
	state := d.detectTurnStateFromTail(path)
	if !state.known || !state.completed {
		t.Fatalf("detectTurnStateFromTail() = %#v, want latest stable completed state", state)
	}
	if !state.startedAt.IsZero() {
		t.Fatalf("startedAt = %v, want zero after control-only task start is ignored", state.startedAt)
	}
}

func TestDetectFromStateDBPrefersSessionFileCWD(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	codexHome := filepath.Join(root, ".codex")
	sessionDir := filepath.Join(codexHome, "sessions", "2026", "04", "01")
	if err := os.MkdirAll(sessionDir, 0o755); err != nil {
		t.Fatalf("mkdir session dir: %v", err)
	}

	correctPath := filepath.Join(root, "correct-project")
	wrongPath := filepath.Join(root, "wrong-project")
	for _, path := range []string{correctPath, wrongPath} {
		if err := os.MkdirAll(path, 0o755); err != nil {
			t.Fatalf("mkdir %s: %v", path, err)
		}
	}

	sessionID := "ses_db_prefers_file"
	sessionFile := filepath.Join(sessionDir, sessionID+".jsonl")
	timestamp := time.Date(2026, 4, 1, 10, 0, 0, 0, time.UTC)
	content := "{\"type\":\"session_meta\",\"payload\":{\"id\":\"" + sessionID + "\",\"cwd\":\"" + correctPath + "\",\"timestamp\":\"" + timestamp.Format(time.RFC3339) + "\"}}\n"
	if err := os.WriteFile(sessionFile, []byte(content), 0o644); err != nil {
		t.Fatalf("write session file: %v", err)
	}

	dbPath := filepath.Join(codexHome, "state_5.sqlite")
	db, err := sql.Open("sqlite", dbPath)
	if err != nil {
		t.Fatalf("open sqlite: %v", err)
	}
	defer db.Close()
	if _, err := db.Exec(`
		CREATE TABLE threads (
			id TEXT NOT NULL,
			cwd TEXT NOT NULL,
			created_at INTEGER NOT NULL,
			updated_at INTEGER NOT NULL,
			rollout_path TEXT NOT NULL
		)
	`); err != nil {
		t.Fatalf("create threads table: %v", err)
	}
	if _, err := db.Exec(`INSERT INTO threads(id, cwd, created_at, updated_at, rollout_path) VALUES (?, ?, ?, ?, ?)`,
		sessionID, wrongPath, timestamp.Unix(), timestamp.Add(2*time.Minute).Unix(), sessionFile,
	); err != nil {
		t.Fatalf("insert thread row: %v", err)
	}

	d := New(codexHome)
	parseCalls := 0
	d.parseOwner = func(path string) (string, string, bool) {
		parseCalls++
		return parseSessionOwnershipFromFile(path)
	}
	results, err := d.Detect(context.Background(), scanner.NewPathScope([]string{root}, nil))
	if err != nil {
		t.Fatalf("Detect() error = %v", err)
	}

	if results[wrongPath] != nil {
		t.Fatalf("wrong-path result = %#v, want session ownership moved to %s", results[wrongPath], correctPath)
	}
	entry := results[correctPath]
	if entry == nil {
		t.Fatalf("missing corrected project entry for %s", correctPath)
	}
	if len(entry.Sessions) != 1 {
		t.Fatalf("session count = %d, want 1", len(entry.Sessions))
	}
	if entry.Sessions[0].DetectedProjectPath != correctPath {
		t.Fatalf("detected project path = %q, want %q", entry.Sessions[0].DetectedProjectPath, correctPath)
	}
	if entry.Sessions[0].ProjectPath != correctPath {
		t.Fatalf("project path = %q, want %q", entry.Sessions[0].ProjectPath, correctPath)
	}

	if _, err := d.Detect(context.Background(), scanner.NewPathScope([]string{root}, nil)); err != nil {
		t.Fatalf("second Detect() error = %v", err)
	}
	if parseCalls != 1 {
		t.Fatalf("unchanged rollout ownership parsed %d times, want once", parseCalls)
	}

	file, err := os.OpenFile(sessionFile, os.O_APPEND|os.O_WRONLY, 0)
	if err != nil {
		t.Fatalf("open rollout for append: %v", err)
	}
	_, writeErr := file.WriteString("{\"type\":\"event_msg\",\"payload\":{\"type\":\"task_complete\"}}\n")
	closeErr := file.Close()
	if writeErr != nil {
		t.Fatalf("append rollout: %v", writeErr)
	}
	if closeErr != nil {
		t.Fatalf("close rollout: %v", closeErr)
	}
	if _, err := d.Detect(context.Background(), scanner.NewPathScope([]string{root}, nil)); err != nil {
		t.Fatalf("Detect() after append error = %v", err)
	}
	if parseCalls != 2 {
		t.Fatalf("appended rollout ownership parsed %d times, want twice", parseCalls)
	}
}

func TestOwnershipCacheRefreshesAtomicReplacementWithSameMetadata(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	firstProject := filepath.Join(root, "project-a")
	secondProject := filepath.Join(root, "project-b")
	rolloutPath := filepath.Join(root, "rollout.jsonl")
	firstContent := "{\"type\":\"session_meta\",\"payload\":{\"id\":\"ses_one\",\"cwd\":\"" + firstProject + "\"}}\n"
	secondContent := "{\"type\":\"session_meta\",\"payload\":{\"id\":\"ses_two\",\"cwd\":\"" + secondProject + "\"}}\n"
	if len(firstContent) != len(secondContent) {
		t.Fatalf("replacement fixture sizes differ: %d != %d", len(firstContent), len(secondContent))
	}
	if err := os.WriteFile(rolloutPath, []byte(firstContent), 0o600); err != nil {
		t.Fatal(err)
	}

	d := New(root)
	parseCalls := 0
	d.parseOwner = func(path string) (string, string, bool) {
		parseCalls++
		return parseSessionOwnershipFromFile(path)
	}
	info, err := os.Stat(rolloutPath)
	if err != nil {
		t.Fatal(err)
	}
	if _, cwd, ok := d.parseSessionOwnershipWithCache(rolloutPath, info); !ok || cwd != firstProject {
		t.Fatalf("first ownership cwd=%q ok=%v, want %q", cwd, ok, firstProject)
	}
	if _, cwd, ok := d.parseSessionOwnershipWithCache(rolloutPath, info); !ok || cwd != firstProject {
		t.Fatalf("cached ownership cwd=%q ok=%v, want %q", cwd, ok, firstProject)
	}
	if parseCalls != 1 {
		t.Fatalf("unchanged ownership parsed %d times, want once", parseCalls)
	}

	replacementPath := filepath.Join(root, "replacement.jsonl")
	if err := os.WriteFile(replacementPath, []byte(secondContent), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.Chtimes(replacementPath, info.ModTime(), info.ModTime()); err != nil {
		t.Fatal(err)
	}
	replacementInfo, err := os.Stat(replacementPath)
	if err != nil {
		t.Fatal(err)
	}
	if replacementInfo.Size() != info.Size() || !replacementInfo.ModTime().Equal(info.ModTime()) {
		t.Fatalf("replacement metadata size=%d mtime=%v, want size=%d mtime=%v", replacementInfo.Size(), replacementInfo.ModTime(), info.Size(), info.ModTime())
	}
	if err := os.Rename(replacementPath, rolloutPath); err != nil {
		t.Fatal(err)
	}
	replacementInfo, err = os.Stat(rolloutPath)
	if err != nil {
		t.Fatal(err)
	}
	if _, cwd, ok := d.parseSessionOwnershipWithCache(rolloutPath, replacementInfo); !ok || cwd != secondProject {
		t.Fatalf("replacement ownership cwd=%q ok=%v, want %q", cwd, ok, secondProject)
	}
	if parseCalls != 2 {
		t.Fatalf("replaced ownership parsed %d times, want twice", parseCalls)
	}
}

func TestDetectFromStateDBCanonicalizesOverlayRolloutPath(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	sourceHome := filepath.Join(root, ".codex")
	sessionDir := filepath.Join(sourceHome, "sessions", "2026", "04", "21")
	if err := os.MkdirAll(sessionDir, 0o755); err != nil {
		t.Fatalf("mkdir session dir: %v", err)
	}

	projectPath := filepath.Join(root, "project")
	if err := os.MkdirAll(projectPath, 0o755); err != nil {
		t.Fatalf("mkdir project: %v", err)
	}

	overlayHome := filepath.Join(root, "internal-workspaces", "lcroom-codex-home-123456")
	if err := os.MkdirAll(overlayHome, 0o755); err != nil {
		t.Fatalf("mkdir overlay home: %v", err)
	}
	if err := os.Symlink(filepath.Join(sourceHome, "state_5.sqlite"), filepath.Join(overlayHome, "state_5.sqlite")); err != nil {
		t.Fatalf("symlink state db: %v", err)
	}
	if err := os.Symlink(filepath.Join(sourceHome, "sessions"), filepath.Join(overlayHome, "sessions")); err != nil {
		t.Fatalf("symlink sessions: %v", err)
	}

	sessionID := "ses_overlay_rollout"
	canonicalSessionFile := filepath.Join(sessionDir, sessionID+".jsonl")
	timestamp := time.Date(2026, 4, 21, 10, 0, 0, 0, time.UTC)
	content := "{\"type\":\"session_meta\",\"payload\":{\"id\":\"" + sessionID + "\",\"cwd\":\"" + projectPath + "\",\"timestamp\":\"" + timestamp.Format(time.RFC3339) + "\"}}\n"
	if err := os.WriteFile(canonicalSessionFile, []byte(content), 0o644); err != nil {
		t.Fatalf("write session file: %v", err)
	}

	db, err := sql.Open("sqlite", filepath.Join(sourceHome, "state_5.sqlite"))
	if err != nil {
		t.Fatalf("open sqlite: %v", err)
	}
	defer db.Close()
	if _, err := db.Exec(`
		CREATE TABLE threads (
			id TEXT NOT NULL,
			cwd TEXT NOT NULL,
			created_at INTEGER NOT NULL,
			updated_at INTEGER NOT NULL,
			rollout_path TEXT NOT NULL
		)
	`); err != nil {
		t.Fatalf("create threads table: %v", err)
	}
	overlayRollout := filepath.Join(overlayHome, "sessions", "2026", "04", "21", sessionID+".jsonl")
	if _, err := db.Exec(`INSERT INTO threads(id, cwd, created_at, updated_at, rollout_path) VALUES (?, ?, ?, ?, ?)`,
		sessionID, projectPath, timestamp.Unix(), timestamp.Add(time.Minute).Unix(), overlayRollout,
	); err != nil {
		t.Fatalf("insert thread row: %v", err)
	}

	d := New(overlayHome)
	results, err := d.Detect(context.Background(), scanner.NewPathScope([]string{root}, nil))
	if err != nil {
		t.Fatalf("Detect() error = %v", err)
	}

	entry := results[projectPath]
	if entry == nil || len(entry.Sessions) != 1 {
		t.Fatalf("entry = %#v, want one session for %s", entry, projectPath)
	}
	if got := entry.Sessions[0].SessionFile; got != canonicalSessionFile {
		t.Fatalf("session file = %q, want %q", got, canonicalSessionFile)
	}
	if len(entry.Artifacts) != 1 || entry.Artifacts[0].Path != canonicalSessionFile {
		t.Fatalf("artifacts = %#v, want canonical session artifact", entry.Artifacts)
	}
}
