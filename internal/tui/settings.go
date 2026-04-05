package tui

import (
	"fmt"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"lcroom/internal/config"

	"github.com/charmbracelet/bubbles/textinput"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
)

const (
	settingsFieldOpenAIAPIKey = iota
	settingsFieldMLXBaseURL
	settingsFieldMLXAPIKey
	settingsFieldOllamaBaseURL
	settingsFieldOllamaAPIKey
	settingsFieldIncludePaths
	settingsFieldExcludePaths
	settingsFieldExcludeProjectPatterns
	settingsFieldPrivacyPatterns
	settingsFieldCodexLaunchPreset
	settingsFieldHideReasoningSections
	settingsFieldActiveThreshold
	settingsFieldStuckThreshold
	settingsFieldInterval
)

type settingsField struct {
	label     string
	hint      string
	input     textinput.Model
	sensitive bool
}

const settingsHintMaxLines = 2

// invertBoolString flips "true"→"false" and vice versa, used when the UI label
// has opposite polarity from the internal config key (e.g. "Show reasoning" vs HideReasoningSections).
func invertBoolString(s string) string {
	if strings.EqualFold(strings.TrimSpace(s), "true") {
		return "false"
	}
	return "true"
}

func (m *Model) openSettingsMode() tea.Cmd {
	return m.openSettingsModeWithBaseline(m.currentSettingsBaseline())
}

func (m *Model) openSettingsModeWithBaseline(settings config.EditableSettings) tea.Cmd {
	m.settingsFields = newSettingsFields(settings)
	saved := cloneEditableSettings(settings)
	m.settingsBaseline = &saved
	m.settingsMode = true
	m.settingsSaving = false
	m.settingsRevealPrivacy = false
	m.setupMode = false
	m.commandMode = false
	m.showHelp = false
	m.err = nil
	m.status = "Editing settings. Enter to save, Esc to cancel"
	return m.setSettingsSelection(0)
}

func (m *Model) closeSettingsMode(status string) {
	m.blurSettingsFields()
	m.settingsMode = false
	m.settingsSaving = false
	if status != "" {
		m.status = status
	}
}

func (m Model) updateSettingsMode(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	if m.settingsSaving {
		return m, nil
	}
	switch msg.String() {
	case "esc":
		m.closeSettingsMode("Settings edit canceled")
		return m, nil
	case "tab", "down", "ctrl+n":
		return m, m.moveSettingsSelection(1)
	case "shift+tab", "up", "ctrl+p":
		return m, m.moveSettingsSelection(-1)
	case "enter":
		settings, err := config.ParseEditableSettings(
			m.currentSettingsBaseline().AIBackend,
			m.settingsFieldValue(settingsFieldOpenAIAPIKey),
			m.settingsFieldValue(settingsFieldMLXBaseURL),
			m.settingsFieldValue(settingsFieldMLXAPIKey),
			m.settingsFieldValue(settingsFieldOllamaBaseURL),
			m.settingsFieldValue(settingsFieldOllamaAPIKey),
			m.settingsFieldValue(settingsFieldIncludePaths),
			m.settingsFieldValue(settingsFieldExcludePaths),
			m.settingsFieldValue(settingsFieldExcludeProjectPatterns),
			m.settingsFieldValue(settingsFieldPrivacyPatterns),
			m.settingsFieldValue(settingsFieldCodexLaunchPreset),
			invertBoolString(m.settingsFieldValue(settingsFieldHideReasoningSections)),
			strconv.FormatBool(m.currentSettingsBaseline().PrivacyMode),
			m.currentSettingsBaseline().OpenCodeModelTier,
			m.settingsFieldValue(settingsFieldActiveThreshold),
			m.settingsFieldValue(settingsFieldStuckThreshold),
			m.settingsFieldValue(settingsFieldInterval),
		)
		if err != nil {
			m.err = nil
			m.status = err.Error()
			return m, nil
		}
		applyEmbeddedModelPreferencesToSettings(&settings, embeddedModelPreferencesFromSettings(m.currentSettingsBaseline()))
		m.err = nil
		m.settingsSaving = true
		m.status = "Saving settings..."
		return m, m.saveSettingsCmd(settings)
	case "ctrl+r":
		if m.settingsSelected == settingsFieldPrivacyPatterns {
			m.settingsRevealPrivacy = !m.settingsRevealPrivacy
			m.syncPrivacyPatternsReveal()
			if m.settingsRevealPrivacy {
				m.status = "Privacy patterns revealed"
			} else {
				m.status = "Privacy patterns hidden"
			}
			return m, nil
		}
	}

	if m.settingsSelected < 0 || m.settingsSelected >= len(m.settingsFields) {
		return m, nil
	}
	input, cmd := m.settingsFields[m.settingsSelected].input.Update(msg)
	m.settingsFields[m.settingsSelected].input = input
	return m, cmd
}

