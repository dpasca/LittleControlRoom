package tui

import (
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"time"

	"lcroom/internal/brand"
	"lcroom/internal/browserctl"
	"lcroom/internal/codexapp"
	"lcroom/internal/config"

	"github.com/charmbracelet/bubbles/textarea"
	"github.com/charmbracelet/bubbles/textinput"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
)

const (
	settingsFieldOpenAIAPIKey = iota
	settingsFieldOpenRouterAPIKey
	settingsFieldDeepSeekAPIKey
	settingsFieldMoonshotAPIKey
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
	settingsFieldLCAgentRoutePreset
	settingsFieldLCAgentProvider
	settingsFieldLCAgentModel
	settingsFieldLCAgentReasoning
	settingsFieldLCAgentAuto
	settingsFieldLCAgentAdminWrite
	settingsFieldLCAgentToolProfile
	settingsFieldLCAgentContextProfile
	settingsFieldLCAgentRequestTimeout
	settingsFieldLCAgentUtilityProvider
	settingsFieldLCAgentUtilityModel
	settingsFieldLCAgentWebSearchBackend
	settingsFieldLCAgentWebSearchAPIKey
	settingsFieldLCAgentWebSearchEngineID
	settingsFieldLCAgentWebSearchURL
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
	settingsSectionLCAgent        settingsSectionID = "lcagent"
	settingsSectionScope          settingsSectionID = "scope"
	settingsSectionBrowser        settingsSectionID = "browser"
	settingsSectionAdvanced       settingsSectionID = "advanced"
)

type settingsDrilldownID string

