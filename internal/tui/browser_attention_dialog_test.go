package tui

import (
	"errors"
	"strings"
	"testing"
	"time"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/x/ansi"

	"lcroom/internal/browserctl"
	"lcroom/internal/codexapp"
	"lcroom/internal/config"
)

func browserAttentionDialogSnapshot(projectPath string) codexapp.Snapshot {
	return codexapp.Snapshot{
		Started:     true,
		ProjectPath: projectPath,
		ThreadID:    "thread-demo",
		Provider:    codexapp.ProviderCodex,
		BrowserActivity: browserctl.SessionActivity{
			Policy:           settingsAutomaticPlaywrightPolicy,
			State:            browserctl.SessionActivityStateWaitingForUser,
			ServerName:       "playwright",
			ToolName:         "browser_navigate",
			AttentionMessage: "Complete the account sign-in in the managed browser, then return here.",
		},
		ManagedBrowserSessionKey: "managed-demo",
		CurrentBrowserPageURL:    "https://example.test/login",
	}
}

func TestBrowserAttentionDialogSurfacesInsideVisibleSession(t *testing.T) {
	const projectPath = "/tmp/demo"
	m := Model{
		codexVisibleProject:          projectPath,
		browserAttentionAcknowledged: make(map[string]string),
	}

	m.detectBrowserAttentionNotification(projectPath, browserAttentionDialogSnapshot(projectPath))
	if m.browserAttention == nil {
		t.Fatal("visible browser wait should open the browser attention dialog")
	}
	rendered := ansi.Strip(m.renderBrowserAttentionContent(76))
	for _, want := range []string{"Browser needs attention", "Complete the account sign-in", "return to session", "browser settings", "ctrl+o"} {
		if !strings.Contains(rendered, want) {
			t.Fatalf("visible browser attention dialog is missing %q:\n%s", want, rendered)
		}
	}
	if strings.Contains(rendered, "playwright/browser_navigate is waiting for user input") {
		t.Fatalf("structured browser instruction should replace the generic wait summary:\n%s", rendered)
	}

	updated, cmd := m.update(tea.KeyMsg{Type: tea.KeyEsc})
	got := updated.(Model)
	if cmd != nil {
		t.Fatalf("dismissing browser attention queued an unexpected command")
	}
	if got.browserAttention != nil {
		t.Fatal("Esc should dismiss browser attention before the embedded composer handles it")
	}
	if got.codexVisibleProject != projectPath {
		t.Fatalf("visible project = %q, want session to remain open", got.codexVisibleProject)
	}
}

func TestBrowserAttentionDialogRendersOverVisibleSession(t *testing.T) {
	const projectPath = "/tmp/demo"
	m := testEmbeddedSidebarModel(projectPath)
	snapshot := browserAttentionDialogSnapshot(projectPath)
	m.codexSnapshots[projectPath] = snapshot
	m.browserAttentionAcknowledged = make(map[string]string)
	m.detectBrowserAttentionNotification(projectPath, snapshot)

	rendered := ansi.Strip(m.View())
	if !strings.Contains(rendered, "Browser needs attention") || !strings.Contains(rendered, "return to session") {
		t.Fatalf("visible embedded view should render the browser attention overlay:\n%s", rendered)
	}
}

func TestBrowserAttentionDialogAcknowledgementLastsUntilWaitResolves(t *testing.T) {
	const projectPath = "/tmp/demo"
	snapshot := browserAttentionDialogSnapshot(projectPath)
	m := Model{browserAttentionAcknowledged: make(map[string]string)}

	m.detectBrowserAttentionNotification(projectPath, snapshot)
	if m.browserAttention == nil {
		t.Fatal("initial browser wait should open a dialog")
	}
	m.dismissBrowserAttentionNotification()
	m.detectBrowserAttentionNotification(projectPath, snapshot)
	if m.browserAttention != nil {
		t.Fatal("an unchanged browser wait should not reopen after acknowledgement")
	}

	resolved := snapshot
	resolved.BrowserActivity.State = browserctl.SessionActivityStateIdle
	m.detectBrowserAttentionNotification(projectPath, resolved)
	if _, ok := m.browserAttentionAcknowledged[normalizeProjectPath(projectPath)]; ok {
		t.Fatal("resolving the browser wait should clear its acknowledgement")
	}

	m.detectBrowserAttentionNotification(projectPath, snapshot)
	if m.browserAttention == nil {
		t.Fatal("a browser wait after a resolved transition should open a fresh dialog")
	}
}

