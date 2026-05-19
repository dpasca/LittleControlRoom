package tui

import (
	"strings"

	"lcroom/internal/aibackend"
	"lcroom/internal/config"

	"github.com/charmbracelet/lipgloss"
)

type providerChoiceRole string

const (
	providerChoiceRoleProjectReports providerChoiceRole = "project-reports"
	providerChoiceRoleBossChat       providerChoiceRole = "boss-chat"
)

type providerChoice struct {
	Value       config.AIBackend
	Label       string
	Summary     string
	Description string
	State       string
	StateStyle  lipgloss.Style
	Detail      string
	NextStep    string
}

func providerChoiceRoleTitle(role providerChoiceRole) string {
	switch role {
	case providerChoiceRoleBossChat:
		return "Boss Chat"
	default:
		return "Project Reports"
	}
}

func providerChoiceRoleListTitle(role providerChoiceRole) string {
	switch role {
	case providerChoiceRoleBossChat:
		return "Who Should Handle Boss Chat?"
	default:
		return "Who Should Handle Project Reports?"
	}
}

func providerChoiceRolePurpose(role providerChoiceRole) string {
	switch role {
	case providerChoiceRoleBossChat:
		return "This is the direct high-level /boss conversation. It can use a different helper from background reports, or stay off."
	default:
		return "This helper writes summaries, classifications, TODO help, and commit help in the background."
	}
}

func providerChoiceRoleFallbackLabel(role providerChoiceRole) string {
	switch role {
	case providerChoiceRoleBossChat:
		return "Auto"
	default:
		return "Not configured"
	}
}

func (m Model) providerChoices(role providerChoiceRole, settings config.EditableSettings) []providerChoice {
	switch role {
	case providerChoiceRoleBossChat:
		return m.bossChatProviderChoices(settings)
	default:
		return m.projectReportsProviderChoices(settings)
	}
}

func (m Model) projectReportsProviderChoices(settings config.EditableSettings) []providerChoice {
	specs := []providerChoice{
		{
			Value:       config.AIBackendCodex,
			Label:       "Codex",
			Summary:     "Writes project summaries, classifications, TODO help, and commit help through your local Codex CLI.",
			Description: "Codex must be installed and signed in. Little Control Room does not store an API key for this path.",
		},
		{
			Value:       config.AIBackendOpenCode,
			Label:       "OpenCode",
			Summary:     "Writes project summaries, classifications, TODO help, and commit help through your local OpenCode installation.",
			Description: "OpenCode must be installed and signed in. Little Control Room does not store an API key for this path.",
		},
		{
			Value:       config.AIBackendClaude,
			Label:       "Claude Code",
			Summary:     "Writes project summaries, classifications, TODO help, and commit help through your local Claude Code installation.",
			Description: "Claude Code must be installed and signed in. Background tasks default to Haiku to keep usage lighter.",
		},
		{
			Value:       config.AIBackendMLX,
			Label:       "MLX",
			Summary:     "Writes project summaries and helper output through a local MLX OpenAI-compatible endpoint.",
			Description: "The MLX server must be running at the configured endpoint. Leave the model blank to use the first discovered local model.",
		},
		{
			Value:       config.AIBackendOllama,
			Label:       "Ollama",
			Summary:     "Writes project summaries and helper output through a local Ollama OpenAI-compatible endpoint.",
			Description: "The Ollama server must be running at the configured endpoint. Leave the model blank to use the first discovered local model.",
		},
		{
			Value:       config.AIBackendOpenAIAPI,
			Label:       "OpenAI API",
			Summary:     "Writes project summaries and helper output directly through the shared OpenAI API connection.",
			Description: "Choose this path only if you want direct API inference; the key field appears after you select it.",
		},
		{
			Value:       config.AIBackendDisabled,
			Label:       "Disabled",
			Summary:     "Keeps Little Control Room running without background project-report AI.",
			Description: "No provider setup is needed. Summaries, classifications, TODO help, and commit help stay off.",
		},
	}
	for i := range specs {
		status, known := m.inferenceBackendStatus(specs[i].Value, settings)
		specs[i].State, specs[i].StateStyle = inferenceStateForBackend(specs[i].Value, status, known)
		specs[i].Detail = m.projectReportsProviderDetail(specs[i].Value, status)
		specs[i].NextStep = projectReportsProviderNextStep(specs[i].Value, status, known)
	}
	return specs
}

