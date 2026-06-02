package tui

import (
	"github.com/charmbracelet/bubbles/viewport"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/x/ansi"
	"lcroom/internal/browserctl"
	"lcroom/internal/codexapp"
	"lcroom/internal/codexcli"
	"lcroom/internal/config"
	"strings"
	"testing"
	"time"
)

func TestPendingToolInputEnterSendsStructuredAnswer(t *testing.T) {
	session := &fakeCodexSession{
		projectPath: "/tmp/demo",
		snapshot: codexapp.Snapshot{
			Started: true,
			Preset:  codexcli.PresetYolo,
			Status:  "Waiting for structured user input",
			PendingToolInput: &codexapp.ToolInputRequest{
				ID: "req_1",
				Questions: []codexapp.ToolInputQuestion{
					{
						ID:       "answer",
						Header:   "Reason",
						Question: "Why should we do this?",
					},
				},
			},
		},
	}
	manager := codexapp.NewManagerWithFactory(func(req codexapp.LaunchRequest, notify func()) (codexapp.Session, error) {
		return session, nil
	})
	if _, _, err := manager.Open(codexapp.LaunchRequest{
		ProjectPath: "/tmp/demo",
		Preset:      codexcli.PresetYolo,
	}); err != nil {
		t.Fatalf("manager.Open() error = %v", err)
	}

	input := newCodexTextarea()
	input.SetValue("Because it removes friction.")

	m := Model{
		codexManager:        manager,
		codexVisibleProject: "/tmp/demo",
		codexHiddenProject:  "/tmp/demo",
		codexInput:          input,
		codexToolAnswers:    make(map[string]codexToolAnswerState),
		codexViewport:       viewport.New(0, 0),
		width:               100,
		height:              24,
	}

	updated, cmd := m.updateCodexMode(tea.KeyMsg{Type: tea.KeyEnter})
	got := updated.(Model)
	if cmd == nil {
		t.Fatalf("enter should submit the structured answer")
	}
	if got.status != "Sending structured input..." {
		t.Fatalf("status = %q, want sending structured input notice", got.status)
	}

	msg := cmd()
	action, ok := msg.(codexActionMsg)
	if !ok {
		t.Fatalf("cmd() returned %T, want codexActionMsg", msg)
	}
	if action.status != "Structured input sent to Codex" {
		t.Fatalf("action status = %q, want structured input notice", action.status)
	}
	if len(session.toolAnswers) != 1 {
		t.Fatalf("tool answers = %d, want 1", len(session.toolAnswers))
	}
	if got := session.toolAnswers[0]["answer"]; len(got) != 1 || got[0] != "Because it removes friction." {
		t.Fatalf("tool answer = %#v, want submitted text", got)
	}
}

func TestVisibleCodexURLBasedElicitationCanOpenBrowser(t *testing.T) {
	session := &fakeCodexSession{
		projectPath: "/tmp/demo",
		snapshot: codexapp.Snapshot{
			Started:  true,
			ThreadID: "thread-demo",
			Preset:   codexcli.PresetYolo,
			Status:   "Browser needs attention",
			BrowserActivity: browserctl.SessionActivity{
				Policy:     settingsAutomaticPlaywrightPolicy,
				State:      browserctl.SessionActivityStateWaitingForUser,
				ServerName: "playwright",
				ToolName:   "browser_navigate",
			},
			ManagedBrowserSessionKey: "managed-demo",
			PendingElicitation: &codexapp.ElicitationRequest{
				ID:   "elicitation_1",
				Mode: codexapp.ElicitationModeURL,
				URL:  "https://example.test/login",
			},
		},
	}
	manager := codexapp.NewManagerWithFactory(func(req codexapp.LaunchRequest, notify func()) (codexapp.Session, error) {
		return session, nil
	})
	if _, _, err := manager.Open(codexapp.LaunchRequest{
		ProjectPath: "/tmp/demo",
		Preset:      codexcli.PresetYolo,
	}); err != nil {
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
		codexVisibleProject: "/tmp/demo",
		codexHiddenProject:  "/tmp/demo",
		codexInput:          newCodexTextarea(),
		codexViewport:       viewport.New(0, 0),
		width:               100,
		height:              24,
	}

	updated, cmd := m.updateCodexMode(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("o")})
	got := updated.(Model)
	if cmd == nil {
		t.Fatalf("o should queue the browser-open command")
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
	if openMsg.status != "Managed browser window is ready. Finish the browser flow there, then press Enter when you are ready to continue." {
		t.Fatalf("browserOpenMsg.status = %q, want managed browser handoff status", openMsg.status)
	}
	if revealedSessionKey != "managed-demo" {
		t.Fatalf("revealed session key = %q, want managed-demo", revealedSessionKey)
	}
}

