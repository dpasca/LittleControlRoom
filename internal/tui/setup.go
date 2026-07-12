package tui

import (
	"fmt"
	"os"
	"strings"
	"time"

	"lcroom/internal/aibackend"
	"lcroom/internal/brand"
	"lcroom/internal/config"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
)

type setupRole int

const (
	setupRoleProjectReports setupRole = iota
	setupRoleBossChat
	setupRoleLCAgent
)

type setupStep int

const (
	setupStepProjectProvider setupStep = iota
	setupStepProjectConfig
	setupStepBossProvider
	setupStepBossConfig
	setupStepLCAgentConfig
	setupStepSave
)

type setupSectionMenuRow struct {
	step    setupStep
	label   string
	summary string
	detail  string
}

func setupSectionMenuRows() []setupSectionMenuRow {
	return []setupSectionMenuRow{
		{
			step:    setupStepProjectProvider,
			label:   "Project reports",
			summary: "Background AI",
			detail:  "Choose the runner for background summaries, classification, TODO help, commit help, and other project-reporting work.",
		},
		{
			step:    setupStepBossProvider,
			label:   "Help chat",
			summary: "Realtime chat",
			detail:  "Choose the backend for /help. This is separate from background project reports so chat can use a faster or higher-grade model.",
		},
		{
			step:    setupStepLCAgentConfig,
			label:   "LCAgent",
			summary: "Native worker",
			detail:  "Configure the LCR-native worker essentials: main model, utility model, credentials, and web search.",
		},
		{
			step:    setupStepSave,
			label:   "Save",
			summary: "Write config",
			detail:  "Review the selected setup choices and write them to config.toml.",
		},
	}
}

func (m *Model) openSetupMode() tea.Cmd {
	settings := m.currentSettingsBaseline()
	m.settingsFields = newSettingsFields(settings)
	saved := cloneEditableSettings(settings)
	m.settingsBaseline = &saved
	m.blurSettingsFields()
	m.setupMode = true
	m.settingsMode = false
	m.commandMode = false
	m.showHelp = false
	m.err = nil
	m.setupSaving = false
	m.setupReviewMode = false
	m.setupSectionNavigation = true
	m.setupSectionMenu = true
	m.setupSectionSelected = 0
	m.localModelPickerVisible = false
	m.settingsLCAgentProviderVisible = false
	m.settingsLCAgentProviderSelected = 0
	m.settingsLCAgentSearchPickerVisible = false
	m.settingsLCAgentSearchPickerSelected = 0
	m.settingsLCAgentModelPicker = nil
	m.settingsLCAgentVisionCheckInFlight = false
	m.settingsChoicePicker = nil
	m.setupStep = setupStepProjectProvider
	m.setupFocusedRole = setupRoleProjectReports
	m.setupConfigMode = false
	m.setupConfigSelected = 0
	m.setupSelected = m.setupSelectionForBackend(m.recommendedSetupBackend())
	m.setupBossSelected = m.setupBossSelectionForBackend(m.recommendedSetupBossBackend())
	tier, _ := config.ParseModelTier(m.currentSettingsBaseline().OpenCodeModelTier)
	m.setupModelTier = tier
	m.setupLoading = true
	m.status = "Setup open. Choose a section, then press Enter."
	return m.refreshSetupSnapshotCmd(false)
}

func (m Model) startupSetupSnapshotCmd() tea.Cmd {
	if m.currentSettingsBaseline().AIBackend != config.AIBackendUnset {
		return nil
	}
	return m.refreshSetupSnapshotCmd(true)
}

func (m *Model) closeSetupMode(status string) {
	m.setupMode = false
	m.setupLoading = false
	m.setupSaving = false
	m.setupReviewMode = false
	m.setupSectionNavigation = false
	m.setupSectionMenu = false
	m.setupSectionSelected = 0
	m.setupConfigMode = false
	m.setupStep = setupStepProjectProvider
	m.setupConfigSelected = 0
	m.localModelPickerVisible = false
	m.settingsLCAgentProviderVisible = false
	m.settingsLCAgentProviderSelected = 0
	m.settingsLCAgentSearchPickerVisible = false
	m.settingsLCAgentSearchPickerSelected = 0
	m.settingsLCAgentModelPicker = nil
	m.settingsLCAgentVisionCheckInFlight = false
	m.settingsChoicePicker = nil
	m.blurSettingsFields()
	if status != "" {
		m.status = status
	}
}

func (m Model) refreshSetupSnapshotCmd(openOnStartup bool) tea.Cmd {
	cfg := setupDetectionConfig(m.currentSettingsBaseline())
	return func() tea.Msg {
		return setupSnapshotMsg{
			snapshot:      aibackend.Detect(m.ctx, cfg),
			openOnStartup: openOnStartup,
		}
	}
}

func setupDetectionConfig(settings config.EditableSettings) config.AppConfig {
	return config.AppConfigFromEditableSettings(config.Default(), settings)
}

func (m Model) saveSetupCmd(settings config.EditableSettings) tea.Cmd {
	settings = config.NormalizeEditableSettings(settings)
	path := m.currentWritableConfigPath()
	return func() tea.Msg {
		err := config.SaveEditableSettings(path, settings)
		return setupSavedMsg{settings: settings, path: path, err: err}
	}
}

func (m Model) updateSetupMode(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	if m.setupLoading || m.setupSaving {
		return m, nil
	}
	if m.setupReviewMode {
		return m.updateSetupReviewMode(msg)
	}
	if m.setupConfigMode {
		return m.updateSetupConfigMode(msg)
	}
	if m.setupSectionMenu {
		return m.updateSetupSectionMenuMode(msg)
	}
	switch msg.String() {
	case "esc":
		return m.setupGoBack()
	case "right", "]":
		return m.setupAdvance()
	case "left", "[":
		return m.setupGoBack()
	case "down", "j", "ctrl+n":
		m.moveSetupProvider(1)
		return m, nil
	case "up", "k", "ctrl+p":
		m.moveSetupProvider(-1)
		return m, nil
	case "r":
		m.setupLoading = true
		m.status = "Refreshing AI backend checks..."
		return m, m.refreshSetupSnapshotCmd(false)
	case "ctrl+s":
		if m.setupSectionNavigation {
			return m.saveSetupFromCurrentChoices()
		}
	case "t":
		if m.setupStep == setupStepProjectProvider && m.setupSelectedBackend().SupportsModelTier() {
			m.setupModelTier = m.cycleModelTier(m.setupModelTier)
			return m, nil
		}
	case "e":
		// Keep the old shortcut working quietly while Enter becomes the guided path.
		return m.setupEnterConfigStep()
	case "m":
		if isLocalBackendModelPickerBackend(m.setupSelectedLocalModelBackend()) {
			return m.openLocalBackendModelPicker()
		}
	case "enter":
		return m.setupAdvance()
	}
	return m, nil
}

func (m Model) updateSetupConfigMode(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	switch msg.String() {
	case "esc":
		if m.setupSectionNavigation {
			return m.closeSetupSectionDialog("Back to setup sections.")
		}
		return m.setupGoBack()
	case "tab", "down", "ctrl+n":
		cmd := m.moveSetupConfigSelection(1)
		return m, cmd
	case "shift+tab", "up", "ctrl+p":
		cmd := m.moveSetupConfigSelection(-1)
		return m, cmd
	case "ctrl+s":
		if m.setupSectionNavigation {
			return m.saveSetupFromCurrentChoices()
		}
		return m.setupAdvance()
	case "v":
		if settingsFieldCanCheckLCAgentVision(m.setupSelectedConfigFieldIndex()) {
			return m.checkSettingsLCAgentVision()
		}
	case "enter":
		if settingsFieldUsesLocalBackendModelPicker(m.setupSelectedConfigFieldIndex()) {
			return m.openLocalBackendModelPicker()
		}
		if settingsFieldUsesPicker(m.setupSelectedConfigFieldIndex()) {
			return m.openSettingsPickerForField(m.setupSelectedConfigFieldIndex())
		}
		if m.settingsFieldUsesUnifiedCloudModelPicker(m.setupSelectedConfigFieldIndex()) {
			return m.openSettingsLCAgentModelPicker()
		}
		return m.setupAdvance()
	}

	fieldIndex := m.setupSelectedConfigFieldIndex()
	if fieldIndex < 0 || fieldIndex >= len(m.settingsFields) {
		return m, nil
	}
	if settingsFieldUsesPicker(fieldIndex) {
		return m, nil
	}
	if m.settingsFieldUsesUnifiedCloudModelPicker(fieldIndex) {
		return m, nil
	}
	input, cmd := m.settingsFields[fieldIndex].input.Update(msg)
	m.settingsFields[fieldIndex].input = input
	return m, cmd
}