func TestBrowserAttentionAcknowledgementSurvivesTransientRevealUnavailability(t *testing.T) {
	const projectPath = "/tmp/demo"
	snapshot := browserAttentionDialogSnapshot(projectPath)
	snapshot.Provider = codexapp.ProviderOpenCode
	projectKey := normalizeProjectPath(projectPath)
	m := Model{browserAttentionAcknowledged: map[string]string{projectKey: "acknowledged-wait"}}

	m.detectBrowserAttentionNotification(projectPath, snapshot)
	if got := m.browserAttentionAcknowledged[projectKey]; got != "acknowledged-wait" {
		t.Fatalf("transient reveal unavailability cleared acknowledgement: %q", got)
	}

	snapshot.BrowserActivity.State = browserctl.SessionActivityStateIdle
	m.detectBrowserAttentionNotification(projectPath, snapshot)
	if _, ok := m.browserAttentionAcknowledged[projectKey]; ok {
		t.Fatal("resolved wait should clear acknowledgement after transient unavailability")
	}
}

func TestBrowserAttentionDialogNewStructuredInstructionReopensWait(t *testing.T) {
	const projectPath = "/tmp/demo"
	snapshot := browserAttentionDialogSnapshot(projectPath)
	m := Model{browserAttentionAcknowledged: make(map[string]string)}

	m.detectBrowserAttentionNotification(projectPath, snapshot)
	m.dismissBrowserAttentionNotification()
	snapshot.BrowserActivity.AttentionMessage = "Approve the security prompt in the managed browser."
	m.detectBrowserAttentionNotification(projectPath, snapshot)
	if m.browserAttention == nil {
		t.Fatal("a changed structured browser instruction should surface as a new handoff")
	}
	if got := m.browserAttention.AttentionMessage; got != snapshot.BrowserActivity.AttentionMessage {
		t.Fatalf("attention message = %q, want updated structured instruction", got)
	}
}

func TestBrowserAttentionDialogDoesNotDuplicateVisibleURLElicitation(t *testing.T) {
	const projectPath = "/tmp/demo"
	snapshot := browserAttentionDialogSnapshot(projectPath)
	snapshot.PendingElicitation = &codexapp.ElicitationRequest{
		ID:         "elicitation-demo",
		Mode:       codexapp.ElicitationModeURL,
		ServerName: "playwright",
		URL:        snapshot.CurrentBrowserPageURL,
	}
	m := Model{
		codexVisibleProject:          projectPath,
		browserAttentionAcknowledged: make(map[string]string),
	}

	m.detectBrowserAttentionNotification(projectPath, snapshot)
	if m.browserAttention != nil {
		t.Fatal("the browser attention dialog should not cover the visible URL elicitation dialog")
	}
	if m.browserAttentionAcknowledged[normalizeProjectPath(projectPath)] == "" {
		t.Fatal("the visible URL elicitation should acknowledge the same browser wait")
	}

	snapshot.PendingElicitation = nil
	m.detectBrowserAttentionNotification(projectPath, snapshot)
	if m.browserAttention != nil {
		t.Fatal("the same wait should not produce a duplicate dialog after URL elicitation")
	}
}

