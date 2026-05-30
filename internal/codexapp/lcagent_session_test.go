package codexapp

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"lcroom/internal/projectrun"
)

func TestLCAgentSessionLaunchesConfiguredCommandAndStreamsTranscript(t *testing.T) {
	root := t.TempDir()
	dataDir := t.TempDir()
	argsPath := filepath.Join(t.TempDir(), "args.txt")
	envPath := filepath.Join(t.TempDir(), "openrouter.env")
	if err := os.WriteFile(envPath, []byte("OPENROUTER_API_KEY=test\n"), 0o600); err != nil {
		t.Fatalf("write env file: %v", err)
	}

	exe := filepath.Join(t.TempDir(), "fake-lcagent")
	script := `#!/bin/sh
{
  for arg in "$@"; do
    printf '%s\n' "$arg"
  done
} > "$LCAGENT_ARGS_FILE"
printf '%s\n' '{"type":"session_meta","id":"lca_fake_session","cwd":"/tmp/demo"}'
printf '%s\n' '{"type":"model_response","model":"test-model","usage":{"prompt_tokens":120,"prompt_tokens_details":{"cached_tokens":40},"completion_tokens":30,"total_tokens":150},"usage_summary":{"input_tokens":120,"output_tokens":30,"total_tokens":150,"cached_input_tokens":40}}'
printf '%s\n' '{"type":"tool_call","tool":"run_command"}'
printf '%s\n' '{"type":"tool_result","tool":"run_command","result":{"success":true,"output":"command ok"}}'
printf '%s\n' '{"type":"plan_update","items":[{"step":"exercise fake agent","status":"completed"}]}'
printf '%s\n' '{"type":"assistant_message","message":"fake lcagent response"}'
printf '%s\n' '{"type":"files_touched","files":["README.md"]}'
printf '%s\n' '{"type":"turn_complete"}'
`
	if err := os.WriteFile(exe, []byte(script), 0o700); err != nil {
		t.Fatalf("write fake lcagent: %v", err)
	}

	t.Setenv("LCAGENT_ARGS_FILE", argsPath)

	notify := make(chan struct{}, 20)
	session, err := newLCAgentSession(LaunchRequest{
		Provider:              ProviderLCAgent,
		ProjectPath:           root,
		AppDataDir:            dataDir,
		LCAgentPath:           exe,
		LCAgentEnvFile:        envPath,
		LCAgentProvider:       "deepseek",
		LCAgentAuto:           "medium",
		LCAgentAdminWrite:     true,
		LCAgentToolProfile:    "generous",
		LCAgentContextProfile: "large",
		LCAgentRequestTimeout: 10 * time.Minute,
		PendingModel:          "deepseek/test-model",
		PendingReasoning:      "low",
		Prompt:                "please run the fake agent",
	}, func() {
		select {
		case notify <- struct{}{}:
		default:
		}
	})
	if err != nil {
		t.Fatalf("newLCAgentSession() error = %v", err)
	}

	snapshot := waitForLCAgentIdleSnapshot(t, session, notify)
	snapshot = waitForLCAgentTranscript(t, session, "Files touched:\nREADME.md")
	if snapshot.Provider != ProviderLCAgent {
		t.Fatalf("Provider = %q, want %q", snapshot.Provider, ProviderLCAgent)
	}
	if snapshot.ThreadID != "lca_fake_session" {
		t.Fatalf("ThreadID = %q, want fake session id", snapshot.ThreadID)
	}
	if snapshot.Busy {
		t.Fatalf("Busy = true, want false")
	}
	if snapshot.Model != "test-model" || snapshot.ModelProvider != "deepseek" || snapshot.ReasoningEffort != "low" {
		t.Fatalf("model = %q/%q reasoning=%q, want deepseek/test-model low", snapshot.ModelProvider, snapshot.Model, snapshot.ReasoningEffort)
	}
	if snapshot.TokenUsage == nil || snapshot.TokenUsage.Last.InputTokens != 120 || snapshot.TokenUsage.Last.OutputTokens != 30 || snapshot.TokenUsage.Last.CachedInputTokens != 40 || snapshot.TokenUsage.Total.TotalTokens != 150 {
		t.Fatalf("TokenUsage = %#v", snapshot.TokenUsage)
	}
	for _, want := range []string{"please run the fake agent", "Tool run_command running", "command ok", "Plan:\n[x] exercise fake agent", "fake lcagent response", "Files touched:\nREADME.md"} {
		if !strings.Contains(snapshot.Transcript, want) {
			t.Fatalf("transcript missing %q:\n%s", want, snapshot.Transcript)
		}
	}

	argsBytes, err := os.ReadFile(argsPath)
	if err != nil {
		t.Fatalf("read fake args: %v", err)
	}
	args := strings.Split(strings.TrimSpace(string(argsBytes)), "\n")
	for _, want := range []string{
		"exec",
		"--cwd", root,
		"--data-dir", dataDir,
		"--auto", "medium",
		"--output", "stream-json",
		"--approval-mode", "ask",
		"--admin-write",
		"--utility-provider", "deepseek",
		"--utility-model", "test-model",
		"--provider", "deepseek",
		"--model", "test-model",
		"--tool-profile", "generous",
		"--context-profile", "large",
		"--request-timeout", "10m0s",
		"--reasoning-effort", "low",
		"--env-file", envPath,
		"please run the fake agent",
	} {
		if !lcagentTestStringSliceContains(args, want) {
			t.Fatalf("args missing %q: %#v", want, args)
		}
	}
}

func TestLCAgentSubmitReturnsBeforeSlowPreflight(t *testing.T) {
	root := t.TempDir()
	releasePreflight := make(chan struct{})
	preflightStarted := make(chan struct{})
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/models" {
			t.Fatalf("path = %s, want /models", r.URL.Path)
		}
		select {
		case <-preflightStarted:
		default:
			close(preflightStarted)
		}
		<-releasePreflight
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"data":[{"id":"mimo-v2.5-pro","name":"MiMo"}]}`))
	}))
	defer server.Close()

	exe := filepath.Join(t.TempDir(), "fake-lcagent")
	script := `#!/bin/sh