func (m Model) updateSetupReviewMode(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	switch msg.String() {
	case "enter", "ctrl+s":
		return m.saveSetupFromCurrentChoices()
	case "esc":
		if m.setupSectionNavigation {
			return m.closeSetupSectionDialog("Back to setup sections.")
		}
		return m.setupGoBack()
	case "left", "[":
		return m.setupGoBack()
	case "r":
		m.setupLoading = true
		m.status = "Refreshing AI backend checks..."
		return m, m.refreshSetupSnapshotCmd(false)
	}
	return m, nil
}

func (m Model) updateSetupSectionMenuMode(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	switch msg.String() {
	case "esc":
		m.closeSetupMode("Setup skipped for now. Run /setup anytime.")
		return m, nil
	case "tab", "down", "j", "ctrl+n":
		m.moveSetupSectionSelection(1)
		return m, nil
	case "shift+tab", "up", "k", "ctrl+p":
		m.moveSetupSectionSelection(-1)
		return m, nil
	case "r":
		m.setupLoading = true
		m.status = "Refreshing AI backend checks..."
		return m, m.refreshSetupSnapshotCmd(false)
	case "ctrl+s":
		return m.saveSetupFromCurrentChoices()
	case "enter":
		return m.openSetupSectionDialog()
	}
	return m, nil
}

func (m *Model) moveSetupSectionSelection(delta int) {
	m.setupSectionSelected = wrapIndex(m.setupSectionSelected+delta, len(setupSectionMenuRows()))
	m.blurSettingsFields()
}

func (m Model) cycleModelTier(tier config.ModelTier) config.ModelTier {
	switch tier {
	case config.ModelTierFree:
		return config.ModelTierCheap
	case config.ModelTierCheap:
		return config.ModelTierBalanced
	default:
		return config.ModelTierFree
	}
}

func (m *Model) moveSetupRole(delta int) {
	if delta == 0 {
		return
	}
	m.setupConfigMode = false
	m.setupReviewMode = false
	m.setupConfigSelected = 0
	m.blurSettingsFields()
	count := int(setupRoleBossChat) + 1
	next := int(m.setupFocusedRole) + delta
	if next < 0 {
		next = count - 1
	}
	if next >= count {
		next = 0
	}
	m.setupFocusedRole = setupRole(next)
}

func (m *Model) moveSetupProvider(delta int) {
	if delta == 0 {
		return
	}
	m.setupConfigMode = false
	m.setupReviewMode = false
	m.setupConfigSelected = 0
	m.blurSettingsFields()
	if m.setupFocusedRole == setupRoleBossChat {
		m.setupBossSelected = wrapIndex(m.setupBossSelected+delta, len(m.setupFocusedProviderChoices()))
		return
	}
	m.setupSelected = wrapIndex(m.setupSelected+delta, len(m.setupFocusedProviderChoices()))
}

func wrapIndex(index, count int) int {
	if count <= 0 {
		return 0
	}
	for index < 0 {
		index += count
	}
	return index % count
}

func (m Model) setupAdvance() (tea.Model, tea.Cmd) {
	if m.setupSectionNavigation {
		return m.setupAdvanceSectionDialog()
	}
	switch m.setupStep {
	case setupStepProjectProvider:
		return m.enterSetupStep(m.nextSetupStepAfterProjectProvider(), "Project reports selected. Press Enter to continue.")
	case setupStepProjectConfig:
		return m.enterSetupStep(setupStepBossProvider, "Project reports details accepted. Choose the Help chat helper.")
	case setupStepBossProvider:
		return m.enterSetupStep(m.nextSetupStepAfterBossProvider(), "Help chat selected. Press Enter to continue.")
	case setupStepBossConfig:
		return m.enterSetupStep(setupStepLCAgentConfig, "Help chat details accepted. Configure LCAgent or keep the defaults.")
	case setupStepLCAgentConfig:
		return m.enterSetupStep(setupStepSave, "LCAgent details accepted. Press Enter to save setup.")
	case setupStepSave:
		return m.saveSetupFromCurrentChoices()
	default:
		return m.enterSetupStep(setupStepProjectProvider, "Setup wizard open. Press Enter to accept each page and continue.")
	}
}

func (m Model) setupGoBack() (tea.Model, tea.Cmd) {
	if m.setupSectionNavigation && !m.setupSectionMenu {
		return m.closeSetupSectionDialog("Back to setup sections.")
	}
	switch m.setupStep {
	case setupStepProjectProvider:
		m.closeSetupMode("Setup skipped for now. Run /setup anytime.")
		return m, nil
	case setupStepProjectConfig:
		return m.enterSetupStep(setupStepProjectProvider, "Back to project reports. Press Enter to accept the selected provider.")
	case setupStepBossProvider:
		return m.enterSetupStep(m.previousSetupStepBeforeBossProvider(), "Back to project reports. Press Enter to continue.")
	case setupStepBossConfig:
		return m.enterSetupStep(setupStepBossProvider, "Back to Help chat. Press Enter to accept the selected provider.")
	case setupStepLCAgentConfig:
		return m.enterSetupStep(m.previousSetupStepBeforeLCAgent(), "Back to Help chat. Press Enter to continue.")
	case setupStepSave:
		return m.enterSetupStep(m.previousSetupStepBeforeSave(), "Back to the previous setup page.")
	default:
		m.closeSetupMode("Setup skipped for now. Run /setup anytime.")
		return m, nil
	}
}

func (m Model) setupAdvanceSectionDialog() (tea.Model, tea.Cmd) {
	switch m.setupStep {
	case setupStepProjectProvider:
		if m.setupStepNeedsConfig(setupStepProjectConfig) {
			return m.enterSetupStep(setupStepProjectConfig, "Project reports details. Press Enter to return to setup sections.")
		}
		return m.closeSetupSectionDialog("Project reports setup updated in this draft. Open Save to write config.")
	case setupStepProjectConfig:
		return m.closeSetupSectionDialog("Project reports setup updated in this draft. Open Save to write config.")
	case setupStepBossProvider:
		if m.setupStepNeedsConfig(setupStepBossConfig) {
			return m.enterSetupStep(setupStepBossConfig, "Help chat details. Press Enter to return to setup sections.")
		}
		return m.closeSetupSectionDialog("Help chat setup updated in this draft. Open Save to write config.")
	case setupStepBossConfig:
		return m.closeSetupSectionDialog("Help chat setup updated in this draft. Open Save to write config.")
	case setupStepLCAgentConfig:
		return m.closeSetupSectionDialog("LCAgent setup updated in this draft. Open Save to write config.")
	case setupStepSave:
		return m.saveSetupFromCurrentChoices()
	default:
		return m.closeSetupSectionDialog("Back to setup sections.")
	}
}

func (m Model) openSetupSectionDialog() (tea.Model, tea.Cmd) {
	m.setupSectionSelected = wrapIndex(m.setupSectionSelected, len(setupSectionMenuRows()))
	m.setupSectionMenu = false
	switch setupSectionMenuRows()[m.setupSectionSelected].step {
	case setupStepProjectProvider:
		return m.enterSetupStep(setupStepProjectProvider, "Project reports setup. Choose a runner, then press Enter.")
	case setupStepBossProvider:
		return m.enterSetupStep(setupStepBossProvider, "Help chat setup. Choose a realtime backend, then press Enter.")
	case setupStepLCAgentConfig:
		return m.enterSetupStep(setupStepLCAgentConfig, "LCAgent setup. Press Enter to return to setup sections.")
	case setupStepSave:
		return m.enterSetupStep(setupStepSave, "Review setup. Enter saves, Esc returns to setup sections.")
	default:
		return m, nil
	}
}

