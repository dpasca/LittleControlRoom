package cli

import (
	"context"
	"encoding/json"
	"io"
	"net"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"lcroom/internal/config"
	"lcroom/internal/events"
	"lcroom/internal/helpmeta"
	"lcroom/internal/model"
	"lcroom/internal/runtimeguard"
	"lcroom/internal/server"
	"lcroom/internal/service"
	"lcroom/internal/store"
)

func TestRunVersion(t *testing.T) {
	code, output := captureRunStdout(t, []string{"version"})
	if code != 0 {
		t.Fatalf("code = %d", code)
	}
	if got, want := strings.TrimSpace(output), "lcroom dev"; got != want {
		t.Fatalf("version output = %q, want %q", got, want)
	}
}

func TestLogServeScanEventsSurfacesWarningsAndDeduplicatesRepeats(t *testing.T) {
	at := time.Date(2026, 7, 16, 10, 0, 0, 0, time.UTC)
	eventCh := make(chan events.Event, 4)
	for _, event := range []events.Event{
		{Type: events.ScanCompleted, At: at, Payload: map[string]string{
			"git_metadata_timeouts":             "2",
			"git_metadata_timeout_path_samples": "/tmp/one\n/tmp/two",
		}},
		{Type: events.ScanFailed, At: at.Add(time.Minute), Payload: map[string]string{
			"error_kind": "timeout",
			"error":      "scan timed out\nwhile detecting projects",
		}},
		{Type: events.ScanCompleted, At: at.Add(2 * time.Minute), Payload: map[string]string{
			"git_metadata_timeouts":             "2",
			"git_metadata_timeout_path_samples": "/tmp/one\n/tmp/two",
		}},
		{Type: events.ScanFailed, At: at.Add(3 * time.Minute), Payload: map[string]string{
			"error_kind": "timeout",
			"error":      "scan timed out\nwhile detecting projects",
		}},
	} {
		eventCh <- event
	}
	close(eventCh)

	var output strings.Builder
	logServeScanEvents(context.Background(), eventCh, &output)
	want := "scan warning: Git metadata reads timed out for 2 projects: /tmp/one, /tmp/two\n" +
		"scheduled scan timed out: scan timed out while detecting projects\n"
	if got := output.String(); got != want {
		t.Fatalf("serve scan event log = %q, want %q", got, want)
	}
}

func TestConfigureMobileServerAuthOnlyProtectsLANListeners(t *testing.T) {
	t.Parallel()
	dataDir := t.TempDir()
	cfg := config.Default()
	cfg.DataDir = dataDir
	cfg.DBPath = filepath.Join(dataDir, "little-control-room.sqlite")
	svc := service.New(cfg, nil, events.NewBus(), nil)

	loopbackAuth, err := configureMobileServerAuth(server.New(svc), svc, "127.0.0.1:7777")
	if err != nil {
		t.Fatalf("configure loopback mobile auth: %v", err)
	}
	if loopbackAuth != nil {
		t.Fatal("loopback listener should not require mobile auth")
	}

	lanAuth, err := configureMobileServerAuth(server.New(svc), svc, "192.168.1.20:7777")
	if err != nil {
		t.Fatalf("configure LAN mobile auth: %v", err)
	}
	if lanAuth == nil || len(lanAuth.PairingCode()) != 7 {
		t.Fatalf("LAN mobile auth pairing code = %q", lanAuth.PairingCode())
	}
	keyInfo, err := os.Stat(filepath.Join(dataDir, "mobile-auth.key"))
	if err != nil {
		t.Fatalf("stat LAN mobile auth key: %v", err)
	}
	if got, want := keyInfo.Mode().Perm(), os.FileMode(0o600); got != want {
		t.Fatalf("LAN mobile auth key permissions = %o, want %o", got, want)
	}
}

func TestResolveMobileRuntimeOptionsUsesSavedSettings(t *testing.T) {
	cfg := config.Default()
	cfg.MobileEnabled = false
	cfg.MobileListenAddress = "0.0.0.0:8787"

	address, enabled, err := resolveMobileRuntimeOptions(cfg, "")
	if err != nil {
		t.Fatalf("resolve mobile runtime options: %v", err)
	}
	if address != "0.0.0.0:8787" || enabled {
		t.Fatalf("resolved address/enabled = %q/%v, want 0.0.0.0:8787/false", address, enabled)
	}
}

