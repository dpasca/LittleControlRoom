package codexapp

import (
	"path/filepath"
	"sync"

	"lcroom/internal/codexstate"
)

var codexStateCleanupCache = struct {
	sync.Mutex
	results map[string]codexstate.CleanupResult
}{
	results: make(map[string]codexstate.CleanupResult),
}

// sanitizeCodexStateRolloutPathsOnce avoids rescanning the entire Codex thread
// table for every embedded session. A failed cleanup is deliberately not
// cached, so a transient SQLite lock can recover on a later launch.
func sanitizeCodexStateRolloutPathsOnce(codexHome string) (codexstate.CleanupResult, error) {
	key := filepath.Clean(codexstate.ResolveHomeRoot(codexHome))
	codexStateCleanupCache.Lock()
	defer codexStateCleanupCache.Unlock()
	if result, ok := codexStateCleanupCache.results[key]; ok {
		return result, nil
	}
	result, err := codexstate.SanitizeStateRolloutPaths(codexHome)
	if err == nil {
		codexStateCleanupCache.results[key] = result
	}
	return result, err
}
