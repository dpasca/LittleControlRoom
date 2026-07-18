package codexapp

import (
	"os"
	"runtime"
	"strings"
	"sync"
	"testing"
	"time"

	"lcroom/internal/codexcli"
)

func TestRestartIntentsFromSnapshotsKeepsOnlyLocallyOwnedInFlightTurns(t *testing.T) {
	capturedAt := time.Date(2026, 7, 12, 10, 0, 0, 0, time.UTC)
	intents := RestartIntentsFromSnapshots([]Snapshot{
		{
			Provider:     ProviderCodex,
			ProjectPath:  "/tmp/busy",
			ThreadID:     "thread-busy",
			ActiveTurnID: "turn-busy",
			Started:      true,
			Busy:         true,
			Phase:        SessionPhaseRunning,
		},
		{
			Provider:     ProviderOpenCode,
			ProjectPath:  "/tmp/external",
			ThreadID:     "session-external",
			ActiveTurnID: "session-external",
			Started:      true,
			Busy:         true,
			BusyExternal: true,
			Phase:        SessionPhaseExternal,
		},
		{
			Provider:    ProviderLCAgent,
			ProjectPath: "/tmp/idle",
			ThreadID:    "thread-idle",
			Started:     true,
			Phase:       SessionPhaseIdle,
		},
	}, capturedAt)

	if len(intents) != 1 {
		t.Fatalf("restart intents = %#v, want only one locally-owned busy turn", intents)
	}
	got := intents[0]
	if got.Provider != ProviderCodex || got.ProjectPath != "/tmp/busy" || got.SessionID != "thread-busy" || got.ActiveTurnID != "turn-busy" || !got.CapturedAt.Equal(capturedAt) {
		t.Fatalf("restart intent = %#v", got)
	}
}

func TestAcknowledgeRestartIntentsSerializesParallelSessionOpens(t *testing.T) {
	dataDir := t.TempDir()
	intents := []RestartIntent{
		{Provider: ProviderCodex, ProjectPath: "/tmp/a", SessionID: "thread-a", CapturedAt: time.Now()},
		{Provider: ProviderOpenCode, ProjectPath: "/tmp/b", SessionID: "thread-b", CapturedAt: time.Now()},
	}
	if err := WriteRestartIntents(dataDir, intents); err != nil {
		t.Fatal(err)
	}
	start := make(chan struct{})
	errs := make(chan error, len(intents))
	var wg sync.WaitGroup
	for _, intent := range intents {
		intent := intent
		wg.Add(1)
		go func() {
			defer wg.Done()
			<-start
			errs <- AcknowledgeRestartIntents(dataDir, []string{intent.Key()})
		}()
	}
	close(start)
	wg.Wait()
	close(errs)
	for err := range errs {
		if err != nil {
			t.Fatalf("parallel acknowledgement error = %v", err)
		}
	}
	remaining, err := ReadRestartIntents(dataDir)
	if err != nil || len(remaining) != 0 {
		t.Fatalf("parallel acknowledgements left %#v, err=%v", remaining, err)
	}
}

