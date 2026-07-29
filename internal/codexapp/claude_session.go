package codexapp

import (
	"bufio"
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"syscall"
	"time"

	"lcroom/internal/browserctl"
	"lcroom/internal/claudeartifact"
	"lcroom/internal/codexcli"
	"lcroom/internal/projectrun"
)

const (
	claudeThinkingStatus                = "Claude Code is thinking..."
	claudeFinishingStatus               = "Claude Code is finalizing the current turn..."
	claudeReadyStatus                   = "Claude Code session ready"
	claudeOpenElsewhereStatus           = "Claude Code session open in another terminal"
	claudeFreshReadyStatus              = "Fresh embedded Claude Code session ready. Send a prompt to start it."
	claudeSupportStatus                 = "Embedded Claude Code session ready"
	claudeInterruptNotice               = "Interrupted embedded Claude Code turn."
	claudeCompactingStatus              = "Claude Code is compacting conversation history..."
	claudeApprovalUnsupported           = "Embedded Claude Code approval responses are not supported yet"
	claudeToolInputUnsupported          = "Embedded Claude Code tool-input responses are not supported yet"
	claudeElicitationUnsupported        = "Embedded Claude Code elicitation responses are not supported yet"
	claudeSafePresetMappingNotice       = "Embedded Claude Code currently maps Safe/Full Auto presets to Claude's acceptEdits mode until Claude-specific approval prompts are wired."
	claudeYoloPresetMappingNotice       = "Embedded Claude Code is running in Claude's bypassPermissions mode because the current launch preset is YOLO."
	claudeDefaultModelAlias             = "sonnet"
	claudeDefaultReasoningEffort        = "medium"
	claudeSyntheticModelPlaceholder     = "<synthetic>"
	claudeRuntimeMCPListControlsTool    = "mcp__lcr_runtime__list_control_capabilities"
	claudeRuntimeMCPDescribeControlTool = "mcp__lcr_runtime__describe_control_capability"
	claudeRuntimeMCPProposeControlTool  = "mcp__lcr_runtime__propose_control_operation"
	claudeRuntimeMCPGetControlTool      = "mcp__lcr_runtime__get_control_operation"
	claudeRuntimeMCPListTODOsTool       = "mcp__lcr_runtime__list_project_todos"
	claudeRuntimeMCPAddTODOTool         = "mcp__lcr_runtime__add_project_todo"
	claudePIDStatusBusy                 = "busy"
	claudePIDStatusIdle                 = "idle"
	claudePIDStatusShell                = "shell"
)

type claudeCodeSession struct {
	projectPath      string
	preset           codexcli.Preset
	notify           func()
	playwrightPolicy browserctl.Policy
	runtimeManager   *projectrun.Manager
	runtimeMCPConfig string
	runtimeMCPPrompt string
	safetySettings   string

	mu                 sync.Mutex
	claudeHome         string
	sessionFile        string
	sessionID          string
	started            bool
	closed             bool
	busy               bool
	busyExternal       bool
	externalTurnActive bool
	compacting         bool
	compactCommand     *claudeCompactCommand
	busySince          time.Time
	pendingSubmissions int
	interruptPending   bool
	lastActivityAt     time.Time
	model              string
	reasoningEffort    string
	tokenUsage         *TokenUsageSnapshot
	modelContextWindow int64
	pendingModel       string
	pendingReasoning   string
	status             string
	lastError          string
	lastSystemNotice   string
	entries            []TranscriptEntry
	lastFileSize       int64
	runningPID         int
	cmd                *exec.Cmd
	stdin              io.WriteCloser
	cancel             context.CancelFunc
	closedCh           chan struct{}
	closedOnce         sync.Once
	modeNoticeShown    bool

	assistantBlocks    map[string]map[string]struct{}
	toolCalls          map[string]claudeToolCall
	toolResults        map[string]struct{}
	transcriptRevision uint64
	transcriptCache    transcriptExportCache
}

type claudeToolCall struct {
	Name    string
	Summary string
	Command string
}

type claudeSubmissionMode int

const (
	claudeSubmissionNormal claudeSubmissionMode = iota
	claudeSubmissionCompact
)

type claudeTokenUsage struct {
	InputTokens              int64 `json:"input_tokens"`
	CacheCreationInputTokens int64 `json:"cache_creation_input_tokens"`
	CacheReadInputTokens     int64 `json:"cache_read_input_tokens"`
	OutputTokens             int64 `json:"output_tokens"`
}

type claudeModelUsage struct {
	InputTokens              int64 `json:"inputTokens"`
	CacheCreationInputTokens int64 `json:"cacheCreationInputTokens"`
	CacheReadInputTokens     int64 `json:"cacheReadInputTokens"`
	OutputTokens             int64 `json:"outputTokens"`
	ContextWindow            int64 `json:"contextWindow"`
}

type claudeCompactMetadata struct {
	PreTokens int64  `json:"pre_tokens"`
	Trigger   string `json:"trigger"`
}

type claudeCompactCommand struct {
	done         chan claudeCompactCompletion
	boundarySeen bool
	metadata     claudeCompactMetadata
	resultText   string
	resultError  bool
}

type claudeCompactCompletion struct {
	result CompactionResult
	err    error
}

type claudeStreamEnvelope struct {
	Type              string                      `json:"type"`
	Subtype           string                      `json:"subtype"`
	SessionID         string                      `json:"session_id"`
	UUID              string                      `json:"uuid"`
	Model             string                      `json:"model"`
	PermissionMode    string                      `json:"permissionMode"`
	Effort            string                      `json:"effort"`
	Message           json.RawMessage             `json:"message"`
	Result            string                      `json:"result"`
	IsError           bool                        `json:"is_error"`
	StopReason        string                      `json:"stop_reason"`
	LastMessage       string                      `json:"last_message"`
	ModelUsage        map[string]claudeModelUsage `json:"modelUsage"`
	CompactMetadata   claudeCompactMetadata       `json:"compact_metadata"`
	CompactMetadataV2 claudeCompactMetadata       `json:"compactMetadata"`
}

type claudeStreamMessage struct {
	ID      string `json:"id"`
	Model   string `json:"model"`
	Role    string `json:"role"`
	Content []struct {
		Type      string          `json:"type"`
		Text      string          `json:"text"`
		Thinking  string          `json:"thinking"`
		ID        string          `json:"id"`
		Name      string          `json:"name"`
		Input     json.RawMessage `json:"input"`
		ToolUseID string          `json:"tool_use_id"`
		Content   any             `json:"content"`
	} `json:"content"`
	Usage claudeTokenUsage `json:"usage"`
}

type claudeActivePIDSession struct {
	PID             int    `json:"pid"`
	SessionID       string `json:"sessionId"`
	StartedAt       int64  `json:"startedAt"`
	Status          string `json:"status"`
	StatusUpdatedAt int64  `json:"statusUpdatedAt"`
}

func newClaudeCodeSession(req LaunchRequest, notify func()) (Session, error) {
	if _, err := exec.LookPath("claude"); err != nil {
		return nil, fmt.Errorf("claude executable not found: %w", err)
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return nil, fmt.Errorf("resolve home directory: %w", err)
	}
	claudeHome := filepath.Join(home, ".claude")
	preset := req.Preset
	if preset == "" {
		preset = codexcli.DefaultPreset()
	}
	runtimeMCPConfig, runtimeMCPPrompt, err := claudeRuntimeMCPLaunchOptions(req)
	if err != nil {
		return nil, fmt.Errorf("configure Claude Code runtime MCP: %w", err)
	}
	safetySettings, err := claudeSafetyHookSettings(req)
	if err != nil {
		return nil, fmt.Errorf("configure Claude Code destructive-command guard: %w", err)
	}

	s := &claudeCodeSession{
		projectPath:      req.ProjectPath,
		preset:           preset,
		notify:           notify,
		playwrightPolicy: req.PlaywrightPolicy.Normalize(),
		runtimeManager:   req.RuntimeManager,
		runtimeMCPConfig: runtimeMCPConfig,
		runtimeMCPPrompt: runtimeMCPPrompt,
		safetySettings:   safetySettings,
		claudeHome:       claudeHome,
		pendingModel:     concreteClaudeModel(req.PendingModel),
		pendingReasoning: strings.TrimSpace(req.PendingReasoning),
		status:           claudeSupportStatus,
		closedCh:         make(chan struct{}),
		assistantBlocks:  make(map[string]map[string]struct{}),
		toolCalls:        make(map[string]claudeToolCall),
		toolResults:      make(map[string]struct{}),
	}

	if !req.ForceNew {
		switch resumeID := strings.TrimSpace(req.ResumeID); {
		case resumeID != "":
			s.sessionID = resumeID
			s.sessionFile = claudeSessionFilePath(s.claudeHome, s.projectPath, resumeID)
			s.started = true
		default:
			sessionFile, sessionID, ok := s.findLatestSession()
			if ok {
				s.sessionFile = sessionFile
				s.sessionID = sessionID
				s.started = true
			}
		}
	}

	s.mu.Lock()
	s.loadTranscriptLocked()
	s.refreshActiveLocked()
	s.updateStatusLocked()
	s.mu.Unlock()

	if initialInput := launchRequestInitialInput(req); !initialInput.Empty() {
		if err := s.SubmitInput(initialInput); err != nil {
			return nil, err
		}
	}

	return s, nil
}

