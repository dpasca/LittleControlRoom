package codexstate

import (
	"bufio"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

type rolloutThreadMetadata struct {
	Type    string `json:"type"`
	Payload struct {
		ID           string          `json:"id"`
		SessionID    string          `json:"session_id"`
		ForkedFromID string          `json:"forked_from_id"`
		AgentRole    string          `json:"agent_role"`
		Source       json.RawMessage `json:"source"`
	} `json:"payload"`
}

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

	meta, err := readRolloutThreadMetadata(rolloutPath, threadID)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return threadID, nil
		}
		return threadID, err
	}
	if meta.Type != "session_meta" || strings.TrimSpace(meta.Payload.ID) != threadID {
		return threadID, nil
	}
	if rootID := strings.TrimSpace(meta.Payload.SessionID); rootID != "" {
		return rootID, nil
	}
	return threadID, nil
}

// RolloutIsRootThread reports whether a rollout is a direct-input Codex
// conversation rather than a forked/sub-agent thread. Unknown older metadata
// is treated as a root so recovery does not hide a legitimate user session.
func RolloutIsRootThread(rolloutPath, threadID string) (bool, error) {
	threadID = strings.TrimSpace(threadID)
	rolloutPath = filepath.Clean(strings.TrimSpace(rolloutPath))
	if threadID == "" || rolloutPath == "" || rolloutPath == "." {
		return true, nil
	}
	meta, err := readRolloutThreadMetadata(rolloutPath, threadID)
	if err != nil {
		return true, err
	}
	if meta.Type != "session_meta" || strings.TrimSpace(meta.Payload.ID) != threadID {
		return true, nil
	}
	if rootID := strings.TrimSpace(meta.Payload.SessionID); rootID != "" && rootID != threadID {
		return false, nil
	}
	if rolloutSubagentParentThreadID(meta.Payload.Source) != "" {
		return false, nil
	}
	return !(strings.TrimSpace(meta.Payload.ForkedFromID) != "" && strings.TrimSpace(meta.Payload.AgentRole) != ""), nil
}

func readRolloutThreadMetadata(rolloutPath, threadID string) (rolloutThreadMetadata, error) {
	file, err := os.Open(rolloutPath)
	if err != nil {
		return rolloutThreadMetadata{}, fmt.Errorf("open Codex thread rollout metadata: %w", err)
	}
	defer file.Close()

	line, err := bufio.NewReader(file).ReadBytes('\n')
	if err != nil && len(line) == 0 {
		return rolloutThreadMetadata{}, fmt.Errorf("read Codex thread rollout metadata: %w", err)
	}
	var meta rolloutThreadMetadata
	if err := json.Unmarshal(line, &meta); err != nil {
		return rolloutThreadMetadata{}, fmt.Errorf("decode Codex thread rollout metadata: %w", err)
	}
	return meta, nil
}

func rolloutSubagentParentThreadID(raw json.RawMessage) string {
	var source struct {
		Subagent struct {
			ThreadSpawn struct {
				ParentThreadID string `json:"parent_thread_id"`
			} `json:"thread_spawn"`
		} `json:"subagent"`
	}
	if len(raw) == 0 || json.Unmarshal(raw, &source) != nil {
		return ""
	}
	return strings.TrimSpace(source.Subagent.ThreadSpawn.ParentThreadID)
}
