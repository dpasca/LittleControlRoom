package config

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	toml "github.com/pelletier/go-toml/v2"
)

type ScreenshotConfig struct {
	Path               string
	DemoData           bool
	TerminalWidth      int
	TerminalHeight     int
	CaptureScale       float64
	OutputDir          string
	BrowserPath        string
	ProjectFilters     []string
	SelectedProject    string
	LiveCodexProject   string
	LiveRuntimeProject string
}

type screenshotFileConfig struct {
	DemoData           bool     `toml:"demo_data"`
	TerminalWidth      int      `toml:"terminal_width"`
	TerminalHeight     int      `toml:"terminal_height"`
	CaptureScale       float64  `toml:"capture_scale"`
	OutputDir          string   `toml:"output_dir"`
	BrowserPath        string   `toml:"browser_path"`
	ProjectFilters     []string `toml:"project_filters"`
	SelectedProject    string   `toml:"selected_project"`
	LiveCodexProject   string   `toml:"live_codex_project"`
	LiveRuntimeProject string   `toml:"live_runtime_project"`
}

func DefaultScreenshotConfig() ScreenshotConfig {
	return ScreenshotConfig{
		TerminalWidth:  112,
		TerminalHeight: 31,
		CaptureScale:   1.5,
		OutputDir:      "docs/screenshots",
	}
}

func ParseScreenshotConfig(path string) (ScreenshotConfig, error) {
	cfg := DefaultScreenshotConfig()
	if strings.TrimSpace(path) == "" {
		path = "screenshots.local.toml"
	}

	expandedPath, err := expandHome(path)
	if err != nil {
		return ScreenshotConfig{}, err
	}
	cfg.Path = expandedPath

	raw, err := os.ReadFile(expandedPath)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return ScreenshotConfig{}, fmt.Errorf("read screenshot config %s: %w", expandedPath, err)
		}
		return ScreenshotConfig{}, fmt.Errorf("read screenshot config %s: %w", expandedPath, err)
	}

	var fc screenshotFileConfig
	if err := toml.Unmarshal(raw, &fc); err != nil {
		return ScreenshotConfig{}, fmt.Errorf("parse screenshot config %s: %w", expandedPath, err)
	}

	if fc.TerminalWidth > 0 {
		cfg.TerminalWidth = fc.TerminalWidth
	}
	if fc.TerminalHeight > 0 {
		cfg.TerminalHeight = fc.TerminalHeight
	}
	if fc.CaptureScale > 0 {
		cfg.CaptureScale = fc.CaptureScale
	}
	if strings.TrimSpace(fc.OutputDir) != "" {
		cfg.OutputDir = strings.TrimSpace(fc.OutputDir)
	}
	cfg.DemoData = fc.DemoData
	cfg.BrowserPath = normalizeScreenshotBrowserPath(fc.BrowserPath)
	cfg.ProjectFilters = normalizeScreenshotFilters(fc.ProjectFilters)
	cfg.SelectedProject = strings.TrimSpace(fc.SelectedProject)
	cfg.LiveCodexProject = strings.TrimSpace(fc.LiveCodexProject)
	cfg.LiveRuntimeProject = strings.TrimSpace(fc.LiveRuntimeProject)

	if !filepath.IsAbs(cfg.OutputDir) {
		cfg.OutputDir = filepath.Join(filepath.Dir(expandedPath), cfg.OutputDir)
	}
	cfg.OutputDir = filepath.Clean(cfg.OutputDir)

	if err := validateScreenshotConfig(cfg); err != nil {
		return ScreenshotConfig{}, err
	}
	return cfg, nil
}

func normalizeScreenshotFilters(filters []string) []string {
	if len(filters) == 0 {
		return nil
	}
	out := make([]string, 0, len(filters))
	seen := make(map[string]struct{}, len(filters))
	for _, filter := range filters {
		filter = strings.TrimSpace(filter)
		if filter == "" {
			continue
		}
		key := strings.ToLower(filter)
		if _, ok := seen[key]; ok {
			continue
		}
		seen[key] = struct{}{}
		out = append(out, filter)
	}
	return out
}

func normalizeScreenshotBrowserPath(value string) string {
	value = strings.TrimSpace(value)
	if value == "" {
		return ""
	}
	if strings.HasPrefix(value, "~") {
		expanded, err := expandHome(value)
		if err == nil {
			return expanded
		}
	}
	return value
}

func validateScreenshotConfig(cfg ScreenshotConfig) error {
	if strings.TrimSpace(cfg.Path) == "" {
		return fmt.Errorf("screenshot config path is required")
	}
	if cfg.TerminalWidth < 40 {
		return fmt.Errorf("terminal_width must be at least 40")
	}
	if cfg.TerminalHeight < 12 {
		return fmt.Errorf("terminal_height must be at least 12")
	}
	if cfg.CaptureScale < 1 {
		return fmt.Errorf("capture_scale must be at least 1")
	}
	if strings.TrimSpace(cfg.OutputDir) == "" {
		return fmt.Errorf("output_dir is required")
	}
	return nil
}
