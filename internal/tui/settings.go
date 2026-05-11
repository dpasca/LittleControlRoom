package tui

import (
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"time"

	"lcroom/internal/browserctl"
	"lcroom/internal/codexapp"
	"lcroom/internal/config"

	"github.com/charmbracelet/bubbles/textinput"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
)

const (
	settingsFieldOpenAIAPIKey = iota
	settingsFieldBossChatBackend
	settingsFieldBossChatModel
	settingsFieldBossUtilityModel
	settingsFieldMLXBaseURL
	settingsFieldMLXAPIKey
	settingsFieldMLXModel
	settingsFieldOllamaBaseURL
	settingsFieldOllamaAPIKey
	settingsFieldOllamaModel
	settingsFieldLCAgentPath
	settingsFieldLCAgentEnvFile
	settingsFieldLCAgentProvider
	settingsFieldLCAgentAuto
	settingsFieldLCAgentToolProfile
	settingsFieldLCAgentContextProfile
	settingsFieldLCAgentRequestTimeout
	settingsFieldIncludePaths
	settingsFieldExcludePaths
	settingsFieldExcludeProjectPatterns
	settingsFieldPrivacyPatterns
	settingsFieldCodexLaunchPreset
	settingsFieldBrowserAutomation
	settingsFieldHideReasoningSections
	settingsFieldActiveThreshold
	settingsFieldStuckThreshold
	settingsFieldInterval
	settingsFieldAIBackend
)

type settingsSectionID string

const (
	settingsSectionGettingStarted settingsSectionID = "getting-started"
	settingsSectionAI             settingsSectionID = "ai"
	settingsSectionScope          settingsSectionID = "scope"
	settingsSectionBrowser        settingsSectionID = "browser"
	settingsSectionAdvanced       settingsSectionID = "advanced"
)

const settingsConfigIssueStatus = "LCAgent env file warning"

type settingsSection struct {
	id         settingsSectionID
	label      string
	hint       string
	fieldOrder []int
}

type settingsField struct {
	label     string
	hint      string
	input     textinput.Model
	sensitive bool
	section   settingsSectionID
}

type settingsBrowserAutomationOption struct {
	Value       string
	Label       string
	Summary     string
	Description string
}

const settingsHintMaxLines = 2

const (
	settingsBrowserAutomationOnlyWhenNeeded = "only-when-needed"
	settingsBrowserAutomationAlwaysShow     = "always-show"
	settingsBrowserAutomationClassic        = "classic-browser-behavior"
	settingsBrowserAutomationCustom         = "use-config-file-as-is"
)

var (
	settingsClassicPlaywrightPolicy = browserctl.Policy{
		ManagementMode:     browserctl.ManagementModeLegacy,
		DefaultBrowserMode: browserctl.BrowserModeHeadless,
		LoginMode:          browserctl.LoginModeManual,
		IsolationScope:     browserctl.IsolationScopeTask,
	}
	settingsAutomaticPlaywrightPolicy = browserctl.Policy{
		ManagementMode:     browserctl.ManagementModeManaged,
		DefaultBrowserMode: browserctl.BrowserModeHeadless,
		LoginMode:          browserctl.LoginModePromote,
		IsolationScope:     browserctl.IsolationScopeTask,
	}
	settingsAlwaysShowPlaywrightPolicy = browserctl.Policy{
		ManagementMode:     browserctl.ManagementModeManaged,
		DefaultBrowserMode: browserctl.BrowserModeHeaded,
		LoginMode:          browserctl.LoginModePromote,
		IsolationScope:     browserctl.IsolationScopeTask,
	}
	settingsObservePlaywrightPolicy = browserctl.Policy{
		ManagementMode:     browserctl.ManagementModeObserve,
		DefaultBrowserMode: browserctl.BrowserModeHeadless,
		LoginMode:          browserctl.LoginModeManual,
		IsolationScope:     browserctl.IsolationScopeTask,
	}
)

// invertBoolString flips "true"→"false" and vice versa, used when the UI label
// has opposite polarity from the internal config key (e.g. "Show reasoning" vs HideReasoningSections).
func invertBoolString(s string) string {
	if strings.EqualFold(strings.TrimSpace(s), "true") {
		return "false"
	}
	return "true"
}

func settingsSections() []settingsSection {
	return []settingsSection{
		{
			id:    settingsSectionGettingStarted,
			label: "Getting Started",
			hint:  "I only need two decisions first: who writes project reports, and whether /boss should answer. Everything else can wait.",
			fieldOrder: []int{
				settingsFieldAIBackend,
				settingsFieldOpenAIAPIKey,
				settingsFieldBossChatBackend,
				settingsFieldIncludePaths,
			},
		},
		{
			id:    settingsSectionAI,
			label: "AI & Models",
			hint:  "Project-analysis backend credentials, boss-chat inference, local model overrides, and embedded assistant launch defaults.",
			fieldOrder: []int{
				settingsFieldBossChatModel,
				settingsFieldBossUtilityModel,
				settingsFieldMLXBaseURL,
				settingsFieldMLXAPIKey,
				settingsFieldMLXModel,
				settingsFieldOllamaBaseURL,
				settingsFieldOllamaAPIKey,
				settingsFieldOllamaModel,
				settingsFieldCodexLaunchPreset,
				settingsFieldHideReasoningSections,
			},
		},
		{
			id:    settingsSectionScope,
			label: "Project Scope",
			hint:  "Choose which folders stay visible and which names should stay hidden or masked in demos.",
			fieldOrder: []int{
				settingsFieldExcludePaths,
				settingsFieldExcludeProjectPatterns,
				settingsFieldPrivacyPatterns,
			},
		},
		{
			id:    settingsSectionBrowser,
			label: "Browser",
			hint:  "Keep browser work out of the way by default, or choose to always show browser windows when you prefer to watch every step.",
			fieldOrder: []int{
				settingsFieldBrowserAutomation,
			},
		},
		{
			id:    settingsSectionAdvanced,
			label: "Advanced",
			hint:  "Experimental features and tuning knobs. Most users can leave these alone.",
			fieldOrder: []int{
				settingsFieldLCAgentPath,
				settingsFieldLCAgentEnvFile,
				settingsFieldLCAgentProvider,
				settingsFieldLCAgentAuto,
				settingsFieldLCAgentToolProfile,
				settingsFieldLCAgentContextProfile,
				settingsFieldLCAgentRequestTimeout,
				settingsFieldActiveThreshold,
				settingsFieldStuckThreshold,
				settingsFieldInterval,
			},
		},
	}
}

func settingsSectionIndexForField(index int) int {
	for sectionIndex, section := range settingsSections() {
		for _, fieldIndex := range section.fieldOrder {
			if fieldIndex == index {
				return sectionIndex
			}
		}
	}
	return 0
}

func settingsBrowserAutomationValue(policy browserctl.Policy) string {
	normalized := policy.Normalize()
	switch {
	case normalized.ManagementMode == browserctl.ManagementModeLegacy:
		return settingsBrowserAutomationClassic
	case normalized == settingsAutomaticPlaywrightPolicy:
		return settingsBrowserAutomationOnlyWhenNeeded
	case normalized == settingsAlwaysShowPlaywrightPolicy:
		return settingsBrowserAutomationAlwaysShow
	default:
		return settingsBrowserAutomationCustom
	}
}

func parseSettingsBrowserAutomation(raw string, baseline browserctl.Policy) (browserctl.Policy, error) {
	normalized := normalizeSettingsChoice(raw)
	switch normalized {
	case "", settingsBrowserAutomationClassic, "compatibility", string(browserctl.ManagementModeLegacy):
		return settingsClassicPlaywrightPolicy, nil
	case settingsBrowserAutomationOnlyWhenNeeded, "automatic":
		return settingsAutomaticPlaywrightPolicy, nil
	case settingsBrowserAutomationAlwaysShow:
		return settingsAlwaysShowPlaywrightPolicy, nil
	case "observe":
		return settingsObservePlaywrightPolicy, nil
	case settingsBrowserAutomationCustom, "advanced", "custom", "current":
		return baseline.Normalize(), nil
	default:
		return browserctl.Policy{}, fmt.Errorf("browser windows must be one of: only-when-needed, always-show, classic-browser-behavior, use-config-file-as-is")
	}
}

