package tui

import (
	"fmt"
	"github.com/charmbracelet/bubbles/viewport"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/x/ansi"
	"lcroom/internal/browserctl"
	"lcroom/internal/codexapp"
	"lcroom/internal/config"
	"strings"
	"testing"
	"time"
)

func TestLCAgentBrowserWaitAcceptsResumeInputWhileBusy(t *testing.T) {
	projectPath := "/tmp/lcagent-browser-wait"
	session := &fakeCodexSession{
		projectPath: projectPath,
		snapshot: codexapp.Snapshot{
			Provider: codexapp.ProviderLCAgent,
			Started:  true,
			Busy:     true,
			Phase:    codexapp.SessionPhaseRunning,
			Status:   "Browser waiting for user input",
			BrowserActivity: browserctl.SessionActivity{
				State:      browserctl.SessionActivityStateWaitingForUser,
				ServerName: "playwright",
				ToolName:   "browser_wait_for_user",
			},
			ManagedBrowserSessionKey: "managed-login",
			CurrentBrowserPageURL:    "https://accounts.google.com/",
		},
	}
	manager := codexapp.NewManagerWithFactory(func(req codexapp.LaunchRequest, notify func()) (codexapp.Session, error) {
		return session, nil
	})
	if _, _, err := manager.Open(codexapp.LaunchRequest{ProjectPath: projectPath, Provider: codexapp.ProviderLCAgent}); err != nil {
		t.Fatalf("manager.Open() error = %v", err)
	}
	input := newCodexTextarea()
	input.SetValue("done")
	m := Model{
		codexManager:        manager,
		codexVisibleProject: projectPath,
		codexHiddenProject:  projectPath,
		codexSnapshots: map[string]codexapp.Snapshot{
			projectPath: session.snapshot,
		},
		codexInput: input,
	}

	updated, cmd := m.updateCodexMode(tea.KeyMsg{Type: tea.KeyEnter})
	got := updated.(Model)
	if cmd == nil {
		t.Fatal("Enter during LCAgent browser wait should send a resume command")
	}
	_ = cmd()
	if len(session.submissions) != 1 || session.submissions[0].TranscriptText() != "done" {
		t.Fatalf("submissions = %#v, want one resume message", session.submissions)
	}
	if strings.TrimSpace(got.codexInput.Value()) != "" {
		t.Fatalf("composer value = %q, want cleared after sending", got.codexInput.Value())
	}
	if !strings.Contains(got.status, "LCAgent") {
		t.Fatalf("status = %q, want LCAgent resume status", got.status)
	}
}

func TestLCAgentBusyWithoutBrowserWaitQueuesPrompt(t *testing.T) {
	snapshot := codexapp.Snapshot{
		Provider: codexapp.ProviderLCAgent,
		Started:  true,
		Busy:     true,
		Phase:    codexapp.SessionPhaseRunning,
		Status:   "LCAgent running",
	}
	if !codexSnapshotCanSubmitBusyInput(snapshot) {
		t.Fatal("busy LCAgent without a browser wait should accept queued prompt input")
	}
	if codexSnapshotCanSteer(snapshot) {
		t.Fatal("busy LCAgent without a browser wait should not be reported as immediate-steerable")
	}
	if !codexSnapshotQueuesBusyInput(snapshot) {
		t.Fatal("busy LCAgent without a browser wait should be reported as queueing input")
	}

	snapshot.BrowserActivity = browserctl.SessionActivity{State: browserctl.SessionActivityStateWaitingForUser}
	if !codexSnapshotCanSubmitBusyInput(snapshot) {
		t.Fatal("busy LCAgent waiting for browser input should accept resume input")
	}
	if !codexSnapshotCanSteer(snapshot) {
		t.Fatal("busy LCAgent waiting for browser input should be reported as steerable")
	}
	if codexSnapshotQueuesBusyInput(snapshot) {
		t.Fatal("busy LCAgent waiting for browser input should not be reported as queueing input")
	}
}

