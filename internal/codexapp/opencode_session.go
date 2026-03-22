package codexapp

import (
	"bufio"
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"mime"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"

	"lcroom/internal/codexcli"
)

const (
	openCodeRPCTimeout        = 20 * time.Second
	openCodeReconnectDelay    = 500 * time.Millisecond
	openCodeListeningPrefix   = "opencode server listening on "
	openCodeDefaultAgent      = "build"
	openCodeIdleShutdownAfter = idleShutdownAfter
)

type openCodeSession struct {
	projectPath string
	preset      codexcli.Preset
	notify      func()

	cmd      *exec.Cmd
	http     *http.Client
	baseURL  string
	agent    string
	cancel   context.CancelFunc
	exitCh   chan struct{}
	exitOnce sync.Once

	mu                 sync.Mutex
	sessionID          string
	started            bool
	busy               bool
	busyExternal       bool
	closed             bool
	status             string
	lastError          string
	lastSystemNotice   string
	lastActivityAt     time.Time
	busySince          time.Time
	lastBusyActivityAt time.Time
	currentCWD         string
	model              string
	modelProvider      string
	reasoningEffort    string
	pendingModel       string
	pendingReasoning   string
	pendingFromLaunch  bool
	activeTurnID       string
	tokenUsage         *threadTokenUsage
	entries            []transcriptEntry
	entryIndex         map[string]int
	messageRole        map[string]string
	partKind           map[string]TranscriptKind
	partType           map[string]string
	modelOptions       []ModelOption
	modelOptionsByKey  map[string]ModelOption
	pendingApproval    *ApprovalRequest
	pendingToolInput   *ToolInputRequest
}

type openCodeSessionEnvelope struct {
	ID        string              `json:"id"`
	Directory string              `json:"directory"`
	Time      openCodeSessionTime `json:"time"`
}

type openCodeSessionTime struct {
	Created int64 `json:"created"`
	Updated int64 `json:"updated"`
}

type openCodeSessionStatusMap map[string]openCodeStatus

type openCodeStatus struct {
	Type    string `json:"type"`
	Attempt int    `json:"attempt,omitempty"`
	Message string `json:"message,omitempty"`
	Next    int64  `json:"next,omitempty"`
}

type openCodeMessage struct {
	Info  openCodeMessageInfo `json:"info"`
	Parts []json.RawMessage   `json:"parts"`
}

type openCodeMessageInfo struct {
	ID         string                `json:"id"`
	SessionID  string                `json:"sessionID"`
	Role       string                `json:"role"`
	ModelID    string                `json:"modelID"`
	ProviderID string                `json:"providerID"`
	Agent      string                `json:"agent"`
	Finish     string                `json:"finish"`
	Error      *openCodeMessageError `json:"error,omitempty"`
	Path       struct {
		CWD  string `json:"cwd"`
		Root string `json:"root"`
	} `json:"path"`
	Time struct {
		Created   int64 `json:"created"`
		Completed int64 `json:"completed"`
	} `json:"time"`
	Tokens *struct {
		Total     int64 `json:"total"`
		Input     int64 `json:"input"`
		Output    int64 `json:"output"`
		Reasoning int64 `json:"reasoning"`
		Cache     struct {
			Read  int64 `json:"read"`
			Write int64 `json:"write"`
		} `json:"cache"`
	} `json:"tokens"`
}

type openCodeMessageError struct {
	Name string `json:"name"`
	Data struct {
		Message    string `json:"message"`
		StatusCode int    `json:"statusCode"`
		Metadata   struct {
			URL string `json:"url"`
		} `json:"metadata"`
	} `json:"data"`
}

type openCodePartHeader struct {
	ID        string `json:"id"`
	SessionID string `json:"sessionID"`
	MessageID string `json:"messageID"`
	Type      string `json:"type"`
}

type openCodeEvent struct {
	Type       string          `json:"type"`
	Properties json.RawMessage `json:"properties"`
}

type openCodeEventMessageUpdated struct {
	Info openCodeMessageInfo `json:"info"`
}

type openCodeEventPartUpdated struct {
	Part json.RawMessage `json:"part"`
}

type openCodeEventPartDelta struct {
	SessionID string `json:"sessionID"`
	MessageID string `json:"messageID"`
	PartID    string `json:"partID"`
	Field     string `json:"field"`
	Delta     string `json:"delta"`
}

type openCodeEventSessionStatus struct {
	SessionID string         `json:"sessionID"`
	Status    openCodeStatus `json:"status"`
}

type openCodeProvidersResponse struct {
	Providers []openCodeProviderEnvelope `json:"providers"`
	Default   map[string]string          `json:"default"`
}

type openCodeProviderEnvelope struct {
	ID     string                           `json:"id"`
	Name   string                           `json:"name"`
	Models map[string]openCodeProviderModel `json:"models"`
}

type openCodeProviderModel struct {
	ID          string `json:"id"`
	ProviderID  string `json:"providerID"`
	Name        string `json:"name"`
	Status      string `json:"status"`
	Description string `json:"description"`
	Limit       struct {
		Context int64 `json:"context"`
	} `json:"limit"`
	Capabilities struct {
		Reasoning  bool `json:"reasoning"`
		Attachment bool `json:"attachment"`
	} `json:"capabilities"`
	Options  map[string]any `json:"options"`
	Variants map[string]struct {
		ReasoningEffort string `json:"reasoningEffort"`
	} `json:"variants"`
}

type openCodeAgents []struct {
	Name string `json:"name"`
	Mode string `json:"mode"`
}

type openCodePromptRequest struct {
	Model   *openCodePromptModel `json:"model,omitempty"`
	Agent   string               `json:"agent,omitempty"`
	Variant string               `json:"variant,omitempty"`
	Parts   []openCodePromptPart `json:"parts"`
}

type openCodePromptModel struct {
	ProviderID string `json:"providerID"`
	ModelID    string `json:"modelID"`
}

type openCodePromptPart struct {
	Type     string `json:"type"`
	Text     string `json:"text,omitempty"`
	Mime     string `json:"mime,omitempty"`
	Filename string `json:"filename,omitempty"`
	URL      string `json:"url,omitempty"`
}

type openCodePermissionRequest struct {
	ID         string         `json:"id"`
	SessionID  string         `json:"sessionID"`
	Permission string         `json:"permission"`
	Patterns   []string       `json:"patterns"`
	Metadata   map[string]any `json:"metadata"`
}

type openCodePermissionReply struct {
	Reply   string `json:"reply"`
	Message string `json:"message,omitempty"`
}

type openCodeQuestionRequest struct {
	ID        string `json:"id"`
	SessionID string `json:"sessionID"`
	Questions []struct {
		Question string `json:"question"`
		Header   string `json:"header"`
		Options  []struct {
			Label       string `json:"label"`
			Description string `json:"description"`
		} `json:"options"`
		Multiple bool `json:"multiple"`
		Custom   bool `json:"custom"`
	} `json:"questions"`
}

type openCodeQuestionReply struct {
	Answers [][]string `json:"answers"`
}

func newOpenCodeHTTPClient() *http.Client {
	// Use per-request contexts for RPC deadlines and leave the shared client
	// without a global timeout so the long-lived SSE event stream can stay open.
	return &http.Client{}
}

func newOpenCodeSession(req LaunchRequest, notify func()) (Session, error) {
	ctx, cancel := context.WithCancel(context.Background())
	s := &openCodeSession{
		projectPath:       req.ProjectPath,
		preset:            req.Preset,
		notify:            notify,
		http:              newOpenCodeHTTPClient(),
		agent:             openCodeDefaultAgent,
		cancel:            cancel,
		exitCh:            make(chan struct{}),
		entryIndex:        make(map[string]int),
		messageRole:       make(map[string]string),
		partKind:          make(map[string]TranscriptKind),
		partType:          make(map[string]string),
		modelOptionsByKey: make(map[string]ModelOption),
		status:            "Starting OpenCode server...",
		lastActivityAt:    time.Now(),
	}
	if err := s.start(ctx, req); err != nil {
		_ = s.Close()
		return nil, err
	}
	return s, nil
}

