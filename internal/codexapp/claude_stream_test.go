package codexapp

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"
)

func TestClaudeManagedStreamProcess(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("shell helper requires Unix")
	}
	dir := t.TempDir()
	executable, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	t.Setenv("LCROOM_CLAUDE_STREAM_HELPER", executable)
	t.Setenv("PATH", dir+string(os.PathListSeparator)+os.Getenv("PATH"))
	script := "#!/bin/sh\nexec \"$LCROOM_CLAUDE_STREAM_HELPER\" -test.run='^TestClaudeManagedStreamProcessHelper$'\n"
	if err := os.WriteFile(filepath.Join(dir, "claude"), []byte(script), 0700); err != nil {
		t.Fatal(err)
	}
	s, _ := managedClaudeTestSession()
	s.cmd, s.stdin, s.busy, s.pendingSubmissions, s.messageDeliveries = nil, nil, false, 0, nil
	s.projectPath, s.claudeHome, s.safetySettings = dir, dir, `{}`
	s.checkAuthentication = func(context.Context) error { return nil }
	t.Cleanup(func() { _ = s.Close(); s.WaitClosed(time.Second) })
	if err := s.Submit("Start worker"); err != nil {
		t.Fatal(err)
	}
	wait := func(check func(Snapshot) bool) Snapshot {
		t.Helper()
		deadline := time.Now().Add(8 * time.Second)
		for time.Now().Before(deadline) {
			snapshot := s.StateSnapshot()
			if check(snapshot) {
				return snapshot
			}
			time.Sleep(10 * time.Millisecond)
		}
		t.Fatalf("stream did not reach expected state: %#v", s.StateSnapshot())
		return Snapshot{}
	}
	wait(func(snapshot Snapshot) bool {
		return snapshot.ParentStatus == "Parent available · background work running"
	})
	if err := s.Submit("Steer worker"); err != nil {
		t.Fatal(err)
	}
	snapshot := wait(func(snapshot Snapshot) bool { return !snapshot.Busy })
	if snapshot.LastError != "" || len(snapshot.BackgroundTasks) != 0 || len(snapshot.MessageDeliveries) != 2 {
		t.Fatalf("bad settled stream: %#v", snapshot)
	}
	for _, receipt := range snapshot.MessageDeliveries {
		if receipt.State != "completed" {
			t.Fatalf("lost input: %#v", receipt)
		}
	}
}

func TestClaudeManagedStreamProcessHelper(t *testing.T) {
	if os.Getenv("LCROOM_CLAUDE_STREAM_HELPER") == "" {
		t.Skip("subprocess helper")
	}
	fail := func(message string) { fmt.Fprintln(os.Stderr, message); os.Exit(9) }
	if os.Getenv(claudeDisableBackgroundTasksEnv) != "0" || os.Getenv("CLAUDE_CODE_FORK_SUBAGENT") != "1" {
		fail("delegation remained foreground")
	}
	emit := func(message string) { fmt.Fprintln(os.Stdout, message) }
	scanner := bufio.NewScanner(os.Stdin)
	initialized, prompts := false, 0
	for scanner.Scan() {
		var env claudeStreamEnvelope
		if json.Unmarshal(scanner.Bytes(), &env) != nil {
			fail("invalid stream input")
		}
		if env.Type == "control_request" {
			initialized = true
			emit(`{"type":"control_response","response":{"request_id":"lcr-stream-initialize","subtype":"success","response":{"session_state":"idle"}}}`)
			continue
		}
		if !initialized || env.Type != "user" || env.UUID == "" {
			fail("input arrived before initialize or without identity")
		}
		prompts++
		emit(`{"type":"system","subtype":"init","session_id":"test-stream"}`)
		emit(`{"type":"system","subtype":"session_state_changed","state":"running"}`)
		emit(`{"type":"command_lifecycle","command_uuid":"` + env.UUID + `","state":"started"}`)
		if prompts == 1 {
			emit(`{"type":"system","subtype":"task_started","task_id":"worker","task_type":"local_agent"}`)
		}
		emit(`{"type":"assistant","message":{"id":"reply-` + env.UUID + `","content":[{"type":"text","text":"Acknowledged"}],"stop_reason":"end_turn"}}`)
		emit(`{"type":"result","subtype":"success"}`)
		emit(`{"type":"command_lifecycle","command_uuid":"` + env.UUID + `","state":"completed"}`)
		emit(`{"type":"system","subtype":"session_state_changed","state":"idle"}`)
		if prompts == 2 {
			emit(`{"type":"system","subtype":"task_updated","task_id":"worker","patch":{"status":"completed"}}`)
			emit(`{"type":"system","subtype":"session_state_changed","state":"running"}`)
			emit(`{"type":"result","subtype":"success","origin":{"kind":"task-notification"}}`)
			emit(`{"type":"system","subtype":"session_state_changed","state":"idle"}`)
		}
	}
	if prompts != 2 {
		fail("stdin closed while worker needed ownership")
	}
	os.Exit(0)
}

