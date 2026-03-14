package store

import (
	"context"
	"database/sql"
	"path/filepath"
	"testing"
	"time"

	"lcroom/internal/model"
)

func TestListProjectsScopeFiltering(t *testing.T) {
	t.Parallel()
	ctx := context.Background()

	dbPath := filepath.Join(t.TempDir(), "little-control-room.sqlite")
	st, err := Open(dbPath)
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	defer st.Close()

	now := time.Now()
	if err := st.UpsertProjectState(ctx, model.ProjectState{
		Path:           "/tmp/in-scope",
		Name:           "in-scope",
		Status:         model.StatusIdle,
		AttentionScore: 10,
		InScope:        true,
		UpdatedAt:      now,
	}); err != nil {
		t.Fatalf("upsert in-scope project: %v", err)
	}
	if err := st.UpsertProjectState(ctx, model.ProjectState{
		Path:           "/tmp/historical",
		Name:           "historical",
		Status:         model.StatusIdle,
		AttentionScore: 10,
		InScope:        false,
		UpdatedAt:      now,
	}); err != nil {
		t.Fatalf("upsert historical project: %v", err)
	}
	if err := st.UpsertProjectState(ctx, model.ProjectState{
		Path:           "/tmp/forgotten",
		Name:           "forgotten",
		Status:         model.StatusIdle,
		AttentionScore: 10,
		Forgotten:      true,
		InScope:        true,
		UpdatedAt:      now,
	}); err != nil {
		t.Fatalf("upsert forgotten project: %v", err)
	}

	currentOnly, err := st.ListProjects(ctx, false)
	if err != nil {
		t.Fatalf("list current-only projects: %v", err)
	}
	if len(currentOnly) != 1 {
		t.Fatalf("expected 1 current-scope project, got %d", len(currentOnly))
	}
	if currentOnly[0].Path != "/tmp/in-scope" {
		t.Fatalf("unexpected current-scope project path: %s", currentOnly[0].Path)
	}

	allProjects, err := st.ListProjects(ctx, true)
	if err != nil {
		t.Fatalf("list all projects: %v", err)
	}
	if len(allProjects) != 2 {
		t.Fatalf("expected 2 non-forgotten projects when including historical, got %d", len(allProjects))
	}
}

func TestOpenMigratesProjectsInScopeColumn(t *testing.T) {
	t.Parallel()
	ctx := context.Background()

	dbPath := filepath.Join(t.TempDir(), "legacy.sqlite")
	db, err := sql.Open("sqlite", dbPath)
	if err != nil {
		t.Fatalf("open legacy sqlite: %v", err)
	}
	db.SetMaxOpenConns(1)
	_, err = db.ExecContext(ctx, `
		CREATE TABLE projects (
			path TEXT PRIMARY KEY,
			name TEXT NOT NULL,
			last_activity INTEGER,
			status TEXT NOT NULL,
			attention_score INTEGER NOT NULL,
			pinned INTEGER NOT NULL DEFAULT 0,
			snoozed_until INTEGER,
			note TEXT NOT NULL DEFAULT '',
			updated_at INTEGER NOT NULL
		);
		INSERT INTO projects(path, name, status, attention_score, pinned, note, updated_at)
		VALUES ('/tmp/legacy', 'legacy', 'idle', 10, 0, '', 0);
	`)
	if err != nil {
		t.Fatalf("seed legacy schema: %v", err)
	}
	if err := db.Close(); err != nil {
		t.Fatalf("close legacy db: %v", err)
	}

	st, err := Open(dbPath)
	if err != nil {
		t.Fatalf("open migrated store: %v", err)
	}
	defer st.Close()

	projects, err := st.ListProjects(ctx, false)
	if err != nil {
		t.Fatalf("list migrated projects: %v", err)
	}
	if len(projects) != 1 {
		t.Fatalf("expected migrated project to remain visible, got %d projects", len(projects))
	}
	if !projects[0].InScope {
		t.Fatalf("expected migrated row to default to in_scope=true")
	}
	if !projects[0].PresentOnDisk {
		t.Fatalf("expected migrated row to default to present_on_disk=true")
	}
	if projects[0].RepoDirty {
		t.Fatalf("expected migrated row to default to repo_dirty=false")
	}
	if projects[0].RepoSyncStatus != "" {
		t.Fatalf("expected migrated row to default to empty repo_sync_status")
	}
	if projects[0].RepoAheadCount != 0 || projects[0].RepoBehindCount != 0 {
		t.Fatalf("expected migrated row to default to zero ahead/behind counts")
	}
	if projects[0].Forgotten {
		t.Fatalf("expected migrated row to default to forgotten=false")
	}
}

