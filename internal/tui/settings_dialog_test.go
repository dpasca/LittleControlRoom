package tui

import (
	"github.com/charmbracelet/bubbles/textinput"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
	"github.com/charmbracelet/x/ansi"
	"github.com/muesli/termenv"
	"lcroom/internal/aibackend"
	"lcroom/internal/commands"
	"lcroom/internal/config"
	"lcroom/internal/model"
	"os"
	"path/filepath"
	"runtime"
	"slices"
	"strings"
	"testing"
)

func TestSettingsBossChatBackendPickerUpdatesField(t *testing.T) {
	m := Model{
		settingsMode:   true,
		settingsFields: newSettingsFields(config.EditableSettingsFromAppConfig(config.Default())),
		width:          100,
		height:         24,
	}
	updated, _ := m.openSettingsDrilldown(settingsDrilldownBossChat)
	m = updated.(Model)

	updated, cmd := m.updateSettingsMode(tea.KeyMsg{Type: tea.KeyEnter})
	got := updated.(Model)
	if cmd != nil {
		t.Fatalf("opening Chat picker should not queue a command")
	}
	if !got.settingsBossChatPickerVisible {
		t.Fatalf("Chat picker should open")
	}

	updated, _ = got.updateSettingsBossChatBackendPickerMode(tea.KeyMsg{Type: tea.KeyDown})
	got = updated.(Model)
	updated, _ = got.updateSettingsBossChatBackendPickerMode(tea.KeyMsg{Type: tea.KeyEnter})
	got = updated.(Model)
	if got.settingsBossChatPickerVisible {
		t.Fatalf("Chat picker should close after choosing")
	}
	if got.settingsFieldValue(settingsFieldBossChatBackend) != string(config.AIBackendOpenAIAPI) {
		t.Fatalf("Chat backend field = %q, want openai_api", got.settingsFieldValue(settingsFieldBossChatBackend))
	}
}

func TestSettingsBossChatOllamaThinkingFieldUsesChoicePicker(t *testing.T) {
	settings := config.EditableSettingsFromAppConfig(config.Default())
	settings.BossChatBackend = config.AIBackendOllama
	settings.OllamaModel = "gemma4:12b-mlx"

	m := Model{
		settingsMode:     true,
		settingsFields:   newSettingsFields(settings),
		settingsBaseline: &settings,
		width:            100,
		height:           24,
	}
	updated, _ := m.openSettingsDrilldown(settingsDrilldownBossChat)
	got := updated.(Model)
	fields := got.visibleSettingsDrilldownFieldOrder(settingsDrilldownBossChat)
	if !slices.Contains(fields, settingsFieldBossChatOllamaThinking) {
		t.Fatalf("Chat Ollama drilldown fields = %#v, want thinking field", fields)
	}
	rendered := ansi.Strip(got.renderSettingsContent(100, 24))
	for _, want := range []string{"Ollama Thinking", "Chat Ollama thinking"} {
		if !strings.Contains(rendered, want) {
			t.Fatalf("settings content missing %q:\n%s", want, rendered)
		}
	}

	_ = got.setSettingsSelection(settingsFieldBossChatOllamaThinking)
	updated, _ = got.openSettingsChoicePicker(settingsFieldBossChatOllamaThinking)
	got = updated.(Model)
	if got.settingsChoicePicker == nil {
		t.Fatalf("Chat Ollama thinking should open choice picker")
	}
	rendered = ansi.Strip(got.renderSettingsChoicePickerContent(56, 18))
	for _, want := range []string{"Chat Ollama Thinking", "Off", "On"} {
		if !strings.Contains(rendered, want) {
			t.Fatalf("choice picker missing %q:\n%s", want, rendered)
		}
	}
	updated, _ = got.updateSettingsChoicePickerMode(tea.KeyMsg{Type: tea.KeyEnter})
	got = updated.(Model)
	if got.settingsFieldValue(settingsFieldBossChatOllamaThinking) != "true" {
		t.Fatalf("Chat Ollama thinking = %q, want true", got.settingsFieldValue(settingsFieldBossChatOllamaThinking))
	}
}

func TestSettingsLCAgentReasoningPickerUsesProviderOptions(t *testing.T) {
	settings := config.EditableSettingsFromAppConfig(config.Default())
	settings.LCAgentProvider = "deepseek"
	m := Model{
		settingsMode:   true,
		settingsFields: newSettingsFields(settings),
		width:          100,
		height:         24,
	}

	options := m.settingsChoiceOptionsForField(settingsFieldLCAgentReasoning)
	labels := map[string]bool{}
	for _, option := range options {
		labels[option.Label] = true
	}
	for _, want := range []string{"Provider Default", "High", "Max"} {
		if !labels[want] {
			t.Fatalf("DeepSeek reasoning picker labels = %#v, want %q", labels, want)
		}
	}
	if labels["Low"] || labels["Medium"] {
		t.Fatalf("DeepSeek reasoning picker should not expose low/medium labels: %#v", labels)
	}

	m.settingsFields[settingsFieldLCAgentProvider].input.SetValue("openrouter")
	m.settingsFields[settingsFieldLCAgentRoutePreset].input.SetValue("balanced")
	options = m.settingsChoiceOptionsForField(settingsFieldLCAgentReasoning)
	labels = map[string]bool{}
	for _, option := range options {
		labels[option.Label] = true
	}
	if !labels["Max"] {
		t.Fatalf("Balanced route preset should expose DeepSeek Max reasoning, labels = %#v", labels)
	}

	m.settingsFields[settingsFieldLCAgentRoutePreset].input.SetValue("")
	m.settingsFields[settingsFieldLCAgentProvider].input.SetValue("moonshot")
	options = m.settingsChoiceOptionsForField(settingsFieldLCAgentReasoning)
	if len(options) != 1 || options[0].Label != "Provider Default" {
		t.Fatalf("Moonshot reasoning picker options = %#v, want only Provider Default", options)
	}
}

func TestCommandEnterOpensRunCommandDialogWhenUnset(t *testing.T) {
	tests := []struct {
		name    string
		command string
	}{
		{name: "run", command: "/run"},
		{name: "start alias", command: "/start"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			input := textinput.New()
			input.SetValue(tt.command)

			m := Model{
				projects: []model.ProjectSummary{{
					Name:          "demo",
					Path:          "/tmp/demo",
					PresentOnDisk: true,
				}},
				selected:     0,
				commandMode:  true,
				commandInput: input,
				width:        100,
				height:       24,
			}
			m.syncCommandSelection()

			updated, cmd := m.updateCommandMode(tea.KeyMsg{Type: tea.KeyEnter})
			got := updated.(Model)
			if got.commandMode {
				t.Fatalf("command mode should close after %s", tt.command)
			}
			if got.runCommandDialog == nil {
				t.Fatalf("run command dialog should open after %s when no command is saved", tt.command)
			}
			if got.runCommandDialog.ProjectPath != "/tmp/demo" {
				t.Fatalf("run command dialog project path = %q, want /tmp/demo", got.runCommandDialog.ProjectPath)
			}
			if cmd == nil {
				t.Fatalf("%s should return a focus command for the input", tt.command)
			}
		})
	}
}

func TestOpenRunCommandDialogLoadsSuggestionAsync(t *testing.T) {
	t.Parallel()

	projectPath := t.TempDir()
	if err := os.MkdirAll(filepath.Join(projectPath, "bin"), 0o755); err != nil {
		t.Fatalf("mkdir bin: %v", err)
	}
	if err := os.WriteFile(filepath.Join(projectPath, "bin", "dev"), []byte("#!/bin/sh\n"), 0o755); err != nil {
		t.Fatalf("write bin/dev: %v", err)
	}

	m := Model{}
	cmd := m.openRunCommandDialog(model.ProjectSummary{
		Name:          "demo",
		Path:          projectPath,
		PresentOnDisk: true,
	}, true)
	if m.runCommandDialog == nil {
		t.Fatal("openRunCommandDialog() should open the dialog")
	}
	if !m.runCommandDialog.SuggestionPending {
		t.Fatal("openRunCommandDialog() should mark suggestion loading pending when the command is empty")
	}

	for _, msg := range collectCmdMsgs(cmd) {
		updated, _ := m.Update(msg)
		m = updated.(Model)
	}

	if got := m.runCommandDialog.Input.Value(); got != "./bin/dev" {
		t.Fatalf("suggested command = %q, want %q", got, "./bin/dev")
	}
	if got := m.runCommandDialog.SuggestionReason; got != "Found bin/dev in the project root." {
		t.Fatalf("suggestion reason = %q, want bin/dev hint", got)
	}
	if m.runCommandDialog.SuggestionPending {
		t.Fatal("suggestion pending should clear after the async lookup returns")
	}
	if got := m.runCommandDialog.Input.AvailableSuggestions(); !slices.Contains(got, "./bin/dev") {
		t.Fatalf("available run command suggestions = %#v, want ./bin/dev", got)
	}
}

