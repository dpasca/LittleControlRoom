package codexapp

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	"lcroom/internal/browserctl"
)

func claudeBrowserToolResult(s *claudeCodeSession, id, name, input, result string, failed bool) {
	s.handleClaudeStdoutLine(`{"type":"assistant","message":{"id":"msg-` + id + `","content":[{"type":"tool_use","id":"` + id + `","name":"` + name + `","input":` + input + `}]}}`)
	content, _ := json.Marshal(result)
	isError, _ := json.Marshal(failed)
	s.handleClaudeStdoutLine(`{"type":"user","message":{"content":[{"type":"tool_result","tool_use_id":"` + id + `","is_error":` + string(isError) + `,"content":` + string(content) + `}]}}`)
}

func TestClaudeOpenBrowserSurvivesConversationTurns(t *testing.T) {
	for _, boundary := range []string{"managed idle", "terminal assistant", "result", "transcript"} {
		for _, handoff := range []bool{false, true} {
			name := boundary + "/browser tools"
			if handoff {
				name = boundary + "/browser handoff"
			}
			t.Run(name, func(t *testing.T) {
				s, stdin := managedClaudeTestSession()
				s.managedStream = boundary == "managed idle"
				s.playwrightPolicy = browserctl.DefaultPolicy()
				s.playwrightPolicy.DefaultBrowserMode = browserctl.BrowserModeHeaded
				s.managedBrowserSessionKey = "test-browser"
				s.dataDir = t.TempDir()
				cmd := s.cmd
				complete := func(id string) {
					t.Helper()
					switch boundary {
					case "managed idle":
						s.handleClaudeStdoutLine(`{"type":"result","subtype":"success"}`)
						s.handleClaudeStdoutLine(`{"type":"command_lifecycle","command_uuid":"` + id + `","state":"completed"}`)
						s.handleClaudeStdoutLine(`{"type":"system","subtype":"session_state_changed","state":"idle"}`)
					case "terminal assistant":
						s.handleClaudeStdoutLine(`{"type":"assistant","message":{"id":"reply-` + id + `","content":[{"type":"text","text":"Done"}],"stop_reason":"end_turn"}}`)
					case "result":
						s.handleClaudeStdoutLine(`{"type":"result","subtype":"success"}`)
					case "transcript":
						s.latestSubmittedAt = time.Now().Add(-time.Second)
						s.latestTurnStateKnown, s.latestTurnCompleted, s.latestTurnVerified = true, true, true
						s.latestTurnStateAt = time.Now()
						if input := s.finishClaudeSubmissionFromTranscriptLocked(); input != nil {
							_ = input.Close()
						}
					}
				}
				claudeBrowserToolResult(s, "navigate", "mcp__playwright__browser_navigate", `{"url":"https://example.com/form"}`, "### Page state\n- Page URL: https://example.com/form\n", false)
				if handoff {
					claudeBrowserToolResult(s, "attention", "mcp__lcr_runtime__request_browser_attention", `{"message":"Review the form."}`, `{"success":true}`, false)
				}
				complete("first")
				if stdin.closed || s.stdin != stdin || s.Snapshot().Busy {
					t.Fatal("completed browser turn must leave an idle, retained process")
				}
				for _, prompt := range []string{"Do we need localisation?", "Please bring the form back."} {
					if err := s.Submit(prompt); err != nil {
						t.Fatal(err)
					}
					var sent struct {
						UUID string `json:"uuid"`
						Type string `json:"type"`
					}
					if err := json.Unmarshal([]byte(stdin.writes[len(stdin.writes)-1]), &sent); err != nil || sent.Type != "user" {
						t.Fatalf("follow-up did not reuse the user-input stream: %v", err)
					}
					if s.browserHandoffPending {
						t.Fatal("follow-up should clear attention without releasing the browser")
					}
					complete(sent.UUID)
					if stdin.closed || s.stdin != stdin || s.cmd != cmd || s.pendingSubmissions != 0 || s.Snapshot().Busy {
						t.Fatal("ordinary conversation discarded the browser or left the turn busy")
					}
					if s.currentBrowserPageURL != "https://example.com/form" {
						t.Fatal("ordinary conversation discarded the current page")
					}
				}
				if err := s.CloseDueToInactivity(); err != nil || s.Snapshot().Closed {
					t.Fatalf("inactivity closed an open browser: %v", err)
				}
				if err := s.Submit("Close the browser."); err != nil {
					t.Fatal(err)
				}
				var sent struct {
					UUID string `json:"uuid"`
				}
				_ = json.Unmarshal([]byte(stdin.writes[len(stdin.writes)-1]), &sent)
				claudeBrowserToolResult(s, "close", "mcp__playwright__browser_close", `{}`, "Browser closed", false)
				complete(sent.UUID)
				if !stdin.closed || s.stdin != nil || s.managedBrowserOpen || s.currentBrowserPageURL != "" {
					t.Fatal("closing the browser did not release the settled Claude stream")
				}
			})
		}
	}
}

