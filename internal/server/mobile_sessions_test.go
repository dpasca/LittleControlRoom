package server

import (
	"bufio"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
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

type fakeLiveSessionMap map[string]codexapp.Snapshot

func (s fakeLiveSessionMap) TrySessionSnapshot(projectPath string) (codexapp.Snapshot, bool) {
	snapshot, ok := s[projectPath]
	return snapshot, ok
}

type fakeLiveSessionController struct {
	snapshot    codexapp.Snapshot
	ok          bool
	result      codexapp.SessionInputResult
	err         error
	projectPath string
	threadID    string
	text        string
	calls       int
}

type sequenceLiveSessionSource struct {
	results []struct {
		snapshot codexapp.Snapshot
		ok       bool
	}
	next int
}

type mutableLiveSessionSource struct {
	mu       sync.RWMutex
	snapshot codexapp.Snapshot
	ok       bool
}

func (s *mutableLiveSessionSource) TrySessionSnapshot(string) (codexapp.Snapshot, bool) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.snapshot, s.ok
}

func (s *mutableLiveSessionSource) Set(snapshot codexapp.Snapshot, ok bool) {
	s.mu.Lock()
	s.snapshot = snapshot
	s.ok = ok
	s.mu.Unlock()
}

func (s *sequenceLiveSessionSource) TrySessionSnapshot(string) (codexapp.Snapshot, bool) {
	if s.next >= len(s.results) {
		return codexapp.Snapshot{}, false
	}
	result := s.results[s.next]
	s.next++
	return result.snapshot, result.ok
}

func (s *fakeLiveSessionController) TrySessionSnapshot(string) (codexapp.Snapshot, bool) {
	return s.snapshot, s.ok
}

func (s *fakeLiveSessionController) SubmitSessionInput(projectPath, expectedThreadID string, input codexapp.Submission) (codexapp.SessionInputResult, error) {
	s.calls++
	s.projectPath = projectPath
	s.threadID = expectedThreadID
	s.text = input.Text
	return s.result, s.err
}

func TestMobileDashboardLiveSessionsPrioritizeAttentionAndSkipClosed(t *testing.T) {
	t.Parallel()
	now := time.Date(2026, time.July, 13, 7, 0, 0, 0, time.UTC)
	server := &Server{liveSessions: fakeLiveSessionMap{
		"/tmp/working": {
			Provider:       codexapp.ProviderCodex,
			ProjectPath:    "/tmp/working",
			ThreadID:       "working",
			Started:        true,
			Busy:           true,
			LastActivityAt: now,
		},
		"/tmp/input": {
			Provider:        codexapp.ProviderClaudeCode,
			ProjectPath:     "/tmp/input",
			ThreadID:        "input",
			Started:         true,
			LastActivityAt:  now.Add(-time.Minute),
			PendingApproval: &codexapp.ApprovalRequest{},
		},
		"/tmp/closed": {
			Provider:    codexapp.ProviderOpenCode,
			ProjectPath: "/tmp/closed",
			ThreadID:    "closed",
			Started:     true,
			Closed:      true,
			Phase:       codexapp.SessionPhaseClosed,
		},
	}}

	items := server.mobileDashboardLiveSessions([]uisurface.ProjectItem{
		{Path: "/tmp/working", Name: "Working project"},
		{Path: "/tmp/input", Name: "Input project"},
		{Path: "/tmp/closed", Name: "Closed project"},
	}, now)
	if got, want := len(items), 2; got != want {
		t.Fatalf("live session count = %d, want %d: %#v", got, want, items)
	}
	if items[0].ProjectName != "Input project" || items[0].Status.Label != "Input needed" {
		t.Fatalf("first live session = %#v, want input-needed channel", items[0])
	}
	if items[1].ProjectName != "Working project" || items[1].Status.Label != "Working" {
		t.Fatalf("second live session = %#v, want working channel", items[1])
	}
}

