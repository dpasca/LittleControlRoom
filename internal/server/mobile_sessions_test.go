package server

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"testing"
	"time"

	"lcroom/internal/codexapp"
	"lcroom/internal/config"
	"lcroom/internal/events"
	"lcroom/internal/model"
	"lcroom/internal/service"
	"lcroom/internal/store"
	"lcroom/internal/uisurface"
)

type fakeLiveSessionSource struct {
	snapshot codexapp.Snapshot
	ok       bool
}

func (s fakeLiveSessionSource) TrySessionSnapshot(string) (codexapp.Snapshot, bool) {
	return s.snapshot, s.ok
}

func TestMobileSessionEndpointsMergeLiveAndRecordedSessions(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	dataDir := t.TempDir()
	dbPath := filepath.Join(dataDir, "little-control-room.sqlite")
	st, err := store.Open(dbPath)
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	defer st.Close()

	now := time.Now()
	projectPath := "/tmp/mobile-sessions"
	if err := st.UpsertProjectState(ctx, model.ProjectState{
		Path:          projectPath,
		Name:          "Mobile sessions",
		PresentOnDisk: true,
		InScope:       true,
		Status:        model.StatusActive,
		LastActivity:  now,
		UpdatedAt:     now,
		Sessions: []model.SessionEvidence{{
			Source:               model.SessionSourceCodex,
			SessionID:            "codex:recorded-session",
			RawSessionID:         "recorded-session",
			ProjectPath:          projectPath,
			Format:               "modern",
			LastEventAt:          now.Add(-time.Hour),
			LatestTurnStateKnown: true,
			LatestTurnCompleted:  true,
		}},
	}); err != nil {
		t.Fatalf("upsert project: %v", err)
	}

	cfg := config.Default()
	cfg.DataDir = dataDir
	cfg.DBPath = dbPath
	svc := service.New(cfg, st, events.NewBus(), nil)
	handler := New(svc).WithLiveSessions(fakeLiveSessionSource{
		ok: true,
		snapshot: codexapp.Snapshot{
			Provider:           codexapp.ProviderCodex,
			ProjectPath:        projectPath,
			ThreadID:           "live-session",
			TranscriptRevision: 4,
			Started:            true,
			Busy:               true,
			Phase:              codexapp.SessionPhaseRunning,
			LastActivityAt:     now,
			Model:              "gpt-5.4",
			Entries: []codexapp.TranscriptEntry{
				{Kind: codexapp.TranscriptUser, Text: "Show me the live session."},
				{Kind: codexapp.TranscriptAgent, Text: "The transcript is connected."},
			},
		},
	}).Handler(ctx)

	listResponse := httptest.NewRecorder()
	handler.ServeHTTP(listResponse, httptest.NewRequest(http.MethodGet, "/api/mobile/projects/sessions?path="+projectPath, nil))
	if listResponse.Code != http.StatusOK {
		t.Fatalf("GET sessions status = %d, body = %s", listResponse.Code, listResponse.Body.String())
	}
	var list uisurface.EngineerSessionListSurface
	if err := json.Unmarshal(listResponse.Body.Bytes(), &list); err != nil {
		t.Fatalf("decode session list: %v", err)
	}
	if got, want := len(list.Sessions), 2; got != want {
		t.Fatalf("session count = %d, want %d", got, want)
	}
	if !list.Sessions[0].Live || list.Sessions[0].ID != "codex:live-session" {
		t.Fatalf("first session = %#v, want live session", list.Sessions[0])
	}
	if list.Sessions[1].Live || list.Sessions[1].ID != "codex:recorded-session" {
		t.Fatalf("second session = %#v, want recorded session", list.Sessions[1])
	}

	detailResponse := httptest.NewRecorder()
	handler.ServeHTTP(detailResponse, httptest.NewRequest(http.MethodGet, "/api/mobile/sessions/detail?path="+projectPath+"&session_id=codex%3Alive-session", nil))
	if detailResponse.Code != http.StatusOK {
		t.Fatalf("GET live session status = %d, body = %s", detailResponse.Code, detailResponse.Body.String())
	}
	var detail uisurface.EngineerSessionDetailSurface
	if err := json.Unmarshal(detailResponse.Body.Bytes(), &detail); err != nil {
		t.Fatalf("decode live session: %v", err)
	}
	if got, want := len(detail.Entries), 2; got != want {
		t.Fatalf("live transcript entries = %d, want %d", got, want)
	}
	if got, want := detail.Entries[1].Text, "The transcript is connected."; got != want {
		t.Fatalf("latest transcript text = %q, want %q", got, want)
	}

	missingResponse := httptest.NewRecorder()
	handler.ServeHTTP(missingResponse, httptest.NewRequest(http.MethodGet, "/api/mobile/sessions/detail?path="+projectPath+"&session_id=unknown", nil))
	if missingResponse.Code != http.StatusNotFound {
		t.Fatalf("GET unknown session status = %d, want 404", missingResponse.Code)
	}
}

func TestMobileSessionSourceCannotCrossProjectBoundary(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	dataDir := t.TempDir()
	dbPath := filepath.Join(dataDir, "little-control-room.sqlite")
	st, err := store.Open(dbPath)
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	defer st.Close()

	projectPath := "/tmp/mobile-visible"
	if err := st.UpsertProjectState(ctx, model.ProjectState{
		Path:          projectPath,
		Name:          "Visible project",
		PresentOnDisk: true,
		InScope:       true,
		UpdatedAt:     time.Now(),
	}); err != nil {
		t.Fatalf("upsert project: %v", err)
	}

	cfg := config.Default()
	cfg.DataDir = dataDir
	cfg.DBPath = dbPath
	handler := New(service.New(cfg, st, events.NewBus(), nil)).WithLiveSessions(fakeLiveSessionSource{
		ok: true,
		snapshot: codexapp.Snapshot{
			ProjectPath: "/tmp/different-project",
			ThreadID:    "private-live-session",
		},
	}).Handler(ctx)

	response := httptest.NewRecorder()
	handler.ServeHTTP(response, httptest.NewRequest(http.MethodGet, "/api/mobile/projects/sessions?path="+projectPath, nil))
	if response.Code != http.StatusOK {
		t.Fatalf("GET sessions status = %d", response.Code)
	}
	var list uisurface.EngineerSessionListSurface
	if err := json.Unmarshal(response.Body.Bytes(), &list); err != nil {
		t.Fatalf("decode session list: %v", err)
	}
	if len(list.Sessions) != 0 {
		t.Fatalf("cross-project live session leaked: %#v", list.Sessions)
	}
}