func TestManagerCloseAllForRestartPersistsInterruptsAndCloses(t *testing.T) {
	dataDir := t.TempDir()
	created := map[string]*fakeSession{}
	manager := NewManagerWithFactory(func(req LaunchRequest, notify func()) (Session, error) {
		snapshot := Snapshot{
			Provider:    req.Provider.Normalized(),
			ThreadID:    req.ResumeID,
			Started:     true,
			Preset:      req.Preset,
			Phase:       SessionPhaseIdle,
			ProjectPath: req.ProjectPath,
		}
		if req.ProjectPath == "/tmp/busy" {
			snapshot.Busy = true
			snapshot.ActiveTurnID = "turn-busy"
			snapshot.Phase = SessionPhaseRunning
		}
		if req.ProjectPath == "/tmp/external" {
			snapshot.Busy = true
			snapshot.BusyExternal = true
			snapshot.ActiveTurnID = "turn-external"
			snapshot.Phase = SessionPhaseExternal
		}
		session := &fakeSession{projectPath: req.ProjectPath, snapshot: snapshot}
		created[req.ProjectPath] = session
		return session, nil
	})

	for _, req := range []LaunchRequest{
		{Provider: ProviderCodex, ProjectPath: "/tmp/busy", ResumeID: "thread-busy", Preset: codexcli.PresetYolo},
		{Provider: ProviderCodex, ProjectPath: "/tmp/external", ResumeID: "thread-external", Preset: codexcli.PresetYolo},
		{Provider: ProviderLCAgent, ProjectPath: "/tmp/idle", ResumeID: "thread-idle"},
	} {
		if _, _, err := manager.Open(req); err != nil {
			t.Fatalf("manager.Open(%s) error = %v", req.ProjectPath, err)
		}
	}

	intents, err := manager.CloseAllForRestart(dataDir)
	if err != nil {
		t.Fatalf("CloseAllForRestart() error = %v", err)
	}
	if len(intents) != 1 || intents[0].SessionID != "thread-busy" {
		t.Fatalf("saved intents = %#v, want busy thread", intents)
	}
	if !created["/tmp/busy"].interrupted {
		t.Fatalf("locally-owned busy session was not interrupted before close")
	}
	if created["/tmp/external"].interrupted {
		t.Fatalf("external session must not be interrupted")
	}
	for path, session := range created {
		if !session.closed {
			t.Fatalf("session %s was not closed", path)
		}
	}

	loaded, err := ReadRestartIntents(dataDir)
	if err != nil {
		t.Fatalf("ReadRestartIntents() error = %v", err)
	}
	if len(loaded) != 1 || loaded[0].Key() != intents[0].Key() {
		t.Fatalf("loaded intents = %#v", loaded)
	}
	path, err := restartIntentPath(dataDir)
	if err != nil {
		t.Fatal(err)
	}
	info, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	if runtime.GOOS != "windows" && info.Mode().Perm() != 0o600 {
		t.Fatalf("restart intent permissions = %o, want 600", info.Mode().Perm())
	}

	// A process-level cleanup defer may run after the TUI already shut the
	// manager down. It must not erase the first pass's journal.
	if _, err := manager.CloseAllForRestart(dataDir); err != nil {
		t.Fatalf("second CloseAllForRestart() error = %v", err)
	}
	loaded, err = ReadRestartIntents(dataDir)
	if err != nil || len(loaded) != 1 {
		t.Fatalf("second shutdown changed journal: intents=%#v err=%v", loaded, err)
	}

	if err := AcknowledgeRestartIntents(dataDir, []string{intents[0].Key()}); err != nil {
		t.Fatalf("AcknowledgeRestartIntents() error = %v", err)
	}
	loaded, err = ReadRestartIntents(dataDir)
	if err != nil || len(loaded) != 0 {
		t.Fatalf("acknowledged journal = %#v, err=%v", loaded, err)
	}
}

func TestManagerCloseAllForRestartPreservesParallelLane(t *testing.T) {
	dataDir := t.TempDir()
	var created []*fakeSession
	manager := NewManagerWithFactory(func(req LaunchRequest, notify func()) (Session, error) {
		threadID := req.ResumeID
		activeTurnID := "turn-interactive"
		if strings.TrimSpace(req.Prompt) != "" {
			threadID = "thread-resolver"
			activeTurnID = "turn-resolver"
		}
		session := &fakeSession{
			projectPath: req.ProjectPath,
			snapshot: Snapshot{
				Provider:     req.Provider.Normalized(),
				ProjectPath:  req.ProjectPath,
				ThreadID:     threadID,
				ActiveTurnID: activeTurnID,
				Started:      true,
				Busy:         true,
				Phase:        SessionPhaseRunning,
				Preset:       req.Preset,
			},
		}
		created = append(created, session)
		return session, nil
	})

	if _, _, err := manager.Open(LaunchRequest{
		Provider:    ProviderCodex,
		ProjectPath: "/tmp/shared",
		ResumeID:    "thread-interactive",
	}); err != nil {
		t.Fatalf("Open() error = %v", err)
	}
	if _, _, err := manager.OpenParallel(LaunchRequest{
		Provider:    ProviderCodex,
		ProjectPath: "/tmp/shared",
		ForceNew:    true,
		Prompt:      "resolve conflicts",
	}); err != nil {
		t.Fatalf("OpenParallel() error = %v", err)
	}

	intents, err := manager.CloseAllForRestart(dataDir)
	if err != nil {
		t.Fatalf("CloseAllForRestart() error = %v", err)
	}
	if len(intents) != 2 {
		t.Fatalf("restart intents = %#v, want interactive and parallel lanes", intents)
	}
	var interactive, parallel *RestartIntent
	for i := range intents {
		intent := &intents[i]
		if intent.Parallel {
			parallel = intent
		} else {
			interactive = intent
		}
	}
	if interactive == nil || interactive.SessionID != "thread-interactive" {
		t.Fatalf("interactive restart intent = %#v", interactive)
	}
	if parallel == nil || parallel.SessionID != "thread-resolver" {
		t.Fatalf("parallel restart intent = %#v", parallel)
	}
	if interactive.Key() == parallel.Key() {
		t.Fatalf("restart lane keys collided: %q", interactive.Key())
	}
	for i, session := range created {
		if !session.interrupted || !session.closed {
			t.Fatalf("managed session %d shutdown state = interrupted %v closed %v", i, session.interrupted, session.closed)
		}
	}
}
