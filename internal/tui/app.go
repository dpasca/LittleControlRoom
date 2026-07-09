package tui

import (
	"context"
	"errors"
	"fmt"
	"github.com/charmbracelet/bubbles/cursor"
	"github.com/charmbracelet/bubbles/textarea"
	"github.com/charmbracelet/bubbles/textinput"
	"github.com/charmbracelet/bubbles/viewport"
	tea "github.com/charmbracelet/bubbletea"
	"lcroom/internal/aibackend"
	bossui "lcroom/internal/boss"
	"lcroom/internal/brand"
	"lcroom/internal/browserctl"
	"lcroom/internal/codexapp"
	"lcroom/internal/config"
	"lcroom/internal/events"
	"lcroom/internal/inputcomposer"
	"lcroom/internal/model"
	"lcroom/internal/procinspect"
	"lcroom/internal/projectrun"
	"lcroom/internal/service"
	"lcroom/internal/sessionclassify"
	"os"
	"path/filepath"
	"strings"
	"time"
)

type Model struct {
	ctx   context.Context
	svc   *service.Service
	busCh <-chan events.Event
	unsub func()

	allProjects             []model.ProjectSummary
	archivedProjects        []model.ProjectSummary
	projectCategories       []model.ProjectCategory
	openAgentTasks          []model.AgentTask
	orphanedWorktreesByRoot map[string][]model.ProjectSummary
	projects                []model.ProjectSummary
	projectRows             []projectListRow
	detail                  model.ProjectDetail
	selected                int
	offset                  int
	sortMode                projectSortMode
	visibility              projectVisibilityMode
	archiveMode             projectArchiveMode
	selectedCategoryID      string
	excludeProjectPatterns  []string
	privacyMode             bool
	privacyPatterns         []string

	loading bool
	status  string
	err     error

	topStatusAttentionPulseStatus string
	topStatusAttentionPulseUntil  time.Time

	startupScanCompleted    bool
	suspendedTurnChecked    bool
	dismissedSuspendedTurns map[string]struct{}
	projectsReloadInFlight  bool
	projectsReloadQueued    bool
	scanInFlight            bool
	scanQueued              bool
	scanQueuedForceRetry    bool

	width     int
	height    int
	nowFn     func() time.Time
	homeDirFn func() (string, error)
	homeDir   string

	todoDialog              *todoDialogState
	todoEditor              *todoEditorState
	todoDeleteConfirm       *todoDeleteConfirmState
	scratchTaskAction       *scratchTaskActionConfirmState
	agentTaskAction         *agentTaskActionConfirmState
	projectRemoveConfirm    *projectRemoveConfirmState
	externalStopConfirm     *externalProcessStopConfirmState
	todoLaunchDrafts        map[string]todoLaunchDraftState
	todoPendingSave         *todoPendingSaveState
	todoPendingLaunch       *todoPendingLaunchState
	todoCopyDialog          *todoCopyDialogState
	todoWorktreeEditor      *todoWorktreeEditorState
	todoExistingWorktree    *todoExistingWorktreeDialogState
	todoPendingLaunchDialog *todoPendingLaunchDialogState
	todoModelPickerReturn   *todoModelPickerReturnState
	worktreeMergeConfirm    *worktreeMergeConfirmState
	worktreePostMerge       *worktreePostMergeState
	worktreeRemoveConfirm   *worktreeRemoveConfirmState
	attentionDialog         *attentionDialogState
	suspendedTurnDialog     *suspendedTurnResumeDialogState

	commandMode                         bool
	commandInput                        textinput.Model
	commandSelected                     int
	bossMode                            bool
	helpChatMode                        bool
	bossModelActive                     bool
	bossModel                           bossui.Model
	helpChatModelActive                 bool
	helpChatModel                       bossui.Model
	returnToBossModeAfterCodexHide      bool
	bossSetupPrompt                     *bossSetupPromptState
	errorLogVisible                     bool
	errorLogSelected                    int
	errorLogEntries                     []errorLogEntry
	projectFilter                       string
	projectFilterDialog                 *projectFilterDialogState
	categoryDialog                      *categoryDialogState
	archiveDialog                       *archiveDialogState
	ignoredPickerVisible                bool
	ignoredPickerLoading                bool
	ignoredPickerSelected               int
	ignoredPickerItems                  []model.IgnoredProject
	newProjectDialog                    *newProjectDialogState
	newTaskDialog                       *newTaskDialogState
	runCommandDialog                    *runCommandDialogState
	skillsDialog                        *skillsDialogState
	preferredSelectPath                 string
	diffView                            *diffViewState
	gitStatusDialog                     *gitStatusDialog
	gitStatusApplying                   bool
	commitPreview                       *service.CommitPreview
	commitPreviewMessageOverride        string
	commitPreviewRefreshing             bool
	commitPreviewRequestID              int
	commitApplying                      bool
	commitTodoCompletions               []commitTodoItem
	commitTodoSelected                  int
	setupMode                           bool
	setupChecked                        bool
	setupLoading                        bool
	setupSaving                         bool
	setupReviewMode                     bool
	setupSectionNavigation              bool
	setupSectionMenu                    bool
	setupSectionSelected                int
	setupStep                           setupStep
	setupFocusedRole                    setupRole
	setupSelected                       int
	setupBossSelected                   int
	setupConfigMode                     bool
	setupConfigSelected                 int
	setupModelTier                      config.ModelTier
	setupSnapshot                       aibackend.Snapshot
	localModelPickerVisible             bool
	localModelPickerBackend             config.AIBackend
	localModelPickerSelected            int
	settingsMode                        bool
	settingsSaving                      bool
	settingsFields                      []settingsField
	settingsSectionMenu                 bool
	settingsSectionSelected             int
	settingsSelected                    int
	settingsDrilldown                   settingsDrilldownID
	settingsBaseline                    *config.EditableSettings
	settingsConfigPath                  string
	appDataDirPath                      string
	codexHomePath                       string
	settingsRevealPrivacy               bool
	settingsPrivacyEditor               *settingsPrivacyEditorState
	settingsBossChatPickerVisible       bool
	settingsBossChatPickerSelected      int
	settingsBrowserPickerVisible        bool
	settingsBrowserPickerSelected       int
	settingsAIBackendPickerVisible      bool
	settingsAIBackendPickerSelected     int
	settingsLCAgentProviderVisible      bool
	settingsLCAgentProviderSelected     int
	settingsLCAgentSearchPickerVisible  bool
	settingsLCAgentSearchPickerSelected int
	settingsLCAgentModelPicker          *settingsLCAgentModelPickerState
	settingsLCAgentVisionCheckInFlight  bool
	settingsChoicePicker                *settingsChoicePickerState
	settingsEmbeddedProject             string
	settingsEmbeddedProvider            codexapp.Provider

	detailViewport        viewport.Model
	runtimeViewport       viewport.Model
	runtimeActionSelected int
	focusedPane           paneFocus
	assessmentFlashUntil  map[string]time.Time
	selectionFlashUntil   time.Time
	usagePulseUntil       time.Time
	lastUsageTotals       model.LLMUsage
	haveUsageTotals       bool

	mouseEnabled                  bool
	codexSelection                textSelection
	codexManager                  *codexapp.Manager
	runtimeManager                *projectrun.Manager
	runtimeRefreshInFlight        bool
	runtimeRefreshQueued          bool
	runtimeSnapshots              map[string]projectrun.Snapshot
	runtimeProcessSnapshots       []projectrun.Snapshot
	runtimeProcessSelected        map[string]string
	cpuSnapshot                   procinspect.CPUSnapshot
	cpuMonitorInFlight            bool
	cpuMonitorQueued              bool
	cpuDialog                     *cpuDialogState
	cpuRemediationEditor          *cpuRemediationEditorState
	processScanInFlight           bool
	processScanQueued             bool
	processScanQueuedDialogPath   string
	processReports                map[string]procinspect.ProjectReport
	processDialog                 *processDialogState
	portsDialog                   *portsDialogState
	processWarningLastCount       int
	codexSnapshots                map[string]codexapp.Snapshot
	embeddedProviderOverrides     map[string]codexapp.Provider
	lastEmbeddedProvider          codexapp.Provider
	embeddedActivityInFlight      map[string]bool
	embeddedActivityQueued        map[string]embeddedSessionActivityRecordRequest
	embeddedActivityWatermark     map[string]time.Time
	codexTranscriptRev            map[string]uint64
	codexVisibleProject           string
	codexHiddenProject            string
	codexPendingOpen              *codexPendingOpenState
	codexInput                    textarea.Model
	codexDrafts                   map[string]codexDraft
	codexSuggestedDraftsApplied   map[string]string
	codexTranscriptRenderInFlight map[codexTranscriptRenderKey]struct{}
	codexPanelFocus               embeddedCodexPanelFocus
	codexSidebarSelected          embeddedCodexSidebarSection
	embeddedSidebarDetail         *embeddedSidebarDetailState
	embeddedSidebarDiffs          map[string]embeddedSidebarDiffState
	embeddedSidebarDiffSeq        int64
	embeddedSidebarDiffAutoAt     map[string]time.Time
	pendingGitOperations          map[string]pendingGitOperation
	codexPasteTokenSeq            int
	codexClosedHandled            map[string]struct{}
	codexSkipNextLiveRefresh      map[string]struct{}
	pendingGitSummaries           map[string]string
	pendingGitSummaryExpireNext   map[string]bool
	codexPickerVisible            bool
	codexPickerSelected           int
	codexPickerChoices            []codexSessionChoice
	codexPickerLoading            bool
	codexPickerKind               codexPickerKind
	codexPickerTitle              string
	codexPickerHint               string
	codexPickerEmpty              string
	codexPickerProject            string
	codexPickerProvider           codexapp.Provider
	browserAttention              *browserAttentionNotification
	browserController             *browserctl.Controller
	browserLeaseSnapshot          browserctl.ControllerSnapshot
	managedBrowserStates          map[string]browserctl.ManagedPlaywrightState
	questionNotify                *questionNotification
	codexInputCopyDialog          *inputcomposer.CopyDialogState
	codexInputSelection           *codexInputSelectionState
	codexComposerSelection        textSelection
	codexModelPicker              *codexModelPickerState
	codexLCAgentProviderSetup     *codexLCAgentProviderSetupState
	embeddedModelPrefs            map[codexapp.Provider]embeddedModelPreference
	recentCodexModels             []string
	recentClaudeModels            []string
	recentOpenCodeModels          []string
	recentLCAgentModels           []string
	codexDenseBlockMode           codexDenseBlockMode
	codexArtifactPicker           *codexArtifactPickerState
	codexArtifactLinkScans        map[string]codexArtifactLinkScanState
	codexArtifactLinkScanSeq      int64
	codexLCAgentStatusVisible     map[string]struct{}
	codexSlashSelected            int
	codexToolAnswers              map[string]codexToolAnswerState
	codexViewport                 viewport.Model
	codexTranscriptCache          codexTranscriptRenderCache
	codexViewportContent          codexViewportContentState
	codexTranscriptFullHistory    map[string]struct{}
	codexUpdateAckSeq             map[string]uint64
	codexComposerLastKeyAt        time.Time
	codexComposerLastChangeAt     time.Time
	codexComposerKeyCount         int64
	codexComposerChangeCount      int64
	uiDiagnostics                 *uiStallDiagnostics
	aiLatencyNextID               int64
	aiLatencyInFlight             map[int64]aiLatencyOp
	aiLatencyRecent               []aiLatencySample
	modelSettlePending            map[string]pendingModelSettleOp
	lastSpinnerTickAt             time.Time
	skillsInventorySeq            int64
	pendingBossHostNotices        []bossHostNotice
	bossTrackedTodos              map[string]bossTrackedTodo

	pendingG      bool
	todoLaunchSeq int64

	spinnerFrame  int
	marqueeOffset int
	showSessions  bool
	showEvents    bool
	showHelp      bool
	showAIStats   bool
	showPerf      bool

	hideReasoningSections bool

	newProjectRecentParents []string
	worktreeExpanded        map[string]bool
	detailReloadInFlight    map[string]bool
	detailReloadQueued      map[string]bool
	detailReloadErrors      map[string]string
	summaryReloadInFlight   map[string]bool
	summaryReloadQueued     map[string]bool
}