func (s *openCodeSession) ProjectPath() string {
	return s.projectPath
}

func (s *openCodeSession) Snapshot() Snapshot {
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
		lines = append(lines, formatTranscriptEntryForProvider(ProviderOpenCode, entry.Kind, text))
	}

	return Snapshot{
		Provider:           ProviderOpenCode,
		ProjectPath:        s.projectPath,
		ThreadID:           s.sessionID,
		Preset:             s.preset,
		Phase:              s.phaseLocked(),
		Started:            s.started,
		Busy:               s.busy,
		BusyExternal:       s.busyExternal,
		BusySince:          s.busySinceLocked(),
		LastBusyActivityAt: s.lastBusyActivityAt,
		Closed:             s.closed,
		ActiveTurnID:       s.activeTurnID,
		PendingApproval:    cloneApprovalRequest(s.pendingApproval),
		PendingToolInput:   cloneToolInputRequest(s.pendingToolInput),
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
		PendingModel:       s.pendingModel,
		PendingReasoning:   s.pendingReasoning,
		TokenUsage:         exportedTokenUsageSnapshot(s.tokenUsage),
	}
}

func (s *openCodeSession) phaseLocked() SessionPhase {
	switch {
	case s.closed:
		return SessionPhaseClosed
	case s.busyExternal:
		return SessionPhaseExternal
	case s.busy:
		return SessionPhaseRunning
	default:
		return SessionPhaseIdle
	}
}

func (s *openCodeSession) busySinceLocked() time.Time {
	if !s.busy {
		return time.Time{}
	}
	return s.busySince
}

func (s *openCodeSession) setBusyLocked(external bool) {
	now := time.Now()
	if !s.busy || s.busySince.IsZero() {
		s.busySince = now
	}
	s.busy = true
	s.busyExternal = external
	s.lastActivityAt = now
	s.lastBusyActivityAt = now
}

func (s *openCodeSession) touchBusyLocked() {
	now := time.Now()
	s.lastActivityAt = now
	if s.busy {
		s.lastBusyActivityAt = now
	}
}

func (s *openCodeSession) clearBusyLocked() {
	s.busy = false
	s.busyExternal = false
	s.busySince = time.Time{}
	s.lastBusyActivityAt = time.Time{}
	s.activeTurnID = ""
}

func (s *openCodeSession) Submit(prompt string) error {
	return s.SubmitInput(Submission{Text: prompt})
}

func (s *openCodeSession) SubmitInput(input Submission) error {
	input = normalizeSubmission(input)
	if input.Empty() {
		return nil
	}

	s.mu.Lock()
	if s.closed {
		s.mu.Unlock()
		return fmt.Errorf("opencode session is closed")
	}
	if s.busyExternal {
		s.mu.Unlock()
		return fmt.Errorf("this OpenCode session is already busy in another process; Little Control Room is read-only until it finishes")
	}
	sessionID := s.sessionID
	model := firstNonEmpty(strings.TrimSpace(s.pendingModel), strings.TrimSpace(s.model))
	reasoning := firstNonEmpty(strings.TrimSpace(s.pendingReasoning), strings.TrimSpace(s.reasoningEffort))
	agent := strings.TrimSpace(s.agent)
	s.touchLocked()
	s.status = "Sending prompt to OpenCode..."
	s.mu.Unlock()
	s.notify()

	payload, err := buildOpenCodePromptRequest(input, agent, model, reasoning)
	if err != nil {
		s.appendSystemError(err)
		return err
	}

	if err := s.postJSON(context.Background(), "/session/"+sessionID+"/prompt_async", payload, nil); err != nil {
		s.appendSystemError(err)
		return err
	}

	s.mu.Lock()
	s.setBusyLocked(false)
	if model != "" {
		s.model = model
	}
	if reasoning != "" {
		s.reasoningEffort = reasoning
	}
	s.pendingModel = ""
	s.pendingReasoning = ""
	s.status = "OpenCode is working..."
	if s.activeTurnID == "" {
		s.activeTurnID = sessionID
	}
	s.mu.Unlock()
	s.notify()
	return nil
}

func (s *openCodeSession) ShowStatus() error {
	s.mu.Lock()
	if s.closed {
		s.mu.Unlock()
		return fmt.Errorf("opencode session is closed")
	}
	statusText := buildOpenCodeStatusText(
		s.sessionID,
		s.projectPath,
		s.currentCWD,
		s.model,
		s.modelProvider,
		s.reasoningEffort,
		s.agent,
		s.tokenUsage,
	)
	s.touchLocked()
	s.appendEntryLocked("", TranscriptStatus, statusText)
	s.lastSystemNotice = "Displayed embedded OpenCode status"
	s.status = "Displayed embedded OpenCode status"
	s.mu.Unlock()
	s.notify()
	return nil
}

func (s *openCodeSession) ListModels() ([]ModelOption, error) {
	models, err := s.refreshModelOptions(context.Background())
	if err != nil {
		return nil, err
	}
	return models, nil
}

func (s *openCodeSession) StageModelOverride(model, reasoningEffort string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.closed {
		return fmt.Errorf("opencode session is closed")
	}
	currentModel := strings.TrimSpace(s.model)
	currentReasoning := strings.TrimSpace(s.reasoningEffort)
	model = strings.TrimSpace(model)
	reasoningEffort = strings.TrimSpace(reasoningEffort)
	if model == "" {
		model = currentModel
	}
	if reasoningEffort == "" {
		reasoningEffort = currentReasoning
	}
	if strings.EqualFold(model, currentModel) && strings.EqualFold(reasoningEffort, currentReasoning) {
		s.pendingModel = ""
		s.pendingReasoning = ""
	} else {
		s.pendingModel, s.pendingReasoning = stagedModelOverride(currentModel, currentReasoning, model, reasoningEffort)
	}
	s.touchLocked()
	go s.notify()
	return nil
}

func (s *openCodeSession) Interrupt() error {
	s.mu.Lock()
	if s.closed {
		s.mu.Unlock()
		return fmt.Errorf("opencode session is closed")
	}
	if s.busyExternal {
		s.mu.Unlock()
		return fmt.Errorf("this OpenCode session is already busy in another process; interrupt it there or hide it here")
	}
	if !s.busy {
		s.mu.Unlock()
		return fmt.Errorf("no active OpenCode turn to interrupt")
	}
	sessionID := s.sessionID
	s.touchLocked()
	s.status = "Interrupting OpenCode turn..."
	s.mu.Unlock()
	s.notify()
	if err := s.postJSON(context.Background(), "/session/"+sessionID+"/abort", nil, nil); err != nil {
		s.appendSystemError(err)
		return err
	}
	return nil
}

func (s *openCodeSession) RespondApproval(decision ApprovalDecision) error {
	s.mu.Lock()
	if s.closed {
		s.mu.Unlock()
		return fmt.Errorf("opencode session is closed")
	}
	request := cloneApprovalRequest(s.pendingApproval)
	if request == nil {
		s.mu.Unlock()
		return fmt.Errorf("no pending approval")
	}
	s.touchLocked()
	s.status = "Sending OpenCode approval decision..."
	s.mu.Unlock()
	s.notify()

	reply := "once"
	switch decision {
	case DecisionAcceptForSession:
		reply = "always"
	case DecisionDecline, DecisionCancel:
		reply = "reject"
	}
	if err := s.postJSON(context.Background(), "/permission/"+request.ID+"/reply", openCodePermissionReply{Reply: reply}, nil); err != nil {
		s.appendSystemError(err)
		return err
	}
	_ = s.refreshPendingRequests(context.Background())
	return nil
}

