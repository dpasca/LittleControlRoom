package lcagent

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"lcroom/internal/lcagent/modeladapter"
	"lcroom/internal/lcagent/session"
)

const (
	threadStateVersion            = 1
	threadStateStatusStable       = "stable"
	threadStateStatusInFlight     = "in_flight_model_response"
	threadStateStatusPendingTools = "pending_tool_results"
	threadContextModeExact        = "exact"
	threadContextModeCompacted    = "compacted"
)

type threadState struct {
	Version          int                     `json:"version"`
	ThreadID         string                  `json:"thread_id"`
	ProjectPath      string                  `json:"project_path"`
	CreatedAt        time.Time               `json:"created_at"`
	UpdatedAt        time.Time               `json:"updated_at"`
	LastRunID        string                  `json:"last_run_id,omitempty"`
	Status           string                  `json:"status"`
	ContextMode      string                  `json:"context_mode"`
	LastStablePoint  string                  `json:"last_stable_point,omitempty"`
	Messages         []modeladapter.Message  `json:"messages,omitempty"`
	MessageCount     int                     `json:"message_count"`
	ApproxChars      int                     `json:"approx_chars"`
	PendingToolCalls []modeladapter.ToolCall `json:"pending_tool_calls,omitempty"`
	PendingReason    string                  `json:"pending_reason,omitempty"`
}

type ThreadStateInfo struct {
	ThreadID    string
	ProjectPath string
	LastRunID   string
	Status      string
	ContextMode string
	UpdatedAt   time.Time
}

type threadStateStore struct {
	DataDir     string
	ThreadID    string
	ProjectPath string
	RunID       string
	CreatedAt   time.Time
}

func newThreadStateStore(dataDir, threadID, projectPath, runID string, createdAt time.Time) *threadStateStore {
	return &threadStateStore{
		DataDir:     strings.TrimSpace(dataDir),
		ThreadID:    strings.TrimSpace(threadID),
		ProjectPath: strings.TrimSpace(projectPath),
		RunID:       strings.TrimSpace(runID),
		CreatedAt:   createdAt,
	}
}

func newLCAgentThreadID() (string, error) {
	id, err := session.NewID()
	if err != nil {
		return "", err
	}
	return "lct_" + strings.TrimPrefix(id, "lca_"), nil
}

func (s *threadStateStore) MarkInFlight(source string, messages []modeladapter.Message, compacted bool) error {
	return s.write(threadStateStatusInFlight, source, messages, compacted, nil, "model response has not reached a stable checkpoint")
}

func (s *threadStateStore) MarkPendingTools(source string, messages []modeladapter.Message, compacted bool, calls []modeladapter.ToolCall) error {
	return s.write(threadStateStatusPendingTools, source, messages, compacted, calls, "assistant tool calls are awaiting matching tool results")
}

func (s *threadStateStore) SaveCheckpoint(source string, messages []modeladapter.Message, compacted bool) error {
	return s.write(threadStateStatusStable, source, messages, compacted, nil, "")
}

func (s *threadStateStore) write(status, source string, messages []modeladapter.Message, compacted bool, pendingCalls []modeladapter.ToolCall, pendingReason string) error {
	if s == nil || strings.TrimSpace(s.ThreadID) == "" || len(messages) == 0 {
		return nil
	}
	existing, ok, err := loadThreadState(s.DataDir, s.ThreadID, "")
	if err != nil {
		return err
	}
	createdAt := s.CreatedAt
	if ok && !existing.CreatedAt.IsZero() {
		createdAt = existing.CreatedAt
	}
	if createdAt.IsZero() {
		createdAt = time.Now()
	}
	contextMode := threadContextModeExact
	if compacted {
		contextMode = threadContextModeCompacted
	}
	now := time.Now()
	state := threadState{
		Version:          threadStateVersion,
		ThreadID:         strings.TrimSpace(s.ThreadID),
		ProjectPath:      strings.TrimSpace(s.ProjectPath),
		CreatedAt:        createdAt,
		UpdatedAt:        now,
		LastRunID:        strings.TrimSpace(s.RunID),
		Status:           firstResumeNonEmpty(strings.TrimSpace(status), threadStateStatusStable),
		ContextMode:      contextMode,
		LastStablePoint:  strings.TrimSpace(source),
		Messages:         cloneModelMessages(messages),
		MessageCount:     len(messages),
		ApproxChars:      messagesApproxChars(messages),
		PendingToolCalls: cloneThreadStateToolCalls(pendingCalls),
		PendingReason:    strings.TrimSpace(pendingReason),
	}
	if state.Status != threadStateStatusStable && ok {
		state.LastStablePoint = existing.LastStablePoint
	}
	return saveThreadState(s.DataDir, state)
}

