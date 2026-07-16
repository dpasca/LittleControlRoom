package tui

import (
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"
)

func TestParseTmuxTerminalHealthMatchesCurrentTTY(t *testing.T) {
	snapshot, err := parseTmuxTerminalHealth(strings.Join([]string{
		"/dev/ttys009\t1\t0\t%5",
		"/dev/ttys010\t0\t1\t%6",
	}, "\n"), "/dev/ttys010")
	if err != nil {
		t.Fatalf("parse terminal health: %v", err)
	}
	if !snapshot.Supported || snapshot.PaneID != "%6" {
		t.Fatalf("snapshot identity = %#v", snapshot)
	}
	if snapshot.Healthy() || snapshot.AlternateScreen || snapshot.CursorHidden {
		t.Fatalf("damaged pane reported healthy: %#v", snapshot)
	}
}

func TestParseTmuxTerminalHealthReportsHealthyModes(t *testing.T) {
	snapshot, err := parseTmuxTerminalHealth("/dev/ttys010\t1\t0\t%6\n", "/dev/ttys010")
	if err != nil {
		t.Fatalf("parse terminal health: %v", err)
	}
	if !snapshot.Healthy() {
		t.Fatalf("healthy pane reported damaged: %#v", snapshot)
	}
}

func TestDamagedTerminalHealthQueuesRepairAndNotifiesUser(t *testing.T) {
	m := Model{terminalHealthCheckInFlight: true, mouseEnabled: true}
	updated, cmd := m.applyTerminalHealthCheckMsg(terminalHealthCheckMsg{snapshot: terminalHealthSnapshot{
		Supported:       true,
		PaneID:          "%6",
		AlternateScreen: false,
		CursorHidden:    false,
	}})
	got := updated.(Model)
	if cmd == nil {
		t.Fatal("damaged terminal should queue an automatic repair")
	}
	if got.terminalHealthCheckInFlight || !got.terminalRepairInFlight {
		t.Fatalf("terminal state flags = check:%t repair:%t", got.terminalHealthCheckInFlight, got.terminalRepairInFlight)
	}
	if !strings.Contains(got.status, "repairing") {
		t.Fatalf("status = %q, want repair notice", got.status)
	}
	if len(got.errorLogEntries) != 1 || !strings.Contains(got.errorLogEntries[0].Message, "alternate screen is disabled") {
		t.Fatalf("terminal repair error log = %#v", got.errorLogEntries)
	}
}

func TestHealthyTerminalHealthDoesNotInterruptUI(t *testing.T) {
	m := Model{terminalHealthCheckInFlight: true, status: "Working"}
	updated, cmd := m.applyTerminalHealthCheckMsg(terminalHealthCheckMsg{snapshot: terminalHealthSnapshot{
		Supported:       true,
		AlternateScreen: true,
		CursorHidden:    true,
	}})
	got := updated.(Model)
	if cmd != nil || got.terminalHealthCheckInFlight || got.status != "Working" {
		t.Fatalf("healthy terminal update = cmd:%v in-flight:%t status:%q", cmd != nil, got.terminalHealthCheckInFlight, got.status)
	}
}

func TestCtrlLQueuesManualTerminalRepair(t *testing.T) {
	updated, cmd := (Model{}).Update(tea.KeyMsg{Type: tea.KeyCtrlL})
	got := updated.(Model)
	if cmd == nil || !got.terminalRepairInFlight {
		t.Fatalf("ctrl+l repair = cmd:%v in-flight:%t", cmd != nil, got.terminalRepairInFlight)
	}
	if !strings.Contains(got.status, "Reinitializing terminal") {
		t.Fatalf("status = %q", got.status)
	}
}

func TestTerminalRepairCompletionKeepsRestartFallbackVisible(t *testing.T) {
	updated, cmd := (Model{terminalRepairInFlight: true}).applyTerminalRepairFinishedMsg(terminalRepairFinishedMsg{automatic: true})
	got := updated.(Model)
	if cmd != nil || got.terminalRepairInFlight {
		t.Fatalf("repair completion = cmd:%v in-flight:%t", cmd != nil, got.terminalRepairInFlight)
	}
	if !strings.Contains(got.status, "Restart LCR") {
		t.Fatalf("status = %q, want restart fallback", got.status)
	}
}
