package tui

import (
	"encoding/json"
	"path/filepath"
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"
	bossui "lcroom/internal/boss"
	"lcroom/internal/config"
	"lcroom/internal/control"
	"lcroom/internal/events"
	"lcroom/internal/service"
	"lcroom/internal/store"
)

func TestScopedPermissionUIRequiresReviewAndIgnoresRepeatSave(t *testing.T) {
	for _, key := range []string{"s", "p"} {
		t.Run(key, func(t *testing.T) {
			ctx := t.Context()
			st, err := store.Open(filepath.Join(t.TempDir(), "state.sqlite"))
			if err != nil {
				t.Fatal(err)
			}
			defer st.Close()
			path := t.TempDir()
			args, _ := json.Marshal(control.TodoAddInput{ProjectPath: path, Text: "Track progress"})
			op, err := st.CreateControlOperation(ctx, control.Operation{ID: "lcrop_permission", Source: "mcp", Provider: "codex", SessionKey: "caller", ProjectPath: path, Invocation: control.Invocation{Capability: control.CapabilityTodoAdd, Args: args}})
			if err != nil {
				t.Fatal(err)
			}
			claimed, _, err := st.ClaimNextControlOperation(ctx)
			if err != nil {
				t.Fatal(err)
			}
			m := New(ctx, service.New(config.Default(), st, events.NewBus(), nil))
			updated, _ := m.applyExternalControlProposalLoaded(externalControlProposalLoadedMsg{operation: claimed})
			m = normalizeUpdateModel(updated)
			updated, _ = m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune(key)})
			m = normalizeUpdateModel(updated)
			permissions, _ := st.ListControlPermissions(ctx, path)
			if len(permissions) != 0 {
				t.Fatal("saved without deliberate review")
			}
			updated, _ = m.Update(tea.KeyMsg{Type: tea.KeyCtrlG})
			m = normalizeUpdateModel(updated)
			updated, save := m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune(key)})
			m = normalizeUpdateModel(updated)
			if save == nil || !m.externalControlConfirmation.submitting {
				t.Fatal("missing submission state")
			}
			if _, again := m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune(key)}); again != nil {
				t.Fatal("repeat save")
			}
			updated, execute := m.Update(save())
			m = normalizeUpdateModel(updated)
			if m.externalControlConfirmation != nil || execute == nil {
				t.Fatal("approval did not execute")
			}
			confirmed := execute().(bossui.ControlInvocationConfirmedMsg)
			if !confirmed.OperationRecorded || confirmed.Invocation.RequestID != op.ID {
				t.Fatal(confirmed)
			}
			permissions, err = st.ListControlPermissions(ctx, path)
			if err != nil || len(permissions) != 1 {
				t.Fatal(permissions, err)
			}
			if (permissions[0].SessionKey != "") != (key == "s") {
				t.Fatal("wrong permission lifetime", permissions)
			}
			msg := m.projectCollaborationsCmd(path, nil)().(projectCollaborationsLoadedMsg)
			if msg.err != nil || len(msg.permissions) != 1 {
				t.Fatal(msg)
			}
			m.projectCollaborationDialog = &projectCollaborationDialog{project: path, permissions: msg.permissions}
			updated, revoke := m.updateProjectCollaborations(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("r")})
			m = normalizeUpdateModel(updated)
			if revoke == nil || !m.projectCollaborationDialog.busy {
				t.Fatal("revoke not busy")
			}
			result := revoke().(projectCollaborationsLoadedMsg)
			if result.err != nil || len(result.permissions) != 0 {
				t.Fatal(result)
			}
		})
	}
}

func TestPermissionReviewScrollDoesNotApprove(t *testing.T) {
	args, _ := json.Marshal(control.EngineerSendPromptInput{ProjectPath: "/target", Provider: control.ProviderCodex, TargetSessionID: "worker", SessionMode: control.SessionModeResumeOrNew, Prompt: strings.Repeat("Review the implementation.\n", 40)})
	m := Model{width: 80, height: 18, externalControlConfirmation: &externalControlConfirmationState{
		reviewing: true,
		operation: control.Operation{Capability: control.CapabilityEngineerSendPrompt, ProjectPath: "/caller", Provider: "codex", SessionKey: "key", Invocation: control.Invocation{Capability: control.CapabilityEngineerSendPrompt, Args: args}},
	}}
	for _, key := range []tea.KeyType{tea.KeyDown, tea.KeyPgDown, tea.KeyEnd, tea.KeyUp, tea.KeyPgUp, tea.KeyHome} {
		updated, cmd := m.updateExternalControlConfirmationMode(tea.KeyMsg{Type: key})
		m = normalizeUpdateModel(updated)
		if cmd != nil || m.externalControlConfirmation == nil || m.externalControlConfirmation.submitting {
			t.Fatal("scroll key changed approval state")
		}
		if key == tea.KeyEnd && m.externalControlConfirmation.scrollOffset == 0 {
			t.Fatal("End did not scroll")
		}
	}
	if m.externalControlConfirmation.scrollOffset != 0 {
		t.Fatal("Home did not reset scroll")
	}
	for _, wantDetails := range []bool{true, false} {
		m.externalControlConfirmation.scrollOffset = 10
		updated, cmd := m.updateExternalControlConfirmationMode(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("d")})
		m = normalizeUpdateModel(updated)
		state := m.externalControlConfirmation
		if cmd != nil || state.submitting || state.showDetails != wantDetails || state.scrollOffset != 0 {
			t.Fatal("details toggle changed approval state or retained the old scroll position")
		}
	}
}