type codexTranscriptRenderCache struct {
	projectPath    string
	width          int
	denseBlockMode codexDenseBlockMode
	fullHistory    bool
	transcriptRev  uint64
	rendered       string
	links          []codexTranscriptLinkSpan
}

type codexViewportContentState struct {
	projectPath    string
	width          int
	denseBlockMode codexDenseBlockMode
	fullHistory    bool
	transcriptRev  uint64
}

type codexArtifactLinkScanState struct {
	scanSeq        int64
	transcriptRev  uint64
	inFlight       bool
	complete       bool
	nextEntry      int
	nextTextOffset int
	targets        []codexArtifactOpenTarget
}

type actionMsg struct {
	projectPath            string
	selectPath             string
	status                 string
	clearPendingGitSummary bool
	refresh                projectInvalidationIntent
	err                    error
}

type browserOpenMsg struct {
	projectPath             string
	status                  string
	err                     error
	browserLeaseSnapshot    browserctl.ControllerSnapshot
	browserLeaseSnapshotSet bool
	managedBrowserState     browserctl.ManagedPlaywrightState
	managedBrowserStateSet  bool
}

type managedBrowserStateMsg struct {
	sessionKey             string
	state                  browserctl.ManagedPlaywrightState
	err                    error
	retryAttemptsRemaining int
}

type codexArtifactPreviewMsg struct {
	projectPath string
	path        string
	seq         int64
	data        []byte
	err         error
}

type codexArtifactLinkScanMsg struct {
	projectPath    string
	scanSeq        int64
	transcriptRev  uint64
	nextEntry      int
	nextTextOffset int
	complete       bool
	targets        []codexArtifactOpenTarget
}

type runtimeActionMsg struct {
	projectPath string
	status      string
	err         error
}

type externalProcessStopMsg struct {
	projectPath string
	pid         int
	status      string
	err         error
}

type runtimeSnapshotsMsg struct {
	snapshots []projectrun.Snapshot
}

type processScanMsg struct {
	reports           []procinspect.ProjectReport
	dialogProjectPath string
	err               error
}

