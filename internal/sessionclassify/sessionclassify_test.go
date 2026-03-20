package sessionclassify

import (
	"context"
	"database/sql"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"lcroom/internal/events"
	"lcroom/internal/model"
	"lcroom/internal/store"
)

type fakeClassifier struct {
	result Result
	err    error
	model  string
	seen   *SessionSnapshot
}

func (f *fakeClassifier) Classify(_ context.Context, snapshot SessionSnapshot) (Result, error) {
	if f.seen != nil {
		*f.seen = snapshot
	}
	return f.result, f.err
}

func (f *fakeClassifier) ModelName() string {
	return f.model
}

type blockingClassifier struct {
	result  Result
	started chan struct{}
	release chan struct{}
}

func (b *blockingClassifier) Classify(_ context.Context, _ SessionSnapshot) (Result, error) {
	close(b.started)
	<-b.release
	return b.result, nil
}

func TestExtractSnapshotModernFixture(t *testing.T) {
	t.Parallel()

	fixture := filepath.Clean(filepath.Join("..", "..", "testdata", "codex_footprint", "sessions", "2026", "03", "05", "rollout-modern.jsonl"))
	snapshot, err := ExtractSnapshot(context.Background(), model.SessionClassification{
		SessionID:       "fixture-modern",
		ProjectPath:     "/tmp/baton",
		SessionFile:     fixture,
		SessionFormat:   "modern",
		SourceUpdatedAt: time.Now(),
	}, model.SessionEvidence{
		LatestTurnStateKnown: true,
		LatestTurnCompleted:  true,
	}, GitStatusSnapshot{})
	if err != nil {
		t.Fatalf("extract snapshot: %v", err)
	}
	if !snapshot.LatestTurnStateKnown || !snapshot.LatestTurnCompleted {
		t.Fatalf("expected lifecycle flags in snapshot, got known=%v completed=%v", snapshot.LatestTurnStateKnown, snapshot.LatestTurnCompleted)
	}
	if len(snapshot.Transcript) < 2 {
		t.Fatalf("expected transcript items, got %d", len(snapshot.Transcript))
	}
	if snapshot.Transcript[0].Role != "user" {
		t.Fatalf("first role = %s, want user", snapshot.Transcript[0].Role)
	}
	last := snapshot.Transcript[len(snapshot.Transcript)-1]
	if last.Role != "assistant" {
		t.Fatalf("last role = %s, want assistant", last.Role)
	}
	if !strings.Contains(last.Text, "Done") {
		t.Fatalf("last text = %q, want Done", last.Text)
	}
}

func TestExtractSnapshotModernFixtureRecoversLifecycleFromTranscript(t *testing.T) {
	t.Parallel()

	fixture := filepath.Clean(filepath.Join("..", "..", "testdata", "codex_footprint", "sessions", "2026", "03", "05", "rollout-modern.jsonl"))
	snapshot, err := ExtractSnapshot(context.Background(), model.SessionClassification{
		SessionID:       "fixture-modern-recovered",
		ProjectPath:     "/tmp/baton",
		SessionFile:     fixture,
		SessionFormat:   "modern",
		SourceUpdatedAt: time.Now(),
	}, model.SessionEvidence{}, GitStatusSnapshot{})
	if err != nil {
		t.Fatalf("extract snapshot: %v", err)
	}
	if !snapshot.LatestTurnStateKnown || !snapshot.LatestTurnCompleted {
		t.Fatalf("expected recovered lifecycle flags in snapshot, got known=%v completed=%v", snapshot.LatestTurnStateKnown, snapshot.LatestTurnCompleted)
	}
}