func TestClaudeFailedBrowserClosePreservesPageAndHandoff(t *testing.T) {
	s, _ := managedClaudeTestSession()
	s.playwrightPolicy = browserctl.DefaultPolicy()
	s.managedBrowserSessionKey = "test-browser"
	claudeBrowserToolResult(s, "navigate", "mcp__playwright__browser_navigate", `{"url":"https://example.com/form"}`, "Loaded", false)
	claudeBrowserToolResult(s, "attention", "mcp__lcr_runtime__request_browser_attention", `{}`, `{"success":true}`, false)
	claudeBrowserToolResult(s, "failed-close", "mcp__playwright__browser_close", `{}`, "Failed to close", true)
	if !s.managedBrowserOpen || !s.browserHandoffPending || s.currentBrowserPageURL != "https://example.com/form" {
		t.Fatal("failed close discarded a live page or handoff")
	}
}

func TestClaudeUnusedBrowserDoesNotRetainStream(t *testing.T) {
	for _, scenario := range []string{"failed navigation", "browser install", "unmanaged browser"} {
		t.Run(scenario, func(t *testing.T) {
			s, stdin := managedClaudeTestSession()
			s.playwrightPolicy = browserctl.DefaultPolicy()
			s.managedBrowserSessionKey = "test-browser"
			tool, failed := "browser_navigate", false
			switch scenario {
			case "failed navigation":
				failed = true
			case "browser install":
				tool = "browser_install"
			case "unmanaged browser":
				s.playwrightPolicy.ManagementMode = browserctl.ManagementModeLegacy
			}
			claudeBrowserToolResult(s, "tool", "mcp__playwright__"+tool, `{}`, "Result", failed)
			s.handleClaudeStdoutLine(`{"type":"command_lifecycle","command_uuid":"first","state":"completed"}`)
			s.handleClaudeStdoutLine(`{"type":"system","subtype":"session_state_changed","state":"idle"}`)
			if !stdin.closed || s.managedBrowserOpen {
				t.Fatal("tool without an owned browser retained Claude's stdin")
			}
		})
	}
}

func TestClaudeBrowserExplainsDeferredSettings(t *testing.T) {
	s := &claudeCodeSession{cmd: &exec.Cmd{}, managedBrowserOpen: true}
	if got := s.claudeStagedChangeTimingLocked(); got != "on the next prompt after the managed browser is closed and current work finishes" {
		t.Fatalf("settings timing = %q", got)
	}
}

func TestClaudeCompactionPreservesRetainedBrowser(t *testing.T) {
	s, stdin := managedClaudeTestSession()
	s.busy, s.pendingSubmissions, s.managedBrowserOpen = false, 0, true
	_, err := s.CompactWithInstructions("")
	if err == nil || !strings.Contains(err.Error(), "close the managed browser") {
		t.Fatalf("expected explicit browser-preservation error, got %v", err)
	}
	if stdin.closed || s.stdin != stdin || s.compacting {
		t.Fatal("compaction changed a retained browser's process")
	}
}

func TestClaudeRetainedBrowserLateResultDoesNotConsumeFollowup(t *testing.T) {
	s, stdin := managedClaudeTestSession()
	s.managedStream = false
	s.managedBrowserOpen = true
	s.handleClaudeStdoutLine(`{"type":"assistant","message":{"id":"first-reply","content":[{"type":"text","text":"Done"}],"stop_reason":"end_turn"}}`)
	if err := s.Submit("Follow up before the result arrives."); err != nil {
		t.Fatal(err)
	}
	s.handleClaudeStdoutLine(`{"type":"result","subtype":"success"}`)
	if s.pendingSubmissions != 1 || !s.Snapshot().Busy || stdin.closed {
		t.Fatal("late result consumed the new submission")
	}
	// A provider that omits the earlier result can instead start the next reply
	// directly. Its own result must still settle that submission.
	s.legacyTurnSettled = true
	s.handleClaudeStdoutLine(`{"type":"assistant","message":{"id":"next-tool","content":[{"type":"tool_use","id":"tool","name":"Read","input":{}}],"stop_reason":"tool_use"}}`)
	s.handleClaudeStdoutLine(`{"type":"result","subtype":"success"}`)
	if s.pendingSubmissions != 0 || s.Snapshot().Busy || stdin.closed {
		t.Fatal("next response did not release work ownership while retaining the browser")
	}
}

