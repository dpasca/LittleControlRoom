package codexapp

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os/exec"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"lcroom/internal/codexcli"
)

const (
	rpcTimeout         = 15 * time.Second
	idleShutdownAfter  = time.Hour
	idleShutdownNotice = "Closed embedded Codex session after 1 hour of inactivity."
)

type appServerSession struct {
	projectPath string
	preset      codexcli.Preset
	notify      func()
	rpcCallHook func(context.Context, string, any) (json.RawMessage, error)

	cmd   *exec.Cmd
	stdin io.WriteCloser

	writeMu sync.Mutex

	pendingMu sync.Mutex
	pending   map[string]chan rpcEnvelope
	nextID    int64
	exitCh    chan struct{}
	exitOnce  sync.Once

	mu                 sync.Mutex
	threadID           string
	activeTurnID       string
	activeItems        map[string]struct{}
	pendingCompletion  *turnCompletionState
	started            bool
	busy               bool
	busyExternal       bool
	reconciling        bool
	reportedAuth403    bool
	busySince          time.Time
	closed             bool
	status             string
	lastError          string
	lastSystemNotice   string
	lastActivityAt     time.Time
	lastBusyActivityAt time.Time
	currentCWD         string
	model              string
	modelProvider      string
	reasoningEffort    string
	pendingModel       string
	pendingReasoning   string
	serviceTier        string
	approvalPolicy     json.RawMessage
	sandboxPolicy      json.RawMessage
	tokenUsage         *threadTokenUsage
	rateLimits         *rateLimitSnapshot
	rateLimitsByID     map[string]rateLimitSnapshot
	pendingApproval    *ApprovalRequest
	pendingToolInput   *ToolInputRequest
	pendingElicitation *ElicitationRequest
	entries            []transcriptEntry
	entryIndex         map[string]int
}

type transcriptEntry struct {
	ItemID string
	Kind   TranscriptKind
	Text   string
}

type turnCompletionState struct {
	TurnID string
	Status string
}

type rpcEnvelope struct {
	Method string          `json:"method,omitempty"`
	Params json.RawMessage `json:"params,omitempty"`
	ID     json.RawMessage `json:"id,omitempty"`
	Result json.RawMessage `json:"result,omitempty"`
	Error  *rpcError       `json:"error,omitempty"`
}

type rpcError struct {
	Code    int    `json:"code"`
	Message string `json:"message"`
}

type rpcRequest struct {
	Method string `json:"method"`
	ID     any    `json:"id"`
	Params any    `json:"params,omitempty"`
}

type rpcNotification struct {
	Method string `json:"method"`
	Params any    `json:"params,omitempty"`
}

type rpcResponse struct {
	ID     any `json:"id"`
	Result any `json:"result,omitempty"`
}

type rpcErrorResponse struct {
	ID    any      `json:"id"`
	Error rpcError `json:"error"`
}

type threadResponse struct {
	ApprovalPolicy  json.RawMessage `json:"approvalPolicy"`
	CWD             string          `json:"cwd"`
	Model           string          `json:"model"`
	ModelProvider   string          `json:"modelProvider"`
	ReasoningEffort *string         `json:"reasoningEffort"`
	ServiceTier     *string         `json:"serviceTier"`
	Sandbox         json.RawMessage `json:"sandbox"`
	Thread          struct {
		ID string `json:"id"`
	} `json:"thread"`
}

type resumedThreadStatus struct {
	Type        string   `json:"type"`
	ActiveFlags []string `json:"activeFlags"`
}

type threadStatusChangedNotification struct {
	ThreadID string              `json:"threadId"`
	Status   resumedThreadStatus `json:"status"`
}

type resumedTurnError struct {
	Message string `json:"message"`
}

type resumedTurn struct {
	ID     string                       `json:"id"`
	Status string                       `json:"status"`
	Error  *resumedTurnError            `json:"error"`
	Items  []map[string]json.RawMessage `json:"items"`
}

type resumedThread struct {
	ID     string              `json:"id"`
	Status resumedThreadStatus `json:"status"`
	Turns  []resumedTurn       `json:"turns"`
}

type threadResumeResponse struct {
	ApprovalPolicy  json.RawMessage `json:"approvalPolicy"`
	CWD             string          `json:"cwd"`
	Model           string          `json:"model"`
	ModelProvider   string          `json:"modelProvider"`
	ReasoningEffort *string         `json:"reasoningEffort"`
	ServiceTier     *string         `json:"serviceTier"`
	Sandbox         json.RawMessage `json:"sandbox"`
	Thread          resumedThread   `json:"thread"`
}

type threadReadResponse struct {
	Thread resumedThread `json:"thread"`
}

type turnStartResponse struct {
	Turn struct {
		ID string `json:"id"`
	} `json:"turn"`
}

type turnSteerResponse struct {
	TurnID string `json:"turnId"`
}

type modelListParams struct {
	Cursor        *string `json:"cursor,omitempty"`
	Limit         *int    `json:"limit,omitempty"`
	IncludeHidden *bool   `json:"includeHidden,omitempty"`
}

type modelListResponse struct {
	Data       []modelListEntry `json:"data"`
	NextCursor *string          `json:"nextCursor"`
}

type modelListEntry struct {
	ID                        string                       `json:"id"`
	Model                     string                       `json:"model"`
	DisplayName               string                       `json:"displayName"`
	Description               string                       `json:"description"`
	Hidden                    bool                         `json:"hidden"`
	SupportedReasoningEfforts []reasoningEffortOptionEntry `json:"supportedReasoningEfforts"`
	DefaultReasoningEffort    string                       `json:"defaultReasoningEffort"`
	SupportsPersonality       bool                         `json:"supportsPersonality"`
	IsDefault                 bool                         `json:"isDefault"`
}

type reasoningEffortOptionEntry struct {
	ReasoningEffort string `json:"reasoningEffort"`
	Description     string `json:"description"`
}

type turnInterruptParams struct {
	ThreadID string `json:"threadId"`
	TurnID   string `json:"turnId"`
}

type turnSteerParams struct {
	ThreadID       string      `json:"threadId"`
	ExpectedTurnID string      `json:"expectedTurnId"`
	Input          []userInput `json:"input"`
}

type turnStartParams struct {
	ThreadID string      `json:"threadId"`
	Input    []userInput `json:"input"`
	Model    string      `json:"model,omitempty"`
	Effort   string      `json:"effort,omitempty"`
}

type threadStartParams struct {
	CWD            string `json:"cwd"`
	ApprovalPolicy string `json:"approvalPolicy"`
	Sandbox        string `json:"sandbox"`
	ServiceName    string `json:"serviceName"`
}

type threadResumeParams struct {
	ThreadID       string `json:"threadId"`
	ApprovalPolicy string `json:"approvalPolicy"`
	Sandbox        string `json:"sandbox"`
}

type threadReadParams struct {
	ThreadID     string `json:"threadId"`
	IncludeTurns bool   `json:"includeTurns,omitempty"`
}

type userInput struct {
	Type         string        `json:"type"`
	Text         string        `json:"text,omitempty"`
	Path         string        `json:"path,omitempty"`
	URL          string        `json:"url,omitempty"`
	TextElements []textElement `json:"text_elements,omitempty"`
}

type textElement struct {
	ByteRange   byteRange `json:"byteRange"`
	Placeholder string    `json:"placeholder,omitempty"`
}

type byteRange struct {
	Start int `json:"start"`
	End   int `json:"end"`
}

type threadStartedNotification struct {
	Thread struct {
		ID string `json:"id"`
	} `json:"thread"`
}

type turnNotification struct {
	ThreadID string `json:"threadId"`
	Turn     struct {
		ID     string `json:"id"`
		Status string `json:"status"`
	} `json:"turn"`
}

type deltaNotification struct {
	ThreadID string `json:"threadId"`
	TurnID   string `json:"turnId"`
	ItemID   string `json:"itemId"`
	Delta    string `json:"delta"`
}

type reasoningSummaryPartAddedNotification struct {
	ThreadID     string `json:"threadId"`
	TurnID       string `json:"turnId"`
	ItemID       string `json:"itemId"`
	SummaryIndex int64  `json:"summaryIndex"`
}

type mcpToolCallProgressNotification struct {
	ThreadID string `json:"threadId"`
	TurnID   string `json:"turnId"`
	ItemID   string `json:"itemId"`
	Message  string `json:"message"`
}

type serverRequestResolvedNotification struct {
	ThreadID  string `json:"threadId"`
	RequestID any    `json:"requestId"`
}

type threadTokenUsageUpdatedNotification struct {
	ThreadID   string           `json:"threadId"`
	TurnID     string           `json:"turnId"`
	TokenUsage threadTokenUsage `json:"tokenUsage"`
}

type threadTokenUsage struct {
	Last               tokenUsageBreakdown `json:"last"`
	Total              tokenUsageBreakdown `json:"total"`
	ModelContextWindow *int64              `json:"modelContextWindow"`
}

type tokenUsageBreakdown struct {
	CachedInputTokens     int64 `json:"cachedInputTokens"`
	InputTokens           int64 `json:"inputTokens"`
	OutputTokens          int64 `json:"outputTokens"`
	ReasoningOutputTokens int64 `json:"reasoningOutputTokens"`
	TotalTokens           int64 `json:"totalTokens"`
}

type accountRateLimitsUpdatedNotification struct {
	RateLimits rateLimitSnapshot `json:"rateLimits"`
}

type modelReroutedNotification struct {
	ThreadID  string `json:"threadId"`
	FromModel string `json:"fromModel"`
	ToModel   string `json:"toModel"`
	Reason    string `json:"reason"`
}

type accountRateLimitsResponse struct {
	RateLimits     rateLimitSnapshot            `json:"rateLimits"`
	RateLimitsByID map[string]rateLimitSnapshot `json:"rateLimitsByLimitId"`
}

type rateLimitSnapshot struct {
	LimitID   *string           `json:"limitId"`
	LimitName *string           `json:"limitName"`
	PlanType  *string           `json:"planType"`
	Primary   *rateLimitWindow  `json:"primary"`
	Secondary *rateLimitWindow  `json:"secondary"`
	Credits   *rateLimitCredits `json:"credits"`
}

type rateLimitWindow struct {
	UsedPercent        int    `json:"usedPercent"`
	ResetsAt           *int64 `json:"resetsAt"`
	WindowDurationMins *int64 `json:"windowDurationMins"`
}

type rateLimitCredits struct {
	Balance    *string `json:"balance"`
	HasCredits bool    `json:"hasCredits"`
	Unlimited  bool    `json:"unlimited"`
}

type commandApprovalParams struct {
	RequestID string
	ThreadID  string `json:"threadId"`
	TurnID    string `json:"turnId"`
	ItemID    string `json:"itemId"`
	Command   string `json:"command"`
	CWD       string `json:"cwd"`
	Reason    string `json:"reason"`
}

type fileChangeApprovalParams struct {
	RequestID string
	ThreadID  string `json:"threadId"`
	TurnID    string `json:"turnId"`
	ItemID    string `json:"itemId"`
	GrantRoot string `json:"grantRoot"`
	Reason    string `json:"reason"`
}

type toolRequestUserInputParams struct {
	RequestID string
	ThreadID  string `json:"threadId"`
	TurnID    string `json:"turnId"`
	ItemID    string `json:"itemId"`
	Questions []struct {
		Header   string `json:"header"`
		ID       string `json:"id"`
		Question string `json:"question"`
		IsOther  bool   `json:"isOther"`
		IsSecret bool   `json:"isSecret"`
		Options  []struct {
			Label       string `json:"label"`
			Description string `json:"description"`
		} `json:"options"`
	} `json:"questions"`
}

type mcpServerElicitationRequestParams struct {
	RequestID       string
	ServerName      string          `json:"serverName"`
	ThreadID        string          `json:"threadId"`
	TurnID          string          `json:"turnId"`
	Mode            string          `json:"mode"`
	Message         string          `json:"message"`
	URL             string          `json:"url"`
	ElicitationID   string          `json:"elicitationId"`
	RequestedSchema json.RawMessage `json:"requestedSchema"`
}

func newAppServerSession(req LaunchRequest, notify func()) (Session, error) {
	s := &appServerSession{
		projectPath:    req.ProjectPath,
		preset:         req.Preset,
		notify:         notify,
		pending:        make(map[string]chan rpcEnvelope),
		exitCh:         make(chan struct{}),
		activeItems:    make(map[string]struct{}),
		entryIndex:     make(map[string]int),
		status:         "Starting Codex app-server...",
		lastActivityAt: time.Now(),
	}
	if err := s.start(req); err != nil {
		_ = s.Close()
		return nil, err
	}
	return s, nil
}

func (s *appServerSession) ProjectPath() string {
	return s.projectPath
}