func (s *claudeCodeSession) ProjectPath() string {
	return s.projectPath
}

func (s *claudeCodeSession) Snapshot() Snapshot {
	s.mu.Lock()
	defer s.mu.Unlock()
	entries, transcript := s.exportedTranscriptLocked()
	snapshot := s.stateSnapshotLocked()
	snapshot.Entries = entries
	snapshot.Transcript = transcript
	return snapshot
}

func (s *claudeCodeSession) TrySnapshot() (Snapshot, bool) {
	if !s.mu.TryLock() {
		return Snapshot{}, false
	}
	defer s.mu.Unlock()
	entries, transcript := s.exportedTranscriptLocked()
	snapshot := s.stateSnapshotLocked()
	snapshot.Entries = entries
	snapshot.Transcript = transcript
	return snapshot, true
}

func (s *claudeCodeSession) StateSnapshot() Snapshot {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.stateSnapshotLocked()
}

func (s *claudeCodeSession) TryStateSnapshot() (Snapshot, bool) {
	if !s.mu.TryLock() {
		return Snapshot{}, false
	}
	defer s.mu.Unlock()
	return s.stateSnapshotLocked(), true
}

func (s *claudeCodeSession) stateSnapshotLocked() Snapshot {
	return Snapshot{
		Provider:           ProviderClaudeCode,
		ProjectPath:        s.projectPath,
		ThreadID:           s.sessionID,
		Preset:             s.preset,
		TranscriptRevision: s.transcriptRevision,
		Phase:              s.phaseLocked(),
		Started:            s.started,
		Busy:               s.busy || s.externalTurnActive || s.compacting,
		BusyExternal:       s.busyExternal,
		BusySince:          s.busySince,
		Closed:             s.closed,
		ActivityPreview:    activityPreviewFromEntries(s.entries),
		Status:             s.status,
		LastError:          s.lastError,
		LastSystemNotice:   s.lastSystemNotice,
		LastActivityAt:     s.lastActivityAt,
		Model:              concreteClaudeModel(s.model),
		ReasoningEffort:    s.reasoningEffort,
		PendingModel:       concreteClaudeModel(s.pendingModel),
		PendingReasoning:   s.pendingReasoning,
		TokenUsage:         cloneTokenUsageSnapshot(s.tokenUsage),
	}
}

func cloneTokenUsageSnapshot(usage *TokenUsageSnapshot) *TokenUsageSnapshot {
	if usage == nil {
		return nil
	}
	cloned := *usage
	return &cloned
}

func (s *claudeCodeSession) phaseLocked() SessionPhase {
	phase := SessionPhaseIdle
	switch {
	case s.closed:
		phase = SessionPhaseClosed
	case s.externalTurnActive:
		phase = SessionPhaseExternal
	case s.compacting:
		phase = SessionPhaseReconciling
	case s.busy:
		if s.pendingSubmissions > 0 {
			phase = SessionPhaseRunning
		} else {
			phase = SessionPhaseFinishing
		}
	}
	return phase
}

func (s *claudeCodeSession) invalidateTranscriptCacheLocked() {
	s.transcriptCache.invalidate(&s.transcriptRevision)
}

func (s *claudeCodeSession) exportedTranscriptLocked() ([]TranscriptEntry, string) {
	if !s.transcriptCache.ready || s.transcriptCache.revision != s.transcriptRevision {
		entries := cloneTranscriptEntries(s.entries)
		s.transcriptCache.entries = entries
		s.transcriptCache.transcript = buildTranscriptText(ProviderClaudeCode, entries, "\n", true)
		s.transcriptCache.revision = s.transcriptRevision
		s.transcriptCache.ready = true
	}
	return cloneTranscriptEntries(s.transcriptCache.entries), s.transcriptCache.transcript
}

func (s *claudeCodeSession) Submit(prompt string) error {
	return s.SubmitInput(Submission{Text: prompt})
}

func (s *claudeCodeSession) SubmitInput(input Submission) error {
	return s.submitInput(input, claudeSubmissionNormal, nil)
}

func (s *claudeCodeSession) submitInput(input Submission, mode claudeSubmissionMode, compactCommand *claudeCompactCommand) error {
	input = normalizeSubmission(input)
	if input.Empty() {
		return nil
	}

	displayText := strings.TrimSpace(input.TranscriptDisplayText())
	if displayText == "" {
		return fmt.Errorf("Claude Code prompt required")
	}

	modelInput := input
	if mode == claudeSubmissionNormal {
		modelInput = augmentSubmissionWithRuntimeContext(input, s.runtimeManager, s.projectPath)
	}
	payload, err := buildClaudeStreamInput(modelInput)
	if err != nil {
		return fmt.Errorf("prepare Claude Code prompt: %w", err)
	}

	s.mu.Lock()
	if err := s.submissionStateErrorLocked(mode, compactCommand); err != nil {
		s.mu.Unlock()
		return err
	}
	if s.cmd == nil {
		s.mu.Unlock()
		if err := CheckClaudeCodeAuthentication(context.Background()); err != nil {
			s.mu.Lock()
			if s.closed {
				s.mu.Unlock()
				return fmt.Errorf("Claude Code session is closed")
			}
			s.appendSystemErrorLocked(err.Error())
			s.touchLocked()
			s.mu.Unlock()
			s.notifyAsync()
			return err
		}
		s.mu.Lock()
		if err := s.submissionStateErrorLocked(mode, compactCommand); err != nil {
			s.mu.Unlock()
			return err
		}
	}

	var (
		ctx         context.Context
		cancel      context.CancelFunc
		cmd         *exec.Cmd
		stdin       io.WriteCloser
		stdout      io.ReadCloser
		stderr      io.ReadCloser
		control     string
		startStream bool
	)
	if s.cmd == nil {
		model := firstNonEmptyTrimmed(concreteClaudeModel(s.pendingModel), concreteClaudeModel(s.model))
		reasoning := firstNonEmptyTrimmed(strings.TrimSpace(s.pendingReasoning), strings.TrimSpace(s.reasoningEffort))
		sessionID := strings.TrimSpace(s.sessionID)
		permissionMode, modeNotice := claudePermissionModeForPreset(s.preset)

		ctx, cancel = context.WithCancel(context.Background())
		var err error
		cmd, stdin, stdout, stderr, err = startClaudeTurnWithRuntimeMCP(ctx, s.projectPath, sessionID, model, reasoning, permissionMode, s.playwrightPolicy, s.runtimeMCPConfig, s.runtimeMCPPrompt, s.safetySettings)
		if err != nil {
			cancel()
			s.mu.Unlock()
			return err
		}

		s.cmd = cmd
		s.stdin = stdin
		s.cancel = cancel
		s.runningPID = cmd.Process.Pid
		s.started = s.started || sessionID != ""
		s.lastSystemNotice = ""
		if modeNotice != "" && !s.modeNoticeShown {
			s.appendSystemNoticeLocked(modeNotice)
			s.modeNoticeShown = true
		}
		startStream = true
	} else {
		if mode == claudeSubmissionCompact {
			s.mu.Unlock()
			return fmt.Errorf("Claude Code cannot compact while a turn is active")
		}
		stdin = s.stdin
		if stdin == nil {
			s.mu.Unlock()
			return fmt.Errorf("Claude Code is finishing the current turn")
		}
		var err error
		control, err = buildClaudeInterruptRequest()
		if err != nil {
			s.mu.Unlock()
			return fmt.Errorf("encode Claude interrupt: %w", err)
		}
	}

	if s.busySince.IsZero() {
		s.busySince = time.Now()
	}
	s.busy = true
	s.busyExternal = false
	s.pendingSubmissions++
	s.interruptPending = control != ""
	if mode == claudeSubmissionCompact {
		s.status = claudeCompactingStatus
	} else {
		s.status = claudeThinkingStatus
	}
	s.lastError = ""
	if mode == claudeSubmissionNormal {
		s.appendEntryLocked(TranscriptEntry{Kind: TranscriptUser, Text: displayText})
	}
	s.touchLocked()
	s.mu.Unlock()

	if startStream {
		go s.consumeClaudeTurn(ctx, cmd, stdout, stderr)
	}

	if control != "" {
		if _, err := io.WriteString(stdin, control+"\n"); err != nil {
			s.mu.Lock()
			if s.pendingSubmissions > 0 {
				s.pendingSubmissions--
			}
			s.interruptPending = false
			s.updateStatusLocked()
			s.mu.Unlock()
			return fmt.Errorf("write Claude interrupt: %w", err)
		}
	}

	if _, err := io.WriteString(stdin, payload+"\n"); err != nil {
		if startStream {
			_ = terminateAppServerCommand(cmd)
			_ = stdin.Close()
			s.finishClaudeTurn(fmt.Errorf("write Claude input: %w", err), nil, nil)
		} else {
			s.mu.Lock()
			if s.pendingSubmissions > 0 {
				s.pendingSubmissions--
			}
			s.interruptPending = false
			s.updateStatusLocked()
			s.mu.Unlock()
		}
		return err
	}
	s.notifyAsync()
	return nil
}