func TestVisibleCodexCanOpenCurrentBackgroundBrowserPage(t *testing.T) {
	now := time.Date(2026, 5, 30, 19, 10, 0, 0, time.UTC)
	session := &fakeCodexSession{
		projectPath: "/tmp/demo",
		snapshot: codexapp.Snapshot{
			Started:                  true,
			ThreadID:                 "thread-demo",
			Preset:                   codexcli.PresetYolo,
			Status:                   "Codex session ready",
			BrowserActivity:          browserctl.SessionActivity{Policy: settingsAutomaticPlaywrightPolicy},
			ManagedBrowserSessionKey: "managed-demo",
			CurrentBrowserPageURL:    "https://chartboost.us.auth0.com/u/login?state=demo",
		},
	}
	manager := codexapp.NewManagerWithFactory(func(req codexapp.LaunchRequest, notify func()) (codexapp.Session, error) {
		return session, nil
	})
	if _, _, err := manager.Open(codexapp.LaunchRequest{
		ProjectPath: "/tmp/demo",
		Preset:      codexcli.PresetYolo,
	}); err != nil {
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
		codexVisibleProject: "/tmp/demo",
		codexHiddenProject:  "/tmp/demo",
		codexInput:          newCodexTextarea(),
		codexViewport:       viewport.New(0, 0),
		nowFn:               func() time.Time { return now },
		managedBrowserStates: map[string]browserctl.ManagedPlaywrightState{
			"managed-demo": {SessionKey: "managed-demo", BrowserPID: 123, Hidden: true, UpdatedAt: now},
		},
		width:  100,
		height: 24,
	}

	updated, cmd := m.updateCodexMode(tea.KeyMsg{Type: tea.KeyCtrlO})
	got := updated.(Model)
	if cmd == nil {
		t.Fatalf("ctrl+o should queue the current-page browser-open command")
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
	if openMsg.status != "Managed browser window is ready. Continue there, then return here when you want Codex to keep going." {
		t.Fatalf("browserOpenMsg.status = %q, want current page success status", openMsg.status)
	}
	if revealedSessionKey != "managed-demo" {
		t.Fatalf("revealed session key = %q, want managed-demo", revealedSessionKey)
	}

	updated, followupCmd := got.Update(openMsg)
	got = updated.(Model)
	if followupCmd != nil {
		t.Fatalf("browser-open followup should not queue more work")
	}
	renderedBlocks := ansi.Strip(got.renderCodexBrowserPanel(session.snapshot, 120))
	if strings.Contains(renderedBlocks, "Press ctrl+o to reveal the managed browser window for this same session.") {
		t.Fatalf("renderCodexBrowserPanel() kept stale ctrl+o reveal hint after successful reveal: %q", renderedBlocks)
	}
	if !strings.Contains(renderedBlocks, "Managed browser page: https://chartboost.us.auth0.com/u/login?state=demo") {
		t.Fatalf("renderCodexBrowserPanel() missing managed browser page label after reveal: %q", renderedBlocks)
	}
	footer := ansi.Strip(got.renderCodexFooter(session.snapshot, 160))
	if !strings.Contains(footer, "ctrl+o focus browser") {
		t.Fatalf("renderCodexFooter() should downgrade ctrl+o to focus browser after reveal: %q", footer)
	}
}

func TestVisibleCodexURLBasedElicitationBlocksWhenInteractiveLeaseOwnedElsewhere(t *testing.T) {
	session := &fakeCodexSession{
		projectPath: "/tmp/demo",
		snapshot: codexapp.Snapshot{
			Started:  true,
			ThreadID: "thread-demo",
			Preset:   codexcli.PresetYolo,
			Status:   "Browser needs attention",
			BrowserActivity: browserctl.SessionActivity{
				Policy:     settingsAutomaticPlaywrightPolicy,
				State:      browserctl.SessionActivityStateWaitingForUser,
				ServerName: "playwright",
				ToolName:   "browser_navigate",
			},
			ManagedBrowserSessionKey: "managed-demo",
			PendingElicitation: &codexapp.ElicitationRequest{
				ID:   "elicitation_1",
				Mode: codexapp.ElicitationModeURL,
				URL:  "https://example.test/login",
			},
		},
	}
	manager := codexapp.NewManagerWithFactory(func(req codexapp.LaunchRequest, notify func()) (codexapp.Session, error) {
		return session, nil
	})
	if _, _, err := manager.Open(codexapp.LaunchRequest{
		ProjectPath: "/tmp/demo",
		Preset:      codexcli.PresetYolo,
	}); err != nil {
		t.Fatalf("manager.Open() error = %v", err)
	}

	controller := browserctl.NewController()
	ownerObservation := browserctl.Observation{
		Ref: browserctl.SessionRef{
			Provider:    "codex",
			ProjectPath: "/tmp/owner-demo",
			SessionID:   "thread-owner",
		},
		Policy:   settingsAutomaticPlaywrightPolicy,
		Activity: browserctl.SessionActivity{Policy: settingsAutomaticPlaywrightPolicy, State: browserctl.SessionActivityStateWaitingForUser, ServerName: "playwright", ToolName: "browser_navigate"},
		LoginURL: "https://example.test/owner",
	}
	controller.Observe(ownerObservation)
	controller.AcquireInteractive(ownerObservation.Ref)

	m := Model{
		codexManager:         manager,
		codexVisibleProject:  "/tmp/demo",
		codexHiddenProject:   "/tmp/demo",
		codexInput:           newCodexTextarea(),
		codexViewport:        viewport.New(0, 0),
		browserController:    controller,
		browserLeaseSnapshot: controller.Snapshot(),
		width:                100,
		height:               24,
	}

	updated, cmd := m.updateCodexMode(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("o")})
	got := updated.(Model)
	if cmd != nil {
		for _, msg := range collectCmdMsgs(cmd) {
			if _, ok := msg.(browserOpenMsg); ok {
				t.Fatalf("blocked browser open should not queue a browser-open command")
			}
		}
	}
	if !strings.Contains(got.status, "Interactive browser is already reserved by Codex / owner-demo") {
		t.Fatalf("status = %q, want blocked browser ownership status", got.status)
	}
}