func (s *appServerSession) Snapshot() Snapshot {
	s.mu.Lock()
	defer s.mu.Unlock()
	entries := make([]TranscriptEntry, 0, len(s.entries))
	lines := make([]string, 0, len(s.entries))
	for _, entry := range s.entries {
		text := strings.TrimSpace(entry.Text)
		if text == "" {
			continue
		}
		entries = append(entries, TranscriptEntry{
			ItemID: entry.ItemID,
			Kind:   entry.Kind,
			Text:   text,
		})
		lines = append(lines, formatTranscriptEntry(entry.Kind, text))
	}
	tokenUsage := exportedTokenUsageSnapshot(s.tokenUsage)
	usageWindows := exportedUsageWindowsSnapshot(s.rateLimits, s.rateLimitsByID)
	return Snapshot{
		Provider:           ProviderCodex,
		ProjectPath:        s.projectPath,
		ThreadID:           s.threadID,
		Preset:             s.preset,
		Phase:              s.phaseLocked(),
		Started:            s.started,
		Busy:               s.busy,
		BusyExternal:       s.busyExternal,
		BusySince:          s.busySince,
		LastBusyActivityAt: s.lastBusyActivityAt,
		Closed:             s.closed,
		ActiveTurnID:       s.activeTurnID,
		PendingApproval:    cloneApprovalRequest(s.pendingApproval),
		PendingToolInput:   cloneToolInputRequest(s.pendingToolInput),
		PendingElicitation: cloneElicitationRequest(s.pendingElicitation),
		Entries:            entries,
		Transcript:         strings.Join(lines, "\n\n"),
		Status:             s.status,
		LastError:          s.lastError,
		LastSystemNotice:   s.lastSystemNotice,
		LastActivityAt:     s.lastActivityAt,
		CurrentCWD:         s.currentCWD,
		Model:              s.model,
		ModelProvider:      s.modelProvider,
		ReasoningEffort:    s.reasoningEffort,
		ServiceTier:        s.serviceTier,
		PendingModel:       s.pendingModel,
		PendingReasoning:   s.pendingReasoning,
		TokenUsage:         tokenUsage,
		UsageWindows:       usageWindows,
	}
}

func newEmbeddedSession(req LaunchRequest, notify func()) (Session, error) {
	switch req.Provider.Normalized() {
	case ProviderOpenCode:
		return newOpenCodeSession(req, notify)
	default:
		return newAppServerSession(req, notify)
	}
}

func (s *appServerSession) phaseLocked() SessionPhase {
	switch {
	case s.closed:
		return SessionPhaseClosed
	case s.busyExternal:
		return SessionPhaseExternal
	case s.reconciling:
		return SessionPhaseReconciling
	case s.pendingCompletion != nil && s.busy:
		return SessionPhaseFinishing
	case s.busy:
		return SessionPhaseRunning
	default:
		return SessionPhaseIdle
	}
}

func (s *appServerSession) setBusyLocked(turnID string, external bool) {
	turnID = strings.TrimSpace(turnID)
	turnChanged := turnID != "" && s.activeTurnID != "" && s.activeTurnID != turnID
	if turnChanged {
		s.activeItems = nil
		s.pendingCompletion = nil
		s.busySince = time.Time{}
	}
	if s.pendingCompletion != nil {
		pendingTurnID := strings.TrimSpace(s.pendingCompletion.TurnID)
		if turnID != "" && pendingTurnID != "" && pendingTurnID != turnID {
			s.pendingCompletion = nil
		}
	}
	s.busy = true
	s.busyExternal = external
	s.reconciling = false
	if turnID != "" {
		s.activeTurnID = turnID
	}
	now := time.Now()
	if s.busySince.IsZero() {
		s.busySince = now
	}
	s.lastActivityAt = now
	s.lastBusyActivityAt = now
}

func (s *appServerSession) clearBusyLocked(turnID string) {
	s.busy = false
	s.busyExternal = false
	s.reconciling = false
	s.busySince = time.Time{}
	s.lastBusyActivityAt = time.Time{}
	s.activeItems = nil
	s.pendingCompletion = nil
	if turnID == "" || s.activeTurnID == turnID {
		s.activeTurnID = ""
	}
}

func (s *appServerSession) markItemActiveLocked(turnID, itemID string) {
	s.reconciling = false
	if itemID = strings.TrimSpace(itemID); itemID != "" {
		if s.activeItems == nil {
			s.activeItems = make(map[string]struct{})
		}
		s.activeItems[itemID] = struct{}{}
	}
	turnID = strings.TrimSpace(turnID)
	if turnID == "" {
		return
	}
	if !s.busy || s.activeTurnID == "" || s.activeTurnID == turnID {
		s.setBusyLocked(turnID, false)
	}
}

func (s *appServerSession) markItemCompletedLocked(itemID string) {
	itemID = strings.TrimSpace(itemID)
	if itemID != "" && len(s.activeItems) > 0 {
		delete(s.activeItems, itemID)
		if len(s.activeItems) == 0 {
			s.activeItems = nil
		}
	}
	if len(s.activeItems) == 0 {
		s.finishPendingCompletionLocked()
	}
}

func (s *appServerSession) queueTurnCompletionLocked(turnID, status string) {
	s.pendingCompletion = &turnCompletionState{
		TurnID: strings.TrimSpace(turnID),
		Status: strings.TrimSpace(status),
	}
	if len(s.activeItems) == 0 {
		s.finishPendingCompletionLocked()
	}
}

func (s *appServerSession) finishPendingCompletionLocked() {
	if s.pendingCompletion == nil {
		return
	}
	completion := *s.pendingCompletion
	s.clearBusyLocked(completion.TurnID)
	if completion.Status != "" {
		s.status = completion.Status
		s.lastSystemNotice = completion.Status
	}
}

func tracksBusyItemLifecycle(itemType string) bool {
	switch strings.TrimSpace(itemType) {
	case "agentMessage", "commandExecution", "fileChange", "plan", "reasoning", "mcpToolCall":
		return true
	default:
		return false
	}
}

func formatTurnCompletionStatus(turnStatus string, busySince, now time.Time) string {
	status := normalizeTurnStatus(turnStatus)
	switch status {
	case "", "complete", "completed":
		if !busySince.IsZero() {
			return "Completed in " + formatTurnStatusDuration(now.Sub(busySince))
		}
		return "Turn completed"
	default:
		return "Turn " + status
	}
}

func normalizeTurnStatus(status string) string {
	status = strings.TrimSpace(strings.ToLower(status))
	status = strings.ReplaceAll(status, "_", " ")
	status = strings.ReplaceAll(status, "-", " ")
	return strings.Join(strings.Fields(status), " ")
}

func formatTurnStatusDuration(d time.Duration) string {
	if d < 0 {
		d = 0
	}
	totalSeconds := int64(d / time.Second)
	days := totalSeconds / (24 * 60 * 60)
	hours := (totalSeconds % (24 * 60 * 60)) / (60 * 60)
	minutes := (totalSeconds % (60 * 60)) / 60
	seconds := totalSeconds % 60

	switch {
	case days > 0:
		if hours > 0 {
			return fmt.Sprintf("%dd %02dh", days, hours)
		}
		return fmt.Sprintf("%dd", days)
	case totalSeconds >= int64(time.Hour/time.Second):
		return fmt.Sprintf("%d:%02d:%02d", hours, minutes, seconds)
	default:
		return fmt.Sprintf("%02d:%02d", minutes, seconds)
	}
}

func (s *appServerSession) Submit(prompt string) error {
	return s.SubmitInput(Submission{Text: prompt})
}

func (s *appServerSession) SubmitInput(input Submission) error {
	input = normalizeSubmission(input)
	if input.Empty() {
		return nil
	}
	s.mu.Lock()
	if s.closed {
		s.mu.Unlock()
		return fmt.Errorf("codex session is closed")
	}
	threadID := s.threadID
	activeTurnID := s.activeTurnID
	busy := s.busy
	busyExternal := s.busyExternal
	pendingModel := strings.TrimSpace(s.pendingModel)
	pendingReasoning := strings.TrimSpace(s.pendingReasoning)
	currentModel := strings.TrimSpace(s.model)
	currentReasoning := strings.TrimSpace(s.reasoningEffort)
	if busyExternal {
		s.mu.Unlock()
		return fmt.Errorf("this Codex session is already busy in another process; Little Control Room is read-only until it finishes")
	}
	s.touchLocked()
	s.appendEntryLocked("", TranscriptUser, input.TranscriptText())
	if !busy {
		s.status = "Sending prompt to Codex..."
	}
	s.mu.Unlock()
	s.notify()

	ctx, cancel := context.WithTimeout(context.Background(), rpcTimeout)
	defer cancel()

	if busy && activeTurnID != "" {
		return s.submitBusyInput(ctx, threadID, activeTurnID, input, pendingModel, pendingReasoning, currentModel, currentReasoning)
	}

	return s.startTurnWithInput(ctx, threadID, input, pendingModel, pendingReasoning, currentModel, currentReasoning)
}

func (s *appServerSession) submitBusyInput(ctx context.Context, threadID, activeTurnID string, input Submission, pendingModel, pendingReasoning, currentModel, currentReasoning string) error {
	turnID, err := s.steerTurn(ctx, threadID, activeTurnID, input)
	if err == nil {
		s.recordSteerSubmission(firstNonEmpty(turnID, activeTurnID))
		return nil
	}
	if !isActiveTurnMismatchError(err) {
		s.appendSystemError(err)
		return err
	}

	recoveredTurnID, threadIdle, recoveryErr := s.recoverSteerTarget(ctx, threadID, activeTurnID, err)
	if recoveryErr != nil {
		combined := fmt.Errorf("%s (and failed to refresh thread state: %v)", err.Error(), recoveryErr)
		s.appendSystemError(combined)
		return combined
	}

	switch {
	case threadIdle:
		return s.startTurnWithInput(ctx, threadID, input, pendingModel, pendingReasoning, currentModel, currentReasoning)
	case recoveredTurnID != "" && recoveredTurnID != activeTurnID:
		turnID, err = s.steerTurn(ctx, threadID, recoveredTurnID, input)
		if err == nil {
			s.recordSteerSubmission(firstNonEmpty(turnID, recoveredTurnID))
			return nil
		}
	}

	s.appendSystemError(err)
	return err
}

func (s *appServerSession) startTurnWithInput(ctx context.Context, threadID string, input Submission, pendingModel, pendingReasoning, currentModel, currentReasoning string) error {
	params := turnStartParams{
		ThreadID: threadID,
		Input:    encodeSubmissionInput(input),
	}
	if pendingModel != "" {
		params.Model = pendingModel
	}
	if pendingReasoning != "" {
		params.Effort = pendingReasoning
	}
	result, err := s.call(ctx, "turn/start", params)
	if err != nil {
		s.appendSystemError(err)
		return err
	}
	var response turnStartResponse
	_ = json.Unmarshal(result, &response)
	s.mu.Lock()
	if pendingModel != "" {
		s.model = pendingModel
		s.pendingModel = ""
	} else {
		s.model = currentModel
	}
	if pendingReasoning != "" {
		s.reasoningEffort = pendingReasoning
		s.pendingReasoning = ""
	} else {
		s.reasoningEffort = currentReasoning
	}
	s.setBusyLocked(response.Turn.ID, false)
	s.status = "Codex is working..."
	s.mu.Unlock()
	s.notify()
	return nil
}

func (s *appServerSession) steerTurn(ctx context.Context, threadID, expectedTurnID string, input Submission) (string, error) {
	result, err := s.call(ctx, "turn/steer", turnSteerParams{
		ThreadID:       threadID,
		ExpectedTurnID: expectedTurnID,
		Input:          encodeSubmissionInput(input),
	})
	if err != nil {
		return "", err
	}
	var response turnSteerResponse
	_ = json.Unmarshal(result, &response)
	return strings.TrimSpace(response.TurnID), nil
}

func (s *appServerSession) recordSteerSubmission(turnID string) {
	turnID = strings.TrimSpace(turnID)
	s.mu.Lock()
	if turnID != "" {
		s.setBusyLocked(turnID, false)
	} else {
		s.busy = true
		s.busyExternal = false
		s.reconciling = false
	}
	s.status = "Sent follow-up to Codex"
	s.mu.Unlock()
	s.notify()
}

func (s *appServerSession) recoverSteerTarget(ctx context.Context, threadID, expectedTurnID string, mismatchErr error) (string, bool, error) {
	thread, err := s.readThreadState(ctx, threadID)
	if err != nil {
		return "", false, err
	}

	recoveredTurnID := activeTurnIDFromThread(thread)
	if recoveredTurnID == "" {
		if mismatch := parseActiveTurnMismatchError(mismatchErr); mismatch != nil && mismatch.ExpectedTurnID == expectedTurnID {
			recoveredTurnID = mismatch.FoundTurnID
		}
	}
	threadIdle := strings.EqualFold(strings.TrimSpace(thread.Status.Type), "idle")

	s.mu.Lock()
	s.touchLocked()
	switch {
	case threadIdle:
		s.syncThreadStatusLocked(thread.ID, thread.Status, true)
	case recoveredTurnID != "":
		s.setBusyLocked(recoveredTurnID, false)
		s.status = "Codex is working..."
	default:
		s.syncThreadStatusLocked(thread.ID, thread.Status, true)
	}
	s.mu.Unlock()
	s.notify()

	return recoveredTurnID, threadIdle, nil
}

