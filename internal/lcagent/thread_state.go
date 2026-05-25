package lcagent

import (
	"fmt"
	"strings"
	"time"

	"lcroom/internal/agentcontext"
	"lcroom/internal/lcagent/modeladapter"
	"lcroom/internal/lcagent/session"
)

const (
	threadStateVersion            = agentcontext.StateVersion
	threadStateStatusStable       = agentcontext.StatusStable
	threadStateStatusInFlight     = agentcontext.StatusInFlight
	threadStateStatusPendingTools = agentcontext.StatusPendingTools
	threadContextModeExact        = agentcontext.ContextModeExact
	threadContextModeCompacted    = agentcontext.ContextModeCompacted
	lcagentContextNamespace       = "lcagent"
)

type threadState = agentcontext.State[modeladapter.Message, modeladapter.ToolCall]

type ThreadStateInfo = agentcontext.Info

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
	return s.store().MarkInFlight(source, messages, compacted)
}

func (s *threadStateStore) MarkPendingTools(source string, messages []modeladapter.Message, compacted bool, calls []modeladapter.ToolCall) error {
	return s.store().MarkPendingTools(source, messages, compacted, calls)
}

func (s *threadStateStore) SaveCheckpoint(source string, messages []modeladapter.Message, compacted bool) error {
	return s.store().SaveCheckpoint(source, messages, compacted)
}

func loadThreadState(dataDir, threadID, workspaceRoot string) (*threadState, bool, error) {
	state, ok, err := agentcontext.LoadState[modeladapter.Message, modeladapter.ToolCall](dataDir, lcagentContextNamespace, threadID, workspaceRoot, sameCleanPath)
	if err != nil {
		err = relabelLCAgentContextError(err)
	}
	return state, ok, err
}

func saveThreadState(dataDir string, state threadState) error {
	return agentcontext.SaveState(dataDir, lcagentContextNamespace, state, messagesApproxChars)
}

func threadStatePath(dataDir, threadID string) (string, error) {
	path, err := agentcontext.StatePath(dataDir, lcagentContextNamespace, threadID)
	if err != nil {
		return "", relabelLCAgentContextError(err)
	}
	return path, nil
}

func latestThreadState(dataDir, projectPath string) (*threadState, bool, error) {
	return agentcontext.LatestState[modeladapter.Message, modeladapter.ToolCall](dataDir, lcagentContextNamespace, projectPath, sameCleanPath)
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
	return agentcontext.InfoFromState(state)
}

func cloneThreadStateToolCalls(calls []modeladapter.ToolCall) []modeladapter.ToolCall {
	if len(calls) == 0 {
		return nil
	}
	out := make([]modeladapter.ToolCall, len(calls))
	copy(out, calls)
	return out
}

func (s *threadStateStore) store() *agentcontext.Store[modeladapter.Message, modeladapter.ToolCall] {
	if s == nil {
		return nil
	}
	return &agentcontext.Store[modeladapter.Message, modeladapter.ToolCall]{
		DataDir:       strings.TrimSpace(s.DataDir),
		Namespace:     lcagentContextNamespace,
		ThreadID:      strings.TrimSpace(s.ThreadID),
		ProjectPath:   strings.TrimSpace(s.ProjectPath),
		RunID:         strings.TrimSpace(s.RunID),
		CreatedAt:     s.CreatedAt,
		ApproxChars:   messagesApproxChars,
		CloneMessages: cloneModelMessages,
		CloneCalls:    cloneThreadStateToolCalls,
		SameWorkspace: sameCleanPath,
	}
}

func relabelLCAgentContextError(err error) error {
	if err == nil {
		return nil
	}
	text := err.Error()
	switch {
	case strings.HasPrefix(text, "invalid context thread id"):
		text = strings.Replace(text, "invalid context thread id", "invalid LCAgent thread id", 1)
	case strings.HasPrefix(text, "read context state"):
		text = strings.Replace(text, "read context state", "read LCAgent thread state", 1)
	case strings.HasPrefix(text, "thread "):
		text = "LCAgent " + text
	default:
		text = strings.Replace(text, "context state", "LCAgent thread state", 1)
	}
	return fmt.Errorf("%s", text)
}