func TestExtractSnapshotOpenCodePreservesStructuredParts(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	dbPath := filepath.Join(root, "opencode.db")
	if err := seedOpenCodeTranscriptFixture(dbPath); err != nil {
		t.Fatalf("seed opencode fixture: %v", err)
	}

	snapshot, err := ExtractSnapshot(context.Background(), model.SessionClassification{
		SessionID:       "ses_open",
		ProjectPath:     "/tmp/opencode-demo",
		SessionFile:     dbPath + "#session:ses_open",
		SessionFormat:   "opencode_db",
		SourceUpdatedAt: time.Now(),
	}, model.SessionEvidence{}, GitStatusSnapshot{})
	if err != nil {
		t.Fatalf("extract snapshot: %v", err)
	}
	if len(snapshot.Transcript) != 2 {
		t.Fatalf("expected 2 transcript items, got %#v", snapshot.Transcript)
	}

	user := snapshot.Transcript[0]
	if user.Role != "user" {
		t.Fatalf("user role = %q, want user", user.Role)
	}
	if !strings.Contains(user.Text, "Please review the latest OpenCode session.") {
		t.Fatalf("user text = %q, want preserved user prompt", user.Text)
	}
	if !strings.Contains(user.Text, "Attached file: clipboard.png (image/png)") {
		t.Fatalf("user text = %q, want preserved file attachment summary", user.Text)
	}

	assistant := snapshot.Transcript[1]
	if assistant.Role != "assistant" {
		t.Fatalf("assistant role = %q, want assistant", assistant.Role)
	}
	if !strings.Contains(assistant.Text, "Reasoning: Reviewing the repository state") {
		t.Fatalf("assistant text = %q, want reasoning summary", assistant.Text)
	}
	if !strings.Contains(assistant.Text, "Tool bash completed: Run focused service tests") {
		t.Fatalf("assistant text = %q, want tool summary", assistant.Text)
	}
	if !strings.Contains(assistant.Text, "Patch touched service.go, README.md") {
		t.Fatalf("assistant text = %q, want patch summary", assistant.Text)
	}
	if strings.Contains(assistant.Text, "Step finished: tool-calls") {
		t.Fatalf("assistant text = %q, want tool-calls finish marker omitted", assistant.Text)
	}
	if !strings.Contains(assistant.Text, "Step finished: stop") {
		t.Fatalf("assistant text = %q, want step finish summary", assistant.Text)
	}
}

func TestExtractSnapshotOpenCodePrefersVisibleAssistantTextOverPlanningParts(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	dbPath := filepath.Join(root, "opencode.db")
	if err := seedOpenCodeVisibleTextFixture(dbPath); err != nil {
		t.Fatalf("seed opencode visible-text fixture: %v", err)
	}

	snapshot, err := ExtractSnapshot(context.Background(), model.SessionClassification{
		SessionID:       "ses_visible",
		ProjectPath:     "/tmp/opencode-visible",
		SessionFile:     dbPath + "#session:ses_visible",
		SessionFormat:   "opencode_db",
		SourceUpdatedAt: time.Now(),
	}, model.SessionEvidence{}, GitStatusSnapshot{})
	if err != nil {
		t.Fatalf("extract snapshot: %v", err)
	}
	if len(snapshot.Transcript) != 2 {
		t.Fatalf("expected 2 transcript items, got %#v", snapshot.Transcript)
	}

	assistant := snapshot.Transcript[1]
	if assistant.Role != "assistant" {
		t.Fatalf("assistant role = %q, want assistant", assistant.Role)
	}
	if !strings.Contains(assistant.Text, "Committed the fix and pushed it to origin/master.") {
		t.Fatalf("assistant text = %q, want visible completion text", assistant.Text)
	}
	if strings.Contains(assistant.Text, "Reasoning:") {
		t.Fatalf("assistant text = %q, want reasoning omitted when visible text is present", assistant.Text)
	}
	if strings.Contains(assistant.Text, "Tool bash completed") {
		t.Fatalf("assistant text = %q, want tool summary omitted when visible text is present", assistant.Text)
	}
	if strings.Contains(assistant.Text, "Step finished: stop") {
		t.Fatalf("assistant text = %q, want step summary omitted when visible text is present", assistant.Text)
	}
}