func TestRunCommandDialogAutocompletesDetectedCommandWhileEditing(t *testing.T) {
	t.Parallel()

	projectPath := t.TempDir()
	manifest := `{"scripts":{"dev":"vite"},"packageManager":"pnpm@9.0.0"}`
	if err := os.WriteFile(filepath.Join(projectPath, "package.json"), []byte(manifest), 0o644); err != nil {
		t.Fatalf("write package.json: %v", err)
	}
	if err := os.Mkdir(filepath.Join(projectPath, "pnpm-cache"), 0o755); err != nil {
		t.Fatalf("mkdir pnpm-cache: %v", err)
	}

	m := Model{}
	cmd := m.openRunCommandDialog(model.ProjectSummary{
		Name:          "demo",
		Path:          projectPath,
		PresentOnDisk: true,
		RunCommand:    "pn",
	}, false)
	if m.runCommandDialog == nil {
		t.Fatal("openRunCommandDialog() should open the dialog")
	}
	if !m.runCommandDialog.SuggestionPending {
		t.Fatal("openRunCommandDialog() should load completions for an existing command")
	}

	for _, msg := range collectCmdMsgs(cmd) {
		updated, _ := m.Update(msg)
		m = updated.(Model)
	}

	if got := m.runCommandDialog.Input.Value(); got != "pn" {
		t.Fatalf("command before completion = %q, want typed prefix pn", got)
	}
	if got := m.runCommandDialog.Input.CurrentSuggestion(); got != "pnpm dev" {
		t.Fatalf("current completion = %q, want pnpm dev", got)
	}

	updated, _ := m.updateRunCommandDialogMode(tea.KeyMsg{Type: tea.KeyTab})
	m = updated.(Model)
	if got := m.runCommandDialog.Input.Value(); got != "pnpm dev" {
		t.Fatalf("command after Tab = %q, want pnpm dev", got)
	}
	if got := m.runCommandDialog.SuggestionReason; got != `Found package.json script "dev".` {
		t.Fatalf("completion reason = %q", got)
	}
	if rendered := ansi.Strip(m.renderRunCommandContent(80)); !strings.Contains(rendered, "Tab completes") {
		t.Fatalf("run command dialog should advertise completion controls:\n%s", rendered)
	}
}

func TestRunCommandDialogShowsAndCyclesMatchingProjectCommands(t *testing.T) {
	t.Parallel()

	projectPath := t.TempDir()
	makefile := `.PHONY: test tui tui-parallel serve

test:
	go test ./...

tui:
	go run ./cmd/app

tui-parallel:
	go run ./cmd/app --parallel

serve:
	go run ./cmd/app serve
`
	if err := os.WriteFile(filepath.Join(projectPath, "Makefile"), []byte(makefile), 0o644); err != nil {
		t.Fatalf("write Makefile: %v", err)
	}

	m := Model{}
	cmd := m.openRunCommandDialog(model.ProjectSummary{
		Name:          "demo",
		Path:          projectPath,
		PresentOnDisk: true,
		RunCommand:    "make tu",
	}, false)
	for _, msg := range collectCmdMsgs(cmd) {
		updated, _ := m.Update(msg)
		m = updated.(Model)
	}

	rendered := ansi.Strip(m.renderRunCommandContent(88))
	for _, want := range []string{"Autocomplete", "make tui", "make tui-parallel", "Tab completes", "Up/Down selects"} {
		if !strings.Contains(rendered, want) {
			t.Fatalf("run command autocomplete missing %q:\n%s", want, rendered)
		}
	}
	if got := m.runCommandDialog.Input.CurrentSuggestion(); got != "make tui" {
		t.Fatalf("initial completion = %q, want make tui", got)
	}

	updated, _ := m.Update(tea.KeyMsg{Type: tea.KeyDown})
	m = updated.(Model)
	if got := m.runCommandDialog.Input.CurrentSuggestion(); got != "make tui-parallel" {
		t.Fatalf("completion after Down = %q, want make tui-parallel", got)
	}

	updated, _ = m.Update(tea.KeyMsg{Type: tea.KeyTab})
	m = updated.(Model)
	if got := m.runCommandDialog.Input.Value(); got != "make tui-parallel" {
		t.Fatalf("command after Tab = %q, want make tui-parallel", got)
	}
}

func TestRunCommandDialogCompletesExecutableProjectPathByDirectory(t *testing.T) {
	t.Parallel()

	projectPath := t.TempDir()
	toolsPath := filepath.Join(projectPath, "tools")
	if err := os.MkdirAll(toolsPath, 0o755); err != nil {
		t.Fatalf("mkdir tools: %v", err)
	}
	scriptName := "build_and_run_desktop.sh"
	if runtime.GOOS == "windows" {
		scriptName = "build_and_run_desktop.cmd"
	}
	scriptPath := filepath.Join(toolsPath, scriptName)
	if err := os.WriteFile(scriptPath, []byte("#!/bin/sh\n"), 0o755); err != nil {
		t.Fatalf("write script: %v", err)
	}
	if err := os.WriteFile(filepath.Join(toolsPath, "build_notes.txt"), []byte("notes\n"), 0o644); err != nil {
		t.Fatalf("write non-executable file: %v", err)
	}

	m := Model{}
	cmd := m.openRunCommandDialog(model.ProjectSummary{
		Name:          "demo",
		Path:          projectPath,
		PresentOnDisk: true,
		RunCommand:    "./too",
	}, false)
	if rendered := ansi.Strip(m.renderRunCommandContent(88)); !strings.Contains(rendered, "Checking ./ for path completions") {
		t.Fatalf("path lookup should have an explicit pending state:\n%s", rendered)
	}
	for _, msg := range collectCmdMsgs(cmd) {
		updated, _ := m.Update(msg)
		m = updated.(Model)
	}

	if got := m.runCommandDialog.Input.CurrentSuggestion(); got != "./tools/" {
		t.Fatalf("directory completion = %q, want ./tools/", got)
	}
	updated, cmd := m.Update(tea.KeyMsg{Type: tea.KeyTab})
	m = updated.(Model)
	if got := m.runCommandDialog.Input.Value(); got != "./tools/" {
		t.Fatalf("command after directory Tab = %q, want ./tools/", got)
	}
	for _, msg := range collectCmdMsgs(cmd) {
		updated, _ = m.Update(msg)
		m = updated.(Model)
	}

	expectedScript := "./tools/" + scriptName
	if got := m.runCommandDialog.Input.CurrentSuggestion(); got != expectedScript {
		t.Fatalf("script completion = %q", got)
	}
	if suggestions := m.runCommandDialog.Input.AvailableSuggestions(); slices.Contains(suggestions, "./tools/build_notes.txt") {
		t.Fatalf("first command word should not offer non-executable files: %#v", suggestions)
	}
	updated, _ = m.Update(tea.KeyMsg{Type: tea.KeyTab})
	m = updated.(Model)
	if got := m.runCommandDialog.Input.Value(); got != expectedScript {
		t.Fatalf("command after script Tab = %q", got)
	}
	if rendered := ansi.Strip(m.renderRunCommandContent(88)); !strings.Contains(rendered, "Executable file under the selected project") {
		t.Fatalf("completed executable should retain a path hint:\n%s", rendered)
	}
}

func TestRunCommandDialogCompletesBarePathAndEnterUsesSelection(t *testing.T) {
	t.Parallel()

	projectPath := t.TempDir()
	toolsPath := filepath.Join(projectPath, "tools")
	if err := os.MkdirAll(toolsPath, 0o755); err != nil {
		t.Fatalf("mkdir tools: %v", err)
	}
	scriptName := "build_and_run_desktop.sh"
	if runtime.GOOS == "windows" {
		scriptName = "build_and_run_desktop.cmd"
	}
	if err := os.WriteFile(filepath.Join(toolsPath, scriptName), []byte("#!/bin/sh\n"), 0o755); err != nil {
		t.Fatalf("write script: %v", err)
	}

	m := Model{}
	cmd := m.openRunCommandDialog(model.ProjectSummary{
		Name:          "demo",
		Path:          projectPath,
		PresentOnDisk: true,
		RunCommand:    "too",
	}, true)
	for _, msg := range collectCmdMsgs(cmd) {
		updated, _ := m.Update(msg)
		m = updated.(Model)
	}

	if got := m.runCommandDialog.Input.CurrentSuggestion(); got != "tools/" {
		t.Fatalf("bare directory completion = %q, want tools/", got)
	}
	updated, cmd := m.Update(tea.KeyMsg{Type: tea.KeyEnter})
	m = updated.(Model)
	if m.runCommandDialog == nil {
		t.Fatal("Enter on a directory completion should keep the dialog open")
	}
	if got := m.runCommandDialog.Input.Value(); got != "tools/" {
		t.Fatalf("command after directory Enter = %q, want tools/", got)
	}
	for _, msg := range collectCmdMsgs(cmd) {
		updated, _ = m.Update(msg)
		m = updated.(Model)
	}

	expectedScript := "tools/" + scriptName
	if got := m.runCommandDialog.Input.CurrentSuggestion(); got != expectedScript {
		t.Fatalf("script completion = %q, want %q", got, expectedScript)
	}
	updated, cmd = m.Update(tea.KeyMsg{Type: tea.KeyEnter})
	m = updated.(Model)
	if m.runCommandDialog != nil {
		t.Fatal("Enter on an executable completion should submit the dialog")
	}
	msgs := collectCmdMsgs(cmd)
	if len(msgs) != 1 {
		t.Fatalf("save command messages = %#v, want one", msgs)
	}
	saved, ok := msgs[0].(runCommandSavedMsg)
	if !ok {
		t.Fatalf("save command message = %T, want runCommandSavedMsg", msgs[0])
	}
	if saved.command != expectedScript {
		t.Fatalf("submitted command = %q, want highlighted completion %q", saved.command, expectedScript)
	}
}