func TestVisibleCodexURLBasedElicitationHintsOpenBrowser(t *testing.T) {
	m := Model{
		codexVisibleProject: "/tmp/demo",
		codexSnapshots: map[string]codexapp.Snapshot{
			"/tmp/demo": {
				Provider:                 codexapp.ProviderCodex,
				ManagedBrowserSessionKey: "managed-demo",
				BrowserActivity: browserctl.SessionActivity{
					Policy:     settingsAutomaticPlaywrightPolicy,
					State:      browserctl.SessionActivityStateWaitingForUser,
					ServerName: "playwright",
				},
			},
		},
	}

	request := codexapp.ElicitationRequest{
		ID:      "elicitation_1",
		Mode:    codexapp.ElicitationModeURL,
		Message: "Please log in",
		URL:     "https://example.test/login",
	}

	renderedBlocks := strings.Join(m.renderCodexElicitationBlocks(request, 120), "\n")
	if !strings.Contains(renderedBlocks, "Press O to reveal the managed browser window, then finish the login flow and press Enter when you are done.") {
		t.Fatalf("renderCodexElicitationBlocks() missing managed login hint: %q", renderedBlocks)
	}

	footer := ansi.Strip(m.renderCodexFooter(codexapp.Snapshot{
		Provider:                 codexapp.ProviderCodex,
		Started:                  true,
		Status:                   "Browser needs attention",
		ManagedBrowserSessionKey: "managed-demo",
		BrowserActivity: browserctl.SessionActivity{
			Policy:     settingsAutomaticPlaywrightPolicy,
			State:      browserctl.SessionActivityStateWaitingForUser,
			ServerName: "playwright",
		},
		PendingElicitation: &request,
	}, 160))
	for _, want := range []string{"O show browser", "Enter done/accept", "d decline", "c cancel"} {
		if !strings.Contains(footer, want) {
			t.Fatalf("renderCodexFooter() missing %q for managed browser login: %q", want, footer)
		}
	}
}