func TestOpenConfiguresSQLitePragmas(t *testing.T) {
	t.Parallel()
	ctx := context.Background()

	dbPath := filepath.Join(t.TempDir(), "little-control-room.sqlite")
	st, err := Open(dbPath)
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	defer st.Close()

	var journalMode string
	if err := st.db.QueryRowContext(ctx, `PRAGMA journal_mode`).Scan(&journalMode); err != nil {
		t.Fatalf("query journal_mode: %v", err)
	}
	if journalMode != "wal" {
		t.Fatalf("journal_mode = %q, want wal", journalMode)
	}

	var synchronous int
	if err := st.db.QueryRowContext(ctx, `PRAGMA synchronous`).Scan(&synchronous); err != nil {
		t.Fatalf("query synchronous: %v", err)
	}
	if synchronous != 1 {
		t.Fatalf("synchronous = %d, want 1 (NORMAL)", synchronous)
	}

	var busyTimeout int
	if err := st.db.QueryRowContext(ctx, `PRAGMA busy_timeout`).Scan(&busyTimeout); err != nil {
		t.Fatalf("query busy_timeout: %v", err)
	}
	if busyTimeout != int(sqliteBusyTimeout/time.Millisecond) {
		t.Fatalf("busy_timeout = %d, want %d", busyTimeout, sqliteBusyTimeout/time.Millisecond)
	}

	var foreignKeys int
	if err := st.db.QueryRowContext(ctx, `PRAGMA foreign_keys`).Scan(&foreignKeys); err != nil {
		t.Fatalf("query foreign_keys: %v", err)
	}
	if foreignKeys != 1 {
		t.Fatalf("foreign_keys = %d, want 1", foreignKeys)
	}
}

