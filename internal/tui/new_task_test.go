package tui

import (
	"strings"
	"testing"

	"lcroom/internal/service"

	"github.com/charmbracelet/bubbles/textinput"
	tea "github.com/charmbracelet/bubbletea"
)

func TestSubmittingNewTaskDialogEscClosesWhileCommandContinues(t *testing.T) {
	input := textinput.New()
	m := Model{
		newTaskDialog: &newTaskDialogState{
			TitleInput: input,
			Submitting: true,
			RequestID:  1,
		},
	}

	updated, cmd := m.updateNewTaskMode(tea.KeyMsg{Type: tea.KeyEsc})
	got := updated.(Model)
	if cmd != nil {
		t.Fatalf("Esc while submitting should not queue work")
	}
	if got.newTaskDialog != nil {
		t.Fatalf("Esc while submitting should close the new task dialog")
	}
	if !strings.Contains(got.status, "running in the background") {
		t.Fatalf("status = %q, want background creation notice", got.status)
	}
}

func TestNewTaskResultDoesNotCloseNewerDialog(t *testing.T) {
	input := textinput.New()
	m := Model{
		newTaskDialog: &newTaskDialogState{
			TitleInput: input,
			Submitting: true,
			RequestID:  2,
		},
	}

	updated, _ := m.applyNewTaskResultMsg(newTaskResultMsg{
		requestID: 1,
		result: service.CreateScratchTaskResult{
			TaskPath: "/tmp/tasks/old",
			TaskName: "old",
		},
	})
	got := updated.(Model)
	if got.newTaskDialog == nil {
		t.Fatalf("stale new task result should not close the current dialog")
	}
	if !got.newTaskDialog.Submitting {
		t.Fatalf("stale new task result should not alter the current dialog submit state")
	}
	if got.preferredSelectPath == "/tmp/tasks/old" {
		t.Fatalf("stale new task result should not steal selection from the current dialog")
	}
	if got.status != "Background scratch task created and added to the list" {
		t.Fatalf("status = %q, want background completion notice", got.status)
	}
}