func (s *appServerSession) ShowStatus() error {
	s.mu.Lock()
	if s.closed {
		s.mu.Unlock()
		return fmt.Errorf("codex session is closed")
	}
	threadID := strings.TrimSpace(s.threadID)
	projectPath := s.projectPath
	currentCWD := firstNonEmpty(strings.TrimSpace(s.currentCWD), projectPath)
	model := strings.TrimSpace(s.model)
	modelProvider := strings.TrimSpace(s.modelProvider)
	reasoningEffort := strings.TrimSpace(s.reasoningEffort)
	serviceTier := strings.TrimSpace(s.serviceTier)
	approvalPolicy := append(json.RawMessage(nil), s.approvalPolicy...)
	sandboxPolicy := append(json.RawMessage(nil), s.sandboxPolicy...)
	tokenUsage := cloneThreadTokenUsage(s.tokenUsage)
	rateLimits := cloneRateLimitSnapshot(s.rateLimits)
	rateLimitsByID := cloneRateLimitSnapshotMap(s.rateLimitsByID)
	s.touchLocked()
	s.status = "Reading embedded Codex status..."
	s.mu.Unlock()
	s.notify()

	if refreshed, byID, err := s.readRateLimits(); err == nil {
		rateLimits = cloneRateLimitSnapshot(refreshed)
		rateLimitsByID = cloneRateLimitSnapshotMap(byID)
		s.mu.Lock()
		s.rateLimits = cloneRateLimitSnapshot(refreshed)
		s.rateLimitsByID = cloneRateLimitSnapshotMap(byID)
		s.mu.Unlock()
	} else if rateLimits == nil && len(rateLimitsByID) == 0 {
		s.mu.Lock()
		s.lastSystemNotice = "Embedded Codex status could not refresh rate limits: " + err.Error()
		s.mu.Unlock()
	}

	statusText := buildEmbeddedStatusText(
		threadID,
		projectPath,
		currentCWD,
		model,
		modelProvider,
		reasoningEffort,
		serviceTier,
		approvalPolicy,
		sandboxPolicy,
		tokenUsage,
		rateLimits,
		rateLimitsByID,
	)

	s.mu.Lock()
	s.touchLocked()
	s.appendEntryLocked("", TranscriptStatus, statusText)
	s.lastSystemNotice = "Displayed embedded Codex status"
	s.status = "Displayed embedded Codex status"
	s.mu.Unlock()
	s.notify()
	return nil
}

func (s *appServerSession) ListModels() ([]ModelOption, error) {
	s.mu.Lock()
	if s.closed {
		s.mu.Unlock()
		return nil, fmt.Errorf("codex session is closed")
	}
	s.touchLocked()
	s.mu.Unlock()

	const pageSize = 100
	includeHidden := false
	limit := pageSize
	var cursor *string
	models := []ModelOption{}

	for {
		ctx, cancel := context.WithTimeout(context.Background(), rpcTimeout)
		result, err := s.call(ctx, "model/list", modelListParams{
			Cursor:        cursor,
			Limit:         &limit,
			IncludeHidden: &includeHidden,
		})
		cancel()
		if err != nil {
			return nil, err
		}
		var response modelListResponse
		if err := json.Unmarshal(result, &response); err != nil {
			return nil, err
		}
		for _, entry := range response.Data {
			models = append(models, ModelOption{
				ID:                        strings.TrimSpace(entry.ID),
				Model:                     strings.TrimSpace(entry.Model),
				DisplayName:               strings.TrimSpace(entry.DisplayName),
				Description:               strings.TrimSpace(entry.Description),
				Hidden:                    entry.Hidden,
				SupportedReasoningEfforts: exportedReasoningEffortOptions(entry.SupportedReasoningEfforts),
				DefaultReasoningEffort:    strings.TrimSpace(entry.DefaultReasoningEffort),
				SupportsPersonality:       entry.SupportsPersonality,
				IsDefault:                 entry.IsDefault,
			})
		}
		if response.NextCursor == nil || strings.TrimSpace(*response.NextCursor) == "" {
			return models, nil
		}
		next := strings.TrimSpace(*response.NextCursor)
		cursor = &next
	}
}

func exportedReasoningEffortOptions(in []reasoningEffortOptionEntry) []ReasoningEffortOption {
	if len(in) == 0 {
		return nil
	}
	out := make([]ReasoningEffortOption, 0, len(in))
	for _, option := range in {
		effort := strings.TrimSpace(option.ReasoningEffort)
		if effort == "" {
			continue
		}
		out = append(out, ReasoningEffortOption{
			ReasoningEffort: effort,
			Description:     strings.TrimSpace(option.Description),
		})
	}
	return out
}

func (s *appServerSession) StageModelOverride(model, reasoningEffort string) error {
	model = strings.TrimSpace(model)
	reasoningEffort = strings.TrimSpace(reasoningEffort)

	s.mu.Lock()
	if s.closed {
		s.mu.Unlock()
		return fmt.Errorf("codex session is closed")
	}

	currentModel := strings.TrimSpace(s.model)
	currentReasoning := strings.TrimSpace(s.reasoningEffort)
	if model == "" {
		model = currentModel
	}
	if reasoningEffort == "" {
		reasoningEffort = currentReasoning
	}

	if model == currentModel && reasoningEffort == currentReasoning {
		s.pendingModel = ""
		s.pendingReasoning = ""
	} else {
		s.pendingModel = model
		s.pendingReasoning = reasoningEffort
	}
	s.touchLocked()
	s.mu.Unlock()
	s.notify()
	return nil
}

func (s *appServerSession) Interrupt() error {
	s.mu.Lock()
	if s.closed {
		s.mu.Unlock()
		return fmt.Errorf("codex session is closed")
	}
	threadID := s.threadID
	turnID := s.activeTurnID
	busyExternal := s.busyExternal
	if busyExternal {
		s.mu.Unlock()
		return fmt.Errorf("this Codex session is already busy in another process; interrupt it there or hide it here")
	}
	if threadID == "" || turnID == "" {
		s.mu.Unlock()
		return fmt.Errorf("no active Codex turn to interrupt")
	}
	s.touchLocked()
	s.status = "Interrupting Codex turn..."
	s.mu.Unlock()
	s.notify()

	ctx, cancel := context.WithTimeout(context.Background(), rpcTimeout)
	defer cancel()
	if _, err := s.call(ctx, "turn/interrupt", turnInterruptParams{
		ThreadID: threadID,
		TurnID:   turnID,
	}); err != nil {
		s.appendSystemError(err)
		return err
	}
	return nil
}

func (s *appServerSession) RespondApproval(decision ApprovalDecision) error {
	s.mu.Lock()
	if s.closed {
		s.mu.Unlock()
		return fmt.Errorf("codex session is closed")
	}
	approval := cloneApprovalRequest(s.pendingApproval)
	if approval == nil {
		s.mu.Unlock()
		return fmt.Errorf("no pending approval")
	}
	if !approval.AllowsDecision(decision) {
		s.mu.Unlock()
		return fmt.Errorf("approval decision %q is not supported for %s approvals", decision, approval.Kind)
	}
	s.touchLocked()
	s.status = "Sending approval decision..."
	s.mu.Unlock()
	s.notify()

	response := map[string]any{
		"decision": string(decision),
	}
	if err := s.send(rpcResponse{
		ID:     decodeRequestID(approval.ID),
		Result: response,
	}); err != nil {
		s.appendSystemError(err)
		return err
	}

	s.mu.Lock()
	s.lastSystemNotice = "Approval decision sent"
	s.status = "Approval decision sent"
	s.mu.Unlock()
	s.notify()
	return nil
}

func (s *appServerSession) RespondToolInput(answers map[string][]string) error {
	s.mu.Lock()
	if s.closed {
		s.mu.Unlock()
		return fmt.Errorf("codex session is closed")
	}
	request := cloneToolInputRequest(s.pendingToolInput)
	if request == nil {
		s.mu.Unlock()
		return fmt.Errorf("no pending tool input request")
	}
	s.touchLocked()
	s.status = "Sending tool input..."
	s.mu.Unlock()
	s.notify()

	payload := map[string]any{
		"answers": map[string]any{},
	}
	answerMap := payload["answers"].(map[string]any)
	for questionID, values := range answers {
		trimmed := make([]string, 0, len(values))
		for _, value := range values {
			if text := strings.TrimSpace(value); text != "" {
				trimmed = append(trimmed, text)
			}
		}
		answerMap[questionID] = map[string]any{
			"answers": trimmed,
		}
	}

	if err := s.send(rpcResponse{
		ID:     decodeRequestID(request.ID),
		Result: payload,
	}); err != nil {
		s.appendSystemError(err)
		return err
	}

	s.mu.Lock()
	s.lastSystemNotice = "Tool input sent"
	s.status = "Tool input sent"
	s.mu.Unlock()
	s.notify()
	return nil
}

func (s *appServerSession) RespondElicitation(decision ElicitationDecision, content json.RawMessage) error {
	s.mu.Lock()
	if s.closed {
		s.mu.Unlock()
		return fmt.Errorf("codex session is closed")
	}
	request := cloneElicitationRequest(s.pendingElicitation)
	if request == nil {
		s.mu.Unlock()
		return fmt.Errorf("no pending MCP elicitation")
	}
	s.touchLocked()
	s.status = "Sending MCP response..."
	s.mu.Unlock()
	s.notify()

	payload := map[string]any{
		"action": string(decision),
	}
	if len(content) > 0 && string(content) != "null" {
		var decoded any
		if err := json.Unmarshal(content, &decoded); err != nil {
			return fmt.Errorf("decode elicitation content: %w", err)
		}
		payload["content"] = decoded
	}

	if err := s.send(rpcResponse{
		ID:     decodeRequestID(request.ID),
		Result: payload,
	}); err != nil {
		s.appendSystemError(err)
		return err
	}

	s.mu.Lock()
	s.lastSystemNotice = "MCP input sent"
	s.status = "MCP input sent"
	s.mu.Unlock()
	s.notify()
	return nil
}

func (s *appServerSession) Close() error {
	s.mu.Lock()
	if s.closed {
		s.mu.Unlock()
		return nil
	}
	s.closed = true
	s.clearActiveStateLocked()
	s.status = "Codex session closed"
	cmd := s.cmd
	stdin := s.stdin
	s.mu.Unlock()

	if stdin != nil {
		_ = stdin.Close()
	}
	if cmd != nil && cmd.Process != nil {
		_ = terminateAppServerCommand(cmd)
	} else {
		s.closeExitCh()
	}
	s.failPending("session closed")
	s.notify()
	return nil
}

func (s *appServerSession) CloseDueToInactivity() error {
	s.mu.Lock()
	if s.closed {
		s.mu.Unlock()
		return nil
	}
	if s.busy || s.pendingApproval != nil {
		s.touchLocked()
		s.mu.Unlock()
		return nil
	}
	s.closed = true
	s.clearActiveStateLocked()
	s.lastSystemNotice = idleShutdownNotice
	s.status = idleShutdownNotice
	s.appendEntryLocked("", TranscriptSystem, idleShutdownNotice)
	cmd := s.cmd
	stdin := s.stdin
	s.mu.Unlock()

	if stdin != nil {
		_ = stdin.Close()
	}
	if cmd != nil && cmd.Process != nil {
		_ = terminateAppServerCommand(cmd)
	} else {
		s.closeExitCh()
	}
	s.failPending("session closed")
	s.notify()
	return nil
}

func (s *appServerSession) WaitClosed(timeout time.Duration) bool {
	if s == nil {
		return true
	}
	s.mu.Lock()
	closed := s.closed
	cmd := s.cmd
	exitCh := s.exitCh
	s.mu.Unlock()
	if cmd == nil || exitCh == nil {
		return true
	}
	if !closed {
		return false
	}
	if timeout <= 0 {
		<-exitCh
		return true
	}
	select {
	case <-exitCh:
		return true
	case <-time.After(timeout):
		return false
	}
}

func (s *appServerSession) start(req LaunchRequest) error {
	cmd := exec.Command("codex", "app-server")
	cmd.Dir = req.ProjectPath
	configureAppServerCommand(cmd)

	stdin, err := cmd.StdinPipe()
	if err != nil {
		return err
	}
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return err
	}
	stderr, err := cmd.StderrPipe()
	if err != nil {
		return err
	}

	s.cmd = cmd
	s.stdin = stdin

	if err := cmd.Start(); err != nil {
		return err
	}

	go s.readStdout(stdout)
	go s.readStderr(stderr)
	go s.waitForExit()

	ctx, cancel := context.WithTimeout(context.Background(), rpcTimeout)
	defer cancel()

	if _, err := s.call(ctx, "initialize", map[string]any{
		"clientInfo": map[string]any{
			"name":    "little_control_room",
			"title":   "Little Control Room",
			"version": "0.1.0",
		},
	}); err != nil {
		return err
	}
	if err := s.send(rpcNotification{Method: "initialized", Params: map[string]any{}}); err != nil {
		return err
	}

	var threadID string
	if !req.ForceNew && strings.TrimSpace(req.ResumeID) != "" {
		threadID, err = s.resumeThread(ctx, req.ResumeID)
		if err != nil {
			s.appendSystemNotice("Resume failed, starting a new Codex thread.")
		}
	}
	if threadID == "" {
		threadID, err = s.startThread(ctx)
		if err != nil {
			return err
		}
	}

	s.mu.Lock()
	s.threadID = threadID
	s.started = true
	s.status = ""
	s.mu.Unlock()
	s.notify()

	if strings.TrimSpace(req.Prompt) != "" {
		if snapshot := s.Snapshot(); snapshot.BusyExternal {
			s.appendSystemNotice("This Codex session is already active in another process. The embedded prompt was not sent; use /codex-new for a separate session.")
			return nil
		}
		return s.Submit(req.Prompt)
	}
	return nil
}

