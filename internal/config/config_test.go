package config

import (
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	"lcroom/internal/brand"
	"lcroom/internal/codexcli"
)

func useTempHome(t *testing.T) string {
	t.Helper()

	home := t.TempDir()
	t.Setenv("HOME", home)
	return home
}

func TestParseLoadsIncludePathsFromConfigFile(t *testing.T) {
	useTempHome(t)
	dir := t.TempDir()
	configPath := filepath.Join(dir, "config.toml")
	content := "include_paths = [\"/tmp/a\", \"/tmp/b\"]\n"
	if err := os.WriteFile(configPath, []byte(content), 0o644); err != nil {
		t.Fatalf("write config file: %v", err)
	}

	cfg, err := Parse("scan", []string{"--config", configPath})
	if err != nil {
		t.Fatalf("parse config: %v", err)
	}

	wantIncludePaths := []string{"/tmp/a", "/tmp/b"}
	if !reflect.DeepEqual(cfg.IncludePaths, wantIncludePaths) {
		t.Fatalf("include paths = %v, want %v", cfg.IncludePaths, wantIncludePaths)
	}
	if !cfg.ConfigLoaded {
		t.Fatalf("expected config to be loaded")
	}
	if cfg.ConfigPath != configPath {
		t.Fatalf("config path = %s, want %s", cfg.ConfigPath, configPath)
	}
}

func TestParseLoadsEditableSettingsFromConfigFile(t *testing.T) {
	useTempHome(t)
	dir := t.TempDir()
	configPath := filepath.Join(dir, "config.toml")
	content := "" +
		"include_paths = [\"/tmp/a\", \"/tmp/b\"]\n" +
		"exclude_paths = [\"/tmp/skip\"]\n" +
		"exclude_project_patterns = [\"quickgame_*\", \"secret-demo\"]\n" +
		"codex_launch_preset = \"safe\"\n" +
		"interval = \"45s\"\n" +
		"active-threshold = \"15m\"\n" +
		"stuck-threshold = \"3h\"\n"
	if err := os.WriteFile(configPath, []byte(content), 0o644); err != nil {
		t.Fatalf("write config file: %v", err)
	}

	cfg, err := Parse("scan", []string{"--config", configPath})
	if err != nil {
		t.Fatalf("parse config: %v", err)
	}

	if got, want := cfg.ScanInterval, 45*time.Second; got != want {
		t.Fatalf("interval = %s, want %s", got, want)
	}
	if got, want := cfg.ExcludePaths, []string{"/tmp/skip"}; !reflect.DeepEqual(got, want) {
		t.Fatalf("exclude paths = %v, want %v", got, want)
	}
	if got, want := cfg.ExcludeProjectPatterns, []string{"quickgame_*", "secret-demo"}; !reflect.DeepEqual(got, want) {
		t.Fatalf("exclude project patterns = %v, want %v", got, want)
	}
	if got, want := cfg.CodexLaunchPreset, codexcli.PresetSafe; got != want {
		t.Fatalf("codex launch preset = %s, want %s", got, want)
	}
	if got, want := cfg.ActiveThreshold, 15*time.Minute; got != want {
		t.Fatalf("active-threshold = %s, want %s", got, want)
	}
	if got, want := cfg.StuckThreshold, 3*time.Hour; got != want {
		t.Fatalf("stuck-threshold = %s, want %s", got, want)
	}
}

func TestParseIncludePathsFlagOverridesConfigFile(t *testing.T) {
	useTempHome(t)
	dir := t.TempDir()
	configPath := filepath.Join(dir, "config.toml")
	content := "include_paths = [\"/tmp/a\", \"/tmp/b\"]\n"
	if err := os.WriteFile(configPath, []byte(content), 0o644); err != nil {
		t.Fatalf("write config file: %v", err)
	}

	cfg, err := Parse("scan", []string{"--config", configPath, "--include-paths", "/tmp/override"})
	if err != nil {
		t.Fatalf("parse config with override: %v", err)
	}

	wantIncludePaths := []string{"/tmp/override"}
	if !reflect.DeepEqual(cfg.IncludePaths, wantIncludePaths) {
		t.Fatalf("include paths = %v, want %v", cfg.IncludePaths, wantIncludePaths)
	}
}

