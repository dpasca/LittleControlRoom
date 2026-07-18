package codexapp

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"
)

const (
	restartIntentVersion  = 1
	restartIntentDirName  = "embedded-sessions"
	restartIntentFileName = "restart-intents.json"
)

var restartIntentMu sync.Mutex

// RestartIntent records an in-flight embedded turn that Little Control Room
// owned immediately before shutting down. It is deliberately small: provider
// artifacts remain the source of truth for conversation history, while this
// file records the user's intent to continue work after restarting LCR.
type RestartIntent struct {
	Provider     Provider  `json:"provider"`
	ProjectPath  string    `json:"project_path"`
	SessionID    string    `json:"session_id"`
	ActiveTurnID string    `json:"active_turn_id,omitempty"`
	Parallel     bool      `json:"parallel,omitempty"`
	CapturedAt   time.Time `json:"captured_at"`
}

func (i RestartIntent) Key() string {
	provider := i.Provider.Normalized()
	projectPath := strings.TrimSpace(i.ProjectPath)
	sessionID := strings.TrimSpace(i.SessionID)
	if provider == "" || projectPath == "" || sessionID == "" {
		return ""
	}
	lane := "interactive"
	if i.Parallel {
		lane = "parallel"
	}
	return projectPath + "\x00" + string(provider) + "\x00" + sessionID + "\x00" + lane
}

func (i RestartIntent) normalized() RestartIntent {
	i.Provider = i.Provider.Normalized()
	i.ProjectPath = strings.TrimSpace(i.ProjectPath)
	i.SessionID = strings.TrimSpace(i.SessionID)
	i.ActiveTurnID = strings.TrimSpace(i.ActiveTurnID)
	i.CapturedAt = i.CapturedAt.UTC()
	return i
}

type restartIntentState struct {
	Version    int             `json:"version"`
	CapturedAt time.Time       `json:"captured_at"`
	Intents    []RestartIntent `json:"intents"`
}

func restartIntentPath(dataDir string) (string, error) {
	dataDir = strings.TrimSpace(dataDir)
	if dataDir == "" {
		return "", fmt.Errorf("app data directory required for embedded restart intents")
	}
	return filepath.Join(filepath.Clean(dataDir), restartIntentDirName, restartIntentFileName), nil
}

// RestartIntentsFromSnapshots returns only locally-owned in-flight turns.
// BusyExternal sessions belong to another process and must never be claimed by
// LCR's restart flow.
func RestartIntentsFromSnapshots(snapshots []Snapshot, capturedAt time.Time) []RestartIntent {
	return restartIntentsFromSnapshots(snapshots, capturedAt, false)
}

func restartIntentsFromSnapshots(snapshots []Snapshot, capturedAt time.Time, parallel bool) []RestartIntent {
	if capturedAt.IsZero() {
		capturedAt = time.Now()
	}
	capturedAt = capturedAt.UTC()
	intents := make([]RestartIntent, 0, len(snapshots))
	seen := make(map[string]struct{}, len(snapshots))
	for _, snapshot := range snapshots {
		if !restartableOwnedSnapshot(snapshot) {
			continue
		}
		intent := RestartIntent{
			Provider:     snapshot.Provider.Normalized(),
			ProjectPath:  snapshot.ProjectPath,
			SessionID:    snapshot.ThreadID,
			ActiveTurnID: snapshot.ActiveTurnID,
			Parallel:     parallel,
			CapturedAt:   capturedAt,
		}.normalized()
		key := intent.Key()
		if key == "" {
			continue
		}
		if _, ok := seen[key]; ok {
			continue
		}
		seen[key] = struct{}{}
		intents = append(intents, intent)
	}
	sort.SliceStable(intents, func(i, j int) bool {
		if intents[i].ProjectPath == intents[j].ProjectPath {
			return intents[i].Key() < intents[j].Key()
		}
		return intents[i].ProjectPath < intents[j].ProjectPath
	})
	return intents
}

func restartableOwnedSnapshot(snapshot Snapshot) bool {
	if !snapshot.Started || snapshot.Closed || snapshot.BusyExternal {
		return false
	}
	if strings.TrimSpace(snapshot.ProjectPath) == "" || strings.TrimSpace(snapshot.ThreadID) == "" {
		return false
	}
	if snapshot.Provider.Normalized() == "" {
		return false
	}
	if snapshot.Busy || strings.TrimSpace(snapshot.ActiveTurnID) != "" {
		return true
	}
	if snapshot.PendingApproval != nil || snapshot.PendingToolInput != nil || snapshot.PendingElicitation != nil {
		return true
	}
	switch snapshot.Phase {
	case SessionPhaseRunning, SessionPhaseFinishing, SessionPhaseReconciling, SessionPhaseStalled:
		return true
	default:
		return false
	}
}

func WriteRestartIntents(dataDir string, intents []RestartIntent) error {
	restartIntentMu.Lock()
	defer restartIntentMu.Unlock()
	return writeRestartIntentsUnlocked(dataDir, intents)
}