func (s *appServerSession) startThread(ctx context.Context) (string, error) {
	result, err := s.call(ctx, "thread/start", threadStartParams{
		CWD:            s.projectPath,
		ApprovalPolicy: approvalPolicyForPreset(s.preset),
		Sandbox:        sandboxModeForPreset(s.preset),
		ServiceName:    "little-control-room",
	})
	if err != nil {
		return "", err
	}
	var response threadResponse
	if err := json.Unmarshal(result, &response); err != nil {
		return "", err
	}
	if strings.TrimSpace(response.Thread.ID) == "" {
		return "", fmt.Errorf("thread/start returned no thread id")
	}
	s.mu.Lock()
	s.applyThreadConfigLocked(response.ApprovalPolicy, response.CWD, response.Model, response.ModelProvider, stringValue(response.ReasoningEffort), stringValue(response.ServiceTier), response.Sandbox)
	s.mu.Unlock()
	s.appendSystemNotice("Started a new embedded Codex session.")
	return response.Thread.ID, nil
}

func (s *appServerSession) resumeThread(ctx context.Context, threadID string) (string, error) {
	result, err := s.call(ctx, "thread/resume", threadResumeParams{
		ThreadID:       threadID,
		ApprovalPolicy: approvalPolicyForPreset(s.preset),
		Sandbox:        sandboxModeForPreset(s.preset),
	})
	if err != nil {
		return "", err
	}
	var response threadResumeResponse
	if err := json.Unmarshal(result, &response); err != nil {
		return "", err
	}
	if strings.TrimSpace(response.Thread.ID) == "" {
		return "", fmt.Errorf("thread/resume returned no thread id")
	}
	s.mu.Lock()
	s.applyThreadConfigLocked(response.ApprovalPolicy, response.CWD, response.Model, response.ModelProvider, stringValue(response.ReasoningEffort), stringValue(response.ServiceTier), response.Sandbox)
	s.mu.Unlock()
	s.hydrateResumedThread(response.Thread)
	if snapshot := s.Snapshot(); snapshot.BusyExternal {
		s.appendSystemNotice("Resumed embedded Codex session " + shortID(response.Thread.ID) + ". It is already active in another Codex process, so embedded controls are read-only until it finishes.")
	} else {
		s.appendSystemNotice("Resumed embedded Codex session " + shortID(response.Thread.ID) + ".")
	}
	return response.Thread.ID, nil
}

func (s *appServerSession) RefreshBusyElsewhere() error {
	s.mu.Lock()
	if s.closed {
		s.mu.Unlock()
		return fmt.Errorf("codex session is closed")
	}
	if !s.busyExternal {
		s.mu.Unlock()
		return nil
	}
	threadID := strings.TrimSpace(s.threadID)
	s.touchLocked()
	s.mu.Unlock()

	if threadID == "" {
		return nil
	}

	ctx, cancel := context.WithTimeout(context.Background(), rpcTimeout)
	defer cancel()

	result, err := s.call(ctx, "thread/resume", threadResumeParams{
		ThreadID:       threadID,
		ApprovalPolicy: approvalPolicyForPreset(s.preset),
		Sandbox:        sandboxModeForPreset(s.preset),
	})
	if err != nil {
		s.appendSystemError(err)
		return err
	}

	var response threadResumeResponse
	if err := json.Unmarshal(result, &response); err != nil {
		s.appendSystemError(err)
		return err
	}

	s.mu.Lock()
	wasBusyExternal := s.busyExternal
	s.applyThreadConfigLocked(response.ApprovalPolicy, response.CWD, response.Model, response.ModelProvider, stringValue(response.ReasoningEffort), stringValue(response.ServiceTier), response.Sandbox)
	s.hydrateResumedThreadLocked(response.Thread)
	noticeID := shortID(firstNonEmpty(response.Thread.ID, threadID))
	switch {
	case wasBusyExternal && !s.busyExternal:
		message := "Embedded Codex session " + noticeID + " is no longer active in another Codex process. Embedded controls are live again."
		s.appendEntryLocked("", TranscriptSystem, message)
		s.lastSystemNotice = message
		s.status = message
	case s.busyExternal:
		message := "Embedded Codex session " + noticeID + " is already active in another Codex process, so embedded controls are read-only until it finishes."
		s.lastSystemNotice = message
		s.status = message
	default:
		s.status = "Codex session ready"
	}
	s.mu.Unlock()
	return nil
}

func (s *appServerSession) readThreadState(ctx context.Context, threadID string) (resumedThread, error) {
	result, err := s.call(ctx, "thread/read", threadReadParams{
		ThreadID:     strings.TrimSpace(threadID),
		IncludeTurns: true,
	})
	if err != nil {
		return resumedThread{}, err
	}
	var response threadReadResponse
	if err := json.Unmarshal(result, &response); err != nil {
		return resumedThread{}, err
	}
	if strings.TrimSpace(response.Thread.ID) == "" {
		return resumedThread{}, fmt.Errorf("thread/read returned no thread id")
	}
	return response.Thread, nil
}

func (s *appServerSession) ReconcileBusyState() error {
	s.mu.Lock()
	if s.closed {
		s.mu.Unlock()
		return fmt.Errorf("codex session is closed")
	}
	if s.busyExternal || !s.busy || s.pendingApproval != nil || s.pendingToolInput != nil || s.pendingElicitation != nil || s.reconciling {
		s.mu.Unlock()
		return nil
	}
	threadID := strings.TrimSpace(s.threadID)
	if threadID == "" {
		s.mu.Unlock()
		return nil
	}
	s.reconciling = true
	s.mu.Unlock()
	s.notify()

	ctx, cancel := context.WithTimeout(context.Background(), rpcTimeout)
	defer cancel()

	thread, err := s.readThreadState(ctx, threadID)

	s.mu.Lock()
	if s.closed {
		s.mu.Unlock()
		return nil
	}
	s.reconciling = false
	if err == nil {
		s.syncThreadStatusLocked(threadID, thread.Status, true)
	}
	s.mu.Unlock()
	s.notify()
	return err
}

func (s *appServerSession) readStdout(r io.Reader) {
	err := readLines(r, func(line []byte) {
		var env rpcEnvelope
		if err := json.Unmarshal(line, &env); err != nil {
			s.appendSystemError(fmt.Errorf("invalid app-server message: %w", err))
			return
		}
		s.routeEnvelope(env)
	})
	if err != nil {
		s.handleTransportFailure(fmt.Errorf("app-server stream error: %w", err))
	}
}

func (s *appServerSession) readStderr(r io.Reader) {
	err := readLines(r, func(raw []byte) {
		line := strings.TrimSpace(string(raw))
		if line == "" {
			return
		}
		s.appendSystemNotice("codex stderr: " + line)
		s.maybeAppendAuth403Diagnosis(line)
	})
	if err != nil {
		s.appendSystemNotice("codex stderr stream error: " + err.Error())
	}
}

func (s *appServerSession) waitForExit() {
	if s.cmd == nil {
		s.closeExitCh()
		return
	}
	err := s.cmd.Wait()
	s.closeExitCh()
	s.mu.Lock()
	if !s.closed {
		s.closed = true
		s.clearActiveStateLocked()
		if err != nil {
			s.lastError = err.Error()
			s.status = "Codex app-server exited with error"
			s.lastSystemNotice = "Codex app-server exited with error"
		} else {
			s.status = "Codex app-server exited"
			s.lastSystemNotice = "Codex app-server exited"
		}
	}
	s.mu.Unlock()
	s.failPending("session exited")
	s.notify()
}

func (s *appServerSession) closeExitCh() {
	if s == nil {
		return
	}
	s.exitOnce.Do(func() {
		if s.exitCh != nil {
			close(s.exitCh)
		}
	})
}

func (s *appServerSession) routeEnvelope(env rpcEnvelope) {
	if env.Method != "" && len(env.ID) > 0 {
		s.handleServerRequest(env)
		return
	}
	if env.Method != "" {
		s.handleNotification(env.Method, env.Params)
		return
	}
	if len(env.ID) == 0 {
		return
	}
	s.pendingMu.Lock()
	ch, ok := s.pending[idKey(env.ID)]
	if ok {
		delete(s.pending, idKey(env.ID))
	}
	s.pendingMu.Unlock()
	if ok {
		ch <- env
	}
}

func (s *appServerSession) handleServerRequest(env rpcEnvelope) {
	switch env.Method {
	case "item/commandExecution/requestApproval":
		var params commandApprovalParams
		if err := json.Unmarshal(env.Params, &params); err != nil {
			s.appendSystemError(err)
			return
		}
		params.RequestID = idKey(env.ID)
		s.mu.Lock()
		s.touchLocked()
		s.pendingApproval = &ApprovalRequest{
			ID:       params.RequestID,
			Kind:     ApprovalCommandExecution,
			ThreadID: params.ThreadID,
			TurnID:   params.TurnID,
			ItemID:   params.ItemID,
			Command:  params.Command,
			CWD:      params.CWD,
			Reason:   params.Reason,
		}
		s.status = "Waiting for command approval"
		s.lastSystemNotice = "Codex requested command approval"
		s.mu.Unlock()
		s.notify()
	case "item/fileChange/requestApproval":
		var params fileChangeApprovalParams
		if err := json.Unmarshal(env.Params, &params); err != nil {
			s.appendSystemError(err)
			return
		}
		params.RequestID = idKey(env.ID)
		s.mu.Lock()
		s.touchLocked()
		s.pendingApproval = &ApprovalRequest{
			ID:        params.RequestID,
			Kind:      ApprovalFileChange,
			ThreadID:  params.ThreadID,
			TurnID:    params.TurnID,
			ItemID:    params.ItemID,
			Reason:    params.Reason,
			GrantRoot: params.GrantRoot,
		}
		s.status = "Waiting for file change approval"
		s.lastSystemNotice = "Codex requested file change approval"
		s.mu.Unlock()
		s.notify()
	case "item/tool/requestUserInput":
		var params toolRequestUserInputParams
		if err := json.Unmarshal(env.Params, &params); err != nil {
			s.appendSystemError(err)
			_ = s.respondRequestError(env.ID, -32602, "invalid tool input request")
			return
		}
		params.RequestID = idKey(env.ID)
		request := &ToolInputRequest{
			ID:       params.RequestID,
			ThreadID: params.ThreadID,
			TurnID:   params.TurnID,
			ItemID:   params.ItemID,
		}
		for _, question := range params.Questions {
			options := make([]ToolInputOption, 0, len(question.Options))
			for _, option := range question.Options {
				options = append(options, ToolInputOption{
					Label:       option.Label,
					Description: option.Description,
				})
			}
			request.Questions = append(request.Questions, ToolInputQuestion{
				Header:   question.Header,
				ID:       question.ID,
				Question: question.Question,
				IsOther:  question.IsOther,
				IsSecret: question.IsSecret,
				Options:  options,
			})
		}
		s.mu.Lock()
		s.touchLocked()
		s.pendingToolInput = request
		s.status = "Waiting for structured user input"
		s.lastSystemNotice = "Codex requested structured user input"
		s.mu.Unlock()
		s.notify()
	case "mcpServer/elicitation/request":
		var params mcpServerElicitationRequestParams
		if err := json.Unmarshal(env.Params, &params); err != nil {
			s.appendSystemError(err)
			_ = s.respondRequestError(env.ID, -32602, "invalid MCP elicitation request")
			return
		}
		params.RequestID = idKey(env.ID)
		request := &ElicitationRequest{
			ID:              params.RequestID,
			ServerName:      params.ServerName,
			ThreadID:        params.ThreadID,
			TurnID:          params.TurnID,
			Mode:            ElicitationMode(params.Mode),
			Message:         params.Message,
			URL:             params.URL,
			ElicitationID:   params.ElicitationID,
			RequestedSchema: params.RequestedSchema,
		}
		s.mu.Lock()
		s.touchLocked()
		s.pendingElicitation = request
		s.status = "Waiting for MCP input"
		s.lastSystemNotice = "MCP server requested input"
		s.mu.Unlock()
		s.notify()
	default:
		s.appendSystemNotice("Unsupported app-server request: " + env.Method)
		_ = s.respondRequestError(env.ID, -32601, "unsupported request: "+env.Method)
	}
}

