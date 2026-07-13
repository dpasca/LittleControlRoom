package server

import (
	"context"
	"encoding/json"
	"net"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
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

func TestHandlerServesMobileAppAndSemanticDashboard(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	dataDir := t.TempDir()
	st, err := store.Open(filepath.Join(dataDir, "little-control-room.sqlite"))
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	defer st.Close()

	now := time.Now().Add(-5 * time.Minute)
	projectPath := "/tmp/mobile-demo"
	if err := st.UpsertProjectState(ctx, model.ProjectState{
		Path:          projectPath,
		Name:          "Mobile demo",
		PresentOnDisk: true,
		InScope:       true,
		Status:        model.StatusActive,
		LastActivity:  now,
		RepoBranch:    "mobile-interface",
		RepoDirty:     true,
		AttentionReason: []model.AttentionReason{
			{Code: "review", Text: "Review the mobile surface", Weight: 20},
		},
		UpdatedAt: now,
	}); err != nil {
		t.Fatalf("upsert project: %v", err)
	}
	category, err := st.CreateProjectCategory(ctx, "Mobile")
	if err != nil {
		t.Fatalf("create category: %v", err)
	}
	if err := st.SetResourceCategory(ctx, model.CategoryResourceProject, projectPath, category.ID); err != nil {
		t.Fatalf("assign category: %v", err)
	}

	cfg := config.Default()
	cfg.DataDir = dataDir
	cfg.DBPath = filepath.Join(dataDir, "little-control-room.sqlite")
	svc := service.New(cfg, st, events.NewBus(), nil)
	handler := New(svc).WithLiveSessions(fakeLiveSessionSource{
		ok: true,
		snapshot: codexapp.Snapshot{
			Provider:       codexapp.ProviderCodex,
			ProjectPath:    projectPath,
			ThreadID:       "dashboard-live",
			Started:        true,
			Busy:           true,
			Phase:          codexapp.SessionPhaseRunning,
			LastActivityAt: now,
			Entries: []codexapp.TranscriptEntry{
				{Kind: codexapp.TranscriptAgent, Text: "Monitoring the live mobile channel."},
			},
		},
	}).Handler(ctx)

	appRequest := httptest.NewRequest(http.MethodGet, "/", nil)
	appResponse := httptest.NewRecorder()
	handler.ServeHTTP(appResponse, appRequest)
	if appResponse.Code != http.StatusOK {
		t.Fatalf("GET / status = %d, want 200", appResponse.Code)
	}
	if !strings.Contains(appResponse.Body.String(), "Little Control Room") {
		t.Fatalf("GET / did not return the mobile shell: %s", appResponse.Body.String())
	}
	for _, id := range []string{"dashboard-live-channels", "transcript-mode", "session-follow-button", "session-composer", "session-message", "session-send-button"} {
		if !strings.Contains(appResponse.Body.String(), `id="`+id+`"`) {
			t.Fatalf("GET / missing monitoring control %q", id)
		}
	}
	if appResponse.Header().Get("Content-Security-Policy") == "" {
		t.Fatal("GET / should set a content security policy")
	}
	if got, want := appResponse.Header().Get("Cache-Control"), "no-store"; got != want {
		t.Fatalf("GET / cache control = %q, want %q", got, want)
	}

	cssRequest := httptest.NewRequest(http.MethodGet, "/app.css", nil)
	cssResponse := httptest.NewRecorder()
	handler.ServeHTTP(cssResponse, cssRequest)
	if cssResponse.Code != http.StatusOK {
		t.Fatalf("GET /app.css status = %d, want 200", cssResponse.Code)
	}
	if got, want := cssResponse.Header().Get("Cache-Control"), "no-store"; got != want {
		t.Fatalf("GET /app.css cache control = %q, want %q", got, want)
	}

	dashboardRequest := httptest.NewRequest(http.MethodGet, "/api/mobile/dashboard", nil)
	dashboardResponse := httptest.NewRecorder()
	handler.ServeHTTP(dashboardResponse, dashboardRequest)
	if dashboardResponse.Code != http.StatusOK {
		t.Fatalf("GET dashboard status = %d, body = %s", dashboardResponse.Code, dashboardResponse.Body.String())
	}
	var dashboard uisurface.DashboardSurface
	if err := json.Unmarshal(dashboardResponse.Body.Bytes(), &dashboard); err != nil {
		t.Fatalf("decode dashboard: %v", err)
	}
	if got, want := len(dashboard.Projects), 1; got != want {
		t.Fatalf("dashboard project count = %d, want %d", got, want)
	}
	if got, want := dashboard.Projects[0].CategoryName, "Mobile"; got != want {
		t.Fatalf("project category = %q, want %q", got, want)
	}
	if got, want := dashboard.Projects[0].Summary, "Working tree has local changes"; got != want {
		t.Fatalf("project summary = %q, want %q", got, want)
	}
	if got, want := len(dashboard.LiveSessions), 1; got != want {
		t.Fatalf("dashboard live session count = %d, want %d", got, want)
	}
	if got, want := dashboard.LiveSessions[0].ProjectName, "Mobile demo"; got != want {
		t.Fatalf("dashboard live session project = %q, want %q", got, want)
	}
	if !dashboard.LiveSessions[0].Live || dashboard.LiveSessions[0].Status.Label != "Working" {
		t.Fatalf("dashboard live session = %#v", dashboard.LiveSessions[0])
	}

	detailRequest := httptest.NewRequest(http.MethodGet, "/api/mobile/projects/detail?path="+projectPath, nil)
	detailResponse := httptest.NewRecorder()
	handler.ServeHTTP(detailResponse, detailRequest)
	if detailResponse.Code != http.StatusOK {
		t.Fatalf("GET detail status = %d, body = %s", detailResponse.Code, detailResponse.Body.String())
	}
	var detail uisurface.ProjectDetailSurface
	if err := json.Unmarshal(detailResponse.Body.Bytes(), &detail); err != nil {
		t.Fatalf("decode detail: %v", err)
	}
	if got, want := detail.Project.Path, projectPath; got != want {
		t.Fatalf("detail project path = %q, want %q", got, want)
	}
	for _, block := range detail.Blocks {
		if block.Label == "Attention" || block.Label == "Attention score" {
			t.Fatalf("detail surface contains redundant attention-total block: %#v", block)
		}
	}
}

func TestFilterMobileDashboardProjectsMatchesDesktopTabs(t *testing.T) {
	t.Parallel()
	projects := []model.ProjectSummary{
		{Path: "/tmp/scoped", InScope: true},
		{Path: "/tmp/manual", ManuallyAdded: true},
		{Path: "/tmp/archived", Archived: true},
		{Path: "/tmp/outside"},
	}

	filtered := filterMobileDashboardProjects(projects)
	if got, want := len(filtered), 3; got != want {
		t.Fatalf("filtered count = %d, want %d", got, want)
	}
	for i, want := range []string{"/tmp/scoped", "/tmp/manual", "/tmp/archived"} {
		if filtered[i].Path != want {
			t.Fatalf("filtered[%d].Path = %q, want %q", i, filtered[i].Path, want)
		}
	}
}

func TestHandlerRejectsPrivateProjectInPrivacyMode(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	dataDir := t.TempDir()
	st, err := store.Open(filepath.Join(dataDir, "little-control-room.sqlite"))
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	defer st.Close()

	projectPath := "/tmp/private-mobile-demo"
	if err := st.UpsertProjectState(ctx, model.ProjectState{
		Path:          projectPath,
		Name:          "Private mobile demo",
		PresentOnDisk: true,
		InScope:       true,
		Status:        model.StatusIdle,
		UpdatedAt:     time.Now(),
	}); err != nil {
		t.Fatalf("upsert project: %v", err)
	}
	category, err := st.CreateProjectCategory(ctx, "Private")
	if err != nil {
		t.Fatalf("create category: %v", err)
	}
	if _, err := st.SetProjectCategoryPrivate(ctx, category.Name, true); err != nil {
		t.Fatalf("mark category private: %v", err)
	}
	if err := st.SetResourceCategory(ctx, model.CategoryResourceProject, projectPath, category.ID); err != nil {
		t.Fatalf("assign category: %v", err)
	}

	cfg := config.Default()
	cfg.DataDir = dataDir
	cfg.DBPath = filepath.Join(dataDir, "little-control-room.sqlite")
	cfg.PrivacyMode = true
	handler := New(service.New(cfg, st, events.NewBus(), nil)).WithLiveSessions(fakeLiveSessionSource{
		ok: true,
		snapshot: codexapp.Snapshot{
			Provider:    codexapp.ProviderCodex,
			ProjectPath: projectPath,
			ThreadID:    "private-live",
			Started:     true,
			Busy:        true,
		},
	}).Handler(ctx)

	dashboardResponse := httptest.NewRecorder()
	handler.ServeHTTP(dashboardResponse, httptest.NewRequest(http.MethodGet, "/api/mobile/dashboard", nil))
	var dashboard uisurface.DashboardSurface
	if err := json.Unmarshal(dashboardResponse.Body.Bytes(), &dashboard); err != nil {
		t.Fatalf("decode dashboard: %v", err)
	}
	if len(dashboard.Projects) != 0 {
		t.Fatalf("private project leaked into dashboard: %#v", dashboard.Projects)
	}
	if len(dashboard.LiveSessions) != 0 {
		t.Fatalf("private live session leaked into dashboard: %#v", dashboard.LiveSessions)
	}

	detailResponse := httptest.NewRecorder()
	handler.ServeHTTP(detailResponse, httptest.NewRequest(http.MethodGet, "/api/mobile/projects/detail?path="+projectPath, nil))
	if detailResponse.Code != http.StatusNotFound {
		t.Fatalf("private detail status = %d, want 404", detailResponse.Code)
	}
}

func TestListenAddressValidation(t *testing.T) {
	t.Parallel()
	if err := ValidateListenAddress(DefaultListenAddress); err != nil {
		t.Fatalf("default listen address should be valid: %v", err)
	}
	if !ListenAddressIsLoopback(DefaultListenAddress) {
		t.Fatalf("default listen address should be loopback: %s", DefaultListenAddress)
	}
	if ListenAddressIsLoopback("0.0.0.0:7777") {
		t.Fatal("all-interface listen address should not be loopback")
	}
	if err := ValidateListenAddress("7777"); err == nil {
		t.Fatal("port-only listen address should be rejected")
	}
}

func TestStartBindsBeforeReturningAndStopsWithContext(t *testing.T) {
	t.Parallel()
	ctx, cancel := context.WithCancel(context.Background())
	running, err := New(nil).Start(ctx, "127.0.0.1:0")
	if err != nil {
		cancel()
		t.Fatalf("start server: %v", err)
	}

	client := &http.Client{Timeout: 2 * time.Second}
	response, err := client.Get(running.URL() + "/health")
	if err != nil {
		cancel()
		t.Fatalf("GET health: %v", err)
	}
	_ = response.Body.Close()
	if response.StatusCode != http.StatusOK {
		cancel()
		t.Fatalf("GET health status = %d, want 200", response.StatusCode)
	}

	cancel()
	select {
	case <-running.Done():
	case <-time.After(2 * time.Second):
		_ = running.Close()
		t.Fatal("server did not stop after context cancellation")
	}
	if err := running.Wait(); err != nil {
		t.Fatalf("wait for server: %v", err)
	}
}

func TestStartReportsBindConflictSynchronously(t *testing.T) {
	t.Parallel()
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("reserve port: %v", err)
	}
	defer listener.Close()

	running, err := New(nil).Start(context.Background(), listener.Addr().String())
	if err == nil {
		_ = running.Close()
		t.Fatal("start on occupied port should fail")
	}
	if running != nil {
		t.Fatal("start on occupied port returned a running server")
	}
	if !strings.Contains(err.Error(), "listen on") {
		t.Fatalf("bind error = %q, want explicit listen context", err)
	}
}
