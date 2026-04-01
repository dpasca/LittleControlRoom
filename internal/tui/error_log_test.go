package tui

import (
	"errors"
	"strings"
	"testing"
	"time"

	"lcroom/internal/commands"
	"lcroom/internal/model"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/x/ansi"
)

func TestActionMsgErrorIsLoggedAndHinted(t *testing.T) {
	now := time.Date(2026, 4, 1, 12, 34, 56, 0, time.FixedZone("JST", 9*60*60))
	updated, _ := Model{
		nowFn: func() time.Time { return now },
		allProjects: []model.ProjectSummary{{
			Path: "/tmp/demo",
			Name: "demo",
		}},
	}.Update(actionMsg{
		projectPath: "/tmp/demo",
		err:         errors.New("git push failed: permission denied"),
	})
	got := updated.(Model)

	if got.err != nil {
		t.Fatalf("got.err = %v, want nil after logging", got.err)
	}
	if got.status != "Action failed (use /errors)" {
		t.Fatalf("status = %q, want action failure hint", got.status)
	}
	if len(got.errorLogEntries) != 1 {
		t.Fatalf("errorLogEntries = %d, want 1", len(got.errorLogEntries))
	}
	entry := got.errorLogEntries[0]
	if entry.Status != "Action failed" {
		t.Fatalf("entry.Status = %q, want Action failed", entry.Status)
	}
	if entry.ProjectName != "demo" {
		t.Fatalf("entry.ProjectName = %q, want demo", entry.ProjectName)
	}
	if entry.Message != "git push failed: permission denied" {
		t.Fatalf("entry.Message = %q, want full error text", entry.Message)
	}

	rendered := ansi.Strip(got.renderTopStatusLine(160))
	if !strings.Contains(rendered, "Action failed (use /errors)") {
		t.Fatalf("top status = %q, want error log hint", rendered)
	}
	if strings.Contains(rendered, "git push failed: permission denied") {
		t.Fatalf("top status should not inline the raw error anymore, got %q", rendered)
	}
}

func TestDispatchCommandOpensErrorLog(t *testing.T) {
	updated, _ := Model{
		errorLogEntries: []errorLogEntry{{
			Status:  "Action failed",
			Message: "boom",
		}},
	}.dispatchCommand(commands.Invocation{Kind: commands.KindErrors, Canonical: "/errors"})
	got := updated.(Model)

	if !got.errorLogVisible {
		t.Fatalf("errorLogVisible = false, want true")
	}
	if got.status != "Error log open. 1 entry available" {
		t.Fatalf("status = %q, want open notice", got.status)
	}
}

func TestUpdateErrorLogModeCopiesSelectedEntry(t *testing.T) {
	prevWriter := clipboardTextWriter
	var copied string
	clipboardTextWriter = func(text string) error {
		copied = text
		return nil
	}
	t.Cleanup(func() {
		clipboardTextWriter = prevWriter
	})

	now := time.Date(2026, 4, 1, 12, 34, 56, 0, time.FixedZone("JST", 9*60*60))
	updated, _ := Model{
		errorLogVisible: true,
		errorLogEntries: []errorLogEntry{{
			At:          now,
			Status:      "Action failed",
			Message:     "git push failed: permission denied",
			ProjectPath: "/tmp/demo",
			ProjectName: "demo",
		}},
	}.updateErrorLogMode(tea.KeyMsg{Type: tea.KeyEnter})
	got := updated.(Model)

	if got.status != "Copied error details to clipboard" {
		t.Fatalf("status = %q, want copy confirmation", got.status)
	}
	if !strings.Contains(copied, "Summary: Action failed") {
		t.Fatalf("copied text missing summary: %q", copied)
	}
	if !strings.Contains(copied, "Project: demo") {
		t.Fatalf("copied text missing project: %q", copied)
	}
	if !strings.Contains(copied, "git push failed: permission denied") {
		t.Fatalf("copied text missing error message: %q", copied)
	}
}

func TestRenderErrorLogPanelShowsDetails(t *testing.T) {
	now := time.Date(2026, 4, 1, 12, 34, 56, 0, time.FixedZone("JST", 9*60*60))
	rendered := ansi.Strip(Model{
		errorLogEntries: []errorLogEntry{{
			At:          now,
			Status:      "Action failed",
			Message:     "git push failed: permission denied",
			ProjectPath: "/tmp/demo",
			ProjectName: "demo",
		}},
	}.renderErrorLogPanel(100, 24))

	if !strings.Contains(rendered, "Error Log") {
		t.Fatalf("panel missing title: %q", rendered)
	}
	if !strings.Contains(rendered, "Action failed") {
		t.Fatalf("panel missing summary: %q", rendered)
	}
	if !strings.Contains(rendered, "Project: demo") {
		t.Fatalf("panel missing project detail: %q", rendered)
	}
	if !strings.Contains(rendered, "git push failed: permission denied") {
		t.Fatalf("panel missing error detail: %q", rendered)
	}
}
