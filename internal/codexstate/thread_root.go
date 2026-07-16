package codexstate

import (
	"bufio"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"strings"
)

// ThreadRootID returns the direct-input thread for a Codex session tree.
// Modern sub-agent rollout metadata keeps the root in payload.session_id.
// If the metadata is unavailable or predates session trees, the supplied
// thread ID remains the safe compatibility fallback.
func ThreadRootID(codexHome, threadID string) (string, error) {
	threadID = strings.TrimSpace(threadID)
	if threadID == "" {
		return "", nil
	}
	rolloutPath, err := ThreadRolloutPath(codexHome, threadID)
	if err != nil || strings.TrimSpace(rolloutPath) == "" {
		return threadID, err
	}

	file, err := os.Open(rolloutPath)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return threadID, nil
		}
		return threadID, fmt.Errorf("open Codex thread rollout metadata: %w", err)
	}
	defer file.Close()

	line, err := bufio.NewReader(file).ReadBytes('\n')
	if err != nil && len(line) == 0 {
		return threadID, fmt.Errorf("read Codex thread rollout metadata: %w", err)
	}
	var meta struct {
		Type    string `json:"type"`
		Payload struct {
			ID        string `json:"id"`
			SessionID string `json:"session_id"`
		} `json:"payload"`
	}
	if err := json.Unmarshal(line, &meta); err != nil {
		return threadID, fmt.Errorf("decode Codex thread rollout metadata: %w", err)
	}
	if meta.Type != "session_meta" || strings.TrimSpace(meta.Payload.ID) != threadID {
		return threadID, nil
	}
	if rootID := strings.TrimSpace(meta.Payload.SessionID); rootID != "" {
		return rootID, nil
	}
	return threadID, nil
}
