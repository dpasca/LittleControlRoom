package tui

import (
	"strings"
	"testing"

	"lcroom/internal/codexapp"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/x/ansi"
)

func TestCodexFilterModelsMatchesFuzzyProviderAndModelTokens(t *testing.T) {
	models := []codexapp.ModelOption{
		{
			Model:         "gpt-5.5-nano",
			ModelProvider: "openai",
			DisplayName:   "GPT-5.5 Nano",
		},
		{
			Model:         "mimo-v2.5-pro",
			ModelProvider: "xiaomi",
			DisplayName:   "MiMo V2.5 Pro",
		},
	}

	got := codexFilterModels(models, "nano openai")
	if len(got) != 1 || got[0].Model != "gpt-5.5-nano" {
		t.Fatalf("codexFilterModels(nano openai) = %#v, want OpenAI nano model", got)
	}
}

func TestCodexModelPickerPageKeysWorkFromFilterFocus(t *testing.T) {
	models := make([]codexapp.ModelOption, 0, 8)
	for i := 0; i < 8; i++ {
		models = append(models, codexapp.ModelOption{
			Model:       "model-" + string(rune('a'+i)),
			DisplayName: "Model " + string(rune('A'+i)),
		})
	}
	m := Model{
		codexModelPicker: &codexModelPickerState{
			Models:         append([]codexapp.ModelOption(nil), models...),
			FilteredModels: append([]codexapp.ModelOption(nil), models...),
			ModelIndex:     0,
			Focus:          codexModelPickerFocusFilter,
		},
	}

	updated, _ := m.updateCodexModelPickerMode(tea.KeyMsg{Type: tea.KeyPgDown})
	got := updated.(Model)
	if got.codexModelPicker.Focus != codexModelPickerFocusModels {
		t.Fatalf("focus after pgdown = %q, want models", got.codexModelPicker.Focus)
	}
	if got.codexModelPicker.ModelIndex != 5 {
		t.Fatalf("model index after pgdown = %d, want 5", got.codexModelPicker.ModelIndex)
	}

	updated, _ = got.updateCodexModelPickerMode(tea.KeyMsg{Type: tea.KeyPgUp})
	got = updated.(Model)
	if got.codexModelPicker.ModelIndex != 0 {
		t.Fatalf("model index after pgup = %d, want 0", got.codexModelPicker.ModelIndex)
	}
}

func TestRenderCodexModelPickerShowsSelectedModelStatus(t *testing.T) {
	models := []codexapp.ModelOption{{
		Model:                  "gpt-5.5-nano",
		ModelProvider:          "openai",
		DisplayName:            "GPT-5.5 Nano",
		DefaultReasoningEffort: "low",
		Description:            "Small fast OpenAI coding model.",
	}}
	m := Model{
		codexModelPicker: &codexModelPickerState{
			Models:         append([]codexapp.ModelOption(nil), models...),
			FilteredModels: append([]codexapp.ModelOption(nil), models...),
			SelectedModel:  "gpt-5.5-nano",
			ModelIndex:     0,
			Focus:          codexModelPickerFocusModels,
		},
	}

	rendered := ansi.Strip(m.renderCodexModelPickerContent(80, 24))
	for _, want := range []string{"Selected", "OpenAI", "gpt-5.5-nano"} {
		if !strings.Contains(rendered, want) {
			t.Fatalf("rendered picker missing %q: %q", want, rendered)
		}
	}
}

func TestCodexModelPickerLCAgentDefaultOnlyReasoningUsesProviderOptions(t *testing.T) {
	models := []codexapp.ModelOption{{
		Model:                  "gpt-5.5",
		ModelProvider:          "openai",
		DisplayName:            "GPT 5.5",
		DefaultReasoningEffort: "low",
	}}
	m := Model{
		codexVisibleProject: "/tmp/demo",
		codexSnapshots: map[string]codexapp.Snapshot{
			"/tmp/demo": {
				Provider:      codexapp.ProviderLCAgent,
				ProjectPath:   "/tmp/demo",
				Model:         "gpt-5.5",
				ModelProvider: "openai",
			},
		},
		codexModelPicker: &codexModelPickerState{
			Models:         append([]codexapp.ModelOption(nil), models...),
			FilteredModels: append([]codexapp.ModelOption(nil), models...),
			SelectedModel:  "gpt-5.5",
			ModelIndex:     0,
			Focus:          codexModelPickerFocusEfforts,
		},
	}
	m.setCodexModelPickerModel(models[0], "")

	options := m.currentCodexReasoningOptions()
	if len(options) != 4 {
		t.Fatalf("reasoning options = %#v, want low/medium/high/xhigh", options)
	}
	got := m
	for range 3 {
		updated, _ := got.updateCodexModelPickerMode(tea.KeyMsg{Type: tea.KeyDown})
		got = updated.(Model)
	}
	selected, ok := got.currentCodexReasoningOption()
	if !ok || selected.ReasoningEffort != "xhigh" {
		t.Fatalf("selected reasoning = %#v ok=%v, want xhigh", selected, ok)
	}
}

func TestClaudePrelaunchModelOptionsFoldCLIDefaultIntoProviderDefault(t *testing.T) {
	options := claudePrelaunchModelOptions([]codexapp.ModelOption{
		{ID: "default", Model: "default", ResolvedModel: "claude-opus-5-5[1m]", DisplayName: "Opus 5.5 (1M)", Description: "Opus 5.5 with 1M context", IsDefault: true},
		{ID: "sonnet", Model: "sonnet", ResolvedModel: "claude-sonnet-5", DisplayName: "Sonnet 5"},
	})
	if len(options) != 2 {
		t.Fatalf("options = %#v, want provider default plus sonnet", options)
	}
	if options[0].Model != "" || options[0].DisplayName != "Claude Code default · Opus 5.5 (1M)" {
		t.Fatalf("provider default = %#v, want it to name the model Claude Code resolves", options[0])
	}
	if options[1].Model != "sonnet" {
		t.Fatalf("second option = %#v, want sonnet", options[1])
	}
}

func TestCodexModelOptionIndexMatchesResolvedClaudeModel(t *testing.T) {
	models := []codexapp.ModelOption{
		{Model: "sonnet", ResolvedModel: "claude-sonnet-5"},
		{Model: "opus[1m]", ResolvedModel: "claude-opus-5-5[1m]"},
	}
	if got := codexModelOptionIndex(models, "claude-opus-5-5[1m]"); got != 1 {
		t.Fatalf("index for running concrete model = %d, want its alias row", got)
	}
	if got := codexModelOptionIndex(models, "sonnet"); got != 0 {
		t.Fatalf("index for alias = %d, want exact alias row", got)
	}
}

func TestClaudeModelPickerRowShowsVersionedNameAndAlias(t *testing.T) {
	m := Model{codexModelPicker: &codexModelPickerState{Provider: codexapp.ProviderClaudeCode}}
	row := ansi.Strip(m.renderCodexModelPickerRow(codexapp.ModelOption{Model: "default", DisplayName: "Opus 5.5 (1M)", IsDefault: true}, false, 60, false))
	if strings.TrimSpace(row) != "Opus 5.5 (1M)  default" {
		t.Fatalf("default row = %q, want one default marker", row)
	}
}
