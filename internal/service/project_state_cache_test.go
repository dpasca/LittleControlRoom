package service

import (
	"context"
	"database/sql"
	"os"
	"path/filepath"
	"testing"
	"time"

	"lcroom/internal/config"
	"lcroom/internal/events"
	"lcroom/internal/model"
	"lcroom/internal/store"
)

func TestProjectStateCacheIgnoresScanTimestampButDetectsEvidenceChanges(t *testing.T) {
	t.Parallel()

	svc := &Service{}
	now := time.Date(2026, 7, 13, 12, 0, 0, 0, time.UTC)
	state := model.ProjectState{
		Path:           "/tmp/demo",
		Name:           "demo",
		Status:         model.StatusActive,
		AttentionScore: 50,
		PresentOnDisk:  true,
		InScope:        true,
		Sessions: []model.SessionEvidence{{
			Source:      model.SessionSourceCodex,
			SessionID:   "codex:thread-demo",
			LastEventAt: now,
		}},
		UpdatedAt: now,
	}
	svc.rememberProjectState(state)

	sameState := state
	sameState.UpdatedAt = now.Add(time.Minute)
	if svc.projectStateNeedsPersistence(sameState) {
		t.Fatal("scan timestamp alone should not require persistence")
	}

	changed := sameState
	changed.Sessions = append([]model.SessionEvidence(nil), sameState.Sessions...)
	changed.Sessions[0].LastEventAt = now.Add(time.Second)
	if !svc.projectStateNeedsPersistence(changed) {
		t.Fatal("changed session evidence should require persistence")
	}

	cached, ok := svc.cachedProjectState(state.Path)
	if !ok {
		t.Fatal("expected cached project state")
	}
	cached.Sessions[0].SessionID = "mutated-copy"
	again, _ := svc.cachedProjectState(state.Path)
	if again.Sessions[0].SessionID != state.Sessions[0].SessionID {
		t.Fatal("cached state should be returned as an immutable copy")
	}
}

func TestScanSkipsUnchangedProjectStateRewrite(t *testing.T) {
	ctx := context.Background()
	root := t.TempDir()
	projectPath := filepath.Join(root, "demo")
	if err := os.MkdirAll(projectPath, 0o755); err != nil {
		t.Fatalf("mkdir project: %v", err)
	}
	dbPath := filepath.Join(root, "little-control-room.sqlite")
	st, err := store.Open(dbPath)
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	defer st.Close()

	createdAt := time.Now().Add(-10 * 24 * time.Hour)
	if err := st.UpsertProjectState(ctx, model.ProjectState{
		Path:           projectPath,
		Name:           "demo",
		Kind:           model.ProjectKindProject,
		Status:         model.StatusIdle,
		AttentionScore: 10,
		PresentOnDisk:  true,
		ManuallyAdded:  true,
		InScope:        true,
		AttentionReason: []model.AttentionReason{{
			Code:   "no_activity",
			Text:   "No agent activity detected yet",
			Weight: 10,
		}},
		CreatedAt: createdAt,
		UpdatedAt: createdAt,
	}); err != nil {
		t.Fatalf("seed project: %v", err)
	}

	triggerDB, err := sql.Open("sqlite", dbPath)
	if err != nil {
		t.Fatalf("open trigger connection: %v", err)
	}
	if _, err := triggerDB.ExecContext(ctx, `
		CREATE TRIGGER reject_unchanged_project_update
		BEFORE UPDATE ON projects
		BEGIN
			SELECT RAISE(ABORT, 'unexpected project rewrite');
		END;
	`); err != nil {
		triggerDB.Close()
		t.Fatalf("create update guard trigger: %v", err)
	}
	if err := triggerDB.Close(); err != nil {
		t.Fatalf("close trigger connection: %v", err)
	}

	cfg := config.Default()
	cfg.IncludePaths = nil
	svc := New(cfg, st, events.NewBus(), nil)
	svc.gitFingerprintReader = nil
	svc.gitRepoStatusReader = nil
	svc.gitWorktreeInfoReader = nil
	svc.gitWorktreeListReader = nil

	report, err := svc.ScanWithOptions(ctx, ScanOptions{})
	if err != nil {
		t.Fatalf("ScanWithOptions() rewrote unchanged project state: %v", err)
	}
	if report.TrackedProjectCount != 1 {
		t.Fatalf("tracked projects = %d, want 1", report.TrackedProjectCount)
	}
}
