package tui

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"time"

	"lcroom/internal/aibackend"
	"lcroom/internal/brand"
	"lcroom/internal/codexapp"
	"lcroom/internal/codexcli"
	"lcroom/internal/commands"
	"lcroom/internal/config"
	"lcroom/internal/events"
	"lcroom/internal/model"
	"lcroom/internal/projectrun"
	"lcroom/internal/service"

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

	width     int
	height    int
	nowFn     func() time.Time
	homeDirFn func() (string, error)

	noteDialog        *noteDialogState
	noteCopyDialog    *noteCopyDialogState
	noteClearConfirm  *noteClearConfirmState
	todoDialog        *todoDialogState
	todoEditor        *todoEditorState
	todoDeleteConfirm *todoDeleteConfirmState
	todoLaunchDraft   *todoLaunchDraftState

	commandMode                  bool
	commandInput                 textinput.Model
	commandSelected              int
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
	setupMode                    bool
	setupChecked                 bool
	setupLoading                 bool
	setupSelected                int
	setupSnapshot                aibackend.Snapshot
	settingsMode                 bool
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

	codexManager         *codexapp.Manager
	runtimeManager       *projectrun.Manager
	runtimeSnapshots     map[string]projectrun.Snapshot
	codexSnapshots       map[string]codexapp.Snapshot
	codexTranscriptRev   map[string]uint64
	codexVisibleProject  string
	codexHiddenProject   string
	codexPendingOpen     *codexPendingOpenState
	codexInput           textarea.Model
	codexDrafts          map[string]codexDraft
	codexPasteTokenSeq   int
	codexClosedHandled   map[string]struct{}
	codexPickerVisible   bool
	codexPickerSelected  int
	codexPickerChoices   []codexSessionChoice
	codexPickerLoading   bool
	codexPickerKind      codexPickerKind
	codexPickerTitle     string
	codexPickerHint      string
	codexPickerEmpty     string
	codexPickerProject   string
	codexPickerProvider  codexapp.Provider
	codexModelPicker     *codexModelPickerState
	embeddedModelPrefs   map[codexapp.Provider]embeddedModelPreference
	recentCodexModels    []string
	recentOpenCodeModels []string
	codexDenseExpanded   bool
	codexSlashSelected   int
	codexToolAnswers     map[string]codexToolAnswerState
	codexViewport        viewport.Model
	codexTranscriptCache codexTranscriptRenderCache
	codexViewportContent codexViewportContentState

	spinnerFrame int
	showSessions bool
	showEvents   bool
	showHelp     bool

	newProjectRecentParents []string
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
	detail model.ProjectDetail
	err    error
}

type scanMsg struct {
	report service.ScanReport
	err    error
}

type actionMsg struct {
	status string
	err    error
}

type browserOpenMsg struct {
	status string
	err    error
}

type runtimeActionMsg struct {
	projectPath string
	status      string
	err         error
}

type runCommandSavedMsg struct {
	projectPath string
	command     string
	startAfter  bool
	err         error
}

