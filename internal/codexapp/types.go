package codexapp

import (
	"encoding/json"
	"fmt"
	"strings"
	"sync"
	"time"

	"lcroom/internal/browserctl"
	"lcroom/internal/codexcli"
	"lcroom/internal/keyedmutex"
)

type TranscriptKind string

const (
	TranscriptUser       TranscriptKind = "user"
	TranscriptAgent      TranscriptKind = "agent"
	TranscriptSystem     TranscriptKind = "system"
	TranscriptStatus     TranscriptKind = "status"
	TranscriptError      TranscriptKind = "error"
	TranscriptPlan       TranscriptKind = "plan"
	TranscriptReasoning  TranscriptKind = "reasoning"
	TranscriptCommand    TranscriptKind = "command"
	TranscriptFileChange TranscriptKind = "file_change"
	TranscriptTool       TranscriptKind = "tool"
	TranscriptOther      TranscriptKind = "other"
)

type Provider string

const (
	ProviderCodex      Provider = "codex"
	ProviderOpenCode   Provider = "opencode"
	ProviderClaudeCode Provider = "claude_code"
)

func (p Provider) Normalized() Provider {
	switch strings.ToLower(strings.TrimSpace(string(p))) {
	case "", string(ProviderCodex):
		return ProviderCodex
	case string(ProviderOpenCode), "open-code":
		return ProviderOpenCode
	case string(ProviderClaudeCode), "claude-code", "claude":
		return ProviderClaudeCode
	default:
		return ""
	}
}

func (p Provider) Label() string {
	switch p.Normalized() {
	case ProviderOpenCode:
		return "OpenCode"
	case ProviderClaudeCode:
		return "Claude Code"
	default:
		return "Codex"
	}
}

func (p Provider) SourceTag() string {
	switch p.Normalized() {
	case ProviderOpenCode:
		return "OC"
	case ProviderClaudeCode:
		return "CC"
	default:
		return "CX"
	}
}

type TranscriptEntry struct {
	ItemID         string
	Kind           TranscriptKind
	Text           string
	DisplayText    string // optional; if set, used for rendering instead of Text
	GeneratedImage *GeneratedImageArtifact
}

type GeneratedImageArtifact struct {
	ID          string
	Path        string
	SourcePath  string
	Width       int
	Height      int
	ByteSize    int64
	PreviewData []byte
}

type SessionPhase string

const (
	SessionPhaseIdle        SessionPhase = "idle"
	SessionPhaseRunning     SessionPhase = "running"
	SessionPhaseFinishing   SessionPhase = "finishing"
	SessionPhaseReconciling SessionPhase = "reconciling"
	SessionPhaseStalled     SessionPhase = "stalled"
	SessionPhaseExternal    SessionPhase = "external"
	SessionPhaseClosed      SessionPhase = "closed"
)

type AttachmentKind string

const (
	AttachmentLocalImage AttachmentKind = "local_image"
)

type Attachment struct {
	Kind AttachmentKind
	Path string
}

func (a Attachment) DisplayLabel() string {
	return attachmentDisplayLabel(a.Path)
}

func attachmentDisplayLabel(path string) string {
	path = strings.TrimSpace(path)
	if path == "" {
		return "attachment"
	}
	parts := strings.Split(path, "/")
	if len(parts) == 0 {
		return path
	}
	base := parts[len(parts)-1]
	if base == "" {
		return "attachment"
	}
	if strings.HasPrefix(base, "lcroom-codex-clipboard-") {
		return "clipboard image"
	}
	return base
}

type Submission struct {
	Text        string
	DisplayText string // optional; if set, used for transcript display instead of Text
	Attachments []Attachment
}

// TranscriptDisplayText returns the display-friendly transcript text,
// using DisplayText (collapsed paste placeholders) when available.
func (s Submission) TranscriptDisplayText() string {
	parts := []string{}
	text := strings.TrimSpace(s.DisplayText)
	if text == "" {
		text = strings.TrimSpace(s.Text)
	}
	if text != "" {
		parts = append(parts, text)
	}
	for _, attachment := range s.Attachments {
		switch attachment.Kind {
		case AttachmentLocalImage:
			parts = append(parts, "[attached image] "+attachment.DisplayLabel())
		default:
			parts = append(parts, "[attachment] "+attachment.DisplayLabel())
		}
	}
	return strings.Join(parts, "\n")
}

