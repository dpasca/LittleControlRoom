package tui

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"time"

	"lcroom/internal/aibackend"
	"lcroom/internal/attention"
	"lcroom/internal/brand"
	"lcroom/internal/codexapp"
	"lcroom/internal/codexcli"
	"lcroom/internal/commands"
	"lcroom/internal/config"
	"lcroom/internal/events"
	"lcroom/internal/model"
	"lcroom/internal/projectrun"
	"lcroom/internal/service"
	"lcroom/internal/sessionclassify"

	"github.com/charmbracelet/bubbles/textarea"
	"github.com/charmbracelet/bubbles/textinput"
	"github.com/charmbracelet/bubbles/viewport"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
	"github.com/charmbracelet/x/ansi"
)

type Model struct {
	ctx   context.Context
	svc   *service.Service
	busCh <-chan events.Event
	unsub func()

	allProjects            []model.ProjectSummary
	projects               []model.ProjectSummary
	projectRows            []projectListRow
	detail                 model.ProjectDetail
	selected               int
	offset                 int
	sortMode               projectSortMode
	visibility             projectVisibilityMode
	excludeProjectPatterns []string
	privacyMode            bool
	privacyPatterns        []string

	loading bool
	status  string
	err     error

	startupScanCompleted   bool
	projectsReloadInFlight bool
	projectsReloadQueued   bool
	scanInFlight           bool
	scanQueued             bool
	scanQueuedForceRetry   bool

	width     int
	height    int
	nowFn     func() time.Time
	homeDirFn func() (string, error)
	homeDir   string

	todoDialog            *todoDialogState
	todoEditor            *todoEditorState
	todoDeleteConfirm     *todoDeleteConfirmState
	todoLaunchDraft       *todoLaunchDraftState
	todoPendingLaunch     *todoPendingLaunchState
	todoCopyDialog        *todoCopyDialogState
	todoWorktreeEditor    *todoWorktreeEditorState
	todoExistingWorktree  *todoExistingWorktreeDialogState
	todoModelPickerReturn *todoModelPickerReturnState
	worktreeMergeConfirm  *worktreeMergeConfirmState
	worktreePostMerge     *worktreePostMergeState
	worktreeRemoveConfirm *worktreeRemoveConfirmState
	attentionDialog       *attentionDialogState

	commandMode                  bool
	commandInput                 textinput.Model
	commandSelected              int
	errorLogVisible              bool
	errorLogSelected             int
	errorLogEntries              []errorLogEntry
	projectFilter                string
	projectFilterDialog          *projectFilterDialogState
	ignoredPickerVisible         bool
	ignoredPickerLoading         bool
	ignoredPickerSelected        int
	ignoredPickerItems           []model.IgnoredProjectName
	newProjectDialog             *newProjectDialogState
	runCommandDialog             *runCommandDialogState
	preferredSelectPath          string
	diffView                     *diffViewState
	gitStatusDialog              *gitStatusDialog
	gitStatusApplying            bool
	commitPreview                *service.CommitPreview
	commitPreviewMessageOverride string
	commitPreviewRefreshing      bool
	commitApplying               bool
	commitTodoCompletions        []commitTodoItem
	commitTodoSelected           int
	setupMode                    bool
	setupChecked                 bool
	setupLoading                 bool
	setupSaving                  bool
	setupSelected                int
	setupModelTier               config.ModelTier
	setupSnapshot                aibackend.Snapshot
	settingsMode                 bool
	settingsSaving               bool
	settingsFields               []settingsField
	settingsSelected             int
	settingsBaseline             *config.EditableSettings
	settingsRevealPrivacy        bool

	detailViewport        viewport.Model
	runtimeViewport       viewport.Model
	runtimeActionSelected int
	focusedPane           paneFocus
	assessmentFlashUntil  map[string]time.Time
	usagePulseUntil       time.Time
	lastUsageTotals       model.LLMUsage
	haveUsageTotals       bool

	mouseEnabled           bool
	codexSelection         textSelection
	codexManager           *codexapp.Manager
	runtimeManager         *projectrun.Manager
	runtimeRefreshInFlight bool
	runtimeRefreshQueued   bool
	runtimeSnapshots       map[string]projectrun.Snapshot
	codexSnapshots         map[string]codexapp.Snapshot
	codexTranscriptRev     map[string]uint64
	codexVisibleProject    string
	codexHiddenProject     string
	codexPendingOpen       *codexPendingOpenState
	codexInput             textarea.Model
	codexDrafts            map[string]codexDraft
	pendingGitOperations   map[string]pendingGitOperation
	codexPasteTokenSeq     int
	codexClosedHandled     map[string]struct{}
	pendingGitSummaries         map[string]string
	pendingGitSummaryExpireNext map[string]bool
	codexPickerVisible          bool
	codexPickerSelected    int
	codexPickerChoices     []codexSessionChoice
	codexPickerLoading     bool
	codexPickerKind        codexPickerKind
	codexPickerTitle       string
	codexPickerHint        string
	codexPickerEmpty       string
	codexPickerProject     string
	codexPickerProvider    codexapp.Provider
	questionNotify         *questionNotification
	codexInputSelection    *codexInputSelectionState
	codexComposerSelection textSelection
	codexModelPicker       *codexModelPickerState
	embeddedModelPrefs     map[codexapp.Provider]embeddedModelPreference
	recentCodexModels      []string
	recentClaudeModels     []string
	recentOpenCodeModels   []string
	codexDenseExpanded     bool
	codexSlashSelected     int
	codexToolAnswers       map[string]codexToolAnswerState
	codexViewport          viewport.Model
	codexTranscriptCache   codexTranscriptRenderCache
	codexViewportContent   codexViewportContentState
	uiDiagnostics          *uiStallDiagnostics
	aiLatencyNextID        int64
	aiLatencyInFlight      map[int64]aiLatencyOp
	aiLatencyRecent        []aiLatencySample
	modelSettlePending     map[string]pendingModelSettleOp
	lastSpinnerTickAt      time.Time

	pendingG      bool
	todoLaunchSeq int64

	spinnerFrame int
	showSessions bool
	showEvents   bool
	showHelp     bool
	showAIStats  bool
	showPerf     bool

	hideReasoningSections bool

	newProjectRecentParents []string
	worktreeExpanded        map[string]bool
	detailReloadInFlight    map[string]bool
	detailReloadQueued      map[string]bool
	summaryReloadInFlight   map[string]bool
	summaryReloadQueued     map[string]bool
}

type codexTranscriptRenderCache struct {
	projectPath   string
	width         int
	denseExpanded bool
	transcriptRev uint64
	rendered      string
}

type codexViewportContentState struct {
	projectPath   string
	width         int
	denseExpanded bool
	transcriptRev uint64
}

type projectsMsg struct {
	projects               []model.ProjectSummary
	excludeProjectPatterns []string
	err                    error
	filterErr              error
}

type detailMsg struct {
	path   string
	detail model.ProjectDetail
	err    error
}

type projectSummaryMsg struct {
	path    string
	summary model.ProjectSummary
	found   bool
	err     error
}

type projectSessionSeenMsg struct {
	path string
	err  error
}

type scanMsg struct {
	report service.ScanReport
	err    error
}

type projectInvalidationKind uint8

const (
	projectInvalidationNone projectInvalidationKind = iota
	projectInvalidationProjectData
	projectInvalidationProjectStructure
	projectInvalidationProjectScan
)

type projectInvalidationIntent struct {
	kind                            projectInvalidationKind
	projectPath                     string
	detailPath                      string
	forceRetryFailedClassifications bool
}

type projectRefreshRequest struct {
	scan                            bool
	forceRetryFailedClassifications bool
	projects                        bool
	detailPath                      string
	summaryPaths                    []string
}

type actionMsg struct {
	projectPath            string
	status                 string
	clearPendingGitSummary bool
	err                    error
}

type browserOpenMsg struct {
	projectPath string
	status      string
	err         error
}

type runtimeActionMsg struct {
	projectPath string
	status      string
	err         error
}

type runtimeSnapshotsMsg struct {
	snapshots []projectrun.Snapshot
}

type runCommandSavedMsg struct {
	projectPath string
	command     string
	startAfter  bool
	err         error
}

type runCommandSuggestionMsg struct {
	projectPath string
	seq         int64
	suggestion  projectrun.Suggestion
	err         error
}

type todoActionMsg struct {
	projectPath string
	status      string
	err         error
}

type todoWorktreeLaunchMsg struct {
	launchID       int64
	perfOpID       int64
	perfDuration   time.Duration
	projectPath    string
	todoText       string
	status         string
	provider       codexapp.Provider
	openModelFirst bool
	err            error
}

type worktreeActionMsg struct {
	projectPath            string
	selectPath             string
	status                 string
	clearPendingGitSummary bool
	offerPostMergeCleanup  bool
	postMergeRootPath      string
	postMergeSourceBranch  string
	postMergeTargetBranch  string
	postMergeTodoID        int64
	postMergeTodoText      string
	postMergeTodoPath      string
	err                    error
}

type commitTodoItem struct {
	ID       int64
	Text     string
	Selected bool
}

type commitPreviewMsg struct {
	preview     service.CommitPreview
	projectPath string
	intent      service.GitActionIntent
	message     string
	err         error
}

type diffPreviewMsg struct {
	preview service.DiffPreview
	err     error
}

type diffStageToggleMsg struct {
	preview      service.DiffPreview
	status       string
	path         string
	originalPath string
	selectStaged bool
	err          error
}

type gitStatusDialog struct {
	Title             string
	ProjectPath       string
	ProjectName       string
	Branch            string
	Status            string
	RemoteStatus      string
	Warnings          []string
	CanPush           bool
	Ahead             int
	ReadyStatus       string
	DismissStatus     string
	ResolveSubmodules bool
	CommitIntent      service.GitActionIntent
	CommitMessage     string
}

type settingsSavedMsg struct {
	settings config.EditableSettings
	path     string
	err      error
}

type embeddedModelPreferencesSavedMsg struct {
	settings config.EditableSettings
	path     string
	err      error
}

type setupSavedMsg struct {
	settings config.EditableSettings
	path     string
	err      error
}

type setupSnapshotMsg struct {
	snapshot      aibackend.Snapshot
	openOnStartup bool
}

type privacyModeSavedMsg struct {
	privacyMode bool
	path        string
	err         error
}

type ignoredProjectsMsg struct {
	items []model.IgnoredProjectName
	err   error
}

type ignoredProjectActionMsg struct {
	status string
	err    error
}

type codexSessionOpenedMsg struct {
	projectPath  string
	snapshot     codexapp.Snapshot
	status       string
	perfOpID     int64
	perfDuration time.Duration
	err          error
}

type codexPendingOpenState struct {
	projectPath      string
	provider         codexapp.Provider
	showWhilePending bool
}

type pendingGitOperationKind string

const (
	pendingGitOperationUnknown       pendingGitOperationKind = ""
	pendingGitOperationCommit        pendingGitOperationKind = "commit"
	pendingGitOperationCommitPush    pendingGitOperationKind = "commit_push"
	pendingGitOperationPush          pendingGitOperationKind = "push"
	pendingGitOperationCommitMerge   pendingGitOperationKind = "commit_merge"
	pendingGitOperationPrune         pendingGitOperationKind = "prune"
	pendingGitOperationPrepareCommit pendingGitOperationKind = "prepare_commit"
	pendingGitOperationPrepareDiff   pendingGitOperationKind = "prepare_diff"
)

type pendingGitOperation struct {
	Kind    pendingGitOperationKind
	Summary string
}

type busMsg events.Event

type spinnerTickMsg struct{}
type codexUpdateMsg struct {
	projectPath string
}
type codexActionMsg struct {
	projectPath  string
	perfOpID     int64
	perfDuration time.Duration
	status       string
	closed       bool
	restoreDraft codexDraft
	provider     codexapp.Provider
	model        string
	reasoning    string
	awaitSettle  bool
	err          error
}
type codexModelListMsg struct {
	projectPath  string
	models       []codexapp.ModelOption
	perfOpID     int64
	perfDuration time.Duration
	err          error
}

// codexDeferredSnapshotMsg is sent when a non-blocking TrySnapshot failed due
// to lock contention and a goroutine was spawned to acquire the snapshot in
// the background. When this message arrives, the snapshot is stored in the
// cache and the normal update-cycle follow-up logic (viewport sync, model
// settle, etc.) runs.
type codexDeferredSnapshotMsg struct {
	projectPath string
	snapshot    codexapp.Snapshot
}

func (m codexModelListMsg) statusSummary() string {
	if m.err != nil {
		return ""
	}
	if len(m.models) == 0 {
		return "0 models"
	}
	return fmt.Sprintf("%d models", len(m.models))
}

type codexResumeChoicesMsg struct {
	projectPath string
	provider    codexapp.Provider
	choices     []codexSessionChoice
	err         error
}

type projectSortMode string
type projectVisibilityMode string
type paneFocus string

type bodyLayout struct {
	width               int
	height              int
	listPaneHeight      int
	bottomPaneHeight    int
	listContentWidth    int
	detailPaneWidth     int
	runtimePaneWidth    int
	detailContentWidth  int
	runtimeContentWidth int
}

const (
	sortByAttention projectSortMode = "attention"
	sortByRecent    projectSortMode = "recent"

	visibilityAIFolders  projectVisibilityMode = "ai_folders"
	visibilityAllFolders projectVisibilityMode = "all_folders"

	focusProjects paneFocus = "projects"
	focusDetail   paneFocus = "detail"
	focusRuntime  paneFocus = "runtime"

	initialProjectsStatus = "Loading projects..."
)

func New(ctx context.Context, svc *service.Service) Model {
	busCh, unsub := svc.Bus().Subscribe(128)
	commandInput := textinput.New()
	commandInput.Placeholder = "/help"
	commandInput.CharLimit = 280
	commandInput.Width = 56
	codexInput := newCodexTextarea()
	detailViewport := viewport.New(0, 0)
	runtimeViewport := viewport.New(0, 0)
	codexViewport := viewport.New(0, 0)
	initialSettings := config.EditableSettingsFromAppConfig(svc.Config())
	homeDir, _ := os.UserHomeDir()

	return Model{
		ctx:                    ctx,
		svc:                    svc,
		busCh:                  busCh,
		unsub:                  unsub,
		loading:                true,
		status:                 initialProjectsStatus,
		commandInput:           commandInput,
		codexInput:             codexInput,
		codexDrafts:            make(map[string]codexDraft),
		codexClosedHandled:     make(map[string]struct{}),
		pendingGitOperations:   make(map[string]pendingGitOperation),
		pendingGitSummaries:    make(map[string]string),
		codexSnapshots:         make(map[string]codexapp.Snapshot),
		codexTranscriptRev:     make(map[string]uint64),
		codexToolAnswers:       make(map[string]codexToolAnswerState),
		aiLatencyInFlight:      make(map[int64]aiLatencyOp),
		detailViewport:         detailViewport,
		runtimeViewport:        runtimeViewport,
		codexViewport:          codexViewport,
		uiDiagnostics:          newUIStallDiagnostics(strings.TrimSpace(homeDir), os.Getpid()),
		focusedPane:            focusProjects,
		assessmentFlashUntil:   make(map[string]time.Time),
		sortMode:               sortByAttention,
		visibility:             visibilityAIFolders,
		excludeProjectPatterns: currentExcludeProjectPatterns(svc),
		privacyMode:            initialSettings.PrivacyMode,
		privacyPatterns:        currentPrivacyPatterns(svc),
		codexManager:           codexapp.NewManager(),
		runtimeManager:         projectrun.NewManager(),
		runtimeSnapshots:       make(map[string]projectrun.Snapshot),
		embeddedModelPrefs:     embeddedModelPreferencesFromSettings(initialSettings),
		recentCodexModels:      append([]string(nil), initialSettings.RecentCodexModels...),
		recentClaudeModels:     append([]string(nil), initialSettings.RecentClaudeModels...),
		recentOpenCodeModels:   append([]string(nil), initialSettings.RecentOpenCodeModels...),
		hideReasoningSections:  initialSettings.HideReasoningSections,
		detailReloadInFlight:   make(map[string]bool),
		detailReloadQueued:     make(map[string]bool),
		summaryReloadInFlight:  make(map[string]bool),
		summaryReloadQueued:    make(map[string]bool),
		nowFn:                  time.Now,
		homeDirFn:              os.UserHomeDir,
		homeDir:                strings.TrimSpace(homeDir),
	}
}

func (m Model) currentTime() time.Time {
	if m.nowFn != nil {
		return m.nowFn()
	}
	return time.Now()
}

func (m Model) assessmentStallThreshold() time.Duration {
	if m.svc == nil {
		return 0
	}
	cfg := m.svc.Config()
	return sessionclassify.EffectiveAssessmentStallThreshold(cfg.ActiveThreshold, cfg.StuckThreshold)
}

func (m *Model) markAssessmentFlash(projectPath string, at time.Time) {
	projectPath = strings.TrimSpace(projectPath)
	if projectPath == "" {
		return
	}
	if at.IsZero() {
		at = m.currentTime()
	}
	if m.assessmentFlashUntil == nil {
		m.assessmentFlashUntil = make(map[string]time.Time)
	}
	m.assessmentFlashUntil[projectPath] = at.Add(assessmentFlashDuration)
}

func (m *Model) setPendingGitSummary(projectPath, summary string) {
	op := inferPendingGitOperation(summary)
	m.setPendingGitOperation(projectPath, op.Kind, op.Summary)
}

func (m *Model) setPendingGitOperation(projectPath string, kind pendingGitOperationKind, summary string) {
	projectPath = strings.TrimSpace(projectPath)
	summary = strings.TrimSpace(summary)
	if projectPath == "" || summary == "" {
		return
	}
	if kind == pendingGitOperationUnknown {
		kind = inferPendingGitOperation(summary).Kind
	}
	if m.pendingGitOperations == nil {
		m.pendingGitOperations = make(map[string]pendingGitOperation)
	}
	m.pendingGitOperations[projectPath] = pendingGitOperation{Kind: kind, Summary: summary}
	if m.pendingGitSummaries == nil {
		m.pendingGitSummaries = make(map[string]string)
	}
	m.pendingGitSummaries[projectPath] = summary
}

func (m *Model) clearPendingGitSummary(projectPath string) {
	projectPath = strings.TrimSpace(projectPath)
	if projectPath == "" {
		return
	}
	if m.pendingGitSummaries != nil {
		delete(m.pendingGitSummaries, projectPath)
	}
	if m.pendingGitOperations != nil {
		delete(m.pendingGitOperations, projectPath)
	}
}

func (m *Model) expirePendingGitSummaryOnRefresh(projectPath string) {
	projectPath = strings.TrimSpace(projectPath)
	if projectPath == "" {
		return
	}
	if m.pendingGitSummaryExpireNext == nil {
		m.pendingGitSummaryExpireNext = make(map[string]bool)
	}
	m.pendingGitSummaryExpireNext[projectPath] = true
}

func (m *Model) flushExpiredPendingGitSummaries() {
	for path := range m.pendingGitSummaryExpireNext {
		m.clearPendingGitSummary(path)
	}
	m.pendingGitSummaryExpireNext = nil
}

func (m Model) pendingGitSummary(projectPath string) string {
	projectPath = strings.TrimSpace(projectPath)
	if projectPath == "" || m.pendingGitSummaries == nil {
		return ""
	}
	return strings.TrimSpace(m.pendingGitSummaries[projectPath])
}

func inferPendingGitOperation(summary string) pendingGitOperation {
	summary = strings.TrimSpace(summary)
	switch summary {
	case "Committing...":
		return pendingGitOperation{Kind: pendingGitOperationCommit, Summary: summary}
	case "Committing and pushing...":
		return pendingGitOperation{Kind: pendingGitOperationCommitPush, Summary: summary}
	case "Pushing...", "Pushing existing commits...":
		return pendingGitOperation{Kind: pendingGitOperationPush, Summary: summary}
	case "Committing and merging worktree back...":
		return pendingGitOperation{Kind: pendingGitOperationCommitMerge, Summary: summary}
	case "Pruning worktrees...", "Pruning stale git worktrees...":
		return pendingGitOperation{Kind: pendingGitOperationPrune, Summary: summary}
	case "Preparing commit preview...", "Preparing finish preview...", "Refreshing commit preview...", "Resolving submodule commits...":
		return pendingGitOperation{Kind: pendingGitOperationPrepareCommit, Summary: summary}
	case "Preparing diff view...":
		return pendingGitOperation{Kind: pendingGitOperationPrepareDiff, Summary: summary}
	default:
		return pendingGitOperation{Kind: pendingGitOperationUnknown, Summary: summary}
	}
}

func normalizePendingGitLabel(summary string) string {
	summary = strings.TrimSpace(summary)
	summary = strings.TrimRight(summary, ".")
	if summary == "" {
		return "git op"
	}
	return strings.ToLower(summary[:1]) + summary[1:]
}

func (op pendingGitOperation) summaryText() string {
	return strings.TrimSpace(op.Summary)
}

func (op pendingGitOperation) shortLabel() string {
	switch op.Kind {
	case pendingGitOperationCommit:
		return "committing"
	case pendingGitOperationCommitPush:
		return "commit + push"
	case pendingGitOperationPush:
		return "pushing"
	case pendingGitOperationCommitMerge:
		return "commit + merge"
	case pendingGitOperationPrune:
		return "pruning"
	case pendingGitOperationPrepareCommit:
		return "preparing commit"
	case pendingGitOperationPrepareDiff:
		return "preparing diff"
	default:
		return normalizePendingGitLabel(op.Summary)
	}
}

func (m Model) pendingGitOperation(projectPath string) (pendingGitOperation, bool) {
	projectPath = strings.TrimSpace(projectPath)
	if projectPath == "" {
		return pendingGitOperation{}, false
	}
	if m.pendingGitOperations != nil {
		if op, ok := m.pendingGitOperations[projectPath]; ok && strings.TrimSpace(op.Summary) != "" {
			if op.Kind == pendingGitOperationUnknown {
				op.Kind = inferPendingGitOperation(op.Summary).Kind
			}
			return op, true
		}
	}
	if summary := m.pendingGitSummary(projectPath); summary != "" {
		op := inferPendingGitOperation(summary)
		if strings.TrimSpace(op.Summary) == "" {
			op.Summary = summary
		}
		return op, true
	}
	return pendingGitOperation{}, false
}

func (m Model) assessmentFlashActive(projectPath string) bool {
	projectPath = strings.TrimSpace(projectPath)
	if projectPath == "" {
		return false
	}
	until, ok := m.assessmentFlashUntil[projectPath]
	if !ok {
		return false
	}
	return until.After(m.currentTime())
}

func (m *Model) pruneTransientHighlights(now time.Time) {
	if now.IsZero() {
		now = m.currentTime()
	}
	for projectPath, until := range m.assessmentFlashUntil {
		if !until.After(now) {
			delete(m.assessmentFlashUntil, projectPath)
		}
	}
	if !m.usagePulseUntil.After(now) {
		m.usagePulseUntil = time.Time{}
	}
}

func (m Model) projectPendingEmbeddedApproval(projectPath string) (*codexapp.ApprovalRequest, codexapp.Provider, bool) {
	projectPath = strings.TrimSpace(projectPath)
	if projectPath == "" {
		return nil, "", false
	}
	snapshot, ok := m.nonBlockingCodexSnapshot(projectPath)
	if !ok {
		return nil, "", false
	}
	if snapshot.Closed || snapshot.PendingApproval == nil {
		return nil, "", false
	}
	return snapshot.PendingApproval, embeddedProvider(snapshot), true
}

func (m Model) projectApprovalPulseActive(projectPath string) bool {
	if _, _, ok := m.projectPendingEmbeddedApproval(projectPath); !ok {
		return false
	}
	return m.spinnerFrame%2 == 0
}

func (m Model) projectPendingEmbeddedQuestion(projectPath string) (string, codexapp.Provider, bool) {
	projectPath = strings.TrimSpace(projectPath)
	if projectPath == "" {
		return "", "", false
	}
	snapshot, ok := m.nonBlockingCodexSnapshot(projectPath)
	if !ok {
		return "", "", false
	}
	if snapshot.Closed {
		return "", "", false
	}
	if snapshot.PendingToolInput != nil {
		return snapshot.PendingToolInput.Summary(), embeddedProvider(snapshot), true
	}
	if snapshot.PendingElicitation != nil {
		return snapshot.PendingElicitation.Summary(), embeddedProvider(snapshot), true
	}
	return "", "", false
}

func (m Model) projectQuestionPulseActive(projectPath string) bool {
	if _, _, ok := m.projectPendingEmbeddedQuestion(projectPath); !ok {
		return false
	}
	return m.spinnerFrame%2 == 0
}

func (m *Model) refreshUsagePulse() {
	usage := m.currentUsage()
	if !usage.Enabled {
		m.lastUsageTotals = model.LLMUsage{}
		m.haveUsageTotals = false
		m.usagePulseUntil = time.Time{}
		return
	}
	totals := usage.Totals
	if !m.haveUsageTotals {
		m.lastUsageTotals = totals
		m.haveUsageTotals = true
		return
	}
	if totals.EstimatedCostUSD > m.lastUsageTotals.EstimatedCostUSD {
		m.usagePulseUntil = m.currentTime().Add(usagePulseDuration)
	}
	m.lastUsageTotals = totals
}

func currentExcludeProjectPatterns(svc *service.Service) []string {
	if svc == nil {
		return nil
	}
	return append([]string(nil), svc.Config().ExcludeProjectPatterns...)
}

func currentPrivacyPatterns(svc *service.Service) []string {
	if svc == nil {
		return nil
	}
	return append([]string(nil), svc.Config().PrivacyPatterns...)
}

func (m Model) Init() tea.Cmd {
	m.noteUIProgress("Init")
	done := m.beginUIPhase("Init", "", "")
	defer done()
	cmds := []tea.Cmd{
		m.requestProjectsReloadCmd(),
		m.requestScanCmd(false),
		m.loadRecentProjectParentsCmd(),
		m.loadRuntimeSnapshotsCmd(),
		m.waitBusCmd(),
		m.waitCodexCmd(),
		spinnerTickCmd(),
	}
	if setupCmd := m.startupSetupSnapshotCmd(); setupCmd != nil {
		cmds = append(cmds, setupCmd)
	}
	return tea.Batch(cmds...)
}

func batchCmds(cmds ...tea.Cmd) tea.Cmd {
	nonNil := make([]tea.Cmd, 0, len(cmds))
	for _, cmd := range cmds {
		if cmd != nil {
			nonNil = append(nonNil, cmd)
		}
	}
	switch len(nonNil) {
	case 0:
		return nil
	case 1:
		return nonNil[0]
	default:
		return tea.Batch(nonNil...)
	}
}

func (m Model) loadRuntimeSnapshotsCmd() tea.Cmd {
	manager := m.runtimeManager
	if manager == nil {
		return nil
	}
	return func() tea.Msg {
		return runtimeSnapshotsMsg{snapshots: manager.Snapshots()}
	}
}

func (m *Model) requestRuntimeSnapshotsRefreshCmd() tea.Cmd {
	if m.runtimeManager == nil {
		return nil
	}
	if m.runtimeRefreshInFlight {
		m.runtimeRefreshQueued = true
		return nil
	}
	m.runtimeRefreshInFlight = true
	return m.loadRuntimeSnapshotsCmd()
}

func (m *Model) finishRuntimeSnapshotsRefreshCmd() tea.Cmd {
	if !m.runtimeRefreshInFlight {
		return nil
	}
	if m.runtimeRefreshQueued {
		m.runtimeRefreshQueued = false
		return m.loadRuntimeSnapshotsCmd()
	}
	m.runtimeRefreshInFlight = false
	return nil
}

func cloneRuntimeSnapshots(snapshots []projectrun.Snapshot) map[string]projectrun.Snapshot {
	if len(snapshots) == 0 {
		return make(map[string]projectrun.Snapshot)
	}
	cloned := make(map[string]projectrun.Snapshot, len(snapshots))
	for _, snapshot := range snapshots {
		path := normalizeProjectPath(snapshot.ProjectPath)
		if path == "" {
			continue
		}
		snapshot.ProjectPath = path
		cloned[path] = snapshot
	}
	return cloned
}

func runtimeRunningStateChanged(prev, next map[string]projectrun.Snapshot) bool {
	seen := make(map[string]struct{}, len(prev))
	for path, snapshot := range prev {
		seen[path] = struct{}{}
		if snapshot.Running != next[path].Running {
			return true
		}
	}
	for path, snapshot := range next {
		if _, ok := seen[path]; ok {
			continue
		}
		if snapshot.Running {
			return true
		}
	}
	return false
}

func normalizeProjectPath(path string) string {
	path = strings.TrimSpace(path)
	if path == "" {
		return ""
	}
	return filepath.Clean(path)
}

func (m *Model) ensureRefreshState() {
	if m.detailReloadInFlight == nil {
		m.detailReloadInFlight = make(map[string]bool)
	}
	if m.detailReloadQueued == nil {
		m.detailReloadQueued = make(map[string]bool)
	}
	if m.summaryReloadInFlight == nil {
		m.summaryReloadInFlight = make(map[string]bool)
	}
	if m.summaryReloadQueued == nil {
		m.summaryReloadQueued = make(map[string]bool)
	}
}

func (m *Model) requestProjectsReloadCmd() tea.Cmd {
	if m.projectsReloadInFlight {
		m.projectsReloadQueued = true
		return nil
	}
	m.projectsReloadInFlight = true
	m.projectsReloadQueued = false
	return m.loadProjectsCmd()
}

func (m *Model) finishProjectsReloadCmd() tea.Cmd {
	m.projectsReloadInFlight = false
	if !m.projectsReloadQueued {
		return nil
	}
	m.projectsReloadQueued = false
	return m.requestProjectsReloadCmd()
}

func (m *Model) requestScanCmd(forceRetryFailedClassifications bool) tea.Cmd {
	if m.scanInFlight {
		m.scanQueued = true
		m.scanQueuedForceRetry = m.scanQueuedForceRetry || forceRetryFailedClassifications
		return nil
	}
	m.scanInFlight = true
	m.scanQueued = false
	m.scanQueuedForceRetry = false
	return m.scanCmd(forceRetryFailedClassifications)
}

func (m *Model) finishScanCmd() tea.Cmd {
	m.scanInFlight = false
	if !m.scanQueued {
		return nil
	}
	forceRetry := m.scanQueuedForceRetry
	m.scanQueued = false
	m.scanQueuedForceRetry = false
	return m.requestScanCmd(forceRetry)
}

func (m *Model) requestDetailReloadCmd(path string) tea.Cmd {
	path = normalizeProjectPath(path)
	if path == "" {
		return nil
	}
	m.ensureRefreshState()
	if m.detailReloadInFlight[path] {
		m.detailReloadQueued[path] = true
		return nil
	}
	m.detailReloadInFlight[path] = true
	delete(m.detailReloadQueued, path)
	return m.loadDetailCmd(path)
}

func (m *Model) finishDetailReloadCmd(path string) tea.Cmd {
	path = normalizeProjectPath(path)
	if path == "" {
		return nil
	}
	m.ensureRefreshState()
	delete(m.detailReloadInFlight, path)
	if !m.detailReloadQueued[path] {
		delete(m.detailReloadQueued, path)
		return nil
	}
	delete(m.detailReloadQueued, path)
	return m.requestDetailReloadCmd(path)
}

func (m *Model) requestProjectSummaryReloadCmd(path string) tea.Cmd {
	path = normalizeProjectPath(path)
	if path == "" {
		return nil
	}
	m.ensureRefreshState()
	if m.summaryReloadInFlight[path] {
		m.summaryReloadQueued[path] = true
		return nil
	}
	m.summaryReloadInFlight[path] = true
	delete(m.summaryReloadQueued, path)
	return m.loadProjectSummaryCmd(path)
}

func (m *Model) finishProjectSummaryReloadCmd(path string) tea.Cmd {
	path = normalizeProjectPath(path)
	if path == "" {
		return nil
	}
	m.ensureRefreshState()
	delete(m.summaryReloadInFlight, path)
	if !m.summaryReloadQueued[path] {
		delete(m.summaryReloadQueued, path)
		return nil
	}
	delete(m.summaryReloadQueued, path)
	return m.requestProjectSummaryReloadCmd(path)
}