func TestParseExcludeProjectPatternsFlagOverridesConfigFile(t *testing.T) {
	useTempHome(t)
	dir := t.TempDir()
	configPath := filepath.Join(dir, "config.toml")
	content := "exclude_project_patterns = [\"quickgame_*\"]\n"
	if err := os.WriteFile(configPath, []byte(content), 0o644); err != nil {
		t.Fatalf("write config file: %v", err)
	}

	cfg, err := Parse("scan", []string{"--config", configPath, "--exclude-project-patterns", "secret-*,demo-*"})
	if err != nil {
		t.Fatalf("parse config with override: %v", err)
	}

	wantPatterns := []string{"secret-*", "demo-*"}
	if !reflect.DeepEqual(cfg.ExcludeProjectPatterns, wantPatterns) {
		t.Fatalf("exclude project patterns = %v, want %v", cfg.ExcludeProjectPatterns, wantPatterns)
	}
}

func TestParseCodexLaunchPresetFlagOverridesConfigFile(t *testing.T) {
	useTempHome(t)
	dir := t.TempDir()
	configPath := filepath.Join(dir, "config.toml")
	content := "codex_launch_preset = \"safe\"\n"
	if err := os.WriteFile(configPath, []byte(content), 0o644); err != nil {
		t.Fatalf("write config file: %v", err)
	}

	cfg, err := Parse("scan", []string{"--config", configPath, "--codex-launch-preset", "full-auto"})
	if err != nil {
		t.Fatalf("parse config with override: %v", err)
	}

	if got, want := cfg.CodexLaunchPreset, codexcli.PresetFullAuto; got != want {
		t.Fatalf("codex launch preset = %s, want %s", got, want)
	}
}

func TestParseAllowsEmptyIncludePathsFromConfigFile(t *testing.T) {
	useTempHome(t)
	dir := t.TempDir()
	configPath := filepath.Join(dir, "config.toml")
	content := "include_paths = []\nexclude_paths = [\"/tmp/skip\"]\n"
	if err := os.WriteFile(configPath, []byte(content), 0o644); err != nil {
		t.Fatalf("write config file: %v", err)
	}

	cfg, err := Parse("scan", []string{"--config", configPath})
	if err != nil {
		t.Fatalf("parse config: %v", err)
	}

	if len(cfg.IncludePaths) != 0 {
		t.Fatalf("expected include paths to be cleared, got %v", cfg.IncludePaths)
	}
	if got, want := cfg.ExcludePaths, []string{"/tmp/skip"}; !reflect.DeepEqual(got, want) {
		t.Fatalf("exclude paths = %v, want %v", got, want)
	}
}