func TestBrowserAttentionDialogUsesFocusLabelForVisibleManagedWindow(t *testing.T) {
	const projectPath = "/tmp/demo"
	now := time.Date(2026, 7, 16, 12, 0, 0, 0, time.UTC)
	m := Model{
		codexVisibleProject: projectPath,
		nowFn:               func() time.Time { return now },
		managedBrowserStates: map[string]browserctl.ManagedPlaywrightState{
			"managed-demo": {
				SessionKey: "managed-demo",
				BrowserPID: 123,
				Hidden:     false,
				UpdatedAt:  now,
			},
		},
		browserAttention: &browserAttentionNotification{
			ProjectPath:              projectPath,
			ProjectName:              "demo",
			Provider:                 codexapp.ProviderCodex,
			Activity:                 browserctl.SessionActivity{State: browserctl.SessionActivityStateWaitingForUser},
			ManagedBrowserSessionKey: "managed-demo",
		},
	}

	rendered := ansi.Strip(m.renderBrowserAttentionContent(76))
	if !strings.Contains(rendered, "focus browser") {
		t.Fatalf("visible managed browser should use a focus action:\n%s", rendered)
	}
}

func TestBrowserAttentionSettingsActionHidesLiveSessionBeforeOpeningSettings(t *testing.T) {
	const projectPath = "/tmp/demo"
	settings := config.EditableSettingsFromAppConfig(config.Default())
	m := testEmbeddedSidebarModel(projectPath)
	m.settingsBaseline = &settings
	m.browserAttention = &browserAttentionNotification{
		ProjectPath:              projectPath,
		ProjectName:              "demo",
		Provider:                 codexapp.ProviderCodex,
		SessionID:                "thread-demo",
		Activity:                 browserAttentionDialogSnapshot(projectPath).BrowserActivity,
		ManagedBrowserSessionKey: "managed-demo",
	}

	updated, _ := m.updateBrowserAttentionMode(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("b")})
	got := updated.(Model)
	if got.browserAttention != nil {
		t.Fatal("opening Browser settings should acknowledge the attention dialog")
	}
	if got.codexVisibleProject != "" || got.codexHiddenProject != projectPath {
		t.Fatalf("embedded session should be hidden, not closed: visible=%q hidden=%q", got.codexVisibleProject, got.codexHiddenProject)
	}
	if !got.settingsMode || got.activeSettingsSection().id != settingsSectionBrowser {
		t.Fatal("Browser settings should be visible after leaving the embedded pane")
	}
}

func TestManagedBrowserHydrationSurfacesDeferredBrowserAttention(t *testing.T) {
	for _, provider := range []codexapp.Provider{codexapp.ProviderOpenCode, codexapp.ProviderLCAgent} {
		t.Run(string(provider), func(t *testing.T) {
			const projectPath = "/tmp/demo"
			snapshot := browserAttentionDialogSnapshot(projectPath)
			snapshot.Provider = provider
			m := Model{
				codexSnapshots:               map[string]codexapp.Snapshot{projectPath: snapshot},
				browserAttentionAcknowledged: make(map[string]string),
			}

			m.detectBrowserAttentionNotification(projectPath, snapshot)
			if m.browserAttention != nil {
				t.Fatal("browser attention should wait until the managed window is hydrated")
			}

			updated, cmd := m.update(managedBrowserStateMsg{
				sessionKey: snapshot.ManagedBrowserSessionKey,
				state: browserctl.ManagedPlaywrightState{
					SessionKey:      snapshot.ManagedBrowserSessionKey,
					Provider:        string(provider),
					ProjectPath:     projectPath,
					BrowserPID:      4242,
					RevealSupported: true,
					UpdatedAt:       time.Now(),
				},
			})
			got := updated.(Model)
			if cmd != nil {
				t.Fatalf("managed browser hydration queued an unexpected command")
			}
			if got.browserAttention == nil {
				t.Fatal("managed browser hydration should surface the deferred attention dialog")
			}
			if got.browserAttention.ProjectPath != projectPath {
				t.Fatalf("browser attention project = %q, want %q", got.browserAttention.ProjectPath, projectPath)
			}
		})
	}
}