func TestVisibleCodexCurrentBackgroundBrowserPageHintsOpenPage(t *testing.T) {
	now := time.Date(2026, 5, 30, 19, 10, 0, 0, time.UTC)
	snapshot := codexapp.Snapshot{
		Provider:                 codexapp.ProviderCodex,
		Started:                  true,
		Status:                   "Codex session ready",
		BrowserActivity:          browserctl.SessionActivity{Policy: settingsAutomaticPlaywrightPolicy},
		ManagedBrowserSessionKey: "managed-demo",
		CurrentBrowserPageURL:    "https://chartboost.us.auth0.com/u/login?state=demo",
	}

	m := Model{
		codexVisibleProject: "/tmp/demo",
		nowFn:               func() time.Time { return now },
		managedBrowserStates: map[string]browserctl.ManagedPlaywrightState{
			"managed-demo": {SessionKey: "managed-demo", BrowserPID: 123, Hidden: true, UpdatedAt: now},
		},
	}
	renderedBlocks := ansi.Strip(m.renderCodexBrowserPanel(snapshot, 120))
	if !strings.Contains(renderedBlocks, "Background browser page: https://chartboost.us.auth0.com/u/login?state=demo") {
		t.Fatalf("renderCodexBrowserPanel() missing current background page: %q", renderedBlocks)
	}
	if !strings.Contains(renderedBlocks, "Press ctrl+o to reveal the managed browser window for this same session.") {
		t.Fatalf("renderCodexBrowserPanel() missing ctrl+o reveal hint: %q", renderedBlocks)
	}

	footer := ansi.Strip(m.renderCodexFooter(snapshot, 160))
	if !strings.Contains(footer, "ctrl+o show browser") {
		t.Fatalf("renderCodexFooter() missing ctrl+o show browser action: %q", footer)
	}
}

func TestVisibleCodexCurrentBackgroundBrowserPageUsesVisibleBrowserCopyWhenCachedVisible(t *testing.T) {
	now := time.Date(2026, 5, 30, 19, 10, 0, 0, time.UTC)
	snapshot := codexapp.Snapshot{
		Provider:                 codexapp.ProviderCodex,
		Started:                  true,
		Status:                   "Codex session ready",
		BrowserActivity:          browserctl.SessionActivity{Policy: settingsAutomaticPlaywrightPolicy},
		ManagedBrowserSessionKey: "managed-demo",
		CurrentBrowserPageURL:    "https://chartboost.us.auth0.com/u/login?state=demo",
	}

	m := Model{
		codexVisibleProject: "/tmp/demo",
		nowFn:               func() time.Time { return now },
		managedBrowserStates: map[string]browserctl.ManagedPlaywrightState{
			"managed-demo": {
				SessionKey: "managed-demo",
				BrowserPID: 123,
				Hidden:     false,
				UpdatedAt:  now,
			},
		},
	}
	renderedBlocks := ansi.Strip(m.renderCodexBrowserPanel(snapshot, 120))
	if strings.Contains(renderedBlocks, "Press ctrl+o to reveal the managed browser window for this same session.") {
		t.Fatalf("renderCodexBrowserPanel() should hide stale ctrl+o reveal hint when browser is already visible: %q", renderedBlocks)
	}
	if !strings.Contains(renderedBlocks, "Managed browser page: https://chartboost.us.auth0.com/u/login?state=demo") {
		t.Fatalf("renderCodexBrowserPanel() missing managed browser page label: %q", renderedBlocks)
	}

	footer := ansi.Strip(m.renderCodexFooter(snapshot, 160))
	if !strings.Contains(footer, "ctrl+o focus browser") {
		t.Fatalf("renderCodexFooter() should show focus-browser action when browser is already visible: %q", footer)
	}
}