func (m *Model) requestProjectRefreshCmd(req projectRefreshRequest) tea.Cmd {
	cmds := make([]tea.Cmd, 0, 2+len(req.summaryPaths))
	if req.scan {
		cmds = append(cmds, m.requestScanCmd(req.forceRetryFailedClassifications))
	}
	if req.projects {
		cmds = append(cmds, m.requestProjectsReloadCmd())
	}
	seen := make(map[string]struct{}, len(req.summaryPaths))
	for _, path := range req.summaryPaths {
		path = normalizeProjectPath(path)
		if path == "" {
			continue
		}
		if _, ok := seen[path]; ok {
			continue
		}
		seen[path] = struct{}{}
		cmds = append(cmds, m.requestProjectSummaryReloadCmd(path))
	}
	cmds = append(cmds, m.requestDetailReloadCmd(req.detailPath))
	return batchCmds(cmds...)
}

func invalidateProjectData(projectPath string) projectInvalidationIntent {
	return projectInvalidationIntent{
		kind:        projectInvalidationProjectData,
		projectPath: projectPath,
	}
}

func invalidateProjectStructure(detailPath string) projectInvalidationIntent {
	return projectInvalidationIntent{
		kind:       projectInvalidationProjectStructure,
		detailPath: detailPath,
	}
}

func invalidateProjectScan(detailPath string, forceRetryFailedClassifications bool) projectInvalidationIntent {
	return projectInvalidationIntent{
		kind:                            projectInvalidationProjectScan,
		detailPath:                      detailPath,
		forceRetryFailedClassifications: forceRetryFailedClassifications,
	}
}

func (m Model) visibleDetailPathForProject(path string) string {
	path = normalizeProjectPath(path)
	if path == "" {
		return ""
	}
	if m.currentDetailTargetPath() != path {
		return ""
	}
	return path
}

func (m *Model) requestProjectInvalidationCmd(intent projectInvalidationIntent) tea.Cmd {
	switch intent.kind {
	case projectInvalidationProjectData:
		path := normalizeProjectPath(intent.projectPath)
		if path == "" {
			return nil
		}
		return m.requestProjectRefreshCmd(projectRefreshRequest{
			detailPath:   m.visibleDetailPathForProject(path),
			summaryPaths: []string{path},
		})
	case projectInvalidationProjectStructure:
		return m.requestProjectRefreshCmd(projectRefreshRequest{
			projects:   true,
			detailPath: normalizeProjectPath(intent.detailPath),
		})
	case projectInvalidationProjectScan:
		return m.requestProjectRefreshCmd(projectRefreshRequest{
			scan:                            true,
			forceRetryFailedClassifications: intent.forceRetryFailedClassifications,
			projects:                        true,
			detailPath:                      normalizeProjectPath(intent.detailPath),
		})
	default:
		return nil
	}
}

func (m *Model) requestProjectDetailViewCmd(path string) tea.Cmd {
	return m.requestDetailReloadCmd(normalizeProjectPath(path))
}

func (m *Model) requestSelectedProjectDetailViewCmd() tea.Cmd {
	return m.requestProjectDetailViewCmd(m.currentSelectedProjectPath())
}

func (m Model) currentSelectedProjectPath() string {
	if p, ok := m.selectedProject(); ok {
		return normalizeProjectPath(p.Path)
	}
	return ""
}

func (m Model) currentDetailTargetPath() string {
	if p, ok := m.selectedProject(); ok {
		return normalizeProjectPath(p.Path)
	}
	return normalizeProjectPath(m.detail.Summary.Path)
}

func normalizeUpdateModel(m tea.Model) Model {
	switch mm := m.(type) {
	case Model:
		return mm
	case *Model:
		if mm == nil {
			panic("tui update returned nil *Model")
		}
		return *mm
	default:
		panic(fmt.Sprintf("tui update returned unsupported model type %T", m))
	}
}

func (m Model) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	m.noteUIProgress("Update " + uiMessageLabel(msg))
	done := m.beginUIPhase("Update", m.currentLatencyProjectPath(), uiMessageLabel(msg))
	defer done()
	mdl, cmd := m.update(msg)
	mm := normalizeUpdateModel(mdl)
	prevWant := m.codexVisible() || m.diffView != nil
	want := mm.codexVisible() || mm.diffView != nil
	mm.mouseEnabled = want
	if want != prevWant {
		var mouseCmd tea.Cmd
		if want {
			mouseCmd = tea.EnableMouseCellMotion
		} else {
			mouseCmd = tea.DisableMouse
		}
		return mm, tea.Batch(cmd, mouseCmd)
	}
	return mm, cmd
}

func (m Model) update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
		m.width = msg.Width
		m.height = msg.Height
		m.ensureSelectionVisible()
		m.syncCommandInputWidth()
		m.syncTodoDialogSize()
		m.syncTodoEditorSize()
		m.syncDiffView(false)
		m.syncDetailViewport(false)
		m.syncCodexComposerSize()
		m.syncCodexViewport(false)
		m.syncRuntimeViewport(false)
		return m, nil
	case tea.MouseMsg:
		if m.todoDialog != nil && (msg.Button == tea.MouseButtonWheelUp || msg.Button == tea.MouseButtonWheelDown) {
			return m.updateTodoDialogMouseScroll(msg)
		}
		if m.diffView != nil && m.diffView.hasFiles() {
			var cmd tea.Cmd
			m.diffView.contentViewport, cmd = m.diffView.contentViewport.Update(msg)
			m.updateDiffSelectionFromScroll()
			return m, cmd
		}
		if m.codexVisible() {
			// Try composer selection first (bottom area), then viewport.
			if cmd, handled := m.handleCodexComposerMouseSelection(msg); handled {
				return m, cmd
			}
			if cmd, handled := m.handleCodexMouseSelection(msg); handled {
				// Clear any stale composer selection when viewport drag starts.
				m.codexComposerSelection = textSelection{}
				return m, cmd
			}
			// Unhandled mouse event (e.g. scroll wheel) — finalize any
			// pending drag (missed release), clear selection, and forward
			// to viewport.
			if m.codexSelection.dragging {
				m.finalizeCodexSelection()
			}
			if m.codexComposerSelection.dragging {
				m.finalizeCodexComposerSelection()
			}
			m.codexSelection = textSelection{}
			m.codexComposerSelection = textSelection{}
			var cmd tea.Cmd
			m.codexViewport, cmd = m.codexViewport.Update(msg)
			return m, cmd
		}
	case tea.KeyMsg:
		if m.codexModelPickerVisible() {
			return m.updateCodexModelPickerMode(msg)
		}
		if m.codexPickerVisible {
			return m.updateCodexPickerMode(msg)
		}
		if m.ignoredPickerVisible {
			return m.updateIgnoredPickerMode(msg)
		}
		if m.errorLogVisible {
			return m.updateErrorLogMode(msg)
		}
		if m.attentionDialog != nil {
			return m.updateAttentionDialogMode(msg)
		}
		if m.codexVisible() {
			return m.updateCodexMode(msg)
		}
		if m.newProjectDialog != nil {
			return m.updateNewProjectMode(msg)
		}
		if m.runCommandDialog != nil {
			return m.updateRunCommandDialogMode(msg)
		}
		if m.worktreeMergeConfirm != nil {
			return m.updateWorktreeMergeConfirmMode(msg)
		}
		if m.worktreePostMerge != nil {
			return m.updateWorktreePostMergeMode(msg)
		}
		if m.worktreeRemoveConfirm != nil {
			return m.updateWorktreeRemoveConfirmMode(msg)
		}
		if m.todoDeleteConfirm != nil {
			return m.updateTodoDeleteConfirmMode(msg)
		}
		if m.todoExistingWorktree != nil {
			return m.updateTodoExistingWorktreeMode(msg)
		}
		if m.todoCopyDialog != nil {
			return m.updateTodoCopyDialogMode(msg)
		}
		if m.todoWorktreeEditor != nil {
			return m.updateTodoWorktreeEditorMode(msg)
		}
		if m.todoEditor != nil {
			return m.updateTodoEditorMode(msg)
		}
		if m.todoDialog != nil {
			return m.updateTodoDialogMode(msg)
		}
		if m.projectFilterDialog != nil {
			return m.updateProjectFilterMode(msg)
		}
		if m.commandMode {
			return m.updateCommandMode(msg)
		}
		if m.diffView != nil {
			return m.updateDiffMode(msg)
		}
		if m.gitStatusDialog != nil {
			return m.updateGitStatusDialogMode(msg)
		}
		if m.commitPreview != nil {
			return m.updateCommitPreviewMode(msg)
		}
		if m.setupMode {
			return m.updateSetupMode(msg)
		}
		if m.settingsMode {
			return m.updateSettingsMode(msg)
		}
		if m.showPerf {
			return m.updatePerfMode(msg)
		}
		if m.showAIStats {
			return m.updateAIStatsMode(msg)
		}
		if m.questionNotify != nil {
			return m.updateQuestionNotifyMode(msg)
		}
		return m.updateNormalMode(msg)
	case projectsMsg:
		m.flushExpiredPendingGitSummaries()
		reloadCmd := m.finishProjectsReloadCmd()
		if msg.err != nil {
			m.loading = false
			m.err = msg.err
			m.status = projectLoadFailedStatus(len(m.projects) > 0)
			return m, reloadCmd
		}
		startupEmptyCache := m.status == initialProjectsStatus && len(msg.projects) == 0
		if !startupEmptyCache {
			m.loading = false
		}
		m.err = msg.filterErr
		selectedPath := ""
		if strings.TrimSpace(m.preferredSelectPath) != "" {
			selectedPath = strings.TrimSpace(m.preferredSelectPath)
			m.preferredSelectPath = ""
		} else if p, ok := m.selectedProject(); ok {
			selectedPath = p.Path
		}

		m.excludeProjectPatterns = append([]string(nil), msg.excludeProjectPatterns...)
		m.allProjects = msg.projects
		m.rebuildProjectList(selectedPath)
		if !startupEmptyCache && (strings.TrimSpace(m.status) == "" || m.status == initialProjectsStatus || len(m.projects) == 0) {
			m.status = loadedProjectsStatus(len(m.projects), m.sortMode, m.visibility, m.projectFilter)
		}
		if len(m.projects) > 0 {
			m.syncDetailViewport(false)
			return m, batchCmds(reloadCmd, m.requestSelectedProjectDetailViewCmd())
		}
		m.detail = model.ProjectDetail{}
		m.syncDetailViewport(true)
		return m, reloadCmd
	case recentProjectParentsMsg:
		if msg.err == nil {
			m.newProjectRecentParents = append([]string(nil), msg.paths...)
		}
		return m, nil
	case newProjectPreviewMsg:
		if m.newProjectDialog == nil || msg.seq != m.newProjectDialog.PreviewSeq {
			return m, nil
		}
		m.newProjectDialog.Preview = msg.preview
		m.newProjectDialog.PreviewPending = false
		return m, nil
	case setupSnapshotMsg:
		m.setupChecked = true
		m.setupLoading = false
		m.setupSnapshot = msg.snapshot
		if msg.openOnStartup && msg.snapshot.NeedsSetup() {
			return m, m.openSetupMode()
		}
		return m, nil
	case newProjectResultMsg:
		if m.newProjectDialog != nil {
			m.newProjectDialog.Submitting = false
		}
		if msg.err != nil {
			m.reportError("Project setup failed", msg.err, "")
			return m, nil
		}
		m.err = nil
		m.newProjectRecentParents = append([]string(nil), msg.result.RecentParentPaths...)
		m.newProjectDialog = nil
		m.focusedPane = focusProjects
		m.preferredSelectPath = msg.result.ProjectPath
		switch msg.result.Action {
		case service.CreateOrAttachProjectCreated:
			if msg.result.GitRepoCreated {
				m.status = "Project created, git initialized, and added to the list"
			} else {
				m.status = "Project created and added to the list"
			}
		case service.CreateOrAttachProjectAdded:
			if msg.result.NameDerivedFromPath {
				m.status = fmt.Sprintf("Existing folder added to the list using %q as the project name", msg.result.ProjectName)
			} else {
				m.status = "Existing folder added to the list"
			}
		default:
			if msg.result.NameDerivedFromPath {
				m.status = fmt.Sprintf("Project already in the list as %q", msg.result.ProjectName)
			} else {
				m.status = "Project already in the list"
			}
		}
		return m, m.requestProjectInvalidationCmd(invalidateProjectStructure(""))
	case detailMsg:
		reloadCmd := m.finishDetailReloadCmd(msg.path)
		if targetPath := m.currentDetailTargetPath(); targetPath != "" && normalizeProjectPath(msg.path) != targetPath {
			return m, reloadCmd
		}
		m.err = msg.err
		if msg.err == nil {
			m.detail = msg.detail
			m.syncTodoDialogSelection()
			m.syncDetailViewport(false)
		}
		return m, reloadCmd
	case projectSummaryMsg:
		reloadCmd := m.finishProjectSummaryReloadCmd(msg.path)
		if msg.err != nil {
			m.err = msg.err
			return m, reloadCmd
		}
		selectedPath := ""
		if p, ok := m.selectedProject(); ok {
			selectedPath = p.Path
		}
		if msg.found {
			m.loading = false
			m.upsertProjectSummary(msg.summary)
		} else {
			m.removeProjectSummary(msg.path)
			if filepath.Clean(selectedPath) == filepath.Clean(strings.TrimSpace(msg.path)) {
				selectedPath = ""
			}
		}
		m.rebuildProjectList(selectedPath)
		if len(m.projects) > 0 && strings.TrimSpace(m.detail.Summary.Path) == "" {
			m.syncDetailViewport(false)
			return m, batchCmds(reloadCmd, m.requestSelectedProjectDetailViewCmd())
		}
		if len(m.projects) == 0 {
			m.detail = model.ProjectDetail{}
			m.syncDetailViewport(true)
		}
		return m, reloadCmd
	case projectSessionSeenMsg:
		if msg.err != nil {
			m.reportError("Could not mark session as read", msg.err, msg.path)
		}
		return m, nil
	case scanMsg:
		reloadCmd := m.finishScanCmd()
		m.loading = false
		if msg.err != nil {
			m.reportError("Scan failed", msg.err, "")
			return m, reloadCmd
		}
		m.startupScanCompleted = true
		m.status = scanCompleteStatus(msg.report)
		return m, batchCmds(reloadCmd, m.requestProjectInvalidationCmd(invalidateProjectStructure("")))
	case commitPreviewMsg:
		m.clearPendingGitSummary(msg.projectPath)
		m.diffView = nil
		m.commitPreviewRefreshing = false
		if msg.err != nil {
			var noChangesErr service.NoChangesToCommitError
			if errors.As(msg.err, &noChangesErr) {
				dialog := gitStatusDialogFromNoChanges(noChangesErr)
				m.err = nil
				m.showHelp = false
				m.gitStatusDialog = &dialog
				m.gitStatusApplying = false
				m.commitPreview = nil
				m.commitTodoCompletions = nil
				m.commitPreviewMessageOverride = ""
				m.commitApplying = false
				m.status = gitStatusDialogReadyStatus(dialog)
				return m, nil
			}
			var submoduleErr service.SubmoduleAttentionError
			if errors.As(msg.err, &submoduleErr) {
				dialog := gitStatusDialogFromSubmoduleAttention(submoduleErr, msg.intent, msg.message)
				m.err = nil
				m.showHelp = false
				m.gitStatusDialog = &dialog
				m.gitStatusApplying = false
				m.commitPreview = nil
				m.commitTodoCompletions = nil
				m.commitPreviewMessageOverride = ""
				m.commitApplying = false
				m.status = gitStatusDialogReadyStatus(dialog)
				return m, nil
			}
			m.reportError("Commit preview failed", msg.err, msg.projectPath)
			m.gitStatusDialog = nil
			m.gitStatusApplying = false
			m.commitPreview = nil
			m.commitTodoCompletions = nil
			m.commitPreviewMessageOverride = ""
			m.commitApplying = false
			return m, nil
		}
		m.err = nil
		m.showHelp = false
		m.gitStatusDialog = nil
		m.gitStatusApplying = false
		m.commitPreview = &msg.preview
		m.commitPreviewMessageOverride = msg.message
		m.commitApplying = false
		m.commitTodoCompletions = buildCommitTodoItems(msg.preview.SuggestedTodos)
		m.commitTodoSelected = 0
		m.status = commitPreviewReadyStatus(msg.preview.CanPush)
		return m, nil
	case diffPreviewMsg:
		if m.diffView == nil {
			return m, nil
		}
		m.clearPendingGitSummary(m.diffView.ProjectPath)
		m.diffView.loading = false
		if msg.err != nil {
			var noDiffErr service.NoDiffChangesError
			if errors.As(msg.err, &noDiffErr) {
				m.err = nil
				m.diffView.preview = &service.DiffPreview{
					ProjectPath: noDiffErr.ProjectPath,
					ProjectName: noDiffErr.ProjectName,
					Branch:      noDiffErr.Branch,
				}
				m.diffView.selected = 0
				m.diffView.offset = 0
				m.diffView.resetRenderCache()
				m.syncDiffView(true)
				m.status = diffViewReadyStatus(*m.diffView)
				return m, nil
			}
			m.diffView = nil
			m.reportError("Diff preview failed", msg.err, "")
			return m, nil
		}
		m.err = nil
		m.diffView.preview = &msg.preview
		m.diffView.selected = 0
		m.diffView.offset = 0
		m.diffView.resetRenderCache()
		m.syncDiffView(true)
		m.status = diffViewReadyStatus(*m.diffView)
		return m, nil
	case diffStageToggleMsg:
		if m.diffView == nil {
			return m, nil
		}
		m.diffView.loading = false
		if msg.err != nil {
			var noDiffErr service.NoDiffChangesError
			if errors.As(msg.err, &noDiffErr) {
				m.err = nil
				m.diffView.preview = &service.DiffPreview{
					ProjectPath: noDiffErr.ProjectPath,
					ProjectName: noDiffErr.ProjectName,
					Branch:      noDiffErr.Branch,
				}
				m.diffView.selected = 0
				m.diffView.offset = 0
				m.diffView.resetRenderCache()
				m.syncDiffView(true)
				m.status = diffViewReadyStatus(*m.diffView)
				return m, nil
			}
			m.reportError("Diff staging failed", msg.err, "")
			return m, nil
		}
		m.err = nil
		m.diffView.preview = &msg.preview
		m.diffView.selected = diffPreviewStagedSelectionIndex(msg.preview.Files, msg.path, msg.originalPath, m.diffView.selected, msg.selectStaged)
		m.diffView.resetRenderCache()
		m.syncDiffView(true)
		if strings.TrimSpace(msg.status) != "" {
			m.status = msg.status
		} else {
			m.status = diffViewReadyStatus(*m.diffView)
		}
		return m, nil
	case actionMsg:
		m.gitStatusApplying = false
		m.gitStatusDialog = nil
		m.commitApplying = false
		m.commitPreviewRefreshing = false
		if msg.clearPendingGitSummary {
			if msg.err != nil {
				m.clearPendingGitSummary(msg.projectPath)
			} else {
				m.expirePendingGitSummaryOnRefresh(msg.projectPath)
			}
		}
		m.commitPreview = nil
		m.commitTodoCompletions = nil
		m.commitPreviewMessageOverride = ""
		m.diffView = nil
		if msg.err != nil {
			m.reportError("Action failed", msg.err, msg.projectPath)
			return m, nil
		}
		m.status = msg.status
		return m, m.requestProjectInvalidationCmd(invalidateProjectScan("", false))
	case browserOpenMsg:
		if msg.err != nil {
			m.reportError("Open failed", msg.err, msg.projectPath)
			return m, nil
		}
		m.err = nil
		m.status = msg.status
		return m, nil
	case runtimeActionMsg:
		if msg.err != nil {
			m.reportError("Runtime action failed", msg.err, msg.projectPath)
			return m, nil
		}
		m.err = nil
		if strings.TrimSpace(msg.status) != "" {
			m.status = msg.status
		}
		selectedPath := msg.projectPath
		if selectedPath == "" {
			if p, ok := m.selectedProject(); ok {
				selectedPath = p.Path
			}
		}
		m.rebuildProjectList(selectedPath)
		m.syncRuntimeViewport(false)
		return m, m.requestRuntimeSnapshotsRefreshCmd()
	case runtimeSnapshotsMsg:
		reloadCmd := m.finishRuntimeSnapshotsRefreshCmd()
		prevSnapshots := m.runtimeSnapshots
		m.runtimeSnapshots = cloneRuntimeSnapshots(msg.snapshots)
		selectedPath := ""
		if p, ok := m.selectedProject(); ok {
			selectedPath = p.Path
		}
		if runtimeRunningStateChanged(prevSnapshots, m.runtimeSnapshots) {
			m.rebuildProjectList(selectedPath)
		}
		m.syncRuntimeViewport(false)
		return m, reloadCmd
	case runCommandSavedMsg:
		if m.runCommandDialog != nil && m.runCommandDialog.ProjectPath == msg.projectPath {
			m.runCommandDialog.Submitting = false
		}
		if msg.err != nil {
			m.reportError("Run command save failed", msg.err, msg.projectPath)
			return m, nil
		}
		m.closeRunCommandDialog("")
		refreshCmd := m.requestProjectInvalidationCmd(invalidateProjectData(msg.projectPath))
		if strings.TrimSpace(msg.command) != "" && msg.startAfter {
			return m, batchCmds(refreshCmd, m.startProjectRuntimeCmd(msg.projectPath, msg.command))
		} else {
			m.status = "Saved run command"
		}
		return m, refreshCmd
	case runCommandSuggestionMsg:
		dialog := m.runCommandDialog
		if dialog == nil || filepath.Clean(dialog.ProjectPath) != filepath.Clean(msg.projectPath) || dialog.SuggestionSeq != msg.seq {
			return m, nil
		}
		dialog.SuggestionPending = false
		if msg.err != nil {
			return m, nil
		}
		if strings.TrimSpace(dialog.Input.Value()) != "" {
			return m, nil
		}
		if strings.TrimSpace(msg.suggestion.Command) == "" {
			return m, nil
		}
		dialog.Input.SetValue(msg.suggestion.Command)
		dialog.Input.CursorEnd()
		dialog.SuggestionReason = strings.TrimSpace(msg.suggestion.Reason)
		return m, nil
	case todoActionMsg:
		if msg.err != nil {
			m.reportError("TODO action failed", msg.err, msg.projectPath)
			if m.todoDialog != nil && filepath.Clean(strings.TrimSpace(m.todoDialog.ProjectPath)) == filepath.Clean(strings.TrimSpace(msg.projectPath)) {
				m.todoDialog.Busy = false
			}
			if m.todoEditor != nil {
				m.todoEditor.Submitting = false
			}
			return m, nil
		}
		if m.todoDialog != nil && filepath.Clean(strings.TrimSpace(m.todoDialog.ProjectPath)) == filepath.Clean(strings.TrimSpace(msg.projectPath)) {
			m.todoDialog.Busy = false
		}
		if m.todoEditor != nil {
			m.todoEditor.Submitting = false
			m.todoEditor = nil
		}
		m.todoDeleteConfirm = nil
		m.err = nil
		if strings.TrimSpace(msg.status) != "" {
			m.status = msg.status
		}
		return m, m.requestProjectInvalidationCmd(invalidateProjectData(msg.projectPath))
	case todoWorktreeLaunchMsg:
		m.completeAILatencyOp(msg.perfOpID, msg.perfDuration, msg.err, msg.status)
		pendingCanceled := false
		if msg.launchID != 0 {
			pending := m.todoPendingLaunch
			if pending == nil || pending.ID != msg.launchID {
				return m, nil
			}
			pendingCanceled = pending.Canceled
			m.todoPendingLaunch = nil
		}
		if m.todoCopyDialog != nil && (msg.launchID == 0 || m.todoCopyDialog.LaunchID == 0 || m.todoCopyDialog.LaunchID == msg.launchID) {
			m.todoCopyDialog.Submitting = false
			m.todoCopyDialog.LaunchID = 0
		}
		if pendingCanceled {
			m.err = nil
			if msg.err == nil {
				m.status = "TODO start canceled after worktree creation"
				if m.svc == nil {
					return m, nil
				}
				return m, m.requestProjectInvalidationCmd(invalidateProjectStructure(m.detail.Summary.Path))
			}
			if errors.Is(msg.err, context.Canceled) {
				m.status = "TODO start canceled"
				return m, nil
			}
			m.status = "TODO start canceled"
			return m, nil
		}
		if msg.err != nil {
			m.todoLaunchDraft = nil
			m.reportError("TODO launch failed", msg.err, msg.projectPath)
			return m, nil
		}
		provider := msg.provider.Normalized()
		if provider == "" {
			provider = codexapp.ProviderCodex
		}
		m.todoDialog = nil
		m.todoCopyDialog = nil
		m.todoWorktreeEditor = nil
		m.todoExistingWorktree = nil
		m.err = nil
		if msg.openModelFirst {
			m.restoreCodexDraft(msg.projectPath, codexDraft{Text: msg.todoText})
		} else {
			m.clearCodexDraft(msg.projectPath)
		}
		m.todoLaunchDraft = &todoLaunchDraftState{
			projectPath:    msg.projectPath,
			provider:       provider,
			openModelFirst: msg.openModelFirst,
			autoSubmit:     !msg.openModelFirst,
		}
		req := codexapp.LaunchRequest{
			Provider:    provider,
			ProjectPath: strings.TrimSpace(msg.projectPath),
			ForceNew:    true,
			Preset:      m.currentCodexLaunchPreset(),
		}
		if !msg.openModelFirst {
			req.Prompt = msg.todoText
		}
		if err := req.Validate(); err != nil {
			m.todoLaunchDraft = nil
			m.reportError("Embedded session open failed", err, msg.projectPath)
			return m, nil
		}
		m.ensureCodexRuntime()
		m.beginCodexPendingOpenWithVisibility(req.ProjectPath, provider, msg.openModelFirst)
		if msg.openModelFirst {
			m.status = "Opening embedded " + provider.Label() + " session in new worktree..."
		} else {
			m.status = "Starting TODO in dedicated worktree..."
		}
		return m, batchCmds(
			m.requestProjectInvalidationCmd(invalidateProjectStructure(m.currentSelectedProjectPath())),
			m.openCodexSessionCmd(req),
		)
	case worktreeActionMsg:
		if msg.clearPendingGitSummary {
			if msg.err != nil {
				m.clearPendingGitSummary(msg.projectPath)
			} else {
				m.expirePendingGitSummaryOnRefresh(msg.projectPath)
			}
		}
		if msg.err != nil {
			m.reportError("Worktree action failed", msg.err, msg.projectPath)
			if m.worktreeMergeConfirm != nil && filepath.Clean(strings.TrimSpace(m.worktreeMergeConfirm.ProjectPath)) == filepath.Clean(strings.TrimSpace(msg.projectPath)) {
				m.worktreeMergeConfirm.Busy = false
				m.worktreeMergeConfirm.BusyMessage = ""
				m.worktreeMergeConfirm.ErrorMessage = msg.err.Error()
				m.worktreePostMerge = nil
				m.worktreeRemoveConfirm = nil
				return m, nil
			}
			if m.worktreePostMerge != nil && filepath.Clean(strings.TrimSpace(m.worktreePostMerge.ProjectPath)) == filepath.Clean(strings.TrimSpace(msg.projectPath)) {
				m.worktreePostMerge.Busy = false
				m.worktreePostMerge.BusyTitle = ""
				m.worktreePostMerge.BusyMessage = ""
				m.worktreePostMerge.ErrorMessage = msg.err.Error()
				return m, nil
			}
			m.worktreeMergeConfirm = nil
			m.worktreePostMerge = nil
			m.worktreeRemoveConfirm = nil
			return m, nil
		}
		m.worktreeMergeConfirm = nil
		m.worktreePostMerge = nil
		m.worktreeRemoveConfirm = nil
		m.err = nil
		if strings.TrimSpace(msg.selectPath) != "" {
			m.preferredSelectPath = strings.TrimSpace(msg.selectPath)
		}
		if strings.TrimSpace(msg.status) != "" {
			m.status = msg.status
		}
		if msg.offerPostMergeCleanup {
			m.worktreePostMerge = &worktreePostMergeState{
				ProjectPath:  strings.TrimSpace(msg.projectPath),
				RootPath:     strings.TrimSpace(msg.postMergeRootPath),
				BranchName:   strings.TrimSpace(msg.postMergeSourceBranch),
				TargetBranch: strings.TrimSpace(msg.postMergeTargetBranch),
				TodoID:       msg.postMergeTodoID,
				TodoText:     strings.TrimSpace(msg.postMergeTodoText),
				TodoPath:     strings.TrimSpace(msg.postMergeTodoPath),
				Status:       strings.TrimSpace(msg.status),
				Selected:     defaultWorktreePostMergeSelection(msg.postMergeTodoID > 0),
			}
		}
		return m, m.requestProjectInvalidationCmd(invalidateProjectScan(msg.selectPath, false))
	case settingsSavedMsg:
		m.settingsSaving = false
		m.err = nil
		if msg.err != nil {
			m.reportError("Settings save failed", msg.err, "")
			return m, nil
		}
		if m.svc != nil {
			m.svc.ApplyEditableSettings(msg.settings)
		}
		selectedPath := ""
		if p, ok := m.selectedProject(); ok {
			selectedPath = p.Path
		}
		saved := cloneEditableSettings(msg.settings)
		m.settingsBaseline = &saved
		m.excludeProjectPatterns = append([]string(nil), msg.settings.ExcludeProjectPatterns...)
		m.privacyPatterns = append([]string(nil), msg.settings.PrivacyPatterns...)
		m.embeddedModelPrefs = embeddedModelPreferencesFromSettings(msg.settings)
		m.hideReasoningSections = msg.settings.HideReasoningSections
		m.settingsMode = false
		m.status = fmt.Sprintf("Settings saved to %s. Filters, API keys, local endpoints, and Codex launch mode apply now; the running scheduler keeps its current timing until the next launch of %s.", msg.path, brand.CLIName)
		m.rebuildProjectList(selectedPath)
		cmds := []tea.Cmd{m.refreshSetupSnapshotCmd(false)}
		if len(m.projects) > 0 {
			m.syncDetailViewport(false)
			cmds = append(cmds, m.requestSelectedProjectDetailViewCmd())
			return m, tea.Batch(cmds...)
		}
		m.detail = model.ProjectDetail{}
		m.syncDetailViewport(true)
		return m, tea.Batch(cmds...)
	case setupSavedMsg:
		m.setupSaving = false
		m.err = nil
		if msg.err != nil {
			m.reportError("AI setup save failed", msg.err, "")
			return m, nil
		}
		if m.svc != nil {
			m.svc.ApplyEditableSettings(msg.settings)
		}
		saved := cloneEditableSettings(msg.settings)
		m.settingsBaseline = &saved
		m.embeddedModelPrefs = embeddedModelPreferencesFromSettings(msg.settings)
		m.setupSnapshot.Selected = msg.settings.AIBackend
		m.setupMode = false
		m.status = fmt.Sprintf("AI setup saved to %s. %s is now selected.", msg.path, msg.settings.AIBackend.Label())
		return m, m.refreshSetupSnapshotCmd(false)
	case embeddedModelPreferencesSavedMsg:
		if msg.err != nil {
			m.reportError("Embedded model updated for this run; config save failed", msg.err, "")
			return m, nil
		}
		m.err = nil
		// Embedded model preferences are already persisted to disk and mirrored in
		// the TUI baseline below. Re-entering Service.ApplyEditableSettings here
		// can block Update behind long-running scan/worktree mutex holders and
		// freeze the UI during /model changes.
		saved := cloneEditableSettings(msg.settings)
		m.settingsBaseline = &saved
		m.embeddedModelPrefs = embeddedModelPreferencesFromSettings(msg.settings)
		return m, nil
	case privacyModeSavedMsg:
		if msg.err != nil {
			m.reportError("Privacy mode updated for this run; config save failed", msg.err, "")
			return m, nil
		}
		m.err = nil
		if m.settingsBaseline != nil {
			m.settingsBaseline.PrivacyMode = msg.privacyMode
		}
		return m, nil
	case ignoredProjectsMsg:
		if msg.err != nil {
			m.closeIgnoredPicker(msg.err.Error())
			return m, nil
		}
		if len(msg.items) == 0 {
			m.closeIgnoredPicker("No ignored projects")
			return m, nil
		}
		m.ignoredPickerLoading = false
		m.ignoredPickerItems = append([]model.IgnoredProjectName(nil), msg.items...)
		if m.ignoredPickerSelected >= len(m.ignoredPickerItems) {
			m.ignoredPickerSelected = len(m.ignoredPickerItems) - 1
		}
		if m.ignoredPickerSelected < 0 {
			m.ignoredPickerSelected = 0
		}
		m.status = "Ignored projects"
		return m, nil
	case ignoredProjectActionMsg:
		if msg.err != nil {
			m.reportError("Ignore action failed", msg.err, "")
			return m, nil
		}
		m.status = msg.status
		cmds := []tea.Cmd{m.requestProjectInvalidationCmd(invalidateProjectStructure(""))}
		if m.ignoredPickerVisible {
			cmds = append(cmds, m.loadIgnoredProjectsCmd())
		}
		return m, batchCmds(cmds...)
	case codexSessionOpenedMsg:
		m.completeAILatencyOp(msg.perfOpID, msg.perfDuration, msg.err, msg.status)
		m.err = nil
		if msg.err != nil {
			provider := m.codexPendingOpenProvider()
			m.finishCodexPendingOpen(msg.projectPath, codexapp.Snapshot{}, false, false)
			m.todoLaunchDraft = nil
			if projectPath := strings.TrimSpace(msg.projectPath); projectPath != "" {
				shouldShowFailure := true
				if snapshot, ok := m.codexCachedSnapshot(projectPath); ok {
					shouldShowFailure = snapshot.Closed
				} else if _, ok := m.codexSession(projectPath); ok {
					shouldShowFailure = false
				}
				if shouldShowFailure {
					m.showEmbeddedOpenFailure(msg.projectPath, provider, msg.err)
				}
			}
			m.reportError("Embedded session open failed", msg.err, msg.projectPath)
			return m, nil
		}
		revealOnOpen := true
		focusInput := true
		if m.todoLaunchDraft != nil && strings.TrimSpace(m.todoLaunchDraft.projectPath) == strings.TrimSpace(msg.projectPath) {
			if m.todoLaunchDraft.openModelFirst {
				focusInput = false
			} else if m.todoLaunchDraft.autoSubmit {
				revealOnOpen = false
				focusInput = false
			}
		}
		seenCmd := m.finishCodexPendingOpen(msg.projectPath, msg.snapshot, true, revealOnOpen)
		if m.todoLaunchDraft != nil && strings.TrimSpace(m.todoLaunchDraft.projectPath) == strings.TrimSpace(msg.projectPath) {
			draft := m.todoLaunchDraft
			m.todoLaunchDraft = nil
			if draft.openModelFirst {
				m.openCodexModelPickerLoading()
				m.status = "Pick a model, then send the TODO draft."
				return m, tea.Batch(seenCmd, m.openCodexModelPickerCmd())
			}
			if draft.autoSubmit {
				if strings.TrimSpace(msg.status) != "" {
					m.status = msg.status
				} else {
					m.status = "Started TODO in background"
				}
			} else {
				m.status = "Fresh " + draft.provider.Label() + " session ready with TODO draft. Edit and press Enter to send."
			}
		} else {
			m.status = msg.status
		}
		if focusInput {
			return m, tea.Batch(seenCmd, m.codexInput.Focus())
		}
		return m, seenCmd
	case codexActionMsg:
		m.completeAILatencyOp(msg.perfOpID, msg.perfDuration, msg.err, msg.status)
		if msg.err != nil {
			m.reportError("Embedded session action failed", msg.err, msg.projectPath)
			if msg.projectPath != "" && !msg.restoreDraft.Empty() {
				m.restoreCodexDraft(msg.projectPath, msg.restoreDraft)
			}
			return m, nil
		}
		m.err = nil
		if msg.status != "" {
			m.status = msg.status
		}
		if msg.provider.Normalized() != "" && (strings.TrimSpace(msg.model) != "" || strings.TrimSpace(msg.reasoning) != "") {
			var asyncCmd tea.Cmd
			if msg.awaitSettle {
				m.beginModelSettleLatency(msg.projectPath, strings.TrimSpace(msg.provider.Label()+" "+msg.model+" "+msg.reasoning), msg.model, msg.reasoning)
				snapshot, ok, needsAsync := m.refreshCodexSnapshot(msg.projectPath)
				if ok {
					m.completeModelSettleLatency(msg.projectPath, snapshot)
				}
				if needsAsync {
					asyncCmd = m.deferredCodexSnapshotCmd(msg.projectPath)
				}
			}
			m.rememberEmbeddedModelPreference(msg.provider, msg.model, msg.reasoning)
			m.recordRecentModel(msg.provider, msg.model)
			m.returnToTodoFromModelPicker()
			if strings.TrimSpace(m.codexVisibleProject) == strings.TrimSpace(msg.projectPath) && m.todoDialog == nil && m.todoCopyDialog == nil {
				return m, tea.Batch(asyncCmd, m.saveEmbeddedModelPreferencesCmd(), m.codexInput.Focus())
			}
			return m, tea.Batch(asyncCmd, m.saveEmbeddedModelPreferencesCmd())
		}
		if msg.closed {
			m.cancelModelSettleLatency(msg.projectPath, "session closed")
			delete(m.codexClosedHandled, msg.projectPath)
			if m.codexVisibleProject == msg.projectPath {
				m.codexVisibleProject = ""
				m.codexInput.Blur()
				m.syncDetailViewport(false)
			}
			if m.codexHiddenProject == msg.projectPath {
				m.codexHiddenProject = ""
			}
			return m, m.requestProjectInvalidationCmd(invalidateProjectScan(m.currentSelectedProjectPath(), false))
		}
		return m, nil
	case codexModelListMsg:
		result := msg.statusSummary()
		if strings.TrimSpace(msg.projectPath) != strings.TrimSpace(m.codexVisibleProject) {
			if result == "" {
				result = "stale"
			}
			m.completeAILatencyOp(msg.perfOpID, msg.perfDuration, msg.err, result)
			return m, nil
		}
		m.completeAILatencyOp(msg.perfOpID, msg.perfDuration, msg.err, result)
		if msg.err != nil {
			m.codexModelPicker = nil
			m.reportError("Embedded model picker failed", msg.err, msg.projectPath)
			m.returnToTodoFromModelPicker()
			return m, nil
		}
		m.err = nil
		m.openLoadedCodexModelPicker(msg.models)
		return m, nil
	case codexResumeChoicesMsg:
		return m.applyCodexResumeChoices(msg)
	case busMsg:
		cmds := []tea.Cmd{m.waitBusCmd()}
		switch msg.Type {
		case events.ClassificationUpdated:
			if msg.Payload["status"] == "completed" {
				m.markAssessmentFlash(msg.ProjectPath, msg.At)
			}
			cmds = append(cmds, m.requestProjectInvalidationCmd(invalidateProjectData(msg.ProjectPath)))
			return m, batchCmds(cmds...)
		case events.ProjectChanged, events.ActionApplied:
			if strings.TrimSpace(msg.ProjectPath) != "" {
				cmds = append(cmds, m.requestProjectInvalidationCmd(invalidateProjectData(msg.ProjectPath)))
			} else {
				cmds = append(cmds, m.requestProjectInvalidationCmd(invalidateProjectStructure("")))
			}
			return m, batchCmds(cmds...)
		case events.ProjectMoved, events.ScanCompleted:
			cmds = append(cmds, m.requestProjectInvalidationCmd(invalidateProjectStructure(m.currentSelectedProjectPath())))
			return m, batchCmds(cmds...)
		}
		cmds = append(cmds, m.requestProjectInvalidationCmd(invalidateProjectStructure("")))
		return m, batchCmds(cmds...)
	case spinnerTickMsg:
		m.recordUIStallFromSpinnerTick(m.currentTime())
		m.spinnerFrame = (m.spinnerFrame + 1) % spinnerAnimationFrameWrap
		m.refreshUsagePulse()
		m.pruneTransientHighlights(m.currentTime())
		refreshCmd := tea.Cmd(nil)
		if m.spinnerFrame%runtimeSnapshotRefreshEveryTicks == 0 {
			refreshCmd = m.requestRuntimeSnapshotsRefreshCmd()
		}
		return m, batchCmds(spinnerTickCmd(), refreshCmd)
	case codexUpdateMsg:
		cmds := []tea.Cmd{m.waitCodexCmd()}
		if m.codexManager != nil {
			m.codexManager.AckUpdate(msg.projectPath)
		}
		prevSnapshot, hadPrevSnapshot := m.codexSnapshots[strings.TrimSpace(msg.projectPath)]
		refreshStarted := time.Now()
		snapshot, ok, needsAsync := m.refreshCodexSnapshot(msg.projectPath)
		refreshDuration := time.Since(refreshStarted)
		if needsAsync {
			cmds = append(cmds, m.deferredCodexSnapshotCmd(msg.projectPath))
		}
		providerLabel := ""
		transcriptChanged := false
		if ok {
			providerLabel = embeddedProvider(snapshot).Label()
			transcriptChanged = !hadPrevSnapshot || codexTranscriptStateChanged(prevSnapshot, snapshot)
		}
		m.recordAISyncLatency("Embedded snapshot", msg.projectPath, providerLabel, refreshDuration, "")
		if m.codexVisibleProject == msg.projectPath {
			viewportStarted := time.Now()
			m.resetCodexToolAnswerState(msg.projectPath)
			m.syncCodexViewport(transcriptChanged)
			m.recordAISyncLatency("Embedded viewport", msg.projectPath, providerLabel, time.Since(viewportStarted), "")
		}
		if ok {
			m.completeModelSettleLatency(msg.projectPath, snapshot)
			if !snapshot.Closed {
				m.markCodexSessionLive(msg.projectPath)
				m.detectQuestionNotification(msg.projectPath, snapshot)
				return m, tea.Batch(cmds...)
			}
			m.cancelModelSettleLatency(msg.projectPath, "session closed")
			if !m.markCodexSessionClosedHandled(msg.projectPath) {
				return m, tea.Batch(cmds...)
			}
			if m.codexHiddenProject == msg.projectPath {
				m.codexHiddenProject = ""
			}
			if m.codexVisibleProject == msg.projectPath && strings.TrimSpace(snapshot.Status) != "" {
				m.status = snapshot.Status
			} else if strings.TrimSpace(snapshot.Status) != "" {
				m.status = snapshot.Status
			}
			m.loading = true
			cmds = append(cmds, m.requestProjectInvalidationCmd(invalidateProjectScan("", false)))
		} else if !needsAsync {
			m.cancelModelSettleLatency(msg.projectPath, "stale")
			m.dropCodexSnapshot(msg.projectPath)
		}
		return m, tea.Batch(cmds...)
	case codexDeferredSnapshotMsg:
		// Deferred snapshot arrived from a background goroutine after
		// TrySnapshot encountered lock contention. Run the same follow-up
		// logic that codexUpdateMsg would have done with a fresh snapshot.
		projectPath := strings.TrimSpace(msg.projectPath)
		if projectPath == "" {
			return m, nil
		}
		prevSnapshot, hadPrev := m.codexSnapshots[projectPath]
		m.storeCodexSnapshot(projectPath, msg.snapshot)
		snapshot := msg.snapshot
		providerLabel := embeddedProvider(snapshot).Label()
		transcriptChanged := !hadPrev || codexTranscriptStateChanged(prevSnapshot, snapshot)
		if m.codexVisibleProject == projectPath {
			viewportStarted := time.Now()
			m.resetCodexToolAnswerState(projectPath)
			m.syncCodexViewport(transcriptChanged)
			m.recordAISyncLatency("Embedded viewport", projectPath, providerLabel, time.Since(viewportStarted), "deferred")
		}
		m.completeModelSettleLatency(projectPath, snapshot)
		if !snapshot.Closed {
			m.markCodexSessionLive(projectPath)
			m.detectQuestionNotification(projectPath, snapshot)
			return m, nil
		}
		m.cancelModelSettleLatency(projectPath, "session closed")
		if !m.markCodexSessionClosedHandled(projectPath) {
			return m, nil
		}
		if m.codexHiddenProject == projectPath {
			m.codexHiddenProject = ""
		}
		if m.codexVisibleProject == projectPath && strings.TrimSpace(snapshot.Status) != "" {
			m.status = snapshot.Status
		} else if strings.TrimSpace(snapshot.Status) != "" {
			m.status = snapshot.Status
		}
		m.loading = true
		return m, m.requestProjectInvalidationCmd(invalidateProjectScan("", false))
	}

	return m, nil
}