type todoActionMsg struct {
	projectPath string
	status      string
	err         error
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

type ignoredProjectsMsg struct {
	items []model.IgnoredProjectName
	err   error
}

type ignoredProjectActionMsg struct {
	status string
	err    error
}

type codexSessionOpenedMsg struct {
	projectPath string
	status      string
	err         error
}

type codexPendingOpenState struct {
	projectPath string
	provider    codexapp.Provider
}

type busMsg events.Event

type spinnerTickMsg struct{}
type codexUpdateMsg struct {
	projectPath string
}
type codexActionMsg struct {
	projectPath  string
	status       string
	closed       bool
	restoreDraft codexDraft
	provider     codexapp.Provider
	model        string
	reasoning    string
	err          error
}
type codexModelListMsg struct {
	projectPath string
	models      []codexapp.ModelOption
	err         error
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

	return Model{
		ctx:                    ctx,
		svc:                    svc,
		busCh:                  busCh,
		unsub:                  unsub,
		status:                 initialProjectsStatus,
		commandInput:           commandInput,
		codexInput:             codexInput,
		codexDrafts:            make(map[string]codexDraft),
		codexClosedHandled:     make(map[string]struct{}),
		codexSnapshots:         make(map[string]codexapp.Snapshot),
		codexTranscriptRev:     make(map[string]uint64),
		codexToolAnswers:       make(map[string]codexToolAnswerState),
		detailViewport:         detailViewport,
		runtimeViewport:        runtimeViewport,
		codexViewport:          codexViewport,
		focusedPane:            focusProjects,
		assessmentFlashUntil:   make(map[string]time.Time),
		sortMode:               sortByAttention,
		visibility:             visibilityAIFolders,
		excludeProjectPatterns: currentExcludeProjectPatterns(svc),
		privacyMode:            false,
		privacyPatterns:        currentPrivacyPatterns(svc),
		codexManager:           codexapp.NewManager(),
		runtimeManager:         projectrun.NewManager(),
		embeddedModelPrefs:     embeddedModelPreferencesFromSettings(initialSettings),
		recentCodexModels:      append([]string(nil), initialSettings.RecentCodexModels...),
		recentOpenCodeModels:   append([]string(nil), initialSettings.RecentOpenCodeModels...),
		nowFn:                  time.Now,
		homeDirFn:              os.UserHomeDir,
	}
}

func (m Model) currentTime() time.Time {
	if m.nowFn != nil {
		return m.nowFn()
	}
	return time.Now()
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
	session, ok := m.codexSession(projectPath)
	if !ok {
		return nil, "", false
	}
	snapshot := session.Snapshot()
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
	cmds := []tea.Cmd{
		m.loadProjectsCmd(),
		m.loadRecentProjectParentsCmd(),
		m.refreshSetupSnapshotCmd(true),
		m.waitBusCmd(),
		m.waitCodexCmd(),
		spinnerTickCmd(),
	}
	return tea.Batch(cmds...)
}

func (m Model) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
		m.width = msg.Width
		m.height = msg.Height
		m.ensureSelectionVisible()
		m.syncCommandInputWidth()
		m.syncNoteDialogSize()
		m.syncTodoDialogSize()
		m.syncTodoEditorSize()
		m.syncDiffView(false)
		m.syncDetailViewport(false)
		m.syncCodexComposerSize()
		m.syncCodexViewport(false)
		m.syncRuntimeViewport(false)
		return m, nil
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
		if m.codexVisible() {
			return m.updateCodexMode(msg)
		}
		if m.newProjectDialog != nil {
			return m.updateNewProjectMode(msg)
		}
		if m.runCommandDialog != nil {
			return m.updateRunCommandDialogMode(msg)
		}
		if m.todoDeleteConfirm != nil {
			return m.updateTodoDeleteConfirmMode(msg)
		}
		if m.todoEditor != nil {
			return m.updateTodoEditorMode(msg)
		}
		if m.todoDialog != nil {
			return m.updateTodoDialogMode(msg)
		}
		if m.noteClearConfirm != nil {
			return m.updateNoteClearConfirmMode(msg)
		}
		if m.noteCopyDialog != nil {
			return m.updateNoteCopyDialogMode(msg)
		}
		if m.noteDialog != nil {
			return m.updateNoteDialogMode(msg)
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
		return m.updateNormalMode(msg)
	case projectsMsg:
		m.loading = false
		if msg.err != nil {
			m.err = msg.err
			m.status = projectLoadFailedStatus(len(m.projects) > 0)
			return m, nil
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
		if strings.TrimSpace(m.status) == "" || m.status == initialProjectsStatus || len(m.projects) == 0 {
			m.status = loadedProjectsStatus(len(m.projects), m.sortMode, m.visibility, m.projectFilter)
		}
		if len(m.projects) > 0 {
			m.syncDetailViewport(false)
			return m, m.loadDetailCmd(m.projects[m.selected].Path)
		}
		m.detail = model.ProjectDetail{}
		m.syncDetailViewport(true)
		return m, nil
	case recentProjectParentsMsg:
		if msg.err == nil {
			m.newProjectRecentParents = append([]string(nil), msg.paths...)
		}
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
			m.err = nil
			m.status = msg.err.Error()
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
		return m, m.loadProjectsCmd()
	case detailMsg:
		m.err = msg.err
		if msg.err == nil {
			m.detail = msg.detail
			m.syncTodoDialogSelection()
			m.syncDetailViewport(false)
		}
		return m, nil
	case scanMsg:
		m.loading = false
		if msg.err != nil {
			m.err = msg.err
			m.status = "Scan failed"
			return m, nil
		}
		m.status = scanCompleteStatus(msg.report)
		return m, tea.Batch(m.loadProjectsCmd())
	case commitPreviewMsg:
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
				m.commitPreviewMessageOverride = ""
				m.commitApplying = false
				m.status = gitStatusDialogReadyStatus(dialog)
				return m, nil
			}
			m.err = msg.err
			m.status = "Commit preview failed"
			m.gitStatusDialog = nil
			m.gitStatusApplying = false
			m.commitPreview = nil
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
		m.status = commitPreviewReadyStatus(msg.preview.CanPush)
		return m, nil
	case diffPreviewMsg:
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
			m.err = msg.err
			m.diffView = nil
			m.status = "Diff preview failed"
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
			m.err = msg.err
			m.status = "Diff staging failed"
			return m, nil
		}
		m.err = nil
		m.diffView.preview = &msg.preview
		m.diffView.selected = diffPreviewSelectionIndex(msg.preview.Files, msg.path, msg.originalPath, m.diffView.selected)
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
		m.commitPreview = nil
		m.commitPreviewMessageOverride = ""
		m.diffView = nil
		if msg.err != nil {
			m.err = msg.err
			m.status = "Action failed"
			return m, nil
		}
		m.status = msg.status
		return m, tea.Batch(m.scanCmd(false), m.loadProjectsCmd())
	case browserOpenMsg:
		if msg.err != nil {
			m.err = msg.err
			m.status = "Open failed"
			return m, nil
		}
		m.err = nil
		m.status = msg.status
		return m, nil
	case runtimeActionMsg:
		if msg.err != nil {
			m.err = nil
			m.status = msg.err.Error()
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
		return m, nil
	case runCommandSavedMsg:
		if m.runCommandDialog != nil && m.runCommandDialog.ProjectPath == msg.projectPath {
			m.runCommandDialog.Submitting = false
		}
		if msg.err != nil {
			m.err = nil
			m.status = msg.err.Error()
			return m, nil
		}
		m.closeRunCommandDialog("")
		cmds := []tea.Cmd{m.loadProjectsCmd()}
		if p, ok := m.selectedProject(); ok && p.Path == msg.projectPath {
			cmds = append(cmds, m.loadDetailCmd(msg.projectPath))
		}
		if strings.TrimSpace(msg.command) != "" && msg.startAfter {
			cmds = append(cmds, m.startProjectRuntimeCmd(msg.projectPath, msg.command))
		} else {
			m.status = "Saved run command"
		}
		return m, tea.Batch(cmds...)
	case todoActionMsg:
		if msg.err != nil {
			m.err = nil
			m.status = msg.err.Error()
			if m.todoEditor != nil {
				m.todoEditor.Submitting = false
			}
			return m, nil
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
		return m, nil
	case settingsSavedMsg:
		m.err = nil
		if msg.err != nil {
			m.status = msg.err.Error()
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
		m.settingsMode = false
		m.status = fmt.Sprintf("Settings saved to %s. Filters, API key, and Codex launch mode apply now; the running scheduler keeps its current timing until the next launch of %s.", msg.path, brand.CLIName)
		m.rebuildProjectList(selectedPath)
		cmds := []tea.Cmd{m.refreshSetupSnapshotCmd(false)}
		if len(m.projects) > 0 {
			m.syncDetailViewport(false)
			cmds = append(cmds, m.loadDetailCmd(m.projects[m.selected].Path))
			return m, tea.Batch(cmds...)
		}
		m.detail = model.ProjectDetail{}
		m.syncDetailViewport(true)
		return m, tea.Batch(cmds...)
	case setupSavedMsg:
		m.err = nil
		if msg.err != nil {
			m.status = msg.err.Error()
			return m, nil
		}
		if m.svc != nil {
			m.svc.ApplyEditableSettings(msg.settings)
		}
		saved := cloneEditableSettings(msg.settings)
		m.settingsBaseline = &saved
		m.embeddedModelPrefs = embeddedModelPreferencesFromSettings(msg.settings)
		m.setupMode = false
		m.status = fmt.Sprintf("AI setup saved to %s. %s is now selected.", msg.path, msg.settings.AIBackend.Label())
		return m, m.refreshSetupSnapshotCmd(false)
	case embeddedModelPreferencesSavedMsg:
		if msg.err != nil {
			m.err = msg.err
			m.status = fmt.Sprintf("Embedded model updated for this run, but saving %s failed: %v", msg.path, msg.err)
			return m, nil
		}
		m.err = nil
		if m.svc != nil {
			m.svc.ApplyEditableSettings(msg.settings)
		}
		saved := cloneEditableSettings(msg.settings)
		m.settingsBaseline = &saved
		m.embeddedModelPrefs = embeddedModelPreferencesFromSettings(msg.settings)
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
			m.status = msg.err.Error()
			return m, nil
		}
		m.status = msg.status
		cmds := []tea.Cmd{m.loadProjectsCmd()}
		if m.ignoredPickerVisible {
			cmds = append(cmds, m.loadIgnoredProjectsCmd())
		}
		return m, tea.Batch(cmds...)
	case codexSessionOpenedMsg:
		m.err = nil
		if msg.err != nil {
			provider := m.codexPendingOpenProvider()
			m.finishCodexPendingOpen(msg.projectPath, false)
			m.todoLaunchDraft = nil
			if strings.TrimSpace(msg.projectPath) != "" {
				if _, ok := m.codexSession(msg.projectPath); !ok {
					m.showEmbeddedOpenFailure(msg.projectPath, provider, msg.err)
				}
			}
			m.status = "Embedded session open failed"
			m.err = msg.err
			return m, nil
		}
		m.finishCodexPendingOpen(msg.projectPath, true)
		if m.todoLaunchDraft != nil && strings.TrimSpace(m.todoLaunchDraft.projectPath) == strings.TrimSpace(msg.projectPath) {
			m.status = "Fresh " + m.todoLaunchDraft.provider.Label() + " session ready with TODO draft. Edit and press Enter to send."
			m.todoLaunchDraft = nil
		} else {
			m.status = msg.status
		}
		return m, m.codexInput.Focus()
	case codexActionMsg:
		if msg.err != nil {
			m.err = msg.err
			m.status = "Embedded session action failed"
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
			m.rememberEmbeddedModelPreference(msg.provider, msg.model, msg.reasoning)
			m.recordRecentModel(msg.provider, msg.model)
			return m, m.saveEmbeddedModelPreferencesCmd()
		}
		if msg.closed {
			delete(m.codexClosedHandled, msg.projectPath)
			if m.codexVisibleProject == msg.projectPath {
				m.codexVisibleProject = ""
				m.codexInput.Blur()
			}
			if m.codexHiddenProject == msg.projectPath {
				m.codexHiddenProject = ""
			}
			cmds := []tea.Cmd{m.scanCmd(false), m.loadProjectsCmd()}
			if p, ok := m.selectedProject(); ok {
				cmds = append(cmds, m.loadDetailCmd(p.Path))
			}
			return m, tea.Batch(cmds...)
		}
		return m, nil
	case codexModelListMsg:
		if strings.TrimSpace(msg.projectPath) != strings.TrimSpace(m.codexVisibleProject) {
			return m, nil
		}
		if msg.err != nil {
			m.codexModelPicker = nil
			m.err = msg.err
			m.status = "Embedded model picker failed"
			return m, nil
		}
		m.err = nil
		m.openLoadedCodexModelPicker(msg.models)
		return m, nil
	case codexResumeChoicesMsg:
		return m.applyCodexResumeChoices(msg)
	case busMsg:
		cmds := []tea.Cmd{m.waitBusCmd()}
		if msg.Type == events.ClassificationUpdated {
			if msg.Payload["status"] == "completed" {
				m.markAssessmentFlash(msg.ProjectPath, msg.At)
			}
			cmds = append(cmds, m.loadProjectsCmd())
			if p, ok := m.selectedProject(); ok && p.Path == msg.ProjectPath {
				cmds = append(cmds, m.loadDetailCmd(p.Path))
			}
			return m, tea.Batch(cmds...)
		}
		cmds = append(cmds, m.loadProjectsCmd())
		if p, ok := m.selectedProject(); ok {
			cmds = append(cmds, m.loadDetailCmd(p.Path))
		}
		return m, tea.Batch(cmds...)
	case spinnerTickMsg:
		m.spinnerFrame = (m.spinnerFrame + 1) % spinnerAnimationFrameWrap
		m.refreshUsagePulse()
		m.pruneTransientHighlights(m.currentTime())
		m.syncRuntimeViewport(false)
		return m, spinnerTickCmd()
	case codexUpdateMsg:
		cmds := []tea.Cmd{m.waitCodexCmd()}
		if m.codexManager != nil {
			m.codexManager.AckUpdate(msg.projectPath)
		}
		snapshot, ok := m.refreshCodexSnapshot(msg.projectPath)
		if m.codexVisibleProject == msg.projectPath {
			m.resetCodexToolAnswerState(msg.projectPath)
			m.syncCodexViewport(true)
		}
		if ok {
			if !snapshot.Closed {
				m.markCodexSessionLive(msg.projectPath)
				return m, tea.Batch(cmds...)
			}
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
			cmds = append(cmds, m.scanCmd(false))
			cmds = append(cmds, m.loadProjectsCmd())
		} else {
			m.dropCodexSnapshot(msg.projectPath)
		}
		return m, tea.Batch(cmds...)
	}

	return m, nil
}

func (m Model) updateNormalMode(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
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
			return m.launchEmbeddedForSelection(preferredEmbeddedProviderForProject(project), false, "")
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
	case "end":
		if m.focusedPane == focusDetail {
			m.detailViewport.GotoBottom()
			return m, nil
		}
		if m.focusedPane == focusRuntime {
			m.runtimeViewport.GotoBottom()
			return m, nil
		}
		return m, m.moveSelectionTo(max(0, len(m.projects)-1))
	case "left", "h":
		if m.focusedPane == focusRuntime {
			m.moveRuntimeActionSelection(-1)
			return m, nil
		}
	case "right", "l":
		if m.focusedPane == focusRuntime {
			m.moveRuntimeActionSelection(1)
			return m, nil
		}
	case "o":
		if m.focusedPane == focusRuntime {
			return m, nil
		}
		if m.sortMode == sortByAttention {
			return m, m.setSortMode(sortByRecent)
		}
		return m, m.setSortMode(sortByAttention)
	case "v":
		if m.visibility == visibilityAIFolders {
			return m, m.setVisibilityMode(visibilityAllFolders)
		}
		return m, m.setVisibilityMode(visibilityAIFolders)
	case "p":
		if p, ok := m.selectedProject(); ok {
			return m, m.togglePinCmd(p.Path)
		}
	case "t":
		return m, m.openTodoDialogForSelection()
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
		m.commitPreviewMessageOverride = ""
		m.commitPreviewRefreshing = false
		m.commitApplying = false
		m.status = "Commit preview canceled"
		return m, nil
	case "d":
		cmd := m.startDiffViewFromCommitPreview(*m.commitPreview, m.commitPreviewMessageOverride)
		m.commitPreview = nil
		m.commitPreviewMessageOverride = ""
		m.commitPreviewRefreshing = false
		m.commitApplying = false
		return m, cmd
	case "shift+enter", "alt+enter":
		if !m.commitPreview.CanPush {
			m.status = "Commit & push is unavailable for this repo"
			return m, nil
		}
		m.commitApplying = true
		m.status = "Committing and pushing..."
		return m, m.applyCommitPreviewCmd(*m.commitPreview, true)
	case "enter":
		m.commitApplying = true
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
	return m.loadDetailCmd(m.projects[m.selected].Path)
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
		return m.loadDetailCmd(p.Path)
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
		return m.loadDetailCmd(p.Path)
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
	layout := m.bodyLayout()
	m.detailViewport.Width = layout.detailContentWidth
	m.detailViewport.Height = max(1, layout.bottomPaneHeight-2)

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
	content := strings.ReplaceAll(m.renderDetailContent(width), "\r\n", "\n")
	lines := strings.Split(content, "\n")
	offset := m.detailViewport.YOffset
	maxOffset := max(0, len(lines)-height)
	if offset > maxOffset {
		offset = maxOffset
	}
	if offset < 0 {
		offset = 0
	}
	end := min(len(lines), offset+height)
	if offset >= len(lines) {
		return fitPaneContent("", width, height)
	}
	return fitPaneContent(strings.Join(lines[offset:end], "\n"), width, height)
}

func (m Model) View() string {
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
	} else if m.showHelp {
		body = m.renderHelpPanelOverlay(body, layout.width, layout.height)
	} else if m.projectFilterDialog != nil {
		body = m.renderProjectFilterOverlay(body, layout.width, layout.height)
	} else if m.commandMode {
		body = m.renderCommandPaletteOverlay(body, layout.width, layout.height)
	} else if m.codexPickerVisible {
		body = m.renderCodexPickerOverlay(body, layout.width, layout.height)
	} else if m.ignoredPickerVisible {
		body = m.renderIgnoredPickerOverlay(body, layout.width, layout.height)
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
	if m.noteDialog != nil {
		body = m.renderNoteDialogOverlay(body, layout.width, layout.height)
	}
	if m.noteCopyDialog != nil {
		body = m.renderNoteCopyDialogOverlay(body, layout.width, layout.height)
	}
	if m.noteClearConfirm != nil {
		body = m.renderNoteClearConfirmOverlay(body, layout.width, layout.height)
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
	status := m.status
	if m.err != nil {
		status = fmt.Sprintf("%s | error: %v", status, m.err)
	}
	if aiNotice := m.renderAIBackendStatusNotice(); aiNotice != "" {
		status = fmt.Sprintf("%s | %s", status, aiNotice)
	}
	status = fmt.Sprintf("%s | AI %s", status, m.renderClassificationSummary())
	return fitFooterWidth(title+" - "+status, width)
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

func (m Model) selectedProject() (model.ProjectSummary, bool) {
	if m.selected < 0 || m.selected >= len(m.projects) {
		return model.ProjectSummary{}, false
	}
	return m.projects[m.selected], true
}

func (m Model) projectHasLiveCodexSession(projectPath string) bool {
	projectPath = strings.TrimSpace(projectPath)
	if projectPath == "" || m.codexManager == nil {
		return false
	}
	session, ok := m.codexManager.Session(projectPath)
	if !ok {
		return false
	}
	snapshot := session.Snapshot()
	return snapshot.Started && !snapshot.Closed
}

func (m Model) liveCodexSnapshot(projectPath string) (codexapp.Snapshot, bool) {
	projectPath = strings.TrimSpace(projectPath)
	if projectPath == "" || m.codexManager == nil {
		return codexapp.Snapshot{}, false
	}
	session, ok := m.codexManager.Session(projectPath)
	if !ok {
		return codexapp.Snapshot{}, false
	}
	snapshot := session.Snapshot()
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
		selectedRow := i == m.selected
		cellStyle := func(style lipgloss.Style) lipgloss.Style {
			style = projectListCellStyle(style, selectedRow)
			if m.projectApprovalPulseActive(p.Path) {
				style = approvalPulseStyle(style)
			}
			return style
		}
		last := formatListActivityTime(now, p.LastActivity)
		attention := projectAttentionLabelForScore(p, m.projectAttentionScore(p))
		name := truncateText(p.Name, projectW)
		assessment := truncateText(projectAssessmentTextAt(p, now), assessmentW)
		runtimeSnapshot := m.projectRuntimeSnapshot(p.Path)
		agentLabel, agentTag, agentLive := m.projectAgentDisplay(p, now)
		todoCount := projectTODOCountLabel(p.OpenTODOCount)
		runLabel, runState := projectRunSummary(runtimeSnapshot, p.RunCommand)
		row := lipgloss.JoinHorizontal(
			lipgloss.Top,
			cellStyle(lipgloss.NewStyle().Width(5).Align(lipgloss.Right).Bold(selectedRow)).Render(attention),
			"  ",
			cellStyle(m.projectListAssessmentStatusStyle(p).Width(8)).Render(projectListStatus(p)),
			" ",
			cellStyle(lipgloss.NewStyle().Width(10)).Render(last),
			" ",
			cellStyle(sourceStyleForTag(agentTag, agentLive).Width(projectListAgentWidth).Align(lipgloss.Left)).Render(truncateText(agentLabel, projectListAgentWidth)),
			" ",
			cellStyle(noteListIndicatorStyle.Width(projectListTODOWidth).Align(lipgloss.Right)).Render(todoCount),
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
	p, ok := m.selectedProject()
	if !ok {
		if len(m.allProjects) > 0 && m.visibility == visibilityAIFolders {
			return "No AI-linked folder selected\nPress v for All folders"
		}
		return "Select a project"
	}
	d := m.detail
	if d.Summary.Path != "" && d.Summary.Path != p.Path {
		d = model.ProjectDetail{}
	}
	assessmentValue := assessmentDisplayStyle(p).Render(projectAssessmentLabelAt(p, m.currentTime()))
	statusValue := activityDisplayStyle(p).Render(projectActivityStatus(p))
	attentionValue := detailAttentionValueStyle.Render(fmt.Sprintf("%d", m.projectAttentionScore(p)))

	lines := []string{detailField("Path", detailValueStyle.Render(p.Path))}
	lines = appendDetailFields(lines, width,
		detailField("Assessment", assessmentValue),
		detailField("Status", statusValue),
	)
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
	lines = appendDetailFields(lines, width,
		detailField("Repo", repoDirtyDetailValue(p)),
		detailField("Remote", repoSyncDetailValue(p)),
	)
	lines = append(lines, detailField("Attention", attentionValue))
	if p.SnoozedUntil != nil {
		lines = append(lines, detailField("Snoozed until", detailValueStyle.Render(p.SnoozedUntil.Format(time.RFC3339))))
	}
	lines = append(lines, detailSectionStyle.Render("TODO"))
	if p.TotalTODOCount == 0 {
		lines = append(lines, detailMutedStyle.Render("No TODOs yet. Press t or run /todo."))
	} else {
		lines = append(lines, detailField("Counts", detailValueStyle.Render(fmt.Sprintf("%d open, %d total", p.OpenTODOCount, p.TotalTODOCount))))
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

	lines = append(lines, detailSectionStyle.Render("Attention reasons"))
	reasons := m.projectAttentionReasons(p, d.Reasons)
	if len(reasons) == 0 {
		lines = append(lines, detailMutedStyle.Render("- none"))
	} else {
		for _, r := range reasons {
			lines = append(lines, detailReasonLine(r))
		}
	}

	lines = append(lines, detailSectionStyle.Render("Session summary"))
	summaryText := projectAssessmentTextAt(p, m.currentTime())
	summaryStyle := detailValueStyle
	if projectAssessmentRefreshing(p) {
		summaryStyle = detailMutedStyle
	}
	if strings.TrimSpace(summaryText) == "" || summaryText == "-" {
		lines = append(lines, renderWrappedDetailBullet(detailMutedStyle, width, "not assessed yet"))
	} else {
		lines = append(lines, renderWrappedDetailBullet(summaryStyle, width, summaryText))
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
	dialogPanelBackground       = lipgloss.Color("235")
	dialogPanelBorderColor      = lipgloss.Color("81")
	dialogPanelFillReset        = "\x1b[48;5;235m"
	dialogPanelResetReplacer    = strings.NewReplacer("\x1b[0m", "\x1b[0m"+dialogPanelFillReset, "\x1b[m", "\x1b[m"+dialogPanelFillReset)
	dialogPanelFillStyle        = lipgloss.NewStyle().Background(dialogPanelBackground)
	detailLabelStyle            = lipgloss.NewStyle().Foreground(lipgloss.Color("81")).Bold(true)
	detailSectionStyle          = lipgloss.NewStyle().Foreground(lipgloss.Color("214")).Bold(true)
	detailValueStyle            = lipgloss.NewStyle().Foreground(lipgloss.Color("252"))
	detailMutedStyle            = lipgloss.NewStyle().Foreground(lipgloss.Color("244"))
	detailWarningStyle          = lipgloss.NewStyle().Foreground(lipgloss.Color("178")).Bold(true)
	detailDangerStyle           = lipgloss.NewStyle().Foreground(lipgloss.Color("203")).Bold(true)
	topStatusWarningBadgeStyle  = lipgloss.NewStyle().Foreground(lipgloss.Color("255")).Background(lipgloss.Color("160")).Bold(true).Padding(0, 1)
	topStatusSetupBadgeStyle    = lipgloss.NewStyle().Foreground(lipgloss.Color("16")).Background(lipgloss.Color("214")).Bold(true).Padding(0, 1)
	detailAttentionValueStyle   = lipgloss.NewStyle().Foreground(lipgloss.Color("229")).Bold(true)
	projectListSelectedRowStyle = lipgloss.NewStyle().
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

func spinnerTickCmd() tea.Cmd {
	return tea.Tick(120*time.Millisecond, func(time.Time) tea.Msg {
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
	return func() tea.Msg {
		d, err := m.svc.Store().GetProjectDetail(m.ctx, path, 20)
		return detailMsg{detail: d, err: err}
	}
}

func (m Model) dispatchCommand(inv commands.Invocation) (tea.Model, tea.Cmd) {
	switch inv.Kind {
	case commands.KindHelp:
		m.showHelp = true
		m.status = "Help open. Press ? or Esc to close"
		return m, nil
	case commands.KindRefresh:
		m.loading = true
		m.status = "Scanning and retrying failed assessments..."
		return m, m.scanCmd(true)
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
		m.status = "Pushing..."
		return m, m.pushCmd(p.Path)
	case commands.KindCodex:
		return m.launchCodexForSelection(false, inv.Prompt)
	case commands.KindCodexNew:
		return m.launchCodexForSelection(true, inv.Prompt)
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
	case commands.KindNote:
		p, ok := m.selectedProject()
		if !ok {
			m.status = "No project selected"
			return m, nil
		}
		if inv.Clear {
			if !projectHasNote(p.Note) {
				m.status = "No note to clear"
				return m, nil
			}
			return m, m.openNoteClearConfirm(p.Path, p.Name)
		}
		return m, m.openNoteDialog(p)
	case commands.KindPin:
		p, ok := m.selectedProject()
		if !ok {
			m.status = "No project selected"
			return m, nil
		}
		return m, m.togglePinCmd(p.Path)
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
		return m, nil
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

func (m Model) selectedProjectCodexSessionID(project model.ProjectSummary) string {
	return m.selectedProjectSessionID(project, codexapp.ProviderCodex)
}

func (m Model) liveEmbeddedSnapshotForProject(projectPath string, provider codexapp.Provider) (codexapp.Snapshot, bool) {
	projectPath = strings.TrimSpace(projectPath)
	provider = provider.Normalized()
	if projectPath == "" || provider == "" {
		return codexapp.Snapshot{}, false
	}
	session, ok := m.codexSession(projectPath)
	if !ok {
		return codexapp.Snapshot{}, false
	}
	snapshot := session.Snapshot()
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
			if providerForSessionFormat(session.Format) == provider.Normalized() && strings.TrimSpace(session.SessionID) != "" {
				return session.SessionID
			}
		}
	}
	if providerForSessionFormat(project.LatestSessionFormat) == provider.Normalized() {
		return strings.TrimSpace(project.LatestSessionID)
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
	default:
		return ""
	}
}

func preferredEmbeddedProviderForProject(project model.ProjectSummary) codexapp.Provider {
	if provider := providerForSessionFormat(project.LatestSessionFormat); provider != "" {
		return provider
	}
	return codexapp.ProviderCodex
}

func (m Model) currentEmbeddedLaunchLabel() string {
	if project, ok := m.selectedProject(); ok {
		return preferredEmbeddedProviderForProject(project).Label()
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

func (m Model) setNoteCmd(path, note string) tea.Cmd {
	return func() tea.Msg {
		err := m.svc.SetNote(m.ctx, path, note)
		return actionMsg{status: "Note saved", err: err}
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
			return browserOpenMsg{err: err}
		}
		return browserOpenMsg{status: "Opened project in browser"}
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
	m.commitPreviewMessageOverride = strings.TrimSpace(messageOverride)
	m.commitPreviewRefreshing = true
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

func (m Model) toggleDiffStageCmd(projectPath string, file service.DiffFilePreview) tea.Cmd {
	return func() tea.Msg {
		status, err := m.svc.ToggleDiffFileStage(m.ctx, projectPath, file)
		if err != nil {
			return diffStageToggleMsg{status: status, path: file.Path, originalPath: file.OriginalPath, err: err}
		}
		preview, err := m.svc.PrepareDiff(m.ctx, projectPath)
		return diffStageToggleMsg{
			preview:      preview,
			status:       status,
			path:         file.Path,
			originalPath: file.OriginalPath,
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

func (m Model) resolveSubmodulesAndContinueCmd(path string, intent service.GitActionIntent, message string) tea.Cmd {
	return func() tea.Msg {
		preview, err := m.svc.ResolveSubmodulesAndPrepareCommit(m.ctx, path, intent, message)
		return commitPreviewMsg{preview: preview, projectPath: path, intent: intent, message: message, err: err}
	}
}

func (m Model) applyCommitPreviewCmd(preview service.CommitPreview, pushAfterCommit bool) tea.Cmd {
	return func() tea.Msg {
		result, err := m.svc.ApplyCommit(m.ctx, preview, pushAfterCommit)
		if err != nil {
			return actionMsg{status: "Commit failed", err: err}
		}
		status := "Committed " + result.CommitHash
		if result.Pushed {
			status = "Committed " + result.CommitHash + " and pushed"
		}
		if result.Warning != "" {
			status = result.Warning
		}
		return actionMsg{status: status, err: nil}
	}
}

func (m Model) pushCmd(path string) tea.Cmd {
	return func() tea.Msg {
		result, err := m.svc.PushProject(m.ctx, path)
		if err != nil {
			return actionMsg{status: "Push failed", err: err}
		}
		status := result.Summary
		if strings.TrimSpace(status) == "" {
			status = "Push complete"
		}
		return actionMsg{status: status, err: nil}
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
	return project.RepoDirty || repoSyncWarning(project.RepoSyncStatus)
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
	return projectAssessmentTextAt(project, time.Time{})
}

func projectAssessmentTextAt(project model.ProjectSummary, now time.Time) string {
	_ = now
	if strings.TrimSpace(project.LatestSessionSummary) != "" && project.LatestSessionClassification == model.ClassificationCompleted {
		return project.LatestSessionSummary
	}
	if strings.TrimSpace(project.LatestCompletedSessionSummary) != "" {
		return project.LatestCompletedSessionSummary
	}
	if label, _, ok := visibleAssessmentStatusLabel(project); ok {
		return label
	}
	if project.LatestSessionFormat != "" {
		return "not assessed yet"
	}
	return "-"
}

func projectAssessmentStyle(project model.ProjectSummary) lipgloss.Style {
	if _, _, ok := visibleAssessmentStatusLabel(project); ok {
		if projectAssessmentRefreshing(project) {
			return detailMutedStyle
		}
		return detailValueStyle
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

func (m Model) projectAgentDisplay(project model.ProjectSummary, now time.Time) (string, string, bool) {
	if snapshot, ok := m.liveCodexSnapshot(project.Path); ok {
		tag := embeddedProvider(snapshot).SourceTag()
		label := tag
		if snapshot.Busy {
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

func repoDirtyDetailValue(project model.ProjectSummary) string {
	if project.RepoDirty {
		return detailWarningStyle.Render("dirty worktree")
	}
	return detailMutedStyle.Render("clean")
}

func repoSyncDetailValue(project model.ProjectSummary) string {
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

func assessmentDisplayStyle(project model.ProjectSummary) lipgloss.Style {
	if _, category, ok := visibleAssessmentStatusLabel(project); ok {
		if projectAssessmentRefreshing(project) {
			return detailMutedStyle
		}
		return classificationCategoryStyle(category)
	}
	return detailMutedStyle
}

func projectAssessmentLabelAt(project model.ProjectSummary, now time.Time) string {
	_ = now
	if label, _, ok := visibleAssessmentStatusLabel(project); ok {
		return label
	}
	if project.LatestSessionFormat != "" {
		return "not assessed yet"
	}
	return "not assessed"
}

func projectListStatus(project model.ProjectSummary) string {
	if projectMissing(project) {
		return "missing"
	}
	if moveStatusActive(project.MovedAt, project.Path, project.LatestSessionDetectedProjectPath) {
		return "moved"
	}
	if label, _, ok := visibleAssessmentStatusLabel(project); ok {
		return label
	}
	if project.LatestSessionFormat != "" {
		return "new"
	}
	return projectActivityStatus(project)
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

func (m *Model) rebuildProjectList(selectedPath string) {
	sorted := append([]model.ProjectSummary(nil), m.allProjects...)
	m.sortProjects(sorted)
	m.projects = filterProjects(sorted, m.visibility, m.excludeProjectPatterns, m.projectFilter)
	if m.privacyMode {
		m.projects = filterProjectsByPrivacy(m.projects, m.privacyPatterns)
	}
	if len(m.projects) == 0 {
		m.selected = 0
		m.offset = 0
		return
	}
	preservedSelection := false
	if selectedPath != "" {
		if idx := m.indexByPath(selectedPath); idx >= 0 {
			m.selected = idx
			preservedSelection = true
		}
	}
	if selectedPath != "" && !preservedSelection {
		m.selected = 0
	}
	if m.selected >= len(m.projects) {
		m.selected = max(0, len(m.projects)-1)
	}
	m.ensureSelectionVisible()
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

func (m Model) renderFooter(width int) string {
	usageLabel := m.footerUsageLabel()
	usageSegment := m.renderFooterUsageSegment(usageLabel)
	filterSegment := m.renderFooterProjectFilterSegment()
	if m.diffView != nil {
		return renderFooterLine(width, renderDiffFooter(width, *m.diffView, usageSegment), filterSegment)
	}
	if m.gitStatusDialog != nil {
		label := gitStatusDialogReadyStatus(*m.gitStatusDialog)
		if m.gitStatusApplying {
			label = "Applying git action..."
		}
		return m.renderModalFooter(width, label, filterSegment, usageSegment)
	}
	if m.commitPreview != nil {
		label := commitPreviewReadyStatus(m.commitPreview.CanPush)
		if m.commitApplying {
			label = "Applying git action..."
		} else if m.commitPreviewRefreshing {
			label = "Refreshing commit preview..."
		}
		return m.renderModalFooter(width, label, filterSegment, usageSegment)
	}
	if m.newProjectDialog != nil {
		label := "New project: Enter create/add, Space toggle git, Alt+1..3 recent, Esc cancel"
		if m.newProjectDialog.Submitting {
			label = "New project: applying..."
		}
		return m.renderModalFooter(width, label, filterSegment, usageSegment)
	}
	if m.projectFilterDialog != nil {
		label := "Project filter: type to narrow, Enter keep, Esc close"
		return m.renderModalFooter(width, label, filterSegment, usageSegment)
	}
	if m.commandMode {
		return m.renderModalFooter(width, "Command palette open", filterSegment, usageSegment)
	}
	if m.setupMode {
		return m.renderModalFooter(width, "Setup: Enter choose, r refresh, s settings, Esc continue", filterSegment, usageSegment)
	}
	if m.settingsMode {
		return m.renderModalFooter(width, "Settings: Enter save, Tab next, Esc cancel", filterSegment, usageSegment)
	}
	if m.noteClearConfirm != nil {
		return m.renderModalFooter(width, "Confirm note clear", filterSegment, usageSegment)
	}
	if m.noteCopyDialog != nil {
		return m.renderModalFooter(width, "Copy note text: Enter copy, Tab next, Esc cancel", filterSegment, usageSegment)
	}
	if m.noteDialog != nil && m.noteDialog.Selection != nil {
		return m.renderModalFooter(width, "Note selection: Space mark/copy, arrows move, Esc cancel", filterSegment, usageSegment)
	}
	if m.noteDialog != nil {
		return m.renderModalFooter(width, "Project notes: Ctrl+Y copy, Ctrl+S save, Tab actions, Esc cancel", filterSegment, usageSegment)
	}
	return renderFooterLine(
		width,
		compactFooterBase(width, m.focusedPane, m.detailViewport.ScrollPercent(), m.runtimeViewport.ScrollPercent(), m.hasHiddenCodexSession(), m.currentEmbeddedLaunchLabel()),
		filterSegment,
		usageSegment,
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

func (m Model) renderCommitPreview(bodyW int) string {
	panelWidth := min(bodyW, min(max(54, bodyW-12), 96))
	panelInnerWidth := max(28, panelWidth-4)
	return renderDialogPanel(panelWidth, panelInnerWidth, m.renderCommitPreviewContent(panelInnerWidth))
}

func (m Model) renderCommitPreviewOverlay(body string, bodyW, bodyH int) string {
	panel := m.renderCommitPreview(bodyW)
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

func (m Model) renderCommitPreviewContent(width int) string {
	if m.commitPreview == nil {
		return ""
	}
	preview := *m.commitPreview
	placeholder := commitPreviewHasPlaceholderState(preview)

	lines := []string{
		renderDialogHeader("Commit Preview", preview.ProjectName, preview.Branch, width),
		"",
	}
	lines = append(lines, renderCommitPreviewMessageBlock("Message", preview.Message, width))
	lines = append(lines, "")
	lines = append(lines,
		commitPreviewLine("Stage", stageModeLabel(preview.StageMode, len(preview.SelectedUntracked))),
	)

	if strings.TrimSpace(preview.LatestSummary) != "" {
		lines = append(lines, commitPreviewLine("Context", preview.LatestSummary))
	}

	lines = append(lines, "")
	lines = append(lines, commandPaletteTitleStyle.Render("Changes"))
	if placeholder {
		lines = append(lines, detailMutedStyle.Render("- Inspecting repo changes..."))
	} else {
		lines = append(lines, renderCommitPreviewFiles(preview.Included, 6, width)...)
	}
	if !placeholder && strings.TrimSpace(preview.DiffSummary) != "" {
		lines = append(lines, commandPaletteHintStyle.Render(strings.TrimSpace(preview.DiffSummary)))
	}

	if !placeholder && len(preview.Excluded) > 0 {
		lines = append(lines, "")
		lines = append(lines, commandPaletteTitleStyle.Render("Left out"))
		lines = append(lines, renderCommitPreviewFiles(preview.Excluded, 4, width)...)
	}

	if len(preview.Warnings) > 0 {
		lines = append(lines, "")
		lines = append(lines, commandPaletteTitleStyle.Render("Warnings"))
		for _, warning := range preview.Warnings {
			lines = append(lines, detailWarningStyle.Render("- "+warning))
		}
	}

	lines = append(lines, "")
	if m.commitApplying {
		lines = append(lines, commandPaletteHintStyle.Render("Applying git action..."))
	} else if m.commitPreviewRefreshing {
		hint := "Refreshing commit preview..."
		if placeholder {
			hint = "Building commit preview..."
		}
		lines = append(lines, commandPaletteHintStyle.Render(hint))
	} else {
		lines = append(lines, renderCommitPreviewActions(preview.CanPush))
	}

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

func renderCommitPreviewMessageBlock(label, value string, width int) string {
	lines := []string{detailLabelStyle.Render(label)}
	body := strings.TrimSpace(value)
	if body == "" {
		body = "(empty)"
	}
	lines = append(lines, lipgloss.NewStyle().
		Width(max(12, width)).
		Foreground(lipgloss.Color("229")).
		Bold(true).
		Render(body))
	return strings.Join(lines, "\n")
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
		return compactUsageLabel(m.currentUsage())
	}
	switch status := m.setupSnapshot.SelectedStatus(); {
	case m.setupSnapshot.NeedsSetup():
		return "AI setup"
	case m.setupSnapshot.Selected == config.AIBackendDisabled:
		return "AI disabled"
	case m.setupSnapshot.Selected != config.AIBackendUnset && !status.Ready:
		return "AI unavailable"
	default:
		switch m.setupSnapshot.Selected {
		case config.AIBackendCodex, config.AIBackendOpenCode:
			return compactLocalUsageLabel(m.setupSnapshot.Selected.Label(), m.currentUsage())
		}
		return compactUsageLabel(m.currentUsage())
	}
}

func (m Model) aiBackendStatusNotice() string {
	if !m.setupChecked {
		return ""
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

func compactFooterBase(width int, focused paneFocus, detailScroll, runtimeScroll float64, hasHiddenCodex bool, launchLabel string) string {
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
			return joinFooterSegments(
				renderFooterMeta("Focus: list"),
				renderFooterActionList(
					footerPrimaryAction("Enter", launchLabel),
					footerNavAction("Alt+Down", "picker"),
					footerNavAction("Alt+[/]", "sessions"),
					footerNavAction("f", "filter"),
					footerNavAction("/", "command"),
					footerLowAction("?", "help"),
					footerExitAction("q", "quit"),
				),
			)
		}
		return joinFooterSegments(
			renderFooterMeta("Focus: list"),
			renderFooterActionList(
				footerPrimaryAction("Enter", launchLabel),
				footerNavAction("Alt+Down", "picker"),
				footerNavAction("f", "filter"),
				footerNavAction("/", "command"),
				footerNavAction("Tab", "switch"),
				footerNavAction("v", "view"),
				footerLowAction("?", "help"),
				footerExitAction("q", "quit"),
			),
		)
	case width >= 60:
		if hasHiddenCodex {
			return joinFooterSegments(
				renderFooterMeta("Focus: list"),
				renderFooterActionList(
					footerPrimaryAction("Enter", launchLabel),
					footerNavAction("Alt+Down", "picker"),
					footerNavAction("f", "filter"),
					footerNavAction("/", "command"),
					footerLowAction("?", "help"),
					footerExitAction("q", "quit"),
				),
			)
		}
		return joinFooterSegments(
			renderFooterMeta("Focus: list"),
			renderFooterActionList(
				footerPrimaryAction("Enter", launchLabel),
				footerNavAction("Alt+Down", "picker"),
				footerNavAction("f", "filter"),
				footerNavAction("/", "command"),
				footerNavAction("Tab", "switch"),
				footerLowAction("?", "help"),
				footerExitAction("q", "quit"),
			),
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
		return joinFooterSegments(renderFooterMeta("List"), renderFooterActionList(actions...))
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
		commandPaletteHintStyle.Render("Try /setup, /codex, /opencode, /todo, /commit, /diff, or /run."),
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

func statusDisplayStyle(project model.ProjectSummary) lipgloss.Style {
	if projectMissing(project) || moveStatusActive(project.MovedAt, project.Path, project.LatestSessionDetectedProjectPath) {
		return activityDisplayStyle(project)
	}
	if _, _, ok := visibleAssessmentStatusLabel(project); ok {
		return assessmentDisplayStyle(project)
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
	style := statusDisplayStyle(project)
	if m.assessmentFlashActive(project.Path) {
		return assessmentFlashStyle(style)
	}
	return style
}

func (m Model) projectListAssessmentSummaryStyle(project model.ProjectSummary) lipgloss.Style {
	style := projectAssessmentStyle(project)
	if m.assessmentFlashActive(project.Path) {
		return assessmentFlashStyle(style)
	}
	return style
}

func assessmentStatusLabel(project model.ProjectSummary, compact bool) (string, model.SessionCategory, bool) {
	if project.LatestSessionClassification != model.ClassificationCompleted {
		return "", model.SessionCategoryUnknown, false
	}
	return assessmentStatusLabelForCategory(project.LatestSessionClassificationType, compact)
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
	if label, category, ok := assessmentStatusLabel(project, false); ok {
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