func (m *Model) moveSettingsSelection(delta int) tea.Cmd {
	if len(m.settingsFields) == 0 || delta == 0 {
		return nil
	}
	index := m.settingsSelected + delta
	if index < 0 {
		index = len(m.settingsFields) - 1
	}
	if index >= len(m.settingsFields) {
		index = 0
	}
	return m.setSettingsSelection(index)
}

func (m *Model) setSettingsSelection(index int) tea.Cmd {
	if len(m.settingsFields) == 0 {
		m.settingsSelected = 0
		return nil
	}
	if index < 0 {
		index = 0
	}
	if index >= len(m.settingsFields) {
		index = len(m.settingsFields) - 1
	}

	m.settingsSelected = index
	cmds := make([]tea.Cmd, 0, 1)
	for i := range m.settingsFields {
		if i == index {
			m.settingsFields[i].input.CursorEnd()
			cmds = append(cmds, m.settingsFields[i].input.Focus())
			continue
		}
		m.settingsFields[i].input.Blur()
	}
	return tea.Batch(cmds...)
}

func (m *Model) blurSettingsFields() {
	for i := range m.settingsFields {
		m.settingsFields[i].input.Blur()
	}
}

func (m *Model) syncPrivacyPatternsReveal() {
	if len(m.settingsFields) <= settingsFieldPrivacyPatterns {
		return
	}
	field := &m.settingsFields[settingsFieldPrivacyPatterns]
	if m.settingsRevealPrivacy {
		field.input.EchoMode = textinput.EchoNormal
		field.input.EchoCharacter = ' '
	} else {
		field.input.EchoMode = textinput.EchoPassword
		field.input.EchoCharacter = '*'
	}
}

func (m Model) saveSettingsCmd(settings config.EditableSettings) tea.Cmd {
	path := m.currentConfigPath()
	return func() tea.Msg {
		err := config.SaveEditableSettings(path, settings)
		return settingsSavedMsg{settings: settings, path: path, err: err}
	}
}

func (m Model) saveEmbeddedModelPreferencesCmd() tea.Cmd {
	baseline := m.currentSettingsBaseline()
	settings := baseline
	applyEmbeddedModelPreferencesToSettings(&settings, m.embeddedModelPrefs)
	settings.RecentCodexModels = append([]string(nil), m.recentCodexModels...)
	settings.RecentClaudeModels = append([]string(nil), m.recentClaudeModels...)
	settings.RecentOpenCodeModels = append([]string(nil), m.recentOpenCodeModels...)
	if embeddedModelSettingsEqual(baseline, settings) {
		return nil
	}
	path := m.currentConfigPath()
	return func() tea.Msg {
		err := config.SaveEditableSettings(path, settings)
		return embeddedModelPreferencesSavedMsg{settings: settings, path: path, err: err}
	}
}

func (m Model) savePrivacyModeCmd(privacyMode bool) tea.Cmd {
	settings := m.currentSettingsBaseline()
	settings.PrivacyMode = privacyMode
	path := m.currentConfigPath()
	return func() tea.Msg {
		err := config.SaveEditableSettings(path, settings)
		return privacyModeSavedMsg{privacyMode: privacyMode, path: path, err: err}
	}
}

func (m Model) currentSettingsBaseline() config.EditableSettings {
	if m.settingsBaseline != nil {
		return cloneEditableSettings(*m.settingsBaseline)
	}
	if m.svc != nil {
		return config.EditableSettingsFromAppConfig(m.svc.Config())
	}
	return config.EditableSettingsFromAppConfig(config.Default())
}

func (m Model) currentConfigPath() string {
	if m.svc != nil {
		return m.svc.Config().ConfigPath
	}
	return config.Default().ConfigPath
}

func displayPathWithHomeTilde(path, homeDir string) string {
	path = strings.TrimSpace(path)
	if path == "" {
		return ""
	}
	home := strings.TrimSpace(homeDir)
	if home == "" {
		return path
	}
	home = filepath.Clean(home)
	path = filepath.Clean(path)
	if path == home {
		return "~"
	}
	prefix := home + string(filepath.Separator)
	if strings.HasPrefix(path, prefix) {
		return "~/" + strings.TrimPrefix(path, prefix)
	}
	return path
}

func (m Model) displayPathWithHomeTilde(path string) string {
	return displayPathWithHomeTilde(path, m.homeDir)
}