func (m Model) updateNormalMode(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	if m.pendingG {
		m.pendingG = false
		if msg.String() == "g" {
			if m.focusedPane == focusDetail {
				m.detailViewport.GotoTop()
				return m, nil
			}
			if m.focusedPane == focusRuntime {
				m.runtimeViewport.GotoTop()
				return m, nil
			}
			return m, m.moveSelectionTo(0)
		}
	}
	switch msg.String() {
	case "ctrl+c", "q":
		if m.codexManager != nil {
			_ = m.codexManager.CloseAll()
		}
		if m.runtimeManager != nil {
			_ = m.runtimeManager.CloseAll()
		}
		if m.unsub != nil {
			m.unsub()
		}
		return m, tea.Quit
	case "/":
		m.openCommandMode()
		return m, textinput.Blink
	case "tab":
		m.cyclePaneFocus(1)
		return m, nil
	case "shift+tab":
		m.cyclePaneFocus(-1)
		return m, nil
	case "?":
		m.showHelp = !m.showHelp
		if m.showHelp {
			m.status = "Help open. Press ? or Esc to close"
		} else {
			m.status = "Help closed"
		}
		return m, nil
	case "f":
		return m, m.openProjectFilterDialog()
	case "f3":
		return m.cycleCodexSession(1)
	case "alt+down":
		return m.openCodexPicker()
	case "alt+[":
		return m.cycleCodexSession(-1)
	case "alt+]":
		return m.cycleCodexSession(1)
	case "esc":
		if m.showHelp {
			m.showHelp = false
			m.status = "Help closed"
			return m, nil
		}
		if m.focusProjectsPane() {
			return m, nil
		}
	case "enter":
		if m.focusedPane == focusProjects {
			project, ok := m.selectedProject()
			if !ok {
				m.status = "No project selected"
				return m, nil
			}
			return m.launchEmbeddedForSelection(m.preferredEmbeddedProviderForProject(project), false, "")
		}
		if m.focusedPane == focusRuntime {
			return m, m.activateRuntimePaneAction()
		}
	case "up", "k":
		if m.focusedPane == focusDetail {
			m.detailViewport.LineUp(1)
			return m, nil
		}
		if m.focusedPane == focusRuntime {
			m.runtimeViewport.LineUp(1)
			return m, nil
		}
		return m, m.moveSelectionBy(-1)
	case "down", "j":
		if m.focusedPane == focusDetail {
			m.detailViewport.LineDown(1)
			return m, nil
		}
		if m.focusedPane == focusRuntime {
			m.runtimeViewport.LineDown(1)
			return m, nil
		}
		return m, m.moveSelectionBy(1)
	case "pgup":
		if m.focusedPane == focusDetail {
			m.detailViewport.PageUp()
			return m, nil
		}
		if m.focusedPane == focusRuntime {
			m.runtimeViewport.PageUp()
			return m, nil
		}
		return m, m.moveSelectionBy(-m.rowsVisible())
	case "pgdown":
		if m.focusedPane == focusDetail {
			m.detailViewport.PageDown()
			return m, nil
		}
		if m.focusedPane == focusRuntime {
			m.runtimeViewport.PageDown()
			return m, nil
		}
		return m, m.moveSelectionBy(m.rowsVisible())
	case "ctrl+u":
		if m.focusedPane == focusDetail {
			m.detailViewport.HalfPageUp()
			return m, nil
		}
		if m.focusedPane == focusRuntime {
			m.runtimeViewport.HalfPageUp()
			return m, nil
		}
		return m, m.moveSelectionBy(-m.rowsVisible())
	case "ctrl+d":
		if m.focusedPane == focusDetail {
			m.detailViewport.HalfPageDown()
			return m, nil
		}
		if m.focusedPane == focusRuntime {
			m.runtimeViewport.HalfPageDown()
			return m, nil
		}
		return m, m.moveSelectionBy(m.rowsVisible())
	case "home":
		if m.focusedPane == focusDetail {
			m.detailViewport.GotoTop()
			return m, nil
		}
		if m.focusedPane == focusRuntime {
			m.runtimeViewport.GotoTop()
			return m, nil
		}
		return m, m.moveSelectionTo(0)
	case "end", "G":
		if m.focusedPane == focusDetail {
			m.detailViewport.GotoBottom()
			return m, nil
		}
		if m.focusedPane == focusRuntime {
			m.runtimeViewport.GotoBottom()
			return m, nil
		}
		return m, m.moveSelectionTo(max(0, len(m.projects)-1))
	case "g":
		m.pendingG = true
		return m, nil
	case "left", "h":
		if m.focusedPane == focusRuntime {
			m.moveRuntimeActionSelection(-1)
			return m, nil
		}
		if m.focusedPane == focusProjects {
			if row, project, ok := m.selectedProjectRow(); ok {
				if row.Kind == projectListRowWorktree {
					if m.worktreeExpanded == nil {
						m.worktreeExpanded = map[string]bool{}
					}
					m.worktreeExpanded[row.RootPath] = false
					m.rebuildProjectList(projectWorktreeRootPath(project))
					m.status = "Worktrees collapsed"
					return m, m.requestProjectDetailViewCmd(projectWorktreeRootPath(project))
				}
				if row.Kind == projectListRowRepo && row.LinkedCount > 0 && row.Expanded {
					return m, m.toggleSelectedWorktreeGroup()
				}
			}
		}
	case "right", "l":
		if m.focusedPane == focusRuntime {
			m.moveRuntimeActionSelection(1)
			return m, nil
		}
		if m.focusedPane == focusProjects {
			if row, _, ok := m.selectedProjectRow(); ok && row.Kind == projectListRowRepo && row.LinkedCount > 0 && !row.Expanded {
				return m, m.toggleSelectedWorktreeGroup()
			}
		}
	case "o":
		if m.focusedPane == focusRuntime {
			return m, nil
		}
		if m.sortMode == sortByAttention {
			return m, m.setSortMode(sortByRecent)
		}
		return m, m.setSortMode(sortByAttention)
	case "p":
		if p, ok := m.selectedProject(); ok {
			return m, m.togglePinCmd(p.Path)
		}
	case "t":
		return m, m.openTodoDialogForSelection()
	case "w":
		return m, m.toggleSelectedWorktreeGroup()
	case "M":
		return m, m.openWorktreeMergeConfirmForSelection()
	case "x":
		return m, m.openWorktreeRemoveConfirmForSelection()
	}
	return m, nil
}

func (m Model) updateCommandMode(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	switch msg.String() {
	case "esc":
		m.closeCommandMode("Command canceled")
		return m, nil
	case "up", "ctrl+p":
		m.moveCommandSelection(-1)
		return m, nil
	case "down", "ctrl+n":
		m.moveCommandSelection(1)
		return m, nil
	case "tab":
		if m.applySelectedCommandSuggestion() {
			return m, nil
		}
	case "shift+tab":
		m.moveCommandSelection(-1)
		return m, nil
	case "enter":
		raw := m.resolvedCommandInput()
		inv, err := commands.Parse(raw)
		if err != nil {
			m.err = nil
			m.status = err.Error()
			return m, nil
		}
		m.closeCommandMode("")
		m.err = nil
		return m.dispatchCommand(inv)
	}

	var cmd tea.Cmd
	m.commandInput, cmd = m.commandInput.Update(msg)
	m.syncCommandSelection()
	return m, cmd
}

func (m Model) updateCommitPreviewMode(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	if m.commitPreview == nil {
		return m, nil
	}
	if m.commitApplying || m.commitPreviewRefreshing {
		return m, nil
	}
	switch msg.String() {
	case "esc":
		m.commitPreview = nil
		m.commitTodoCompletions = nil
		m.commitPreviewMessageOverride = ""
		m.commitPreviewRefreshing = false
		m.commitApplying = false
		m.status = "Commit preview canceled"
		return m, nil
	case "d":
		cmd := m.startDiffViewFromCommitPreview(*m.commitPreview, m.commitPreviewMessageOverride)
		m.commitPreview = nil
		m.commitTodoCompletions = nil
		m.commitPreviewMessageOverride = ""
		m.commitPreviewRefreshing = false
		m.commitApplying = false
		return m, cmd
	case "up", "k":
		if len(m.commitTodoCompletions) > 0 && m.commitTodoSelected > 0 {
			m.commitTodoSelected--
		}
		return m, nil
	case "down", "j":
		if len(m.commitTodoCompletions) > 0 && m.commitTodoSelected < len(m.commitTodoCompletions)-1 {
			m.commitTodoSelected++
		}
		return m, nil
	case " ":
		if len(m.commitTodoCompletions) > 0 {
			m.commitTodoCompletions[m.commitTodoSelected].Selected = !m.commitTodoCompletions[m.commitTodoSelected].Selected
		}
		return m, nil
	case "shift+enter", "alt+enter":
		if !m.commitPreview.CanPush {
			m.status = "Commit & push is unavailable for this repo"
			return m, nil
		}
		m.commitApplying = true
		m.setPendingGitSummary(m.commitPreview.ProjectPath, "Committing and pushing...")
		m.status = "Committing and pushing..."
		return m, m.applyCommitPreviewCmd(*m.commitPreview, true)
	case "enter":
		m.commitApplying = true
		m.setPendingGitSummary(m.commitPreview.ProjectPath, "Committing...")
		m.status = "Committing..."
		return m, m.applyCommitPreviewCmd(*m.commitPreview, false)
	}
	return m, nil
}

func (m Model) updateGitStatusDialogMode(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	if m.gitStatusDialog == nil {
		return m, nil
	}
	if m.gitStatusApplying {
		return m, nil
	}
	switch msg.String() {
	case "esc":
		m.status = gitStatusDialogDismissStatus(*m.gitStatusDialog)
		m.gitStatusDialog = nil
		m.gitStatusApplying = false
		return m, nil
	case "enter":
		if m.gitStatusDialog.ResolveSubmodules {
			m.gitStatusApplying = true
			m.setPendingGitSummary(m.gitStatusDialog.ProjectPath, "Resolving submodule commits...")
			m.status = "Resolving submodule commits..."
			return m, m.resolveSubmodulesAndContinueCmd(m.gitStatusDialog.ProjectPath, m.gitStatusDialog.CommitIntent, m.gitStatusDialog.CommitMessage)
		}
		if !m.gitStatusDialog.CanPush {
			m.status = gitStatusDialogDismissStatus(*m.gitStatusDialog)
			m.gitStatusDialog = nil
			m.gitStatusApplying = false
			return m, nil
		}
		m.gitStatusApplying = true
		m.setPendingGitOperation(m.gitStatusDialog.ProjectPath, pendingGitOperationPush, "Pushing existing commits...")
		m.status = "Pushing existing commits..."
		return m, m.pushCmd(m.gitStatusDialog.ProjectPath)
	}
	return m, nil
}

func (m *Model) moveSelectionBy(delta int) tea.Cmd {
	if len(m.projects) == 0 || delta == 0 {
		return nil
	}
	return m.moveSelectionTo(m.selected + delta)
}

func (m *Model) moveSelectionTo(index int) tea.Cmd {
	if len(m.projects) == 0 {
		m.selected = 0
		m.detail = model.ProjectDetail{}
		m.syncDetailViewport(true)
		return nil
	}
	if index < 0 {
		index = 0
	}
	if index >= len(m.projects) {
		index = len(m.projects) - 1
	}
	if index == m.selected {
		return nil
	}
	m.selected = index
	m.ensureSelectionVisible()
	m.syncDetailViewport(true)
	return m.requestSelectedProjectDetailViewCmd()
}

func (m *Model) cyclePaneFocus(delta int) {
	order := []paneFocus{focusProjects, focusDetail, focusRuntime}
	current := 0
	for i, pane := range order {
		if pane == m.focusedPane {
			current = i
			break
		}
	}
	if delta == 0 {
		delta = 1
	}
	next := (current + delta) % len(order)
	if next < 0 {
		next += len(order)
	}
	m.focusedPane = order[next]
	m.status = focusedPaneStatus(m.focusedPane)
	m.ensureSelectionVisible()
	m.syncDetailViewport(false)
}

func (m *Model) focusProjectsPane() bool {
	if m.focusedPane == focusProjects {
		return false
	}
	m.focusedPane = focusProjects
	m.status = focusedPaneStatus(m.focusedPane)
	m.ensureSelectionVisible()
	m.syncDetailViewport(false)
	return true
}

func (m *Model) setFocusedPaneFromCommand(target commands.FocusTarget) {
	switch target {
	case commands.FocusDetail:
		m.focusedPane = focusDetail
	case commands.FocusRuntime:
		m.focusedPane = focusRuntime
	default:
		m.focusedPane = focusProjects
	}
	m.status = focusedPaneStatus(m.focusedPane)
	m.ensureSelectionVisible()
	m.syncDetailViewport(false)
}

func focusedPaneStatus(pane paneFocus) string {
	switch pane {
	case focusDetail:
		return "Focus: detail pane"
	case focusRuntime:
		return "Focus: runtime pane"
	default:
		return "Focus: project list"
	}
}

func (m *Model) setSortMode(mode projectSortMode) tea.Cmd {
	selectedPath := ""
	if p, ok := m.selectedProject(); ok {
		selectedPath = p.Path
	}
	m.sortMode = mode
	m.rebuildProjectList(selectedPath)
	m.syncDetailViewport(false)
	m.status = fmt.Sprintf("Sort: %s | View: %s", m.sortMode, visibilityLabel(m.visibility))
	if p, ok := m.selectedProject(); ok {
		return m.requestProjectDetailViewCmd(p.Path)
	}
	m.detail = model.ProjectDetail{}
	m.syncDetailViewport(true)
	return nil
}

func (m *Model) setVisibilityMode(mode projectVisibilityMode) tea.Cmd {
	selectedPath := ""
	if p, ok := m.selectedProject(); ok {
		selectedPath = p.Path
	}
	m.visibility = mode
	m.rebuildProjectList(selectedPath)
	m.syncDetailViewport(false)
	m.status = fmt.Sprintf("Visibility: %s", visibilityLabel(m.visibility))
	if p, ok := m.selectedProject(); ok {
		return m.requestProjectDetailViewCmd(p.Path)
	}
	m.detail = model.ProjectDetail{}
	m.syncDetailViewport(true)
	return nil
}

func (m *Model) applySectionToggle(label string, mode commands.ToggleMode, target *bool) {
	switch mode {
	case commands.ToggleOn:
		*target = true
	case commands.ToggleOff:
		*target = false
	default:
		*target = !*target
	}
	m.syncDetailViewport(false)
	if *target {
		m.status = label + " section shown"
		return
	}
	m.status = label + " section hidden"
}

func (m *Model) openCommandMode() {
	m.commandMode = true
	m.showHelp = false
	m.commandSelected = 0
	m.commandInput.Focus()
	m.commandInput.SetValue("/")
	m.commandInput.CursorEnd()
	m.syncCommandInputWidth()
	m.syncCommandSelection()
	m.err = nil
	m.status = "Command palette open"
}

func (m *Model) closeCommandMode(status string) {
	m.commandMode = false
	m.commandSelected = 0
	m.commandInput.Blur()
	if status != "" {
		m.status = status
	}
}

func (m *Model) syncCommandInputWidth() {
	width := m.width
	if width <= 0 {
		width = 120
	}
	m.commandInput.Width = max(12, min(72, width-12))
}

func (m Model) commandSuggestions() []commands.Suggestion {
	return commands.Suggestions(m.commandInput.Value())
}

func (m *Model) syncCommandSelection() {
	suggestions := m.commandSuggestions()
	if len(suggestions) == 0 {
		m.commandSelected = 0
		return
	}
	if m.commandSelected < 0 {
		m.commandSelected = 0
	}
	if m.commandSelected >= len(suggestions) {
		m.commandSelected = len(suggestions) - 1
	}
}

func (m *Model) moveCommandSelection(delta int) {
	suggestions := m.commandSuggestions()
	if len(suggestions) == 0 || delta == 0 {
		return
	}
	m.commandSelected += delta
	if m.commandSelected < 0 {
		m.commandSelected = len(suggestions) - 1
	}
	if m.commandSelected >= len(suggestions) {
		m.commandSelected = 0
	}
}

func (m Model) selectedCommandSuggestion() (commands.Suggestion, bool) {
	suggestions := m.commandSuggestions()
	if len(suggestions) == 0 {
		return commands.Suggestion{}, false
	}
	index := m.commandSelected
	if index < 0 {
		index = 0
	}
	if index >= len(suggestions) {
		index = len(suggestions) - 1
	}
	return suggestions[index], true
}

func (m *Model) applySelectedCommandSuggestion() bool {
	suggestion, ok := m.selectedCommandSuggestion()
	if !ok {
		return false
	}
	m.commandInput.SetValue(suggestion.Insert)
	m.commandInput.CursorEnd()
	m.syncCommandSelection()
	return true
}

func (m Model) resolvedCommandInput() string {
	raw := strings.TrimSpace(m.commandInput.Value())
	if raw == "" {
		return raw
	}
	suggestion, ok := m.selectedCommandSuggestion()
	if ok {
		insert := strings.TrimSpace(suggestion.Insert)
		if strings.HasPrefix(strings.ToLower(insert), strings.ToLower(raw)) && !strings.EqualFold(insert, raw) {
			return suggestion.Insert
		}
	}
	if _, err := commands.Parse(raw); err == nil {
		return raw
	}
	if !ok {
		return raw
	}
	if strings.HasPrefix(strings.ToLower(suggestion.Insert), strings.ToLower(raw)) {
		return suggestion.Insert
	}
	return raw
}

func (m *Model) syncDetailViewport(reset bool) {
	done := m.beginUIPhase("syncDetailViewport", m.currentLatencyProjectPath(), fmt.Sprintf("reset=%t", reset))
	defer done()
	layout := m.bodyLayout()
	m.detailViewport.Width = layout.detailContentWidth
	m.detailViewport.Height = max(1, layout.bottomPaneHeight-2)
	if m.codexVisible() {
		if reset {
			m.detailViewport.GotoTop()
		}
		return
	}

	offset := m.detailViewport.YOffset
	m.detailViewport.SetContent(m.renderDetailContent(layout.detailContentWidth))
	if reset {
		m.detailViewport.GotoTop()
		m.syncRuntimeViewport(true)
		return
	}
	maxOffset := max(0, m.detailViewport.TotalLineCount()-m.detailViewport.Height)
	if offset > maxOffset {
		offset = maxOffset
	}
	m.detailViewport.SetYOffset(offset)
	m.syncRuntimeViewport(false)
}

func (m Model) renderDetailViewport(width, height int) string {
	if height <= 0 {
		return ""
	}

	view := m.detailViewport
	view.Width = max(1, width)
	view.Height = max(1, height)

	if m.detailViewport.Width != width || m.detailViewport.Height <= 0 {
		content := strings.ReplaceAll(m.renderDetailContent(width), "\r\n", "\n")
		view.SetContent(content)
	}

	maxOffset := max(0, view.TotalLineCount()-view.Height)
	if view.YOffset > maxOffset {
		view.SetYOffset(maxOffset)
	}
	if view.YOffset < 0 {
		view.SetYOffset(0)
	}
	return fitPaneContent(view.View(), width, height)
}