func (s *appServerSession) handleNotification(method string, params json.RawMessage) {
	switch method {
	case "thread/status/changed":
		var msg threadStatusChangedNotification
		if err := json.Unmarshal(params, &msg); err != nil {
			return
		}
		s.mu.Lock()
		s.touchLocked()
		s.syncThreadStatusLocked(msg.ThreadID, msg.Status, false)
		s.mu.Unlock()
		s.notify()
	case "thread/started":
		var msg threadStartedNotification
		if err := json.Unmarshal(params, &msg); err == nil && msg.Thread.ID != "" {
			s.mu.Lock()
			s.touchLocked()
			s.threadID = msg.Thread.ID
			s.started = true
			s.mu.Unlock()
			s.notify()
		}
	case "turn/started":
		var msg turnNotification
		if err := json.Unmarshal(params, &msg); err != nil {
			return
		}
		s.mu.Lock()
		s.touchLocked()
		s.setBusyLocked(msg.Turn.ID, false)
		s.status = "Codex is working..."
		s.mu.Unlock()
		s.notify()
	case "turn/completed":
		var msg turnNotification
		if err := json.Unmarshal(params, &msg); err != nil {
			return
		}
		s.mu.Lock()
		s.touchBusyLocked()
		status := formatTurnCompletionStatus(msg.Turn.Status, s.busySince, time.Now())
		s.queueTurnCompletionLocked(msg.Turn.ID, status)
		s.mu.Unlock()
		s.notify()
	case "item/started":
		s.handleItemStarted(params)
	case "item/completed":
		s.handleItemCompleted(params)
	case "item/agentMessage/delta":
		s.handleItemDelta(params, TranscriptAgent)
	case "item/plan/delta":
		s.handleItemDelta(params, TranscriptPlan)
	case "item/commandExecution/outputDelta":
		s.handleItemDelta(params, TranscriptCommand)
	case "item/fileChange/outputDelta":
		s.handleItemDelta(params, TranscriptFileChange)
	case "item/reasoning/summaryTextDelta":
		s.handleItemDelta(params, TranscriptReasoning)
	case "item/reasoning/textDelta":
		s.handleItemDelta(params, TranscriptReasoning)
	case "item/reasoning/summaryPartAdded":
		var msg reasoningSummaryPartAddedNotification
		if err := json.Unmarshal(params, &msg); err != nil {
			return
		}
		s.mu.Lock()
		s.touchBusyLocked()
		s.markItemActiveLocked(msg.TurnID, msg.ItemID)
		if msg.SummaryIndex > 0 {
			s.appendDeltaToItemLocked(msg.ItemID, TranscriptReasoning, "\n\n")
		} else {
			s.ensureItemEntryLocked(msg.ItemID, TranscriptReasoning, "")
		}
		s.mu.Unlock()
		s.notify()
	case "item/mcpToolCall/progress":
		var msg mcpToolCallProgressNotification
		if err := json.Unmarshal(params, &msg); err != nil {
			return
		}
		s.mu.Lock()
		s.touchBusyLocked()
		s.markItemActiveLocked(msg.TurnID, msg.ItemID)
		progress := strings.TrimSpace(msg.Message)
		if progress == "" {
			s.ensureItemEntryLocked(msg.ItemID, TranscriptTool, "")
		} else {
			if index, ok := s.entryIndex[msg.ItemID]; ok && strings.TrimSpace(s.entries[index].Text) != "" {
				progress = "\n" + progress
			}
			s.appendDeltaToItemLocked(msg.ItemID, TranscriptTool, progress)
		}
		s.mu.Unlock()
		s.notify()
	case "serverRequest/resolved":
		var msg serverRequestResolvedNotification
		if err := json.Unmarshal(params, &msg); err != nil {
			return
		}
		requestID := normalizeRequestID(msg.RequestID)
		s.mu.Lock()
		s.touchLocked()
		if s.pendingApproval != nil && s.pendingApproval.ID == requestID {
			s.pendingApproval = nil
		}
		if s.pendingToolInput != nil && s.pendingToolInput.ID == requestID {
			s.pendingToolInput = nil
		}
		if s.pendingElicitation != nil && s.pendingElicitation.ID == requestID {
			s.pendingElicitation = nil
		}
		if s.busy {
			s.status = "Codex is working..."
		} else {
			s.status = ""
		}
		s.mu.Unlock()
		s.notify()
	case "thread/tokenUsage/updated":
		var msg threadTokenUsageUpdatedNotification
		if err := json.Unmarshal(params, &msg); err != nil {
			return
		}
		s.mu.Lock()
		s.touchLocked()
		s.tokenUsage = cloneThreadTokenUsage(&msg.TokenUsage)
		s.mu.Unlock()
		s.notify()
	case "account/rateLimits/updated":
		var msg accountRateLimitsUpdatedNotification
		if err := json.Unmarshal(params, &msg); err != nil {
			return
		}
		s.mu.Lock()
		s.touchLocked()
		s.rateLimits = cloneRateLimitSnapshot(&msg.RateLimits)
		s.mu.Unlock()
		s.notify()
	case "model/rerouted":
		var msg modelReroutedNotification
		if err := json.Unmarshal(params, &msg); err != nil {
			return
		}
		s.mu.Lock()
		s.touchLocked()
		if toModel := strings.TrimSpace(msg.ToModel); toModel != "" {
			s.model = toModel
			if strings.EqualFold(strings.TrimSpace(s.pendingModel), toModel) {
				s.pendingModel = ""
			}
		}
		s.mu.Unlock()
		s.notify()
	case "error":
		var msg struct {
			Error struct {
				Message string `json:"message"`
			} `json:"error"`
		}
		if err := json.Unmarshal(params, &msg); err == nil && strings.TrimSpace(msg.Error.Message) != "" {
			s.appendSystemError(errors.New(msg.Error.Message))
		}
	}
}

func (s *appServerSession) handleItemStarted(params json.RawMessage) {
	var msg struct {
		TurnID string                     `json:"turnId"`
		Item   map[string]json.RawMessage `json:"item"`
	}
	if err := json.Unmarshal(params, &msg); err != nil {
		return
	}
	itemType := decodeRawString(msg.Item["type"])
	itemID := decodeRawString(msg.Item["id"])

	s.mu.Lock()
	s.touchBusyLocked()
	if tracksBusyItemLifecycle(itemType) {
		s.markItemActiveLocked(msg.TurnID, itemID)
	}
	switch itemType {
	case "agentMessage":
		s.ensureItemEntryLocked(itemID, TranscriptAgent, "")
	default:
		itemID, kind, text := renderResumedThreadItem(msg.Item)
		if strings.TrimSpace(text) != "" && itemType == "commandExecution" && !strings.HasSuffix(text, "\n") {
			text += "\n"
		}
		if strings.TrimSpace(text) != "" {
			s.upsertItemEntryLocked(itemID, kind, text)
		}
	}
	s.mu.Unlock()
	s.notify()
}

func (s *appServerSession) handleItemDelta(params json.RawMessage, kind TranscriptKind) {
	var msg deltaNotification
	if err := json.Unmarshal(params, &msg); err != nil {
		return
	}
	s.mu.Lock()
	s.touchBusyLocked()
	s.markItemActiveLocked(msg.TurnID, msg.ItemID)
	s.appendDeltaToItemLocked(msg.ItemID, kind, msg.Delta)
	s.mu.Unlock()
	s.notify()
}

func (s *appServerSession) handleItemCompleted(params json.RawMessage) {
	var msg struct {
		TurnID string                     `json:"turnId"`
		Item   map[string]json.RawMessage `json:"item"`
	}
	if err := json.Unmarshal(params, &msg); err != nil {
		return
	}
	itemType := decodeRawString(msg.Item["type"])
	itemID := decodeRawString(msg.Item["id"])

	s.mu.Lock()
	s.touchBusyLocked()
	switch itemType {
	case "commandExecution":
		s.finalizeCommandItemLocked(itemID, msg.Item)
	case "fileChange":
		s.finalizeFileChangeItemLocked(itemID, msg.Item)
	default:
		itemID, kind, text := renderResumedThreadItem(msg.Item)
		if strings.TrimSpace(text) != "" {
			s.upsertItemEntryLocked(itemID, kind, text)
		}
	}
	s.markItemCompletedLocked(itemID)
	s.mu.Unlock()
	s.notify()
}

func (s *appServerSession) call(ctx context.Context, method string, params any) (json.RawMessage, error) {
	if s.rpcCallHook != nil {
		return s.rpcCallHook(ctx, method, params)
	}
	id := s.nextRequestID()
	key := idKey(id)
	ch := make(chan rpcEnvelope, 1)

	s.pendingMu.Lock()
	s.pending[key] = ch
	s.pendingMu.Unlock()

	if err := s.send(rpcRequest{Method: method, ID: decodeRequestID(key), Params: params}); err != nil {
		s.pendingMu.Lock()
		delete(s.pending, key)
		s.pendingMu.Unlock()
		return nil, err
	}

	select {
	case <-ctx.Done():
		s.pendingMu.Lock()
		delete(s.pending, key)
		s.pendingMu.Unlock()
		return nil, ctx.Err()
	case env := <-ch:
		if env.Error != nil {
			return nil, errors.New(env.Error.Message)
		}
		return env.Result, nil
	}
}

func (s *appServerSession) send(v any) error {
	s.writeMu.Lock()
	defer s.writeMu.Unlock()
	if s.stdin == nil {
		return fmt.Errorf("codex app-server stdin unavailable")
	}
	payload, err := json.Marshal(v)
	if err != nil {
		return err
	}
	if _, err := s.stdin.Write(append(payload, '\n')); err != nil {
		return err
	}
	return nil
}

func (s *appServerSession) respondRequestError(id json.RawMessage, code int, message string) error {
	return s.send(rpcErrorResponse{
		ID: decodeRequestID(idKey(id)),
		Error: rpcError{
			Code:    code,
			Message: message,
		},
	})
}

func (s *appServerSession) nextRequestID() json.RawMessage {
	s.pendingMu.Lock()
	defer s.pendingMu.Unlock()
	s.nextID++
	return json.RawMessage(strconv.AppendInt(nil, s.nextID, 10))
}

func (s *appServerSession) failPending(message string) {
	s.pendingMu.Lock()
	pending := s.pending
	s.pending = make(map[string]chan rpcEnvelope)
	s.pendingMu.Unlock()
	for _, ch := range pending {
		ch <- rpcEnvelope{Error: &rpcError{Message: message}}
	}
}

func (s *appServerSession) applyThreadConfigLocked(approvalPolicy json.RawMessage, cwd, model, modelProvider, reasoningEffort, serviceTier string, sandboxPolicy json.RawMessage) {
	s.currentCWD = firstNonEmpty(strings.TrimSpace(cwd), s.projectPath)
	s.model = strings.TrimSpace(model)
	s.modelProvider = strings.TrimSpace(modelProvider)
	s.reasoningEffort = strings.TrimSpace(reasoningEffort)
	if strings.EqualFold(strings.TrimSpace(s.pendingModel), s.model) {
		s.pendingModel = ""
	}
	if strings.EqualFold(strings.TrimSpace(s.pendingReasoning), s.reasoningEffort) {
		s.pendingReasoning = ""
	}
	s.serviceTier = strings.TrimSpace(serviceTier)
	s.approvalPolicy = append(json.RawMessage(nil), approvalPolicy...)
	s.sandboxPolicy = append(json.RawMessage(nil), sandboxPolicy...)
}

func (s *appServerSession) readRateLimits() (*rateLimitSnapshot, map[string]rateLimitSnapshot, error) {
	ctx, cancel := context.WithTimeout(context.Background(), rpcTimeout)
	defer cancel()

	result, err := s.call(ctx, "account/rateLimits/read", nil)
	if err != nil {
		return nil, nil, err
	}
	var response accountRateLimitsResponse
	if err := json.Unmarshal(result, &response); err != nil {
		return nil, nil, err
	}

	var limits *rateLimitSnapshot
	if hasRateLimitSnapshot(response.RateLimits) {
		limits = cloneRateLimitSnapshot(&response.RateLimits)
	}
	return limits, cloneRateLimitSnapshotMap(response.RateLimitsByID), nil
}

func cloneThreadTokenUsage(in *threadTokenUsage) *threadTokenUsage {
	if in == nil {
		return nil
	}
	out := *in
	return &out
}

func exportedTokenUsageSnapshot(in *threadTokenUsage) *TokenUsageSnapshot {
	if in == nil {
		return nil
	}
	out := &TokenUsageSnapshot{
		Last:  exportedTokenUsageBreakdown(in.Last),
		Total: exportedTokenUsageBreakdown(in.Total),
	}
	if in.ModelContextWindow != nil && *in.ModelContextWindow > 0 {
		out.ModelContextWindow = *in.ModelContextWindow
	}
	return out
}

func exportedTokenUsageBreakdown(in tokenUsageBreakdown) TokenUsageBreakdown {
	return TokenUsageBreakdown{
		CachedInputTokens:     in.CachedInputTokens,
		InputTokens:           in.InputTokens,
		OutputTokens:          in.OutputTokens,
		ReasoningOutputTokens: in.ReasoningOutputTokens,
		TotalTokens:           in.TotalTokens,
	}
}

func exportedUsageWindowsSnapshot(primary *rateLimitSnapshot, byID map[string]rateLimitSnapshot) []UsageWindowSnapshot {
	embedded := collectEmbeddedStatusUsageWindows(primary, byID)
	if len(embedded) == 0 {
		return nil
	}
	out := make([]UsageWindowSnapshot, 0, len(embedded))
	for _, window := range embedded {
		snapshot := UsageWindowSnapshot{
			Limit:       window.Limit,
			Plan:        window.Plan,
			Window:      window.Window,
			LeftPercent: window.LeftPercent,
		}
		if window.ResetsAt > 0 {
			snapshot.ResetsAt = time.Unix(window.ResetsAt, 0).Local()
		}
		out = append(out, snapshot)
	}
	return out
}

func cloneRateLimitSnapshot(in *rateLimitSnapshot) *rateLimitSnapshot {
	if in == nil {
		return nil
	}
	out := *in
	if in.Primary != nil {
		window := *in.Primary
		out.Primary = &window
	}
	if in.Secondary != nil {
		window := *in.Secondary
		out.Secondary = &window
	}
	if in.Credits != nil {
		credits := *in.Credits
		out.Credits = &credits
	}
	return &out
}

func cloneRateLimitSnapshotMap(in map[string]rateLimitSnapshot) map[string]rateLimitSnapshot {
	if len(in) == 0 {
		return nil
	}
	out := make(map[string]rateLimitSnapshot, len(in))
	for key, value := range in {
		cloned := cloneRateLimitSnapshot(&value)
		if cloned != nil {
			out[key] = *cloned
		}
	}
	return out
}