func TestSessionClassificationQueueAndDetail(t *testing.T) {
	t.Parallel()
	ctx := context.Background()

	dbPath := filepath.Join(t.TempDir(), "little-control-room.sqlite")
	st, err := Open(dbPath)
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	defer st.Close()

	now := time.Now()
	turnStartedAt := now.Add(-3 * time.Minute)
	state := model.ProjectState{
		Path:           "/tmp/classified",
		Name:           "classified",
		Status:         model.StatusIdle,
		AttentionScore: 10,
		InScope:        true,
		UpdatedAt:      now,
		Sessions: []model.SessionEvidence{{
			SessionID:            "ses_1",
			ProjectPath:          "/tmp/classified",
			DetectedProjectPath:  "/tmp/classified",
			SessionFile:          "/tmp/session.jsonl",
			Format:               "modern",
			SnapshotHash:         "session-hash-1",
			LastEventAt:          now,
			LatestTurnStartedAt:  turnStartedAt,
			LatestTurnStateKnown: true,
			LatestTurnCompleted:  false,
		}},
	}
	if err := st.UpsertProjectState(ctx, state); err != nil {
		t.Fatalf("upsert project state: %v", err)
	}

	queued, err := st.QueueSessionClassification(ctx, model.SessionClassification{
		SessionID:         "ses_1",
		ProjectPath:       "/tmp/classified",
		SessionFile:       "/tmp/session.jsonl",
		SessionFormat:     "modern",
		SnapshotHash:      "hash-1",
		Model:             "gpt-5-mini",
		ClassifierVersion: "v1",
		SourceUpdatedAt:   now,
	}, 15*time.Minute)
	if err != nil {
		t.Fatalf("queue classification: %v", err)
	}
	if !queued {
		t.Fatalf("expected initial queue to enqueue work")
	}
	queuedClassification, err := st.GetSessionClassification(ctx, "ses_1")
	if err != nil {
		t.Fatalf("get queued classification: %v", err)
	}
	if queuedClassification.Stage != model.ClassificationStageQueued {
		t.Fatalf("queued classification stage = %s, want %s", queuedClassification.Stage, model.ClassificationStageQueued)
	}
	if queuedClassification.StageStartedAt.IsZero() {
		t.Fatalf("expected queued classification stage timestamp")
	}

	queued, err = st.QueueSessionClassification(ctx, model.SessionClassification{
		SessionID:         "ses_1",
		ProjectPath:       "/tmp/classified",
		SessionFile:       "/tmp/session.jsonl",
		SessionFormat:     "modern",
		SnapshotHash:      "hash-1",
		Model:             "gpt-5-mini",
		ClassifierVersion: "v1",
		SourceUpdatedAt:   now,
	}, 15*time.Minute)
	if err != nil {
		t.Fatalf("requeue same snapshot: %v", err)
	}
	if queued {
		t.Fatalf("expected same snapshot to be skipped")
	}

	classification, err := st.ClaimNextPendingSessionClassification(ctx, time.Minute)
	if err != nil {
		t.Fatalf("claim pending classification: %v", err)
	}
	if classification.SessionID != "ses_1" {
		t.Fatalf("claimed session_id = %s, want ses_1", classification.SessionID)
	}
	if classification.Stage != model.ClassificationStagePreparingSnapshot {
		t.Fatalf("claimed classification stage = %s, want %s", classification.Stage, model.ClassificationStagePreparingSnapshot)
	}
	if classification.StageStartedAt.IsZero() {
		t.Fatalf("expected claimed classification stage timestamp")
	}
	if err := st.UpdateSessionClassificationStage(ctx, classification.SessionID, model.ClassificationStageWaitingForModel); err != nil {
		t.Fatalf("update classification stage: %v", err)
	}
	classification.Stage = model.ClassificationStageWaitingForModel

	classification.Category = model.SessionCategoryCompleted
	classification.Summary = "Work appears complete for now."
	classification.Confidence = 0.93
	classification.Model = "gpt-5-mini-2025-08-07"
	if err := st.CompleteSessionClassification(ctx, classification); err != nil {
		t.Fatalf("complete classification: %v", err)
	}

	detail, err := st.GetProjectDetail(ctx, "/tmp/classified", 5)
	if err != nil {
		t.Fatalf("get project detail: %v", err)
	}
	if detail.LatestSessionClassification == nil {
		t.Fatalf("expected latest session classification in detail")
	}
	if len(detail.Sessions) != 1 || detail.Sessions[0].SnapshotHash != "session-hash-1" {
		t.Fatalf("expected session snapshot hash to persist, got %#v", detail.Sessions)
	}
	if got := detail.Sessions[0].LatestTurnStartedAt; got.Unix() != turnStartedAt.Unix() {
		t.Fatalf("latest turn started at = %v, want %v", got, turnStartedAt)
	}
	if detail.LatestSessionClassification.Category != model.SessionCategoryCompleted {
		t.Fatalf("classification category = %s, want %s", detail.LatestSessionClassification.Category, model.SessionCategoryCompleted)
	}
	if detail.LatestSessionClassification.Stage != "" {
		t.Fatalf("completed classification stage = %q, want empty", detail.LatestSessionClassification.Stage)
	}

	projects, err := st.ListProjects(ctx, false)
	if err != nil {
		t.Fatalf("list projects with classification summary: %v", err)
	}
	if len(projects) != 1 {
		t.Fatalf("expected one project, got %d", len(projects))
	}
	if projects[0].LatestSessionFormat != "modern" {
		t.Fatalf("latest session format = %s, want modern", projects[0].LatestSessionFormat)
	}
	if projects[0].LatestSessionDetectedProjectPath != "/tmp/classified" {
		t.Fatalf("latest session detected path = %s, want /tmp/classified", projects[0].LatestSessionDetectedProjectPath)
	}
	if projects[0].LatestSessionClassification != model.ClassificationCompleted {
		t.Fatalf("latest session classification = %s, want completed", projects[0].LatestSessionClassification)
	}
	if projects[0].LatestSessionClassificationStage != "" {
		t.Fatalf("latest session classification stage = %q, want empty", projects[0].LatestSessionClassificationStage)
	}
	if projects[0].LatestSessionClassificationType != model.SessionCategoryCompleted {
		t.Fatalf("latest session classification type = %s, want completed", projects[0].LatestSessionClassificationType)
	}
	if projects[0].LatestSessionSummary != "Work appears complete for now." {
		t.Fatalf("latest session summary = %q, want completed summary", projects[0].LatestSessionSummary)
	}
	if got := projects[0].LatestTurnStartedAt; got.Unix() != turnStartedAt.Unix() {
		t.Fatalf("project latest turn started at = %v, want %v", got, turnStartedAt)
	}
	if !projects[0].LatestTurnStateKnown || projects[0].LatestTurnCompleted {
		t.Fatalf("project latest turn state = known:%v completed:%v, want known:true completed:false", projects[0].LatestTurnStateKnown, projects[0].LatestTurnCompleted)
	}

	queued, err = st.QueueSessionClassification(ctx, model.SessionClassification{
		SessionID:         "ses_1",
		ProjectPath:       "/tmp/classified",
		SessionFile:       "/tmp/session.jsonl",
		SessionFormat:     "modern",
		SnapshotHash:      "hash-1",
		Model:             "gpt-5-mini",
		ClassifierVersion: "v1",
		SourceUpdatedAt:   now,
	}, 15*time.Minute)
	if err != nil {
		t.Fatalf("queue completed snapshot: %v", err)
	}
	if queued {
		t.Fatalf("expected completed snapshot to be skipped")
	}

	queued, err = st.QueueSessionClassification(ctx, model.SessionClassification{
		SessionID:         "ses_1",
		ProjectPath:       "/tmp/classified",
		SessionFile:       "/tmp/session.jsonl",
		SessionFormat:     "modern",
		SnapshotHash:      "hash-1",
		Model:             "gpt-5-mini",
		ClassifierVersion: "v1",
		SourceUpdatedAt:   now,
	}, 15*time.Minute)
	if err != nil {
		t.Fatalf("queue completed snapshot with alias model: %v", err)
	}
	if queued {
		t.Fatalf("expected versioned completed model to match configured alias")
	}

	queued, err = st.QueueSessionClassification(ctx, model.SessionClassification{
		SessionID:         "ses_1",
		ProjectPath:       "/tmp/classified",
		SessionFile:       "/tmp/session.jsonl",
		SessionFormat:     "modern",
		SnapshotHash:      "hash-2",
		Model:             "gpt-5-mini",
		ClassifierVersion: "v1",
		SourceUpdatedAt:   now.Add(time.Minute),
	}, 15*time.Minute)
	if err != nil {
		t.Fatalf("queue changed snapshot: %v", err)
	}
	if !queued {
		t.Fatalf("expected changed snapshot to requeue work")
	}
}