func (m Model) View() string {
	m.noteUIProgress("View")
	done := m.beginUIPhase("View", m.currentLatencyProjectPath(), "")
	defer done()
	if m.codexVisible() {
		body := m.renderCodexView()
		if m.codexModelPickerVisible() {
			width := m.width
			if width <= 0 {
				width = 120
			}
			height := m.height
			if height <= 0 {
				height = 30
			}
			return m.renderCodexModelPickerOverlay(body, width, height)
		}
		if m.codexPickerVisible {
			width := m.width
			if width <= 0 {
				width = 120
			}
			height := m.height
			if height <= 0 {
				height = 30
			}
			return m.renderCodexPickerOverlay(body, width, height)
		}
		return body
	}

	layout := m.bodyLayout()
	header := m.renderTopStatusLine(layout.width)
	if m.diffView != nil {
		return strings.Join([]string{header, m.renderDiffView(layout.width, layout.height), m.renderFooter(layout.width)}, "\n")
	}
	listHeight := max(1, layout.listPaneHeight-2)
	bottomHeight := max(1, layout.bottomPaneHeight-2)
	list := m.renderProjectList(layout.listContentWidth, listHeight)
	detail := m.renderDetailViewport(layout.detailContentWidth, bottomHeight)
	runtime := m.renderRuntimePanel(layout.runtimeContentWidth, bottomHeight)

	listPane := m.renderFramedPane(list, layout.width, listHeight, m.focusedPane == focusProjects)
	detailPane := m.renderFramedPane(detail, layout.detailPaneWidth, bottomHeight, m.focusedPane == focusDetail)
	runtimePane := m.renderFramedPane(runtime, layout.runtimePaneWidth, bottomHeight, m.focusedPane == focusRuntime)
	bottomRow := lipgloss.JoinHorizontal(lipgloss.Top, detailPane, " ", runtimePane)
	body := lipgloss.JoinVertical(lipgloss.Left, listPane, bottomRow)
	if m.gitStatusDialog != nil {
		body = m.renderGitStatusDialogOverlay(body, layout.width, layout.height)
	} else if m.commitPreview != nil {
		body = m.renderCommitPreviewOverlay(body, layout.width, layout.height)
	} else if m.newProjectDialog != nil {
		body = m.renderNewProjectOverlay(body, layout.width, layout.height)
	} else if m.runCommandDialog != nil {
		body = m.renderRunCommandOverlay(body, layout.width, layout.height)
	} else if m.setupMode {
		body = m.renderSetupOverlay(body, layout.width, layout.height)
	} else if m.settingsMode {
		body = m.renderSettingsOverlay(body, layout.width, layout.height)
	} else if m.showPerf {
		body = m.renderPerfOverlay(body, layout.width, layout.height)
	} else if m.showAIStats {
		body = m.renderAIStatsOverlay(body, layout.width, layout.height)
	} else if m.showHelp {
		body = m.renderHelpPanelOverlay(body, layout.width, layout.height)
	} else if m.projectFilterDialog != nil {
		body = m.renderProjectFilterOverlay(body, layout.width, layout.height)
	} else if m.commandMode {
		body = m.renderCommandPaletteOverlay(body, layout.width, layout.height)
	} else if m.errorLogVisible {
		body = m.renderErrorLogOverlay(body, layout.width, layout.height)
	} else if m.codexPickerVisible {
		body = m.renderCodexPickerOverlay(body, layout.width, layout.height)
	} else if m.ignoredPickerVisible {
		body = m.renderIgnoredPickerOverlay(body, layout.width, layout.height)
	} else if m.questionNotify != nil {
		body = m.renderQuestionNotifyOverlay(body, layout.width, layout.height)
	}
	if m.todoDialog != nil {
		body = m.renderTodoDialogOverlay(body, layout.width, layout.height)
	}
	if m.todoEditor != nil {
		body = m.renderTodoEditorOverlay(body, layout.width, layout.height)
	}
	if m.todoDeleteConfirm != nil {
		body = m.renderTodoDeleteConfirmOverlay(body, layout.width, layout.height)
	}
	if m.todoExistingWorktree != nil {
		body = m.renderTodoExistingWorktreeOverlay(body, layout.width, layout.height)
	}
	if m.todoCopyDialog != nil {
		body = m.renderTodoCopyDialogOverlay(body, layout.width, layout.height)
	}
	if m.todoWorktreeEditor != nil {
		body = m.renderTodoWorktreeEditorOverlay(body, layout.width, layout.height)
	}
	if m.worktreeMergeConfirm != nil {
		body = m.renderWorktreeMergeConfirmOverlay(body, layout.width, layout.height)
	}
	if m.worktreePostMerge != nil {
		body = m.renderWorktreePostMergeOverlay(body, layout.width, layout.height)
	}
	if m.worktreeRemoveConfirm != nil {
		body = m.renderWorktreeRemoveConfirmOverlay(body, layout.width, layout.height)
	}
	if m.attentionDialog != nil {
		body = m.renderAttentionDialogOverlay(body, layout.width, layout.height)
	}

	return strings.Join([]string{header, body, m.renderFooter(layout.width)}, "\n")
}

func (m Model) bodyLayout() bodyLayout {
	width := m.width
	if width <= 0 {
		width = 120
	}

	height := m.height
	if height <= 0 {
		height = 30
	}
	bodyHeight := height - 2 // top line + footer
	if bodyHeight < 8 {
		bodyHeight = 8
	}

	listPaneHeight, bottomPaneHeight := splitBodyHeights(bodyHeight, m.focusedPane)
	detailPaneWidth, runtimePaneWidth := splitBottomPaneWidths(width, m.focusedPane)
	return bodyLayout{
		width:               width,
		height:              bodyHeight,
		listPaneHeight:      listPaneHeight,
		bottomPaneHeight:    bottomPaneHeight,
		listContentWidth:    max(24, width-4),
		detailPaneWidth:     detailPaneWidth,
		runtimePaneWidth:    runtimePaneWidth,
		detailContentWidth:  max(20, detailPaneWidth-4),
		runtimeContentWidth: max(18, runtimePaneWidth-4),
	}
}

func splitBodyHeights(bodyHeight int, focused paneFocus) (int, int) {
	if bodyHeight < 8 {
		bodyHeight = 8
	}

	listHeight := (bodyHeight * 3) / 5
	bottomHeight := bodyHeight - listHeight
	if focused == focusDetail || focused == focusRuntime {
		bottomHeight = (bodyHeight * 13) / 20
		listHeight = bodyHeight - bottomHeight
	}

	if listHeight < 6 {
		listHeight = 6
		bottomHeight = bodyHeight - listHeight
	}
	if bottomHeight < 6 {
		bottomHeight = 6
		listHeight = bodyHeight - bottomHeight
	}
	return listHeight, bottomHeight
}

func splitBottomPaneWidths(totalWidth int, focused paneFocus) (int, int) {
	if totalWidth <= 0 {
		totalWidth = 120
	}
	gap := 1
	available := max(2, totalWidth-gap)
	detailWidth := (available * 3) / 5
	switch focused {
	case focusDetail:
		detailWidth = (available * 17) / 25
	case focusRuntime:
		detailWidth = (available * 2) / 5
	}
	runtimeWidth := available - detailWidth

	minDetail := min(available-18, 28)
	if minDetail < 18 {
		minDetail = 18
	}
	minRuntime := min(available-18, 24)
	if minRuntime < 18 {
		minRuntime = 18
	}

	if detailWidth < minDetail {
		detailWidth = minDetail
		runtimeWidth = available - detailWidth
	}
	if runtimeWidth < minRuntime {
		runtimeWidth = minRuntime
		detailWidth = available - runtimeWidth
	}
	if detailWidth < 18 {
		detailWidth = max(18, available/2)
		runtimeWidth = available - detailWidth
	}
	if runtimeWidth < 18 {
		runtimeWidth = max(18, available/2)
		detailWidth = available - runtimeWidth
	}
	return detailWidth, runtimeWidth
}

func (m Model) renderTopStatusLine(width int) string {
	title := lipgloss.NewStyle().Bold(true).Foreground(lipgloss.Color("81")).Render(brand.Name)
	rawStatus := singleLineStatusText(m.status)
	status := rawStatus
	if m.err != nil {
		errText := singleLineStatusText(m.err.Error())
		if status == "" {
			status = "error: " + errText
		} else {
			status = fmt.Sprintf("%s | error: %s", status, errText)
		}
	}

	statusParts := make([]string, 0, 4)
	if strings.TrimSpace(status) != "" {
		statusParts = append(statusParts, m.renderTopStatusMessage(rawStatus, status))
	}
	if aiNotice := m.renderAIBackendStatusNotice(); aiNotice != "" {
		statusParts = append(statusParts, aiNotice)
	}
	if project, ok := m.selectedProject(); ok && project.RepoConflict {
		statusParts = append(statusParts, topStatusConflictBadgeStyle.Render("MERGE CONFLICT"))
		statusParts = append(statusParts, detailConflictStyle.Render("selected repo has unmerged files"))
	}

	segments := []string{title}
	if actions := m.renderTopStatusActions(width); actions != "" {
		segments = append(segments, actions)
	}
	if len(statusParts) > 0 {
		segments = append(segments, joinFooterSegments(statusParts...))
	}
	return fitFooterWidth(strings.Join(segments, "  "), width)
}

type topStatusSeverity int

const (
	topStatusSeverityNormal topStatusSeverity = iota
	topStatusSeverityWarning
	topStatusSeverityDanger
)

func (m Model) renderTopStatusMessage(rawStatus, displayStatus string) string {
	displayStatus = strings.TrimSpace(displayStatus)
	if displayStatus == "" {
		return ""
	}

	switch topStatusSeverityForMessage(rawStatus, m.err) {
	case topStatusSeverityWarning:
		return renderTopStatusWarningMessage(displayStatus, m.spinnerFrame)
	case topStatusSeverityDanger:
		return renderTopStatusDangerMessage(displayStatus, m.spinnerFrame)
	default:
		return renderFooterStatus(displayStatus)
	}
}

// Until status updates carry structured severity, keep the top-banner alert rules
// focused on explicit action-required and failure messages that should stand out.
func topStatusSeverityForMessage(status string, err error) topStatusSeverity {
	if err != nil {
		return topStatusSeverityDanger
	}

	status = strings.TrimSpace(status)
	if status == "" {
		return topStatusSeverityNormal
	}

	lowerStatus := strings.ToLower(status)
	switch {
	case strings.Contains(lowerStatus, "failed"),
		strings.Contains(lowerStatus, "merge conflict"),
		strings.Contains(lowerStatus, " error"):
		return topStatusSeverityDanger
	case topStatusNeedsAttention(status):
		return topStatusSeverityWarning
	default:
		return topStatusSeverityNormal
	}
}

func topStatusNeedsAttention(status string) bool {
	for _, prefix := range []string{
		"Stop the runtime before ",
		"Close the embedded agent session before ",
		"A commit is still in progress.",
	} {
		if strings.HasPrefix(status, prefix) {
			return true
		}
	}

	for _, snippet := range []string{
		"Resolve or abort the in-progress Git operation before ",
		"Commit or discard changes before ",
		"Switch it to ",
	} {
		if strings.Contains(status, snippet) {
			return true
		}
	}

	return false
}

func renderTopStatusWarningMessage(text string, spinnerFrame int) string {
	text = strings.TrimSpace(text)
	if text == "" {
		return ""
	}
	if spinnerFrame%2 == 0 {
		return topStatusWarningBadgeStyle.Render(text)
	}
	return topStatusWarningPulseBadgeStyle.Render(text)
}

func renderTopStatusDangerMessage(text string, spinnerFrame int) string {
	text = strings.TrimSpace(text)
	if text == "" {
		return ""
	}
	if spinnerFrame%2 == 0 {
		return topStatusDangerBadgeStyle.Render(text)
	}
	return topStatusDangerPulseBadgeStyle.Render(text)
}

func (m Model) renderTopStatusActions(width int) string {
	if width < 72 {
		return ""
	}
	actions := []footerAction{
		footerNavAction("f", "filter"),
		footerNavAction("/", "command"),
	}
	if len(m.errorLogEntries) > 0 && width >= 112 {
		actions = append(actions, footerNavAction("/errors", "log"))
	}
	if width >= 96 {
		actions = append(actions, footerNavAction("Tab", "switch"))
	}
	return renderFooterActionList(actions...)
}

func paneBoxStyle(focused bool) lipgloss.Style {
	borderColor := lipgloss.Color("238")
	if focused {
		borderColor = lipgloss.Color("81")
	}
	return lipgloss.NewStyle().
		Border(lipgloss.RoundedBorder()).
		BorderForeground(borderColor).
		Padding(0, 1)
}

func (m Model) renderFramedPane(content string, width, innerHeight int, focused bool) string {
	contentWidth := max(0, width-4)
	content = fitPaneContent(content, contentWidth, innerHeight)
	return paneBoxStyle(focused).Render(content)
}

func hFramedPaneStyle(focused bool) lipgloss.Style {
	borderColor := lipgloss.Color("238")
	if focused {
		borderColor = lipgloss.Color("81")
	}
	return lipgloss.NewStyle().
		Border(lipgloss.RoundedBorder()).
		BorderForeground(borderColor).
		BorderLeft(false).
		BorderRight(false)
}

func (m Model) renderHFramedPane(content string, width, innerHeight int, focused bool) string {
	content = fitPaneContent(content, width, innerHeight)
	return hFramedPaneStyle(focused).Width(width).Render(content)
}

func (m Model) selectedProject() (model.ProjectSummary, bool) {
	if m.selected < 0 || m.selected >= len(m.projects) {
		return model.ProjectSummary{}, false
	}
	return m.projects[m.selected], true
}

func (m Model) projectHasLiveCodexSession(projectPath string) bool {
	snapshot, ok := m.liveCodexSnapshot(projectPath)
	if !ok {
		return false
	}
	return snapshot.Started && !snapshot.Closed
}

func (m Model) liveCodexSnapshot(projectPath string) (codexapp.Snapshot, bool) {
	snapshot, ok := m.nonBlockingCodexSnapshot(projectPath)
	if !ok {
		return codexapp.Snapshot{}, false
	}
	if !snapshot.Started || snapshot.Closed {
		return codexapp.Snapshot{}, false
	}
	return snapshot, true
}

func (m Model) renderProjectList(width, height int) string {
	if len(m.projects) == 0 {
		if m.loading {
			return "Loading..."
		}
		if filterLabel := m.projectFilterSummaryLabel(24); filterLabel != "" {
			return fmt.Sprintf("No projects match %s\nPress f or /filter to change it", filterLabel)
		}
		if len(m.allProjects) > 0 && m.visibility == visibilityAIFolders {
			return "No AI-linked folders\nPress v for All folders"
		}
		return "No projects detected"
	}

	if height < 3 {
		height = 3
	}
	visible := height - 1 // minus header
	if visible < 1 {
		visible = 1
	}

	metaParts := []string{
		fmt.Sprintf("sort=%s", m.sortMode),
		fmt.Sprintf("view=%s", visibilityShortLabel(m.visibility)),
	}
	filterLabel := m.projectFilterSummaryLabel(16)
	if filterLabel != "" {
		metaParts = append(metaParts, "filter:"+filterLabel)
	}
	if m.privacyMode {
		metaParts = append(metaParts, "privacy")
	}
	meta := "  (" + strings.Join(metaParts, " ") + ")"
	columnWidth := width
	if filterLabel != "" {
		if reserved := lipgloss.Width(meta); reserved > 0 && width > reserved+53 {
			columnWidth = width - reserved
		}
	}
	projectW, assessmentW := projectListColumnWidths(columnWidth)
	rows := make([]string, 0, visible+2)
	header := renderProjectListHeader(projectW, assessmentW)
	if lipgloss.Width(header)+lipgloss.Width(meta) <= width {
		header += meta
	}
	if width > 0 {
		header = fitStyledWidth(header, width)
	}
	rows = append(rows, header)
	now := m.currentTime()

	selected := m.selected
	if selected < 0 {
		selected = 0
	}
	if selected >= len(m.projects) {
		selected = len(m.projects) - 1
	}

	start := m.offset
	if start < 0 {
		start = 0
	}
	maxOffset := max(0, len(m.projects)-visible)
	if start > maxOffset {
		start = maxOffset
	}
	if selected < start {
		start = selected
	}
	if selected >= start+visible {
		start = selected - visible + 1
	}

	end := min(len(m.projects), start+visible)
	for i := start; i < end; i++ {
		p := m.projects[i]
		rowMeta := projectListRow{
			Kind:        projectListRowStandalone,
			ProjectPath: p.Path,
			RootPath:    projectWorktreeRootPath(p),
		}
		if i >= 0 && i < len(m.projectRows) {
			rowMeta = m.projectRows[i]
		}
		selectedRow := i == m.selected
		cellStyle := func(style lipgloss.Style) lipgloss.Style {
			style = projectListCellStyle(style, selectedRow)
			if m.projectApprovalPulseActive(p.Path) {
				style = approvalPulseStyle(style)
			} else if m.projectQuestionPulseActive(p.Path) {
				style = questionPulseStyle(style)
			}
			return style
		}
		last := formatListActivityTime(now, p.LastActivity)
		flagIndicators := m.projectRepoWarningIndicator(p, m.spinnerFrame) + projectUnreadIndicator(p, now, m.assessmentStallThreshold())
		attention := projectAttentionLabelForScore(m.projectAttentionScore(p))
		name := p.Name
		assessmentText := m.projectAssessmentDisplayTextAt(p, now, m.assessmentStallThreshold())
		switch rowMeta.Kind {
		case projectListRowRepo:
			if rowMeta.LinkedCount > 0 {
				disclosure := "▸ "
				if rowMeta.Expanded {
					disclosure = "▾ "
				}
				name = disclosure + name
				if badge := worktreeLinkedBadgeSummary(rowMeta.LinkedCount, rowMeta.LinkedActiveCount, rowMeta.LinkedDirtyCount); badge != "" {
					if assessmentText == "-" {
						assessmentText = badge
					} else {
						assessmentText += "  " + badge
					}
				}
			}
		case projectListRowWorktree:
			name = "  ↳ " + projectWorktreeLabel(p)
		}
		name = truncateText(name, projectW)
		assessment := truncateText(assessmentText, assessmentW)
		runtimeSnapshot := m.projectRuntimeSnapshot(p.Path)
		agentLabel, agentTag, agentLive := m.projectAgentDisplay(p, now)
		todoCount := projectTODOCountLabel(p.OpenTODOCount)
		runLabel, runState := projectRunSummary(runtimeSnapshot, p.RunCommand)
		row := lipgloss.JoinHorizontal(
			lipgloss.Top,
			flagIndicators+cellStyle(lipgloss.NewStyle().Width(4).Align(lipgloss.Right).Bold(selectedRow)).Render(attention),
			" ",
			cellStyle(m.projectListAssessmentStatusStyle(p).Width(8)).Render(projectListStatusAt(p, now, m.assessmentStallThreshold())),
			" ",
			cellStyle(lipgloss.NewStyle().Width(10)).Render(last),
			" ",
			cellStyle(sourceStyleForTag(agentTag, agentLive).Width(projectListAgentWidth).Align(lipgloss.Left)).Render(truncateText(agentLabel, projectListAgentWidth)),
			" ",
			cellStyle(todoListIndicatorStyle.Width(projectListTODOWidth).Align(lipgloss.Right)).Render(todoCount),
			" ",
			cellStyle(projectRunStyle(runState).Width(projectListRunWidth).Align(lipgloss.Left)).Render(truncateText(runLabel, projectListRunWidth)),
			"  ",
			cellStyle(lipgloss.NewStyle().Width(projectW).Bold(selectedRow)).Render(name),
			"  ",
			cellStyle(m.projectListAssessmentSummaryStyle(p).Width(assessmentW).Bold(selectedRow)).Render(assessment),
		)
		if width > 0 {
			row = fitStyledWidth(row, width)
		}
		if selectedRow {
			row = projectListSelectedRowStyle.Render(row)
		}
		rows = append(rows, row)
	}
	if end < len(m.projects) {
		rows = append(rows, fmt.Sprintf("... %d more rows", len(m.projects)-end))
	}
	return strings.Join(rows, "\n")
}

func (m Model) renderDetailContent(width int) string {
	done := m.beginUIPhase("renderDetailContent", m.currentLatencyProjectPath(), fmt.Sprintf("width=%d", width))
	defer done()
	p, ok := m.selectedProject()
	if !ok {
		if len(m.allProjects) > 0 && m.visibility == visibilityAIFolders {
			return "No AI-linked folder selected\nUse /view to switch folders"
		}
		return "Select a project"
	}
	d := m.detail
	if d.Summary.Path != "" && d.Summary.Path != p.Path {
		d = model.ProjectDetail{}
	}
	assessmentValue := assessmentDisplayStyle(p, m.currentTime(), m.assessmentStallThreshold()).Render(projectAssessmentLabelWithThreshold(p, m.currentTime(), m.assessmentStallThreshold()))
	statusValue := activityDisplayStyle(p).Render(projectActivityStatus(p))
	attentionValue := detailAttentionValueStyle.Render(fmt.Sprintf("%d", m.projectAttentionScore(p)))

	lines := []string{detailField("Path", detailValueStyle.Render(p.Path))}
	lines = append(lines, detailField("Assessment", assessmentValue))
	if shouldShowProjectActivity(p) {
		lines = append(lines, detailField("Activity", statusValue))
	}
	lines = append(lines, detailSectionStyle.Render("Session summary"))
	summaryText := m.projectAssessmentDisplayTextAt(p, m.currentTime(), m.assessmentStallThreshold())
	summaryStyle := detailValueStyle
	if projectAssessmentRefreshing(p) {
		summaryStyle = detailMutedStyle
	}
	if strings.TrimSpace(summaryText) == "" || summaryText == "-" {
		lines = append(lines, renderWrappedDetailBullet(detailMutedStyle, width, "not assessed yet"))
	} else {
		lines = append(lines, renderWrappedDetailBullet(summaryStyle, width, summaryText))
	}
	if projectMissing(p) {
		lines = append(lines, detailWarningStyle.Render("Folder: missing on disk"))
	}
	lastActivityValue := detailMutedStyle.Render("never")
	if !p.LastActivity.IsZero() {
		lastActivityValue = detailValueStyle.Render(p.LastActivity.Format(time.RFC3339))
	}
	if p.LatestSessionFormat != "" || !p.LastActivity.IsZero() {
		lastSourceValue := detailMutedStyle.Render("None")
		if p.LatestSessionFormat != "" {
			lastSourceValue = sourceStyle(p.LatestSessionFormat, m.projectHasLiveCodexSession(p.Path)).Render(sourceLabel(p.LatestSessionFormat))
		}
		lastActivityValue += "  " + lastSourceValue
	}
	lines = append(lines, detailField("Last activity", lastActivityValue))
	if p.MovedFromPath != "" && moveStatusActive(p.MovedAt, p.Path, p.LatestSessionDetectedProjectPath) {
		movedFields := []string{detailField("Moved from", detailValueStyle.Render(p.MovedFromPath))}
		if !p.MovedAt.IsZero() {
			movedFields = append(movedFields, detailField("Moved at", detailValueStyle.Render(p.MovedAt.Format(time.RFC3339))))
		}
		lines = appendDetailFields(lines, width, movedFields...)
	}
	lines = append(lines, detailField("Repo", m.repoCombinedDetailValue(p)))
	if p.RepoConflict {
		lines = append(lines, detailField("Conflict", repoConflictDetailValue(p)))
	}
	if p.WorktreeKind == model.WorktreeKindLinked {
		mergeBackValue := detailMutedStyle.Render("parent branch unavailable")
		targetBranch := strings.TrimSpace(p.WorktreeParentBranch)
		sourceBranch := strings.TrimSpace(p.RepoBranch)
		switch {
		case targetBranch == "":
		case sourceBranch != "" && sourceBranch != targetBranch:
			mergeBackValue = detailValueStyle.Render(sourceBranch + " -> " + targetBranch)
		default:
			mergeBackValue = detailValueStyle.Render(targetBranch)
		}
		lines = append(lines, detailField("Merge back", mergeBackValue))
		lines = append(lines, detailField("Merge status", worktreeMergeStatusDetailValue(p)))
	}
	lines = append(lines, detailField("Attention", attentionValue))
	family := m.worktreeFamily(projectWorktreeRootPath(p))
	if len(family) > 1 || p.WorktreeKind == model.WorktreeKindLinked {
		activeCount, dirtyCount := m.worktreeActivityCounts(family)
		lines = append(lines, detailField("Worktrees", detailValueStyle.Render(worktreeGroupSummary(family, activeCount, dirtyCount))))
		if projectIsWorktreeRoot(p) {
			lines = append(lines, detailSectionStyle.Render("Worktree lanes"))
			family = append([]model.ProjectSummary(nil), family...)
			sort.SliceStable(family, func(i, j int) bool {
				leftRoot := projectIsWorktreeRoot(family[i])
				rightRoot := projectIsWorktreeRoot(family[j])
				if leftRoot != rightRoot {
					return leftRoot
				}
				if !family[i].LastActivity.Equal(family[j].LastActivity) {
					return family[i].LastActivity.After(family[j].LastActivity)
				}
				return strings.ToLower(family[i].Path) < strings.ToLower(family[j].Path)
			})
			for _, member := range family {
				label := projectWorktreeLabel(member)
				if projectIsWorktreeRoot(member) {
					label = "root: " + label
				}
				statusParts := []string{}
				lineStyle := detailValueStyle
				if op, ok := m.pendingGitOperation(member.Path); ok {
					statusParts = append(statusParts, op.shortLabel())
					lineStyle = detailValueStyle
				} else if member.RepoConflict {
					statusParts = append(statusParts, "conflict")
					lineStyle = detailConflictStyle
				} else if member.RepoDirty {
					statusParts = append(statusParts, "dirty")
				} else {
					statusParts = append(statusParts, "clean")
				}
				if member.Status != model.StatusIdle {
					statusParts = append(statusParts, string(member.Status))
				}
				if m.projectHasLiveCodexSession(member.Path) {
					statusParts = append(statusParts, "agent")
				}
				if snapshot := m.projectRuntimeSnapshot(member.Path); snapshot.Running {
					statusParts = append(statusParts, "runtime")
				}
				if member.WorktreeKind == model.WorktreeKindLinked {
					switch member.WorktreeMergeStatus {
					case model.WorktreeMergeStatusMerged:
						statusParts = append(statusParts, "merged")
					case model.WorktreeMergeStatusNotMerged:
						statusParts = append(statusParts, "not merged")
					}
				}
				if filepath.Clean(member.Path) == filepath.Clean(p.Path) {
					statusParts = append(statusParts, "current")
				}
				lines = append(lines, renderWrappedDetailBullet(lineStyle, width, label+" · "+strings.Join(statusParts, ", ")))
			}
		}
		if hints := m.worktreeActionHints(p, family); len(hints) > 0 {
			lines = append(lines, detailSectionStyle.Render("Worktree actions"))
			for _, hint := range hints {
				lines = append(lines, renderWrappedDetailBullet(detailValueStyle, width, hint))
			}
		}
	}

	if p.SnoozedUntil != nil {
		lines = append(lines, detailField("Snoozed until", detailValueStyle.Render(p.SnoozedUntil.Format(time.RFC3339))))
	}
	todoProject := p
	if rootPath := projectWorktreeRootPath(p); rootPath != "" && filepath.Clean(rootPath) != filepath.Clean(p.Path) {
		if rootProject, ok := m.projectSummaryByPath(rootPath); ok {
			todoProject = rootProject
		}
	}
	lines = append(lines, detailSectionStyle.Render("TODO"))
	if todoProject.TotalTODOCount == 0 {
		lines = append(lines, detailMutedStyle.Render("No TODOs yet. Press t or run /todo."))
	} else {
		lines = append(lines, detailField("Counts", detailValueStyle.Render(fmt.Sprintf("%d open, %d total", todoProject.OpenTODOCount, todoProject.TotalTODOCount))))
		if filepath.Clean(todoProject.Path) != filepath.Clean(p.Path) {
			lines = append(lines, detailMutedStyle.Render("TODOs are repo-scoped. Press t to open the root repo list."))
		} else {
			openShown := 0
			for _, item := range d.Todos {
				if item.Done {
					continue
				}
				lines = append(lines, renderWrappedDetailBullet(detailValueStyle, width, "[ ] "+strings.TrimSpace(item.Text)))
				openShown++
				if openShown >= 5 {
					break
				}
			}
			if openShown == 0 {
				lines = append(lines, detailMutedStyle.Render("All TODOs are done. Press t or run /todo."))
			}
		}
	}

	lines = append(lines, detailSectionStyle.Render("Attention reasons"))
	reasons := m.projectAttentionReasons(p, d.Reasons)
	if len(reasons) == 0 {
		lines = append(lines, detailMutedStyle.Render("- none"))
	} else {
		for _, r := range reasons {
			lines = append(lines, detailReasonLine(r))
		}
	}

	if m.showSessions {
		lines = append(lines, detailSectionStyle.Render("Sessions"))
		if len(d.Sessions) == 0 {
			lines = append(lines, detailMutedStyle.Render("- none"))
		} else {
			limit := min(6, len(d.Sessions))
			for i := 0; i < limit; i++ {
				s := d.Sessions[i]
				lines = append(lines, detailValueStyle.Render(fmt.Sprintf("- %s | %s | errors=%d", shortID(s.SessionID), s.LastEventAt.Format("01-02 15:04"), s.ErrorCount)))
			}
		}
	}

	if m.showEvents {
		lines = append(lines, detailSectionStyle.Render("Recent events"))
		if len(d.RecentEvents) == 0 {
			lines = append(lines, detailMutedStyle.Render("- none"))
		} else {
			limit := min(8, len(d.RecentEvents))
			for i := 0; i < limit; i++ {
				e := d.RecentEvents[i]
				lines = append(lines, detailValueStyle.Render(fmt.Sprintf("- %s %s", e.At.Format("01-02 15:04"), e.Payload)))
			}
		}
	}

	content := strings.Join(lines, "\n")
	return fitPaneContent(content, width, len(strings.Split(content, "\n")))
}

func projectListCellStyle(style lipgloss.Style, selected bool) lipgloss.Style {
	if !selected {
		return style
	}
	return style.Inherit(projectListSelectedRowStyle)
}

func approvalPulseStyle(style lipgloss.Style) lipgloss.Style {
	return style.Foreground(lipgloss.Color("255")).Background(lipgloss.Color("160")).Bold(true)
}

func questionPulseStyle(style lipgloss.Style) lipgloss.Style {
	return style.Foreground(lipgloss.Color("255")).Background(lipgloss.Color("33")).Bold(true)
}