func TestVisibleLCAgentBrowserWaitAlwaysHasEscapeAndInterrupt(t *testing.T) {
	projectPath := "/tmp/lcagent-browser-escape"
	snapshot := codexapp.Snapshot{
		Provider: codexapp.ProviderLCAgent,
		Started:  true,
		Busy:     true,
		Phase:    codexapp.SessionPhaseRunning,
		BrowserActivity: browserctl.SessionActivity{
			State:      browserctl.SessionActivityStateWaitingForUser,
			ServerName: "playwright",
			ToolName:   "browser_wait_for_user",
		},
	}
	session := &fakeCodexSession{projectPath: projectPath, snapshot: snapshot}
	manager := codexapp.NewManagerWithFactory(func(req codexapp.LaunchRequest, notify func()) (codexapp.Session, error) {
		return session, nil
	})
	if _, _, err := manager.Open(codexapp.LaunchRequest{ProjectPath: projectPath, Provider: codexapp.ProviderLCAgent}); err != nil {
		t.Fatalf("manager.Open() error = %v", err)
	}
	base := Model{
		codexManager:        manager,
		codexVisibleProject: projectPath,
		codexHiddenProject:  projectPath,
		codexSnapshots: map[string]codexapp.Snapshot{
			projectPath: snapshot,
		},
		codexInput:       newCodexTextarea(),
		detailViewport:   viewport.New(20, 5),
		runtimeViewport:  viewport.New(20, 5),
		width:            100,
		height:           24,
		settingsBaseline: &config.EditableSettings{},
	}

	updated, _ := base.updateCodexMode(tea.KeyMsg{Type: tea.KeyEsc})
	if got := updated.(Model); got.codexVisibleProject != "" {
		t.Fatalf("Esc should hide visible LCAgent wait pane, codexVisibleProject=%q", got.codexVisibleProject)
	}

	updated, cmd := base.updateCodexMode(tea.KeyMsg{Type: tea.KeyCtrlC})
	if cmd == nil {
		t.Fatal("ctrl+c during visible LCAgent wait should return an interrupt and hide command")
	}
	_ = collectCmdMsgs(cmd)
	if !session.interrupted {
		t.Fatal("ctrl+c during visible LCAgent wait should interrupt the session")
	}
	if got := updated.(Model); got.codexVisibleProject != "" {
		t.Fatalf("ctrl+c should immediately leave visible LCAgent wait pane, codexVisibleProject=%q", got.codexVisibleProject)
	} else if !strings.Contains(got.status, "returning to the project list") {
		t.Fatalf("status = %q, want browser wait stop-and-return status", got.status)
	}

	footer := ansi.Strip(base.renderCodexFooter(snapshot, 160))
	for _, want := range []string{"Enter continue", "ctrl+c stop", "Alt+Up hide", "Esc hide"} {
		if !strings.Contains(footer, want) {
			t.Fatalf("renderCodexFooter() missing %q during LCAgent browser wait: %q", want, footer)
		}
	}
}

func TestLCAgentBrowserWaitFocusesBlurredComposerForTyping(t *testing.T) {
	projectPath := "/tmp/lcagent-browser-typing"
	snapshot := codexapp.Snapshot{
		Provider:                 codexapp.ProviderLCAgent,
		Started:                  true,
		Busy:                     true,
		Phase:                    codexapp.SessionPhaseRunning,
		Status:                   "Browser waiting for user input",
		ManagedBrowserSessionKey: "managed-login",
		CurrentBrowserPageURL:    "https://accounts.google.com/",
		BrowserActivity: browserctl.SessionActivity{
			Policy:     settingsAutomaticPlaywrightPolicy,
			State:      browserctl.SessionActivityStateWaitingForUser,
			ServerName: "playwright",
			ToolName:   "browser_wait_for_user",
		},
	}
	input := newCodexTextarea()
	input.Blur()
	m := Model{
		codexVisibleProject: projectPath,
		codexHiddenProject:  projectPath,
		codexSnapshots: map[string]codexapp.Snapshot{
			projectPath: snapshot,
		},
		codexInput: input,
		width:      100,
		height:     24,
	}

	updated, _ := m.updateCodexMode(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'d'}})
	got := updated.(Model)
	if !got.codexInput.Focused() {
		t.Fatal("browser wait should focus the composer before accepting typed input")
	}
	if got.codexInput.Value() != "d" {
		t.Fatalf("composer value = %q, want typed text", got.codexInput.Value())
	}
}