func (m Model) closeSetupSectionDialog(status string) (tea.Model, tea.Cmd) {
	m.setupSectionMenu = true
	m.setupReviewMode = false
	m.setupConfigMode = false
	m.setupConfigSelected = 0
	m.blurSettingsFields()
	if status != "" {
		m.status = status
	}
	return m, nil
}

func (m Model) setupEnterConfigStep() (tea.Model, tea.Cmd) {
	switch m.setupFocusedRole {
	case setupRoleBossChat:
		if m.setupStep == setupStepBossProvider || m.setupStep == setupStepBossConfig {
			return m.enterSetupStep(setupStepBossConfig, "Help chat details. Press Enter to continue.")
		}
	case setupRoleLCAgent:
		if m.setupStep == setupStepLCAgentConfig {
			return m.enterSetupStep(setupStepLCAgentConfig, "LCAgent details. Press Enter to continue.")
		}
	default:
		if m.setupStep == setupStepProjectProvider || m.setupStep == setupStepProjectConfig {
			return m.enterSetupStep(setupStepProjectConfig, "Project reports details. Press Enter to continue.")
		}
	}
	m.status = "No setup details page for this step."
	return m, nil
}

func (m Model) nextSetupStepAfterProjectProvider() setupStep {
	if m.setupStepNeedsConfig(setupStepProjectConfig) {
		return setupStepProjectConfig
	}
	return setupStepBossProvider
}

func (m Model) nextSetupStepAfterBossProvider() setupStep {
	if m.setupStepNeedsConfig(setupStepBossConfig) {
		return setupStepBossConfig
	}
	return setupStepLCAgentConfig
}

func (m Model) previousSetupStepBeforeBossProvider() setupStep {
	if m.setupStepNeedsConfig(setupStepProjectConfig) {
		return setupStepProjectConfig
	}
	return setupStepProjectProvider
}

func (m Model) previousSetupStepBeforeSave() setupStep {
	return setupStepLCAgentConfig
}

func (m Model) previousSetupStepBeforeLCAgent() setupStep {
	if m.setupStepNeedsConfig(setupStepBossConfig) {
		return setupStepBossConfig
	}
	return setupStepBossProvider
}

func (m Model) setupStepNeedsConfig(step setupStep) bool {
	role := m.setupFocusedRole
	switch step {
	case setupStepProjectConfig:
		role = setupRoleProjectReports
	case setupStepBossConfig:
		role = setupRoleBossChat
	case setupStepLCAgentConfig:
		role = setupRoleLCAgent
	default:
		return false
	}
	check := m
	check.setupFocusedRole = role
	return len(check.setupConfigFieldIndexes()) > 0
}

func (m Model) enterSetupStep(step setupStep, status string) (tea.Model, tea.Cmd) {
	m.setupStep = step
	m.setupReviewMode = false
	m.setupConfigMode = false
	m.setupConfigSelected = 0
	m.localModelPickerVisible = false
	m.blurSettingsFields()
	switch step {
	case setupStepProjectProvider:
		m.setupFocusedRole = setupRoleProjectReports
		if status == "" {
			status = "Choose the project reports helper. Enter accepts the selected provider."
		}
	case setupStepProjectConfig:
		m.setupFocusedRole = setupRoleProjectReports
		m.setupConfigMode = true
		if status == "" {
			status = "Project reports details. Enter accepts these fields."
		}
		cmd := m.focusSetupConfigField()
		m.status = status
		return m, cmd
	case setupStepBossProvider:
		m.setupFocusedRole = setupRoleBossChat
		if status == "" {
			status = "Choose the Help chat helper. Enter accepts the selected provider."
		}
	case setupStepBossConfig:
		m.setupFocusedRole = setupRoleBossChat
		m.setupConfigMode = true
		if status == "" {
			status = "Help chat details. Enter accepts these fields."
		}
		cmd := m.focusSetupConfigField()
		m.status = status
		return m, cmd
	case setupStepLCAgentConfig:
		m.setupFocusedRole = setupRoleLCAgent
		m.setupConfigMode = true
		if status == "" {
			status = "LCAgent details. Enter accepts these fields."
		}
		cmd := m.focusSetupConfigField()
		m.status = status
		return m, cmd
	case setupStepSave:
		m.setupReviewMode = true
		if status == "" {
			status = "Save setup. Enter saves, Esc goes back."
		}
	default:
		m.setupStep = setupStepProjectProvider
		m.setupFocusedRole = setupRoleProjectReports
	}
	m.status = status
	return m, nil
}

func (m *Model) moveSetupConfigSelection(delta int) tea.Cmd {
	fields := m.setupConfigFieldIndexes()
	if len(fields) == 0 || delta == 0 {
		return nil
	}
	m.setupConfigSelected = wrapIndex(m.setupConfigSelected+delta, len(fields))
	return m.focusSetupConfigField()
}

func (m *Model) focusSetupConfigField() tea.Cmd {
	fields := m.setupConfigFieldIndexes()
	if len(fields) == 0 || len(m.settingsFields) == 0 {
		return nil
	}
	m.setupConfigSelected = wrapIndex(m.setupConfigSelected, len(fields))
	selectedField := fields[m.setupConfigSelected]
	cmds := make([]tea.Cmd, 0, 1)
	for i := range m.settingsFields {
		if i == selectedField {
			m.settingsFields[i].input.CursorEnd()
			cmds = append(cmds, m.settingsFields[i].input.Focus())
			continue
		}
		m.settingsFields[i].input.Blur()
	}
	return tea.Batch(cmds...)
}

func (m Model) setupSelectedConfigFieldIndex() int {
	fields := m.setupConfigFieldIndexes()
	if len(fields) == 0 {
		return -1
	}
	return fields[wrapIndex(m.setupConfigSelected, len(fields))]
}

func (m Model) setupConfigFieldIndexes() []int {
	if len(m.settingsFields) == 0 {
		return nil
	}
	if m.setupFocusedRole == setupRoleLCAgent {
		settings := m.setupDraftSettingsForProviderChoices()
		fields := []int{settingsFieldLCAgentRoutePreset}
		if strings.TrimSpace(settings.LCAgentRoutePreset) == "" {
			fields = append(fields,
				settingsFieldLCAgentModel,
				settingsFieldLCAgentReasoning,
			)
		}
		fields = append(fields,
			settingsFieldLCAgentUtilityProvider,
			settingsFieldLCAgentUtilityModel,
			settingsFieldLCAgentVisionProvider,
			settingsFieldLCAgentVisionModel,
		)
		fields = append(fields, settingsLCAgentConnectionFields(settings)...)
		fields = append(fields, settingsFieldLCAgentWebSearchBackend)
		fields = append(fields, settingsLCAgentWebSearchDetailFields(settings.LCAgentWebSearchBackend)...)
		if strings.TrimSpace(settings.LCAgentRoutePreset) == "" {
			fields = append(fields,
				settingsFieldLCAgentAuto,
				settingsFieldLCAgentToolProfile,
				settingsFieldLCAgentContextProfile,
			)
		}
		fields = append(fields, settingsFieldLCAgentRequestTimeout)
		return fields
	}
	if m.setupFocusedRole == setupRoleBossChat {
		switch m.setupSelectedBossBackend() {
		case config.AIBackendUnset, config.AIBackendOpenAIAPI:
			return []int{settingsFieldOpenAIAPIKey, settingsFieldBossChatModel, settingsFieldBossUtilityModel}
		case config.AIBackendOpenRouter:
			return []int{settingsFieldOpenRouterAPIKey, settingsFieldBossChatModel, settingsFieldBossUtilityModel}
		case config.AIBackendDeepSeek:
			return []int{settingsFieldDeepSeekAPIKey, settingsFieldBossChatModel, settingsFieldBossUtilityModel}
		case config.AIBackendMoonshot:
			return []int{settingsFieldMoonshotAPIKey, settingsFieldBossChatModel, settingsFieldBossUtilityModel}
		case config.AIBackendXiaomi:
			return []int{settingsFieldXiaomiBaseURL, settingsFieldXiaomiAPIKey, settingsFieldBossChatModel, settingsFieldBossUtilityModel}
		case config.AIBackendMLX:
			return []int{settingsFieldMLXBaseURL, settingsFieldMLXAPIKey, settingsFieldMLXModel}
		case config.AIBackendOllama:
			return []int{settingsFieldOllamaBaseURL, settingsFieldOllamaAPIKey, settingsFieldOllamaModel}
		default:
			return nil
		}
	}
	switch m.setupSelectedBackend() {
	case config.AIBackendOpenAIAPI:
		return []int{settingsFieldOpenAIAPIKey}
	case config.AIBackendOpenRouter:
		return []int{settingsFieldOpenRouterAPIKey, settingsFieldOpenRouterModel}
	case config.AIBackendDeepSeek:
		return []int{settingsFieldDeepSeekAPIKey, settingsFieldDeepSeekModel}
	case config.AIBackendMoonshot:
		return []int{settingsFieldMoonshotAPIKey, settingsFieldMoonshotModel}
	case config.AIBackendXiaomi:
		return []int{settingsFieldXiaomiBaseURL, settingsFieldXiaomiAPIKey, settingsFieldXiaomiModel}
	case config.AIBackendMLX:
		return []int{settingsFieldMLXBaseURL, settingsFieldMLXAPIKey, settingsFieldMLXModel}
	case config.AIBackendOllama:
		return []int{settingsFieldOllamaBaseURL, settingsFieldOllamaAPIKey, settingsFieldOllamaModel}
	default:
		return nil
	}
}