func hasRateLimitSnapshot(snapshot rateLimitSnapshot) bool {
	return strings.TrimSpace(stringValue(snapshot.LimitID)) != "" ||
		strings.TrimSpace(stringValue(snapshot.LimitName)) != "" ||
		strings.TrimSpace(stringValue(snapshot.PlanType)) != "" ||
		snapshot.Primary != nil ||
		snapshot.Secondary != nil ||
		snapshot.Credits != nil
}

func (s *appServerSession) touchLocked() {
	s.lastActivityAt = time.Now()
}

func (s *appServerSession) touchBusyLocked() {
	now := time.Now()
	s.lastActivityAt = now
	s.lastBusyActivityAt = now
}

func (s *appServerSession) clearActiveStateLocked() {
	s.clearBusyLocked("")
	s.pendingApproval = nil
	s.pendingToolInput = nil
	s.pendingElicitation = nil
}

func (s *appServerSession) syncThreadStatusLocked(threadID string, status resumedThreadStatus, recovered bool) {
	threadID = strings.TrimSpace(threadID)
	currentThreadID := strings.TrimSpace(s.threadID)
	if currentThreadID != "" && threadID != "" && currentThreadID != threadID {
		return
	}

	switch strings.TrimSpace(status.Type) {
	case "idle":
		hadPendingCompletion := s.pendingCompletion != nil
		hadActiveTurn := s.busy || strings.TrimSpace(s.activeTurnID) != "" || len(s.activeItems) > 0
		hadInteractiveState := s.pendingApproval != nil || s.pendingToolInput != nil || s.pendingElicitation != nil

		statusText := ""
		if hadPendingCompletion {
			statusText = strings.TrimSpace(s.pendingCompletion.Status)
		} else if hadActiveTurn && recovered {
			statusText = "Recovered idle after status check"
		} else if hadActiveTurn {
			statusText = "Turn finished"
		} else if hadInteractiveState {
			statusText = "Codex session ready"
		}

		s.clearActiveStateLocked()
		if statusText != "" {
			s.status = statusText
			s.lastSystemNotice = statusText
		}
	case "active":
		s.reconciling = false
	case "systemError":
		hadState := s.busy || s.pendingCompletion != nil || strings.TrimSpace(s.activeTurnID) != "" || s.pendingApproval != nil || s.pendingToolInput != nil || s.pendingElicitation != nil
		s.clearActiveStateLocked()
		if hadState {
			s.status = "Codex thread reported a system error"
			s.lastSystemNotice = "Codex thread reported a system error"
		}
	}
}

func (s *appServerSession) handleTransportFailure(err error) {
	if err == nil {
		return
	}
	message := strings.TrimSpace(err.Error())
	if message == "" {
		return
	}

	var cmd *exec.Cmd
	var stdin io.WriteCloser

	s.mu.Lock()
	if s.closed {
		s.mu.Unlock()
		return
	}
	s.touchLocked()
	s.closed = true
	s.clearActiveStateLocked()
	s.appendEntryLocked("", TranscriptError, message)
	s.lastError = message
	s.lastSystemNotice = message
	s.status = "Codex transport failed; session closed"
	cmd = s.cmd
	stdin = s.stdin
	s.mu.Unlock()

	if stdin != nil {
		_ = stdin.Close()
	}
	if cmd != nil && cmd.Process != nil {
		_ = terminateAppServerCommand(cmd)
	}
	s.failPending(message)
	s.notify()
}

func (s *appServerSession) appendSystemNotice(message string) {
	if strings.TrimSpace(message) == "" {
		return
	}
	s.mu.Lock()
	s.touchLocked()
	s.appendEntryLocked("", TranscriptSystem, message)
	s.lastSystemNotice = message
	s.status = message
	s.mu.Unlock()
	s.notify()
}

func (s *appServerSession) appendSystemError(err error) {
	if err == nil {
		return
	}
	message := err.Error()
	s.mu.Lock()
	s.touchLocked()
	s.appendEntryLocked("", TranscriptError, message)
	s.lastError = message
	s.lastSystemNotice = message
	s.status = "Codex error"
	s.mu.Unlock()
	s.notify()
	s.maybeAppendAuth403Diagnosis(message)
}

func diagnoseCodexAuth403(message string) string {
	normalized := strings.ToLower(strings.TrimSpace(message))
	if !strings.Contains(normalized, "403 forbidden") {
		return ""
	}
	switch {
	case strings.Contains(normalized, "backend-api/codex/responses"),
		strings.Contains(normalized, "failed to connect to websocket"),
		strings.Contains(normalized, "unexpected status 403 forbidden"):
		return "Codex rejected the request with HTTP 403. This usually means ChatGPT authentication, session access, or Codex entitlement is unavailable, or ChatGPT account access is temporarily degraded. It is usually not a Little Control Room transport bug. Check `codex login status`; if needed, run `codex logout` and `codex login`, then retry once ChatGPT account access is healthy again."
	default:
		return ""
	}
}

func codexAuth403StatusLabel() string {
	return "Codex auth/session rejected (HTTP 403)"
}

func (s *appServerSession) maybeAppendAuth403Diagnosis(message string) {
	diagnosis := diagnoseCodexAuth403(message)
	if diagnosis == "" {
		return
	}

	s.mu.Lock()
	if s.reportedAuth403 {
		s.mu.Unlock()
		return
	}
	s.touchLocked()
	s.reportedAuth403 = true
	s.appendEntryLocked("", TranscriptSystem, diagnosis)
	s.lastSystemNotice = diagnosis
	status := strings.ToLower(strings.TrimSpace(s.status))
	if status == "" || status == "codex error" || strings.HasPrefix(status, "codex stderr:") || strings.Contains(status, "403 forbidden") {
		s.status = codexAuth403StatusLabel()
	}
	s.mu.Unlock()
	s.notify()
}

func (s *appServerSession) appendEntryLocked(itemID string, kind TranscriptKind, text string) {
	if itemID != "" {
		if index, ok := s.entryIndex[itemID]; ok {
			s.entries[index].Text += text
			return
		}
		s.entryIndex[itemID] = len(s.entries)
	}
	s.entries = append(s.entries, transcriptEntry{ItemID: itemID, Kind: kind, Text: text})
}

func (s *appServerSession) ensureItemEntryLocked(itemID string, kind TranscriptKind, text string) {
	if itemID == "" {
		s.appendEntryLocked("", kind, text)
		return
	}
	if index, ok := s.entryIndex[itemID]; ok {
		if s.entries[index].Kind == "" {
			s.entries[index].Kind = kind
		}
		if s.entries[index].Text == "" {
			s.entries[index].Text = text
		}
		return
	}
	s.entryIndex[itemID] = len(s.entries)
	s.entries = append(s.entries, transcriptEntry{ItemID: itemID, Kind: kind, Text: text})
}

func (s *appServerSession) bindOptimisticEntryLocked(itemID string, kind TranscriptKind, text string) bool {
	if itemID == "" || strings.TrimSpace(text) == "" {
		return false
	}
	trimmed := strings.TrimSpace(text)
	for i := len(s.entries) - 1; i >= 0; i-- {
		entry := &s.entries[i]
		if entry.ItemID != "" || entry.Kind != kind {
			continue
		}
		if strings.TrimSpace(entry.Text) != trimmed {
			continue
		}
		entry.ItemID = itemID
		entry.Kind = kind
		entry.Text = text
		s.entryIndex[itemID] = i
		return true
	}
	return false
}

func (s *appServerSession) upsertItemEntryLocked(itemID string, kind TranscriptKind, text string) {
	if itemID == "" {
		s.appendEntryLocked("", kind, text)
		return
	}
	if index, ok := s.entryIndex[itemID]; ok {
		s.entries[index].Kind = kind
		s.entries[index].Text = text
		return
	}
	if s.bindOptimisticEntryLocked(itemID, kind, text) {
		return
	}
	s.entryIndex[itemID] = len(s.entries)
	s.entries = append(s.entries, transcriptEntry{ItemID: itemID, Kind: kind, Text: text})
}

func (s *appServerSession) appendDeltaToItemLocked(itemID string, kind TranscriptKind, text string) {
	if text == "" {
		return
	}
	if itemID == "" {
		s.appendEntryLocked("", kind, text)
		return
	}
	if index, ok := s.entryIndex[itemID]; ok {
		if s.entries[index].Kind == "" || s.entries[index].Kind == TranscriptOther {
			s.entries[index].Kind = kind
		}
		s.entries[index].Text += text
		return
	}
	s.entryIndex[itemID] = len(s.entries)
	s.entries = append(s.entries, transcriptEntry{ItemID: itemID, Kind: kind, Text: text})
}

func (s *appServerSession) mergeHistoryItemLocked(itemID string, kind TranscriptKind, text string) {
	if strings.TrimSpace(text) == "" {
		return
	}
	if itemID == "" {
		s.appendEntryLocked("", kind, text)
		return
	}
	if index, ok := s.entryIndex[itemID]; ok {
		if s.entries[index].Kind == "" {
			s.entries[index].Kind = kind
		}
		current := s.entries[index].Text
		switch {
		case current == "":
			s.entries[index].Text = text
		case strings.HasPrefix(text, current):
			s.entries[index].Text = text
		case strings.HasPrefix(current, text):
			return
		}
		return
	}
	s.entryIndex[itemID] = len(s.entries)
	s.entries = append(s.entries, transcriptEntry{ItemID: itemID, Kind: kind, Text: text})
}

func (s *appServerSession) finalizeCommandItemLocked(itemID string, item map[string]json.RawMessage) {
	text := strings.TrimSpace(renderResumedCommandExecution(item))
	if itemID == "" {
		if text != "" {
			s.appendEntryLocked("", TranscriptCommand, text)
		}
		return
	}
	if index, ok := s.entryIndex[itemID]; ok {
		s.entries[index].Kind = TranscriptCommand
		aggregated := strings.TrimSpace(decodeRawNullableString(item["aggregatedOutput"]))
		switch {
		case text == "":
			return
		case aggregated != "", strings.TrimSpace(s.entries[index].Text) == "":
			s.entries[index].Text = text
		default:
			statusLine := renderCommandStatusLine(decodeRawString(item["status"]), decodeRawNullableInt(item["exitCode"]))
			if statusLine == "" {
				s.entries[index].Text = text
			} else {
				s.entries[index].Text = upsertTrailingSummaryLine(s.entries[index].Text, statusLine, "[command ")
			}
		}
		return
	}
	s.upsertItemEntryLocked(itemID, TranscriptCommand, text)
}

func (s *appServerSession) finalizeFileChangeItemLocked(itemID string, item map[string]json.RawMessage) {
	text := strings.TrimSpace(renderResumedFileChange(item))
	if itemID == "" {
		if text != "" {
			s.appendEntryLocked("", TranscriptFileChange, text)
		}
		return
	}
	if index, ok := s.entryIndex[itemID]; ok {
		s.entries[index].Kind = TranscriptFileChange
		switch {
		case strings.TrimSpace(s.entries[index].Text) == "":
			s.entries[index].Text = text
		default:
			statusLine := renderFileChangeStatusLine(decodeRawString(item["status"]))
			if statusLine == "" {
				if text != "" {
					s.entries[index].Text = text
				}
			} else {
				s.entries[index].Text = upsertTrailingSummaryLine(s.entries[index].Text, statusLine, "[file changes ")
			}
		}
		return
	}
	s.upsertItemEntryLocked(itemID, TranscriptFileChange, text)
}

func upsertTrailingSummaryLine(text, summary, prefix string) string {
	text = strings.TrimRight(text, "\n")
	summary = strings.TrimSpace(summary)
	if text == "" {
		return summary
	}
	if summary == "" {
		return text
	}
	lines := strings.Split(text, "\n")
	for i := len(lines) - 1; i >= 0; i-- {
		trimmed := strings.TrimSpace(lines[i])
		if trimmed == "" {
			continue
		}
		if strings.HasPrefix(trimmed, prefix) {
			lines[i] = summary
			return strings.Join(lines, "\n")
		}
	}
	lines = append(lines, summary)
	return strings.Join(lines, "\n")
}

func (s *appServerSession) hydrateResumedThread(thread resumedThread) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.hydrateResumedThreadLocked(thread)
}

func (s *appServerSession) hydrateResumedThreadLocked(thread resumedThread) {
	s.touchLocked()
	if thread.ID != "" {
		s.threadID = thread.ID
	}
	wasBusy := s.busy
	previousBusySince := s.busySince
	previousBusyActivityAt := s.lastBusyActivityAt
	previousTurnID := strings.TrimSpace(s.activeTurnID)

	busy := thread.Status.Type == "active"
	busyExternal := busy
	activeTurnID := ""
	s.activeItems = nil
	s.pendingCompletion = nil

	for _, turn := range thread.Turns {
		if turn.Status == "inProgress" {
			busy = true
			busyExternal = true
			activeTurnID = turn.ID
		}
		for _, item := range turn.Items {
			itemID, kind, text := renderResumedThreadItem(item)
			s.mergeHistoryItemLocked(itemID, kind, text)
		}
		if turn.Status == "failed" && turn.Error != nil && strings.TrimSpace(turn.Error.Message) != "" {
			s.appendEntryLocked("", TranscriptError, turn.Error.Message)
		}
	}

	busySince := time.Time{}
	lastBusyActivityAt := time.Time{}
	switch {
	case !busy:
		busySince = time.Time{}
	case !previousBusySince.IsZero() && wasBusy && previousTurnID == strings.TrimSpace(activeTurnID):
		busySince = previousBusySince
		lastBusyActivityAt = previousBusyActivityAt
	}

	s.busy = busy
	s.busyExternal = busyExternal
	s.busySince = busySince
	s.lastBusyActivityAt = lastBusyActivityAt
	s.activeTurnID = activeTurnID
}