type cpuSnapshotMsg struct {
	snapshot procinspect.CPUSnapshot
	err      error
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

type scratchTaskActionMsg struct {
	projectPath string
	selectPath  string
	status      string
	err         error
}

type agentTaskActionMsg struct {
	task        model.AgentTask
	projectPath string
	selectPath  string
	status      string
	err         error
}

type projectRemoveActionMsg struct {
	projectPath string
	status      string
	err         error
}

type todoWorktreeLaunchMsg struct {
	launchID       int64
	perfOpID       int64
	perfDuration   time.Duration
	projectPath    string
	todoID         int64
	todoText       string
	attachments    []model.TodoAttachment
	status         string
	prepProfile    string
	preparedPaths  []string
	provider       codexapp.Provider
	openModelFirst bool
	err            error
}

type worktreeActionMsg struct {
	projectPath            string
	removedProjectPath     string
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
	preview               service.CommitPreview
	projectPath           string
	intent                service.GitActionIntent
	message               string
	requestID             int
	refreshedProjectState bool
	err                   error
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

type settingsLCAgentVisionCheckMsg struct {
	result       codexapp.LCAgentVisionCheckResult
	err          error
	checkedMain  bool
	mainProvider string
	mainModel    string
}

type settingsLCAgentVisionCapabilitySavedMsg struct {
	settings config.EditableSettings
	path     string
	err      error
}

type codexLCAgentProviderSetupSavedMsg struct {
	projectPath string
	settings    config.EditableSettings
	path        string
	err         error
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

type editableSettingsAppliedMsg struct {
	scanAfter bool
}

type privacyModeSavedMsg struct {
	privacyMode bool
	path        string
	err         error
}

type ignoredProjectsMsg struct {
	items []model.IgnoredProject
	err   error
}

type ignoredProjectActionMsg struct {
	status string
	err    error
}

type pendingGitOperationKind string

const (
	pendingGitOperationUnknown       pendingGitOperationKind = ""
	pendingGitOperationCommit        pendingGitOperationKind = "commit"
	pendingGitOperationCommitPush    pendingGitOperationKind = "commit_push"
	pendingGitOperationPush          pendingGitOperationKind = "push"
	pendingGitOperationPull          pendingGitOperationKind = "pull"
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

type projectSortMode string
type projectVisibilityMode string
type projectArchiveMode string
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

	projectArchiveMain     projectArchiveMode = "main"
	projectArchiveActive   projectArchiveMode = projectArchiveMain
	projectArchiveCategory projectArchiveMode = "category"
	projectArchiveArchived projectArchiveMode = "archived"

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
	initialConfig := svc.Config()
	initialSettings := config.EditableSettingsFromAppConfig(initialConfig)
	settingsBaseline := cloneEditableSettings(initialSettings)
	homeDir, _ := os.UserHomeDir()

	m := Model{
		ctx:                           ctx,
		svc:                           svc,
		busCh:                         busCh,
		unsub:                         unsub,
		loading:                       true,
		status:                        initialProjectsStatus,
		commandInput:                  commandInput,
		codexInput:                    codexInput,
		codexPanelFocus:               embeddedCodexFocusMain,
		embeddedSidebarDiffs:          make(map[string]embeddedSidebarDiffState),
		embeddedSidebarDiffAutoAt:     make(map[string]time.Time),
		dismissedSuspendedTurns:       make(map[string]struct{}),
		codexDrafts:                   make(map[string]codexDraft),
		codexSuggestedDraftsApplied:   make(map[string]string),
		codexTranscriptRenderInFlight: make(map[codexTranscriptRenderKey]struct{}),
		codexClosedHandled:            make(map[string]struct{}),
		pendingGitOperations:          make(map[string]pendingGitOperation),
		pendingGitSummaries:           make(map[string]string),
		codexSnapshots:                make(map[string]codexapp.Snapshot),
		embeddedActivityInFlight:      make(map[string]bool),
		embeddedActivityQueued:        make(map[string]embeddedSessionActivityRecordRequest),
		embeddedActivityWatermark:     make(map[string]time.Time),
		codexTranscriptRev:            make(map[string]uint64),
		codexTranscriptFullHistory:    make(map[string]struct{}),
		codexArtifactLinkScans:        make(map[string]codexArtifactLinkScanState),
		codexToolAnswers:              make(map[string]codexToolAnswerState),
		aiLatencyInFlight:             make(map[int64]aiLatencyOp),
		detailViewport:                detailViewport,
		runtimeViewport:               runtimeViewport,
		codexViewport:                 codexViewport,
		uiDiagnostics:                 newUIStallDiagnostics(strings.TrimSpace(homeDir), os.Getpid()),
		focusedPane:                   focusProjects,
		assessmentFlashUntil:          make(map[string]time.Time),
		sortMode:                      sortByAttention,
		visibility:                    visibilityAIFolders,
		archiveMode:                   projectArchiveMain,
		settingsBaseline:              &settingsBaseline,
		settingsConfigPath:            strings.TrimSpace(initialConfig.ConfigPath),
		appDataDirPath:                strings.TrimSpace(initialConfig.DataDir),
		codexHomePath:                 strings.TrimSpace(initialConfig.CodexHome),
		excludeProjectPatterns:        append([]string(nil), initialSettings.ExcludeProjectPatterns...),
		privacyMode:                   initialSettings.PrivacyMode,
		privacyPatterns:               append([]string(nil), initialSettings.PrivacyPatterns...),
		codexManager:                  codexapp.NewManager(),
		runtimeManager:                projectrun.NewManager(),
		runtimeSnapshots:              make(map[string]projectrun.Snapshot),
		runtimeProcessSnapshots:       nil,
		runtimeProcessSelected:        make(map[string]string),
		processReports:                make(map[string]procinspect.ProjectReport),
		embeddedModelPrefs:            embeddedModelPreferencesFromSettings(initialSettings),
		recentCodexModels:             append([]string(nil), initialSettings.RecentCodexModels...),
		recentClaudeModels:            append([]string(nil), initialSettings.RecentClaudeModels...),
		recentOpenCodeModels:          append([]string(nil), initialSettings.RecentOpenCodeModels...),
		recentLCAgentModels:           append([]string(nil), initialSettings.RecentLCAgentModels...),
		hideReasoningSections:         initialSettings.HideReasoningSections,
		browserController:             browserctl.NewController(),
		managedBrowserStates:          make(map[string]browserctl.ManagedPlaywrightState),
		detailReloadInFlight:          make(map[string]bool),
		detailReloadQueued:            make(map[string]bool),
		detailReloadErrors:            make(map[string]string),
		summaryReloadInFlight:         make(map[string]bool),
		summaryReloadQueued:           make(map[string]bool),
		nowFn:                         time.Now,
		homeDirFn:                     os.UserHomeDir,
		homeDir:                       strings.TrimSpace(homeDir),
	}
	if issue := settingsLocalFileIssue(initialSettings); issue != nil {
		m.appendSettingsConfigIssue(issue)
		m.status = errorStatusWithHint(settingsConfigIssueStatus)
	}
	return m
}

func (m Model) currentTime() time.Time {
	if m.nowFn != nil {
		return m.nowFn()
	}
	return time.Now()
}

func (m Model) assessmentStallThreshold() time.Duration {
	settings := m.currentSettingsBaseline()
	return sessionclassify.EffectiveAssessmentStallThreshold(settings.ActiveThreshold, settings.StuckThreshold)
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

func (m *Model) markSelectionFlash(at time.Time) {
	if at.IsZero() {
		at = m.currentTime()
	}
	m.selectionFlashUntil = at.Add(projectListSelectionFlashDuration)
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

func (m *Model) clearExpiredPendingGitSummaryForPath(projectPath string) {
	projectPath = normalizeProjectPath(projectPath)
	if projectPath == "" || m.pendingGitSummaryExpireNext == nil {
		return
	}
	if !m.pendingGitSummaryExpireNext[projectPath] {
		return
	}
	delete(m.pendingGitSummaryExpireNext, projectPath)
	if len(m.pendingGitSummaryExpireNext) == 0 {
		m.pendingGitSummaryExpireNext = nil
	}
	m.clearPendingGitSummary(projectPath)
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
	case "Pulling...":
		return pendingGitOperation{Kind: pendingGitOperationPull, Summary: summary}
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
	case pendingGitOperationPull:
		return "pulling"
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

func (m Model) selectionFlashActive() bool {
	return m.selectionFlashUntil.After(m.currentTime())
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
	if !m.selectionFlashUntil.After(now) {
		m.selectionFlashUntil = time.Time{}
	}
	if !m.topStatusAttentionPulseUntil.After(now) {
		m.topStatusAttentionPulseStatus = ""
		m.topStatusAttentionPulseUntil = time.Time{}
	}
}

func (m Model) projectBrowserPulseActive(projectPath string) bool {
	if _, ok := m.projectPendingBrowserAttention(projectPath); !ok {
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
	if llmUsageTotalsIncreased(totals, m.lastUsageTotals) {
		m.usagePulseUntil = m.currentTime().Add(usagePulseDuration)
	}
	m.lastUsageTotals = totals
}

func llmUsageTotalsIncreased(current, previous model.LLMUsage) bool {
	return current.EstimatedCostUSD > previous.EstimatedCostUSD ||
		current.InputTokens > previous.InputTokens ||
		current.OutputTokens > previous.OutputTokens ||
		current.TotalTokens > previous.TotalTokens ||
		current.CachedInputTokens > previous.CachedInputTokens ||
		current.ReasoningTokens > previous.ReasoningTokens
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
		m.loadCPUSnapshotCmd(),
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
		if existing, ok := cloned[path]; !ok || runtimeSnapshotPreferred(snapshot, existing) {
			cloned[path] = snapshot
		}
	}
	return cloned
}

func cloneRuntimeProcessSnapshots(snapshots []projectrun.Snapshot) []projectrun.Snapshot {
	if len(snapshots) == 0 {
		return nil
	}
	out := make([]projectrun.Snapshot, 0, len(snapshots))
	for _, snapshot := range snapshots {
		path := normalizeProjectPath(snapshot.ProjectPath)
		if path == "" {
			continue
		}
		snapshot.ProjectPath = path
		out = append(out, snapshot)
	}
	return out
}

func runtimeSnapshotPreferred(candidate, existing projectrun.Snapshot) bool {
	if candidate.Running != existing.Running {
		return candidate.Running
	}
	if candidate.Default != existing.Default {
		return candidate.Default
	}
	if candidate.StartedAt.Equal(existing.StartedAt) {
		return strings.TrimSpace(candidate.ID) < strings.TrimSpace(existing.ID)
	}
	return candidate.StartedAt.After(existing.StartedAt)
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

func (m Model) currentSelectedProjectPath() string {
	if p, ok := m.selectedProject(); ok {
		return normalizeProjectPath(p.Path)
	}
	return ""
}

func (m Model) currentDetailTargetPath() string {
	if m.todoDialog != nil {
		if path := normalizeProjectPath(m.todoDialog.ProjectPath); path != "" {
			return path
		}
	}
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
	prevWant := m.bossMode || m.helpChatMode || m.codexVisible() || m.diffView != nil
	want := mm.bossMode || mm.helpChatMode || mm.codexVisible() || mm.diffView != nil
	mm.mouseEnabled = want
	mm.syncEmbeddedSessionIdleProtection()
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
	if _, ok := msg.(bossui.ExitMsg); ok {
		if m.helpChatMode {
			m.closeHelpChatMode("Help chat hidden")
			return m, nil
		}
		m.closeBossMode("Boss mode hidden")
		return m, nil
	}
	if msg, ok := msg.(bossui.ControlInvocationConfirmedMsg); ok {
		return m.executeBossControlInvocation(msg)
	}
	if msg, ok := msg.(bossui.GoalRunConfirmedMsg); ok {
		return m.executeBossGoalRun(msg)
	}
	if msg, ok := msg.(bossui.GoalRunResultMsg); ok {
		m = m.applyBossGoalRunResultToHost(msg)
	}
	if msg, ok := msg.(bossui.ControlInvocationResultMsg); ok {
		m = m.recordBossTrackedTodoFromControlResult(msg)
	}
	if msg, ok := msg.(bossHostNoticePersistedMsg); ok {
		if msg.err != nil {
			m.appendBackgroundErrorLogEntry("Boss chat notice save failed", msg.err, "")
		}
		return m, nil
	}
	if m.helpChatMode && bossui.IsMessage(msg) {
		return m.updateHelpChatModeMessage(msg)
	}
	if m.bossMode && bossui.IsMessage(msg) {
		return m.updateBossModeMessage(msg)
	}
	if !m.bossMode && !m.helpChatMode && m.bossModelActive && bossui.IsBackgroundMessage(msg) {
		return m.updateBossModeMessage(msg)
	}

	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
		m.width = msg.Width
		m.height = msg.Height
		m.ensureSelectionVisible()
		m.syncCommandInputWidth()
		m.syncTodoDialogSize()
		m.syncTodoEditorSize()
		m.syncCPURemediationEditorSize()
		m.syncDiffView(false)
		m.syncDetailViewport(false)
		m.syncCodexComposerSize()
		m.syncCodexViewport(false)
		m.syncRuntimeViewport(false)
		codexTranscriptCmd := m.requestVisibleCodexTranscriptRenderCmd()
		if m.bossMode {
			updated, bossCmd := m.updateBossModeWindowSize()
			return updated, batchCmds(codexTranscriptCmd, bossCmd)
		}
		if m.helpChatMode {
			updated, bossCmd := m.updateHelpChatModeWindowSize()
			return updated, batchCmds(codexTranscriptCmd, bossCmd)
		}
		return m, codexTranscriptCmd
	case cursor.BlinkMsg:
		if m.codexVisible() && m.codexInput.Focused() {
			var cmd tea.Cmd
			m.codexInput, cmd = m.codexInput.Update(msg)
			return m, cmd
		}
		return m, nil
	case tea.MouseMsg:
		if m.bossMode {
			msg.Y--
			return m.updateBossModeMessage(msg)
		}
		if m.helpChatMode {
			return m.updateHelpChatModeMouse(msg)
		}
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
			// Unhandled mouse event: finalize any pending drag (missed
			// release), clear selection, and only let scoped wheel presses
			// reach the transcript viewport.
			if m.codexSelection.dragging {
				m.finalizeCodexSelection()
			}
			if m.codexComposerSelection.dragging {
				m.finalizeCodexComposerSelection()
			}
			m.codexSelection = textSelection{}
			m.codexComposerSelection = textSelection{}
			if !codexMouseWheelPress(msg) {
				return m, nil
			}
			if codexHorizontalMouseWheel(msg) {
				return m, nil
			}
			var cmd tea.Cmd
			m.codexViewport, cmd = m.codexViewport.Update(msg)
			if msg.Button == tea.MouseButtonWheelUp {
				m.maybeLoadFullCodexHistoryAtViewportTop()
			}
			return m, cmd
		}
	case tea.KeyMsg:
		if m.bossMode {
			return m.updateBossModeKey(msg)
		}
		if m.helpChatMode {
			return m.updateHelpChatModeKey(msg)
		}
		if m.bossSetupPrompt != nil {
			return m.updateBossSetupPromptMode(msg)
		}
		if m.codexArtifactPicker != nil {
			return m.updateCodexArtifactPickerMode(msg)
		}
		if m.codexLCAgentProviderSetup != nil {
			return m.updateCodexLCAgentProviderSetupMode(msg)
		}
		if m.codexModelPickerVisible() {
			return m.updateCodexModelPickerMode(msg)
		}
		if m.codexPickerVisible {
			return m.updateCodexPickerMode(msg)
		}
		if m.localModelPickerVisible {
			return m.updateLocalBackendModelPickerMode(msg)
		}
		if m.settingsLCAgentModelPicker != nil {
			return m.updateSettingsLCAgentModelPickerMode(msg)
		}
		if m.settingsAIBackendPickerVisible {
			return m.updateSettingsAIBackendPickerMode(msg)
		}
		if m.settingsBossChatPickerVisible {
			return m.updateSettingsBossChatBackendPickerMode(msg)
		}
		if m.settingsLCAgentProviderVisible {
			return m.updateSettingsLCAgentProviderPickerMode(msg)
		}
		if m.settingsBrowserPickerVisible {
			return m.updateSettingsBrowserAutomationPickerMode(msg)
		}
		if m.settingsLCAgentSearchPickerVisible {
			return m.updateSettingsLCAgentWebSearchPickerMode(msg)
		}
		if m.settingsChoicePicker != nil {
			return m.updateSettingsChoicePickerMode(msg)
		}
		if m.ignoredPickerVisible {
			return m.updateIgnoredPickerMode(msg)
		}
		if m.errorLogVisible {
			return m.updateErrorLogMode(msg)
		}
		if m.cpuRemediationEditor != nil {
			return m.updateCPURemediationEditorMode(msg)
		}
		if m.cpuDialog != nil {
			return m.updateCPUDialogMode(msg)
		}
		if m.portsDialog != nil {
			return m.updatePortsDialogMode(msg)
		}
		if m.processDialog != nil {
			return m.updateProcessDialogMode(msg)
		}
		if m.skillsDialog != nil {
			return m.updateSkillsDialogMode(msg)
		}
		if m.suspendedTurnDialog != nil {
			return m.updateSuspendedTurnResumeDialogMode(msg)
		}
		if m.attentionDialog != nil {
			return m.updateAttentionDialogMode(msg)
		}
		if m.newProjectDialog != nil {
			return m.updateNewProjectMode(msg)
		}
		if m.newTaskDialog != nil {
			return m.updateNewTaskDialogMode(msg)
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
		if m.scratchTaskAction != nil {
			return m.updateScratchTaskActionConfirmMode(msg)
		}
		if m.agentTaskAction != nil {
			return m.updateAgentTaskActionConfirmMode(msg)
		}
		if m.projectRemoveConfirm != nil {
			return m.updateProjectRemoveConfirmMode(msg)
		}
		if m.externalStopConfirm != nil {
			return m.updateExternalProcessStopConfirmMode(msg)
		}
		if m.todoDeleteConfirm != nil {
			return m.updateTodoDeleteConfirmMode(msg)
		}
		if m.todoExistingWorktree != nil {
			return m.updateTodoExistingWorktreeMode(msg)
		}
		if m.todoPendingLaunchDialog != nil {
			return m.updateTodoPendingLaunchDialogMode(msg)
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
		if m.categoryDialog != nil {
			return m.updateCategoryDialogMode(msg)
		}
		if m.archiveDialog != nil {
			return m.updateArchiveDialogMode(msg)
		}
		if m.codexVisible() {
			return m.updateCodexMode(msg)
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
		if m.browserAttention != nil {
			return m.updateBrowserAttentionMode(msg)
		}
		if m.questionNotify != nil {
			return m.updateQuestionNotifyMode(msg)
		}
		return m.updateNormalMode(msg)
	case mergeConflictResolveTargetMsg:
		return m.applyMergeConflictResolveTargetMsg(msg)
	case projectsMsg:
		m.flushExpiredPendingGitSummaries()
		reloadCmd := m.finishProjectsReloadCmd()
		if msg.err != nil {
			m.loading = false
			m.err = msg.err
			m.status = projectLoadFailedStatus(len(m.projects) > 0)
			return m, reloadCmd
		}
		startupEmptyCache := m.status == initialProjectsStatus && len(msg.projects) == 0 && len(msg.archivedProjects) == 0
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
		m.allProjects = m.preserveRefreshingAssessmentDisplays(msg.projects)
		m.archivedProjects = m.preserveRefreshingAssessmentDisplays(msg.archivedProjects)
		m.projectCategories = append([]model.ProjectCategory(nil), msg.categories...)
		m.ensureSelectedCategoryTab()
		m.openAgentTasks = append([]model.AgentTask(nil), msg.openAgentTasks...)
		m.orphanedWorktreesByRoot = msg.orphanedWorktreesByRoot
		m.rebuildProjectList(selectedPath)
		if !startupEmptyCache && (strings.TrimSpace(m.status) == "" || m.status == initialProjectsStatus || len(m.projects) == 0) {
			m.status = loadedProjectsStatus(len(m.projects), m.sortMode, m.visibility, m.projectFilter)
		}
		if !m.suspendedTurnChecked {
			allProjects := append([]model.ProjectSummary(nil), msg.projects...)
			allProjects = append(allProjects, msg.archivedProjects...)
			m.openSuspendedTurnResumeDialog(buildSuspendedTurnResumeChoices(allProjects, suspendedTurnResumeChoiceLimit))
		}
		if len(m.projects) > 0 {
			m.syncDetailViewport(false)
			return m, batchCmds(reloadCmd, m.requestSelectedProjectDetailViewCmd(), m.requestProcessScanCmd(""))
		}
		m.detail = model.ProjectDetail{}
		m.syncDetailViewport(true)
		return m, reloadCmd
	case agentTaskEngineerReturnedMsg:
		if strings.TrimSpace(msg.projectPath) != "" && msg.snapshot.Started {
			m.storeCodexSnapshot(msg.projectPath, msg.snapshot)
		}
		if msg.err != nil {
			m.status = fmt.Sprintf("Agent task %s handoff failed: %v", msg.taskID, msg.err)
			notice := bossEngineerCompletionNotice(msg.label, msg.summary, msg.engineerName) + "\n\nI couldn't save that review handoff: " + msg.err.Error()
			var cmd tea.Cmd
			m, cmd = m.recordBossHostNotice(bossHostNotice{Content: notice, AnnounceInChat: true, Handoff: bossEngineerCompletionHandoff(msg.label, msg.engineerName)})
			return m, cmd
		}
		m.upsertOpenAgentTask(msg.task)
		label := strings.TrimSpace(msg.label)
		if label == "" {
			label = strings.TrimSpace(msg.taskID)
		}
		if label == "" {
			label = "agent task"
		}
		m.status = "Agent task " + label + " needs your call"
		var cmd tea.Cmd
		m, cmd = m.recordBossHostNotice(bossHostNotice{Content: msg.notice, AnnounceInChat: true, Handoff: msg.handoff})
		if m.bossMode {
			cmd = batchCmds(cmd, m.bossModel.RefreshCmd())
		}
		if m.helpChatMode {
			cmd = batchCmds(cmd, m.helpChatModel.RefreshCmd())
		}
		return m, cmd
	case bossEngineerReturnedMsg:
		if strings.TrimSpace(msg.projectPath) != "" && msg.snapshot.Started {
			m.storeCodexSnapshot(msg.projectPath, msg.snapshot)
		}
		var cmd tea.Cmd
		m, cmd = m.recordBossHostNotice(bossHostNotice{Content: msg.notice, AnnounceInChat: true, Handoff: msg.handoff})
		return m, cmd
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
	case newProjectPathSuggestionsMsg:
		if m.newProjectDialog == nil || msg.seq != m.newProjectDialog.PathSuggestionSeq {
			return m, nil
		}
		m.newProjectDialog.PathSuggestionItems = append([]newProjectPathSuggestion(nil), msg.result.Suggestions...)
		m.newProjectDialog.PathSuggestionHidden = msg.result.HiddenCount
		m.newProjectDialog.PathInput.SetSuggestions(newProjectPathSuggestionStrings(msg.result.Suggestions))
		m.newProjectDialog.PathSuggestionsPending = false
		return m, nil
	case setupSnapshotMsg:
		m.setupChecked = true
		m.setupLoading = false
		m.setupSnapshot = msg.snapshot
		if msg.openOnStartup && msg.snapshot.NeedsSetup() {
			return m, m.openQuickSetupSettingsMode(false)
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
		provider := explicitEmbeddedProvider(msg.provider)
		if provider != "" {
			m.rememberEmbeddedProvider(provider)
			m.setEmbeddedLaunchProviderOverride(msg.result.ProjectPath, provider)
		}
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
		if provider != "" {
			m.status += "; Enter opens " + provider.Label()
		}
		return m, m.requestProjectInvalidationCmd(invalidateProjectStructure(""))
	case newTaskResultMsg:
		return m.applyNewTaskResultMsg(msg)
	case cpuRemediationTaskCreatedMsg:
		return m.applyCPURemediationTaskCreatedMsg(msg)
	case detailMsg:
		reloadCmd := m.finishDetailReloadCmd(msg.path)
		m.rememberDetailReloadResult(msg.path, msg.err)
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
		m.clearExpiredPendingGitSummaryForPath(msg.path)
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
			m.upsertProjectSummary(m.preserveRefreshingAssessmentDisplay(msg.summary))
			m.syncWorktreeMergeConfirmFromProjects(msg.path)
		} else {
			m.removeProjectSummary(msg.path)
			m.syncWorktreeMergeConfirmFromProjects(msg.path)
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
			return m, nil
		}
		return m, m.requestProjectInvalidationCmd(msg.refresh)
	case embeddedSessionActivityRecordedMsg:
		followUp := m.finishEmbeddedSessionActivityRecordCmd(msg)
		if msg.err != nil {
			m.reportError("Embedded session activity update failed", msg.err, msg.projectPath)
			return m, followUp
		}
		return m, followUp
	case projectStatusRefreshedMsg:
		if worktreeMergeConfirmTracksPath(m.worktreeMergeConfirm, msg.projectPath) {
			if msg.err != nil {
				m.worktreeMergeConfirm.ErrorMessage = "Could not refresh live git status: " + msg.err.Error()
			}
			if worktreeMergeConfirmMarkRefreshDone(m.worktreeMergeConfirm, msg.projectPath) {
				m.worktreeMergeConfirm.Busy = false
				m.worktreeMergeConfirm.BusyMessage = ""
			}
			m.status = worktreeMergeConfirmStatus(m.worktreeMergeConfirm)
		}
		if msg.err != nil {
			m.reportError("Project status refresh failed", msg.err, msg.projectPath)
			return m, nil
		}
		return m, m.requestProjectInvalidationCmd(invalidateProjectData(msg.projectPath))
	case scanMsg:
		reloadCmd := m.finishScanCmd()
		m.loading = false
		if msg.err != nil {
			m.reportError("Scan failed", msg.err, "")
			return m, reloadCmd
		}
		m.startupScanCompleted = true
		m.status = scanCompleteStatus(msg.report)
		suspendedTurnsCmd := tea.Cmd(nil)
		if !m.suspendedTurnChecked {
			suspendedTurnsCmd = m.loadSuspendedTurnResumeChoicesCmd()
		}
		return m, batchCmds(reloadCmd, m.requestProjectInvalidationCmd(invalidateProjectStructure("")), suspendedTurnsCmd)
	case suspendedTurnResumeChoicesMsg:
		return m.applySuspendedTurnResumeChoicesMsg(msg)
	case commitPreviewMsg:
		if msg.requestID != 0 && msg.requestID != m.commitPreviewRequestID {
			return m, nil
		}
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
				if msg.refreshedProjectState {
					return m, m.requestProjectInvalidationCmd(invalidateProjectData(noChangesErr.ProjectPath))
				}
				return m, m.refreshProjectStatusCmd(noChangesErr.ProjectPath)
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
				return m, m.refreshProjectStatusCmd(submoduleErr.ProjectPath)
			}
			var submoduleResolvedErr service.SubmoduleResolvedNoParentChangesError
			if errors.As(msg.err, &submoduleResolvedErr) {
				dialog := gitStatusDialogFromSubmoduleResolved(submoduleResolvedErr)
				m.err = nil
				m.showHelp = false
				m.gitStatusDialog = &dialog
				m.gitStatusApplying = false
				m.commitPreview = nil
				m.commitTodoCompletions = nil
				m.commitPreviewMessageOverride = ""
				m.commitApplying = false
				m.status = gitStatusDialogReadyStatus(dialog)
				return m, m.refreshProjectStatusCmd(submoduleResolvedErr.ProjectPath)
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
		var backgroundError errorLogAppendResult
		if errText := strings.TrimSpace(msg.preview.CommitMessageError); errText != "" {
			previouslyLogged := m.commitPreview != nil &&
				m.commitPreview.StateHash == msg.preview.StateHash &&
				strings.TrimSpace(m.commitPreview.CommitMessageError) == errText
			if !previouslyLogged {
				backgroundError = m.appendBackgroundErrorLogEntry("Commit message fallback used", errors.New(errText), msg.projectPath)
			}
		}
		m.commitPreview = &msg.preview
		m.commitPreviewMessageOverride = msg.message
		m.commitApplying = false
		m.commitTodoCompletions = buildCommitTodoItems(msg.preview.SuggestedTodos)
		m.commitTodoSelected = 0
		if backgroundError.Escalated {
			m.status = errorStatusWithHint(backgroundError.Status)
		} else {
			m.status = commitPreviewReadyStatus(msg.preview)
		}
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
				m.rememberEmbeddedSidebarCleanDiff(noDiffErr.ProjectPath, noDiffErr.ProjectName, noDiffErr.Branch)
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
				return m, m.refreshProjectStatusCmd(noDiffErr.ProjectPath)
			}
			m.diffView = nil
			m.reportError("Diff preview failed", msg.err, "")
			return m, nil
		}
		m.err = nil
		m.rememberEmbeddedSidebarDiffPreview(msg.preview)
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
				m.rememberEmbeddedSidebarCleanDiff(noDiffErr.ProjectPath, noDiffErr.ProjectName, noDiffErr.Branch)
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
				return m, m.refreshProjectStatusCmd(noDiffErr.ProjectPath)
			}
			m.reportError("Diff staging failed", msg.err, "")
			return m, nil
		}
		m.err = nil
		m.rememberEmbeddedSidebarDiffPreview(msg.preview)
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
		if strings.TrimSpace(msg.selectPath) != "" {
			m.preferredSelectPath = strings.TrimSpace(msg.selectPath)
		}
		if msg.refresh.kind != projectInvalidationNone {
			return m, m.requestProjectInvalidationCmd(msg.refresh)
		}
		return m, m.requestProjectInvalidationCmd(invalidateProjectScan("", false))
	case browserOpenMsg:
		if msg.browserLeaseSnapshotSet {
			m.browserLeaseSnapshot = msg.browserLeaseSnapshot
		}
		if msg.managedBrowserStateSet {
			if msg.managedBrowserState.UpdatedAt.IsZero() {
				msg.managedBrowserState.UpdatedAt = m.currentTime()
			}
			m.rememberManagedBrowserState(msg.managedBrowserState)
		}
		if msg.err != nil {
			m.reportError("Open failed", msg.err, msg.projectPath)
			return m, nil
		}
		m.err = nil
		m.status = msg.status
		if m.bossMode && strings.TrimSpace(msg.status) != "" {
			var hostCmd tea.Cmd
			m, hostCmd = m.updateBossHostNotice("Browser handoff: " + strings.TrimSpace(msg.status))
			return m, hostCmd
		}
		return m, nil
	case managedBrowserStateMsg:
		if msg.err == nil {
			m.rememberManagedBrowserState(msg.state)
		} else {
			m.forgetManagedBrowserState(msg.sessionKey)
			if msg.retryAttemptsRemaining > 0 {
				return m, m.delayedReadManagedBrowserStateCmd(msg.sessionKey, msg.retryAttemptsRemaining-1)
			}
		}
		return m, nil
	case codexArtifactPreviewMsg:
		return m.applyCodexArtifactPreviewMsg(msg)
	case codexArtifactLinkScanMsg:
		return m.applyCodexArtifactLinkScanMsg(msg)
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
	case externalProcessStopMsg:
		if m.externalStopConfirm != nil && m.externalStopConfirm.PID == msg.pid {
			m.externalStopConfirm.Submitting = false
		}
		if msg.err != nil {
			m.reportError("External process stop failed", msg.err, msg.projectPath)
			return m, nil
		}
		m.externalStopConfirm = nil
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
		return m, batchCmds(m.requestProcessScanCmd(msg.projectPath), m.requestRuntimeSnapshotsRefreshCmd())
	case runtimeSnapshotsMsg:
		reloadCmd := m.finishRuntimeSnapshotsRefreshCmd()
		prevSnapshots := m.runtimeSnapshots
		m.runtimeProcessSnapshots = cloneRuntimeProcessSnapshots(msg.snapshots)
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
	case processScanMsg:
		return m, m.applyProcessScanMsg(msg)
	case embeddedSidebarDiffPreviewMsg:
		return m.applyEmbeddedSidebarDiffPreviewMsg(msg)
	case cpuSnapshotMsg:
		return m, m.applyCPUSnapshotMsg(msg)
	case skillsInventoryMsg:
		return m.applySkillsInventoryMsg(msg)
	case runCommandSavedMsg:
		if m.runCommandDialog != nil && m.runCommandDialog.ProjectPath == msg.projectPath {
			m.runCommandDialog.Submitting = false
		}
		if msg.err != nil {
			m.reportError("Run command save failed", msg.err, msg.projectPath)
			return m, nil
		}
		m.closeRunCommandDialog("")
		m.applyRunCommandSavedLocal(msg.projectPath, msg.command)
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
			if m.todoPendingSave != nil && filepath.Clean(strings.TrimSpace(m.todoPendingSave.ProjectPath)) == filepath.Clean(strings.TrimSpace(msg.projectPath)) {
				return m, m.reopenPendingTodoEditor()
			}
			if m.todoEditor != nil {
				m.todoEditor.Submitting = false
			}
			return m, nil
		}
		if m.todoDialog != nil && filepath.Clean(strings.TrimSpace(m.todoDialog.ProjectPath)) == filepath.Clean(strings.TrimSpace(msg.projectPath)) {
			m.todoDialog.Busy = false
		}
		if m.todoPendingSave != nil && filepath.Clean(strings.TrimSpace(m.todoPendingSave.ProjectPath)) == filepath.Clean(strings.TrimSpace(msg.projectPath)) {
			m.todoPendingSave = nil
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
	case scratchTaskActionMsg:
		if msg.err != nil {
			if m.scratchTaskAction != nil && filepath.Clean(strings.TrimSpace(m.scratchTaskAction.ProjectPath)) == filepath.Clean(strings.TrimSpace(msg.projectPath)) {
				m.scratchTaskAction.Submitting = false
			}
			m.reportError("Scratch task action failed", msg.err, msg.projectPath)
			return m, nil
		}
		m.scratchTaskAction = nil
		m.err = nil
		m.preferredSelectPath = strings.TrimSpace(msg.selectPath)
		if strings.TrimSpace(msg.status) != "" {
			m.status = msg.status
		}
		return m, m.requestProjectInvalidationCmd(invalidateProjectStructure(""))
	case agentTaskActionMsg:
		if msg.err != nil {
			if m.agentTaskAction != nil && filepath.Clean(strings.TrimSpace(m.agentTaskAction.ProjectPath)) == filepath.Clean(strings.TrimSpace(msg.projectPath)) {
				m.agentTaskAction.Submitting = false
			}
			m.reportError("Agent task action failed", msg.err, msg.projectPath)
			return m, nil
		}
		m.agentTaskAction = nil
		m.err = nil
		m.preferredSelectPath = strings.TrimSpace(msg.selectPath)
		if strings.TrimSpace(msg.task.ID) != "" {
			m.upsertOpenAgentTask(msg.task)
			if strings.TrimSpace(msg.selectPath) != "" {
				m.rebuildProjectList(msg.selectPath)
			}
		}
		if strings.TrimSpace(msg.status) != "" {
			m.status = msg.status
		}
		return m, m.requestProjectInvalidationCmd(invalidateProjectStructure(""))
	case projectRemoveActionMsg:
		if msg.err != nil {
			if m.projectRemoveConfirm != nil && filepath.Clean(strings.TrimSpace(m.projectRemoveConfirm.ProjectPath)) == filepath.Clean(strings.TrimSpace(msg.projectPath)) {
				m.projectRemoveConfirm.Submitting = false
			}
			m.reportError("Remove action failed", msg.err, msg.projectPath)
			return m, nil
		}
		m.projectRemoveConfirm = nil
		m.err = nil
		if strings.TrimSpace(msg.status) != "" {
			m.status = msg.status
		}
		return m, m.requestProjectInvalidationCmd(invalidateProjectStructure(""))
	case todoWorktreeLaunchMsg:
		m.completeAILatencyOp(msg.perfOpID, msg.perfDuration, msg.err, msg.status)
		pendingCanceled := false
		pendingRootPath := ""
		selectedWasPending := false
		selectAfterPending := m.currentSelectedProjectPath()
		if msg.launchID != 0 {
			pending := m.todoPendingLaunch
			if pending == nil || pending.ID != msg.launchID {
				return m, nil
			}
			pendingRootPath = pending.ProjectPath
			_, selectedWasPending = m.todoPendingLaunchForProjectPath(m.currentSelectedProjectPath())
			if selectedWasPending {
				selectAfterPending = pendingRootPath
			}
			pendingCanceled = pending.Canceled
			m.todoPendingLaunch = nil
		}
		if m.todoPendingLaunchDialog != nil && (msg.launchID == 0 || m.todoPendingLaunchDialog.LaunchID == msg.launchID) {
			m.todoPendingLaunchDialog = nil
		}
		if m.todoCopyDialog != nil && (msg.launchID == 0 || m.todoCopyDialog.LaunchID == 0 || m.todoCopyDialog.LaunchID == msg.launchID) {
			m.todoCopyDialog.Submitting = false
			m.todoCopyDialog.LaunchID = 0
		}
		if pendingCanceled {
			m.err = nil
			m.clearTodoLaunchDraft(msg.projectPath)
			if pendingRootPath != "" {
				m.rebuildProjectList(selectAfterPending)
			}
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
			m.clearTodoLaunchDraft(msg.projectPath)
			if pendingRootPath != "" {
				m.rebuildProjectList(selectAfterPending)
			}
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
		codexAttachments := codexAttachmentsFromTodo(msg.attachments)
		if msg.openModelFirst {
			m.restoreCodexDraft(msg.projectPath, codexDraftFromTodo(msg.todoText, msg.attachments))
		} else {
			m.clearCodexDraft(msg.projectPath)
		}
		m.storeTodoLaunchDraft(todoLaunchDraftState{
			projectPath:    msg.projectPath,
			todoID:         msg.todoID,
			provider:       provider,
			openModelFirst: msg.openModelFirst,
			autoSubmit:     !msg.openModelFirst,
			attachments:    codexAttachments,
		})
		req := codexapp.LaunchRequest{
			Provider:                   provider,
			ProjectPath:                strings.TrimSpace(msg.projectPath),
			ForceNew:                   true,
			Preset:                     m.currentCodexLaunchPreset(),
			PlaywrightPolicy:           m.currentPlaywrightPolicy(),
			AppDataDir:                 m.appDataDir(),
			CodexHome:                  m.codexHome(),
			LCAgentPath:                m.lcagentPath(),
			LCAgentEnvFile:             m.lcagentEnvFile(),
			LCAgentOpenAIAPIKey:        m.openAIAPIKey(),
			LCAgentOpenRouterAPIKey:    m.openRouterAPIKey(),
			LCAgentDeepSeekAPIKey:      m.deepSeekAPIKey(),
			LCAgentMoonshotAPIKey:      m.moonshotAPIKey(),
			LCAgentXiaomiAPIKey:        m.xiaomiAPIKey(),
			LCAgentXiaomiBaseURL:       m.xiaomiBaseURL(),
			LCAgentOllamaAPIKey:        m.ollamaAPIKey(),
			LCAgentOllamaBaseURL:       m.ollamaBaseURL(),
			LCAgentOllamaModel:         m.ollamaModel(),
			LCAgentProviderAccessCheck: true,
			LCAgentRoutePreset:         m.lcagentRoutePreset(),
			LCAgentProvider:            m.lcagentProvider(),
			LCAgentAuto:                m.lcagentAuto(),
			LCAgentAdminWrite:          m.lcagentAdminWrite(),
			LCAgentToolProfile:         m.lcagentToolProfile(),
			LCAgentContextProfile:      m.lcagentContextProfile(),
			LCAgentRequestTimeout:      m.lcagentRequestTimeout(),
			LCAgentUtilityProvider:     m.lcagentUtilityProvider(),
			LCAgentUtilityModel:        m.lcagentUtilityModel(),
		}
		if !msg.openModelFirst {
			if len(codexAttachments) > 0 {
				req.InitialInput = codexapp.Submission{
					Text:        msg.todoText,
					Attachments: codexAttachments,
				}
			} else {
				req.Prompt = msg.todoText
			}
		}
		if err := req.Validate(); err != nil {
			m.clearTodoLaunchDraft(msg.projectPath)
			m.reportError("Embedded session open failed", err, msg.projectPath)
			return m, nil
		}
		m.ensureCodexRuntime()
		m.rememberEmbeddedProvider(provider)
		m.beginNewCodexPendingOpenWithVisibilityAndReveal(req.ProjectPath, provider, msg.openModelFirst, msg.openModelFirst)
		m.status = todoWorktreeSessionStartStatus(provider, msg.openModelFirst, len(msg.preparedPaths))
		m.preferredSelectPath = msg.projectPath
		if pendingRootPath != "" {
			m.rebuildProjectList(selectAfterPending)
		}
		return m, batchCmds(
			m.requestProjectInvalidationCmd(invalidateProjectStructure(msg.projectPath)),
			m.openCodexSessionCmdWithVisibility(req, msg.openModelFirst),
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
			refreshCmd := tea.Cmd(nil)
			if shouldRefreshWorktreeMergeFamilyAfterError(msg.err) {
				refreshCmd = m.refreshProjectStatusPathsCmd(msg.projectPath, msg.selectPath)
			}
			var submodulePublishErr service.SubmodulePublishBlockedError
			if errors.As(msg.err, &submodulePublishErr) {
				m.appendErrorLogEntry("Submodule publish blocked", msg.err, msg.projectPath)
				m.showSubmodulePublishBlockedMergeDialog(msg, submodulePublishErr)
				return m, refreshCmd
			}
			m.reportError("Worktree action failed", msg.err, msg.projectPath)
			if m.worktreeMergeConfirm != nil && filepath.Clean(strings.TrimSpace(m.worktreeMergeConfirm.ProjectPath)) == filepath.Clean(strings.TrimSpace(msg.projectPath)) {
				m.worktreeMergeConfirm.Busy = false
				m.worktreeMergeConfirm.BusyMessage = ""
				m.worktreeMergeConfirm.ErrorMessage = msg.err.Error()
				m.worktreePostMerge = nil
				m.worktreeRemoveConfirm = nil
				return m, refreshCmd
			}
			if m.worktreePostMerge != nil && filepath.Clean(strings.TrimSpace(m.worktreePostMerge.ProjectPath)) == filepath.Clean(strings.TrimSpace(msg.projectPath)) {
				m.worktreePostMerge.Busy = false
				m.worktreePostMerge.BusyTitle = ""
				m.worktreePostMerge.BusyMessage = ""
				m.worktreePostMerge.ErrorMessage = msg.err.Error()
				return m, refreshCmd
			}
			m.worktreeMergeConfirm = nil
			m.worktreePostMerge = nil
			m.worktreeRemoveConfirm = nil
			return m, refreshCmd
		}
		m.worktreeMergeConfirm = nil
		m.worktreePostMerge = nil
		m.worktreeRemoveConfirm = nil
		m.err = nil
		if strings.TrimSpace(msg.removedProjectPath) != "" {
			selectPath := strings.TrimSpace(msg.selectPath)
			if currentPath := m.currentSelectedProjectPath(); currentPath != "" && currentPath != normalizeProjectPath(msg.removedProjectPath) {
				selectPath = ""
			}
			m.applyRemovedProjectLocally(msg.removedProjectPath, selectPath)
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
		previousSettings := m.currentSettingsBaseline()
		scopeSettingsChanged := projectScopeSettingsChanged(previousSettings, msg.settings)
		reloadLCAgentProject, shouldReloadLCAgent := m.shouldReloadEmbeddedLCAgentAfterSettingsSave(previousSettings, msg.settings)
		settingsEmbeddedProject := m.settingsEmbeddedProject
		settingsEmbeddedProvider := m.settingsEmbeddedProvider
		lcagentSettingsChanged := strings.TrimSpace(settingsEmbeddedProject) != "" &&
			settingsEmbeddedProvider.Normalized() == codexapp.ProviderLCAgent &&
			lcagentLaunchSettingsChanged(previousSettings, msg.settings)
		m.settingsEmbeddedProject = ""
		m.settingsEmbeddedProvider = ""
		selectedPath := ""
		if p, ok := m.selectedProject(); ok {
			selectedPath = p.Path
		}
		saved := cloneEditableSettings(msg.settings)
		m.settingsBaseline = &saved
		m.settingsConfigPath = strings.TrimSpace(msg.path)
		m.excludeProjectPatterns = append([]string(nil), msg.settings.ExcludeProjectPatterns...)
		m.privacyPatterns = append([]string(nil), msg.settings.PrivacyPatterns...)
		m.embeddedModelPrefs = embeddedModelPreferencesFromSettings(msg.settings)
		m.hideReasoningSections = msg.settings.HideReasoningSections
		m.settingsMode = false
		m.status = fmt.Sprintf("Settings saved to %s. Filters, API keys, local endpoint/model overrides, Codex launch mode, and browser automation policy are applying in the background now; the running scheduler keeps its current timing until the next launch of %s.", msg.path, brand.CLIName)
		if scopeSettingsChanged {
			m.status = fmt.Sprintf("Settings saved to %s. Project scope changed; rescanning projects in the background now.", msg.path)
		}
		if shouldReloadLCAgent {
			m.beginCodexPendingOpen(reloadLCAgentProject, codexapp.ProviderLCAgent)
			m.status = fmt.Sprintf("Settings saved to %s. Restarting LCAgent so the next run uses the new configuration.", msg.path)
		} else if lcagentSettingsChanged {
			m.status = fmt.Sprintf("Settings saved to %s. New LCAgent sessions will use the saved configuration.", msg.path)
		}
		if issue := settingsLocalFileIssue(msg.settings); issue != nil {
			m.appendSettingsConfigIssue(issue)
			m.status += " " + errorStatusWithHint(settingsConfigIssueStatus) + "."
		}
		m.rebuildProjectList(selectedPath)
		cmds := []tea.Cmd{m.applyEditableSettingsCmdWithScan(msg.settings, scopeSettingsChanged), m.refreshSetupSnapshotCmd(false)}
		if shouldReloadLCAgent {
			cmds = append(cmds, m.reloadEmbeddedLCAgentAfterSettingsCmd(reloadLCAgentProject, msg.settings))
		}
		if len(m.projects) > 0 {
			m.syncDetailViewport(false)
			cmds = append(cmds, m.requestSelectedProjectDetailViewCmd())
			return m, tea.Batch(cmds...)
		}
		m.detail = model.ProjectDetail{}
		m.syncDetailViewport(true)
		return m, tea.Batch(cmds...)
	case settingsLCAgentModelListMsg:
		return m.applySettingsLCAgentModelListMsg(msg)
	case settingsLCAgentVisionCheckMsg:
		return m.applySettingsLCAgentVisionCheckMsg(msg)
	case settingsLCAgentVisionCapabilitySavedMsg:
		if msg.err != nil {
			m.reportError("LCAgent vision capability save failed", msg.err, "")
			m.status = "LCAgent vision capability check succeeded, but saving the capability cache failed: " + truncateText(msg.err.Error(), 160)
			return m, nil
		}
		saved := cloneEditableSettings(msg.settings)
		m.settingsBaseline = &saved
		m.settingsConfigPath = strings.TrimSpace(msg.path)
		return m, nil
	case codexLCAgentProviderSetupSavedMsg:
		return m.applyCodexLCAgentProviderSetupSavedMsg(msg)
	case setupSavedMsg:
		m.setupSaving = false
		m.err = nil
		if msg.err != nil {
			m.reportError("AI setup save failed", msg.err, "")
			return m, nil
		}
		saved := cloneEditableSettings(msg.settings)
		m.settingsBaseline = &saved
		m.settingsConfigPath = strings.TrimSpace(msg.path)
		m.embeddedModelPrefs = embeddedModelPreferencesFromSettings(msg.settings)
		m.setupSnapshot.Selected = msg.settings.AIBackend
		m.setupMode = false
		m.status = fmt.Sprintf("AI setup saved to %s. %s is now selected.", msg.path, msg.settings.AIBackend.Label())
		return m, tea.Batch(m.applyEditableSettingsCmd(msg.settings), m.refreshSetupSnapshotCmd(false))
	case editableSettingsAppliedMsg:
		if msg.scanAfter {
			return m, m.requestScanCmd(false)
		}
		return m, nil
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
		m.settingsConfigPath = strings.TrimSpace(msg.path)
		m.embeddedModelPrefs = embeddedModelPreferencesFromSettings(msg.settings)
		return m, nil
	case privacyModeSavedMsg:
		if msg.err != nil {
			m.reportError("Privacy mode updated for this run; config save failed", msg.err, "")
			return m, nil
		}
		m.err = nil
		m.settingsConfigPath = strings.TrimSpace(msg.path)
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
		m.ignoredPickerItems = append([]model.IgnoredProject(nil), msg.items...)
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
		return m.applyCodexSessionOpenedMsg(msg)
	case codexActionMsg:
		return m.applyCodexActionMsg(msg)
	case codexModelListMsg:
		return m.applyCodexModelListMsg(msg)
	case codexResumeChoicesMsg:
		return m.applyCodexResumeChoices(msg)
	case busMsg:
		cmds := []tea.Cmd{m.waitBusCmd()}
		if m.bossMode {
			m.bossModel = m.bossModel.WithChatOnly(false).WithViewContext(m.bossViewContext())
			cmds = append(cmds, m.bossModel.RefreshCmd())
		}
		if m.helpChatMode {
			m.helpChatModel = m.helpChatModel.WithViewContext(m.bossViewContext())
			cmds = append(cmds, m.helpChatModel.RefreshCmd())
		}
		switch msg.Type {
		case events.ClassificationUpdated:
			if msg.Payload["status"] == "completed" {
				m.markAssessmentFlash(msg.ProjectPath, msg.At)
			} else if msg.Payload["status"] == "failed" {
				m.appendBackgroundErrorLogEntry(classificationUpdateStatus(msg.Payload), classificationUpdateError(msg.Payload), msg.ProjectPath)
			}
			cmds = append(cmds, m.requestProjectInvalidationCmd(invalidateProjectData(msg.ProjectPath)))
			return m, batchCmds(cmds...)
		case events.ProjectChanged, events.ActionApplied:
			if msg.Payload["action"] == "todo_worktree_suggestion_failed" {
				m.appendBackgroundErrorLogEntry("TODO worktree suggestion failed", todoSuggestionEventError(msg.Payload), msg.ProjectPath)
			}
			if msg.Type == events.ActionApplied && actionChangesProjectStructure(msg.Payload["action"]) {
				cmds = append(cmds, m.requestProjectInvalidationCmd(invalidateProjectStructure(structureActionDetailPath(events.Event(msg), m.currentSelectedProjectPath()))))
			} else if strings.TrimSpace(msg.ProjectPath) != "" {
				cmds = append(cmds, m.requestProjectInvalidationCmd(invalidateProjectData(msg.ProjectPath)))
			} else {
				cmds = append(cmds, m.requestProjectInvalidationCmd(invalidateProjectStructure("")))
			}
			return m, batchCmds(cmds...)
		case events.ProjectMoved, events.ScanCompleted, events.EventsDropped:
			cmds = append(cmds, m.requestProjectInvalidationCmd(invalidateProjectStructure(m.currentSelectedProjectPath())))
			return m, batchCmds(cmds...)
		}
		cmds = append(cmds, m.requestProjectInvalidationCmd(invalidateProjectStructure("")))
		return m, batchCmds(cmds...)
	case spinnerTickMsg:
		m.recordUIStallFromSpinnerTick(m.currentTime())
		m.spinnerFrame = (m.spinnerFrame + 1) % spinnerAnimationFrameWrap
		m.marqueeOffset++
		m.refreshUsagePulse()
		m.pruneTransientHighlights(m.currentTime())
		refreshCmd := tea.Cmd(nil)
		if m.spinnerFrame%runtimeSnapshotRefreshEveryTicks == 0 {
			refreshCmd = m.requestRuntimeSnapshotsRefreshCmd()
		}
		processScanCmd := tea.Cmd(nil)
		if m.spinnerFrame%processScanRefreshEveryTicks == 0 {
			processScanCmd = m.requestProcessScanCmd("")
		}
		cpuSnapshotCmd := tea.Cmd(nil)
		if m.spinnerFrame%cpuSnapshotRefreshEveryTicks == 0 {
			cpuSnapshotCmd = m.requestCPUSnapshotRefreshCmd()
		}
		sidebarDiffCmd := m.requestVisibleBusyEmbeddedSidebarDiffRefreshCmd()
		return m, batchCmds(spinnerTickCmd(), refreshCmd, processScanCmd, cpuSnapshotCmd, sidebarDiffCmd)
	case codexUpdateMsg:
		return m.applyCodexUpdateMsg(msg)
	case codexUpdateAckMsg:
		return m.applyCodexUpdateAckMsg(msg)
	case codexDeferredSnapshotMsg:
		return m.applyCodexDeferredSnapshotMsg(msg)
	case codexTranscriptRenderedMsg:
		return m.applyCodexTranscriptRenderedMsg(msg)
	}

	return m, nil
}