func TestCodexUpdateFocusesComposerWhenLCAgentBrowserWaitStarts(t *testing.T) {
	projectPath := "/tmp/lcagent-browser-update-focus"
	session := &fakeCodexSession{
		projectPath: projectPath,
		snapshot: codexapp.Snapshot{
			Provider:                 codexapp.ProviderLCAgent,
			Started:                  true,
			Busy:                     true,
			Phase:                    codexapp.SessionPhaseRunning,
			Status:                   "Browser waiting for user input",
			ManagedBrowserSessionKey: "managed-login",
			CurrentBrowserPageURL:    "https://accounts.google.com/",
			BrowserActivity: browserctl.SessionActivity{
				Policy:     settingsAutomaticPlaywrightPolicy,
				State:      browserctl.SessionActivityStateWaitingForUser,
				ServerName: "playwright",
				ToolName:   "browser_wait_for_user",
			},
		},
	}
	manager := codexapp.NewManagerWithFactory(func(req codexapp.LaunchRequest, notify func()) (codexapp.Session, error) {
		return session, nil
	})
	if _, _, err := manager.Open(codexapp.LaunchRequest{ProjectPath: projectPath, Provider: codexapp.ProviderLCAgent}); err != nil {
		t.Fatalf("manager.Open() error = %v", err)
	}
	input := newCodexTextarea()
	input.Blur()
	m := Model{
		codexManager:        manager,
		codexVisibleProject: projectPath,
		codexHiddenProject:  projectPath,
		codexSnapshots: map[string]codexapp.Snapshot{
			projectPath: {
				Provider: codexapp.ProviderLCAgent,
				Started:  true,
				Busy:     true,
				Phase:    codexapp.SessionPhaseRunning,
				Status:   "LCAgent running",
			},
		},
		codexInput:    input,
		codexViewport: viewport.New(0, 0),
		width:         100,
		height:        24,
		settingsBaseline: &config.EditableSettings{
			PlaywrightPolicy: settingsAutomaticPlaywrightPolicy,
		},
	}

	updated, _ := m.Update(codexUpdateMsg{projectPath: projectPath})
	got := updated.(Model)
	if !got.codexInput.Focused() {
		t.Fatal("visible LCAgent browser wait update should focus the composer")
	}
}

func TestVisibleLCAgentBrowserWaitExplainsContinueAndExitChoices(t *testing.T) {
	snapshot := codexapp.Snapshot{
		Provider:                 codexapp.ProviderLCAgent,
		Started:                  true,
		Busy:                     true,
		Phase:                    codexapp.SessionPhaseRunning,
		Status:                   "Browser waiting for user input",
		ManagedBrowserSessionKey: "managed-login",
		CurrentBrowserPageURL:    "https://accounts.google.com/",
		BrowserActivity: browserctl.SessionActivity{
			Policy:     settingsAutomaticPlaywrightPolicy,
			State:      browserctl.SessionActivityStateWaitingForUser,
			ServerName: "playwright",
			ToolName:   "browser_wait_for_user",
		},
	}
	m := Model{codexVisibleProject: "/tmp/lcagent-browser-wait"}

	renderedPanel := ansi.Strip(m.renderCodexBrowserPanel(snapshot, 140))
	for _, want := range []string{
		"browser_wait_for_user",
		"Background browser page: https://accounts.google.com/",
	} {
		if !strings.Contains(renderedPanel, want) {
			t.Fatalf("renderCodexBrowserPanel() missing %q during LCAgent browser wait: %q", want, renderedPanel)
		}
	}
	for _, unwanted := range []string{
		"Type a note below",
		"Press ctrl+c to stop the turn",
		"Esc or Alt+Up",
		"Press ctrl+o",
	} {
		if strings.Contains(renderedPanel, unwanted) {
			t.Fatalf("renderCodexBrowserPanel() should keep browser-wait copy compact, found %q in %q", unwanted, renderedPanel)
		}
	}

	renderedFooter := ansi.Strip(m.renderCodexFooter(snapshot, 160))
	for _, want := range []string{"Enter continue", "ctrl+c stop", "Alt+Up hide", "Esc hide"} {
		if !strings.Contains(renderedFooter, want) {
			t.Fatalf("renderCodexFooter() missing %q during LCAgent browser wait: %q", want, renderedFooter)
		}
	}
	if strings.Contains(renderedFooter, "ctrl+o") {
		t.Fatalf("renderCodexFooter() should not offer ctrl+o before browser state is confirmed: %q", renderedFooter)
	}
}

