package tui

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	bossui "lcroom/internal/boss"
	"lcroom/internal/config"
	"lcroom/internal/control"
	"lcroom/internal/events"
	"lcroom/internal/service"
	"lcroom/internal/store"
	"lcroom/internal/submodulealign/aligntest"

	tea "github.com/charmbracelet/bubbletea"
)

type alignHarness struct {
	t     *testing.T
	f     aligntest.Fixture
	st    *store.Store
	model Model
	n     int
}

func newAlignHarness(t *testing.T) *alignHarness {
	t.Helper()
	aligntest.IsolateEnv(t)
	st, err := store.Open(filepath.Join(t.TempDir(), "control.sqlite"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { st.Close() })
	ctx := context.Background()
	m := New(ctx, service.New(config.Default(), st, events.NewBus(), nil))
	m.width, m.height = 120, 44
	return &alignHarness{t: t, f: aligntest.New(t), st: st, model: m}
}

// propose queues and claims one alignment operation like the MCP relay does.
func (h *alignHarness) propose() control.Operation {
	h.t.Helper()
	h.n++
	id := "lcrop_align_" + string(rune('a'+h.n))
	args, _ := json.Marshal(control.GitSubmoduleAlignInput{RequestID: id, ParentPath: h.f.Task, SubmodulePath: "asset"})
	if _, err := h.st.CreateControlOperation(context.Background(), control.Operation{
		ID: id, Source: "mcp", Provider: "claude_code", SessionKey: "session", ProjectPath: h.f.Task,
		Invocation: control.Invocation{RequestID: id, Capability: control.CapabilityGitSubmoduleAlign, Args: args},
	}); err != nil {
		h.t.Fatal(err)
	}
	op, found, err := h.st.ClaimNextControlOperation(context.Background())
	if err != nil || !found || op.ID != id {
		h.t.Fatalf("claim = %+v found=%v err=%v", op, found, err)
	}
	return op
}

func (h *alignHarness) load(op control.Operation) externalControlProposalLoadedMsg {
	h.t.Helper()
	msg, ok := h.model.loadExternalControlProposalCmd(op.ID)().(externalControlProposalLoadedMsg)
	if !ok {
		h.t.Fatal("proposal loader returned the wrong message")
	}
	return msg
}

func (h *alignHarness) status(id string) control.Operation {
	h.t.Helper()
	op, err := h.st.GetControlOperation(context.Background(), id)
	if err != nil {
		h.t.Fatal(err)
	}
	return op
}

func (h *alignHarness) review(op control.Operation) Model {
	h.t.Helper()
	updated, cmd := h.model.applyExternalControlProposalLoaded(h.load(op))
	m := normalizeUpdateModel(updated)
	if cmd != nil || m.externalControlConfirmation == nil {
		h.t.Fatal("a dialog was expected without a follow-up command")
	}
	updated, _ = m.Update(tea.KeyMsg{Type: tea.KeyCtrlG})
	return normalizeUpdateModel(updated)
}

func TestSubmoduleAlignDialogShowsAncestryAndOffersAStandingPermissionForFastForward(t *testing.T) {
	h := newAlignHarness(t)
	h.f.Pin(t, h.f.C2, h.f.C1)
	m := h.review(h.propose())

	view := m.View()
	for _, want := range []string{"Align Submodule Worktree", "fast-forward (+1 commits)", "eligible", "allow for session", "save permission"} {
		if !strings.Contains(view, want) {
			t.Fatalf("dialog missing %q:\n%s", want, view)
		}
	}
	if got := h.f.Head(t); got != h.f.C1 {
		t.Fatalf("reviewing moved HEAD to %s", got)
	}
}

func TestSubmoduleAlignBackwardMoveShowsAncestryAndNeverOffersAStandingPermission(t *testing.T) {
	h := newAlignHarness(t)
	h.f.Pin(t, h.f.C1, h.f.C2)
	m := h.review(h.propose())

	view := m.View()
	for _, want := range []string{"BACKWARD", "No standing permission", "Enter"} {
		if !strings.Contains(view, want) {
			t.Fatalf("dialog missing %q:\n%s", want, view)
		}
	}
	for _, unwanted := range []string{"allow for session", "save permission"} {
		if strings.Contains(view, unwanted) {
			t.Fatalf("dialog offers %q for a backward move:\n%s", unwanted, view)
		}
	}
	for _, key := range []string{"s", "p"} {
		updated, cmd := m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune(key)})
		if cmd != nil || normalizeUpdateModel(updated).externalControlConfirmation.submitting {
			t.Fatalf("key %q saved a standing permission for a backward move", key)
		}
	}
	if perms, _ := h.st.ListControlPermissions(context.Background(), h.f.Task); len(perms) != 0 {
		t.Fatalf("permissions = %+v", perms)
	}
}

