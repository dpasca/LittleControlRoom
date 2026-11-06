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
	"lcroom/internal/model"
	"lcroom/internal/service"
	"lcroom/internal/store"
)

func TestScopedPermissionUIRequiresReviewAndIgnoresRepeatSave(t *testing.T) {
	for _, key := range []string{"2", "3", "down+enter"} {
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
			press := tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune(key)}
			if key == "down+enter" {
				updated, cmd := m.Update(tea.KeyMsg{Type: tea.KeyDown})
				m = normalizeUpdateModel(updated)
				if cmd != nil || m.externalControlConfirmation.submitting {
					t.Fatal("moving the selection approved")
				}
				press = tea.KeyMsg{Type: tea.KeyEnter}
			}
			updated, save := m.Update(press)
			m = normalizeUpdateModel(updated)
			if save == nil || !m.externalControlConfirmation.submitting {
				t.Fatal("missing submission state")
			}
			if _, again := m.Update(press); again != nil {
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
			if (permissions[0].SessionKey != "") != (key != "3") {
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
	for _, key := range []tea.KeyType{tea.KeyDown, tea.KeyPgDown, tea.KeyEnd, tea.KeyUp, tea.KeyPgUp, tea.KeyHome, tea.KeyDown, tea.KeyDown, tea.KeyDown} {
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
	if m.externalControlConfirmation.selected != 2 {
		t.Fatal("arrows did not move the selection within the three choices", m.externalControlConfirmation.selected)
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

func TestWorkerSessionApprovalRequiresReviewAndCoversLaterLaunch(t *testing.T) {
	ctx := t.Context()
	st, err := store.Open(filepath.Join(t.TempDir(), "state.sqlite"))
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	path := t.TempDir()
	create := func(id, args string) control.Operation {
		t.Helper()
		op, err := st.CreateControlOperation(ctx, control.Operation{ID: id, Source: "mcp", Provider: "claude_code", SessionKey: "organizer", ProjectPath: path,
			Invocation: control.Invocation{Capability: control.CapabilityAgentTaskCreate, Args: json.RawMessage(args)}})
		if err != nil {
			t.Fatal(err)
		}
		return op
	}
	op := create("lcrop_first_worker", `{"title":"First worker","provider":"codex","model":"first-model"}`)
	claimed, _, err := st.ClaimNextControlOperation(ctx)
	if err != nil {
		t.Fatal(err)
	}
	m := New(ctx, service.New(config.Default(), st, events.NewBus(), nil))
	loaded := m.loadExternalControlProposalCmd(claimed.ID)().(externalControlProposalLoadedMsg)
	if loaded.err != nil || !loaded.workerDelegationEligible {
		t.Fatal(loaded)
	}
	updated, _ := m.applyExternalControlProposalLoaded(loaded)
	m = normalizeUpdateModel(updated)
	press := tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("4")}
	updated, _ = m.Update(press)
	m = normalizeUpdateModel(updated)
	if permissions, _ := st.ListControlPermissions(ctx, path); len(permissions) != 0 {
		t.Fatal("worker delegation approved before review")
	}
	updated, _ = m.Update(tea.KeyMsg{Type: tea.KeyCtrlG})
	m = normalizeUpdateModel(updated)
	updated, save := m.Update(press)
	m = normalizeUpdateModel(updated)
	if save == nil || !m.externalControlConfirmation.submitting || m.externalControlConfirmation.selected != 3 {
		t.Fatal("worker approval did not retain its selected submitting state")
	}
	if _, again := m.Update(press); again != nil {
		t.Fatal("repeat worker approval")
	}
	updated, execute := m.Update(save())
	m = normalizeUpdateModel(updated)
	if m.externalControlConfirmation != nil || execute == nil {
		t.Fatal("worker approval did not execute")
	}
	confirmed := execute().(bossui.ControlInvocationConfirmedMsg)
	if !confirmed.OperationRecorded || confirmed.Invocation.RequestID != op.ID {
		t.Fatal(confirmed)
	}
	permissions, err := st.ListControlPermissions(ctx, path)
	if err != nil || len(permissions) != 1 || permissions[0].Scope != control.PermissionScopeSessionWorkers {
		t.Fatal(permissions, err)
	}
	next := create("lcrop_next_worker", `{"title":"Different worker","provider":"claude_code","model":"different-model","repository_write":true}`)
	if _, _, err := st.ClaimNextControlOperation(ctx); err != nil {
		t.Fatal(err)
	}
	loaded = m.loadExternalControlProposalCmd(next.ID)().(externalControlProposalLoadedMsg)
	if loaded.err != nil || !loaded.automatic {
		t.Fatal("later worker still asks", loaded)
	}
	updated, execute = m.applyExternalControlProposalLoaded(loaded)
	m = normalizeUpdateModel(updated)
	if m.externalControlConfirmation != nil || execute == nil {
		t.Fatal("covered launch opened another review")
	}
	m.projectCollaborationDialog = &projectCollaborationDialog{project: path, permissions: permissions}
	view := m.renderProjectCollaborations("", 120, 30)
	for _, want := range []string{"Worker delegation", "unlimited"} {
		if !strings.Contains(view, want) {
			t.Fatalf("permission manager missing %q: %s", want, view)
		}
	}
	_, revoke := m.updateProjectCollaborations(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("r")})
	if revoke == nil {
		t.Fatal("no revoke command")
	}
	if result := revoke().(projectCollaborationsLoadedMsg); result.err != nil || len(result.permissions) != 0 {
		t.Fatal(result)
	}
}

func TestWorkerCleanupApprovalRequiresReviewAndCoversNextWorker(t *testing.T) {
	m, root, path := newWorktreeControlFixture(t)
	m.width, m.height = 100, 30
	st, ctx := m.svc.Store(), t.Context()
	create := func(id, workspace string) control.Operation {
		t.Helper()
		if err := st.UpsertProjectState(ctx, model.ProjectState{Path: workspace, Name: "worker", WorktreeKind: model.WorktreeKindLinked, WorktreeRootPath: root}); err != nil {
			t.Fatal(err)
		}
		if _, err := st.CreateAgentTask(ctx, model.CreateAgentTaskInput{
			Title: "Worker", WorkspacePath: workspace, OriginProjectPath: root,
			OriginProvider: model.SessionSourceClaudeCode, OriginSessionKey: "organizer",
			Repository: model.AgentTaskRepository{Isolated: true, Root: workspace},
		}); err != nil {
			t.Fatal(err)
		}
		args, _ := json.Marshal(control.WorktreeRemoveInput{WorktreePath: workspace})
		op, err := st.CreateControlOperation(ctx, control.Operation{ID: id, Source: "mcp", Provider: "claude_code", SessionKey: "organizer", ProjectPath: root,
			Invocation: control.Invocation{Capability: control.CapabilityWorktreeRemove, Args: args}})
		if err != nil {
			t.Fatal(err)
		}
		if _, found, err := st.ClaimNextControlOperation(ctx); err != nil || !found {
			t.Fatal(found, err)
		}
		return op
	}
	first := create("lcrop_cleanup_first", path)
	loaded := m.loadExternalControlProposalCmd(first.ID)().(externalControlProposalLoadedMsg)
	if loaded.err != nil || loaded.automatic || !loaded.workerCleanupEligible {
		t.Fatal(loaded)
	}
	updated, _ := m.applyExternalControlProposalLoaded(loaded)
	m = normalizeUpdateModel(updated)
	press := tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("2")}
	updated, _ = m.Update(press)
	m = normalizeUpdateModel(updated)
	if permissions, _ := st.ListControlPermissions(ctx, root); len(permissions) != 0 {
		t.Fatal("cleanup permission granted before review")
	}
	updated, _ = m.Update(tea.KeyMsg{Type: tea.KeyCtrlG})
	m = normalizeUpdateModel(updated)
	updated, save := m.Update(press)
	m = normalizeUpdateModel(updated)
	if save == nil || !m.externalControlConfirmation.submitting {
		t.Fatal("cleanup approval did not enter submitting state")
	}
	if _, again := m.Update(press); again != nil {
		t.Fatal("duplicate cleanup approval")
	}
	updated, execute := m.Update(save())
	m = normalizeUpdateModel(updated)
	if m.externalControlConfirmation != nil || execute == nil {
		t.Fatal("approved cleanup did not execute")
	}
	if confirmed := execute().(bossui.ControlInvocationConfirmedMsg); !confirmed.OperationRecorded || confirmed.Invocation.RequestID != first.ID {
		t.Fatal(confirmed)
	}
	next := create("lcrop_cleanup_next", t.TempDir())
	loaded = m.loadExternalControlProposalCmd(next.ID)().(externalControlProposalLoadedMsg)
	if loaded.err != nil || !loaded.automatic {
		t.Fatal("second worker asks again", loaded)
	}
	updated, execute = m.applyExternalControlProposalLoaded(loaded)
	m = normalizeUpdateModel(updated)
	if m.externalControlConfirmation != nil || execute == nil {
		t.Fatal("covered deletion opened another review")
	}
	permissions, err := st.ListControlPermissions(ctx, root)
	if err != nil || len(permissions) != 1 || permissions[0].Scope != control.PermissionScopeSessionWorkerCleanup || permissions[0].Used != 2 {
		t.Fatal(permissions, err)
	}
	m.projectCollaborationDialog = &projectCollaborationDialog{project: root, permissions: permissions}
	view := m.renderProjectCollaborations("", 120, 30)
	if !strings.Contains(view, "Worker worktree deletion") || !strings.Contains(view, "unlimited") {
		t.Fatal(view)
	}
	_, revoke := m.updateProjectCollaborations(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("r")})
	if revoke == nil {
		t.Fatal("cleanup permission has no revoke command")
	}
	if result := revoke().(projectCollaborationsLoadedMsg); result.err != nil || len(result.permissions) != 0 {
		t.Fatal(result)
	}
}

func TestWorkerApprovalDialogUsesPersistedParentOwnership(t *testing.T) {
	ctx := t.Context()
	st, err := store.Open(filepath.Join(t.TempDir(), "state.sqlite"))
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	path := t.TempDir()
	parent, err := st.CreateAgentTask(ctx, model.CreateAgentTaskInput{Title: "Other caller's worker", Kind: model.AgentTaskKindAgent,
		OriginProjectPath: path, OriginProvider: model.SessionSourceCodex, OriginSessionKey: "other"})
	if err != nil {
		t.Fatal(err)
	}
	args, _ := json.Marshal(map[string]any{"title": "Nested worker", "parent_task_id": parent.ID, "provider": "codex", "model": "worker-model"})
	op, err := st.CreateControlOperation(ctx, control.Operation{ID: "nested-worker", Source: "mcp", Provider: "codex", SessionKey: "organizer", ProjectPath: path,
		Invocation: control.Invocation{Capability: control.CapabilityAgentTaskCreate, Args: args}})
	if err != nil {
		t.Fatal(err)
	}
	if _, found, err := st.ClaimNextControlOperation(ctx); err != nil || !found {
		t.Fatal(found, err)
	}
	m := New(ctx, service.New(config.Default(), st, events.NewBus(), nil))
	loaded := m.loadExternalControlProposalCmd(op.ID)().(externalControlProposalLoadedMsg)
	if loaded.err != nil || loaded.workerDelegationEligible || loaded.automatic {
		t.Fatal(loaded)
	}
	updated, _ := m.applyExternalControlProposalLoaded(loaded)
	m = normalizeUpdateModel(updated)
	updated, _ = m.openExternalControlConfirmationReview()
	m = normalizeUpdateModel(updated)
	panel, _, err := m.externalControlConfirmationPanel(120, 40)
	if err != nil || strings.Contains(panel, "Manage workers") {
		t.Fatal(panel, err)
	}
	updated, cmd := m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("4")})
	m = normalizeUpdateModel(updated)
	if cmd != nil || m.externalControlConfirmation.submitting {
		t.Fatal("hidden worker choice was activated")
	}
	// The operator can still deny the request without creating a permission.
	_, deny := m.Update(tea.KeyMsg{Type: tea.KeyEsc})
	if deny == nil {
		t.Fatal("Esc did not deny")
	}
	if _, ok := deny().(bossui.ControlInvocationCanceledMsg); !ok {
		t.Fatal("wrong denial message")
	}
	permissions, err := st.ListControlPermissions(ctx, path)
	if err != nil || len(permissions) != 0 {
		t.Fatal(permissions, err)
	}
}