func TestVisibleLCAgentBrowserWaitShowsBrowserActionAfterStateHydrates(t *testing.T) {
	now := time.Date(2026, 5, 30, 19, 10, 0, 0, time.UTC)
	snapshot := codexapp.Snapshot{
		Provider:                 codexapp.ProviderLCAgent,
		Started:                  true,
		Busy:                     true,
		Phase:                    codexapp.SessionPhaseRunning,
		Status:                   "Browser waiting for user input",
		ManagedBrowserSessionKey: "managed-login",
		CurrentBrowserPageURL:    "https://accounts.google.com/",
		BrowserActivity: browserctl.SessionActivity{
			Policy:     settingsAutomaticPlaywrightPolicy,
			State:      browserctl.SessionActivityStateWaitingForUser,
			ServerName: "playwright",
			ToolName:   "browser_wait_for_user",
		},
	}
	m := Model{
		codexVisibleProject: "/tmp/lcagent-browser-wait",
		nowFn:               func() time.Time { return now },
		managedBrowserStates: map[string]browserctl.ManagedPlaywrightState{
			"managed-login": {SessionKey: "managed-login", BrowserPID: 123, Hidden: true, UpdatedAt: now},
		},
	}

	renderedPanel := ansi.Strip(m.renderCodexBrowserPanel(snapshot, 140))
	if !strings.Contains(renderedPanel, "Press ctrl+o to reveal the managed browser window") {
		t.Fatalf("renderCodexBrowserPanel() should offer ctrl+o after browser state hydrates: %q", renderedPanel)
	}
	renderedFooter := ansi.Strip(m.renderCodexFooter(snapshot, 160))
	if !strings.Contains(renderedFooter, "ctrl+o show browser") {
		t.Fatalf("renderCodexFooter() should offer ctrl+o after browser state hydrates: %q", renderedFooter)
	}
}

