package agentcontext

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"
)

const (
	StateVersion = 1

	StatusStable       = "stable"
	StatusInFlight     = "in_flight_model_response"
	StatusPendingTools = "pending_tool_results"

	ContextModeExact     = "exact"
	ContextModeCompacted = "compacted"
	ContextModeClipped   = "clipped"
)

type State[M any, C any] struct {
	Version          int       `json:"version"`
	ThreadID         string    `json:"thread_id"`
	ProjectPath      string    `json:"project_path"`
	CreatedAt        time.Time `json:"created_at"`
	UpdatedAt        time.Time `json:"updated_at"`
	LastRunID        string    `json:"last_run_id,omitempty"`
	Status           string    `json:"status"`
	ContextMode      string    `json:"context_mode"`
	LastStablePoint  string    `json:"last_stable_point,omitempty"`
	Summary          string    `json:"summary,omitempty"`
	SummaryCount     int       `json:"summary_count,omitempty"`
	Messages         []M       `json:"messages,omitempty"`
	MessageCount     int       `json:"message_count"`
	ApproxChars      int       `json:"approx_chars"`
	PendingToolCalls []C       `json:"pending_tool_calls,omitempty"`
	PendingReason    string    `json:"pending_reason,omitempty"`
}

type Info struct {
	ThreadID    string
	ProjectPath string
	LastRunID   string
	Status      string
	ContextMode string
	UpdatedAt   time.Time
}

type Store[M any, C any] struct {
	DataDir     string
	Namespace   string
	ThreadID    string
	ProjectPath string
	RunID       string
	CreatedAt   time.Time

	ApproxChars   func([]M) int
	CloneMessages func([]M) []M
	CloneCalls    func([]C) []C
	SameWorkspace func(string, string) bool
}

type MessageParts struct {
	Role       string
	Content    string
	ToolCallID string
	ToolCalls  []ToolCallParts
}

type ToolCallParts struct {
	ID        string
	Type      string
	Name      string
	Arguments string
}

func (s *Store[M, C]) MarkInFlight(source string, messages []M, compacted bool) error {
	return s.write(StatusInFlight, source, messages, compacted, nil, "model response has not reached a stable checkpoint")
}

func (s *Store[M, C]) MarkPendingTools(source string, messages []M, compacted bool, calls []C) error {
	return s.write(StatusPendingTools, source, messages, compacted, calls, "assistant tool calls are awaiting matching tool results")
}

func (s *Store[M, C]) SaveCheckpoint(source string, messages []M, compacted bool) error {
	return s.write(StatusStable, source, messages, compacted, nil, "")
}

func (s *Store[M, C]) write(status, source string, messages []M, compacted bool, pendingCalls []C, pendingReason string) error {
	if s == nil || strings.TrimSpace(s.ThreadID) == "" || len(messages) == 0 {
		return nil
	}
	existing, ok, err := LoadState[M, C](s.DataDir, s.Namespace, s.ThreadID, "", s.SameWorkspace)
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
	contextMode := ContextModeForCompacted(compacted)
	state := State[M, C]{
		Version:          StateVersion,
		ThreadID:         strings.TrimSpace(s.ThreadID),
		ProjectPath:      strings.TrimSpace(s.ProjectPath),
		CreatedAt:        createdAt,
		UpdatedAt:        time.Now(),
		LastRunID:        strings.TrimSpace(s.RunID),
		Status:           NormalizeStatus(status),
		ContextMode:      contextMode,
		LastStablePoint:  strings.TrimSpace(source),
		Messages:         cloneSlice(messages, s.CloneMessages),
		MessageCount:     len(messages),
		ApproxChars:      approxChars(messages, s.ApproxChars),
		PendingToolCalls: cloneSlice(pendingCalls, s.CloneCalls),
		PendingReason:    strings.TrimSpace(pendingReason),
	}
	if state.Status != StatusStable && ok {
		state.LastStablePoint = existing.LastStablePoint
	}
	return SaveState(s.DataDir, s.Namespace, state, s.ApproxChars)
}

func LoadState[M any, C any](dataDir, namespace, threadID, workspaceRoot string, sameWorkspace func(string, string) bool) (*State[M, C], bool, error) {
	path, err := StatePath(dataDir, namespace, threadID)
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
	var state State[M, C]
	if err := json.Unmarshal(body, &state); err != nil {
		return nil, false, fmt.Errorf("read context state %s: %w", path, err)
	}
	if state.ThreadID == "" {
		state.ThreadID = strings.TrimSpace(threadID)
	}
	if workspaceRoot != "" && state.ProjectPath != "" && !sameWorkspacePath(workspaceRoot, state.ProjectPath, sameWorkspace) {
		return nil, false, fmt.Errorf("thread %s belongs to %s, not %s", state.ThreadID, state.ProjectPath, workspaceRoot)
	}
	return &state, true, nil
}