func (m Model) saveSetupFromCurrentChoices() (tea.Model, tea.Cmd) {
	settings := m.setupSettingsFromCurrentChoices()
	if issue, ok := settingsLCAgentKnownModelProviderIssue(settings); ok {
		m.status = issue.saveStatus()
		return m, nil
	}
	m.setupSaving = true
	m.setupConfigMode = false
	m.setupReviewMode = false
	m.blurSettingsFields()
	m.status = "Saving AI setup..."
	return m, m.saveSetupCmd(settings)
}

func (m Model) setupSettingsFromCurrentChoices() config.EditableSettings {
	settings := m.setupDraftSettingsForProviderChoices()
	settings.AIBackend = m.setupSelectedBackend()
	settings.BossChatBackend = m.setupSelectedBossBackend()
	settings.OpenCodeModelTier = string(m.setupModelTier)
	return settings
}

func (m Model) setupDraftSettingsForProviderChoices() config.EditableSettings {
	settings := m.currentSettingsBaseline()
	if len(m.settingsFields) == 0 {
		return settings
	}
	settings.OpenAIAPIKey = m.settingsFieldValue(settingsFieldOpenAIAPIKey)
	settings.OpenRouterAPIKey = m.settingsFieldValue(settingsFieldOpenRouterAPIKey)
	settings.OpenRouterModel = m.settingsFieldValue(settingsFieldOpenRouterModel)
	settings.DeepSeekAPIKey = m.settingsFieldValue(settingsFieldDeepSeekAPIKey)
	settings.DeepSeekModel = m.settingsFieldValue(settingsFieldDeepSeekModel)
	settings.MoonshotAPIKey = m.settingsFieldValue(settingsFieldMoonshotAPIKey)
	settings.MoonshotModel = m.settingsFieldValue(settingsFieldMoonshotModel)
	settings.XiaomiBaseURL = m.settingsFieldValue(settingsFieldXiaomiBaseURL)
	settings.XiaomiAPIKey = m.settingsFieldValue(settingsFieldXiaomiAPIKey)
	settings.XiaomiModel = m.settingsFieldValue(settingsFieldXiaomiModel)
	settings.ProjectReasoningEffort = m.settingsFieldValue(settingsFieldProjectReasoning)
	settings.BossHelmModel = m.settingsFieldValue(settingsFieldBossChatModel)
	settings.BossUtilityModel = m.settingsFieldValue(settingsFieldBossUtilityModel)
	settings.MLXBaseURL = m.settingsFieldValue(settingsFieldMLXBaseURL)
	settings.MLXAPIKey = m.settingsFieldValue(settingsFieldMLXAPIKey)
	settings.MLXModel = m.settingsFieldValue(settingsFieldMLXModel)
	settings.OllamaBaseURL = m.settingsFieldValue(settingsFieldOllamaBaseURL)
	settings.OllamaAPIKey = m.settingsFieldValue(settingsFieldOllamaAPIKey)
	settings.OllamaModel = m.settingsFieldValue(settingsFieldOllamaModel)
	settings.LCAgentPath = m.settingsFieldValue(settingsFieldLCAgentPath)
	settings.LCAgentEnvFile = m.settingsFieldValue(settingsFieldLCAgentEnvFile)
	settings.LCAgentRoutePreset = m.settingsFieldValue(settingsFieldLCAgentRoutePreset)
	settings.LCAgentProvider = m.settingsFieldValue(settingsFieldLCAgentProvider)
	settings.EmbeddedLCAgentModel = m.settingsFieldValue(settingsFieldLCAgentModel)
	settings.EmbeddedLCAgentReasoning = m.settingsFieldValue(settingsFieldLCAgentReasoning)
	settings.LCAgentAuto = m.settingsFieldValue(settingsFieldLCAgentAuto)
	settings.LCAgentToolProfile = m.settingsFieldValue(settingsFieldLCAgentToolProfile)
	settings.LCAgentContextProfile = m.settingsFieldValue(settingsFieldLCAgentContextProfile)
	settings.LCAgentUtilityProvider = m.settingsFieldValue(settingsFieldLCAgentUtilityProvider)
	settings.LCAgentUtilityModel = m.settingsFieldValue(settingsFieldLCAgentUtilityModel)
	settings.LCAgentVisionProvider = m.settingsFieldValue(settingsFieldLCAgentVisionProvider)
	settings.LCAgentVisionModel = m.settingsFieldValue(settingsFieldLCAgentVisionModel)
	settings.LCAgentWebSearchBackend = m.settingsFieldValue(settingsFieldLCAgentWebSearchBackend)
	settings.LCAgentWebSearchAPIKey = m.settingsFieldValue(settingsFieldLCAgentWebSearchAPIKey)
	settings.LCAgentWebSearchEngineID = m.settingsFieldValue(settingsFieldLCAgentWebSearchEngineID)
	settings.LCAgentWebSearchURL = m.settingsFieldValue(settingsFieldLCAgentWebSearchURL)
	if timeout, err := time.ParseDuration(m.settingsFieldValue(settingsFieldLCAgentRequestTimeout)); err == nil {
		settings.LCAgentRequestTimeout = timeout
	}
	return settings
}

func (m Model) recommendedSetupBackend() config.AIBackend {
	current := m.setupCurrentBackend()
	if current != config.AIBackendUnset && m.setupSnapshot.StatusFor(current).Ready {
		return current
	}
	for _, choice := range m.setupProjectProviderChoices() {
		if choice.Value == config.AIBackendDisabled {
			continue
		}
		if choice.State == "ready" {
			return choice.Value
		}
	}
	if current != config.AIBackendUnset {
		return current
	}
	return config.AIBackendCodex
}

func (m Model) recommendedSetupBossBackend() config.AIBackend {
	current := m.setupCurrentBossBackend()
	if current != config.AIBackendUnset {
		return current
	}
	return config.AIBackendUnset
}

func (m Model) setupCurrentBackend() config.AIBackend {
	if current := m.currentSettingsBaseline().AIBackend; current != config.AIBackendUnset {
		return current
	}
	return m.setupSnapshot.Selected
}

func (m Model) setupCurrentBossBackend() config.AIBackend {
	return m.currentSettingsBaseline().BossChatBackend
}

func (m Model) setupSelectedBackend() config.AIBackend {
	choices := m.setupProjectProviderChoices()
	if m.setupSelected < 0 || m.setupSelected >= len(choices) {
		return config.AIBackendCodex
	}
	return choices[m.setupSelected].Value
}