func TestStaleScopedApprovalClosesReviewAndNamesRequestState(t *testing.T) {
	ctx := t.Context()
	st, err := store.Open(filepath.Join(t.TempDir(), "state.sqlite"))
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	path := t.TempDir()
	op, err := st.CreateControlOperation(ctx, control.Operation{ID: "stale-worker", Source: "mcp", Provider: "codex", SessionKey: "organizer", ProjectPath: path,
		Invocation: control.Invocation{Capability: control.CapabilityAgentTaskCreate, Args: json.RawMessage(`{"title":"Worker"}`)}})
	if err != nil {
		t.Fatal(err)
	}
	if _, found, err := st.ClaimNextControlOperation(ctx); err != nil || !found {
		t.Fatal(found, err)
	}
	m := New(ctx, service.New(config.Default(), st, events.NewBus(), nil))
	loaded := m.loadExternalControlProposalCmd(op.ID)().(externalControlProposalLoadedMsg)
	updated, _ := m.applyExternalControlProposalLoaded(loaded)
	m = normalizeUpdateModel(updated)
	if _, err := st.UpdateControlOperationStatus(ctx, op.ID, control.OperationCanceled, nil, nil); err != nil {
		t.Fatal(err)
	}
	result := m.approveControlPermissionCmd(op.ID, bossui.PermissionAllowSessionWorkers)()
	updated, execute := m.Update(result)
	m = normalizeUpdateModel(updated)
	if execute != nil || m.externalControlConfirmation != nil {
		t.Fatal("stale approval remained actionable")
	}
	if !strings.Contains(m.status, "LCR permission approval failed") || !strings.Contains(m.status, "canceled") {
		t.Fatal(m.status)
	}
	permissions, err := st.ListControlPermissions(ctx, path)
	if err != nil || len(permissions) != 0 {
		t.Fatal(permissions, err)
	}
}