func (s Submission) Empty() bool {
	if strings.TrimSpace(s.Text) != "" {
		return false
	}
	for _, attachment := range s.Attachments {
		if strings.TrimSpace(attachment.Path) != "" {
			return false
		}
	}
	return true
}

func (s Submission) TranscriptText() string {
	parts := []string{}
	if text := strings.TrimSpace(s.Text); text != "" {
		parts = append(parts, text)
	}
	for _, attachment := range s.Attachments {
		switch attachment.Kind {
		case AttachmentLocalImage:
			parts = append(parts, "[attached image] "+attachment.DisplayLabel())
		default:
			parts = append(parts, "[attachment] "+attachment.DisplayLabel())
		}
	}
	return strings.Join(parts, "\n")
}

type ApprovalKind string

const (
	ApprovalCommandExecution ApprovalKind = "command"
	ApprovalFileChange       ApprovalKind = "file_change"
)

type ApprovalDecision string

const (
	DecisionAccept           ApprovalDecision = "accept"
	DecisionAcceptForSession ApprovalDecision = "acceptForSession"
	DecisionDecline          ApprovalDecision = "decline"
	DecisionCancel           ApprovalDecision = "cancel"
)

type ApprovalRequest struct {
	ID        string
	Kind      ApprovalKind
	ThreadID  string
	TurnID    string
	ItemID    string
	Command   string
	CWD       string
	Reason    string
	GrantRoot string
}

func (r ApprovalRequest) AllowsDecision(decision ApprovalDecision) bool {
	switch decision {
	case DecisionAcceptForSession:
		return r.Kind == ApprovalCommandExecution
	default:
		return true
	}
}

func (r ApprovalRequest) Summary() string {
	switch r.Kind {
	case ApprovalFileChange:
		if r.GrantRoot != "" {
			return "File changes want write access under " + r.GrantRoot
		}
		if r.Reason != "" {
			return "File changes need approval: " + r.Reason
		}
		return "File changes need approval"
	default:
		parts := []string{}
		if r.Command != "" {
			parts = append(parts, r.Command)
		}
		if r.CWD != "" {
			parts = append(parts, "in "+r.CWD)
		}
		if r.Reason != "" {
			parts = append(parts, "("+r.Reason+")")
		}
		if len(parts) == 0 {
			return "Command execution needs approval"
		}
		return "Command approval: " + strings.Join(parts, " ")
	}
}

type ToolInputOption struct {
	Label       string
	Description string
}

type ToolInputQuestion struct {
	Header   string
	ID       string
	Question string
	IsOther  bool
	IsSecret bool
	Options  []ToolInputOption
}

type ToolInputRequest struct {
	ID        string
	ThreadID  string
	TurnID    string
	ItemID    string
	Questions []ToolInputQuestion
}

func (r ToolInputRequest) Summary() string {
	if len(r.Questions) == 0 {
		return "Codex requested structured user input"
	}
	if len(r.Questions) == 1 {
		question := strings.TrimSpace(r.Questions[0].Question)
		if question != "" {
			return question
		}
	}
	return fmt.Sprintf("Codex requested answers for %d question(s)", len(r.Questions))
}

type ElicitationMode string

const (
	ElicitationModeForm ElicitationMode = "form"
	ElicitationModeURL  ElicitationMode = "url"
)

type ElicitationDecision string

const (
	ElicitationAccept  ElicitationDecision = "accept"
	ElicitationDecline ElicitationDecision = "decline"
	ElicitationCancel  ElicitationDecision = "cancel"
)

type ElicitationRequest struct {
	ID              string
	ServerName      string
	ThreadID        string
	TurnID          string
	Mode            ElicitationMode
	Message         string
	URL             string
	ElicitationID   string
	RequestedSchema json.RawMessage
}