func TestSessionClassificationAttemptGuardsIgnoreStaleWorker(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	dbPath := filepath.Join(t.TempDir(), "little-control-room.sqlite")
	st, err := Open(dbPath)
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	defer st.Close()

	now := time.Now().UTC().Truncate(time.Second)
	state := model.ProjectState{
		Path:         "/tmp/guarded",
		Name:         "guarded",
		LastActivity: now,
		Status:       model.StatusIdle,
		InScope:      true,
		UpdatedAt:    now,
		Sessions: []model.SessionEvidence{{
			SessionID:   "ses_guarded",
			ProjectPath: "/tmp/guarded",
			SessionFile: "/tmp/guarded/session.jsonl",
			Format:      "modern",
			LastEventAt: now,
		}},
	}
	if err := st.UpsertProjectState(ctx, state); err != nil {
		t.Fatalf("upsert project state: %v", err)
	}

	base := model.SessionClassification{
		SessionID:         "ses_guarded",
		ProjectPath:       state.Path,
		SessionFile:       state.Sessions[0].SessionFile,
		SessionFormat:     state.Sessions[0].Format,
		SnapshotHash:      "hash-1",
		Model:             "gpt-5-mini",
		ClassifierVersion: "v1",
		SourceUpdatedAt:   now,
	}
	if queued, err := st.QueueSessionClassification(ctx, base, 15*time.Minute); err != nil || !queued {
		t.Fatalf("initial queue: queued=%v err=%v", queued, err)
	}
	claimed, err := st.ClaimNextPendingSessionClassification(ctx, time.Minute)
	if err != nil {
		t.Fatalf("claim first attempt: %v", err)
	}
	advanced, err := st.AdvanceSessionClassificationStage(ctx, &claimed, model.ClassificationStageWaitingForModel)
	if err != nil {
		t.Fatalf("advance first attempt: %v", err)
	}
	if !advanced {
		t.Fatalf("expected first attempt stage advance to succeed")
	}
	staleAttempt := claimed

	requeued := base
	requeued.SnapshotHash = "hash-2"
	requeued.SourceUpdatedAt = now.Add(time.Minute)
	if queued, err := st.QueueSessionClassification(ctx, requeued, 15*time.Minute); err != nil || !queued {
		t.Fatalf("requeue changed snapshot: queued=%v err=%v", queued, err)
	}
	current, err := st.ClaimNextPendingSessionClassification(ctx, time.Minute)
	if err != nil {
		t.Fatalf("claim second attempt: %v", err)
	}
	if current.SnapshotHash != "hash-2" {
		t.Fatalf("second attempt snapshot hash = %q, want hash-2", current.SnapshotHash)
	}

	staleAttempt.Category = model.SessionCategoryCompleted
	staleAttempt.Summary = "stale completion"
	staleAttempt.Confidence = 0.2
	completed, err := st.CompleteSessionClassificationAttempt(ctx, &staleAttempt)
	if err != nil {
		t.Fatalf("complete stale attempt: %v", err)
	}
	if completed {
		t.Fatalf("expected stale attempt completion to be ignored")
	}

	current.Category = model.SessionCategoryCompleted
	current.Summary = "current completion"
	current.Confidence = 0.9
	completed, err = st.CompleteSessionClassificationAttempt(ctx, &current)
	if err != nil {
		t.Fatalf("complete current attempt: %v", err)
	}
	if !completed {
		t.Fatalf("expected current attempt completion to succeed")
	}

	stored, err := st.GetSessionClassification(ctx, base.SessionID)
	if err != nil {
		t.Fatalf("get stored classification: %v", err)
	}
	if stored.Status != model.ClassificationCompleted {
		t.Fatalf("stored status = %s, want completed", stored.Status)
	}
	if stored.Stage != "" {
		t.Fatalf("stored stage = %q, want empty", stored.Stage)
	}
	if stored.Summary != "current completion" {
		t.Fatalf("stored summary = %q, want current completion", stored.Summary)
	}
}

