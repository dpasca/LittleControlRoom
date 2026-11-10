package codexapp

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"
)

func TestClaudeCheckpointInterruptsThenResumesSameConversation(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("shell helper requires Unix")
	}
	dir := t.TempDir()
	executable, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	t.Setenv("LCROOM_CLAUDE_CHECKPOINT_HELPER", executable)
	t.Setenv("LCROOM_CLAUDE_CHECKPOINT_INPUT", filepath.Join(dir, "input.jsonl"))
	t.Setenv("PATH", dir+string(os.PathListSeparator)+os.Getenv("PATH"))
	if err := os.WriteFile(filepath.Join(dir, "claude"), []byte("#!/bin/sh\nexport LCROOM_CLAUDE_CHECKPOINT_ARGS=\"$*\"\nexec \"$LCROOM_CLAUDE_CHECKPOINT_HELPER\" -test.run='^TestClaudeCheckpointProcessHelper$'\n"), 0700); err != nil {
		t.Fatal(err)
	}
	s, _ := managedClaudeTestSession()
	s.cmd, s.stdin, s.busy, s.pendingSubmissions, s.messageDeliveries = nil, nil, false, 0, nil
	s.projectPath, s.claudeHome, s.safetySettings = dir, dir, `{}`
	s.mcpOptions.PermissionPromptTool = claudeRuntimeMCPApprovalTool
	s.checkAuthentication = func(context.Context) error { return nil }
	t.Cleanup(func() { _ = s.Close(); s.WaitClosed(time.Second) })
	if err := s.Submit("Run validation"); err != nil {
		t.Fatal(err)
	}
	waitClaudeCheckpointState(t, s, func() bool { return len(s.backgroundTasks) == 1 })
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if err := s.RequestCheckpoint(ctx, "Only report existing evidence; keep local games at one."); err != nil {
		t.Fatal(err)
	}
	waitClaudeCheckpointState(t, s, func() bool { return !s.busy })
	data, err := os.ReadFile(filepath.Join(dir, "input.jsonl"))
	if err != nil {
		t.Fatal(err)
	}
	lines := strings.Split(strings.TrimSpace(string(data)), "\n")
	if len(lines) != 2 {
		t.Fatalf("expected initial work and one report request, got %d inputs", len(lines))
	}
	var received struct {
		Args  []string        `json:"args"`
		Input json.RawMessage `json:"input"`
	}
	if err := json.Unmarshal([]byte(lines[1]), &received); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(strings.Join(received.Args, " "), "--resume checkpoint-test") {
		t.Fatalf("checkpoint did not resume original conversation: %v", received.Args)
	}
	for _, want := range []string{"Only report existing evidence", "test-job", "Stop launching builds", "does not prove background or remote jobs stopped", "acceptance criteria passed, failed and unverified"} {
		if !strings.Contains(string(received.Input), want) {
			t.Fatalf("checkpoint lost %q: %s", want, received.Input)
		}
	}
	if s.StateSnapshot().CheckpointPending {
		t.Fatal("checkpoint stayed pending after report submission")
	}
}

func waitClaudeCheckpointState(t *testing.T, s *claudeCodeSession, ready func() bool) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		s.mu.Lock()
		ok := ready()
		s.mu.Unlock()
		if ok {
			return
		}
		time.Sleep(5 * time.Millisecond)
	}
	t.Fatal("checkpoint session did not reach expected state")
}

func TestClaudeCheckpointProcessHelper(t *testing.T) {
	if os.Getenv("LCROOM_CLAUDE_CHECKPOINT_HELPER") == "" {
		t.Skip("subprocess helper")
	}
	path := os.Getenv("LCROOM_CLAUDE_CHECKPOINT_INPUT")
	scanner := bufio.NewScanner(os.Stdin)
	for scanner.Scan() {
		var env struct {
			Type      string `json:"type"`
			RequestID string `json:"request_id"`
			UUID      string `json:"uuid"`
		}
		if err := json.Unmarshal(scanner.Bytes(), &env); err != nil {
			os.Exit(9)
		}
		if env.Type == "control_request" {
			fmt.Printf("{\"type\":\"control_response\",\"response\":{\"request_id\":%q,\"subtype\":\"success\"}}\n", env.RequestID)
			continue
		}
		_, statErr := os.Stat(path)
		f, err := os.OpenFile(path, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0600)
		if err != nil {
			os.Exit(9)
		}
		data, _ := json.Marshal(map[string]any{"args": strings.Fields(os.Getenv("LCROOM_CLAUDE_CHECKPOINT_ARGS")), "input": json.RawMessage(scanner.Bytes())})
		_, _ = fmt.Fprintln(f, string(data))
		_ = f.Close()
		fmt.Println(`{"type":"system","subtype":"init","session_id":"checkpoint-test"}`)
		fmt.Println(`{"type":"system","subtype":"session_state_changed","state":"running"}`)
		fmt.Printf("{\"type\":\"command_lifecycle\",\"command_uuid\":%q,\"state\":\"started\"}\n", env.UUID)
		if os.IsNotExist(statErr) {
			fmt.Println(`{"type":"system","subtype":"task_started","task_id":"test-job","task_type":"local_bash","description":"validation waiter"}`)
			continue // No result or idle: the host must interrupt this process.
		}
		fmt.Println(`{"type":"assistant","message":{"id":"report","content":[{"type":"text","text":"Checkpoint report"}],"stop_reason":"end_turn"}}`)
		fmt.Println(`{"type":"result","subtype":"success"}`)
		fmt.Printf("{\"type\":\"command_lifecycle\",\"command_uuid\":%q,\"state\":\"completed\"}\n", env.UUID)
		fmt.Println(`{"type":"system","subtype":"session_state_changed","state":"idle"}`)
	}
	os.Exit(0)
}

