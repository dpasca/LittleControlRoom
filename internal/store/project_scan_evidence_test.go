package store

import (
	"context"
	"fmt"
	"path/filepath"
	"sort"
	"testing"
	"time"

	"lcroom/internal/model"
)

func TestGetProjectScanEvidenceMapLoadsBoundedOrderedEvidence(t *testing.T) {
	ctx := context.Background()
	st, err := Open(filepath.Join(t.TempDir(), "little-control-room.sqlite"))
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	defer st.Close()

	now := time.Date(2026, 7, 13, 12, 0, 0, 0, time.UTC)
	state := model.ProjectState{
		Path:          "/tmp/demo",
		Name:          "demo",
		PresentOnDisk: true,
		InScope:       true,
		AttentionReason: []model.AttentionReason{
			{Code: "first", Text: "First reason", Weight: 10},
			{Code: "second", Text: "Second reason", Weight: 5},
		},
		UpdatedAt: now,
	}
	for i := 0; i < 35; i++ {
		state.Sessions = append(state.Sessions, model.SessionEvidence{
			Source:      model.SessionSourceCodex,
			SessionID:   fmt.Sprintf("codex:session-%02d", i),
			ProjectPath: state.Path,
			Format:      "modern",
			LastEventAt: now.Add(time.Duration(i) * time.Second),
		})
	}
	for i := 0; i < 55; i++ {
		state.Artifacts = append(state.Artifacts, model.ArtifactEvidence{
			Path:      fmt.Sprintf("artifact-%02d.txt", i),
			Kind:      "file",
			UpdatedAt: now.Add(time.Duration(i) * time.Second),
		})
	}
	if err := st.UpsertProjectState(ctx, state); err != nil {
		t.Fatalf("seed state: %v", err)
	}

	evidenceByProject, err := st.GetProjectScanEvidenceMap(ctx)
	if err != nil {
		t.Fatalf("GetProjectScanEvidenceMap() error = %v", err)
	}
	evidence := evidenceByProject[state.Path]
	if len(evidence.Reasons) != 2 || evidence.Reasons[0].Code != "first" || evidence.Reasons[1].Code != "second" {
		t.Fatalf("reasons = %#v, want stored order", evidence.Reasons)
	}
	if len(evidence.Sessions) != 30 {
		t.Fatalf("sessions = %d, want bounded latest 30", len(evidence.Sessions))
	}
	if evidence.Sessions[0].SessionID != "codex:session-34" || evidence.Sessions[29].SessionID != "codex:session-05" {
		t.Fatalf("session bounds = %q..%q, want latest descending", evidence.Sessions[0].SessionID, evidence.Sessions[29].SessionID)
	}
	if len(evidence.Artifacts) != 50 {
		t.Fatalf("artifacts = %d, want bounded latest 50", len(evidence.Artifacts))
	}
	if evidence.Artifacts[0].Path != "artifact-54.txt" || evidence.Artifacts[49].Path != "artifact-05.txt" {
		t.Fatalf("artifact bounds = %q..%q, want latest descending", evidence.Artifacts[0].Path, evidence.Artifacts[49].Path)
	}
}

func TestGetOrphanedWorktreeSummaryMapExcludesOrdinaryAndMissingRows(t *testing.T) {
	ctx := context.Background()
	st, err := Open(filepath.Join(t.TempDir(), "little-control-room.sqlite"))
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	defer st.Close()

	rootPath := "/tmp/repo"
	states := []model.ProjectState{
		{Path: rootPath, Name: "repo", PresentOnDisk: true, InScope: true, WorktreeRootPath: rootPath, WorktreeKind: model.WorktreeKindMain},
		{Path: "/tmp/repo--orphan", Name: "orphan", PresentOnDisk: true, Forgotten: true, WorktreeRootPath: rootPath, WorktreeKind: model.WorktreeKindLinked},
		{Path: "/tmp/repo--missing", Name: "missing", PresentOnDisk: false, Forgotten: true, WorktreeRootPath: rootPath, WorktreeKind: model.WorktreeKindLinked},
		{Path: "/tmp/repo--active", Name: "active", PresentOnDisk: true, Forgotten: false, WorktreeRootPath: rootPath, WorktreeKind: model.WorktreeKindLinked},
	}
	for _, state := range states {
		if err := st.UpsertProjectState(ctx, state); err != nil {
			t.Fatalf("seed %s: %v", state.Path, err)
		}
	}

	orphans, err := st.GetOrphanedWorktreeSummaryMap(ctx)
	if err != nil {
		t.Fatalf("GetOrphanedWorktreeSummaryMap() error = %v", err)
	}
	if len(orphans) != 1 || orphans["/tmp/repo--orphan"].Path == "" {
		t.Fatalf("orphaned worktrees = %#v, want only present forgotten linked row", orphans)
	}
}

func TestProjectReadPerformanceIndexesExist(t *testing.T) {
	st, err := Open(filepath.Join(t.TempDir(), "little-control-room.sqlite"))
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	defer st.Close()

	rows, err := st.db.Query(`
		SELECT name
		FROM sqlite_master
		WHERE type = 'index' AND name IN (
			'idx_project_sessions_project_last_event',
			'idx_project_artifacts_project_updated',
			'idx_session_classifications_raw_session_updated',
			'idx_session_classifications_project_status_completed',
			'idx_events_project_id'
		)
	`)
	if err != nil {
		t.Fatalf("list performance indexes: %v", err)
	}
	defer rows.Close()

	var names []string
	for rows.Next() {
		var name string
		if err := rows.Scan(&name); err != nil {
			t.Fatalf("scan index name: %v", err)
		}
		names = append(names, name)
	}
	if err := rows.Err(); err != nil {
		t.Fatalf("list index names: %v", err)
	}
	sort.Strings(names)
	want := []string{
		"idx_events_project_id",
		"idx_project_artifacts_project_updated",
		"idx_project_sessions_project_last_event",
		"idx_session_classifications_project_status_completed",
		"idx_session_classifications_raw_session_updated",
	}
	if fmt.Sprint(names) != fmt.Sprint(want) {
		t.Fatalf("performance indexes = %v, want %v", names, want)
	}
}
