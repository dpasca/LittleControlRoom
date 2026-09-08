package tui

import (
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"
	"lcroom/internal/codexapp"
	"lcroom/internal/model"
)

func TestNewCodexBusySessionConfirmation(t *testing.T) {
	for _, confirm := range []bool{false, true} {
		name := "keep"
		if confirm {
			name = "replace"
		}
		t.Run(name, func(t *testing.T) {
			var created []*fakeCodexSession
			manager := codexapp.NewManagerWithFactory(func(req codexapp.LaunchRequest, notify func()) (codexapp.Session, error) {
				id := "original"
				if len(created) > 0 {
					id = "replacement"
				}
				s := &fakeCodexSession{projectPath: req.ProjectPath, snapshot: codexapp.Snapshot{ThreadID: id, Started: true, Busy: true, Provider: codexapp.ProviderCodex}}
				created = append(created, s)
				return s, nil
			})
			_, _, err := manager.Open(codexapp.LaunchRequest{ProjectPath: "/tmp/demo"})
			if err != nil {
				t.Fatal(err)
			}
			m := Model{codexManager: manager, projects: []model.ProjectSummary{{Path: "/tmp/demo", Name: "demo", PresentOnDisk: true}}}
			updated, cmd := m.launchCodexForSelection(true, "")
			if cmd == nil {
				t.Fatal("missing launch command")
			}
			request, ok := cmd().(busySessionReplacementRequestedMsg)
			if !ok {
				t.Fatal("busy launch did not request confirmation")
			}
			if len(created) != 1 || created[0].snapshot.Closed {
				t.Fatal("session changed before confirmation")
			}
			updated, _ = updated.(Model).Update(request)
			m = updated.(Model)
			if m.busySessionReplacement == nil || m.busySessionReplacement.Replace {
				t.Fatal("keep current must be selected by default")
			}
			if !strings.Contains(m.renderBusySessionReplacementOverlay(strings.Repeat(" \n", 30), 100, 30), "/new-task") {
				t.Fatal("missing new-task guidance")
			}
			if confirm {
				updated, _ = m.Update(tea.KeyMsg{Type: tea.KeyTab})
				m = updated.(Model)
			}
			updated, cmd = m.Update(tea.KeyMsg{Type: tea.KeyEnter})
			m = updated.(Model)
			_, duplicate := m.Update(tea.KeyMsg{Type: tea.KeyEnter})
			if duplicate != nil {
				t.Fatal("repeat confirmation submitted twice")
			}
			result := cmd()
			updated, _ = m.Update(result)
			m = updated.(Model)
			if m.busySessionReplacement != nil {
				t.Fatal("confirmation did not close after result")
			}
			want := 1
			if confirm {
				want = 2
			}
			if len(created) != want {
				t.Fatalf("created %d sessions, want %d", len(created), want)
			}
			if !confirm && m.err != nil {
				t.Fatalf("cancel surfaced as error: %v", m.err)
			}
		})
	}
}