var spinnerFrames = []string{"|", "/", "-", `\`}

const (
	recentMoveWindow          = 24 * time.Hour
	spinnerAnimationFrameWrap = 4096
	assessmentFlashDuration   = time.Second
	projectListAgentWidth     = 10
	projectListTODOWidth      = 4
	projectListRunWidth       = 11
	usagePulseDuration        = 900 * time.Millisecond
)

var (
	dialogPanelBackground           = lipgloss.Color("235")
	dialogPanelBorderColor          = lipgloss.Color("81")
	dialogPanelFillReset            = "\x1b[48;5;235m"
	dialogPanelResetReplacer        = strings.NewReplacer("\x1b[0m", "\x1b[0m"+dialogPanelFillReset, "\x1b[m", "\x1b[m"+dialogPanelFillReset)
	dialogPanelFillStyle            = lipgloss.NewStyle().Background(dialogPanelBackground)
	detailLabelStyle                = lipgloss.NewStyle().Foreground(lipgloss.Color("81")).Bold(true)
	detailSectionStyle              = lipgloss.NewStyle().Foreground(lipgloss.Color("214")).Bold(true)
	detailValueStyle                = lipgloss.NewStyle().Foreground(lipgloss.Color("252"))
	detailMutedStyle                = lipgloss.NewStyle().Foreground(lipgloss.Color("244"))
	detailWarningStyle              = lipgloss.NewStyle().Foreground(lipgloss.Color("178")).Bold(true)
	detailDangerStyle               = lipgloss.NewStyle().Foreground(lipgloss.Color("203")).Bold(true)
	detailConflictStyle             = lipgloss.NewStyle().Foreground(lipgloss.Color("141")).Bold(true)
	topStatusWarningBadgeStyle      = lipgloss.NewStyle().Foreground(lipgloss.Color("16")).Background(lipgloss.Color("214")).Bold(true).Padding(0, 1)
	topStatusWarningPulseBadgeStyle = lipgloss.NewStyle().Foreground(lipgloss.Color("16")).Background(lipgloss.Color("172")).Bold(true).Padding(0, 1)
	topStatusDangerBadgeStyle       = lipgloss.NewStyle().Foreground(lipgloss.Color("255")).Background(lipgloss.Color("160")).Bold(true).Padding(0, 1)
	topStatusDangerPulseBadgeStyle  = lipgloss.NewStyle().Foreground(lipgloss.Color("255")).Background(lipgloss.Color("88")).Bold(true).Padding(0, 1)
	topStatusConflictBadgeStyle     = lipgloss.NewStyle().Foreground(lipgloss.Color("255")).Background(lipgloss.Color("92")).Bold(true).Padding(0, 1)
	topStatusSetupBadgeStyle        = lipgloss.NewStyle().Foreground(lipgloss.Color("16")).Background(lipgloss.Color("214")).Bold(true).Padding(0, 1)
	detailAttentionValueStyle       = lipgloss.NewStyle().Foreground(lipgloss.Color("229")).Bold(true)
	projectListSelectedRowStyle     = lipgloss.NewStyle().
					Background(lipgloss.AdaptiveColor{Light: "255", Dark: "236"})
	commandPaletteTitleStyle  = lipgloss.NewStyle().Foreground(lipgloss.Color("81")).Bold(true)
	commandPaletteHintStyle   = lipgloss.NewStyle().Foreground(lipgloss.Color("244"))
	commandPaletteRowStyle    = lipgloss.NewStyle().Foreground(lipgloss.Color("252"))
	commandPalettePickStyle   = lipgloss.NewStyle().Foreground(lipgloss.Color("153")).Bold(true)
	commandPaletteSelectStyle = lipgloss.NewStyle().Foreground(lipgloss.Color("230")).Background(lipgloss.Color("24")).Bold(true)
	commitPreviewInfoStyle    = lipgloss.NewStyle().Foreground(lipgloss.Color("252"))
	commitPreviewValueStyle   = lipgloss.NewStyle().Foreground(lipgloss.Color("229")).Bold(true)
	dialogProjectTitleStyle   = lipgloss.NewStyle().Foreground(lipgloss.Color("214")).Bold(true)
	commitActionKeyStyle      = lipgloss.NewStyle().Foreground(lipgloss.Color("16")).Background(lipgloss.Color("42")).Bold(true).Padding(0, 1)
	commitActionTextStyle     = lipgloss.NewStyle().Foreground(lipgloss.Color("120")).Bold(true)
	navigateActionKeyStyle    = lipgloss.NewStyle().Foreground(lipgloss.Color("16")).Background(lipgloss.Color("81")).Bold(true).Padding(0, 1)
	navigateActionTextStyle   = lipgloss.NewStyle().Foreground(lipgloss.Color("153")).Bold(true)
	pushActionKeyStyle        = lipgloss.NewStyle().Foreground(lipgloss.Color("16")).Background(lipgloss.Color("214")).Bold(true).Padding(0, 1)
	pushActionTextStyle       = lipgloss.NewStyle().Foreground(lipgloss.Color("222")).Bold(true)
	cancelActionKeyStyle      = lipgloss.NewStyle().Foreground(lipgloss.Color("255")).Background(lipgloss.Color("160")).Bold(true).Padding(0, 1)
	cancelActionTextStyle     = lipgloss.NewStyle().Foreground(lipgloss.Color("210")).Bold(true)
	disabledActionKeyStyle    = lipgloss.NewStyle().Foreground(lipgloss.Color("252")).Background(lipgloss.Color("238")).Bold(true).Padding(0, 1)
	disabledActionTextStyle   = lipgloss.NewStyle().Foreground(lipgloss.Color("244"))
)

const spinnerTickInterval = 120 * time.Millisecond
const runtimeSnapshotRefreshEveryTicks = 8

func spinnerTickCmd() tea.Cmd {
	return tea.Tick(spinnerTickInterval, func(time.Time) tea.Msg {
		return spinnerTickMsg{}
	})
}

func onOffLabel(v bool) string {
	if v {
		return "ON"
	}
	return "OFF"
}

func (m Model) waitBusCmd() tea.Cmd {
	return func() tea.Msg {
		evt, ok := <-m.busCh
		if !ok {
			return nil
		}
		return busMsg(evt)
	}
}

func (m Model) loadProjectsCmd() tea.Cmd {
	return func() tea.Msg {
		projects, err := m.svc.Store().ListProjects(m.ctx, false)
		if err != nil {
			return projectsMsg{err: err}
		}
		patterns, filterErr := config.LoadExcludeProjectPatterns(m.currentConfigPath(), m.excludeProjectPatterns)
		return projectsMsg{
			projects:               projects,
			excludeProjectPatterns: patterns,
			filterErr:              filterErr,
		}
	}
}

func (m Model) loadDetailCmd(path string) tea.Cmd {
	path = normalizeProjectPath(path)
	return func() tea.Msg {
		d, err := m.svc.Store().GetProjectDetail(m.ctx, path, 20)
		return detailMsg{path: path, detail: d, err: err}
	}
}

func (m Model) loadProjectSummaryCmd(path string) tea.Cmd {
	return func() tea.Msg {
		summary, err := m.svc.Store().GetProjectSummary(m.ctx, path, false)
		if err != nil {
			if errors.Is(err, sql.ErrNoRows) {
				return projectSummaryMsg{path: path}
			}
			return projectSummaryMsg{path: path, err: err}
		}
		return projectSummaryMsg{path: path, summary: summary, found: true}
	}
}

func (m Model) markProjectSessionSeenCmd(path string, seenAt time.Time) tea.Cmd {
	if m.svc == nil {
		return nil
	}
	path = filepath.Clean(strings.TrimSpace(path))
	if path == "" {
		return nil
	}
	if seenAt.IsZero() {
		seenAt = m.currentTime()
	}
	return func() tea.Msg {
		return projectSessionSeenMsg{
			path: path,
			err:  m.svc.MarkProjectSessionSeen(m.ctx, path, seenAt),
		}
	}
}

func (m Model) markProjectSessionUnreadCmd(path string) tea.Cmd {
	if m.svc == nil {
		return nil
	}
	path = filepath.Clean(strings.TrimSpace(path))
	if path == "" {
		return nil
	}
	return func() tea.Msg {
		return actionMsg{
			projectPath: path,
			status:      "Marked unread",
			err:         m.svc.MarkProjectSessionUnread(m.ctx, path),
		}
	}
}

func (m *Model) markProjectSessionSeen(projectPath string) tea.Cmd {
	projectPath = filepath.Clean(strings.TrimSpace(projectPath))
	if projectPath == "" {
		return nil
	}
	seenAt := m.currentTime()
	m.markProjectSessionSeenLocal(projectPath, seenAt)
	return m.markProjectSessionSeenCmd(projectPath, seenAt)
}

func (m *Model) markProjectSessionUnread(projectPath string) tea.Cmd {
	projectPath = filepath.Clean(strings.TrimSpace(projectPath))
	if projectPath == "" {
		return nil
	}
	m.clearProjectSessionSeenLocal(projectPath)
	return m.markProjectSessionUnreadCmd(projectPath)
}

func (m *Model) upsertProjectSummary(summary model.ProjectSummary) {
	path := filepath.Clean(strings.TrimSpace(summary.Path))
	if path == "" {
		return
	}
	summary.Path = path
	for i := range m.allProjects {
		if filepath.Clean(m.allProjects[i].Path) == path {
			m.allProjects[i] = summary
			return
		}
	}
	m.allProjects = append(m.allProjects, summary)
}

func (m *Model) removeProjectSummary(projectPath string) {
	path := filepath.Clean(strings.TrimSpace(projectPath))
	if path == "" || len(m.allProjects) == 0 {
		return
	}
	filtered := m.allProjects[:0]
	for _, project := range m.allProjects {
		if filepath.Clean(project.Path) == path {
			continue
		}
		filtered = append(filtered, project)
	}
	m.allProjects = filtered
}

func (m *Model) markProjectSessionSeenLocal(projectPath string, seenAt time.Time) {
	path := filepath.Clean(strings.TrimSpace(projectPath))
	if path == "" {
		return
	}
	if seenAt.IsZero() {
		seenAt = m.currentTime()
	}
	for i := range m.allProjects {
		if filepath.Clean(m.allProjects[i].Path) == path {
			m.allProjects[i].LastSessionSeenAt = seenAt
		}
	}
	for i := range m.projects {
		if filepath.Clean(m.projects[i].Path) == path {
			m.projects[i].LastSessionSeenAt = seenAt
		}
	}
	if filepath.Clean(m.detail.Summary.Path) == path {
		m.detail.Summary.LastSessionSeenAt = seenAt
	}
}

func (m *Model) clearProjectSessionSeenLocal(projectPath string) {
	path := filepath.Clean(strings.TrimSpace(projectPath))
	if path == "" {
		return
	}
	for i := range m.allProjects {
		if filepath.Clean(m.allProjects[i].Path) == path {
			m.allProjects[i].LastSessionSeenAt = time.Time{}
		}
	}
	for i := range m.projects {
		if filepath.Clean(m.projects[i].Path) == path {
			m.projects[i].LastSessionSeenAt = time.Time{}
		}
	}
	if filepath.Clean(m.detail.Summary.Path) == path {
		m.detail.Summary.LastSessionSeenAt = time.Time{}
	}
}

func (m Model) dispatchCommand(inv commands.Invocation) (tea.Model, tea.Cmd) {
	switch inv.Kind {
	case commands.KindHelp:
		m.showPerf = false
		m.showAIStats = false
		m.showHelp = true
		m.status = "Help open. Press ? or Esc to close"
		return m, nil
	case commands.KindAIStats:
		return m, m.openAIStatsDialog()
	case commands.KindPerf:
		return m, m.openPerfDialog()
	case commands.KindErrors:
		return m.openErrorLog()
	case commands.KindRefresh:
		m.loading = true
		m.status = "Scanning and retrying failed assessments..."
		return m, m.requestScanCmd(true)
	case commands.KindSort:
		return m, m.setSortMode(projectSortMode(inv.Sort))
	case commands.KindView:
		return m, m.setVisibilityMode(commandVisibilityMode(inv.View))
	case commands.KindSetup:
		return m, m.openSetupMode()
	case commands.KindSettings:
		return m, m.openSettingsMode()
	case commands.KindFilter:
		if inv.Clear {
			return m, m.setProjectFilter("")
		}
		if strings.TrimSpace(inv.Filter) != "" {
			return m, m.setProjectFilter(inv.Filter)
		}
		return m, m.openProjectFilterDialog()
	case commands.KindNewProject:
		return m, m.openNewProjectDialog()
	case commands.KindOpen:
		p, ok := m.selectedProject()
		if !ok {
			m.status = "No project selected"
			return m, nil
		}
		if !p.PresentOnDisk {
			m.status = "Open requires a folder present on disk"
			return m, nil
		}
		m.status = "Opening project in browser..."
		return m, m.openProjectDirInBrowserCmd(p.Path)
	case commands.KindRun:
		p, ok := m.selectedProject()
		if !ok {
			m.status = "No project selected"
			return m, nil
		}
		if !p.PresentOnDisk {
			m.status = "Run requires a folder present on disk"
			return m, nil
		}
		return m.handleRunCommand(p, inv.Command)
	case commands.KindRestart:
		p, ok := m.selectedProject()
		if !ok {
			m.status = "No project selected"
			return m, nil
		}
		if !p.PresentOnDisk {
			m.status = "Restart requires a folder present on disk"
			return m, nil
		}
		snapshot := m.projectRuntimeSnapshot(p.Path)
		command := effectiveRuntimeCommand(p.RunCommand, snapshot)
		if command == "" {
			m.status = "Runtime command is not set"
			return m, nil
		}
		m.status = "Restarting runtime..."
		return m, m.restartProjectRuntimeCmd(p.Path, command)
	case commands.KindRunEdit:
		p, ok := m.selectedProject()
		if !ok {
			m.status = "No project selected"
			return m, nil
		}
		if !p.PresentOnDisk {
			m.status = "Run command editing requires a folder present on disk"
			return m, nil
		}
		return m, m.openRunCommandDialog(p, false)
	case commands.KindRuntime:
		return m, m.openRuntimeInspectorForSelection()
	case commands.KindStop:
		p, ok := m.selectedProject()
		if !ok {
			m.status = "No project selected"
			return m, nil
		}
		return m, m.stopProjectRuntimeCmd(p.Path)
	case commands.KindDiff:
		p, ok := m.selectedProject()
		if !ok {
			m.status = "No project selected"
			return m, nil
		}
		if !p.PresentOnDisk {
			m.status = "Diff requires a folder present on disk"
			return m, nil
		}
		return m, m.startDiffView(p.Path, p.Name)
	case commands.KindCommit:
		p, ok := m.selectedProject()
		if !ok {
			m.status = "No project selected"
			return m, nil
		}
		return m, m.startCommitPreview(p, service.GitActionCommit, inv.Message)
	case commands.KindPush:
		p, ok := m.selectedProject()
		if !ok {
			m.status = "No project selected"
			return m, nil
		}
		m.setPendingGitOperation(p.Path, pendingGitOperationPush, "Pushing...")
		m.status = "Pushing..."
		return m, m.pushCmd(p.Path)
	case commands.KindCodex:
		return m.launchCodexForSelection(false, inv.Prompt)
	case commands.KindCodexNew:
		return m.launchCodexForSelection(true, inv.Prompt)
	case commands.KindClaude:
		return m.launchClaudeForSelection(false, inv.Prompt)
	case commands.KindClaudeNew:
		return m.launchClaudeForSelection(true, inv.Prompt)
	case commands.KindOpenCode:
		return m.launchOpenCodeForSelection(false, inv.Prompt)
	case commands.KindOpenCodeNew:
		return m.launchOpenCodeForSelection(true, inv.Prompt)
	case commands.KindTodo:
		p, ok := m.selectedProject()
		if !ok {
			m.status = "No project selected"
			return m, nil
		}
		return m, m.openTodoDialog(p)
	case commands.KindWorktreeLanes:
		return m, m.toggleSelectedWorktreeGroup()
	case commands.KindWorktreeMerge:
		return m, m.openWorktreeMergeConfirmForSelection()
	case commands.KindWorktreeRemove:
		return m, m.openWorktreeRemoveConfirmForSelection()
	case commands.KindWorktreePrune:
		row, project, ok := m.selectedProjectRow()
		if !ok {
			m.status = "No project selected"
			return m, nil
		}
		rootPath := row.RootPath
		if rootPath == "" {
			rootPath = projectWorktreeRootPath(project)
		}
		if rootPath == "" {
			m.status = "No project selected"
			return m, nil
		}
		m.setPendingGitSummary(rootPath, "Pruning worktrees...")
		m.status = "Pruning stale git worktrees..."
		return m, m.pruneWorktreesCmd(rootPath, rootPath)
	case commands.KindPin:
		p, ok := m.selectedProject()
		if !ok {
			m.status = "No project selected"
			return m, nil
		}
		return m, m.togglePinCmd(p.Path)
	case commands.KindRead:
		if inv.All {
			paths := make([]string, 0, len(m.projects))
			seenAt := m.currentTime()
			for _, project := range m.projects {
				if attention.AssessmentUnreadAt(project).IsZero() {
					continue
				}
				paths = append(paths, project.Path)
				m.markProjectSessionSeenLocal(project.Path, seenAt)
			}
			if len(paths) == 0 {
				m.status = "No visible completed assessments to mark read"
				return m, nil
			}
			cmds := make([]tea.Cmd, 0, len(paths))
			for _, path := range paths {
				cmds = append(cmds, m.markProjectSessionSeenCmd(path, seenAt))
			}
			m.status = fmt.Sprintf("Marked %d visible project(s) read", len(paths))
			return m, tea.Batch(cmds...)
		}
		p, ok := m.selectedProject()
		if !ok {
			m.status = "No project selected"
			return m, nil
		}
		if attention.AssessmentUnreadAt(p).IsZero() {
			m.status = "Selected project has no completed assessment to mark read"
			return m, nil
		}
		m.status = "Marked read"
		return m, m.markProjectSessionSeen(p.Path)
	case commands.KindUnread:
		p, ok := m.selectedProject()
		if !ok {
			m.status = "No project selected"
			return m, nil
		}
		if attention.AssessmentUnreadAt(p).IsZero() {
			m.status = "Selected project has no completed assessment to mark unread"
			return m, nil
		}
		m.status = "Marked unread"
		return m, m.markProjectSessionUnread(p.Path)
	case commands.KindSnooze:
		p, ok := m.selectedProject()
		if !ok {
			m.status = "No project selected"
			return m, nil
		}
		return m, m.snoozeCmd(p.Path, inv.Duration)
	case commands.KindClearSnooze:
		p, ok := m.selectedProject()
		if !ok {
			m.status = "No project selected"
			return m, nil
		}
		return m, m.clearSnoozeCmd(p.Path)
	case commands.KindSessions:
		m.applySectionToggle("Sessions", inv.Toggle, &m.showSessions)
		return m, nil
	case commands.KindEvents:
		m.applySectionToggle("Recent events", inv.Toggle, &m.showEvents)
		return m, nil
	case commands.KindIgnore:
		p, ok := m.selectedProject()
		if !ok {
			m.status = "No project selected"
			return m, nil
		}
		return m, m.ignoreProjectCmd(p)
	case commands.KindIgnored:
		return m.openIgnoredPicker()
	case commands.KindForget:
		p, ok := m.selectedProject()
		if !ok {
			m.status = "No project selected"
			return m, nil
		}
		if p.PresentOnDisk {
			m.status = "Forget only applies to missing folders"
			return m, nil
		}
		return m, m.forgetProjectCmd(p.Path)
	case commands.KindFocus:
		m.setFocusedPaneFromCommand(inv.Focus)
		return m, nil
	case commands.KindPrivacy:
		switch inv.Toggle {
		case commands.ToggleOn:
			m.privacyMode = true
			m.status = "Privacy mode enabled"
		case commands.ToggleOff:
			m.privacyMode = false
			m.status = "Privacy mode disabled"
		case commands.ToggleToggle:
			m.privacyMode = !m.privacyMode
			if m.privacyMode {
				m.status = "Privacy mode enabled"
			} else {
				m.status = "Privacy mode disabled"
			}
		}
		selectedPath := ""
		if p, ok := m.selectedProject(); ok {
			selectedPath = p.Path
		}
		m.rebuildProjectList(selectedPath)
		return m, m.savePrivacyModeCmd(m.privacyMode)
	case commands.KindQuit:
		if m.codexManager != nil {
			_ = m.codexManager.CloseAll()
		}
		if m.runtimeManager != nil {
			_ = m.runtimeManager.CloseAll()
		}
		if m.unsub != nil {
			m.unsub()
		}
		return m, tea.Quit
	default:
		m.status = "Command not implemented"
		return m, nil
	}
}

func (m Model) launchCodexForSelection(forceNew bool, prompt string) (tea.Model, tea.Cmd) {
	return m.launchEmbeddedForSelection(codexapp.ProviderCodex, forceNew, prompt)
}

func (m Model) launchOpenCodeForSelection(forceNew bool, prompt string) (tea.Model, tea.Cmd) {
	return m.launchEmbeddedForSelection(codexapp.ProviderOpenCode, forceNew, prompt)
}

func (m Model) launchClaudeForSelection(forceNew bool, prompt string) (tea.Model, tea.Cmd) {
	return m.launchEmbeddedForSelection(codexapp.ProviderClaudeCode, forceNew, prompt)
}

func (m Model) launchEmbeddedForSelection(provider codexapp.Provider, forceNew bool, prompt string) (tea.Model, tea.Cmd) {
	p, ok := m.selectedProject()
	if !ok {
		m.status = "No project selected"
		return m, nil
	}
	if !p.PresentOnDisk {
		m.status = provider.Label() + " launch requires a folder present on disk"
		return m, nil
	}
	if block, blocked := m.embeddedLaunchBlock(p, provider); blocked {
		actionLabel := m.attentionDialogSessionActionLabel(p, block.BlockingProvider)
		hint := fmt.Sprintf("Finish or close the %s session, then try starting %s here again.", block.BlockingProvider.Label(), provider.Label())
		if actionLabel != "" {
			hint = fmt.Sprintf("%s to finish or close it, then try starting %s here again.", actionLabel, provider.Label())
		}
		m.showAttentionDialog(attentionDialogState{
			Title:           "Launch blocked",
			ProjectName:     projectNameForPicker(p, p.Path),
			ProjectPath:     p.Path,
			Message:         block.Message,
			Hint:            hint,
			PrimaryLabel:    actionLabel,
			PrimaryProvider: block.BlockingProvider,
		})
		return m, nil
	}
	if !forceNew && strings.TrimSpace(prompt) == "" {
		if _, ok := m.liveEmbeddedSnapshotForProject(p.Path, provider); ok {
			return m.showCodexProject(p.Path, "Embedded "+provider.Label()+" session reopened. Alt+Up hides it.")
		}
	}

	req := codexapp.LaunchRequest{
		Provider:    provider,
		ProjectPath: p.Path,
		ResumeID:    m.selectedProjectSessionID(p, provider),
		ForceNew:    forceNew,
		Prompt:      prompt,
		Preset:      m.currentCodexLaunchPreset(),
	}
	if err := req.Validate(); err != nil {
		m.status = err.Error()
		return m, nil
	}

	m.ensureCodexRuntime()
	m.beginCodexPendingOpen(p.Path, provider)
	m.err = nil
	m.status = "Opening embedded " + provider.Label() + " session..."
	return m, m.openCodexSessionCmd(req)
}

type embeddedLaunchBlock struct {
	Message          string
	BlockingProvider codexapp.Provider
}

func (m Model) embeddedLaunchBlock(project model.ProjectSummary, requested codexapp.Provider) (embeddedLaunchBlock, bool) {
	requested = requested.Normalized()
	if requested == "" {
		requested = codexapp.ProviderCodex
	}
	if project.Path == "" {
		return embeddedLaunchBlock{}, false
	}
	if snapshot, ok := m.liveCodexSnapshot(project.Path); ok {
		liveProvider := embeddedProvider(snapshot)
		if liveProvider != requested && embeddedSessionBlocksProviderSwitch(snapshot) {
			return embeddedLaunchBlock{
				Message:          fmt.Sprintf("This project already has an active embedded %s session. Finish or close it before starting %s here.", liveProvider.Label(), requested.Label()),
				BlockingProvider: liveProvider,
			}, true
		}
	}
	latestProvider := providerForSessionFormat(project.LatestSessionFormat)
	if latestProvider == "" || latestProvider == requested {
		return embeddedLaunchBlock{}, false
	}
	if !projectLatestSessionBlocksProviderSwitch(project, m.currentTime(), m.embeddedLaunchProtectionWindow()) {
		return embeddedLaunchBlock{}, false
	}
	return embeddedLaunchBlock{
		Message:          fmt.Sprintf("This project already has an unfinished %s session. Finish or close it before starting %s here.", latestProvider.Label(), requested.Label()),
		BlockingProvider: latestProvider,
	}, true
}

func embeddedSessionBlocksProviderSwitch(snapshot codexapp.Snapshot) bool {
	if !snapshot.Started || snapshot.Closed {
		return false
	}
	if snapshot.Busy || snapshot.BusyExternal || strings.TrimSpace(snapshot.ActiveTurnID) != "" {
		return true
	}
	if snapshot.PendingApproval != nil || snapshot.PendingToolInput != nil || snapshot.PendingElicitation != nil {
		return true
	}
	switch snapshot.Phase {
	case codexapp.SessionPhaseRunning, codexapp.SessionPhaseFinishing, codexapp.SessionPhaseReconciling, codexapp.SessionPhaseStalled, codexapp.SessionPhaseExternal:
		return true
	default:
		return false
	}
}

func projectLatestSessionBlocksProviderSwitch(project model.ProjectSummary, now time.Time, protectionWindow time.Duration) bool {
	if !project.LatestTurnStateKnown || project.LatestTurnCompleted {
		return false
	}
	if project.LatestSessionLastEventAt.IsZero() {
		return true
	}
	if protectionWindow <= 0 || now.IsZero() {
		return true
	}
	return now.Sub(project.LatestSessionLastEventAt) <= protectionWindow
}

func (m Model) embeddedLaunchProtectionWindow() time.Duration {
	settings := m.currentSettingsBaseline()
	if settings.StuckThreshold > 0 {
		return settings.StuckThreshold
	}
	if settings.ActiveThreshold > 0 {
		return settings.ActiveThreshold
	}
	return config.Default().StuckThreshold
}

func (m Model) selectedProjectCodexSessionID(project model.ProjectSummary) string {
	return m.selectedProjectSessionID(project, codexapp.ProviderCodex)
}

func (m Model) liveEmbeddedSnapshotForProject(projectPath string, provider codexapp.Provider) (codexapp.Snapshot, bool) {
	projectPath = strings.TrimSpace(projectPath)
	provider = provider.Normalized()
	if projectPath == "" || provider == "" {
		return codexapp.Snapshot{}, false
	}
	snapshot, ok := m.nonBlockingCodexSnapshot(projectPath)
	if !ok {
		return codexapp.Snapshot{}, false
	}
	if snapshot.Closed || strings.TrimSpace(snapshot.ThreadID) == "" {
		return codexapp.Snapshot{}, false
	}
	if embeddedProvider(snapshot) != provider {
		return codexapp.Snapshot{}, false
	}
	return snapshot, true
}

func (m Model) selectedProjectSessionID(project model.ProjectSummary, provider codexapp.Provider) string {
	if snapshot, ok := m.liveEmbeddedSnapshotForProject(project.Path, provider); ok {
		return strings.TrimSpace(snapshot.ThreadID)
	}
	if m.detail.Summary.Path == project.Path {
		for _, session := range m.detail.Sessions {
			sessionID := session.ExternalID()
			if providerForSessionFormat(session.Format) == provider.Normalized() && strings.TrimSpace(sessionID) != "" {
				return sessionID
			}
		}
	}
	if providerForSessionFormat(project.LatestSessionFormat) == provider.Normalized() {
		return strings.TrimSpace(project.ExternalLatestSessionID())
	}
	return ""
}

func (m Model) currentCodexLaunchPreset() codexcli.Preset {
	settings := m.currentSettingsBaseline()
	if settings.CodexLaunchPreset == "" {
		return codexcli.DefaultPreset()
	}
	return settings.CodexLaunchPreset
}

func isCodexSessionFormat(format string) bool {
	return providerForSessionFormat(format) == codexapp.ProviderCodex
}

func isOpenCodeSessionFormat(format string) bool {
	return providerForSessionFormat(format) == codexapp.ProviderOpenCode
}

func providerForSessionFormat(format string) codexapp.Provider {
	switch strings.TrimSpace(format) {
	case "modern", "legacy":
		return codexapp.ProviderCodex
	case "opencode_db":
		return codexapp.ProviderOpenCode
	case "claude_code":
		return codexapp.ProviderClaudeCode
	default:
		return ""
	}
}

func preferredEmbeddedProviderFromProjectSummary(project model.ProjectSummary) codexapp.Provider {
	if provider := providerForSessionFormat(project.LatestSessionFormat); provider != "" {
		return provider
	}
	return codexapp.ProviderCodex
}

func (m Model) preferredEmbeddedProviderForProject(project model.ProjectSummary) codexapp.Provider {
	if snapshot, ok := m.liveCodexSnapshot(project.Path); ok {
		return embeddedProvider(snapshot)
	}
	return preferredEmbeddedProviderFromProjectSummary(project)
}

func (m Model) currentEmbeddedLaunchLabel() string {
	if project, ok := m.selectedProject(); ok {
		return m.preferredEmbeddedProviderForProject(project).Label()
	}
	return codexapp.ProviderCodex.Label()
}

func scanCompleteStatus(report service.ScanReport) string {
	if report.QueuedClassifications <= 0 {
		return fmt.Sprintf("Scan complete: %d updated", len(report.UpdatedProjects))
	}
	label := "classifications"
	if report.QueuedClassifications == 1 {
		label = "classification"
	}
	return fmt.Sprintf("Scan complete: %d updated, %d %s queued", len(report.UpdatedProjects), report.QueuedClassifications, label)
}

func loadedProjectsStatus(projectCount int, sortMode projectSortMode, visibility projectVisibilityMode, projectFilter string) string {
	status := fmt.Sprintf("Loaded %d projects (%s, %s)", projectCount, sortMode, visibilityLabel(visibility))
	if label := compactProjectFilterLabel(projectFilter, 24); label != "" {
		return status + " with " + label
	}
	return status
}

func projectLoadFailedStatus(hadProjects bool) string {
	if hadProjects {
		return "Project refresh failed"
	}
	return "Project load failed"
}

func (m Model) scanCmd(forceRetryFailedClassifications bool) tea.Cmd {
	return func() tea.Msg {
		report, err := m.svc.ScanWithOptions(m.ctx, service.ScanOptions{
			ForceRetryFailedClassifications: forceRetryFailedClassifications,
		})
		return scanMsg{report: report, err: err}
	}
}

func (m Model) togglePinCmd(path string) tea.Cmd {
	return func() tea.Msg {
		err := m.svc.TogglePin(m.ctx, path)
		return actionMsg{status: "Pin toggled", err: err}
	}
}

func (m Model) snoozeCmd(path string, d time.Duration) tea.Cmd {
	label := formatSnoozeDuration(d)
	return func() tea.Msg {
		err := m.svc.Snooze(m.ctx, path, d)
		return actionMsg{status: "Snoozed for " + label, err: err}
	}
}

func (m Model) clearSnoozeCmd(path string) tea.Cmd {
	return func() tea.Msg {
		err := m.svc.ClearSnooze(m.ctx, path)
		return actionMsg{status: "Snooze cleared", err: err}
	}
}

func (m Model) forgetProjectCmd(path string) tea.Cmd {
	return func() tea.Msg {
		err := m.svc.ForgetProject(m.ctx, path)
		return actionMsg{status: "Missing folder forgotten", err: err}
	}
}

func (m Model) ignoreProjectCmd(project model.ProjectSummary) tea.Cmd {
	name := strings.TrimSpace(project.Name)
	if name == "" {
		name = filepath.Base(filepath.Clean(project.Path))
	}
	return func() tea.Msg {
		err := m.svc.Store().SetIgnoredProjectName(m.ctx, name, true)
		status := fmt.Sprintf("Ignored %q", name)
		return ignoredProjectActionMsg{status: status, err: err}
	}
}

func (m Model) openProjectDirInBrowserCmd(path string) tea.Cmd {
	return func() tea.Msg {
		if err := openProjectDirInBrowser(path); err != nil {
			return browserOpenMsg{projectPath: path, err: err}
		}
		return browserOpenMsg{projectPath: path, status: "Opened project in browser"}
	}
}

func (m Model) openRuntimeURLInBrowserCmd(rawURL string) tea.Cmd {
	return func() tea.Msg {
		if err := openRuntimeURLInBrowser(rawURL); err != nil {
			return browserOpenMsg{err: err}
		}
		return browserOpenMsg{status: "Opened runtime URL in browser"}
	}
}

func (m Model) prepareCommitPreviewCmd(path string, intent service.GitActionIntent, message string) tea.Cmd {
	return func() tea.Msg {
		preview, err := m.svc.PrepareCommit(m.ctx, path, intent, message)
		return commitPreviewMsg{preview: preview, projectPath: path, intent: intent, message: message, err: err}
	}
}

func (m *Model) startCommitPreview(project model.ProjectSummary, intent service.GitActionIntent, messageOverride string) tea.Cmd {
	projectName := strings.TrimSpace(project.Name)
	if projectName == "" {
		projectName = filepath.Base(filepath.Clean(project.Path))
	}

	preview := service.CommitPreview{
		Intent:        intent,
		ProjectPath:   project.Path,
		ProjectName:   projectName,
		StageMode:     service.GitStageStagedOnly,
		Message:       commitPreviewLoadingMessage(intent, messageOverride),
		LatestSummary: strings.TrimSpace(project.LatestSessionSummary),
	}

	m.err = nil
	m.showHelp = false
	m.gitStatusDialog = nil
	m.gitStatusApplying = false
	m.diffView = nil
	m.commitApplying = false
	m.commitPreview = &preview
	m.commitTodoCompletions = nil
	m.commitTodoSelected = 0
	m.commitPreviewMessageOverride = strings.TrimSpace(messageOverride)
	m.commitPreviewRefreshing = true
	m.setPendingGitSummary(project.Path, commitPreviewPreparingStatus(intent))
	m.status = commitPreviewPreparingStatus(intent)
	return m.prepareCommitPreviewCmd(project.Path, intent, messageOverride)
}

func commitPreviewPreparingStatus(intent service.GitActionIntent) string {
	if intent == service.GitActionFinish {
		return "Preparing finish preview..."
	}
	return "Preparing commit preview..."
}

func commitPreviewLoadingMessage(intent service.GitActionIntent, messageOverride string) string {
	messageOverride = strings.TrimSpace(messageOverride)
	if messageOverride != "" {
		return messageOverride
	}
	if intent == service.GitActionFinish {
		return "Generating finish message..."
	}
	return "Generating commit message..."
}

func (m Model) prepareDiffPreviewCmd(path string) tea.Cmd {
	return func() tea.Msg {
		preview, err := m.svc.PrepareDiff(m.ctx, path)
		return diffPreviewMsg{preview: preview, err: err}
	}
}

func (m *Model) startDiffView(projectPath, projectName string) tea.Cmd {
	m.err = nil
	m.showHelp = false
	m.diffView = newDiffViewState(projectPath, projectName)
	m.syncDiffView(true)
	m.setPendingGitSummary(projectPath, "Preparing diff view...")
	m.status = "Preparing diff view..."
	return m.prepareDiffPreviewCmd(projectPath)
}

func (m *Model) startDiffViewFromCommitPreview(preview service.CommitPreview, messageOverride string) tea.Cmd {
	cmd := m.startDiffView(preview.ProjectPath, preview.ProjectName)
	if m.diffView != nil {
		m.diffView.returnToCommitPreview = &commitPreviewReturnState{
			preview:         preview,
			messageOverride: messageOverride,
		}
	}
	return cmd
}

func (m Model) toggleDiffStageCmd(projectPath string, file service.DiffFilePreview, selectStaged bool) tea.Cmd {
	return func() tea.Msg {
		status, err := m.svc.ToggleDiffFileStage(m.ctx, projectPath, file)
		if err != nil {
			return diffStageToggleMsg{status: status, path: file.Path, originalPath: file.OriginalPath, selectStaged: selectStaged, err: err}
		}
		preview, err := m.svc.PrepareDiff(m.ctx, projectPath)
		return diffStageToggleMsg{
			preview:      preview,
			status:       status,
			path:         file.Path,
			originalPath: file.OriginalPath,
			selectStaged: selectStaged,
			err:          err,
		}
	}
}

func (m Model) resumeCommitPreviewCmd(cached service.CommitPreview, messageOverride string) tea.Cmd {
	return func() tea.Msg {
		previewMsg := commitPreviewMsg{
			projectPath: cached.ProjectPath,
			intent:      cached.Intent,
			message:     messageOverride,
		}
		if m.svc == nil {
			previewMsg.err = fmt.Errorf("service unavailable")
			return previewMsg
		}

		currentHash, err := m.svc.CommitPreviewStateHash(m.ctx, cached.ProjectPath)
		if err != nil {
			previewMsg.err = err
			return previewMsg
		}
		if currentHash == cached.StateHash && currentHash != "" {
			previewMsg.preview = cached
			return previewMsg
		}

		preview, err := m.svc.PrepareCommit(m.ctx, cached.ProjectPath, cached.Intent, messageOverride)
		previewMsg.preview = preview
		previewMsg.err = err
		return previewMsg
	}
}

func diffPreviewSelectionIndex(files []service.DiffFilePreview, path, originalPath string, fallback int) int {
	for i, file := range files {
		if strings.TrimSpace(file.Path) == strings.TrimSpace(path) && strings.TrimSpace(file.OriginalPath) == strings.TrimSpace(originalPath) {
			return i
		}
		if strings.TrimSpace(file.Path) == strings.TrimSpace(path) {
			return i
		}
	}
	if len(files) == 0 {
		return 0
	}
	if fallback < 0 {
		return 0
	}
	if fallback >= len(files) {
		return len(files) - 1
	}
	return fallback
}

func diffPreviewStagedSelectionIndex(files []service.DiffFilePreview, path, originalPath string, fallback int, selectStaged bool) int {
	currentIdx := diffPreviewSelectionIndex(files, path, originalPath, fallback)
	if len(files) == 0 {
		return 0
	}
	for i := 0; i < len(files); i++ {
		idx := (currentIdx + 1 + i) % len(files)
		if files[idx].Staged == selectStaged {
			return idx
		}
	}
	return currentIdx
}

func (m Model) resolveSubmodulesAndContinueCmd(path string, intent service.GitActionIntent, message string) tea.Cmd {
	return func() tea.Msg {
		preview, err := m.svc.ResolveSubmodulesAndPrepareCommit(m.ctx, path, intent, message)
		return commitPreviewMsg{preview: preview, projectPath: path, intent: intent, message: message, err: err}
	}
}

func (m Model) applyCommitPreviewCmd(preview service.CommitPreview, pushAfterCommit bool) tea.Cmd {
	completedTodoIDs := selectedCommitTodoIDs(m.commitTodoCompletions)
	return func() tea.Msg {
		result, err := m.svc.ApplyCommit(m.ctx, preview, pushAfterCommit, completedTodoIDs)
		if err != nil {
			return actionMsg{projectPath: preview.ProjectPath, status: "Commit failed", clearPendingGitSummary: true, err: err}
		}
		status := "Committed " + result.CommitHash
		if result.Pushed {
			status = "Committed " + result.CommitHash + " and pushed"
		}
		if result.Warning != "" {
			status = result.Warning
		}
		return actionMsg{projectPath: preview.ProjectPath, status: status, clearPendingGitSummary: true, err: nil}
	}
}

func (m Model) pushCmd(path string) tea.Cmd {
	return func() tea.Msg {
		result, err := m.svc.PushProject(m.ctx, path)
		if err != nil {
			return actionMsg{projectPath: path, status: "Push failed", clearPendingGitSummary: true, err: err}
		}
		status := result.Summary
		if strings.TrimSpace(status) == "" {
			status = "Push complete"
		}
		return actionMsg{projectPath: path, status: status, clearPendingGitSummary: true, err: nil}
	}
}

func shortID(id string) string {
	if len(id) <= 8 {
		return id
	}
	return id[:8]
}

func formatListActivityTime(now, activity time.Time) string {
	if activity.IsZero() {
		return "Never"
	}
	if now.IsZero() {
		now = time.Now()
	}

	loc := now.Location()
	if loc == nil {
		loc = time.Local
	}
	nowLocal := now.In(loc)
	activityLocal := activity.In(loc)
	if activityLocal.After(nowLocal) {
		return activityLocal.Format("2006-01-02 15:04")
	}

	diffDays := calendarDayDiff(nowLocal, activityLocal)
	switch {
	case diffDays <= 0:
		return activityLocal.Format("15:04")
	case diffDays == 1:
		return "Yesterday"
	case diffDays < 7:
		return formatRelativeUnit(diffDays, "day")
	case diffDays < 28:
		return formatRelativeUnit(diffDays/7, "week")
	default:
		return formatRelativeUnit(max(1, wholeMonthsBetween(nowLocal, activityLocal)), "month")
	}
}

func calendarDayDiff(now, activity time.Time) int {
	return dayIndex(now) - dayIndex(activity)
}

func dayIndex(t time.Time) int {
	y, m, d := t.Date()
	return int(time.Date(y, m, d, 0, 0, 0, 0, time.UTC).Unix() / (24 * 60 * 60))
}

func wholeMonthsBetween(now, activity time.Time) int {
	months := (now.Year()-activity.Year())*12 + int(now.Month()-activity.Month())
	if now.Day() < activity.Day() {
		months--
	}
	return months
}

func formatRelativeUnit(n int, unit string) string {
	if n <= 1 {
		return fmt.Sprintf("1 %s", unit)
	}
	return fmt.Sprintf("%d %ss", n, unit)
}

func projectHasRepoWarning(project model.ProjectSummary) bool {
	return project.RepoConflict || project.RepoDirty || (projectShowsRemoteSyncStatus(project) && repoSyncWarning(project.RepoSyncStatus))
}

func appendDetailFields(lines []string, width int, fields ...string) []string {
	if len(fields) == 0 {
		return lines
	}
	if width < 72 {
		return append(lines, fields...)
	}
	for i := 0; i < len(fields); i += 2 {
		if i+1 < len(fields) {
			lines = append(lines, fields[i]+"  "+fields[i+1])
			continue
		}
		lines = append(lines, fields[i])
	}
	return lines
}

func projectAssessmentText(project model.ProjectSummary) string {
	return projectAssessmentTextAt(project, time.Time{}, 0)
}

func (m Model) projectAssessmentDisplayTextAt(project model.ProjectSummary, now time.Time, stuckThreshold time.Duration) string {
	if pending := m.pendingGitSummary(project.Path); pending != "" {
		return pending
	}
	return projectAssessmentTextAt(project, now, stuckThreshold)
}

func projectAssessmentTextAt(project model.ProjectSummary, now time.Time, stuckThreshold time.Duration) string {
	effective := effectiveAssessmentForProject(project, now, stuckThreshold)
	if strings.TrimSpace(effective.Summary) != "" && (project.LatestSessionClassification == model.ClassificationCompleted || projectAssessmentRefreshing(project)) {
		return effective.Summary
	}
	if strings.TrimSpace(project.LatestCompletedSessionSummary) != "" {
		return project.LatestCompletedSessionSummary
	}
	if label, _, ok := visibleAssessmentStatusLabelAt(project, now, stuckThreshold); ok {
		return label
	}
	if project.LatestSessionFormat != "" {
		return "not assessed yet"
	}
	return "-"
}

func projectAssessmentStyle(project model.ProjectSummary, now time.Time, stuckThreshold time.Duration) lipgloss.Style {
	if _, _, ok := visibleAssessmentStatusLabelAt(project, now, stuckThreshold); ok {
		if projectAssessmentRefreshing(project) {
			return detailMutedStyle
		}
		if projectAssessmentUnread(project, now, stuckThreshold) {
			return detailValueStyle
		}
		return detailMutedStyle
	}
	return detailMutedStyle
}

type projectRunState uint8

const (
	projectRunIdle projectRunState = iota
	projectRunActive
	projectRunError
)

func projectTODOCountLabel(count int) string {
	if count <= 0 {
		return ""
	}
	return strconv.Itoa(count)
}

func projectListColumnWidths(totalWidth int) (int, int) {
	const baseWidth = 56

	if totalWidth < baseWidth+22 {
		return 10, 10
	}

	remaining := totalWidth - baseWidth
	projectWidth := min(28, max(16, remaining/4))
	assessmentWidth := remaining - projectWidth - 2
	if assessmentWidth < 18 {
		projectWidth = max(14, remaining-20)
		assessmentWidth = remaining - projectWidth - 2
	}
	if assessmentWidth < 10 {
		projectWidth = max(10, remaining/3)
		assessmentWidth = max(10, remaining-projectWidth-2)
	}
	return projectWidth, assessmentWidth
}

func renderProjectListHeader(projectW, assessmentW int) string {
	return lipgloss.JoinHorizontal(
		lipgloss.Top,
		lipgloss.NewStyle().Width(5).Align(lipgloss.Right).Render("ATTN"),
		"  ",
		lipgloss.NewStyle().Width(8).Render("ASSESS"),
		" ",
		lipgloss.NewStyle().Width(10).Render("LAST"),
		" ",
		lipgloss.NewStyle().Width(projectListAgentWidth).Align(lipgloss.Left).Render("AGENT"),
		" ",
		lipgloss.NewStyle().Width(projectListTODOWidth).Align(lipgloss.Right).Render("TODO"),
		" ",
		lipgloss.NewStyle().Width(projectListRunWidth).Align(lipgloss.Left).Render("RUN"),
		"  ",
		lipgloss.NewStyle().Width(projectW).Render("PROJECT"),
		"  ",
		lipgloss.NewStyle().Width(assessmentW).Render("SUMMARY"),
	)
}

func (m Model) projectTurnLiveWindow() time.Duration {
	activeThreshold := m.currentSettingsBaseline().ActiveThreshold
	if activeThreshold > 0 {
		return activeThreshold
	}
	return config.Default().ActiveThreshold
}

func (m Model) projectUnfinishedTurnLooksLive(project model.ProjectSummary, now time.Time) bool {
	if !project.LatestTurnStateKnown || project.LatestTurnCompleted {
		return false
	}

	if project.LatestSessionClassification == model.ClassificationCompleted {
		effective := effectiveAssessmentForProject(project, now, m.assessmentStallThreshold())
		switch effective.Category {
		case model.SessionCategoryCompleted,
			model.SessionCategoryBlocked,
			model.SessionCategoryWaitingForUser,
			model.SessionCategoryNeedsFollowUp:
			return false
		}
	}

	if now.IsZero() {
		return true
	}
	lastEventAt := project.LatestSessionLastEventAt
	if lastEventAt.IsZero() {
		lastEventAt = project.LastActivity
	}
	if lastEventAt.IsZero() {
		return true
	}
	return now.Sub(lastEventAt) <= m.projectTurnLiveWindow()
}

func (m Model) projectAgentDisplay(project model.ProjectSummary, now time.Time) (string, string, bool) {
	if snapshot, ok := m.liveCodexSnapshot(project.Path); ok {
		tag := embeddedProvider(snapshot).SourceTag()
		label := tag
		if snapshot.Phase == codexapp.SessionPhaseStalled {
			label += " stalled"
		} else if snapshot.Busy {
			startedAt := snapshot.BusySince
			if startedAt.IsZero() && !project.LatestTurnStartedAt.IsZero() {
				startedAt = project.LatestTurnStartedAt
			}
			if !startedAt.IsZero() && !now.IsZero() {
				label += " " + formatRunningDuration(now.Sub(startedAt))
			}
		}
		return label, tag, true
	}

	provider := providerForSessionFormat(project.LatestSessionFormat)
	if provider == "" {
		return "", "", false
	}
	tag := provider.SourceTag()
	if project.LatestTurnStateKnown && !project.LatestTurnCompleted {
		if !m.startupScanCompleted {
			return tag, tag, false
		}
	}
	if m.projectUnfinishedTurnLooksLive(project, now) {
		label := tag
		if !project.LatestTurnStartedAt.IsZero() && !now.IsZero() {
			label += " " + formatRunningDuration(now.Sub(project.LatestTurnStartedAt))
		}
		return label, tag, true
	}
	return tag, tag, false
}

func projectRunSummary(snapshot projectrun.Snapshot, savedCommand string) (string, projectRunState) {
	command := strings.TrimSpace(snapshot.Command)
	if command == "" {
		command = strings.TrimSpace(savedCommand)
	}
	label := projectRunCommandLabel(command)
	port := projectRunPortSummary(snapshot)
	if snapshot.Running {
		if label == "" {
			label = "run"
		}
		if port != "" {
			if len(snapshot.ConflictPorts) > 0 {
				return label + "!" + port, projectRunError
			}
			return label + "@" + port, projectRunActive
		}
		return label, projectRunActive
	}
	if strings.TrimSpace(snapshot.LastError) != "" {
		if label == "" {
			return "err", projectRunError
		}
		return label + " err", projectRunError
	}
	if snapshot.ExitCodeKnown && snapshot.ExitCode != 0 {
		if label == "" {
			return "err", projectRunError
		}
		return label + " err", projectRunError
	}
	if label != "" {
		return label, projectRunIdle
	}
	return "", projectRunIdle
}

func projectRunPortSummary(snapshot projectrun.Snapshot) string {
	if len(snapshot.Ports) == 0 {
		return ""
	}
	switch len(snapshot.Ports) {
	case 1:
		return strconv.Itoa(snapshot.Ports[0])
	default:
		return fmt.Sprintf("%dp", len(snapshot.Ports))
	}
}

func projectRunCommandLabel(command string) string {
	tokens := strings.Fields(strings.TrimSpace(command))
	for i := 0; i < len(tokens); i++ {
		token := trimRunToken(tokens[i])
		if token == "" {
			continue
		}
		if token == "cd" {
			i++
			for i < len(tokens) {
				next := trimRunToken(tokens[i])
				if next == "&&" || next == ";" {
					break
				}
				i++
			}
			continue
		}
		if isShellEnvAssignment(token) {
			continue
		}
		switch token {
		case "env", "command", "nohup", "time":
			continue
		case "sudo":
			for i+1 < len(tokens) {
				next := trimRunToken(tokens[i+1])
				if !strings.HasPrefix(next, "-") {
					break
				}
				i++
			}
			continue
		case "npx":
			if i+1 < len(tokens) {
				next := trimRunToken(tokens[i+1])
				if next != "" {
					return filepath.Base(next)
				}
			}
			return "npx"
		default:
			return filepath.Base(token)
		}
	}
	return ""
}

func trimRunToken(token string) string {
	return strings.TrimSpace(strings.Trim(token, `"'`))
}