func TestRunCommandDialogCompletesNonExecutablePathArgument(t *testing.T) {
	t.Parallel()

	projectPath := t.TempDir()
	toolsPath := filepath.Join(projectPath, "tools")
	if err := os.MkdirAll(toolsPath, 0o755); err != nil {
		t.Fatalf("mkdir tools: %v", err)
	}
	if err := os.WriteFile(filepath.Join(toolsPath, "setup script.sh"), []byte("echo setup\n"), 0o644); err != nil {
		t.Fatalf("write script: %v", err)
	}

	m := Model{}
	cmd := m.openRunCommandDialog(model.ProjectSummary{
		Name:          "demo",
		Path:          projectPath,
		PresentOnDisk: true,
		RunCommand:    "bash ./tools/set",
	}, false)
	for _, msg := range collectCmdMsgs(cmd) {
		updated, _ := m.Update(msg)
		m = updated.(Model)
	}

	if got := m.runCommandDialog.Input.CurrentSuggestion(); got != `bash ./tools/setup\ script.sh` {
		t.Fatalf("argument completion = %q", got)
	}
	updated, _ := m.Update(tea.KeyMsg{Type: tea.KeyTab})
	m = updated.(Model)
	if got := m.runCommandDialog.Input.Value(); got != `bash ./tools/setup\ script.sh` {
		t.Fatalf("command after argument Tab = %q", got)
	}
}

func TestRunCommandDialogExplainsWhenNoProjectCommandsAreDetected(t *testing.T) {
	t.Parallel()

	projectPath := t.TempDir()
	m := Model{}
	cmd := m.openRunCommandDialog(model.ProjectSummary{
		Name:          "empty",
		Path:          projectPath,
		PresentOnDisk: true,
	}, false)
	for _, msg := range collectCmdMsgs(cmd) {
		updated, _ := m.Update(msg)
		m = updated.(Model)
	}

	if !m.runCommandDialog.SuggestionChecked {
		t.Fatal("run command autocomplete lookup should be marked complete")
	}
	rendered := ansi.Strip(m.renderRunCommandContent(80))
	if !strings.Contains(rendered, "No conventional commands detected; start typing a project path to complete it.") {
		t.Fatalf("run command dialog should explain the empty autocomplete state:\n%s", rendered)
	}
}

func TestRunCommandSuggestionDoesNotOverwriteTypedInput(t *testing.T) {
	t.Parallel()

	projectPath := t.TempDir()
	if err := os.MkdirAll(filepath.Join(projectPath, "bin"), 0o755); err != nil {
		t.Fatalf("mkdir bin: %v", err)
	}
	if err := os.WriteFile(filepath.Join(projectPath, "bin", "dev"), []byte("#!/bin/sh\n"), 0o755); err != nil {
		t.Fatalf("write bin/dev: %v", err)
	}

	m := Model{}
	cmd := m.openRunCommandDialog(model.ProjectSummary{
		Name:          "demo",
		Path:          projectPath,
		PresentOnDisk: true,
	}, true)
	if m.runCommandDialog == nil {
		t.Fatal("openRunCommandDialog() should open the dialog")
	}
	m.runCommandDialog.Input.SetValue("npm run local")

	for _, msg := range collectCmdMsgs(cmd) {
		updated, _ := m.Update(msg)
		m = updated.(Model)
	}

	if got := m.runCommandDialog.Input.Value(); got != "npm run local" {
		t.Fatalf("typed command = %q, want %q", got, "npm run local")
	}
	if got := m.runCommandDialog.SuggestionReason; got != "" {
		t.Fatalf("suggestion reason = %q, want empty when the user has already typed a command", got)
	}
	if m.runCommandDialog.SuggestionPending {
		t.Fatal("suggestion pending should clear even when the suggestion is ignored")
	}
}

func TestRestartCommandQueuesRuntimeRestart(t *testing.T) {
	m := Model{
		projects: []model.ProjectSummary{{
			Name:          "demo",
			Path:          "/tmp/demo",
			PresentOnDisk: true,
			RunCommand:    "pnpm dev",
		}},
		selected: 0,
	}

	updated, cmd := m.dispatchCommand(commands.Invocation{Kind: commands.KindRestart})
	got := updated.(Model)
	if got.status != "Restarting runtime..." {
		t.Fatalf("status = %q, want restarting status", got.status)
	}
	if cmd == nil {
		t.Fatalf("dispatchCommand(/restart) should return a restart command")
	}
}

func TestRestartCommandRequiresSavedOrActiveCommand(t *testing.T) {
	m := Model{
		projects: []model.ProjectSummary{{
			Name:          "demo",
			Path:          "/tmp/demo",
			PresentOnDisk: true,
		}},
		selected: 0,
	}

	updated, cmd := m.dispatchCommand(commands.Invocation{Kind: commands.KindRestart})
	got := updated.(Model)
	if got.status != "Runtime command is not set" {
		t.Fatalf("status = %q, want missing runtime command status", got.status)
	}
	if cmd != nil {
		t.Fatalf("dispatchCommand(/restart) should fail locally when no runtime command exists")
	}
}

func TestDetailFocusScrollsViewportWithoutChangingSelection(t *testing.T) {
	m := Model{
		projects: []model.ProjectSummary{{
			Path:                            "/tmp/demo",
			Name:                            "demo",
			Status:                          model.StatusIdle,
			PresentOnDisk:                   true,
			LatestSessionClassification:     model.ClassificationCompleted,
			LatestSessionClassificationType: model.SessionCategoryNeedsFollowUp,
		}},
		selected:    0,
		focusedPane: focusDetail,
		width:       100,
		height:      18,
		detail: model.ProjectDetail{
			Reasons: []model.AttentionReason{
				{Text: "one"},
				{Text: "two"},
				{Text: "three"},
				{Text: "four"},
				{Text: "five"},
				{Text: "six"},
				{Text: "seven"},
				{Text: "eight"},
			},
			LatestSessionClassification: &model.SessionClassification{
				Status:   model.ClassificationCompleted,
				Category: model.SessionCategoryNeedsFollowUp,
				Summary:  "A concrete next step still remains.",
			},
		},
	}
	m.syncDetailViewport(false)

	before := m.detailViewport.YOffset
	updated, _ := m.updateNormalMode(tea.KeyMsg{Type: tea.KeyPgDown})
	got := updated.(Model)
	if got.selected != 0 {
		t.Fatalf("detail scrolling should not change selected project, got %d", got.selected)
	}
	if got.detailViewport.YOffset <= before {
		t.Fatalf("detail scrolling should advance viewport offset, before=%d after=%d", before, got.detailViewport.YOffset)
	}
}

func TestViewWithCommandModeRespectsHeight(t *testing.T) {
	input := textinput.New()
	input.SetValue("/")

	m := Model{
		projects: []model.ProjectSummary{{
			Name:                             "demo",
			Path:                             "/tmp/demo",
			Status:                           model.StatusIdle,
			PresentOnDisk:                    true,
			LatestSessionClassification:      model.ClassificationCompleted,
			LatestSessionClassificationType:  model.SessionCategoryCompleted,
			LatestSessionSummary:             "Work appears complete for now.",
			LatestSessionFormat:              "modern",
			LatestSessionDetectedProjectPath: "/tmp/demo",
		}},
		selected:     0,
		commandMode:  true,
		commandInput: input,
		width:        100,
		height:       24,
	}
	m.syncCommandSelection()
	m.syncDetailViewport(false)

	rendered := m.View()
	if got := len(strings.Split(rendered, "\n")); got != m.height {
		t.Fatalf("View() line count = %d, want terminal height %d; render was %q", got, m.height, rendered)
	}
	if !strings.Contains(rendered, "Command Palette") {
		t.Fatalf("View() missing command palette: %q", rendered)
	}
	if !strings.Contains(rendered, "Selected project: demo") {
		t.Fatalf("View() missing selected project context: %q", rendered)
	}
	visible := ansi.Strip(rendered)
	if !strings.Contains(visible, "Main 0") || !strings.Contains(visible, "Path:") {
		t.Fatalf("View() should preserve background list and detail context under the command palette: %q", rendered)
	}
}

func TestCommandPaletteRendersColoredActionLegend(t *testing.T) {
	input := textinput.New()
	input.SetValue("/")

	m := Model{
		commandMode:  true,
		commandInput: input,
		width:        100,
		height:       24,
	}
	m.syncCommandSelection()

	rendered := ansi.Strip(m.renderCommandPaletteContent(72))
	if !strings.Contains(rendered, "Enter") || !strings.Contains(rendered, "run") {
		t.Fatalf("command palette should render Enter run action: %q", rendered)
	}
	if !strings.Contains(rendered, "Tab") || !strings.Contains(rendered, "complete") {
		t.Fatalf("command palette should render Tab complete action: %q", rendered)
	}
	if !strings.Contains(rendered, "Up/Down") || !strings.Contains(rendered, "choose") {
		t.Fatalf("command palette should render Up/Down choose action: %q", rendered)
	}
	if !strings.Contains(rendered, "Esc") || !strings.Contains(rendered, "cancel") {
		t.Fatalf("command palette should render Esc cancel action: %q", rendered)
	}
}

func TestCommandPaletteDoesNotShowWorktreeCommandHintForLinkedWorktree(t *testing.T) {
	input := textinput.New()
	input.SetValue("/")

	rootPath := "/tmp/repo"
	childPath := "/tmp/repo--feat-parallel-lane"
	m := Model{
		allProjects: []model.ProjectSummary{
			{
				Name:             "repo",
				Path:             rootPath,
				PresentOnDisk:    true,
				WorktreeRootPath: rootPath,
				WorktreeKind:     model.WorktreeKindMain,
				RepoBranch:       "master",
			},
			{
				Name:                 "repo--feat-parallel-lane",
				Path:                 childPath,
				PresentOnDisk:        true,
				WorktreeRootPath:     rootPath,
				WorktreeKind:         model.WorktreeKindLinked,
				WorktreeParentBranch: "master",
				RepoBranch:           "feat/parallel-lane",
				RepoDirty:            true,
			},
		},
		visibility:   visibilityAllFolders,
		sortMode:     sortByAttention,
		commandMode:  true,
		commandInput: input,
		width:        100,
		height:       24,
	}
	m.rebuildProjectList(childPath)
	m.syncCommandSelection()

	rendered := ansi.Strip(m.renderCommandPaletteContent(72))
	if strings.Contains(rendered, "Worktrees: try") {
		t.Fatalf("command palette should not render the worktree slash-command legend, got %q", rendered)
	}
}