func (s *claudeCodeSession) submissionStateErrorLocked(mode claudeSubmissionMode, compactCommand *claudeCompactCommand) error {
	if s.closed {
		return fmt.Errorf("Claude Code session is closed")
	}
	s.refreshActiveLocked()
	switch {
	case s.compacting && (mode != claudeSubmissionCompact || compactCommand == nil || s.compactCommand != compactCommand):
		return fmt.Errorf("Claude Code is already compacting conversation history")
	case mode == claudeSubmissionCompact && (!s.compacting || compactCommand == nil || s.compactCommand != compactCommand):
		return fmt.Errorf("Claude Code compaction request is no longer active")
	case s.busyExternal:
		return fmt.Errorf("this Claude Code session is already busy in another process; Little Control Room is read-only until it finishes")
	case mode == claudeSubmissionCompact && s.busy:
		return fmt.Errorf("Claude Code cannot compact while a turn is active")
	case s.busy && s.pendingSubmissions == 0:
		return fmt.Errorf("Claude Code is finishing the current turn")
	default:
		return nil
	}
}

func (s *claudeCodeSession) ShowStatus() error {
	s.mu.Lock()
	defer s.mu.Unlock()

	s.loadTranscriptLocked()
	s.refreshActiveLocked()
	s.updateStatusLocked()

	sessionID := strings.TrimSpace(s.sessionID)
	if sessionID == "" {
		sessionID = "(not started yet)"
	}
	model := concreteClaudeModel(s.model)
	if model == "" {
		model = "(default)"
	}
	mode := claudePermissionModeLabel(s.preset)
	sessionFile := strings.TrimSpace(s.sessionFile)
	if sessionFile == "" {
		sessionFile = "(not created yet)"
	}
	lines := []string{
		"Claude session " + sessionID,
		"Model: " + model,
		"Mode: " + mode,
		"Session file: " + sessionFile,
	}
	usage := cloneTokenUsageSnapshot(s.tokenUsage)
	contextWindow := s.modelContextWindow
	if usage != nil && usage.ModelContextWindow > 0 {
		contextWindow = usage.ModelContextWindow
	}
	switch {
	case usage != nil && contextWindow > 0:
		used := usage.EstimatedContextTokens()
		usedPercent := int(float64(used)*100/float64(contextWindow) + 0.5)
		if usedPercent < 0 {
			usedPercent = 0
		}
		if usedPercent > 100 {
			usedPercent = 100
		}
		lines = append(lines, fmt.Sprintf(
			"Context: %d / %d tokens used (%d%% used, %d%% left)",
			used,
			contextWindow,
			usedPercent,
			100-usedPercent,
		))
	case usage != nil:
		lines = append(lines, fmt.Sprintf(
			"Context: %d tokens used (model context window unavailable)",
			usage.EstimatedContextTokens(),
		))
	case contextWindow > 0:
		lines = append(lines, fmt.Sprintf(
			"Context: current usage unavailable; model window is %d tokens",
			contextWindow,
		))
	default:
		lines = append(lines, "Context: unavailable until Claude completes a turn")
	}
	s.appendEntryLocked(TranscriptEntry{
		Kind: TranscriptStatus,
		Text: strings.Join(lines, "\n"),
	})
	s.notifyAsync()
	return nil
}

func (s *claudeCodeSession) ShowGoal() error {
	return fmt.Errorf("embedded Claude Code goals are not supported yet")
}

func (s *claudeCodeSession) SetGoal(objective string, tokenBudget *int64) error {
	return fmt.Errorf("embedded Claude Code goals are not supported yet")
}

func (s *claudeCodeSession) PauseGoal() error {
	return fmt.Errorf("embedded Claude Code goals are not supported yet")
}

func (s *claudeCodeSession) ResumeGoal() error {
	return fmt.Errorf("embedded Claude Code goals are not supported yet")
}

func (s *claudeCodeSession) ClearGoal() error {
	return fmt.Errorf("embedded Claude Code goals are not supported yet")
}

func (s *claudeCodeSession) Compact() error {
	_, err := s.CompactWithInstructions("")
	return err
}

func (s *claudeCodeSession) CompactWithInstructions(instructions string) (CompactionResult, error) {
	instructions = strings.TrimSpace(instructions)
	command := &claudeCompactCommand{
		done: make(chan claudeCompactCompletion, 1),
	}

	s.mu.Lock()
	s.loadTranscriptLocked()
	s.refreshActiveLocked()
	switch {
	case s.closed:
		s.mu.Unlock()
		return CompactionResult{}, fmt.Errorf("Claude Code session is closed")
	case s.compacting:
		s.mu.Unlock()
		return CompactionResult{}, fmt.Errorf("Claude Code is already compacting conversation history")
	case s.busyExternal:
		s.mu.Unlock()
		return CompactionResult{}, fmt.Errorf("this Claude Code session is already open in another process; Little Control Room is read-only")
	case s.busy || s.externalTurnActive:
		s.mu.Unlock()
		return CompactionResult{}, fmt.Errorf("Claude Code cannot compact while a turn is active")
	case strings.TrimSpace(s.sessionID) == "":
		s.mu.Unlock()
		return CompactionResult{}, fmt.Errorf("start the Claude Code session with a prompt before compacting it")
	}
	s.compacting = true
	s.compactCommand = command
	s.status = claudeCompactingStatus
	s.lastError = ""
	s.touchLocked()
	s.mu.Unlock()
	s.notifyAsync()

	prompt := "/compact"
	if instructions != "" {
		prompt += " " + instructions
	}
	if err := s.submitInput(Submission{Text: prompt}, claudeSubmissionCompact, command); err != nil {
		s.mu.Lock()
		if s.compactCommand == command {
			s.compactCommand = nil
			s.compacting = false
			s.updateStatusLocked()
		}
		s.mu.Unlock()
		s.notifyAsync()
		return CompactionResult{}, err
	}

	timer := time.NewTimer(compactionWaitTimeout)
	defer timer.Stop()
	select {
	case completion := <-command.done:
		return completion.result, completion.err
	case <-timer.C:
		s.mu.Lock()
		cmd := s.cmd
		if s.compactCommand == command {
			s.compactCommand = nil
			s.compacting = false
		}
		s.updateStatusLocked()
		s.mu.Unlock()
		if cmd != nil {
			_ = terminateAppServerCommand(cmd)
		}
		s.notifyAsync()
		return CompactionResult{}, fmt.Errorf("timed out waiting for Claude Code to compact conversation history")
	}
}

func (s *claudeCodeSession) Review() error {
	return fmt.Errorf("Embedded Claude Code review mode is not supported yet")
}

func (s *claudeCodeSession) ListModels() ([]ModelOption, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	models := append([]ModelOption(nil), claudeEmbeddedModelOptions()...)
	extra := make([]ModelOption, 0, 2)
	for _, model := range []string{s.pendingModel, s.model} {
		model = concreteClaudeModel(model)
		if model == "" || claudeModelOptionExists(models, model) {
			continue
		}
		extra = append(extra, ModelOption{
			ID:                        model,
			Model:                     model,
			DisplayName:               model,
			Description:               "Current Claude Code model",
			SupportedReasoningEfforts: claudeReasoningEffortOptions(),
			DefaultReasoningEffort:    claudeDefaultReasoningEffort,
		})
	}
	models = append(extra, models...)
	return models, nil
}

func (s *claudeCodeSession) StageModelOverride(model, reasoning string) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	if s.closed {
		return fmt.Errorf("Claude Code session is closed")
	}
	s.pendingModel = concreteClaudeModel(model)
	s.pendingReasoning = strings.TrimSpace(reasoning)
	if s.pendingModel == "" && s.pendingReasoning == "" {
		s.lastSystemNotice = ""
		s.updateStatusLocked()
		return nil
	}
	parts := []string{}
	if s.pendingModel != "" {
		parts = append(parts, "model "+s.pendingModel)
	}
	if s.pendingReasoning != "" {
		parts = append(parts, "effort "+s.pendingReasoning)
	}
	s.lastSystemNotice = "Claude Code will use " + strings.Join(parts, ", ") + " on the next prompt."
	s.updateStatusLocked()
	s.notifyAsync()
	return nil
}