func settingsBrowserAutomationOptions(baseline browserctl.Policy) []settingsBrowserAutomationOption {
	normalizedBaseline := baseline.Normalize()
	options := []settingsBrowserAutomationOption{
		{
			Value:       settingsBrowserAutomationOnlyWhenNeeded,
			Label:       "Only when needed",
			Summary:     "Keep browser windows hidden until a human step is actually needed.",
			Description: "This is the target low-friction experience for newly opened embedded sessions: background browsing by default, per-task isolation, and a visible browser handoff only when a login or manual step needs your eyes or hands.",
		},
		{
			Value:       settingsBrowserAutomationAlwaysShow,
			Label:       "Always show",
			Summary:     "Open browser windows up front for newly opened embedded sessions.",
			Description: "Use this when you prefer visible browser windows for the full flow instead of letting Little Control Room keep most browser activity in the background. Already-running sessions keep the browser policy they launched with until they are reopened or reconnected.",
		},
		{
			Value:       settingsBrowserAutomationClassic,
			Label:       "Classic browser behavior",
			Summary:     "Fall back to the original provider-owned Playwright behavior.",
			Description: "Use this as the recovery path for newly opened embedded sessions when you want each embedded assistant to keep handling Playwright the old way, without Little Control Room trying to shape the browser flow.",
		},
	}
	if normalizedBaseline != settingsClassicPlaywrightPolicy &&
		normalizedBaseline != settingsAutomaticPlaywrightPolicy &&
		normalizedBaseline != settingsAlwaysShowPlaywrightPolicy {
		options = append(options, settingsBrowserAutomationOption{
			Value:       settingsBrowserAutomationCustom,
			Label:       "Use config file as-is",
			Summary:     "Keep the current raw Playwright policy untouched.",
			Description: "Preserves the current raw browser policy exactly as stored in config.toml. Current raw policy: " + normalizedBaseline.Summary(),
		})
	}
	return options
}

func settingsBrowserAutomationOptionLabel(raw string, baseline browserctl.Policy) string {
	normalized := normalizeSettingsChoice(raw)
	for _, option := range settingsBrowserAutomationOptions(baseline) {
		if option.Value == normalized {
			return option.Label
		}
	}
	if normalized == "" {
		return settingsBrowserAutomationOptions(baseline)[0].Label
	}
	words := strings.Split(strings.ReplaceAll(normalized, "-", " "), " ")
	for i, word := range words {
		if word == "" {
			continue
		}
		runes := []rune(word)
		runes[0] = []rune(strings.ToUpper(string(runes[0])))[0]
		words[i] = string(runes)
	}
	return strings.Join(words, " ")
}

func settingsFieldUsesPicker(index int) bool {
	return index == settingsFieldAIBackend || index == settingsFieldBossChatBackend || index == settingsFieldBrowserAutomation
}

func normalizeSettingsChoice(raw string) string {
	trimmed := strings.ToLower(strings.TrimSpace(raw))
	trimmed = strings.ReplaceAll(trimmed, "_", "-")
	trimmed = strings.ReplaceAll(trimmed, " ", "-")
	return trimmed
}

func (m *Model) openSettingsMode() tea.Cmd {
	cmd := m.openSettingsModeWithBaseline(m.currentSettingsBaseline())
	return tea.Batch(cmd, m.refreshSetupSnapshotCmd(false))
}

func (m *Model) openBrowserSettingsMode() tea.Cmd {
	settings := m.currentSettingsBaseline()
	m.settingsFields = newSettingsFields(settings)
	saved := cloneEditableSettings(settings)
	m.settingsBaseline = &saved
	m.settingsMode = true
	m.settingsSaving = false
	m.settingsRevealPrivacy = false
	m.settingsAIBackendPickerVisible = false
	m.settingsAIBackendPickerSelected = 0
	m.settingsBossChatPickerVisible = false
	m.settingsBossChatPickerSelected = 0
	m.settingsBrowserPickerVisible = false
	m.settingsBrowserPickerSelected = 0
	m.localModelPickerVisible = false
	m.setupMode = false
	m.commandMode = false
	m.showHelp = false
	m.err = nil
	m.status = "Browser settings open. Press Enter to choose automation mode and ctrl+s to save."
	return m.setSettingsSelection(settingsFieldBrowserAutomation)
}

func (m *Model) openSettingsModeWithBaseline(settings config.EditableSettings) tea.Cmd {
	m.settingsFields = newSettingsFields(settings)
	saved := cloneEditableSettings(settings)
	m.settingsBaseline = &saved
	m.settingsMode = true
	m.settingsSaving = false
	m.settingsSectionSelected = 0
	m.settingsRevealPrivacy = false
	m.settingsAIBackendPickerVisible = false
	m.settingsAIBackendPickerSelected = 0
	m.settingsBossChatPickerVisible = false
	m.settingsBossChatPickerSelected = 0
	m.settingsBrowserPickerVisible = false
	m.settingsBrowserPickerSelected = 0
	m.localModelPickerVisible = false
	m.setupMode = false
	m.commandMode = false
	m.showHelp = false
	m.err = nil
	m.status = "Settings open. Getting Started is the short setup guide; Enter chooses pickers, ctrl+s saves, Esc cancels."
	if issue := settingsLocalFileIssue(saved); issue != nil {
		m.appendSettingsConfigIssue(issue)
		m.status = errorStatusWithHint(settingsConfigIssueStatus)
	}
	return m.setSettingsSelection(defaultSettingsSelection())
}

func defaultSettingsSelection() int {
	sections := settingsSections()
	if len(sections) == 0 || len(sections[0].fieldOrder) == 0 {
		return 0
	}
	return sections[0].fieldOrder[0]
}

func (m *Model) closeSettingsMode(status string) {
	m.blurSettingsFields()
	m.settingsMode = false
	m.settingsSaving = false
	m.settingsAIBackendPickerVisible = false
	m.settingsAIBackendPickerSelected = 0
	m.settingsBossChatPickerVisible = false
	m.settingsBossChatPickerSelected = 0
	m.settingsBrowserPickerVisible = false
	m.settingsBrowserPickerSelected = 0
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
	case "pgup", "[", "ctrl+b":
		return m, m.moveSettingsSection(-1)
	case "pgdown", "]", "ctrl+f":
		return m, m.moveSettingsSection(1)
	case "tab", "down", "ctrl+n":
		return m, m.moveSettingsSelection(1)
	case "shift+tab", "up", "ctrl+p":
		return m, m.moveSettingsSelection(-1)
	case "ctrl+s":
		return m.saveSettingsFromFields()
	case "enter":
		if settingsFieldUsesPicker(m.settingsSelected) {
			switch m.settingsSelected {
			case settingsFieldAIBackend:
				return m.openSettingsAIBackendPicker()
			case settingsFieldBossChatBackend:
				return m.openSettingsBossChatBackendPicker()
			case settingsFieldBrowserAutomation:
				return m.openSettingsBrowserAutomationPicker()
			}
		}
		m.status = "Press ctrl+s to save settings."
		return m, nil
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
	if settingsFieldUsesPicker(m.settingsSelected) {
		return m, nil
	}
	input, cmd := m.settingsFields[m.settingsSelected].input.Update(msg)
	m.settingsFields[m.settingsSelected].input = input
	return m, cmd
}

