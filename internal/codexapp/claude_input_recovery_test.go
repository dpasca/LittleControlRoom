package codexapp

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"
)

func recoveryTestSession(t *testing.T) (*claudeCodeSession, *recordingWriteCloser) {
	t.Helper()
	s, stdin := managedClaudeTestSession()
	s.dataDir = t.TempDir()
	s.projectPath = t.TempDir()
	s.sessionID = "recovery-session"
	s.messageDeliveries = nil
	s.claudeHome = t.TempDir()
	s.sessionFile = claudeSessionFilePath(s.claudeHome, s.projectPath, s.sessionID)
	if err := os.MkdirAll(filepath.Dir(s.sessionFile), 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(s.sessionFile, []byte(`{"type":"assistant","uuid":"earlier","message":{"role":"assistant","stop_reason":"end_turn","content":[{"type":"text","text":"Earlier answer"}]}}`+"\n"), 0600); err != nil {
		t.Fatal(err)
	}
	return s, stdin
}

func TestClaudeQueuedInputsSurviveStopAndColdResume(t *testing.T) {
	s, _ := recoveryTestSession(t)
	prompts := []string{"First request\n" + strings.Repeat("full text beyond the queue preview. ", 20), "Second request\nKeep every detail here."}
	for i := range prompts {
		prompts[i] = strings.TrimSpace(prompts[i])
	}
	for _, prompt := range prompts {
		if err := s.Submit(prompt); err != nil {
			t.Fatal(err)
		}
	}
	// Load once while still busy, reproducing the overwrite of the live echo.
	s.mu.Lock()
	if err := s.loadTranscriptLocked(); err != nil {
		t.Fatal(err)
	}
	s.mu.Unlock()
	for _, entry := range s.Snapshot().Entries {
		if strings.HasPrefix(entry.ItemID, "saved-input:") {
			t.Fatal("live queued input was offered for duplicate resend")
		}
	}
	s.interruptPending = true
	s.finishClaudeTurn(nil, nil, nil)
	assertSavedRecoveryPrompts(t, s, prompts)
	if s.pendingSubmissions != 0 || s.busy {
		t.Fatal("Stop restarted queued work")
	}
	cold := &claudeCodeSession{dataDir: s.dataDir, projectPath: s.projectPath, sessionID: s.sessionID, sessionFile: s.sessionFile}
	var err error
	cold.savedInputs, err = loadClaudeSavedInputs(cold.dataDir, cold.projectPath, cold.sessionID)
	if err != nil {
		t.Fatal(err)
	}
	if err := cold.loadTranscriptLocked(); err != nil {
		t.Fatal(err)
	}
	assertSavedRecoveryPrompts(t, cold, prompts)
	if cold.cmd != nil || cold.pendingSubmissions != 0 || cold.latestTurnStartedAt != (time.Time{}) {
		t.Fatal("recovery was treated as a new provider turn")
	}
	// Unchanged files must not duplicate restored prompts.
	if err := cold.loadTranscriptLocked(); err != nil {
		t.Fatal(err)
	}
	assertSavedRecoveryPrompts(t, cold, prompts)
}

func assertSavedRecoveryPrompts(t *testing.T, s *claudeCodeSession, prompts []string) {
	t.Helper()
	var actual []string
	notices := 0
	for _, entry := range s.Snapshot().Entries {
		if entry.Kind == TranscriptUser {
			actual = append(actual, entry.Text)
		}
		if strings.HasPrefix(entry.ItemID, "saved-input:") {
			notices++
		}
	}
	if strings.Join(actual, "\x00") != strings.Join(prompts, "\x00") || notices != len(prompts) {
		t.Fatalf("recovered prompts = %#v, notices = %d", actual, notices)
	}
}

func TestClaudeRecoveryDeduplicatesProviderPromptByUUID(t *testing.T) {
	for _, queued := range []bool{false, true} {
		t.Run(map[bool]string{false: "user", true: "queued_attachment"}[queued], func(t *testing.T) {
			s, _ := recoveryTestSession(t)
			const prompt = "identical text is not a message identity"
			for i := 0; i < 2; i++ {
				if err := s.Submit(prompt); err != nil {
					t.Fatal(err)
				}
			}
			id := s.savedInputs[0].ID
			var native any
			if queued {
				native = map[string]any{"type": "attachment", "uuid": "different-attachment-id", "attachment": map[string]any{"type": "queued_command", "commandMode": "prompt", "source_uuid": id, "prompt": prompt}}
			} else {
				native = map[string]any{"type": "user", "uuid": id, "message": map[string]any{"role": "user", "content": prompt}}
			}
			data, _ := json.Marshal(native)
			if err := os.WriteFile(s.sessionFile, append(data, '\n'), 0600); err != nil {
				t.Fatal(err)
			}
			if err := s.loadTranscriptLocked(); err != nil {
				t.Fatal(err)
			}
			if len(s.savedInputs) != 1 || s.savedInputs[0].ID == id {
				t.Fatalf("saved inputs = %#v; want only second identical prompt", s.savedInputs)
			}
			records, err := loadClaudeSavedInputs(s.dataDir, s.projectPath, s.sessionID)
			if err != nil || len(records) != 1 {
				t.Fatalf("records = %#v, %v", records, err)
			}
			userCount := 0
			for _, entry := range s.entries {
				if entry.Kind == TranscriptUser {
					userCount++
				}
			}
			if userCount != 2 {
				t.Fatalf("user count = %d", userCount)
			}
		})
	}
}

func TestClaudeRecoveryWriteFailureDoesNotSend(t *testing.T) {
	s, stdin := recoveryTestSession(t)
	blocked := filepath.Join(s.dataDir, "not-a-directory")
	if err := os.WriteFile(blocked, []byte("blocked"), 0600); err != nil {
		t.Fatal(err)
	}
	s.dataDir = blocked
	err := s.Submit("Keep this in the composer if saving fails")
	if err == nil || !strings.Contains(err.Error(), "save Claude Code follow-up") {
		t.Fatalf("error = %v", err)
	}
	if len(stdin.writes) != 0 || len(s.messageDeliveries) != 0 {
		t.Fatal("unsaved prompt was sent or accepted")
	}
}

func TestClaudeRecoveryRemainsViewableWithoutProviderTranscript(t *testing.T) {
	s, _ := recoveryTestSession(t)
	if err := s.Submit("Save this even if Claude never creates its transcript"); err != nil {
		t.Fatal(err)
	}
	if err := os.Remove(s.sessionFile); err != nil {
		t.Fatal(err)
	}
	cold := &claudeCodeSession{dataDir: s.dataDir, projectPath: s.projectPath, sessionID: s.sessionID, sessionFile: s.sessionFile}
	var err error
	cold.savedInputs, err = loadClaudeSavedInputs(cold.dataDir, cold.projectPath, cold.sessionID)
	if err != nil {
		t.Fatal(err)
	}
	if err := cold.loadTranscriptLocked(); err != nil {
		t.Fatal(err)
	}
	assertSavedRecoveryPrompts(t, cold, []string{"Save this even if Claude never creates its transcript"})
	if !strings.Contains(cold.lastError, "transcript is missing") || cold.latestTurnStateKnown || cold.busy {
		t.Fatalf("missing native history was not explicit: %s", cold.lastError)
	}
}

func TestClaudeRecoveryIsScopedAndKeepsPrivateFullSubmission(t *testing.T) {
	s, _ := recoveryTestSession(t)
	input := Submission{Text: "Full model context", DisplayText: "Visible request", Attachments: []Attachment{{Kind: AttachmentLocalImage, Path: "/tmp/reference.png"}}}
	record := claudeSavedInput{ID: "saved", ProjectPath: s.projectPath, SessionID: s.sessionID, SubmittedAt: time.Now(), Input: input}
	if err := saveClaudeInput(s.dataDir, record); err != nil {
		t.Fatal(err)
	}
	records, err := loadClaudeSavedInputs(s.dataDir, s.projectPath, s.sessionID)
	if err != nil || len(records) != 1 || records[0].Input.Text != input.Text || records[0].Input.Attachments[0].Path != input.Attachments[0].Path {
		t.Fatalf("records = %#v, %v", records, err)
	}
	for _, scope := range [][2]string{{s.projectPath, "other-session"}, {"other-project", s.sessionID}} {
		records, err := loadClaudeSavedInputs(s.dataDir, scope[0], scope[1])
		if err != nil || len(records) != 0 {
			t.Fatalf("cross-scope recovery = %#v, %v", records, err)
		}
	}
	info, err := os.Stat(filepath.Join(claudeInputRecoveryDir(s.dataDir, s.projectPath, s.sessionID), "saved.json"))
	if err != nil || info.Mode().Perm() != 0600 {
		t.Fatalf("saved prompt permissions = %v, %v", info, err)
	}
}

// Exercise the real Stop/process-exit path with a provider that acknowledges
// queued messages but never writes their bodies into its native transcript.
func TestClaudeQueuedRecoveryAfterInterruptProcess(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("shell helper requires Unix")
	}
	s, _ := recoveryTestSession(t)
	s.cmd, s.stdin, s.busy, s.pendingSubmissions = nil, nil, false, 0
	s.safetySettings = `{}`
	s.checkAuthentication = func(context.Context) error { return nil }
	executable, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	binDir := t.TempDir()
	t.Setenv("LCROOM_CLAUDE_RECOVERY_HELPER", executable)
	t.Setenv("PATH", binDir+string(os.PathListSeparator)+os.Getenv("PATH"))
	script := "#!/bin/sh\nexec \"$LCROOM_CLAUDE_RECOVERY_HELPER\" -test.run='^TestClaudeQueuedRecoveryProcessHelper$'\n"
	if err := os.WriteFile(filepath.Join(binDir, "claude"), []byte(script), 0700); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = s.Close(); s.WaitClosed(time.Second) })
	if err := s.Submit("Start slow work"); err != nil {
		t.Fatal(err)
	}
	prompts := []string{"First complete queued request", "Second complete queued request"}
	for _, prompt := range prompts {
		if err := s.Submit(prompt); err != nil {
			t.Fatal(err)
		}
	}
	if err := s.Interrupt(); err != nil {
		t.Fatal(err)
	}
	deadline := time.Now().Add(5 * time.Second)
	for s.StateSnapshot().Busy && time.Now().Before(deadline) {
		time.Sleep(10 * time.Millisecond)
	}
	if s.StateSnapshot().Busy {
		t.Fatal("provider did not stop")
	}
	assertSavedRecoveryPrompts(t, s, prompts)
	if s.StateSnapshot().MessageDeliveries[2].State != "interrupted" {
		t.Fatal("queued prompt was not marked interrupted")
	}
}

func TestClaudeQueuedRecoveryProcessHelper(t *testing.T) {
	if os.Getenv("LCROOM_CLAUDE_RECOVERY_HELPER") == "" {
		t.Skip("subprocess helper")
	}
	scanner := bufio.NewScanner(os.Stdin)
	count := 0
	for scanner.Scan() {
		var env claudeStreamEnvelope
		if err := json.Unmarshal(scanner.Bytes(), &env); err != nil {
			os.Exit(9)
		}
		if env.Type == "control_request" {
			fmt.Println(`{"type":"system","subtype":"init","session_id":"recovery-session"}`)
			fmt.Println(`{"type":"control_response","response":{"request_id":"lcr-stream-initialize","subtype":"success"}}`)
		} else if env.Type == "user" {
			count++
			state := "queued"
			if count == 1 {
				state = "started"
			}
			fmt.Printf("{\"type\":\"command_lifecycle\",\"command_uuid\":%q,\"state\":%q}\n", env.UUID, state)
			fmt.Println(`{"type":"system","subtype":"session_state_changed","state":"running"}`)
		}
	}
	os.Exit(0)
}