func TestParseFailsWhenConfigFlagMissingValue(t *testing.T) {
	useTempHome(t)
	_, err := Parse("scan", []string{"--config"})
	if err == nil {
		t.Fatalf("expected parse error")
	}
	if !strings.Contains(err.Error(), "--config requires a value") {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestParseRejectsJSONConfigFile(t *testing.T) {
	useTempHome(t)
	dir := t.TempDir()
	configPath := filepath.Join(dir, "config.json")
	content := `{"include_paths":["/tmp/a","/tmp/b"]}`
	if err := os.WriteFile(configPath, []byte(content), 0o644); err != nil {
		t.Fatalf("write config file: %v", err)
	}

	if _, err := Parse("scan", []string{"--config", configPath}); err == nil {
		t.Fatalf("expected JSON config to be rejected")
	}
}

func TestParseDoctorScanFlag(t *testing.T) {
	useTempHome(t)
	cfg, err := Parse("doctor", []string{"--scan"})
	if err != nil {
		t.Fatalf("parse doctor config: %v", err)
	}
	if !cfg.DoctorScan {
		t.Fatalf("expected doctor scan flag to be enabled")
	}
}

func TestParseSnapshotFlags(t *testing.T) {
	useTempHome(t)

	cfg, err := Parse("snapshot", []string{
		"--limit", "5",
		"--project", "/tmp/demo",
		"--session-id", "ses_demo",
	})
	if err != nil {
		t.Fatalf("parse snapshot config: %v", err)
	}
	if cfg.SnapshotLimit != 5 {
		t.Fatalf("snapshot limit = %d, want 5", cfg.SnapshotLimit)
	}
	if cfg.SnapshotProject != "/tmp/demo" {
		t.Fatalf("snapshot project = %q, want /tmp/demo", cfg.SnapshotProject)
	}
	if cfg.SnapshotSessionID != "ses_demo" {
		t.Fatalf("snapshot session id = %q, want ses_demo", cfg.SnapshotSessionID)
	}
}

func TestParseRejectsInvalidSnapshotLimit(t *testing.T) {
	useTempHome(t)

	if _, err := Parse("snapshot", []string{"--limit", "0"}); err == nil {
		t.Fatalf("expected snapshot limit validation error")
	}
}

func TestParseEditableSettings(t *testing.T) {
	useTempHome(t)

	settings, err := ParseEditableSettings("~/dev/repos,/tmp/other", "/tmp/skip", "quickgame_*,secret-demo", "yolo", "10m", "2h", "45s")
	if err != nil {
		t.Fatalf("ParseEditableSettings() error = %v", err)
	}

	home, err := os.UserHomeDir()
	if err != nil {
		t.Fatalf("UserHomeDir() error = %v", err)
	}
	wantIncludePaths := []string{filepath.Join(home, "dev", "repos"), "/tmp/other"}
	if !reflect.DeepEqual(settings.IncludePaths, wantIncludePaths) {
		t.Fatalf("include paths = %v, want %v", settings.IncludePaths, wantIncludePaths)
	}
	if got, want := settings.ExcludePaths, []string{"/tmp/skip"}; !reflect.DeepEqual(got, want) {
		t.Fatalf("exclude paths = %v, want %v", got, want)
	}
	if got, want := settings.ExcludeProjectPatterns, []string{"quickgame_*", "secret-demo"}; !reflect.DeepEqual(got, want) {
		t.Fatalf("exclude project patterns = %v, want %v", got, want)
	}
	if got, want := settings.CodexLaunchPreset, codexcli.PresetYolo; got != want {
		t.Fatalf("codex launch preset = %s, want %s", got, want)
	}
	if got, want := settings.ActiveThreshold, 10*time.Minute; got != want {
		t.Fatalf("active-threshold = %s, want %s", got, want)
	}
	if got, want := settings.StuckThreshold, 2*time.Hour; got != want {
		t.Fatalf("stuck-threshold = %s, want %s", got, want)
	}
	if got, want := settings.ScanInterval, 45*time.Second; got != want {
		t.Fatalf("interval = %s, want %s", got, want)
	}
}

func TestParseEditableSettingsRejectsInvalidThresholds(t *testing.T) {
	useTempHome(t)

	if _, err := ParseEditableSettings("/tmp/a", "", "", "yolo", "20m", "10m", "60s"); err == nil {
		t.Fatalf("expected validation error")
	}
}

func TestParseEditableSettingsRejectsInvalidCodexPreset(t *testing.T) {
	useTempHome(t)

	if _, err := ParseEditableSettings("/tmp/a", "", "", "turbo", "20m", "2h", "60s"); err == nil {
		t.Fatalf("expected codex preset validation error")
	}
}

func TestSaveEditableSettingsWritesReadableTOML(t *testing.T) {
	useTempHome(t)
	configPath := filepath.Join(t.TempDir(), "config.toml")

	err := SaveEditableSettings(configPath, EditableSettings{
		IncludePaths:           []string{"/tmp/a", "/tmp/b"},
		ExcludePaths:           []string{"/tmp/skip"},
		ExcludeProjectPatterns: []string{"quickgame_*", "secret-demo"},
		CodexLaunchPreset:      codexcli.PresetFullAuto,
		ScanInterval:           45 * time.Second,
		ActiveThreshold:        15 * time.Minute,
		StuckThreshold:         3 * time.Hour,
	})
	if err != nil {
		t.Fatalf("SaveEditableSettings() error = %v", err)
	}

	raw, err := os.ReadFile(configPath)
	if err != nil {
		t.Fatalf("read config file: %v", err)
	}
	text := string(raw)
	if !strings.Contains(text, "include_paths = [") {
		t.Fatalf("saved config should include include_paths array: %q", text)
	}
	if !strings.Contains(text, "exclude_paths = [") {
		t.Fatalf("saved config should include exclude_paths array: %q", text)
	}
	if !strings.Contains(text, "exclude_project_patterns = [") {
		t.Fatalf("saved config should include exclude_project_patterns array: %q", text)
	}
	if !strings.Contains(text, "codex_launch_preset = \"full-auto\"") {
		t.Fatalf("saved config should include codex launch preset: %q", text)
	}
	if !strings.Contains(text, "interval = \"45s\"") {
		t.Fatalf("saved config should include interval: %q", text)
	}
	if !strings.Contains(text, "active-threshold = \"15m\"") {
		t.Fatalf("saved config should include active threshold: %q", text)
	}
	if !strings.Contains(text, "stuck-threshold = \"3h\"") {
		t.Fatalf("saved config should include stuck threshold: %q", text)
	}
}

func TestLoadExcludeProjectPatternsUsesFallbackWhenKeyMissing(t *testing.T) {
	useTempHome(t)
	configPath := filepath.Join(t.TempDir(), "config.toml")
	if err := os.WriteFile(configPath, []byte("include_paths = [\"/tmp/demo\"]\n"), 0o644); err != nil {
		t.Fatalf("write config file: %v", err)
	}

	got, err := LoadExcludeProjectPatterns(configPath, []string{"demo-*"})
	if err != nil {
		t.Fatalf("LoadExcludeProjectPatterns() error = %v", err)
	}
	if want := []string{"demo-*"}; !reflect.DeepEqual(got, want) {
		t.Fatalf("exclude project patterns = %v, want %v", got, want)
	}
}

func TestProjectNameExcludedSupportsWildcardPatterns(t *testing.T) {
	tests := []struct {
		name     string
		patterns []string
		value    string
		want     bool
	}{
		{name: "prefix wildcard", patterns: []string{"quickgame_*"}, value: "quickgame_19", want: true},
		{name: "infix wildcard", patterns: []string{"*control*"}, value: "LittleControlRoom", want: true},
		{name: "exact match", patterns: []string{"SecretDemo"}, value: "secretdemo", want: true},
		{name: "no match", patterns: []string{"client-*"}, value: "server-api", want: false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := ProjectNameExcluded(tt.value, tt.patterns); got != tt.want {
				t.Fatalf("ProjectNameExcluded(%q, %v) = %v, want %v", tt.value, tt.patterns, got, tt.want)
			}
		})
	}
}

func TestParseMigratesLegacyStateIntoPreferredDir(t *testing.T) {
	home := useTempHome(t)

	legacyDir := filepath.Join(home, brand.LegacyDataDirName)
	if err := os.MkdirAll(legacyDir, 0o755); err != nil {
		t.Fatalf("create legacy dir: %v", err)
	}

	legacyDB := filepath.Join(legacyDir, brand.LegacyDBFileName)
	if err := os.WriteFile(legacyDB, []byte("sqlite"), 0o644); err != nil {
		t.Fatalf("write legacy db: %v", err)
	}

	cfg, err := Parse("scan", nil)
	if err != nil {
		t.Fatalf("parse config: %v", err)
	}

	preferredDir := filepath.Join(home, brand.DataDirName)
	preferredConfig := filepath.Join(preferredDir, brand.ConfigFileName)
	preferredDB := filepath.Join(preferredDir, brand.DBFileName)

	if cfg.ConfigPath != preferredConfig {
		t.Fatalf("config path = %s, want %s", cfg.ConfigPath, preferredConfig)
	}
	if cfg.DBPath != preferredDB {
		t.Fatalf("db path = %s, want %s", cfg.DBPath, preferredDB)
	}
	if cfg.ConfigLoaded {
		t.Fatalf("expected config to remain unloaded without a TOML file")
	}
	wantIncludePaths := []string{filepath.Join(home, "dev", "repos")}
	if !reflect.DeepEqual(cfg.IncludePaths, wantIncludePaths) {
		t.Fatalf("include paths = %v, want %v", cfg.IncludePaths, wantIncludePaths)
	}
	if _, err := os.Stat(preferredConfig); !os.IsNotExist(err) {
		t.Fatalf("expected no migrated config file, err=%v", err)
	}
	if _, err := os.Stat(preferredDB); err != nil {
		t.Fatalf("expected migrated db file: %v", err)
	}
	if _, err := os.Stat(legacyDB); !os.IsNotExist(err) {
		t.Fatalf("expected legacy db to be moved away, err=%v", err)
	}
}

func TestParseIgnoresPreferredJSONConfig(t *testing.T) {
	home := useTempHome(t)

	preferredDir := filepath.Join(home, brand.DataDirName)
	if err := os.MkdirAll(preferredDir, 0o755); err != nil {
		t.Fatalf("create preferred dir: %v", err)
	}

	legacyConfig := filepath.Join(preferredDir, "config.json")
	if err := os.WriteFile(legacyConfig, []byte(`{"include_paths":["/tmp/from-json"]}`), 0o644); err != nil {
		t.Fatalf("write legacy config: %v", err)
	}

	cfg, err := Parse("scan", nil)
	if err != nil {
		t.Fatalf("parse config: %v", err)
	}

	preferredConfig := filepath.Join(preferredDir, brand.ConfigFileName)
	if cfg.ConfigPath != preferredConfig {
		t.Fatalf("config path = %s, want %s", cfg.ConfigPath, preferredConfig)
	}
	if cfg.ConfigLoaded {
		t.Fatalf("expected config to remain unloaded")
	}
	wantIncludePaths := []string{filepath.Join(home, "dev", "repos")}
	if !reflect.DeepEqual(cfg.IncludePaths, wantIncludePaths) {
		t.Fatalf("include paths = %v, want %v", cfg.IncludePaths, wantIncludePaths)
	}
	if _, err := os.Stat(preferredConfig); !os.IsNotExist(err) {
		t.Fatalf("expected no TOML config file to be created, err=%v", err)
	}
	if _, err := os.Stat(legacyConfig); err != nil {
		t.Fatalf("expected JSON config to remain untouched, err=%v", err)
	}
}