func TestClaudeCheckpointCoalescesAndHonorsLaterStop(t *testing.T) {
	s, _ := managedClaudeTestSession()
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	done := make(chan error, 1)
	go func() { done <- s.RequestCheckpoint(ctx, "report") }()
	waitClaudeCheckpointState(t, s, func() bool { return s.checkpointPending })
	if err := s.RequestCheckpoint(ctx, "duplicate"); err == nil {
		t.Fatal("duplicate checkpoint accepted")
	}
	if err := s.Submit("competing input"); err == nil {
		t.Fatal("input accepted while checkpoint was settling")
	}
	if err := s.Interrupt(); err != nil {
		t.Fatal(err)
	}
	if err := <-done; !errors.Is(err, ErrSessionChanged) {
		t.Fatalf("later stop did not cancel checkpoint: %v", err)
	}
	if s.StateSnapshot().CheckpointPending {
		t.Fatal("canceled checkpoint stayed pending")
	}
}

func TestClaudeCheckpointTimeoutDoesNotResume(t *testing.T) {
	s, stdin := managedClaudeTestSession()
	ctx, cancel := context.WithTimeout(context.Background(), 25*time.Millisecond)
	defer cancel()
	if err := s.RequestCheckpoint(ctx, "report"); !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("expected bounded wait, got %v", err)
	}
	if len(stdin.writes) != 0 || s.StateSnapshot().CheckpointPending || !s.controlInput.Stopped {
		t.Fatal("timeout resumed work or left checkpoint busy")
	}
}

func TestClaudeCheckpointDoesNotOverrideAnEarlierStop(t *testing.T) {
	s, stdin := managedClaudeTestSession()
	if err := s.Interrupt(); err != nil {
		t.Fatal(err)
	}
	if err := s.RequestCheckpoint(context.Background(), "report"); !errors.Is(err, ErrSessionChanged) {
		t.Fatalf("checkpoint revived a stopped turn: %v", err)
	}
	if s.checkpointPending || len(stdin.writes) != 0 {
		t.Fatal("checkpoint accepted after stop")
	}
}

func TestClaudeCheckpointRetainsOwnershipBetweenProcesses(t *testing.T) {
	s, _ := managedClaudeTestSession()
	s.cmd, s.stdin, s.busy = nil, nil, false
	s.checkpointPending = true
	snapshot := s.StateSnapshot()
	if !snapshot.Busy || snapshot.Phase != SessionPhaseRunning || snapshot.BackgroundInputSupported {
		t.Fatalf("worker could hand off before checkpoint submission: %+v", snapshot)
	}
	if err := s.Interrupt(); err != nil || !s.controlInput.Stopped {
		t.Fatalf("stop between processes did not cancel checkpoint: %v", err)
	}
}

func TestClaudeCheckpointExitRetainsUnverifiedJobs(t *testing.T) {
	s, _ := managedClaudeTestSession()
	s.checkpointPending, s.interruptPending = true, true
	s.backgroundTasks["remote-job"] = BackgroundTaskSnapshot{ID: "remote-job", Status: "running", Tool: "Bash"}
	s.backgroundTaskOrder = []string{"remote-job"}
	s.finishClaudeTurn(nil, nil, nil)
	snapshot := s.StateSnapshot()
	if !snapshot.Busy || len(snapshot.BackgroundTasks) != 1 || snapshot.BackgroundTasks[0].Status != "unresolved" {
		t.Fatalf("interrupt lost unverified job ownership: %+v", snapshot)
	}
}