func (s *openCodeSession) RespondToolInput(answers map[string][]string) error {
	s.mu.Lock()
	if s.closed {
		s.mu.Unlock()
		return fmt.Errorf("opencode session is closed")
	}
	request := cloneToolInputRequest(s.pendingToolInput)
	if request == nil {
		s.mu.Unlock()
		return fmt.Errorf("no pending question request")
	}
	s.touchLocked()
	s.status = "Sending OpenCode answers..."
	s.mu.Unlock()
	s.notify()

	ordered := make([][]string, 0, len(request.Questions))
	for _, question := range request.Questions {
		ordered = append(ordered, normalizeAnswerValues(answers[question.ID]))
	}
	if err := s.postJSON(context.Background(), "/question/"+request.ID+"/reply", openCodeQuestionReply{Answers: ordered}, nil); err != nil {
		s.appendSystemError(err)
		return err
	}
	_ = s.refreshPendingRequests(context.Background())
	return nil
}

func (s *openCodeSession) RespondElicitation(decision ElicitationDecision, content json.RawMessage) error {
	return fmt.Errorf("OpenCode does not currently expose embedded MCP elicitation replies")
}

func (s *openCodeSession) Close() error {
	s.mu.Lock()
	if s.closed {
		s.mu.Unlock()
		return nil
	}
	s.closed = true
	s.clearBusyLocked()
	s.pendingApproval = nil
	s.pendingToolInput = nil
	s.status = "OpenCode session closed"
	cmd := s.cmd
	cancel := s.cancel
	s.mu.Unlock()

	if cancel != nil {
		cancel()
	}
	if cmd != nil && cmd.Process != nil {
		_ = cmd.Process.Kill()
	} else {
		s.closeExitCh()
	}
	s.notify()
	return nil
}

func (s *openCodeSession) CloseDueToInactivity() error {
	s.mu.Lock()
	if s.closed {
		s.mu.Unlock()
		return nil
	}
	if s.busy || s.pendingApproval != nil || s.pendingToolInput != nil {
		s.touchLocked()
		s.mu.Unlock()
		return nil
	}
	s.closed = true
	s.status = idleShutdownNotice
	s.lastSystemNotice = idleShutdownNotice
	s.appendEntryLocked("", TranscriptSystem, idleShutdownNotice)
	cmd := s.cmd
	cancel := s.cancel
	s.mu.Unlock()

	if cancel != nil {
		cancel()
	}
	if cmd != nil && cmd.Process != nil {
		_ = cmd.Process.Kill()
	} else {
		s.closeExitCh()
	}
	s.notify()
	return nil
}