func TestResolveMobileRuntimeOptionsListenOverrideForcesOneRunStart(t *testing.T) {
	cfg := config.Default()
	cfg.MobileEnabled = false
	cfg.MobileListenAddress = "127.0.0.1:7777"

	address, enabled, err := resolveMobileRuntimeOptions(cfg, " 0.0.0.0:9999 ")
	if err != nil {
		t.Fatalf("resolve mobile runtime options: %v", err)
	}
	if address != "0.0.0.0:9999" || !enabled {
		t.Fatalf("resolved address/enabled = %q/%v, want 0.0.0.0:9999/true", address, enabled)
	}
}

func TestLocalPrivateLANIPv4AddressesOnlyReturnsPrivateIPv4(t *testing.T) {
	for _, address := range localPrivateLANIPv4Addresses() {
		ip := net.ParseIP(address)
		if ip == nil || ip.To4() == nil || !ip.IsPrivate() || ip.IsLoopback() {
			t.Fatalf("LAN discovery returned unsuitable address %q", address)
		}
	}
}

func TestLANInterfaceRankPrefersPhysicalNetworkNames(t *testing.T) {
	if got := lanInterfaceRank("en0"); got != 0 {
		t.Fatalf("en0 rank = %d, want 0", got)
	}
	if got := lanInterfaceRank("bridge100"); got != 1 {
		t.Fatalf("bridge100 rank = %d, want 1", got)
	}
	if got := lanInterfaceRank("utun4"); got != 2 {
		t.Fatalf("utun4 rank = %d, want 2", got)
	}
}

func TestRunHelpMetaWritesJSONCorpus(t *testing.T) {
	code, output := captureRunStdout(t, []string{"help-meta"})
	if code != 0 {
		t.Fatalf("code = %d, output = %s", code, output)
	}
	var topics []helpmeta.Topic
	if err := json.Unmarshal([]byte(output), &topics); err != nil {
		t.Fatalf("help-meta output is not JSON: %v\n%s", err, output)
	}
	seen := map[string]helpmeta.Topic{}
	for _, topic := range topics {
		seen[topic.ID] = topic
	}
	for _, id := range []string{
		helpmeta.CommandTopicID(helpmeta.SurfaceMainTUI, "chat"),
		helpmeta.TopicID(helpmeta.SurfaceMainTUI, helpmeta.TopicKindWorkflow, "merge-conflict-recovery"),
		helpmeta.TopicID(helpmeta.SurfaceMainTUI, helpmeta.TopicKindKeybinding, "project-todos"),
	} {
		if _, ok := seen[id]; !ok {
			t.Fatalf("help-meta output missing topic %q", id)
		}
	}
}

func captureRunStdout(t *testing.T, args []string) (int, string) {
	t.Helper()
	oldStdout := os.Stdout
	reader, writer, err := os.Pipe()
	if err != nil {
		t.Fatalf("pipe: %v", err)
	}
	os.Stdout = writer
	defer func() {
		os.Stdout = oldStdout
		_ = reader.Close()
	}()

	code := Run("lcroom", args)
	if err := writer.Close(); err != nil {
		t.Fatalf("close writer: %v", err)
	}
	output, err := io.ReadAll(reader)
	if err != nil {
		t.Fatalf("read output: %v", err)
	}
	return code, string(output)
}