func (s *claudeCodeSession) Interrupt() error {
	s.mu.Lock()
	cmd := s.cmd
	if s.closed {
		s.mu.Unlock()
		return fmt.Errorf("Claude Code session is closed")
	}
	if cmd == nil || !s.busy {
		s.mu.Unlock()
		return fmt.Errorf("Claude Code is not currently running")
	}
	s.interruptPending = true
	s.lastSystemNotice = claudeInterruptNotice
	s.status = claudeInterruptNotice
	s.mu.Unlock()

	if err := terminateAppServerCommand(cmd); err != nil {
		return err
	}
	s.notifyAsync()
	return nil
}

func claudeEmbeddedModelOptions() []ModelOption {
	return []ModelOption{
		{
			ID:                        claudeDefaultModelAlias,
			Model:                     claudeDefaultModelAlias,
			DisplayName:               "Sonnet",
			Description:               "Latest Claude Sonnet alias for general coding work.",
			SupportedReasoningEfforts: claudeReasoningEffortOptions(),
			DefaultReasoningEffort:    claudeDefaultReasoningEffort,
			IsDefault:                 true,
		},
		{
			ID:                        "fable",
			Model:                     "fable",
			DisplayName:               "Fable",
			Description:               "Latest Claude Fable alias.",
			SupportedReasoningEfforts: claudeReasoningEffortOptions(),
			DefaultReasoningEffort:    claudeDefaultReasoningEffort,
		},
		{
			ID:                        "opus",
			Model:                     "opus",
			DisplayName:               "Opus",
			Description:               "Latest Claude Opus alias for deeper reasoning.",
			SupportedReasoningEfforts: claudeReasoningEffortOptions(),
			DefaultReasoningEffort:    claudeDefaultReasoningEffort,
		},
		{
			ID:                        "haiku",
			Model:                     "haiku",
			DisplayName:               "Haiku",
			Description:               "Latest Claude Haiku alias for faster, lighter turns.",
			SupportedReasoningEfforts: claudeReasoningEffortOptions(),
			DefaultReasoningEffort:    claudeDefaultReasoningEffort,
		},
	}
}

func ClaudeCodeModelOptions() []ModelOption {
	return append([]ModelOption(nil), claudeEmbeddedModelOptions()...)
}

func claudeReasoningEffortOptions() []ReasoningEffortOption {
	return []ReasoningEffortOption{
		{ReasoningEffort: "low", Description: "Fastest response"},
		{ReasoningEffort: "medium", Description: "Balanced"},
		{ReasoningEffort: "high", Description: "More deliberate"},
		{ReasoningEffort: "xhigh", Description: "Extra deliberate"},
		{ReasoningEffort: "max", Description: "Most thorough"},
	}
}

func claudeModelOptionExists(models []ModelOption, id string) bool {
	id = strings.TrimSpace(id)
	for _, option := range models {
		if strings.EqualFold(strings.TrimSpace(option.ID), id) ||
			strings.EqualFold(strings.TrimSpace(option.Model), id) {
			return true
		}
	}
	return false
}

func (s *claudeCodeSession) RespondApproval(_ ApprovalDecision) error {
	return fmt.Errorf(claudeApprovalUnsupported)
}

func (s *claudeCodeSession) RespondToolInput(_ map[string][]string) error {
	return fmt.Errorf(claudeToolInputUnsupported)
}

func (s *claudeCodeSession) RespondElicitation(_ ElicitationDecision, _ json.RawMessage) error {
	return fmt.Errorf(claudeElicitationUnsupported)
}

func (s *claudeCodeSession) Close() error {
	s.mu.Lock()
	if s.closed {
		s.mu.Unlock()
		return nil
	}
	s.closed = true
	cmd := s.cmd
	s.updateStatusLocked()
	if cmd == nil {
		s.closeClosedCh()
	}
	s.mu.Unlock()

	if cmd != nil {
		return terminateAppServerCommand(cmd)
	}
	return nil
}

func (s *claudeCodeSession) CloseDueToInactivity() error {
	return s.Close()
}

func (s *claudeCodeSession) WaitClosed(timeout time.Duration) bool {
	if timeout <= 0 {
		timeout = time.Second
	}
	select {
	case <-s.closedCh:
		return true
	case <-time.After(timeout):
		return false
	}
}

// RefreshBusyElsewhere implements busyElsewhereRefresher.
func (s *claudeCodeSession) RefreshBusyElsewhere() error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.loadTranscriptLocked()
	s.refreshActiveLocked()
	s.updateStatusLocked()
	s.notifyAsync()
	return nil
}

// ReconcileBusyState implements busyReconciler.
func (s *claudeCodeSession) ReconcileBusyState() error {
	return s.RefreshBusyElsewhere()
}

func (s *claudeCodeSession) consumeClaudeTurn(ctx context.Context, cmd *exec.Cmd, stdout, stderr io.ReadCloser) {
	stdoutErrCh := make(chan error, 1)
	stderrErrCh := make(chan error, 1)

	go func() {
		stdoutErrCh <- s.readClaudeStdout(stdout)
	}()
	go func() {
		stderrErrCh <- s.readClaudeStderr(stderr)
	}()

	waitErr := cmd.Wait()
	stdoutErr := <-stdoutErrCh
	stderrErr := <-stderrErrCh

	if ctx.Err() != nil && waitErr != nil {
		waitErr = nil
	}
	if waitErr != nil {
		if authErr := CheckClaudeCodeAuthentication(context.Background()); authErr != nil {
			waitErr = authErr
		}
	}
	s.finishClaudeTurn(waitErr, stdoutErr, stderrErr)
}

func (s *claudeCodeSession) finishClaudeTurn(waitErr, stdoutErr, stderrErr error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	compactCommand := s.compactCommand
	pendingSubmissions := s.pendingSubmissions
	s.busy = false
	s.pendingSubmissions = 0
	s.cmd = nil
	s.stdin = nil
	s.cancel = nil
	s.runningPID = 0
	interrupted := s.interruptPending && pendingSubmissions <= 1
	s.interruptPending = false

	if s.sessionID != "" {
		s.sessionFile = claudeSessionFilePath(s.claudeHome, s.projectPath, s.sessionID)
	}
	s.loadTranscriptLocked()
	s.refreshActiveLocked()
	if !s.busy && !s.externalTurnActive {
		s.busySince = time.Time{}
	}

	if compactCommand != nil {
		result, compactErr := claudeCompactionCompletion(compactCommand, waitErr, stdoutErr, stderrErr)
		s.compactCommand = nil
		s.compacting = false
		if compactErr != nil {
			if strings.TrimSpace(s.lastError) == "" {
				s.appendSystemErrorLocked(compactErr.Error())
			}
		} else {
			s.lastError = ""
			if strings.TrimSpace(result.Message) != "" && result.Message != s.lastSystemNotice {
				s.appendSystemNoticeLocked(result.Message)
			}
			s.updateStatusLocked()
		}
		compactCommand.done <- claudeCompactCompletion{result: result, err: compactErr}
	} else {
		switch {
		case interrupted:
			s.lastError = ""
			s.lastSystemNotice = claudeInterruptNotice
			s.status = claudeReadyStatus
		case errors.Is(waitErr, ErrClaudeCodeAuthenticationRequired):
			s.appendSystemErrorLocked(waitErr.Error())
		case waitErr != nil:
			s.appendSystemErrorLocked(fmt.Sprintf("Claude Code exited with error: %v", waitErr))
		case stdoutErr != nil:
			s.appendSystemErrorLocked(fmt.Sprintf("Could not read Claude Code output: %v", stdoutErr))
		case stderrErr != nil:
			s.appendSystemErrorLocked(fmt.Sprintf("Could not read Claude Code stderr: %v", stderrErr))
		default:
			s.lastError = ""
			s.lastSystemNotice = "Claude Code turn completed."
			s.updateStatusLocked()
		}
	}

	if s.closed {
		s.closeClosedCh()
	}
	s.notifyAsync()
}

