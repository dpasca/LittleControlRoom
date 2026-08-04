package codexstate

import (
	"fmt"
	"strings"
)

// ThreadLineage is the relationship information persisted in the first
// session_meta record of a Codex rollout. Known is false when the record does
// not establish enough identity to make a cascading thread/delete safe.
type ThreadLineage struct {
	ThreadID string
	RootID   string
	ParentID string
	IsRoot   bool
	Known    bool
}

// ThreadSourceParentID returns the structured parent id stored in the thread
// index's source column for spawned threads. Direct-source values such as
// provider names return an empty id.
func ThreadSourceParentID(source string) string {
	return rolloutSubagentParentThreadID([]byte(strings.TrimSpace(source)))
}

// ReadThreadLineage reads only the rollout metadata record. It does not scan
// conversation contents or mutate Codex state.
func ReadThreadLineage(rolloutPath, threadID string) (ThreadLineage, error) {
	threadID = strings.TrimSpace(threadID)
	if threadID == "" {
		return ThreadLineage{}, fmt.Errorf("Codex thread id is required")
	}
	meta, err := readRolloutThreadMetadata(rolloutPath, threadID)
	if err != nil {
		return ThreadLineage{}, err
	}
	if meta.Type != "session_meta" || strings.TrimSpace(meta.Payload.ID) != threadID {
		return ThreadLineage{ThreadID: threadID}, nil
	}

	lineage := ThreadLineage{
		ThreadID: threadID,
		RootID:   strings.TrimSpace(meta.Payload.SessionID),
		ParentID: rolloutSubagentParentThreadID(meta.Payload.Source),
	}
	if lineage.ParentID == "" && strings.TrimSpace(meta.Payload.AgentRole) != "" {
		lineage.ParentID = strings.TrimSpace(meta.Payload.ForkedFromID)
	}
	if lineage.RootID == threadID {
		lineage.IsRoot = true
		lineage.Known = true
		return lineage, nil
	}
	if lineage.RootID != "" {
		lineage.Known = true
		return lineage, nil
	}
	if lineage.ParentID != "" {
		lineage.Known = true
		return lineage, nil
	}
	if strings.TrimSpace(meta.Payload.AgentRole) != "" || strings.TrimSpace(meta.Payload.ForkedFromID) != "" {
		return lineage, nil
	}

	lineage.RootID = threadID
	lineage.IsRoot = true
	lineage.Known = true
	return lineage, nil
}