func TestBrowserAttentionDialogDefersToForegroundEmbeddedInput(t *testing.T) {
	const projectPath = "/tmp/demo"
	snapshot := browserAttentionDialogSnapshot(projectPath)
	snapshot.PendingToolInput = &codexapp.ToolInputRequest{
		ID: "question-demo",
		Questions: []codexapp.ToolInputQuestion{{
			ID:       "answer",
			Question: "Confirm the account choice.",
		}},
	}
	m := testEmbeddedSidebarModel(projectPath)
	m.codexSnapshots[projectPath] = snapshot
	m.browserAttention = &browserAttentionNotification{ProjectPath: "/tmp/another", ProjectName: "another"}

	if m.browserAttentionDialogCanTakeFocus() {
		t.Fatal("browser attention should wait behind foreground embedded input")
	}
	rendered := ansi.Strip(m.View())
	if strings.Contains(rendered, "Browser needs attention") {
		t.Fatalf("browser attention should not cover foreground embedded input:\n%s", rendered)
	}
}

func TestManagedBrowserRevealFailureRestoresActionableDialog(t *testing.T) {
	const projectPath = "/tmp/demo"
	snapshot := browserAttentionDialogSnapshot(projectPath)
	m := Model{
		codexVisibleProject:          projectPath,
		codexSnapshots:               map[string]codexapp.Snapshot{projectPath: snapshot},
		browserAttentionAcknowledged: map[string]string{normalizeProjectPath(projectPath): "previous"},
	}

	updated, cmd := m.update(browserOpenMsg{
		projectPath:              projectPath,
		err:                      errors.New("activation failed"),
		managedBrowserSessionKey: snapshot.ManagedBrowserSessionKey,
		managedBrowserRef: browserctl.SessionRef{
			Provider:    string(codexapp.ProviderCodex),
			ProjectPath: projectPath,
			SessionID:   snapshot.ThreadID,
		},
	})
	got := updated.(Model)
	if cmd != nil {
		t.Fatalf("browser reveal failure queued an unexpected command")
	}
	if got.browserAttention == nil {
		t.Fatal("browser reveal failure should reopen an actionable browser dialog")
	}
	rendered := ansi.Strip(got.renderBrowserAttentionContent(76))
	for _, want := range []string{"activation failed", "retry browser", "browser settings", "return to session"} {
		if !strings.Contains(rendered, want) {
			t.Fatalf("browser failure dialog is missing %q:\n%s", want, rendered)
		}
	}
}

func TestBrowserAttentionInstructionRemainsInSidebarAndCurrentPagePanel(t *testing.T) {
	const projectPath = "/tmp/demo"
	snapshot := browserAttentionDialogSnapshot(projectPath)
	m := Model{codexVisibleProject: projectPath}

	sidebar := ansi.Strip(strings.Join(m.embeddedSidebarBrowserDetailRows(snapshot, 76), "\n"))
	if !strings.Contains(sidebar, "Needed") || !strings.Contains(sidebar, "Complete the account sign-in") {
		t.Fatalf("Browser sidebar detail should retain the structured instruction:\n%s", sidebar)
	}
	panel := ansi.Strip(m.renderCodexBrowserPanel(snapshot, 120))
	if !strings.Contains(panel, "Complete the account sign-in") {
		t.Fatalf("current-page browser panel should retain the structured instruction:\n%s", panel)
	}
}

func TestBrowserAttentionStructuredInstructionReplacesGenericDetailSummary(t *testing.T) {
	const projectPath = "/tmp/demo"
	snapshot := browserAttentionDialogSnapshot(projectPath)
	state, ok := browserAttentionFromSnapshot(snapshot)
	if !ok {
		t.Fatal("test snapshot should contain browser attention")
	}

	detail := browserAttentionDetailSummary(state)
	if !strings.Contains(detail, "Complete the account sign-in") || strings.Contains(detail, "waiting for user input") {
		t.Fatalf("project detail should prefer the structured instruction: %q", detail)
	}
	notice := bossBrowserAttentionHostNoticeForSnapshot(projectPath, false, codexapp.Snapshot{}, snapshot)
	if !strings.Contains(notice, "Complete the account sign-in") || strings.Contains(notice, "waiting for user input") {
		t.Fatalf("Chat host notice should prefer the structured instruction: %q", notice)
	}
}