func TestMobileLiveSessionSnapshotSurvivesBriefLockContention(t *testing.T) {
	t.Parallel()
	projectPath := "/tmp/mobile-live-cache"
	now := time.Date(2026, time.July, 13, 8, 0, 0, 0, time.UTC)
	snapshot := codexapp.Snapshot{
		Provider:           codexapp.ProviderCodex,
		ProjectPath:        projectPath,
		ThreadID:           "live-cache",
		Started:            true,
		Busy:               true,
		TranscriptRevision: 42,
		Entries: []codexapp.TranscriptEntry{
			{Kind: codexapp.TranscriptAgent, Text: "Stable live history"},
		},
	}
	source := &sequenceLiveSessionSource{results: []struct {
		snapshot codexapp.Snapshot
		ok       bool
	}{
		{snapshot: snapshot, ok: true},
		{ok: false},
		{ok: false},
	}}
	server := &Server{liveSessions: source}

	first, ok := server.liveSessionSnapshotAt(projectPath, now)
	if !ok || first.ThreadID != snapshot.ThreadID {
		t.Fatalf("first live snapshot = (%+v, %v), want current session", first, ok)
	}
	cached, ok := server.liveSessionSnapshotAt(projectPath, now.Add(mobileLiveSnapshotCacheTTL/2))
	if !ok || cached.ThreadID != snapshot.ThreadID || cached.TranscriptRevision != snapshot.TranscriptRevision {
		t.Fatalf("contended live snapshot = (%+v, %v), want cached current session", cached, ok)
	}
	if _, ok := server.liveSessionSnapshotAt(projectPath, now.Add(mobileLiveSnapshotCacheTTL+time.Nanosecond)); ok {
		t.Fatal("expired live snapshot should not hide a missing session indefinitely")
	}
}