func claudeCompactionCompletion(command *claudeCompactCommand, waitErr, stdoutErr, stderrErr error) (CompactionResult, error) {
	if command.boundarySeen {
		result := CompactionResult{
			Compacted: true,
			PreTokens: command.metadata.PreTokens,
			Trigger:   strings.TrimSpace(command.metadata.Trigger),
			Message:   claudeCompactionNotice(command.metadata),
		}
		return result, nil
	}

	message := strings.TrimSpace(command.resultText)
	switch {
	case command.resultError:
		if message == "" {
			message = "Claude Code returned an error while compacting conversation history"
		}
		return CompactionResult{}, errors.New(message)
	case errors.Is(waitErr, ErrClaudeCodeAuthenticationRequired):
		return CompactionResult{}, waitErr
	case waitErr != nil:
		return CompactionResult{}, fmt.Errorf("Claude Code exited while compacting conversation history: %w", waitErr)
	case stdoutErr != nil:
		return CompactionResult{}, fmt.Errorf("could not read Claude Code compaction output: %w", stdoutErr)
	case stderrErr != nil:
		return CompactionResult{}, fmt.Errorf("could not read Claude Code compaction stderr: %w", stderrErr)
	}

	if message == "" {
		message = "Claude Code completed /compact without compacting; the conversation may already be below its compaction threshold."
	}
	return CompactionResult{
		Compacted: false,
		Message:   message,
	}, nil
}

func claudeCompactionNotice(metadata claudeCompactMetadata) string {
	message := "Claude Code compacted conversation history."
	if metadata.PreTokens > 0 {
		message = fmt.Sprintf("Claude Code compacted conversation history (%d tokens before compaction).", metadata.PreTokens)
	}
	if trigger := strings.TrimSpace(metadata.Trigger); trigger != "" && !strings.EqualFold(trigger, "manual") {
		message = strings.TrimSuffix(message, ".") + "; trigger: " + trigger + "."
	}
	return message
}

func (s *claudeCodeSession) readClaudeStdout(r io.Reader) error {
	sc := bufio.NewScanner(r)
	sc.Buffer(make([]byte, 0, 64*1024), 16*1024*1024)
	for sc.Scan() {
		s.handleClaudeStdoutLine(sc.Text())
	}
	return sc.Err()
}

func (s *claudeCodeSession) readClaudeStderr(r io.Reader) error {
	sc := bufio.NewScanner(r)
	sc.Buffer(make([]byte, 0, 64*1024), 4*1024*1024)
	for sc.Scan() {
		line := strings.TrimSpace(sc.Text())
		if line == "" {
			continue
		}
		s.mu.Lock()
		s.appendEntryLocked(TranscriptEntry{
			Kind: TranscriptError,
			Text: "claude stderr: " + line,
		})
		s.lastError = "claude stderr: " + line
		s.status = "Claude Code reported an error"
		s.touchLocked()
		s.mu.Unlock()
		s.notifyAsync()
	}
	return sc.Err()
}

func (s *claudeCodeSession) handleClaudeStdoutLine(line string) {
	line = strings.TrimSpace(line)
	if line == "" {
		return
	}

	var env claudeStreamEnvelope
	if err := json.Unmarshal([]byte(line), &env); err != nil {
		s.mu.Lock()
		s.appendEntryLocked(TranscriptEntry{
			Kind: TranscriptOther,
			Text: line,
		})
		s.touchLocked()
		s.mu.Unlock()
		s.notifyAsync()
		return
	}

	var stdinToClose io.WriteCloser
	s.mu.Lock()

	if effort := strings.TrimSpace(env.Effort); effort != "" {
		s.reasoningEffort = effort
	}

	switch env.Type {
	case "system":
		switch env.Subtype {
		case "init":
			if sessionID := strings.TrimSpace(env.SessionID); sessionID != "" {
				s.sessionID = sessionID
				s.sessionFile = claudeSessionFilePath(s.claudeHome, s.projectPath, sessionID)
				s.started = true
			}
			var initMsg struct {
				Model          string `json:"model"`
				PermissionMode string `json:"permissionMode"`
			}
			if err := json.Unmarshal(env.Message, &initMsg); err == nil {
				env.Model = firstNonEmptyTrimmed(env.Model, initMsg.Model)
				env.PermissionMode = firstNonEmptyTrimmed(env.PermissionMode, initMsg.PermissionMode)
			}
			if model := concreteClaudeModel(env.Model); model != "" {
				s.model = model
				s.pendingModel = ""
			}
			if effort := strings.TrimSpace(s.pendingReasoning); effort != "" {
				s.reasoningEffort = effort
				s.pendingReasoning = ""
			}
			if mode := strings.TrimSpace(env.PermissionMode); mode != "" {
				s.lastSystemNotice = "Claude Code permission mode: " + mode
			}
			if s.compacting {
				s.status = claudeCompactingStatus
			} else {
				s.status = claudeThinkingStatus
			}
		case "compact_boundary":
			s.handleClaudeCompactBoundaryLocked(claudeCompactMetadataFromEnvelope(env))
		}
	case "assistant":
		s.handleClaudeAssistantLocked(env.Message)
	case "user":
		s.handleClaudeUserLocked(env.Message)
	case "result":
		interruptedResult := s.interruptPending
		if interruptedResult {
			s.interruptPending = false
		}
		s.applyClaudeModelUsageLocked(env.ModelUsage)
		if command := s.compactCommand; command != nil {
			command.resultText = firstNonEmptyTrimmed(env.Result, env.LastMessage, command.resultText)
			command.resultError = env.IsError
		}
		if env.IsError {
			message := strings.TrimSpace(env.Result)
			if message == "" {
				message = "Claude Code returned an error result"
			}
			if !interruptedResult && s.compactCommand == nil {
				s.appendSystemErrorLocked(message)
			}
		}
		if s.pendingSubmissions > 0 {
			s.pendingSubmissions--
		}
		if interruptedResult && s.pendingSubmissions <= 0 {
			s.lastError = ""
			s.lastSystemNotice = claudeInterruptNotice
		}
		if s.pendingSubmissions == 0 && s.stdin != nil {
			stdinToClose = s.stdin
			s.stdin = nil
		}
		s.updateStatusLocked()
	default:
	}

	s.touchLocked()
	s.mu.Unlock()
	if stdinToClose != nil {
		_ = stdinToClose.Close()
	}
	s.notifyAsync()
}

func (s *claudeCodeSession) handleClaudeAssistantLocked(raw json.RawMessage) {
	var msg claudeStreamMessage
	if err := json.Unmarshal(raw, &msg); err != nil {
		return
	}
	if model := concreteClaudeModel(msg.Model); model != "" {
		s.model = model
		s.pendingModel = ""
	}
	s.applyClaudeUsageLocked(msg.Usage)
	if msg.ID == "" {
		msg.ID = fmt.Sprintf("assistant-%d", len(s.entries))
	}
	seen := s.assistantBlocks[msg.ID]
	if seen == nil {
		seen = make(map[string]struct{})
		s.assistantBlocks[msg.ID] = seen
	}
	for _, block := range msg.Content {
		switch block.Type {
		case "text":
			text := strings.TrimSpace(block.Text)
			if text == "" {
				continue
			}
			key := "text:" + text
			if _, ok := seen[key]; ok {
				continue
			}
			seen[key] = struct{}{}
			s.appendEntryLocked(TranscriptEntry{
				ItemID: msg.ID,
				Kind:   TranscriptAgent,
				Text:   text,
			})
		case "thinking":
			thinking := strings.TrimSpace(block.Thinking)
			if thinking == "" {
				continue
			}
			key := "thinking:" + thinking
			if _, ok := seen[key]; ok {
				continue
			}
			seen[key] = struct{}{}
			s.appendEntryLocked(TranscriptEntry{
				ItemID: msg.ID,
				Kind:   TranscriptReasoning,
				Text:   thinking,
			})
		case "tool_use":
			summary, command := summarizeClaudeToolUse(block.Name, block.Input)
			key := "tool:" + block.ID + ":" + block.Name + ":" + summary
			if _, ok := seen[key]; ok {
				continue
			}
			seen[key] = struct{}{}
			text := block.Name
			if summary != "" {
				text = block.Name + ": " + summary
			}
			s.appendEntryLocked(TranscriptEntry{
				ItemID: msg.ID,
				Kind:   TranscriptTool,
				Text:   text,
			})
			if block.ID != "" {
				s.toolCalls[block.ID] = claudeToolCall{
					Name:    block.Name,
					Summary: summary,
					Command: command,
				}
			}
		}
	}
	if command := s.compactCommand; command != nil {
		for _, block := range msg.Content {
			if block.Type == "text" && strings.TrimSpace(block.Text) != "" {
				command.resultText = strings.TrimSpace(block.Text)
			}
		}
	}
}

func (s *claudeCodeSession) applyClaudeUsageLocked(usage claudeTokenUsage) {
	snapshot := claudeTokenUsageSnapshot(usage, s.modelContextWindow)
	if snapshot == nil {
		return
	}
	s.tokenUsage = snapshot
}