func TestClaudeBrowserProcessSurvivesConversation(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("shell helper requires Unix")
	}
	dir := t.TempDir()
	executable, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	t.Setenv("LCROOM_CLAUDE_BROWSER_HELPER", executable)
	t.Setenv("PATH", dir+string(os.PathListSeparator)+os.Getenv("PATH"))
	script := "#!/bin/sh\nexec \"$LCROOM_CLAUDE_BROWSER_HELPER\" -test.run='^TestClaudeBrowserProcessHelper$'\n"
	if err := os.WriteFile(filepath.Join(dir, "claude"), []byte(script), 0700); err != nil {
		t.Fatal(err)
	}
	for _, managed := range []bool{false, true} {
		t.Run(fmt.Sprintf("managed=%t", managed), func(t *testing.T) {
			s, _ := managedClaudeTestSession()
			s.managedStream = managed
			s.cmd, s.stdin, s.busy, s.pendingSubmissions, s.messageDeliveries = nil, nil, false, 0, nil
			s.projectPath, s.claudeHome, s.dataDir, s.safetySettings = dir, dir, dir, `{}`
			s.playwrightPolicy = browserctl.DefaultPolicy()
			s.playwrightPolicy.DefaultBrowserMode = browserctl.BrowserModeHeaded
			s.managedBrowserSessionKey = "test-browser"
			s.checkAuthentication = func(context.Context) error { return nil }
			t.Cleanup(func() { _ = s.Close(); s.WaitClosed(time.Second) })
			pid := 0
			for _, prompt := range []string{"Fill the form", "Discuss localisation", "Inspect the form", "Close the browser"} {
				if err := s.Submit(prompt); err != nil {
					t.Fatal(err)
				}
				s.mu.Lock()
				currentPID := s.runningPID
				s.mu.Unlock()
				if pid == 0 {
					pid = currentPID
				} else if currentPID != pid {
					t.Fatalf("%q restarted the process: %d -> %d", prompt, pid, currentPID)
				}
				deadline := time.Now().Add(8 * time.Second)
				for s.StateSnapshot().Busy && time.Now().Before(deadline) {
					time.Sleep(10 * time.Millisecond)
				}
				snapshot := s.StateSnapshot()
				if snapshot.Busy || snapshot.LastError != "" {
					t.Fatalf("%q did not settle: %#v", prompt, snapshot)
				}
				if prompt != "Close the browser" && snapshot.CurrentBrowserPageURL != "https://example.com/filled-form" {
					t.Fatalf("%q lost the filled page: %q", prompt, snapshot.CurrentBrowserPageURL)
				}
			}
			s.mu.Lock()
			defer s.mu.Unlock()
			if s.cmd != nil || s.managedBrowserOpen {
				t.Fatal("explicit browser close retained the process")
			}
		})
	}
}

func TestClaudeBrowserProcessHelper(t *testing.T) {
	if os.Getenv("LCROOM_CLAUDE_BROWSER_HELPER") == "" {
		t.Skip("subprocess helper")
	}
	emit := func(message string) { fmt.Fprintln(os.Stdout, message) }
	tool := func(id, name, input, result string) {
		emit(`{"type":"assistant","message":{"id":"msg-` + id + `","content":[{"type":"tool_use","id":"` + id + `","name":"` + name + `","input":` + input + `}]}}`)
		encoded, _ := json.Marshal(result)
		emit(`{"type":"user","message":{"content":[{"type":"tool_result","tool_use_id":"` + id + `","content":` + string(encoded) + `}]}}`)
	}
	scanner := bufio.NewScanner(os.Stdin)
	managed, prompts := false, 0
	for scanner.Scan() {
		var env claudeStreamEnvelope
		if err := json.Unmarshal(scanner.Bytes(), &env); err != nil {
			os.Exit(9)
		}
		if env.Type == "control_request" {
			managed = true
			emit(`{"type":"control_response","response":{"request_id":"lcr-stream-initialize","subtype":"success"}}`)
			continue
		}
		prompts++
		if managed {
			emit(`{"type":"system","subtype":"session_state_changed","state":"running"}`)
			emit(`{"type":"command_lifecycle","command_uuid":"` + env.UUID + `","state":"started"}`)
		}
		switch prompts {
		case 1:
			tool("navigate", "mcp__playwright__browser_navigate", `{"url":"https://example.com/filled-form"}`, "Loaded")
			tool("handoff", "mcp__lcr_runtime__request_browser_attention", `{}`, `{"success":true}`)
		case 3:
			tool("tabs", "mcp__playwright__browser_tabs", `{"action":"list"}`, "### Page state\n- Page URL: https://example.com/filled-form\n")
		case 4:
			tool("close", "mcp__playwright__browser_close", `{}`, "Browser closed")
		}
		emit(`{"type":"assistant","message":{"id":"reply-` + env.UUID + `","content":[{"type":"text","text":"Done"}],"stop_reason":"end_turn"}}`)
		emit(`{"type":"result","subtype":"success"}`)
		if managed {
			emit(`{"type":"command_lifecycle","command_uuid":"` + env.UUID + `","state":"completed"}`)
			emit(`{"type":"system","subtype":"session_state_changed","state":"idle"}`)
		}
	}
	if prompts != 4 {
		fmt.Fprintln(os.Stderr, "stdin closed before the browser workflow completed")
		os.Exit(9)
	}
	os.Exit(0)
}