func managedClaudeTestSession() (*claudeCodeSession, *recordingWriteCloser) {
	stdin := &recordingWriteCloser{}
	s := &claudeCodeSession{managedStream: true, busy: true, started: true, cmd: &exec.Cmd{}, stdin: stdin,
		streamState: "running", pendingSubmissions: 1, assistantBlocks: make(map[string]map[string]struct{}),
		toolCalls: make(map[string]claudeToolCall), toolResults: make(map[string]struct{}),
		backgroundTasks: make(map[string]BackgroundTaskSnapshot), closedCh: make(chan struct{}),
	}
	s.addClaudeDeliveryLocked("first", "Start work", time.Now().Add(-time.Minute))
	s.handleClaudeStdoutLine(`{"type":"command_lifecycle","command_uuid":"first","state":"started"}`)
	return s, stdin
}

func TestClaudeManagedStreamKeepsWorkerAndCompletionTurnOwned(t *testing.T) {
	s, stdin := managedClaudeTestSession()
	// Ordering recorded from Claude Code 2.1.284: the result and idle state
	// precede task completion; another parent turn follows the task notification.
	for _, line := range []string{
		`{"type":"system","subtype":"background_tasks_changed","tasks":[{"task_id":"worker","task_type":"local_agent","description":"Device check"}]}`,
		`{"type":"system","subtype":"task_started","task_id":"worker","tool_use_id":"agent-call","task_type":"local_agent","is_backgrounded":true}`,
		`{"type":"assistant","message":{"id":"reply","content":[{"type":"text","text":"Worker started"}],"stop_reason":"end_turn"}}`,
		`{"type":"result","subtype":"success","user_message_uuid":"first"}`,
		`{"type":"command_lifecycle","command_uuid":"first","state":"completed"}`,
		`{"type":"system","subtype":"session_state_changed","state":"idle"}`,
	} {
		s.handleClaudeStdoutLine(line)
	}
	if stdin.closed || s.pendingSubmissions != 0 || len(s.backgroundTasks) != 1 || !s.busy {
		t.Fatalf("released worker ownership: closed=%v pending=%d tasks=%v busy=%v", stdin.closed, s.pendingSubmissions, s.backgroundTasks, s.busy)
	}
	if !s.stateSnapshotLocked().BackgroundInputSupported {
		t.Fatal("worker disabled parent input")
	}
	if err := s.Submit("Please steer the worker"); err != nil {
		t.Fatal(err)
	}
	var sent struct {
		UUID string `json:"uuid"`
	}
	if err := json.Unmarshal([]byte(stdin.writes[0]), &sent); err != nil || sent.UUID == "" {
		t.Fatalf("missing message identity: %v %s", err, stdin.writes[0])
	}
	s.handleClaudeStdoutLine(`{"type":"command_lifecycle","command_uuid":"` + sent.UUID + `","state":"queued"}`)
	s.handleClaudeStdoutLine(`{"type":"system","subtype":"background_tasks_changed","tasks":[]}`)
	s.handleClaudeStdoutLine(`{"type":"system","subtype":"task_updated","task_id":"worker","patch":{"status":"completed"}}`)
	s.handleClaudeStdoutLine(`{"type":"system","subtype":"task_notification","task_id":"worker","status":"completed"}`)
	if stdin.closed || len(s.backgroundTasks) != 0 {
		t.Fatal("closed on worker completion before its handback")
	}
	s.handleClaudeStdoutLine(`{"type":"system","subtype":"session_state_changed","state":"running"}`)
	s.handleClaudeStdoutLine(`{"type":"result","origin":{"kind":"task-notification"},"subtype":"success"}`)
	s.handleClaudeStdoutLine(`{"type":"system","subtype":"session_state_changed","state":"idle"}`)
	if stdin.closed || s.pendingSubmissions != 1 {
		t.Fatal("worker handback consumed queued user input")
	}
	s.handleClaudeStdoutLine(`{"type":"command_lifecycle","command_uuid":"` + sent.UUID + `","state":"started"}`)
	s.handleClaudeStdoutLine(`{"type":"result","subtype":"success"}`)
	s.handleClaudeStdoutLine(`{"type":"command_lifecycle","command_uuid":"` + sent.UUID + `","state":"completed"}`)
	if stdin.closed {
		t.Fatal("closed before provider idle")
	}
	s.handleClaudeStdoutLine(`{"type":"system","subtype":"session_state_changed","state":"idle"}`)
	if !stdin.closed || s.pendingSubmissions != 0 {
		t.Fatal("idle session did not release settled ownership")
	}
}