func TestPreviewFromTranscriptUsesInitialUserAndLatestAssistantSnippets(t *testing.T) {
	t.Parallel()

	preview := previewFromTranscript([]TranscriptItem{
		{Role: "user", Text: "Original request"},
		{Role: "assistant", Text: "Working through the setup now."},
		{Role: "user", Text: "Please switch to the older session and inspect the failing test output."},
		{Role: "assistant", Text: "I found the failing assertion in the footer rendering path."},
	})

	if preview.Title != "Original request" {
		t.Fatalf("preview title = %q", preview.Title)
	}
	if preview.Summary != "I found the failing assertion in the footer rendering path." {
		t.Fatalf("preview summary = %q", preview.Summary)
	}
}

func TestPreviewFromTranscriptSkipsScaffoldTitlesAndHeadingOnlySummaries(t *testing.T) {
	t.Parallel()

	preview := previewFromTranscript([]TranscriptItem{
		{Role: "user", Text: "# AGENTS.md instructions for /tmp/demo\n\n<INSTRUCTIONS>\nKeep STATUS.md updated.\n</INSTRUCTIONS>\n\n<environment_context>\n  <cwd>/tmp/demo</cwd>\n</environment_context>"},
		{Role: "user", Text: "why is the quickgame_27 project marked stuck?"},
		{Role: "assistant", Text: "Still checking the latest session state."},
		{Role: "assistant", Text: "**Classification**\n\nI'd classify the latest session as completed, not in progress."},
	})

	if preview.Title != "why is the quickgame_27 project marked stuck?" {
		t.Fatalf("preview title = %q", preview.Title)
	}
	if preview.Summary != "I'd classify the latest session as completed, not in progress." {
		t.Fatalf("preview summary = %q", preview.Summary)
	}
}

