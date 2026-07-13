package codexapp

import (
	"errors"
	"fmt"
	"path/filepath"
	"strings"
)

type SessionInputMode string

const (
	SessionInputSend  SessionInputMode = "send"
	SessionInputSteer SessionInputMode = "steer"
	SessionInputQueue SessionInputMode = "queue"
)

type SessionInputAvailability struct {
	Available bool
	Mode      SessionInputMode
	Reason    string
}

type SessionInputResult struct {
	Mode     SessionInputMode
	Provider Provider
	ThreadID string
}

var (
	ErrSessionInputUnavailable = errors.New("engineer session input unavailable")
	ErrSessionChanged          = errors.New("engineer session changed")
)

func DescribeSessionInput(snapshot Snapshot) SessionInputAvailability {
	provider := snapshot.Provider.Normalized()
	if provider == "" {
		provider = ProviderCodex
	}
	switch {
	case snapshot.Closed || snapshot.Phase == SessionPhaseClosed:
		return unavailableSessionInput("This engineer session is closed.")
	case snapshot.BusyExternal:
		return unavailableSessionInput("This engineer session is active outside Little Control Room.")
	case snapshot.PendingApproval != nil || snapshot.PendingToolInput != nil || snapshot.PendingElicitation != nil:
		return unavailableSessionInput("This engineer session needs a response from the desktop.")
	case snapshot.Phase == SessionPhaseFinishing:
		return unavailableSessionInput("The engineer is finishing the current turn.")
	case snapshot.Phase == SessionPhaseReconciling:
		return unavailableSessionInput("The engineer is checking the current turn state.")
	case snapshot.Phase == SessionPhaseStalled:
		return unavailableSessionInput("The engineer session is stalled and needs desktop attention.")
	case !snapshot.Started:
		return unavailableSessionInput("The engineer session is still starting.")
	case !snapshot.Busy:
		return SessionInputAvailability{Available: true, Mode: SessionInputSend}
	}

	switch provider {
	case ProviderCodex:
		if strings.TrimSpace(snapshot.ActiveTurnID) != "" && (snapshot.Phase == "" || snapshot.Phase == SessionPhaseRunning) {
			return SessionInputAvailability{Available: true, Mode: SessionInputSteer}
		}
	case ProviderLCAgent:
		return SessionInputAvailability{Available: true, Mode: SessionInputQueue}
	}
	return unavailableSessionInput("Wait for the current engineer turn to finish before sending another message.")
}

func unavailableSessionInput(reason string) SessionInputAvailability {
	return SessionInputAvailability{Reason: strings.TrimSpace(reason)}
}

// SubmitSessionInput targets an already-open session and refuses to cross to a
// replacement thread. Starting or resuming sessions remains a separate action.
func (m *Manager) SubmitSessionInput(projectPath, expectedThreadID string, input Submission) (SessionInputResult, error) {
	if m == nil {
		return SessionInputResult{}, fmt.Errorf("%w: session manager required", ErrSessionInputUnavailable)
	}
	projectPath = filepath.Clean(strings.TrimSpace(projectPath))
	expectedThreadID = strings.TrimSpace(expectedThreadID)
	input = normalizeSubmission(input)
	if projectPath == "." || expectedThreadID == "" || input.Empty() {
		return SessionInputResult{}, fmt.Errorf("%w: project, session, and message are required", ErrSessionInputUnavailable)
	}

	unlock := m.opLocks.Lock(projectPath)
	defer unlock()

	m.mu.Lock()
	session, ok := m.sessions[projectPath]
	m.mu.Unlock()
	if !ok || session == nil {
		return SessionInputResult{}, fmt.Errorf("%w: live engineer session not found", ErrSessionInputUnavailable)
	}
	snapshot := sessionStateSnapshot(session)
	if strings.TrimSpace(snapshot.ThreadID) != expectedThreadID {
		return SessionInputResult{}, fmt.Errorf("%w: reopen the current engineer channel", ErrSessionChanged)
	}
	availability := DescribeSessionInput(snapshot)
	if !availability.Available {
		return SessionInputResult{}, fmt.Errorf("%w: %s", ErrSessionInputUnavailable, availability.Reason)
	}
	if err := session.SubmitInput(input); err != nil {
		return SessionInputResult{}, err
	}
	return SessionInputResult{
		Mode:     availability.Mode,
		Provider: snapshot.Provider.Normalized(),
		ThreadID: expectedThreadID,
	}, nil
}
