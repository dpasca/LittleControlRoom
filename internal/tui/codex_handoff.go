package tui

import (
	"strings"

	"lcroom/internal/codexapp"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
)

type codexHandoffDialogState struct {
	Source   codexapp.Snapshot
	Note     string
	Provider codexapp.Provider
}

func (m *Model) openCodexHandoffDialog(source codexapp.Snapshot, note string) {
	provider := explicitEmbeddedProvider(embeddedProvider(source))
	if provider == "" {
		provider = codexapp.ProviderCodex
	}
	source.Provider = embeddedProvider(source)
	source.ProjectPath = strings.TrimSpace(m.codexVisibleProject)
	m.codexHandoffDialog = &codexHandoffDialogState{
		Source:   source,
		Note:     strings.TrimSpace(note),
		Provider: provider,
	}
	m.err = nil
	m.status = "Choose the AI agent for this handoff, then choose its model and reasoning effort"
}

func (m Model) updateCodexHandoffDialogMode(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	if m.codexHandoffDialog == nil {
		return m, nil
	}
	switch msg.String() {
	case "esc", "ctrl+c":
		m.codexHandoffDialog = nil
		m.status = "Handoff canceled; the current session is unchanged"
		return m, nil
	case "down", "j":
		m.cycleCodexHandoffProvider(1)
		return m, nil
	case "up", "k":
		m.cycleCodexHandoffProvider(-1)
		return m, nil
	case "enter", "m":
		return m, m.openCodexHandoffModelPickerCmd()
	}
	return m, nil
}

func (m *Model) cycleCodexHandoffProvider(delta int) {
	dialog := m.codexHandoffDialog
	if dialog == nil || delta == 0 {
		return
	}
	options := embeddedLaunchProviderOptions()
	current := explicitEmbeddedProvider(dialog.Provider)
	index := 0
	for i, provider := range options {
		if provider == current {
			index = i
			break
		}
	}
	index = (index + delta + len(options)) % len(options)
	dialog.Provider = options[index]
}

func (m *Model) openCodexHandoffModelPickerCmd() tea.Cmd {
	dialog := m.codexHandoffDialog
	if dialog == nil {
		return nil
	}
	provider := explicitEmbeddedProvider(dialog.Provider)
	dialog.Provider = provider
	m.openCodexModelPickerLoadingForProvider(codexModelPickerTargetHandoff, provider)
	m.status = "Loading " + provider.Label() + " models for the handoff..."
	return m.openPrelaunchCodexModelPickerCmd(provider, codexModelPickerTargetHandoff)
}

func (m Model) applyCodexHandoffModelPickerSelection(modelOption codexapp.ModelOption, effort string) (tea.Model, tea.Cmd) {
	provider := m.codexModelPickerProvider()
	modelProvider := strings.TrimSpace(modelOption.ModelProvider)
	if provider == codexapp.ProviderLCAgent &&
		strings.TrimSpace(modelOption.Model) != "" &&
		modelProvider != "" &&
		!m.lcagentModelProviderReady(modelProvider) {
		return m.openCodexLCAgentProviderSetup(modelOption, effort)
	}
	return m.startCodexHandoff(provider, modelOption, modelProvider, effort)
}

func (m Model) startCodexHandoff(
	provider codexapp.Provider,
	modelOption codexapp.ModelOption,
	modelProvider string,
	effort string,
) (tea.Model, tea.Cmd) {
	dialog := m.codexHandoffDialog
	if dialog == nil {
		m.closeCodexModelPicker("Handoff choices expired; the current session is unchanged")
		return m, nil
	}
	provider = explicitEmbeddedProvider(provider)
	if provider == "" {
		provider = codexapp.ProviderCodex
	}
	modelOption.ModelProvider = strings.TrimSpace(modelProvider)
	modelName := strings.TrimSpace(modelOption.Model)
	effort = strings.TrimSpace(effort)
	if modelName != "" {
		m.recordRecentModel(provider, modelName, modelOption.ModelProvider)
	}
	source := dialog.Source
	note := dialog.Note
	m.closeCodexModelPicker("")
	m.codexHandoffDialog = nil
	m.beginNewCodexPendingOpen(m.codexVisibleProject, provider)
	m.status = "Saving a continuation brief and starting a fresh embedded " + provider.Label() + " session"
	if modelName != "" {
		m.status += " with " + modelName
	}
	if effort != "" {
		m.status += " (" + effort + " reasoning)"
	}
	m.status += "..."
	return m, m.handoffVisibleCodexSessionToSelectionCmd(source, note, provider, modelOption, effort, true)
}

func (m Model) renderCodexHandoffOverlay(body string, bodyW, bodyH int) string {
	dialog := m.codexHandoffDialog
	if dialog == nil {
		return body
	}
	panelW := min(bodyW, min(max(68, bodyW-10), 100))
	panelInnerW := max(24, panelW-4)
	provider := explicitEmbeddedProvider(dialog.Provider)
	settings := m.currentSettingsBaseline()
	lines := []string{
		commandPaletteTitleStyle.Render("Handoff"),
	}
	lines = append(lines, renderWrappedDialogTextLines(
		commandPaletteHintStyle,
		panelInnerW,
		"Choose the embedded agent that should continue this session. You will choose its model and reasoning effort next; nothing is replaced until you confirm both.",
	)...)
	if dialog.Note != "" {
		lines = append(lines, "", detailField("Note", detailValueStyle.Render(truncateText(dialog.Note, max(12, panelInnerW-8)))))
	}
	lines = append(lines, "", detailSectionStyle.Render("Agent"))
	for _, option := range embeddedLaunchProviderOptions() {
		label := m.todoCopyProviderButtonLabel(dialog.Source.ProjectPath, option, settings)
		lines = append(lines, fitStyledWidth(renderDialogButton(label, provider == option), panelInnerW))
	}
	if statusLine := m.todoCopyProviderStatusLine(provider, settings); statusLine != "" {
		lines = append(lines, detailField("Agent status", statusLine))
	}
	lines = append(lines, "", renderHelpPanelActionRow(
		renderDialogAction("Enter", "choose model", commitActionKeyStyle, commitActionTextStyle),
		renderDialogAction("↑↓/j/k", "agent", navigateActionKeyStyle, navigateActionTextStyle),
		renderDialogAction("Esc", "cancel", cancelActionKeyStyle, cancelActionTextStyle),
	))
	panel := renderDialogPanel(panelW, panelInnerW, strings.Join(lines, "\n"))
	left := max(0, (bodyW-lipgloss.Width(panel))/2)
	top := max(0, (bodyH-lipgloss.Height(panel))/3)
	return overlayBlock(body, panel, bodyW, bodyH, left, top)
}
