package tui

import (
	"strings"
	"testing"

	"lcroom/internal/commands"
	"lcroom/internal/config"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/x/ansi"
)

func TestMobileCommandOpensLocalOnlyStatusPanel(t *testing.T) {
	m := Model{}
	m.SetMobileServerStatus(MobileServerStatus{
		URL:           "http://127.0.0.1:7777",
		ListenAddress: "127.0.0.1:7777",
		LANAddresses:  []string{"192.168.1.20"},
	})

	updated, cmd := m.dispatchCommand(commands.Invocation{Kind: commands.KindMobile})
	got := updated.(Model)
	if cmd != nil {
		t.Fatal("/mobile should not queue background work")
	}
	if !got.mobileDialogOpen {
		t.Fatal("/mobile should open the mobile access panel")
	}
	panel := ansi.Strip(got.renderMobileDialogPanel(80, 24))
	for _, want := range []string{
		"Mobile Access",
		"Local only",
		"http://127.0.0.1:7777",
		"127.0.0.1 accepts",
		"connections only from this computer",
		"Not reachable from the LAN",
		"192.168.1.20",
		"http://192.168.1.20:7777",
		"Open Mobile Setup",
	} {
		if !strings.Contains(panel, want) {
			t.Fatalf("mobile panel = %q, want %q", panel, want)
		}
	}
	header := ansi.Strip(got.renderTopStatusLine(80))
	if !strings.Contains(header, "/mobile") || strings.Contains(header, "SETUP") {
		t.Fatalf("80-column top status = %q, want compact /mobile indicator", header)
	}
}

func TestMobileCommandShowsLANPhoneURLAndPairingCode(t *testing.T) {
	m := Model{}
	m.SetMobileServerStatus(MobileServerStatus{
		URL:           "http://0.0.0.0:7777",
		ListenAddress: "0.0.0.0:0",
		BoundAddress:  "0.0.0.0:7777",
		LANAddresses:  []string{"192.168.1.20", "10.0.0.12"},
		PairingCode:   "123 456",
		AuthRequired:  true,
	})

	updated, cmd := m.dispatchCommand(commands.Invocation{Kind: commands.KindMobile})
	got := updated.(Model)
	if cmd != nil {
		t.Fatal("/mobile should not queue background work")
	}
	panel := ansi.Strip(got.renderMobileDialogPanel(88, 26))
	for _, want := range []string{"LAN ready", "Technical listener: 0.0.0.0:7777", "0.0.0.0 accepts connections", "http://192.168.1.20:7777", "http://10.0.0.12:7777", "Required - code 123 456", "Trusted LAN only"} {
		if !strings.Contains(panel, want) {
			t.Fatalf("mobile panel = %q, want %q", panel, want)
		}
	}
	header := ansi.Strip(got.renderTopStatusLine(100))
	if !strings.Contains(header, "/mobile LAN") {
		t.Fatalf("top status = %q, want /mobile LAN indicator", header)
	}
}

func TestMobileCommandShowsDisabledSetupGuidance(t *testing.T) {
	settings := config.EditableSettingsFromAppConfig(config.Default())
	settings.MobileEnabled = false
	m := Model{settingsBaseline: &settings}
	m.SetMobileServerStatus(MobileServerStatus{
		ListenAddress: "127.0.0.1:7777",
		LANAddresses:  []string{"192.168.1.20"},
		Disabled:      true,
	})

	updated, cmd := m.dispatchCommand(commands.Invocation{Kind: commands.KindMobile})
	got := updated.(Model)
	if cmd != nil {
		t.Fatal("/mobile should not queue background work")
	}
	panel := ansi.Strip(got.renderMobileDialogPanel(80, 24))
	for _, want := range []string{"Disabled", "Not running", "http://192.168.1.20:7777", "enable the interface", "choose Phones on this", "LAN, save"} {
		if !strings.Contains(panel, want) {
			t.Fatalf("mobile panel = %q, want %q", panel, want)
		}
	}
	header := ansi.Strip(got.renderTopStatusLine(80))
	if !strings.Contains(header, "/mobile") || strings.Contains(header, "OFF") {
		t.Fatalf("top status = %q, want compact /mobile indicator", header)
	}
}

func TestMobileDialogEnterOpensAuthoritativeSetupDrilldown(t *testing.T) {
	settings := config.EditableSettingsFromAppConfig(config.Default())
	m := Model{
		mobileDialogOpen: true,
		settingsBaseline: &settings,
	}

	updated, cmd := m.updateMobileDialogMode(tea.KeyMsg{Type: tea.KeyEnter})
	got := updated.(Model)
	if cmd == nil {
		t.Fatal("opening Mobile Setup should return field focus commands")
	}
	if got.mobileDialogOpen {
		t.Fatal("mobile dialog should close before opening settings")
	}
	if !got.settingsMode || got.settingsDrilldown != settingsDrilldownMobile {
		t.Fatalf("settings state = mode %v drilldown %q, want Mobile setup", got.settingsMode, got.settingsDrilldown)
	}
	if got.settingsSelected != settingsFieldMobileEnabled {
		t.Fatalf("settings selection = %d, want mobile enabled field %d", got.settingsSelected, settingsFieldMobileEnabled)
	}
}

func TestMobileDialogCopiesReachablePhoneURL(t *testing.T) {
	previousWriter := clipboardTextWriter
	var copied string
	clipboardTextWriter = func(value string) error {
		copied = value
		return nil
	}
	t.Cleanup(func() { clipboardTextWriter = previousWriter })

	m := Model{mobileDialogOpen: true}
	m.SetMobileServerStatus(MobileServerStatus{
		URL:           "http://0.0.0.0:7777",
		ListenAddress: "0.0.0.0:7777",
		LANAddresses:  []string{"192.168.1.20"},
		AuthRequired:  true,
		PairingCode:   "123 456",
	})

	updated, _ := m.updateMobileDialogMode(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'c'}})
	got := updated.(Model)
	if copied != "http://192.168.1.20:7777" {
		t.Fatalf("copied URL = %q", copied)
	}
	if got.status != "Phone URL copied to clipboard" {
		t.Fatalf("status = %q", got.status)
	}
}

func TestMobileBindFailureRemainsVisibleInTopStatus(t *testing.T) {
	m := Model{status: "Projects loaded"}
	m.SetMobileServerStatus(MobileServerStatus{
		ListenAddress: "127.0.0.1:7777",
		Error:         "listen tcp 127.0.0.1:7777: bind: address already in use",
	})

	rendered := ansi.Strip(m.renderTopStatusLine(240))
	for _, want := range []string{
		"Projects loaded",
		"Mobile client unavailable at http://127.0.0.1:7777",
		"address already in use",
	} {
		if !strings.Contains(rendered, want) {
			t.Fatalf("top status = %q, want %q", rendered, want)
		}
	}
}