func writeRestartIntentsUnlocked(dataDir string, intents []RestartIntent) error {
	path, err := restartIntentPath(dataDir)
	if err != nil {
		return err
	}
	normalized := normalizeRestartIntents(intents)
	if len(normalized) == 0 {
		if err := os.Remove(path); err != nil && !errors.Is(err, os.ErrNotExist) {
			return fmt.Errorf("clear embedded restart intents: %w", err)
		}
		return nil
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return fmt.Errorf("create embedded restart intent directory: %w", err)
	}
	state := restartIntentState{
		Version:    restartIntentVersion,
		CapturedAt: normalized[0].CapturedAt,
		Intents:    normalized,
	}
	raw, err := json.MarshalIndent(state, "", "  ")
	if err != nil {
		return fmt.Errorf("encode embedded restart intents: %w", err)
	}
	raw = append(raw, '\n')
	tmp, err := os.CreateTemp(filepath.Dir(path), ".restart-intents-*.json")
	if err != nil {
		return fmt.Errorf("create embedded restart intent temp file: %w", err)
	}
	tmpPath := tmp.Name()
	defer func() {
		_ = tmp.Close()
		_ = os.Remove(tmpPath)
	}()
	if _, err := tmp.Write(raw); err != nil {
		return fmt.Errorf("write embedded restart intents: %w", err)
	}
	if err := tmp.Chmod(0o600); err != nil {
		return fmt.Errorf("secure embedded restart intents: %w", err)
	}
	if err := tmp.Close(); err != nil {
		return fmt.Errorf("close embedded restart intents: %w", err)
	}
	if err := os.Rename(tmpPath, path); err != nil {
		// Windows cannot atomically replace an existing file with Rename.
		// Keep the POSIX atomic path above and use the same remove/retry
		// fallback as other LCR state writers when replacement is required.
		_ = os.Remove(path)
		if retryErr := os.Rename(tmpPath, path); retryErr != nil {
			return fmt.Errorf("install embedded restart intents: %w", retryErr)
		}
	}
	return nil
}

func ReadRestartIntents(dataDir string) ([]RestartIntent, error) {
	restartIntentMu.Lock()
	defer restartIntentMu.Unlock()
	return readRestartIntentsUnlocked(dataDir)
}

func readRestartIntentsUnlocked(dataDir string) ([]RestartIntent, error) {
	path, err := restartIntentPath(dataDir)
	if err != nil {
		return nil, err
	}
	raw, err := os.ReadFile(path)
	if errors.Is(err, os.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("read embedded restart intents: %w", err)
	}
	var state restartIntentState
	if err := json.Unmarshal(raw, &state); err != nil {
		return nil, fmt.Errorf("decode embedded restart intents: %w", err)
	}
	if state.Version != restartIntentVersion {
		return nil, fmt.Errorf("unsupported embedded restart intent version %d", state.Version)
	}
	return normalizeRestartIntents(state.Intents), nil
}

func mergeRestartIntents(dataDir string, intents []RestartIntent) error {
	if len(intents) == 0 {
		return nil
	}
	restartIntentMu.Lock()
	defer restartIntentMu.Unlock()
	existing, err := readRestartIntentsUnlocked(dataDir)
	if err != nil {
		return err
	}
	combined := append(append([]RestartIntent(nil), intents...), existing...)
	return writeRestartIntentsUnlocked(dataDir, combined)
}

// AcknowledgeRestartIntents removes successfully continued or explicitly
// skipped intents without disturbing other pending restarts.
func AcknowledgeRestartIntents(dataDir string, keys []string) error {
	if len(keys) == 0 {
		return nil
	}
	remove := make(map[string]struct{}, len(keys))
	for _, key := range keys {
		if key = strings.TrimSpace(key); key != "" {
			remove[key] = struct{}{}
		}
	}
	if len(remove) == 0 {
		return nil
	}
	restartIntentMu.Lock()
	defer restartIntentMu.Unlock()
	intents, err := readRestartIntentsUnlocked(dataDir)
	if err != nil {
		return err
	}
	remaining := make([]RestartIntent, 0, len(intents))
	for _, intent := range intents {
		if _, ok := remove[intent.Key()]; ok {
			continue
		}
		remaining = append(remaining, intent)
	}
	return writeRestartIntentsUnlocked(dataDir, remaining)
}

func normalizeRestartIntents(intents []RestartIntent) []RestartIntent {
	out := make([]RestartIntent, 0, len(intents))
	seen := make(map[string]struct{}, len(intents))
	for _, intent := range intents {
		intent = intent.normalized()
		if intent.CapturedAt.IsZero() {
			intent.CapturedAt = time.Now().UTC()
		}
		key := intent.Key()
		if key == "" {
			continue
		}
		if _, ok := seen[key]; ok {
			continue
		}
		seen[key] = struct{}{}
		out = append(out, intent)
	}
	sort.SliceStable(out, func(i, j int) bool {
		if out[i].CapturedAt.Equal(out[j].CapturedAt) {
			return out[i].Key() < out[j].Key()
		}
		return out[i].CapturedAt.After(out[j].CapturedAt)
	})
	return out
}