func TestOpenRepairsTerminalSessionClassificationStages(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	dbPath := filepath.Join(t.TempDir(), "repair.sqlite")
	db, err := sql.Open("sqlite", dbPath)
	if err != nil {
		t.Fatalf("open sqlite: %v", err)
	}
	db.SetMaxOpenConns(1)
	_, err = db.ExecContext(ctx, `
		CREATE TABLE session_classifications (
			session_id TEXT PRIMARY KEY,
			project_path TEXT NOT NULL,
			session_file TEXT NOT NULL,
			session_format TEXT NOT NULL,
			snapshot_hash TEXT NOT NULL,
			status TEXT NOT NULL,
			stage TEXT NOT NULL DEFAULT '',
			category TEXT NOT NULL DEFAULT '',
			summary TEXT NOT NULL DEFAULT '',
			confidence REAL NOT NULL DEFAULT 0,
			model TEXT NOT NULL DEFAULT '',
			classifier_version TEXT NOT NULL DEFAULT '',
			last_error TEXT NOT NULL DEFAULT '',
			source_updated_at INTEGER NOT NULL,
			created_at INTEGER NOT NULL,
			stage_started_at INTEGER,
			updated_at INTEGER NOT NULL,
			completed_at INTEGER
		);
		INSERT INTO session_classifications(
			session_id, project_path, session_file, session_format, snapshot_hash,
			status, stage, category, summary, confidence, model, classifier_version,
			last_error, source_updated_at, created_at, stage_started_at, updated_at, completed_at
		) VALUES (
			'ses_repair', '/tmp/repair', '/tmp/repair/session.jsonl', 'modern', 'hash-repair',
			'completed', 'waiting_for_model', 'completed', 'done', 1, 'gpt-5-mini', 'v1',
			'', 1, 1, 123, 2, 2
		);
	`)
	if err != nil {
		t.Fatalf("seed legacy terminal row: %v", err)
	}
	if err := db.Close(); err != nil {
		t.Fatalf("close sqlite: %v", err)
	}

	st, err := Open(dbPath)
	if err != nil {
		t.Fatalf("open repaired store: %v", err)
	}
	defer st.Close()

	stored, err := st.GetSessionClassification(ctx, "ses_repair")
	if err != nil {
		t.Fatalf("get repaired classification: %v", err)
	}
	if stored.Status != model.ClassificationCompleted {
		t.Fatalf("status = %s, want completed", stored.Status)
	}
	if stored.Stage != "" {
		t.Fatalf("stage = %q, want empty", stored.Stage)
	}
	if !stored.StageStartedAt.IsZero() {
		t.Fatalf("expected repaired stage timestamp to be zero, got %v", stored.StageStartedAt)
	}
}