printf '%s\n' '{"type":"assistant_message","message":"slow preflight still did not trap the UI"}'
printf '%s\n' '{"type":"turn_complete","status":"ok"}'
`
	if err := os.WriteFile(exe, []byte(script), 0o755); err != nil {
		t.Fatalf("write fake lcagent: %v", err)
	}
	notify := make(chan struct{}, 20)
	session, err := newLCAgentSession(LaunchRequest{
		Provider:               ProviderLCAgent,
		ProjectPath:            root,
		AppDataDir:             t.TempDir(),
		LCAgentPath:            exe,
		LCAgentProvider:        "xiaomi",
		LCAgentXiaomiAPIKey:    "test-xiaomi-key",
		LCAgentXiaomiBaseURL:   server.URL,
		LCAgentPreflightAccess: true,
		LCAgentRequestTimeout:  time.Second,
	}, func() {
		select {
		case notify <- struct{}{}:
		default:
		}
	})
	if err != nil {
		t.Fatalf("newLCAgentSession() error = %v", err)
	}

	errCh := make(chan error, 1)
	go func() {
		errCh <- session.SubmitInput(Submission{Text: "exercise async preflight"})
	}()
	select {
	case err := <-errCh:
		if err != nil {
			t.Fatalf("SubmitInput() error = %v", err)
		}
	case <-time.After(100 * time.Millisecond):
		t.Fatal("SubmitInput blocked behind provider preflight")
	}
	if snapshot := session.Snapshot(); !snapshot.Busy {
		t.Fatalf("snapshot.Busy = false, want prompt accepted and run marked busy")
	}
	select {
	case <-preflightStarted:
	case <-time.After(time.Second):
		t.Fatal("preflight did not start")
	}
	close(releasePreflight)
	snapshot := waitForLCAgentIdleSnapshot(t, session, notify)
	if !strings.Contains(snapshot.Transcript, "slow preflight still did not trap the UI") {
		t.Fatalf("transcript missing fake response:\n%s", snapshot.Transcript)
	}
}

func TestLCAgentSessionWarnsWhenWebSearchUnavailable(t *testing.T) {
	session, err := newLCAgentSession(LaunchRequest{
		Provider:    ProviderLCAgent,
		ProjectPath: t.TempDir(),
		AppDataDir:  t.TempDir(),
	}, nil)
	if err != nil {
		t.Fatalf("newLCAgentSession() error = %v", err)
	}
	snapshot := session.Snapshot()
	if !strings.Contains(snapshot.Transcript, "LCAgent web search is not available") ||
		!strings.Contains(snapshot.Transcript, "/settings") {
		t.Fatalf("transcript missing web search setup warning:\n%s", snapshot.Transcript)
	}
}

func TestLCAgentSessionCloseDueToInactivity(t *testing.T) {
	notify := make(chan struct{}, 1)
	rawCtx, cancel := context.WithCancel(context.Background())
	session := &lcagentSession{
		projectPath: t.TempDir(),
		started:     true,
		status:      "LCAgent idle",
		cancel:      cancel,
		notify: func() {
			select {
			case notify <- struct{}{}:
			default:
			}
		},
		lastActivityAt: time.Now().Add(-2 * time.Minute),
	}

	if err := session.CloseDueToInactivity(); err != nil {
		t.Fatalf("CloseDueToInactivity() error = %v", err)
	}

	select {
	case <-notify:
	default:
		t.Fatalf("close should queue a notify callback")
	}
	select {
	case <-rawCtx.Done():
	default:
		t.Fatalf("close should cancel session context")
	}

	snapshot := session.Snapshot()
	if !snapshot.Closed {
		t.Fatalf("snapshot.Closed = %v, want true", snapshot.Closed)
	}
	if snapshot.Status != lcagentIdleShutdownNotice {
		t.Fatalf("snapshot.Status = %q, want %q", snapshot.Status, lcagentIdleShutdownNotice)
	}
	if !strings.Contains(snapshot.Transcript, lcagentIdleShutdownNotice) {
		t.Fatalf("snapshot.Transcript missing inactivity close notice: %q", snapshot.Transcript)
	}
}

func TestLCAgentSessionCloseDueToInactivitySkipsBusySession(t *testing.T) {
	prior := time.Now().Add(-2 * time.Minute)
	session := &lcagentSession{
		projectPath:    t.TempDir(),
		started:        true,
		busy:           true,
		status:         "LCAgent run",
		lastActivityAt: prior,
	}

	if err := session.CloseDueToInactivity(); err != nil {
		t.Fatalf("CloseDueToInactivity() error = %v", err)
	}

	snapshot := session.Snapshot()
	if snapshot.Closed {
		t.Fatalf("busy session should not close")
	}
	if snapshot.LastActivityAt.Equal(prior) {
		t.Fatalf("busy session inactivity close should touch last activity")
	}
}

func TestLCAgentSessionCloseDueToInactivitySkipsPendingApproval(t *testing.T) {
	prior := time.Now().Add(-2 * time.Minute)
	session := &lcagentSession{
		projectPath:    t.TempDir(),
		started:        true,
		status:         "Waiting for approval",
		lastActivityAt: prior,
		pendingApproval: &ApprovalRequest{
			ID:       "pending-1",
			Kind:     ApprovalCommandExecution,
			Command:  "echo hi",
			ThreadID: "thread-1",
		},
	}

	if err := session.CloseDueToInactivity(); err != nil {
		t.Fatalf("CloseDueToInactivity() error = %v", err)
	}

	snapshot := session.Snapshot()
	if snapshot.Closed {
		t.Fatalf("pending approval session should not close")
	}
	if snapshot.LastActivityAt.Equal(prior) {
		t.Fatalf("pending approval inactivity close should touch last activity")
	}
}

func TestLCAgentSessionApprovalRequestRoundTrip(t *testing.T) {
	stdin := &recordingWriteCloser{}
	session := &lcagentSession{
		projectPath: t.TempDir(),
		stdin:       stdin,
		started:     true,
		busy:        true,
		status:      "LCAgent running",
	}
	session.handleEvent([]byte(`{"type":"approval_request","session_id":"lca_approval_session","id":"approval-1","kind":"command","tool":"run_command","command":"corepack enable","cwd":"/repo","reason":"requires medium autonomy","scope":"this exact command in /repo"}`))

	snapshot := session.Snapshot()
	if snapshot.PendingApproval == nil {
		t.Fatal("PendingApproval = nil, want request")
	}
	if snapshot.PendingApproval.ID != "approval-1" || snapshot.PendingApproval.Kind != ApprovalCommandExecution ||
		snapshot.PendingApproval.Command != "corepack enable" || snapshot.PendingApproval.Scope != "this exact command in /repo" || snapshot.Status != "Waiting for command approval" {
		t.Fatalf("pending approval snapshot = %#v status=%q", snapshot.PendingApproval, snapshot.Status)
	}
	if !strings.Contains(snapshot.Transcript, "LCAgent requested command approval") {
		t.Fatalf("transcript missing approval request:\n%s", snapshot.Transcript)
	}
	if err := session.RespondApproval(DecisionAcceptForSession); err != nil {
		t.Fatalf("RespondApproval() error = %v", err)
	}
	if got := strings.Join(stdin.writes, ""); !strings.Contains(got, `"type":"approval_response"`) ||
		!strings.Contains(got, `"id":"approval-1"`) ||
		!strings.Contains(got, `"decision":"acceptForSession"`) {
		t.Fatalf("approval response payload = %q", got)
	}
	if snapshot = session.Snapshot(); !strings.Contains(snapshot.Transcript, "LCAgent permission level changed to Medium for this run: corepack enable") {
		t.Fatalf("transcript missing immediate approval feedback:\n%s", snapshot.Transcript)
	}

	session.handleEvent([]byte(`{"type":"permission_level_changed","session_id":"lca_approval_session","from":"low","to":"medium","reason":"approval accepted for session"}`))
	snapshot = session.Snapshot()
	if snapshot.Status != "LCAgent permission level changed from Low to Medium for this run" {
		t.Fatalf("status after permission level change = %q", snapshot.Status)
	}

	session.handleEvent([]byte(`{"type":"approval_resolved","session_id":"lca_approval_session","id":"approval-1","kind":"command","tool":"run_command","command":"corepack enable","scope":"this exact command in /repo","decision":"acceptForSession","status":"approved"}`))
	snapshot = session.Snapshot()
	if snapshot.PendingApproval != nil {
		t.Fatalf("PendingApproval after resolution = %#v, want nil", snapshot.PendingApproval)
	}
	if !strings.Contains(snapshot.Status, "Medium") ||
		!strings.Contains(snapshot.Transcript, "corepack enable") {
		t.Fatalf("approval resolution not reflected; status=%q transcript=\n%s", snapshot.Status, snapshot.Transcript)
	}
}

func TestLCAgentProcessRequestStartsManagedRuntime(t *testing.T) {
	projectPath := t.TempDir()
	frontend := filepath.Join(projectPath, "frontend")
	if err := os.MkdirAll(frontend, 0o755); err != nil {
		t.Fatal(err)
	}
	manager := projectrun.NewManager()
	defer func() { _ = manager.CloseAll() }()
	stdin := &recordingWriteCloser{}
	session := &lcagentSession{
		projectPath:    projectPath,
		runtimeManager: manager,
		stdin:          stdin,
		started:        true,
		busy:           true,
		status:         "LCAgent running",
	}

	session.handleEvent([]byte(`{"type":"process_request","session_id":"lca_process_session","id":"process-1","action":"start","command":"pwd; sleep 30","cwd":"frontend"}`))

	if got := strings.Join(stdin.writes, ""); !strings.Contains(got, `"type":"process_response"`) ||
		!strings.Contains(got, `"id":"process-1"`) ||
		!strings.Contains(got, `"success":true`) ||
		!strings.Contains(got, `"managed_process"`) ||
		!strings.Contains(got, `"command":"pwd; sleep 30"`) {
		t.Fatalf("process response payload = %q", got)
	}
	snapshot, err := manager.Snapshot(projectPath)
	if err != nil {
		t.Fatalf("Snapshot() error = %v", err)
	}
	if !snapshot.Running || snapshot.Command != "pwd; sleep 30" || snapshot.CWD != frontend {
		t.Fatalf("runtime snapshot = %#v", snapshot)
	}
	transcript := session.Snapshot().Transcript
	if !strings.Contains(transcript, "LCAgent starting managed process: pwd; sleep 30 in frontend") ||
		!strings.Contains(transcript, "Started managed process") ||
		!strings.Contains(transcript, "pwd; sleep 30") {
		t.Fatalf("transcript missing managed process status:\n%s", transcript)
	}
}

func TestLCAgentProcessRequestStartsDistinctManagedRuntimes(t *testing.T) {
	projectPath := t.TempDir()
	manager := projectrun.NewManager()
	defer func() { _ = manager.CloseAll() }()
	stdin := &recordingWriteCloser{}
	session := &lcagentSession{
		projectPath:    projectPath,
		runtimeManager: manager,
		stdin:          stdin,
		started:        true,
		busy:           true,
		status:         "LCAgent running",
	}

	session.handleEvent([]byte(`{"type":"process_request","session_id":"lca_process_session","id":"process-1","action":"start","name":"frontend","command":"sleep 30"}`))
	session.handleEvent([]byte(`{"type":"process_request","session_id":"lca_process_session","id":"process-2","action":"start","name":"emulators","command":"sleep 30"}`))

	if got := strings.Join(stdin.writes, ""); !strings.Contains(got, `"id":"process-1"`) ||
		!strings.Contains(got, `"id":"process-2"`) ||
		strings.Count(got, `"success":true`) != 2 {
		t.Fatalf("process response payloads = %q", got)
	}
	snapshots := manager.SnapshotsForProject(projectPath)
	if len(snapshots) != 2 {
		t.Fatalf("SnapshotsForProject() len = %d, want 2: %+v", len(snapshots), snapshots)
	}
	ids := map[string]bool{}
	names := map[string]bool{}
	for _, snapshot := range snapshots {
		if !snapshot.Running || snapshot.Command != "sleep 30" {
			t.Fatalf("runtime snapshot = %#v", snapshot)
		}
		if snapshot.ID == "" {
			t.Fatalf("runtime snapshot missing process ID: %#v", snapshot)
		}
		ids[snapshot.ID] = true
		names[snapshot.Name] = true
	}
	if len(ids) != 2 || !names["frontend"] || !names["emulators"] {
		t.Fatalf("snapshots should have distinct IDs and names: %+v", snapshots)
	}
}

func TestLCAgentProcessListIsScopedToSessionWorkspace(t *testing.T) {
	projectPath := t.TempDir()
	otherPath := t.TempDir()
	manager := projectrun.NewManager()
	defer func() { _ = manager.CloseAll() }()
	if _, err := manager.Start(projectrun.StartRequest{
		ProjectPath: projectPath,
		Command:     "sleep 30",
		Name:        "current",
		CreateNew:   true,
	}); err != nil {
		t.Fatalf("Start(current) error = %v", err)
	}
	if _, err := manager.Start(projectrun.StartRequest{
		ProjectPath: otherPath,
		Command:     "sleep 30",
		Name:        "other",
		CreateNew:   true,
	}); err != nil {
		t.Fatalf("Start(other) error = %v", err)
	}
	stdin := &recordingWriteCloser{}
	session := &lcagentSession{
		projectPath:    projectPath,
		runtimeManager: manager,
		stdin:          stdin,
		started:        true,
		busy:           true,
		status:         "LCAgent running",
	}

	session.handleEvent([]byte(`{"type":"process_request","session_id":"lca_process_session","id":"process-list","action":"list"}`))

	got := strings.Join(stdin.writes, "")
	if !strings.Contains(got, `"id":"process-list"`) || !strings.Contains(got, "name current") {
		t.Fatalf("process list response missing current workspace process: %q", got)
	}
	if strings.Contains(got, "name other") || strings.Contains(got, otherPath) {
		t.Fatalf("process list response should not include other workspace process: %q", got)
	}
}

func TestLCAgentSessionLaunchesManagedProcessAfterStopWithoutDoubleApproval(t *testing.T) {
	root := t.TempDir()
	dataDir := t.TempDir()
	frontend := filepath.Join(root, "frontend")
	if err := os.MkdirAll(frontend, 0o755); err != nil {
		t.Fatal(err)
	}
	manager := projectrun.NewManager()
	defer func() { _ = manager.CloseAll() }()

	exe := filepath.Join(t.TempDir(), "fake-lcagent-managed-process")
	script := `#!/bin/sh
