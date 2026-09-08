package codexapp

// BusySessionReplacementError leaves the active session untouched. Callers
// must obtain operator confirmation before retrying a forced-new launch.
type BusySessionReplacementError struct {
	SessionID string
}

func (e *BusySessionReplacementError) Error() string {
	return "the current session is busy; confirm stopping it before starting a new session"
}