func TestCommandPaletteDoesNotShowWorktreeCommandHintForCleanLinkedWorktree(t *testing.T) {
	input := textinput.New()
	input.SetValue("/")

	rootPath := "/tmp/repo"
	childPath := "/tmp/repo--feat-parallel-lane"
	m := Model{
		allProjects: []model.ProjectSummary{
			{
				Name:             "repo",
				Path:             rootPath,
				PresentOnDisk:    true,
				WorktreeRootPath: rootPath,
				WorktreeKind:     model.WorktreeKindMain,
				RepoBranch:       "master",
			},
			{
				Name:                 "repo--feat-parallel-lane",
				Path:                 childPath,
				PresentOnDisk:        true,
				WorktreeRootPath:     rootPath,
				WorktreeKind:         model.WorktreeKindLinked,
				WorktreeParentBranch: "master",
				RepoBranch:           "feat/parallel-lane",
			},
		},
		visibility:   visibilityAllFolders,
		sortMode:     sortByAttention,
		commandMode:  true,
		commandInput: input,
		width:        100,
		height:       24,
	}
	m.rebuildProjectList(childPath)
	m.syncCommandSelection()

	rendered := ansi.Strip(m.renderCommandPaletteContent(72))
	if strings.Contains(rendered, "Worktrees: try") {
		t.Fatalf("command palette should not render a linked-worktree command legend, got %q", rendered)
	}
}

func TestCommandPaletteDoesNotShowWorktreeCommandHintForRepoRoot(t *testing.T) {
	input := textinput.New()
	input.SetValue("/")

	rootPath := "/tmp/repo"
	m := Model{
		allProjects: []model.ProjectSummary{
			{
				Name:             "repo",
				Path:             rootPath,
				PresentOnDisk:    true,
				WorktreeRootPath: rootPath,
				WorktreeKind:     model.WorktreeKindMain,
				RepoBranch:       "master",
			},
			{
				Name:                 "repo--feat-parallel-lane",
				Path:                 "/tmp/repo--feat-parallel-lane",
				PresentOnDisk:        true,
				WorktreeRootPath:     rootPath,
				WorktreeKind:         model.WorktreeKindLinked,
				WorktreeParentBranch: "master",
				RepoBranch:           "feat/parallel-lane",
			},
		},
		visibility:   visibilityAllFolders,
		sortMode:     sortByAttention,
		commandMode:  true,
		commandInput: input,
		width:        100,
		height:       24,
	}
	m.rebuildProjectList(rootPath)
	m.syncCommandSelection()

	rendered := ansi.Strip(m.renderCommandPaletteContent(72))
	if strings.Contains(rendered, "Worktrees: try") {
		t.Fatalf("command palette should not render a repo-root worktree command legend, got %q", rendered)
	}
}

func TestRenderDialogPanelRestoresBackgroundAfterStyledResets(t *testing.T) {
	prevProfile := lipgloss.ColorProfile()
	lipgloss.SetColorProfile(termenv.ANSI256)
	defer lipgloss.SetColorProfile(prevProfile)

	panel := renderDialogPanel(30, 26, detailSectionStyle.Render("TODO")+"  "+detailValueStyle.Render("demo"))
	if !strings.Contains(panel, dialogPanelFillReset) {
		t.Fatalf("dialog panel should reapply its background color after nested style resets: %q", panel)
	}
	if !strings.Contains(ansi.Strip(panel), "TODO  demo") {
		t.Fatalf("dialog panel should preserve the rendered content, got %q", ansi.Strip(panel))
	}
}

func TestTodoDialogLegendUsesDistinctActionTones(t *testing.T) {
	prevProfile := lipgloss.ColorProfile()
	lipgloss.SetColorProfile(termenv.ANSI256)
	defer lipgloss.SetColorProfile(prevProfile)

	rendered := todoDialogLegendLine()
	for _, bgCode := range []string{"48;5;42", "48;5;81", "48;5;214", "48;5;160"} {
		if !strings.Contains(rendered, bgCode) {
			t.Fatalf("todo dialog legend should include tone %s, got %q", bgCode, rendered)
		}
	}
	stripped := ansi.Strip(rendered)
	if !strings.Contains(stripped, "\n") {
		t.Fatalf("todo dialog legend should split Enter/Esc onto a second line, got %q", stripped)
	}
	lines := strings.Split(stripped, "\n")
	if len(lines) != 2 {
		t.Fatalf("todo dialog legend line count = %d, want 2; got %q", len(lines), stripped)
	}
	if (strings.Contains(lines[0], "Enter") && strings.Contains(lines[0], "start")) || (strings.Contains(lines[0], "Esc") && strings.Contains(lines[0], "close")) {
		t.Fatalf("todo dialog legend first line should keep Enter/Esc separate, got %q", lines[0])
	}
	if !strings.Contains(lines[1], "Enter") || !strings.Contains(lines[1], "start") || !strings.Contains(lines[1], "Esc") || !strings.Contains(lines[1], "close") {
		t.Fatalf("todo dialog legend second line should contain Enter/Esc, got %q", lines[1])
	}
}

func TestLocalBackendModelPickerLegendUsesDistinctActionTones(t *testing.T) {
	prevProfile := lipgloss.ColorProfile()
	lipgloss.SetColorProfile(termenv.ANSI256)
	defer lipgloss.SetColorProfile(prevProfile)

	m := Model{
		localModelPickerBackend: config.AIBackendMLX,
		setupSnapshot: aibackend.Snapshot{
			MLX: aibackend.Status{
				Backend:     config.AIBackendMLX,
				Label:       "MLX",
				Ready:       true,
				Endpoint:    "http://127.0.0.1:8080/v1",
				Models:      []string{"mlx-community/Qwen3.5-9B-MLX-4bit"},
				ActiveModel: "mlx-community/Qwen3.5-9B-MLX-4bit",
			},
		},
	}

	rendered := m.renderLocalBackendModelPickerContent(80, 16)
	for _, bgCode := range []string{"48;5;42", "48;5;81", "48;5;214", "48;5;160"} {
		if !strings.Contains(rendered, bgCode) {
			t.Fatalf("local backend model picker legend should include tone %s, got %q", bgCode, rendered)
		}
	}

	stripped := ansi.Strip(rendered)
	if !strings.Contains(stripped, "Up/Down") || !strings.Contains(stripped, "move") {
		t.Fatalf("local backend model picker legend should include navigation guidance, got %q", stripped)
	}
	if !strings.Contains(stripped, "Enter") || !strings.Contains(stripped, "choose") {
		t.Fatalf("local backend model picker legend should include choose guidance, got %q", stripped)
	}
	if !strings.Contains(stripped, "a") || !strings.Contains(stripped, "auto") {
		t.Fatalf("local backend model picker legend should include auto guidance, got %q", stripped)
	}
	if !strings.Contains(stripped, "Esc") || !strings.Contains(stripped, "close") {
		t.Fatalf("local backend model picker legend should include close guidance, got %q", stripped)
	}
}

func TestSettingsOllamaModelFieldOpensPicker(t *testing.T) {
	settings := config.EditableSettingsFromAppConfig(config.Default())
	settings.AIBackend = config.AIBackendOllama

	m := Model{
		settingsMode:            true,
		settingsFields:          newSettingsFields(settings),
		settingsBaseline:        &settings,
		settingsSelected:        settingsFieldOllamaModel,
		settingsSectionSelected: settingsSectionIndexByID(settingsSectionAI),
		setupSnapshot: aibackend.Snapshot{
			Selected: config.AIBackendOllama,
			Ollama: aibackend.Status{
				Backend:     config.AIBackendOllama,
				Label:       "Ollama",
				Ready:       true,
				Endpoint:    "http://127.0.0.1:11434/v1",
				Models:      []string{"qwen3:8b", "llama3.2:3b"},
				ActiveModel: "qwen3:8b",
			},
		},
		width:  100,
		height: 24,
	}

	actions := ansi.Strip(m.renderSettingsActions())
	if !strings.Contains(actions, "Enter") || !strings.Contains(actions, "choose") {
		t.Fatalf("Ollama model field should advertise Enter choose, got %q", actions)
	}

	updated, cmd := m.updateSettingsMode(tea.KeyMsg{Type: tea.KeyEnter})
	got := updated.(Model)
	if cmd != nil {
		t.Fatalf("opening Ollama picker should not queue a command")
	}
	if !got.localModelPickerVisible || got.localModelPickerBackend != config.AIBackendOllama {
		t.Fatalf("Ollama model field should open Ollama picker, visible=%v backend=%s", got.localModelPickerVisible, got.localModelPickerBackend)
	}

	updated, _ = got.Update(tea.KeyMsg{Type: tea.KeyDown})
	got = updated.(Model)
	updated, _ = got.Update(tea.KeyMsg{Type: tea.KeyEnter})
	got = updated.(Model)
	if got.localModelPickerVisible {
		t.Fatalf("local model picker should close after choosing a model")
	}
	if got.currentSettingsBaseline().OllamaModel != "qwen3:8b" {
		t.Fatalf("saved Ollama model = %q, want qwen3:8b", got.currentSettingsBaseline().OllamaModel)
	}
}