func TestVisibleCodexStaleResumedBrowserPageDoesNotRenderPersistentNoticeOrOfferReveal(t *testing.T) {
	session := &fakeCodexSession{
		projectPath: "/tmp/demo",
		snapshot: codexapp.Snapshot{
			Provider:                 codexapp.ProviderCodex,
			Started:                  true,
			ThreadID:                 "thread-demo",
			Preset:                   codexcli.PresetYolo,
			Status:                   "Codex session ready",
			BrowserActivity:          browserctl.SessionActivity{Policy: settingsAutomaticPlaywrightPolicy},
			ManagedBrowserSessionKey: "managed-demo",
			CurrentBrowserPageURL:    "https://kakaku.com/item/K0001687585/pricehistory/",
			CurrentBrowserPageStale:  true,
		},
	}
	manager := codexapp.NewManagerWithFactory(func(req codexapp.LaunchRequest, notify func()) (codexapp.Session, error) {
		return session, nil
	})
	if _, _, err := manager.Open(codexapp.LaunchRequest{
		ProjectPath: "/tmp/demo",
		Preset:      codexcli.PresetYolo,
	}); err != nil {
		t.Fatalf("manager.Open() error = %v", err)
	}

	m := Model{
		codexManager:        manager,
		codexVisibleProject: "/tmp/demo",
		codexHiddenProject:  "/tmp/demo",
		codexInput:          newCodexTextarea(),
		codexViewport:       viewport.New(0, 0),
		width:               100,
		height:              24,
	}

	renderedBlocks := ansi.Strip(m.renderCodexBrowserPanel(session.snapshot, 140))
	if strings.Contains(renderedBlocks, "Previous browser page is no longer attached") {
		t.Fatalf("renderCodexBrowserPanel() rendered stale browser page notice: %q", renderedBlocks)
	}
	if strings.Contains(renderedBlocks, "resumed transcript") {
		t.Fatalf("renderCodexBrowserPanel() rendered persistent stale browser page explanation: %q", renderedBlocks)
	}
	if strings.Contains(renderedBlocks, "Press ctrl+o to reveal the managed browser window for this same session.") {
		t.Fatalf("renderCodexBrowserPanel() offered stale ctrl+o reveal hint: %q", renderedBlocks)
	}
	footer := ansi.Strip(m.renderCodexFooter(session.snapshot, 160))
	if strings.Contains(footer, "ctrl+o show browser") || strings.Contains(footer, "ctrl+o focus browser") {
		t.Fatalf("renderCodexFooter() offered stale browser action: %q", footer)
	}

	updated, cmd := m.updateCodexMode(tea.KeyMsg{Type: tea.KeyCtrlO})
	got := updated.(Model)
	if cmd != nil {
		t.Fatalf("ctrl+o should not queue a browser-open command for stale resumed browser page")
	}
	if !strings.Contains(got.status, "came from the resumed transcript") {
		t.Fatalf("status = %q, want stale browser page guidance", got.status)
	}
}

