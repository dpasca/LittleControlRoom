package tui

import (
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/charmbracelet/bubbles/viewport"
	"github.com/charmbracelet/lipgloss"
	"github.com/charmbracelet/x/ansi"
	"lcroom/internal/codexapp"
)

func TestCodexEmptySchemaElicitationRendersWrappedApprovalDialog(t *testing.T) {
	const projectPath = "/tmp/demo"
	message := "Il GDD vive su Google Drive: il plugin permetterebbe di leggere direttamente l’ultima versione anche quando il login Drive di gcloud non è configurato."
	snapshot := codexapp.Snapshot{
		Provider: codexapp.ProviderCodex,
		Started:  true,
		Busy:     true,
		Status:   "Waiting for MCP input",
		PendingElicitation: &codexapp.ElicitationRequest{
			ID:              "elicitation_1",
			ServerName:      "plugin installer",
			Mode:            codexapp.ElicitationModeForm,
			Message:         message,
			RequestedSchema: json.RawMessage(`{"type":"object","properties":{}}`),
		},
	}
	m := Model{
		codexVisibleProject: projectPath,
		codexInput:          newCodexTextarea(),
		codexViewport:       viewport.New(0, 0),
		codexSnapshots:      map[string]codexapp.Snapshot{projectPath: snapshot},
		width:               64,
		height:              24,
	}

	rendered := ansi.Strip(m.renderCodexView())
	normalized := strings.Join(strings.Fields(rendered), " ")
	for _, want := range []string{
		"Connected tool needs approval",
		"Il GDD vive su Google Drive: il plugin",
		"l’ultima versione anche quando il login",
		"Codex is paused for this request. If it",
		"expires before you answer, the turn may",
		"continue without the connected tool.",
		"No text or JSON is required.",
		"Enter allow",
	} {
		if !strings.Contains(normalized, want) {
			t.Fatalf("rendered dialog missing %q: %q", want, normalized)
		}
	}
	for _, unwanted := range []string{"MCP input:", "Paste JSON or text", "Requested schema:", `{"type":"object"`} {
		if strings.Contains(rendered, unwanted) {
			t.Fatalf("rendered dialog should hide implementation detail %q: %q", unwanted, rendered)
		}
	}
	blocks := ansi.Strip(strings.Join(m.codexLowerBlocks(snapshot, m.width), "\n"))
	if strings.Contains(blocks, "> ") {
		t.Fatalf("empty-schema approval should not show a response composer: %q", blocks)
	}
	for _, line := range strings.Split(rendered, "\n") {
		if got := lipgloss.Width(line); got > m.width {
			t.Fatalf("rendered line width = %d, want <= %d: %q", got, m.width, line)
		}
	}
}

func TestCodexFieldSchemaElicitationKeepsComposer(t *testing.T) {
	request := codexapp.ElicitationRequest{
		ID:      "elicitation_1",
		Mode:    codexapp.ElicitationModeForm,
		Message: "Which account should be used?",
		RequestedSchema: json.RawMessage(
			`{"type":"object","properties":{"account":{"type":"string"}},"required":["account"]}`,
		),
	}
	if !codexElicitationNeedsComposer(request) {
		t.Fatalf("field-bearing schema should keep the composer visible")
	}
	snapshot := codexapp.Snapshot{
		Provider:           codexapp.ProviderCodex,
		Started:            true,
		PendingElicitation: &request,
	}
	m := Model{codexInput: newCodexTextarea()}
	dialog := ansi.Strip(m.renderCodexElicitationDialogContent(snapshot, request, 56))
	for _, want := range []string{"Connected tool needs input", "Type the requested JSON or text in the composer below", `"account"`} {
		if !strings.Contains(dialog, want) {
			t.Fatalf("field elicitation dialog missing %q: %q", want, dialog)
		}
	}
	blocks := ansi.Strip(strings.Join(m.codexLowerBlocks(snapshot, 64), "\n"))
	if !strings.Contains(blocks, "> ") {
		t.Fatalf("field elicitation should render the composer below the dialog: %q", blocks)
	}
	m.codexVisibleProject = "/tmp/demo"
	m.codexViewport = viewport.New(0, 0)
	m.codexSnapshots = map[string]codexapp.Snapshot{"/tmp/demo": snapshot}
	m.width = 64
	m.height = 24
	viewLines := strings.Split(ansi.Strip(m.renderCodexView()), "\n")
	composerArea := strings.Join(viewLines[max(0, len(viewLines)-4):], "\n")
	if !strings.Contains(composerArea, "> ") {
		t.Fatalf("field elicitation dialog should leave the composer visible below it: %q", composerArea)
	}
}

func TestCodexElicitationFooterStatusOverridesWorkingTimer(t *testing.T) {
	snapshot := codexapp.Snapshot{
		Busy:      true,
		BusySince: time.Now().Add(-10 * time.Minute),
		PendingElicitation: &codexapp.ElicitationRequest{
			Mode:            codexapp.ElicitationModeForm,
			RequestedSchema: json.RawMessage(`{"type":"object","properties":{}}`),
		},
	}
	if got := codexFooterStatus(snapshot, time.Now()); got != "Waiting for your decision" {
		t.Fatalf("codexFooterStatus() = %q, want waiting decision status", got)
	}
}