func SaveState[M any, C any](dataDir, namespace string, state State[M, C], measure func([]M) int) error {
	if strings.TrimSpace(state.ThreadID) == "" {
		return nil
	}
	state.Status = NormalizeStatus(state.Status)
	state.ContextMode = NormalizeContextMode(state.ContextMode)
	if state.Version <= 0 {
		state.Version = StateVersion
	}
	if state.MessageCount <= 0 {
		state.MessageCount = len(state.Messages)
	}
	if state.ApproxChars <= 0 && len(state.Messages) > 0 {
		state.ApproxChars = approxChars(state.Messages, measure)
	}
	path, err := StatePath(dataDir, namespace, state.ThreadID)
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

func LatestState[M any, C any](dataDir, namespace, projectPath string, sameWorkspace func(string, string) bool) (*State[M, C], bool, error) {
	projectPath = strings.TrimSpace(projectPath)
	if projectPath == "" {
		return nil, false, nil
	}
	namespace, err := normalizeNamespace(namespace)
	if err != nil {
		return nil, false, err
	}
	root := filepath.Join(dataDir, namespace, "threads")
	if _, err := os.Stat(root); err != nil {
		if os.IsNotExist(err) {
			return nil, false, nil
		}
		return nil, false, err
	}
	var latest *State[M, C]
	if err := filepath.WalkDir(root, func(path string, entry os.DirEntry, walkErr error) error {
		if walkErr != nil || entry.IsDir() || entry.Name() != "state.json" {
			return nil
		}
		body, err := os.ReadFile(path)
		if err != nil {
			return nil
		}
		var state State[M, C]
		if err := json.Unmarshal(body, &state); err != nil {
			return nil
		}
		if state.ProjectPath == "" || !sameWorkspacePath(projectPath, state.ProjectPath, sameWorkspace) {
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

func StatePath(dataDir, namespace, threadID string) (string, error) {
	threadID = strings.TrimSpace(threadID)
	if threadID == "" {
		return "", nil
	}
	if err := ValidateID(threadID); err != nil {
		return "", err
	}
	namespace, err := normalizeNamespace(namespace)
	if err != nil {
		return "", err
	}
	return filepath.Join(dataDir, namespace, "threads", threadID, "state.json"), nil
}

func normalizeNamespace(namespace string) (string, error) {
	namespace = strings.Trim(strings.TrimSpace(namespace), `/\`)
	if namespace == "" || namespace == "." || namespace == ".." || strings.ContainsAny(namespace, `/\`) {
		return "", fmt.Errorf("invalid context namespace: %s", namespace)
	}
	return namespace, nil
}

func ValidateID(id string) error {
	id = strings.TrimSpace(id)
	if id == "" || id == "." || id == ".." || strings.ContainsAny(id, `/\`) {
		return fmt.Errorf("invalid context thread id: %s", id)
	}
	return nil
}

func InfoFromState[M any, C any](state State[M, C]) Info {
	return Info{
		ThreadID:    strings.TrimSpace(state.ThreadID),
		ProjectPath: strings.TrimSpace(state.ProjectPath),
		LastRunID:   strings.TrimSpace(state.LastRunID),
		Status:      strings.TrimSpace(state.Status),
		ContextMode: strings.TrimSpace(state.ContextMode),
		UpdatedAt:   state.UpdatedAt,
	}
}

func NormalizeStatus(status string) string {
	switch strings.TrimSpace(status) {
	case StatusInFlight:
		return StatusInFlight
	case StatusPendingTools:
		return StatusPendingTools
	case StatusStable:
		return StatusStable
	default:
		return StatusStable
	}
}

func NormalizeContextMode(mode string) string {
	switch strings.TrimSpace(mode) {
	case ContextModeCompacted:
		return ContextModeCompacted
	case ContextModeClipped:
		return ContextModeClipped
	case ContextModeExact:
		return ContextModeExact
	default:
		return ContextModeExact
	}
}

func ContextModeForCompacted(compacted bool) string {
	if compacted {
		return ContextModeCompacted
	}
	return ContextModeExact
}

func ApproxMessages[M any](messages []M, parts func(M) MessageParts) int {
	total := 0
	for _, msg := range messages {
		p := parts(msg)
		total += len(p.Role) + len(p.Content) + len(p.ToolCallID)
		for _, call := range p.ToolCalls {
			total += len(call.ID) + len(call.Type) + len(call.Name) + len(call.Arguments)
		}
	}
	return total
}

func approxChars[M any](messages []M, measure func([]M) int) int {
	if measure == nil {
		return 0
	}
	return measure(messages)
}

func cloneSlice[T any](values []T, clone func([]T) []T) []T {
	if len(values) == 0 {
		return nil
	}
	if clone != nil {
		return clone(values)
	}
	out := make([]T, len(values))
	copy(out, values)
	return out
}

func sameWorkspacePath(a, b string, sameWorkspace func(string, string) bool) bool {
	if sameWorkspace != nil {
		return sameWorkspace(a, b)
	}
	return filepath.Clean(strings.TrimSpace(a)) == filepath.Clean(strings.TrimSpace(b))
}