func TestViewWithSettingsModeRespectsHeight(t *testing.T) {
	m := Model{
		projects: []model.ProjectSummary{{
			Name:                             "demo",
			Path:                             "/tmp/demo",
			Status:                           model.StatusIdle,
			PresentOnDisk:                    true,
			LatestSessionClassification:      model.ClassificationCompleted,
			LatestSessionClassificationType:  model.SessionCategoryCompleted,
			LatestSessionSummary:             "Work appears complete for now.",
			LatestSessionFormat:              "modern",
			LatestSessionDetectedProjectPath: "/tmp/demo",
		}},
		selected:       0,
		settingsMode:   true,
		settingsFields: newSettingsFields(config.EditableSettingsFromAppConfig(config.Default())),
		width:          100,
		height:         24,
	}
	m.syncDetailViewport(false)
	_ = m.setSettingsSelection(0)

	rendered := ansi.Strip(m.View())
	if got := len(strings.Split(rendered, "\n")); got != m.height {
		t.Fatalf("View() line count = %d, want terminal height %d; render was %q", got, m.height, rendered)
	}
	if !strings.Contains(rendered, "Settings") {
		t.Fatalf("View() missing settings modal: %q", rendered)
	}
	if !strings.Contains(rendered, "Config:") {
		t.Fatalf("View() missing config path context: %q", rendered)
	}
	if !strings.Contains(rendered, "Esc") || !strings.Contains(rendered, "section") {
		t.Fatalf("View() should keep the settings section/back legend visible at 24 rows: %q", rendered)
	}
	if !strings.Contains(rendered, "│  [") || !strings.Contains(rendered, "│ Pa") {
		t.Fatalf("View() should preserve background list and detail context under the settings modal: %q", rendered)
	}
}

func TestSettingsModalRendersColoredActionLegend(t *testing.T) {
	settings := config.EditableSettingsFromAppConfig(config.Default())
	settings.OpenAIAPIKey = "sk-test-example"

	m := Model{
		settingsMode:     true,
		settingsFields:   newSettingsFields(settings),
		settingsBaseline: &settings,
		width:            100,
		height:           24,
	}
	_ = m.setSettingsSection(3)
	_ = m.setSettingsSelection(settingsFieldExcludePaths)

	rendered := ansi.Strip(m.renderSettingsContent(72, 18))
	if !strings.Contains(rendered, "ctrl+s") || !strings.Contains(rendered, "save") {
		t.Fatalf("settings modal should render ctrl+s save action: %q", rendered)
	}
	if strings.Contains(rendered, "Enter") {
		t.Fatalf("settings modal should not render Enter for plain text fields: %q", rendered)
	}
	if !strings.Contains(rendered, "Tab") || !strings.Contains(rendered, "next") {
		t.Fatalf("settings modal should render Tab next action: %q", rendered)
	}
	if !strings.Contains(rendered, "Esc") || !strings.Contains(rendered, "returns to section list") {
		t.Fatalf("settings modal should clearly render the section-list back action: %q", rendered)
	}
	if !strings.Contains(rendered, "Up/Down") || !strings.Contains(rendered, "move") {
		t.Fatalf("settings modal should render Up/Down move action: %q", rendered)
	}
	if !strings.Contains(rendered, "Esc") || !strings.Contains(rendered, "sections") {
		t.Fatalf("settings modal should render Esc sections action: %q", rendered)
	}
}

func TestProjectScopeKeepsPathFieldsPaired(t *testing.T) {
	var fields []int
	for _, section := range settingsSections() {
		if section.id == settingsSectionScope {
			fields = append([]int(nil), section.fieldOrder...)
			break
		}
	}
	if !slices.Contains(fields, settingsFieldIncludePaths) {
		t.Fatalf("Project Scope section missing include paths field: %#v", fields)
	}
	if !slices.Contains(fields, settingsFieldExcludePaths) {
		t.Fatalf("Project Scope section missing exclude paths field: %#v", fields)
	}

	for _, section := range settingsSections() {
		if section.id != settingsSectionGettingStarted {
			continue
		}
		if slices.Contains(section.fieldOrder, settingsFieldIncludePaths) || slices.Contains(section.fieldOrder, settingsFieldExcludePaths) {
			t.Fatalf("Getting Started should not include advanced project path fields: %#v", section.fieldOrder)
		}
	}
}

func TestMobileSettingsAppearInSetupAndDedicatedSection(t *testing.T) {
	var mobileFields []int
	var gettingStartedFields []int
	for _, section := range settingsSections() {
		switch section.id {
		case settingsSectionMobile:
			mobileFields = append([]int(nil), section.fieldOrder...)
		case settingsSectionGettingStarted:
			gettingStartedFields = append([]int(nil), section.fieldOrder...)
		}
	}
	for _, field := range []int{settingsFieldMobileEnabled, settingsFieldMobileInputEnabled, settingsFieldMobileAccessMode, settingsFieldMobilePort, settingsFieldMobileListenAddress} {
		if !slices.Contains(mobileFields, field) {
			t.Fatalf("Mobile section missing field %d: %#v", field, mobileFields)
		}
	}
	if !slices.Contains(gettingStartedFields, settingsFieldMobileEnabled) {
		t.Fatalf("Getting Started missing mobile entry: %#v", gettingStartedFields)
	}

	settings := config.EditableSettingsFromAppConfig(config.Default())
	m := Model{settingsFields: newSettingsFields(settings), settingsBaseline: &settings}
	if got := m.settingsDrilldownFieldOrder(settingsDrilldownMobile); !slices.Equal(got, []int{settingsFieldMobileEnabled, settingsFieldMobileInputEnabled, settingsFieldMobileAccessMode, settingsFieldMobilePort, settingsFieldMobileListenAddress}) {
		t.Fatalf("mobile drilldown fields = %#v", got)
	}
	if !m.settingsFieldVisible(settingsFieldMobileInputEnabled) || !m.settingsFieldVisible(settingsFieldMobileAccessMode) || !m.settingsFieldVisible(settingsFieldMobilePort) {
		t.Fatal("session messages, access mode, and port should be visible for the default local setup")
	}
	if m.settingsFieldVisible(settingsFieldMobileListenAddress) {
		t.Fatal("custom address should be hidden for the default local setup")
	}
	m.settingsFields[settingsFieldMobileAccessMode].input.SetValue(settingsMobileAccessCustom)
	if m.settingsFieldVisible(settingsFieldMobilePort) || !m.settingsFieldVisible(settingsFieldMobileListenAddress) {
		t.Fatal("custom mode should replace the simple port field with host:port")
	}
	m.settingsFields[settingsFieldMobileEnabled].input.SetValue("false")
	for _, field := range []int{settingsFieldMobileAccessMode, settingsFieldMobilePort, settingsFieldMobileListenAddress} {
		if m.settingsFieldVisible(field) {
			t.Fatalf("mobile detail field %d should be hidden while disabled", field)
		}
	}
}

func TestGettingStartedShowsMobileReachability(t *testing.T) {
	settings := config.EditableSettingsFromAppConfig(config.Default())
	m := Model{settingsFields: newSettingsFields(settings), settingsBaseline: &settings}
	steps := m.settingsGettingStartedSteps()
	if len(steps) != 4 {
		t.Fatalf("getting started steps = %d, want 4", len(steps))
	}
	mobile := steps[3]
	if mobile.Title != "Mobile access" || mobile.State != "phone setup needed" || mobile.Value != "This computer only" {
		t.Fatalf("default mobile step = %#v", mobile)
	}
	m.SetMobileServerStatus(MobileServerStatus{LANAddresses: []string{"192.168.1.20"}})
	m.settingsFields[settingsFieldMobileAccessMode].input.SetValue(settingsMobileAccessLAN)
	m.settingsFields[settingsFieldMobilePort].input.SetValue("8787")
	mobile = m.settingsGettingStartedSteps()[3]
	if mobile.State != "LAN ready" || mobile.Value != "http://192.168.1.20:8787" {
		t.Fatalf("LAN mobile step = %#v", mobile)
	}
}

func TestMobileAccessModesDeriveCompatibleListenAddresses(t *testing.T) {
	settings := config.EditableSettingsFromAppConfig(config.Default())
	m := Model{settingsFields: newSettingsFields(settings), settingsBaseline: &settings}

	if got := m.settingsMobileListenAddressFromFields(); got != "127.0.0.1:7777" {
		t.Fatalf("default listen address = %q", got)
	}
	m.settingsFields[settingsFieldMobileAccessMode].input.SetValue(settingsMobileAccessLAN)
	m.settingsFields[settingsFieldMobilePort].input.SetValue("8787")
	if got := m.settingsMobileListenAddressFromFields(); got != "0.0.0.0:8787" {
		t.Fatalf("LAN listen address = %q", got)
	}
	m.settingsFields[settingsFieldMobileAccessMode].input.SetValue(settingsMobileAccessCustom)
	m.settingsFields[settingsFieldMobileListenAddress].input.SetValue("192.168.1.20:9000")
	if got := m.settingsMobileListenAddressFromFields(); got != "192.168.1.20:9000" {
		t.Fatalf("custom listen address = %q", got)
	}
}