func (m Model) setupSelectedBossBackend() config.AIBackend {
	choices := m.setupBossProviderChoices()
	if m.setupBossSelected < 0 || m.setupBossSelected >= len(choices) {
		return config.AIBackendUnset
	}
	return choices[m.setupBossSelected].Value
}

func (m Model) setupSelectedLocalModelBackend() config.AIBackend {
	if m.setupFocusedRole == setupRoleBossChat {
		return m.setupSelectedBossBackend()
	}
	return m.setupSelectedBackend()
}

func (m Model) setupSelectionForBackend(backend config.AIBackend) int {
	return providerChoiceSelection(m.setupProjectProviderChoices(), backend)
}

func (m Model) setupBossSelectionForBackend(backend config.AIBackend) int {
	return providerChoiceSelection(m.setupBossProviderChoices(), backend)
}

func (m Model) renderSetupOverlay(body string, bodyW, bodyH int) string {
	panel := m.renderSetupPanel(bodyW, bodyH)
	panelWidth := lipgloss.Width(panel)
	panelHeight := lipgloss.Height(panel)
	left := max(0, (bodyW-panelWidth)/2)
	top := max(0, (bodyH-panelHeight)/4)
	return overlayBlock(body, panel, bodyW, bodyH, left, top)
}

func (m Model) renderSetupPanel(bodyW, bodyH int) string {
	panelWidth := min(bodyW, min(max(66, bodyW-10), 108))
	panelInnerWidth := max(28, panelWidth-4)
	maxContentHeight := max(12, bodyH-2)
	return renderDialogPanel(panelWidth, panelInnerWidth, m.renderSetupContent(panelInnerWidth, maxContentHeight))
}

func (m Model) renderSetupContent(width, maxHeight int) string {
	lines := []string{
		commandPaletteTitleStyle.Render("Setup Wizard"),
		m.renderSetupWizardProgress(width),
	}
	if m.setupLoading {
		lines = append(lines, "")
		lines = append(lines, commandPaletteHintStyle.Render("Checking local backend availability..."))
	} else if m.setupSaving {
		lines = append(lines, "")
		lines = append(lines, commandPaletteHintStyle.Render("Saving AI setup..."))
	}
	if m.setupSectionMenu {
		lines = append(lines, "")
		lines = append(lines, m.renderSetupSectionMenu(width, maxHeight)...)
		if actions := m.renderSetupActionLines(width); len(actions) > 0 {
			lines = append(lines, "")
			lines = append(lines, actions...)
		}
		return strings.Join(lines, "\n")
	}
	if m.setupReviewMode {
		lines = append(lines, "")
		lines = append(lines, m.renderSetupReview(width))
		if actions := m.renderSetupActionLines(width); len(actions) > 0 {
			lines = append(lines, "")
			lines = append(lines, actions...)
		}
		return strings.Join(lines, "\n")
	}
	if m.setupConfigMode {
		lines = append(lines, "")
		lines = append(lines, m.renderSetupConfigContent(width))
		return strings.Join(lines, "\n")
	}
	lines = append(lines, "")
	lines = append(lines, detailSectionStyle.Render(m.setupProviderListTitle()))
	lines = append(lines, renderWrappedDialogTextLines(commandPaletteHintStyle, width, m.setupRolePurpose())...)

	hintLines := []string{}
	if hint := m.renderSetupHint(width); hint != "" {
		hintLines = []string{"", hint}
	}
	actionLines := m.renderSetupActionLines(width)
	actionHeight := 0
	if len(actionLines) > 0 {
		actionHeight = renderedLinesHeight(actionLines) + 1
	}
	providerRows := m.renderSetupProviderRows(width)
	providerLimit := max(1, maxHeight-renderedLinesHeight(lines)-renderedLinesHeight(hintLines)-actionHeight-2)
	start, end := m.setupProviderWindow(len(providerRows), providerLimit)
	if start > 0 {
		lines = append(lines, commandPaletteHintStyle.Render(fmt.Sprintf("↑ %d above", start)))
	}
	for _, line := range providerRows[start:end] {
		lines = append(lines, line)
	}
	if end < len(providerRows) {
		lines = append(lines, commandPaletteHintStyle.Render(fmt.Sprintf("↓ %d below", len(providerRows)-end)))
	}
	if len(actionLines) > 0 {
		lines = append(lines, "")
		lines = append(lines, actionLines...)
	}
	if len(hintLines) > 0 {
		lines = append(lines, hintLines...)
	}
	return strings.Join(lines, "\n")
}

func (m Model) renderSetupSectionMenu(width, maxHeight int) []string {
	rows := setupSectionMenuRows()
	lines := []string{
		detailSectionStyle.Render("Sections"),
		commandPaletteHintStyle.Render(lipgloss.NewStyle().Width(max(18, width)).Render("Open one setup area at a time. Enter saves only from the Save section.")),
	}
	limit := max(1, maxHeight-12)
	start, end := settingsSectionMenuWindow(len(rows), wrapIndex(m.setupSectionSelected, len(rows)), limit)
	if start > 0 {
		lines = append(lines, commandPaletteHintStyle.Render(fmt.Sprintf("↑ %d above", start)))
	}
	for i := start; i < end; i++ {
		lines = append(lines, m.renderSetupSectionMenuRow(rows[i], i == wrapIndex(m.setupSectionSelected, len(rows)), width))
	}
	if end < len(rows) {
		lines = append(lines, commandPaletteHintStyle.Render(fmt.Sprintf("↓ %d below", len(rows)-end)))
	}
	if len(rows) > 0 {
		selected := rows[wrapIndex(m.setupSectionSelected, len(rows))]
		lines = append(lines, "")
		lines = append(lines, renderWrappedDetailField("About", detailValueStyle, width, selected.detail))
		if status := strings.TrimSpace(m.setupSectionMenuStatus(selected.step)); status != "" {
			lines = append(lines, renderWrappedDetailField("Current", detailValueStyle, width, status))
		}
	}
	return lines
}

func (m Model) renderSetupSectionMenuRow(row setupSectionMenuRow, selected bool, width int) string {
	titleStyle := detailValueStyle.Bold(true)
	summaryStyle := detailMutedStyle
	marker := " "
	if selected {
		titleStyle = lipgloss.NewStyle().Foreground(lipgloss.Color("230")).Bold(true)
		summaryStyle = lipgloss.NewStyle().Foreground(lipgloss.Color("230"))
		marker = ">"
	}
	titleWidth := min(22, max(12, width/3))
	summaryWidth := max(8, width-titleWidth-4)
	summary := row.summary
	line := marker + " " +
		titleStyle.Width(titleWidth).Render(truncateText(row.label, titleWidth)) + " " +
		summaryStyle.Width(summaryWidth).Render(truncateText(summary, summaryWidth))
	line = fitFooterWidth(line, width)
	if selected {
		return dialogSelectedRowStyle.Width(width).Render(line)
	}
	return lipgloss.NewStyle().Width(width).Render(line)
}

func (m Model) setupSectionMenuStatus(step setupStep) string {
	settings := m.setupSettingsFromCurrentChoices()
	switch step {
	case setupStepProjectProvider:
		choice := m.selectedSettingsProviderChoice(providerChoiceRoleProjectReports, settings.AIBackend, settings)
		return m.setupReviewChoiceSummary(choice)
	case setupStepBossProvider:
		choice := m.selectedSettingsProviderChoice(providerChoiceRoleBossChat, settings.BossChatBackend, settings)
		return m.setupReviewChoiceSummary(choice)
	case setupStepLCAgentConfig:
		return m.setupReviewLCAgentSummary(settings)
	case setupStepSave:
		return "Review and write config.toml"
	default:
		return ""
	}
}