func TestVisibleLCAgentFinishedBrowserPageDoesNotOfferExpiredReveal(t *testing.T) {
	now := time.Date(2026, 5, 30, 19, 10, 0, 0, time.UTC)
	snapshot := codexapp.Snapshot{
		Provider:                 codexapp.ProviderLCAgent,
		Started:                  true,
		Status:                   "LCAgent run complete",
		ManagedBrowserSessionKey: "managed-demo",
		CurrentBrowserPageURL:    "https://play.google.com/console/",
		BrowserActivity:          browserctl.SessionActivity{Policy: settingsAutomaticPlaywrightPolicy},
	}
	m := Model{
		codexVisibleProject: "/tmp/demo",
		nowFn:               func() time.Time { return now },
		managedBrowserStates: map[string]browserctl.ManagedPlaywrightState{
			"managed-demo": {
				SessionKey: "managed-demo",
				BrowserPID: 123,
				Hidden:     true,
				UpdatedAt:  now.Add(-managedBrowserStateFreshWindow - time.Second),
			},
		},
	}

	renderedBlocks := ansi.Strip(m.renderCodexBrowserPanel(snapshot, 140))
	if !strings.Contains(renderedBlocks, "Background browser page: https://play.google.com/console/") {
		t.Fatalf("renderCodexBrowserPanel() should still show last browser page: %q", renderedBlocks)
	}
	if strings.Contains(renderedBlocks, "Press ctrl+o") {
		t.Fatalf("renderCodexBrowserPanel() offered expired browser reveal: %q", renderedBlocks)
	}
	footer := ansi.Strip(m.renderCodexFooter(snapshot, 160))
	if strings.Contains(footer, "ctrl+o show browser") || strings.Contains(footer, "ctrl+o focus browser") {
		t.Fatalf("renderCodexFooter() offered expired browser action: %q", footer)
	}
}

func TestVisibleCodexBrowserPanelShowsReconnectHintForChangedBrowserSettings(t *testing.T) {
	settings := config.EditableSettings{PlaywrightPolicy: settingsAlwaysShowPlaywrightPolicy}
	snapshot := codexapp.Snapshot{
		Provider:                 codexapp.ProviderCodex,
		Started:                  true,
		Status:                   "Codex session ready",
		ManagedBrowserSessionKey: "managed-demo",
		BrowserActivity:          browserctl.SessionActivity{Policy: settingsAutomaticPlaywrightPolicy},
	}

	m := Model{
		codexVisibleProject: "/tmp/demo",
		settingsBaseline:    &settings,
	}

	rendered := ansi.Strip(m.renderCodexBrowserPanel(snapshot, 140))
	for _, want := range []string{
		"Session browser setting: Only when needed.",
		"Current browser setting: Always show.",
		"Use /reconnect to reopen this thread with the current browser behavior, or /codex-new for a fresh session.",
	} {
		if !strings.Contains(rendered, want) {
			t.Fatalf("renderCodexBrowserPanel() missing %q: %q", want, rendered)
		}
	}

	footer := ansi.Strip(m.renderCodexFooter(snapshot, 180))
	for _, want := range []string{"/reconnect apply browser", "/codex-new fresh"} {
		if !strings.Contains(footer, want) {
			t.Fatalf("renderCodexFooter() missing %q for browser policy mismatch: %q", want, footer)
		}
	}
}