func (m Model) bossChatProviderChoices(settings config.EditableSettings) []providerChoice {
	specs := []providerChoice{
		{
			Value:       config.AIBackendUnset,
			Label:       "Auto",
			Summary:     "Leaves /boss unconfigured until you choose a chat backend.",
			Description: "If the shared OpenAI API connection already exists, /boss can use it automatically. No OpenAI key is required unless you choose OpenAI API.",
		},
		{
			Value:       config.AIBackendOpenAIAPI,
			Label:       "OpenAI API",
			Summary:     "Lets /boss answer through direct OpenAI API inference.",
			Description: "A saved OpenAI API key is required. Project reports can still use Codex, OpenCode, Claude Code, MLX, Ollama, or another provider.",
		},
		{
			Value:       config.AIBackendMLX,
			Label:       "MLX",
			Summary:     "Lets /boss answer through your local MLX OpenAI-compatible endpoint.",
			Description: "The MLX endpoint must be running. It uses the shared MLX endpoint, API key, and model fields.",
		},
		{
			Value:       config.AIBackendOllama,
			Label:       "Ollama",
			Summary:     "Lets /boss answer through your local Ollama OpenAI-compatible endpoint.",
			Description: "The Ollama endpoint must be running. It uses the shared Ollama endpoint, API key, and model fields.",
		},
		{
			Value:       config.AIBackendDisabled,
			Label:       "Off",
			Summary:     "Keeps /boss from answering with AI.",
			Description: "No provider setup is needed. The rest of the TUI and project-report inference keep working.",
		},
	}
	for i := range specs {
		optionSettings := settings
		optionSettings.BossChatBackend = specs[i].Value
		card := m.bossChatStatusCard(optionSettings)
		specs[i].State = card.State
		specs[i].StateStyle = card.StateStyle
		specs[i].Detail = strings.TrimSpace(card.Detail)
		specs[i].NextStep = bossChatProviderNextStep(specs[i], settings)
	}
	return specs
}

func (m Model) projectReportsProviderDetail(backend config.AIBackend, status aibackend.Status) string {
	if isLocalBackendModelPickerBackend(backend) && status.Ready {
		if detail := m.localProviderChoiceDetail(backend, status); detail != "" {
			return detail
		}
	}
	return providerChoiceStatusDetail(status, "Availability will refresh in the background.")
}

func (m Model) localProviderChoiceDetail(backend config.AIBackend, status aibackend.Status) string {
	models := localBackendPickerModels(status.Models)
	if len(models) == 0 {
		return ""
	}
	selectedModel := strings.TrimSpace(m.currentSettingsBaseline().OpenAICompatibleModel(backend))
	endpoint := strings.TrimSpace(status.Endpoint)
	if selectedModel != "" {
		if localBackendModelExists(selectedModel, models) {
			if endpoint != "" {
				return "using " + selectedModel + " @ " + endpoint
			}
			return "using " + selectedModel
		}
		return "configured " + selectedModel + " (server offers " + summarizeLocalBackendModels(models) + ")"
	}
	if endpoint != "" {
		return "auto " + firstString(models) + " @ " + endpoint
	}
	return "auto " + firstString(models)
}

func projectReportsProviderNextStep(backend config.AIBackend, status aibackend.Status, known bool) string {
	switch {
	case backend == config.AIBackendDisabled:
		return "Save to keep background AI off."
	case status.Ready:
		return "Save to use this for project reports."
	case backend == config.AIBackendOpenAIAPI:
		return "Paste and save an OpenAI API key."
	case !known:
		return "Refresh availability, then save if this is the provider you want."
	case !status.Installed && backend.RequiresCLIInstallHint():
		return "Install and sign in to " + backend.Label() + "."
	case status.LoginHint != "":
		return strings.TrimSpace(status.LoginHint)
	default:
		return "Finish setup for " + backend.Label() + ", then refresh."
	}
}

