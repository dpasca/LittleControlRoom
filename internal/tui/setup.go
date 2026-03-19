package tui

import (
	"strings"

	"lcroom/internal/aibackend"
	"lcroom/internal/config"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
)

var setupBackendOptions = []config.AIBackend{
	config.AIBackendCodex,
	config.AIBackendOpenCode,
	config.AIBackendOpenAIAPI,
	config.AIBackendDisabled,
}

func (m *Model) openSetupMode() tea.Cmd {
	m.setupMode = true
	m.settingsMode = false
	m.commandMode = false
	m.showHelp = false
	m.err = nil
	m.setupSelected = m.setupSelectionForBackend(m.recommendedSetupBackend())
	m.setupLoading = true
	m.status = "Choose how Little Control Room should run AI summaries, classifications, and commit help."
	return m.refreshSetupSnapshotCmd(false)
}

func (m *Model) closeSetupMode(status string) {
	m.setupMode = false
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
	case "s":
		settings := m.currentSettingsBaseline()
		if selected := m.setupSelectedBackend(); selected == config.AIBackendOpenAIAPI {
			settings.AIBackend = selected
		}
		return m, m.openSettingsModeWithBaseline(settings)
	case "enter":
		return m.activateSetupSelection()
	}
	return m, nil
}

func (m Model) activateSetupSelection() (tea.Model, tea.Cmd) {
	settings := m.currentSettingsBaseline()
	settings.AIBackend = m.setupSelectedBackend()
	selectedStatus := m.setupSnapshot.StatusFor(settings.AIBackend)
	switch settings.AIBackend {
	case config.AIBackendDisabled:
		m.status = "Saving AI setup..."
		return m, m.saveSetupCmd(settings)
	case config.AIBackendOpenAIAPI:
		if strings.TrimSpace(settings.OpenAIAPIKey) == "" {
			m.status = "OpenAI API key backend selected. Save a key to finish setup."
			return m, m.openSettingsModeWithBaseline(settings)
		}
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
	return lipgloss.NewStyle().
		Width(panelWidth).
		Border(lipgloss.RoundedBorder()).
		BorderForeground(lipgloss.Color("81")).
		Padding(0, 1).
		Background(lipgloss.Color("235")).
		Foreground(lipgloss.Color("252")).
		Render(m.renderSetupContent(panelInnerWidth, maxContentHeight))
}

func (m Model) renderSetupContent(width, _ int) string {
	lines := []string{
		commandPaletteTitleStyle.Render("Setup"),
		commandPaletteHintStyle.Render("Config: " + truncateText(m.currentConfigPath(), max(20, width-8))),
		commandPaletteHintStyle.Render("Pick the backend Little Control Room should use for summaries, classifications, and commit help."),
	}
	if m.setupLoading {
		lines = append(lines, "")
		lines = append(lines, commandPaletteHintStyle.Render("Checking local backend availability..."))
	}
	for i, backend := range setupBackendOptions {
		lines = append(lines, m.renderSetupOptionRow(backend, i == m.setupSelected, width))
	}
	if hint := m.renderSetupHint(width); hint != "" {
		lines = append(lines, "")
		lines = append(lines, hint)
	}
	lines = append(lines, "")
	lines = append(lines, renderSetupActions())
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
	return labelStyle.Width(labelWidth).Render(truncateText(label, labelWidth)) +
		" " + stateStyle.Width(stateWidth).Render(truncateText(state, stateWidth)) +
		" " + detailStyle.Render(truncateText(status.Detail, detailWidth))
}

func (m Model) setupOptionState(backend config.AIBackend, status aibackend.Status) (string, lipgloss.Style) {
	switch {
	case backend == m.setupCurrentBackend() && backend != config.AIBackendUnset:
		return "active", commandPalettePickStyle
	case backend == config.AIBackendDisabled:
		return "off", detailMutedStyle
	case status.Ready:
		return "ready", footerPrimaryLabelStyle
	case !status.Installed && backend != config.AIBackendOpenAIAPI:
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
	if selectedStatus.LoginHint != "" && !selectedStatus.Ready {
		hint = selectedStatus.LoginHint
	}
	return commandPaletteHintStyle.Render(lipgloss.NewStyle().Width(width).Render("Hint: " + hint))
}

func renderSetupActions() string {
	return strings.Join([]string{
		renderDialogAction("Enter", "choose", commitActionKeyStyle, commitActionTextStyle),
		renderDialogAction("R", "refresh", navigateActionKeyStyle, navigateActionTextStyle),
		renderDialogAction("S", "settings", pushActionKeyStyle, pushActionTextStyle),
		renderDialogAction("Esc", "continue", cancelActionKeyStyle, cancelActionTextStyle),
	}, "   ")
}
