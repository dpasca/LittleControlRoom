package tui

import (
	"context"
	"path/filepath"
	"strings"
	"testing"
	"time"

	tea "github.com/charmbracelet/bubbletea"

	"lcroom/internal/config"
	"lcroom/internal/control"
	"lcroom/internal/events"
	"lcroom/internal/service"
	"lcroom/internal/store"
)

func wakeupsTestModel(t *testing.T) (Model, *store.Store) {
	t.Helper()
	cfg := config.Default()
	cfg.DataDir = t.TempDir()
	cfg.DBPath = filepath.Join(cfg.DataDir, "little-control-room.sqlite")
	st, err := store.Open(cfg.DBPath)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { st.Close() })
	return Model{ctx: context.Background(), svc: service.New(cfg, st, events.NewBus(), nil), width: 120, height: 40}, st
}

func queueTestWakeup(t *testing.T, st *store.Store, project string, in time.Duration) control.EngineerMessage {
	t.Helper()
	at := time.Now().Add(in)
	message, err := st.CreateEngineerMessage(context.Background(), control.EngineerMessage{
		ProjectPath: project, Provider: control.ProviderCodex, SessionMode: control.SessionModeResumeOrNew,
		TargetSessionID: "session-1", Prompt: "Verify the repost.", DeliverAt: at, ExpiresAt: at.Add(time.Hour),
		Unattended: &control.UnattendedGrant{Tools: []string{"mcp__playwright__*"}},
	})
	if err != nil {
		t.Fatal(err)
	}
	return message
}

func runWakeupsCmd(t *testing.T, m Model, cmd tea.Cmd) Model {
	t.Helper()
	if cmd == nil {
		t.Fatal("expected a wakeups command")
	}
	updated, _ := m.applyWakeupsLoaded(cmd().(wakeupsLoadedMsg))
	return updated.(Model)
}

func TestWakeupsDialogListsAndCancels(t *testing.T) {
	m, st := wakeupsTestModel(t)
	projectA, projectB := t.TempDir(), t.TempDir()
	later := queueTestWakeup(t, st, projectA, 3*time.Hour)
	sooner := queueTestWakeup(t, st, projectB, time.Hour)

	m = runWakeupsCmd(t, m, m.openWakeups(""))
	d := m.wakeupsDialog
	if d == nil || d.busy || len(d.items) != 2 || d.items[0].ID != sooner.ID || d.items[1].ID != later.ID {
		t.Fatalf("dialog = %#v, want both wakeups soonest first", d)
	}
	view := m.renderWakeups("", m.width, m.height)
	for _, want := range []string{"Scheduled Wakeups", "mcp__playwright__*", "cancel wakeup"} {
		if !strings.Contains(view, want) {
			t.Errorf("view missing %q", want)
		}
	}

	updated, _ := m.updateWakeups(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("j")})
	m = updated.(Model)
	updated, cmd := m.updateWakeups(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("c")})
	m = updated.(Model)
	if !m.wakeupsDialog.busy {
		t.Fatal("cancel did not mark the dialog busy")
	}
	if again, extra := m.updateWakeups(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("c")}); extra != nil {
		_ = again
		t.Fatal("a second cancel was accepted while the first was in flight")
	}
	m = runWakeupsCmd(t, m, cmd)
	if len(m.wakeupsDialog.items) != 1 || m.wakeupsDialog.items[0].ID != sooner.ID {
		t.Fatalf("remaining = %#v, want only the sooner wakeup", m.wakeupsDialog.items)
	}
	if !strings.Contains(m.status, "Wakeup canceled") {
		t.Fatalf("status = %q", m.status)
	}
	got, err := st.GetEngineerMessage(context.Background(), later.ID)
	if err != nil || got.State != control.EngineerMessageFailed {
		t.Fatalf("canceled wakeup state = %s err=%v", got.State, err)
	}
}

func TestWakeupsDialogFiltersToProject(t *testing.T) {
	m, st := wakeupsTestModel(t)
	projectA, projectB := t.TempDir(), t.TempDir()
	mine := queueTestWakeup(t, st, projectA, time.Hour)
	queueTestWakeup(t, st, projectB, time.Hour)

	m = runWakeupsCmd(t, m, m.openWakeups(projectA))
	if len(m.wakeupsDialog.items) != 1 || m.wakeupsDialog.items[0].ID != mine.ID {
		t.Fatalf("project dialog = %#v, want only its own wakeup", m.wakeupsDialog.items)
	}
	updated, _ := m.updateWakeups(tea.KeyMsg{Type: tea.KeyEsc})
	if updated.(Model).wakeupsDialog != nil {
		t.Fatal("Esc did not close the dialog")
	}
}

func TestHostCancelWakeupControl(t *testing.T) {
	m, st := wakeupsTestModel(t)
	wakeup := queueTestWakeup(t, st, t.TempDir(), time.Hour)
	inv, err := control.ValidateInvocation(control.Invocation{
		Capability: control.CapabilityEngineerCancelWakeup,
		Args:       []byte(`{"wakeup_id":"` + wakeup.ID + `"}`),
	})
	if err != nil {
		t.Fatal(err)
	}
	outcome := m.executeCancelWakeupControl(inv)
	msg := outcome.cmd().(wakeupCanceledMsg)
	if msg.err != nil || msg.message.State != control.EngineerMessageFailed {
		t.Fatalf("host cancel = %#v", msg)
	}
	if status, err := bossControlExecutionStatus(inv, msg); err != nil || !strings.Contains(status, "Wakeup canceled") {
		t.Fatalf("chat status = %q err=%v", status, err)
	}
	again := m.executeCancelWakeupControl(inv).cmd().(wakeupCanceledMsg)
	if again.err == nil {
		t.Fatal("canceled the same wakeup twice")
	}
}