func (m Model) settingsFieldValue(index int) string {
	if index < 0 || index >= len(m.settingsFields) {
		return ""
	}
	return strings.TrimSpace(m.settingsFields[index].input.Value())
}

func (m Model) renderSettingsOverlay(body string, bodyW, bodyH int) string {
	panel := m.renderSettingsPanel(bodyW, bodyH)
	panelWidth := lipgloss.Width(panel)
	panelHeight := lipgloss.Height(panel)
	left := max(0, (bodyW-panelWidth)/2)
	top := max(0, (bodyH-panelHeight)/4)
	return overlayBlock(body, panel, bodyW, bodyH, left, top)
}

func (m Model) renderSettingsPanel(bodyW, bodyH int) string {
	panelWidth := min(bodyW, min(max(64, bodyW-10), 104))
	panelInnerWidth := max(28, panelWidth-4)
	maxContentHeight := max(10, bodyH-2)
	return renderDialogPanel(panelWidth, panelInnerWidth, m.renderSettingsContent(panelInnerWidth, maxContentHeight))
}

func (m Model) renderSettingsContent(width, maxHeight int) string {
	lines := []string{
		commandPaletteTitleStyle.Render("Settings"),
		commandPaletteHintStyle.Render("Config: " + truncateText(m.displayPathWithHomeTilde(m.currentConfigPath()), max(20, width-8))),
	}
	lines = append(lines, commandPaletteHintStyle.Render(fmt.Sprintf("AI backend: %s. Use /setup to change it. Scope, API keys, and local endpoint overrides save here.", m.currentSettingsBaseline().AIBackend.Label())))
	if m.settingsSaving {
		lines = append(lines, "")
		lines = append(lines, commandPaletteHintStyle.Render("Saving settings..."))
	}

	labelWidth := m.settingsLabelWidth(width)
	inputWidth := max(10, width-labelWidth-1)
	start, end := m.settingsVisibleWindow(m.settingsVisibleFieldCount(maxHeight))
	if start > 0 {
		lines = append(lines, commandPaletteHintStyle.Render(fmt.Sprintf("↑ %d above", start)))
	}
	for i := start; i < end; i++ {
		lines = append(lines, m.renderSettingsFieldRow(m.settingsFields[i], i == m.settingsSelected, labelWidth, inputWidth))
	}
	if end < len(m.settingsFields) {
		lines = append(lines, commandPaletteHintStyle.Render(fmt.Sprintf("↓ %d below", len(m.settingsFields)-end)))
	}

	if hint := m.renderSelectedSettingsHint(width); hint != "" {
		lines = append(lines, "")
		lines = append(lines, hint)
	}

	lines = append(lines, "")
	lines = append(lines, renderSettingsActions())
	return strings.Join(lines, "\n")
}

func (m Model) settingsVisibleFieldCount(maxHeight int) int {
	total := len(m.settingsFields)
	if total == 0 {
		return 0
	}

	// Header (3), hint block (blank + up to 2 lines), actions (blank + 1),
	// plus up to 2 scroll indicators when the field list is windowed.
	reserved := 10
	visible := maxHeight - reserved
	if visible < 1 {
		visible = 1
	}
	if visible > total {
		visible = total
	}
	return visible
}

func (m Model) settingsVisibleWindow(limit int) (int, int) {
	total := len(m.settingsFields)
	if total == 0 || limit <= 0 || total <= limit {
		return 0, total
	}

	start := m.settingsSelected - limit/2
	if start < 0 {
		start = 0
	}
	maxStart := total - limit
	if start > maxStart {
		start = maxStart
	}
	return start, start + limit
}

func (m Model) settingsLabelWidth(width int) int {
	widest := 0
	for _, field := range m.settingsFields {
		widest = max(widest, lipgloss.Width(field.label)+2)
	}
	maxAllowed := max(14, width-12)
	return min(maxAllowed, max(18, widest))
}

func (m Model) renderSettingsFieldRow(field settingsField, selected bool, labelWidth, inputWidth int) string {
	label := "  " + field.label
	labelStyle := detailLabelStyle
	if selected {
		label = "> " + field.label
		labelStyle = commandPalettePickStyle
	}
	label = truncateText(label, labelWidth)

	input := field.input
	input.Width = inputWidth
	return labelStyle.Width(labelWidth).Render(label) + " " + input.View()
}