func (r ElicitationRequest) Summary() string {
	message := strings.TrimSpace(r.Message)
	if message != "" {
		return message
	}
	if strings.TrimSpace(r.ServerName) != "" {
		return "MCP server " + r.ServerName + " requested input"
	}
	return "MCP server requested input"
}

type TokenUsageSnapshot struct {
	Last               TokenUsageBreakdown
	Total              TokenUsageBreakdown
	ModelContextWindow int64
}

type TokenUsageBreakdown struct {
	CachedInputTokens     int64
	InputTokens           int64
	OutputTokens          int64
	ReasoningOutputTokens int64
	TotalTokens           int64
}

func (b TokenUsageBreakdown) EstimatedVisibleOutputTokens() int64 {
	output := b.OutputTokens
	if output < 0 {
		output = 0
	}
	reasoning := b.ReasoningOutputTokens
	if reasoning <= 0 {
		return output
	}
	visible := output - reasoning
	if visible < 0 {
		return 0
	}
	return visible
}

func (b TokenUsageBreakdown) EstimatedContextTokens() int64 {
	input := b.InputTokens
	if input < 0 {
		input = 0
	}
	return input + b.EstimatedVisibleOutputTokens()
}

func (u *TokenUsageSnapshot) EstimatedContextTokens() int64 {
	if u == nil {
		return 0
	}
	if used := u.Last.EstimatedContextTokens(); used > 0 {
		return used
	}
	if used := u.Total.EstimatedContextTokens(); used > 0 {
		return used
	}
	if u.Last.TotalTokens > 0 {
		return u.Last.TotalTokens
	}
	if u.Total.TotalTokens > 0 {
		return u.Total.TotalTokens
	}
	return 0
}

func (u *TokenUsageSnapshot) ContextLeftTokens() int64 {
	if u == nil || u.ModelContextWindow <= 0 {
		return 0
	}
	left := u.ModelContextWindow - u.EstimatedContextTokens()
	if left < 0 {
		return 0
	}
	return left
}

func (u *TokenUsageSnapshot) ContextLeftPercent() int {
	if u == nil || u.ModelContextWindow <= 0 {
		return 0
	}
	used := u.EstimatedContextTokens()
	if used < 0 {
		used = 0
	}
	percent := 100 - int(float64(used)*100/float64(u.ModelContextWindow))
	switch {
	case percent < 0:
		return 0
	case percent > 100:
		return 100
	default:
		return percent
	}
}

type UsageWindowSnapshot struct {
	Limit       string
	Plan        string
	Window      string
	LeftPercent int
	ResetsAt    time.Time
}

type ReasoningEffortOption struct {
	ReasoningEffort string
	Description     string
}

type ModelOption struct {
	ID                        string
	Model                     string
	DisplayName               string
	Description               string
	Hidden                    bool
	SupportedReasoningEfforts []ReasoningEffortOption
	DefaultReasoningEffort    string
	SupportsPersonality       bool
	IsDefault                 bool
}

type Snapshot struct {
	Provider                 Provider
	ProjectPath              string
	ThreadID                 string
	Preset                   codexcli.Preset
	BrowserActivity          browserctl.SessionActivity
	ManagedBrowserSessionKey string
	CurrentBrowserPageURL    string
	TranscriptRevision       uint64
	Phase                    SessionPhase
	Started                  bool
	Busy                     bool
	BusyExternal             bool
	BusySince                time.Time
	LastBusyActivityAt       time.Time
	Closed                   bool
	ActiveTurnID             string
	PendingApproval          *ApprovalRequest
	PendingToolInput         *ToolInputRequest
	PendingElicitation       *ElicitationRequest
	Entries                  []TranscriptEntry
	Transcript               string
	Status                   string
	LastError                string
	LastSystemNotice         string
	LastActivityAt           time.Time
	CurrentCWD               string
	Model                    string
	ModelProvider            string
	ReasoningEffort          string
	ServiceTier              string
	PendingModel             string
	PendingReasoning         string
	TokenUsage               *TokenUsageSnapshot
	UsageWindows             []UsageWindowSnapshot
}

