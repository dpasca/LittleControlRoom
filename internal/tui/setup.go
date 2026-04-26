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

var setupBackendOptions = config.SelectableAIBackends()

func (m *Model) openSetupMode() tea.Cmd {
	m.setupMode = true
	m.settingsMode = false
	m.commandMode = false
	m.showHelp = false
	m.err = nil
	m.setupSaving = false
	m.localModelPickerVisible = false
	m.setupSelected = m.setupSelectionForBackend(m.recommendedSetupBackend())
	tier, _ := config.ParseModelTier(m.currentSettingsBaseline().OpenCodeModelTier)
	m.setupModelTier = tier
	m.setupLoading = true
	m.status = "Choose how Little Control Room should run AI summaries, classifications, and commit help."
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
	case "tab", "down", "ctrl+n":
		m.setupSelected = (m.setupSelected + 1) % len(setupBackendOptions)
		return m, nil
	case "shift+tab", "up", "ctrl+p":
		m.setupSelected--
		if m.setupSelected < 0 {
			m.setupSelected = len(setupBackendOptions) - 1
		}
		return m, nil
	case "r":
		m.setupLoading = true
		m.status = "Refreshing AI backend checks..."
		return m, m.refreshSetupSnapshotCmd(false)
	case "t":
		if m.setupSelectedBackend().SupportsModelTier() {
			m.setupModelTier = m.cycleModelTier(m.setupModelTier)
			return m, nil
		}
	case "s":
		settings := m.currentSettingsBaseline()
		if selected := m.setupSelectedBackend(); selected == config.AIBackendOpenAIAPI || selected == config.AIBackendMLX || selected == config.AIBackendOllama {
			settings.AIBackend = selected
		}
		return m, m.openSettingsModeWithBaseline(settings)
	case "m":
		if isLocalBackendModelPickerBackend(m.setupSelectedBackend()) {
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

func (m Model) activateSetupSelection() (tea.Model, tea.Cmd) {
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

func (m Model) setupCurrentBackend() config.AIBackend {
	if current := m.currentSettingsBaseline().AIBackend; current != config.AIBackendUnset {
		return current
	}
	return m.setupSnapshot.Selected
}

func (m Model) setupSelectedBackend() config.AIBackend {
	if m.setupSelected < 0 || m.setupSelected >= len(setupBackendOptions) {
		return config.AIBackendCodex
	}
	return setupBackendOptions[m.setupSelected]
}

func (m Model) setupSelectionForBackend(backend config.AIBackend) int {
	for i, option := range setupBackendOptions {
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

func (m Model) renderSetupContent(width, _ int) string {
	lines := []string{
		commandPaletteTitleStyle.Render("Setup"),
		commandPaletteHintStyle.Render("Config: " + truncateText(m.displayPathWithHomeTilde(m.currentConfigPath()), max(20, width-8))),
		commandPaletteHintStyle.Render("Pick the backend Little Control Room should use for project reports."),
	}
	lines = append(lines, "")
	lines = append(lines, m.renderInferenceStatusCards(width))
	if m.setupLoading {
		lines = append(lines, "")
		lines = append(lines, commandPaletteHintStyle.Render("Checking local backend availability..."))
	} else if m.setupSaving {
		lines = append(lines, "")
		lines = append(lines, commandPaletteHintStyle.Render("Saving AI setup..."))
	}
	for i, backend := range setupBackendOptions {
		lines = append(lines, m.renderSetupOptionRow(backend, i == m.setupSelected, width))
	}
	if hint := m.renderSetupHint(width); hint != "" {
		lines = append(lines, "")
		lines = append(lines, hint)
	}
	lines = append(lines, "")
	lines = append(lines, m.renderSetupActions())
	return strings.Join(lines, "\n")
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
	return labelStyle.Width(labelWidth).Render(truncateText(label, labelWidth)) +
		" " + stateStyle.Width(stateWidth).Render(truncateText(state, stateWidth)) +
		" " + detailStyle.Render(truncateText(detailText, detailWidth))
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

func (m Model) renderSetupActions() string {
	actions := []string{
		renderDialogAction("Enter", "choose", commitActionKeyStyle, commitActionTextStyle),
		renderDialogAction("r", "refresh", navigateActionKeyStyle, navigateActionTextStyle),
		renderDialogAction("s", "settings", pushActionKeyStyle, pushActionTextStyle),
		renderDialogAction("Esc", "continue", cancelActionKeyStyle, cancelActionTextStyle),
	}
	if isLocalBackendModelPickerBackend(m.setupSelectedBackend()) {
		actions = append(actions[:2], append([]string{
			renderDialogAction("m", "model", navigateActionKeyStyle, navigateActionTextStyle),
		}, actions[2:]...)...)
	}
	if m.setupSelectedBackend().SupportsModelTier() {
		actions = append(actions[:3], append([]string{
			renderDialogAction("t", "tier", navigateActionKeyStyle, navigateActionTextStyle),
		}, actions[3:]...)...)
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