func claudeTokenUsageSnapshot(usage claudeTokenUsage, contextWindow int64) *TokenUsageSnapshot {
	inputTokens := max(usage.InputTokens, 0)
	cacheCreationTokens := max(usage.CacheCreationInputTokens, 0)
	cacheReadTokens := max(usage.CacheReadInputTokens, 0)
	outputTokens := max(usage.OutputTokens, 0)
	contextTokens := inputTokens + cacheCreationTokens + cacheReadTokens
	if contextTokens == 0 && outputTokens == 0 {
		return nil
	}
	breakdown := TokenUsageBreakdown{
		CachedInputTokens: cacheReadTokens,
		InputTokens:       contextTokens,
		OutputTokens:      outputTokens,
		TotalTokens:       contextTokens + outputTokens,
	}
	return &TokenUsageSnapshot{
		Last:               breakdown,
		Total:              breakdown,
		ModelContextWindow: contextWindow,
		ContextTokens:      contextTokens,
	}
}

func (s *claudeCodeSession) applyClaudeModelUsageLocked(modelUsage map[string]claudeModelUsage) {
	if len(modelUsage) == 0 {
		return
	}

	currentModel := concreteClaudeModel(s.model)
	var selected claudeModelUsage
	found := false
	for model, usage := range modelUsage {
		if currentModel != "" && strings.EqualFold(strings.TrimSpace(model), currentModel) {
			selected = usage
			found = true
			break
		}
		if !found || usage.ContextWindow > selected.ContextWindow {
			selected = usage
			found = true
		}
	}
	if !found || selected.ContextWindow <= 0 {
		return
	}
	s.modelContextWindow = selected.ContextWindow
	if s.tokenUsage != nil {
		s.tokenUsage.ModelContextWindow = selected.ContextWindow
	}
}

func claudeCompactMetadataFromEnvelope(env claudeStreamEnvelope) claudeCompactMetadata {
	if env.CompactMetadata.PreTokens > 0 || strings.TrimSpace(env.CompactMetadata.Trigger) != "" {
		return env.CompactMetadata
	}
	return env.CompactMetadataV2
}

func (s *claudeCodeSession) handleClaudeCompactBoundaryLocked(metadata claudeCompactMetadata) {
	s.tokenUsage = nil
	if command := s.compactCommand; command != nil {
		command.boundarySeen = true
		command.metadata = metadata
	}
	notice := claudeCompactionNotice(metadata)
	if notice != s.lastSystemNotice {
		s.appendSystemNoticeLocked(notice)
	}
}

func (s *claudeCodeSession) handleClaudeUserLocked(raw json.RawMessage) {
	var msg claudeStreamMessage
	if err := json.Unmarshal(raw, &msg); err != nil {
		return
	}
	for _, block := range msg.Content {
		if block.Type != "tool_result" {
			continue
		}
		toolUseID := strings.TrimSpace(block.ToolUseID)
		if toolUseID == "" {
			continue
		}
		if _, ok := s.toolResults[toolUseID]; ok {
			continue
		}
		s.toolResults[toolUseID] = struct{}{}
		call := s.toolCalls[toolUseID]
		if !strings.EqualFold(call.Name, "Bash") {
			continue
		}
		text := strings.TrimSpace(flattenClaudeToolResultContent(block.Content))
		if text == "" {
			text = "[command completed]"
		}
		command := strings.TrimSpace(call.Command)
		if command == "" {
			command = call.Summary
		}
		if command != "" {
			text = "$ " + command + "\n" + text
		}
		s.appendEntryLocked(TranscriptEntry{
			Kind: TranscriptCommand,
			Text: text,
		})
	}
}

func (s *claudeCodeSession) appendEntryLocked(entry TranscriptEntry) {
	s.invalidateTranscriptCacheLocked()
	s.entries = append(s.entries, entry)
}

func (s *claudeCodeSession) appendSystemNoticeLocked(text string) {
	text = strings.TrimSpace(text)
	if text == "" {
		return
	}
	s.lastSystemNotice = text
	s.appendEntryLocked(TranscriptEntry{
		Kind: TranscriptSystem,
		Text: text,
	})
	s.updateStatusLocked()
}

func (s *claudeCodeSession) appendSystemErrorLocked(text string) {
	text = strings.TrimSpace(text)
	if text == "" {
		return
	}
	s.lastError = text
	s.appendEntryLocked(TranscriptEntry{
		Kind: TranscriptError,
		Text: text,
	})
	s.status = "Claude Code error"
}

func (s *claudeCodeSession) touchLocked() {
	s.lastActivityAt = time.Now()
}

func (s *claudeCodeSession) updateStatusLocked() {
	switch {
	case s.closed:
		s.status = "Claude Code session closed"
	case s.externalTurnActive:
		s.status = "Claude Code session active in another terminal"
	case s.compacting:
		s.status = claudeCompactingStatus
	case s.busy:
		if s.pendingSubmissions > 0 {
			s.status = claudeThinkingStatus
		} else {
			s.status = claudeFinishingStatus
		}
	case s.busyExternal:
		s.status = claudeOpenElsewhereStatus
	case strings.TrimSpace(s.lastError) != "":
		s.status = "Claude Code error"
	case s.started:
		s.status = claudeReadyStatus
	default:
		s.status = claudeFreshReadyStatus
	}
}

func (s *claudeCodeSession) notifyAsync() {
	if s.notify != nil {
		s.notify()
	}
}

func (s *claudeCodeSession) closeClosedCh() {
	s.closedOnce.Do(func() {
		close(s.closedCh)
	})
}

func (s *claudeCodeSession) findLatestSession() (path string, sessionID string, ok bool) {
	projectsDir := filepath.Join(s.claudeHome, "projects")
	encodedPath := encodeCCProjectPath(s.projectPath)
	projectDir := filepath.Join(projectsDir, encodedPath)

	entries, err := os.ReadDir(projectDir)
	if err != nil {
		return "", "", false
	}

	var bestPath string
	var bestMod time.Time
	var bestID string

	for _, e := range entries {
		if e.IsDir() || filepath.Ext(e.Name()) != ".jsonl" {
			continue
		}
		info, err := e.Info()
		if err != nil {
			continue
		}
		if bestPath == "" || info.ModTime().After(bestMod) {
			bestPath = filepath.Join(projectDir, e.Name())
			bestMod = info.ModTime()
			bestID = strings.TrimSuffix(e.Name(), ".jsonl")
		}
	}
	if bestPath == "" {
		return "", "", false
	}
	return bestPath, bestID, true
}

func (s *claudeCodeSession) loadTranscriptLocked() {
	if strings.TrimSpace(s.sessionFile) == "" {
		return
	}
	file, err := os.Open(s.sessionFile)
	if err != nil {
		return
	}
	defer file.Close()

	stat, err := file.Stat()
	if err != nil {
		return
	}
	if stat.Size() == s.lastFileSize {
		return
	}

	sc := bufio.NewScanner(file)
	sc.Buffer(make([]byte, 0, 64*1024), 16*1024*1024)

	var entries []TranscriptEntry
	toolCalls := make(map[string]claudeToolCall)
	toolResults := make(map[string]struct{})
	var conversationTracker claudeartifact.ConversationTracker
	lastType := ""
	latestReasoningEffort := ""
	latestModel := ""
	var latestUsage *TokenUsageSnapshot
	sawTokenState := false
	for sc.Scan() {
		line := sc.Text()
		lineEntries, entryType, reasoningEffort, parsedState := parseCCLineEntries(line, toolCalls, toolResults, &conversationTracker)
		entries = append(entries, lineEntries...)
		if entryType != "" {
			lastType = entryType
		}
		if reasoningEffort != "" {
			latestReasoningEffort = reasoningEffort
		}
		if parsedState.model != "" {
			latestModel = parsedState.model
		}
		if parsedState.usage != nil {
			latestUsage = parsedState.usage
			sawTokenState = true
		}
		if parsedState.compactBoundary {
			latestUsage = nil
			sawTokenState = true
			if command := s.compactCommand; command != nil {
				command.boundarySeen = true
				command.metadata = parsedState.compactMetadata
			}
		}
	}

	s.entries = entries
	if latestReasoningEffort != "" {
		s.reasoningEffort = latestReasoningEffort
	}
	if latestModel != "" {
		s.model = latestModel
	}
	if sawTokenState {
		s.tokenUsage = cloneTokenUsageSnapshot(latestUsage)
		if s.tokenUsage != nil {
			s.tokenUsage.ModelContextWindow = s.modelContextWindow
		}
	}
	s.invalidateTranscriptCacheLocked()
	s.lastFileSize = stat.Size()

	switch lastType {
	case "assistant", "progress":
		s.busyExternal = true
		s.externalTurnActive = true
	default:
		s.busyExternal = false
		s.externalTurnActive = false
	}

	if len(entries) > 0 {
		s.lastActivityAt = stat.ModTime()
	}
}