type Session interface {
	ProjectPath() string
	Snapshot() Snapshot
	// TrySnapshot returns the current snapshot without blocking. If the
	// session's internal lock is contended it returns (Snapshot{}, false)
	// immediately so the caller can fall back to a cached value instead of
	// freezing the event loop.
	TrySnapshot() (Snapshot, bool)
	Submit(prompt string) error
	SubmitInput(input Submission) error
	ShowStatus() error
	Compact() error
	Review() error
	ListModels() ([]ModelOption, error)
	StageModelOverride(model, reasoningEffort string) error
	Interrupt() error
	RespondApproval(decision ApprovalDecision) error
	RespondToolInput(answers map[string][]string) error
	RespondElicitation(decision ElicitationDecision, content json.RawMessage) error
	Close() error
}

type LaunchRequest struct {
	Provider                 Provider
	ProjectPath              string
	ResumeID                 string
	ForceNew                 bool
	Prompt                   string
	Preset                   codexcli.Preset
	PendingModel             string
	PendingReasoning         string
	PlaywrightPolicy         browserctl.Policy
	ManagedBrowserSessionKey string
	AppDataDir               string
	OpenCodeDataHome         string
	CodexHome                string
	CLIExecutablePath        string
}

func (r LaunchRequest) Validate() error {
	if strings.TrimSpace(r.ProjectPath) == "" {
		return fmt.Errorf("project path required")
	}
	if err := r.PlaywrightPolicy.Validate(); err != nil {
		return err
	}
	preset := r.Preset
	if preset == "" {
		preset = codexcli.DefaultPreset()
	}
	switch r.Provider.Normalized() {
	case ProviderCodex, ProviderOpenCode:
		if _, err := codexcli.ParsePreset(string(preset)); err != nil {
			return err
		}
	case ProviderClaudeCode:
		// Claude Code sessions are read-only; no preset validation needed.
	default:
		return fmt.Errorf("embedded provider must be one of: codex, opencode, claude_code")
	}
	return nil
}

type sessionFactory func(req LaunchRequest, notify func()) (Session, error)

type idleClosable interface {
	CloseDueToInactivity() error
}

type busyElsewhereRefresher interface {
	RefreshBusyElsewhere() error
}

type closeWaiter interface {
	WaitClosed(timeout time.Duration) bool
}

type busyReconciler interface {
	ReconcileBusyState() error
}

type Manager struct {
	mu              sync.Mutex
	sessions        map[string]Session
	updates         chan string
	pendingUpdates  map[string]struct{}
	deferredUpdates map[string]struct{}
	opLocks         keyedmutex.Locker
	factory         sessionFactory

	idleTimeout  time.Duration
	reapInterval time.Duration

	busyReconcileAfter time.Duration
}

func NewManager() *Manager {
	return NewManagerWithFactory(newEmbeddedSession)
}

func NewManagerWithFactory(factory func(req LaunchRequest, notify func()) (Session, error)) *Manager {
	if factory == nil {
		factory = newEmbeddedSession
	}
	manager := &Manager{
		sessions:           make(map[string]Session),
		updates:            make(chan string, 256),
		pendingUpdates:     make(map[string]struct{}),
		deferredUpdates:    make(map[string]struct{}),
		factory:            factory,
		idleTimeout:        idleShutdownAfter,
		reapInterval:       time.Minute,
		busyReconcileAfter: busyStateReconcileAfter,
	}
	go manager.reapLoop()
	return manager
}

func (m *Manager) Updates() <-chan string {
	if m == nil {
		return nil
	}
	return m.updates
}

func (m *Manager) Session(projectPath string) (Session, bool) {
	if m == nil {
		return nil, false
	}
	projectPath = strings.TrimSpace(projectPath)
	m.mu.Lock()
	defer m.mu.Unlock()
	session, ok := m.sessions[projectPath]
	return session, ok
}