func TestFormatRuntimeConflictMessageIncludesRecovery(t *testing.T) {
	t.Parallel()

	startedAt := time.Date(2026, 6, 2, 12, 25, 45, 0, time.FixedZone("JST", 9*60*60))
	message := formatRuntimeConflictMessage(&runtimeguard.Owner{
		PID:       50891,
		Mode:      "tui",
		DBPath:    "/tmp/little-control-room.sqlite",
		Hostname:  "demo-host",
		CWD:       "/tmp/LittleControlRoom",
		Command:   "/tmp/lcroom tui --config /tmp/config.toml --db /tmp/little-control-room.sqlite",
		StartedAt: startedAt,
	}, "/tmp/little-control-room.sqlite", "tui")

	for _, want := range []string{
		"Little Control Room is already running a tui runtime for this database.",
		"database: /tmp/little-control-room.sqlite",
		"To recover:",
		"  kill 50891",
		"  # if it does not exit: kill -9 50891",
		"  # if your prompt still looks stair-stepped: stty sane",
		"Active runtime:",
		"  pid: 50891",
		"  mode: tui",
		"  started: 2026-06-02T12:25:45+09:00",
		"For intentional short-lived dev/debug overlap only, re-run with --allow-multiple-instances.",
	} {
		if !strings.Contains(message, want) {
			t.Fatalf("message missing %q:\n%s", want, message)
		}
	}
}

func TestWriteInteractivePanicDump(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	cfg := config.AppConfig{
		DataDir:    dir,
		DBPath:     filepath.Join(dir, "little-control-room.sqlite"),
		ConfigPath: filepath.Join(dir, "config.toml"),
	}
	path, err := writeInteractivePanicDump(cfg, "tui", "boom")
	if err != nil {
		t.Fatalf("writeInteractivePanicDump() error = %v", err)
	}
	if !strings.HasPrefix(path, filepath.Join(dir, "crash-dumps")+string(os.PathSeparator)) {
		t.Fatalf("dump path = %s, want under crash-dumps", path)
	}
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read dump: %v", err)
	}
	text := string(raw)
	for _, want := range []string{
		"mode: tui",
		"panic: boom",
		"db_path: " + cfg.DBPath,
		"config_path: " + cfg.ConfigPath,
		"goroutine ",
	} {
		if !strings.Contains(text, want) {
			t.Fatalf("dump missing %q:\n%s", want, text)
		}
	}
}

func TestLoadStoredProjectStates(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	dbPath := filepath.Join(t.TempDir(), "little-control-room.sqlite")
	st, err := store.Open(dbPath)
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	defer st.Close()

	lastActivity := time.Unix(1_700_000_000, 0)
	snoozedUntil := lastActivity.Add(time.Hour)
	updatedAt := lastActivity.Add(2 * time.Hour)
	state := model.ProjectState{
		Path:            "/tmp/demo",
		Name:            "demo",
		LastActivity:    lastActivity,
		Status:          model.StatusIdle,
		AttentionScore:  37,
		PresentOnDisk:   true,
		RepoDirty:       true,
		RepoSyncStatus:  model.RepoSyncAhead,
		RepoAheadCount:  2,
		RepoBehindCount: 0,
		InScope:         true,
		Pinned:          true,
		SnoozedUntil:    &snoozedUntil,
		MovedFromPath:   "/tmp/old-demo",
		MovedAt:         lastActivity.Add(-time.Hour),
		AttentionReason: []model.AttentionReason{{
			Code:   "repo_dirty",
			Text:   "Git worktree has uncommitted changes",
			Weight: 15,
		}},
		Sessions: []model.SessionEvidence{{
			SessionID:           "ses_1",
			ProjectPath:         "/tmp/demo",
			DetectedProjectPath: "/tmp/demo",
			SessionFile:         "/tmp/demo/session.jsonl",
			Format:              "modern",
			SnapshotHash:        "abc",
			StartedAt:           lastActivity.Add(-30 * time.Minute),
			LastEventAt:         lastActivity,
		}},
		Artifacts: []model.ArtifactEvidence{{
			Path:      "/tmp/demo/session.jsonl",
			Kind:      "codex_session_jsonl",
			UpdatedAt: lastActivity,
			Note:      "rollout path from state_5.sqlite threads",
		}},
		UpdatedAt: updatedAt,
	}
	if err := st.UpsertProjectState(ctx, state); err != nil {
		t.Fatalf("upsert project state: %v", err)
	}

	states, err := loadStoredProjectStates(ctx, st)
	if err != nil {
		t.Fatalf("load stored project states: %v", err)
	}
	if len(states) != 1 {
		t.Fatalf("expected 1 state, got %d", len(states))
	}

	got := states[0]
	if got.Path != state.Path || got.Name != state.Name {
		t.Fatalf("unexpected project identity: %+v", got)
	}
	if !got.RepoDirty || got.RepoSyncStatus != model.RepoSyncAhead || got.RepoAheadCount != 2 {
		t.Fatalf("unexpected repo state: %+v", got)
	}
	if got.SnoozedUntil == nil || got.SnoozedUntil.Unix() != snoozedUntil.Unix() {
		t.Fatalf("unexpected snooze time: %+v", got.SnoozedUntil)
	}
	if len(got.AttentionReason) != 1 || got.AttentionReason[0].Code != "repo_dirty" {
		t.Fatalf("unexpected reasons: %+v", got.AttentionReason)
	}
	if len(got.Sessions) != 1 || got.Sessions[0].SessionID != "codex:ses_1" {
		t.Fatalf("unexpected sessions: %+v", got.Sessions)
	}
	if len(got.Artifacts) != 1 || got.Artifacts[0].Kind != "codex_session_jsonl" {
		t.Fatalf("unexpected artifacts: %+v", got.Artifacts)
	}
}

