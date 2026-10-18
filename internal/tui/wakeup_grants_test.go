package tui

import (
	"strings"
	"testing"
	"time"

	"lcroom/internal/codexapp"
	"lcroom/internal/control"
)

func wakeupGrantTestModel(t *testing.T, grant control.UnattendedGrant) (Model, *fakeCodexSession, string) {
	t.Helper()
	path := t.TempDir()
	live := &fakeCodexSession{projectPath: path, snapshot: codexapp.Snapshot{
		Provider: codexapp.ProviderCodex, ThreadID: "session-1", Started: true, Busy: true,
	}}
	manager := codexapp.NewManagerWithFactory(func(codexapp.LaunchRequest, func()) (codexapp.Session, error) { return live, nil })
	if _, _, err := manager.Open(codexapp.LaunchRequest{ProjectPath: path, Provider: codexapp.ProviderCodex}); err != nil {
		t.Fatal(err)
	}
	grant, err := control.NormalizeUnattendedGrant(grant)
	if err != nil {
		t.Fatal(err)
	}
	m := Model{codexManager: manager}
	m.registerWakeupGrant(control.EngineerMessage{
		ID: "msg-1", ProjectPath: path, TargetSessionID: "session-1",
		DeliverAt: time.Now(), ExpiresAt: time.Now().Add(time.Hour), Unattended: &grant,
	})
	return m, live, path
}

func approvalSnapshot(approval codexapp.ApprovalRequest) codexapp.Snapshot {
	return codexapp.Snapshot{Provider: codexapp.ProviderCodex, ThreadID: "session-1", Started: true, Busy: true, PendingApproval: &approval}
}

func TestWakeupGrantAnswersCoveredApprovalOnce(t *testing.T) {
	m, live, path := wakeupGrantTestModel(t, control.UnattendedGrant{CommandPrefixes: []string{"go test"}})
	snapshot := approvalSnapshot(codexapp.ApprovalRequest{ID: "a1", Kind: codexapp.ApprovalCommandExecution, Command: "go test ./..."})
	cmd := m.reviewWakeupGrant(path, snapshot)
	if cmd == nil {
		t.Fatal("covered approval was not answered")
	}
	cmd()
	if len(live.decisions) != 1 || live.decisions[0] != codexapp.DecisionAccept {
		t.Fatalf("decisions = %v, want one one-shot accept", live.decisions)
	}
	if again := m.reviewWakeupGrant(path, snapshot); again != nil {
		t.Fatal("the same approval was answered twice while the first response was in flight")
	}
}

func TestWakeupGrantLeavesUncoveredApprovalForOperator(t *testing.T) {
	m, live, path := wakeupGrantTestModel(t, control.UnattendedGrant{CommandPrefixes: []string{"go test"}})
	snapshot := approvalSnapshot(codexapp.ApprovalRequest{ID: "a2", Kind: codexapp.ApprovalCommandExecution, Command: "git push origin master"})
	if cmd := m.reviewWakeupGrant(path, snapshot); cmd != nil {
		cmd()
	}
	if len(live.decisions) != 0 {
		t.Fatalf("uncovered approval was answered: %v", live.decisions)
	}
	if m.questionNotify == nil || m.questionNotify.ProjectPath != path {
		t.Fatalf("operator was not notified: %#v", m.questionNotify)
	}
	entries := len(m.errorLogEntries)
	m.reviewWakeupGrant(path, snapshot)
	if len(m.errorLogEntries) != entries {
		t.Fatal("the same parked approval was reported repeatedly")
	}
}

func TestWakeupGrantKeepsFileChangesInsideProject(t *testing.T) {
	m, live, path := wakeupGrantTestModel(t, control.UnattendedGrant{FileChanges: true})
	outside := approvalSnapshot(codexapp.ApprovalRequest{ID: "f1", Kind: codexapp.ApprovalFileChange, GrantRoot: "/etc"})
	if cmd := m.reviewWakeupGrant(path, outside); cmd != nil {
		cmd()
	}
	if len(live.decisions) != 0 {
		t.Fatal("file change outside the project was approved")
	}
	inside := approvalSnapshot(codexapp.ApprovalRequest{ID: "f2", Kind: codexapp.ApprovalFileChange, GrantRoot: path + "/src"})
	cmd := m.reviewWakeupGrant(path, inside)
	if cmd == nil {
		t.Fatal("file change inside the project was not approved")
	}
}

func TestWakeupGrantEndsWithTheWokenTurn(t *testing.T) {
	m, _, path := wakeupGrantTestModel(t, control.UnattendedGrant{Commands: true})
	busy := codexapp.Snapshot{Provider: codexapp.ProviderCodex, ThreadID: "session-1", Started: true, Busy: true}
	m.reviewWakeupGrant(path, busy)
	idle := busy
	idle.Busy = false
	m.reviewWakeupGrant(path, idle)
	if m.wakeupGrants[path] != nil {
		t.Fatal("grant outlived the turn it was issued for")
	}
	later := approvalSnapshot(codexapp.ApprovalRequest{ID: "a3", Kind: codexapp.ApprovalCommandExecution, Command: "rm -rf build"})
	if cmd := m.reviewWakeupGrant(path, later); cmd != nil {
		t.Fatal("a later operator turn was auto-approved")
	}
}

func TestWakeupGrantFailsClosedForDifferentSession(t *testing.T) {
	m, _, path := wakeupGrantTestModel(t, control.UnattendedGrant{Commands: true})
	other := approvalSnapshot(codexapp.ApprovalRequest{ID: "a4", Kind: codexapp.ApprovalCommandExecution, Command: "ls"})
	other.ThreadID = "someone-else"
	if cmd := m.reviewWakeupGrant(path, other); cmd != nil {
		t.Fatal("approval in a different session was answered")
	}
}

func TestWakeupGrantExpires(t *testing.T) {
	m, _, path := wakeupGrantTestModel(t, control.UnattendedGrant{Commands: true, ValidMinutes: 5})
	m.nowFn = func() time.Time { return time.Now().Add(10 * time.Minute) }
	snapshot := approvalSnapshot(codexapp.ApprovalRequest{ID: "a5", Kind: codexapp.ApprovalCommandExecution, Command: "ls"})
	if cmd := m.reviewWakeupGrant(path, snapshot); cmd != nil {
		t.Fatal("expired grant answered an approval")
	}
}

func TestEngineerMessageDeliveryPromptStatesWakeupAuthority(t *testing.T) {
	grant, _ := control.NormalizeUnattendedGrant(control.UnattendedGrant{Notes: "no new posts"})
	prompt := engineerMessageDeliveryPrompt(control.EngineerMessage{
		ID: "m1", Prompt: "Verify.", DeliverAt: time.Now(), ExpiresAt: time.Now().Add(time.Hour), Unattended: &grant,
	})
	for _, want := range []string{"scheduled wakeup", "no new posts", "Verify."} {
		if !strings.Contains(strings.ToLower(prompt), strings.ToLower(want)) {
			t.Errorf("delivery prompt missing %q:\n%s", want, prompt)
		}
	}
}