printf '%s\n' '{"type":"session_meta","id":"lca_fake_managed_process","cwd":"` + root + `"}'
printf '%s\n' '{"type":"tool_call","tool":"stop_process","args":{}}'
printf '%s\n' '{"type":"process_request","session_id":"lca_fake_managed_process","id":"process-stop","action":"stop"}'
IFS= read -r stop_response || exit 2
printf '%s\n' '{"type":"tool_result","tool":"stop_process","result":{"success":true,"output":"No managed process is running for this workspace."}}'
printf '%s\n' '{"type":"tool_call","tool":"start_process","args":{"command":"printf managed-ready; sleep 30","cwd":"frontend"}}'
printf '%s\n' '{"type":"approval_request","session_id":"lca_fake_managed_process","id":"approval-start","kind":"command","tool":"start_process","command":"printf managed-ready; sleep 30","cwd":"` + frontend + `","reason":"managed background process requires approval at low autonomy","scope":"this exact command in ` + frontend + `"}'
IFS= read -r approval_response || exit 3
printf '%s\n' '{"type":"approval_resolved","session_id":"lca_fake_managed_process","id":"approval-start","kind":"command","tool":"start_process","command":"printf managed-ready; sleep 30","cwd":"` + frontend + `","decision":"accept","status":"approved"}'
printf '%s\n' '{"type":"process_request","session_id":"lca_fake_managed_process","id":"process-start","action":"start","command":"printf managed-ready; sleep 30","cwd":"frontend"}'
IFS= read -r start_response || exit 4
printf '%s\n' '{"type":"tool_result","tool":"start_process","result":{"success":true,"output":"Started managed process"}}'
printf '%s\n' '{"type":"turn_complete"}'
`
	if err := os.WriteFile(exe, []byte(script), 0o700); err != nil {
		t.Fatalf("write fake lcagent: %v", err)
	}

	notify := make(chan struct{}, 20)
	session, err := newLCAgentSession(LaunchRequest{
		Provider:       ProviderLCAgent,
		ProjectPath:    root,
		AppDataDir:     dataDir,
		LCAgentPath:    exe,
		LCAgentAuto:    "low",
		RuntimeManager: manager,
		Prompt:         "again please",
	}, func() {
		select {
		case notify <- struct{}{}:
		default:
		}
	})
	if err != nil {
		t.Fatalf("newLCAgentSession() error = %v", err)
	}

	snapshot := waitForLCAgentPendingApproval(t, session, notify, "printf managed-ready; sleep 30")
	if strings.Contains(snapshot.Transcript, "Command approval: stop managed process") {
		t.Fatalf("stop_process should not request approval:\n%s", snapshot.Transcript)
	}
	if err := session.RespondApproval(DecisionAccept); err != nil {
		t.Fatalf("RespondApproval() error = %v", err)
	}

	snapshot = waitForLCAgentIdleSnapshot(t, session, notify)
	if snapshot.LastError != "" {
		t.Fatalf("LCAgent session finished with error: %s\n%s", snapshot.LastError, snapshot.Transcript)
	}
	runtimeSnapshot, err := manager.Snapshot(root)
	if err != nil {
		t.Fatalf("runtime Snapshot() error = %v", err)
	}
	if !runtimeSnapshot.Running || runtimeSnapshot.Command != "printf managed-ready; sleep 30" || runtimeSnapshot.CWD != frontend {
		t.Fatalf("runtime snapshot = %#v", runtimeSnapshot)
	}
	for _, want := range []string{
		"LCAgent stopping managed process",
		"No managed process is running for this workspace.",
		"LCAgent requested command approval",
		"LCAgent approval decision sent: printf managed-ready; sleep 30",
		"LCAgent starting managed process: printf managed-ready; sleep 30 in frontend",
		"Started managed process",
	} {
		if !strings.Contains(snapshot.Transcript, want) {
			t.Fatalf("transcript missing %q:\n%s", want, snapshot.Transcript)
		}
	}
}

func TestLCAgentRepoOverviewTranscriptSummaries(t *testing.T) {
	call := lcagentToolCallText("repo_overview", json.RawMessage(`{"path":".","max_files":40,"include_hidden":true}`))
	if call != "Tool repo_overview running: . 40 files include hidden" {
		t.Fatalf("call summary = %q", call)
	}

	raw := json.RawMessage(`{"success":true,"truncated":true,"output":"path: .\nsource: git ls-files + untracked\ngit: true\nbranch: feature/repo-overview\ntracked_files: 12\ndirty: true\n\nfile_sample:\n- README.md\n"}`)
	result := lcagentToolResultText("repo_overview", raw)
	want := "Tool repo_overview completed: . git feature/repo-overview dirty 12 tracked via git ls-files + untracked truncated"
	if result != want {
		t.Fatalf("result summary = %q, want %q", result, want)
	}
}

func TestLCAgentCommandTranscriptSummariesIncludeCWD(t *testing.T) {
	call := lcagentToolCallText("run_command", json.RawMessage(`{"argv":["pnpm","run","lint"],"cwd":"frontend"}`))
	if call != "Tool run_command running: pnpm run lint in frontend" {
		t.Fatalf("call summary = %q", call)
	}

	result := lcagentToolResultText("run_command", json.RawMessage(`{"success":false,"command":"pnpm run lint","cwd":"/repo/frontend","error":"exit status 1","exit_code":1}`))
	for _, want := range []string{"Tool run_command failed", "pnpm run lint", "in /repo/frontend", "exit status 1"} {
		if !strings.Contains(result, want) {
			t.Fatalf("result summary missing %q: %q", want, result)
		}
	}
}

func TestLCAgentVerificationCheckTextIncludesCWD(t *testing.T) {
	got := lcagentVerificationCheckText(map[string]json.RawMessage{
		"command": json.RawMessage(`"pnpm run build"`),
		"cwd":     json.RawMessage(`"/repo/frontend"`),
		"status":  json.RawMessage(`"passed"`),
	})
	if got != "Verification passed: pnpm run build in /repo/frontend" {
		t.Fatalf("verification check text = %q", got)
	}
}

func TestLCAgentContinuationStatusOmitsOldSummaryBody(t *testing.T) {
	event := map[string]json.RawMessage{
		"parent_session_id":   json.RawMessage(`"lca_previous"`),
		"chain_depth":         json.RawMessage(`2`),
		"context_mode":        json.RawMessage(`"exact"`),
		"exact_message_count": json.RawMessage(`34`),
		"pending_status":      json.RawMessage(`"failed"`),
		"parent_summary":      json.RawMessage(`"Next step: deploy the built out directory to Firebase Hosting."`),
	}
	got := lcagentContinuationText(event)
	if !strings.Contains(got, "Continuing LCAgent from lca_previous") || !strings.Contains(got, "exact replay 34 messages") {
		t.Fatalf("continuation text = %q", got)
	}
	if strings.Contains(got, "deploy the built out") {
		t.Fatalf("continuation text should not include old summary body: %q", got)
	}
}

func TestLCAgentSessionReplaysRequestedArtifact(t *testing.T) {
	root := t.TempDir()
	dataDir := t.TempDir()
	sessionID := "lca_replay_requested"
	started := time.Date(2026, 5, 9, 9, 30, 0, 0, time.UTC)
	last := started.Add(6 * time.Second)
	writeLCAgentReplayArtifact(t, dataDir, started, sessionID, []map[string]any{
		{
			"type":       "session_meta",
			"id":         sessionID,
			"cwd":        root,
			"started_at": started.Format(time.RFC3339Nano),
			"provider":   "openrouter",
			"model":      "deepseek/replay-model",
		},
		{
			"type":       "user_message",
			"session_id": sessionID,
			"timestamp":  started.Add(time.Second).Format(time.RFC3339Nano),
			"message":    "summarize this repo",
		},
		{
			"type":                "continuation",
			"session_id":          sessionID,
			"timestamp":           started.Add(1500 * time.Millisecond).Format(time.RFC3339Nano),
			"parent_session_id":   "lca_parent_replay",
			"chain_depth":         1,
			"handoff_source":      "final_handoff",
			"pending_status":      "missing_after_changes",
			"pending_files":       []string{"README.md"},
			"continuation_reason": "continue_from",
		},
		{
			"type":       "tool_call",
			"session_id": sessionID,
			"timestamp":  started.Add(2 * time.Second).Format(time.RFC3339Nano),
			"tool":       "read_file",
		},
		{
			"type":       "model_response",
			"session_id": sessionID,
			"timestamp":  started.Add(2500 * time.Millisecond).Format(time.RFC3339Nano),
			"model":      "deepseek/replay-model",
			"usage_summary": map[string]any{
				"input_tokens":        200,
				"output_tokens":       50,
				"total_tokens":        250,
				"cached_input_tokens": 75,
			},
		},
		{
			"type":       "tool_result",
			"session_id": sessionID,
			"timestamp":  started.Add(3 * time.Second).Format(time.RFC3339Nano),
			"tool":       "read_file",
			"result": map[string]any{
				"success": true,
				"output":  "file: README.md\nlines: 1-1\n\n1 | hello from README\n",
			},
		},
		{
			"type":       "plan_update",
			"session_id": sessionID,
			"timestamp":  started.Add(4 * time.Second).Format(time.RFC3339Nano),
			"items": []map[string]any{{
				"step":   "inspect repo",
				"status": "done",
			}},
		},
		{
			"type":       "assistant_message",
			"session_id": sessionID,
			"timestamp":  started.Add(5 * time.Second).Format(time.RFC3339Nano),
			"message":    "Replay answer",
		},
		{
			"type":       "files_touched",
			"session_id": sessionID,
			"timestamp":  last.Format(time.RFC3339Nano),
			"files":      []string{"README.md"},
		},
		{
			"type":       "turn_complete",
			"session_id": sessionID,
			"timestamp":  last.Format(time.RFC3339Nano),
		},
	})

	openedAt := time.Now()
	session, err := newLCAgentSession(LaunchRequest{
		Provider:    ProviderLCAgent,
		ProjectPath: root,
		AppDataDir:  dataDir,
		ResumeID:    sessionID,
	}, nil)
	if err != nil {
		t.Fatalf("newLCAgentSession() error = %v", err)
	}

	snapshot := session.Snapshot()
	if snapshot.ThreadID != sessionID {
		t.Fatalf("ThreadID = %q, want replayed session id", snapshot.ThreadID)
	}
	if !snapshot.Started || snapshot.Busy {
		t.Fatalf("Started/Busy = %v/%v, want replayed idle session", snapshot.Started, snapshot.Busy)
	}
	if snapshot.Status != "Loaded LCAgent thread "+sessionID+" from disk" {
		t.Fatalf("Status = %q", snapshot.Status)
	}
	if snapshot.LastActivityAt.Before(openedAt) {
		t.Fatalf("LastActivityAt = %s, want refreshed embedded activity at or after %s", snapshot.LastActivityAt, openedAt)
	}
	if snapshot.Model != "deepseek/replay-model" || snapshot.ModelProvider != "openrouter" {
		t.Fatalf("model = %q/%q, want openrouter/deepseek replay model", snapshot.ModelProvider, snapshot.Model)
	}
	if snapshot.TokenUsage == nil || snapshot.TokenUsage.Last.InputTokens != 200 || snapshot.TokenUsage.Last.OutputTokens != 50 || snapshot.TokenUsage.Last.CachedInputTokens != 75 || snapshot.TokenUsage.Total.TotalTokens != 250 {
		t.Fatalf("TokenUsage = %#v", snapshot.TokenUsage)
	}
	for _, want := range []string{
		"Loaded LCAgent thread " + sessionID + " from disk. Sending a prompt starts a continuing run from canonical thread state.",
		"summarize this repo",
		"Continuing LCAgent from lca_parent_replay",
		"pending verification missing_after_changes",
		"Tool read_file running",
		"README.md lines 1-1",
		"Plan:\n[x] inspect repo",
		"Replay answer",
		"Files touched:\nREADME.md",
	} {
		if !strings.Contains(snapshot.Transcript, want) {
			t.Fatalf("transcript missing %q:\n%s", want, snapshot.Transcript)
		}
	}
	if strings.Contains(snapshot.Transcript, "1 | hello from README") {
		t.Fatalf("transcript should summarize read_file contents instead of embedding them:\n%s", snapshot.Transcript)
	}
}

func TestLCAgentSessionReplayStitchesAncestorHistory(t *testing.T) {
	root := t.TempDir()
	dataDir := t.TempDir()
	started := time.Date(2026, 5, 22, 19, 0, 0, 0, time.UTC)
	writeLCAgentReplayArtifact(t, dataDir, started, "lca_parent", []map[string]any{
		{
			"type":       "session_meta",
			"id":         "lca_parent",
			"cwd":        root,
			"started_at": started.Format(time.RFC3339Nano),
		},
		{
			"type":       "user_message",
			"session_id": "lca_parent",
			"timestamp":  started.Add(time.Second).Format(time.RFC3339Nano),
			"message":    "launch the site locally",
		},
		{
			"type":       "assistant_message",
			"session_id": "lca_parent",
			"timestamp":  started.Add(2 * time.Second).Format(time.RFC3339Nano),
			"message":    "The dev server is running at http://localhost:3001.",
		},
		{
			"type":                "turn_complete",
			"session_id":          "lca_parent",
			"timestamp":           started.Add(3 * time.Second).Format(time.RFC3339Nano),
			"summary":             "The dev server is running at http://localhost:3001.",
			"verification_status": "not_run",
		},
	})
	writeLCAgentReplayArtifact(t, dataDir, started.Add(4*time.Second), "lca_child", []map[string]any{
		{
			"type":              "session_meta",
			"id":                "lca_child",
			"parent_session_id": "lca_parent",
			"cwd":               root,
			"started_at":        started.Add(4 * time.Second).Format(time.RFC3339Nano),
		},
		{
			"type":              "continuation",
			"session_id":        "lca_child",
			"parent_session_id": "lca_parent",
			"timestamp":         started.Add(5 * time.Second).Format(time.RFC3339Nano),
		},
		{
			"type":       "user_message",
			"session_id": "lca_child",
			"timestamp":  started.Add(6 * time.Second).Format(time.RFC3339Nano),
			"message":    "again please",
		},
	})

	session, err := newLCAgentSession(LaunchRequest{
		Provider:    ProviderLCAgent,
		ProjectPath: root,
		AppDataDir:  dataDir,
		ResumeID:    "lca_child",
	}, nil)
	if err != nil {
		t.Fatalf("newLCAgentSession() error = %v", err)
	}
	snapshot := session.Snapshot()
	if snapshot.ThreadID != "lca_child" {
		t.Fatalf("ThreadID = %q, want child session", snapshot.ThreadID)
	}
	for _, want := range []string{"launch the site locally", "http://localhost:3001", "again please"} {
		if !strings.Contains(snapshot.Transcript, want) {
			t.Fatalf("stitched transcript missing %q:\n%s", want, snapshot.Transcript)
		}
	}
}

func TestLCAgentSessionReplayUsesCanonicalThreadState(t *testing.T) {
	root := t.TempDir()
	dataDir := t.TempDir()
	threadID := "lct_replay_thread"
	started := time.Date(2026, 5, 23, 1, 0, 0, 0, time.UTC)
	writeLCAgentReplayArtifact(t, dataDir, started, "lca_run_one", []map[string]any{
		{"type": "session_meta", "id": "lca_run_one", "thread_id": threadID, "cwd": root, "started_at": started.Format(time.RFC3339Nano)},
		{"type": "user_message", "session_id": "lca_run_one", "message": "first prompt", "timestamp": started.Add(time.Second).Format(time.RFC3339Nano)},
		{"type": "assistant_message", "session_id": "lca_run_one", "message": "first answer", "timestamp": started.Add(2 * time.Second).Format(time.RFC3339Nano)},
		{"type": "turn_complete", "session_id": "lca_run_one", "summary": "first answer", "timestamp": started.Add(3 * time.Second).Format(time.RFC3339Nano)},
	})
	writeLCAgentReplayArtifact(t, dataDir, started.Add(4*time.Second), "lca_run_two", []map[string]any{
		{"type": "session_meta", "id": "lca_run_two", "thread_id": threadID, "cwd": root, "started_at": started.Add(4 * time.Second).Format(time.RFC3339Nano)},
		{"type": "user_message", "session_id": "lca_run_two", "message": "second prompt", "timestamp": started.Add(5 * time.Second).Format(time.RFC3339Nano)},
		{"type": "assistant_message", "session_id": "lca_run_two", "message": "second answer", "timestamp": started.Add(6 * time.Second).Format(time.RFC3339Nano)},
		{"type": "turn_complete", "session_id": "lca_run_two", "summary": "second answer", "timestamp": started.Add(7 * time.Second).Format(time.RFC3339Nano)},
	})
	stateDir := filepath.Join(dataDir, "lcagent", "threads", threadID)
	if err := os.MkdirAll(stateDir, 0o755); err != nil {
		t.Fatalf("mkdir thread state: %v", err)
	}
	state := map[string]any{
		"version":           1,
		"thread_id":         threadID,
		"project_path":      root,
		"created_at":        started,
		"updated_at":        started.Add(8 * time.Second),
		"last_run_id":       "lca_run_two",
		"status":            "stable",
		"context_mode":      "exact",
		"last_stable_point": "assistant_message",
		"messages":          []map[string]string{{"role": "user", "content": "first prompt"}, {"role": "assistant", "content": "first answer"}},
		"message_count":     2,
	}
	body, err := json.Marshal(state)
	if err != nil {
		t.Fatalf("marshal thread state: %v", err)
	}
	if err := os.WriteFile(filepath.Join(stateDir, "state.json"), body, 0o600); err != nil {
		t.Fatalf("write thread state: %v", err)
	}

	session, err := newLCAgentSession(LaunchRequest{
		Provider:    ProviderLCAgent,
		ProjectPath: root,
		AppDataDir:  dataDir,
	}, nil)
	if err != nil {
		t.Fatalf("newLCAgentSession() error = %v", err)
	}
	snapshot := session.Snapshot()
	if snapshot.ThreadID != threadID {
		t.Fatalf("ThreadID = %q, want canonical thread", snapshot.ThreadID)
	}
	for _, want := range []string{"first prompt", "first answer", "second prompt", "second answer"} {
		if !strings.Contains(snapshot.Transcript, want) {
			t.Fatalf("thread replay missing %q:\n%s", want, snapshot.Transcript)
		}
	}
}

func TestLCAgentSessionListModelsReturnsCuratedCodingRoutes(t *testing.T) {
	session, err := newLCAgentSession(LaunchRequest{
		Provider:        ProviderLCAgent,
		ProjectPath:     t.TempDir(),
		AppDataDir:      t.TempDir(),
		LCAgentProvider: "openrouter",
	}, nil)
	if err != nil {
		t.Fatalf("newLCAgentSession() error = %v", err)
	}
	models, err := session.ListModels()
	if err != nil {
		t.Fatalf("ListModels() error = %v", err)
	}
	if len(models) < 3 {
		t.Fatalf("ListModels() returned %d models, want curated routes: %#v", len(models), models)
	}
	want := map[string]string{
		"deepseek/deepseek-v4-pro":   "Balanced: DeepSeek V4 Pro",
		"openai/gpt-5.5":             "Quality: GPT-5.5",
		"deepseek/deepseek-v4-flash": "Cheap Scout: DeepSeek V4 Flash",
	}
	for _, option := range models {
		delete(want, option.Model)
		if option.Model == "openai/gpt-5.5" && option.DefaultReasoningEffort != "low" {
			t.Fatalf("GPT-5.5 default reasoning = %q, want low", option.DefaultReasoningEffort)
		}
	}
	if len(want) > 0 {
		t.Fatalf("missing curated LCAgent model options: %#v in %#v", want, models)
	}
	if !models[0].IsDefault {
		t.Fatalf("first model should be default balanced route: %#v", models[0])
	}
}

func TestLCAgentModelOptionsForProviderMoonshotHasNoReasoningControls(t *testing.T) {
	options := lcagentModelOptionsForProvider("moonshot")
	if len(options) != 1 {
		t.Fatalf("provider options = %#v, want one moonshot option", options)
	}
	if options[0].Model != "kimi-k2.6" {
		t.Fatalf("moonshot model = %q, want kimi-k2.6", options[0].Model)
	}
	if len(options[0].SupportedReasoningEfforts) != 0 {
		t.Fatalf("reasoning options = %#v, want none", options[0].SupportedReasoningEfforts)
	}
}

func TestLCAgentProviderModelOptionsMoonshotHasNoReasoningControls(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/models" {
			t.Fatalf("path = %q, want /models", r.URL.Path)
		}
		_, _ = w.Write([]byte(`{"data":[{"id":"kimi-k2.6","name":"Kimi K2.6","description":"Moonshot direct coding model"}]}`))
	}))
	defer server.Close()
	t.Setenv("MOONSHOT_BASE_URL", server.URL)

	models, err := lcagentProviderModelOptions(context.Background(), LCAgentModelListConfig{
		MoonshotAPIKey: "moonshot-key",
	}, "moonshot")
	if err != nil {
		t.Fatalf("LCAgentProviderModelOptions() error = %v", err)
	}
	if len(models) != 1 {
		t.Fatalf("models = %#v, want one moonshot model option", models)
	}
	if len(models[0].SupportedReasoningEfforts) != 0 {
		t.Fatalf("reasoning options = %#v, want none for moonshot", models[0].SupportedReasoningEfforts)
	}
	if models[0].DefaultReasoningEffort != "" {
		t.Fatalf("default reasoning = %q, want empty", models[0].DefaultReasoningEffort)
	}
}

func TestLCAgentSessionListModelsKeepsCustomCurrentModel(t *testing.T) {
	session, err := newLCAgentSession(LaunchRequest{
		Provider:        ProviderLCAgent,
		ProjectPath:     t.TempDir(),
		AppDataDir:      t.TempDir(),
		LCAgentProvider: "deepseek",
		PendingModel:    "deepseek/custom-experiment",
	}, nil)
	if err != nil {
		t.Fatalf("newLCAgentSession() error = %v", err)
	}
	models, err := session.ListModels()
	if err != nil {
		t.Fatalf("ListModels() error = %v", err)
	}
	if len(models) == 0 || models[0].Model != "custom-experiment" {
		t.Fatalf("custom model should be preserved first: %#v", models)
	}
}

func TestLCAgentModelOptionsMergesProviderModelList(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/models" {
			t.Fatalf("path = %q, want /models", r.URL.Path)
		}
		if r.Header.Get("Authorization") != "Bearer key" {
			t.Fatalf("Authorization = %q", r.Header.Get("Authorization"))
		}
		_, _ = w.Write([]byte(`{"data":[{"id":"deepseek/deepseek-v4-pro","name":"DeepSeek V4 Pro"},{"id":"provider/new-coder","name":"New Coder","description":"fresh provider route"}]}`))
	}))
	defer server.Close()
	t.Setenv("OPENROUTER_BASE_URL", server.URL)

	models, err := LCAgentModelOptions(context.Background(), LCAgentModelListConfig{
		Provider:         "openrouter",
		Model:            "missing/custom",
		OpenRouterAPIKey: "key",
	})
	if err != nil {
		t.Fatalf("LCAgentModelOptions() error = %v", err)
	}
	if len(models) < 2 || models[0].Model != "missing/custom" {
		t.Fatalf("custom model should be preserved first: %#v", models)
	}
	if !strings.Contains(models[0].Description, "did not return") {
		t.Fatalf("custom model should mention provider miss: %#v", models[0])
	}
	foundProviderModel := false
	foundVerifiedDefault := false
	for _, option := range models {
		if option.Model == "provider/new-coder" && option.DisplayName == "New Coder" && strings.Contains(option.Description, "fresh provider route") {
			foundProviderModel = true
		}
		if option.Model == "deepseek/deepseek-v4-pro" && strings.Contains(strings.ToLower(option.Description), "verified") {
			foundVerifiedDefault = true
		}
	}
	if !foundProviderModel {
		t.Fatalf("provider model missing from options: %#v", models)
	}
	if !foundVerifiedDefault {
		t.Fatalf("curated default should be marked verified by provider list: %#v", models)
	}
}

func TestLCAgentSessionLaunchUsesRoutePresetBundle(t *testing.T) {
	root := t.TempDir()
	argsPath := filepath.Join(t.TempDir(), "args.txt")
	envCapturePath := filepath.Join(t.TempDir(), "env.txt")
	exe := filepath.Join(t.TempDir(), "fake-lcagent")
	script := `#!/bin/sh
{
  for arg in "$@"; do
    printf '%s\n' "$arg"
  done
} > "$LCAGENT_ARGS_FILE"
printf '%s\n' "$OPENAI_API_KEY" > "$LCAGENT_ENV_CAPTURE_FILE"
printf '%s\n' '{"type":"session_meta","id":"lca_route_session","cwd":"/tmp/demo"}'
printf '%s\n' '{"type":"turn_complete","summary":"route preset run"}'
`
	if err := os.WriteFile(exe, []byte(script), 0o700); err != nil {
		t.Fatalf("write fake lcagent: %v", err)
	}
	t.Setenv("LCAGENT_ARGS_FILE", argsPath)
	t.Setenv("LCAGENT_ENV_CAPTURE_FILE", envCapturePath)
	t.Setenv("OPENAI_API_KEY", "process-openai-key")

	notify := make(chan struct{}, 20)
	session, err := newLCAgentSession(LaunchRequest{
		Provider:              ProviderLCAgent,
		ProjectPath:           root,
		AppDataDir:            t.TempDir(),
		LCAgentPath:           exe,
		LCAgentOpenAIAPIKey:   "saved-openai-key",
		LCAgentRoutePreset:    "quality",
		LCAgentProvider:       "deepseek",
		LCAgentAuto:           "medium",
		LCAgentAdminWrite:     true,
		LCAgentToolProfile:    "generous",
		LCAgentRequestTimeout: 37 * time.Minute,
		PendingModel:          "",
		PendingReasoning:      "high",
		LCAgentEnvFile:        "/tmp/test.env",
		LCAgentWebSearchURL:   "http://127.0.0.1:8888",
		Prompt:                "use the configured route",
	}, func() {
		select {
		case notify <- struct{}{}:
		default:
		}
	})
	if err != nil {
		t.Fatalf("newLCAgentSession() error = %v", err)
	}
	snapshot := waitForLCAgentIdleSnapshot(t, session, notify)
	if snapshot.Model != "gpt-5.5" || snapshot.ModelProvider != "openai" {
		t.Fatalf("snapshot model/provider = %q/%q, want quality route", snapshot.Model, snapshot.ModelProvider)
	}
	argsBytes, err := os.ReadFile(argsPath)
	if err != nil {
		t.Fatalf("read fake args: %v", err)
	}
	args := strings.Split(strings.TrimSpace(string(argsBytes)), "\n")
	for _, want := range []string{"--route-preset", "quality", "--request-timeout", "37m0s", "--admin-write", "--env-file", "/tmp/test.env", "--web-search-url", "http://127.0.0.1:8888"} {
		if !lcagentTestStringSliceContains(args, want) {
			t.Fatalf("args missing %q: %#v", want, args)
		}
	}
	for _, blocked := range []string{"--provider", "--model", "--auto", "--tool-profile", "--context-profile", "--reasoning-effort"} {
		if lcagentTestStringSliceContains(args, blocked) {
			t.Fatalf("route preset should own %s unless review/override is active: %#v", blocked, args)
		}
	}
	envBytes, err := os.ReadFile(envCapturePath)
	if err != nil {
		t.Fatalf("read captured env: %v", err)
	}
	if got := strings.TrimSpace(string(envBytes)); got != "saved-openai-key" {
		t.Fatalf("OPENAI_API_KEY passed to route preset = %q, want saved settings key", got)
	}
}

func TestLCAgentSessionPassesXiaomiBaseURLForDirectRoute(t *testing.T) {
	root := t.TempDir()
	argsPath := filepath.Join(t.TempDir(), "args.txt")
	envCapturePath := filepath.Join(t.TempDir(), "env.txt")
	exe := filepath.Join(t.TempDir(), "fake-lcagent")
	script := `#!/bin/sh
{
  for arg in "$@"; do
    printf '%s\n' "$arg"
  done
} > "$LCAGENT_ARGS_FILE"
{
  printf 'key=%s\n' "$XIAOMI_API_KEY"
  printf 'base=%s\n' "$XIAOMI_BASE_URL"
} > "$LCAGENT_ENV_CAPTURE_FILE"
printf '%s\n' '{"type":"session_meta","id":"lca_xiaomi_route_session","cwd":"/tmp/demo"}'
printf '%s\n' '{"type":"turn_complete","summary":"xiaomi route run"}'
`
	if err := os.WriteFile(exe, []byte(script), 0o700); err != nil {
		t.Fatalf("write fake lcagent: %v", err)
	}
	t.Setenv("LCAGENT_ARGS_FILE", argsPath)
	t.Setenv("LCAGENT_ENV_CAPTURE_FILE", envCapturePath)
	t.Setenv("XIAOMI_API_KEY", "process-xiaomi-key")
	t.Setenv("XIAOMI_BASE_URL", "https://api.xiaomimimo.com/v1")

	notify := make(chan struct{}, 20)
	session, err := newLCAgentSession(LaunchRequest{
		Provider:              ProviderLCAgent,
		ProjectPath:           root,
		AppDataDir:            t.TempDir(),
		LCAgentPath:           exe,
		LCAgentXiaomiAPIKey:   "saved-xiaomi-key",
		LCAgentXiaomiBaseURL:  "https://token-plan-sgp.xiaomimimo.com/v1",
		LCAgentRoutePreset:    "mimo-2.5-pro-low",
		LCAgentProvider:       "openrouter",
		LCAgentRequestTimeout: 10 * time.Minute,
		Prompt:                "use the xiaomi route",
	}, func() {
		select {
		case notify <- struct{}{}:
		default:
		}
	})
	if err != nil {
		t.Fatalf("newLCAgentSession() error = %v", err)
	}
	snapshot := waitForLCAgentIdleSnapshot(t, session, notify)
	if snapshot.Model != "mimo-v2.5-pro" || snapshot.ModelProvider != "xiaomi" {
		t.Fatalf("snapshot model/provider = %q/%q, want xiaomi route", snapshot.Model, snapshot.ModelProvider)
	}
	argsBytes, err := os.ReadFile(argsPath)
	if err != nil {
		t.Fatalf("read fake args: %v", err)
	}
	args := strings.Split(strings.TrimSpace(string(argsBytes)), "\n")
	for _, want := range []string{"--route-preset", "mimo-2.5-pro-low", "use the xiaomi route"} {
		if !lcagentTestStringSliceContains(args, want) {
			t.Fatalf("args missing %q: %#v", want, args)
		}
	}
	envBytes, err := os.ReadFile(envCapturePath)
	if err != nil {
		t.Fatalf("read captured env: %v", err)
	}
	envText := string(envBytes)
	for _, want := range []string{"key=saved-xiaomi-key", "base=https://token-plan-sgp.xiaomimimo.com/v1"} {
		if !strings.Contains(envText, want) {
			t.Fatalf("captured env missing %q:\n%s", want, envText)
		}
	}
}

func TestCheckLCAgentProviderAccessUsesXiaomiBaseURL(t *testing.T) {
	var sawModels bool
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/models" {
			t.Fatalf("path = %q, want /models", r.URL.Path)
		}
		sawModels = true
		if got := r.Header.Get("api-key"); got != "saved-xiaomi-key" {
			t.Fatalf("api-key = %q, want saved-xiaomi-key", got)
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"data":[{"id":"mimo-v2.5-pro","name":"MiMo"}]}`))
	}))
	defer server.Close()

	err := CheckLCAgentProviderAccess(context.Background(), LaunchRequest{
		Provider:             ProviderLCAgent,
		ProjectPath:          t.TempDir(),
		LCAgentRoutePreset:   "mimo-2.5-pro-low",
		LCAgentXiaomiAPIKey:  "saved-xiaomi-key",
		LCAgentXiaomiBaseURL: server.URL,
	})
	if err != nil {
		t.Fatalf("CheckLCAgentProviderAccess() error = %v", err)
	}
	if !sawModels {
		t.Fatalf("provider access check did not call Xiaomi model list")
	}
}

