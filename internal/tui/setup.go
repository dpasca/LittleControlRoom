package tui

import (
	"fmt"
	"os"
	"strings"

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
)

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
	m.localModelPickerVisible = false
	m.setupFocusedRole = setupRoleProjectReports
	m.setupConfigMode = false
	m.setupConfigSelected = 0
	m.setupSelected = m.setupSelectionForBackend(m.recommendedSetupBackend())
	m.setupBossSelected = m.setupBossSelectionForBackend(m.recommendedSetupBossBackend())
	tier, _ := config.ParseModelTier(m.currentSettingsBaseline().OpenCodeModelTier)
	m.setupModelTier = tier
	m.setupLoading = true
	m.status = "Setup concierge open. Pick who should handle project reports and boss chat."
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
	m.setupConfigMode = false
	m.setupConfigSelected = 0
	m.localModelPickerVisible = false
	m.blurSettingsFields()
	if status != "" {
		m.status = status
	}
}

func (m Model) refreshSetupSnapshotCmd(openOnStartup bool) tea.Cmd {
	settings := m.currentSettingsBaseline()
	return func() tea.Msg {
		cfg := config.Default()
		cfg.AIBackend = settings.AIBackend
		cfg.OpenAIAPIKey = settings.OpenAIAPIKey
		cfg.MLXBaseURL = settings.MLXBaseURL
		cfg.MLXAPIKey = settings.MLXAPIKey
		cfg.MLXModel = settings.MLXModel
		cfg.OllamaBaseURL = settings.OllamaBaseURL
		cfg.OllamaAPIKey = settings.OllamaAPIKey
		cfg.OllamaModel = settings.OllamaModel
		return setupSnapshotMsg{
			snapshot:      aibackend.Detect(m.ctx, cfg),
			openOnStartup: openOnStartup,
		}
	}
}

func (m Model) saveSetupCmd(settings config.EditableSettings) tea.Cmd {
	path := m.currentConfigPath()
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
	switch msg.String() {
	case "esc":
		m.closeSetupMode("Setup skipped for now. Run /setup anytime.")
		return m, nil
	case "tab", "right", "]":
		m.moveSetupRole(1)
		return m, nil
	case "shift+tab", "left", "[":
		m.moveSetupRole(-1)
		return m, nil
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
	case "t":
		if m.setupFocusedRole == setupRoleProjectReports && m.setupSelectedBackend().SupportsModelTier() {
			m.setupModelTier = m.cycleModelTier(m.setupModelTier)
			return m, nil
		}
	case "e":
		// Keep the old shortcut working quietly while Enter becomes the guided path.
		return m.enterSetupConfigMode()
	case "m":
		if isLocalBackendModelPickerBackend(m.setupSelectedLocalModelBackend()) {
			return m.openLocalBackendModelPicker()
		}
	case "enter":
		return m.activateSetupSelection()
	}
	return m, nil
}