func TestMobileSetupShowsPhoneURLAndTechnicalBindNote(t *testing.T) {
	settings := config.EditableSettingsFromAppConfig(config.Default())
	m := Model{
		settingsFields:    newSettingsFields(settings),
		settingsBaseline:  &settings,
		settingsDrilldown: settingsDrilldownMobile,
	}
	m.SetMobileServerStatus(MobileServerStatus{LANAddresses: []string{"192.168.1.20"}})
	m.settingsFields[settingsFieldMobileAccessMode].input.SetValue(settingsMobileAccessLAN)

	rendered := ansi.Strip(strings.Join(m.renderSettingsDrilldownStatus(72), "\n"))
	for _, want := range []string{"Phone: http://192.168.1.20:7777", "Implicit bind: 0.0.0.0:7777"} {
		if !strings.Contains(rendered, want) {
			t.Fatalf("mobile setup status = %q, want %q", rendered, want)
		}
	}

	m.settingsSelected = settingsFieldMobilePort
	rendered = ansi.Strip(m.renderSettingsContent(72, 22))
	if !strings.Contains(rendered, "> Port") {
		t.Fatalf("compact mobile setup should keep selected Port visible: %q", rendered)
	}
}

func TestSettingsModalShowsEscCancel(t *testing.T) {
	m := Model{
		settingsMode:        true,
		settingsSectionMenu: true,
		settingsFields:      newSettingsFields(config.EditableSettingsFromAppConfig(config.Default())),
		width:               100,
		height:              24,
	}
	_ = m.setSettingsSelection(0)

	rendered := ansi.Strip(m.renderSettingsContent(72, 18))
	if !strings.Contains(rendered, "Esc") || !strings.Contains(rendered, "cancel") {
		t.Fatalf("settings modal should render Esc cancel action: %q", rendered)
	}
}

func TestInferenceStatusCardsShowProjectAndBossChatSelections(t *testing.T) {
	settings := config.EditableSettingsFromAppConfig(config.Default())
	settings.AIBackend = config.AIBackendOpenCode
	settings.BossChatBackend = config.AIBackendOpenAIAPI
	settings.OpenAIAPIKey = "sk-test-example"

	m := Model{
		settingsBaseline: &settings,
		setupSnapshot: aibackend.Snapshot{
			OpenCode: aibackend.Status{
				Backend: config.AIBackendOpenCode,
				Label:   config.AIBackendOpenCode.Label(),
				Ready:   true,
				Detail:  "OpenCode ready.",
			},
			OpenAIAPI: aibackend.Status{
				Backend: config.AIBackendOpenAIAPI,
				Label:   config.AIBackendOpenAIAPI.Label(),
				Ready:   true,
				Detail:  "Saved OpenAI API key ready.",
			},
		},
	}

	rendered := ansi.Strip(m.renderInferenceStatusCards(140))
	for _, want := range []string{
		"Project reports",
		"OpenCode",
		"Chat",
		"OpenAI API",
		"shared OpenAI API connection",
		"project reports stay",
		"separate",
	} {
		if !strings.Contains(rendered, want) {
			t.Fatalf("inference cards missing %q: %q", want, rendered)
		}
	}
}

func TestInferenceStatusCardsTreatMissingSnapshotAsUnchecked(t *testing.T) {
	settings := config.EditableSettingsFromAppConfig(config.Default())
	settings.AIBackend = config.AIBackendOpenCode
	settings.BossChatBackend = config.AIBackendDisabled

	m := Model{
		settingsBaseline: &settings,
	}

	rendered := ansi.Strip(m.renderInferenceStatusCards(140))
	if !strings.Contains(rendered, "UNCHECKED") {
		t.Fatalf("inference cards should show stale unknown availability as unchecked: %q", rendered)
	}
	if strings.Contains(rendered, "INSTALL") {
		t.Fatalf("inference cards should not invent an install warning from an empty snapshot: %q", rendered)
	}
	if !strings.Contains(rendered, "Availability will refresh in the background") {
		t.Fatalf("inference cards should explain how to refresh stale availability: %q", rendered)
	}
}

func TestProviderChoicesAreRoleSpecific(t *testing.T) {
	settings := config.EditableSettingsFromAppConfig(config.Default())
	settings.OpenAIAPIKey = "sk-test-example"

	m := Model{
		setupSnapshot: aibackend.Snapshot{
			Codex: aibackend.Status{
				Backend:       config.AIBackendCodex,
				Label:         config.AIBackendCodex.Label(),
				Installed:     true,
				Authenticated: true,
				Ready:         true,
				Detail:        "Codex ready.",
			},
		},
	}

	projectChoices := m.providerChoices(providerChoiceRoleProjectReports, settings)
	foundCodex := false
	for _, choice := range projectChoices {
		if choice.Value == config.AIBackendCodex {
			foundCodex = true
			break
		}
	}
	if !foundCodex {
		t.Fatalf("project report choices should include Codex")
	}
	if got := providerChoiceLabel(projectChoices, config.AIBackendDisabled, "missing"); got != "Disabled" {
		t.Fatalf("disabled project label = %q, want Disabled", got)
	}

	bossChoices := m.providerChoices(providerChoiceRoleBossChat, settings)
	foundBossDeepSeek := false
	for _, choice := range bossChoices {
		if choice.Value == config.AIBackendDeepSeek {
			foundBossDeepSeek = true
			break
		}
	}
	if !foundBossDeepSeek {
		t.Fatalf("Chat choices should include direct DeepSeek")
	}
	if got := providerChoiceLabel(bossChoices, config.AIBackendUnset, "missing"); got != "Auto" {
		t.Fatalf("auto boss label = %q, want Auto", got)
	}
	if index := providerChoiceSelection(bossChoices, config.AIBackendCodex); index != 0 {
		t.Fatalf("Chat choices should not include Codex; selection fallback = %d, want 0", index)
	}
	auto := bossChoices[providerChoiceSelection(bossChoices, config.AIBackendUnset)]
	if auto.State != "ready" || !strings.Contains(auto.Detail, "shared OpenAI API connection") {
		t.Fatalf("auto boss choice = state:%q detail:%q, want ready with shared connection detail", auto.State, auto.Detail)
	}
	if !strings.Contains(auto.NextStep, "Save") || !strings.Contains(auto.NextStep, "automatically") {
		t.Fatalf("auto boss next step = %q, want save guidance", auto.NextStep)
	}
}

func TestSettingsProviderPickersRenderSharedStatus(t *testing.T) {
	settings := config.EditableSettingsFromAppConfig(config.Default())
	settings.AIBackend = config.AIBackendCodex
	settings.BossChatBackend = config.AIBackendUnset
	settings.OpenAIAPIKey = "sk-test-example"

	m := Model{
		settingsMode:   true,
		settingsFields: newSettingsFields(settings),
		setupSnapshot: aibackend.Snapshot{
			Codex: aibackend.Status{
				Backend:       config.AIBackendCodex,
				Label:         config.AIBackendCodex.Label(),
				Installed:     true,
				Authenticated: true,
				Ready:         true,
				Detail:        "Codex ready.",
			},
		},
		width:  100,
		height: 24,
	}

	projectPicker := ansi.Strip(m.renderSettingsAIBackendPickerContent(72))
	for _, want := range []string{"Project Reports", "Codex", "ready", "Selected Helper", "Will do", "Needs", "Readiness", "After choosing"} {
		if !strings.Contains(projectPicker, want) {
			t.Fatalf("project picker missing %q: %q", want, projectPicker)
		}
	}

	bossPicker := ansi.Strip(m.renderSettingsBossChatBackendPickerContent(72))
	for _, want := range []string{"Chat", "Auto", "ready", "shared OpenAI API connection", "Selected Helper", "After choosing"} {
		if !strings.Contains(bossPicker, want) {
			t.Fatalf("boss picker missing %q: %q", want, bossPicker)
		}
	}
}

func TestSettingsGettingStartedRendersStepGuide(t *testing.T) {
	settings := config.EditableSettingsFromAppConfig(config.Default())
	settings.AIBackend = config.AIBackendOpenCode
	settings.BossChatBackend = config.AIBackendOpenAIAPI
	settings.OpenAIAPIKey = "sk-test-example"

	m := Model{
		settingsMode:     true,
		settingsFields:   newSettingsFields(settings),
		settingsBaseline: &settings,
		setupSnapshot: aibackend.Snapshot{
			OpenCode: aibackend.Status{
				Backend: config.AIBackendOpenCode,
				Label:   config.AIBackendOpenCode.Label(),
				Ready:   true,
				Detail:  "OpenCode ready.",
			},
		},
		width:  120,
		height: 30,
	}
	_ = m.setSettingsSelection(settingsFieldAIBackend)

	rendered := ansi.Strip(m.renderSettingsContent(100, 24))
	for _, want := range []string{"Setup Guide", "Project reports", "Chat", "LCAgent", "Next: press Enter"} {
		if !strings.Contains(rendered, want) {
			t.Fatalf("getting started guide missing %q: %q", want, rendered)
		}
	}
	if strings.Contains(rendered, "Project roots") {
		t.Fatalf("getting started guide should leave project roots in Project Scope: %q", rendered)
	}
	if strings.Contains(rendered, "OpenAI key") {
		t.Fatalf("getting started guide should not show OpenAI key as a required top-level step: %q", rendered)
	}
	for _, want := range []string{"About:", "Save to use this for project reports."} {
		if !strings.Contains(rendered, want) {
			t.Fatalf("getting started guide should show selected detail %q below the rows: %q", want, rendered)
		}
	}
	narrowRow := ansi.Strip(m.renderSettingsGettingStartedStep(m.settingsGettingStartedSteps()[0], 44))
	if !strings.Contains(narrowRow, "ready") || strings.Contains(narrowRow, "rea...") {
		t.Fatalf("getting started guide should preserve short status in narrow rows: %q", narrowRow)
	}
	if strings.Contains(rendered, "I will keep the setup small") {
		t.Fatalf("getting started guide should use steps instead of paragraph copy: %q", rendered)
	}
}