func TestVisibleCodexBrowserPanelShowsReconnectHintWhenManagedBrowserNotAttached(t *testing.T) {
	session := &fakeCodexSession{
		projectPath: "/tmp/demo",
		snapshot: codexapp.Snapshot{
			Provider:              codexapp.ProviderCodex,
			Started:               true,
			ThreadID:              "thread-demo",
			Preset:                codexcli.PresetYolo,
			Status:                "Codex session ready",
			BrowserActivity:       browserctl.SessionActivity{Policy: settingsAutomaticPlaywrightPolicy},
			CurrentBrowserPageURL: "https://chartboost.us.auth0.com/u/login?state=demo",
		},
	}
	manager := codexapp.NewManagerWithFactory(func(req codexapp.LaunchRequest, notify func()) (codexapp.Session, error) {
		return session, nil
	})
	if _, _, err := manager.Open(codexapp.LaunchRequest{
		ProjectPath: "/tmp/demo",
		Preset:      codexcli.PresetYolo,
	}); err != nil {
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
		width:               100,
		height:              24,
	}

	rendered := ansi.Strip(m.renderCodexBrowserPanel(session.snapshot, 140))
	for _, want := range []string{
		"Managed browser controls are not attached to this session yet.",
		"Current browser setting: Only when needed.",
		"Use /reconnect to reopen this thread with the current browser behavior, or /codex-new for a fresh session.",
	} {
		if !strings.Contains(rendered, want) {
			t.Fatalf("renderCodexBrowserPanel() missing %q: %q", want, rendered)
		}
	}

	updated, cmd := m.updateCodexMode(tea.KeyMsg{Type: tea.KeyCtrlO})
	got := updated.(Model)
	if cmd != nil {
		t.Fatalf("ctrl+o should not queue a browser-open command when managed browser is not attached")
	}
	if !strings.Contains(got.status, "Use /reconnect to reopen this thread with the current browser behavior") {
		t.Fatalf("status = %q, want reconnect guidance", got.status)
	}
}

func TestVisibleOpenCodeCurrentBackgroundBrowserPageHintsOpenPage(t *testing.T) {
	now := time.Date(2026, 5, 30, 19, 10, 0, 0, time.UTC)
	snapshot := codexapp.Snapshot{
		Provider:                 codexapp.ProviderOpenCode,
		Started:                  true,
		Status:                   "OpenCode session ready",
		BrowserActivity:          browserctl.SessionActivity{Policy: settingsAutomaticPlaywrightPolicy},
		ManagedBrowserSessionKey: "managed-demo",
		CurrentBrowserPageURL:    "https://example.com/",
	}

	m := Model{
		codexVisibleProject: "/tmp/demo",
		nowFn:               func() time.Time { return now },
		managedBrowserStates: map[string]browserctl.ManagedPlaywrightState{
			"managed-demo": {SessionKey: "managed-demo", BrowserPID: 123, Hidden: true, UpdatedAt: now},
		},
	}
	renderedBlocks := ansi.Strip(m.renderCodexBrowserPanel(snapshot, 120))
	if !strings.Contains(renderedBlocks, "Background browser page: https://example.com/") {
		t.Fatalf("renderCodexBrowserPanel() missing current background page for OpenCode: %q", renderedBlocks)
	}
	if !strings.Contains(renderedBlocks, "Press ctrl+o to reveal the managed browser window for this same session.") {
		t.Fatalf("renderCodexBrowserPanel() missing ctrl+o reveal hint for OpenCode: %q", renderedBlocks)
	}

	footer := ansi.Strip(m.renderCodexFooter(snapshot, 160))
	if !strings.Contains(footer, "ctrl+o show browser") {
		t.Fatalf("renderCodexFooter() missing ctrl+o show browser action for OpenCode: %q", footer)
	}
}

func TestVisibleLCAgentCurrentBackgroundBrowserPageHintsOpenPage(t *testing.T) {
	now := time.Date(2026, 5, 30, 19, 10, 0, 0, time.UTC)
	snapshot := codexapp.Snapshot{
		Provider:                 codexapp.ProviderLCAgent,
		Started:                  true,
		Status:                   "LCAgent session ready",
		BrowserActivity:          browserctl.SessionActivity{Policy: settingsAutomaticPlaywrightPolicy},
		ManagedBrowserSessionKey: "managed-demo",
		CurrentBrowserPageURL:    "https://example.com/",
	}

	m := Model{
		codexVisibleProject: "/tmp/demo",
		nowFn:               func() time.Time { return now },
		managedBrowserStates: map[string]browserctl.ManagedPlaywrightState{
			"managed-demo": {SessionKey: "managed-demo", BrowserPID: 123, Hidden: true, UpdatedAt: now},
		},
	}
	renderedBlocks := ansi.Strip(m.renderCodexBrowserPanel(snapshot, 120))
	if !strings.Contains(renderedBlocks, "Background browser page: https://example.com/") {
		t.Fatalf("renderCodexBrowserPanel() missing current background page for LCAgent: %q", renderedBlocks)
	}
	if !strings.Contains(renderedBlocks, "Press ctrl+o to reveal the managed browser window for this same session.") {
		t.Fatalf("renderCodexBrowserPanel() missing ctrl+o reveal hint for LCAgent: %q", renderedBlocks)
	}

	footer := ansi.Strip(m.renderCodexFooter(snapshot, 160))
	if !strings.Contains(footer, "ctrl+o show browser") {
		t.Fatalf("renderCodexFooter() missing ctrl+o show browser action for LCAgent: %q", footer)
	}
}