func TestLCAgentSessionPermissionOverrideKeepsRoutePresetAndAddsAuto(t *testing.T) {
	root := t.TempDir()
	argsPath := filepath.Join(t.TempDir(), "args.txt")
	exe := filepath.Join(t.TempDir(), "fake-lcagent")
	script := `#!/bin/sh
{
  for arg in "$@"; do
    printf '%s\n' "$arg"
  done
} > "$LCAGENT_ARGS_FILE"
printf '%s\n' '{"type":"turn_complete","summary":"route preset run"}'
`
	if err := os.WriteFile(exe, []byte(script), 0o700); err != nil {
		t.Fatalf("write fake lcagent: %v", err)
	}
	t.Setenv("LCAGENT_ARGS_FILE", argsPath)

	notify := make(chan struct{}, 20)
	session, err := newLCAgentSession(LaunchRequest{
		Provider:           ProviderLCAgent,
		ProjectPath:        root,
		AppDataDir:         t.TempDir(),
		LCAgentPath:        exe,
		LCAgentRoutePreset: "quality",
	}, func() {
		select {
		case notify <- struct{}{}:
		default:
		}
	})
	if err != nil {
		t.Fatalf("newLCAgentSession() error = %v", err)
	}
	lcSession := session.(*lcagentSession)
	if err := lcSession.SetPermissionLevel("medium"); err != nil {
		t.Fatalf("SetPermissionLevel() error = %v", err)
	}
	if err := lcSession.Submit("run with medium permissions"); err != nil {
		t.Fatalf("Submit() error = %v", err)
	}
	waitForLCAgentIdleSnapshot(t, session, notify)
	argsBytes, err := os.ReadFile(argsPath)
	if err != nil {
		t.Fatalf("read fake args: %v", err)
	}
	args := strings.Split(strings.TrimSpace(string(argsBytes)), "\n")
	for _, want := range []string{"--route-preset", "quality", "--auto", "medium"} {
		if !lcagentTestStringSliceContains(args, want) {
			t.Fatalf("args missing %q: %#v", want, args)
		}
	}
}