func TestQueueSessionClassificationFailedSameSnapshotCanRetryImmediatelyWhenForced(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	dbPath := filepath.Join(t.TempDir(), "little-control-room.sqlite")
	st, err := Open(dbPath)
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	defer st.Close()

	now := time.Now().UTC().Truncate(time.Second)
	classification := model.SessionClassification{
		SessionID:         "ses_force_retry",
		ProjectPath:       "/tmp/force-retry",
		SessionFile:       "/tmp/force-retry/session.jsonl",
		SessionFormat:     "modern",
		SnapshotHash:      "hash-force-retry",
		Model:             "gpt-5-mini",
		ClassifierVersion: "v1",
		SourceUpdatedAt:   now,
	}
	if err := st.UpsertProjectState(ctx, model.ProjectState{
		Path:         classification.ProjectPath,
		Name:         "force-retry",
		LastActivity: now,
		Status:       model.StatusIdle,
		InScope:      true,
		UpdatedAt:    now,
		Sessions: []model.SessionEvidence{{
			SessionID:   classification.SessionID,
			ProjectPath: classification.ProjectPath,
			SessionFile: classification.SessionFile,
			Format:      classification.SessionFormat,
			LastEventAt: now,
		}},
	}); err != nil {
		t.Fatalf("upsert project state: %v", err)
	}

	if queued, err := st.QueueSessionClassification(ctx, classification, 15*time.Minute); err != nil || !queued {
		t.Fatalf("initial queue: queued=%v err=%v", queued, err)
	}
	claimed, err := st.ClaimNextPendingSessionClassification(ctx, time.Minute)
	if err != nil {
		t.Fatalf("claim classification: %v", err)
	}
	if err := st.FailSessionClassification(ctx, claimed.SessionID, "network offline"); err != nil {
		t.Fatalf("fail classification: %v", err)
	}

	if queued, err := st.QueueSessionClassification(ctx, classification, 15*time.Minute); err != nil {
		t.Fatalf("normal retry queue: %v", err)
	} else if queued {
		t.Fatalf("expected retry window to block same-snapshot failed classification")
	}

	if queued, err := st.QueueSessionClassification(ctx, classification, 0); err != nil {
		t.Fatalf("forced retry queue: %v", err)
	} else if !queued {
		t.Fatalf("expected forced retry to requeue failed classification immediately")
	}

	requeued, err := st.ClaimNextPendingSessionClassification(ctx, time.Minute)
	if err != nil {
		t.Fatalf("claim requeued classification: %v", err)
	}
	if requeued.SessionID != classification.SessionID {
		t.Fatalf("requeued session_id = %s, want %s", requeued.SessionID, classification.SessionID)
	}
}

func TestQueueSessionClassificationRunningSameSnapshotDoesNotRetryImmediately(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	dbPath := filepath.Join(t.TempDir(), "little-control-room.sqlite")
	st, err := Open(dbPath)
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	defer st.Close()

	now := time.Now().UTC().Truncate(time.Second)
	classification := model.SessionClassification{
		SessionID:         "ses_running_retry",
		ProjectPath:       "/tmp/running-retry",
		SessionFile:       "/tmp/running-retry/session.jsonl",
		SessionFormat:     "modern",
		SnapshotHash:      "hash-running-retry",
		Model:             "gpt-5-mini",
		ClassifierVersion: "v1",
		SourceUpdatedAt:   now,
	}
	if err := st.UpsertProjectState(ctx, model.ProjectState{
		Path:         classification.ProjectPath,
		Name:         "running-retry",
		LastActivity: now,
		Status:       model.StatusIdle,
		InScope:      true,
		UpdatedAt:    now,
		Sessions: []model.SessionEvidence{{
			SessionID:   classification.SessionID,
			ProjectPath: classification.ProjectPath,
			SessionFile: classification.SessionFile,
			Format:      classification.SessionFormat,
			LastEventAt: now,
		}},
	}); err != nil {
		t.Fatalf("upsert project state: %v", err)
	}

	if queued, err := st.QueueSessionClassification(ctx, classification, 15*time.Minute); err != nil || !queued {
		t.Fatalf("initial queue: queued=%v err=%v", queued, err)
	}
	if _, err := st.ClaimNextPendingSessionClassification(ctx, time.Minute); err != nil {
		t.Fatalf("claim classification: %v", err)
	}

	if queued, err := st.QueueSessionClassification(ctx, classification, 0); err != nil {
		t.Fatalf("forced retry queue while running: %v", err)
	} else if queued {
		t.Fatalf("expected running same-snapshot classification to stay blocked")
	}

	stored, err := st.GetSessionClassification(ctx, classification.SessionID)
	if err != nil {
		t.Fatalf("get classification: %v", err)
	}
	if stored.Status != model.ClassificationRunning {
		t.Fatalf("status = %s, want running", stored.Status)
	}
}