func (m Model) renderSetupWizardProgress(width int) string {
	steps := []struct {
		step  setupStep
		label string
	}{
		{setupStepProjectProvider, "Project reports"},
		{setupStepProjectConfig, "Project details"},
		{setupStepBossProvider, "Help chat"},
		{setupStepBossConfig, "Help Chat details"},
		{setupStepLCAgentConfig, "LCAgent"},
		{setupStepSave, "Save"},
	}
	parts := make([]string, 0, len(steps)*2)
	for i, step := range steps {
		label := step.label
		if !m.setupStepIsVisible(step.step) {
			label = commandPaletteHintStyle.Render(label)
		} else if step.step == m.setupStep {
			label = detailSectionStyle.Render(label)
		} else {
			label = detailValueStyle.Render(label)
		}
		if i > 0 {
			parts = append(parts, commandPaletteHintStyle.Render(">"))
		}
		parts = append(parts, label)
	}
	line := strings.Join(parts, " ")
	if lipgloss.Width(line) <= width {
		return line
	}
	return commandPaletteHintStyle.Render(m.setupStepTitle())
}

func (m Model) setupStepIsVisible(step setupStep) bool {
	switch step {
	case setupStepProjectConfig, setupStepBossConfig, setupStepLCAgentConfig:
		return m.setupStepNeedsConfig(step)
	default:
		return true
	}
}

func (m Model) setupStepTitle() string {
	switch m.setupStep {
	case setupStepProjectProvider:
		return "Project reports"
	case setupStepProjectConfig:
		return "Project details"
	case setupStepBossProvider:
		return "Help chat"
	case setupStepBossConfig:
		return "Help Chat details"
	case setupStepLCAgentConfig:
		return "LCAgent"
	case setupStepSave:
		return "Save"
	default:
		return "Setup"
	}
}

func (m Model) renderSetupReview(width int) string {
	settings := m.setupSettingsFromCurrentChoices()
	projectChoice := m.selectedSettingsProviderChoice(providerChoiceRoleProjectReports, settings.AIBackend, settings)
	bossChoice := m.selectedSettingsProviderChoice(providerChoiceRoleBossChat, settings.BossChatBackend, settings)
	lines := []string{
		detailSectionStyle.Render("Save Setup"),
		renderWrappedDetailField("Project reports", detailValueStyle, width, m.setupReviewChoiceSummary(projectChoice)),
		renderWrappedDetailField("Help chat", detailValueStyle, width, m.setupReviewChoiceSummary(bossChoice)),
		renderWrappedDetailField("LCAgent", detailValueStyle, width, m.setupReviewLCAgentSummary(settings)),
	}
	if strings.TrimSpace(settings.OpenAIAPIKey) != "" {
		keyText := "saved"
		if m.settingsSensitiveAPIKeyEdited(settingsFieldOpenAIAPIKey) {
			keyText = "entered and hidden until saved"
		} else if suffix := m.settingsSensitiveAPIKeyStableSuffix(settingsFieldOpenAIAPIKey); suffix != "" {
			keyText += " " + suffix
		}
		lines = append(lines, renderWrappedDetailField("OpenAI key", detailValueStyle, width, keyText))
	}
	if settings.AIBackend == config.AIBackendOpenCode {
		lines = append(lines, renderWrappedDetailField("OpenCode tier", detailValueStyle, width, string(m.setupModelTier)))
	}
	lines = append(lines, renderWrappedDetailField("Next", detailValueStyle, width, "Enter saves setup. Esc goes back to the previous page."))
	return strings.Join(lines, "\n")
}

func (m Model) setupReviewLCAgentSummary(settings config.EditableSettings) string {
	if preset := strings.TrimSpace(settings.LCAgentRoutePreset); preset != "" {
		state, _, detail := lcagentCredentialSmokeCheck(settings)
		parts := []string{settingsChoiceOptionLabelForField(settingsFieldLCAgentRoutePreset, preset), "(" + state + ")"}
		if detail != "" && state != "ready" {
			parts = append(parts, "- "+detail)
		}
		return strings.Join(parts, " ")
	}
	provider := firstNonEmptyTrimmed(settings.LCAgentProvider, "openrouter")
	model := firstNonEmptyTrimmed(settings.EmbeddedLCAgentModel, lcagentDefaultModelForProvider(provider))
	auto := firstNonEmptyTrimmed(settings.LCAgentAuto, "low")
	toolProfile := firstNonEmptyTrimmed(settings.LCAgentToolProfile, "balanced")
	contextProfile := firstNonEmptyTrimmed(settings.LCAgentContextProfile, "balanced")
	state, _, detail := lcagentCredentialSmokeCheck(settings)
	parts := []string{provider, model, "auto " + auto, toolProfile + "/" + contextProfile, "(" + state + ")"}
	if detail != "" && state != "ready" {
		parts = append(parts, "- "+detail)
	}
	return strings.Join(parts, " ")
}

func (m Model) setupReviewChoiceSummary(choice providerChoice) string {
	parts := []string{firstNonEmptyTrimmed(choice.Label, string(choice.Value))}
	if state := strings.TrimSpace(choice.State); state != "" {
		parts = append(parts, "("+state+")")
	}
	if next := strings.TrimSpace(choice.NextStep); next != "" && choice.State != "ready" && choice.State != "off" {
		parts = append(parts, "- "+next)
	}
	return strings.Join(parts, " ")
}

func (m Model) setupProviderWindow(total, limit int) (int, int) {
	if total == 0 || limit <= 0 || total <= limit {
		return 0, total
	}
	selected := m.setupSelected
	if m.setupFocusedRole == setupRoleBossChat {
		selected = m.setupBossSelected
	}
	if selected < 0 {
		selected = 0
	}
	if selected >= total {
		selected = total - 1
	}
	start := selected - limit/2
	if start < 0 {
		start = 0
	}
	maxStart := total - limit
	if start > maxStart {
		start = maxStart
	}
	return start, start + limit
}

func renderedLinesHeight(lines []string) int {
	height := 0
	for _, line := range lines {
		height += max(1, lipgloss.Height(line))
	}
	return height
}

func (m Model) renderSetupRoleCards(width int) string {
	settings := m.setupSettingsFromCurrentChoices()
	projectCard := m.projectReportsStatusCard(settings)
	projectCard.Selected = m.setupFocusedRole == setupRoleProjectReports
	projectCard.PulseFrame = m.spinnerFrame
	bossCard := m.bossChatStatusCard(settings)
	bossCard.Selected = m.setupFocusedRole == setupRoleBossChat
	bossCard.PulseFrame = m.spinnerFrame
	if width < 56 {
		return lipgloss.JoinVertical(
			lipgloss.Left,
			renderInferenceStatusCard(projectCard, width),
			renderInferenceStatusCard(bossCard, width),
		)
	}
	gap := "  "
	cardWidth := max(26, (width-lipgloss.Width(gap))/2)
	return lipgloss.JoinHorizontal(
		lipgloss.Top,
		renderInferenceStatusCard(projectCard, cardWidth),
		gap,
		renderInferenceStatusCard(bossCard, cardWidth),
	)
}

func (m Model) setupProviderListTitle() string {
	return providerChoiceRoleListTitle(m.setupFocusedProviderChoiceRole())
}

func (m Model) setupRolePurpose() string {
	return providerChoiceRolePurpose(m.setupFocusedProviderChoiceRole())
}

func (m Model) setupProjectProviderChoices() []providerChoice {
	return m.providerChoices(providerChoiceRoleProjectReports, m.setupDraftSettingsForProviderChoices())
}

func (m Model) setupBossProviderChoices() []providerChoice {
	return m.providerChoices(providerChoiceRoleBossChat, m.setupDraftSettingsForProviderChoices())
}

func (m Model) setupFocusedProviderChoiceRole() providerChoiceRole {
	if m.setupFocusedRole == setupRoleBossChat {
		return providerChoiceRoleBossChat
	}
	return providerChoiceRoleProjectReports
}

func (m Model) setupFocusedProviderChoices() []providerChoice {
	if m.setupFocusedRole == setupRoleBossChat {
		return m.setupBossProviderChoices()
	}
	return m.setupProjectProviderChoices()
}

func (m Model) setupFocusedCurrentBackend() config.AIBackend {
	if m.setupFocusedRole == setupRoleBossChat {
		return m.setupCurrentBossBackend()
	}
	return m.setupCurrentBackend()
}