func TestLCAgentSessionShowPermissionsNotifiesAndAppendsTranscript(t *testing.T) {
	notify := make(chan struct{}, 2)
	session, err := newLCAgentSession(LaunchRequest{
		Provider:    ProviderLCAgent,
		ProjectPath: t.TempDir(),
		AppDataDir:  t.TempDir(),
	}, func() {
		select {
		case notify <- struct{}{}:
		default:
		}
	})
	if err != nil {
		t.Fatalf("newLCAgentSession() error = %v", err)
	}
	if err := session.(*lcagentSession).ShowPermissions(); err != nil {
		t.Fatalf("ShowPermissions() error = %v", err)
	}
	select {
	case <-notify:
	case <-time.After(time.Second):
		t.Fatalf("ShowPermissions() did not notify listeners")
	}
	snapshot := session.Snapshot()
	if len(snapshot.Entries) == 0 || !strings.Contains(snapshot.Entries[len(snapshot.Entries)-1].Text, "LCAgent permissions") {
		t.Fatalf("ShowPermissions() did not append permissions transcript entry: %#v", snapshot.Entries)
	}
}

func TestLCAgentSessionContinuesLoadedReplayWithContinueFromArg(t *testing.T) {
	root := t.TempDir()
	dataDir := t.TempDir()
	argsPath := filepath.Join(t.TempDir(), "args.txt")
	sessionID := "lca_replay_continue"
	started := time.Date(2026, 5, 9, 11, 0, 0, 0, time.UTC)
	writeLCAgentReplayArtifact(t, dataDir, started, sessionID, []map[string]any{
		{"type": "session_meta", "id": sessionID, "cwd": root, "started_at": started.Format(time.RFC3339Nano), "model": "deepseek/replay-model"},
		{"type": "assistant_message", "session_id": sessionID, "timestamp": started.Add(time.Second).Format(time.RFC3339Nano), "message": "loaded answer"},
		{"type": "turn_complete", "session_id": sessionID, "timestamp": started.Add(2 * time.Second).Format(time.RFC3339Nano), "summary": "loaded answer"},
	})

	exe := filepath.Join(t.TempDir(), "fake-lcagent")
	script := `#!/bin/sh
{
  for arg in "$@"; do
    printf '%s\n' "$arg"
  done
} > "$LCAGENT_ARGS_FILE"
	printf '%s\n' '{"type":"session_meta","id":"lca_new_after_resume","thread_id":"lca_replay_continue","cwd":"/tmp/demo"}'
printf '%s\n' '{"type":"continuation","parent_session_id":"lca_replay_continue","chain_depth":1,"continuation_reason":"continue_from","handoff_source":"turn_complete","pending_status":"reported","pending_files":["README.md"],"parent_summary":"source lca_replay_continue; summary: loaded answer"}'
printf '%s\n' '{"type":"resume_context","source_session_id":"lca_replay_continue","summary":"source lca_replay_continue; summary: loaded answer"}'
printf '%s\n' '{"type":"assistant_message","message":"continued answer"}'
printf '%s\n' '{"type":"turn_complete"}'
`
	if err := os.WriteFile(exe, []byte(script), 0o700); err != nil {
		t.Fatalf("write fake lcagent: %v", err)
	}
	t.Setenv("LCAGENT_ARGS_FILE", argsPath)

	notify := make(chan struct{}, 20)
	session, err := newLCAgentSession(LaunchRequest{
		Provider:    ProviderLCAgent,
		ProjectPath: root,
		AppDataDir:  dataDir,
		LCAgentPath: exe,
		ResumeID:    sessionID,
	}, func() {
		select {
		case notify <- struct{}{}:
		default:
		}
	})
	if err != nil {
		t.Fatalf("newLCAgentSession() error = %v", err)
	}
	if err := session.Submit("continue the replay"); err != nil {
		t.Fatalf("Submit() error = %v", err)
	}

	snapshot := waitForLCAgentIdleSnapshot(t, session, notify)
	snapshot = waitForLCAgentTranscript(t, session, "continued answer")
	if snapshot.ThreadID != sessionID {
		t.Fatalf("ThreadID = %q, want stable thread id", snapshot.ThreadID)
	}
	for _, want := range []string{
		"Starting a continuing LCAgent run from thread " + sessionID,
		"Continuing LCAgent from " + sessionID,
		"Loaded summarized LCAgent context from " + sessionID,
		"continued answer",
	} {
		if !strings.Contains(snapshot.Transcript, want) {
			t.Fatalf("transcript missing %q:\n%s", want, snapshot.Transcript)
		}
	}
	argsBytes, err := os.ReadFile(argsPath)
	if err != nil {
		t.Fatalf("read fake args: %v", err)
	}
	args := strings.Split(strings.TrimSpace(string(argsBytes)), "\n")
	for _, want := range []string{"--continue-from", sessionID, "continue the replay"} {
		if !lcagentTestStringSliceContains(args, want) {
			t.Fatalf("args missing %q: %#v", want, args)
		}
	}
}