func readLines(r io.Reader, handle func([]byte)) error {
	reader := bufio.NewReader(r)
	for {
		line, err := reader.ReadBytes('\n')
		if len(line) > 0 {
			line = bytes.TrimRight(line, "\r\n")
			if len(line) > 0 {
				handle(line)
			}
		}
		if err != nil {
			if errors.Is(err, io.EOF) {
				return nil
			}
			return err
		}
	}
}

func approvalPolicyForPreset(p codexcli.Preset) string {
	switch p {
	case codexcli.PresetFullAuto, codexcli.PresetSafe:
		return "on-request"
	default:
		return "never"
	}
}

func sandboxModeForPreset(p codexcli.Preset) string {
	switch p {
	case codexcli.PresetFullAuto:
		return "workspace-write"
	case codexcli.PresetSafe:
		return "read-only"
	default:
		return "danger-full-access"
	}
}

func decodeRawString(raw json.RawMessage) string {
	if len(raw) == 0 {
		return ""
	}
	var value string
	if err := json.Unmarshal(raw, &value); err != nil {
		return ""
	}
	return value
}

func decodeRawInt(raw json.RawMessage) int {
	if len(raw) == 0 {
		return -1
	}
	var value int
	if err := json.Unmarshal(raw, &value); err != nil {
		return -1
	}
	return value
}

func idKey(raw json.RawMessage) string {
	return strings.TrimSpace(string(raw))
}

func decodeRequestID(value string) any {
	value = strings.TrimSpace(value)
	if value == "" {
		return nil
	}
	if value[0] == '"' {
		var raw string
		if err := json.Unmarshal([]byte(value), &raw); err == nil {
			return raw
		}
	}
	if n, err := strconv.ParseInt(value, 10, 64); err == nil {
		return n
	}
	return value
}

func normalizeRequestID(value any) string {
	switch v := value.(type) {
	case string:
		return strconv.Quote(v)
	case float64:
		return strconv.FormatInt(int64(v), 10)
	case int64:
		return strconv.FormatInt(v, 10)
	case int:
		return strconv.Itoa(v)
	default:
		raw, err := json.Marshal(v)
		if err != nil {
			return ""
		}
		return strings.TrimSpace(string(raw))
	}
}

func cloneApprovalRequest(req *ApprovalRequest) *ApprovalRequest {
	if req == nil {
		return nil
	}
	clone := *req
	return &clone
}

func cloneToolInputRequest(req *ToolInputRequest) *ToolInputRequest {
	if req == nil {
		return nil
	}
	clone := *req
	if len(req.Questions) > 0 {
		clone.Questions = make([]ToolInputQuestion, len(req.Questions))
		copy(clone.Questions, req.Questions)
		for i := range req.Questions {
			if len(req.Questions[i].Options) > 0 {
				clone.Questions[i].Options = append([]ToolInputOption(nil), req.Questions[i].Options...)
			}
		}
	}
	return &clone
}

func cloneElicitationRequest(req *ElicitationRequest) *ElicitationRequest {
	if req == nil {
		return nil
	}
	clone := *req
	if len(req.RequestedSchema) > 0 {
		clone.RequestedSchema = append(json.RawMessage(nil), req.RequestedSchema...)
	}
	return &clone
}

func shortID(id string) string {
	if len(id) <= 8 {
		return id
	}
	return id[:8]
}

func firstNonEmpty(values ...string) string {
	for _, value := range values {
		if strings.TrimSpace(value) != "" {
			return value
		}
	}
	return ""
}

type activeTurnMismatch struct {
	ExpectedTurnID string
	FoundTurnID    string
}

func isActiveTurnMismatchError(err error) bool {
	return parseActiveTurnMismatchError(err) != nil
}

func parseActiveTurnMismatchError(err error) *activeTurnMismatch {
	if err == nil {
		return nil
	}
	message := strings.TrimSpace(err.Error())
	const prefix = "expected active turn id "
	if !strings.HasPrefix(message, prefix) {
		return nil
	}
	rest := strings.TrimPrefix(message, prefix)
	expectedTurnID, remainder, ok := parseQuotedTurnID(rest)
	if !ok {
		return nil
	}
	const separator = " but found "
	if !strings.HasPrefix(remainder, separator) {
		return nil
	}
	foundTurnID, remainder, ok := parseQuotedTurnID(strings.TrimPrefix(remainder, separator))
	if !ok || strings.TrimSpace(remainder) != "" {
		return nil
	}
	return &activeTurnMismatch{
		ExpectedTurnID: expectedTurnID,
		FoundTurnID:    foundTurnID,
	}
}

func parseQuotedTurnID(value string) (string, string, bool) {
	value = strings.TrimSpace(value)
	if value == "" {
		return "", "", false
	}
	quote := value[0]
	if quote != '`' && quote != '"' && quote != '\'' {
		return "", "", false
	}
	end := strings.IndexByte(value[1:], quote)
	if end < 0 {
		return "", "", false
	}
	end++
	return value[1:end], value[end+1:], true
}

func activeTurnIDFromThread(thread resumedThread) string {
	for i := len(thread.Turns) - 1; i >= 0; i-- {
		turn := thread.Turns[i]
		if strings.EqualFold(strings.TrimSpace(turn.Status), "inProgress") && strings.TrimSpace(turn.ID) != "" {
			return strings.TrimSpace(turn.ID)
		}
	}
	return ""
}

func normalizeSubmission(input Submission) Submission {
	input.Text = strings.TrimSpace(input.Text)
	attachments := make([]Attachment, 0, len(input.Attachments))
	for _, attachment := range input.Attachments {
		if strings.TrimSpace(attachment.Path) == "" {
			continue
		}
		attachments = append(attachments, attachment)
	}
	input.Attachments = attachments
	return input
}

func encodeSubmissionInput(input Submission) []userInput {
	input = normalizeSubmission(input)
	items := make([]userInput, 0, 1+len(input.Attachments))
	if input.Text != "" {
		items = append(items, userInput{
			Type: "text",
			Text: input.Text,
		})
	}
	for _, attachment := range input.Attachments {
		switch attachment.Kind {
		case AttachmentLocalImage:
			items = append(items, userInput{
				Type: "localImage",
				Path: attachment.Path,
			})
		}
	}
	return items
}

func formatTranscriptEntry(kind TranscriptKind, text string) string {
	text = strings.TrimSpace(text)
	if text == "" {
		return ""
	}
	switch kind {
	case TranscriptUser:
		return "You: " + text
	case TranscriptAgent:
		return "Codex: " + text
	case TranscriptStatus:
		return "[status] " + text
	case TranscriptSystem:
		return "[system] " + text
	case TranscriptError:
		return "[error] " + text
	case TranscriptPlan:
		return "Plan: " + text
	case TranscriptReasoning:
		return "Reasoning: " + text
	default:
		return text
	}
}

func renderResumedThreadItem(item map[string]json.RawMessage) (string, TranscriptKind, string) {
	itemID := decodeRawString(item["id"])
	switch decodeRawString(item["type"]) {
	case "userMessage":
		text := renderResumedUserMessage(item["content"])
		if text == "" {
			return itemID, TranscriptUser, ""
		}
		return itemID, TranscriptUser, text
	case "agentMessage":
		text := strings.TrimSpace(decodeRawString(item["text"]))
		if text == "" {
			return itemID, TranscriptAgent, ""
		}
		return itemID, TranscriptAgent, text
	case "plan":
		text := strings.TrimSpace(decodeRawString(item["text"]))
		if text == "" {
			return itemID, TranscriptPlan, ""
		}
		return itemID, TranscriptPlan, text
	case "reasoning":
		content := decodeRawStringSlice(item["summary"])
		if len(content) == 0 {
			content = decodeRawStringSlice(item["content"])
		}
		if len(content) == 0 {
			return itemID, TranscriptReasoning, ""
		}
		return itemID, TranscriptReasoning, strings.Join(content, "\n")
	case "commandExecution":
		return itemID, TranscriptCommand, renderResumedCommandExecution(item)
	case "fileChange":
		return itemID, TranscriptFileChange, renderResumedFileChange(item)
	case "mcpToolCall":
		tool := strings.TrimSpace(decodeRawString(item["tool"]))
		server := strings.TrimSpace(decodeRawString(item["server"]))
		status := strings.TrimSpace(decodeRawString(item["status"]))
		if tool == "" && server == "" {
			return itemID, TranscriptTool, ""
		}
		label := "MCP tool"
		if server != "" {
			label += " " + server
		}
		if tool != "" {
			label += "/" + tool
		}
		if status != "" {
			label += " [" + status + "]"
		}
		return itemID, TranscriptTool, label
	case "dynamicToolCall":
		tool := strings.TrimSpace(decodeRawString(item["tool"]))
		status := strings.TrimSpace(decodeRawString(item["status"]))
		if tool == "" {
			return itemID, TranscriptTool, ""
		}
		label := "Tool " + tool
		if status != "" {
			label += " [" + status + "]"
		}
		return itemID, TranscriptTool, label
	case "webSearch":
		query := strings.TrimSpace(decodeRawString(item["query"]))
		if query == "" {
			return itemID, TranscriptTool, ""
		}
		return itemID, TranscriptTool, "Web search: " + query
	case "imageView":
		path := strings.TrimSpace(decodeRawString(item["path"]))
		if path == "" {
			return itemID, TranscriptTool, ""
		}
		return itemID, TranscriptTool, "Viewed image: " + path
	case "imageGeneration":
		status := strings.TrimSpace(decodeRawString(item["status"]))
		result := strings.TrimSpace(decodeRawString(item["result"]))
		text := "Image generation"
		if status != "" {
			text += " [" + status + "]"
		}
		if result != "" {
			text += "\n" + result
		}
		return itemID, TranscriptTool, text
	case "enteredReviewMode":
		review := strings.TrimSpace(decodeRawString(item["review"]))
		if review == "" {
			review = "Entered review mode"
		}
		return itemID, TranscriptSystem, review
	case "exitedReviewMode":
		review := strings.TrimSpace(decodeRawString(item["review"]))
		if review == "" {
			review = "Exited review mode"
		}
		return itemID, TranscriptSystem, review
	case "contextCompaction":
		return itemID, TranscriptSystem, "Conversation history compacted"
	default:
		return itemID, TranscriptOther, ""
	}
}

func renderResumedUserMessage(raw json.RawMessage) string {
	if len(raw) == 0 {
		return ""
	}
	var content []map[string]json.RawMessage
	if err := json.Unmarshal(raw, &content); err != nil {
		return ""
	}
	parts := make([]string, 0, len(content))
	for _, item := range content {
		switch decodeRawString(item["type"]) {
		case "text", "input_text", "output_text":
			text := strings.TrimSpace(decodeRawString(item["text"]))
			if text != "" {
				parts = append(parts, text)
			}
		case "localImage", "local_image":
			path := strings.TrimSpace(decodeRawString(item["path"]))
			if path != "" {
				label := Attachment{Kind: AttachmentLocalImage, Path: path}.DisplayLabel()
				parts = append(parts, "[attached image] "+label)
			} else {
				parts = append(parts, "[attached image]")
			}
		case "image", "input_image":
			parts = append(parts, "[image]")
		}
	}
	return strings.Join(parts, "\n")
}

func renderResumedCommandExecution(item map[string]json.RawMessage) string {
	command := strings.TrimSpace(decodeRawString(item["command"]))
	cwd := strings.TrimSpace(decodeRawString(item["cwd"]))
	status := strings.TrimSpace(decodeRawString(item["status"]))
	output := decodeRawNullableString(item["aggregatedOutput"])

	lines := make([]string, 0, 4)
	if command != "" {
		lines = append(lines, "$ "+command)
	}
	if cwd != "" {
		lines = append(lines, "# cwd: "+cwd)
	}
	if strings.TrimSpace(output) != "" {
		lines = append(lines, output)
	}
	if summary := renderCommandStatusLine(status, decodeRawNullableInt(item["exitCode"])); summary != "" {
		lines = append(lines, summary)
	}
	return strings.Join(lines, "\n")
}

func renderResumedFileChange(item map[string]json.RawMessage) string {
	status := strings.TrimSpace(decodeRawString(item["status"]))
	changeCount := decodeRawArrayLen(item["changes"])
	text := "Applying file changes"
	if changeCount > 0 {
		text = fmt.Sprintf("Applying %d file change(s)", changeCount)
	}
	if summary := renderFileChangeStatusLine(status); summary != "" {
		text += "\n" + summary
	}
	return text
}

func renderCommandStatusLine(status string, exitCode *int) string {
	status = strings.TrimSpace(status)
	if status == "" {
		return ""
	}
	summary := "[command " + status
	if exitCode != nil {
		summary += fmt.Sprintf(", exit %d", *exitCode)
	}
	summary += "]"
	return summary
}