func TestSettingsGettingStartedNavigationSkipsProviderDetailFields(t *testing.T) {
	settings := config.EditableSettingsFromAppConfig(config.Default())
	settings.BossChatBackend = config.AIBackendOpenAIAPI
	settings.OpenAIAPIKey = "sk-test-example"

	m := Model{
		settingsMode:     true,
		settingsFields:   newSettingsFields(settings),
		settingsBaseline: &settings,
		width:            120,
		height:           30,
	}
	_ = m.setSettingsSelection(settingsFieldBossChatBackend)

	updated, cmd := m.updateSettingsMode(tea.KeyMsg{Type: tea.KeyDown})
	got := updated.(Model)
	if cmd == nil {
		t.Fatalf("moving down should refocus the next top-level setup row")
	}
	if got.settingsSelected != settingsFieldLCAgentRoutePreset {
		t.Fatalf("settingsSelected = %d, want LCAgent setup row, not OpenAI key", got.settingsSelected)
	}
}

func TestSettingsAISectionShowsCompactProviderConnections(t *testing.T) {
	settings := config.EditableSettingsFromAppConfig(config.Default())
	settings.AIBackend = config.AIBackendOpenCode
	settings.BossChatBackend = config.AIBackendOpenAIAPI
	settings.OpenAIAPIKey = "sk-test-example"

	m := Model{
		settingsMode:     true,
		settingsFields:   newSettingsFields(settings),
		settingsBaseline: &settings,
		width:            120,
		height:           30,
	}
	_ = m.setSettingsSection(1)
	if m.activeSettingsSection().id != settingsSectionAI {
		t.Fatalf("active settings section = %q, want providers and models", m.activeSettingsSection().id)
	}

	rendered := ansi.Strip(m.renderSettingsContent(100, 24))
	for _, want := range []string{"Providers & Models", "Provider Connections", "OpenAI API", "ready", "Chat", "Codex launch mode", "Show reasoning"} {
		if !strings.Contains(rendered, want) {
			t.Fatalf("providers and models section missing %q: %q", want, rendered)
		}
	}
	for _, hidden := range []string{"OpenAI API key", "Chat main model", "MLX base URL"} {
		if strings.Contains(rendered, hidden) {
			t.Fatalf("providers and models should stay compact and hide %q: %q", hidden, rendered)
		}
	}

	updated, cmd := m.updateSettingsMode(tea.KeyMsg{Type: tea.KeyTab})
	if cmd == nil {
		t.Fatalf("Tab should refocus the next compact setting")
	}
	got := updated.(Model)
	if got.activeSettingsSection().id != settingsSectionAI {
		t.Fatalf("compact settings should stay in Providers & Models section, got %q", got.activeSettingsSection().id)
	}
	if got.settingsSelected != settingsFieldHideReasoningSections {
		t.Fatalf("settingsSelected = %d, want show reasoning row", got.settingsSelected)
	}
}

func TestSettingsDrilldownShowsProviderDetailFieldsWhenRelevant(t *testing.T) {
	settings := config.EditableSettingsFromAppConfig(config.Default())
	m := Model{
		settingsMode:     true,
		settingsFields:   newSettingsFields(settings),
		settingsBaseline: &settings,
		width:            120,
		height:           30,
	}
	updated, _ := m.openSettingsDrilldown(settingsDrilldownProjectReports)
	m = updated.(Model)

	rendered := ansi.Strip(m.renderSettingsContent(100, 24))
	for _, hidden := range []string{"OpenAI API key", "Chat main model", "MLX base URL", "Ollama base URL"} {
		if strings.Contains(rendered, hidden) {
			t.Fatalf("default project-report setup should hide %q until relevant: %q", hidden, rendered)
		}
	}

	m.settingsFields[settingsFieldAIBackend].input.SetValue(string(config.AIBackendMLX))
	rendered = ansi.Strip(m.renderSettingsContent(100, 24))
	if !strings.Contains(rendered, "Shared MLX Connection") || !strings.Contains(rendered, "MLX base URL") || !strings.Contains(rendered, "MLX model") {
		t.Fatalf("MLX provider selection should reveal shared MLX details: %q", rendered)
	}
	if strings.Contains(rendered, "OpenAI API key") || strings.Contains(rendered, "Ollama base URL") {
		t.Fatalf("MLX provider selection should keep unrelated details hidden: %q", rendered)
	}
}

func TestSettingsProjectAndBossDrilldownsUseSharedOpenAIConnection(t *testing.T) {
	settings := config.EditableSettingsFromAppConfig(config.Default())
	settings.AIBackend = config.AIBackendOpenAIAPI
	settings.BossChatBackend = config.AIBackendOpenAIAPI
	settings.OpenAIAPIKey = "sk-shared-example"

	m := Model{
		settingsMode:     true,
		settingsFields:   newSettingsFields(settings),
		settingsBaseline: &settings,
		width:            120,
		height:           30,
	}

	updated, _ := m.openSettingsDrilldown(settingsDrilldownProjectReports)
	project := updated.(Model)
	projectFields := project.visibleSettingsDrilldownFieldOrder(settingsDrilldownProjectReports)
	if !slices.Contains(projectFields, settingsFieldOpenAIAPIKey) {
		t.Fatalf("project report drilldown should include the shared OpenAI connection field: %#v", projectFields)
	}
	projectRendered := ansi.Strip(project.renderSettingsContent(100, 24))
	for _, want := range []string{"Project Reports Setup", "Shared OpenAI Connection", "OpenAI API key"} {
		if !strings.Contains(projectRendered, want) {
			t.Fatalf("project report drilldown missing %q: %q", want, projectRendered)
		}
	}

	updated, _ = project.closeSettingsDrilldown("")
	back := updated.(Model)
	updated, _ = back.openSettingsDrilldown(settingsDrilldownBossChat)
	boss := updated.(Model)
	bossFields := boss.visibleSettingsDrilldownFieldOrder(settingsDrilldownBossChat)
	if !slices.Contains(bossFields, settingsFieldOpenAIAPIKey) {
		t.Fatalf("Chat drilldown should include the same shared OpenAI connection field: %#v", bossFields)
	}
	bossRendered := ansi.Strip(boss.renderSettingsContent(100, 24))
	for _, want := range []string{"Chat Setup", "Shared OpenAI Connection", "Chat Models", "Default: gpt-5.5", "Default: gpt-5.4-mini"} {
		if !strings.Contains(bossRendered, want) {
			t.Fatalf("Chat drilldown missing %q: %q", want, bossRendered)
		}
	}
}

func TestSettingsDeepSeekProjectAndBossModelsAreSeparate(t *testing.T) {
	settings := config.EditableSettingsFromAppConfig(config.Default())
	settings.AIBackend = config.AIBackendDeepSeek
	settings.BossChatBackend = config.AIBackendDeepSeek
	settings.LCAgentProvider = "deepseek"
	settings.DeepSeekAPIKey = "ds-shared-example"

	m := Model{
		settingsMode:     true,
		settingsFields:   newSettingsFields(settings),
		settingsBaseline: &settings,
		width:            120,
		height:           30,
	}

	overview := ansi.Strip(strings.Join(m.renderSettingsGettingStartedGuide(100), "\n"))
	for _, want := range []string{
		"Project reports",
		"DeepSeek / " + config.DefaultDeepSeekModel,
		"Chat",
		"DeepSeek / " + config.DefaultDeepSeekProModel,
		"LCAgent",
	} {
		if !strings.Contains(overview, want) {
			t.Fatalf("getting started overview missing %q: %q", want, overview)
		}
	}
	if got := m.settingsGettingStartedSteps()[2].Value; got != "DeepSeek / "+lcagentDefaultModelForProvider("deepseek") {
		t.Fatalf("LCAgent overview value = %q, want DeepSeek label with model", got)
	}

	updated, _ := m.openSettingsDrilldown(settingsDrilldownProjectReports)
	project := updated.(Model)
	projectFields := project.visibleSettingsDrilldownFieldOrder(settingsDrilldownProjectReports)
	if !slices.Contains(projectFields, settingsFieldDeepSeekModel) {
		t.Fatalf("project report drilldown should include DeepSeek project model field: %#v", projectFields)
	}
	projectRendered := ansi.Strip(project.renderSettingsContent(100, 24))
	for _, want := range []string{"DeepSeek project model", "Default: " + config.DefaultDeepSeekModel} {
		if !strings.Contains(projectRendered, want) {
			t.Fatalf("project report drilldown missing %q: %q", want, projectRendered)
		}
	}

	updated, _ = project.closeSettingsDrilldown("")
	back := updated.(Model)
	updated, _ = back.openSettingsDrilldown(settingsDrilldownBossChat)
	boss := updated.(Model)
	bossFields := boss.visibleSettingsDrilldownFieldOrder(settingsDrilldownBossChat)
	if slices.Contains(bossFields, settingsFieldDeepSeekModel) {
		t.Fatalf("Chat drilldown should not use the project DeepSeek model field: %#v", bossFields)
	}
	bossRendered := ansi.Strip(boss.renderSettingsContent(100, 24))
	for _, want := range []string{
		"Chat main model",
		"Default: " + config.DefaultDeepSeekProModel + " from DeepSeek",
		"Chat utility model",
		"Default: " + config.DefaultDeepSeekModel + " from DeepSeek",
	} {
		if !strings.Contains(bossRendered, want) {
			t.Fatalf("Chat drilldown missing %q: %q", want, bossRendered)
		}
	}
}