func isShellEnvAssignment(token string) bool {
	idx := strings.IndexByte(token, '=')
	if idx <= 0 {
		return false
	}
	key := token[:idx]
	for i, r := range key {
		if r == '_' || (r >= 'A' && r <= 'Z') || (r >= 'a' && r <= 'z') {
			continue
		}
		if i > 0 && r >= '0' && r <= '9' {
			continue
		}
		return false
	}
	return true
}

func truncateText(text string, width int) string {
	if width <= 0 {
		return ""
	}
	runes := []rune(text)
	if len(runes) <= width {
		return text
	}
	if width <= 3 {
		return string(runes[:width])
	}
	return string(runes[:width-3]) + "..."
}

func singleLineStatusText(text string) string {
	text = strings.ReplaceAll(text, "\r\n", "\n")
	lines := strings.Split(text, "\n")
	out := make([]string, 0, len(lines))
	for _, line := range lines {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		out = append(out, line)
	}
	return strings.Join(out, " | ")
}

func fitStyledWidth(text string, width int) string {
	if width <= 0 {
		return text
	}
	text = ansi.Truncate(text, width, "")
	if padding := width - ansi.StringWidth(ansi.Strip(text)); padding > 0 {
		text += strings.Repeat(" ", padding)
	}
	return text
}

func fitPaneContent(content string, width, height int) string {
	if height <= 0 {
		return ""
	}
	content = strings.ReplaceAll(content, "\r\n", "\n")
	lines := strings.Split(content, "\n")
	if len(lines) > height {
		lines = lines[:height]
	}
	fitted := make([]string, 0, height)
	for _, line := range lines {
		fitted = append(fitted, fitStyledWidth(line, width))
	}
	blank := strings.Repeat(" ", max(0, width))
	for len(fitted) < height {
		fitted = append(fitted, blank)
	}
	return strings.Join(fitted, "\n")
}

func renderWrappedDetailBullet(style lipgloss.Style, width int, text string) string {
	if width <= 0 {
		return style.Render("- " + text)
	}
	wrapped := lipgloss.NewStyle().Width(max(1, width-2)).Render(text)
	lines := strings.Split(strings.ReplaceAll(wrapped, "\r\n", "\n"), "\n")
	for i := range lines {
		prefix := "- "
		if i > 0 {
			prefix = "  "
		}
		lines[i] = style.Render(prefix + lines[i])
	}
	return strings.Join(lines, "\n")
}

func renderWrappedDialogTextLines(style lipgloss.Style, width int, text string) []string {
	normalized := strings.ReplaceAll(text, "\r\n", "\n")
	rawLines := strings.Split(normalized, "\n")
	out := make([]string, 0, len(rawLines))
	for _, raw := range rawLines {
		trimmed := strings.TrimSpace(raw)
		if trimmed == "" {
			out = append(out, "")
			continue
		}
		if strings.HasPrefix(trimmed, "- ") {
			out = append(out, strings.Split(renderWrappedDetailBullet(style, width, strings.TrimSpace(strings.TrimPrefix(trimmed, "- "))), "\n")...)
			continue
		}
		wrapped := lipgloss.NewStyle().Width(max(1, width)).Render(trimmed)
		for _, line := range strings.Split(strings.ReplaceAll(wrapped, "\r\n", "\n"), "\n") {
			out = append(out, style.Render(line))
		}
	}
	return out
}

func repoSyncWarning(status model.RepoSyncStatus) bool {
	switch status {
	case model.RepoSyncAhead, model.RepoSyncBehind, model.RepoSyncDiverged, model.RepoSyncNoUpstream:
		return true
	default:
		return false
	}
}

func repoSyncDetailLine(project model.ProjectSummary) string {
	value := repoSyncDetailValue(project)
	if value == "" {
		return ""
	}
	return "Remote: " + value
}

func (m Model) repoCombinedDetailValue(project model.ProjectSummary) string {
	var parts []string
	if op, ok := m.pendingGitOperation(project.Path); ok {
		parts = append(parts, detailValueStyle.Render(op.summaryText()))
	} else if project.RepoConflict {
		parts = append(parts, detailConflictStyle.Render("conflict"))
	} else if project.RepoDirty {
		parts = append(parts, detailWarningStyle.Render("dirty"))
	} else {
		parts = append(parts, detailMutedStyle.Render("clean"))
	}
	if projectShowsRemoteSyncStatus(project) && m.pendingGitSummary(project.Path) == "" {
		switch project.RepoSyncStatus {
		case model.RepoSyncNoRemote:
			parts = append(parts, detailMutedStyle.Render("no remote"))
		case model.RepoSyncNoUpstream:
			parts = append(parts, repoSyncDetailStyle(project.RepoSyncStatus).Render("no upstream"))
		case model.RepoSyncSynced:
			parts = append(parts, repoSyncDetailStyle(project.RepoSyncStatus).Render("synced"))
		case model.RepoSyncAhead:
			parts = append(parts, repoSyncDetailStyle(project.RepoSyncStatus).Render(fmt.Sprintf("ahead %d", project.RepoAheadCount)))
		case model.RepoSyncBehind:
			parts = append(parts, repoSyncDetailStyle(project.RepoSyncStatus).Render(fmt.Sprintf("behind %d", project.RepoBehindCount)))
		case model.RepoSyncDiverged:
			parts = append(parts, repoSyncDetailStyle(project.RepoSyncStatus).Render(fmt.Sprintf("diverged +%d/-%d", project.RepoAheadCount, project.RepoBehindCount)))
		}
	}
	value := strings.Join(parts, ", ")
	if branch := strings.TrimSpace(project.RepoBranch); branch != "" {
		branchValue := detailValueStyle.Render("(" + branch + ")")
		if value == "" {
			return branchValue
		}
		return value + " " + branchValue
	}
	return value
}

func repoDirtyDetailValue(project model.ProjectSummary) string {
	if project.RepoConflict {
		return detailConflictStyle.Render("unmerged files")
	}
	if project.RepoDirty {
		return detailWarningStyle.Render("dirty worktree")
	}
	return detailMutedStyle.Render("clean")
}

func repoConflictDetailValue(project model.ProjectSummary) string {
	location := "repo"
	if project.WorktreeKind == model.WorktreeKindLinked {
		location = "worktree"
	}
	return detailConflictStyle.Render("Unmerged files are present in this " + location + ". Resolve or abort the in-progress Git operation before continuing.")
}

func worktreeMergeStatusDetailValue(project model.ProjectSummary) string {
	targetBranch := strings.TrimSpace(project.WorktreeParentBranch)
	switch project.WorktreeMergeStatus {
	case model.WorktreeMergeStatusMerged:
		if targetBranch != "" {
			return detailValueStyle.Render("merged into " + targetBranch)
		}
		return detailValueStyle.Render("merged")
	case model.WorktreeMergeStatusNotMerged:
		if targetBranch != "" {
			return detailWarningStyle.Render("not merged into " + targetBranch)
		}
		return detailWarningStyle.Render("not merged")
	default:
		if targetBranch != "" {
			return detailMutedStyle.Render("unavailable for " + targetBranch)
		}
		return detailMutedStyle.Render("unavailable")
	}
}

func repoSyncDetailValue(project model.ProjectSummary) string {
	if !projectShowsRemoteSyncStatus(project) {
		return ""
	}
	switch project.RepoSyncStatus {
	case model.RepoSyncNoRemote:
		return detailMutedStyle.Render("none")
	case model.RepoSyncNoUpstream:
		return repoSyncDetailStyle(project.RepoSyncStatus).Render("has remote, no upstream tracking branch")
	case model.RepoSyncSynced:
		return repoSyncDetailStyle(project.RepoSyncStatus).Render("synced")
	case model.RepoSyncAhead:
		return repoSyncDetailStyle(project.RepoSyncStatus).Render(fmt.Sprintf("ahead by %d", project.RepoAheadCount))
	case model.RepoSyncBehind:
		return repoSyncDetailStyle(project.RepoSyncStatus).Render(fmt.Sprintf("behind by %d", project.RepoBehindCount))
	case model.RepoSyncDiverged:
		return repoSyncDetailStyle(project.RepoSyncStatus).Render(fmt.Sprintf("diverged (+%d/-%d)", project.RepoAheadCount, project.RepoBehindCount))
	default:
		return ""
	}
}

func projectShowsRemoteSyncStatus(project model.ProjectSummary) bool {
	return project.WorktreeKind != model.WorktreeKindLinked
}

func repoSyncDetailStyle(status model.RepoSyncStatus) lipgloss.Style {
	switch status {
	case model.RepoSyncSynced:
		return lipgloss.NewStyle().Foreground(lipgloss.Color("42")).Bold(true)
	case model.RepoSyncAhead, model.RepoSyncBehind, model.RepoSyncNoUpstream:
		return detailWarningStyle
	case model.RepoSyncDiverged:
		return detailDangerStyle
	case model.RepoSyncNoRemote:
		return detailMutedStyle
	default:
		return detailValueStyle
	}
}

func projectConflictIndicatorStyle(spinnerFrame int) lipgloss.Style {
	color := lipgloss.Color("141")
	if spinnerFrame%2 == 0 {
		color = lipgloss.Color("177")
	}
	return lipgloss.NewStyle().Foreground(color).Bold(true)
}

func detailField(label, value string) string {
	return detailLabelStyle.Render(label+":") + " " + value
}

func detailReasonLine(reason model.AttentionReason) string {
	weightStyle := detailMutedStyle
	if reason.Weight > 0 {
		weightStyle = detailWarningStyle
	}
	if reason.Weight < 0 {
		weightStyle = lipgloss.NewStyle().Foreground(lipgloss.Color("42")).Bold(true)
	}
	return detailValueStyle.Render("- ") + weightStyle.Render(fmt.Sprintf("[%+d]", reason.Weight)) + detailValueStyle.Render(" "+reason.Text)
}

func assessmentDisplayStyle(project model.ProjectSummary, now time.Time, stuckThreshold time.Duration) lipgloss.Style {
	if _, category, ok := visibleAssessmentStatusLabelAt(project, now, stuckThreshold); ok {
		if projectAssessmentRefreshing(project) {
			return detailMutedStyle
		}
		style := classificationCategoryStyle(category)
		if projectAssessmentUnread(project, now, stuckThreshold) {
			return style
		}
		return style.Bold(false).Faint(true)
	}
	return detailMutedStyle
}

func projectAssessmentLabelAt(project model.ProjectSummary, now time.Time) string {
	return projectAssessmentLabelWithThreshold(project, now, 0)
}

func projectAssessmentLabelWithThreshold(project model.ProjectSummary, now time.Time, stuckThreshold time.Duration) string {
	if label, _, ok := visibleAssessmentStatusLabelAt(project, now, stuckThreshold); ok {
		return label
	}
	if project.LatestSessionFormat != "" {
		return "not assessed yet"
	}
	return "not assessed"
}

func projectListStatus(project model.ProjectSummary) string {
	return projectListStatusAt(project, time.Time{}, 0)
}

func projectListStatusAt(project model.ProjectSummary, now time.Time, stuckThreshold time.Duration) string {
	if projectMissing(project) {
		return "missing"
	}
	if moveStatusActive(project.MovedAt, project.Path, project.LatestSessionDetectedProjectPath) {
		return "moved"
	}
	if label, _, ok := visibleAssessmentStatusLabelAt(project, now, stuckThreshold); ok {
		return label
	}
	if project.LatestSessionFormat != "" {
		return "new"
	}
	return projectActivityStatus(project)
}

func projectUnreadIndicator(project model.ProjectSummary, now time.Time, stuckThreshold time.Duration) string {
	if !projectAssessmentUnread(project, now, stuckThreshold) {
		return " "
	}
	return lipgloss.NewStyle().Foreground(lipgloss.Color("229")).Bold(true).Render("u")
}

func classificationProgressText(status model.SessionClassificationStatus, stage model.SessionClassificationStage, stageStartedAt, updatedAt, now time.Time, includeAssessmentPrefix bool) string {
	label := classificationProgressStageLabel(status, stage)
	if label == "" {
		if includeAssessmentPrefix {
			return "assessment"
		}
		return ""
	}
	if includeAssessmentPrefix {
		label = "assessment " + label
	}
	startedAt := classificationProgressStartedAt(stageStartedAt, updatedAt)
	if startedAt.IsZero() || now.IsZero() {
		return label
	}
	return label + " " + formatRunningDuration(now.Sub(startedAt))
}

func classificationProgressStageLabel(status model.SessionClassificationStatus, stage model.SessionClassificationStage) string {
	switch status {
	case model.ClassificationPending:
		return "queued"
	case model.ClassificationRunning:
		switch stage {
		case model.ClassificationStagePreparingSnapshot:
			return "preparing snapshot"
		case model.ClassificationStageWaitingForModel:
			return "waiting for model"
		default:
			return "running"
		}
	default:
		return ""
	}
}

func classificationProgressStartedAt(stageStartedAt, updatedAt time.Time) time.Time {
	if !stageStartedAt.IsZero() {
		return stageStartedAt
	}
	return updatedAt
}

func classificationFailureText(classification *model.SessionClassification) string {
	if classification == nil {
		return "assessment failed"
	}
	label := "assessment failed"
	if stageLabel := classificationProgressStageLabel(model.ClassificationRunning, classification.Stage); stageLabel != "" {
		label += " during " + stageLabel
	}
	if strings.TrimSpace(classification.LastError) == "" {
		return label
	}
	return label + ": " + classification.LastError
}

func activityDisplayStyle(project model.ProjectSummary) lipgloss.Style {
	if projectMissing(project) {
		return lipgloss.NewStyle().Foreground(lipgloss.Color("178")).Bold(true)
	}
	if moveStatusActive(project.MovedAt, project.Path, project.LatestSessionDetectedProjectPath) {
		return lipgloss.NewStyle().Foreground(lipgloss.Color("229")).Bold(true)
	}
	return statusStyle(project.Status)
}

func projectActivityStatus(project model.ProjectSummary) string {
	if projectMissing(project) {
		return "missing"
	}
	if moveStatusActive(project.MovedAt, project.Path, project.LatestSessionDetectedProjectPath) {
		return "moved"
	}
	return attentionStatusLabel(project.Status)
}

func shouldShowProjectActivity(project model.ProjectSummary) bool {
	if projectMissing(project) || moveStatusActive(project.MovedAt, project.Path, project.LatestSessionDetectedProjectPath) {
		return true
	}
	return project.Status != model.StatusIdle
}

func visibilityLabel(mode projectVisibilityMode) string {
	switch mode {
	case visibilityAllFolders:
		return "All folders"
	default:
		return "AI folders"
	}
}

func visibilityShortLabel(mode projectVisibilityMode) string {
	switch mode {
	case visibilityAllFolders:
		return "all"
	default:
		return "AI"
	}
}

func projectHasAIMetadata(project model.ProjectSummary) bool {
	return project.ManuallyAdded || !project.LastActivity.IsZero() || project.LatestSessionFormat != "" || project.LatestSessionClassification != ""
}

func projectMissing(project model.ProjectSummary) bool {
	return !project.PresentOnDisk
}

func filterProjects(projects []model.ProjectSummary, mode projectVisibilityMode, excludeProjectPatterns []string, projectFilter string) []model.ProjectSummary {
	if mode == visibilityAllFolders {
		return filterProjectsByFilter(filterProjectsByName(projects, excludeProjectPatterns), projectFilter)
	}
	filtered := make([]model.ProjectSummary, 0, len(projects))
	for _, project := range projects {
		if projectHasAIMetadata(project) {
			filtered = append(filtered, project)
		}
	}
	return filterProjectsByFilter(filterProjectsByName(filtered, excludeProjectPatterns), projectFilter)
}

func expandVisibleWorktreeFamilies(filtered, sorted []model.ProjectSummary) []model.ProjectSummary {
	if len(filtered) == 0 {
		return nil
	}

	includePaths := make(map[string]struct{}, len(filtered))
	visibleRoots := map[string]struct{}{}
	for _, project := range filtered {
		path := filepath.Clean(strings.TrimSpace(project.Path))
		if path == "" || path == "." {
			continue
		}
		includePaths[path] = struct{}{}
		if projectIsWorktreeRoot(project) {
			visibleRoots[projectWorktreeRootPath(project)] = struct{}{}
		}
	}
	if len(visibleRoots) == 0 {
		return append([]model.ProjectSummary(nil), filtered...)
	}

	out := make([]model.ProjectSummary, 0, len(sorted))
	for _, project := range sorted {
		path := filepath.Clean(strings.TrimSpace(project.Path))
		if path == "" || path == "." {
			continue
		}
		if _, ok := includePaths[path]; ok {
			out = append(out, project)
			continue
		}
		if _, ok := visibleRoots[projectWorktreeRootPath(project)]; ok {
			out = append(out, project)
		}
	}
	return out
}