func renderFileChangeStatusLine(status string) string {
	status = strings.TrimSpace(status)
	if status == "" {
		return ""
	}
	return "[file changes " + status + "]"
}

func decodeRawNullableString(raw json.RawMessage) string {
	if len(raw) == 0 || string(raw) == "null" {
		return ""
	}
	return decodeRawString(raw)
}

func decodeRawNullableInt(raw json.RawMessage) *int {
	if len(raw) == 0 || string(raw) == "null" {
		return nil
	}
	value := decodeRawInt(raw)
	if value < 0 {
		return nil
	}
	return &value
}

func decodeRawStringSlice(raw json.RawMessage) []string {
	if len(raw) == 0 {
		return nil
	}
	var values []string
	if err := json.Unmarshal(raw, &values); err != nil {
		return nil
	}
	out := make([]string, 0, len(values))
	for _, value := range values {
		value = strings.TrimSpace(value)
		if value != "" {
			out = append(out, value)
		}
	}
	return out
}

func decodeRawArrayLen(raw json.RawMessage) int {
	if len(raw) == 0 {
		return 0
	}
	var values []json.RawMessage
	if err := json.Unmarshal(raw, &values); err != nil {
		return 0
	}
	return len(values)
}

func stringValue(value *string) string {
	if value == nil {
		return ""
	}
	return *value
}

func formatApprovalPolicy(raw json.RawMessage) string {
	if len(raw) == 0 || string(raw) == "null" {
		return ""
	}
	var simple string
	if err := json.Unmarshal(raw, &simple); err == nil {
		return strings.TrimSpace(simple)
	}
	var value any
	if err := json.Unmarshal(raw, &value); err != nil {
		return ""
	}
	compact, err := json.Marshal(value)
	if err != nil {
		return ""
	}
	return string(compact)
}

type sandboxPolicySummary struct {
	Mode          string
	NetworkAccess string
	WritableRoots []string
}

func summarizeSandboxPolicy(raw json.RawMessage) sandboxPolicySummary {
	if len(raw) == 0 || string(raw) == "null" {
		return sandboxPolicySummary{}
	}
	var value map[string]json.RawMessage
	if err := json.Unmarshal(raw, &value); err != nil {
		return sandboxPolicySummary{}
	}
	summary := sandboxPolicySummary{}
	switch strings.TrimSpace(decodeRawString(value["type"])) {
	case "dangerFullAccess":
		summary.Mode = "danger-full-access"
		summary.NetworkAccess = "full"
	case "readOnly":
		summary.Mode = "read-only"
		if decodeRawBool(value["networkAccess"]) {
			summary.NetworkAccess = "enabled"
		} else {
			summary.NetworkAccess = "disabled"
		}
	case "workspaceWrite":
		summary.Mode = "workspace-write"
		if decodeRawBool(value["networkAccess"]) {
			summary.NetworkAccess = "enabled"
		} else {
			summary.NetworkAccess = "disabled"
		}
		summary.WritableRoots = decodeRawStringSlice(value["writableRoots"])
	case "externalSandbox":
		summary.Mode = "external-sandbox"
		network := strings.TrimSpace(decodeRawString(value["networkAccess"]))
		if network == "" {
			network = "restricted"
		}
		summary.NetworkAccess = network
	}
	return summary
}

func formatThreadTokenUsage(usage *threadTokenUsage) string {
	if usage == nil {
		return ""
	}
	parts := []string{}
	if usage.Total.TotalTokens > 0 {
		total := fmt.Sprintf("total %d", usage.Total.TotalTokens)
		if usage.ModelContextWindow != nil && *usage.ModelContextWindow > 0 {
			percent := int(float64(usage.Total.TotalTokens) * 100 / float64(*usage.ModelContextWindow))
			total += fmt.Sprintf(" of %d tokens (%d%%)", *usage.ModelContextWindow, percent)
		} else {
			total += " tokens"
		}
		parts = append(parts, total)
	}
	if usage.Last.TotalTokens > 0 {
		parts = append(parts, fmt.Sprintf("last turn %d tokens", usage.Last.TotalTokens))
	}
	if len(parts) == 0 {
		return ""
	}
	return strings.Join(parts, "; ")
}

func buildEmbeddedStatusText(threadID, projectPath, currentCWD, model, modelProvider, reasoningEffort, serviceTier string, approvalPolicy, sandboxPolicy json.RawMessage, tokenUsage *threadTokenUsage, rateLimits *rateLimitSnapshot, rateLimitsByID map[string]rateLimitSnapshot) string {
	lines := []string{"Embedded Codex status"}
	if threadID != "" {
		lines = append(lines, "thread: "+threadID)
	}
	if projectPath != "" {
		lines = append(lines, "project: "+projectPath)
	}
	if currentCWD != "" {
		lines = append(lines, "cwd: "+currentCWD)
	}
	if model != "" {
		lines = append(lines, "model: "+model)
	}
	if modelProvider != "" {
		lines = append(lines, "model provider: "+modelProvider)
	}
	if reasoningEffort != "" {
		lines = append(lines, "reasoning effort: "+reasoningEffort)
	}
	if serviceTier != "" {
		lines = append(lines, "service tier: "+serviceTier)
	}
	if approval := formatApprovalPolicy(approvalPolicy); approval != "" {
		lines = append(lines, "approval: "+approval)
	}
	sandboxSummary := summarizeSandboxPolicy(sandboxPolicy)
	if sandboxSummary.Mode != "" {
		lines = append(lines, "sandbox: "+sandboxSummary.Mode)
	}
	if sandboxSummary.NetworkAccess != "" {
		lines = append(lines, "network: "+sandboxSummary.NetworkAccess)
	}
	if len(sandboxSummary.WritableRoots) > 0 {
		lines = append(lines, "writable roots: "+strings.Join(sandboxSummary.WritableRoots, ", "))
	}
	if tokenUsage != nil {
		tokenUsageSnapshot := exportedTokenUsageSnapshot(tokenUsage)
		if tokenUsage.Total.TotalTokens > 0 {
			lines = append(lines, fmt.Sprintf("total tokens: %d", tokenUsage.Total.TotalTokens))
		}
		if tokenUsageSnapshot != nil && tokenUsageSnapshot.ModelContextWindow > 0 {
			lines = append(lines, fmt.Sprintf("model context window: %d", tokenUsageSnapshot.ModelContextWindow))
			if contextTokens := tokenUsageSnapshot.EstimatedContextTokens(); contextTokens > 0 {
				lines = append(lines, fmt.Sprintf("context tokens: %d", contextTokens))
				lines = append(lines, fmt.Sprintf("context used percent: %d", 100-tokenUsageSnapshot.ContextLeftPercent()))
			}
		}
		if tokenUsage.Last.TotalTokens > 0 {
			lines = append(lines, fmt.Sprintf("last turn tokens: %d", tokenUsage.Last.TotalTokens))
		}
	}
	for _, window := range collectEmbeddedStatusUsageWindows(rateLimits, rateLimitsByID) {
		parts := []string{
			"limit=" + window.Limit,
			"window=" + window.Window,
			fmt.Sprintf("left=%d", window.LeftPercent),
		}
		if window.Plan != "" {
			parts = append(parts, "plan="+window.Plan)
		}
		if window.ResetsAt > 0 {
			parts = append(parts, fmt.Sprintf("resetsAt=%d", window.ResetsAt))
		}
		lines = append(lines, "usage window: "+strings.Join(parts, "; "))
	}
	return strings.Join(lines, "\n")
}

type embeddedStatusUsageWindow struct {
	Limit       string
	Plan        string
	Window      string
	LeftPercent int
	ResetsAt    int64
}

func collectEmbeddedStatusUsageWindows(primary *rateLimitSnapshot, byID map[string]rateLimitSnapshot) []embeddedStatusUsageWindow {
	windows := make([]embeddedStatusUsageWindow, 0, 4)
	seen := make(map[string]struct{})
	keys := make([]string, 0, len(byID))
	for key := range byID {
		keys = append(keys, key)
	}
	sort.Slice(keys, func(i, j int) bool {
		left := strings.ToLower(keys[i])
		right := strings.ToLower(keys[j])
		if left == "codex" {
			return right != "codex"
		}
		if right == "codex" {
			return false
		}
		return left < right
	})
	for _, key := range keys {
		snapshot := byID[key]
		appendEmbeddedStatusUsageWindows(&windows, seen, &snapshot, key)
	}
	appendEmbeddedStatusUsageWindows(&windows, seen, primary, "")
	return windows
}

func appendEmbeddedStatusUsageWindows(windows *[]embeddedStatusUsageWindow, seen map[string]struct{}, snapshot *rateLimitSnapshot, fallbackLabel string) {
	if snapshot == nil {
		return
	}
	limitLabel := firstNonEmpty(
		strings.TrimSpace(stringValue(snapshot.LimitName)),
		strings.TrimSpace(stringValue(snapshot.LimitID)),
		strings.TrimSpace(fallbackLabel),
	)
	if limitLabel == "" {
		limitLabel = "usage"
	}
	plan := strings.TrimSpace(stringValue(snapshot.PlanType))
	appendEmbeddedStatusUsageWindow(windows, seen, limitLabel, plan, "primary", snapshot.Primary)
	appendEmbeddedStatusUsageWindow(windows, seen, limitLabel, plan, "secondary", snapshot.Secondary)
}

func appendEmbeddedStatusUsageWindow(windows *[]embeddedStatusUsageWindow, seen map[string]struct{}, limitLabel, plan, fallbackWindow string, window *rateLimitWindow) {
	if window == nil {
		return
	}
	windowLabel := rateLimitWindowDisplayLabel(fallbackWindow, window.WindowDurationMins)
	key := strings.ToLower(limitLabel) + "|" + strings.ToLower(windowLabel)
	if _, ok := seen[key]; ok {
		return
	}
	seen[key] = struct{}{}
	resetsAt := int64(0)
	if window.ResetsAt != nil {
		resetsAt = *window.ResetsAt
	}
	*windows = append(*windows, embeddedStatusUsageWindow{
		Limit:       limitLabel,
		Plan:        plan,
		Window:      windowLabel,
		LeftPercent: clampPercent(100 - window.UsedPercent),
		ResetsAt:    resetsAt,
	})
}

func rateLimitWindowDisplayLabel(fallback string, durationMins *int64) string {
	if durationMins == nil || *durationMins <= 0 {
		return fallback
	}
	switch mins := *durationMins; {
	case mins == 7*24*60:
		return "weekly"
	case mins%(24*60) == 0:
		return fmt.Sprintf("%dd", mins/(24*60))
	case mins%60 == 0:
		return fmt.Sprintf("%dh", mins/60)
	default:
		return fmt.Sprintf("%dm", mins)
	}
}

func clampPercent(percent int) int {
	switch {
	case percent < 0:
		return 0
	case percent > 100:
		return 100
	default:
		return percent
	}
}

func formatRateLimitStatus(primary *rateLimitSnapshot, byID map[string]rateLimitSnapshot) string {
	snapshot := primary
	if snapshot == nil && len(byID) > 0 {
		if preferred, ok := byID["codex"]; ok {
			snapshot = cloneRateLimitSnapshot(&preferred)
		} else {
			keys := make([]string, 0, len(byID))
			for key := range byID {
				keys = append(keys, key)
			}
			sort.Strings(keys)
			first := byID[keys[0]]
			snapshot = cloneRateLimitSnapshot(&first)
		}
	}
	if snapshot == nil {
		return ""
	}
	parts := []string{}
	if label := firstNonEmpty(strings.TrimSpace(stringValue(snapshot.LimitName)), strings.TrimSpace(stringValue(snapshot.LimitID))); label != "" {
		parts = append(parts, label)
	}
	if plan := strings.TrimSpace(stringValue(snapshot.PlanType)); plan != "" {
		parts = append(parts, "plan "+plan)
	}
	if window := formatRateLimitWindow("primary", snapshot.Primary); window != "" {
		parts = append(parts, window)
	}
	if window := formatRateLimitWindow("secondary", snapshot.Secondary); window != "" {
		parts = append(parts, window)
	}
	if snapshot.Credits != nil {
		switch {
		case snapshot.Credits.Unlimited:
			parts = append(parts, "credits unlimited")
		case snapshot.Credits.HasCredits && strings.TrimSpace(stringValue(snapshot.Credits.Balance)) != "":
			parts = append(parts, "credits "+strings.TrimSpace(stringValue(snapshot.Credits.Balance)))
		}
	}
	return strings.Join(parts, "; ")
}

func formatRateLimitWindow(label string, window *rateLimitWindow) string {
	if window == nil {
		return ""
	}
	parts := []string{fmt.Sprintf("%s %d%%", label, window.UsedPercent)}
	if window.WindowDurationMins != nil && *window.WindowDurationMins > 0 {
		parts = append(parts, fmt.Sprintf("%dm", *window.WindowDurationMins))
	}
	if window.ResetsAt != nil && *window.ResetsAt > 0 {
		parts = append(parts, "resets "+time.Unix(*window.ResetsAt, 0).Local().Format("15:04"))
	}
	return strings.Join(parts, " ")
}

func decodeRawBool(raw json.RawMessage) bool {
	if len(raw) == 0 || string(raw) == "null" {
		return false
	}
	var value bool
	if err := json.Unmarshal(raw, &value); err != nil {
		return false
	}
	return value
}