func TestSelectOpenCodeSnapshotSessionsSortsAndFilters(t *testing.T) {
	t.Parallel()

	now := time.Unix(1_700_000_000, 0)
	states := []model.ProjectState{
		{
			Path: "/tmp/beta",
			Sessions: []model.SessionEvidence{
				{
					SessionID:   "ses_beta_new",
					SessionFile: "/tmp/beta/opencode.db#session:ses_beta_new",
					Format:      "opencode_db",
					LastEventAt: now.Add(10 * time.Minute),
				},
				{
					SessionID:   "ses_beta_codex",
					SessionFile: "/tmp/beta/session.jsonl",
					Format:      "modern",
					LastEventAt: now.Add(9 * time.Minute),
				},
			},
		},
		{
			Path: "/tmp/alpha",
			Sessions: []model.SessionEvidence{
				{
					SessionID:   "ses_alpha_old",
					SessionFile: "/tmp/alpha/opencode.db#session:ses_alpha_old",
					Format:      "opencode_db",
					LastEventAt: now.Add(5 * time.Minute),
				},
			},
		},
	}

	selected := selectSnapshotSessions(states, "", "", 2)
	if len(selected) != 2 {
		t.Fatalf("selected len = %d, want 2", len(selected))
	}
	if selected[0].Session.SessionID != "ses_beta_new" {
		t.Fatalf("first session = %q, want ses_beta_new", selected[0].Session.SessionID)
	}
	if selected[1].Session.SessionID != "ses_beta_codex" {
		t.Fatalf("second session = %q, want ses_beta_codex", selected[1].Session.SessionID)
	}
}

func TestSelectOpenCodeSnapshotSessionsSupportsProjectAndSessionFilters(t *testing.T) {
	t.Parallel()

	now := time.Unix(1_700_000_000, 0)
	states := []model.ProjectState{
		{
			Path: "/tmp/demo",
			Sessions: []model.SessionEvidence{
				{
					SessionID:   "ses_keep",
					SessionFile: "/tmp/demo/opencode.db#session:ses_keep",
					Format:      "opencode_db",
					LastEventAt: now,
				},
				{
					SessionID:   "ses_skip",
					SessionFile: "/tmp/demo/opencode.db#session:ses_skip",
					Format:      "opencode_db",
					LastEventAt: now.Add(-time.Minute),
				},
			},
		},
		{
			Path: "/tmp/other",
			Sessions: []model.SessionEvidence{
				{
					SessionID:   "ses_other",
					SessionFile: "/tmp/other/opencode.db#session:ses_other",
					Format:      "opencode_db",
					LastEventAt: now.Add(time.Minute),
				},
			},
		},
	}

	selected := selectSnapshotSessions(states, "/tmp/demo", "ses_keep", 3)
	if len(selected) != 1 {
		t.Fatalf("selected len = %d, want 1", len(selected))
	}
	if selected[0].State.Path != "/tmp/demo" || selected[0].Session.SessionID != "ses_keep" {
		t.Fatalf("unexpected selection: %+v", selected[0])
	}
}