func (s *openCodeSession) WaitClosed(timeout time.Duration) bool {
	if s == nil {
		return true
	}
	s.mu.Lock()
	closed := s.closed
	exitCh := s.exitCh
	s.mu.Unlock()
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

func (s *openCodeSession) RefreshBusyElsewhere() error {
	return s.refreshSessionState(context.Background(), true)
}

func (s *openCodeSession) ReconcileBusyState() error {
	return s.refreshSessionState(context.Background(), false)
}

func (s *openCodeSession) start(parent context.Context, req LaunchRequest) error {
	baseURL, cmd, err := startOpenCodeServer(req.ProjectPath, req.Preset)
	if err != nil {
		return err
	}
	s.baseURL = baseURL
	s.cmd = cmd

	go s.waitForExit()
	if err := s.initializeSession(parent, req); err != nil {
		return err
	}
	go s.runEventLoop(parent)
	return nil
}

func (s *openCodeSession) initializeSession(parent context.Context, req LaunchRequest) error {
	ctx, cancel := context.WithTimeout(parent, openCodeRPCTimeout)
	defer cancel()

	if err := s.loadDefaultAgent(ctx); err != nil {
		s.appendSystemNotice("OpenCode agent list was unavailable; using build.")
	}
	if _, err := s.refreshModelOptions(ctx); err != nil {
		s.appendSystemNotice("OpenCode model list was unavailable during startup.")
	}

	sessionID := strings.TrimSpace(req.ResumeID)
	resumed := false
	if req.ForceNew {
		sessionID = ""
	}
	if !req.ForceNew && sessionID != "" {
		var existing openCodeSessionEnvelope
		if err := s.getJSON(ctx, "/session/"+sessionID, &existing); err == nil && strings.TrimSpace(existing.ID) != "" {
			resumed = true
		} else {
			sessionID = ""
		}
	}
	launchPending := strings.TrimSpace(req.PendingModel) != "" || strings.TrimSpace(req.PendingReasoning) != ""
	if sessionID == "" {
		var created openCodeSessionEnvelope
		if err := s.postJSON(ctx, "/session", nil, &created); err != nil {
			return err
		}
		sessionID = strings.TrimSpace(created.ID)
		if sessionID == "" {
			return fmt.Errorf("opencode session create returned no session id")
		}
		if req.ForceNew {
			if err := s.ensureFreshSession(ctx, sessionID); err != nil {
				return err
			}
		}
	} else {
		if req.ForceNew {
			if err := s.ensureFreshSession(ctx, sessionID); err != nil {
				return err
			}
		}
	}

	s.mu.Lock()
	s.sessionID = sessionID
	s.started = true
	s.pendingFromLaunch = launchPending
	s.pendingModel, s.pendingReasoning = stagedModelOverride(s.model, s.reasoningEffort, req.PendingModel, req.PendingReasoning)
	s.status = ""
	s.mu.Unlock()

	if err := s.refreshSessionState(ctx, resumed); err != nil {
		return err
	}

	if resumed {
		s.appendSystemNotice("Resumed embedded OpenCode session " + shortID(sessionID) + ".")
	} else {
		s.appendSystemNotice("Started a new embedded OpenCode session " + shortID(sessionID) + ".")
	}
	if strings.TrimSpace(req.Prompt) != "" {
		if snapshot := s.Snapshot(); snapshot.BusyExternal {
			s.appendSystemNotice("This OpenCode session is already active in another process. The embedded prompt was not sent; use /opencode-new for a separate session.")
			return nil
		}
		return s.Submit(req.Prompt)
	}
	return nil
}

func (s *openCodeSession) ensureFreshSession(ctx context.Context, sessionID string) error {
	sessionID = strings.TrimSpace(sessionID)
	if sessionID == "" {
		return fmt.Errorf("ensureFreshSession called with empty sessionID")
	}
	messages := []openCodeMessage{}
	if err := s.getJSON(ctx, "/session/"+sessionID+"/message", &messages); err != nil {
		return err
	}
	if len(messages) > 0 {
		return &ForceNewSessionReusedError{Provider: ProviderOpenCode, ThreadID: sessionID}
	}
	return nil
}

func (s *openCodeSession) refreshSessionState(parent context.Context, external bool) error {
	s.mu.Lock()
	if s.closed {
		s.mu.Unlock()
		return fmt.Errorf("opencode session is closed")
	}
	sessionID := s.sessionID
	s.touchLocked()
	s.mu.Unlock()

	ctx, cancel := context.WithTimeout(parent, openCodeRPCTimeout)
	defer cancel()

	var messages []openCodeMessage
	if err := s.getJSON(ctx, "/session/"+sessionID+"/message", &messages); err != nil {
		s.appendSystemError(err)
		return err
	}
	var statuses openCodeSessionStatusMap
	if err := s.getJSON(ctx, "/session/status", &statuses); err != nil {
		statuses = nil
	}
	if err := s.refreshPendingRequests(ctx); err != nil {
		s.appendSystemNotice("OpenCode pending requests could not be refreshed.")
	}

	s.mu.Lock()
	if s.closed {
		s.mu.Unlock()
		return nil
	}
	latestError, replayedModel := s.rebuildTranscriptLocked(messages)
	if replayedModel {
		s.reconcilePendingModelFromReplayedMessagesLocked()
	}
	status := statuses[sessionID]
	switch status.Type {
	case "busy", "retry":
		s.setBusyLocked(external)
		if s.activeTurnID == "" {
			s.activeTurnID = sessionID
		}
		if external {
			s.status = "OpenCode is active in another process"
			s.lastSystemNotice = "OpenCode is active in another process"
		} else {
			s.status = "OpenCode is working..."
		}
	default:
		s.clearBusyLocked()
		if latestError != "" {
			s.lastError = latestError
			s.lastSystemNotice = latestError
			s.status = latestError
		} else {
			s.lastError = ""
		}
		if external && latestError == "" {
			s.status = "OpenCode session ready"
			s.lastSystemNotice = "OpenCode session ready"
		}
	}
	s.mu.Unlock()
	s.notify()
	return nil
}

func (s *openCodeSession) loadDefaultAgent(ctx context.Context) error {
	var agents openCodeAgents
	if err := s.getJSON(ctx, "/agent", &agents); err != nil {
		return err
	}
	chosen := openCodeDefaultAgent
	for _, agent := range agents {
		if strings.EqualFold(strings.TrimSpace(agent.Name), openCodeDefaultAgent) {
			chosen = openCodeDefaultAgent
			break
		}
		if chosen == openCodeDefaultAgent && strings.TrimSpace(agent.Name) != "" && strings.EqualFold(strings.TrimSpace(agent.Mode), "primary") {
			chosen = strings.TrimSpace(agent.Name)
		}
	}
	s.mu.Lock()
	s.agent = chosen
	s.mu.Unlock()
	return nil
}

func (s *openCodeSession) refreshModelOptions(ctx context.Context) ([]ModelOption, error) {
	var response openCodeProvidersResponse
	if err := s.getJSON(ctx, "/config/providers", &response); err != nil {
		return nil, err
	}

	models := make([]ModelOption, 0, 64)
	index := make(map[string]ModelOption)
	for _, provider := range response.Providers {
		defaultModel := strings.TrimSpace(response.Default[strings.TrimSpace(provider.ID)])
		for _, model := range provider.Models {
			if strings.TrimSpace(model.ID) == "" || strings.EqualFold(strings.TrimSpace(model.Status), "deprecated") {
				continue
			}
			option := ModelOption{
				ID:                        qualifiedOpenCodeModelKey(provider.ID, model.ID),
				Model:                     qualifiedOpenCodeModelKey(provider.ID, model.ID),
				DisplayName:               firstNonEmpty(strings.TrimSpace(provider.Name)+" / "+strings.TrimSpace(model.Name), qualifiedOpenCodeModelKey(provider.ID, model.ID)),
				Description:               strings.TrimSpace(model.Description),
				DefaultReasoningEffort:    openCodeDefaultReasoningEffort(model),
				SupportsPersonality:       false,
				IsDefault:                 strings.EqualFold(strings.TrimSpace(model.ID), defaultModel),
				SupportedReasoningEfforts: openCodeReasoningOptions(model),
			}
			if option.Description == "" {
				option.Description = strings.TrimSpace(provider.Name)
			}
			models = append(models, option)
			index[option.Model] = option
		}
	}
	sort.SliceStable(models, func(i, j int) bool {
		if models[i].IsDefault != models[j].IsDefault {
			return models[i].IsDefault
		}
		return strings.ToLower(models[i].DisplayName) < strings.ToLower(models[j].DisplayName)
	})

	s.mu.Lock()
	s.modelOptions = append([]ModelOption(nil), models...)
	s.modelOptionsByKey = index
	if s.model != "" && s.reasoningEffort == "" {
		if option, ok := index[s.model]; ok {
			s.reasoningEffort = firstNonEmpty(strings.TrimSpace(s.reasoningEffort), strings.TrimSpace(option.DefaultReasoningEffort))
		}
	}
	s.mu.Unlock()
	return models, nil
}

func (s *openCodeSession) refreshPendingRequests(ctx context.Context) error {
	var permissions []openCodePermissionRequest
	if err := s.getJSON(ctx, "/permission", &permissions); err != nil {
		return err
	}
	var questions []openCodeQuestionRequest
	if err := s.getJSON(ctx, "/question", &questions); err != nil {
		return err
	}

	s.mu.Lock()
	defer s.mu.Unlock()

	sessionID := s.sessionID
	s.pendingApproval = nil
	for _, request := range permissions {
		if strings.TrimSpace(request.SessionID) != sessionID {
			continue
		}
		s.pendingApproval = mapOpenCodePermission(request)
		break
	}

	s.pendingToolInput = nil
	for _, request := range questions {
		if strings.TrimSpace(request.SessionID) != sessionID {
			continue
		}
		s.pendingToolInput = mapOpenCodeQuestion(request)
		break
	}
	return nil
}

func (s *openCodeSession) rebuildTranscriptLocked(messages []openCodeMessage) (string, bool) {
	s.entries = nil
	s.entryIndex = make(map[string]int)
	s.messageRole = make(map[string]string)
	s.partKind = make(map[string]TranscriptKind)
	s.partType = make(map[string]string)

	latestError := ""
	replayedModel := false
	for _, message := range messages {
		if strings.EqualFold(strings.TrimSpace(message.Info.Role), "assistant") {
			if modelKey := qualifiedOpenCodeModelKey(message.Info.ProviderID, message.Info.ModelID); modelKey != "" {
				replayedModel = true
			}
		}
		if errorSummary := s.applyMessageInfoLocked(message.Info); errorSummary != "" {
			latestError = errorSummary
		} else {
			latestError = ""
		}
		for _, rawPart := range message.Parts {
			s.applyPartLocked(message.Info.Role, rawPart, true)
		}
	}
	return latestError, replayedModel
}

func (s *openCodeSession) reconcilePendingModelFromReplayedMessagesLocked() {
	if !s.pendingFromLaunch {
		return
	}

	pendingModel := strings.TrimSpace(s.pendingModel)
	if pendingModel == "" {
		s.pendingFromLaunch = false
		return
	}

	if strings.EqualFold(strings.TrimSpace(s.model), pendingModel) {
		s.pendingFromLaunch = false
		s.pendingModel = ""
		s.pendingReasoning = ""
		return
	}

	s.pendingModel = ""
	s.pendingReasoning = ""
	s.pendingFromLaunch = false
}

func (s *openCodeSession) applyMessageInfoLocked(info openCodeMessageInfo) string {
	messageID := strings.TrimSpace(info.ID)
	if messageID != "" {
		s.messageRole[messageID] = strings.TrimSpace(info.Role)
	}
	if summary, detail := renderOpenCodeMessageError(info); summary != "" {
		s.upsertItemEntryLocked(messageID, TranscriptError, detail)
		return summary
	}
	if strings.EqualFold(strings.TrimSpace(info.Role), "assistant") {
		if modelKey := qualifiedOpenCodeModelKey(info.ProviderID, info.ModelID); modelKey != "" {
			s.model = modelKey
			s.modelProvider = strings.TrimSpace(info.ProviderID)
			if option, ok := s.modelOptionsByKey[modelKey]; ok && s.reasoningEffort == "" {
				s.reasoningEffort = strings.TrimSpace(option.DefaultReasoningEffort)
			}
		}
		if cwd := firstNonEmpty(strings.TrimSpace(info.Path.CWD), strings.TrimSpace(info.Path.Root)); cwd != "" {
			s.currentCWD = cwd
		}
		if strings.TrimSpace(info.Agent) != "" {
			s.agent = strings.TrimSpace(info.Agent)
		}
		if info.Time.Completed == 0 && messageID != "" {
			s.activeTurnID = messageID
		}
		if info.Tokens != nil && info.Tokens.Total > 0 {
			s.tokenUsage = &threadTokenUsage{
				Last: tokenUsageBreakdown{
					CachedInputTokens:     info.Tokens.Cache.Read,
					InputTokens:           info.Tokens.Input,
					OutputTokens:          info.Tokens.Output,
					ReasoningOutputTokens: info.Tokens.Reasoning,
					TotalTokens:           info.Tokens.Total,
				},
				Total: tokenUsageBreakdown{
					CachedInputTokens:     info.Tokens.Cache.Read,
					InputTokens:           info.Tokens.Input,
					OutputTokens:          info.Tokens.Output,
					ReasoningOutputTokens: info.Tokens.Reasoning,
					TotalTokens:           info.Tokens.Total,
				},
			}
		}
	}
	return ""
}

func (s *openCodeSession) applyPartLocked(role string, raw json.RawMessage, replace bool) {
	var header openCodePartHeader
	if err := json.Unmarshal(raw, &header); err != nil {
		return
	}
	if strings.TrimSpace(header.SessionID) != "" && strings.TrimSpace(header.SessionID) != s.sessionID {
		return
	}
	kind, text := renderOpenCodePart(role, raw)
	partID := strings.TrimSpace(header.ID)
	partType := strings.TrimSpace(header.Type)
	if partID != "" {
		s.partKind[partID] = kind
		s.partType[partID] = partType
	}
	if strings.TrimSpace(text) == "" {
		return
	}
	if replace {
		s.mergeHistoryItemLocked(partID, kind, text)
		return
	}
	s.upsertItemEntryLocked(partID, kind, text)
}

func (s *openCodeSession) runEventLoop(parent context.Context) {
	for {
		if s.isClosed() {
			return
		}
		err := s.consumeEventStream(parent)
		if s.isClosed() {
			return
		}
		if err == nil {
			time.Sleep(openCodeReconnectDelay)
			continue
		}
		s.handleTransportFailure(fmt.Errorf("opencode event stream error: %w", err))
		return
	}
}

func (s *openCodeSession) consumeEventStream(parent context.Context) error {
	ctx, cancel := context.WithCancel(parent)
	defer cancel()

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, s.baseURL+"/event", nil)
	if err != nil {
		return err
	}
	resp, err := s.http.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("GET /event: unexpected status %s", resp.Status)
	}

	reader := bufio.NewReader(resp.Body)
	var dataLines []string
	for {
		line, err := reader.ReadString('\n')
		if line != "" {
			line = strings.TrimRight(line, "\r\n")
			if line == "" {
				if len(dataLines) > 0 {
					s.handleEventData(strings.Join(dataLines, "\n"))
					dataLines = dataLines[:0]
				}
			} else if strings.HasPrefix(line, "data:") {
				dataLines = append(dataLines, strings.TrimSpace(strings.TrimPrefix(line, "data:")))
			}
		}
		if err != nil {
			if err == io.EOF {
				return nil
			}
			return err
		}
	}
}