const (
	settingsDrilldownNone           settingsDrilldownID = ""
	settingsDrilldownProjectReports settingsDrilldownID = "project-reports"
	settingsDrilldownBossChat       settingsDrilldownID = "boss-chat"
	settingsDrilldownLCAgent        settingsDrilldownID = "lcagent"
	settingsDrilldownProjectScope   settingsDrilldownID = "project-scope"
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

type settingsPrivacyEditorState struct {
	Input textarea.Model
}

type settingsBrowserAutomationOption struct {
	Value       string
	Label       string
	Summary     string
	Description string
}

type settingsLCAgentWebSearchOption struct {
	Value       string
	Label       string
	Summary     string
	Description string
}

type settingsLCAgentProviderOption struct {
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
				settingsFieldBossChatBackend,
				settingsFieldLCAgentProvider,
				settingsFieldIncludePaths,
			},
		},
		{
			id:    settingsSectionAI,
			label: "Providers & Models",
			hint:  "A compact inventory of shared provider connections and global model display defaults. Use Getting Started for feature setup.",
			fieldOrder: []int{
				settingsFieldCodexLaunchPreset,
				settingsFieldHideReasoningSections,
			},
		},
		{
			id:    settingsSectionLCAgent,
			label: "LCAgent",
			hint:  "Configure the experimental LCR-native worker separately from project reports and Boss chat.",
			fieldOrder: []int{
				settingsFieldLCAgentProvider,
				settingsFieldLCAgentModel,
				settingsFieldLCAgentReasoning,
				settingsFieldLCAgentUtilityProvider,
				settingsFieldLCAgentUtilityModel,
				settingsFieldOpenRouterAPIKey,
				settingsFieldOpenAIAPIKey,
				settingsFieldDeepSeekAPIKey,
				settingsFieldMoonshotAPIKey,
				settingsFieldLCAgentWebSearchBackend,
				settingsFieldLCAgentWebSearchAPIKey,
				settingsFieldLCAgentWebSearchEngineID,
				settingsFieldLCAgentWebSearchURL,
				settingsFieldLCAgentAuto,
				settingsFieldLCAgentAdminWrite,
				settingsFieldLCAgentToolProfile,
				settingsFieldLCAgentContextProfile,
				settingsFieldLCAgentRequestTimeout,
				settingsFieldLCAgentPath,
				settingsFieldLCAgentEnvFile,
				settingsFieldLCAgentRoutePreset,
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
			hint:  "Refresh timing and other low-level tuning knobs. Most users can leave these alone.",
			fieldOrder: []int{
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

func settingsSectionIndexByID(id settingsSectionID) int {
	for i, section := range settingsSections() {
		if section.id == id {
			return i
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
	return index == settingsFieldAIBackend ||
		index == settingsFieldBossChatBackend ||
		index == settingsFieldLCAgentProvider ||
		index == settingsFieldLCAgentUtilityProvider ||
		index == settingsFieldBrowserAutomation ||
		index == settingsFieldLCAgentWebSearchBackend ||
		settingsFieldUsesChoicePicker(index)
}

func normalizeSettingsChoice(raw string) string {
	trimmed := strings.ToLower(strings.TrimSpace(raw))
	trimmed = strings.ReplaceAll(trimmed, "_", "-")
	trimmed = strings.ReplaceAll(trimmed, " ", "-")
	return trimmed
}

func (m *Model) openSettingsMode() tea.Cmd {
	m.settingsEmbeddedProject = ""
	m.settingsEmbeddedProvider = ""
	cmd := m.openSettingsModeWithBaseline(m.currentSettingsBaseline())
	return tea.Batch(cmd, m.refreshSetupSnapshotCmd(false))
}

func (m *Model) openEmbeddedLCAgentSettingsMode(projectPath string) tea.Cmd {
	cmd := m.openSettingsModeWithBaseline(m.currentSettingsBaseline())
	m.settingsEmbeddedProject = strings.TrimSpace(projectPath)
	m.settingsEmbeddedProvider = codexapp.ProviderLCAgent
	m.status = "LCAgent settings open. Configure web search, then press ctrl+s to save."
	return tea.Batch(cmd, m.setSettingsSelection(settingsFieldLCAgentWebSearchBackend), m.refreshSetupSnapshotCmd(false))
}

func (m *Model) openBrowserSettingsMode() tea.Cmd {
	settings := m.currentSettingsBaseline()
	m.settingsFields = newSettingsFields(settings)
	saved := cloneEditableSettings(settings)
	m.settingsBaseline = &saved
	m.settingsMode = true
	m.settingsSaving = false
	m.settingsRevealPrivacy = false
	m.settingsPrivacyEditor = nil
	m.settingsDrilldown = settingsDrilldownNone
	m.settingsAIBackendPickerVisible = false
	m.settingsAIBackendPickerSelected = 0
	m.settingsLCAgentProviderVisible = false
	m.settingsLCAgentProviderSelected = 0
	m.settingsBossChatPickerVisible = false
	m.settingsBossChatPickerSelected = 0
	m.settingsBrowserPickerVisible = false
	m.settingsBrowserPickerSelected = 0
	m.settingsLCAgentSearchPickerVisible = false
	m.settingsLCAgentSearchPickerSelected = 0
	m.settingsLCAgentModelPicker = nil
	m.settingsChoicePicker = nil
	m.settingsEmbeddedProject = ""
	m.settingsEmbeddedProvider = ""
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
	m.settingsPrivacyEditor = nil
	m.settingsDrilldown = settingsDrilldownNone
	m.settingsAIBackendPickerVisible = false
	m.settingsAIBackendPickerSelected = 0
	m.settingsLCAgentProviderVisible = false
	m.settingsLCAgentProviderSelected = 0
	m.settingsBossChatPickerVisible = false
	m.settingsBossChatPickerSelected = 0
	m.settingsBrowserPickerVisible = false
	m.settingsBrowserPickerSelected = 0
	m.settingsLCAgentSearchPickerVisible = false
	m.settingsLCAgentSearchPickerSelected = 0
	m.settingsLCAgentModelPicker = nil
	m.settingsChoicePicker = nil
	m.settingsEmbeddedProject = ""
	m.settingsEmbeddedProvider = ""
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
	m.settingsDrilldown = settingsDrilldownNone
	m.settingsPrivacyEditor = nil
	m.settingsAIBackendPickerVisible = false
	m.settingsAIBackendPickerSelected = 0
	m.settingsLCAgentProviderVisible = false
	m.settingsLCAgentProviderSelected = 0
	m.settingsBossChatPickerVisible = false
	m.settingsBossChatPickerSelected = 0
	m.settingsBrowserPickerVisible = false
	m.settingsBrowserPickerSelected = 0
	m.settingsLCAgentSearchPickerVisible = false
	m.settingsLCAgentSearchPickerSelected = 0
	m.settingsLCAgentModelPicker = nil
	m.settingsChoicePicker = nil
	m.settingsEmbeddedProject = ""
	m.settingsEmbeddedProvider = ""
	if status != "" {
		m.status = status
	}
}

func (m Model) updateSettingsMode(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	if m.settingsSaving {
		return m, nil
	}
	if m.settingsPrivacyEditor != nil {
		return m.updateSettingsPrivacyEditorMode(msg)
	}
	switch msg.String() {
	case "esc":
		if m.settingsDrilldown != settingsDrilldownNone {
			return m.closeSettingsDrilldown("Back to Getting Started.")
		}
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
		if m.settingsSelected == settingsFieldPrivacyPatterns {
			return m.openSettingsPrivacyEditor()
		}
		if m.activeSettingsSection().id == settingsSectionGettingStarted && m.settingsDrilldown == settingsDrilldownNone {
			if drilldown := settingsDrilldownForField(m.settingsSelected); drilldown != settingsDrilldownNone {
				return m.openSettingsDrilldown(drilldown)
			}
		}
		if settingsFieldUsesPicker(m.settingsSelected) {
			return m.openSettingsPickerForField(m.settingsSelected)
		}
		if settingsFieldUsesLCAgentModelPicker(m.settingsSelected) {
			return m.openSettingsLCAgentModelPicker()
		}
		m.status = "Press ctrl+s to save settings."
		return m, nil
	case "ctrl+r":
		if m.settingsSelected == settingsFieldPrivacyPatterns {
			m.toggleSettingsPrivacyPatternsReveal()
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

func settingsDrilldownForField(fieldIndex int) settingsDrilldownID {
	switch fieldIndex {
	case settingsFieldAIBackend:
		return settingsDrilldownProjectReports
	case settingsFieldBossChatBackend:
		return settingsDrilldownBossChat
	case settingsFieldLCAgentProvider:
		return settingsDrilldownLCAgent
	case settingsFieldIncludePaths:
		return settingsDrilldownProjectScope
	default:
		return settingsDrilldownNone
	}
}

func (m Model) openSettingsDrilldown(drilldown settingsDrilldownID) (tea.Model, tea.Cmd) {
	if drilldown == settingsDrilldownNone {
		return m, nil
	}
	m.settingsDrilldown = drilldown
	m.settingsSectionSelected = settingsSectionIndexByID(settingsSectionGettingStarted)
	fields := m.visibleSettingsDrilldownFieldOrder(drilldown)
	if len(fields) == 0 {
		m.settingsDrilldown = settingsDrilldownNone
		m.status = "No setup fields are available for this section."
		return m, nil
	}
	m.status = settingsDrilldownTitle(drilldown) + " setup open. Press Esc to go back or ctrl+s to save."
	return m, m.setSettingsSelection(fields[0])
}

func (m Model) closeSettingsDrilldown(status string) (tea.Model, tea.Cmd) {
	drilldown := m.settingsDrilldown
	m.settingsDrilldown = settingsDrilldownNone
	m.settingsSectionSelected = settingsSectionIndexByID(settingsSectionGettingStarted)
	target := settingsDrilldownTopField(drilldown)
	if target < 0 {
		target = defaultSettingsSelection()
	}
	if status != "" {
		m.status = status
	}
	return m, m.setSettingsSelection(target)
}

func (m Model) openSettingsPickerForField(fieldIndex int) (tea.Model, tea.Cmd) {
	switch fieldIndex {
	case settingsFieldAIBackend:
		return m.openSettingsAIBackendPicker()
	case settingsFieldBossChatBackend:
		return m.openSettingsBossChatBackendPicker()
	case settingsFieldLCAgentProvider:
		return m.openSettingsLCAgentProviderPicker()
	case settingsFieldLCAgentUtilityProvider:
		return m.openSettingsLCAgentProviderPicker()
	case settingsFieldBrowserAutomation:
		return m.openSettingsBrowserAutomationPicker()
	case settingsFieldLCAgentWebSearchBackend:
		return m.openSettingsLCAgentWebSearchPicker()
	default:
		if settingsFieldUsesChoicePicker(fieldIndex) {
			return m.openSettingsChoicePicker(fieldIndex)
		}
		return m, nil
	}
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
		m.settingsFieldValue(settingsFieldOpenRouterAPIKey),
		m.settingsFieldValue(settingsFieldDeepSeekAPIKey),
		m.settingsFieldValue(settingsFieldMoonshotAPIKey),
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
		m.settingsFieldValue(settingsFieldLCAgentRoutePreset),
		m.settingsFieldValue(settingsFieldLCAgentProvider),
		m.settingsFieldValue(settingsFieldLCAgentAuto),
		m.settingsFieldValue(settingsFieldLCAgentAdminWrite),
		m.settingsFieldValue(settingsFieldLCAgentToolProfile),
		m.settingsFieldValue(settingsFieldLCAgentContextProfile),
		m.settingsFieldValue(settingsFieldLCAgentRequestTimeout),
		m.settingsFieldValue(settingsFieldLCAgentUtilityProvider),
		m.settingsFieldValue(settingsFieldLCAgentUtilityModel),
		m.settingsFieldValue(settingsFieldLCAgentWebSearchBackend),
		m.settingsFieldValue(settingsFieldLCAgentWebSearchAPIKey),
		m.settingsFieldValue(settingsFieldLCAgentWebSearchEngineID),
		m.settingsFieldValue(settingsFieldLCAgentWebSearchURL),
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
	settings.EmbeddedLCAgentModel = strings.TrimSpace(m.settingsFieldValue(settingsFieldLCAgentModel))
	settings.EmbeddedLCAgentReasoning = strings.TrimSpace(m.settingsFieldValue(settingsFieldLCAgentReasoning))
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
	fields := m.currentSettingsFieldOrder()
	if len(fields) == 0 {
		return nil
	}
	position := -1
	for i, index := range fields {
		if index == m.settingsSelected {
			position = i
			break
		}
	}
	if position < 0 {
		return m.setSettingsSelection(fields[0])
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

	sections := settingsSections()
	if !m.settingsFieldVisible(index) {
		if m.settingsDrilldown != settingsDrilldownNone {
			if fields := m.visibleSettingsDrilldownFieldOrder(m.settingsDrilldown); len(fields) > 0 {
				index = fields[0]
			}
		} else {
			sectionIndex := settingsSectionIndexForField(index)
			if sectionIndex >= 0 && sectionIndex < len(sections) {
				if fields := m.visibleSettingsFieldOrder(sections[sectionIndex]); len(fields) > 0 {
					index = fields[0]
				}
			}
		}
	}

	m.settingsSelected = index
	if m.settingsSectionSelected < 0 || m.settingsSectionSelected >= len(sections) || !settingsSectionContainsField(sections[m.settingsSectionSelected], index) {
		if m.settingsDrilldown != settingsDrilldownNone {
			m.settingsSectionSelected = settingsSectionIndexByID(settingsSectionGettingStarted)
		} else {
			m.settingsSectionSelected = settingsSectionIndexForField(index)
		}
	}
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

func settingsSectionContainsField(section settingsSection, fieldIndex int) bool {
	for _, candidate := range section.fieldOrder {
		if candidate == fieldIndex {
			return true
		}
	}
	return false
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
	m.settingsDrilldown = settingsDrilldownNone
	m.settingsSectionSelected = index
	fields := m.visibleSettingsFieldOrder(sections[index])
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
	if m.settingsDrilldown != settingsDrilldownNone {
		return settingsSectionIndexByID(settingsSectionGettingStarted)
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

func (m Model) visibleSettingsFieldOrder(section settingsSection) []int {
	if len(section.fieldOrder) == 0 {
		return nil
	}
	fields := make([]int, 0, len(section.fieldOrder))
	for _, fieldIndex := range section.fieldOrder {
		if m.settingsFieldVisible(fieldIndex) {
			fields = append(fields, fieldIndex)
		}
	}
	return fields
}

func (m Model) currentSettingsFieldOrder() []int {
	if m.settingsDrilldown != settingsDrilldownNone {
		return m.visibleSettingsDrilldownFieldOrder(m.settingsDrilldown)
	}
	return m.visibleSettingsFieldOrder(m.activeSettingsSection())
}

func (m Model) settingsFieldVisible(index int) bool {
	settings := m.settingsDraftForInferenceStatus()
	switch index {
	case settingsFieldOpenAIAPIKey:
		if m.settingsShowingLCAgentCredentials() {
			return settingsLCAgentCredentialFieldRelevant(settings, "openai")
		}
		return settingsOpenAIKeyFieldRelevant(settings)
	case settingsFieldOpenRouterAPIKey:
		return settings.AIBackend == config.AIBackendOpenRouter ||
			settings.BossChatBackend == config.AIBackendOpenRouter ||
			settingsLCAgentCredentialFieldRelevant(settings, "openrouter") ||
			strings.TrimSpace(settings.OpenRouterAPIKey) != ""
	case settingsFieldDeepSeekAPIKey:
		return settings.AIBackend == config.AIBackendDeepSeek ||
			settings.BossChatBackend == config.AIBackendDeepSeek ||
			settingsLCAgentCredentialFieldRelevant(settings, "deepseek") ||
			strings.TrimSpace(settings.DeepSeekAPIKey) != ""
	case settingsFieldMoonshotAPIKey:
		return settings.AIBackend == config.AIBackendMoonshot ||
			settings.BossChatBackend == config.AIBackendMoonshot ||
			settingsLCAgentCredentialFieldRelevant(settings, "moonshot") ||
			strings.TrimSpace(settings.MoonshotAPIKey) != ""
	case settingsFieldBossChatModel, settingsFieldBossUtilityModel:
		return settingsBossModelFieldsRelevant(settings)
	case settingsFieldMLXBaseURL, settingsFieldMLXAPIKey, settingsFieldMLXModel:
		return settingsOpenAICompatibleFieldsRelevant(settings, config.AIBackendMLX)
	case settingsFieldOllamaBaseURL, settingsFieldOllamaAPIKey, settingsFieldOllamaModel:
		return settingsOpenAICompatibleFieldsRelevant(settings, config.AIBackendOllama)
	case settingsFieldLCAgentUtilityModel:
		return settingsLCAgentUtilityProviderValue(m.settingsFieldValue(settingsFieldLCAgentUtilityProvider)) != "off"
	case settingsFieldLCAgentWebSearchAPIKey:
		backend := normalizeSettingsChoice(m.settingsFieldValue(settingsFieldLCAgentWebSearchBackend))
		return backend == "exa" || backend == "google"
	case settingsFieldLCAgentWebSearchEngineID:
		return normalizeSettingsChoice(m.settingsFieldValue(settingsFieldLCAgentWebSearchBackend)) == "google"
	case settingsFieldLCAgentWebSearchURL:
		return normalizeSettingsChoice(m.settingsFieldValue(settingsFieldLCAgentWebSearchBackend)) == "searxng"
	default:
		return true
	}
}

func (m Model) settingsShowingLCAgentCredentials() bool {
	if m.settingsDrilldown == settingsDrilldownLCAgent {
		return true
	}
	return m.settingsDrilldown == settingsDrilldownNone && m.activeSettingsSection().id == settingsSectionLCAgent
}

func settingsOpenAIKeyFieldRelevant(settings config.EditableSettings) bool {
	return settings.AIBackend == config.AIBackendOpenAIAPI ||
		settings.BossChatBackend == config.AIBackendOpenAIAPI ||
		settingsLCAgentCredentialFieldRelevant(settings, "openai") ||
		strings.TrimSpace(settings.OpenAIAPIKey) != ""
}

func settingsLCAgentCredentialFieldRelevant(settings config.EditableSettings, provider string) bool {
	selected := settingsLCAgentMainProvider(settings)
	if strings.EqualFold(strings.TrimSpace(selected), strings.TrimSpace(provider)) {
		return true
	}
	utilityProvider := settingsLCAgentUtilityProviderValue(settings.LCAgentUtilityProvider)
	return !strings.EqualFold(utilityProvider, "off") &&
		!strings.EqualFold(utilityProvider, "main") &&
		strings.EqualFold(strings.TrimSpace(utilityProvider), strings.TrimSpace(provider))
}

func settingsBossModelFieldsRelevant(settings config.EditableSettings) bool {
	return settings.BossChatBackend == config.AIBackendOpenAIAPI ||
		settings.BossChatBackend == config.AIBackendOpenRouter ||
		settings.BossChatBackend == config.AIBackendDeepSeek ||
		settings.BossChatBackend == config.AIBackendMoonshot ||
		settings.BossChatBackend == config.AIBackendMLX ||
		settings.BossChatBackend == config.AIBackendOllama ||
		strings.TrimSpace(settings.OpenAIAPIKey) != ""
}

func settingsOpenAICompatibleFieldsRelevant(settings config.EditableSettings, backend config.AIBackend) bool {
	return settings.AIBackend == backend || settings.BossChatBackend == backend
}

func (m Model) focusSettingsProviderDetail(backend config.AIBackend) (tea.Model, tea.Cmd) {
	fieldIndex := settingsProviderDetailField(backend)
	if fieldIndex < 0 || len(m.settingsFields) == 0 || !m.settingsFieldVisible(fieldIndex) {
		return m, nil
	}
	cmd := m.setSettingsSelection(fieldIndex)
	return m, cmd
}

func settingsProviderDetailField(backend config.AIBackend) int {
	switch backend {
	case config.AIBackendOpenAIAPI:
		return settingsFieldOpenAIAPIKey
	case config.AIBackendOpenRouter:
		return settingsFieldOpenRouterAPIKey
	case config.AIBackendDeepSeek:
		return settingsFieldDeepSeekAPIKey
	case config.AIBackendMoonshot:
		return settingsFieldMoonshotAPIKey
	case config.AIBackendMLX:
		return settingsFieldMLXBaseURL
	case config.AIBackendOllama:
		return settingsFieldOllamaBaseURL
	default:
		return -1
	}
}

func (m Model) visibleSettingsDrilldownFieldOrder(drilldown settingsDrilldownID) []int {
	fields := m.settingsDrilldownFieldOrder(drilldown)
	visible := make([]int, 0, len(fields))
	for _, fieldIndex := range fields {
		if m.settingsFieldVisible(fieldIndex) {
			visible = append(visible, fieldIndex)
		}
	}
	return visible
}

func (m Model) settingsDrilldownFieldOrder(drilldown settingsDrilldownID) []int {
	settings := m.settingsDraftForInferenceStatus()
	switch drilldown {
	case settingsDrilldownProjectReports:
		fields := []int{settingsFieldAIBackend}
		fields = append(fields, settingsProviderConnectionFields(settings.AIBackend)...)
		return fields
	case settingsDrilldownBossChat:
		fields := []int{settingsFieldBossChatBackend}
		fields = append(fields, settingsProviderConnectionFields(settings.BossChatBackend)...)
		if settingsBossModelFieldsRelevant(settings) {
			fields = append(fields, settingsFieldBossChatModel, settingsFieldBossUtilityModel)
		}
		return fields
	case settingsDrilldownLCAgent:
		fields := []int{
			settingsFieldLCAgentProvider,
			settingsFieldLCAgentModel,
			settingsFieldLCAgentReasoning,
			settingsFieldLCAgentUtilityProvider,
			settingsFieldLCAgentUtilityModel,
		}
		if credentialField := settingsLCAgentCredentialField(settings); credentialField >= 0 {
			fields = append(fields, credentialField)
		}
		if utilityCredentialField := settingsLCAgentUtilityCredentialField(settings); utilityCredentialField >= 0 && !intSliceContains(fields, utilityCredentialField) {
			fields = append(fields, utilityCredentialField)
		}
		fields = append(fields, settingsFieldLCAgentWebSearchBackend)
		fields = append(fields, settingsLCAgentWebSearchDetailFields(settings.LCAgentWebSearchBackend)...)
		return fields
	case settingsDrilldownProjectScope:
		return []int{
			settingsFieldIncludePaths,
			settingsFieldExcludePaths,
			settingsFieldExcludeProjectPatterns,
			settingsFieldPrivacyPatterns,
		}
	default:
		return nil
	}
}

func settingsProviderConnectionFields(backend config.AIBackend) []int {
	switch backend {
	case config.AIBackendOpenAIAPI:
		return []int{settingsFieldOpenAIAPIKey}
	case config.AIBackendOpenRouter:
		return []int{settingsFieldOpenRouterAPIKey}
	case config.AIBackendDeepSeek:
		return []int{settingsFieldDeepSeekAPIKey}
	case config.AIBackendMoonshot:
		return []int{settingsFieldMoonshotAPIKey}
	case config.AIBackendMLX:
		return []int{settingsFieldMLXBaseURL, settingsFieldMLXAPIKey, settingsFieldMLXModel}
	case config.AIBackendOllama:
		return []int{settingsFieldOllamaBaseURL, settingsFieldOllamaAPIKey, settingsFieldOllamaModel}
	default:
		return nil
	}
}

func settingsLCAgentCredentialField(settings config.EditableSettings) int {
	return settingsLCAgentCredentialFieldForProvider(settingsLCAgentMainProvider(settings))
}

func settingsLCAgentUtilityCredentialField(settings config.EditableSettings) int {
	provider := settingsLCAgentUtilityProviderValue(settings.LCAgentUtilityProvider)
	if strings.EqualFold(provider, "off") || strings.EqualFold(provider, "main") {
		return -1
	}
	return settingsLCAgentCredentialFieldForProvider(provider)
}

func settingsLCAgentCredentialFieldForProvider(provider string) int {
	switch strings.ToLower(strings.TrimSpace(provider)) {
	case "openai":
		return settingsFieldOpenAIAPIKey
	case "", "openrouter":
		return settingsFieldOpenRouterAPIKey
	case "deepseek":
		return settingsFieldDeepSeekAPIKey
	case "moonshot":
		return settingsFieldMoonshotAPIKey
	default:
		return -1
	}
}

func settingsLCAgentWebSearchDetailFields(backend string) []int {
	switch normalizeSettingsChoice(backend) {
	case "exa":
		return []int{settingsFieldLCAgentWebSearchAPIKey}
	case "google":
		return []int{settingsFieldLCAgentWebSearchAPIKey, settingsFieldLCAgentWebSearchEngineID}
	case "searxng":
		return []int{settingsFieldLCAgentWebSearchURL}
	default:
		return nil
	}
}

func intSliceContains(values []int, want int) bool {
	for _, value := range values {
		if value == want {
			return true
		}
	}
	return false
}

func settingsDrilldownTopField(drilldown settingsDrilldownID) int {
	switch drilldown {
	case settingsDrilldownProjectReports:
		return settingsFieldAIBackend
	case settingsDrilldownBossChat:
		return settingsFieldBossChatBackend
	case settingsDrilldownLCAgent:
		return settingsFieldLCAgentProvider
	case settingsDrilldownProjectScope:
		return settingsFieldIncludePaths
	default:
		return -1
	}
}

func settingsDrilldownTitle(drilldown settingsDrilldownID) string {
	switch drilldown {
	case settingsDrilldownProjectReports:
		return "Project Reports"
	case settingsDrilldownBossChat:
		return "Boss Chat"
	case settingsDrilldownLCAgent:
		return "LCAgent"
	case settingsDrilldownProjectScope:
		return "Project Roots"
	default:
		return "Setup"
	}
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

func (m *Model) toggleSettingsPrivacyPatternsReveal() {
	m.settingsRevealPrivacy = !m.settingsRevealPrivacy
	m.syncPrivacyPatternsReveal()
	if m.settingsRevealPrivacy {
		m.status = "Privacy patterns revealed"
	} else {
		m.status = "Privacy patterns hidden"
	}
}

func newSettingsPrivacyEditorInput(value string) textarea.Model {
	input := textarea.New()
	input.Prompt = ""
	input.Placeholder = "e.g., *medical*"
	input.CharLimit = 2048
	input.ShowLineNumbers = false
	styleDialogTextarea(&input)
	input.SetWidth(72)
	input.SetHeight(8)
	input.SetValue(settingsPrivacyEditorInitialValue(value))
	input.CursorEnd()
	return input
}

func settingsPrivacyEditorInitialValue(raw string) string {
	values := splitCommaList(raw)
	if len(values) == 0 {
		return strings.TrimSpace(raw)
	}
	return strings.Join(values, "\n")
}

func normalizeSettingsPrivacyEditorValue(raw string) string {
	raw = strings.ReplaceAll(raw, "\r\n", "\n")
	raw = strings.ReplaceAll(raw, "\r", "\n")
	values := []string{}
	for _, line := range strings.Split(raw, "\n") {
		for _, part := range strings.Split(line, ",") {
			trimmed := strings.TrimSpace(part)
			if trimmed != "" {
				values = append(values, trimmed)
			}
		}
	}
	return strings.Join(values, ",")
}

func (m Model) openSettingsPrivacyEditor() (tea.Model, tea.Cmd) {
	input := newSettingsPrivacyEditorInput(m.settingsFieldValue(settingsFieldPrivacyPatterns))
	cmd := input.Focus()
	m.settingsPrivacyEditor = &settingsPrivacyEditorState{Input: input}
	m.settingsRevealPrivacy = true
	m.syncPrivacyPatternsReveal()
	m.status = "Privacy patterns editor open. Press ctrl+s to save or Esc to cancel."
	return m, cmd
}

func (m Model) closeSettingsPrivacyEditor(status string) (tea.Model, tea.Cmd) {
	m.settingsPrivacyEditor = nil
	if status != "" {
		m.status = status
	}
	return m, m.setSettingsSelection(settingsFieldPrivacyPatterns)
}

func (m Model) updateSettingsPrivacyEditorMode(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	editor := m.settingsPrivacyEditor
	if editor == nil {
		return m, nil
	}
	switch msg.String() {
	case "esc":
		return m.closeSettingsPrivacyEditor("Privacy patterns edit canceled")
	case "ctrl+s":
		value := normalizeSettingsPrivacyEditorValue(editor.Input.Value())
		m.settingsFields[settingsFieldPrivacyPatterns].input.SetValue(value)
		m.settingsPrivacyEditor = nil
		m.status = "Saving settings..."
		return m.saveSettingsFromFields()
	}
	input, cmd := editor.Input.Update(msg)
	editor.Input = input
	m.settingsPrivacyEditor = editor
	return m, cmd
}

func (m Model) saveSettingsCmd(settings config.EditableSettings) tea.Cmd {
	settings = config.NormalizeEditableSettings(settings)
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
	settings.BossHelmModel = m.settingsFieldValue(settingsFieldBossChatModel)
	settings.BossUtilityModel = m.settingsFieldValue(settingsFieldBossUtilityModel)
	settings.OpenAIAPIKey = m.settingsFieldValue(settingsFieldOpenAIAPIKey)
	settings.OpenRouterAPIKey = m.settingsFieldValue(settingsFieldOpenRouterAPIKey)
	settings.DeepSeekAPIKey = m.settingsFieldValue(settingsFieldDeepSeekAPIKey)
	settings.MoonshotAPIKey = m.settingsFieldValue(settingsFieldMoonshotAPIKey)
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
	settings.LCAgentAdminWrite = strings.EqualFold(strings.TrimSpace(m.settingsFieldValue(settingsFieldLCAgentAdminWrite)), "true")
	settings.LCAgentToolProfile = m.settingsFieldValue(settingsFieldLCAgentToolProfile)
	settings.LCAgentContextProfile = m.settingsFieldValue(settingsFieldLCAgentContextProfile)
	settings.LCAgentUtilityProvider = m.settingsFieldValue(settingsFieldLCAgentUtilityProvider)
	settings.LCAgentUtilityModel = m.settingsFieldValue(settingsFieldLCAgentUtilityModel)
	settings.LCAgentWebSearchBackend = m.settingsFieldValue(settingsFieldLCAgentWebSearchBackend)
	settings.LCAgentWebSearchAPIKey = m.settingsFieldValue(settingsFieldLCAgentWebSearchAPIKey)
	settings.LCAgentWebSearchEngineID = m.settingsFieldValue(settingsFieldLCAgentWebSearchEngineID)
	settings.LCAgentWebSearchURL = m.settingsFieldValue(settingsFieldLCAgentWebSearchURL)
	if timeout, err := time.ParseDuration(m.settingsFieldValue(settingsFieldLCAgentRequestTimeout)); err == nil {
		settings.LCAgentRequestTimeout = timeout
	}
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

func (m Model) renderSettingsPrivacyEditorOverlay(body string, bodyW, bodyH int) string {
	if m.settingsPrivacyEditor == nil {
		return body
	}
	panel := m.renderSettingsPrivacyEditorPanel(bodyW, bodyH)
	panelWidth := lipgloss.Width(panel)
	panelHeight := lipgloss.Height(panel)
	left := max(0, (bodyW-panelWidth)/2)
	top := max(0, (bodyH-panelHeight)/5)
	return overlayBlock(body, panel, bodyW, bodyH, left, top)
}

func (m Model) renderSettingsPrivacyEditorPanel(bodyW, bodyH int) string {
	panelWidth := min(bodyW, min(max(64, bodyW-12), 104))
	panelInnerWidth := max(28, panelWidth-4)
	return renderDialogPanel(panelWidth, panelInnerWidth, m.renderSettingsPrivacyEditorContent(panelInnerWidth, bodyH))
}

func (m Model) renderSettingsPrivacyEditorContent(width, bodyH int) string {
	if m.settingsPrivacyEditor == nil {
		return ""
	}
	input := m.settingsPrivacyEditor.Input
	editorHeight := min(12, max(6, bodyH-12))
	input.SetWidth(max(24, width))
	input.SetHeight(editorHeight)
	lines := []string{
		commandPaletteTitleStyle.Render("Privacy Patterns"),
		commandPaletteHintStyle.Render("One pattern per line. Commas also work."),
		input.View(),
		"",
		strings.Join([]string{
			renderDialogAction("ctrl+s", "save", commitActionKeyStyle, commitActionTextStyle),
			renderDialogAction("Enter", "newline", navigateActionKeyStyle, navigateActionTextStyle),
			renderDialogAction("Esc", "cancel", cancelActionKeyStyle, cancelActionTextStyle),
		}, "   "),
	}
	return strings.Join(lines, "\n")
}

func (m Model) renderSettingsPanel(bodyW, bodyH int) string {
	panelWidth := min(bodyW, min(max(64, bodyW-10), 104))
	panelInnerWidth := max(28, panelWidth-4)
	maxContentHeight := min(max(10, bodyH-2), 32)
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

	gettingStartedGuide := activeSection.id == settingsSectionGettingStarted && m.settingsDrilldown == settingsDrilldownNone && maxHeight >= 20
	gettingStartedDrilldown := activeSection.id == settingsSectionGettingStarted && m.settingsDrilldown != settingsDrilldownNone
	mainLines := []string{}
	if gettingStartedDrilldown {
		mainLines = append(mainLines, m.renderSettingsDrilldown(contentWidth, maxHeight)...)
	} else if gettingStartedGuide {
		mainLines = append(mainLines, m.renderSettingsGettingStartedGuide(contentWidth)...)
	} else {
		mainLines = append(mainLines, m.renderSettingsSectionHint(activeSection, contentWidth))
	}
	if m.settingsSaving {
		mainLines = append(mainLines, "")
		mainLines = append(mainLines, commandPaletteHintStyle.Render("Saving settings..."))
	}

	fieldIndexes := m.currentSettingsFieldOrder()
	labelWidth := m.settingsLabelWidth(contentWidth, fieldIndexes)
	inputWidth := max(10, contentWidth-labelWidth-1)
	if gettingStartedDrilldown {
		// The drilldown renderer owns its field rows so it can add shared
		// provider group labels between feature-specific controls.
	} else if gettingStartedGuide {
		// Top-level Getting Started rows are only entry points. Press Enter to
		// drill into the focused row instead of editing fields inline.
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
	if activeSection.id == settingsSectionAI {
		mainLines = append(mainLines, "")
		mainLines = append(mainLines, m.renderProviderConnectionsStatus(contentWidth)...)
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
	total := len(m.currentSettingsFieldOrder())
	if total == 0 {
		return 0
	}

	// Header/summary, selected-field hint, two action rows, and the section
	// sidebar leave less room for fields than the old inline section tabs did.
	reserved := 14
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

func (m Model) renderSettingsDrilldown(width, maxHeight int) []string {
	drilldown := m.settingsDrilldown
	fields := m.visibleSettingsDrilldownFieldOrder(drilldown)
	lines := []string{
		detailSectionStyle.Render(settingsDrilldownTitle(drilldown) + " Setup"),
		commandPaletteHintStyle.Render(lipgloss.NewStyle().Width(max(18, width)).Render(settingsDrilldownSummary(drilldown))),
	}
	lines = append(lines, m.renderSettingsDrilldownStatus(width)...)
	if len(fields) == 0 {
		lines = append(lines, commandPaletteHintStyle.Render("No setup fields are available for this section."))
		return lines
	}

	labelWidth := m.settingsLabelWidth(width, fields)
	inputWidth := max(10, width-labelWidth-1)
	limit := max(1, maxHeight-10-len(lines))
	start, end := m.settingsVisibleWindow(fields, limit)
	if start > 0 {
		lines = append(lines, commandPaletteHintStyle.Render(fmt.Sprintf("↑ %d above", start)))
	}
	previousGroup := ""
	for _, fieldIndex := range fields[start:end] {
		group := settingsDrilldownGroupForField(drilldown, fieldIndex)
		if group != "" && group != previousGroup {
			lines = append(lines, "")
			lines = append(lines, detailLabelStyle.Render(group))
		}
		lines = append(lines, m.renderSettingsFieldRow(fieldIndex, m.settingsFields[fieldIndex], fieldIndex == m.settingsSelected, labelWidth, inputWidth))
		previousGroup = group
	}
	if end < len(fields) {
		lines = append(lines, commandPaletteHintStyle.Render(fmt.Sprintf("↓ %d below", len(fields)-end)))
	}
	return lines
}

func settingsDrilldownSummary(drilldown settingsDrilldownID) string {
	switch drilldown {
	case settingsDrilldownProjectReports:
		return "Choose the runner for background summaries, classification, TODO help, and commit help. Provider credentials are shared when another feature uses the same connection."
	case settingsDrilldownBossChat:
		return "Choose a realtime backend for /boss. This deliberately excludes Codex, OpenCode, and Claude Code because engineer sessions can be too slow for chat."
	case settingsDrilldownLCAgent:
		return "Configure the LCR-native worker essentials: Main Model, Utility Model, credentials, and web search. Use the LCAgent section for runtime policy and advanced launch fields."
	case settingsDrilldownProjectScope:
		return "Choose where projects are discovered and which folders or names stay hidden."
	default:
		return "Configure this setup area."
	}
}

func (m Model) renderSettingsDrilldownStatus(width int) []string {
	settings := m.settingsDraftForInferenceStatus()
	switch m.settingsDrilldown {
	case settingsDrilldownProjectReports:
		choice := m.selectedSettingsProviderChoice(providerChoiceRoleProjectReports, settings.AIBackend, settings)
		return []string{renderWrappedDetailField("Current", detailValueStyle, width, firstNonEmptyTrimmed(choice.NextStep, choice.Detail))}
	case settingsDrilldownBossChat:
		choice := m.selectedSettingsProviderChoice(providerChoiceRoleBossChat, settings.BossChatBackend, settings)
		return []string{renderWrappedDetailField("Current", detailValueStyle, width, firstNonEmptyTrimmed(choice.NextStep, choice.Detail))}
	case settingsDrilldownLCAgent:
		provider := settingsLCAgentMainProvider(settings)
		state, style, detail := lcagentCredentialSmokeCheck(settings)
		label := settingsLCAgentProviderOptionLabel(provider) + " connection"
		return []string{detailField(label, style.Render(state)+detailMutedStyle.Render(" - "+detail))}
	case settingsDrilldownProjectScope:
		_, state, style, detail := m.settingsProjectRootsStepState()
		return []string{detailField("Project roots", style.Render(state)+detailMutedStyle.Render(" - "+detail))}
	default:
		return nil
	}
}

func settingsDrilldownGroupForField(drilldown settingsDrilldownID, fieldIndex int) string {
	switch drilldown {
	case settingsDrilldownProjectReports:
		switch fieldIndex {
		case settingsFieldAIBackend:
			return "Report Runner"
		case settingsFieldOpenAIAPIKey:
			return "Shared OpenAI Connection"
		case settingsFieldOpenRouterAPIKey:
			return "Shared OpenRouter Connection"
		case settingsFieldDeepSeekAPIKey:
			return "Shared DeepSeek Connection"
		case settingsFieldMoonshotAPIKey:
			return "Shared Moonshot Connection"
		case settingsFieldMLXBaseURL, settingsFieldMLXAPIKey, settingsFieldMLXModel:
			return "Shared MLX Connection"
		case settingsFieldOllamaBaseURL, settingsFieldOllamaAPIKey, settingsFieldOllamaModel:
			return "Shared Ollama Connection"
		}
	case settingsDrilldownBossChat:
		switch fieldIndex {
		case settingsFieldBossChatBackend:
			return "Realtime Chat Backend"
		case settingsFieldOpenAIAPIKey:
			return "Shared OpenAI Connection"
		case settingsFieldOpenRouterAPIKey:
			return "Shared OpenRouter Connection"
		case settingsFieldDeepSeekAPIKey:
			return "Shared DeepSeek Connection"
		case settingsFieldMoonshotAPIKey:
			return "Shared Moonshot Connection"
		case settingsFieldMLXBaseURL, settingsFieldMLXAPIKey, settingsFieldMLXModel:
			return "Shared MLX Connection"
		case settingsFieldOllamaBaseURL, settingsFieldOllamaAPIKey, settingsFieldOllamaModel:
			return "Shared Ollama Connection"
		case settingsFieldBossChatModel, settingsFieldBossUtilityModel:
			return "Boss Models"
		}
	case settingsDrilldownLCAgent:
		switch fieldIndex {
		case settingsFieldLCAgentProvider, settingsFieldLCAgentModel, settingsFieldLCAgentReasoning:
			return "Main Model"
		case settingsFieldOpenAIAPIKey, settingsFieldOpenRouterAPIKey, settingsFieldDeepSeekAPIKey, settingsFieldMoonshotAPIKey:
			return "Provider Credentials"
		case settingsFieldLCAgentUtilityProvider, settingsFieldLCAgentUtilityModel:
			return "Utility Model"
		case settingsFieldLCAgentWebSearchBackend, settingsFieldLCAgentWebSearchAPIKey, settingsFieldLCAgentWebSearchEngineID, settingsFieldLCAgentWebSearchURL:
			return "Web Search"
		case settingsFieldLCAgentAuto, settingsFieldLCAgentAdminWrite, settingsFieldLCAgentToolProfile, settingsFieldLCAgentContextProfile, settingsFieldLCAgentRequestTimeout:
			return "Runtime Policy"
		case settingsFieldLCAgentRoutePreset, settingsFieldLCAgentPath:
			return "Advanced"
		}
	case settingsDrilldownProjectScope:
		switch fieldIndex {
		case settingsFieldIncludePaths:
			return "Discovery"
		case settingsFieldExcludePaths, settingsFieldExcludeProjectPatterns:
			return "Filtering"
		case settingsFieldPrivacyPatterns:
			return "Privacy"
		}
	}
	return ""
}

func (m Model) renderProviderConnectionsStatus(width int) []string {
	settings := m.settingsDraftForInferenceStatus()
	lines := []string{detailSectionStyle.Render("Provider Connections")}
	lines = append(lines, m.renderProviderConnectionLine("OpenAI API", settingsOpenAIConnectionState(settings), settingsProviderUsers(settings, config.AIBackendOpenAIAPI), width))
	lines = append(lines, m.renderProviderConnectionLine("OpenRouter", settingsCloudConnectionState(settings, config.AIBackendOpenRouter), settingsProviderUsers(settings, config.AIBackendOpenRouter), width))
	lines = append(lines, m.renderProviderConnectionLine("DeepSeek", settingsCloudConnectionState(settings, config.AIBackendDeepSeek), settingsProviderUsers(settings, config.AIBackendDeepSeek), width))
	lines = append(lines, m.renderProviderConnectionLine("Moonshot", settingsCloudConnectionState(settings, config.AIBackendMoonshot), settingsProviderUsers(settings, config.AIBackendMoonshot), width))
	lines = append(lines, m.renderProviderConnectionLine("MLX", settingsLocalConnectionState(settings, config.AIBackendMLX), settingsProviderUsers(settings, config.AIBackendMLX), width))
	lines = append(lines, m.renderProviderConnectionLine("Ollama", settingsLocalConnectionState(settings, config.AIBackendOllama), settingsProviderUsers(settings, config.AIBackendOllama), width))

	provider := firstNonEmptyTrimmed(lcagentProviderForRoutePreset(settings.LCAgentRoutePreset), settings.LCAgentProvider, "openrouter")
	state, style, detail := lcagentCredentialSmokeCheck(settings)
	label := "LCAgent " + settingsLCAgentProviderOptionLabel(provider)
	line := style.Render(state) + detailMutedStyle.Render(" - "+detail)
	lines = append(lines, renderWrappedDetailField(label, detailValueStyle, width, line))
	return lines
}

func (m Model) renderProviderConnectionLine(label string, state string, users []string, width int) string {
	style := detailMutedStyle
	if state == "ready" {
		style = footerPrimaryLabelStyle
	} else if state == "needed" || state == "blocked" {
		style = detailWarningStyle
	}
	detail := "not used right now"
	if len(users) > 0 {
		detail = "used by " + strings.Join(users, ", ")
	}
	return renderWrappedDetailField(label, detailValueStyle, width, style.Render(state)+detailMutedStyle.Render(" - "+detail))
}

func settingsOpenAIConnectionState(settings config.EditableSettings) string {
	if strings.TrimSpace(settings.OpenAIAPIKey) != "" {
		return "ready"
	}
	if settings.AIBackend == config.AIBackendOpenAIAPI ||
		settings.BossChatBackend == config.AIBackendOpenAIAPI ||
		settingsLCAgentCredentialFieldRelevant(settings, "openai") {
		return "needed"
	}
	return "optional"
}

func settingsCloudConnectionState(settings config.EditableSettings, backend config.AIBackend) string {
	if cloudBackendAPIKeySaved(settings, backend) {
		return "ready"
	}
	if settings.AIBackend == backend || settings.BossChatBackend == backend {
		return "needed"
	}
	switch backend {
	case config.AIBackendOpenRouter:
		if settingsLCAgentCredentialFieldRelevant(settings, "openrouter") {
			return "needed"
		}
	case config.AIBackendDeepSeek:
		if settingsLCAgentCredentialFieldRelevant(settings, "deepseek") {
			return "needed"
		}
	case config.AIBackendMoonshot:
		if settingsLCAgentCredentialFieldRelevant(settings, "moonshot") {
			return "needed"
		}
	}
	return "optional"
}

func settingsLocalConnectionState(settings config.EditableSettings, backend config.AIBackend) string {
	if settings.AIBackend == backend || settings.BossChatBackend == backend {
		return "selected"
	}
	switch backend {
	case config.AIBackendMLX:
		if strings.TrimSpace(settings.MLXBaseURL) != "" || strings.TrimSpace(settings.MLXModel) != "" {
			return "configured"
		}
	case config.AIBackendOllama:
		if strings.TrimSpace(settings.OllamaBaseURL) != "" || strings.TrimSpace(settings.OllamaModel) != "" {
			return "configured"
		}
	}
	return "optional"
}

func settingsProviderUsers(settings config.EditableSettings, backend config.AIBackend) []string {
	users := []string{}
	if settings.AIBackend == backend {
		users = append(users, "Project reports")
	}
	if settings.BossChatBackend == backend {
		users = append(users, "Boss chat")
	}
	if backend == config.AIBackendOpenAIAPI && settingsLCAgentCredentialFieldRelevant(settings, "openai") {
		users = append(users, "LCAgent")
	}
	if backend == config.AIBackendOpenRouter && settingsLCAgentCredentialFieldRelevant(settings, "openrouter") {
		users = append(users, "LCAgent")
	}
	if backend == config.AIBackendDeepSeek && settingsLCAgentCredentialFieldRelevant(settings, "deepseek") {
		users = append(users, "LCAgent")
	}
	if backend == config.AIBackendMoonshot && settingsLCAgentCredentialFieldRelevant(settings, "moonshot") {
		users = append(users, "LCAgent")
	}
	return users
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
	lcagentValue, lcagentState, lcagentStyle, lcagentDetail := settingsLCAgentStepState(settings)
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
			Title:      "Boss chat",
			Value:      firstNonEmptyTrimmed(bossChoice.Label, "Auto"),
			State:      firstNonEmptyTrimmed(bossChoice.State, "needs setup"),
			StateStyle: bossChoice.StateStyle,
			Detail:     firstNonEmptyTrimmed(bossChoice.NextStep, bossChoice.Detail),
			FieldIndex: settingsFieldBossChatBackend,
		},
		{
			Number:     "3",
			Title:      "LCAgent",
			Value:      lcagentValue,
			State:      lcagentState,
			StateStyle: lcagentStyle,
			Detail:     lcagentDetail,
			FieldIndex: settingsFieldLCAgentProvider,
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

func settingsLCAgentStepState(settings config.EditableSettings) (string, string, lipgloss.Style, string) {
	if preset := strings.TrimSpace(settings.LCAgentRoutePreset); preset != "" {
		state, style, detail := lcagentCredentialSmokeCheck(settings)
		return "preset " + preset, state, style, detail
	}
	provider := settingsLCAgentMainProvider(settings)
	model := settingsLCAgentMainModel(settings)
	state, style, detail := lcagentCredentialSmokeCheck(settings)
	value := provider + " / " + model
	if state == "optional" {
		value = provider
	}
	return value, state, style, detail
}

func lcagentCredentialSmokeCheck(settings config.EditableSettings) (string, lipgloss.Style, string) {
	provider := settingsLCAgentMainProvider(settings)
	keyName := lcagentProviderAPIKeyName(provider)
	if keyName == "" {
		return "unknown", detailWarningStyle, "Unknown LCAgent provider " + provider + "."
	}
	if value := lcagentProviderSavedAPIKey(settings, provider); value != "" {
		return "ready", footerPrimaryLabelStyle, lcagentProviderSavedKeyLabel(provider) + " saved " + maskedOpenAIKeySuffix(value) + "."
	}
	envFile := strings.TrimSpace(settings.LCAgentEnvFile)
	if envFile != "" {
		value, found, err := readEnvFileKey(envFile, keyName)
		if err != nil {
			return "blocked", detailWarningStyle, err.Error()
		}
		if found && strings.TrimSpace(value) != "" {
			return "ready", footerPrimaryLabelStyle, keyName + " found in env file " + maskedOpenAIKeySuffix(value) + "."
		}
		return "needed", detailWarningStyle, keyName + " was not found in the selected LCAgent env file."
	}
	if value := strings.TrimSpace(os.Getenv(keyName)); value != "" {
		return "ready", footerPrimaryLabelStyle, keyName + " found in process environment " + maskedOpenAIKeySuffix(value) + "."
	}
	return "needed", detailWarningStyle, keyName + " is not configured; paste the provider key here or use an advanced env file."
}

func lcagentProviderSavedAPIKey(settings config.EditableSettings, provider string) string {
	switch strings.ToLower(strings.TrimSpace(provider)) {
	case "openai":
		return strings.TrimSpace(settings.OpenAIAPIKey)
	case "", "openrouter":
		return strings.TrimSpace(settings.OpenRouterAPIKey)
	case "deepseek":
		return strings.TrimSpace(settings.DeepSeekAPIKey)
	case "moonshot":
		return strings.TrimSpace(settings.MoonshotAPIKey)
	default:
		return ""
	}
}

func lcagentProviderSavedKeyLabel(provider string) string {
	switch strings.ToLower(strings.TrimSpace(provider)) {
	case "openai":
		return "OpenAI API key"
	case "", "openrouter":
		return "OpenRouter API key"
	case "deepseek":
		return "DeepSeek API key"
	case "moonshot":
		return "Moonshot API key"
	default:
		return "Provider key"
	}
}

func lcagentProviderForRoutePreset(preset string) string {
	switch strings.ToLower(strings.TrimSpace(preset)) {
	case "quality":
		return "openai"
	case "balanced", "cheap-scout", "cheap", "scout":
		return "openrouter"
	default:
		return ""
	}
}

func lcagentModelForRoutePreset(preset string) string {
	switch strings.ToLower(strings.TrimSpace(preset)) {
	case "quality":
		return "gpt-5.5"
	case "balanced":
		return "deepseek/deepseek-v4-pro"
	case "cheap-scout", "cheap", "scout":
		return "deepseek/deepseek-v4-flash"
	default:
		return ""
	}
}

func lcagentProviderAPIKeyName(provider string) string {
	switch strings.ToLower(strings.TrimSpace(provider)) {
	case "", "openrouter":
		return "OPENROUTER_API_KEY"
	case "openai":
		return "OPENAI_API_KEY"
	case "deepseek":
		return "DEEPSEEK_API_KEY"
	case "moonshot":
		return "MOONSHOT_API_KEY"
	default:
		return ""
	}
}

func lcagentDefaultModelForProvider(provider string) string {
	switch strings.ToLower(strings.TrimSpace(provider)) {
	case "openai":
		return "gpt-5.5"
	case "deepseek":
		return "deepseek-v4-pro"
	case "moonshot":
		return "kimi-k2.6"
	default:
		return "deepseek/deepseek-v4-pro"
	}
}

func settingsLCAgentMainProvider(settings config.EditableSettings) string {
	return firstNonEmptyTrimmed(lcagentProviderForRoutePreset(settings.LCAgentRoutePreset), settings.LCAgentProvider, "openrouter")
}

func settingsLCAgentMainModel(settings config.EditableSettings) string {
	if strings.TrimSpace(settings.LCAgentRoutePreset) == "" {
		if model := strings.TrimSpace(settings.EmbeddedLCAgentModel); model != "" {
			return model
		}
	}
	return firstNonEmptyTrimmed(lcagentModelForRoutePreset(settings.LCAgentRoutePreset), lcagentDefaultModelForProvider(settingsLCAgentMainProvider(settings)))
}

func settingsLCAgentUtilityDefaultLabel(settings config.EditableSettings) string {
	provider := settingsLCAgentUtilityProviderValue(settings.LCAgentUtilityProvider)
	if provider == "off" {
		return "off"
	}
	if provider == "main" {
		return "same as Main Model (" + settingsLCAgentMainProvider(settings) + " / " + settingsLCAgentMainModel(settings) + ")"
	}
	return lcagentDefaultModelForProvider(provider)
}

func settingsLCAgentUtilityProviderValue(raw string) string {
	normalized := normalizeSettingsChoice(raw)
	switch normalized {
	case "", "main", "same", "same-as-main":
		return "main"
	case "off", "openrouter", "openai", "deepseek", "moonshot":
		return normalized
	default:
		return normalized
	}
}

func settingsBossHelmDefaultLabel(settings config.EditableSettings) string {
	if modelName := strings.TrimSpace(os.Getenv(brand.BossAssistantModelEnvVar)); modelName != "" {
		return modelName + " from " + brand.BossAssistantModelEnvVar
	}
	switch settings.BossChatBackend {
	case config.AIBackendOpenRouter, config.AIBackendDeepSeek, config.AIBackendMoonshot, config.AIBackendMLX, config.AIBackendOllama:
		if modelName := settingsOpenAICompatibleModel(settings, settings.BossChatBackend); modelName != "" {
			return modelName + " from " + settings.BossChatBackend.Label()
		}
		return "the first discovered " + settings.BossChatBackend.Label() + " model"
	default:
		return config.DefaultBossHelmModel
	}
}

func settingsBossUtilityDefaultLabel(settings config.EditableSettings) string {
	if modelName := strings.TrimSpace(os.Getenv(brand.BossAssistantModelEnvVar)); modelName != "" {
		return modelName + " from " + brand.BossAssistantModelEnvVar
	}
	switch settings.BossChatBackend {
	case config.AIBackendOpenRouter, config.AIBackendDeepSeek, config.AIBackendMoonshot, config.AIBackendMLX, config.AIBackendOllama:
		if modelName := settingsOpenAICompatibleModel(settings, settings.BossChatBackend); modelName != "" {
			return modelName + " from " + settings.BossChatBackend.Label()
		}
		return "the first discovered " + settings.BossChatBackend.Label() + " model"
	default:
		return config.DefaultBossUtilityModel
	}
}

func settingsOpenAICompatibleModel(settings config.EditableSettings, backend config.AIBackend) string {
	switch backend {
	case config.AIBackendOpenRouter:
		return config.DefaultOpenRouterModel
	case config.AIBackendDeepSeek:
		return config.DefaultDeepSeekModel
	case config.AIBackendMoonshot:
		return config.DefaultMoonshotModel
	case config.AIBackendMLX:
		return strings.TrimSpace(settings.MLXModel)
	case config.AIBackendOllama:
		return strings.TrimSpace(settings.OllamaModel)
	default:
		return ""
	}
}

func readEnvFileKey(path, key string) (string, bool, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return "", false, fmt.Errorf("LCAgent env file cannot be read: %w", err)
	}
	key = strings.TrimSpace(key)
	for _, line := range strings.Split(string(data), "\n") {
		line = strings.TrimSpace(line)
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		name, value, ok := strings.Cut(line, "=")
		if !ok || strings.TrimSpace(name) != key {
			continue
		}
		return strings.Trim(strings.TrimSpace(value), `"'`), true, nil
	}
	return "", false, nil
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
	if m.settingsSelected == settingsFieldMLXBaseURL || m.settingsSelected == settingsFieldOllamaBaseURL {
		label = "Endpoint"
	}
	if m.settingsSelected == settingsFieldMLXModel || m.settingsSelected == settingsFieldOllamaModel {
		label = "Model"
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
		case settingsFieldLCAgentProvider, settingsFieldLCAgentUtilityProvider:
			row = labelStyle.Width(labelWidth).Render(label) + " " + m.renderSettingsLCAgentProviderValue(fieldIndex, selected, inputWidth)
		case settingsFieldBrowserAutomation:
			row = labelStyle.Width(labelWidth).Render(label) + " " + m.renderSettingsBrowserAutomationValue(selected, inputWidth)
		case settingsFieldLCAgentWebSearchBackend:
			row = labelStyle.Width(labelWidth).Render(label) + " " + m.renderSettingsLCAgentWebSearchValue(selected, inputWidth)
		default:
			if settingsFieldUsesChoicePicker(fieldIndex) {
				row = labelStyle.Width(labelWidth).Render(label) + " " + m.renderSettingsChoiceValue(fieldIndex, selected, inputWidth)
			}
		}
		if selected {
			return dialogSelectedRowStyle.Width(labelWidth + inputWidth + 1).Render(fitFooterWidth(row, labelWidth+inputWidth+1))
		}
		return row
	}

	input := field.input
	if placeholder := m.settingsFieldPlaceholder(fieldIndex); placeholder != "" {
		input.Placeholder = placeholder
	}
	input.Width = inputWidth
	row := labelStyle.Width(labelWidth).Render(label) + " " + input.View()
	if selected {
		return dialogSelectedRowStyle.Width(labelWidth + inputWidth + 1).Render(fitFooterWidth(row, labelWidth+inputWidth+1))
	}
	return row
}

func (m Model) settingsFieldPlaceholder(fieldIndex int) string {
	settings := m.settingsDraftForInferenceStatus()
	switch fieldIndex {
	case settingsFieldBossChatModel:
		return "Default: " + settingsBossHelmDefaultLabel(settings)
	case settingsFieldBossUtilityModel:
		return "Default: " + settingsBossUtilityDefaultLabel(settings)
	case settingsFieldLCAgentModel:
		return "Default: " + settingsLCAgentMainModel(settings)
	case settingsFieldLCAgentUtilityModel:
		return "Default: " + settingsLCAgentUtilityDefaultLabel(settings)
	default:
		return ""
	}
}

func (m Model) renderSelectedSettingsHint(width int) string {
	if m.settingsSelected < 0 || m.settingsSelected >= len(m.settingsFields) {
		return ""
	}
	if m.activeSettingsSection().id == settingsSectionGettingStarted &&
		m.settingsDrilldown == settingsDrilldownNone &&
		settingsDrilldownForField(m.settingsSelected) != settingsDrilldownNone {
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
	action := "Press Enter to open this setup panel, Tab to move, or ctrl+s to save."
	switch m.settingsSelected {
	case settingsFieldAIBackend, settingsFieldBossChatBackend:
		action = "Next: press Enter to open the focused setup panel."
	case settingsFieldLCAgentProvider:
		action = "Next: press Enter to configure LCAgent Main Model, Utility Model, credentials, and web search."
	case settingsFieldOpenAIAPIKey:
		if suffix := maskedOpenAIKeySuffix(m.settingsFieldValue(settingsFieldOpenAIAPIKey)); suffix != "" {
			action = "Stored key ends with " + suffix + ". Replace it here only if you want to change API keys."
		} else {
			action = "Next: paste a key here for the selected OpenAI API path, or go back and choose a local/off provider."
		}
	case settingsFieldOpenRouterAPIKey, settingsFieldDeepSeekAPIKey, settingsFieldMoonshotAPIKey:
		if suffix := maskedOpenAIKeySuffix(m.settingsFieldValue(m.settingsSelected)); suffix != "" {
			action = "Stored key ends with " + suffix + ". Replace it here only if you want to change this provider key."
		} else {
			action = "Next: paste the selected LCAgent model provider key, or go back and choose a different provider."
		}
	case settingsFieldMLXBaseURL, settingsFieldMLXAPIKey, settingsFieldMLXModel:
		action = "Next: adjust MLX only if your local endpoint or model is not the default, then save."
	case settingsFieldOllamaBaseURL, settingsFieldOllamaAPIKey, settingsFieldOllamaModel:
		action = "Next: adjust Ollama only if your local endpoint or model is not the default, then save."
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
	if m.settingsDrilldown != settingsDrilldownNone {
		actions[len(actions)-1] = renderDialogAction("Esc", "back", cancelActionKeyStyle, cancelActionTextStyle)
	}
	if m.activeSettingsSection().id == settingsSectionGettingStarted &&
		m.settingsDrilldown == settingsDrilldownNone &&
		settingsDrilldownForField(m.settingsSelected) != settingsDrilldownNone {
		actions = append([]string{
			renderDialogAction("Enter", "setup", navigateActionKeyStyle, navigateActionTextStyle),
		}, actions...)
	} else if m.settingsSelected == settingsFieldPrivacyPatterns {
		actions = append([]string{
			renderDialogAction("Enter", "edit", navigateActionKeyStyle, navigateActionTextStyle),
		}, actions...)
		label := "reveal"
		if m.settingsRevealPrivacy {
			label = "hide"
		}
		actions = append(actions, renderDialogAction("ctrl+r", label, navigateActionKeyStyle, navigateActionTextStyle))
	} else if settingsFieldUsesPicker(m.settingsSelected) {
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
			"Shared by Project reports, Boss chat, and LCAgent when they use direct OpenAI API.",
			settings.OpenAIAPIKey,
			512,
			settingsSectionGettingStarted,
		),
		newSensitiveSettingsFieldWithPlaceholder(
			"OpenRouter API key",
			"Shared by Project reports, Boss chat, and LCAgent when they use OpenRouter.",
			settings.OpenRouterAPIKey,
			512,
			"Paste OpenRouter API key",
			settingsSectionLCAgent,
		),
		newSensitiveSettingsFieldWithPlaceholder(
			"DeepSeek API key",
			"Shared by Project reports, Boss chat, and LCAgent when they use direct DeepSeek.",
			settings.DeepSeekAPIKey,
			512,
			"Paste DeepSeek API key",
			settingsSectionLCAgent,
		),
		newSensitiveSettingsFieldWithPlaceholder(
			"Moonshot API key",
			"Shared by Project reports, Boss chat, and LCAgent when they use direct Moonshot/Kimi.",
			settings.MoonshotAPIKey,
			512,
			"Paste Moonshot API key",
			settingsSectionLCAgent,
		),
		newSettingsField(
			"Boss chat",
			"Press Enter to choose Auto, direct API, local endpoint, or Off. This is separate from project analysis, so summaries can stay on another backend.",
			string(settings.BossChatBackend),
			32,
			settingsSectionGettingStarted,
		),
		newSettingsFieldWithPlaceholder(
			"Boss helm model",
			"High-grade Boss model for interactive answers, planning, risky choices, and control proposals. Leave blank for the displayed default or set LCROOM_BOSS_MODEL as an environment override.",
			settings.BossHelmModel,
			128,
			"Default: "+settingsBossHelmDefaultLabel(settings),
			settingsSectionAI,
		),
		newSettingsFieldWithPlaceholder(
			"Boss utility model",
			"Lower-cost Boss model for routine read-only query routing. Leave blank for the displayed utility default.",
			settings.BossUtilityModel,
			128,
			"Default: "+settingsBossUtilityDefaultLabel(settings),
			settingsSectionAI,
		),
		newSettingsField(
			"MLX base URL",
			"Used when Project reports or Boss chat uses MLX. Leave blank to use the default local OpenAI-compatible URL: http://127.0.0.1:8080/v1",
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
			"Used when Project reports or Boss chat uses Ollama. Leave blank to use the default OpenAI-compatible URL: http://127.0.0.1:11434/v1",
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
			settingsSectionLCAgent,
		),
		newSettingsField(
			"LCAgent env file",
			"Advanced fallback: optional dotenv file for LCAgent provider credentials. Saved provider keys above are used first.",
			settings.LCAgentEnvFile,
			1024,
			settingsSectionLCAgent,
		),
		newSettingsField(
			"LCAgent route preset",
			"Press Enter to choose a coding route bundle, or use Individual Fields to tune provider, model, autonomy, and profiles separately.",
			settings.LCAgentRoutePreset,
			32,
			settingsSectionLCAgent,
		),
		newSettingsField(
			"Main model provider",
			"Press Enter to choose the provider for the main LCAgent model.",
			settings.LCAgentProvider,
			32,
			settingsSectionLCAgent,
		),
		newSettingsFieldWithPlaceholder(
			"Main model",
			"Optional exact model ID for experimental LCAgent runs. Press Enter to check/fetch provider models when available, or type an exact custom ID. Leave blank to use the provider default.",
			settings.EmbeddedLCAgentModel,
			256,
			"Default: "+lcagentDefaultModelForProvider(firstNonEmptyTrimmed(lcagentProviderForRoutePreset(settings.LCAgentRoutePreset), settings.LCAgentProvider, "openrouter")),
			settingsSectionLCAgent,
		),
		newSettingsField(
			"Main reasoning",
			"Press Enter to choose reasoning effort, or use Provider Default to omit provider-specific effort controls.",
			settings.EmbeddedLCAgentReasoning,
			32,
			settingsSectionLCAgent,
		),
		newSettingsField(
			"LCAgent permissions",
			"Press Enter to choose the LCAgent permission level. Low allows workspace edits plus approved verification commands; Medium runs workspace commands without repeated approvals.",
			settings.LCAgentAuto,
			16,
			settingsSectionLCAgent,
		),
		newSettingsField(
			"LCAgent admin write",
			"Press Enter to choose whether LCAgent may use --admin-write for explicit absolute-path edits outside the project.",
			strconv.FormatBool(settings.LCAgentAdminWrite),
			8,
			settingsSectionLCAgent,
		),
		newSettingsField(
			"LCAgent tool profile",
			"Press Enter to choose file-tool read budgets. Balanced is conservative; generous is useful for large-context model experiments.",
			settings.LCAgentToolProfile,
			32,
			settingsSectionLCAgent,
		),
		newSettingsField(
			"LCAgent context profile",
			"Press Enter to choose provider-loop context handling. Large delays compaction when the selected model/provider can use bigger context.",
			settings.LCAgentContextProfile,
			32,
			settingsSectionLCAgent,
		),
		newSettingsField(
			"LCAgent timeout",
			"Provider HTTP request timeout for experimental LCAgent runs, for example 60m.",
			formatSettingsDuration(settings.LCAgentRequestTimeout),
			32,
			settingsSectionLCAgent,
		),
		newSettingsField(
			"Utility model provider",
			"Press Enter to choose the provider for helper-model work, use the Main Model, or turn utility refinement off.",
			settings.LCAgentUtilityProvider,
			32,
			settingsSectionLCAgent,
		),
		newSettingsFieldWithPlaceholder(
			"Utility model",
			"Secondary model used for helper work such as condensing oversized search results into read/search hints. Press Enter to check/fetch provider models when available.",
			settings.LCAgentUtilityModel,
			256,
			"Default: "+settingsLCAgentUtilityDefaultLabel(settings),
			settingsSectionLCAgent,
		),
		newSettingsField(
			"LCAgent web search",
			"Press Enter to choose Off, Exa, Google, or SearXNG.",
			settings.LCAgentWebSearchBackend,
			32,
			settingsSectionLCAgent,
		),
		newSensitiveSettingsFieldWithPlaceholder(
			"LCAgent search key",
			"Optional Exa or Google search API key for LCAgent web_search.",
			settings.LCAgentWebSearchAPIKey,
			256,
			"Paste Exa or Google search API key",
			settingsSectionLCAgent,
		),
		newSettingsField(
			"LCAgent search engine",
			"Optional Google Programmable Search engine ID for LCAgent web_search.",
			settings.LCAgentWebSearchEngineID,
			128,
			settingsSectionLCAgent,
		),
		newSettingsField(
			"LCAgent SearXNG URL",
			"Optional SearXNG base URL for LCAgent web_search, for example http://127.0.0.1:8888.",
			settings.LCAgentWebSearchURL,
			512,
			settingsSectionLCAgent,
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
			"Project reports",
			"Press Enter to choose the helper for summaries, classification, TODO help, and commit help.",
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

func newSettingsFieldWithPlaceholder(label, hint, value string, charLimit int, placeholder string, section settingsSectionID) settingsField {
	field := newSettingsField(label, hint, value, charLimit, section)
	field.input.Placeholder = placeholder
	return field
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
	settings.OpenRouterAPIKey = strings.TrimSpace(settings.OpenRouterAPIKey)
	settings.DeepSeekAPIKey = strings.TrimSpace(settings.DeepSeekAPIKey)
	settings.MoonshotAPIKey = strings.TrimSpace(settings.MoonshotAPIKey)
	settings.MLXBaseURL = strings.TrimSpace(settings.MLXBaseURL)
	settings.MLXAPIKey = strings.TrimSpace(settings.MLXAPIKey)
	settings.MLXModel = strings.TrimSpace(settings.MLXModel)
	settings.OllamaBaseURL = strings.TrimSpace(settings.OllamaBaseURL)
	settings.OllamaAPIKey = strings.TrimSpace(settings.OllamaAPIKey)
	settings.OllamaModel = strings.TrimSpace(settings.OllamaModel)
	settings.EmbeddedLCAgentModel = strings.TrimSpace(settings.EmbeddedLCAgentModel)
	settings.EmbeddedLCAgentReasoning = strings.TrimSpace(settings.EmbeddedLCAgentReasoning)
	settings.LCAgentPath = strings.TrimSpace(settings.LCAgentPath)
	settings.LCAgentEnvFile = strings.TrimSpace(settings.LCAgentEnvFile)
	settings.LCAgentRoutePreset = strings.TrimSpace(settings.LCAgentRoutePreset)
	settings.LCAgentProvider = strings.TrimSpace(settings.LCAgentProvider)
	settings.LCAgentAuto = strings.TrimSpace(settings.LCAgentAuto)
	settings.LCAgentToolProfile = strings.TrimSpace(settings.LCAgentToolProfile)
	settings.LCAgentContextProfile = strings.TrimSpace(settings.LCAgentContextProfile)
	settings.LCAgentUtilityProvider = strings.TrimSpace(settings.LCAgentUtilityProvider)
	settings.LCAgentUtilityModel = strings.TrimSpace(settings.LCAgentUtilityModel)
	settings.LCAgentWebSearchBackend = strings.TrimSpace(settings.LCAgentWebSearchBackend)
	settings.LCAgentWebSearchAPIKey = strings.TrimSpace(settings.LCAgentWebSearchAPIKey)
	settings.LCAgentWebSearchEngineID = strings.TrimSpace(settings.LCAgentWebSearchEngineID)
	settings.LCAgentWebSearchURL = strings.TrimSpace(settings.LCAgentWebSearchURL)
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
			if strings.TrimSpace(m.settingsFieldValue(settingsFieldOpenAIAPIKey)) == "" {
				return "OpenAI API backend selected. Paste an OpenAI API key in the detail field that appears for this path."
			}
			return "OpenAI API backend selected. Uses the saved API key for summaries and commit help."
		default:
			return backend.Label() + " backend selected. Project reports and commit help will use this provider."
		}
	case settingsFieldOpenAIAPIKey:
		if suffix := maskedOpenAIKeySuffix(field.input.Value()); suffix != "" {
			return "Used for OpenAI API backed features. Stored key ends with " + suffix + "."
		}
		settings := m.settingsDraftForInferenceStatus()
		if settings.AIBackend == config.AIBackendOpenAIAPI || settings.BossChatBackend == config.AIBackendOpenAIAPI || settingsLCAgentCredentialFieldRelevant(settings, "openai") {
			return field.hint + " The selected OpenAI API path still needs a saved key."
		}
		return field.hint
	case settingsFieldOpenRouterAPIKey:
		if suffix := maskedOpenAIKeySuffix(field.input.Value()); suffix != "" {
			return "Used for OpenRouter-backed features. Stored key ends with " + suffix + "."
		}
		settings := m.settingsDraftForInferenceStatus()
		if settings.AIBackend == config.AIBackendOpenRouter || settings.BossChatBackend == config.AIBackendOpenRouter || settingsLCAgentCredentialFieldRelevant(settings, "openrouter") {
			return field.hint + " The selected OpenRouter path still needs a saved key."
		}
		return field.hint
	case settingsFieldDeepSeekAPIKey:
		if suffix := maskedOpenAIKeySuffix(field.input.Value()); suffix != "" {
			return "Used for DeepSeek-backed features. Stored key ends with " + suffix + "."
		}
		settings := m.settingsDraftForInferenceStatus()
		if settings.AIBackend == config.AIBackendDeepSeek || settings.BossChatBackend == config.AIBackendDeepSeek || settingsLCAgentCredentialFieldRelevant(settings, "deepseek") {
			return field.hint + " The selected DeepSeek path still needs a saved key."
		}
		return field.hint
	case settingsFieldMoonshotAPIKey:
		if suffix := maskedOpenAIKeySuffix(field.input.Value()); suffix != "" {
			return "Used for Moonshot-backed features. Stored key ends with " + suffix + "."
		}
		settings := m.settingsDraftForInferenceStatus()
		if settings.AIBackend == config.AIBackendMoonshot || settings.BossChatBackend == config.AIBackendMoonshot || settingsLCAgentCredentialFieldRelevant(settings, "moonshot") {
			return field.hint + " The selected Moonshot path still needs a saved key."
		}
		return field.hint
	case settingsFieldBossChatBackend:
		switch config.AIBackend(strings.TrimSpace(field.input.Value())) {
		case config.AIBackendOpenAIAPI:
			return "Boss chat will use direct OpenAI API inference even if project analysis uses another backend."
		case config.AIBackendOpenRouter, config.AIBackendDeepSeek, config.AIBackendMoonshot:
			return "Boss chat will use direct " + config.AIBackend(strings.TrimSpace(field.input.Value())).Label() + " API inference even if project analysis uses another backend."
		case config.AIBackendMLX:
			return "Boss chat will use the MLX OpenAI-compatible endpoint and model fields below."
		case config.AIBackendOllama:
			return "Boss chat will use the Ollama OpenAI-compatible endpoint and model fields below."
		case config.AIBackendDisabled:
			return "Boss chat will stay offline, while project analysis keeps using its own configured backend."
		case config.AIBackendUnset:
			return "Leave blank to auto-use openai_api when an OpenAI API key is saved; choose any listed API backend explicitly for boss chat."
		default:
			return field.hint
		}
	case settingsFieldBossChatModel:
		if model := strings.TrimSpace(field.input.Value()); model != "" {
			return "Boss helm calls will request model " + model + ". LCROOM_BOSS_MODEL still wins if set in the environment."
		}
		settings := m.settingsDraftForInferenceStatus()
		return "Blank uses " + settingsBossHelmDefaultLabel(settings) + ". " + brand.BossAssistantModelEnvVar + " still wins if set in the environment."
	case settingsFieldBossUtilityModel:
		if model := strings.TrimSpace(field.input.Value()); model != "" {
			return "Routine Boss utility calls will request model " + model + ". LCROOM_BOSS_MODEL still overrides all Boss model choices if set."
		}
		settings := m.settingsDraftForInferenceStatus()
		return "Blank uses " + settingsBossUtilityDefaultLabel(settings) + ". " + brand.BossAssistantModelEnvVar + " still overrides all Boss model choices if set."
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
	case settingsFieldLCAgentRoutePreset:
		switch strings.ToLower(strings.TrimSpace(field.input.Value())) {
		case "":
			return "LCAgent launches will use the individual provider, model, autonomy, tool, and context fields below."
		case "balanced":
			return "Balanced uses DeepSeek V4 Pro through OpenRouter with conservative coding budgets."
		case "quality":
			return "Quality uses GPT-5.5 through the direct OpenAI route with low reasoning and larger retained context."
		case "cheap-scout", "cheap", "scout":
			return "Cheap scout uses a lower-cost DeepSeek V4 Flash route for bounded read-first work."
		default:
			return field.hint
		}
	case settingsFieldLCAgentProvider:
		switch strings.ToLower(strings.TrimSpace(field.input.Value())) {
		case "", "openrouter":
			return "The Main Model will use OpenRouter-compatible tool calls and model IDs such as deepseek/deepseek-v4-pro."
		case "deepseek":
			return "The Main Model will call DeepSeek directly and use direct DeepSeek model IDs."
		case "moonshot":
			return "The Main Model will call Moonshot directly and use Kimi model IDs."
		case "openai":
			return "The Main Model will call the OpenAI Responses API directly."
		default:
			return field.hint
		}
	case settingsFieldLCAgentModel:
		if model := strings.TrimSpace(field.input.Value()); model != "" {
			return "The Main Model will request " + model + ". Press Enter to check this provider's model list when available."
		}
		settings := m.settingsDraftForInferenceStatus()
		return "Blank uses the Main Model default: " + settingsLCAgentMainModel(settings) + ". Press Enter to fetch/select provider models when available, or type an exact ID."
	case settingsFieldLCAgentReasoning:
		if effort := strings.TrimSpace(field.input.Value()); effort != "" {
			return "The Main Model will request reasoning effort " + effort + " when the selected provider supports it."
		}
		return field.hint
	case settingsFieldLCAgentUtilityProvider:
		switch settingsLCAgentUtilityProviderValue(field.input.Value()) {
		case "main":
			return "The Utility Model will use the same provider and model as the Main Model."
		case "off":
			return "Oversized search results will not use a utility model; deterministic compact search still works when requested."
		case "openrouter":
			return "The Utility Model will use OpenRouter. Leave the model blank for the OpenRouter Main Model default."
		case "deepseek":
			return "The Utility Model will use direct DeepSeek. Leave the model blank for the DeepSeek Main Model default."
		case "openai":
			return "The Utility Model will use direct OpenAI. Leave the model blank for the OpenAI Main Model default."
		case "moonshot":
			return "The Utility Model will use direct Moonshot/Kimi."
		default:
			return field.hint
		}
	case settingsFieldLCAgentUtilityModel:
		if model := strings.TrimSpace(field.input.Value()); model != "" {
			return "The Utility Model will request " + model + ". Press Enter to check this provider's model list when available."
		}
		settings := m.settingsDraftForInferenceStatus()
		return "Blank uses " + settingsLCAgentUtilityDefaultLabel(settings) + ". Press Enter to fetch/select provider models when available, or type an exact ID."
	case settingsFieldLCAgentAuto:
		switch strings.ToLower(strings.TrimSpace(field.input.Value())) {
		case "off":
			return "Off denies file edits and non-read commands. Use it for inspect-and-explain runs."
		case "", "low":
			return "Low allows workspace file edits, read-only commands, and approved verifier commands such as tests, lint, typecheck, or build. Broader commands ask for approval."
		case "medium":
			return "Medium allows workspace-contained commands without repeated approvals. Writes outside the workspace still require admin write."
		default:
			return field.hint
		}
	case settingsFieldLCAgentAdminWrite:
		if strings.EqualFold(strings.TrimSpace(field.input.Value()), "true") {
			return "LCAgent will pass --admin-write, allowing write tools to edit absolute paths outside the workspace when a task explicitly needs that."
		}
		return "Default false: absolute writes outside the workspace are denied; absolute project paths that resolve inside the workspace still work."
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
	case settingsFieldLCAgentWebSearchBackend:
		switch strings.ToLower(strings.TrimSpace(field.input.Value())) {
		case "exa":
			return "LCAgent will expose web_search using Exa when an Exa API key is saved."
		case "google":
			return "LCAgent will expose web_search using Google Programmable Search when the key and search engine ID are saved."
		case "searxng":
			return "LCAgent will expose web_search using the configured SearXNG base URL."
		case "", "off":
			return "LCAgent web_search will be unavailable, and new sessions will show a setup warning."
		default:
			return field.hint
		}
	case settingsFieldLCAgentWebSearchAPIKey:
		if suffix := maskedOpenAIKeySuffix(field.input.Value()); suffix != "" {
			return "Used for Exa or Google backed LCAgent web_search. Stored key ends with " + suffix + "."
		}
		return field.hint
	case settingsFieldLCAgentWebSearchEngineID:
		if value := strings.TrimSpace(field.input.Value()); value != "" {
			return "Google-backed LCAgent web_search will use search engine " + value + "."
		}
		return field.hint
	case settingsFieldLCAgentWebSearchURL:
		if value := strings.TrimSpace(field.input.Value()); value != "" {
			return "SearXNG-backed LCAgent web_search will use " + value + "."
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