func TestVisibleOpenCodeBrowserPanelShowsReconnectHintWhenManagedBrowserNotAttached(t *testing.T) {
	settings := config.EditableSettings{PlaywrightPolicy: settingsAutomaticPlaywrightPolicy}
	snapshot := codexapp.Snapshot{
		Provider:              codexapp.ProviderOpenCode,
		Started:               true,
		Status:                "OpenCode session ready",
		BrowserActivity:       browserctl.SessionActivity{Policy: settingsAutomaticPlaywrightPolicy},
		CurrentBrowserPageURL: "https://example.com/",
	}

	m := Model{
		codexVisibleProject: "/tmp/demo",
		settingsBaseline:    &settings,
	}

	rendered := ansi.Strip(m.renderCodexBrowserPanel(snapshot, 140))
	for _, want := range []string{
		"Managed browser controls are not attached to this session yet.",
		"Current browser setting: Only when needed.",
		"Use /reconnect to reopen this thread with the current browser behavior, or /opencode-new for a fresh session.",
	} {
		if !strings.Contains(rendered, want) {
			t.Fatalf("renderCodexBrowserPanel() missing %q for OpenCode: %q", want, rendered)
		}
	}

	footer := ansi.Strip(m.renderCodexFooter(snapshot, 180))
	for _, want := range []string{"/reconnect apply browser", "/opencode-new fresh"} {
		if !strings.Contains(footer, want) {
			t.Fatalf("renderCodexFooter() missing %q for OpenCode browser policy mismatch: %q", want, footer)
		}
	}
}

func TestVisibleOpenCodePendingToolInputKeepsShowBrowserAction(t *testing.T) {
	now := time.Date(2026, 5, 30, 19, 10, 0, 0, time.UTC)
	snapshot := codexapp.Snapshot{
		Provider:                 codexapp.ProviderOpenCode,
		Started:                  true,
		Status:                   "Browser needs attention",
		ManagedBrowserSessionKey: "managed-demo",
		BrowserActivity: browserctl.SessionActivity{
			Policy:     settingsAutomaticPlaywrightPolicy,
			State:      browserctl.SessionActivityStateWaitingForUser,
			ServerName: "playwright",
			ToolName:   "browser_navigate",
		},
		CurrentBrowserPageURL: "https://example.com/",
		PendingToolInput: &codexapp.ToolInputRequest{
			ID: "question_1",
			Questions: []codexapp.ToolInputQuestion{{
				ID:       "answer",
				Question: "Finish the sign-in flow and confirm when ready.",
			}},
		},
	}

	m := Model{
		codexVisibleProject: "/tmp/demo",
		nowFn:               func() time.Time { return now },
		managedBrowserStates: map[string]browserctl.ManagedPlaywrightState{
			"managed-demo": {SessionKey: "managed-demo", BrowserPID: 123, Hidden: true, UpdatedAt: now},
		},
	}
	footer := ansi.Strip(m.renderCodexFooter(snapshot, 180))
	for _, want := range []string{"Enter answer", "ctrl+o show browser"} {
		if !strings.Contains(footer, want) {
			t.Fatalf("renderCodexFooter() missing %q for OpenCode pending browser question: %q", want, footer)
		}
	}
}