func TestVisibleLCAgentBrowserWaitCanRevealFromLiveStateBeforeBrowserStateHydrates(t *testing.T) {
	projectPath := "/tmp/lcagent-browser-live"
	cached := codexapp.Snapshot{
		Provider: codexapp.ProviderLCAgent,
		Started:  true,
		Busy:     true,
		Phase:    codexapp.SessionPhaseRunning,
		Status:   "LCAgent running",
	}
	live := cached
	live.Status = "Browser waiting for user input"
	live.ManagedBrowserSessionKey = "managed-login"
	live.CurrentBrowserPageURL = "https://accounts.google.com/"
	live.BrowserActivity = browserctl.SessionActivity{
		Policy:     settingsAutomaticPlaywrightPolicy,
		State:      browserctl.SessionActivityStateWaitingForUser,
		ServerName: "playwright",
		ToolName:   "browser_wait_for_user",
	}
	session := &fakeCodexSession{
		projectPath: projectPath,
		snapshot:    live,
		trySnapshotFn: func(*fakeCodexSession) (codexapp.Snapshot, bool) {
			return codexapp.Snapshot{}, false
		},
	}
	manager := codexapp.NewManagerWithFactory(func(req codexapp.LaunchRequest, notify func()) (codexapp.Session, error) {
		return session, nil
	})
	if _, _, err := manager.Open(codexapp.LaunchRequest{ProjectPath: projectPath, Provider: codexapp.ProviderLCAgent}); err != nil {
		t.Fatalf("manager.Open() error = %v", err)
	}

	previousSessionRevealer := managedBrowserSessionRevealer
	defer func() {
		managedBrowserSessionRevealer = previousSessionRevealer
	}()

	revealedSessionKey := ""
	managedBrowserSessionRevealer = func(_ string, sessionKey string) (browserctl.ManagedPlaywrightState, error) {
		revealedSessionKey = sessionKey
		return browserctl.ManagedPlaywrightState{SessionKey: sessionKey, BrowserPID: 123, RevealSupported: true}, nil
	}

	m := Model{
		codexManager:        manager,
		codexVisibleProject: projectPath,
		codexHiddenProject:  projectPath,
		codexSnapshots: map[string]codexapp.Snapshot{
			projectPath: cached,
		},
		codexInput:    newCodexTextarea(),
		codexViewport: viewport.New(0, 0),
		width:         100,
		height:        24,
	}

	snapshot, ok := m.currentCodexSnapshot()
	if !ok {
		t.Fatalf("currentCodexSnapshot() unavailable")
	}
	if snapshot.ManagedBrowserSessionKey != "managed-login" || snapshot.CurrentBrowserPageURL == "" {
		t.Fatalf("currentCodexSnapshot() did not include live browser state: %#v", snapshot)
	}
	if session.trySnapshotCalls != 0 || session.snapshotCalls != 0 {
		t.Fatalf("currentCodexSnapshot() should use lightweight state only; TrySnapshot/Snapshot calls = %d/%d", session.trySnapshotCalls, session.snapshotCalls)
	}

	renderedPanel := ansi.Strip(m.renderCodexBrowserPanel(snapshot, 140))
	if !strings.Contains(renderedPanel, "Press ctrl+o to reveal the managed browser window") {
		t.Fatalf("renderCodexBrowserPanel() should offer ctrl+o from live state before browser state hydration: %q", renderedPanel)
	}
	renderedFooter := ansi.Strip(m.renderCodexFooter(snapshot, 160))
	if !strings.Contains(renderedFooter, "ctrl+o show browser") {
		t.Fatalf("renderCodexFooter() should offer ctrl+o from live state before browser state hydration: %q", renderedFooter)
	}

	updated, cmd := m.updateCodexMode(tea.KeyMsg{Type: tea.KeyCtrlO})
	got := updated.(Model)
	if cmd == nil {
		t.Fatalf("ctrl+o should queue browser reveal from live state before browser state hydration")
	}
	if got.status != "Showing the managed browser window..." {
		t.Fatalf("status = %q, want managed browser reveal notice", got.status)
	}
	msg := cmd()
	openMsg, ok := msg.(browserOpenMsg)
	if !ok {
		t.Fatalf("cmd() returned %T, want browserOpenMsg", msg)
	}
	if openMsg.err != nil {
		t.Fatalf("browserOpenMsg.err = %v, want nil", openMsg.err)
	}
	if revealedSessionKey != "managed-login" {
		t.Fatalf("revealed session key = %q, want managed-login", revealedSessionKey)
	}
}

func TestVisibleLCAgentBrowserWaitDoesNotOpenUnhydratedOldBrowser(t *testing.T) {
	projectPath := "/tmp/lcagent-browser-old"
	snapshot := codexapp.Snapshot{
		Provider:                 codexapp.ProviderLCAgent,
		Started:                  true,
		Busy:                     true,
		Phase:                    codexapp.SessionPhaseRunning,
		Status:                   "Browser waiting for user input",
		ManagedBrowserSessionKey: "managed-login",
		CurrentBrowserPageURL:    "https://accounts.google.com/",
		BrowserActivity: browserctl.SessionActivity{
			Policy:     settingsAutomaticPlaywrightPolicy,
			State:      browserctl.SessionActivityStateWaitingForUser,
			ServerName: "playwright",
			ToolName:   "browser_wait_for_user",
		},
	}
	m := Model{
		codexVisibleProject: projectPath,
		codexHiddenProject:  projectPath,
		codexSnapshots: map[string]codexapp.Snapshot{
			projectPath: snapshot,
		},
		codexInput: newCodexTextarea(),
	}

	updated, cmd := m.updateCodexMode(tea.KeyMsg{Type: tea.KeyCtrlO})
	got := updated.(Model)
	if cmd != nil {
		t.Fatalf("ctrl+o should not queue a browser-open command before browser state is confirmed")
	}
	if !strings.Contains(got.status, "not attached") {
		t.Fatalf("status = %q, want detached browser guidance", got.status)
	}
}

func testViewportLines(count int) string {
	lines := make([]string, count)
	for i := range lines {
		lines[i] = fmt.Sprintf("line %02d", i)
	}
	return strings.Join(lines, "\n")
}