func filterProjectsByPrivacy(projects []model.ProjectSummary, privacyPatterns []string) []model.ProjectSummary {
	if len(projects) == 0 || len(privacyPatterns) == 0 {
		return projects
	}
	filtered := make([]model.ProjectSummary, 0, len(projects))
	for _, project := range projects {
		if !config.MatchesPrivacyPattern(project.Name, privacyPatterns) {
			filtered = append(filtered, project)
		}
	}
	return filtered
}

func filterProjectsByName(projects []model.ProjectSummary, excludeProjectPatterns []string) []model.ProjectSummary {
	if len(projects) == 0 {
		return nil
	}
	if len(excludeProjectPatterns) == 0 {
		return append([]model.ProjectSummary(nil), projects...)
	}
	filtered := make([]model.ProjectSummary, 0, len(projects))
	for _, project := range projects {
		if projectMatchesExcludedName(project, excludeProjectPatterns) {
			continue
		}
		filtered = append(filtered, project)
	}
	return filtered
}

func filterProjectsByFilter(projects []model.ProjectSummary, projectFilter string) []model.ProjectSummary {
	projectFilter = strings.TrimSpace(projectFilter)
	if len(projects) == 0 {
		return nil
	}
	if projectFilter == "" {
		return append([]model.ProjectSummary(nil), projects...)
	}
	filtered := make([]model.ProjectSummary, 0, len(projects))
	for _, project := range projects {
		if !projectMatchesFilter(project, projectFilter) {
			continue
		}
		filtered = append(filtered, project)
	}
	return filtered
}

func projectMatchesExcludedName(project model.ProjectSummary, excludeProjectPatterns []string) bool {
	if config.ProjectNameExcluded(project.Name, excludeProjectPatterns) {
		return true
	}
	base := filepath.Base(filepath.Clean(project.Path))
	if strings.EqualFold(strings.TrimSpace(base), strings.TrimSpace(project.Name)) {
		return false
	}
	return config.ProjectNameExcluded(base, excludeProjectPatterns)
}

func projectMatchesFilter(project model.ProjectSummary, projectFilter string) bool {
	projectFilter = strings.TrimSpace(projectFilter)
	if projectFilter == "" {
		return true
	}

	filterNeedle := strings.ToLower(projectFilter)
	normalizedNeedle := normalizeProjectFilterToken(projectFilter)
	candidates := []string{
		project.Name,
		filepath.Base(filepath.Clean(project.Path)),
	}
	for _, candidate := range candidates {
		candidate = strings.TrimSpace(candidate)
		if candidate == "" {
			continue
		}
		if strings.Contains(strings.ToLower(candidate), filterNeedle) {
			return true
		}
		if normalizedNeedle != "" && strings.Contains(normalizeProjectFilterToken(candidate), normalizedNeedle) {
			return true
		}
	}
	return false
}

func normalizeProjectFilterToken(value string) string {
	var out strings.Builder
	out.Grow(len(value))
	for _, r := range strings.ToLower(strings.TrimSpace(value)) {
		if r >= 'a' && r <= 'z' {
			out.WriteRune(r)
			continue
		}
		if r >= '0' && r <= '9' {
			out.WriteRune(r)
		}
	}
	return out.String()
}

func (m Model) renderClassificationSummary() string {
	counts := m.classificationCounts()

	parts := []string{
		classificationStyle(model.ClassificationCompleted).Render(fmt.Sprintf("OK=%d", counts.done)),
		classificationStyle(model.ClassificationRunning).Render(fmt.Sprintf("RUN=%d", counts.running)),
		classificationStyle(model.ClassificationPending).Render(fmt.Sprintf("Q=%d", counts.queued)),
		classificationStyle(model.ClassificationFailed).Render(fmt.Sprintf("ERR=%d", counts.failed)),
	}
	return strings.Join(parts, " ")
}

type classificationSummary struct {
	done    int
	queued  int
	running int
	failed  int
}

func (m Model) classificationCounts() classificationSummary {
	projects := m.allProjects
	if len(projects) == 0 {
		projects = m.projects
	}

	counts := classificationSummary{}
	for _, project := range projects {
		switch project.LatestSessionClassification {
		case model.ClassificationCompleted:
			counts.done++
		case model.ClassificationPending:
			counts.queued++
		case model.ClassificationRunning:
			counts.running++
		case model.ClassificationFailed:
			counts.failed++
		}
	}
	return counts
}

func (m Model) classificationFailureCount() int {
	return m.classificationCounts().failed
}

func (m Model) footerAssessmentAlertLabel() string {
	failed := m.classificationFailureCount()
	switch failed {
	case 0:
		return ""
	case 1:
		return "1 assessment error"
	default:
		return fmt.Sprintf("%d assessment errors", failed)
	}
}

func (m Model) renderFooterAssessmentSegment() string {
	text := m.footerAssessmentAlertLabel()
	if text == "" {
		return ""
	}
	if m.spinnerFrame%2 == 0 {
		return lipgloss.NewStyle().Foreground(lipgloss.Color("255")).Background(lipgloss.Color("160")).Bold(true).Render(text)
	}
	return lipgloss.NewStyle().Foreground(lipgloss.Color("255")).Background(lipgloss.Color("88")).Bold(true).Render(text)
}

func (m Model) renderFooter(width int) string {
	usageLabel := m.footerUsageLabel()
	usageSegment := m.renderFooterUsageSegment(usageLabel)
	assessmentSegment := m.renderFooterAssessmentSegment()
	filterSegment := m.renderFooterProjectFilterSegment()
	if m.diffView != nil {
		return renderFooterLine(width, renderDiffFooter(width, *m.diffView, usageSegment), filterSegment, assessmentSegment)
	}
	if m.gitStatusDialog != nil {
		label := gitStatusDialogReadyStatus(*m.gitStatusDialog)
		if m.gitStatusApplying {
			label = "Applying git action..."
		}
		return m.renderModalFooter(width, label, filterSegment, usageSegment, assessmentSegment)
	}
	if m.commitPreview != nil {
		label := commitPreviewReadyStatus(m.commitPreview.CanPush)
		if m.commitApplying {
			label = "Applying git action..."
		} else if m.commitPreviewRefreshing {
			label = "Refreshing commit preview..."
		}
		return m.renderModalFooter(width, label, filterSegment, usageSegment, assessmentSegment)
	}
	if m.newProjectDialog != nil {
		label := "New project: Enter create/add, Space toggle git, Alt+1..3 recent, Esc cancel"
		if m.newProjectDialog.Submitting {
			label = "New project: applying..."
		}
		return m.renderModalFooter(width, label, filterSegment, usageSegment, assessmentSegment)
	}
	if m.projectFilterDialog != nil {
		label := "Project filter: type to narrow, Enter keep, Esc close"
		return m.renderModalFooter(width, label, filterSegment, usageSegment, assessmentSegment)
	}
	if m.commandMode {
		return m.renderModalFooter(width, "Command palette open", filterSegment, usageSegment, assessmentSegment)
	}
	if m.setupMode {
		return m.renderModalFooter(width, "Setup: Enter choose, r refresh, s settings, Esc continue", filterSegment, usageSegment, assessmentSegment)
	}
	if m.settingsMode {
		return m.renderModalFooter(width, "Settings: Enter save, Tab next, Esc cancel", filterSegment, usageSegment, assessmentSegment)
	}
	if m.showPerf {
		return m.renderModalFooter(width, "Performance: c copy, Esc close", filterSegment, usageSegment, assessmentSegment)
	}
	if m.showAIStats {
		return m.renderModalFooter(width, "AI stats: Esc close", filterSegment, usageSegment, assessmentSegment)
	}
	if m.worktreeMergeConfirm != nil {
		if m.worktreeMergeConfirm.Busy {
			return m.renderModalFooter(width, "Merge worktree: waiting for actions to finish", filterSegment, usageSegment, assessmentSegment)
		}
		label := "Merge worktree: Space toggle, Tab navigate, Enter choose, Esc cancel"
		if !worktreeMergeConfirmReady(m.worktreeMergeConfirm) {
			label = "Merge blocked: adjust options or fix repo state, Space toggle, Esc cancel"
		}
		return m.renderModalFooter(width, label, filterSegment, usageSegment, assessmentSegment)
	}
	if m.worktreePostMerge != nil {
		if m.worktreePostMerge.Busy {
			return m.renderModalFooter(width, "Merged worktree: waiting for removal to finish", filterSegment, usageSegment, assessmentSegment)
		}
		return m.renderModalFooter(width, "Merged worktree: Enter remove, Tab keep, Esc keep", filterSegment, usageSegment, assessmentSegment)
	}
	if m.worktreeRemoveConfirm != nil {
		if m.worktreeRemoveConfirm.Busy {
			return m.renderModalFooter(width, "Remove worktree: waiting for git to finish", filterSegment, usageSegment, assessmentSegment)
		}
		return m.renderModalFooter(width, "Remove worktree: Enter remove, Tab switch, Esc cancel", filterSegment, usageSegment, assessmentSegment)
	}
	return renderFooterLine(
		width,
		compactFooterBase(width, m.focusedPane, m.detailViewport.ScrollPercent(), m.runtimeViewport.ScrollPercent(), m.hasHiddenCodexSession(), m.currentEmbeddedLaunchLabel(), m.worktreeFooterActions(width)),
		filterSegment,
		usageSegment,
		assessmentSegment,
	)
}

func (m Model) renderCommandPalette(bodyW int) string {
	panelWidth := min(bodyW, min(max(48, bodyW-10), 84))
	panelInnerWidth := max(24, panelWidth-4)
	return renderDialogPanel(panelWidth, panelInnerWidth, m.renderCommandPaletteContent(panelInnerWidth))
}

func (m Model) renderCommandPaletteOverlay(body string, bodyW, bodyH int) string {
	panel := m.renderCommandPalette(bodyW)
	panelWidth := lipgloss.Width(panel)
	panelHeight := lipgloss.Height(panel)
	left := max(0, (bodyW-panelWidth)/2)
	top := max(0, (bodyH-panelHeight)/2)
	return overlayBlock(body, panel, bodyW, bodyH, left, top)
}

func (m Model) renderCommitPreview(bodyW, bodyH int) string {
	panelWidth := min(bodyW, min(max(54, bodyW-12), 96))
	panelInnerWidth := max(28, panelWidth-4)
	// Reserve space for panel border (2) and vertical centering margin.
	maxContentHeight := max(8, bodyH-4)
	return renderDialogPanel(panelWidth, panelInnerWidth, m.renderCommitPreviewContent(panelInnerWidth, maxContentHeight))
}

func (m Model) renderCommitPreviewOverlay(body string, bodyW, bodyH int) string {
	panel := m.renderCommitPreview(bodyW, bodyH)
	panelWidth := lipgloss.Width(panel)
	panelHeight := lipgloss.Height(panel)
	left := max(0, (bodyW-panelWidth)/2)
	top := max(0, (bodyH-panelHeight)/4)
	return overlayBlock(body, panel, bodyW, bodyH, left, top)
}

func (m Model) renderGitStatusDialog(bodyW int) string {
	panelWidth := min(bodyW, min(max(54, bodyW-12), 96))
	panelInnerWidth := max(28, panelWidth-4)
	return renderDialogPanel(panelWidth, panelInnerWidth, m.renderGitStatusDialogContent(panelInnerWidth))
}

func (m Model) renderGitStatusDialogOverlay(body string, bodyW, bodyH int) string {
	panel := m.renderGitStatusDialog(bodyW)
	panelWidth := lipgloss.Width(panel)
	panelHeight := lipgloss.Height(panel)
	left := max(0, (bodyW-panelWidth)/2)
	top := max(0, (bodyH-panelHeight)/4)
	return overlayBlock(body, panel, bodyW, bodyH, left, top)
}

func (m Model) renderGitStatusDialogContent(width int) string {
	if m.gitStatusDialog == nil {
		return ""
	}
	dialog := *m.gitStatusDialog

	lines := []string{
		renderDialogHeader(dialog.Title, dialog.ProjectName, dialog.Branch, width),
		"",
		commitPreviewLine("Status", dialog.Status),
	}
	if strings.TrimSpace(dialog.RemoteStatus) != "" {
		lines = append(lines, commitPreviewLine("Remote", dialog.RemoteStatus))
	}

	if len(dialog.Warnings) > 0 {
		lines = append(lines, "")
		lines = append(lines, commandPaletteTitleStyle.Render("Next"))
		for _, warning := range dialog.Warnings {
			lines = append(lines, detailWarningStyle.Render("- "+warning))
		}
	}

	lines = append(lines, "")
	if m.gitStatusApplying {
		lines = append(lines, commandPaletteHintStyle.Render("Applying git action..."))
	} else {
		lines = append(lines, renderGitStatusDialogActions(dialog))
	}

	return strings.Join(lines, "\n")
}

func (m Model) renderCommitPreviewContent(width, maxHeight int) string {
	if m.commitPreview == nil {
		return ""
	}
	preview := *m.commitPreview
	placeholder := commitPreviewHasPlaceholderState(preview)

	// --- Fixed lines (always shown) ---
	lines := []string{
		renderDialogHeader("Commit Preview", preview.ProjectName, preview.Branch, width),
		"",
		renderCommitPreviewMessageInline(preview.Message, width),
		commitPreviewLine("Stage", stageModeLabel(preview.StageMode, len(preview.SelectedUntracked))),
	}

	if strings.TrimSpace(preview.LatestSummary) != "" {
		lines = append(lines, commitPreviewLine("Context", preview.LatestSummary))
	}

	// --- Footer (always shown) ---
	var footer []string
	footer = append(footer, "")
	if m.commitApplying {
		footer = append(footer, commandPaletteHintStyle.Render("Applying git action..."))
	} else if m.commitPreviewRefreshing {
		hint := "Refreshing commit preview..."
		if placeholder {
			hint = "Building commit preview..."
		}
		footer = append(footer, commandPaletteHintStyle.Render(hint))
	} else {
		footer = append(footer, renderCommitPreviewActions(preview.CanPush))
	}

	// Budget = maxHeight minus fixed header and footer lines.
	budget := maxHeight - len(lines) - len(footer)

	// --- Optional sections, added in priority order with budget checks ---

	// Changes section (highest priority after message).
	if budget > 2 {
		lines = append(lines, "")
		lines = append(lines, commandPaletteTitleStyle.Render("Changes"))
		budget -= 2
		if placeholder {
			lines = append(lines, detailMutedStyle.Render("- Inspecting repo changes..."))
			budget--
		} else if budget >= 3 {
			// Show individual files when there's room.
			fileLimit := min(6, budget-1) // reserve 1 for diff summary
			fileLines := renderCommitPreviewFiles(preview.Included, fileLimit, width)
			lines = append(lines, fileLines...)
			budget -= len(fileLines)
		}
		if !placeholder && strings.TrimSpace(preview.DiffSummary) != "" && budget > 0 {
			lines = append(lines, commandPaletteHintStyle.Render(strings.TrimSpace(preview.DiffSummary)))
			budget--
		}
	}

	// Left-out files (lower priority — dropped first when tight).
	if !placeholder && len(preview.Excluded) > 0 && budget > 3 {
		lines = append(lines, "")
		lines = append(lines, commandPaletteTitleStyle.Render("Left out"))
		budget -= 2
		fileLimit := min(4, budget)
		fileLines := renderCommitPreviewFiles(preview.Excluded, fileLimit, width)
		lines = append(lines, fileLines...)
		budget -= len(fileLines)
	}

	// TODO completions — always show at least a summary line so the user
	// knows TODOs will be marked done on commit.
	if !placeholder && len(m.commitTodoCompletions) > 0 {
		selectedCount := len(selectedCommitTodoIDs(m.commitTodoCompletions))
		if budget > 3 {
			// Full view: individual items with checkboxes.
			lines = append(lines, "")
			lines = append(lines, commandPaletteTitleStyle.Render("TODOs addressed"))
			budget -= 2
			todoLimit := min(len(m.commitTodoCompletions), budget-1) // reserve 1 for hint
			todoLines := renderCommitTodoCompletions(m.commitTodoCompletions, m.commitTodoSelected, width, todoLimit)
			lines = append(lines, todoLines...)
			budget -= len(todoLines)
		} else if budget > 0 {
			// Collapsed: single summary line.
			summary := fmt.Sprintf("TODOs: %d will be marked done (↑↓/Space to review)", selectedCount)
			if selectedCount == 0 {
				summary = fmt.Sprintf("TODOs: %d suggested, none selected", len(m.commitTodoCompletions))
			}
			lines = append(lines, commitPreviewInfoStyle.Render(summary))
			budget--
		}
	}

	// Warnings (compact: collapse to count when very tight).
	if len(preview.Warnings) > 0 && budget > 2 {
		lines = append(lines, "")
		lines = append(lines, commandPaletteTitleStyle.Render("Warnings"))
		budget -= 2
		warnLimit := min(len(preview.Warnings), max(1, budget))
		for i := 0; i < warnLimit; i++ {
			lines = append(lines, detailWarningStyle.Render("- "+preview.Warnings[i]))
			budget--
		}
		if warnLimit < len(preview.Warnings) {
			lines = append(lines, detailWarningStyle.Render(fmt.Sprintf("+ %d more", len(preview.Warnings)-warnLimit)))
			budget--
		}
	}

	lines = append(lines, footer...)
	return strings.Join(lines, "\n")
}

func commitPreviewHasPlaceholderState(preview service.CommitPreview) bool {
	return len(preview.Included) == 0 &&
		len(preview.Excluded) == 0 &&
		len(preview.SelectedUntracked) == 0 &&
		strings.TrimSpace(preview.DiffSummary) == "" &&
		strings.TrimSpace(preview.DiffStat) == ""
}

func commitPreviewLine(label, value string) string {
	return detailLabelStyle.Render(label+":") + " " + commitPreviewInfoStyle.Render(value)
}

func renderDialogHeader(title, projectName, branch string, width int) string {
	titleWidth := ansi.StringWidth(title)
	if width <= titleWidth {
		return commandPaletteTitleStyle.Render(truncateText(title, max(1, width)))
	}

	projectName = strings.TrimSpace(projectName)
	branch = strings.TrimSpace(branch)
	if projectName == "" && branch == "" {
		return commandPaletteTitleStyle.Render(title)
	}

	suffixPlain := ""
	switch {
	case projectName != "" && branch != "":
		suffixPlain = fmt.Sprintf("%s (%s)", projectName, branch)
	case projectName != "":
		suffixPlain = projectName
	case branch != "":
		suffixPlain = fmt.Sprintf("(%s)", branch)
	}
	if suffixPlain == "" {
		return commandPaletteTitleStyle.Render(title)
	}

	separator := " - "
	if titleWidth+ansi.StringWidth(separator)+ansi.StringWidth(suffixPlain) > width {
		return commandPaletteTitleStyle.Render(title) + commitPreviewInfoStyle.Render(separator+truncateText(suffixPlain, max(1, width-titleWidth-ansi.StringWidth(separator))))
	}

	parts := []string{
		commandPaletteTitleStyle.Render(title),
		commitPreviewInfoStyle.Render(separator),
	}
	if projectName != "" {
		parts = append(parts, dialogProjectTitleStyle.Render(projectName))
	}
	if branch != "" {
		branchText := "(" + branch + ")"
		if projectName != "" {
			branchText = " " + branchText
		}
		parts = append(parts, commitPreviewInfoStyle.Render(branchText))
	}
	return strings.Join(parts, "")
}

func renderCommitPreviewMessageInline(value string, width int) string {
	body := strings.TrimSpace(value)
	if body == "" {
		body = "(empty)"
	}
	label := detailLabelStyle.Render("Message:")
	labelWidth := ansi.StringWidth(label) + 1 // +1 for space
	messageStyle := lipgloss.NewStyle().
		Width(max(12, width-labelWidth)).
		Foreground(lipgloss.Color("229")).
		Bold(true)
	return label + " " + messageStyle.Render(body)
}

func renderCommitPreviewFiles(files []service.CommitFile, limit, width int) []string {
	if len(files) == 0 {
		return []string{detailMutedStyle.Render("- none")}
	}
	maxWidth := max(12, width-6)
	lines := make([]string, 0, min(limit, len(files))+1)
	for _, file := range files[:min(limit, len(files))] {
		row := commitPreviewValueStyle.Render(file.Code) + " " + commitPreviewInfoStyle.Render(truncateText(file.Summary, maxWidth))
		lines = append(lines, row)
	}
	if len(files) > limit {
		lines = append(lines, commandPaletteHintStyle.Render(fmt.Sprintf("+ %d more", len(files)-limit)))
	}
	return lines
}

func stageModeLabel(mode service.GitStageMode, selectedUntracked int) string {
	switch mode {
	case service.GitStageAllChanges:
		return "stage all current changes"
	case service.GitStageStagedOnly:
		if selectedUntracked == 1 {
			return "commit staged changes plus 1 recommended untracked file"
		}
		if selectedUntracked > 1 {
			return fmt.Sprintf("commit staged changes plus %d recommended untracked files", selectedUntracked)
		}
		return "commit staged changes only"
	default:
		return "commit staged changes only"
	}
}

func commitPreviewReadyStatus(canPush bool) string {
	if canPush {
		return "Commit preview ready. Enter commit, Alt+Enter commit & push, d diff, Esc cancel"
	}
	return "Commit preview ready. Enter commit, Alt+Enter unavailable, d diff, Esc cancel"
}

func gitStatusDialogFromNoChanges(err service.NoChangesToCommitError) gitStatusDialog {
	projectName := strings.TrimSpace(err.ProjectName)
	if projectName == "" && strings.TrimSpace(err.ProjectPath) != "" {
		projectName = filepath.Base(err.ProjectPath)
	}
	if projectName == "" {
		projectName = "(unknown project)"
	}

	branch := strings.TrimSpace(err.Branch)
	if branch == "" {
		branch = "(detached)"
	}

	dialog := gitStatusDialog{
		Title:       "Nothing To Commit",
		ProjectPath: err.ProjectPath,
		ProjectName: projectName,
		Branch:      branch,
		Status:      "Working tree is clean.",
		CanPush:     err.CanPush,
		Ahead:       err.Ahead,
	}

	switch {
	case err.Ahead > 0:
		dialog.RemoteStatus = fmt.Sprintf("ahead of upstream by %d commit(s)", err.Ahead)
	case err.Behind > 0:
		dialog.RemoteStatus = fmt.Sprintf("behind upstream by %d commit(s)", err.Behind)
	}

	if err.Ahead > 0 && err.CanPush {
		if err.Ahead == 1 {
			dialog.Warnings = append(dialog.Warnings, "This branch already has 1 local commit ready to push.")
		} else {
			dialog.Warnings = append(dialog.Warnings, fmt.Sprintf("This branch already has %d local commits ready to push.", err.Ahead))
		}
	} else if warning := strings.TrimSpace(err.PushWarning); warning != "" {
		dialog.Warnings = append(dialog.Warnings, warning)
	}

	return dialog
}

func gitStatusDialogReadyStatus(dialog gitStatusDialog) string {
	if ready := strings.TrimSpace(dialog.ReadyStatus); ready != "" {
		return ready
	}
	if dialog.CanPush {
		if dialog.Ahead == 1 {
			return "Nothing new to commit. Enter push 1 existing commit, Esc cancel"
		}
		return fmt.Sprintf("Nothing new to commit. Enter push %d existing commits, Esc cancel", max(1, dialog.Ahead))
	}
	return "Nothing new to commit. Enter close, Esc close"
}

func gitStatusDialogDismissStatus(dialog gitStatusDialog) string {
	if dismiss := strings.TrimSpace(dialog.DismissStatus); dismiss != "" {
		return dismiss
	}
	if dialog.CanPush {
		return "Nothing new to commit. Use /push to send existing commits."
	}
	return "No changes to commit"
}

func gitStatusDialogFromSubmoduleAttention(err service.SubmoduleAttentionError, intent service.GitActionIntent, message string) gitStatusDialog {
	projectName := strings.TrimSpace(err.ProjectName)
	if projectName == "" && strings.TrimSpace(err.ProjectPath) != "" {
		projectName = filepath.Base(err.ProjectPath)
	}
	if projectName == "" {
		projectName = "(unknown project)"
	}

	branch := strings.TrimSpace(err.Branch)
	if branch == "" {
		branch = "(detached)"
	}

	dialog := gitStatusDialog{
		Title:             "Submodule Attention",
		ProjectPath:       err.ProjectPath,
		ProjectName:       projectName,
		Branch:            branch,
		Status:            "Only submodule-local changes are pending.",
		ReadyStatus:       "Submodule needs attention. Enter resolve & continue, Esc close",
		DismissStatus:     "Submodule changes still need attention",
		ResolveSubmodules: true,
		CommitIntent:      intent,
		CommitMessage:     message,
	}

	if len(err.Submodules) == 1 {
		dialog.Warnings = append(dialog.Warnings, fmt.Sprintf("Commit or discard the local changes inside submodule %s before committing the parent repo.", err.Submodules[0]))
	} else if len(err.Submodules) > 1 {
		dialog.Warnings = append(dialog.Warnings, fmt.Sprintf("Commit or discard the local changes inside these submodules before committing the parent repo: %s.", strings.Join(err.Submodules, ", ")))
	}
	dialog.Warnings = append(dialog.Warnings, "Enter resolve & continue will commit all current changes inside those submodules, push them, and then reopen the parent commit preview.")
	if warning := strings.TrimSpace(err.PushWarning); warning != "" {
		dialog.Warnings = append(dialog.Warnings, warning)
	}
	return dialog
}

func renderGitStatusDialogActions(dialog gitStatusDialog) string {
	if dialog.ResolveSubmodules {
		actions := []string{
			renderDialogAction("Enter", "resolve & continue", commitActionKeyStyle, commitActionTextStyle),
			renderDialogAction("Esc", "close", cancelActionKeyStyle, cancelActionTextStyle),
		}
		return strings.Join(actions, "   ")
	}
	primaryLabel := "close"
	keyStyle := commitActionKeyStyle
	textStyle := commitActionTextStyle
	if dialog.CanPush {
		if dialog.Ahead == 1 {
			primaryLabel = "push 1 existing commit"
		} else {
			primaryLabel = fmt.Sprintf("push %d existing commits", max(1, dialog.Ahead))
		}
		keyStyle = pushActionKeyStyle
		textStyle = pushActionTextStyle
	}
	actions := []string{
		renderDialogAction("Enter", primaryLabel, keyStyle, textStyle),
		renderDialogAction("Esc", "close", cancelActionKeyStyle, cancelActionTextStyle),
	}
	return strings.Join(actions, "   ")
}

func buildCommitTodoItems(suggested []service.TodoCompletion) []commitTodoItem {
	if len(suggested) == 0 {
		return nil
	}
	items := make([]commitTodoItem, len(suggested))
	for i, s := range suggested {
		items[i] = commitTodoItem{ID: s.ID, Text: s.Text, Selected: true}
	}
	return items
}

func selectedCommitTodoIDs(items []commitTodoItem) []int64 {
	var ids []int64
	for _, item := range items {
		if item.Selected {
			ids = append(ids, item.ID)
		}
	}
	return ids
}

func renderCommitTodoCompletions(items []commitTodoItem, selected, width, limit int) []string {
	if limit <= 0 {
		limit = len(items)
	}
	visible := min(limit, len(items))
	maxTextWidth := max(12, width-8)
	lines := make([]string, 0, visible+2)
	for i := 0; i < visible; i++ {
		item := items[i]
		checkbox := "[ ] "
		if item.Selected {
			checkbox = "[x] "
		}
		text := truncateText(item.Text, maxTextWidth)
		row := checkbox + text
		if i == selected {
			row = commitPreviewInfoStyle.Bold(true).Render(row)
		} else if item.Selected {
			row = commitPreviewInfoStyle.Render(row)
		} else {
			row = detailMutedStyle.Render(row)
		}
		lines = append(lines, row)
	}
	if visible < len(items) {
		lines = append(lines, commandPaletteHintStyle.Render(fmt.Sprintf("+ %d more", len(items)-visible)))
	}
	lines = append(lines, commandPaletteHintStyle.Render("Space toggle, ↑↓ navigate"))
	return lines
}

func renderCommitPreviewActions(canPush bool) string {
	actions := []string{renderDialogAction("Enter", "commit", commitActionKeyStyle, commitActionTextStyle)}
	if canPush {
		actions = append(actions, renderDialogAction("Alt+Enter", "commit & push", pushActionKeyStyle, pushActionTextStyle))
	} else {
		actions = append(actions, renderDialogAction("Alt+Enter", "push unavailable", disabledActionKeyStyle, disabledActionTextStyle))
	}
	actions = append(actions, renderDialogAction("d", "diff", navigateActionKeyStyle, navigateActionTextStyle))
	actions = append(actions, renderDialogAction("Esc", "cancel", cancelActionKeyStyle, cancelActionTextStyle))
	return strings.Join(actions, "   ")
}

func renderDialogAction(key, label string, keyStyle, labelStyle lipgloss.Style) string {
	return lipgloss.JoinHorizontal(lipgloss.Left, keyStyle.Render(key), dialogPanelFillStyle.Render(" "), labelStyle.Render(label))
}

func renderCommandPaletteActions() string {
	actions := []string{
		renderDialogAction("Enter", "run", commitActionKeyStyle, commitActionTextStyle),
		renderDialogAction("Tab", "complete", navigateActionKeyStyle, navigateActionTextStyle),
		renderDialogAction("Up/Down", "choose", pushActionKeyStyle, pushActionTextStyle),
		renderDialogAction("Esc", "cancel", cancelActionKeyStyle, cancelActionTextStyle),
	}
	return strings.Join(actions, "   ")
}

func (m Model) renderCommandPaletteContent(width int) string {
	lines := []string{
		commandPaletteTitleStyle.Render("Command Palette"),
	}

	if p, ok := m.selectedProject(); ok {
		lines = append(lines, commandPaletteHintStyle.Render("Selected project: "+p.Name))
		if hint := m.worktreeCommandPaletteHint(p, m.worktreeFamily(projectWorktreeRootPath(p))); hint != "" {
			lines = append(lines, commandPaletteHintStyle.Render(hint))
		}
	} else {
		lines = append(lines, commandPaletteHintStyle.Render("Selected project: none"))
	}

	lines = append(lines, "")
	input := m.commandInput
	input.Width = max(12, width-2)
	lines = append(lines, input.View())
	lines = append(lines, renderCommandPaletteActions())
	lines = append(lines, "")
	lines = append(lines, commandPaletteTitleStyle.Render("Suggestions"))

	suggestions := m.commandSuggestions()
	if len(suggestions) == 0 {
		lines = append(lines, commandPaletteHintStyle.Render("No matching commands. Try /help or /refresh."))
	} else {
		start, end := m.commandSuggestionWindow(len(suggestions))
		if start > 0 {
			lines = append(lines, commandPaletteHintStyle.Render(fmt.Sprintf("↑ %d more", start)))
		}
		for i := start; i < end; i++ {
			row := m.renderCommandSuggestionRow(suggestions[i], i == m.commandSelected, width)
			lines = append(lines, row)
		}
		if end < len(suggestions) {
			lines = append(lines, commandPaletteHintStyle.Render(fmt.Sprintf("↓ %d more", len(suggestions)-end)))
		}
	}

	if selected, ok := m.selectedCommandSuggestion(); ok && strings.TrimSpace(selected.Summary) != "" {
		lines = append(lines, "")
		lines = append(lines, commandPaletteTitleStyle.Render("About"))
		lines = append(lines, commandPaletteHintStyle.Render(selected.Summary))
	}

	return strings.Join(lines, "\n")
}

func (m Model) commandSuggestionWindow(total int) (int, int) {
	if total <= 0 {
		return 0, 0
	}

	limit := min(5, total)
	start := 0
	if m.commandSelected >= limit {
		start = m.commandSelected - limit + 1
	}
	maxStart := total - limit
	if start > maxStart {
		start = maxStart
	}
	if start < 0 {
		start = 0
	}
	return start, start + limit
}

func (m Model) renderCommandSuggestionRow(s commands.Suggestion, selected bool, width int) string {
	left := s.Display
	if left == "" {
		left = s.Insert
	}
	right := strings.TrimSpace(s.Summary)
	maxLeft := max(12, min(28, width/3))
	left = truncateText(left, maxLeft)
	if right != "" {
		right = truncateText(right, max(12, width-maxLeft-7))
	}

	marker := " "
	if selected {
		marker = ">"
	}
	row := marker + " " + left
	if right != "" {
		row += "  " + right
	}
	if selected {
		return commandPaletteSelectStyle.Width(width).Render(row)
	}
	row = marker + " " + commandPalettePickStyle.Render(left)
	if right != "" {
		row += "  " + commandPaletteRowStyle.Render(right)
	}
	return commandPaletteRowStyle.Width(width).Render(row)
}