func TestClaudeNestedMessagesCannotCompleteOrRefreshParent(t *testing.T) {
	s, stdin := managedClaudeTestSession()
	before := s.lastParentActivityAt
	s.handleClaudeStdoutLine(`{"type":"assistant","parent_tool_use_id":"agent-call","message":{"id":"child-reply","model":"other-model","content":[{"type":"text","text":"Child done"}],"stop_reason":"end_turn"}}`)
	s.handleClaudeStdoutLine(`{"type":"command_lifecycle","parent_tool_use_id":"agent-call","command_uuid":"first","state":"completed"}`)
	if len(s.entries) != 0 || stdin.closed || s.pendingSubmissions != 1 || !s.lastParentActivityAt.Equal(before) || s.model != "" {
		t.Fatal("nested activity was mistaken for parent progress")
	}
}

func TestClaudeLateLaunchEvidenceCannotReviveCompletedTask(t *testing.T) {
	s, _ := managedClaudeTestSession()
	s.handleClaudeStdoutLine(`{"type":"system","subtype":"task_started","task_id":"worker","task_type":"local_agent"}`)
	s.handleClaudeStdoutLine(`{"type":"system","subtype":"task_updated","task_id":"worker","patch":{"status":"completed"}}`)
	s.handleClaudeStdoutLine(`{"type":"user","tool_use_result":{"isAsync":true,"status":"async_launched","agentId":"worker"}}`)
	s.handleClaudeStdoutLine(`{"type":"system","subtype":"background_tasks_changed","tasks":[{"task_id":"worker","task_type":"local_agent"}]}`)
	if len(s.backgroundTasks) != 0 {
		t.Fatal("late launch evidence revived a finished task")
	}
	s.handleClaudeStdoutLine(`{"type":"system","subtype":"task_started","task_id":"worker","task_type":"local_agent"}`)
	if len(s.backgroundTasks) != 1 {
		t.Fatal("explicit resumed worker lost ownership")
	}
}