func (s *openCodeSession) handleEventData(raw string) {
	var event openCodeEvent
	if err := json.Unmarshal([]byte(raw), &event); err != nil {
		return
	}
	switch event.Type {
	case "server.connected", "server.heartbeat", "file.watcher.updated":
		return
	case "session.status":
		var payload openCodeEventSessionStatus
		if err := json.Unmarshal(event.Properties, &payload); err != nil {
			return
		}
		if strings.TrimSpace(payload.SessionID) != s.sessionID {
			return
		}
		s.mu.Lock()
		s.touchBusyLocked()
		shouldRefresh := false
		switch payload.Status.Type {
		case "busy", "retry":
			s.setBusyLocked(false)
			if s.activeTurnID == "" {
				s.activeTurnID = s.sessionID
			}
			s.status = "OpenCode is working..."
		default:
			shouldRefresh = s.markIdleLocked()
		}
		s.mu.Unlock()
		if shouldRefresh {
			_ = s.refreshSessionState(context.Background(), false)
			return
		}
		s.notify()
	case "session.idle":
		var payload struct {
			SessionID string `json:"sessionID"`
		}
		if err := json.Unmarshal(event.Properties, &payload); err != nil {
			return
		}
		if strings.TrimSpace(payload.SessionID) != s.sessionID {
			return
		}
		s.mu.Lock()
		s.touchLocked()
		shouldRefresh := s.markIdleLocked()
		s.mu.Unlock()
		if shouldRefresh {
			_ = s.refreshSessionState(context.Background(), false)
			return
		}
		_ = s.refreshPendingRequests(context.Background())
		s.notify()
	case "message.updated":
		var payload openCodeEventMessageUpdated
		if err := json.Unmarshal(event.Properties, &payload); err != nil {
			return
		}
		if strings.TrimSpace(payload.Info.SessionID) != s.sessionID {
			return
		}
		s.mu.Lock()
		s.touchBusyLocked()
		if errorSummary := s.applyMessageInfoLocked(payload.Info); errorSummary != "" {
			s.lastError = errorSummary
			s.lastSystemNotice = errorSummary
			s.status = errorSummary
		}
		s.mu.Unlock()
		s.notify()
	case "message.part.updated":
		var payload openCodeEventPartUpdated
		if err := json.Unmarshal(event.Properties, &payload); err != nil {
			return
		}
		var header openCodePartHeader
		if err := json.Unmarshal(payload.Part, &header); err != nil {
			return
		}
		if strings.TrimSpace(header.SessionID) != s.sessionID {
			return
		}
		s.mu.Lock()
		s.touchBusyLocked()
		role := s.messageRole[strings.TrimSpace(header.MessageID)]
		s.applyPartLocked(role, payload.Part, true)
		s.mu.Unlock()
		s.notify()
	case "message.part.delta":
		var payload openCodeEventPartDelta
		if err := json.Unmarshal(event.Properties, &payload); err != nil {
			return
		}
		if strings.TrimSpace(payload.SessionID) != s.sessionID || !strings.EqualFold(strings.TrimSpace(payload.Field), "text") {
			return
		}
		s.mu.Lock()
		s.touchBusyLocked()
		role := s.messageRole[strings.TrimSpace(payload.MessageID)]
		kind := s.partKind[strings.TrimSpace(payload.PartID)]
		if kind == "" {
			if strings.EqualFold(strings.TrimSpace(role), "user") {
				kind = TranscriptUser
			} else {
				kind = TranscriptAgent
			}
		}
		s.appendDeltaToItemLocked(strings.TrimSpace(payload.PartID), kind, payload.Delta)
		s.mu.Unlock()
		s.notify()
	default:
		if strings.Contains(event.Type, "permission") || strings.Contains(event.Type, "question") {
			_ = s.refreshPendingRequests(context.Background())
			s.notify()
		}
	}
}

func (s *openCodeSession) waitForExit() {
	if s.cmd == nil {
		s.closeExitCh()
		return
	}
	err := s.cmd.Wait()
	s.closeExitCh()

	s.mu.Lock()
	if !s.closed {
		s.closed = true
		s.clearBusyLocked()
		s.pendingApproval = nil
		s.pendingToolInput = nil
		if err != nil {
			s.lastError = err.Error()
			s.lastSystemNotice = "OpenCode server exited with error"
			s.status = "OpenCode server exited with error"
		} else {
			s.lastSystemNotice = "OpenCode server exited"
			s.status = "OpenCode server exited"
		}
	}
	s.mu.Unlock()
	s.notify()
}

func (s *openCodeSession) closeExitCh() {
	s.exitOnce.Do(func() {
		close(s.exitCh)
	})
}

func (s *openCodeSession) isClosed() bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.closed
}