func TestSubmoduleAlignRefusedProposalFailsTheOperationWithoutAskingTheOperator(t *testing.T) {
	h := newAlignHarness(t)
	h.f.Pin(t, h.f.C2, h.f.C1)
	if err := os.WriteFile(filepath.Join(h.f.TaskSub, "scratch.txt"), []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	op := h.propose()
	updated, cmd := h.model.applyExternalControlProposalLoaded(h.load(op))
	m := normalizeUpdateModel(updated)
	if m.externalControlConfirmation != nil || cmd == nil {
		t.Fatal("the operator was asked to confirm an alignment that would refuse")
	}
	cmd()
	failed := h.status(op.ID)
	if failed.Status != control.OperationFailed || !strings.Contains(failed.Error, "scratch.txt") || !strings.Contains(failed.Error, "not clean") {
		t.Fatalf("operation = %+v", failed)
	}
}

func TestSubmoduleAlignSavedPermissionRunsAutomaticallyOnlyForCleanFastForward(t *testing.T) {
	h := newAlignHarness(t)
	h.f.Pin(t, h.f.C2, h.f.C1)
	first := h.propose()
	m := h.review(first)

	updated, save := m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("p")})
	m = normalizeUpdateModel(updated)
	if save == nil {
		t.Fatal("eligible request could not save a permission")
	}
	updated, deliver := m.Update(save())
	m = normalizeUpdateModel(updated)
	confirmed := deliver().(bossui.ControlInvocationConfirmedMsg)
	if h.status(first.ID).ConfirmationBy != control.ConfirmationScopedPermission {
		t.Fatalf("first use not recorded as a scoped permission: %+v", h.status(first.ID))
	}
	// The saved permission covers a later identical request on a clean fast-forward.
	h.model = m
	second := h.propose()
	if msg := h.load(second); !msg.automatic {
		t.Fatalf("clean fast-forward was not automatic: %+v", msg)
	}

	// Executing under the permission re-checks the live state: once the checkout
	// is dirty it refuses and leaves HEAD alone.
	if err := os.WriteFile(filepath.Join(h.f.TaskSub, "scratch.txt"), []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	_, cmd := m.executeBossControlInvocation(confirmed)
	result := runAlignCmd(t, cmd)
	if result.Err == nil || !strings.Contains(result.Err.Error(), "dirty") {
		t.Fatalf("dirty checkout was not refused: %+v", result)
	}
	if got := h.f.Head(t); got != h.f.C1 {
		t.Fatalf("HEAD moved to %s", got)
	}

	// A backward move is never loaded as automatic, so it falls back to a dialog.
	if err := os.Remove(filepath.Join(h.f.TaskSub, "scratch.txt")); err != nil {
		t.Fatal(err)
	}
	h.f.Pin(t, h.f.C1, h.f.C2)
	third := h.propose()
	if msg := h.load(third); msg.automatic || msg.preflight.standingEligible {
		t.Fatalf("a saved permission covered a backward move: %+v", msg)
	}
}

func TestSubmoduleAlignConfirmedOperationMovesHeadAndRecordsTheResult(t *testing.T) {
	h := newAlignHarness(t)
	h.f.Pin(t, h.f.C2, h.f.C1)
	op := h.propose()
	m := h.review(op)

	updated, cmd := m.Update(tea.KeyMsg{Type: tea.KeyEnter})
	m = normalizeUpdateModel(updated)
	if cmd == nil || m.externalControlConfirmation != nil {
		t.Fatal("Enter did not confirm")
	}
	confirmed := cmd().(bossui.ControlInvocationConfirmedMsg)
	// Mirror the recording step that precedes execution.
	if _, err := h.st.UpdateControlOperationStatus(context.Background(), op.ID, control.OperationRunning, nil, nil); err != nil {
		t.Fatal(err)
	}
	confirmed.OperationRecorded = true

	m, cmd = asModel(m.executeBossControlInvocation(confirmed))
	if got := m.pendingGitSummary(h.f.Task); got == "" {
		t.Fatal("no pending git indicator while aligning")
	}
	duplicate := m.executeGitSubmoduleAlignControl(confirmed.Invocation, control.GitSubmoduleAlignInput{ParentPath: h.f.Task, SubmodulePath: "asset"})
	if duplicate.cmd != nil || duplicate.err == nil {
		t.Fatal("a second alignment started while one was in flight")
	}
	result := runAlignCmd(t, cmd)
	if result.Err != nil || result.SubmoduleAlign == nil {
		t.Fatalf("result = %+v", result)
	}
	got := result.SubmoduleAlign
	if !got.Changed || got.Head != h.f.C2 || got.PreviousHead != h.f.C1 || got.Ancestry != "fast_forward" || !got.Verification.ConfigUnchanged || got.Verification.ParentGitlinkDrift != "none" {
		t.Fatalf("recorded result = %+v", got)
	}
	if head := h.f.Head(t); head != h.f.C2 {
		t.Fatalf("HEAD = %s", head)
	}

	// The result is persisted for the originating agent.
	recorded := m.recordExternalControlResultCmd(result)().(externalControlResultRecordedMsg)
	if recorded.err != nil {
		t.Fatal(recorded.err)
	}
	stored := h.status(op.ID)
	if stored.Status != control.OperationCompleted || !strings.Contains(string(stored.Result), `"submodule_align"`) || !strings.Contains(string(stored.Result), h.f.C2) {
		t.Fatalf("stored operation = %+v result=%s", stored, stored.Result)
	}
}

func asModel(m tea.Model, cmd tea.Cmd) (Model, tea.Cmd) {
	return normalizeUpdateModel(m), cmd
}

// runAlignCmd runs a control execution command and returns the boss result,
// which the wrapper emits next to the TUI's own action message.
func runAlignCmd(t *testing.T, cmd tea.Cmd) bossui.ControlInvocationResultMsg {
	t.Helper()
	if cmd == nil {
		t.Fatal("no execution command")
	}
	var found *bossui.ControlInvocationResultMsg
	var walk func(msg tea.Msg)
	walk = func(msg tea.Msg) {
		switch v := msg.(type) {
		case tea.BatchMsg:
			for _, c := range v {
				if c != nil {
					walk(c())
				}
			}
		case bossui.ControlInvocationResultMsg:
			found = &v
		}
	}
	walk(cmd())
	if found == nil {
		t.Fatal("execution produced no control result")
	}
	return *found
}
