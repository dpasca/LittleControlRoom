package codexapp

import "strings"

// MatchesControlSessionKey recognizes the host launch identity and, for
// LCAgent, the logical thread used by its native control executor. A model or
// per-turn run ID is never a session identity.
func (s Snapshot) MatchesControlSessionKey(key string) bool {
	key = strings.TrimSpace(key)
	return key != "" && (key == s.ControlSessionKey ||
		(s.Provider.Normalized() == ProviderLCAgent && key == s.ThreadID))
}