func (s *openCodeSession) markIdleLocked() bool {
	wasBusyExternal := s.busyExternal || strings.TrimSpace(s.status) == "OpenCode is active in another process"
	wasBusy := s.busy || s.activeTurnID != "" || strings.TrimSpace(s.status) == "OpenCode is working..."

	s.clearBusyLocked()

	switch {
	case wasBusyExternal:
		s.status = "OpenCode session ready"
		s.lastSystemNotice = "OpenCode session ready"
	case wasBusy:
		s.status = "Turn completed"
		s.lastSystemNotice = "Turn completed"
	}
	return wasBusyExternal || wasBusy
}

func (s *openCodeSession) touchLocked() {
	s.lastActivityAt = time.Now()
}

func (s *openCodeSession) appendSystemNotice(message string) {
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

func (s *openCodeSession) appendSystemError(err error) {
	if err == nil {
		return
	}
	message := strings.TrimSpace(err.Error())
	if message == "" {
		return
	}
	s.mu.Lock()
	s.touchLocked()
	s.appendEntryLocked("", TranscriptError, message)
	s.lastError = message
	s.lastSystemNotice = message
	s.status = "OpenCode error"
	s.mu.Unlock()
	s.notify()
}

func (s *openCodeSession) handleTransportFailure(err error) {
	if err == nil {
		return
	}
	message := strings.TrimSpace(err.Error())
	if message == "" {
		return
	}

	s.mu.Lock()
	if s.closed {
		s.mu.Unlock()
		return
	}
	s.touchLocked()
	s.closed = true
	s.clearBusyLocked()
	s.pendingApproval = nil
	s.pendingToolInput = nil
	s.appendEntryLocked("", TranscriptError, message)
	s.lastError = message
	s.lastSystemNotice = message
	s.status = "OpenCode transport failed; session closed"
	cmd := s.cmd
	cancel := s.cancel
	s.mu.Unlock()

	if cancel != nil {
		cancel()
	}
	if cmd != nil && cmd.Process != nil {
		_ = cmd.Process.Kill()
	}
	s.notify()
}

func (s *openCodeSession) appendEntryLocked(itemID string, kind TranscriptKind, text string) {
	if itemID != "" {
		if index, ok := s.entryIndex[itemID]; ok {
			s.entries[index].Text += text
			return
		}
		s.entryIndex[itemID] = len(s.entries)
	}
	s.entries = append(s.entries, transcriptEntry{ItemID: itemID, Kind: kind, Text: text})
}

func (s *openCodeSession) upsertItemEntryLocked(itemID string, kind TranscriptKind, text string) {
	if itemID == "" {
		s.appendEntryLocked("", kind, text)
		return
	}
	if index, ok := s.entryIndex[itemID]; ok {
		s.entries[index].Kind = kind
		s.entries[index].Text = text
		return
	}
	s.entryIndex[itemID] = len(s.entries)
	s.entries = append(s.entries, transcriptEntry{ItemID: itemID, Kind: kind, Text: text})
}

func (s *openCodeSession) appendDeltaToItemLocked(itemID string, kind TranscriptKind, text string) {
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

func (s *openCodeSession) mergeHistoryItemLocked(itemID string, kind TranscriptKind, text string) {
	if strings.TrimSpace(text) == "" {
		return
	}
	if itemID == "" {
		s.appendEntryLocked("", kind, text)
		return
	}
	if index, ok := s.entryIndex[itemID]; ok {
		current := s.entries[index].Text
		s.entries[index].Kind = kind
		switch {
		case current == "":
			s.entries[index].Text = text
		case strings.HasPrefix(text, current):
			s.entries[index].Text = text
		case strings.HasPrefix(current, text):
			return
		default:
			s.entries[index].Text = text
		}
		return
	}
	s.entryIndex[itemID] = len(s.entries)
	s.entries = append(s.entries, transcriptEntry{ItemID: itemID, Kind: kind, Text: text})
}

func (s *openCodeSession) getJSON(parent context.Context, path string, out any) error {
	ctx, cancel := context.WithTimeout(parent, openCodeRPCTimeout)
	defer cancel()

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, s.baseURL+path, nil)
	if err != nil {
		return err
	}
	resp, err := s.http.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode >= 300 {
		body, _ := io.ReadAll(io.LimitReader(resp.Body, 8192))
		return fmt.Errorf("GET %s: %s: %s", path, resp.Status, strings.TrimSpace(string(body)))
	}
	if out == nil {
		return nil
	}
	return json.NewDecoder(resp.Body).Decode(out)
}

func (s *openCodeSession) postJSON(parent context.Context, path string, payload any, out any) error {
	ctx, cancel := context.WithTimeout(parent, openCodeRPCTimeout)
	defer cancel()

	raw := []byte("{}")
	if payload != nil {
		var err error
		raw, err = json.Marshal(payload)
		if err != nil {
			return err
		}
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, s.baseURL+path, bytes.NewReader(raw))
	if err != nil {
		return err
	}
	req.Header.Set("content-type", "application/json")
	resp, err := s.http.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode >= 300 {
		data, _ := io.ReadAll(io.LimitReader(resp.Body, 8192))
		return fmt.Errorf("POST %s: %s: %s", path, resp.Status, strings.TrimSpace(string(data)))
	}
	if out == nil || resp.StatusCode == http.StatusNoContent {
		return nil
	}
	return json.NewDecoder(resp.Body).Decode(out)
}

type openCodePermissionOverride struct {
	Edit              string `json:"edit,omitempty"`
	Bash              string `json:"bash,omitempty"`
	WebFetch          string `json:"webfetch,omitempty"`
	DoomLoop          string `json:"doom_loop,omitempty"`
	ExternalDirectory string `json:"external_directory,omitempty"`
}

type openCodeConfigOverride struct {
	Permission openCodePermissionOverride `json:"permission,omitempty"`
}

func openCodePermissionOverrideForPreset(preset codexcli.Preset) openCodePermissionOverride {
	switch preset {
	case codexcli.PresetSafe:
		// OpenCode does not expose Codex's read-only sandbox directly, so the
		// closest safe preset is "ask before everything that mutates or escapes".
		return openCodePermissionOverride{
			Edit:              "ask",
			Bash:              "ask",
			WebFetch:          "ask",
			DoomLoop:          "ask",
			ExternalDirectory: "ask",
		}
	case codexcli.PresetFullAuto:
		// Match workspace-write-like behavior by allowing in-project work while
		// still prompting before broader filesystem escapes or loop-risk cases.
		return openCodePermissionOverride{
			Edit:              "allow",
			Bash:              "allow",
			WebFetch:          "allow",
			DoomLoop:          "ask",
			ExternalDirectory: "ask",
		}
	default:
		return openCodePermissionOverride{
			Edit:              "allow",
			Bash:              "allow",
			WebFetch:          "allow",
			DoomLoop:          "allow",
			ExternalDirectory: "allow",
		}
	}
}

func openCodeConfigContentForPreset(preset codexcli.Preset) (string, error) {
	raw, err := json.Marshal(openCodeConfigOverride{
		Permission: openCodePermissionOverrideForPreset(preset),
	})
	if err != nil {
		return "", err
	}
	return string(raw), nil
}

func envWithOverride(base []string, key, value string) []string {
	prefix := key + "="
	out := make([]string, 0, len(base)+1)
	for _, e := range base {
		if strings.HasPrefix(e, prefix) {
			continue
		}
		out = append(out, e)
	}
	out = append(out, key+"="+value)
	return out
}

func buildOpenCodeServerCommand(projectPath string, preset codexcli.Preset) (*exec.Cmd, error) {
	cmd := exec.Command("opencode", "serve", "--hostname", "127.0.0.1", "--port", "0", "--print-logs")
	cmd.Dir = projectPath
	configContent, err := openCodeConfigContentForPreset(preset)
	if err != nil {
		return nil, err
	}
	cmd.Env = envWithOverride(os.Environ(), "OPENCODE_CONFIG_CONTENT", configContent)
	return cmd, nil
}

func startOpenCodeServer(projectPath string, preset codexcli.Preset) (string, *exec.Cmd, error) {
	cmd, err := buildOpenCodeServerCommand(projectPath, preset)
	if err != nil {
		return "", nil, err
	}

	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return "", nil, err
	}
	stderr, err := cmd.StderrPipe()
	if err != nil {
		return "", nil, err
	}
	if err := cmd.Start(); err != nil {
		return "", nil, err
	}

	ready := make(chan string, 1)
	streamErr := make(chan error, 2)
	var once sync.Once
	handle := func(r io.Reader) {
		scanner := bufio.NewScanner(r)
		const maxTokenSize = 1024 * 1024
		buf := make([]byte, 0, 64*1024)
		scanner.Buffer(buf, maxTokenSize)
		for scanner.Scan() {
			line := strings.TrimSpace(scanner.Text())
			if strings.HasPrefix(line, openCodeListeningPrefix) {
				url := strings.TrimSpace(strings.TrimPrefix(line, openCodeListeningPrefix))
				once.Do(func() { ready <- url })
			}
		}
		if err := scanner.Err(); err != nil {
			streamErr <- err
		}
	}
	go handle(stdout)
	go handle(stderr)

	select {
	case baseURL := <-ready:
		return strings.TrimRight(baseURL, "/"), cmd, nil
	case err := <-streamErr:
		_ = cmd.Process.Kill()
		return "", nil, err
	case <-time.After(openCodeRPCTimeout):
		_ = cmd.Process.Kill()
		return "", nil, fmt.Errorf("timed out waiting for opencode server to start")
	}
}