func (s *claudeCodeSession) refreshActiveLocked() {
	if strings.TrimSpace(s.sessionID) == "" {
		s.busyExternal = false
		s.externalTurnActive = false
		if !s.busy {
			s.busySince = time.Time{}
		}
		return
	}
	sessionsDir := filepath.Join(s.claudeHome, "sessions")
	entries, err := os.ReadDir(sessionsDir)
	if err != nil {
		return
	}
	external := false
	turnActive := false
	activeStartedAt := time.Time{}
	for _, e := range entries {
		if e.IsDir() || filepath.Ext(e.Name()) != ".json" {
			continue
		}
		data, err := os.ReadFile(filepath.Join(sessionsDir, e.Name()))
		if err != nil {
			continue
		}
		var pidSession claudeActivePIDSession
		if err := json.Unmarshal(data, &pidSession); err != nil {
			continue
		}
		if pidSession.SessionID != s.sessionID {
			continue
		}
		if pidSession.PID > 0 && pidSession.PID == s.runningPID {
			continue
		}
		if pidSession.PID > 0 && syscall.Kill(pidSession.PID, 0) == nil {
			external = true
			if !claudePIDSessionTurnActive(pidSession.Status) {
				continue
			}
			turnActive = true
			startedAt := claudePIDSessionTurnStartedAt(pidSession)
			if activeStartedAt.IsZero() || (!startedAt.IsZero() && startedAt.Before(activeStartedAt)) {
				activeStartedAt = startedAt
			}
		}
	}
	s.busyExternal = external
	s.externalTurnActive = turnActive
	switch {
	case turnActive && !activeStartedAt.IsZero():
		s.busySince = activeStartedAt
	case !turnActive && !s.busy:
		s.busySince = time.Time{}
	}
}

func claudePIDSessionTurnActive(status string) bool {
	switch strings.ToLower(strings.TrimSpace(status)) {
	case claudePIDStatusIdle:
		return false
	case claudePIDStatusBusy, claudePIDStatusShell:
		return true
	default:
		// Older Claude Code versions did not publish structured status. Preserve
		// the live-PID fallback for those versions and unknown future states.
		return true
	}
}

func claudePIDSessionTurnStartedAt(session claudeActivePIDSession) time.Time {
	if session.StatusUpdatedAt > 0 && strings.TrimSpace(session.Status) != "" {
		return time.UnixMilli(session.StatusUpdatedAt)
	}
	if session.StartedAt > 0 {
		return time.UnixMilli(session.StartedAt)
	}
	return time.Time{}
}

func startClaudeTurnWithRuntimeMCP(ctx context.Context, projectPath, resumeID, model, reasoning, permissionMode string, policy browserctl.Policy, runtimeMCPConfig, runtimeMCPPrompt, safetySettings string) (*exec.Cmd, io.WriteCloser, io.ReadCloser, io.ReadCloser, error) {
	if strings.TrimSpace(safetySettings) == "" {
		return nil, nil, nil, nil, fmt.Errorf("Claude Code safety-hook settings are required")
	}
	args := claudeTurnArgsWithRuntimeMCP(resumeID, model, reasoning, permissionMode, runtimeMCPConfig, runtimeMCPPrompt, safetySettings)

	cmd := exec.CommandContext(ctx, "claude", args...)
	cmd.Dir = projectPath
	configureAppServerCommand(cmd)
	applyPlaywrightPolicyEnvironment(cmd, ProviderClaudeCode, policy)

	stdin, err := cmd.StdinPipe()
	if err != nil {
		return nil, nil, nil, nil, err
	}
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return nil, nil, nil, nil, err
	}
	stderr, err := cmd.StderrPipe()
	if err != nil {
		return nil, nil, nil, nil, err
	}
	if err := cmd.Start(); err != nil {
		return nil, nil, nil, nil, err
	}

	return cmd, stdin, stdout, stderr, nil
}

func claudeTurnArgs(resumeID, model, reasoning, permissionMode string) []string {
	args := []string{
		"-p",
		"--verbose",
		"--input-format=stream-json",
		"--output-format=stream-json",
		"--permission-mode", permissionMode,
	}
	if strings.TrimSpace(resumeID) != "" {
		args = append(args, "--resume", strings.TrimSpace(resumeID))
	}
	if model = concreteClaudeModel(model); model != "" {
		args = append(args, "--model", model)
	}
	if strings.TrimSpace(reasoning) != "" {
		args = append(args, "--effort", strings.TrimSpace(reasoning))
	}
	return args
}

func buildClaudeStreamInput(input Submission) (string, error) {
	input = normalizeSubmission(input)
	content := make([]map[string]any, 0, 1+len(input.Attachments))
	if input.Text != "" {
		content = append(content, map[string]any{
			"type": "text",
			"text": input.Text,
		})
	}
	for _, attachment := range input.Attachments {
		if attachment.Kind != AttachmentLocalImage {
			return "", fmt.Errorf("unsupported Claude Code attachment kind %q", attachment.Kind)
		}
		path := strings.TrimSpace(attachment.Path)
		data, err := os.ReadFile(path)
		if err != nil {
			return "", fmt.Errorf("read image attachment %q: %w", path, err)
		}
		mediaType := strings.ToLower(strings.TrimSpace(strings.SplitN(http.DetectContentType(data), ";", 2)[0]))
		switch mediaType {
		case "image/jpeg", "image/png", "image/gif", "image/webp":
		default:
			return "", fmt.Errorf("unsupported Claude Code image type %q for %q", mediaType, path)
		}
		content = append(content, map[string]any{
			"type": "image",
			"source": map[string]any{
				"type":       "base64",
				"media_type": mediaType,
				"data":       base64.StdEncoding.EncodeToString(data),
			},
		})
	}
	if len(content) == 0 {
		return "", fmt.Errorf("Claude Code prompt required")
	}
	payload := map[string]any{
		"type": "user",
		"message": map[string]any{
			"role":    "user",
			"content": content,
		},
	}
	data, err := json.Marshal(payload)
	if err != nil {
		return "", err
	}
	return string(data), nil
}

func buildClaudeInterruptRequest() (string, error) {
	payload := map[string]any{
		"type":       "control_request",
		"request_id": fmt.Sprintf("interrupt-%d", time.Now().UnixNano()),
		"request": map[string]any{
			"subtype": "interrupt",
		},
	}
	data, err := json.Marshal(payload)
	if err != nil {
		return "", err
	}
	return string(data), nil
}

func claudeSessionFilePath(claudeHome, projectPath, sessionID string) string {
	if strings.TrimSpace(sessionID) == "" {
		return ""
	}
	return filepath.Join(claudeHome, "projects", encodeCCProjectPath(projectPath), sessionID+".jsonl")
}

func claudePermissionModeForPreset(preset codexcli.Preset) (mode string, notice string) {
	switch preset {
	case codexcli.PresetYolo:
		return "bypassPermissions", claudeYoloPresetMappingNotice
	case codexcli.PresetFullAuto, codexcli.PresetSafe:
		return "acceptEdits", claudeSafePresetMappingNotice
	default:
		return "acceptEdits", claudeSafePresetMappingNotice
	}
}

func claudePermissionModeLabel(preset codexcli.Preset) string {
	mode, _ := claudePermissionModeForPreset(preset)
	return mode
}

func summarizeClaudeToolUse(name string, input json.RawMessage) (summary string, command string) {
	var fields map[string]json.RawMessage
	if err := json.Unmarshal(input, &fields); err != nil {
		return "", ""
	}
	switch name {
	case "Read", "Edit", "Write":
		return ccExtractString(fields, "file_path"), ""
	case "Bash":
		command = ccExtractString(fields, "command")
		if len(command) > 120 {
			return command[:120] + "...", command
		}
		return command, command
	case "Glob", "Grep":
		return ccExtractString(fields, "pattern"), ""
	case "Agent", "Task":
		return ccExtractString(fields, "description"), ""
	case "AskUserQuestion":
		return ccExtractString(fields, "question"), ""
	default:
		return "", ""
	}
}

func flattenClaudeToolResultContent(content any) string {
	switch v := content.(type) {
	case string:
		return v
	case []any:
		parts := make([]string, 0, len(v))
		for _, item := range v {
			part := strings.TrimSpace(flattenClaudeToolResultContent(item))
			if part != "" {
				parts = append(parts, part)
			}
		}
		return strings.Join(parts, "\n")
	case map[string]any:
		if text, ok := v["text"].(string); ok && strings.TrimSpace(text) != "" {
			return text
		}
		if file, ok := v["file"].(map[string]any); ok {
			if text, ok := file["content"].(string); ok && strings.TrimSpace(text) != "" {
				return text
			}
		}
	}
	return ""
}

