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

var setupBackendOptions = config.SelectableAIBackends()
var setupBossChatOptions = []config.AIBackend{
	config.AIBackendOpenAIAPI,
	config.AIBackendDisabled,
}

func (m *Model) openSetupMode() tea.Cmd {
	m.setupMode = true
	m.settingsMode = false
	m.commandMode = false
	m.showHelp = false
	m.err = nil
	m.setupSaving = false
	m.localModelPickerVisible = false
	m.setupFocusedRole = setupRoleProjectReports
	m.setupSelected = m.setupSelectionForBackend(m.recommendedSetupBackend())
	m.setupBossSelected = m.setupBossSelectionForBackend(m.recommendedSetupBossBackend())
	tier, _ := config.ParseModelTier(m.currentSettingsBaseline().OpenCodeModelTier)
	m.setupModelTier = tier
	m.setupLoading = true
	m.status = "Choose AI roles for project reports and boss chat."
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
	m.localModelPickerVisible = false
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
		cfg.OllamaBaseURL = settings.OllamaBaseURL
		cfg.OllamaAPIKey = settings.OllamaAPIKey
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
	switch msg.String() {
	case "esc":
		m.closeSetupMode("AI setup skipped for now. Run /setup anytime.")
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
	case "a", "s":
		settings := m.currentSettingsBaseline()
		switch m.setupFocusedRole {
		case setupRoleBossChat:
			settings.BossChatBackend = m.setupSelectedBossBackend()
		default:
			if selected := m.setupSelectedBackend(); selected == config.AIBackendOpenAIAPI || selected == config.AIBackendMLX || selected == config.AIBackendOllama {
				settings.AIBackend = selected
			}
		}
		return m, m.openSettingsModeWithBaseline(settings)
	case "m":
		if m.setupFocusedRole == setupRoleProjectReports && isLocalBackendModelPickerBackend(m.setupSelectedBackend()) {
			return m.openLocalBackendModelPicker()
		}
	case "enter":
		return m.activateSetupSelection()
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
	if m.setupFocusedRole == setupRoleBossChat {
		m.setupBossSelected = wrapIndex(m.setupBossSelected+delta, len(setupBossChatOptions))
		return
	}
	m.setupSelected = wrapIndex(m.setupSelected+delta, len(setupBackendOptions))
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

func (m Model) activateSetupSelection() (tea.Model, tea.Cmd) {
	if m.setupFocusedRole == setupRoleBossChat {
		return m.activateBossChatSetupSelection()
	}
	settings := m.currentSettingsBaseline()
	settings.AIBackend = m.setupSelectedBackend()
	settings.OpenCodeModelTier = string(m.setupModelTier)
	selectedStatus := m.setupSnapshot.StatusFor(settings.AIBackend)
	switch settings.AIBackend {
	case config.AIBackendDisabled:
		m.setupSaving = true
		m.status = "Saving AI setup..."
		return m, m.saveSetupCmd(settings)
	case config.AIBackendOpenAIAPI:
		if strings.TrimSpace(settings.OpenAIAPIKey) == "" {
			m.status = "OpenAI API key backend selected. Save a key to finish setup."
			return m, m.openSettingsModeWithBaseline(settings)
		}
		m.setupSaving = true
		m.status = "Saving AI setup..."
		return m, m.saveSetupCmd(settings)
	case config.AIBackendMLX, config.AIBackendOllama:
		if strings.TrimSpace(selectedStatus.Detail) == "" || !selectedStatus.Ready {
			if selectedStatus.LoginHint != "" {
				m.status = selectedStatus.LoginHint
			} else {
				m.status = selectedStatus.Detail
			}
			return m, m.openSettingsModeWithBaseline(settings)
		}
		m.setupSaving = true
		m.status = "Saving AI setup..."
		return m, m.saveSetupCmd(settings)
	default:
		if !selectedStatus.Ready {
			if selectedStatus.LoginHint != "" {
				m.status = selectedStatus.LoginHint
			} else {
				m.status = selectedStatus.Detail
			}
			return m, nil
		}
		m.setupSaving = true
		m.status = "Saving AI setup..."
		return m, m.saveSetupCmd(settings)
	}
}

func (m Model) activateBossChatSetupSelection() (tea.Model, tea.Cmd) {
	settings := m.currentSettingsBaseline()
	settings.BossChatBackend = m.setupSelectedBossBackend()
	switch settings.BossChatBackend {
	case config.AIBackendDisabled:
		m.setupSaving = true
		m.status = "Saving AI setup..."
		return m, m.saveSetupCmd(settings)
	case config.AIBackendOpenAIAPI:
		if strings.TrimSpace(settings.OpenAIAPIKey) == "" {
			m.status = "Boss chat selected OpenAI API. Save a key to finish setup."
			return m, m.openSettingsModeWithBaseline(settings)
		}
		m.setupSaving = true
		m.status = "Saving AI setup..."
		return m, m.saveSetupCmd(settings)
	default:
		m.status = "Boss chat can use OpenAI API or stay off for now."
		return m, nil
	}
}

func (m Model) recommendedSetupBackend() config.AIBackend {
	current := m.setupCurrentBackend()
	if current != config.AIBackendUnset && m.setupSnapshot.StatusFor(current).Ready {
		return current
	}
	for _, backend := range setupBackendOptions {
		if backend == config.AIBackendDisabled {
			continue
		}
		if m.setupSnapshot.StatusFor(backend).Ready {
			return backend
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
	return config.AIBackendOpenAIAPI
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
	if m.setupSelected < 0 || m.setupSelected >= len(setupBackendOptions) {
		return config.AIBackendCodex
	}
	return setupBackendOptions[m.setupSelected]
}

func (m Model) setupSelectedBossBackend() config.AIBackend {
	if m.setupBossSelected < 0 || m.setupBossSelected >= len(setupBossChatOptions) {
		return config.AIBackendOpenAIAPI
	}
	return setupBossChatOptions[m.setupBossSelected]
}

func (m Model) setupSelectionForBackend(backend config.AIBackend) int {
	for i, option := range setupBackendOptions {
		if option == backend {
			return i
		}
	}
	return 0
}

func (m Model) setupBossSelectionForBackend(backend config.AIBackend) int {
	for i, option := range setupBossChatOptions {
		if option == backend {
			return i
		}
	}
	return 0
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
	cards := m.renderSetupRoleCards(width)
	lines := []string{
		commandPaletteTitleStyle.Render("Setup"),
		commandPaletteHintStyle.Render("Choose two AI roles. Tab switches cards; Up/Down changes the focused provider."),
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
	lines = append(lines, "")
	lines = append(lines, detailSectionStyle.Render(m.setupProviderListTitle()))
	lines = append(lines, renderWrappedDialogTextLines(commandPaletteHintStyle, width, m.setupRolePurpose())...)

	providerRows := m.renderSetupProviderRows(width)
	providerLimit := max(2, maxHeight-renderedLinesHeight(lines)-5)
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
	if hint := m.renderSetupHint(width); hint != "" {
		lines = append(lines, "")
		lines = append(lines, hint)
	}
	if actions := m.renderSetupActions(); actions != "" && lipgloss.Width(actions) <= width && renderedLinesHeight(lines)+2 <= maxHeight {
		lines = append(lines, "")
		lines = append(lines, actions)
	}
	return strings.Join(lines, "\n")
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
	settings := m.currentSettingsBaseline()
	projectCard := m.projectReportsStatusCard(settings)
	projectCard.Selected = m.setupFocusedRole == setupRoleProjectReports
	bossCard := m.bossChatStatusCard(settings)
	bossCard.Selected = m.setupFocusedRole == setupRoleBossChat
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
	if m.setupFocusedRole == setupRoleBossChat {
		return "Boss Chat Provider"
	}
	return "Project Reports Provider"
}

func (m Model) setupRolePurpose() string {
	if m.setupFocusedRole == setupRoleBossChat {
		return "Used only for the high-level /boss conversation. This can stay separate from background reports."
	}
	return "Used for summaries, classifications, TODO help, and commit help."
}

func (m Model) renderSetupProviderRows(width int) []string {
	if m.setupFocusedRole == setupRoleBossChat {
		lines := make([]string, 0, len(setupBossChatOptions))
		for i, backend := range setupBossChatOptions {
			lines = append(lines, m.renderBossChatSetupOptionRow(backend, i == m.setupBossSelected, width))
		}
		return lines
	}

	lines := make([]string, 0, len(setupBackendOptions))
	for i, backend := range setupBackendOptions {
		lines = append(lines, m.renderSetupOptionRow(backend, i == m.setupSelected, width))
	}
	return lines
}

func (m Model) renderSetupOptionRow(backend config.AIBackend, selected bool, width int) string {
	status := m.setupSnapshot.StatusFor(backend)
	label := "  " + backend.Label()
	labelStyle := detailLabelStyle
	if selected {
		label = "> " + backend.Label()
		labelStyle = commandPalettePickStyle
	} else if status.Ready || backend == m.setupCurrentBackend() {
		labelStyle = detailValueStyle
	}
	state, stateStyle := m.setupOptionState(backend, status)
	labelWidth := min(24, max(18, width/3))
	stateWidth := 9
	detailWidth := max(12, width-labelWidth-stateWidth-2)
	detailStyle := commandPaletteHintStyle
	if status.Ready || backend == m.setupCurrentBackend() {
		detailStyle = detailValueStyle
	}
	detailText := m.setupOptionDetail(backend, status)
	if backend.SupportsModelTier() && selected {
		tierHint := " [T: " + string(m.setupModelTier) + "]"
		if len(detailText)+len(tierHint) < detailWidth {
			detailText = detailText + tierHint
		}
	}
	row := labelStyle.Width(labelWidth).Render(truncateText(label, labelWidth)) +
		" " + stateStyle.Width(stateWidth).Render(truncateText(state, stateWidth)) +
		" " + detailStyle.Render(truncateText(detailText, detailWidth))
	if selected {
		return dialogSelectedRowStyle.Width(width).Render(fitFooterWidth(row, width))
	}
	return row
}

func (m Model) renderBossChatSetupOptionRow(backend config.AIBackend, selected bool, width int) string {
	settings := m.currentSettingsBaseline()
	status, _ := m.inferenceBackendStatus(backend, settings)
	label := "  " + bossChatBackendSetupLabel(backend)
	labelStyle := detailLabelStyle
	if selected {
		label = "> " + bossChatBackendSetupLabel(backend)
		labelStyle = commandPalettePickStyle
	} else if backend == m.setupCurrentBossBackend() {
		labelStyle = detailValueStyle
	}
	state, stateStyle := m.bossChatSetupOptionState(backend, status)
	labelWidth := min(24, max(18, width/3))
	stateWidth := 9
	detailWidth := max(12, width-labelWidth-stateWidth-2)
	detailText := m.bossChatSetupOptionDetail(backend, settings)
	detailStyle := commandPaletteHintStyle
	if status.Ready || backend == m.setupCurrentBossBackend() {
		detailStyle = detailValueStyle
	}
	row := labelStyle.Width(labelWidth).Render(truncateText(label, labelWidth)) +
		" " + stateStyle.Width(stateWidth).Render(truncateText(state, stateWidth)) +
		" " + detailStyle.Render(truncateText(detailText, detailWidth))
	if selected {
		return dialogSelectedRowStyle.Width(width).Render(fitFooterWidth(row, width))
	}
	return row
}

func bossChatBackendSetupLabel(backend config.AIBackend) string {
	if backend == config.AIBackendDisabled {
		return "Off"
	}
	return backend.Label()
}

func (m Model) bossChatSetupOptionState(backend config.AIBackend, status aibackend.Status) (string, lipgloss.Style) {
	switch {
	case backend == m.setupCurrentBossBackend() && backend != config.AIBackendUnset:
		return "active", commandPalettePickStyle
	case backend == config.AIBackendDisabled:
		return "off", detailMutedStyle
	case status.Ready:
		return "ready", footerPrimaryLabelStyle
	default:
		return "setup", detailWarningStyle
	}
}

func (m Model) bossChatSetupOptionDetail(backend config.AIBackend, settings config.EditableSettings) string {
	switch backend {
	case config.AIBackendDisabled:
		return "turn off high-level chat inference"
	case config.AIBackendOpenAIAPI:
		if strings.TrimSpace(settings.OpenAIAPIKey) == "" {
			return "needs a saved OpenAI API key"
		}
		if settings.AIBackend == config.AIBackendOpenAIAPI {
			return "uses the shared saved OpenAI API key"
		}
		return "direct API chat; project reports stay separate"
	default:
		return "not available"
	}
}

func (m Model) setupOptionState(backend config.AIBackend, status aibackend.Status) (string, lipgloss.Style) {
	switch {
	case backend == m.setupCurrentBackend() && backend != config.AIBackendUnset:
		return "active", commandPalettePickStyle
	case backend == config.AIBackendDisabled:
		return "off", detailMutedStyle
	case status.Ready:
		return "ready", footerPrimaryLabelStyle
	case !status.Installed && backend.RequiresCLIInstallHint():
		return "install", detailWarningStyle
	default:
		return "setup", commandPaletteHintStyle
	}
}

func (m Model) renderSetupHint(width int) string {
	if m.setupFocusedRole == setupRoleBossChat {
		return m.renderBossChatSetupHint(width)
	}
	selectedStatus := m.setupSnapshot.StatusFor(m.setupSelectedBackend())
	hint := selectedStatus.Detail
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
	return commandPaletteHintStyle.Render(lipgloss.NewStyle().Width(width).Render("Hint: " + hint))
}

func (m Model) renderBossChatSetupHint(width int) string {
	settings := m.currentSettingsBaseline()
	selected := m.setupSelectedBossBackend()
	hint := "Boss chat is the direct high-level conversation in /boss."
	switch selected {
	case config.AIBackendDisabled:
		hint = "Turn boss chat off. Project reports and embedded sessions can still use their own backends."
	case config.AIBackendOpenAIAPI:
		if strings.TrimSpace(settings.OpenAIAPIKey) == "" {
			hint = "Boss chat uses direct OpenAI API inference. Press Enter to choose it and save an API key in advanced settings."
		} else if settings.AIBackend == config.AIBackendOpenAIAPI {
			hint = "Boss chat will use the saved OpenAI API key. Project reports are also using OpenAI API."
		} else {
			hint = "Boss chat will use the saved OpenAI API key. Project reports stay on " + settings.AIBackend.Label() + "."
		}
	}
	return commandPaletteHintStyle.Render(lipgloss.NewStyle().Width(width).Render("Hint: " + hint))
}

func (m Model) renderSetupActions() string {
	actions := []string{
		renderDialogAction("Enter", "choose", commitActionKeyStyle, commitActionTextStyle),
		renderDialogAction("Tab", "role", navigateActionKeyStyle, navigateActionTextStyle),
		renderDialogAction("Up/Down", "provider", navigateActionKeyStyle, navigateActionTextStyle),
		renderDialogAction("r", "refresh", navigateActionKeyStyle, navigateActionTextStyle),
		renderDialogAction("a/s", "advanced", pushActionKeyStyle, pushActionTextStyle),
		renderDialogAction("Esc", "close", cancelActionKeyStyle, cancelActionTextStyle),
	}
	if m.setupFocusedRole == setupRoleProjectReports && isLocalBackendModelPickerBackend(m.setupSelectedBackend()) {
		actions = append(actions[:3], append([]string{
			renderDialogAction("m", "model", navigateActionKeyStyle, navigateActionTextStyle),
		}, actions[3:]...)...)
	}
	if m.setupFocusedRole == setupRoleProjectReports && m.setupSelectedBackend().SupportsModelTier() {
		actions = append(actions[:4], append([]string{
			renderDialogAction("t", "tier", navigateActionKeyStyle, navigateActionTextStyle),
		}, actions[4:]...)...)
	}
	return strings.Join(actions, "   ")
}

func (m Model) setupOptionDetail(backend config.AIBackend, status aibackend.Status) string {
	if !isLocalBackendModelPickerBackend(backend) {
		return status.Detail
	}

	models := localBackendPickerModels(status.Models)
	if len(models) == 0 {
		return status.Detail
	}

	selectedModel := strings.TrimSpace(m.currentSettingsBaseline().OpenAICompatibleModel(backend))
	endpoint := strings.TrimSpace(status.Endpoint)
	if selectedModel != "" {
		if localBackendModelExists(selectedModel, models) {
			if endpoint != "" {
				return fmt.Sprintf("using %s @ %s", selectedModel, endpoint)
			}
			return "using " + selectedModel
		}
		return fmt.Sprintf("configured %s (server offers %s)", selectedModel, summarizeLocalBackendModels(models))
	}

	if endpoint != "" {
		return fmt.Sprintf("auto %s @ %s", firstString(models), endpoint)
	}
	return "auto " + firstString(models)
}

func (m Model) localBackendSetupHint(backend config.AIBackend, status aibackend.Status) string {
	endpoint := strings.TrimSpace(status.Endpoint)
	if endpoint == "" {
		endpoint = config.Default().OpenAICompatibleBaseURL(backend)
	}
	models := localBackendPickerModels(status.Models)
	selectedModel := strings.TrimSpace(m.currentSettingsBaseline().OpenAICompatibleModel(backend))
	if selectedModel != "" && localBackendModelExists(selectedModel, models) {
		return fmt.Sprintf("%s will use %s for background AI tasks. Press m to pick another discovered model, or s to edit endpoint and manual settings.%s", backend.Label(), selectedModel, localBackendEnvOverrideNotice())
	}
	if len(models) == 0 {
		return fmt.Sprintf("%s uses an OpenAI-compatible local server at %s. Press r after the server is running, then m to pick a model.", backend.Label(), endpoint)
	}
	return fmt.Sprintf("%s will auto-use %s from %s. Press m to pin a discovered model or s to edit endpoint and manual settings.%s", backend.Label(), firstString(models), endpoint, localBackendEnvOverrideNotice())
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