func (m *Manager) Snapshots() []Snapshot {
	if m == nil {
		return nil
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	snapshots := make([]Snapshot, 0, len(m.sessions))
	for _, session := range m.sessions {
		snapshots = append(snapshots, session.Snapshot())
	}
	return snapshots
}

func (m *Manager) Open(req LaunchRequest) (Session, bool, error) {
	if m == nil {
		return nil, false, fmt.Errorf("manager required")
	}
	if req.Preset == "" {
		req.Preset = codexcli.DefaultPreset()
	}
	if err := req.Validate(); err != nil {
		return nil, false, err
	}

	projectPath := strings.TrimSpace(req.ProjectPath)
	ensureManagedPlaywrightSessionKey(&req)
	unlock := m.opLocks.Lock(projectPath)
	defer unlock()

	m.mu.Lock()
	existing, ok := m.sessions[projectPath]
	replaceExisting := false
	if ok && existing.Snapshot().Closed {
		delete(m.sessions, projectPath)
		existing = nil
		ok = false
	}
	if ok {
		existingProvider := existing.Snapshot().Provider.Normalized()
		if existingProvider == "" {
			existingProvider = ProviderCodex
		}
		requestedProvider := req.Provider.Normalized()
		if requestedProvider == "" {
			requestedProvider = ProviderCodex
		}
		if existingProvider != requestedProvider {
			replaceExisting = true
			ok = false
		}
	}
	if ok && !req.ForceNew {
		requestedResumeID := strings.TrimSpace(req.ResumeID)
		if requestedResumeID != "" {
			existingThreadID := strings.TrimSpace(existing.Snapshot().ThreadID)
			if existingThreadID == "" || existingThreadID != requestedResumeID {
				replaceExisting = true
				ok = false
			}
		}
	}
	if ok && !req.ForceNew {
		m.mu.Unlock()
		if existing.Snapshot().BusyExternal {
			if refresher, ok := existing.(busyElsewhereRefresher); ok {
				_ = refresher.RefreshBusyElsewhere()
			}
		}
		if strings.TrimSpace(req.PendingModel) != "" || strings.TrimSpace(req.PendingReasoning) != "" {
			if err := existing.StageModelOverride(req.PendingModel, req.PendingReasoning); err != nil {
				return nil, true, err
			}
		}
		if strings.TrimSpace(req.Prompt) != "" {
			if err := existing.Submit(req.Prompt); err != nil {
				return nil, true, err
			}
		}
		return existing, true, nil
	}
	if ok {
		replaceExisting = true
	}
	m.mu.Unlock()

	if replaceExisting {
		_ = existing.Close()
		if waiter, ok := existing.(closeWaiter); ok {
			waiter.WaitClosed(5 * time.Second)
		}
	}

	session, err := m.factory(req, func() {
		m.notify(projectPath)
	})
	if err != nil {
		return nil, false, err
	}

	m.mu.Lock()
	m.sessions[projectPath] = session
	m.mu.Unlock()
	m.notify(projectPath)
	return session, false, nil
}

func (m *Manager) CloseProject(projectPath string) error {
	if m == nil {
		return nil
	}
	projectPath = strings.TrimSpace(projectPath)
	if projectPath == "" {
		return nil
	}
	unlock := m.opLocks.Lock(projectPath)
	defer unlock()

	m.mu.Lock()
	session, ok := m.sessions[projectPath]
	if ok {
		delete(m.sessions, projectPath)
	}
	m.mu.Unlock()
	if !ok {
		return nil
	}
	return session.Close()
}

func (m *Manager) CloseAll() error {
	if m == nil {
		return nil
	}
	m.mu.Lock()
	sessions := make([]Session, 0, len(m.sessions))
	for _, session := range m.sessions {
		sessions = append(sessions, session)
	}
	m.sessions = make(map[string]Session)
	m.mu.Unlock()

	var firstErr error
	for _, session := range sessions {
		if err := session.Close(); err != nil && firstErr == nil {
			firstErr = err
		}
	}
	return firstErr
}

func (m *Manager) notify(projectPath string) {
	if m == nil {
		return
	}
	projectPath = strings.TrimSpace(projectPath)
	if projectPath == "" {
		return
	}
	m.mu.Lock()
	if _, ok := m.pendingUpdates[projectPath]; ok {
		m.deferredUpdates[projectPath] = struct{}{}
		m.mu.Unlock()
		return
	}
	delete(m.deferredUpdates, projectPath)
	m.pendingUpdates[projectPath] = struct{}{}
	m.mu.Unlock()
	if !m.enqueueUpdate(projectPath) {
		m.mu.Lock()
		delete(m.pendingUpdates, projectPath)
		m.deferredUpdates[projectPath] = struct{}{}
		m.mu.Unlock()
	}
}

func (m *Manager) AckUpdate(projectPath string) {
	if m == nil {
		return
	}
	projectPath = strings.TrimSpace(projectPath)
	if projectPath == "" {
		return
	}
	m.mu.Lock()
	delete(m.pendingUpdates, projectPath)
	_, replay := m.deferredUpdates[projectPath]
	if replay {
		delete(m.deferredUpdates, projectPath)
		m.pendingUpdates[projectPath] = struct{}{}
	}
	m.mu.Unlock()
	if !replay {
		return
	}
	if !m.enqueueUpdate(projectPath) {
		m.mu.Lock()
		delete(m.pendingUpdates, projectPath)
		m.deferredUpdates[projectPath] = struct{}{}
		m.mu.Unlock()
	}
}

func (m *Manager) enqueueUpdate(projectPath string) bool {
	select {
	case m.updates <- projectPath:
		return true
	default:
		return false
	}
}

func (m *Manager) reapLoop() {
	if m == nil || m.reapInterval <= 0 {
		return
	}
	ticker := time.NewTicker(m.reapInterval)
	defer ticker.Stop()
	for range ticker.C {
		m.reconcileBusySessions(time.Now())
		m.reapIdleSessions(time.Now())
	}
}

func (m *Manager) reconcileBusySessions(now time.Time) {
	if m == nil || m.busyReconcileAfter <= 0 {
		return
	}

	m.mu.Lock()
	sessions := make(map[string]Session, len(m.sessions))
	for projectPath, session := range m.sessions {
		sessions[projectPath] = session
	}
	m.mu.Unlock()

	for _, session := range sessions {
		reconciler, ok := session.(busyReconciler)
		if !ok {
			continue
		}
		snapshot := session.Snapshot()
		if snapshot.Closed || snapshot.BusyExternal || !snapshot.Busy {
			continue
		}
		if snapshot.PendingApproval != nil || snapshot.PendingToolInput != nil || snapshot.PendingElicitation != nil {
			continue
		}
		lastBusyActivityAt := snapshot.LastActivityAt
		if !snapshot.LastBusyActivityAt.IsZero() {
			lastBusyActivityAt = snapshot.LastBusyActivityAt
		}
		if lastBusyActivityAt.IsZero() {
			continue
		}
		if now.Sub(lastBusyActivityAt) < m.busyReconcileAfter {
			continue
		}
		_ = reconciler.ReconcileBusyState()
	}
}

func (m *Manager) reapIdleSessions(now time.Time) {
	if m == nil || m.idleTimeout <= 0 {
		return
	}

	m.mu.Lock()
	sessions := make(map[string]Session, len(m.sessions))
	for projectPath, session := range m.sessions {
		sessions[projectPath] = session
	}
	m.mu.Unlock()

	for _, session := range sessions {
		snapshot := session.Snapshot()
		if snapshot.Closed || snapshot.Busy || snapshot.PendingApproval != nil {
			continue
		}
		if snapshot.LastActivityAt.IsZero() {
			continue
		}
		if now.Sub(snapshot.LastActivityAt) < m.idleTimeout {
			continue
		}
		if idleSession, ok := session.(idleClosable); ok {
			_ = idleSession.CloseDueToInactivity()
			continue
		}
		_ = session.Close()
	}
}
