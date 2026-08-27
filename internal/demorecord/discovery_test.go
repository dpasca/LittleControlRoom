package demorecord

import (
	"context"
	"path/filepath"
	"testing"
)

func TestDiscoveryPrefersActiveRecordingAndRetainsAssociation(t *testing.T) {
	dataDir := t.TempDir()
	controller := NewControllerWithDataDir(dataDir)
	firstPath := filepath.Join(t.TempDir(), "first.lcrdemo")
	if _, err := controller.Start(firstPath); err != nil {
		t.Fatalf("start first recording: %v", err)
	}
	controller.Capture(80, 24, "first")
	if _, stopped, err := controller.Stop(); err != nil || !stopped {
		t.Fatalf("stop first recording = stopped %t, error %v", stopped, err)
	}

	secondPath := filepath.Join(t.TempDir(), "second.lcrdemo")
	association := Association{
		ProjectPath: "/repos/demo",
		Provider:    "Codex",
		SessionID:   "thread-demo",
	}
	if _, err := controller.StartWithAssociation(secondPath, association); err != nil {
		t.Fatalf("start associated recording: %v", err)
	}
	t.Cleanup(func() { _ = controller.Close() })
	controller.Capture(100, 30, "second")

	discovery := NewDiscovery(dataDir)
	active, found, err := discovery.Latest(context.Background())
	if err != nil {
		t.Fatalf("discover active recording: %v", err)
	}
	if !found || active.Status != RecordingStatusActive || active.PackagePath != secondPath {
		t.Fatalf("active recording = %#v, found %t", active, found)
	}
	if active.ID == "" || active.FormatVersion != FormatVersion {
		t.Fatalf("active identity/version = %#v", active)
	}
	if got := active.Association; got.ProjectPath != "/repos/demo" || got.Provider != "codex" || got.SessionID != "thread-demo" {
		t.Fatalf("active association = %#v", got)
	}
	staleView := NewDiscovery(dataDir)
	staleView.processAlive = func(int) bool { return false }
	fallback, found, err := staleView.Latest(context.Background())
	if err != nil {
		t.Fatalf("discover finalized fallback: %v", err)
	}
	if !found || fallback.Status != RecordingStatusFinalized || fallback.PackagePath != firstPath {
		t.Fatalf("stale-active fallback = %#v, found %t", fallback, found)
	}

	if _, stopped, err := controller.Stop(); err != nil || !stopped {
		t.Fatalf("finalize associated recording = stopped %t, error %v", stopped, err)
	}
	finalized, found, err := discovery.Latest(context.Background())
	if err != nil {
		t.Fatalf("discover finalized recording: %v", err)
	}
	if !found || finalized.Status != RecordingStatusFinalized || finalized.PackagePath != secondPath || finalized.CompletedAt.IsZero() {
		t.Fatalf("finalized recording = %#v, found %t", finalized, found)
	}
}

func TestDiscoveryFindsUnreferencedFinalizedPackageInDefaultDirectory(t *testing.T) {
	dataDir := t.TempDir()
	path := filepath.Join(dataDir, "demo-recordings", "legacy.lcrdemo")
	recorder, err := NewRecorder(path, RecorderOptions{RecordingID: "rec_legacy"})
	if err != nil {
		t.Fatalf("create recording: %v", err)
	}
	recorder.Capture(80, 24, "legacy")
	if err := recorder.Close(); err != nil {
		t.Fatalf("finalize recording: %v", err)
	}

	resource, found, err := NewDiscovery(dataDir).Latest(context.Background())
	if err != nil {
		t.Fatalf("discover recording: %v", err)
	}
	if !found || resource.ID != "rec_legacy" || resource.PackagePath != path || resource.Status != RecordingStatusFinalized {
		t.Fatalf("legacy discovery = %#v, found %t", resource, found)
	}
}
