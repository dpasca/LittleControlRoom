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