func buildOpenCodePromptRequest(input Submission, agent, model, reasoning string) (openCodePromptRequest, error) {
	parts := make([]openCodePromptPart, 0, 1+len(input.Attachments))
	if text := strings.TrimSpace(input.Text); text != "" {
		parts = append(parts, openCodePromptPart{
			Type: "text",
			Text: text,
		})
	}
	for _, attachment := range input.Attachments {
		if attachment.Kind != AttachmentLocalImage {
			continue
		}
		filePart, err := openCodeFilePart(attachment.Path)
		if err != nil {
			return openCodePromptRequest{}, err
		}
		parts = append(parts, filePart)
	}
	req := openCodePromptRequest{
		Agent: strings.TrimSpace(agent),
		Parts: parts,
	}
	if providerID, modelID := splitOpenCodeModelKey(model); providerID != "" && modelID != "" {
		req.Model = &openCodePromptModel{
			ProviderID: providerID,
			ModelID:    modelID,
		}
	}
	if reasoning = strings.TrimSpace(reasoning); reasoning != "" {
		req.Variant = reasoning
	}
	return req, nil
}

func openCodeFilePart(path string) (openCodePromptPart, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return openCodePromptPart{}, err
	}
	mimeType := mime.TypeByExtension(strings.ToLower(filepath.Ext(path)))
	if mimeType == "" {
		mimeType = "application/octet-stream"
	}
	return openCodePromptPart{
		Type:     "file",
		Mime:     mimeType,
		Filename: filepath.Base(path),
		URL:      "data:" + mimeType + ";base64," + base64.StdEncoding.EncodeToString(data),
	}, nil
}

func renderOpenCodePart(role string, raw json.RawMessage) (TranscriptKind, string) {
	var header struct {
		Type string `json:"type"`
	}
	if err := json.Unmarshal(raw, &header); err != nil {
		return TranscriptOther, ""
	}
	switch strings.TrimSpace(header.Type) {
	case "text":
		var payload struct {
			Text string `json:"text"`
		}
		if err := json.Unmarshal(raw, &payload); err != nil {
			return TranscriptOther, ""
		}
		text := strings.TrimSpace(payload.Text)
		if text == "" {
			return TranscriptOther, ""
		}
		if strings.EqualFold(strings.TrimSpace(role), "user") {
			return TranscriptUser, text
		}
		return TranscriptAgent, text
	case "reasoning":
		var payload struct {
			Text string `json:"text"`
		}
		if err := json.Unmarshal(raw, &payload); err != nil {
			return TranscriptOther, ""
		}
		text := strings.TrimSpace(payload.Text)
		if text == "" {
			return TranscriptOther, ""
		}
		return TranscriptReasoning, text
	case "tool":
		var payload struct {
			Tool  string `json:"tool"`
			State struct {
				Status string `json:"status"`
				Title  string `json:"title"`
				Input  struct {
					Command     string `json:"command"`
					Description string `json:"description"`
				} `json:"input"`
			} `json:"state"`
		}
		if err := json.Unmarshal(raw, &payload); err != nil {
			return TranscriptOther, ""
		}
		toolName := strings.TrimSpace(payload.Tool)
		status := strings.TrimSpace(payload.State.Status)
		summary := summarizeOpenCodeToolDetail(
			toolName,
			payload.State.Input.Description,
			payload.State.Title,
			payload.State.Input.Command,
		)
		switch {
		case toolName != "" && status != "" && summary != "":
			return TranscriptTool, fmt.Sprintf("Tool %s %s: %s", toolName, status, summary)
		case toolName != "" && summary != "":
			return TranscriptTool, fmt.Sprintf("Tool %s: %s", toolName, summary)
		case summary != "":
			return TranscriptTool, "Tool: " + summary
		case toolName != "" && status != "":
			return TranscriptTool, fmt.Sprintf("Tool %s %s", toolName, status)
		case toolName != "":
			return TranscriptTool, "Tool " + toolName
		default:
			return TranscriptTool, "Tool activity"
		}
	case "patch":
		var payload struct {
			Files []string `json:"files"`
		}
		if err := json.Unmarshal(raw, &payload); err != nil {
			return TranscriptOther, ""
		}
		files := summarizeOpenCodePaths(payload.Files, 3)
		if len(files) == 0 {
			return TranscriptFileChange, "Patch applied"
		}
		return TranscriptFileChange, "Patch touched " + strings.Join(files, ", ")
	case "file":
		var payload struct {
			Mime     string `json:"mime"`
			Filename string `json:"filename"`
			Source   struct {
				Path string `json:"path"`
			} `json:"source"`
		}
		if err := json.Unmarshal(raw, &payload); err != nil {
			return TranscriptOther, ""
		}
		name := strings.TrimSpace(payload.Filename)
		if name == "" {
			name = strings.TrimSpace(filepath.Base(payload.Source.Path))
		}
		switch {
		case name != "" && strings.TrimSpace(payload.Mime) != "":
			return TranscriptUser, fmt.Sprintf("[attached file] %s (%s)", name, strings.TrimSpace(payload.Mime))
		case name != "":
			return TranscriptUser, "[attached file] " + name
		default:
			return TranscriptUser, "[attached file]"
		}
	case "compaction":
		return TranscriptSystem, "Conversation history compacted"
	default:
		return TranscriptOther, ""
	}
}

func summarizeOpenCodeToolDetail(toolName string, values ...string) string {
	for _, value := range values {
		text := strings.TrimSpace(value)
		if text == "" {
			continue
		}
		if looksLikeOpenCodePathSummary(text) && toolName != "bash" {
			text = strings.TrimSpace(filepath.Base(text))
		}
		if text != "" {
			return text
		}
	}
	return ""
}