func commandVisibilityMode(mode commands.ViewMode) projectVisibilityMode {
	switch mode {
	case commands.ViewAll:
		return visibilityAllFolders
	default:
		return visibilityAIFolders
	}
}

// overlayBlock keeps the existing panes visible around the modal instead of
// replacing the whole body with a centered popup.
func overlayBlock(base, overlay string, width, height, left, top int) string {
	baseLines := blockLines(base, width, height)
	overlayLines := blockLines(overlay, lipgloss.Width(overlay), lipgloss.Height(overlay))
	overlayWidth := lipgloss.Width(overlay)

	for row, overlayLine := range overlayLines {
		target := top + row
		if target < 0 || target >= len(baseLines) {
			continue
		}
		baseLine := baseLines[target]
		prefix := ansi.Cut(baseLine, 0, left)
		suffix := ansi.Cut(baseLine, left+overlayWidth, width)
		baseLines[target] = prefix + overlayLine + suffix
	}

	return strings.Join(baseLines, "\n")
}

func blockLines(block string, width, height int) []string {
	if width < 0 {
		width = 0
	}
	if height < 0 {
		height = 0
	}
	filled := lipgloss.Place(width, height, lipgloss.Left, lipgloss.Top, block)
	lines := strings.Split(filled, "\n")
	if len(lines) > height {
		lines = lines[:height]
	}
	for len(lines) < height {
		lines = append(lines, "")
	}
	for i, line := range lines {
		lines[i] = lipgloss.PlaceHorizontal(width, lipgloss.Left, line)
	}
	return lines
}

func (m Model) currentUsage() model.LLMSessionUsage {
	if m.svc == nil {
		return model.LLMSessionUsage{}
	}
	return m.svc.SessionUsage()
}

func (m Model) footerUsageLabel() string {
	if !m.setupChecked {
		switch backend := m.currentSettingsBaseline().AIBackend; backend {
		case config.AIBackendDisabled:
			return "AI disabled"
		default:
			if backend.UsesLocalProviderPath() {
				return compactLocalUsageLabel(backend.Label(), m.currentUsage())
			}
			return compactUsageLabel(m.currentUsage())
		}
	}
	switch status := m.setupSnapshot.SelectedStatus(); {
	case m.setupSnapshot.NeedsSetup():
		return "AI setup"
	case m.setupSnapshot.Selected == config.AIBackendDisabled:
		return "AI disabled"
	case m.setupSnapshot.Selected != config.AIBackendUnset && !status.Ready:
		return "AI unavailable"
	default:
		if m.setupSnapshot.Selected.UsesLocalProviderPath() {
			return compactLocalUsageLabel(m.setupSnapshot.Selected.Label(), m.currentUsage())
		}
		return compactUsageLabel(m.currentUsage())
	}
}

func (m Model) aiBackendStatusNotice() string {
	if !m.setupChecked {
		switch m.currentSettingsBaseline().AIBackend {
		case config.AIBackendDisabled:
			return "AI disabled"
		default:
			return ""
		}
	}
	switch status := m.setupSnapshot.SelectedStatus(); {
	case m.setupSnapshot.NeedsSetup():
		return "Use /setup to enable AI"
	case m.setupSnapshot.Selected == config.AIBackendDisabled:
		return "AI disabled"
	case m.setupSnapshot.Selected != config.AIBackendUnset && !status.Ready:
		return "AI unavailable (use /setup)"
	default:
		return ""
	}
}

func (m Model) renderAIBackendStatusNotice() string {
	notice := m.aiBackendStatusNotice()
	if notice == "" {
		return ""
	}
	switch {
	case m.setupSnapshot.NeedsSetup():
		return topStatusSetupBadgeStyle.Render(notice)
	case m.setupSnapshot.Selected == config.AIBackendDisabled:
		return detailMutedStyle.Render(notice)
	default:
		return topStatusWarningBadgeStyle.Render(notice)
	}
}

func compactUsageLabel(usage model.LLMSessionUsage) string {
	if !usage.Enabled {
		return "cost off"
	}
	estimatedCostUSD, ok := estimatedUsageCostUSD(usage)
	if !ok {
		return "cost ?"
	}
	return "cost " + formatEstimatedCostUSD(estimatedCostUSD)
}

func compactLocalUsageLabel(providerLabel string, usage model.LLMSessionUsage) string {
	providerLabel = strings.TrimSpace(providerLabel)
	if providerLabel == "" {
		providerLabel = "AI"
	}
	if usage.Running > 0 {
		return providerLabel + " running"
	}
	callCount := usage.Completed + usage.Failed
	if callCount <= 0 {
		callCount = usage.Started
	}
	if callCount <= 0 {
		return providerLabel + " ready"
	}
	if callCount == 1 {
		return providerLabel + " 1 call"
	}
	return fmt.Sprintf("%s %d calls", providerLabel, callCount)
}

func (m Model) renderFooterUsageSegment(text string) string {
	text = strings.TrimSpace(text)
	if text == "" {
		return ""
	}
	switch text {
	case "AI setup":
		return topStatusSetupBadgeStyle.Render(text)
	case "AI unavailable":
		return topStatusWarningBadgeStyle.Render(text)
	case "AI disabled":
		return detailMutedStyle.Render(text)
	}
	if !m.usagePulseUntil.After(m.currentTime()) {
		return renderFooterUsage(text)
	}
	if m.spinnerFrame%2 == 0 {
		return lipgloss.NewStyle().Foreground(lipgloss.Color("16")).Background(lipgloss.Color("186")).Bold(true).Render(text)
	}
	return lipgloss.NewStyle().Foreground(lipgloss.Color("230")).Background(lipgloss.Color("59")).Bold(true).Render(text)
}

func compactFooterBase(width int, focused paneFocus, detailScroll, runtimeScroll float64, hasHiddenCodex bool, launchLabel string, projectActions []footerAction) string {
	if strings.TrimSpace(launchLabel) == "" {
		launchLabel = "Session"
	}
	if focused == focusDetail {
		detailPercent := int(detailScroll * 100)
		switch {
		case width >= 80:
			return joinFooterSegments(
				renderFooterMeta("Focus: detail"),
				renderFooterActionList(
					footerHideAction("Esc", "list"),
					footerNavAction("PgUp/PgDn", "page"),
					footerNavAction("/", "command"),
					footerNavAction("Tab", "switch"),
					footerLowAction("?", "help"),
					footerExitAction("q", "quit"),
				),
				renderFooterStatus(fmt.Sprintf("%d%%", detailPercent)),
			)
		case width >= 60:
			return joinFooterSegments(
				renderFooterMeta("Focus: detail"),
				renderFooterActionList(
					footerHideAction("Esc", "list"),
					footerNavAction("/", "command"),
					footerNavAction("Tab", "switch"),
					footerLowAction("?", "help"),
					footerExitAction("q", "quit"),
				),
				renderFooterStatus(fmt.Sprintf("%d%%", detailPercent)),
			)
		default:
			return joinFooterSegments(
				renderFooterMeta("Detail"),
				renderFooterActionList(
					footerHideAction("Esc", "list"),
					footerNavAction("/", "cmd"),
					footerLowAction("?", "help"),
					footerExitAction("q", "quit"),
				),
			)
		}
	}
	if focused == focusRuntime {
		runtimePercent := int(runtimeScroll * 100)
		switch {
		case width >= 80:
			return joinFooterSegments(
				renderFooterMeta("Focus: runtime"),
				renderFooterActionList(
					footerPrimaryAction("Enter", "action"),
					footerNavAction("Left/Right", "pick"),
					footerNavAction("PgUp/PgDn", "page"),
					footerNavAction("Tab", "switch"),
					footerHideAction("Esc", "list"),
					footerLowAction("?", "help"),
					footerExitAction("q", "quit"),
				),
				renderFooterStatus(fmt.Sprintf("%d%%", runtimePercent)),
			)
		case width >= 60:
			return joinFooterSegments(
				renderFooterMeta("Focus: runtime"),
				renderFooterActionList(
					footerPrimaryAction("Enter", "action"),
					footerNavAction("L/R", "pick"),
					footerNavAction("Tab", "switch"),
					footerHideAction("Esc", "list"),
					footerLowAction("?", "help"),
					footerExitAction("q", "quit"),
				),
				renderFooterStatus(fmt.Sprintf("%d%%", runtimePercent)),
			)
		default:
			return joinFooterSegments(
				renderFooterMeta("Runtime"),
				renderFooterActionList(
					footerPrimaryAction("Enter", "run"),
					footerNavAction("L/R", "pick"),
					footerHideAction("Esc", "list"),
					footerExitAction("q", "quit"),
				),
			)
		}
	}
	switch {
	case width >= 80:
		if hasHiddenCodex {
			actions := []footerAction{
				footerPrimaryAction("Enter", launchLabel),
				footerNavAction("Alt+Down", "picker"),
			}
			actions = append(actions, projectActions...)
			actions = append(actions,
				footerNavAction("Alt+[/]", "sessions"),
				footerNavAction("f", "filter"),
				footerNavAction("/", "command"),
				footerLowAction("?", "help"),
				footerExitAction("q", "quit"),
			)
			return joinFooterSegments(
				renderFooterActionList(actions...),
			)
		}
		actions := []footerAction{
			footerPrimaryAction("Enter", launchLabel),
			footerNavAction("Alt+Down", "picker"),
		}
		actions = append(actions, projectActions...)
		actions = append(actions,
			footerNavAction("f", "filter"),
			footerNavAction("/", "command"),
			footerNavAction("Tab", "switch"),
			footerNavAction("t", "TODO"),
			footerLowAction("?", "help"),
			footerExitAction("q", "quit"),
		)
		return joinFooterSegments(
			renderFooterActionList(actions...),
		)
	case width >= 60:
		if hasHiddenCodex {
			actions := []footerAction{
				footerPrimaryAction("Enter", launchLabel),
				footerNavAction("Alt+Down", "picker"),
			}
			actions = append(actions, projectActions...)
			actions = append(actions,
				footerNavAction("f", "filter"),
				footerNavAction("/", "command"),
				footerLowAction("?", "help"),
				footerExitAction("q", "quit"),
			)
			return joinFooterSegments(
				renderFooterActionList(actions...),
			)
		}
		actions := []footerAction{
			footerPrimaryAction("Enter", launchLabel),
			footerNavAction("Alt+Down", "picker"),
		}
		actions = append(actions, projectActions...)
		actions = append(actions,
			footerNavAction("f", "filter"),
			footerNavAction("/", "command"),
			footerNavAction("Tab", "switch"),
			footerLowAction("?", "help"),
			footerExitAction("q", "quit"),
		)
		return joinFooterSegments(
			renderFooterActionList(actions...),
		)
	default:
		actions := []footerAction{
			footerPrimaryAction("Enter", launchLabel),
			footerNavAction("/", "cmd"),
			footerLowAction("?", "help"),
			footerExitAction("q", "quit"),
		}
		if hasHiddenCodex {
			actions = []footerAction{
				footerPrimaryAction("Enter", launchLabel),
				footerNavAction("/", "cmd"),
				footerExitAction("q", "quit"),
			}
		}
		return joinFooterSegments(renderFooterActionList(actions...))
	}
}

func formatTokenCount(v int64) string {
	switch {
	case v >= 1_000_000:
		return fmt.Sprintf("%.1fM", float64(v)/1_000_000)
	case v >= 10_000:
		return fmt.Sprintf("%dk", v/1_000)
	case v >= 1_000:
		return fmt.Sprintf("%.1fk", float64(v)/1_000)
	default:
		return fmt.Sprintf("%d", v)
	}
}

func estimatedUsageCostUSD(usage model.LLMSessionUsage) (float64, bool) {
	if usage.Totals.EstimatedCostUSD > 0 {
		return usage.Totals.EstimatedCostUSD, true
	}
	if usage.Totals.InputTokens == 0 && usage.Totals.OutputTokens == 0 && usage.Totals.TotalTokens == 0 {
		return 0, true
	}
	if estimatedCostUSD, ok := model.EstimateLLMCostUSD(usage.Model, usage.Totals); ok {
		return estimatedCostUSD, true
	}
	return 0, false
}

func formatEstimatedCostUSD(costUSD float64) string {
	switch {
	case costUSD >= 1:
		return fmt.Sprintf("$%.2f", costUSD)
	case costUSD >= 0.01:
		return fmt.Sprintf("$%.3f", costUSD)
	default:
		return fmt.Sprintf("$%.4f", costUSD)
	}
}

func helpPanelLines() []string {
	return []string{
		detailSectionStyle.Render("Palette"),
		renderHelpPanelActionRow(
			renderDialogAction("/", "open slash-command palette", navigateActionKeyStyle, navigateActionTextStyle),
			renderDialogAction("Tab", "complete there", navigateActionKeyStyle, navigateActionTextStyle),
			renderDialogAction("?", "toggle help", commitActionKeyStyle, commitActionTextStyle),
		),
		commandPaletteHintStyle.Render("Try /setup, /ai, /perf, /errors, /codex, /todo, /wt merge|remove|prune, /commit, /diff, or /run."),
		detailSectionStyle.Render("Navigate"),
		renderHelpPanelActionRow(
			renderDialogAction("Tab", "switch pane", navigateActionKeyStyle, navigateActionTextStyle),
			renderDialogAction("↑/↓ or j/k", "move", navigateActionKeyStyle, navigateActionTextStyle),
			renderDialogAction("PgUp/PgDn", "page", navigateActionKeyStyle, navigateActionTextStyle),
		),
		renderHelpPanelActionRow(
			renderDialogAction("Enter", "open/send", commitActionKeyStyle, commitActionTextStyle),
			renderDialogAction("Esc", "back out", cancelActionKeyStyle, cancelActionTextStyle),
		),
		detailSectionStyle.Render("Quick Actions"),
		renderHelpPanelActionRow(
			renderDialogAction("f", "filter", navigateActionKeyStyle, navigateActionTextStyle),
			renderDialogAction("t", "todo", commitActionKeyStyle, commitActionTextStyle),
			renderDialogAction("o/v", "sort/view", navigateActionKeyStyle, navigateActionTextStyle),
			renderDialogAction("p", "pin", pushActionKeyStyle, pushActionTextStyle),
			renderDialogAction("Ctrl+V", "image", pushActionKeyStyle, pushActionTextStyle),
		),
		detailSectionStyle.Render("Compose & Status"),
		renderHelpPanelActionRow(
			renderDialogAction("Alt+Enter", "newline", commitActionKeyStyle, commitActionTextStyle),
			renderDialogAction("Ctrl+C", "interrupt busy session", cancelActionKeyStyle, cancelActionTextStyle),
		),
		detailSectionStyle.Render("Legend"),
		renderHelpPanelLegendLine(),
		renderHelpPanelActionRow(
			renderDialogAction("q", "quit", disabledActionKeyStyle, disabledActionTextStyle),
		),
	}
}

func renderHelpPanelActionRow(parts ...string) string {
	filtered := make([]string, 0, len(parts))
	for _, part := range parts {
		if strings.TrimSpace(part) == "" {
			continue
		}
		filtered = append(filtered, part)
	}
	return strings.Join(filtered, dialogPanelFillStyle.Render("   "))
}

func renderHelpPanelLegendLine() string {
	legend := []string{
		renderDialogAction("AGENT", "live", detailLabelStyle, detailValueStyle),
		renderDialogAction("TODO", "open", commitActionKeyStyle, commitActionTextStyle),
		renderDialogAction("RUN", "runtime", pushActionKeyStyle, pushActionTextStyle),
		renderDialogAction("!", "warning", cancelActionKeyStyle, cancelActionTextStyle),
	}
	return strings.Join(legend, "  ")
}

func renderDialogPanel(panelWidth, panelInnerWidth int, content string) string {
	return lipgloss.NewStyle().
		Width(panelWidth).
		Border(lipgloss.RoundedBorder()).
		BorderForeground(dialogPanelBorderColor).
		Padding(0, 1).
		Background(dialogPanelBackground).
		Foreground(lipgloss.Color("252")).
		Render(fillDialogBlock(content, panelInnerWidth))
}

func fillDialogBlock(content string, width int) string {
	if width <= 0 {
		return content
	}
	lines := strings.Split(content, "\n")
	for i, line := range lines {
		lines[i] = fillDialogLine(line, width)
	}
	return strings.Join(lines, "\n")
}

func fillDialogLine(line string, width int) string {
	if width <= 0 {
		return line
	}
	line = dialogPanelResetReplacer.Replace(line)
	visibleWidth := lipgloss.Width(line)
	line = dialogPanelFillStyle.Render(line)
	if visibleWidth >= width {
		return line
	}
	return line + dialogPanelFillStyle.Render(strings.Repeat(" ", width-visibleWidth))
}

func fitFooterWidth(line string, width int) string {
	if width <= 0 || lipgloss.Width(line) <= width {
		return line
	}
	if width <= 3 {
		return ansi.Cut(line, 0, width)
	}
	return ansi.Truncate(line, width, "...")
}

func formatSnoozeDuration(d time.Duration) string {
	switch d {
	case time.Hour:
		return "1 hour"
	case 4 * time.Hour:
		return "4 hours"
	case 24 * time.Hour:
		return "24 hours"
	}
	if d%(24*time.Hour) == 0 {
		days := int(d / (24 * time.Hour))
		if days == 1 {
			return "1 day"
		}
		return fmt.Sprintf("%d days", days)
	}
	return d.String()
}

func (m Model) renderHelpPanel(bodyW int) string {
	panelWidth := min(bodyW, min(max(58, bodyW-12), 80))
	panelInnerWidth := max(30, panelWidth-4)
	contentLines := []string{commandPaletteTitleStyle.Render("Help")}
	contentLines = append(contentLines, helpPanelLines()...)
	content := lipgloss.NewStyle().
		Width(panelInnerWidth).
		Render(strings.Join(contentLines, "\n"))
	return lipgloss.NewStyle().
		Width(panelWidth).
		Border(lipgloss.RoundedBorder()).
		BorderForeground(lipgloss.Color("214")).
		Padding(0, 1, 1, 1).
		Background(lipgloss.Color("234")).
		Foreground(lipgloss.Color("252")).
		Render(content)
}

func (m Model) renderHelpPanelOverlay(body string, bodyW, bodyH int) string {
	panel := m.renderHelpPanel(bodyW)
	panelWidth := lipgloss.Width(panel)
	panelHeight := lipgloss.Height(panel)
	left := max(0, (bodyW-panelWidth)/2)
	top := max(0, (bodyH-panelHeight)/5)
	return overlayBlock(body, panel, bodyW, bodyH, left, top)
}

func (m Model) classificationTag(project model.ProjectSummary) string {
	switch project.LatestSessionClassification {
	case model.ClassificationCompleted:
		return "OK"
	case model.ClassificationPending:
		return "Q"
	case model.ClassificationRunning:
		return spinnerFrames[m.spinnerFrame%len(spinnerFrames)]
	case model.ClassificationFailed:
		return "ERR"
	default:
		if project.LatestSessionFormat == "" {
			return "--"
		}
		return ".."
	}
}

func projectDisplayStatus(project model.ProjectSummary) string {
	return projectActivityStatus(project)
}

func recentlyMoved(movedAt time.Time) bool {
	if movedAt.IsZero() {
		return false
	}
	age := time.Since(movedAt)
	return age >= -time.Minute && age <= recentMoveWindow
}

func moveStatusActive(movedAt time.Time, currentPath, latestDetectedPath string) bool {
	if !recentlyMoved(movedAt) {
		return false
	}
	if latestDetectedPath == "" {
		return true
	}
	return filepath.Clean(latestDetectedPath) != filepath.Clean(currentPath)
}

func statusDisplayStyle(project model.ProjectSummary, now time.Time, stuckThreshold time.Duration) lipgloss.Style {
	if projectMissing(project) || moveStatusActive(project.MovedAt, project.Path, project.LatestSessionDetectedProjectPath) {
		return activityDisplayStyle(project)
	}
	if _, _, ok := visibleAssessmentStatusLabelAt(project, now, stuckThreshold); ok {
		return assessmentDisplayStyle(project, now, stuckThreshold)
	}
	if project.LatestSessionFormat != "" {
		return detailMutedStyle
	}
	return activityDisplayStyle(project)
}

func assessmentFlashStyle(style lipgloss.Style) lipgloss.Style {
	return style.Foreground(lipgloss.Color("16")).Background(lipgloss.Color("186")).Bold(true)
}

func (m Model) projectListAssessmentStatusStyle(project model.ProjectSummary) lipgloss.Style {
	style := statusDisplayStyle(project, m.currentTime(), m.assessmentStallThreshold())
	if m.assessmentFlashActive(project.Path) {
		return assessmentFlashStyle(style)
	}
	return style
}

func (m Model) projectListAssessmentSummaryStyle(project model.ProjectSummary) lipgloss.Style {
	style := projectAssessmentStyle(project, m.currentTime(), m.assessmentStallThreshold())
	if m.assessmentFlashActive(project.Path) {
		return assessmentFlashStyle(style)
	}
	return style
}

func effectiveAssessmentForProject(project model.ProjectSummary, now time.Time, stuckThreshold time.Duration) sessionclassify.EffectiveAssessment {
	return sessionclassify.DeriveEffectiveAssessment(sessionclassify.EffectiveAssessmentInput{
		Status:               project.LatestSessionClassification,
		Category:             project.LatestSessionClassificationType,
		Summary:              project.LatestSessionSummary,
		LastEventAt:          project.LatestSessionLastEventAt,
		LatestTurnStateKnown: project.LatestTurnStateKnown,
		LatestTurnCompleted:  project.LatestTurnCompleted,
		Now:                  now,
		StuckThreshold:       stuckThreshold,
	})
}

func projectAssessmentUnread(project model.ProjectSummary, now time.Time, stuckThreshold time.Duration) bool {
	return !projectAssessmentUnreadAt(project, now, stuckThreshold).IsZero() && attention.AssessmentUnread(project)
}

func projectAssessmentUnreadAt(project model.ProjectSummary, now time.Time, stuckThreshold time.Duration) time.Time {
	if _, _, ok := visibleAssessmentStatusLabelAt(project, now, stuckThreshold); !ok {
		return time.Time{}
	}
	return attention.AssessmentUnreadAt(project)
}

func assessmentStatusLabel(project model.ProjectSummary, compact bool) (string, model.SessionCategory, bool) {
	return assessmentStatusLabelAt(project, compact, time.Time{}, 0)
}

func assessmentStatusLabelAt(project model.ProjectSummary, compact bool, now time.Time, stuckThreshold time.Duration) (string, model.SessionCategory, bool) {
	effective := effectiveAssessmentForProject(project, now, stuckThreshold)
	if effective.Status != model.ClassificationCompleted {
		return "", model.SessionCategoryUnknown, false
	}
	return assessmentStatusLabelForCategory(effective.Category, compact)
}

func assessmentStatusLabelForCategory(category model.SessionCategory, compact bool) (string, model.SessionCategory, bool) {
	_ = compact
	switch category {
	case model.SessionCategoryCompleted:
		return "done", model.SessionCategoryCompleted, true
	case model.SessionCategoryBlocked:
		return "blocked", model.SessionCategoryBlocked, true
	case model.SessionCategoryWaitingForUser:
		return "waiting", model.SessionCategoryWaitingForUser, true
	case model.SessionCategoryNeedsFollowUp:
		return "followup", model.SessionCategoryNeedsFollowUp, true
	case model.SessionCategoryInProgress:
		return "working", model.SessionCategoryInProgress, true
	default:
		return "", model.SessionCategoryUnknown, false
	}
}

func latestCompletedAssessmentStatusLabel(project model.ProjectSummary, compact bool) (string, model.SessionCategory, bool) {
	if project.LatestCompletedSessionClassificationType == "" || project.LatestCompletedSessionClassificationType == model.SessionCategoryUnknown {
		return "", model.SessionCategoryUnknown, false
	}
	return assessmentStatusLabelForCategory(project.LatestCompletedSessionClassificationType, compact)
}

func visibleAssessmentStatusLabel(project model.ProjectSummary) (string, model.SessionCategory, bool) {
	return visibleAssessmentStatusLabelAt(project, time.Time{}, 0)
}

func visibleAssessmentStatusLabelAt(project model.ProjectSummary, now time.Time, stuckThreshold time.Duration) (string, model.SessionCategory, bool) {
	if label, category, ok := assessmentStatusLabelAt(project, false, now, stuckThreshold); ok {
		return label, category, true
	}
	return latestCompletedAssessmentStatusLabel(project, false)
}

func projectAssessmentRefreshing(project model.ProjectSummary) bool {
	switch project.LatestSessionClassification {
	case model.ClassificationPending, model.ClassificationRunning:
		return true
	default:
		return false
	}
}

func classificationProgressCompactLabel(stage model.SessionClassificationStage) string {
	switch stage {
	case model.ClassificationStagePreparingSnapshot:
		return "snapshot"
	case model.ClassificationStageWaitingForModel:
		return "model"
	default:
		return "running"
	}
}

func attentionStatusLabel(status model.ProjectStatus) string {
	switch status {
	case model.StatusPossiblyStuck:
		return "stuck"
	default:
		return string(status)
	}
}

func statusStyle(status model.ProjectStatus) lipgloss.Style {
	switch status {
	case model.StatusActive:
		return lipgloss.NewStyle().Foreground(lipgloss.Color("42")).Bold(true)
	case model.StatusPossiblyStuck:
		return lipgloss.NewStyle().Foreground(lipgloss.Color("203")).Bold(true)
	default:
		return lipgloss.NewStyle().Foreground(lipgloss.Color("214"))
	}
}

func classificationStyle(status model.SessionClassificationStatus) lipgloss.Style {
	switch status {
	case model.ClassificationCompleted:
		return lipgloss.NewStyle().Foreground(lipgloss.Color("42")).Bold(true)
	case model.ClassificationPending:
		return lipgloss.NewStyle().Foreground(lipgloss.Color("214")).Bold(true)
	case model.ClassificationRunning:
		return lipgloss.NewStyle().Foreground(lipgloss.Color("81")).Bold(true)
	case model.ClassificationFailed:
		return lipgloss.NewStyle().Foreground(lipgloss.Color("203")).Bold(true)
	default:
		return lipgloss.NewStyle().Foreground(lipgloss.Color("241"))
	}
}

func classificationCategoryStyle(category model.SessionCategory) lipgloss.Style {
	switch category {
	case model.SessionCategoryCompleted:
		return lipgloss.NewStyle().Foreground(lipgloss.Color("42")).Bold(true)
	case model.SessionCategoryBlocked:
		return lipgloss.NewStyle().Foreground(lipgloss.Color("203")).Bold(true)
	case model.SessionCategoryWaitingForUser:
		return lipgloss.NewStyle().Foreground(lipgloss.Color("214")).Bold(true)
	case model.SessionCategoryNeedsFollowUp:
		return lipgloss.NewStyle().Foreground(lipgloss.Color("81")).Bold(true)
	case model.SessionCategoryInProgress:
		return lipgloss.NewStyle().Foreground(lipgloss.Color("229")).Bold(true)
	default:
		return detailMutedStyle
	}
}

func projectRunStyle(state projectRunState) lipgloss.Style {
	switch state {
	case projectRunActive:
		return lipgloss.NewStyle().Foreground(lipgloss.Color("81")).Bold(true)
	case projectRunError:
		return detailDangerStyle
	default:
		return detailMutedStyle
	}
}

func sessionCategoryLabel(category model.SessionCategory) string {
	switch category {
	case model.SessionCategoryCompleted:
		return "done"
	case model.SessionCategoryBlocked:
		return "blocked"
	case model.SessionCategoryWaitingForUser:
		return "waiting"
	case model.SessionCategoryNeedsFollowUp:
		return "followup"
	case model.SessionCategoryInProgress:
		return "working"
	case model.SessionCategoryUnknown:
		return "unknown"
	default:
		label := strings.ReplaceAll(string(category), "_", " ")
		if strings.TrimSpace(label) == "" {
			return "unknown"
		}
		return label
	}
}

func sourceStyle(format string, live bool) lipgloss.Style {
	return sourceStyleForTag(sourceTag(format), live)
}

func sourceStyleForTag(tag string, live bool) lipgloss.Style {
	switch tag {
	case "CX":
		style := lipgloss.NewStyle().Foreground(lipgloss.Color("81")).Bold(true)
		if !live {
			style = style.Foreground(lipgloss.Color("66")).Faint(true)
		}
		return style
	case "OC":
		style := lipgloss.NewStyle().Foreground(lipgloss.Color("208")).Bold(true)
		if !live {
			style = style.Foreground(lipgloss.Color("172")).Faint(true)
		}
		return style
	case "CC":
		style := lipgloss.NewStyle().Foreground(lipgloss.Color("215")).Bold(true)
		if !live {
			style = style.Foreground(lipgloss.Color("137")).Faint(true)
		}
		return style
	default:
		return lipgloss.NewStyle().Foreground(lipgloss.Color("241"))
	}
}

func sourceTag(format string) string {
	switch format {
	case "modern", "legacy":
		return "CX"
	case "opencode_db":
		return "OC"
	case "claude_code":
		return "CC"
	default:
		return "--"
	}
}

func sourceLabel(format string) string {
	switch sourceTag(format) {
	case "CX":
		return "Codex"
	case "OC":
		return "OpenCode"
	case "CC":
		return "Claude Code"
	default:
		return "None"
	}
}

func (m *Model) sortProjects(projects []model.ProjectSummary) {
	attentionScores := make(map[string]int, len(projects))
	attentionScoreFor := func(project model.ProjectSummary) int {
		if score, ok := attentionScores[project.Path]; ok {
			return score
		}
		score := m.projectAttentionScore(project)
		attentionScores[project.Path] = score
		return score
	}

	switch m.sortMode {
	case sortByRecent:
		sort.SliceStable(projects, func(i, j int) bool {
			li := projects[i].LastActivity
			lj := projects[j].LastActivity
			if li.IsZero() != lj.IsZero() {
				return !li.IsZero()
			}
			if !li.Equal(lj) {
				return li.After(lj)
			}
			scoreI := attentionScoreFor(projects[i])
			scoreJ := attentionScoreFor(projects[j])
			if scoreI != scoreJ {
				return scoreI > scoreJ
			}
			return projects[i].Name < projects[j].Name
		})
	default:
		sort.SliceStable(projects, func(i, j int) bool {
			scoreI := attentionScoreFor(projects[i])
			scoreJ := attentionScoreFor(projects[j])
			if scoreI != scoreJ {
				return scoreI > scoreJ
			}
			li := projects[i].LastActivity
			lj := projects[j].LastActivity
			if li.IsZero() != lj.IsZero() {
				return !li.IsZero()
			}
			if !li.Equal(lj) {
				return li.After(lj)
			}
			return projects[i].Name < projects[j].Name
		})
	}
}

func (m *Model) indexByPath(path string) int {
	for i, p := range m.projects {
		if p.Path == path {
			return i
		}
	}
	return -1
}

func (m *Model) rowsVisible() int {
	layout := m.bodyLayout()
	inner := layout.listPaneHeight - 2 // border
	if inner < 3 {
		inner = 3
	}
	rows := inner - 1 // list header
	if rows < 1 {
		return 1
	}
	return rows
}

func (m *Model) ensureSelectionVisible() {
	if len(m.projects) == 0 {
		m.selected = 0
		m.offset = 0
		return
	}
	if m.selected < 0 {
		m.selected = 0
	}
	if m.selected >= len(m.projects) {
		m.selected = len(m.projects) - 1
	}
	visible := m.rowsVisible()
	maxOffset := max(0, len(m.projects)-visible)
	if m.offset > maxOffset {
		m.offset = maxOffset
	}
	if m.selected < m.offset {
		m.offset = m.selected
	}
	if m.selected >= m.offset+visible {
		m.offset = m.selected - visible + 1
	}
	if m.offset < 0 {
		m.offset = 0
	}
	if m.offset > maxOffset {
		m.offset = maxOffset
	}
}

func max(a, b int) int {
	if a > b {
		return a
	}
	return b
}

func min(a, b int) int {
	if a < b {
		return a
	}
	return b
}