func TestCodexUpdateStatusOnlyBrowserPanelKeepsBottomAnchored(t *testing.T) {
	session := &fakeCodexSession{
		projectPath: "/tmp/demo",
		snapshot: codexapp.Snapshot{
			Provider: codexapp.ProviderCodex,
			Started:  true,
			Status:   "Codex session ready",
			Entries: []codexapp.TranscriptEntry{{
				Kind: codexapp.TranscriptAgent,
				Text: strings.Join([]string{
					"line 01", "line 02", "line 03", "line 04", "line 05", "line 06", "line 07", "line 08",
					"line 09", "line 10", "line 11", "line 12", "line 13", "line 14", "line 15", "line 16",
					"line 17", "line 18", "line 19", "line 20", "line 21", "line 22", "line 23", "line 24",
					"line 25", "line 26", "line 27", "line 28", "line 29", "line 30", "line 31", "line 32",
				}, "\n"),
			}},
			BrowserActivity:          browserctl.SessionActivity{Policy: settingsAutomaticPlaywrightPolicy},
			ManagedBrowserSessionKey: "managed-demo",
		},
	}
	manager := codexapp.NewManagerWithFactory(func(req codexapp.LaunchRequest, notify func()) (codexapp.Session, error) {
		return session, nil
	})
	if _, _, err := manager.Open(codexapp.LaunchRequest{ProjectPath: "/tmp/demo"}); err != nil {
		t.Fatalf("manager.Open() error = %v", err)
	}

	settings := config.EditableSettings{PlaywrightPolicy: settingsAutomaticPlaywrightPolicy}
	m := Model{
		codexManager:        manager,
		codexVisibleProject: "/tmp/demo",
		codexHiddenProject:  "/tmp/demo",
		codexInput:          newCodexTextarea(),
		codexViewport:       viewport.New(0, 0),
		settingsBaseline:    &settings,
		width:               48,
		height:              20,
	}
	if _, ok, _ := m.refreshCodexSnapshot("/tmp/demo"); !ok {
		t.Fatalf("refreshCodexSnapshot() failed")
	}
	m.syncCodexViewport(true)
	if !m.codexViewport.AtBottom() {
		t.Fatalf("initial sync should start at the bottom")
	}
	beforeLowerBlocks := m.codexLowerBlocks(session.snapshot, m.width)
	beforeYOffset := m.codexViewport.YOffset
	beforeHeight := m.codexViewport.Height
	beforeLowerHeight := countRenderedBlockLines(beforeLowerBlocks)

	session.snapshot.Status = "Codex status changed only"
	session.snapshot.ManagedBrowserSessionKey = ""

	updated, _ := m.Update(codexUpdateMsg{projectPath: "/tmp/demo"})
	got := updated.(Model)
	afterSnapshot, ok := got.currentCodexSnapshot()
	if !ok {
		t.Fatalf("currentCodexSnapshot() unavailable after update")
	}
	afterLowerBlocks := got.codexLowerBlocks(afterSnapshot, got.width)
	afterLowerHeight := countRenderedBlockLines(afterLowerBlocks)
	if afterLowerHeight <= beforeLowerHeight {
		t.Fatalf("test fixture should add browser panel rows, before=%d after=%d before=%q after=%q", beforeLowerHeight, afterLowerHeight, strings.Join(beforeLowerBlocks, "\n---\n"), strings.Join(afterLowerBlocks, "\n---\n"))
	}
	if got.codexViewport.Height >= beforeHeight {
		t.Fatalf("browser panel should reduce transcript height, before=%d after=%d", beforeHeight, got.codexViewport.Height)
	}
	if !got.codexViewport.AtBottom() {
		maxOffset := max(0, got.codexViewport.TotalLineCount()-got.codexViewport.Height)
		t.Fatalf("status-only browser update should keep transcript pinned to bottom, offset=%d max=%d", got.codexViewport.YOffset, maxOffset)
	}
	if got.codexViewport.YOffset <= beforeYOffset {
		t.Fatalf("browser panel should advance viewport offset to preserve the bottom, before=%d after=%d", beforeYOffset, got.codexViewport.YOffset)
	}
}