func TestMobileLiveSessionStreamPushesTranscriptRevisions(t *testing.T) {
	t.Parallel()
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	dataDir := t.TempDir()
	dbPath := filepath.Join(dataDir, "little-control-room.sqlite")
	st, err := store.Open(dbPath)
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	defer st.Close()

	projectPath := "/tmp/mobile-live-stream"
	now := time.Now()
	if err := st.UpsertProjectState(ctx, model.ProjectState{
		Path:          projectPath,
		Name:          "Mobile live stream",
		PresentOnDisk: true,
		InScope:       true,
		UpdatedAt:     now,
	}); err != nil {
		t.Fatalf("upsert project: %v", err)
	}

	snapshot := codexapp.Snapshot{
		Provider:           codexapp.ProviderCodex,
		ProjectPath:        projectPath,
		ThreadID:           "streaming-session",
		Started:            true,
		Busy:               true,
		Phase:              codexapp.SessionPhaseRunning,
		TranscriptRevision: 1,
		LastActivityAt:     now,
		Entries: []codexapp.TranscriptEntry{
			{ItemID: "agent-1", Kind: codexapp.TranscriptAgent, Text: "Starting"},
		},
	}
	source := &mutableLiveSessionSource{snapshot: snapshot, ok: true}
	cfg := config.Default()
	cfg.DataDir = dataDir
	cfg.DBPath = dbPath
	svc := service.New(cfg, st, events.NewBus(), nil)
	httpServer := httptest.NewServer(New(svc).WithLiveSessions(source).Handler(ctx))
	defer httpServer.Close()

	response, err := http.Get(httpServer.URL + "/api/mobile/sessions/stream?path=" + projectPath + "&session_id=codex%3Astreaming-session")
	if err != nil {
		t.Fatalf("open live stream: %v", err)
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK {
		t.Fatalf("live stream status = %d", response.StatusCode)
	}
	if got := response.Header.Get("Content-Type"); !strings.HasPrefix(got, "text/event-stream") {
		t.Fatalf("live stream content type = %q", got)
	}

	eventsCh := make(chan uisurface.EngineerSessionDetailSurface, 4)
	go func() {
		scanner := bufio.NewScanner(response.Body)
		scanner.Buffer(make([]byte, 1024), 128*1024)
		for scanner.Scan() {
			line := scanner.Text()
			if !strings.HasPrefix(line, "data: {") {
				continue
			}
			var surface uisurface.EngineerSessionDetailSurface
			if err := json.Unmarshal([]byte(strings.TrimPrefix(line, "data: ")), &surface); err == nil {
				eventsCh <- surface
			}
		}
	}()

	nextEvent := func(label string) uisurface.EngineerSessionDetailSurface {
		t.Helper()
		select {
		case event := <-eventsCh:
			return event
		case <-time.After(3 * time.Second):
			t.Fatalf("timed out waiting for %s stream event", label)
			return uisurface.EngineerSessionDetailSurface{}
		}
	}
	initial := nextEvent("initial")
	if initial.Session.TranscriptRevision != 1 || len(initial.Entries) != 1 || initial.Entries[0].Text != "Starting" {
		t.Fatalf("initial live stream event = %#v", initial)
	}

	snapshot.TranscriptRevision = 2
	snapshot.LastActivityAt = time.Now()
	snapshot.Entries = []codexapp.TranscriptEntry{
		{ItemID: "agent-1", Kind: codexapp.TranscriptAgent, Text: "Streaming in real time"},
	}
	source.Set(snapshot, true)
	updated := nextEvent("updated")
	if updated.Session.TranscriptRevision != 2 || len(updated.Entries) != 1 || updated.Entries[0].Text != "Streaming in real time" {
		t.Fatalf("updated live stream event = %#v", updated)
	}
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
	if detail.Input.Enabled || detail.Input.Available || detail.Input.Reason == "" {
		t.Fatalf("default live input = %#v, want disabled with reason", detail.Input)
	}

	missingResponse := httptest.NewRecorder()
	handler.ServeHTTP(missingResponse, httptest.NewRequest(http.MethodGet, "/api/mobile/sessions/detail?path="+projectPath+"&session_id=unknown", nil))
	if missingResponse.Code != http.StatusNotFound {
		t.Fatalf("GET unknown session status = %d, want 404", missingResponse.Code)
	}
}

func TestMobileRecordedCodexSessionHidesInjectedModelContext(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	dataDir := t.TempDir()
	dbPath := filepath.Join(dataDir, "little-control-room.sqlite")
	st, err := store.Open(dbPath)
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	defer st.Close()

	projectPath := "/tmp/mobile-recorded-preamble"
	sessionFile := filepath.Join(dataDir, "recorded-session.jsonl")
	transcript := strings.Join([]string{
		`{"type":"response_item","payload":{"type":"message","role":"user","content":[{"type":"input_text","text":"# AGENTS.md instructions for /tmp/mobile-recorded-preamble\n\n<INSTRUCTIONS>\nInternal project instructions\n</INSTRUCTIONS>"}]}}`,
		`{"type":"response_item","payload":{"type":"message","role":"user","content":[{"type":"input_text","text":"Fix the mobile transcript.\n[attached image]"}]}}`,
		`{"type":"event_msg","payload":{"type":"user_message","message":"Fix the mobile transcript."}}`,
		`{"type":"response_item","payload":{"type":"message","role":"assistant","content":[{"type":"output_text","text":"## Visible transcript\n\n| Surface | State |\n| --- | --- |\n| Mobile | Clean |"}]}}`,
		`{"type":"event_msg","payload":{"type":"agent_message","message":"## Visible transcript\n\n| Surface | State |\n| --- | --- |\n| Mobile | Clean |"}}`,
	}, "\n") + "\n"
	if err := os.WriteFile(sessionFile, []byte(transcript), 0o644); err != nil {
		t.Fatalf("write recorded session: %v", err)
	}

	now := time.Now()
	if err := st.UpsertProjectState(ctx, model.ProjectState{
		Path:          projectPath,
		Name:          "Mobile recorded preamble",
		PresentOnDisk: true,
		InScope:       true,
		Status:        model.StatusActive,
		LastActivity:  now,
		UpdatedAt:     now,
		Sessions: []model.SessionEvidence{{
			Source:               model.SessionSourceCodex,
			SessionID:            "codex:recorded-preamble",
			RawSessionID:         "recorded-preamble",
			ProjectPath:          projectPath,
			SessionFile:          sessionFile,
			Format:               "modern",
			LastEventAt:          now,
			LatestTurnStateKnown: true,
			LatestTurnCompleted:  true,
		}},
	}); err != nil {
		t.Fatalf("upsert project: %v", err)
	}

	cfg := config.Default()
	cfg.DataDir = dataDir
	cfg.DBPath = dbPath
	handler := New(service.New(cfg, st, events.NewBus(), nil)).Handler(ctx)

	response := httptest.NewRecorder()
	handler.ServeHTTP(response, httptest.NewRequest(http.MethodGet, "/api/mobile/sessions/detail?path="+projectPath+"&session_id=codex%3Arecorded-preamble", nil))
	if response.Code != http.StatusOK {
		t.Fatalf("GET recorded session status = %d, body = %s", response.Code, response.Body.String())
	}
	var detail uisurface.EngineerSessionDetailSurface
	if err := json.Unmarshal(response.Body.Bytes(), &detail); err != nil {
		t.Fatalf("decode recorded session: %v", err)
	}
	if got, want := len(detail.Entries), 2; got != want {
		t.Fatalf("recorded transcript entries = %d, want %d: %#v", got, want, detail.Entries)
	}
	if detail.Entries[0].Kind != "user" || detail.Entries[0].Text != "Fix the mobile transcript." {
		t.Fatalf("visible user entry = %#v", detail.Entries[0])
	}
	wantMarkdown := "## Visible transcript\n\n| Surface | State |\n| --- | --- |\n| Mobile | Clean |"
	if detail.Entries[1].Kind != "agent" || detail.Entries[1].Text != wantMarkdown {
		t.Fatalf("visible agent entry = %#v", detail.Entries[1])
	}
	if strings.Contains(response.Body.String(), "AGENTS.md") || strings.Contains(response.Body.String(), "Internal project instructions") {
		t.Fatalf("recorded mobile transcript leaked injected context: %s", response.Body.String())
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

func TestMobileSessionInputRequiresSettingAndTargetsCurrentLiveSession(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	dataDir := t.TempDir()
	dbPath := filepath.Join(dataDir, "little-control-room.sqlite")
	st, err := store.Open(dbPath)
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	defer st.Close()
	projectPath := "/tmp/mobile-input"
	if err := st.UpsertProjectState(ctx, model.ProjectState{
		Path: projectPath, Name: "Mobile input", PresentOnDisk: true, InScope: true, UpdatedAt: time.Now(),
	}); err != nil {
		t.Fatalf("upsert project: %v", err)
	}

	cfg := config.Default()
	cfg.DataDir = dataDir
	cfg.DBPath = dbPath
	svc := service.New(cfg, st, events.NewBus(), nil)
	controller := &fakeLiveSessionController{
		ok: true,
		snapshot: codexapp.Snapshot{
			Provider: codexapp.ProviderCodex, ProjectPath: projectPath, ThreadID: "live-input", Started: true,
		},
		result: codexapp.SessionInputResult{Mode: codexapp.SessionInputSend, Provider: codexapp.ProviderCodex, ThreadID: "live-input"},
	}
	handler := New(svc).WithLiveSessions(controller).Handler(ctx)
	body := `{"project_path":"` + projectPath + `","session_id":"codex:live-input","request_id":"request-1","text":"Continue from my phone."}`

	disabled := httptest.NewRecorder()
	disabledRequest := httptest.NewRequest(http.MethodPost, "/api/mobile/sessions/input", strings.NewReader(body))
	disabledRequest.Header.Set("Content-Type", "application/json")
	disabledRequest.Header.Set("Origin", "http://lcr.test")
	disabledRequest.Host = "lcr.test"
	handler.ServeHTTP(disabled, disabledRequest)
	if disabled.Code != http.StatusForbidden || controller.calls != 0 {
		t.Fatalf("disabled input status=%d calls=%d body=%s", disabled.Code, controller.calls, disabled.Body.String())
	}

	settings := config.EditableSettingsFromAppConfig(cfg)
	settings.MobileInputEnabled = true
	svc.ApplyEditableSettings(settings)

	detail := httptest.NewRecorder()
	detailRequest := httptest.NewRequest(http.MethodGet, "/api/mobile/sessions/detail?path="+projectPath+"&session_id=codex%3Alive-input", nil)
	handler.ServeHTTP(detail, detailRequest)
	if detail.Code != http.StatusOK {
		t.Fatalf("GET writable session status = %d, body = %s", detail.Code, detail.Body.String())
	}
	var surface uisurface.EngineerSessionDetailSurface
	if err := json.Unmarshal(detail.Body.Bytes(), &surface); err != nil {
		t.Fatalf("decode writable session: %v", err)
	}
	if !surface.Input.Enabled || !surface.Input.Available || surface.Input.Label != "Send" {
		t.Fatalf("writable session input = %#v", surface.Input)
	}

	crossOrigin := httptest.NewRecorder()
	crossOriginRequest := httptest.NewRequest(http.MethodPost, "/api/mobile/sessions/input", strings.NewReader(strings.Replace(body, "request-1", "request-cross", 1)))
	crossOriginRequest.Header.Set("Content-Type", "application/json")
	crossOriginRequest.Header.Set("Origin", "http://other.test")
	crossOriginRequest.Host = "lcr.test"
	handler.ServeHTTP(crossOrigin, crossOriginRequest)
	if crossOrigin.Code != http.StatusForbidden || controller.calls != 0 {
		t.Fatalf("cross-origin input status=%d calls=%d", crossOrigin.Code, controller.calls)
	}

	stale := httptest.NewRecorder()
	staleBody := strings.Replace(body, "codex:live-input", "codex:old-input", 1)
	staleBody = strings.Replace(staleBody, "request-1", "request-stale", 1)
	staleRequest := httptest.NewRequest(http.MethodPost, "/api/mobile/sessions/input", strings.NewReader(staleBody))
	staleRequest.Header.Set("Content-Type", "application/json")
	staleRequest.Header.Set("Origin", "http://lcr.test")
	staleRequest.Host = "lcr.test"
	handler.ServeHTTP(stale, staleRequest)
	if stale.Code != http.StatusConflict || controller.calls != 0 {
		t.Fatalf("stale input status=%d calls=%d body=%s", stale.Code, controller.calls, stale.Body.String())
	}

	accepted := httptest.NewRecorder()
	acceptedRequest := httptest.NewRequest(http.MethodPost, "/api/mobile/sessions/input", strings.NewReader(body))
	acceptedRequest.Header.Set("Content-Type", "application/json")
	acceptedRequest.Header.Set("Origin", "http://lcr.test")
	acceptedRequest.Host = "lcr.test"
	handler.ServeHTTP(accepted, acceptedRequest)
	if accepted.Code != http.StatusOK {
		t.Fatalf("POST input status = %d, body = %s", accepted.Code, accepted.Body.String())
	}
	if controller.calls != 1 || controller.projectPath != projectPath || controller.threadID != "live-input" || controller.text != "Continue from my phone." {
		t.Fatalf("controller call = %#v", controller)
	}

	duplicate := httptest.NewRecorder()
	duplicateRequest := httptest.NewRequest(http.MethodPost, "/api/mobile/sessions/input", strings.NewReader(body))
	duplicateRequest.Header.Set("Content-Type", "application/json")
	duplicateRequest.Header.Set("Origin", "http://lcr.test")
	duplicateRequest.Host = "lcr.test"
	handler.ServeHTTP(duplicate, duplicateRequest)
	if duplicate.Code != http.StatusConflict || controller.calls != 1 {
		t.Fatalf("duplicate input status=%d calls=%d body=%s", duplicate.Code, controller.calls, duplicate.Body.String())
	}
}