func TestPathAliasesAndFingerprintsRoundTrip(t *testing.T) {
	t.Parallel()
	ctx := context.Background()

	dbPath := filepath.Join(t.TempDir(), "little-control-room.sqlite")
	st, err := Open(dbPath)
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	defer st.Close()

	now := time.Now().UTC().Truncate(time.Second)
	if err := st.UpsertPathAlias(ctx, model.PathAlias{
		OldPath:   "/tmp/old-path",
		NewPath:   "/tmp/new-path",
		Reason:    "git_recent_hash_match",
		UpdatedAt: now,
	}); err != nil {
		t.Fatalf("upsert path alias: %v", err)
	}
	if err := st.UpsertProjectGitFingerprint(ctx, model.ProjectGitFingerprint{
		ProjectPath:  "/tmp/new-path",
		HeadHash:     "abc123",
		RecentHashes: []string{"abc123", "def456", "789xyz"},
		UpdatedAt:    now,
	}); err != nil {
		t.Fatalf("upsert git fingerprint: %v", err)
	}

	aliases, err := st.GetPathAliases(ctx)
	if err != nil {
		t.Fatalf("get path aliases: %v", err)
	}
	alias, ok := aliases["/tmp/old-path"]
	if !ok {
		t.Fatalf("expected stored alias for old path")
	}
	if alias.NewPath != "/tmp/new-path" || alias.Reason != "git_recent_hash_match" {
		t.Fatalf("unexpected alias: %#v", alias)
	}

	fingerprints, err := st.GetProjectGitFingerprints(ctx)
	if err != nil {
		t.Fatalf("get git fingerprints: %v", err)
	}
	fingerprint, ok := fingerprints["/tmp/new-path"]
	if !ok {
		t.Fatalf("expected stored git fingerprint")
	}
	if fingerprint.HeadHash != "abc123" {
		t.Fatalf("head hash = %s, want abc123", fingerprint.HeadHash)
	}
	if len(fingerprint.RecentHashes) != 3 || fingerprint.RecentHashes[1] != "def456" {
		t.Fatalf("unexpected recent hashes: %#v", fingerprint.RecentHashes)
	}
}

