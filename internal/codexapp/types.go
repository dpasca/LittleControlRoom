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
	"lcroom/internal/projectrun"
	"lcroom/internal/todocapture"
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
	ProviderLCAgent    Provider = "lcagent"
)

func (p Provider) Normalized() Provider {
	switch strings.ToLower(strings.TrimSpace(string(p))) {
	case "", string(ProviderCodex):
		return ProviderCodex
	case string(ProviderOpenCode), "open-code":
		return ProviderOpenCode
	case string(ProviderClaudeCode), "claude-code", "claude":
		return ProviderClaudeCode
	case string(ProviderLCAgent), "lc-agent", "lc_agent":
		return ProviderLCAgent
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
	case ProviderLCAgent:
		return "LCAgent"
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
	case ProviderLCAgent:
		return "LA"
	default:
		return "CX"
	}
}

type TranscriptEntry struct {
	ItemID         string
	TurnID         string
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
	Scope     string
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
		if r.Scope != "" {
			parts = append(parts, "scope "+r.Scope)
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
	Limit            string
	Plan             string
	Window           string
	LeftPercent      int
	ResetsAt         time.Time
	CreditBalance    string
	HasCredits       bool
	CreditsUnlimited bool
}

type ThreadGoalStatus string

const (
	ThreadGoalStatusActive        ThreadGoalStatus = "active"
	ThreadGoalStatusPaused        ThreadGoalStatus = "paused"
	ThreadGoalStatusBlocked       ThreadGoalStatus = "blocked"
	ThreadGoalStatusBudgetLimited ThreadGoalStatus = "budgetLimited"
	ThreadGoalStatusComplete      ThreadGoalStatus = "complete"
)

type ThreadGoal struct {
	ThreadID        string
	Objective       string
	Status          ThreadGoalStatus
	TokenBudget     *int64
	TokensUsed      int64
	TimeUsedSeconds int64
	CreatedAt       time.Time
	UpdatedAt       time.Time
}

type ReasoningEffortOption struct {
	ReasoningEffort string
	Description     string
}

type ModelOption struct {
	ID                        string
	Model                     string
	ModelProvider             string
	DisplayName               string
	Description               string
	Hidden                    bool
	SupportedReasoningEfforts []ReasoningEffortOption
	DefaultReasoningEffort    string
	SupportsPersonality       bool
	IsDefault                 bool
}

type QualityPlanPhaseSnapshot struct {
	Name          string
	Status        string
	EvidenceCount int
	Notes         string
}

type MCPToolUsageSnapshot struct {
	Name  string
	Calls int
}

type MCPUsageSnapshot struct {
	ServerName string
	ToolCalls  int
	LastTool   string
	Tools      []MCPToolUsageSnapshot
}

type Snapshot struct {
	Provider                    Provider
	ProjectPath                 string
	ThreadID                    string
	Preset                      codexcli.Preset
	PermissionLevel             string
	BrowserActivity             browserctl.SessionActivity
	ManagedBrowserSessionKey    string
	CurrentBrowserPageURL       string
	CurrentBrowserPageStale     bool
	TranscriptRevision          uint64
	Phase                       SessionPhase
	Started                     bool
	Busy                        bool
	BusyExternal                bool
	BusySince                   time.Time
	LastBusyActivityAt          time.Time
	Closed                      bool
	ActiveTurnID                string
	HistoryHasMore              bool
	HistoryLoading              bool
	HistoryLoadError            string
	PendingApproval             *ApprovalRequest
	PendingToolInput            *ToolInputRequest
	PendingElicitation          *ElicitationRequest
	ActivityPreview             []TranscriptEntry // Bounded text-only tail for lightweight state snapshots.
	Entries                     []TranscriptEntry
	Transcript                  string
	Status                      string
	LastError                   string
	LastSystemNotice            string
	SuggestedInputDraftID       string
	SuggestedInputDraft         string
	SuggestedInputDraftSource   string
	LastActivityAt              time.Time
	CurrentCWD                  string
	Model                       string
	ModelProvider               string
	VisionModel                 string
	VisionModelProvider         string
	ImageAnalysisActive         bool
	ImageAnalyses               int
	ImageAnalysisFailures       int
	ImageAnalysisLastSummary    string
	QualityPlanUpdates          int
	QualityPlanPhases           int
	QualityPlanVerified         int
	QualityPlanSkipped          int
	QualityPlanNeedsRepair      int
	QualityPlanRequiresRuntime  bool
	QualityPlanRequiresVisual   bool
	QualityPlanRequiresTemporal bool
	QualityPlanLastSummary      string
	QualityPlanPhaseItems       []QualityPlanPhaseSnapshot
	ReasoningEffort             string
	ServiceTier                 string
	PendingModel                string
	PendingReasoning            string
	MCPUsage                    []MCPUsageSnapshot
	TokenUsage                  *TokenUsageSnapshot
	UsageWindows                []UsageWindowSnapshot
	Goal                        *ThreadGoal
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
	ShowGoal() error
	SetGoal(objective string, tokenBudget *int64) error
	PauseGoal() error
	ResumeGoal() error
	ClearGoal() error
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
	Provider    Provider
	ProjectPath string
	ResumeID    string

	// ContinueInterruptedTurn is set only for a turn captured by LCR's
	// graceful-shutdown journal. Reopening a provider session restores context;
	// this flag authorizes starting a new turn that continues the interrupted
	// work after the provider helper has restarted.
	ContinueInterruptedTurn bool
	InterruptedTurnID       string

	// ReconnectTranscript carries the richer live transcript across an explicit
	// helper restart. Codex resume responses can omit tool activity from an
	// interrupted turn, so the replacement helper merges this history back in.
	ReconnectTranscript []TranscriptEntry

	ForceNew                   bool
	Prompt                     string
	InitialInput               Submission
	Preset                     codexcli.Preset
	PendingModel               string
	PendingReasoning           string
	PlaywrightPolicy           browserctl.Policy
	ManagedBrowserSessionKey   string
	AppDataDir                 string
	AppDBPath                  string
	TodoCaptureMode            todocapture.CaptureMode
	TodoCaptureHandler         todocapture.Handler
	TodoCaptureSessionKey      string
	OpenCodeDataHome           string
	CodexHome                  string
	LCAgentPath                string
	LCAgentEnvFile             string
	LCAgentOpenAIAPIKey        string
	LCAgentOpenRouterAPIKey    string
	LCAgentDeepSeekAPIKey      string
	LCAgentMoonshotAPIKey      string
	LCAgentXiaomiAPIKey        string
	LCAgentXiaomiBaseURL       string
	LCAgentOllamaAPIKey        string
	LCAgentOllamaBaseURL       string
	LCAgentOllamaModel         string
	LCAgentProviderAccessCheck bool
	LCAgentRoutePreset         string
	LCAgentProvider            string
	LCAgentAuto                string
	LCAgentAdminWrite          bool
	LCAgentToolProfile         string
	LCAgentContextProfile      string
	LCAgentRequestTimeout      time.Duration
	LCAgentUtilityProvider     string
	LCAgentUtilityModel        string
	LCAgentVisionProvider      string
	LCAgentVisionModel         string
	LCAgentWebSearchBackend    string
	LCAgentWebSearchAPIKey     string
	LCAgentWebSearchEngineID   string
	LCAgentWebSearchURL        string
	RuntimeManager             *projectrun.Manager
	CLIExecutablePath          string
	WorkspaceContract          WorkspaceContract
	WorkspaceExcursionHandler  WorkspaceExcursionHandler
}

type WorkspaceContract struct {
	AssignedPath       string
	RepositoryRootPath string
	ExpectedRootBranch string
}

type WorkspaceExcursion struct {
	At                 time.Time
	ProjectPath        string
	RepositoryRootPath string
	ExpectedRootBranch string
	Provider           Provider
	SessionID          string
	ItemID             string
	Command            string
	CWD                string
}

type WorkspaceExcursionHandler func(WorkspaceExcursion)

func (r LaunchRequest) Validate() error {
	if strings.TrimSpace(r.ProjectPath) == "" {
		return fmt.Errorf("project path required")
	}
	if r.ContinueInterruptedTurn {
		if r.ForceNew {
			return fmt.Errorf("interrupted turn continuation cannot force a new session")
		}
		if strings.TrimSpace(r.ResumeID) == "" {
			return fmt.Errorf("interrupted turn continuation requires a session id")
		}
		if launchRequestInitialInput(r).Empty() {
			return fmt.Errorf("interrupted turn continuation requires a prompt")
		}
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
	case ProviderClaudeCode, ProviderLCAgent:
		// These providers do not use Codex launch presets.
	default:
		return fmt.Errorf("embedded provider must be one of: codex, opencode, claude_code, lcagent")
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

type stateSnapshooter interface {
	StateSnapshot() Snapshot
}

func sessionStateSnapshot(session Session) Snapshot {
	if session == nil {
		return Snapshot{}
	}
	if state, ok := session.(stateSnapshooter); ok {
		return state.StateSnapshot()
	}
	return session.Snapshot()
}

type Manager struct {
	mu                      sync.Mutex
	shutdownMu              sync.Mutex
	sessions                map[string]Session
	parallelSessions        map[string]Session
	updates                 chan string
	parallelUpdates         chan string
	pendingUpdates          map[string]struct{}
	deferredUpdates         map[string]struct{}
	pendingParallelUpdates  map[string]struct{}
	deferredParallelUpdates map[string]struct{}
	idleProtected           map[string]struct{}
	opLocks                 keyedmutex.Locker
	factory                 sessionFactory

	idleTimeout  time.Duration
	reapInterval time.Duration

	busyReconcileAfter time.Duration
	restartStateSaved  bool
}

func NewManager() *Manager {
	return NewManagerWithFactory(newEmbeddedSession)
}

func NewManagerWithFactory(factory func(req LaunchRequest, notify func()) (Session, error)) *Manager {
	if factory == nil {
		factory = newEmbeddedSession
	}
	manager := &Manager{
		sessions:                make(map[string]Session),
		parallelSessions:        make(map[string]Session),
		updates:                 make(chan string, 256),
		parallelUpdates:         make(chan string, 64),
		pendingUpdates:          make(map[string]struct{}),
		deferredUpdates:         make(map[string]struct{}),
		pendingParallelUpdates:  make(map[string]struct{}),
		deferredParallelUpdates: make(map[string]struct{}),
		idleProtected:           make(map[string]struct{}),
		factory:                 factory,
		idleTimeout:             idleShutdownAfter,
		reapInterval:            time.Minute,
		busyReconcileAfter:      busyStateReconcileAfter,
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

func (m *Manager) ParallelUpdates() <-chan string {
	if m == nil {
		return nil
	}
	return m.parallelUpdates
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

// TrySessionSnapshot returns a live project snapshot without waiting on a
// contended session lock. Read-only observers can use recorded state instead.
func (m *Manager) TrySessionSnapshot(projectPath string) (Snapshot, bool) {
	session, ok := m.Session(projectPath)
	if !ok || session == nil {
		return Snapshot{}, false
	}
	return session.TrySnapshot()
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

// ParallelSession returns the background session for a project, if one is
// currently managed. Parallel sessions are deliberately separate from
// Session: they must not replace the interactive session shown by the TUI.
func (m *Manager) ParallelSession(projectPath string) (Session, bool) {
	if m == nil {
		return nil, false
	}
	projectPath = strings.TrimSpace(projectPath)
	m.mu.Lock()
	defer m.mu.Unlock()
	session, ok := m.parallelSessions[projectPath]
	return session, ok
}

// ParallelSnapshots returns background-session state for diagnostics and
// shutdown coordination without mixing it into the interactive project
// snapshot registry.
func (m *Manager) ParallelSnapshots() []Snapshot {
	if m == nil {
		return nil
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	snapshots := make([]Snapshot, 0, len(m.parallelSessions))
	for _, session := range m.parallelSessions {
		snapshots = append(snapshots, sessionStateSnapshot(session))
	}
	return snapshots
}

func (m *Manager) SetIdleProtectedProject(projectPath string) {
	if m == nil {
		return
	}
	projectPath = strings.TrimSpace(projectPath)
	m.mu.Lock()
	defer m.mu.Unlock()
	clear(m.idleProtected)
	if projectPath != "" {
		if m.idleProtected == nil {
			m.idleProtected = make(map[string]struct{})
		}
		m.idleProtected[projectPath] = struct{}{}
	}
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
	ensureTodoCaptureSessionKey(&req)
	unlock := m.opLocks.Lock(projectPath)
	defer unlock()

	m.mu.Lock()
	existing, ok := m.sessions[projectPath]
	replaceExisting := false
	existingState := Snapshot{}
	if ok {
		existingState = sessionStateSnapshot(existing)
	}
	if ok && existingState.Closed {
		delete(m.sessions, projectPath)
		existing = nil
		ok = false
	}
	if ok {
		existingProvider := existingState.Provider.Normalized()
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
			existingThreadID := strings.TrimSpace(existingState.ThreadID)
			if existingThreadID == "" || existingThreadID != requestedResumeID {
				replaceExisting = true
				ok = false
			}
		}
	}
	if ok && !req.ForceNew {
		m.mu.Unlock()
		if updater, ok := existing.(workspaceContractUpdater); ok {
			updater.SetWorkspaceContract(req.WorkspaceContract, req.WorkspaceExcursionHandler)
		}
		if existingState.BusyExternal {
			if refresher, ok := existing.(busyElsewhereRefresher); ok {
				_ = refresher.RefreshBusyElsewhere()
			}
		}
		if strings.TrimSpace(req.PendingModel) != "" || strings.TrimSpace(req.PendingReasoning) != "" {
			if err := existing.StageModelOverride(req.PendingModel, req.PendingReasoning); err != nil {
				return nil, true, err
			}
		}
		if initialInput := launchRequestInitialInput(req); !initialInput.Empty() {
			if err := existing.SubmitInput(initialInput); err != nil {
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

// OpenParallel opens a background session for the project without replacing
// its interactive session. The request may start fresh work or resume a
// journaled background turn. At most one active parallel session is kept per
// project: a repeated request reuses active work, while an idle or closed
// background session is replaced.
func (m *Manager) OpenParallel(req LaunchRequest) (Session, bool, error) {
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
	ensureTodoCaptureSessionKey(&req)
	unlock := m.opLocks.Lock("parallel\x00" + projectPath)
	defer unlock()

	m.mu.Lock()
	existing, ok := m.parallelSessions[projectPath]
	if ok {
		state := sessionStateSnapshot(existing)
		if parallelSessionActive(state) {
			m.mu.Unlock()
			m.notifyParallel(projectPath)
			return existing, true, nil
		}
		delete(m.parallelSessions, projectPath)
	}
	m.mu.Unlock()

	if ok {
		_ = existing.Close()
		if waiter, waitable := existing.(closeWaiter); waitable {
			waiter.WaitClosed(5 * time.Second)
		}
	}

	// Parallel sessions use a separate update stream so their progress cannot
	// overwrite the foreground project's interactive snapshot cache.
	session, err := m.factory(req, func() {
		m.notifyParallel(projectPath)
	})
	if err != nil {
		return nil, false, err
	}

	m.mu.Lock()
	m.parallelSessions[projectPath] = session
	m.mu.Unlock()
	m.notifyParallel(projectPath)
	return session, false, nil
}

func parallelSessionActive(snapshot Snapshot) bool {
	if snapshot.Closed {
		return false
	}
	if snapshot.Busy || snapshot.BusyExternal || strings.TrimSpace(snapshot.ActiveTurnID) != "" {
		return true
	}
	if snapshot.PendingApproval != nil || snapshot.PendingToolInput != nil || snapshot.PendingElicitation != nil {
		return true
	}
	switch snapshot.Phase {
	case SessionPhaseRunning, SessionPhaseFinishing, SessionPhaseReconciling, SessionPhaseStalled, SessionPhaseExternal:
		return true
	default:
		return false
	}
}

func (m *Manager) CloseParallelProject(projectPath string) error {
	if m == nil {
		return nil
	}
	projectPath = strings.TrimSpace(projectPath)
	if projectPath == "" {
		return nil
	}
	unlock := m.opLocks.Lock("parallel\x00" + projectPath)
	defer unlock()

	m.mu.Lock()
	session, ok := m.parallelSessions[projectPath]
	if ok {
		delete(m.parallelSessions, projectPath)
	}
	m.mu.Unlock()
	if !ok {
		return nil
	}
	return session.Close()
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
		delete(m.idleProtected, projectPath)
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
	sessions := make([]Session, 0, len(m.sessions)+len(m.parallelSessions))
	for _, session := range m.sessions {
		sessions = append(sessions, session)
	}
	for _, session := range m.parallelSessions {
		sessions = append(sessions, session)
	}
	m.sessions = make(map[string]Session)
	m.parallelSessions = make(map[string]Session)
	clear(m.idleProtected)
	m.mu.Unlock()

	var firstErr error
	for _, session := range sessions {
		if err := session.Close(); err != nil && firstErr == nil {
			firstErr = err
		}
	}
	return firstErr
}

// CloseAllForRestart durably captures locally-owned in-flight turns before it
// interrupts and closes provider helpers. Repeated calls are safe and do not
// erase intents written by the first shutdown pass.
func (m *Manager) CloseAllForRestart(dataDir string) ([]RestartIntent, error) {
	if m == nil {
		return nil, nil
	}
	m.shutdownMu.Lock()
	defer m.shutdownMu.Unlock()

	if m.restartStateSaved {
		return nil, m.CloseAll()
	}

	type managedSession struct {
		session  Session
		parallel bool
	}
	m.mu.Lock()
	sessions := make([]managedSession, 0, len(m.sessions)+len(m.parallelSessions))
	for _, session := range m.sessions {
		sessions = append(sessions, managedSession{session: session})
	}
	for _, session := range m.parallelSessions {
		sessions = append(sessions, managedSession{session: session, parallel: true})
	}
	m.mu.Unlock()

	capturedAt := time.Now()
	interactiveSnapshots := make([]Snapshot, 0, len(sessions))
	parallelSnapshots := make([]Snapshot, 0, len(sessions))
	for _, managed := range sessions {
		snapshot := sessionStateSnapshot(managed.session)
		if managed.parallel {
			parallelSnapshots = append(parallelSnapshots, snapshot)
		} else {
			interactiveSnapshots = append(interactiveSnapshots, snapshot)
		}
	}
	intents := append(
		RestartIntentsFromSnapshots(interactiveSnapshots, capturedAt),
		restartIntentsFromSnapshots(parallelSnapshots, capturedAt, true)...,
	)
	intents = normalizeRestartIntents(intents)
	if len(intents) > 0 {
		if err := mergeRestartIntents(dataDir, intents); err != nil {
			return nil, err
		}
	}
	m.restartStateSaved = true

	restartKeys := make(map[string]struct{}, len(intents))
	for _, intent := range intents {
		restartKeys[intent.Key()] = struct{}{}
	}
	for _, managed := range sessions {
		snapshot := sessionStateSnapshot(managed.session)
		intent := RestartIntent{
			Provider:    snapshot.Provider,
			ProjectPath: snapshot.ProjectPath,
			SessionID:   snapshot.ThreadID,
			Parallel:    managed.parallel,
		}
		if _, ok := restartKeys[intent.Key()]; ok {
			// Best effort: provider-specific Interrupt methods make the durable
			// artifact explicitly interrupted before the helper is terminated.
			_ = managed.session.Interrupt()
		}
	}
	if err := m.CloseAll(); err != nil {
		return intents, err
	}
	return intents, nil
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

func (m *Manager) notifyParallel(projectPath string) {
	if m == nil {
		return
	}
	projectPath = strings.TrimSpace(projectPath)
	if projectPath == "" {
		return
	}
	m.mu.Lock()
	if _, ok := m.pendingParallelUpdates[projectPath]; ok {
		m.deferredParallelUpdates[projectPath] = struct{}{}
		m.mu.Unlock()
		return
	}
	delete(m.deferredParallelUpdates, projectPath)
	m.pendingParallelUpdates[projectPath] = struct{}{}
	m.mu.Unlock()
	if !m.enqueueParallelUpdate(projectPath) {
		m.mu.Lock()
		delete(m.pendingParallelUpdates, projectPath)
		m.deferredParallelUpdates[projectPath] = struct{}{}
		m.mu.Unlock()
	}
}

func (m *Manager) AckParallelUpdate(projectPath string) {
	if m == nil {
		return
	}
	projectPath = strings.TrimSpace(projectPath)
	if projectPath == "" {
		return
	}
	m.mu.Lock()
	delete(m.pendingParallelUpdates, projectPath)
	_, replay := m.deferredParallelUpdates[projectPath]
	if replay {
		delete(m.deferredParallelUpdates, projectPath)
		m.pendingParallelUpdates[projectPath] = struct{}{}
	}
	m.mu.Unlock()
	if !replay {
		return
	}
	if !m.enqueueParallelUpdate(projectPath) {
		m.mu.Lock()
		delete(m.pendingParallelUpdates, projectPath)
		m.deferredParallelUpdates[projectPath] = struct{}{}
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

func (m *Manager) enqueueParallelUpdate(projectPath string) bool {
	select {
	case m.parallelUpdates <- projectPath:
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
	sessions := make([]Session, 0, len(m.sessions)+len(m.parallelSessions))
	for _, session := range m.sessions {
		sessions = append(sessions, session)
	}
	for _, session := range m.parallelSessions {
		sessions = append(sessions, session)
	}
	m.mu.Unlock()

	for _, session := range sessions {
		reconciler, ok := session.(busyReconciler)
		if !ok {
			continue
		}
		snapshot := sessionStateSnapshot(session)
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
	sessions := make(map[string]Session, len(m.sessions)+len(m.parallelSessions))
	idleProtected := make(map[string]struct{}, len(m.idleProtected))
	for projectPath, session := range m.sessions {
		sessions[projectPath] = session
	}
	for projectPath, session := range m.parallelSessions {
		sessions["parallel\x00"+projectPath] = session
	}
	for projectPath := range m.idleProtected {
		idleProtected[projectPath] = struct{}{}
	}
	m.mu.Unlock()

	for projectPath, session := range sessions {
		if _, ok := idleProtected[projectPath]; ok {
			continue
		}
		snapshot := sessionStateSnapshot(session)
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