func looksLikeOpenCodePathSummary(text string) bool {
	if text == "" || strings.Contains(text, " ") {
		return false
	}
	return strings.ContainsAny(text, `/\`)
}

func summarizeOpenCodePaths(paths []string, limit int) []string {
	if limit <= 0 {
		limit = len(paths)
	}
	out := make([]string, 0, min(len(paths), limit))
	seen := map[string]struct{}{}
	for _, path := range paths {
		label := strings.TrimSpace(filepath.Base(strings.TrimSpace(path)))
		if label == "" {
			label = strings.TrimSpace(path)
		}
		if label == "" {
			continue
		}
		if _, ok := seen[label]; ok {
			continue
		}
		seen[label] = struct{}{}
		out = append(out, label)
		if len(out) >= limit {
			break
		}
	}
	return out
}

func renderOpenCodeMessageError(info openCodeMessageInfo) (string, string) {
	if info.Error == nil {
		return "", ""
	}
	code := info.Error.Data.StatusCode
	message := strings.TrimSpace(info.Error.Data.Message)
	url := strings.TrimSpace(info.Error.Data.Metadata.URL)
	provider := openCodeProviderLabel(info.ProviderID)

	switch {
	case code == 403 && strings.EqualFold(strings.TrimSpace(info.ProviderID), "openai") && strings.Contains(strings.ToLower(url), "api.openai.com/v1/responses"):
		summary := "OpenCode OpenAI request rejected (HTTP 403)"
		detail := "OpenCode OpenAI request was rejected with HTTP 403 Forbidden by https://api.openai.com/v1/responses. Check `opencode auth list`, refresh OpenCode OpenAI auth or API key if needed, then use `/reconnect` in Little Control Room."
		return summary, detail
	case code > 0:
		summary := fmt.Sprintf("OpenCode %s request failed (HTTP %d)", provider, code)
		detail := summary
		if message != "" {
			detail += ": " + message
		}
		if url != "" {
			detail += " (" + url + ")"
		}
		return summary, detail
	case message != "":
		summary := "OpenCode " + provider + " error"
		return summary, summary + ": " + message
	default:
		name := strings.TrimSpace(info.Error.Name)
		if name == "" {
			name = "error"
		}
		summary := "OpenCode " + provider + " error"
		return summary, summary + ": " + name
	}
}

func openCodeProviderLabel(providerID string) string {
	switch strings.ToLower(strings.TrimSpace(providerID)) {
	case "openai":
		return "OpenAI"
	case "":
		return "provider"
	default:
		return strings.TrimSpace(providerID)
	}
}

func qualifiedOpenCodeModelKey(providerID, modelID string) string {
	providerID = strings.TrimSpace(providerID)
	modelID = strings.TrimSpace(modelID)
	if providerID == "" {
		return modelID
	}
	if modelID == "" {
		return providerID
	}
	return providerID + "/" + modelID
}

func splitOpenCodeModelKey(value string) (string, string) {
	value = strings.TrimSpace(value)
	if value == "" {
		return "", ""
	}
	if providerID, modelID, ok := strings.Cut(value, "/"); ok {
		return strings.TrimSpace(providerID), strings.TrimSpace(modelID)
	}
	return "", value
}

func openCodeReasoningOptions(model openCodeProviderModel) []ReasoningEffortOption {
	options := make([]ReasoningEffortOption, 0, len(model.Variants))
	seen := map[string]struct{}{}
	for variantID, variant := range model.Variants {
		effort := strings.TrimSpace(variant.ReasoningEffort)
		if effort == "" {
			effort = strings.TrimSpace(variantID)
		}
		if effort == "" {
			continue
		}
		if _, ok := seen[effort]; ok {
			continue
		}
		seen[effort] = struct{}{}
		description := strings.TrimSpace(variantID)
		if description == effort {
			description = ""
		}
		options = append(options, ReasoningEffortOption{
			ReasoningEffort: effort,
			Description:     description,
		})
	}
	sort.SliceStable(options, func(i, j int) bool {
		return openCodeReasoningRank(options[i].ReasoningEffort) < openCodeReasoningRank(options[j].ReasoningEffort)
	})
	return options
}

func openCodeDefaultReasoningEffort(model openCodeProviderModel) string {
	if raw, ok := model.Options["reasoningEffort"].(string); ok && strings.TrimSpace(raw) != "" {
		return strings.TrimSpace(raw)
	}
	options := openCodeReasoningOptions(model)
	if len(options) == 0 {
		return ""
	}
	return options[0].ReasoningEffort
}

func openCodeReasoningRank(value string) int {
	switch strings.ToLower(strings.TrimSpace(value)) {
	case "none":
		return 0
	case "low":
		return 1
	case "medium":
		return 2
	case "high":
		return 3
	case "xhigh":
		return 4
	default:
		return 100
	}
}

func normalizeAnswerValues(values []string) []string {
	out := make([]string, 0, len(values))
	for _, value := range values {
		if value = strings.TrimSpace(value); value != "" {
			out = append(out, value)
		}
	}
	return out
}

func mapOpenCodePermission(request openCodePermissionRequest) *ApprovalRequest {
	kind := ApprovalCommandExecution
	grantRoot := ""
	if permission := strings.ToLower(strings.TrimSpace(request.Permission)); permission == "edit" || permission == "external_directory" {
		kind = ApprovalFileChange
		if len(request.Patterns) > 0 {
			grantRoot = strings.TrimSpace(request.Patterns[0])
		}
	}
	command := ""
	if raw, ok := request.Metadata["command"].(string); ok {
		command = strings.TrimSpace(raw)
	}
	reason := strings.TrimSpace(request.Permission)
	if len(request.Patterns) > 0 {
		reason = strings.TrimSpace(reason + " " + strings.Join(request.Patterns, ", "))
	}
	return &ApprovalRequest{
		ID:        strings.TrimSpace(request.ID),
		Kind:      kind,
		ThreadID:  strings.TrimSpace(request.SessionID),
		Command:   command,
		Reason:    reason,
		GrantRoot: grantRoot,
	}
}

func mapOpenCodeQuestion(request openCodeQuestionRequest) *ToolInputRequest {
	out := &ToolInputRequest{
		ID:       strings.TrimSpace(request.ID),
		ThreadID: strings.TrimSpace(request.SessionID),
	}
	for index, question := range request.Questions {
		item := ToolInputQuestion{
			Header:   strings.TrimSpace(question.Header),
			ID:       fmt.Sprintf("question_%d", index+1),
			Question: strings.TrimSpace(question.Question),
			IsOther:  question.Custom,
		}
		for _, option := range question.Options {
			item.Options = append(item.Options, ToolInputOption{
				Label:       strings.TrimSpace(option.Label),
				Description: strings.TrimSpace(option.Description),
			})
		}
		out.Questions = append(out.Questions, item)
	}
	return out
}

func buildOpenCodeStatusText(sessionID, projectPath, currentCWD, model, modelProvider, reasoningEffort, agent string, tokenUsage *threadTokenUsage) string {
	lines := []string{"Embedded OpenCode status"}
	if sessionID != "" {
		lines = append(lines, "thread: "+sessionID)
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
	if agent != "" {
		lines = append(lines, "agent: "+agent)
	}
	if tokenUsage != nil {
		if tokenUsage.Total.TotalTokens > 0 {
			lines = append(lines, fmt.Sprintf("total tokens: %d", tokenUsage.Total.TotalTokens))
		}
		if tokenUsage.Last.TotalTokens > 0 {
			lines = append(lines, fmt.Sprintf("last turn tokens: %d", tokenUsage.Last.TotalTokens))
		}
	}
	return strings.Join(lines, "\n")
}

func formatTranscriptEntryForProvider(provider Provider, kind TranscriptKind, text string) string {
	text = strings.TrimSpace(text)
	if text == "" {
		return ""
	}
	switch kind {
	case TranscriptUser:
		return "You: " + text
	case TranscriptAgent:
		return provider.Label() + ": " + text
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