func (m Model) renderSelectedSettingsHint(width int) string {
	if m.settingsSelected < 0 || m.settingsSelected >= len(m.settingsFields) {
		return ""
	}

	hintWidth := max(18, width)
	wrapped := lipgloss.NewStyle().Width(hintWidth).Render("Hint: " + m.settingsFieldHint(m.settingsSelected))
	lines := strings.Split(wrapped, "\n")
	for i, line := range lines {
		lines[i] = strings.TrimRight(line, " ")
	}
	if len(lines) > settingsHintMaxLines {
		lines = lines[:settingsHintMaxLines]
		lines[len(lines)-1] = truncateText(strings.TrimSpace(lines[len(lines)-1]), hintWidth-1)
	}
	return commandPaletteHintStyle.Render(strings.Join(lines, "\n"))
}

func renderSettingsActions() string {
	actions := []string{
		renderDialogAction("Enter", "save", commitActionKeyStyle, commitActionTextStyle),
		renderDialogAction("Tab", "next", navigateActionKeyStyle, navigateActionTextStyle),
		renderDialogAction("Up/Down", "choose", pushActionKeyStyle, pushActionTextStyle),
		renderDialogAction("Esc", "cancel", cancelActionKeyStyle, cancelActionTextStyle),
	}
	return strings.Join(actions, "   ")
}

func newSettingsFields(settings config.EditableSettings) []settingsField {
	return []settingsField{
		newSensitiveSettingsField(
			"OpenAI API key",
			"Used when the AI backend is OpenAI API key. Leave blank if you plan to use Codex, Claude Code, or OpenCode instead.",
			settings.OpenAIAPIKey,
			512,
		),
		newSettingsField(
			"MLX base URL",
			"Used when the AI backend is MLX. Leave blank to use the default local OpenAI-compatible URL: http://127.0.0.1:8080/v1",
			settings.MLXBaseURL,
			512,
		),
		newSensitiveSettingsFieldWithPlaceholder(
			"MLX API key",
			"Optional API key for the MLX OpenAI-compatible endpoint. Leave blank to use the default value: mlx",
			settings.MLXAPIKey,
			512,
			"Leave blank for mlx",
		),
		newSettingsField(
			"Ollama base URL",
			"Used when the AI backend is Ollama. Leave blank to use the default OpenAI-compatible URL: http://127.0.0.1:11434/v1",
			settings.OllamaBaseURL,
			512,
		),
		newSensitiveSettingsFieldWithPlaceholder(
			"Ollama API key",
			"Optional API key for the Ollama OpenAI-compatible endpoint. Leave blank to use the default value: ollama",
			settings.OllamaAPIKey,
			512,
			"Leave blank for ollama",
		),
		newSettingsField(
			"Include paths",
			"Optional comma-separated path prefixes to keep in scope. Leave blank for all detected paths.",
			strings.Join(settings.IncludePaths, ","),
			2048,
		),
		newSettingsField(
			"Exclude paths",
			"Optional comma-separated path prefixes to hide. Example: /Users/alice/tmp,/Users/alice/archive",
			strings.Join(settings.ExcludePaths, ","),
			2048,
		),
		newSettingsField(
			"Exclude project names",
			"Optional comma-separated name patterns to hide. Supports '*' wildcards. Example: client-*,secret-demo",
			strings.Join(settings.ExcludeProjectPatterns, ","),
			2048,
		),
		newPrivacyPatternsField(
			"Privacy patterns",
			"Comma-separated patterns to hide in demo mode. Supports '*' wildcards. Example: *medical*,*personal*",
			strings.Join(settings.PrivacyPatterns, ","),
			2048,
		),
		newSettingsField(
			"Codex launch mode",
			"Accepted values: yolo, full-auto, safe. YOLO is the default. Anything else adds a lot more approval prompts and user interaction; safe is the most interruption-heavy.",
			string(settings.CodexLaunchPreset),
			24,
		),
		newSettingsField(
			"Show reasoning",
			"Accepted values: true, false. When true, shows model reasoning/thinking sections in the embedded transcript. Default: false.",
			strconv.FormatBool(!settings.HideReasoningSections),
			8,
		),
		newSettingsField(
			"Active threshold",
			"Projects newer than this count as active. Example: 20m",
			formatSettingsDuration(settings.ActiveThreshold),
			24,
		),
		newSettingsField(
			"Stuck threshold",
			"Must be greater than active threshold. Example: 4h",
			formatSettingsDuration(settings.StuckThreshold),
			24,
		),
		newSettingsField(
			"Scan interval",
			"Background refresh interval. Example: 60s",
			formatSettingsDuration(settings.ScanInterval),
			24,
		),
	}
}

func newSettingsField(label, hint, value string, charLimit int) settingsField {
	input := textinput.New()
	input.Prompt = ""
	input.SetValue(value)
	input.CharLimit = charLimit
	return settingsField{
		label: label,
		hint:  hint,
		input: input,
	}
}