func (m Model) saveSettingsFromFields() (tea.Model, tea.Cmd) {
	playwrightPolicy, err := parseSettingsBrowserAutomation(
		m.settingsFieldValue(settingsFieldBrowserAutomation),
		m.currentSettingsBaseline().PlaywrightPolicy,
	)
	if err != nil {
		m.err = nil
		m.status = err.Error()
		return m, nil
	}
	settings, err := config.ParseEditableSettings(
		config.AIBackend(m.settingsFieldValue(settingsFieldAIBackend)),
		config.AIBackend(m.settingsFieldValue(settingsFieldBossChatBackend)),
		m.settingsFieldValue(settingsFieldOpenAIAPIKey),
		m.settingsFieldValue(settingsFieldBossChatModel),
		m.settingsFieldValue(settingsFieldBossUtilityModel),
		m.settingsFieldValue(settingsFieldMLXBaseURL),
		m.settingsFieldValue(settingsFieldMLXAPIKey),
		m.settingsFieldValue(settingsFieldMLXModel),
		m.settingsFieldValue(settingsFieldOllamaBaseURL),
		m.settingsFieldValue(settingsFieldOllamaAPIKey),
		m.settingsFieldValue(settingsFieldOllamaModel),
		m.settingsFieldValue(settingsFieldIncludePaths),
		m.settingsFieldValue(settingsFieldExcludePaths),
		m.settingsFieldValue(settingsFieldExcludeProjectPatterns),
		m.settingsFieldValue(settingsFieldPrivacyPatterns),
		m.settingsFieldValue(settingsFieldCodexLaunchPreset),
		string(playwrightPolicy.ManagementMode),
		string(playwrightPolicy.DefaultBrowserMode),
		string(playwrightPolicy.LoginMode),
		string(playwrightPolicy.IsolationScope),
		invertBoolString(m.settingsFieldValue(settingsFieldHideReasoningSections)),
		strconv.FormatBool(m.currentSettingsBaseline().PrivacyMode),
		m.currentSettingsBaseline().OpenCodeModelTier,
		m.settingsFieldValue(settingsFieldLCAgentPath),
		m.settingsFieldValue(settingsFieldLCAgentEnvFile),
		m.settingsFieldValue(settingsFieldLCAgentProvider),
		m.settingsFieldValue(settingsFieldLCAgentAuto),
		m.settingsFieldValue(settingsFieldLCAgentToolProfile),
		m.settingsFieldValue(settingsFieldLCAgentContextProfile),
		m.settingsFieldValue(settingsFieldLCAgentRequestTimeout),
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
}

func settingsLocalFileIssue(settings config.EditableSettings) error {
	envFile := strings.TrimSpace(settings.LCAgentEnvFile)
	if envFile == "" {
		return nil
	}
	info, err := os.Stat(envFile)
	if err != nil {
		if os.IsNotExist(err) {
			return fmt.Errorf("LCAgent env file not found: %s", envFile)
		}
		return fmt.Errorf("LCAgent env file cannot be checked: %w", err)
	}
	if info.IsDir() {
		return fmt.Errorf("LCAgent env file is a directory: %s", envFile)
	}
	return nil
}

func (m *Model) appendSettingsConfigIssue(err error) {
	if err == nil {
		return
	}
	message := strings.TrimSpace(err.Error())
	if len(m.errorLogEntries) > 0 {
		latest := m.errorLogEntries[0]
		if latest.Status == settingsConfigIssueStatus && latest.Message == message && latest.ProjectPath == "" {
			return
		}
	}
	m.appendErrorLogEntry(settingsConfigIssueStatus, err, "")
}

func (m *Model) moveSettingsSelection(delta int) tea.Cmd {
	if len(m.settingsFields) == 0 || delta == 0 {
		return nil
	}
	fields := m.activeSettingsSection().fieldOrder
	if len(fields) == 0 {
		return nil
	}
	position := 0
	for i, index := range fields {
		if index == m.settingsSelected {
			position = i
			break
		}
	}
	position += delta
	if position < 0 {
		position = len(fields) - 1
	}
	if position >= len(fields) {
		position = 0
	}
	return m.setSettingsSelection(fields[position])
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
	m.settingsSectionSelected = settingsSectionIndexForField(index)
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

func (m *Model) moveSettingsSection(delta int) tea.Cmd {
	sections := settingsSections()
	if len(sections) == 0 || delta == 0 {
		return nil
	}
	index := m.activeSettingsSectionIndex() + delta
	if index < 0 {
		index = len(sections) - 1
	}
	if index >= len(sections) {
		index = 0
	}
	return m.setSettingsSection(index)
}

func (m *Model) setSettingsSection(index int) tea.Cmd {
	sections := settingsSections()
	if len(sections) == 0 {
		m.settingsSectionSelected = 0
		return nil
	}
	if index < 0 {
		index = 0
	}
	if index >= len(sections) {
		index = len(sections) - 1
	}
	m.settingsSectionSelected = index
	fields := sections[index].fieldOrder
	if len(fields) == 0 {
		return nil
	}
	for _, fieldIndex := range fields {
		if fieldIndex == m.settingsSelected {
			return m.setSettingsSelection(fieldIndex)
		}
	}
	return m.setSettingsSelection(fields[0])
}

func (m Model) activeSettingsSectionIndex() int {
	sections := settingsSections()
	if len(sections) == 0 {
		return 0
	}
	selectedSection := settingsSectionIndexForField(m.settingsSelected)
	if m.settingsSectionSelected < 0 || m.settingsSectionSelected >= len(sections) {
		return selectedSection
	}
	current := sections[m.settingsSectionSelected]
	for _, fieldIndex := range current.fieldOrder {
		if fieldIndex == m.settingsSelected {
			return m.settingsSectionSelected
		}
	}
	return selectedSection
}

func (m Model) activeSettingsSection() settingsSection {
	sections := settingsSections()
	if len(sections) == 0 {
		return settingsSection{}
	}
	return sections[m.activeSettingsSectionIndex()]
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

func (m Model) applyEditableSettingsCmd(settings config.EditableSettings) tea.Cmd {
	if m.svc == nil {
		return nil
	}
	saved := cloneEditableSettings(settings)
	return func() tea.Msg {
		// Apply service-side AI client reconfiguration off the Bubble Tea update
		// path so local backend probing cannot stall the UI thread.
		m.svc.ApplyEditableSettings(saved)
		return editableSettingsAppliedMsg{}
	}
}

func (m Model) saveEmbeddedModelPreferencesCmd() tea.Cmd {
	baseline := m.currentSettingsBaseline()
	settings := baseline
	applyEmbeddedModelPreferencesToSettings(&settings, m.embeddedModelPrefs)
	settings.RecentCodexModels = append([]string(nil), m.recentCodexModels...)
	settings.RecentClaudeModels = append([]string(nil), m.recentClaudeModels...)
	settings.RecentOpenCodeModels = append([]string(nil), m.recentOpenCodeModels...)
	settings.RecentLCAgentModels = append([]string(nil), m.recentLCAgentModels...)
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

func (m Model) settingsDraftForInferenceStatus() config.EditableSettings {
	settings := m.currentSettingsBaseline()
	if len(m.settingsFields) == 0 {
		return settings
	}
	settings.AIBackend = config.AIBackend(m.settingsFieldValue(settingsFieldAIBackend))
	settings.BossChatBackend = config.AIBackend(m.settingsFieldValue(settingsFieldBossChatBackend))
	settings.OpenAIAPIKey = m.settingsFieldValue(settingsFieldOpenAIAPIKey)
	settings.MLXBaseURL = m.settingsFieldValue(settingsFieldMLXBaseURL)
	settings.MLXAPIKey = m.settingsFieldValue(settingsFieldMLXAPIKey)
	settings.MLXModel = m.settingsFieldValue(settingsFieldMLXModel)
	settings.OllamaBaseURL = m.settingsFieldValue(settingsFieldOllamaBaseURL)
	settings.OllamaAPIKey = m.settingsFieldValue(settingsFieldOllamaAPIKey)
	settings.OllamaModel = m.settingsFieldValue(settingsFieldOllamaModel)
	return settings
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
	lines = append(lines, m.renderCompactInferenceSetupSummary(width))
	lines = append(lines, "")
	lines = append(lines, splitLines(m.renderSettingsSectionLayout(width, maxHeight))...)
	if hint := m.renderSelectedSettingsHint(width); hint != "" {
		lines = append(lines, "")
		lines = append(lines, hint)
	}
	lines = append(lines, "")
	lines = append(lines, m.renderSettingsActions())
	return strings.Join(lines, "\n")
}

func (m Model) renderSettingsSectionLayout(width, maxHeight int) string {
	activeSection := m.activeSettingsSection()
	sidebarWidth := settingsSectionSidebarWidth(width)
	separatorWidth := 3
	contentWidth := max(18, width-sidebarWidth-separatorWidth)

	gettingStartedGuide := activeSection.id == settingsSectionGettingStarted && maxHeight >= 20
	mainLines := []string{}
	if gettingStartedGuide {
		mainLines = append(mainLines, m.renderSettingsGettingStartedGuide(contentWidth)...)
	} else {
		mainLines = append(mainLines, m.renderSettingsSectionHint(activeSection, contentWidth))
	}
	if m.settingsSaving {
		mainLines = append(mainLines, "")
		mainLines = append(mainLines, commandPaletteHintStyle.Render("Saving settings..."))
	}

	fieldIndexes := activeSection.fieldOrder
	labelWidth := m.settingsLabelWidth(contentWidth, fieldIndexes)
	inputWidth := max(10, contentWidth-labelWidth-1)
	if gettingStartedGuide {
		if maxHeight >= 28 {
			if editor := m.renderSettingsGettingStartedFocusedEditor(contentWidth, labelWidth, inputWidth); editor != "" {
				mainLines = append(mainLines, "")
				mainLines = append(mainLines, editor)
			}
		}
	} else {
		start, end := m.settingsVisibleWindow(fieldIndexes, m.settingsVisibleFieldCount(maxHeight))
		if start > 0 {
			mainLines = append(mainLines, commandPaletteHintStyle.Render(fmt.Sprintf("↑ %d above", start)))
		}
		for _, fieldIndex := range fieldIndexes[start:end] {
			mainLines = append(mainLines, m.renderSettingsFieldRow(fieldIndex, m.settingsFields[fieldIndex], fieldIndex == m.settingsSelected, labelWidth, inputWidth))
		}
		if end < len(fieldIndexes) {
			mainLines = append(mainLines, commandPaletteHintStyle.Render(fmt.Sprintf("↓ %d below", len(fieldIndexes)-end)))
		}
	}
	if activeSection.id == settingsSectionBrowser {
		mainLines = append(mainLines, "")
		mainLines = append(mainLines, m.renderBrowserSettingsStatus(contentWidth)...)
	}

	sidebar := m.renderSettingsSectionSidebar(sidebarWidth)
	separator := detailMutedStyle.Render("│")
	main := lipgloss.NewStyle().Width(contentWidth).Render(strings.Join(mainLines, "\n"))
	return lipgloss.JoinHorizontal(lipgloss.Top, sidebar, " ", separator, " ", main)
}

func (m Model) settingsVisibleFieldCount(maxHeight int) int {
	total := len(m.activeSettingsSection().fieldOrder)
	if total == 0 {
		return 0
	}

	// Header/summary, selected-field hint, two action rows, and the section
	// sidebar leave less room for fields than the old inline section tabs did.
	reserved := 17
	if m.activeSettingsSection().id == settingsSectionGettingStarted && maxHeight >= 28 {
		reserved += 3
	}
	visible := maxHeight - reserved
	if visible < 1 {
		visible = 1
	}
	if visible > total {
		visible = total
	}
	return visible
}

func (m Model) settingsVisibleWindow(fieldIndexes []int, limit int) (int, int) {
	total := len(fieldIndexes)
	if total == 0 || limit <= 0 || total <= limit {
		return 0, total
	}

	selectedPosition := 0
	for i, fieldIndex := range fieldIndexes {
		if fieldIndex == m.settingsSelected {
			selectedPosition = i
			break
		}
	}
	start := selectedPosition - limit/2
	if start < 0 {
		start = 0
	}
	maxStart := total - limit
	if start > maxStart {
		start = maxStart
	}
	return start, start + limit
}

func (m Model) settingsLabelWidth(width int, fieldIndexes []int) int {
	widest := 0
	for _, fieldIndex := range fieldIndexes {
		field := m.settingsFields[fieldIndex]
		widest = max(widest, lipgloss.Width(field.label)+2)
	}
	maxAllowed := max(14, width-12)
	return min(maxAllowed, max(18, widest))
}

func settingsSectionSidebarWidth(width int) int {
	sections := settingsSections()
	widest := lipgloss.Width("Sections:")
	for _, section := range sections {
		widest = max(widest, lipgloss.Width(section.label)+2)
	}
	return min(max(14, widest+2), max(14, width/3))
}

func (m Model) renderSettingsSectionSidebar(width int) string {
	sections := settingsSections()
	lines := make([]string, 0, len(sections)+1)
	lines = append(lines, detailLabelStyle.Width(width).Render("Sections:"))
	activeStyle := lipgloss.NewStyle().
		Foreground(lipgloss.Color("16")).
		Background(lipgloss.Color("81")).
		Bold(true).
		Width(width)
	inactiveStyle := lipgloss.NewStyle().
		Foreground(lipgloss.Color("244")).
		Width(width)
	for i, section := range sections {
		label := "  " + section.label
		if i == m.activeSettingsSectionIndex() {
			lines = append(lines, activeStyle.Render(truncateText("> "+section.label, width)))
			continue
		}
		lines = append(lines, inactiveStyle.Render(truncateText(label, width)))
	}
	return strings.Join(lines, "\n")
}

func (m Model) renderSettingsSectionHint(section settingsSection, width int) string {
	text := fmt.Sprintf("%s section. %s", section.label, section.hint)
	return commandPaletteHintStyle.Render(lipgloss.NewStyle().Width(max(18, width)).Render(text))
}

func (m Model) renderSettingsGettingStartedGuide(width int) []string {
	lines := []string{
		detailSectionStyle.Render("Setup Guide"),
	}
	for _, step := range m.settingsGettingStartedSteps() {
		lines = append(lines, m.renderSettingsGettingStartedStep(step, width))
	}
	return lines
}

func (m Model) settingsGettingStartedSteps() []settingsGettingStartedStep {
	settings := m.settingsDraftForInferenceStatus()
	projectChoice := m.selectedSettingsProviderChoice(providerChoiceRoleProjectReports, settings.AIBackend, settings)
	bossChoice := m.selectedSettingsProviderChoice(providerChoiceRoleBossChat, settings.BossChatBackend, settings)
	keyState, keyStyle, keyDetail := m.settingsOpenAIKeyStepState(settings)
	rootsValue, rootsState, rootsStyle, rootsDetail := m.settingsProjectRootsStepState()
	return []settingsGettingStartedStep{
		{
			Number:     "1",
			Title:      "Project reports",
			Value:      firstNonEmptyTrimmed(projectChoice.Label, "Not configured"),
			State:      firstNonEmptyTrimmed(projectChoice.State, "needs setup"),
			StateStyle: projectChoice.StateStyle,
			Detail:     firstNonEmptyTrimmed(projectChoice.NextStep, projectChoice.Detail),
			FieldIndex: settingsFieldAIBackend,
		},
		{
			Number:     "2",
			Title:      "OpenAI key",
			Value:      keyState,
			State:      keyState,
			StateStyle: keyStyle,
			Detail:     keyDetail,
			FieldIndex: settingsFieldOpenAIAPIKey,
		},
		{
			Number:     "3",
			Title:      "Boss chat",
			Value:      firstNonEmptyTrimmed(bossChoice.Label, "Auto"),
			State:      firstNonEmptyTrimmed(bossChoice.State, "needs setup"),
			StateStyle: bossChoice.StateStyle,
			Detail:     firstNonEmptyTrimmed(bossChoice.NextStep, bossChoice.Detail),
			FieldIndex: settingsFieldBossChatBackend,
		},
		{
			Number:     "4",
			Title:      "Project roots",
			Value:      rootsValue,
			State:      rootsState,
			StateStyle: rootsStyle,
			Detail:     rootsDetail,
			FieldIndex: settingsFieldIncludePaths,
		},
	}
}

func (m Model) selectedSettingsProviderChoice(role providerChoiceRole, backend config.AIBackend, settings config.EditableSettings) providerChoice {
	choices := m.providerChoices(role, settings)
	if len(choices) == 0 {
		return providerChoice{}
	}
	return choices[providerChoiceSelection(choices, backend)]
}

func (m Model) settingsOpenAIKeyStepState(settings config.EditableSettings) (string, lipgloss.Style, string) {
	hasKey := strings.TrimSpace(settings.OpenAIAPIKey) != ""
	needsKey := settings.AIBackend == config.AIBackendOpenAIAPI ||
		settings.BossChatBackend == config.AIBackendOpenAIAPI ||
		settings.BossChatBackend == config.AIBackendUnset
	if hasKey {
		return "saved", footerPrimaryLabelStyle, "Ready for OpenAI API and Auto Boss chat."
	}
	if needsKey {
		return "needed", detailWarningStyle, "Paste a key or choose local/off paths."
	}
	return "optional", detailMutedStyle, "Only needed for OpenAI API paths."
}

func (m Model) settingsProjectRootsStepState() (string, string, lipgloss.Style, string) {
	paths := splitCommaList(m.settingsFieldValue(settingsFieldIncludePaths))
	if len(paths) == 0 {
		return "none", "optional", detailMutedStyle, "Leave blank to scan the default roots."
	}
	if len(paths) == 1 {
		return displayPathWithHomeTilde(paths[0], m.homeDir), "ready", footerPrimaryLabelStyle, "This is where projects are discovered."
	}
	return fmt.Sprintf("%d roots", len(paths)), "ready", footerPrimaryLabelStyle, "These folders are scanned for projects."
}

func splitCommaList(raw string) []string {
	parts := strings.Split(raw, ",")
	values := make([]string, 0, len(parts))
	for _, part := range parts {
		if trimmed := strings.TrimSpace(part); trimmed != "" {
			values = append(values, trimmed)
		}
	}
	return values
}

func (m Model) renderSettingsGettingStartedStep(step settingsGettingStartedStep, width int) string {
	selected := step.FieldIndex == m.settingsSelected
	numberStyle := detailSectionStyle.Width(3)
	titleStyle := detailValueStyle.Bold(true)
	valueStyle := detailValueStyle
	if selected {
		numberStyle = commandPalettePickStyle.Width(3)
		titleStyle = lipgloss.NewStyle().Foreground(lipgloss.Color("230")).Bold(true)
		valueStyle = lipgloss.NewStyle().Foreground(lipgloss.Color("230")).Bold(true)
	}
	fixedWidth := 8
	availableWidth := max(1, width-fixedWidth)
	stateWidth := min(10, max(7, availableWidth/4))
	remainingWidth := max(1, availableWidth-stateWidth)
	titleWidth := min(28, max(10, remainingWidth*56/100))
	if valueRoom := remainingWidth - titleWidth; valueRoom < 8 {
		titleWidth = max(6, remainingWidth-8)
	}
	valueWidth := max(1, remainingWidth-titleWidth)
	marker := " "
	if selected {
		marker = ">"
	}
	row := marker + " " +
		numberStyle.Render(step.Number) + " " +
		titleStyle.Width(titleWidth).Render(truncateText(step.Title, titleWidth)) + " " +
		valueStyle.Width(valueWidth).Render(truncateText(step.Value, valueWidth)) + " " +
		step.StateStyle.Width(stateWidth).Render(truncateText(step.State, stateWidth))
	row = fitFooterWidth(row, width)
	if selected {
		return dialogSelectedRowStyle.Width(width).Render(row)
	}
	return lipgloss.NewStyle().Width(width).Render(row)
}

func (m Model) renderSettingsGettingStartedFocusedEditor(width, labelWidth, inputWidth int) string {
	if m.settingsSelected < 0 || m.settingsSelected >= len(m.settingsFields) || settingsFieldUsesPicker(m.settingsSelected) {
		return ""
	}
	label := "Edit"
	if m.settingsSelected == settingsFieldOpenAIAPIKey {
		label = "Paste key"
	}
	if m.settingsSelected == settingsFieldIncludePaths {
		label = "Project roots"
	}
	field := m.settingsFields[m.settingsSelected]
	row := m.renderSettingsFieldRow(m.settingsSelected, field, true, min(labelWidth, max(12, width/3)), max(12, inputWidth))
	return detailSectionStyle.Render(label) + "\n" + row
}

type settingsBrowserProviderCapability struct {
	provider codexapp.Provider
	summary  string
	style    lipgloss.Style
}

type settingsBrowserLiveActivity struct {
	provider    codexapp.Provider
	projectPath string
	activity    browserctl.SessionActivity
}

type settingsGettingStartedStep struct {
	Number     string
	Title      string
	Value      string
	State      string
	StateStyle lipgloss.Style
	Detail     string
	FieldIndex int
}

func browserProviderCapabilities() []settingsBrowserProviderCapability {
	return []settingsBrowserProviderCapability{
		{
			provider: codexapp.ProviderCodex,
			summary:  "Launch policy plus approval, tool input, and elicitation replies.",
			style:    detailValueStyle,
		},
		{
			provider: codexapp.ProviderOpenCode,
			summary:  "Launch policy plus approval and tool input. Embedded elicitation replies are still missing.",
			style:    detailWarningStyle,
		},
		{
			provider: codexapp.ProviderClaudeCode,
			summary:  "Launch policy only. Approval, tool input, and elicitation replies are not wired yet.",
			style:    detailMutedStyle,
		},
	}
}

func (m Model) browserLiveActivities() []settingsBrowserLiveActivity {
	if len(m.codexSnapshots) == 0 {
		return nil
	}
	activities := make([]settingsBrowserLiveActivity, 0, len(m.codexSnapshots))
	for projectPath, snapshot := range m.codexSnapshots {
		if snapshot.Closed {
			continue
		}
		activity := snapshot.BrowserActivity.Normalize()
		if !activity.Live() {
			continue
		}
		activities = append(activities, settingsBrowserLiveActivity{
			provider:    snapshot.Provider,
			projectPath: projectPath,
			activity:    activity,
		})
	}
	sort.Slice(activities, func(i, j int) bool {
		if activities[i].provider != activities[j].provider {
			return activities[i].provider < activities[j].provider
		}
		return activities[i].projectPath < activities[j].projectPath
	})
	return activities
}

func (m Model) renderBrowserSettingsStatus(width int) []string {
	policy := m.currentSettingsBaseline().PlaywrightPolicy.Normalize()
	lines := []string{
		detailField("Effective", detailValueStyle.Render(policy.Summary())),
	}
	if owner := m.currentInteractiveBrowserLeaseOwner(); owner != nil {
		lines = append(lines, detailField("Ownership", detailWarningStyle.Render("Interactive browser reserved by "+m.describeManagedBrowserLease(*owner))))
	} else {
		lines = append(lines, detailField("Ownership", detailValueStyle.Render("Interactive browser is free.")))
	}
	if waiting := len(m.browserLeaseSnapshot.Waiting); waiting > 0 {
		label := fmt.Sprintf("%d managed login flow(s) waiting", waiting)
		lines = append(lines, detailField("Lease queue", detailMutedStyle.Render(label)))
		for _, lease := range m.browserLeaseSnapshot.Waiting {
			lines = append(lines, renderWrappedDialogTextLines(
				detailMutedStyle,
				max(18, width-2),
				m.describeManagedBrowserLease(lease)+" is waiting to open a browser login flow.",
			)...)
		}
	} else {
		lines = append(lines, detailField("Lease queue", detailMutedStyle.Render("No managed login flows are queued.")))
	}
	lines = append(lines, detailLabelStyle.Render("Live activity:"))
	liveActivities := m.browserLiveActivities()
	if len(liveActivities) == 0 {
		lines = append(lines, renderWrappedDialogTextLines(
			detailMutedStyle,
			max(18, width-2),
			"No cached embedded session is currently reporting Playwright activity.",
		)...)
	} else {
		for _, activity := range liveActivities {
			projectLabel := filepath.Base(strings.TrimSpace(activity.projectPath))
			if projectLabel == "" {
				projectLabel = activity.projectPath
			}
			text := fmt.Sprintf("%s / %s: %s", activity.provider.Label(), projectLabel, activity.activity.Summary())
			style := detailValueStyle
			if activity.activity.Normalize().State == browserctl.SessionActivityStateWaitingForUser {
				style = detailWarningStyle
			}
			lines = append(lines, renderWrappedDialogTextLines(style, max(18, width-2), text)...)
		}
	}
	lines = append(lines, detailLabelStyle.Render("Provider support:"))
	for _, capability := range browserProviderCapabilities() {
		text := fmt.Sprintf("%s: %s", capability.provider.Label(), capability.summary)
		lines = append(lines, renderWrappedDialogTextLines(capability.style, max(18, width-2), text)...)
	}
	return lines
}

func (m Model) renderSettingsFieldRow(fieldIndex int, field settingsField, selected bool, labelWidth, inputWidth int) string {
	label := "  " + field.label
	labelStyle := detailLabelStyle
	if selected {
		label = "> " + field.label
		labelStyle = commandPalettePickStyle
	}
	label = truncateText(label, labelWidth)

	if settingsFieldUsesPicker(fieldIndex) {
		var row string
		switch fieldIndex {
		case settingsFieldAIBackend:
			row = labelStyle.Width(labelWidth).Render(label) + " " + m.renderSettingsAIBackendValue(selected, inputWidth)
		case settingsFieldBossChatBackend:
			row = labelStyle.Width(labelWidth).Render(label) + " " + m.renderSettingsBossChatBackendValue(selected, inputWidth)
		case settingsFieldBrowserAutomation:
			row = labelStyle.Width(labelWidth).Render(label) + " " + m.renderSettingsBrowserAutomationValue(selected, inputWidth)
		}
		if selected {
			return dialogSelectedRowStyle.Width(labelWidth + inputWidth + 1).Render(fitFooterWidth(row, labelWidth+inputWidth+1))
		}
		return row
	}

	input := field.input
	input.Width = inputWidth
	row := labelStyle.Width(labelWidth).Render(label) + " " + input.View()
	if selected {
		return dialogSelectedRowStyle.Width(labelWidth + inputWidth + 1).Render(fitFooterWidth(row, labelWidth+inputWidth+1))
	}
	return row
}

func (m Model) renderSelectedSettingsHint(width int) string {
	if m.settingsSelected < 0 || m.settingsSelected >= len(m.settingsFields) {
		return ""
	}
	if m.activeSettingsSection().id == settingsSectionGettingStarted {
		return m.renderSettingsGettingStartedNextAction(width)
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

func (m Model) renderSettingsGettingStartedNextAction(width int) string {
	step := m.selectedSettingsGettingStartedStep()
	action := "Use Tab to move through setup, then ctrl+s to save."
	switch m.settingsSelected {
	case settingsFieldAIBackend, settingsFieldBossChatBackend:
		action = "Next: press Enter to choose from the provider list."
	case settingsFieldOpenAIAPIKey:
		if suffix := maskedOpenAIKeySuffix(m.settingsFieldValue(settingsFieldOpenAIAPIKey)); suffix != "" {
			action = "Stored key ends with " + suffix + ". Replace it here only if you want to change API keys."
		} else {
			action = "Next: paste a key here, or choose local/off providers if you want no OpenAI API key."
		}
	case settingsFieldIncludePaths:
		action = "Next: enter comma-separated project roots, or leave the default and save."
	}
	lines := []string{}
	if detail := strings.TrimSpace(step.Detail); detail != "" {
		lines = append(lines, renderWrappedDetailField("About", detailValueStyle, width, detail))
	}
	lines = append(lines, detailSectionStyle.Render(lipgloss.NewStyle().Width(max(18, width)).Render(action)))
	return strings.Join(lines, "\n")
}

func (m Model) selectedSettingsGettingStartedStep() settingsGettingStartedStep {
	for _, step := range m.settingsGettingStartedSteps() {
		if step.FieldIndex == m.settingsSelected {
			return step
		}
	}
	return settingsGettingStartedStep{}
}

func (m Model) renderSettingsActions() string {
	actions := []string{
		renderDialogAction("ctrl+s", "save", commitActionKeyStyle, commitActionTextStyle),
		renderDialogAction("Tab", "next", navigateActionKeyStyle, navigateActionTextStyle),
		renderDialogAction("Up/Down", "move", pushActionKeyStyle, pushActionTextStyle),
		renderDialogAction("Esc", "cancel", cancelActionKeyStyle, cancelActionTextStyle),
	}
	if settingsFieldUsesPicker(m.settingsSelected) {
		actions = append([]string{
			renderDialogAction("Enter", "choose", navigateActionKeyStyle, navigateActionTextStyle),
		}, actions...)
	}
	return strings.Join([]string{
		strings.Join(actions, "   "),
		renderDialogAction("Page Up/Page Down", "changes section", pushActionKeyStyle, pushActionTextStyle),
	}, "\n")
}

func newSettingsFields(settings config.EditableSettings) []settingsField {
	return []settingsField{
		newSensitiveSettingsField(
			"OpenAI API key",
			"Used by OpenAI API backed features, including boss chat when its backend is openai_api.",
			settings.OpenAIAPIKey,
			512,
			settingsSectionGettingStarted,
		),
		newSettingsField(
			"Boss chat backend",
			"Press Enter to choose Auto, OpenAI API, or Off. This is separate from project analysis, so summaries can stay on Codex/OpenCode while boss chat uses direct API inference.",
			string(settings.BossChatBackend),
			32,
			settingsSectionGettingStarted,
		),
		newSettingsField(
			"Boss helm model",
			"High-grade Boss model for interactive answers, planning, risky choices, and control proposals. Leave blank for the built-in default or set LCROOM_BOSS_MODEL as an environment override.",
			settings.BossHelmModel,
			128,
			settingsSectionAI,
		),
		newSettingsField(
			"Boss utility model",
			"Lower-cost Boss model for routine read-only query routing. Leave blank for the built-in utility default.",
			settings.BossUtilityModel,
			128,
			settingsSectionAI,
		),
		newSettingsField(
			"MLX base URL",
			"Used when the AI backend is MLX. Leave blank to use the default local OpenAI-compatible URL: http://127.0.0.1:8080/v1",
			settings.MLXBaseURL,
			512,
			settingsSectionAI,
		),
		newSensitiveSettingsFieldWithPlaceholder(
			"MLX API key",
			"Optional API key for the MLX OpenAI-compatible endpoint. Leave blank to use the default value: mlx",
			settings.MLXAPIKey,
			512,
			"Leave blank for mlx",
			settingsSectionAI,
		),
		newSettingsField(
			"MLX model",
			"Optional exact model ID for the MLX backend. Leave blank to auto-use the first model returned by /v1/models.",
			settings.MLXModel,
			512,
			settingsSectionAI,
		),
		newSettingsField(
			"Ollama base URL",
			"Used when the AI backend is Ollama. Leave blank to use the default OpenAI-compatible URL: http://127.0.0.1:11434/v1",
			settings.OllamaBaseURL,
			512,
			settingsSectionAI,
		),
		newSensitiveSettingsFieldWithPlaceholder(
			"Ollama API key",
			"Optional API key for the Ollama OpenAI-compatible endpoint. Leave blank to use the default value: ollama",
			settings.OllamaAPIKey,
			512,
			"Leave blank for ollama",
			settingsSectionAI,
		),
		newSettingsField(
			"Ollama model",
			"Optional exact model ID for the Ollama backend. Leave blank to auto-use the first model returned by /v1/models.",
			settings.OllamaModel,
			512,
			settingsSectionAI,
		),
		newSettingsField(
			"LCAgent executable",
			"Optional path to the lcagent binary. Leave blank to use the bundled sibling binary, PATH lookup, or local go run fallback.",
			settings.LCAgentPath,
			512,
			settingsSectionAdvanced,
		),
		newSettingsField(
			"LCAgent env file",
			"Optional env file for experimental LCAgent provider credentials, such as OpenRouter, DeepSeek, Moonshot, or OpenAI keys. Missing files are shown as warnings and launches will fail until fixed or cleared.",
			settings.LCAgentEnvFile,
			1024,
			settingsSectionAdvanced,
		),
		newSettingsField(
			"LCAgent provider",
			"Experimental LCAgent provider. Accepted values: openrouter, openai, deepseek, moonshot.",
			settings.LCAgentProvider,
			32,
			settingsSectionAdvanced,
		),
		newSettingsField(
			"LCAgent autonomy",
			"Accepted values: off, low, medium. Low allows conservative local edits while keeping higher-risk actions constrained.",
			settings.LCAgentAuto,
			16,
			settingsSectionAdvanced,
		),
		newSettingsField(
			"LCAgent tool profile",
			"Accepted values: balanced, generous. Balanced keeps read budgets conservative; generous is useful for large-context model experiments.",
			settings.LCAgentToolProfile,
			32,
			settingsSectionAdvanced,
		),
		newSettingsField(
			"LCAgent context profile",
			"Accepted values: balanced, large. Large delays provider-loop compaction when the selected model/provider can use bigger context.",
			settings.LCAgentContextProfile,
			32,
			settingsSectionAdvanced,
		),
		newSettingsField(
			"LCAgent timeout",
			"Provider HTTP request timeout for experimental LCAgent runs, for example 10m.",
			formatSettingsDuration(settings.LCAgentRequestTimeout),
			32,
			settingsSectionAdvanced,
		),
		newSettingsField(
			"Include paths",
			"Optional comma-separated path prefixes to keep in scope. Leave blank for all detected paths.",
			strings.Join(settings.IncludePaths, ","),
			2048,
			settingsSectionScope,
		),
		newSettingsField(
			"Exclude paths",
			"Optional comma-separated path prefixes to hide. Example: /Users/alice/tmp,/Users/alice/archive",
			strings.Join(settings.ExcludePaths, ","),
			2048,
			settingsSectionScope,
		),
		newSettingsField(
			"Exclude project names",
			"Optional comma-separated name patterns to hide. Supports '*' wildcards. Example: client-*,secret-demo",
			strings.Join(settings.ExcludeProjectPatterns, ","),
			2048,
			settingsSectionScope,
		),
		newPrivacyPatternsField(
			"Privacy patterns",
			"Comma-separated patterns to hide in demo mode. Supports '*' wildcards. Example: *medical*,*personal*",
			strings.Join(settings.PrivacyPatterns, ","),
			2048,
			settingsSectionScope,
		),
		newSettingsField(
			"Codex launch mode",
			"Accepted values: yolo, full-auto, safe. YOLO is the default. Anything else adds a lot more approval prompts and user interaction; safe is the most interruption-heavy.",
			string(settings.CodexLaunchPreset),
			24,
			settingsSectionAI,
		),
		newSettingsField(
			"Browser windows",
			"Press Enter to choose when newly opened embedded sessions should show browser windows. Existing sessions keep the policy they launched with until reopened or reconnected.",
			settingsBrowserAutomationValue(settings.PlaywrightPolicy),
			24,
			settingsSectionBrowser,
		),
		newSettingsField(
			"Show reasoning",
			"Accepted values: true, false. When true, shows model reasoning/thinking sections in the embedded transcript. Default: false.",
			strconv.FormatBool(!settings.HideReasoningSections),
			8,
			settingsSectionAI,
		),
		newSettingsField(
			"Active threshold",
			"Projects newer than this count as active. Example: 20m",
			formatSettingsDuration(settings.ActiveThreshold),
			24,
			settingsSectionAdvanced,
		),
		newSettingsField(
			"Stuck threshold",
			"Must be greater than active threshold. Example: 4h",
			formatSettingsDuration(settings.StuckThreshold),
			24,
			settingsSectionAdvanced,
		),
		newSettingsField(
			"Scan interval",
			"Background refresh interval. Example: 60s",
			formatSettingsDuration(settings.ScanInterval),
			24,
			settingsSectionAdvanced,
		),
		newSettingsField(
			"AI backend",
			"Press Enter to choose the provider for summaries, classification, and commit help.",
			string(settings.AIBackend),
			32,
			settingsSectionGettingStarted,
		),
	}
}

func newSettingsField(label, hint, value string, charLimit int, section settingsSectionID) settingsField {
	input := textinput.New()
	input.Prompt = ""
	input.SetValue(value)
	input.CharLimit = charLimit
	return settingsField{
		label:   label,
		hint:    hint,
		input:   input,
		section: section,
	}
}

func newSensitiveSettingsField(label, hint, value string, charLimit int, section settingsSectionID) settingsField {
	return newSensitiveSettingsFieldWithPlaceholder(label, hint, value, charLimit, "Paste OpenAI API key", section)
}

func newSensitiveSettingsFieldWithPlaceholder(label, hint, value string, charLimit int, placeholder string, section settingsSectionID) settingsField {
	field := newSettingsField(label, hint, value, charLimit, section)
	field.input.EchoMode = textinput.EchoPassword
	field.input.EchoCharacter = '*'
	field.input.Placeholder = placeholder
	field.sensitive = true
	return field
}

func newPrivacyPatternsField(label, hint, value string, charLimit int, section settingsSectionID) settingsField {
	field := newSettingsField(label, hint, value, charLimit, section)
	field.input.EchoMode = textinput.EchoPassword
	field.input.EchoCharacter = '*'
	field.input.Placeholder = "e.g., *medical*,*personal*"
	field.sensitive = true
	return field
}

func cloneEditableSettings(settings config.EditableSettings) config.EditableSettings {
	settings.AIBackend = config.ResolveAIBackend(settings.AIBackend, settings.OpenAIAPIKey)
	settings.BossChatBackend = config.ResolveBossChatBackend(settings.BossChatBackend, settings.OpenAIAPIKey)
	settings.BossChatModel = strings.TrimSpace(settings.BossChatModel)
	settings.BossHelmModel = strings.TrimSpace(settings.BossHelmModel)
	settings.BossUtilityModel = strings.TrimSpace(settings.BossUtilityModel)
	settings.OpenAIAPIKey = strings.TrimSpace(settings.OpenAIAPIKey)
	settings.MLXBaseURL = strings.TrimSpace(settings.MLXBaseURL)
	settings.MLXAPIKey = strings.TrimSpace(settings.MLXAPIKey)
	settings.MLXModel = strings.TrimSpace(settings.MLXModel)
	settings.OllamaBaseURL = strings.TrimSpace(settings.OllamaBaseURL)
	settings.OllamaAPIKey = strings.TrimSpace(settings.OllamaAPIKey)
	settings.OllamaModel = strings.TrimSpace(settings.OllamaModel)
	settings.LCAgentPath = strings.TrimSpace(settings.LCAgentPath)
	settings.LCAgentEnvFile = strings.TrimSpace(settings.LCAgentEnvFile)
	settings.LCAgentProvider = strings.TrimSpace(settings.LCAgentProvider)
	settings.LCAgentAuto = strings.TrimSpace(settings.LCAgentAuto)
	settings.LCAgentToolProfile = strings.TrimSpace(settings.LCAgentToolProfile)
	settings.LCAgentContextProfile = strings.TrimSpace(settings.LCAgentContextProfile)
	settings.IncludePaths = append([]string(nil), settings.IncludePaths...)
	settings.ExcludePaths = append([]string(nil), settings.ExcludePaths...)
	settings.ExcludeProjectPatterns = append([]string(nil), settings.ExcludeProjectPatterns...)
	settings.PrivacyPatterns = append([]string(nil), settings.PrivacyPatterns...)
	settings.PlaywrightPolicy = settings.PlaywrightPolicy.Normalize()
	settings.RecentCodexModels = append([]string(nil), settings.RecentCodexModels...)
	settings.RecentClaudeModels = append([]string(nil), settings.RecentClaudeModels...)
	settings.RecentOpenCodeModels = append([]string(nil), settings.RecentOpenCodeModels...)
	settings.RecentLCAgentModels = append([]string(nil), settings.RecentLCAgentModels...)
	return settings
}

func (m Model) settingsFieldHint(index int) string {
	if index < 0 || index >= len(m.settingsFields) {
		return ""
	}
	field := m.settingsFields[index]
	switch index {
	case settingsFieldAIBackend:
		backend := config.AIBackend(strings.TrimSpace(field.input.Value()))
		switch backend {
		case config.AIBackendUnset:
			return "Choose a backend to enable summaries, classification, and commit help."
		case config.AIBackendDisabled:
			return "AI features are disabled. Project reports and commit help stay off."
		case config.AIBackendOpenAIAPI:
			if strings.TrimSpace(m.currentSettingsBaseline().OpenAIAPIKey) == "" {
				return "OpenAI API backend selected. Save an OpenAI API key above to enable it."
			}
			return "OpenAI API backend selected. Uses the saved API key for summaries and commit help."
		default:
			return backend.Label() + " backend selected. Project reports and commit help will use this provider."
		}
	case settingsFieldOpenAIAPIKey:
		if suffix := maskedOpenAIKeySuffix(field.input.Value()); suffix != "" {
			return "Used for OpenAI API backed features. Stored key ends with " + suffix + "."
		}
		baseline := m.currentSettingsBaseline()
		if baseline.AIBackend == config.AIBackendOpenAIAPI || baseline.BossChatBackend == config.AIBackendOpenAIAPI {
			return field.hint + " The selected OpenAI API path still needs a saved key."
		}
		return field.hint
	case settingsFieldBossChatBackend:
		switch config.AIBackend(strings.TrimSpace(field.input.Value())) {
		case config.AIBackendOpenAIAPI:
			return "Boss chat will use direct OpenAI API inference even if project analysis uses Codex, OpenCode, Claude Code, MLX, or Ollama."
		case config.AIBackendMLX:
			return "Boss chat will use the MLX OpenAI-compatible endpoint and model fields below."
		case config.AIBackendOllama:
			return "Boss chat will use the Ollama OpenAI-compatible endpoint and model fields below."
		case config.AIBackendDisabled:
			return "Boss chat will stay offline, while project analysis keeps using its own configured backend."
		case config.AIBackendUnset:
			return "Leave blank to auto-use openai_api when an OpenAI API key is saved; choose MLX or Ollama explicitly for local boss chat."
		default:
			return field.hint
		}
	case settingsFieldBossChatModel:
		if model := strings.TrimSpace(field.input.Value()); model != "" {
			return "Boss helm calls will request model " + model + ". LCROOM_BOSS_MODEL still wins if set in the environment."
		}
		return field.hint
	case settingsFieldBossUtilityModel:
		if model := strings.TrimSpace(field.input.Value()); model != "" {
			return "Routine Boss utility calls will request model " + model + ". LCROOM_BOSS_MODEL still overrides all Boss model choices if set."
		}
		return field.hint
	case settingsFieldMLXBaseURL:
		return field.hint
	case settingsFieldMLXAPIKey:
		if suffix := maskedOpenAIKeySuffix(field.input.Value()); suffix != "" {
			return "Used for the MLX backend. Stored key ends with " + suffix + ". Leave blank to use mlx."
		}
		return field.hint
	case settingsFieldMLXModel:
		if model := strings.TrimSpace(field.input.Value()); model != "" {
			return "MLX will prefer model " + model + ". Leave blank to auto-use the first /v1/models result."
		}
		return field.hint
	case settingsFieldOllamaBaseURL:
		return field.hint
	case settingsFieldOllamaAPIKey:
		if suffix := maskedOpenAIKeySuffix(field.input.Value()); suffix != "" {
			return "Used for the Ollama backend. Stored key ends with " + suffix + ". Leave blank to use ollama."
		}
		return field.hint
	case settingsFieldOllamaModel:
		if model := strings.TrimSpace(field.input.Value()); model != "" {
			return "Ollama will prefer model " + model + ". Leave blank to auto-use the first /v1/models result."
		}
		return field.hint
	case settingsFieldLCAgentPath:
		if path := strings.TrimSpace(field.input.Value()); path != "" {
			return "LCAgent launches will use " + path + "."
		}
		return field.hint
	case settingsFieldLCAgentEnvFile:
		if path := strings.TrimSpace(field.input.Value()); path != "" {
			return "LCAgent launches will load provider credentials from " + path + "."
		}
		return field.hint
	case settingsFieldLCAgentProvider:
		switch strings.ToLower(strings.TrimSpace(field.input.Value())) {
		case "", "openrouter":
			return "LCAgent will use OpenRouter-compatible tool calls and model IDs such as deepseek/deepseek-v4-pro."
		case "deepseek":
			return "LCAgent will call DeepSeek directly and use direct DeepSeek model IDs."
		case "moonshot":
			return "LCAgent will call Moonshot directly and use Kimi model IDs."
		case "openai":
			return "LCAgent will call the OpenAI Responses API directly."
		default:
			return field.hint
		}
	case settingsFieldLCAgentAuto:
		switch strings.ToLower(strings.TrimSpace(field.input.Value())) {
		case "off":
			return "LCAgent will deny file edits and use the most restrictive command policy."
		case "", "low":
			return "LCAgent will use the default low autonomy policy."
		case "medium":
			return "LCAgent can run a broader set of workspace-contained actions."
		default:
			return field.hint
		}
	case settingsFieldLCAgentToolProfile:
		switch strings.ToLower(strings.TrimSpace(field.input.Value())) {
		case "", "balanced":
			return "Balanced is the conservative default read budget for early experimental runs."
		case "generous":
			return "Generous allows more file-reading context and works best with larger-context providers."
		default:
			return field.hint
		}
	case settingsFieldLCAgentContextProfile:
		switch strings.ToLower(strings.TrimSpace(field.input.Value())) {
		case "", "balanced":
			return "Balanced keeps provider-loop compaction on the normal budget."
		case "large":
			return "Large retains more loop context before compaction; use it with models that can spend the context well."
		default:
			return field.hint
		}
	case settingsFieldLCAgentRequestTimeout:
		if value := strings.TrimSpace(field.input.Value()); value != "" {
			return "LCAgent provider requests will wait up to " + value + " before timing out."
		}
		return field.hint
	case settingsFieldPrivacyPatterns:
		hint := field.hint
		if m.settingsRevealPrivacy {
			hint += " (revealed - press ctrl+r to hide)"
		} else {
			hint += " (hidden - press ctrl+r to reveal)"
		}
		return hint
	case settingsFieldBrowserAutomation:
		playwrightPolicy, err := parseSettingsBrowserAutomation(field.input.Value(), m.currentSettingsBaseline().PlaywrightPolicy)
		if err != nil {
			return field.hint
		}
		switch normalizeSettingsChoice(field.input.Value()) {
		case "", settingsBrowserAutomationClassic, "compatibility", string(browserctl.ManagementModeLegacy):
			return "Classic browser behavior keeps each provider in charge of Playwright. Use this when you want the familiar fallback path or need to get out of Little Control Room's managed flow quickly."
		case settingsBrowserAutomationOnlyWhenNeeded, "automatic":
			return "Only when needed is the target low-friction path for newly opened embedded sessions: background browsing by default, with a visible browser handoff only when a login or manual step needs you. Current raw policy: " + playwrightPolicy.Summary()
		case settingsBrowserAutomationAlwaysShow:
			return "Always show keeps browser windows visible from the start for newly opened embedded sessions, so you can follow the whole flow in real time. Current raw policy: " + playwrightPolicy.Summary()
		case settingsBrowserAutomationCustom, "observe", "advanced", "custom", "current":
			return "Use config file as-is preserves the raw Playwright policy already stored in config.toml. Use this when you want to keep a custom provider/browser/login combination while the simplified UI stays out of the way."
		default:
			return field.hint
		}
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
