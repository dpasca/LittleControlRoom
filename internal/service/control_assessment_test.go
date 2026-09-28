package service

import (
	"database/sql"
	"encoding/json"
	"errors"
	"path/filepath"
	"testing"
	"time"

	"lcroom/internal/config"
	"lcroom/internal/control"
	"lcroom/internal/detectors"
	"lcroom/internal/events"
	"lcroom/internal/model"
	"lcroom/internal/sessionclassify"
	"lcroom/internal/store"
)

func TestControlConfirmationRequeuesAssessmentOnScanAndRefresh(t *testing.T) {
	ctx := t.Context()
	root := t.TempDir()
	project := filepath.Join(root, "project")
	initGitRepo(t, project)
	st, err := store.Open(filepath.Join(root, "state.sqlite"))
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	now := time.Now()
	session := model.SessionEvidence{
		Source: model.SessionSourceCodex, SessionID: "control-assessment", ProjectPath: project,
		SessionFile: filepath.Join("..", "..", "testdata", "codex_footprint", "sessions", "2026", "03", "05", "rollout-modern.jsonl"),
		Format:      "modern", LastEventAt: now, StartedAt: now.Add(-time.Minute), LatestTurnStateKnown: true, LatestTurnCompleted: true,
	}
	cfg := config.Default()
	cfg.IncludePaths = []string{project}
	detector := &fakeDetector{activities: map[string]*model.DetectorProjectActivity{
		project: {ProjectPath: project, Source: "codex", LastActivity: now, Sessions: []model.SessionEvidence{session}},
	}}
	svc := New(cfg, st, events.NewBus(), []detectors.Detector{detector})
	svc.SetSessionClassifier(sessionclassify.NewManager(st, nil, sessionclassify.Options{Client: unusedIntegrationClassifier{}}))
	if err := st.BindEngineerSession(ctx, project, model.SessionSourceCodex, "channel", session.SessionID); err != nil {
		t.Fatal(err)
	}
	args, _ := json.Marshal(control.TodoAddInput{ProjectPath: project, Text: "queued copy"})
	if _, err := st.CreateControlOperation(ctx, control.Operation{ID: "op", Provider: "codex", ProjectPath: project, SessionKey: "channel",
		Invocation: control.Invocation{Capability: control.CapabilityTodoAdd, Args: args},
	}); err != nil {
		t.Fatal(err)
	}
	previousHash := ""
	for i, status := range []control.OperationStatus{control.OperationWaitingForConfirmation, control.OperationRunning, control.OperationCompleted} {
		if _, err := st.UpdateControlOperationStatus(ctx, "op", status, nil, nil); err != nil {
			t.Fatal(err)
		}
		if i == 1 {
			err = svc.RefreshProjectStatus(ctx, project)
		} else {
			_, err = svc.ScanOnce(ctx)
		}
		if err != nil {
			t.Fatal(err)
		}
		claimed, err := st.ClaimNextPendingSessionClassification(ctx, time.Minute)
		if err != nil {
			t.Fatalf("expected reassessment for %s: %v", status, err)
		}
		if claimed.SnapshotHash == previousHash {
			t.Fatal("stale cached assessment reused")
		}
		detail, err := st.GetProjectDetail(ctx, project, 1)
		if err != nil {
			t.Fatal(err)
		}
		if detail.Sessions[0].SnapshotHash != claimed.SnapshotHash {
			t.Fatal("service and manager hashes disagree")
		}
		claimed.Category = model.SessionCategoryNeedsFollowUp
		claimed.Summary = "Remaining implementation."
		if err := st.CompleteSessionClassification(ctx, claimed); err != nil {
			t.Fatal(err)
		}
		previousHash = claimed.SnapshotHash
		// Both refresh paths must preserve a result with unchanged host evidence.
		if err := svc.RefreshProjectStatus(ctx, project); err != nil {
			t.Fatal(err)
		}
		if _, err := svc.ScanOnce(ctx); err != nil {
			t.Fatal(err)
		}
		if _, err := st.ClaimNextPendingSessionClassification(ctx, time.Minute); !errors.Is(err, sql.ErrNoRows) {
			t.Fatalf("unchanged evidence queued another model call: %v", err)
		}
	}
}
