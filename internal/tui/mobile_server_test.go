package tui

import (
	"strings"
	"testing"

	"lcroom/internal/commands"

	"github.com/charmbracelet/x/ansi"
)

func TestMobileCommandReportsRunningURL(t *testing.T) {
	m := Model{}
	m.SetMobileServerStatus(MobileServerStatus{
		URL:           "http://127.0.0.1:7777",
		ListenAddress: "127.0.0.1:7777",
	})

	updated, cmd := m.dispatchCommand(commands.Invocation{Kind: commands.KindMobile})
	got := updated.(Model)
	if cmd != nil {
		t.Fatal("/mobile should not queue background work")
	}
	if got.status != "Mobile client available at http://127.0.0.1:7777" {
		t.Fatalf("status = %q", got.status)
	}
	rendered := ansi.Strip(got.renderTopStatusLine(80))
	if !strings.Contains(rendered, "http://127.0.0.1:7777") {
		t.Fatalf("80-column top status = %q, want the complete mobile URL", rendered)
	}
}

func TestMobileCommandReportsLANPairingCode(t *testing.T) {
	m := Model{}
	m.SetMobileServerStatus(MobileServerStatus{
		URL:           "http://192.168.1.20:7777",
		ListenAddress: "192.168.1.20:7777",
		PairingCode:   "123 456",
		AuthRequired:  true,
	})

	updated, cmd := m.dispatchCommand(commands.Invocation{Kind: commands.KindMobile})
	got := updated.(Model)
	if cmd != nil {
		t.Fatal("/mobile should not queue background work")
	}
	for _, want := range []string{"http://192.168.1.20:7777", "pairing code 123 456"} {
		if !strings.Contains(got.status, want) {
			t.Fatalf("status = %q, want %q", got.status, want)
		}
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
