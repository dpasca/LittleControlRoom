package tui

import (
	"errors"
	"strings"
	"testing"

	"lcroom/internal/claudecli"
	"lcroom/internal/commands"
	"lcroom/internal/config"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
	"github.com/charmbracelet/x/ansi"
)

func TestClaudeAuthCommandAndSettingsOpenMaskedDialog(t *testing.T) {
	inv, err := commands.Parse("/claude-auth")
	if err != nil {
		t.Fatal(err)
	}
	if host, ok := codexHostSlashCommand("/claude-auth"); !ok || host.Kind != commands.KindClaudeAuth {
		t.Fatal("Claude connection is unavailable inside an embedded pane")
	}
	if _, err := commands.Parse("/claude-auth secret"); err == nil || strings.Contains(err.Error(), "secret") {
		t.Fatal("slash command accepted a credential or echoed it in an error")
	}
	for _, fromSettings := range []bool{false, true} {
		m := Model{width: 100, height: 30}
		var model tea.Model
		var cmd tea.Cmd
		if fromSettings {
			m.settingsFields = newSettingsFields(config.EditableSettings{})
			model, cmd = m.openSettingsPickerForField(settingsFieldClaudeAuth)
		} else {
			model, cmd = m.dispatchCommand(inv)
		}
		got := model.(Model)
		if cmd == nil || got.claudeAuthDialog == nil || !got.claudeAuthDialog.Busy || !got.claudeAuthDialog.Input.Focused() {
			t.Fatal("connection dialog did not queue an async refresh and focus its input")
		}
		// Do not run a real credential-store read in a UI test.
		model, _ = got.applyClaudeAuthResult(claudeAuthResultMsg{info: claudecli.SubscriptionTokenInfo{Storage: "test storage"}})
		got = model.(Model)
		model, _ = got.update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("sk-ant-oat01-secret-fixture"), Paste: true})
		got = model.(Model)
		if got.claudeAuthDialog.Input.Value() != "sk-ant-oat01-secret-fixture" {
			t.Fatal("token input was not routed to the connection dialog")
		}
		for _, size := range [][2]int{{80, 24}, {120, 32}} {
			body := strings.Repeat(strings.Repeat(" ", size[0])+"\n", size[1]-1)
			rendered := ansi.Strip(got.renderClaudeAuthOverlay(body, size[0], size[1]))
			if strings.Contains(rendered, "secret-fixture") || strings.Contains(rendered, "sk-ant-oat01") {
				t.Fatal("token leaked into a rendered or recorded frame")
			}
			if !strings.Contains(rendered, "Esc") || !strings.Contains(rendered, "ctrl+g") || !strings.Contains(rendered, "save token") {
				t.Fatalf("connection actions clipped at %dx%d: %q", size[0], size[1], rendered)
			}
			if lipgloss.Height(rendered) > size[1] {
				t.Fatalf("connection dialog exceeds terminal height at %dx%d", size[0], size[1])
			}
		}
	}
}

func TestClaudeAuthSaveClearsInputAndIgnoresRepeatWhileBusy(t *testing.T) {
	model, _ := (Model{}).openClaudeAuthDialog()
	m := model.(Model)
	m.claudeAuthDialog.Busy = false
	m.claudeAuthDialog.Input.SetValue("sk-ant-oat01-secret-fixture")
	model, saveCmd := m.updateClaudeAuthDialog(tea.KeyMsg{Type: tea.KeyEnter})
	m = model.(Model)
	if saveCmd == nil || !m.claudeAuthDialog.Busy || m.claudeAuthDialog.Input.Value() != "" {
		t.Fatal("save did not enter busy state and clear its secret input")
	}
	for _, key := range []tea.KeyType{tea.KeyEnter, tea.KeyCtrlD, tea.KeyCtrlG, tea.KeyCtrlR, tea.KeyEsc} {
		_, cmd := m.updateClaudeAuthDialog(tea.KeyMsg{Type: key})
		if cmd != nil || m.claudeAuthDialog == nil {
			t.Fatal("busy connection action was repeated or dismissed")
		}
	}
	model, _ = m.applyClaudeAuthResult(claudeAuthResultMsg{err: errors.New("Keychain is locked")})
	m = model.(Model)
	if m.claudeAuthDialog.Busy || m.claudeAuthDialog.Status != "Keychain is locked" {
		t.Fatal("storage failure left a loading state or hid the error")
	}
	model, _ = m.updateClaudeAuthDialog(tea.KeyMsg{Type: tea.KeyEsc})
	if model.(Model).claudeAuthDialog != nil {
		t.Fatal("connection dialog did not close")
	}
}