func TestClaudeDeliveryReceiptsAreCorrelatedAndMonotonic(t *testing.T) {
	s, _ := managedClaudeTestSession()
	s.addClaudeDeliveryLocked("second", "Adjust work", time.Now())
	s.pendingSubmissions++
	for _, line := range []string{
		`{"type":"command_lifecycle","command_uuid":"unknown","state":"completed"}`,
		`{"type":"user","uuid":"second","isReplay":true,"message":{"role":"user","content":"Adjust work"}}`,
		`{"type":"command_lifecycle","command_uuid":"second","state":"queued"}`,
		`{"type":"command_lifecycle","command_uuid":"second","state":"started"}`,
		`{"type":"user","uuid":"second","isReplay":true}`,
		`{"type":"command_lifecycle","command_uuid":"second","state":"queued"}`,
	} {
		s.handleClaudeStdoutLine(line)
	}
	s.updateClaudeDeliveryLocked("second", "sent", time.Now())
	if s.messageDeliveries[1].State != "started" || s.pendingSubmissions != 2 {
		t.Fatal("receipt regressed or unrelated UUID consumed input")
	}
	s.handleClaudeStdoutLine(`{"type":"command_lifecycle","command_uuid":"second","state":"discarded"}`)
	s.handleClaudeStdoutLine(`{"type":"command_lifecycle","command_uuid":"second","state":"completed"}`)
	if s.pendingSubmissions != 1 || s.messageDeliveries[1].State != "discarded" {
		t.Fatal("terminal receipt changed or counted twice")
	}
	s.finishClaudeDeliveriesLocked(false)
	if s.messageDeliveries[0].State != "unknown" || s.messageDeliveries[1].State != "discarded" {
		t.Fatal("exit invented a completed delivery")
	}
}

func TestClaudeManagedTranscriptRefreshCannotDropLiveTasks(t *testing.T) {
	s, stdin := managedClaudeTestSession()
	s.sessionFile = filepath.Join(t.TempDir(), "session.jsonl")
	if err := os.WriteFile(s.sessionFile, []byte(`{"type":"assistant","timestamp":"2026-09-30T13:11:06Z","message":{"role":"assistant","stop_reason":"end_turn","content":[]}}`+"\n"), 0600); err != nil {
		t.Fatal(err)
	}
	s.handleClaudeStdoutLine(`{"type":"system","subtype":"task_started","task_id":"worker","task_type":"local_agent"}`)
	if err := s.loadTranscriptLocked(); err != nil {
		t.Fatal(err)
	}
	if s.finishClaudeSubmissionFromTranscriptLocked() != nil || stdin.closed || len(s.backgroundTasks) != 1 {
		t.Fatal("disk snapshot overruled live ownership")
	}
	s.finishClaudeTurn(nil, nil, nil)
	if s.backgroundTasks["worker"].Status != "unresolved" || s.messageDeliveries[0].State != "unknown" {
		t.Fatal("unexpected exit hid lost work or input")
	}
}

func TestClaudeTranscriptCannotReviveStreamVerifiedCompletedWork(t *testing.T) {
	s, _ := managedClaudeTestSession()
	s.sessionFile = filepath.Join(t.TempDir(), "session.jsonl")
	launch := `{"type":"user","toolUseResult":{"isAsync":true,"status":"async_launched","agentId":"worker"}}` + "\n"
	if err := os.WriteFile(s.sessionFile, []byte(launch), 0600); err != nil {
		t.Fatal(err)
	}
	s.handleClaudeStdoutLine(`{"type":"system","subtype":"task_started","task_id":"worker","task_type":"local_agent"}`)
	s.handleClaudeStdoutLine(`{"type":"system","subtype":"task_updated","task_id":"worker","patch":{"status":"completed"}}`)
	s.finishClaudeTurn(nil, nil, nil)
	if len(s.backgroundTasks) != 0 {
		t.Fatal("exit revived a completed task from lagging disk evidence")
	}
	s.lastFileSize = -1
	if err := s.loadTranscriptLocked(); err != nil {
		t.Fatal(err)
	}
	if len(s.backgroundTasks) != 0 {
		t.Fatal("later transcript reload revived the completed task")
	}
	resumed := fmt.Sprintf(`{"type":"user","timestamp":%q,"toolUseResult":{"isAsync":true,"status":"async_launched","agentId":"worker"}}`+"\n", time.Now().Add(time.Minute).UTC().Format(time.RFC3339Nano))
	if err := os.WriteFile(s.sessionFile, []byte(launch+resumed), 0600); err != nil {
		t.Fatal(err)
	}
	if err := s.loadTranscriptLocked(); err != nil {
		t.Fatal(err)
	}
	if len(s.backgroundTasks) != 1 {
		t.Fatal("a later external resumption was hidden by an old receipt")
	}
}

