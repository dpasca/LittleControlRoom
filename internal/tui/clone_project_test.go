package tui

import (
	"context"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"lcroom/internal/codexapp"
	"lcroom/internal/commands"
	"lcroom/internal/config"
	"lcroom/internal/events"
	"lcroom/internal/service"
	"lcroom/internal/store"

	tea "github.com/charmbracelet/bubbletea"
)

func applyCloneProjectPreviewRefresh(t *testing.T, m Model) Model {
	t.Helper()
	cmd := m.refreshCloneProjectPreview()
	if cmd == nil {
		return m
	}
	updated, nextCmd := m.Update(cmd())
	if nextCmd != nil {
		t.Fatalf("clone preview refresh should not schedule follow-up work")
	}
	return updated.(Model)
}

func TestDispatchCloneProjectCommandOpensDialog(t *testing.T) {
	t.Parallel()

	parent := t.TempDir()
	m := Model{
		width:                   100,
		height:                  28,
		homeDirFn:               func() (string, error) { return "/Users/tester", nil },
		newProjectRecentParents: []string{parent},
	}
	updated, cmd := m.dispatchCommand(commands.Invocation{Kind: commands.KindCloneProject, Assistant: "opencode"})
	got := updated.(Model)
	if got.cloneProjectDialog == nil || got.newProjectDialog != nil {
		t.Fatalf("clone dialog = %#v, new project dialog = %#v", got.cloneProjectDialog, got.newProjectDialog)
	}
	if got.cloneProjectDialog.PathInput.Value() != parent {
		t.Fatalf("clone parent = %q, want %q", got.cloneProjectDialog.PathInput.Value(), parent)
	}
	if got.cloneProjectDialog.Provider != codexapp.ProviderOpenCode {
		t.Fatalf("provider = %q, want OpenCode", got.cloneProjectDialog.Provider)
	}
	if cmd == nil {
		t.Fatalf("opening clone dialog should focus its repository input")
	}
	rendered := got.renderCloneProjectContent(76)
	if !strings.Contains(rendered, "Clone Git Repository") || !strings.Contains(rendered, "Repository") || !strings.Contains(rendered, parent) {
		t.Fatalf("rendered clone dialog missing expected content: %q", rendered)
	}
}

func TestNewProjectCloneActionCarriesChoicesAndEscReturns(t *testing.T) {
	t.Parallel()

	parent := t.TempDir()
	m := Model{homeDirFn: func() (string, error) { return parent, nil }}
	updated, _ := m.dispatchCommand(commands.Invocation{Kind: commands.KindNewProject, Assistant: "claude_code"})
	got := updated.(Model)
	got.newProjectDialog.PathInput.SetValue(parent)
	got.newProjectDialog.Provider = codexapp.ProviderClaudeCode
	got.newProjectDialog.Selected = newProjectFieldCloneRepository
	if rendered := got.renderNewProjectContent(76); !strings.Contains(rendered, "Git clone") || !strings.Contains(rendered, "Clone a Git repository") {
		t.Fatalf("New Project dialog missing clone handoff: %q", rendered)
	}

	updated, cmd := got.updateNewProjectMode(tea.KeyMsg{Type: tea.KeyEnter})
	got = updated.(Model)
	if got.cloneProjectDialog == nil || got.newProjectDialog != nil {
		t.Fatalf("expected transition to clone dialog")
	}
	if got.cloneProjectDialog.PathInput.Value() != parent || got.cloneProjectDialog.Provider != codexapp.ProviderClaudeCode {
		t.Fatalf("clone choices = path %q provider %q", got.cloneProjectDialog.PathInput.Value(), got.cloneProjectDialog.Provider)
	}
	if got.cloneProjectDialog.ReturnToNewProject == nil {
		t.Fatalf("clone dialog should retain New Project return state")
	}
	if cmd == nil {
		t.Fatalf("clone transition should focus its first field")
	}

	updated, cmd = got.updateCloneProjectMode(tea.KeyMsg{Type: tea.KeyEsc})
	got = updated.(Model)
	if got.cloneProjectDialog != nil || got.newProjectDialog == nil {
		t.Fatalf("Esc should return to New Project")
	}
	if got.newProjectDialog.Selected != newProjectFieldCloneRepository || got.newProjectDialog.PathInput.Value() != parent {
		t.Fatalf("restored New Project state = %#v", got.newProjectDialog)
	}
	_ = cmd
}

func TestCloneProjectDialogPreviewsNumberedCollision(t *testing.T) {
	t.Parallel()

	parent := t.TempDir()
	if err := os.Mkdir(filepath.Join(parent, "widget"), 0o755); err != nil {
		t.Fatalf("mkdir collision: %v", err)
	}
	m := Model{homeDirFn: func() (string, error) { return parent, nil }}
	m.openCloneProjectDialog(parent, codexapp.ProviderCodex, "")
	m.cloneProjectDialog.RepositoryInput.SetValue("git@github.com:acme/widget.git")
	m = applyCloneProjectPreviewRefresh(t, m)
	if m.cloneProjectDialog.Preview.ProjectName != "widget-2" || !m.cloneProjectDialog.Preview.Collision {
		t.Fatalf("preview = %#v, want widget-2 collision", m.cloneProjectDialog.Preview)
	}
	rendered := m.renderCloneProjectContent(76)
	if !strings.Contains(rendered, `"widget" already exists`) || !strings.Contains(rendered, `"widget-2"`) {
		t.Fatalf("rendered collision preview missing detail: %q", rendered)
	}
}