func newSensitiveSettingsField(label, hint, value string, charLimit int) settingsField {
	return newSensitiveSettingsFieldWithPlaceholder(label, hint, value, charLimit, "Paste OpenAI API key")
}

func newSensitiveSettingsFieldWithPlaceholder(label, hint, value string, charLimit int, placeholder string) settingsField {
	field := newSettingsField(label, hint, value, charLimit)
	field.input.EchoMode = textinput.EchoPassword
	field.input.EchoCharacter = '*'
	field.input.Placeholder = placeholder
	field.sensitive = true
	return field
}

func newPrivacyPatternsField(label, hint, value string, charLimit int) settingsField {
	field := newSettingsField(label, hint, value, charLimit)
	field.input.EchoMode = textinput.EchoPassword
	field.input.EchoCharacter = '*'
	field.input.Placeholder = "e.g., *medical*,*personal*"
	field.sensitive = true
	return field
}

func cloneEditableSettings(settings config.EditableSettings) config.EditableSettings {
	settings.AIBackend = config.ResolveAIBackend(settings.AIBackend, settings.OpenAIAPIKey)
	settings.OpenAIAPIKey = strings.TrimSpace(settings.OpenAIAPIKey)
	settings.MLXBaseURL = strings.TrimSpace(settings.MLXBaseURL)
	settings.MLXAPIKey = strings.TrimSpace(settings.MLXAPIKey)
	settings.OllamaBaseURL = strings.TrimSpace(settings.OllamaBaseURL)
	settings.OllamaAPIKey = strings.TrimSpace(settings.OllamaAPIKey)
	settings.IncludePaths = append([]string(nil), settings.IncludePaths...)
	settings.ExcludePaths = append([]string(nil), settings.ExcludePaths...)
	settings.ExcludeProjectPatterns = append([]string(nil), settings.ExcludeProjectPatterns...)
	settings.PrivacyPatterns = append([]string(nil), settings.PrivacyPatterns...)
	settings.RecentCodexModels = append([]string(nil), settings.RecentCodexModels...)
	settings.RecentClaudeModels = append([]string(nil), settings.RecentClaudeModels...)
	settings.RecentOpenCodeModels = append([]string(nil), settings.RecentOpenCodeModels...)
	return settings
}

func (m Model) settingsFieldHint(index int) string {
	if index < 0 || index >= len(m.settingsFields) {
		return ""
	}
	field := m.settingsFields[index]
	switch index {
	case settingsFieldOpenAIAPIKey:
		if suffix := maskedOpenAIKeySuffix(field.input.Value()); suffix != "" {
			return "Used for the OpenAI API backend. Stored key ends with " + suffix + "."
		}
		if m.currentSettingsBaseline().AIBackend == config.AIBackendOpenAIAPI {
			return field.hint + " The selected backend still needs a saved key."
		}
		return field.hint
	case settingsFieldMLXBaseURL:
		return field.hint
	case settingsFieldMLXAPIKey:
		if suffix := maskedOpenAIKeySuffix(field.input.Value()); suffix != "" {
			return "Used for the MLX backend. Stored key ends with " + suffix + ". Leave blank to use mlx."
		}
		return field.hint
	case settingsFieldOllamaBaseURL:
		return field.hint
	case settingsFieldOllamaAPIKey:
		if suffix := maskedOpenAIKeySuffix(field.input.Value()); suffix != "" {
			return "Used for the Ollama backend. Stored key ends with " + suffix + ". Leave blank to use ollama."
		}
		return field.hint
	case settingsFieldPrivacyPatterns:
		hint := field.hint
		if m.settingsRevealPrivacy {
			hint += " (revealed - press Ctrl+R to hide)"
		} else {
			hint += " (hidden - press Ctrl+R to reveal)"
		}
		return hint
	default:
		return field.hint
	}
}

func maskedOpenAIKeySuffix(raw string) string {
	trimmed := strings.TrimSpace(raw)
	if trimmed == "" {
		return ""
	}
	runes := []rune(trimmed)
	if len(runes) > 5 {
		runes = runes[len(runes)-5:]
	}
	return "..." + string(runes)
}

func formatSettingsDuration(d time.Duration) string {
	switch {
	case d == 0:
		return "0s"
	case d%time.Hour == 0:
		return fmt.Sprintf("%dh", int(d/time.Hour))
	case d%time.Minute == 0:
		return fmt.Sprintf("%dm", int(d/time.Minute))
	case d%time.Second == 0:
		return fmt.Sprintf("%ds", int(d/time.Second))
	default:
		return d.String()
	}
}