func TestSettingsXiaomiBossChatDrilldownShowsModelFields(t *testing.T) {
	settings := config.EditableSettingsFromAppConfig(config.Default())
	settings.BossChatBackend = config.AIBackendXiaomi
	settings.XiaomiAPIKey = "xm-shared-example"

	m := Model{
		settingsMode:     true,
		settingsFields:   newSettingsFields(settings),
		settingsBaseline: &settings,
		width:            120,
		height:           30,
	}

	updated, _ := m.openSettingsDrilldown(settingsDrilldownBossChat)
	got := updated.(Model)
	fields := got.visibleSettingsDrilldownFieldOrder(settingsDrilldownBossChat)
	for _, want := range []int{
		settingsFieldXiaomiBaseURL,
		settingsFieldXiaomiAPIKey,
		settingsFieldBossChatModel,
		settingsFieldBossUtilityModel,
	} {
		if !slices.Contains(fields, want) {
			t.Fatalf("Chat Xiaomi drilldown fields = %#v, missing %d", fields, want)
		}
	}
	if slices.Contains(fields, settingsFieldXiaomiModel) {
		t.Fatalf("Chat drilldown should use Chat model fields, not the project Xiaomi model field: %#v", fields)
	}

	rendered := ansi.Strip(got.renderSettingsContent(100, 24))
	for _, want := range []string{
		"Chat Setup",
		"Shared Xiaomi Connection",
		"Xiaomi base URL",
		"Xiaomi API key",
		"Chat Models",
		"Chat main model",
		"Default: " + config.DefaultXiaomiProModel + " from Xiaomi",
		"Chat utility model",
		"Default: " + config.DefaultXiaomiModel + " from Xiaomi",
	} {
		if !strings.Contains(rendered, want) {
			t.Fatalf("Chat Xiaomi drilldown missing %q: %q", want, rendered)
		}
	}

	_ = got.setSettingsSelection(settingsFieldBossChatModel)
	updated, cmd := got.updateSettingsMode(tea.KeyMsg{Type: tea.KeyEnter})
	got = updated.(Model)
	if cmd != nil {
		t.Fatalf("opening Boss Xiaomi model picker should not immediately load models")
	}
	if got.settingsLCAgentModelPicker == nil {
		t.Fatalf("Boss Xiaomi model row should open the unified model picker")
	}
	if got.settingsLCAgentModelPicker.Provider != "xiaomi" {
		t.Fatalf("model picker provider = %q, want xiaomi", got.settingsLCAgentModelPicker.Provider)
	}
}

func TestSettingsProviderPickerDrillsIntoNeededDetailField(t *testing.T) {
	settings := config.EditableSettingsFromAppConfig(config.Default())
	m := Model{
		settingsMode:     true,
		settingsFields:   newSettingsFields(settings),
		settingsBaseline: &settings,
		width:            120,
		height:           30,
	}
	_ = m.setSettingsSelection(settingsFieldAIBackend)

	updated, cmd := m.applySettingsAIBackendPickerSelection(providerChoice{Value: config.AIBackendOpenAIAPI, Label: "OpenAI API"})
	got := updated.(Model)
	if cmd == nil {
		t.Fatalf("selecting OpenAI API should focus the API key detail field")
	}
	if got.settingsSelected != settingsFieldOpenAIAPIKey {
		t.Fatalf("settingsSelected = %d, want OpenAI API key detail field", got.settingsSelected)
	}

	updated, cmd = got.applySettingsAIBackendPickerSelection(providerChoice{Value: config.AIBackendMLX, Label: "MLX"})
	got = updated.(Model)
	if cmd == nil {
		t.Fatalf("selecting MLX should focus the MLX detail field")
	}
	if got.settingsSelected != settingsFieldMLXBaseURL {
		t.Fatalf("settingsSelected = %d, want MLX base URL detail field", got.settingsSelected)
	}
}

func TestSettingsModalShowsSelectedHintAndWindowsLowerFields(t *testing.T) {
	m := Model{
		settingsMode:   true,
		settingsFields: newSettingsFields(config.EditableSettingsFromAppConfig(config.Default())),
		width:          100,
		height:         24,
	}
	_ = m.setSettingsSelection(settingsFieldHideReasoningSections)

	rendered := ansi.Strip(m.renderSettingsContent(72, 12))
	if !strings.Contains(rendered, "Show reasoning") {
		t.Fatalf("settings modal should keep the selected lower field visible: %q", rendered)
	}
	if strings.Contains(rendered, "OpenAI API key    ") {
		t.Fatalf("settings modal should window away upper fields in a short modal: %q", rendered)
	}
	if !strings.Contains(rendered, "Accepted values: true, false.") || !strings.Contains(rendered, "reasoning/thinking sections in the embedded transcript. Default: false.") {
		t.Fatalf("settings modal should show the selected field hint: %q", rendered)
	}
	if strings.Contains(rendered, "Used by OpenAI API backed features") {
		t.Fatalf("settings modal should not render every field hint inline anymore: %q", rendered)
	}
}

func TestSettingsSectionSwitchChangesVisibleFields(t *testing.T) {
	settings := config.EditableSettingsFromAppConfig(config.Default())
	settings.BossChatBackend = config.AIBackendOpenAIAPI

	m := Model{
		settingsMode:     true,
		settingsFields:   newSettingsFields(settings),
		settingsBaseline: &settings,
		width:            100,
		height:           24,
	}
	m.settingsSectionMenu = true
	m.settingsSectionSelected = settingsSectionIndexByID(settingsSectionAI)

	updated, cmd := m.updateSettingsMode(tea.KeyMsg{Type: tea.KeyDown})
	if cmd != nil {
		t.Fatalf("moving in the top-level settings menu should not queue work")
	}
	got := updated.(Model)
	if got.settingsSectionSelected != settingsSectionIndexByID(settingsSectionLCAgent) {
		t.Fatalf("settingsSectionSelected = %d, want LCAgent section", got.settingsSectionSelected)
	}

	rendered := ansi.Strip(got.renderSettingsContent(72, 18))
	if !strings.Contains(rendered, "Sections") || !strings.Contains(rendered, "> LCAgent") {
		t.Fatalf("settings modal should make the top-level section chooser obvious: %q", rendered)
	}
	if !strings.Contains(rendered, "Scout override") || !strings.Contains(rendered, "inherits Chat inference") {
		t.Fatalf("settings modal should explain LCAgent's optional Scout role: %q", rendered)
	}

	updated, cmd = got.updateSettingsMode(tea.KeyMsg{Type: tea.KeyEnter})
	if cmd == nil {
		t.Fatalf("opening a settings section should focus the first field")
	}
	got = updated.(Model)
	if got.settingsSectionMenu {
		t.Fatalf("opening a settings section should leave the top-level menu")
	}
	if got.settingsSelected != settingsFieldLCAgentRoutePreset {
		t.Fatalf("settingsSelected = %d, want LCAgent route preset field", got.settingsSelected)
	}
	rendered = ansi.Strip(got.renderSettingsContent(72, 18))
	if !strings.Contains(rendered, "LCAgent section.") {
		t.Fatalf("settings modal should render the new section hint: %q", rendered)
	}
	if !strings.Contains(rendered, "inherits Chat inference") || !strings.Contains(rendered, "available route, fallbacks, evidence, and trace") {
		t.Fatalf("LCAgent section should disclose automatic Scout routing and receipts: %q", rendered)
	}
	if !strings.Contains(rendered, "LCAgent route preset") || !strings.Contains(rendered, "Main model") {
		t.Fatalf("settings modal should show LCAgent fields after switching sections: %q", rendered)
	}
	if strings.Contains(rendered, "Main model provider") {
		t.Fatalf("settings modal should hide the standalone LCAgent provider field: %q", rendered)
	}
	if strings.Contains(rendered, "Chat main model") {
		t.Fatalf("settings modal should not keep rendering the old section fields: %q", rendered)
	}
}

func TestSettingsLeftRightStayWithFocusedInput(t *testing.T) {
	settings := config.EditableSettingsFromAppConfig(config.Default())
	settings.AIBackend = config.AIBackendMLX

	m := Model{
		settingsMode:     true,
		settingsFields:   newSettingsFields(settings),
		settingsBaseline: &settings,
		width:            100,
		height:           24,
	}
	_ = m.setSettingsSection(1)
	_ = m.setSettingsSelection(settingsFieldMLXModel)
	m.settingsFields[settingsFieldMLXModel].input.SetValue("abcdef")
	m.settingsFields[settingsFieldMLXModel].input.CursorEnd()

	beforeSection := m.activeSettingsSectionIndex()
	beforePos := m.settingsFields[settingsFieldMLXModel].input.Position()

	updated, _ := m.updateSettingsMode(tea.KeyMsg{Type: tea.KeyLeft})
	got := updated.(Model)
	if got.activeSettingsSectionIndex() != beforeSection {
		t.Fatalf("left arrow should not switch sections")
	}
	if got.settingsSelected != settingsFieldMLXModel {
		t.Fatalf("left arrow should keep the same focused field")
	}
	leftPos := got.settingsFields[settingsFieldMLXModel].input.Position()
	if leftPos != beforePos-1 {
		t.Fatalf("left arrow cursor position = %d, want %d", leftPos, beforePos-1)
	}

	updated, _ = got.updateSettingsMode(tea.KeyMsg{Type: tea.KeyRight})
	got = updated.(Model)
	if got.activeSettingsSectionIndex() != beforeSection {
		t.Fatalf("right arrow should not switch sections")
	}
	rightPos := got.settingsFields[settingsFieldMLXModel].input.Position()
	if rightPos != beforePos {
		t.Fatalf("right arrow cursor position = %d, want %d", rightPos, beforePos)
	}
}