func TestRunSanitizeSummariesDryRunAndApply(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	tempDir := t.TempDir()
	st, err := store.Open(filepath.Join(tempDir, "little-control-room.sqlite"))
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	defer st.Close()

	projectPath := "/tmp/demo"
	sessionID := "ses_open_status"
	sessionFile := filepath.Join(tempDir, "session-summary.jsonl")
	if err := os.WriteFile(sessionFile, []byte(strings.Join([]string{
		`{"timestamp":"2026-03-14T06:27:12Z","type":"message","role":"user","content":[{"type":"text","text":"Please confirm the session summary behavior."}]}`,
		`{"timestamp":"2026-03-14T06:27:13Z","type":"message","role":"assistant","content":[{"type":"text","text":"I confirmed the behavior and updated the retry guard."}]}`,
	}, "\n")+"\n"), 0o644); err != nil {
		t.Fatalf("write fixture: %v", err)
	}
	now := time.Now().UTC().Truncate(time.Second)
	if err := st.UpsertProjectState(ctx, model.ProjectState{
		Path:           projectPath,
		Name:           "demo",
		Status:         model.StatusIdle,
		AttentionScore: 10,
		InScope:        true,
		UpdatedAt:      now,
		Sessions: []model.SessionEvidence{{
			SessionID:            sessionID,
			ProjectPath:          projectPath,
			DetectedProjectPath:  projectPath,
			SessionFile:          sessionFile,
			Format:               "modern",
			LastEventAt:          now,
			LatestTurnStateKnown: true,
		}},
	}); err != nil {
		t.Fatalf("upsert project state: %v", err)
	}
	if _, err := st.QueueSessionClassification(ctx, model.SessionClassification{
		SessionID:         sessionID,
		ProjectPath:       projectPath,
		SessionFile:       sessionFile,
		SessionFormat:     "modern",
		SnapshotHash:      "hash-1",
		Model:             "gpt-test-mini",
		ClassifierVersion: "v1",
		SourceUpdatedAt:   now,
	}, 15*time.Minute); err != nil {
		t.Fatalf("queue classification: %v", err)
	}
	updated, err := st.UpdateSessionClassificationSummary(ctx, sessionID, "Turn completed")
	if err != nil || !updated {
		t.Fatalf("pre-sanitize summary update: updated=%v err=%v", updated, err)
	}

	if code := runSanitizeSummaries(ctx, st, config.AppConfig{SanitizeDryRun: true}); code != 0 {
		t.Fatalf("runSanitizeSummaries dry-run: code=%d", code)
	}
	stored, err := st.GetSessionClassification(ctx, sessionID)
	if err != nil {
		t.Fatalf("read classification: %v", err)
	}
	if got := strings.TrimSpace(stored.Summary); got != "Turn completed" {
		t.Fatalf("dry-run should not modify summary, got %q", got)
	}

	if code := runSanitizeSummaries(ctx, st, config.AppConfig{SanitizeApply: true}); code != 0 {
		t.Fatalf("runSanitizeSummaries apply: code=%d", code)
	}
	stored, err = st.GetSessionClassification(ctx, sessionID)
	if err != nil {
		t.Fatalf("read classification after apply: %v", err)
	}
	want := "I confirmed the behavior and updated the retry guard."
	if got := strings.TrimSpace(stored.Summary); got != want {
		t.Fatalf("sanitized summary = %q, want %q", got, want)
	}
}

