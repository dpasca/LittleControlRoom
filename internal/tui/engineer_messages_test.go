package tui

import (
	"errors"
	"strings"
	"testing"

	"lcroom/internal/codexapp"
	"lcroom/internal/control"
	"lcroom/internal/model"
)

type fakeResumeAliasSession struct {
	*fakeCodexSession
	resumeIDs map[string]struct{}
}

func (s *fakeResumeAliasSession) MatchesResumeID(resumeID string) bool {
	if s == nil || s.fakeCodexSession == nil {
		return false
	}
	if strings.TrimSpace(s.snapshot.ThreadID) == strings.TrimSpace(resumeID) {
		return true
	}
	_, ok := s.resumeIDs[strings.TrimSpace(resumeID)]
	return ok
}

func TestEngineerMessageReceiptFailureKeepsClaimForRetry(t *testing.T) {
	message := control.EngineerMessage{
		ID:          "lcrmsg_retry",
		ProjectPath: "/tmp/receipt-retry",
		Provider:    control.ProviderLCAgent,
		State:       control.EngineerMessageDelivering,
	}
	m := Model{
		engineerMessageDeliveries: map[string]struct{}{message.ID: {}},
	}
	updated, cmd := m.applyEngineerMessageDeliveryRecorded(engineerMessageDeliveryRecordedMsg{
		message:      message,
		desiredState: control.EngineerMessageDelivered,
		status:       "Message delivered.",
		recordErr:    errors.New("database temporarily unavailable"),
	})
	got := updated.(Model)
	if _, active := got.engineerMessageDeliveries[message.ID]; !active {
		t.Fatal("delivery claim was released before its receipt could be persisted")
	}
	if cmd == nil || !strings.Contains(got.status, "retrying") {
		t.Fatalf("receipt retry cmd=%v status=%q", cmd, got.status)
	}
}

func TestEngineerMessageClaimErrorReleasesOriginalMessageID(t *testing.T) {
	messageID := "lcrmsg_claim"
	m := Model{
		engineerMessageDeliveries: map[string]struct{}{messageID: {}},
	}
	updated, _ := m.applyEngineerMessageClaimed(engineerMessageClaimedMsg{
		messageID: messageID,
		err:       errors.New("claim failed"),
	})
	got := updated.(Model)
	if _, active := got.engineerMessageDeliveries[messageID]; active {
		t.Fatal("failed claim left the message marked in flight")
	}
}

func TestEngineerMessageRecoveryIsRetriedUntilSuccessful(t *testing.T) {
	svc := newControlTestService(t)
	m := Model{svc: svc, engineerMessageDeliveries: make(map[string]struct{})}
	updated, _ := m.applyEngineerMessagesLoaded(engineerMessagesLoadedMsg{err: errors.New("temporary recovery failure")})
	got := updated.(Model)
	if got.engineerMessagesRecovered {
		t.Fatal("failed startup recovery was marked complete")
	}
	cmd := got.requestEngineerMessagesPollCmd()
	if cmd == nil {
		t.Fatal("mailbox did not retry startup recovery")
	}
	loaded, ok := cmd().(engineerMessagesLoadedMsg)
	if !ok || loaded.err != nil || !loaded.recovered {
		t.Fatalf("recovery retry result = %#v", loaded)
	}
}

func TestEngineerMessageDispositionAcceptsOnlyMatchingLCAgentAliases(t *testing.T) {
	projectPath := "/tmp/lcagent-alias-target"
	live := &fakeResumeAliasSession{
		fakeCodexSession: &fakeCodexSession{
			projectPath: projectPath,
			snapshot: codexapp.Snapshot{
				Provider:     codexapp.ProviderLCAgent,
				ThreadID:     "lca_thread_current",
				Started:      true,
				Busy:         true,
				Phase:        codexapp.SessionPhaseRunning,
				ActiveTurnID: "lca_run_current",
			},
		},
		resumeIDs: map[string]struct{}{"lca_run_historical": {}},
	}
	manager := codexapp.NewManagerWithFactory(func(codexapp.LaunchRequest, func()) (codexapp.Session, error) {
		return live, nil
	})
	if _, _, err := manager.Open(codexapp.LaunchRequest{ProjectPath: projectPath, Provider: codexapp.ProviderLCAgent}); err != nil {
		t.Fatal(err)
	}
	m := Model{
		codexManager: manager,
		allProjects: []model.ProjectSummary{{
			Path:          projectPath,
			PresentOnDisk: true,
		}},
	}
	message := control.EngineerMessage{
		ProjectPath:     projectPath,
		Provider:        control.ProviderLCAgent,
		SessionMode:     control.SessionModeResumeOrNew,
		TargetSessionID: "lca_run_historical",
	}
	disposition := m.engineerMessageDisposition(message)
	if !disposition.wait || disposition.failure != nil {
		t.Fatalf("historical run alias disposition = %#v, want queued for matching active thread", disposition)
	}
	message.TargetSessionID = "lca_unrelated"
	disposition = m.engineerMessageDisposition(message)
	if disposition.failure == nil || !errors.Is(disposition.failure, codexapp.ErrSessionChanged) {
		t.Fatalf("unrelated LCAgent target disposition = %#v, want session-changed failure", disposition)
	}
}

func TestEngineerMessageTerminalReplayIsNotReportedAsPending(t *testing.T) {
	message := control.EngineerMessage{
		ID:          "lcrmsg_delivered",
		ProjectPath: "/tmp/delivered",
		Provider:    control.ProviderClaudeCode,
		State:       control.EngineerMessageDelivered,
	}
	result := engineerMessageControlResult(engineerMessageQueuedMsg{message: message})
	if result.OperationPending || result.Delivery == nil || result.Delivery.State != control.EngineerMessageDelivered {
		t.Fatalf("terminal replay result = %#v", result)
	}
	if !strings.Contains(result.Status, "delivered to") {
		t.Fatalf("terminal replay status = %q", result.Status)
	}
}