func bossChatProviderNextStep(choice providerChoice, settings config.EditableSettings) string {
	switch choice.Value {
	case config.AIBackendUnset:
		if strings.TrimSpace(settings.OpenAIAPIKey) != "" {
			return "Save to let boss chat use the shared OpenAI API connection automatically."
		}
		return "Choose a boss chat backend, or save to leave /boss unconfigured."
	case config.AIBackendDisabled:
		return "Save to keep boss chat off."
	case config.AIBackendOpenAIAPI:
		if strings.TrimSpace(settings.OpenAIAPIKey) == "" {
			return "Paste and save an OpenAI API key."
		}
		return "Save to use OpenAI API for boss chat."
	case config.AIBackendMLX, config.AIBackendOllama:
		if choice.State == "ready" {
			return "Save to use this local backend for boss chat."
		}
		return "Start or configure the " + choice.Label + " local endpoint, then refresh."
	default:
		return "Choose a supported boss chat backend."
	}
}

func providerChoiceStatusDetail(status aibackend.Status, fallback string) string {
	detail := strings.TrimSpace(status.Detail)
	if status.LoginHint != "" && !status.Ready {
		detail = strings.TrimSpace(status.LoginHint)
	}
	if detail == "" {
		detail = fallback
	}
	return detail
}

func providerChoiceSelection(options []providerChoice, current config.AIBackend) int {
	for i, option := range options {
		if option.Value == current {
			return i
		}
	}
	return 0
}

func providerChoiceLabel(options []providerChoice, current config.AIBackend, fallback string) string {
	for _, option := range options {
		if option.Value == current {
			return option.Label
		}
	}
	return fallback
}

func renderProviderChoiceStatus(choice providerChoice) string {
	detail := strings.TrimSpace(choice.Detail)
	if detail == "" {
		detail = "Availability will refresh in the background."
	}
	return choice.StateStyle.Render(choice.State) + detailMutedStyle.Render(" - "+detail)
}

func renderProviderChoiceRow(choice providerChoice, selected, current bool, width int) string {
	labelStyle := detailValueStyle.Bold(true)
	if selected {
		labelStyle = labelStyle.Foreground(lipgloss.Color("230"))
	}
	markerStyle := commandPaletteHintStyle
	if selected {
		markerStyle = commandPalettePickStyle
	}
	marker := markerStyle.Render(" ")
	if selected {
		marker = markerStyle.Render("›")
	}
	labelWidth := min(24, max(12, width/2))
	stateWidth := 12
	row := marker + " " +
		labelStyle.Width(labelWidth).Render(truncateText(choice.Label, labelWidth)) +
		choice.StateStyle.Width(stateWidth).Render(truncateText(choice.State, stateWidth))
	if current {
		row += "  " + detailMutedStyle.Render("(current)")
	}
	row = fitFooterWidth(row, width)
	if selected {
		return projectListSelectedRowStyle.Width(width).Render(row)
	}
	return lipgloss.NewStyle().Width(width).Render(row)
}

func renderProviderChoicePickerContent(title string, currentLabel string, options []providerChoice, selectedIndex int, current config.AIBackend, width int) string {
	lines := []string{
		commandPaletteTitleStyle.Render(title),
		renderDialogAction("Up/Down", "move", navigateActionKeyStyle, navigateActionTextStyle) + "   " +
			renderDialogAction("Enter", "choose", commitActionKeyStyle, commitActionTextStyle) + "   " +
			renderDialogAction("Esc", "close", cancelActionKeyStyle, cancelActionTextStyle),
	}
	lines = append(lines, detailField("Current", detailValueStyle.Render(currentLabel)))
	lines = append(lines, "")

	if len(options) == 0 {
		lines = append(lines, commandPaletteHintStyle.Render("No provider choices are available right now."))
		return strings.Join(lines, "\n")
	}

	selectedIndex = wrapIndex(selectedIndex, len(options))
	for i, option := range options {
		lines = append(lines, renderProviderChoiceRow(option, i == selectedIndex, option.Value == current, width))
	}
	lines = append(lines, "")
	lines = append(lines, renderProviderChoiceDetail(options[selectedIndex], width))
	return strings.Join(lines, "\n")
}

func renderProviderChoiceDetail(choice providerChoice, width int) string {
	lines := []string{detailSectionStyle.Render("Selected Helper")}
	lines = append(lines, renderWrappedDetailField("Will do", detailValueStyle, width, choice.Summary))
	lines = append(lines, renderWrappedDetailField("Needs", detailValueStyle, width, choice.Description))
	lines = append(lines, detailField("Readiness", renderProviderChoiceStatus(choice)))
	lines = append(lines, renderWrappedDetailField("After choosing", detailValueStyle, width, choice.NextStep))
	return strings.Join(lines, "\n")
}