func TestManagerProcessOneCompletesClassification(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	st, err := store.Open(filepath.Join(t.TempDir(), "little-control-room.sqlite"))
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	defer st.Close()

	fixture := filepath.Clean(filepath.Join("..", "..", "testdata", "codex_footprint", "sessions", "2026", "03", "05", "rollout-modern.jsonl"))
	now := time.Now()
	state := model.ProjectState{
		Path:           "/tmp/baton",
		Name:           "baton",
		Status:         model.StatusPossiblyStuck,
		AttentionScore: 40,
		InScope:        true,
		UpdatedAt:      now,
		Sessions: []model.SessionEvidence{{
			SessionID:            "ses_modern",
			ProjectPath:          "/tmp/baton",
			SessionFile:          fixture,
			Format:               "modern",
			LastEventAt:          now,
			LatestTurnStateKnown: true,
			LatestTurnCompleted:  true,
		}},
	}
	if err := st.UpsertProjectState(ctx, state); err != nil {
		t.Fatalf("upsert project: %v", err)
	}

	var (
		refreshed    string
		seenSnapshot SessionSnapshot
	)
	manager := NewManager(st, events.NewBus(), Options{
		Client: &fakeClassifier{
			seen:  &seenSnapshot,
			model: "gpt-test-mini",
			result: Result{
				Category:   model.SessionCategoryCompleted,
				Summary:    "Work appears complete for now.",
				Confidence: 0.92,
				Model:      "gpt-test-mini",
				Usage: model.LLMUsage{
					InputTokens:       321,
					OutputTokens:      57,
					TotalTokens:       378,
					CachedInputTokens: 12,
					ReasoningTokens:   4,
				},
			},
		},
		OnProjectUpdated: func(_ context.Context, projectPath string) error {
			refreshed = projectPath
			return nil
		},
		Workers: 1,
	})

	queued, err := manager.QueueProject(ctx, state)
	if err != nil {
		t.Fatalf("queue project: %v", err)
	}
	if !queued {
		t.Fatalf("expected queue project to enqueue work")
	}

	processed, err := manager.processOne(ctx)
	if err != nil {
		t.Fatalf("process one: %v", err)
	}
	if !processed {
		t.Fatalf("expected processOne to process work")
	}
	if refreshed != state.Path {
		t.Fatalf("refreshed project = %q, want %q", refreshed, state.Path)
	}
	if !seenSnapshot.LatestTurnStateKnown || !seenSnapshot.LatestTurnCompleted {
		t.Fatalf("expected classifier snapshot to include lifecycle flags, got %#v", seenSnapshot)
	}

	classification, err := st.GetSessionClassification(ctx, "ses_modern")
	if err != nil {
		t.Fatalf("get classification: %v", err)
	}
	if classification.Status != model.ClassificationCompleted {
		t.Fatalf("status = %s, want completed", classification.Status)
	}
	if classification.Category != model.SessionCategoryCompleted {
		t.Fatalf("category = %s, want completed", classification.Category)
	}
	if classification.Model != "gpt-test-mini" {
		t.Fatalf("model = %q, want %q", classification.Model, "gpt-test-mini")
	}

	detail, err := st.GetProjectDetail(ctx, state.Path, 1)
	if err != nil {
		t.Fatalf("get detail: %v", err)
	}
	if len(detail.Sessions) != 1 {
		t.Fatalf("expected stored session metadata, got %#v", detail.Sessions)
	}
	if !detail.Sessions[0].LatestTurnStateKnown || !detail.Sessions[0].LatestTurnCompleted {
		t.Fatalf("expected recovered lifecycle persisted to project_sessions, got known=%v completed=%v", detail.Sessions[0].LatestTurnStateKnown, detail.Sessions[0].LatestTurnCompleted)
	}

	usage := manager.UsageSnapshot()
	if !usage.Enabled {
		t.Fatalf("expected usage snapshot to be enabled")
	}
	if usage.Model != "gpt-test-mini" {
		t.Fatalf("usage model = %q, want %q", usage.Model, "gpt-test-mini")
	}
	if usage.Running != 0 {
		t.Fatalf("usage running = %d, want 0", usage.Running)
	}
	if usage.Started != 1 || usage.Completed != 1 || usage.Failed != 0 {
		t.Fatalf("unexpected usage counters: %+v", usage)
	}
	if usage.Totals.InputTokens != 321 || usage.Totals.OutputTokens != 57 || usage.Totals.TotalTokens != 378 {
		t.Fatalf("unexpected usage totals: %+v", usage.Totals)
	}
	if usage.Totals.CachedInputTokens != 12 || usage.Totals.ReasoningTokens != 4 {
		t.Fatalf("unexpected usage detail totals: %+v", usage.Totals)
	}
}

func TestManagerProcessOneHeartbeatsWaitingForModel(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	st, err := store.Open(filepath.Join(t.TempDir(), "little-control-room.sqlite"))
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	defer st.Close()

	fixture := filepath.Clean(filepath.Join("..", "..", "testdata", "codex_footprint", "sessions", "2026", "03", "05", "rollout-modern.jsonl"))
	now := time.Now()
	state := model.ProjectState{
		Path:           "/tmp/heartbeat",
		Name:           "heartbeat",
		Status:         model.StatusPossiblyStuck,
		AttentionScore: 25,
		InScope:        true,
		UpdatedAt:      now,
		Sessions: []model.SessionEvidence{{
			SessionID:            "ses_heartbeat",
			ProjectPath:          "/tmp/heartbeat",
			SessionFile:          fixture,
			Format:               "modern",
			LastEventAt:          now,
			LatestTurnStateKnown: true,
			LatestTurnCompleted:  true,
		}},
	}
	if err := st.UpsertProjectState(ctx, state); err != nil {
		t.Fatalf("upsert project: %v", err)
	}

	classifier := &blockingClassifier{
		result: Result{
			Category:   model.SessionCategoryCompleted,
			Summary:    "heartbeat complete",
			Confidence: 0.9,
			Model:      "gpt-test-mini",
		},
		started: make(chan struct{}),
		release: make(chan struct{}),
	}
	manager := NewManager(st, events.NewBus(), Options{
		Client:     classifier,
		Workers:    1,
		RetryAfter: time.Minute,
		StaleAfter: 80 * time.Millisecond,
	})

	queued, err := manager.QueueProject(ctx, state)
	if err != nil {
		t.Fatalf("queue project: %v", err)
	}
	if !queued {
		t.Fatalf("expected queue project to enqueue work")
	}

	done := make(chan error, 1)
	go func() {
		_, err := manager.processOne(ctx)
		done <- err
	}()

	select {
	case <-classifier.started:
	case <-time.After(2 * time.Second):
		t.Fatalf("classifier never started")
	}

	time.Sleep(140 * time.Millisecond)
	if _, err := st.ClaimNextPendingSessionClassification(ctx, 80*time.Millisecond); !errors.Is(err, sql.ErrNoRows) {
		t.Fatalf("expected heartbeat to keep attempt fresh, got err=%v", err)
	}

	close(classifier.release)
	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("process one: %v", err)
		}
	case <-time.After(2 * time.Second):
		t.Fatalf("processOne did not finish")
	}

	classification, err := st.GetSessionClassification(ctx, "ses_heartbeat")
	if err != nil {
		t.Fatalf("get classification: %v", err)
	}
	if classification.Status != model.ClassificationCompleted {
		t.Fatalf("status = %s, want completed", classification.Status)
	}
}