func TestMoveProjectPathPreservesData(t *testing.T) {
	t.Parallel()
	ctx := context.Background()

	dbPath := filepath.Join(t.TempDir(), "little-control-room.sqlite")
	st, err := Open(dbPath)
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	defer st.Close()

	now := time.Now().Add(-30 * time.Minute).UTC().Truncate(time.Second)
	oldPath := "/tmp/old-project"
	newPath := "/tmp/new-project"

	if err := st.UpsertProjectState(ctx, model.ProjectState{
		Path:           oldPath,
		Name:           "old-project",
		LastActivity:   now,
		Status:         model.StatusIdle,
		AttentionScore: 12,
		RepoDirty:      true,
		RepoSyncStatus: model.RepoSyncAhead,
		RepoAheadCount: 2,
		InScope:        true,
		Pinned:         true,
		Note:           "keep this note",
		AttentionReason: []model.AttentionReason{{
			Code:   "idle",
			Text:   "Idle for a while",
			Weight: 10,
		}},
		Sessions: []model.SessionEvidence{{
			SessionID:           "ses_move",
			ProjectPath:         oldPath,
			DetectedProjectPath: oldPath,
			SessionFile:         "/tmp/ses_move.jsonl",
			Format:              "modern",
			StartedAt:           now.Add(-10 * time.Minute),
			LastEventAt:         now,
			ErrorCount:          1,
		}},
		Artifacts: []model.ArtifactEvidence{{
			Path:      "/tmp/out.txt",
			Kind:      "file",
			UpdatedAt: now,
			Note:      "artifact",
		}},
		UpdatedAt: now,
	}); err != nil {
		t.Fatalf("upsert project state: %v", err)
	}
	if err := st.AddEvent(ctx, model.StoredEvent{
		At:          now,
		ProjectPath: oldPath,
		Type:        "project_changed",
		Payload:     "status=idle score=12",
	}); err != nil {
		t.Fatalf("add event: %v", err)
	}
	if queued, err := st.QueueSessionClassification(ctx, model.SessionClassification{
		SessionID:         "ses_move",
		ProjectPath:       oldPath,
		SessionFile:       "/tmp/ses_move.jsonl",
		SessionFormat:     "modern",
		SnapshotHash:      "hash-move-1",
		Model:             "gpt-5-mini",
		ClassifierVersion: "v1",
		SourceUpdatedAt:   now,
	}, time.Minute); err != nil || !queued {
		t.Fatalf("queue classification: queued=%v err=%v", queued, err)
	}

	movedAt := time.Now().UTC().Truncate(time.Second)
	if err := st.MoveProjectPath(ctx, oldPath, newPath, movedAt); err != nil {
		t.Fatalf("move project path: %v", err)
	}

	if _, err := st.GetProjectDetail(ctx, oldPath, 5); err == nil {
		t.Fatalf("expected old project path to be gone after move")
	}

	detail, err := st.GetProjectDetail(ctx, newPath, 5)
	if err != nil {
		t.Fatalf("get moved project detail: %v", err)
	}
	if detail.Summary.Path != newPath {
		t.Fatalf("summary path = %s, want %s", detail.Summary.Path, newPath)
	}
	if detail.Summary.MovedFromPath != oldPath {
		t.Fatalf("moved_from_path = %s, want %s", detail.Summary.MovedFromPath, oldPath)
	}
	if detail.Summary.MovedAt.Unix() != movedAt.Unix() {
		t.Fatalf("moved_at = %s, want %s", detail.Summary.MovedAt, movedAt)
	}
	if !detail.Summary.RepoDirty {
		t.Fatalf("expected repo_dirty to survive move: %#v", detail.Summary)
	}
	if detail.Summary.RepoSyncStatus != model.RepoSyncAhead || detail.Summary.RepoAheadCount != 2 || detail.Summary.RepoBehindCount != 0 {
		t.Fatalf("expected repo sync data to survive move: %#v", detail.Summary)
	}
	if !detail.Summary.Pinned || detail.Summary.Note != "keep this note" {
		t.Fatalf("expected pin/note to survive move: %#v", detail.Summary)
	}
	if len(detail.Reasons) != 1 || detail.Reasons[0].Code != "idle" {
		t.Fatalf("expected reasons to survive move, got %#v", detail.Reasons)
	}
	if len(detail.Sessions) != 1 || detail.Sessions[0].ProjectPath != newPath {
		t.Fatalf("expected sessions to move paths, got %#v", detail.Sessions)
	}
	if detail.Sessions[0].DetectedProjectPath != oldPath {
		t.Fatalf("expected detected path to preserve origin, got %#v", detail.Sessions)
	}
	if len(detail.Artifacts) != 1 {
		t.Fatalf("expected artifacts to survive move, got %#v", detail.Artifacts)
	}
	if len(detail.RecentEvents) != 1 || detail.RecentEvents[0].ProjectPath != newPath {
		t.Fatalf("expected events to move paths, got %#v", detail.RecentEvents)
	}

	classification, err := st.GetSessionClassification(ctx, "ses_move")
	if err != nil {
		t.Fatalf("get moved classification: %v", err)
	}
	if classification.ProjectPath != newPath {
		t.Fatalf("classification project path = %s, want %s", classification.ProjectPath, newPath)
	}
}

func TestRememberRecentProjectParentPathKeepsNewestUniquePaths(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	st, err := Open(filepath.Join(t.TempDir(), "little-control-room.sqlite"))
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	defer st.Close()

	for _, path := range []string{"/tmp/one", "/tmp/two", "/tmp/three", "/tmp/two", "/tmp/four"} {
		if err := st.RememberRecentProjectParentPath(ctx, path, 3); err != nil {
			t.Fatalf("remember recent parent path %q: %v", path, err)
		}
	}

	got, err := st.ListRecentProjectParentPaths(ctx, 5)
	if err != nil {
		t.Fatalf("list recent parent paths: %v", err)
	}
	want := []string{"/tmp/four", "/tmp/two", "/tmp/three"}
	if len(got) != len(want) {
		t.Fatalf("recent parent path count = %d, want %d (%v)", len(got), len(want), got)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("recent parent path %d = %q, want %q (all=%v)", i, got[i], want[i], got)
		}
	}
}