func TestCloneProjectDialogClonesRegistersAndSelectsProject(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	st, err := store.Open(filepath.Join(t.TempDir(), "little-control-room.sqlite"))
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	defer st.Close()
	svc := service.New(config.Default(), st, events.NewBus(), nil)

	root := t.TempDir()
	repository := filepath.Join(root, "widget.git")
	cmd := exec.Command("git", "init", "--bare", "--quiet", repository)
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("git init --bare: %v: %s", err, strings.TrimSpace(string(out)))
	}
	parent := filepath.Join(root, "projects")
	if err := os.Mkdir(parent, 0o755); err != nil {
		t.Fatalf("mkdir projects: %v", err)
	}

	m := New(ctx, svc)
	m.width = 100
	m.height = 28
	m.homeDirFn = func() (string, error) { return parent, nil }
	m.openCloneProjectDialog(parent, codexapp.ProviderOpenCode, "")
	m.cloneProjectDialog.RepositoryInput.SetValue(repository)
	m = applyCloneProjectPreviewRefresh(t, m)

	updated, cloneCmd := m.updateCloneProjectMode(tea.KeyMsg{Type: tea.KeyEnter})
	m = updated.(Model)
	if cloneCmd == nil || !m.cloneProjectDialog.Submitting {
		t.Fatalf("Enter should begin clone submission")
	}
	rawMsg := cloneCmd()
	msg, ok := rawMsg.(cloneProjectResultMsg)
	if !ok {
		t.Fatalf("clone command returned %T", rawMsg)
	}
	if msg.err != nil {
		t.Fatalf("clone command error: %v", msg.err)
	}
	updated, refreshCmd := m.Update(msg)
	m = updated.(Model)
	if m.cloneProjectDialog != nil {
		t.Fatalf("clone dialog should close after success")
	}
	if refreshCmd == nil {
		t.Fatalf("successful clone should refresh projects")
	}
	wantPath := filepath.Join(parent, "widget")
	if _, err := os.Stat(filepath.Join(wantPath, ".git")); err != nil {
		t.Fatalf("cloned repository missing: %v", err)
	}
	if m.preferredSelectPath != wantPath {
		t.Fatalf("preferred selected path = %q, want %q", m.preferredSelectPath, wantPath)
	}
	provider, ok := m.embeddedLaunchProviderOverride(wantPath)
	if !ok || provider != codexapp.ProviderOpenCode {
		t.Fatalf("provider override = (%q, %v), want OpenCode true", provider, ok)
	}
	if !strings.Contains(m.status, `Repository cloned as "widget"`) || !strings.Contains(m.status, "Enter opens OpenCode") {
		t.Fatalf("status = %q", m.status)
	}
}

func TestCloneProjectDialogFailureRetainsInputsAndCleansDestination(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	st, err := store.Open(filepath.Join(t.TempDir(), "little-control-room.sqlite"))
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	defer st.Close()
	svc := service.New(config.Default(), st, events.NewBus(), nil)
	parent := t.TempDir()
	repository := filepath.Join(parent, "missing.git")
	m := New(ctx, svc)
	m.homeDirFn = func() (string, error) { return parent, nil }
	m.openCloneProjectDialog(parent, codexapp.ProviderCodex, "")
	m.cloneProjectDialog.RepositoryInput.SetValue(repository)
	m = applyCloneProjectPreviewRefresh(t, m)

	updated, cloneCmd := m.updateCloneProjectMode(tea.KeyMsg{Type: tea.KeyEnter})
	m = updated.(Model)
	msg := cloneCmd().(cloneProjectResultMsg)
	if msg.err == nil {
		t.Fatalf("clone command should fail for missing repository")
	}
	updated, _ = m.Update(msg)
	m = updated.(Model)
	if m.cloneProjectDialog == nil || m.cloneProjectDialog.RepositoryInput.Value() != repository || m.cloneProjectDialog.Error == "" {
		t.Fatalf("failed clone should retain editable inputs and inline error")
	}
	if _, statErr := os.Stat(filepath.Join(parent, "missing")); !errors.Is(statErr, os.ErrNotExist) {
		t.Fatalf("failed clone destination remains, stat err = %v", statErr)
	}
}

func TestCloneProjectDialogEscapeCancelsBusyClone(t *testing.T) {
	t.Parallel()

	canceled := false
	m := Model{cloneProjectDialog: &cloneProjectDialogState{Submitting: true}}
	m.cloneProjectDialog.Cancel = func() { canceled = true }
	updated, cmd := m.updateCloneProjectMode(tea.KeyMsg{Type: tea.KeyEsc})
	got := updated.(Model)
	if cmd != nil || !canceled || !got.cloneProjectDialog.CancelRequested {
		t.Fatalf("cancel state = canceled %v requested %v cmd %v", canceled, got.cloneProjectDialog.CancelRequested, cmd != nil)
	}

	updated, _ = got.Update(cloneProjectResultMsg{err: context.Canceled})
	got = updated.(Model)
	if got.cloneProjectDialog == nil || got.cloneProjectDialog.Submitting || got.cloneProjectDialog.CancelRequested {
		t.Fatalf("canceled clone should return to editable dialog")
	}
	if got.status != "Repository clone canceled" {
		t.Fatalf("status = %q", got.status)
	}
}