func TestLCAgentSessionReviewStartsReadOnlyCurrentDiffRun(t *testing.T) {
	root := t.TempDir()
	dataDir := t.TempDir()
	initLCAgentReviewGitRepo(t, root)
	if err := os.WriteFile(filepath.Join(root, "README.md"), []byte("# Demo\n\nchanged\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	argsPath := filepath.Join(t.TempDir(), "args.txt")
	exe := filepath.Join(t.TempDir(), "fake-lcagent")
	script := `#!/bin/sh
{
  for arg in "$@"; do
    printf '%s\n' "$arg"
  done
} > "$LCAGENT_ARGS_FILE"
printf '%s\n' '{"type":"session_meta","id":"lca_review_session","cwd":"/tmp/demo"}'
printf '%s\n' '{"type":"assistant_message","message":"reviewed current diff"}'
printf '%s\n' '{"type":"turn_complete","summary":"reviewed current diff","files_changed":[],"verification":["reviewed provided diff"]}'
`
	if err := os.WriteFile(exe, []byte(script), 0o700); err != nil {
		t.Fatalf("write fake lcagent: %v", err)
	}
	t.Setenv("LCAGENT_ARGS_FILE", argsPath)

	notify := make(chan struct{}, 20)
	session, err := newLCAgentSession(LaunchRequest{
		Provider:    ProviderLCAgent,
		ProjectPath: root,
		AppDataDir:  dataDir,
		LCAgentPath: exe,
		LCAgentAuto: "medium",
	}, func() {
		select {
		case notify <- struct{}{}:
		default:
		}
	})
	if err != nil {
		t.Fatalf("newLCAgentSession() error = %v", err)
	}
	lcSession := session.(*lcagentSession)
	lcSession.mu.Lock()
	lcSession.threadID = "previous-session-should-not-be-used"
	lcSession.mu.Unlock()
	if err := session.Review(); err != nil {
		t.Fatalf("Review() error = %v", err)
	}
	snapshot := waitForLCAgentIdleSnapshot(t, session, notify)
	snapshot = waitForLCAgentTranscript(t, session, "reviewed current diff")
	if snapshot.ThreadID != "lca_review_session" {
		t.Fatalf("ThreadID = %q, want review session", snapshot.ThreadID)
	}
	if !strings.Contains(snapshot.Transcript, "/review current uncommitted changes") ||
		!strings.Contains(snapshot.Transcript, "reviewed current diff") {
		t.Fatalf("transcript missing review prompt/answer:\n%s", snapshot.Transcript)
	}
	argsBytes, err := os.ReadFile(argsPath)
	if err != nil {
		t.Fatalf("read fake args: %v", err)
	}
	args := strings.Split(strings.TrimSpace(string(argsBytes)), "\n")
	for _, want := range []string{"--auto", "off", "Review the current uncommitted changes", "Git diff HEAD", "- Do not edit files."} {
		if !lcagentTestStringSliceContains(args, want) && !strings.Contains(string(argsBytes), want) {
			t.Fatalf("args missing %q: %#v", want, args)
		}
	}
	if lcagentTestStringSliceContains(args, "--resume") || strings.Contains(string(argsBytes), "previous-session-should-not-be-used") {
		t.Fatalf("review should not resume previous LCAgent session: %#v", args)
	}
}

func TestLCAgentSessionReviewReportsNoUncommittedChanges(t *testing.T) {
	root := t.TempDir()
	initLCAgentReviewGitRepo(t, root)
	session, err := newLCAgentSession(LaunchRequest{
		Provider:    ProviderLCAgent,
		ProjectPath: root,
		AppDataDir:  t.TempDir(),
	}, nil)
	if err != nil {
		t.Fatalf("newLCAgentSession() error = %v", err)
	}
	if err := session.Review(); err != nil {
		t.Fatalf("Review() error = %v", err)
	}
	snapshot := session.Snapshot()
	if snapshot.Busy {
		t.Fatalf("review should not start a run when no changes exist: %#v", snapshot)
	}
	if !strings.Contains(snapshot.Transcript, "No uncommitted changes to review.") {
		t.Fatalf("transcript missing no-change review notice:\n%s", snapshot.Transcript)
	}
}

func TestLCAgentSessionCompactWritesDurableHandoffSummary(t *testing.T) {
	root := t.TempDir()
	dataDir := t.TempDir()
	sessionID := "lca_compact_source"
	started := time.Date(2026, 5, 13, 12, 0, 0, 0, time.UTC)
	writeLCAgentReplayArtifact(t, dataDir, started, sessionID, []map[string]any{
		{"type": "session_meta", "id": sessionID, "cwd": root, "started_at": started.Format(time.RFC3339Nano)},
		{"type": "patch_diff_summary", "session_id": sessionID, "timestamp": started.Add(time.Second).Format(time.RFC3339Nano), "summary": "README.md: update +1 -0"},
		{"type": "verification_check", "session_id": sessionID, "timestamp": started.Add(2 * time.Second).Format(time.RFC3339Nano), "command": "go test ./...", "status": "passed", "success": true},
		{"type": "verification_summary", "session_id": sessionID, "timestamp": started.Add(3 * time.Second).Format(time.RFC3339Nano), "status": "verified", "message": "Verification checks passed: go test ./..."},
		{"type": "turn_complete", "session_id": sessionID, "timestamp": started.Add(4 * time.Second).Format(time.RFC3339Nano), "summary": "updated README", "files_changed": []string{"README.md"}, "verification": []string{"go test ./..."}, "verification_status": "verified"},
	})
	session, err := newLCAgentSession(LaunchRequest{
		Provider:    ProviderLCAgent,
		ProjectPath: root,
		AppDataDir:  dataDir,
	}, nil)
	if err != nil {
		t.Fatalf("newLCAgentSession() error = %v", err)
	}
	if err := session.Compact(); err != nil {
		t.Fatalf("Compact() error = %v", err)
	}
	snapshot := session.Snapshot()
	if snapshot.Status == "" || !strings.Contains(snapshot.Status, "LCAgent compact summary refreshed") {
		t.Fatalf("Status = %q, want compact notice", snapshot.Status)
	}
	for _, want := range []string{"# LCAgent Compact Summary", "updated README", "Actual Checks", "go test ./... passed", "Re-read exact workspace files before editing"} {
		if !strings.Contains(snapshot.Transcript, want) {
			t.Fatalf("transcript missing %q:\n%s", want, snapshot.Transcript)
		}
	}
	path := lcagentCompactSummaryPath(dataDir, sessionID)
	body, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read compact summary: %v", err)
	}
	for _, want := range []string{"# LCAgent Compact Summary", "Session: " + sessionID, "README.md", "Verification checks passed"} {
		if !strings.Contains(string(body), want) {
			t.Fatalf("compact summary missing %q:\n%s", want, body)
		}
	}
}

func TestLCAgentSessionCompactReportsNoSessionYet(t *testing.T) {
	session, err := newLCAgentSession(LaunchRequest{
		Provider:    ProviderLCAgent,
		ProjectPath: t.TempDir(),
		AppDataDir:  t.TempDir(),
		ForceNew:    true,
	}, nil)
	if err != nil {
		t.Fatalf("newLCAgentSession() error = %v", err)
	}
	if err := session.Compact(); err != nil {
		t.Fatalf("Compact() error = %v", err)
	}
	snapshot := session.Snapshot()
	if !strings.Contains(snapshot.Transcript, "No LCAgent session to compact yet.") {
		t.Fatalf("transcript missing compact no-session notice:\n%s", snapshot.Transcript)
	}
}

func TestLCAgentSessionReplaysLatestProjectArtifact(t *testing.T) {
	root := t.TempDir()
	otherRoot := t.TempDir()
	dataDir := t.TempDir()
	base := time.Date(2026, 5, 9, 10, 0, 0, 0, time.UTC)
	writeLCAgentReplayArtifact(t, dataDir, base, "lca_old", []map[string]any{
		{"type": "session_meta", "id": "lca_old", "cwd": root, "started_at": base.Format(time.RFC3339Nano), "model": "old-model"},
		{"type": "assistant_message", "session_id": "lca_old", "timestamp": base.Add(time.Second).Format(time.RFC3339Nano), "message": "old answer"},
	})
	writeLCAgentReplayArtifact(t, dataDir, base.Add(time.Minute), "lca_other", []map[string]any{
		{"type": "session_meta", "id": "lca_other", "cwd": otherRoot, "started_at": base.Add(time.Minute).Format(time.RFC3339Nano), "model": "other-model"},
		{"type": "assistant_message", "session_id": "lca_other", "timestamp": base.Add(time.Minute + time.Second).Format(time.RFC3339Nano), "message": "other answer"},
	})
	writeLCAgentReplayArtifact(t, dataDir, base.Add(2*time.Minute), "lca_new", []map[string]any{
		{"type": "session_meta", "id": "lca_new", "cwd": root, "started_at": base.Add(2 * time.Minute).Format(time.RFC3339Nano), "model": "new-model"},
		{"type": "assistant_message", "session_id": "lca_new", "timestamp": base.Add(2*time.Minute + time.Second).Format(time.RFC3339Nano), "message": "new answer"},
	})

	session, err := newLCAgentSession(LaunchRequest{
		Provider:    ProviderLCAgent,
		ProjectPath: root,
		AppDataDir:  dataDir,
	}, nil)
	if err != nil {
		t.Fatalf("newLCAgentSession() error = %v", err)
	}

	snapshot := session.Snapshot()
	if snapshot.ThreadID != "lca_new" {
		t.Fatalf("ThreadID = %q, want latest project session", snapshot.ThreadID)
	}
	if !strings.Contains(snapshot.Transcript, "new answer") {
		t.Fatalf("transcript missing latest answer:\n%s", snapshot.Transcript)
	}
	if strings.Contains(snapshot.Transcript, "old answer") || strings.Contains(snapshot.Transcript, "other answer") {
		t.Fatalf("transcript should only replay latest matching project session:\n%s", snapshot.Transcript)
	}
}

func initLCAgentReviewGitRepo(t *testing.T, root string) {
	t.Helper()
	if err := os.WriteFile(filepath.Join(root, "README.md"), []byte("# Demo\n\ninitial\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	commands := [][]string{
		{"git", "init", "-q"},
		{"git", "add", "README.md"},
		{"git", "-c", "user.name=LCAgent Review", "-c", "user.email=lcagent-review@example.invalid", "commit", "-qm", "seed"},
	}
	for _, argv := range commands {
		cmd := exec.Command(argv[0], argv[1:]...)
		cmd.Dir = root
		if output, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("%s failed: %v\n%s", strings.Join(argv, " "), err, output)
		}
	}
}

func TestParseLCAgentTraceFileHarvestsFinalOutcome(t *testing.T) {
	root := t.TempDir()
	dataDir := t.TempDir()
	sessionID := "lca_trace_harvest"
	started := time.Date(2026, 5, 12, 10, 0, 0, 0, time.UTC)
	path := writeLCAgentReplayArtifact(t, dataDir, started, sessionID, []map[string]any{
		{"type": "session_meta", "id": sessionID, "cwd": root, "started_at": started.Format(time.RFC3339Nano), "parent_session_id": "lca_previous_trace", "root_session_id": "lca_root_trace", "continuation_depth": 2, "continuation_reason": "continue_from", "handoff_source": "turn_complete"},
		{"type": "continuation", "session_id": sessionID, "timestamp": started.Add(200 * time.Millisecond).Format(time.RFC3339Nano), "parent_session_id": "lca_previous_trace", "root_session_id": "lca_root_trace", "chain_depth": 2, "continuation_reason": "continue_from", "handoff_source": "turn_complete", "parent_path": filepath.Join(dataDir, "previous.jsonl"), "parent_cwd": root, "parent_summary": "source lca_previous_trace; summary: earlier work", "parent_last_activity": started.Add(-time.Hour).Format(time.RFC3339Nano), "pending_files": []string{"README.md"}, "pending_verification": []string{"go test ./..."}, "pending_status": "reported"},
		{"type": "resume_context", "session_id": sessionID, "timestamp": started.Add(250 * time.Millisecond).Format(time.RFC3339Nano), "source_session_id": "lca_previous_trace", "source_path": filepath.Join(dataDir, "previous.jsonl"), "source_cwd": root, "summary": "source lca_previous_trace; summary: earlier work", "source_last_activity": started.Add(-time.Hour).Format(time.RFC3339Nano)},
		{"type": "model_response", "session_id": sessionID, "timestamp": started.Add(500 * time.Millisecond).Format(time.RFC3339Nano), "model": "deepseek/test-model", "usage_summary": map[string]any{"input_tokens": 120, "output_tokens": 30, "total_tokens": 150, "cached_input_tokens": 40, "estimated_cost_usd": 0.0012}},
		{"type": "permission_denied", "session_id": sessionID, "timestamp": started.Add(time.Second).Format(time.RFC3339Nano), "tool": "run_command", "reason": "shell denied"},
		{"type": "patch_diff_summary", "session_id": sessionID, "timestamp": started.Add(2 * time.Second).Format(time.RFC3339Nano), "summary": "README.md +1 -0"},
		{"type": "patch_feedback", "session_id": sessionID, "timestamp": started.Add(2500 * time.Millisecond).Format(time.RFC3339Nano), "stage": "apply", "path": "README.md", "message": "Patch feedback: README.md failed during apply."},
		{"type": "verification_check", "session_id": sessionID, "timestamp": started.Add(3 * time.Second).Format(time.RFC3339Nano), "command": "go test ./...", "status": "passed", "success": true},
		{"type": "verification_feedback", "session_id": sessionID, "timestamp": started.Add(3200 * time.Millisecond).Format(time.RFC3339Nano), "status": "failed", "command": "go test ./...", "message": "Verification feedback: go test ./... failed."},
		{"type": "repair_feedback_suppressed", "session_id": sessionID, "timestamp": started.Add(3300 * time.Millisecond).Format(time.RFC3339Nano), "kind": "verification", "message": "Verification feedback: go test ./... failed.", "count": 2, "reason": "duplicate feedback already sent to model"},
		{"type": "verification_summary", "session_id": sessionID, "timestamp": started.Add(3500 * time.Millisecond).Format(time.RFC3339Nano), "status": "verified", "message": "Verification checks passed: go test ./..."},
		{"type": "turn_complete", "session_id": sessionID, "timestamp": started.Add(4 * time.Second).Format(time.RFC3339Nano), "summary": "updated docs", "files_changed": []string{"README.md"}, "verification": []string{"go test ./..."}, "verification_status": "verified", "actual_checks": []map[string]any{{"command": "go test ./...", "status": "passed", "success": true}}},
	})

	trace, err := ParseLCAgentTraceFile(path)
	if err != nil {
		t.Fatalf("ParseLCAgentTraceFile() error = %v", err)
	}
	if !trace.Verified() || trace.SessionID != sessionID || trace.ProjectPath != root {
		t.Fatalf("trace = %#v, want verified session for project", trace)
	}
	if trace.Summary != "updated docs" || trace.VerificationStatus != "verified" {
		t.Fatalf("trace summary/status = %q/%q", trace.Summary, trace.VerificationStatus)
	}
	if trace.ResumeSourceSessionID != "lca_previous_trace" || trace.ResumeSourcePath == "" || trace.ResumeSourceSummary == "" || trace.ResumeSourceLastAt.IsZero() {
		t.Fatalf("trace resume source = id:%q path:%q summary:%q last:%v, want continuation metadata", trace.ResumeSourceSessionID, trace.ResumeSourcePath, trace.ResumeSourceSummary, trace.ResumeSourceLastAt)
	}
	if trace.ContinuationRootSessionID != "lca_root_trace" || trace.ContinuationChainDepth != 2 || trace.ContinuationReason != "continue_from" || trace.ContinuationHandoffSource != "turn_complete" {
		t.Fatalf("trace continuation = root:%q depth:%d reason:%q handoff:%q, want explicit chain metadata", trace.ContinuationRootSessionID, trace.ContinuationChainDepth, trace.ContinuationReason, trace.ContinuationHandoffSource)
	}
	if len(trace.PendingFiles) != 1 || trace.PendingFiles[0] != "README.md" || trace.PendingStatus != "reported" || len(trace.PendingVerification) != 1 {
		t.Fatalf("trace pending continuation state = files:%#v status:%q verification:%#v", trace.PendingFiles, trace.PendingStatus, trace.PendingVerification)
	}
	if len(trace.FilesChanged) != 1 || trace.FilesChanged[0] != "README.md" || len(trace.Verification) != 1 || len(trace.ActualChecks) != 1 {
		t.Fatalf("trace files/verification/checks = %#v/%#v/%#v", trace.FilesChanged, trace.Verification, trace.ActualChecks)
	}
	if len(trace.PermissionDenials) != 1 || len(trace.PatchDiffSummaries) != 1 || len(trace.PatchFeedback) != 1 {
		t.Fatalf("trace denials/patches/feedback = %#v/%#v/%#v", trace.PermissionDenials, trace.PatchDiffSummaries, trace.PatchFeedback)
	}
	if len(trace.VerificationFeedback) != 1 || len(trace.RepairFeedbackSuppressed) != 1 || trace.ModelResponses != 1 || trace.TokenUsage.TotalTokens != 150 || trace.TokenUsage.CachedInputTokens != 40 {
		t.Fatalf("trace feedback/model usage = %#v/%#v/%d/%+v, want feedback suppression and token usage", trace.VerificationFeedback, trace.RepairFeedbackSuppressed, trace.ModelResponses, trace.TokenUsage)
	}
	if trace.TraceQuality.Score != 83 || trace.TraceQuality.Grade != "good" || trace.TraceQuality.RepairEvents != 4 {
		t.Fatalf("trace quality = %#v, want derived score and repair pressure", trace.TraceQuality)
	}
	loaded, err := LoadLCAgentTrace(dataDir, sessionID, root)
	if err != nil {
		t.Fatalf("LoadLCAgentTrace() error = %v", err)
	}
	if loaded.ArtifactPath != path || !strings.Contains(loaded.CompactSummary(), "continued from lca_previous_trace") || !strings.Contains(loaded.CompactSummary(), "continuation depth 2") || !strings.Contains(loaded.CompactSummary(), "verification verified") || !strings.Contains(loaded.CompactSummary(), "1 verification check") {
		t.Fatalf("loaded trace = %#v, want artifact path and compact summary", loaded)
	}
	for _, want := range []string{"trace quality: 83/good", "repair events: 4", "continuation: lca_previous_trace", "continuation depth: 2", "handoff source: turn_complete", "pending files: README.md", "actual checks: go test ./... passed", "patch feedback: 1", "duplicate repair feedback suppressed: 1", "tokens: 150", "cached: 40"} {
		if !strings.Contains(loaded.TraceQualitySummary(), want) {
			t.Fatalf("trace quality missing %q:\n%s", want, loaded.TraceQualitySummary())
		}
	}
}

func TestParseLCAgentTraceFileCorrectsLegacyVerificationStatus(t *testing.T) {
	root := t.TempDir()
	dataDir := t.TempDir()
	sessionID := "lca_legacy_trace_status"
	started := time.Date(2026, 5, 22, 12, 0, 0, 0, time.UTC)
	path := writeLCAgentReplayArtifact(t, dataDir, started, sessionID, []map[string]any{
		{"type": "session_meta", "id": sessionID, "cwd": root, "started_at": started.Format(time.RFC3339Nano)},
		{
			"type":                "turn_complete",
			"session_id":          sessionID,
			"timestamp":           started.Add(time.Second).Format(time.RFC3339Nano),
			"summary":             "old final summary claimed checks passed",
			"files_changed":       []string{"frontend/src/pages/index.tsx"},
			"verification":        []string{"pnpm run lint passed", "pnpm run build passed"},
			"verification_status": "failed",
			"actual_checks": []map[string]any{
				{"command": "pnpm run lint", "status": "failed", "success": false},
				{"command": "pnpm run build", "status": "failed", "success": false},
				{"command": "cd " + filepath.Join(root, "frontend") + " && pwd && ls package.json && pnpm run lint", "status": "passed", "success": true},
				{"command": "cd " + filepath.Join(root, "frontend") + " && pnpm run build", "status": "passed", "success": true},
			},
		},
	})
	trace, err := ParseLCAgentTraceFile(path)
	if err != nil {
		t.Fatalf("ParseLCAgentTraceFile() error = %v", err)
	}
	if trace.VerificationStatus != "verified" {
		t.Fatalf("VerificationStatus = %q, want verified", trace.VerificationStatus)
	}
	if !trace.Verified() {
		t.Fatalf("Verified() = false, want true")
	}
}

func TestParseLCAgentTraceFileIncludesProviderQualitySignals(t *testing.T) {
	root := t.TempDir()
	dataDir := t.TempDir()
	sessionID := "lca_provider_retry"
	started := time.Date(2026, 5, 12, 11, 0, 0, 0, time.UTC)
	path := writeLCAgentReplayArtifact(t, dataDir, started, sessionID, []map[string]any{
		{"type": "session_meta", "id": sessionID, "cwd": root, "started_at": started.Format(time.RFC3339Nano)},
		{"type": "provider_failure", "session_id": sessionID, "timestamp": started.Add(time.Second).Format(time.RFC3339Nano), "provider": "openrouter", "kind": "rate_limited", "message": "HTTP 429: slow down", "retryable": true, "retrying": true},
		{"type": "provider_retry", "session_id": sessionID, "timestamp": started.Add(1500 * time.Millisecond).Format(time.RFC3339Nano), "provider": "openrouter", "attempt": 2, "delay_ms": 250},
		{"type": "provider_retry_succeeded", "session_id": sessionID, "timestamp": started.Add(2 * time.Second).Format(time.RFC3339Nano), "provider": "openrouter", "attempt": 2},
		{"type": "verification_summary", "session_id": sessionID, "timestamp": started.Add(2500 * time.Millisecond).Format(time.RFC3339Nano), "status": "verified"},
		{"type": "turn_complete", "session_id": sessionID, "timestamp": started.Add(3 * time.Second).Format(time.RFC3339Nano), "summary": "done", "verification_status": "verified"},
	})

	trace, err := ParseLCAgentTraceFile(path)
	if err != nil {
		t.Fatalf("ParseLCAgentTraceFile() error = %v", err)
	}
	if trace.TraceQuality.ProviderFailures != 1 || trace.TraceQuality.ProviderRetries != 1 {
		t.Fatalf("trace provider quality = %#v, want provider failure and retry", trace.TraceQuality)
	}
	for _, want := range []string{"provider failures: 1", "provider retries: 1"} {
		if !strings.Contains(trace.TraceQualitySummary(), want) {
			t.Fatalf("trace quality summary missing %q:\n%s", want, trace.TraceQualitySummary())
		}
	}
}

func TestLCAgentProviderFailureTextIncludesActionableHint(t *testing.T) {
	event := map[string]json.RawMessage{
		"provider":       json.RawMessage(`"openrouter"`),
		"kind":           json.RawMessage(`"quota"`),
		"message":        json.RawMessage(`"insufficient credits"`),
		"attempt":        json.RawMessage(`1`),
		"retrying":       json.RawMessage(`false`),
		"retry_delay_ms": json.RawMessage(`250`),
	}
	got := lcagentProviderFailureText(event)
	for _, want := range []string{"LCAgent openrouter failure: quota", "insufficient credits", "check provider credits/quota"} {
		if !strings.Contains(got, want) {
			t.Fatalf("provider failure text missing %q: %q", want, got)
		}
	}

	event["kind"] = json.RawMessage(`"rate_limited"`)
	event["retrying"] = json.RawMessage(`true`)
	got = lcagentProviderFailureText(event)
	for _, want := range []string{"retrying", "retry delay 250ms", "waiting for the provider rate limit"} {
		if !strings.Contains(got, want) {
			t.Fatalf("retrying provider failure text missing %q: %q", want, got)
		}
	}
}

func waitForLCAgentIdleSnapshot(t *testing.T, session Session, notify <-chan struct{}) Snapshot {
	t.Helper()
	deadline := time.After(5 * time.Second)
	tick := time.NewTicker(10 * time.Millisecond)
	defer tick.Stop()
	for {
		snapshot := session.Snapshot()
		if snapshot.Started && !snapshot.Busy {
			return snapshot
		}
		select {
		case <-notify:
		case <-tick.C:
		case <-deadline:
			t.Fatalf("timed out waiting for lcagent session to finish; snapshot=%#v", snapshot)
		}
	}
}

func waitForLCAgentPendingApproval(t *testing.T, session Session, notify <-chan struct{}, commandContains string) Snapshot {
	t.Helper()
	deadline := time.After(5 * time.Second)
	tick := time.NewTicker(10 * time.Millisecond)
	defer tick.Stop()
	for {
		snapshot := session.Snapshot()
		if snapshot.PendingApproval != nil && strings.Contains(snapshot.PendingApproval.Command, commandContains) {
			return snapshot
		}
		select {
		case <-notify:
		case <-tick.C:
		case <-deadline:
			t.Fatalf("timed out waiting for lcagent approval %q; snapshot=%#v", commandContains, snapshot)
		}
	}
}

func waitForLCAgentTranscript(t *testing.T, session Session, want string) Snapshot {
	t.Helper()
	deadline := time.After(5 * time.Second)
	tick := time.NewTicker(10 * time.Millisecond)
	defer tick.Stop()
	for {
		snapshot := session.Snapshot()
		if strings.Contains(snapshot.Transcript, want) {
			return snapshot
		}
		select {
		case <-tick.C:
		case <-deadline:
			t.Fatalf("timed out waiting for lcagent transcript %q; snapshot=%#v", want, snapshot)
		}
	}
}

func writeLCAgentReplayArtifact(t *testing.T, dataDir string, started time.Time, sessionID string, events []map[string]any) string {
	t.Helper()
	path := filepath.Join(dataDir, "lcagent", "sessions", started.Format("2006"), started.Format("01"), started.Format("02"), sessionID+".jsonl")
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatalf("mkdir replay artifact dir: %v", err)
	}
	file, err := os.Create(path)
	if err != nil {
		t.Fatalf("create replay artifact: %v", err)
	}
	defer file.Close()
	encoder := json.NewEncoder(file)
	for _, event := range events {
		if err := encoder.Encode(event); err != nil {
			t.Fatalf("encode replay event: %v", err)
		}
	}
	return path
}

func lcagentTestStringSliceContains(values []string, want string) bool {
	for _, value := range values {
		if value == want {
			return true
		}
	}
	return false
}

func TestLCAgentResolvedUtilityModelUsesXiaomiUtilityDefault(t *testing.T) {
	got := lcagentResolvedUtilityModel("mimo-2.5-pro-low", "xiaomi", "mimo-v2.5-pro", "main", "")
	if got != "mimo-v2.5" {
		t.Fatalf("lcagentResolvedUtilityModel() = %q, want mimo-v2.5", got)
	}
}
