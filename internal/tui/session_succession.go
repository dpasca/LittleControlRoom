package tui

import (
	"context"
	"strings"

	"lcroom/internal/browserctl"
	"lcroom/internal/codexapp"
	"lcroom/internal/service"
	"lcroom/internal/store"
)

// handoffSuccession links a /handoff predecessor to the fresh session it
// launches, so exact-session reports, task callbacks and messages follow the
// work instead of reviving the replaced conversation. Every store call runs
// inside the launch command, never on the UI thread.
type handoffSuccession struct {
	svc         *service.Service
	predecessor store.EngineerSessionRef
	sessionKey  string
	launchKey   string
	successor   codexapp.Provider
	recorded    bool
	err         error
}

// newHandoffSuccession returns nil when the source has no provider identity
// yet: nothing can be addressed to it, so there is nothing to forward.
func newHandoffSuccession(svc *service.Service, projectPath string, source codexapp.Snapshot, successor codexapp.Provider) *handoffSuccession {
	threadID := strings.TrimSpace(source.ThreadID)
	if svc == nil || svc.Store() == nil || threadID == "" || strings.TrimSpace(projectPath) == "" {
		return nil
	}
	return &handoffSuccession{
		svc: svc,
		predecessor: store.EngineerSessionRef{
			ProjectPath: projectPath,
			Provider:    embeddedSessionSource(embeddedProvider(source)),
			SessionID:   threadID,
		},
		sessionKey: strings.TrimSpace(source.ControlSessionKey),
		launchKey:  browserctl.NewManagedSessionKey(),
		successor:  successor,
	}
}

// apply pre-assigns the control key the successor will announce, which binds
// the succession to exactly that launch.
func (h *handoffSuccession) apply(req *codexapp.LaunchRequest) {
	if h != nil && req != nil {
		req.TodoCaptureSessionKey = h.launchKey
	}
}

// record runs before launch. A failure never blocks the handoff; it is
// reported so the operator knows reports were not redirected.
func (h *handoffSuccession) record() {
	if h == nil {
		return
	}
	ctx, cancel := context.WithTimeout(context.Background(), engineerMessageStoreTimeout)
	defer cancel()
	_, err := h.svc.Store().RecordEngineerSessionSuccession(ctx, store.EngineerSessionSuccession{
		ProjectPath:          h.predecessor.ProjectPath,
		Provider:             h.predecessor.Provider,
		SessionID:            h.predecessor.SessionID,
		SessionKey:           h.sessionKey,
		SuccessorProjectPath: h.predecessor.ProjectPath,
		SuccessorProvider:    embeddedSessionSource(h.successor),
		SuccessorSessionKey:  h.launchKey,
		Reason:               "handoff",
	})
	h.recorded, h.err = err == nil, err
}

// finish completes or cancels the succession from the launch result and
// returns a status sentence describing where reports now go.
func (h *handoffSuccession) finish(opened codexSessionOpenedMsg) string {
	if h == nil {
		return ""
	}
	if h.err != nil {
		return "Reports for the previous session were not redirected: " + h.err.Error() + "."
	}
	if !h.recorded {
		return ""
	}
	ctx, cancel := context.WithTimeout(context.Background(), engineerMessageStoreTimeout)
	defer cancel()
	if opened.err != nil {
		if err := h.svc.Store().CancelPendingEngineerSessionSuccession(ctx, h.predecessor, h.launchKey); err != nil {
			return "The previous session may still be marked as replaced: " + err.Error() + "."
		}
		return ""
	}
	if threadID := strings.TrimSpace(opened.snapshot.ThreadID); threadID != "" {
		if _, _, err := h.svc.Store().CompleteEngineerSessionSuccession(ctx, h.predecessor, h.launchKey, opened.snapshot.ControlSessionKey, threadID); err != nil {
			return "Reports for the previous session are waiting; linking them here failed: " + err.Error() + "."
		}
		return "Reports, tasks and messages addressed to the previous session now come here."
	}
	if h.successor.Normalized() == codexapp.ProviderLCAgent {
		// LCAgent announces its thread ID as its control key, so a later
		// identity could not be matched to this launch. Fail visibly instead
		// of leaving the predecessor's messages held indefinitely.
		if err := h.svc.Store().CancelPendingEngineerSessionSuccession(ctx, h.predecessor, h.launchKey); err != nil {
			return "The previous session may still be marked as replaced: " + err.Error() + "."
		}
		return "Reports for the previous session were not redirected: the LCAgent thread ID was not available at launch."
	}
	return "Reports, tasks and messages addressed to the previous session will move here once this session starts."
}

// handoffBlockedReason refuses /handoff where a replacement conversation
// would detach durable state that is bound to the exact worker session.
func (m Model) handoffBlockedReason(projectPath string) string {
	if _, ok := m.agentTaskForProjectPath(projectPath); ok {
		return "This is a delegated task's worker session; /handoff would detach the task from it. Let the task finish or stop it instead."
	}
	return ""
}