func TestRunSanitizeSummariesProjectAndSessionFilter(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	tempDir := t.TempDir()
	st, err := store.Open(filepath.Join(tempDir, "little-control-room.sqlite"))
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	defer st.Close()

	now := time.Now().UTC().Truncate(time.Second)
	makeFixture := func(project, file string) {
		_ = os.WriteFile(file, []byte(strings.Join([]string{
			`{"timestamp":"2026-03-14T06:27:12Z","type":"message","role":"user","content":[{"type":"text","text":"` + project + `"}]}`,
			`{"timestamp":"2026-03-14T06:27:13Z","type":"message","role":"assistant","content":[{"type":"text","text":"Summary for ` + project + `"}]}`,
		}, "\n")+"\n"), 0o644)
	}

	fixtureAlpha := filepath.Join(tempDir, "alpha.jsonl")
	fixtureBeta := filepath.Join(tempDir, "beta.jsonl")
	makeFixture("alpha", fixtureAlpha)
	makeFixture("beta", fixtureBeta)

	if err := st.UpsertProjectState(ctx, model.ProjectState{
		Path:           "/tmp/alpha",
		Name:           "alpha",
		Status:         model.StatusIdle,
		AttentionScore: 10,
		InScope:        true,
		UpdatedAt:      now,
		Sessions: []model.SessionEvidence{{
			SessionID:            "ses_alpha",
			ProjectPath:          "/tmp/alpha",
			DetectedProjectPath:  "/tmp/alpha",
			SessionFile:          fixtureAlpha,
			Format:               "modern",
			LastEventAt:          now,
			LatestTurnStateKnown: true,
		}},
	}); err != nil {
		t.Fatalf("upsert alpha project: %v", err)
	}
	if err := st.UpsertProjectState(ctx, model.ProjectState{
		Path:           "/tmp/beta",
		Name:           "beta",
		Status:         model.StatusIdle,
		AttentionScore: 10,
		InScope:        true,
		UpdatedAt:      now,
		Sessions: []model.SessionEvidence{{
			SessionID:            "ses_beta",
			ProjectPath:          "/tmp/beta",
			DetectedProjectPath:  "/tmp/beta",
			SessionFile:          fixtureBeta,
			Format:               "modern",
			LastEventAt:          now,
			LatestTurnStateKnown: true,
		}},
	}); err != nil {
		t.Fatalf("upsert beta project: %v", err)
	}

	for _, classification := range []model.SessionClassification{
		{
			SessionID:         "ses_alpha",
			ProjectPath:       "/tmp/alpha",
			SessionFile:       fixtureAlpha,
			SessionFormat:     "modern",
			SnapshotHash:      "hash-alpha",
			Model:             "gpt-5-mini",
			ClassifierVersion: "v1",
			SourceUpdatedAt:   now,
		},
		{
			SessionID:         "ses_beta",
			ProjectPath:       "/tmp/beta",
			SessionFile:       fixtureBeta,
			SessionFormat:     "modern",
			SnapshotHash:      "hash-beta",
			Model:             "gpt-5-mini",
			ClassifierVersion: "v1",
			SourceUpdatedAt:   now,
		},
	} {
		if _, err := st.QueueSessionClassification(ctx, classification, 15*time.Minute); err != nil {
			t.Fatalf("queue classification %s: %v", classification.SessionID, err)
		}
		if _, err := st.UpdateSessionClassificationSummary(ctx, classification.SessionID, "Turn completed"); err != nil {
			t.Fatalf("seed bad summary %s: %v", classification.SessionID, err)
		}
	}

	if code := runSanitizeSummaries(ctx, st, config.AppConfig{
		SanitizeProject: "/tmp/alpha",
		SanitizeApply:   true,
	}); code != 0 {
		t.Fatalf("runSanitizeSummaries with project filter: code=%d", code)
	}

	alpha, err := st.GetSessionClassification(ctx, "ses_alpha")
	if err != nil {
		t.Fatalf("read alpha classification: %v", err)
	}
	if strings.TrimSpace(alpha.Summary) == "Turn completed" {
		t.Fatalf("alpha summary should be sanitized")
	}
	beta, err := st.GetSessionClassification(ctx, "ses_beta")
	if err != nil {
		t.Fatalf("read beta classification: %v", err)
	}
	if strings.TrimSpace(beta.Summary) != "Turn completed" {
		t.Fatalf("beta summary should remain unsanitized by project filter")
	}

	if code := runSanitizeSummaries(ctx, st, config.AppConfig{
		SanitizeSessionID: "ses_beta",
		SanitizeApply:     true,
	}); code != 0 {
		t.Fatalf("runSanitizeSummaries with session filter: code=%d", code)
	}
	beta, err = st.GetSessionClassification(ctx, "ses_beta")
	if err != nil {
		t.Fatalf("read beta classification after session filter: %v", err)
	}
	if strings.TrimSpace(beta.Summary) == "Turn completed" {
		t.Fatalf("beta summary should be sanitized after session-id filter")
	}
}