func (m Model) setupFocusedProviderSelection() int {
	if m.setupFocusedRole == setupRoleBossChat {
		return m.setupBossSelected
	}
	return m.setupSelected
}

func (m Model) setupFocusedProviderChoice() providerChoice {
	choices := m.setupFocusedProviderChoices()
	if len(choices) == 0 {
		return providerChoice{}
	}
	return choices[wrapIndex(m.setupFocusedProviderSelection(), len(choices))]
}

func (m Model) renderSetupProviderRows(width int) []string {
	choices := m.setupFocusedProviderChoices()
	current := m.setupFocusedCurrentBackend()
	selected := m.setupFocusedProviderSelection()
	lines := make([]string, 0, len(choices))
	for i, choice := range choices {
		lines = append(lines, renderProviderChoiceRow(choice, i == selected, choice.Value == current, width))
	}
	return lines
}

func (m Model) renderSetupConfigContent(width int) string {
	if len(m.settingsFields) == 0 {
		return detailWarningStyle.Render("Settings fields are not loaded yet.")
	}
	fields := m.setupConfigFieldIndexes()
	title := m.setupConfigTitle()
	lines := []string{
		detailSectionStyle.Render("Configure " + title),
		commandPaletteHintStyle.Render("Edit only what this branch needs, then press Enter to continue."),
	}
	if warning := settingsXiaomiTokenPlanBaseURLWarning(m.setupDraftSettingsForProviderChoices()); warning != "" {
		lines = append(lines, renderWrappedDetailField("Warning", detailWarningStyle, width, warning))
	}
	if issue, ok := settingsLCAgentKnownModelProviderIssue(m.setupDraftSettingsForProviderChoices()); ok {
		lines = append(lines, renderWrappedDetailField("Warning", detailWarningStyle, width, issue.message()))
	}
	if m.setupFocusedRole == setupRoleLCAgent {
		lines = append(lines, renderWrappedDetailField("Credential smoke", detailValueStyle, width, m.lcagentSetupSmokeLine()))
	}
	if len(fields) == 0 {
		lines = append(lines, commandPaletteHintStyle.Render(m.setupNoConfigText()))
		lines = append(lines, "")
		lines = append(lines, m.renderSetupActionLines(width)...)
		return strings.Join(lines, "\n")
	}
	labelWidth := m.settingsLabelWidth(width, fields)
	inputWidth := max(10, width-labelWidth-1)
	for position, fieldIndex := range fields {
		selected := m.setupConfigMode && position == wrapIndex(m.setupConfigSelected, len(fields))
		lines = append(lines, m.renderSettingsFieldRow(fieldIndex, m.settingsFields[fieldIndex], selected, labelWidth, inputWidth))
	}
	if hint := strings.TrimSpace(m.settingsFieldHint(m.setupSelectedConfigFieldIndex())); hint != "" {
		lines = append(lines, "")
		lines = append(lines, commandPaletteHintStyle.Render(lipgloss.NewStyle().Width(width).Render("Hint: "+hint)))
	}
	if actions := m.renderSetupActionLines(width); len(actions) > 0 {
		lines = append(lines, "")
		lines = append(lines, actions...)
	}
	return strings.Join(lines, "\n")
}

func (m Model) setupConfigTitle() string {
	if m.setupFocusedRole == setupRoleLCAgent {
		return "LCAgent"
	}
	return providerChoiceRoleTitle(m.setupFocusedProviderChoiceRole())
}

func (m Model) lcagentSetupSmokeLine() string {
	settings := m.setupDraftSettingsForProviderChoices()
	return m.renderLCAgentCredentialSmokeLine(settings)
}

func (m Model) setupNoConfigText() string {
	choice := m.setupFocusedProviderChoice()
	if m.setupFocusedRole == setupRoleProjectReports && m.setupSelectedBackend() == config.AIBackendOpenCode {
		return "OpenCode uses your OpenCode login. Press t to change the model tier."
	}
	if strings.TrimSpace(choice.Description) != "" {
		return choice.Description
	}
	return "No extra fields for this provider."
}

func (m Model) renderSetupHint(width int) string {
	if m.setupFocusedRole == setupRoleBossChat {
		return m.renderBossChatSetupHint(width)
	}
	selectedStatus := m.setupSnapshot.StatusFor(m.setupSelectedBackend())
	choice := m.setupSelectedProjectProviderChoice()
	hint := strings.TrimSpace(choice.NextStep)
	if hint == "" {
		hint = selectedStatus.Detail
	}
	if m.setupSelectedBackend() == config.AIBackendDisabled {
		hint = "Disable AI features completely. Little Control Room keeps working, but summaries, classifications, and commit help stay off until you run /setup again."
	}
	if m.setupSelectedBackend().SupportsModelTier() && selectedStatus.Ready {
		hint = "OpenCode will use " + string(m.setupModelTier) + " tier models for summaries. Press t to cycle: free → cheap → balanced."
	}
	if m.setupSelectedBackend() == config.AIBackendClaude && selectedStatus.Ready {
		hint = "Claude Code will default to the Haiku alias for these background tasks to keep usage lighter."
	}
	if m.setupSelectedBackend() == config.AIBackendMLX {
		hint = m.localBackendSetupHint(config.AIBackendMLX, selectedStatus)
	}
	if m.setupSelectedBackend() == config.AIBackendOllama {
		hint = m.localBackendSetupHint(config.AIBackendOllama, selectedStatus)
	}
	if selectedStatus.LoginHint != "" && !selectedStatus.Ready {
		hint = selectedStatus.LoginHint
	}
	return m.renderSetupChoiceHint(choice, hint, width)
}

func (m Model) renderBossChatSetupHint(width int) string {
	settings := m.setupDraftSettingsForProviderChoices()
	selected := m.setupSelectedBossBackend()
	choice := m.setupSelectedBossProviderChoice()
	hint := strings.TrimSpace(choice.NextStep)
	if hint == "" {
		hint = "Help chat is the direct high-level conversation in /help."
	}
	switch selected {
	case config.AIBackendUnset:
		if strings.TrimSpace(settings.OpenAIAPIKey) == "" {
			hint = "Auto leaves Help chat unconfigured for now. Choose a listed API backend, a local endpoint, or Off when you want a specific path."
		} else {
			hint = "Auto will use the shared OpenAI API connection for Help chat."
		}
	case config.AIBackendDisabled:
		hint = "Turn Help chat off. Project reports and embedded sessions can still use their own backends."
	case config.AIBackendOpenAIAPI:
		if strings.TrimSpace(settings.OpenAIAPIKey) == "" {
			hint = "Help chat uses direct OpenAI API inference. Press Enter to add the API key here."
		} else if settings.AIBackend == config.AIBackendOpenAIAPI {
			hint = "Help chat will use the shared OpenAI API connection. Project reports are also using OpenAI API."
		} else {
			hint = "Help chat will use the shared OpenAI API connection. Project reports stay on " + settings.AIBackend.Label() + "."
		}
	case config.AIBackendOpenRouter, config.AIBackendDeepSeek, config.AIBackendMoonshot, config.AIBackendXiaomi:
		if !cloudBackendAPIKeySaved(settings, selected) {
			hint = "Help chat uses direct " + selected.Label() + " API inference. Press Enter to add the API key here."
		} else if settings.AIBackend == selected {
			hint = "Help chat will use the shared " + selected.Label() + " API connection. Project reports are also using " + selected.Label() + "."
		} else {
			hint = "Help chat will use the shared " + selected.Label() + " API connection. Project reports stay on " + settings.AIBackend.Label() + "."
		}
	case config.AIBackendMLX:
		hint = "Help chat will use your MLX OpenAI-compatible endpoint. Press Enter to select and configure it, or m to pick a discovered model."
	case config.AIBackendOllama:
		hint = "Help chat will use your Ollama OpenAI-compatible endpoint. Press Enter to select and configure it, or m to pick a discovered model."
	}
	return m.renderSetupChoiceHint(choice, hint, width)
}

func (m Model) setupSelectedProjectProviderChoice() providerChoice {
	choices := m.setupProjectProviderChoices()
	if len(choices) == 0 {
		return providerChoice{}
	}
	return choices[wrapIndex(m.setupSelected, len(choices))]
}