func TestSnapshotHashForSnapshotIgnoresLastEventAt(t *testing.T) {
	t.Parallel()

	base := SessionSnapshot{
		ProjectPath:          "/tmp/demo",
		SessionID:            "ses_demo",
		SessionFormat:        "opencode_db",
		LastEventAt:          "2026-03-06T01:00:00Z",
		LatestTurnStateKnown: true,
		Transcript: []TranscriptItem{
			{Role: "user", Text: "Please review the latest idle state."},
			{Role: "assistant", Text: "Everything looks complete for now."},
		},
	}
	changedTimestamp := base
	changedTimestamp.LastEventAt = "2026-03-06T01:05:00Z"

	if got, want := SnapshotHashForSnapshot(base), SnapshotHashForSnapshot(changedTimestamp); got != want {
		t.Fatalf("snapshot hash changed for timestamp-only update: got %s want %s", got, want)
	}
}

func TestSnapshotHashForSnapshotChangesWhenGitStatusChanges(t *testing.T) {
	t.Parallel()

	base := SessionSnapshot{
		ProjectPath:   "/tmp/demo",
		SessionID:     "ses_demo",
		SessionFormat: "modern",
		GitStatus: GitStatusSnapshot{
			WorktreeDirty: false,
			RemoteStatus:  string(model.RepoSyncSynced),
		},
		Transcript: []TranscriptItem{
			{Role: "user", Text: "Please finish this task."},
			{Role: "assistant", Text: "Done, all requested changes are in place."},
		},
	}
	changedGit := base
	changedGit.GitStatus = GitStatusSnapshot{
		WorktreeDirty: true,
		RemoteStatus:  string(model.RepoSyncAhead),
		AheadCount:    1,
	}

	if got, want := SnapshotHashForSnapshot(base), SnapshotHashForSnapshot(changedGit); got == want {
		t.Fatalf("expected snapshot hash to change when git status changes")
	}
}

func TestSnapshotHashForSnapshotChangesWhenTurnLifecycleChanges(t *testing.T) {
	t.Parallel()

	base := SessionSnapshot{
		ProjectPath:          "/tmp/demo",
		SessionID:            "ses_demo",
		SessionFormat:        "modern",
		LatestTurnStateKnown: true,
		LatestTurnCompleted:  false,
		Transcript: []TranscriptItem{
			{Role: "user", Text: "Please finish this task."},
			{Role: "assistant", Text: "Running the last validation step now."},
		},
	}
	completed := base
	completed.LatestTurnCompleted = true

	if got, want := SnapshotHashForSnapshot(base), SnapshotHashForSnapshot(completed); got == want {
		t.Fatalf("expected snapshot hash to change when turn lifecycle changes")
	}
}

