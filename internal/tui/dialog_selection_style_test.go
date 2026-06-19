package tui

import (
	"strings"
	"testing"

	"lcroom/internal/codexapp"

	"github.com/charmbracelet/lipgloss"
	"github.com/muesli/termenv"
)

func TestCodexModelPickerRowsUseDialogSelectionContrast(t *testing.T) {
	withANSI256DarkBackground(t)
	row := (Model{}).renderCodexModelPickerRow(codexapp.ModelOption{
		Model:       "gpt-5.5",
		DisplayName: "GPT 5.5",
	}, true, 40, true)

	requireDialogSelectedContrast(t, row)
}

func TestSettingsLCAgentModelPickerRowsUseDialogSelectionContrast(t *testing.T) {
	withANSI256DarkBackground(t)
	option := codexapp.ModelOption{
		Model:         "openai/gpt-5.5",
		ModelProvider: "openrouter",
		DisplayName:   "GPT 5.5",
	}
	state := &settingsLCAgentModelPickerState{
		FieldIndex:      settingsFieldLCAgentModel,
		Provider:        "openrouter",
		FilteredModels:  []codexapp.ModelOption{option},
		Rows:            buildSettingsLCAgentPickerRows([]codexapp.ModelOption{option}, "openrouter"),
		Selected:        2,
		CurrentProvider: "openrouter",
	}

	row := (Model{}).renderSettingsLCAgentModelPickerRow(2, state, true, 48)

	requireDialogSelectedContrast(t, row)
}

func TestLocalBackendModelPickerRowsUseDialogSelectionContrast(t *testing.T) {
	withANSI256DarkBackground(t)
	row := (Model{}).renderLocalBackendModelPickerRow(1, []string{"mlx-community/Qwen3.5-9B-MLX-4bit"}, true, 48)

	requireDialogSelectedContrast(t, row)
}

func withANSI256DarkBackground(t *testing.T) {
	t.Helper()
	prevProfile := lipgloss.ColorProfile()
	prevDarkBackground := lipgloss.HasDarkBackground()
	lipgloss.SetColorProfile(termenv.ANSI256)
	lipgloss.SetHasDarkBackground(true)
	t.Cleanup(func() {
		lipgloss.SetColorProfile(prevProfile)
		lipgloss.SetHasDarkBackground(prevDarkBackground)
	})
}

func requireDialogSelectedContrast(t *testing.T, rendered string) {
	t.Helper()
	for _, want := range []string{"38;5;230", "48;5;24"} {
		if !strings.Contains(rendered, want) {
			t.Fatalf("selected row missing ANSI %q: %q", want, rendered)
		}
	}
	if strings.Contains(rendered, "48;5;236") {
		t.Fatalf("selected row uses project-list gray background: %q", rendered)
	}
}