func firstNonEmptyTrimmed(values ...string) string {
	for _, value := range values {
		value = strings.TrimSpace(value)
		if value != "" {
			return value
		}
	}
	return ""
}

// Claude Code uses <synthetic> as message.model for locally generated
// assistant records such as usage-limit and model-access errors. It is a
// protocol placeholder, not a model name accepted by the --model flag.
func concreteClaudeModel(model string) string {
	model = strings.TrimSpace(model)
	if model == claudeSyntheticModelPlaceholder {
		return ""
	}
	return model
}

func parseCCLineEntries(
	line string,
	toolCalls map[string]claudeToolCall,
	toolResults map[string]struct{},
	conversationTracker *claudeartifact.ConversationTracker,
) ([]TranscriptEntry, string, string, claudeParsedLineState) {
	var raw struct {
		Type              string                `json:"type"`
		Subtype           string                `json:"subtype"`
		IsMeta            bool                  `json:"isMeta"`
		IsCompactSummary  bool                  `json:"isCompactSummary"`
		UUID              string                `json:"uuid"`
		ParentUUID        string                `json:"parentUuid"`
		PromptSource      string                `json:"promptSource"`
		Effort            string                `json:"effort"`
		Model             string                `json:"model"`
		CompactMetadata   claudeCompactMetadata `json:"compact_metadata"`
		CompactMetadataV2 claudeCompactMetadata `json:"compactMetadata"`
		Origin            struct {
			Kind string `json:"kind"`
		} `json:"origin"`
		Message struct {
			Role    string           `json:"role"`
			Content json.RawMessage  `json:"content"`
			Model   string           `json:"model"`
			Usage   claudeTokenUsage `json:"usage"`
		} `json:"message"`
	}
	if err := json.Unmarshal([]byte(line), &raw); err != nil {
		return nil, "", "", claudeParsedLineState{}
	}
	reasoningEffort := strings.TrimSpace(raw.Effort)
	state := claudeParsedLineState{
		model: concreteClaudeModel(firstNonEmptyTrimmed(raw.Message.Model, raw.Model)),
	}
	if raw.Type == "assistant" {
		state.usage = claudeTokenUsageSnapshot(raw.Message.Usage, 0)
	}
	if raw.Type == "system" && raw.Subtype == "compact_boundary" {
		state.compactBoundary = true
		state.compactMetadata = raw.CompactMetadata
		if state.compactMetadata.PreTokens == 0 && strings.TrimSpace(state.compactMetadata.Trigger) == "" {
			state.compactMetadata = raw.CompactMetadataV2
		}
	}
	includeUserText := true
	if conversationTracker != nil {
		includeUserText = conversationTracker.Observe(claudeartifact.TranscriptEntry{
			Type:             raw.Type,
			UUID:             raw.UUID,
			ParentUUID:       raw.ParentUUID,
			IsMeta:           raw.IsMeta,
			IsCompactSummary: raw.IsCompactSummary,
			PromptSource:     raw.PromptSource,
			OriginKind:       raw.Origin.Kind,
		})
	}

	if raw.IsMeta || raw.IsCompactSummary {
		return nil, raw.Type, reasoningEffort, state
	}

	switch raw.Type {
	case "user":
		return extractCCUserEntries(raw.Message.Content, raw.UUID, includeUserText, toolCalls, toolResults), raw.Type, reasoningEffort, state

	case "assistant":
		return extractCCAssistantEntries(raw.Message.Content, raw.UUID, toolCalls), raw.Type, reasoningEffort, state

	case "progress":
		return nil, raw.Type, reasoningEffort, state
	case "system":
		if state.compactBoundary {
			return []TranscriptEntry{{
				Kind: TranscriptSystem,
				Text: claudeCompactionNotice(state.compactMetadata),
			}}, raw.Type, reasoningEffort, state
		}
		return nil, raw.Type, reasoningEffort, state
	default:
		return nil, raw.Type, reasoningEffort, state
	}
}

type claudeParsedLineState struct {
	model           string
	usage           *TokenUsageSnapshot
	compactBoundary bool
	compactMetadata claudeCompactMetadata
}

func extractCCTextContent(content json.RawMessage) string {
	if len(content) == 0 {
		return ""
	}
	var s string
	if err := json.Unmarshal(content, &s); err == nil {
		return s
	}
	var blocks []struct {
		Type string `json:"type"`
		Text string `json:"text"`
	}
	if err := json.Unmarshal(content, &blocks); err != nil {
		return ""
	}
	var parts []string
	for _, b := range blocks {
		if b.Type == "text" && strings.TrimSpace(b.Text) != "" {
			parts = append(parts, b.Text)
		}
	}
	return strings.Join(parts, "\n")
}

func extractCCAssistantEntries(content json.RawMessage, itemID string, toolCalls map[string]claudeToolCall) []TranscriptEntry {
	if len(content) == 0 {
		return nil
	}
	var blocks []struct {
		ID       string          `json:"id"`
		Type     string          `json:"type"`
		Text     string          `json:"text"`
		Thinking string          `json:"thinking"`
		Name     string          `json:"name"`
		Input    json.RawMessage `json:"input"`
	}
	if err := json.Unmarshal(content, &blocks); err != nil {
		return nil
	}
	entries := make([]TranscriptEntry, 0, len(blocks))
	for _, b := range blocks {
		switch b.Type {
		case "text":
			if text := strings.TrimSpace(b.Text); text != "" {
				entries = append(entries, TranscriptEntry{
					ItemID: itemID,
					Kind:   TranscriptAgent,
					Text:   text,
				})
			}
		case "thinking":
			if thinking := strings.TrimSpace(b.Thinking); thinking != "" {
				entries = append(entries, TranscriptEntry{
					ItemID: itemID,
					Kind:   TranscriptReasoning,
					Text:   thinking,
				})
			}
		case "tool_use":
			summary, command := summarizeClaudeToolUse(b.Name, b.Input)
			text := b.Name
			if summary != "" {
				text = b.Name + ": " + summary
			}
			entries = append(entries, TranscriptEntry{
				ItemID: itemID,
				Kind:   TranscriptTool,
				Text:   text,
			})
			if b.ID != "" && toolCalls != nil {
				toolCalls[b.ID] = claudeToolCall{
					Name:    b.Name,
					Summary: summary,
					Command: command,
				}
			}
		}
	}
	return entries
}

func extractCCUserEntries(
	content json.RawMessage,
	itemID string,
	includeText bool,
	toolCalls map[string]claudeToolCall,
	toolResults map[string]struct{},
) []TranscriptEntry {
	if len(content) == 0 {
		return nil
	}
	entries := make([]TranscriptEntry, 0, 2)
	if includeText {
		text := extractCCTextContent(content)
		if strings.TrimSpace(text) != "" {
			entries = append(entries, TranscriptEntry{
				ItemID: itemID,
				Kind:   TranscriptUser,
				Text:   text,
			})
		}
	}

	var blocks []struct {
		Type      string          `json:"type"`
		Content   json.RawMessage `json:"content"`
		ToolUseID string          `json:"tool_use_id"`
	}
	if err := json.Unmarshal(content, &blocks); err != nil {
		return entries
	}
	for _, block := range blocks {
		if block.Type != "tool_result" {
			continue
		}
		toolUseID := strings.TrimSpace(block.ToolUseID)
		if toolUseID == "" {
			continue
		}
		if _, ok := toolResults[toolUseID]; ok {
			continue
		}
		if toolResults != nil {
			toolResults[toolUseID] = struct{}{}
		}
		call := toolCalls[toolUseID]
		if !strings.EqualFold(call.Name, "Bash") {
			continue
		}
		result := flattenClaudeToolResultRaw(block.Content)
		if result == "" {
			result = "[command completed]"
		}
		command := strings.TrimSpace(call.Command)
		if command == "" {
			command = strings.TrimSpace(call.Summary)
		}
		if command != "" {
			result = "$ " + command + "\n" + result
		}
		entries = append(entries, TranscriptEntry{
			Kind: TranscriptCommand,
			Text: result,
		})
	}
	return entries
}

func flattenClaudeToolResultRaw(raw json.RawMessage) string {
	if len(raw) == 0 {
		return ""
	}
	var content any
	if err := json.Unmarshal(raw, &content); err != nil {
		return ""
	}
	return strings.TrimSpace(flattenClaudeToolResultContent(content))
}

func ccExtractString(fields map[string]json.RawMessage, key string) string {
	raw, ok := fields[key]
	if !ok {
		return ""
	}
	var s string
	if err := json.Unmarshal(raw, &s); err != nil {
		return ""
	}
	return s
}

func encodeCCProjectPath(projectPath string) string {
	cleaned := filepath.Clean(projectPath)
	return strings.ReplaceAll(cleaned, "/", "-")
}