func (m Model) setupSelectedBossProviderChoice() providerChoice {
	choices := m.setupBossProviderChoices()
	if len(choices) == 0 {
		return providerChoice{}
	}
	return choices[wrapIndex(m.setupBossSelected, len(choices))]
}

func (m Model) renderSetupChoiceHint(choice providerChoice, next string, width int) string {
	lines := []string{}
	if summary := strings.TrimSpace(choice.Summary); summary != "" {
		lines = append(lines, renderWrappedDetailField("Why", detailValueStyle, width, summary))
	}
	if choice.State != "" {
		lines = append(lines, renderWrappedDetailField("Status", detailValueStyle, width, renderProviderChoiceStatus(choice)))
	}
	if strings.TrimSpace(next) != "" {
		lines = append(lines, renderWrappedDetailField("Next", detailValueStyle, width, next))
	}
	return strings.Join(lines, "\n")
}

func (m Model) renderSetupActionLines(width int) []string {
	segments := m.setupActionSegments()
	if len(segments) == 0 {
		return nil
	}
	separator := dialogPanelFillStyle.Render("   ")
	lines := make([]string, 0, 2)
	current := ""
	for _, segment := range segments {
		if strings.TrimSpace(segment) == "" {
			continue
		}
		if current == "" {
			current = segment
			continue
		}
		candidate := current + separator + segment
		if lipgloss.Width(candidate) <= width {
			current = candidate
			continue
		}
		lines = append(lines, current)
		current = segment
	}
	if current != "" {
		lines = append(lines, current)
	}
	return lines
}

func (m Model) setupActionSegments() []string {
	if m.setupSectionMenu {
		return []string{
			renderDialogAction("Enter", "open", commitActionKeyStyle, commitActionTextStyle),
			renderDialogAction("ctrl+s", "save", commitActionKeyStyle, commitActionTextStyle),
			renderDialogAction("Up/Down", "section", navigateActionKeyStyle, navigateActionTextStyle),
			renderDialogAction("r", "refresh", navigateActionKeyStyle, navigateActionTextStyle),
			renderDialogAction("Esc", "close", cancelActionKeyStyle, cancelActionTextStyle),
		}
	}
	if m.setupReviewMode {
		return []string{
			renderDialogAction("Enter", "save", commitActionKeyStyle, commitActionTextStyle),
			renderDialogAction("ctrl+s", "save", commitActionKeyStyle, commitActionTextStyle),
			renderDialogAction("Esc", "back", cancelActionKeyStyle, cancelActionTextStyle),
		}
	}
	if m.setupConfigMode {
		if settingsFieldUsesPicker(m.setupSelectedConfigFieldIndex()) ||
			settingsFieldUsesLocalBackendModelPicker(m.setupSelectedConfigFieldIndex()) ||
			m.settingsFieldUsesUnifiedCloudModelPicker(m.setupSelectedConfigFieldIndex()) {
			ctrlSLabel := "continue"
			if m.setupSectionNavigation {
				ctrlSLabel = "save"
			}
			actions := []string{
				renderDialogAction("Enter", "choose", commitActionKeyStyle, commitActionTextStyle),
				renderDialogAction("ctrl+s", ctrlSLabel, commitActionKeyStyle, commitActionTextStyle),
				renderDialogAction("Tab", "field", navigateActionKeyStyle, navigateActionTextStyle),
				renderDialogAction("Up/Down", "field", navigateActionKeyStyle, navigateActionTextStyle),
				renderDialogAction("Esc", "back", cancelActionKeyStyle, cancelActionTextStyle),
			}
			if settingsFieldCanCheckLCAgentVision(m.setupSelectedConfigFieldIndex()) {
				actions = append(actions[:1], append([]string{
					renderDialogAction("v", "check", navigateActionKeyStyle, navigateActionTextStyle),
				}, actions[1:]...)...)
			}
			return actions
		}
		actions := []string{
			renderDialogAction("Enter", "continue", commitActionKeyStyle, commitActionTextStyle),
			renderDialogAction("Tab", "field", navigateActionKeyStyle, navigateActionTextStyle),
			renderDialogAction("Up/Down", "field", navigateActionKeyStyle, navigateActionTextStyle),
			renderDialogAction("Esc", "back", cancelActionKeyStyle, cancelActionTextStyle),
		}
		if m.setupSectionNavigation {
			actions = append([]string{renderDialogAction("ctrl+s", "save", commitActionKeyStyle, commitActionTextStyle)}, actions...)
		}
		return actions
	}
	actions := []string{
		renderDialogAction("Enter", "next", commitActionKeyStyle, commitActionTextStyle),
		renderDialogAction("Up/Down", "provider", navigateActionKeyStyle, navigateActionTextStyle),
		renderDialogAction("r", "refresh", navigateActionKeyStyle, navigateActionTextStyle),
		renderDialogAction("Esc", m.setupBackActionLabel(), cancelActionKeyStyle, cancelActionTextStyle),
	}
	if m.setupSectionNavigation {
		actions = append([]string{renderDialogAction("ctrl+s", "save", commitActionKeyStyle, commitActionTextStyle)}, actions...)
	}
	if isLocalBackendModelPickerBackend(m.setupSelectedLocalModelBackend()) {
		actions = append(actions[:2], append([]string{
			renderDialogAction("m", "model", navigateActionKeyStyle, navigateActionTextStyle),
		}, actions[2:]...)...)
	}
	if m.setupFocusedRole == setupRoleProjectReports && m.setupSelectedBackend().SupportsModelTier() {
		actions = append(actions[:3], append([]string{
			renderDialogAction("t", "tier", navigateActionKeyStyle, navigateActionTextStyle),
		}, actions[3:]...)...)
	}
	return actions
}

func (m Model) setupBackActionLabel() string {
	if m.setupSectionNavigation {
		return "sections"
	}
	if m.setupStep == setupStepProjectProvider {
		return "close"
	}
	return "back"
}

func (m Model) localBackendSetupHint(backend config.AIBackend, status aibackend.Status) string {
	endpoint := strings.TrimSpace(status.Endpoint)
	if endpoint == "" {
		endpoint = config.Default().OpenAICompatibleBaseURL(backend)
	}
	models := localBackendPickerModels(status.Models)
	selectedModel := strings.TrimSpace(m.currentSettingsBaseline().OpenAICompatibleModel(backend))
	if selectedModel != "" && localBackendModelExists(selectedModel, models) {
		return fmt.Sprintf("%s will use %s for background AI tasks. Press m to pick another discovered model; Enter continues to endpoint and manual settings.%s", backend.Label(), selectedModel, localBackendEnvOverrideNotice())
	}
	if len(models) == 0 {
		return fmt.Sprintf("%s uses an OpenAI-compatible local server at %s. Press r after the server is running, then m to pick a model.", backend.Label(), endpoint)
	}
	return fmt.Sprintf("%s will auto-use %s from %s. Press m to pin a discovered model; Enter continues to endpoint and manual settings.%s", backend.Label(), firstString(models), endpoint, localBackendEnvOverrideNotice())
}

func summarizeLocalBackendModels(models []string) string {
	if len(models) == 0 {
		return "no models"
	}
	if len(models) == 1 {
		return models[0]
	}
	return fmt.Sprintf("%s +%d more", models[0], len(models)-1)
}

func localBackendModelExists(target string, models []string) bool {
	target = strings.TrimSpace(target)
	for _, model := range models {
		if strings.EqualFold(strings.TrimSpace(model), target) {
			return true
		}
	}
	return false
}

func localBackendEnvOverrideNotice() string {
	var names []string
	if strings.TrimSpace(os.Getenv(brand.SessionClassifierModelEnvVar)) != "" {
		names = append(names, brand.SessionClassifierModelEnvVar)
	}
	if strings.TrimSpace(os.Getenv(brand.CommitModelEnvVar)) != "" {
		names = append(names, brand.CommitModelEnvVar)
	}
	if len(names) == 0 {
		return ""
	}
	return " Env overrides are set via " + strings.Join(names, " and ") + "."
}
