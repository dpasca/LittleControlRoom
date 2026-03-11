package config

import (
	"os"
	"path/filepath"
	"testing"
)

func TestParseScreenshotConfig(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	configPath := filepath.Join(dir, "screenshots.local.toml")
	if err := os.WriteFile(configPath, []byte(`
demo_data = true
terminal_width = 112
terminal_height = 31
output_dir = "docs/screenshots"
browser_path = "/Applications/Google Chrome.app/Contents/MacOS/Google Chrome"

project_filters = [
  "LCR",
  "assistant-lab",
  "LCR",
]

selected_project = "LCR"
live_codex_project = "assistant-lab"
`), 0o644); err != nil {
		t.Fatalf("write config: %v", err)
	}

	cfg, err := ParseScreenshotConfig(configPath)
	if err != nil {
		t.Fatalf("ParseScreenshotConfig() error = %v", err)
	}

	if cfg.TerminalWidth != 112 || cfg.TerminalHeight != 31 {
		t.Fatalf("terminal size = %dx%d, want 112x31", cfg.TerminalWidth, cfg.TerminalHeight)
	}
	if !cfg.DemoData {
		t.Fatalf("DemoData = false, want true")
	}
	if cfg.OutputDir != filepath.Join(dir, "docs", "screenshots") {
		t.Fatalf("OutputDir = %q", cfg.OutputDir)
	}
	if cfg.BrowserPath != "/Applications/Google Chrome.app/Contents/MacOS/Google Chrome" {
		t.Fatalf("BrowserPath = %q", cfg.BrowserPath)
	}
	if len(cfg.ProjectFilters) != 2 || cfg.ProjectFilters[0] != "LCR" || cfg.ProjectFilters[1] != "assistant-lab" {
		t.Fatalf("ProjectFilters = %#v", cfg.ProjectFilters)
	}
	if cfg.SelectedProject != "LCR" {
		t.Fatalf("SelectedProject = %q", cfg.SelectedProject)
	}
	if cfg.LiveCodexProject != "assistant-lab" {
		t.Fatalf("LiveCodexProject = %q", cfg.LiveCodexProject)
	}
}

func TestParseScreenshotConfigDefaults(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	configPath := filepath.Join(dir, "screenshots.local.toml")
	if err := os.WriteFile(configPath, []byte(`
project_filters = ["LittleControlRoom"]
`), 0o644); err != nil {
		t.Fatalf("write config: %v", err)
	}

	cfg, err := ParseScreenshotConfig(configPath)
	if err != nil {
		t.Fatalf("ParseScreenshotConfig() error = %v", err)
	}

	if cfg.TerminalWidth != 112 || cfg.TerminalHeight != 31 {
		t.Fatalf("defaults = %dx%d, want 112x31", cfg.TerminalWidth, cfg.TerminalHeight)
	}
	if cfg.DemoData {
		t.Fatalf("DemoData = true, want false")
	}
	if cfg.OutputDir != filepath.Join(dir, "docs", "screenshots") {
		t.Fatalf("OutputDir = %q", cfg.OutputDir)
	}
}