func loadThreadState(dataDir, threadID, workspaceRoot string) (*threadState, bool, error) {
	path, err := threadStatePath(dataDir, threadID)
	if err != nil {
		return nil, false, err
	}
	if path == "" {
		return nil, false, nil
	}
	body, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, false, nil
		}
		return nil, false, err
	}
	var state threadState
	if err := json.Unmarshal(body, &state); err != nil {
		return nil, false, fmt.Errorf("read LCAgent thread state %s: %w", path, err)
	}
	if state.ThreadID == "" {
		state.ThreadID = strings.TrimSpace(threadID)
	}
	if workspaceRoot != "" && state.ProjectPath != "" && !sameCleanPath(workspaceRoot, state.ProjectPath) {
		return nil, false, fmt.Errorf("LCAgent thread %s belongs to %s, not %s", state.ThreadID, state.ProjectPath, workspaceRoot)
	}
	return &state, true, nil
}

func saveThreadState(dataDir string, state threadState) error {
	if strings.TrimSpace(state.ThreadID) == "" {
		return nil
	}
	state.Status = firstResumeNonEmpty(strings.TrimSpace(state.Status), threadStateStatusStable)
	state.ContextMode = firstResumeNonEmpty(strings.TrimSpace(state.ContextMode), threadContextModeExact)
	if state.Version <= 0 {
		state.Version = threadStateVersion
	}
	if state.MessageCount <= 0 {
		state.MessageCount = len(state.Messages)
	}
	if state.ApproxChars <= 0 && len(state.Messages) > 0 {
		state.ApproxChars = messagesApproxChars(state.Messages)
	}
	path, err := threadStatePath(dataDir, state.ThreadID)
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	tmp, err := os.CreateTemp(filepath.Dir(path), ".state-*.json")
	if err != nil {
		return err
	}
	tmpPath := tmp.Name()
	enc := json.NewEncoder(tmp)
	enc.SetIndent("", "  ")
	if err := enc.Encode(state); err != nil {
		_ = tmp.Close()
		_ = os.Remove(tmpPath)
		return err
	}
	if err := tmp.Close(); err != nil {
		_ = os.Remove(tmpPath)
		return err
	}
	if err := os.Rename(tmpPath, path); err != nil {
		_ = os.Remove(path)
		if retryErr := os.Rename(tmpPath, path); retryErr != nil {
			_ = os.Remove(tmpPath)
			return retryErr
		}
	}
	return nil
}

func threadStatePath(dataDir, threadID string) (string, error) {
	threadID = strings.TrimSpace(threadID)
	if threadID == "" {
		return "", nil
	}
	if threadID == "." || threadID == ".." || strings.ContainsAny(threadID, `/\`) {
		return "", fmt.Errorf("invalid LCAgent thread id: %s", threadID)
	}
	return filepath.Join(dataDir, "lcagent", "threads", threadID, "state.json"), nil
}

func latestThreadState(dataDir, projectPath string) (*threadState, bool, error) {
	projectPath = strings.TrimSpace(projectPath)
	if projectPath == "" {
		return nil, false, nil
	}
	root := filepath.Join(dataDir, "lcagent", "threads")
	if _, err := os.Stat(root); err != nil {
		if os.IsNotExist(err) {
			return nil, false, nil
		}
		return nil, false, err
	}
	var latest *threadState
	if err := filepath.WalkDir(root, func(path string, entry os.DirEntry, walkErr error) error {
		if walkErr != nil || entry.IsDir() || entry.Name() != "state.json" {
			return nil
		}
		body, err := os.ReadFile(path)
		if err != nil {
			return nil
		}
		var state threadState
		if err := json.Unmarshal(body, &state); err != nil {
			return nil
		}
		if state.ProjectPath == "" || !sameCleanPath(projectPath, state.ProjectPath) {
			return nil
		}
		if latest == nil || state.UpdatedAt.After(latest.UpdatedAt) || (state.UpdatedAt.Equal(latest.UpdatedAt) && state.ThreadID > latest.ThreadID) {
			copied := state
			latest = &copied
		}
		return nil
	}); err != nil {
		return nil, false, err
	}
	if latest == nil {
		return nil, false, nil
	}
	return latest, true, nil
}

func LoadThreadStateInfo(dataDir, threadID, workspaceRoot string) (ThreadStateInfo, bool, error) {
	state, ok, err := loadThreadState(dataDir, threadID, workspaceRoot)
	if err != nil || !ok {
		return ThreadStateInfo{}, ok, err
	}
	return threadStateInfo(*state), true, nil
}

func LatestThreadStateInfo(dataDir, projectPath string) (ThreadStateInfo, bool, error) {
	state, ok, err := latestThreadState(dataDir, projectPath)
	if err != nil || !ok {
		return ThreadStateInfo{}, ok, err
	}
	return threadStateInfo(*state), true, nil
}

func threadStateInfo(state threadState) ThreadStateInfo {
	return ThreadStateInfo{
		ThreadID:    strings.TrimSpace(state.ThreadID),
		ProjectPath: strings.TrimSpace(state.ProjectPath),
		LastRunID:   strings.TrimSpace(state.LastRunID),
		Status:      strings.TrimSpace(state.Status),
		ContextMode: strings.TrimSpace(state.ContextMode),
		UpdatedAt:   state.UpdatedAt,
	}
}

func cloneThreadStateToolCalls(calls []modeladapter.ToolCall) []modeladapter.ToolCall {
	if len(calls) == 0 {
		return nil
	}
	out := make([]modeladapter.ToolCall, len(calls))
	copy(out, calls)
	return out
}
