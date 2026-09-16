package codexapp

import "strings"

const codexTurnFailureWithoutDetails = "Codex turn failed without error details"

// StoppedSessionError exposes a provider error only after work has stopped.
// Retry errors during an active turn remain transcript activity.
func StoppedSessionError(snapshot Snapshot) string {
	if snapshot.Busy || strings.TrimSpace(snapshot.ActiveTurnID) != "" {
		return ""
	}
	switch snapshot.Phase {
	case SessionPhaseRunning, SessionPhaseFinishing, SessionPhaseExternal:
		return ""
	}
	message := strings.TrimSpace(snapshot.LastError)
	if message == "" {
		return ""
	}
	// Keep raw provider diagnostics in the transcript, with a compact reason
	// in the assessment column and sidebar.
	message, _, _ = strings.Cut(message, "\n")
	return snapshot.Provider.Label() + " stopped with an error: " + message
}