func seedOpenCodeTranscriptFixture(dbPath string) error {
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
		CREATE TABLE message (
			id TEXT PRIMARY KEY,
			session_id TEXT NOT NULL,
			time_created INTEGER NOT NULL,
			data TEXT NOT NULL
		);
		CREATE TABLE part (
			id TEXT PRIMARY KEY,
			message_id TEXT NOT NULL,
			session_id TEXT NOT NULL,
			time_created INTEGER NOT NULL,
			data TEXT NOT NULL
		);
		INSERT INTO message(id, session_id, time_created, data) VALUES
			('msg_user', 'ses_open', 1000, '{"role":"user"}'),
			('msg_assistant', 'ses_open', 2000, '{"role":"assistant"}');
		INSERT INTO part(id, message_id, session_id, time_created, data) VALUES
			('part_user_text', 'msg_user', 'ses_open', 1001, '{"type":"text","text":"Please review the latest OpenCode session."}'),
			('part_user_file', 'msg_user', 'ses_open', 1002, '{"type":"file","mime":"image/png","filename":"clipboard.png","url":"data:image/png;base64,AAA"}'),
			('part_assistant_reasoning', 'msg_assistant', 'ses_open', 2001, '{"type":"reasoning","text":"Reviewing the repository state"}'),
			('part_assistant_tool', 'msg_assistant', 'ses_open', 2002, '{"type":"tool","tool":"bash","state":{"status":"completed","input":{"command":"go test ./internal/service","description":"Run focused service tests"}}}'),
			('part_assistant_tool_finish', 'msg_assistant', 'ses_open', 2003, '{"type":"step-finish","reason":"tool-calls"}'),
			('part_assistant_patch', 'msg_assistant', 'ses_open', 2004, '{"type":"patch","files":["/tmp/opencode-demo/internal/service/service.go","/tmp/opencode-demo/README.md"]}'),
			('part_assistant_finish', 'msg_assistant', 'ses_open', 2005, '{"type":"step-finish","reason":"stop"}');
	`)
	return err
}

func seedOpenCodeVisibleTextFixture(dbPath string) error {
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
		CREATE TABLE message (
			id TEXT PRIMARY KEY,
			session_id TEXT NOT NULL,
			time_created INTEGER NOT NULL,
			data TEXT NOT NULL
		);
		CREATE TABLE part (
			id TEXT PRIMARY KEY,
			message_id TEXT NOT NULL,
			session_id TEXT NOT NULL,
			time_created INTEGER NOT NULL,
			data TEXT NOT NULL
		);
		INSERT INTO message(id, session_id, time_created, data) VALUES
			('msg_user', 'ses_visible', 1000, '{"role":"user"}'),
			('msg_assistant_plan', 'ses_visible', 2000, '{"role":"assistant"}'),
			('msg_assistant_reply', 'ses_visible', 3000, '{"role":"assistant"}');
		INSERT INTO part(id, message_id, session_id, time_created, data) VALUES
			('part_user_text', 'msg_user', 'ses_visible', 1001, '{"type":"text","text":"Please bump the version, commit, and push."}'),
			('part_assistant_plan_reasoning', 'msg_assistant_plan', 'ses_visible', 2001, '{"type":"reasoning","text":"I still need to run git push after the commit."}'),
			('part_assistant_plan_tool', 'msg_assistant_plan', 'ses_visible', 2002, '{"type":"tool","tool":"bash","state":{"status":"completed","input":{"command":"git add package.json && git commit -m \"release\" && git push"}}}'),
			('part_assistant_plan_finish', 'msg_assistant_plan', 'ses_visible', 2003, '{"type":"step-finish","reason":"tool-calls"}'),
			('part_assistant_reply_text', 'msg_assistant_reply', 'ses_visible', 3001, '{"type":"text","text":"Committed the fix and pushed it to origin/master."}'),
			('part_assistant_reply_finish', 'msg_assistant_reply', 'ses_visible', 3002, '{"type":"step-finish","reason":"stop"}');
	`)
	return err
}