func TestClaudeStreamInitializeAcknowledgment(t *testing.T) {
	for _, failure := range []bool{false, true} {
		s := &claudeCodeSession{streamInitResult: make(chan error, 1)}
		result := s.streamInitResult
		subtype := "success"
		if failure {
			subtype = "error"
		}
		s.handleClaudeStdoutLine(`{"type":"control_response","response":{"request_id":"lcr-stream-initialize","subtype":"` + subtype + `","error":"unsupported","response":{"session_state":"idle"}}}`)
		err := initializeClaudeStream(context.Background(), io.Discard, result)
		if (err != nil) != failure {
			t.Fatalf("initialize failure=%v: %v", failure, err)
		}
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if err := initializeClaudeStream(ctx, io.Discard, make(chan error)); !errors.Is(err, context.Canceled) {
		t.Fatalf("initialize cancellation: %v", err)
	}
}

func TestClaudeManagedEnvironmentKeepsDelegationResponsive(t *testing.T) {
	cmd := &exec.Cmd{Env: []string{"KEEP=value", claudeDisableBackgroundTasksEnv + "=1", "CLAUDE_CODE_FORK_SUBAGENT=0"}}
	applyEmbeddedClaudeProcessEnvironment(cmd, true)
	for _, want := range []string{"KEEP=value", claudeDisableBackgroundTasksEnv + "=0", "CLAUDE_CODE_FORK_SUBAGENT=1", "CLAUDE_CODE_EMIT_SESSION_STATE_EVENTS=1"} {
		if !strings.Contains(strings.Join(cmd.Env, "\n"), want) {
			t.Fatalf("missing %s in %v", want, cmd.Env)
		}
	}
}

func TestMessageQueueWarningIgnoresWorkerActivity(t *testing.T) {
	now := time.Now()
	s := Snapshot{Busy: true, LastActivityAt: now, MessageDeliveries: []MessageDeliverySnapshot{{ID: "queued", State: "queued", SubmittedAt: now.Add(-3 * time.Minute)}}}
	if summary, warning := s.MessageDeliverySummary(now); !warning || !strings.Contains(summary, "3m0s") {
		t.Fatalf("hidden queue wait: %q %v", summary, warning)
	}
	s.MessageDeliveries[0].State = "sent"
	if summary, _ := s.MessageDeliverySummary(now); !strings.Contains(summary, "not yet confirmed") {
		t.Fatalf("missing evidence was treated as proof of unread input: %q", summary)
	}
	s.MessageDeliveries[0].State = "started"
	if summary, warning := s.MessageDeliverySummary(now); warning || summary != "Latest message: processing" {
		t.Fatalf("wrong receipt: %q %v", summary, warning)
	}
}

func TestParentSilenceIsSeparateFromWorkerAndTransportActivity(t *testing.T) {
	now := time.Now()
	s := Snapshot{ParentStatus: "Parent working", ParentTurnActive: true, ParentActivityAt: now.Add(-6 * time.Minute), LastActivityAt: now}
	if summary, quiet := s.ParentActivitySummary(now); !quiet || !strings.Contains(summary, "6m0s") {
		t.Fatalf("hidden parent silence: %q %v", summary, quiet)
	}
	s.ParentTurnActive = false
	s.ParentStatus = "Parent available · background work running"
	if _, quiet := s.ParentActivitySummary(now); quiet {
		t.Fatal("available parent should not be warned for awaiting background work")
	}
}