func (m Model) updateSetupConfigMode(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	switch msg.String() {
	case "esc":
		m.setupConfigMode = false
		m.blurSettingsFields()
		m.status = "Setup fields closed. Press Enter to select this provider."
		return m, nil
	case "tab", "down", "ctrl+n":
		cmd := m.moveSetupConfigSelection(1)
		return m, cmd
	case "shift+tab", "up", "ctrl+p":
		cmd := m.moveSetupConfigSelection(-1)
		return m, cmd
	case "ctrl+s", "enter":
		return m.enterSetupReviewMode("Save setup selected. Press Enter to save, or Esc to go back.")
	}

	fieldIndex := m.setupSelectedConfigFieldIndex()
	if fieldIndex < 0 || fieldIndex >= len(m.settingsFields) {
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
		m.setupReviewMode = false
		m.status = "Save step closed. Use the provider tree, then press Enter to select."
		return m, nil
	case "tab", "right", "]":
		m.setupReviewMode = false
		m.moveSetupRole(1)
		return m, nil
	case "shift+tab", "left", "[":
		m.setupReviewMode = false
		m.moveSetupRole(-1)
		return m, nil
	case "down", "j", "ctrl+n":
		m.setupReviewMode = false
		m.moveSetupProvider(1)
		return m, nil
	case "up", "k", "ctrl+p":
		m.setupReviewMode = false
		m.moveSetupProvider(-1)
		return m, nil
	case "e":
		m.setupReviewMode = false
		return m.enterSetupConfigMode()
	case "r":
		m.setupReviewMode = false
		m.setupLoading = true
		m.status = "Refreshing AI backend checks..."
		return m, m.refreshSetupSnapshotCmd(false)
	}
	return m, nil
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

func (m Model) enterSetupConfigMode() (tea.Model, tea.Cmd) {
	fields := m.setupConfigFieldIndexes()
	if len(fields) == 0 {
		m.status = "No extra configuration fields for this setup choice."
		return m, nil
	}
	m.setupConfigMode = true
	m.setupReviewMode = false
	if m.setupConfigSelected < 0 || m.setupConfigSelected >= len(fields) {
		m.setupConfigSelected = 0
	}
	m.status = "Configure this branch, then press Enter to continue."
	cmd := m.focusSetupConfigField()
	return m, cmd
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
	if m.setupFocusedRole == setupRoleBossChat {
		switch m.setupSelectedBossBackend() {
		case config.AIBackendUnset, config.AIBackendOpenAIAPI:
			return []int{settingsFieldOpenAIAPIKey, settingsFieldBossChatModel, settingsFieldBossUtilityModel}
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
	case config.AIBackendMLX:
		return []int{settingsFieldMLXBaseURL, settingsFieldMLXAPIKey, settingsFieldMLXModel}
	case config.AIBackendOllama:
		return []int{settingsFieldOllamaBaseURL, settingsFieldOllamaAPIKey, settingsFieldOllamaModel}
	default:
		return nil
	}
}

func (m Model) activateSetupSelection() (tea.Model, tea.Cmd) {
	if m.setupFocusedRole == setupRoleBossChat {
		return m.activateBossChatSetupSelection()
	}
	settings := m.setupSettingsFromCurrentChoices()
	selectedStatus := m.setupSnapshot.StatusFor(settings.AIBackend)
	switch settings.AIBackend {
	case config.AIBackendDisabled:
		return m.enterSetupReviewMode("Project reports: Off selected. Press Enter to save, or Esc to go back.")
	case config.AIBackendOpenAIAPI:
		if strings.TrimSpace(settings.OpenAIAPIKey) == "" {
			return m.enterSetupConfigModeWithStatus("Paste an OpenAI API key here, then press Enter to continue.")
		}
		return m.enterSetupConfigModeWithStatus("OpenAI API selected. Press Enter to continue, or update the saved key first.")
	case config.AIBackendMLX, config.AIBackendOllama:
		if strings.TrimSpace(selectedStatus.Detail) == "" || !selectedStatus.Ready {
			if selectedStatus.LoginHint != "" {
				m.status = selectedStatus.LoginHint
			} else {
				m.status = selectedStatus.Detail
			}
			return m.enterSetupConfigMode()
		}
		return m.enterSetupConfigModeWithStatus(settings.AIBackend.Label() + " selected. Press Enter to continue, or adjust endpoint/model first.")
	default:
		if !selectedStatus.Ready {
			if selectedStatus.LoginHint != "" {
				m.status = selectedStatus.LoginHint
			} else {
				m.status = selectedStatus.Detail
			}
			return m, nil
		}
		return m.enterSetupReviewMode(settings.AIBackend.Label() + " selected. Press Enter to save, or Esc to go back.")
	}
}

func (m Model) activateBossChatSetupSelection() (tea.Model, tea.Cmd) {
	settings := m.setupSettingsFromCurrentChoices()
	switch settings.BossChatBackend {
	case config.AIBackendUnset:
		return m.enterSetupConfigModeWithStatus("Boss chat: Auto selected. Press Enter to continue, or adjust OpenAI/model fields first.")
	case config.AIBackendDisabled:
		return m.enterSetupReviewMode("Boss chat: Off selected. Press Enter to save, or Esc to go back.")
	case config.AIBackendOpenAIAPI:
		if strings.TrimSpace(settings.OpenAIAPIKey) == "" {
			return m.enterSetupConfigModeWithStatus("Paste an OpenAI API key here, then press Enter to continue.")
		}
		return m.enterSetupConfigModeWithStatus("Boss chat: OpenAI API selected. Press Enter to continue, or adjust model fields first.")
	case config.AIBackendMLX, config.AIBackendOllama:
		selectedStatus := m.setupSnapshot.StatusFor(settings.BossChatBackend)
		if strings.TrimSpace(selectedStatus.Detail) == "" || !selectedStatus.Ready {
			if selectedStatus.LoginHint != "" {
				m.status = selectedStatus.LoginHint
			} else {
				m.status = selectedStatus.Detail
			}
			return m.enterSetupConfigMode()
		}
		return m.enterSetupConfigModeWithStatus(settings.BossChatBackend.Label() + " selected for boss chat. Press Enter to continue, or adjust endpoint/model first.")
	default:
		m.status = "Boss chat can use OpenAI API, MLX, Ollama, or stay off for now."
		return m, nil
	}
}

func (m Model) enterSetupConfigModeWithStatus(status string) (tea.Model, tea.Cmd) {
	updated, cmd := m.enterSetupConfigMode()
	got := updated.(Model)
	if strings.TrimSpace(status) != "" {
		got.status = status
	}
	return got, cmd
}

func (m Model) enterSetupReviewMode(status string) (tea.Model, tea.Cmd) {
	m.setupReviewMode = true
	m.setupConfigMode = false
	m.blurSettingsFields()
	if strings.TrimSpace(status) == "" {
		status = "Save setup selected. Press Enter to save, or Esc to go back."
	}
	m.status = status
	return m, nil
}

func (m Model) saveSetupFromCurrentChoices() (tea.Model, tea.Cmd) {
	settings := m.setupSettingsFromCurrentChoices()
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
	settings.BossHelmModel = m.settingsFieldValue(settingsFieldBossChatModel)
	settings.BossUtilityModel = m.settingsFieldValue(settingsFieldBossUtilityModel)
	settings.MLXBaseURL = m.settingsFieldValue(settingsFieldMLXBaseURL)
	settings.MLXAPIKey = m.settingsFieldValue(settingsFieldMLXAPIKey)
	settings.MLXModel = m.settingsFieldValue(settingsFieldMLXModel)
	settings.OllamaBaseURL = m.settingsFieldValue(settingsFieldOllamaBaseURL)
	settings.OllamaAPIKey = m.settingsFieldValue(settingsFieldOllamaAPIKey)
	settings.OllamaModel = m.settingsFieldValue(settingsFieldOllamaModel)
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
	if m.setupConfigMode {
		panel = m.renderSetupConfigSubdialogOverlay(panel)
	}
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
	contentModel := m
	contentModel.setupConfigMode = false
	return renderDialogPanel(panelWidth, panelInnerWidth, contentModel.renderSetupContent(panelInnerWidth, maxContentHeight))
}

func (m Model) renderSetupConfigSubdialogOverlay(panel string) string {
	panelWidth := lipgloss.Width(panel)
	panelHeight := lipgloss.Height(panel)
	dialogWidth := min(max(50, panelWidth-22), max(34, panelWidth-6))
	dialogInnerWidth := max(24, dialogWidth-4)
	dialog := renderDialogPanel(dialogWidth, dialogInnerWidth, m.renderSetupConfigDialogContent(dialogInnerWidth))
	left := max(0, (panelWidth-lipgloss.Width(dialog))/2)
	top := max(2, (panelHeight-lipgloss.Height(dialog))/2)
	return overlayBlock(panel, dialog, panelWidth, panelHeight, left, top)
}

func (m Model) renderSetupContent(width, maxHeight int) string {
	cards := m.renderSetupRoleCards(width)
	lines := []string{
		commandPaletteTitleStyle.Render("Setup Concierge"),
		commandPaletteHintStyle.Render(m.setupConciergeSummary()),
	}
	lines = append(lines, "")
	lines = append(lines, cards)
	if m.setupLoading {
		lines = append(lines, "")
		lines = append(lines, commandPaletteHintStyle.Render("Checking local backend availability..."))
	} else if m.setupSaving {
		lines = append(lines, "")
		lines = append(lines, commandPaletteHintStyle.Render("Saving AI setup..."))
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

func (m Model) setupConciergeSummary() string {
	settings := m.setupSettingsFromCurrentChoices()
	projectChoice := m.selectedSettingsProviderChoice(providerChoiceRoleProjectReports, settings.AIBackend, settings)
	bossChoice := m.selectedSettingsProviderChoice(providerChoiceRoleBossChat, settings.BossChatBackend, settings)
	projectState := strings.ToLower(strings.TrimSpace(projectChoice.State))
	bossState := strings.ToLower(strings.TrimSpace(bossChoice.State))
	if projectState == "" {
		projectState = "unchecked"
	}
	if bossState == "" {
		bossState = "unchecked"
	}
	return "I can write project reports with " + firstNonEmptyTrimmed(projectChoice.Label, "a helper") + " (" + projectState + ") and answer /boss with " + firstNonEmptyTrimmed(bossChoice.Label, "Auto") + " (" + bossState + ")."
}

func (m Model) renderSetupReview(width int) string {
	settings := m.setupSettingsFromCurrentChoices()
	projectChoice := m.selectedSettingsProviderChoice(providerChoiceRoleProjectReports, settings.AIBackend, settings)
	bossChoice := m.selectedSettingsProviderChoice(providerChoiceRoleBossChat, settings.BossChatBackend, settings)
	lines := []string{
		detailSectionStyle.Render("Save Setup"),
		renderWrappedDetailField("Project reports", detailValueStyle, width, m.setupReviewChoiceSummary(projectChoice)),
		renderWrappedDetailField("Boss chat", detailValueStyle, width, m.setupReviewChoiceSummary(bossChoice)),
	}
	if strings.TrimSpace(settings.OpenAIAPIKey) != "" {
		keyText := "saved"
		if suffix := maskedOpenAIKeySuffix(settings.OpenAIAPIKey); suffix != "" {
			keyText += " " + suffix
		}
		lines = append(lines, renderWrappedDetailField("OpenAI key", detailValueStyle, width, keyText))
	}
	if settings.AIBackend == config.AIBackendOpenCode {
		lines = append(lines, renderWrappedDetailField("OpenCode tier", detailValueStyle, width, string(m.setupModelTier)))
	}
	lines = append(lines, renderWrappedDetailField("Next", detailValueStyle, width, "Enter selects Save setup. Esc returns to the provider tree."))
	return strings.Join(lines, "\n")
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

func (m Model) renderSetupConfigDialogContent(width int) string {
	if len(m.settingsFields) == 0 {
		return detailWarningStyle.Render("Settings fields are not loaded yet.")
	}
	fields := m.setupConfigFieldIndexes()
	title := providerChoiceRoleTitle(m.setupFocusedProviderChoiceRole())
	lines := []string{
		detailSectionStyle.Render("Configure " + title),
		commandPaletteHintStyle.Render("Edit only what this branch needs, then press Enter to continue."),
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
		hint = "Boss chat is the direct high-level conversation in /boss."
	}
	switch selected {
	case config.AIBackendUnset:
		if strings.TrimSpace(settings.OpenAIAPIKey) == "" {
			hint = "Auto keeps boss chat unconfigured until you save an OpenAI API key here."
		} else {
			hint = "Auto will use the saved OpenAI API key for boss chat."
		}
	case config.AIBackendDisabled:
		hint = "Turn boss chat off. Project reports and embedded sessions can still use their own backends."
	case config.AIBackendOpenAIAPI:
		if strings.TrimSpace(settings.OpenAIAPIKey) == "" {
			hint = "Boss chat uses direct OpenAI API inference. Press Enter to add the API key here."
		} else if settings.AIBackend == config.AIBackendOpenAIAPI {
			hint = "Boss chat will use the saved OpenAI API key. Project reports are also using OpenAI API."
		} else {
			hint = "Boss chat will use the saved OpenAI API key. Project reports stay on " + settings.AIBackend.Label() + "."
		}
	case config.AIBackendMLX:
		hint = "Boss chat will use your MLX OpenAI-compatible endpoint. Press Enter to select and configure it, or m to pick a discovered model."
	case config.AIBackendOllama:
		hint = "Boss chat will use your Ollama OpenAI-compatible endpoint. Press Enter to select and configure it, or m to pick a discovered model."
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
	if m.setupReviewMode {
		return []string{
			renderDialogAction("Enter", "save", commitActionKeyStyle, commitActionTextStyle),
			renderDialogAction("Esc", "back", cancelActionKeyStyle, cancelActionTextStyle),
			renderDialogAction("Tab", "role", navigateActionKeyStyle, navigateActionTextStyle),
		}
	}
	if m.setupConfigMode {
		return []string{
			renderDialogAction("Enter", "continue", commitActionKeyStyle, commitActionTextStyle),
			renderDialogAction("Tab", "field", navigateActionKeyStyle, navigateActionTextStyle),
			renderDialogAction("Up/Down", "field", navigateActionKeyStyle, navigateActionTextStyle),
			renderDialogAction("Esc", "back", cancelActionKeyStyle, cancelActionTextStyle),
		}
	}
	actions := []string{
		renderDialogAction("Enter", "select", commitActionKeyStyle, commitActionTextStyle),
		renderDialogAction("Tab", "role", navigateActionKeyStyle, navigateActionTextStyle),
		renderDialogAction("Up/Down", "provider", navigateActionKeyStyle, navigateActionTextStyle),
		renderDialogAction("r", "refresh", navigateActionKeyStyle, navigateActionTextStyle),
		renderDialogAction("Esc", "close", cancelActionKeyStyle, cancelActionTextStyle),
	}
	if isLocalBackendModelPickerBackend(m.setupSelectedLocalModelBackend()) {
		actions = append(actions[:3], append([]string{
			renderDialogAction("m", "model", navigateActionKeyStyle, navigateActionTextStyle),
		}, actions[3:]...)...)
	}
	if m.setupFocusedRole == setupRoleProjectReports && m.setupSelectedBackend().SupportsModelTier() {
		actions = append(actions[:4], append([]string{
			renderDialogAction("t", "tier", navigateActionKeyStyle, navigateActionTextStyle),
		}, actions[4:]...)...)
	}
	return actions
}

func (m Model) localBackendSetupHint(backend config.AIBackend, status aibackend.Status) string {
	endpoint := strings.TrimSpace(status.Endpoint)
	if endpoint == "" {
		endpoint = config.Default().OpenAICompatibleBaseURL(backend)
	}
	models := localBackendPickerModels(status.Models)
	selectedModel := strings.TrimSpace(m.currentSettingsBaseline().OpenAICompatibleModel(backend))
	if selectedModel != "" && localBackendModelExists(selectedModel, models) {
		return fmt.Sprintf("%s will use %s for background AI tasks. Press m to pick another discovered model, or e to edit endpoint and manual settings.%s", backend.Label(), selectedModel, localBackendEnvOverrideNotice())
	}
	if len(models) == 0 {
		return fmt.Sprintf("%s uses an OpenAI-compatible local server at %s. Press r after the server is running, then m to pick a model.", backend.Label(), endpoint)
	}
	return fmt.Sprintf("%s will auto-use %s from %s. Press m to pin a discovered model or e to edit endpoint and manual settings.%s", backend.Label(), firstString(models), endpoint, localBackendEnvOverrideNotice())
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
