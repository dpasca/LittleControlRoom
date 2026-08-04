package codexapp

import (
	"sort"
	"strings"
)

// LoadedThreadIDs returns every Codex thread held by an LCR-managed app-server
// session. Callers should use it from background work because taking a complete
// manager snapshot may wait for a session lock.
func LoadedThreadIDs(manager *Manager) []string {
	if manager == nil {
		return nil
	}
	set := make(map[string]struct{})
	for _, snapshot := range append(manager.Snapshots(), manager.ParallelSnapshots()...) {
		if snapshot.Provider.Normalized() != ProviderCodex || snapshot.Closed {
			continue
		}
		if threadID := strings.TrimSpace(snapshot.ThreadID); threadID != "" {
			set[threadID] = struct{}{}
		}
	}
	threadIDs := make([]string, 0, len(set))
	for threadID := range set {
		threadIDs = append(threadIDs, threadID)
	}
	sort.Strings(threadIDs)
	return threadIDs
}
