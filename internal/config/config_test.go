package config

import (
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	"lcroom/internal/brand"
	"lcroom/internal/browserctl"
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

func TestDefaultUsesManagedPlaywrightPolicy(t *testing.T) {
	cfg := Default()

	if got, want := cfg.PlaywrightPolicy.ManagementMode, browserctl.ManagementModeManaged; got != want {
		t.Fatalf("default playwright management mode = %s, want %s", got, want)
	}
	if got, want := cfg.PlaywrightPolicy.DefaultBrowserMode, browserctl.BrowserModeHeadless; got != want {
		t.Fatalf("default playwright default browser mode = %s, want %s", got, want)
	}
	if got, want := cfg.PlaywrightPolicy.LoginMode, browserctl.LoginModePromote; got != want {
		t.Fatalf("default playwright login mode = %s, want %s", got, want)
	}
	if got, want := cfg.PlaywrightPolicy.IsolationScope, browserctl.IsolationScopeTask; got != want {
		t.Fatalf("default playwright isolation scope = %s, want %s", got, want)
	}
}

func TestParseLoadsEditableSettingsFromConfigFile(t *testing.T) {
	useTempHome(t)
	dir := t.TempDir()
	configPath := filepath.Join(dir, "config.toml")
	content := "" +
		"boss_chat_backend = \"openai_api\"\n" +
		"boss_chat_model = \"gpt-5.4-mini\"\n" +
		"boss_helm_model = \"gpt-5.5\"\n" +
		"boss_utility_model = \"gpt-5.4-mini\"\n" +
		"openai_api_key = \"sk-live-example\"\n" +
		"include_paths = [\"/tmp/a\", \"/tmp/b\"]\n" +
		"exclude_paths = [\"/tmp/skip\"]\n" +
		"exclude_project_patterns = [\"quickgame_*\", \"secret-demo\"]\n" +
		"codex_launch_preset = \"safe\"\n" +
		"playwright_management_mode = \"observe\"\n" +
		"playwright_default_browser_mode = \"headed\"\n" +
		"playwright_login_mode = \"promote\"\n" +
		"playwright_isolation_scope = \"project\"\n" +
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
	if got, want := cfg.OpenAIAPIKey, "sk-live-example"; got != want {
		t.Fatalf("openai api key = %q, want %q", got, want)
	}
	if got, want := cfg.BossChatBackend, AIBackendOpenAIAPI; got != want {
		t.Fatalf("boss chat backend = %q, want %q", got, want)
	}
	if got, want := cfg.BossChatModel, "gpt-5.4-mini"; got != want {
		t.Fatalf("boss chat model = %q, want %q", got, want)
	}
	if got, want := cfg.BossHelmModel, "gpt-5.5"; got != want {
		t.Fatalf("boss helm model = %q, want %q", got, want)
	}
	if got, want := cfg.BossUtilityModel, "gpt-5.4-mini"; got != want {
		t.Fatalf("boss utility model = %q, want %q", got, want)
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
	if got, want := cfg.PlaywrightPolicy.ManagementMode, browserctl.ManagementModeObserve; got != want {
		t.Fatalf("playwright management mode = %s, want %s", got, want)
	}
	if got, want := cfg.PlaywrightPolicy.DefaultBrowserMode, browserctl.BrowserModeHeaded; got != want {
		t.Fatalf("playwright default browser mode = %s, want %s", got, want)
	}
	if got, want := cfg.PlaywrightPolicy.LoginMode, browserctl.LoginModePromote; got != want {
		t.Fatalf("playwright login mode = %s, want %s", got, want)
	}
	if got, want := cfg.PlaywrightPolicy.IsolationScope, browserctl.IsolationScopeProject; got != want {
		t.Fatalf("playwright isolation scope = %s, want %s", got, want)
	}
	if got, want := cfg.ActiveThreshold, 15*time.Minute; got != want {
		t.Fatalf("active-threshold = %s, want %s", got, want)
	}
	if got, want := cfg.StuckThreshold, 3*time.Hour; got != want {
		t.Fatalf("stuck-threshold = %s, want %s", got, want)
	}
}

func TestParseLoadsEmbeddedModelPreferencesFromConfigFile(t *testing.T) {
	useTempHome(t)
	dir := t.TempDir()
	configPath := filepath.Join(dir, "config.toml")
	content := "" +
		"embedded_codex_model = \"gpt-5.4\"\n" +
		"embedded_codex_reasoning_effort = \"high\"\n" +
		"embedded_claude_model = \"sonnet\"\n" +
		"embedded_claude_reasoning_effort = \"max\"\n" +
		"embedded_opencode_model = \"openai/gpt-5.4\"\n" +
		"embedded_opencode_reasoning_effort = \"medium\"\n"
	if err := os.WriteFile(configPath, []byte(content), 0o644); err != nil {
		t.Fatalf("write config file: %v", err)
	}

	cfg, err := Parse("scan", []string{"--config", configPath})
	if err != nil {
		t.Fatalf("parse config: %v", err)
	}

	if got, want := cfg.EmbeddedCodexModel, "gpt-5.4"; got != want {
		t.Fatalf("embedded codex model = %q, want %q", got, want)
	}
	if got, want := cfg.EmbeddedCodexReasoning, "high"; got != want {
		t.Fatalf("embedded codex reasoning = %q, want %q", got, want)
	}
	if got, want := cfg.EmbeddedClaudeModel, "sonnet"; got != want {
		t.Fatalf("embedded claude model = %q, want %q", got, want)
	}
	if got, want := cfg.EmbeddedClaudeReasoning, "max"; got != want {
		t.Fatalf("embedded claude reasoning = %q, want %q", got, want)
	}
	if got, want := cfg.EmbeddedOpenCodeModel, "openai/gpt-5.4"; got != want {
		t.Fatalf("embedded opencode model = %q, want %q", got, want)
	}
	if got, want := cfg.EmbeddedOpenCodeReasoning, "medium"; got != want {
		t.Fatalf("embedded opencode reasoning = %q, want %q", got, want)
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

func TestParseAllowMultipleInstancesFlag(t *testing.T) {
	useTempHome(t)

	cfg, err := Parse("tui", []string{"--allow-multiple-instances"})
	if err != nil {
		t.Fatalf("parse config: %v", err)
	}
	if !cfg.AllowMultipleInstances {
		t.Fatalf("expected allow-multiple-instances to be enabled")
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

func TestParseSanitizeSummaryFlags(t *testing.T) {
	useTempHome(t)

	cfg, err := Parse("sanitize-summaries", []string{
		"--project", "/tmp/demo",
		"--session-id", "ses_demo",
		"--dry-run=false",
	})
	if err != nil {
		t.Fatalf("parse sanitize config: %v", err)
	}
	if cfg.SanitizeProject != "/tmp/demo" {
		t.Fatalf("sanitize project = %q, want /tmp/demo", cfg.SanitizeProject)
	}
	if cfg.SanitizeSessionID != "ses_demo" {
		t.Fatalf("sanitize session id = %q, want ses_demo", cfg.SanitizeSessionID)
	}
	if cfg.SanitizeDryRun {
		t.Fatalf("sanitize dry-run = %v, want false", cfg.SanitizeDryRun)
	}
}

func TestParseSanitizeSummaryApplyFlag(t *testing.T) {
	useTempHome(t)

	cfg, err := Parse("sanitize-summaries", []string{"--apply"})
	if err != nil {
		t.Fatalf("parse sanitize config: %v", err)
	}
	if !cfg.SanitizeApply {
		t.Fatalf("sanitize apply = %v, want true", cfg.SanitizeApply)
	}
	if !cfg.SanitizeDryRun {
		t.Fatalf("sanitize dry-run = %v, want true by default", cfg.SanitizeDryRun)
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

	settings, err := ParseEditableSettings(AIBackendOpenAIAPI, AIBackendOpenAIAPI, "sk-test-example", "gpt-5.5", "gpt-5.4-mini", "", "", "", "", "", "", "~/dev/repos,/tmp/other", "/tmp/skip", "quickgame_*,secret-demo", "medical,visa", "yolo", "observe", "headed", "promote", "project", "true", "false", "free", "10m", "2h", "45s")
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
	if got, want := settings.OpenAIAPIKey, "sk-test-example"; got != want {
		t.Fatalf("openai api key = %q, want %q", got, want)
	}
	if got, want := settings.BossChatBackend, AIBackendOpenAIAPI; got != want {
		t.Fatalf("boss chat backend = %q, want %q", got, want)
	}
	if got, want := settings.BossHelmModel, "gpt-5.5"; got != want {
		t.Fatalf("boss helm model = %q, want %q", got, want)
	}
	if got, want := settings.BossUtilityModel, "gpt-5.4-mini"; got != want {
		t.Fatalf("boss utility model = %q, want %q", got, want)
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
	if got, want := settings.PlaywrightPolicy.ManagementMode, browserctl.ManagementModeObserve; got != want {
		t.Fatalf("playwright management mode = %s, want %s", got, want)
	}
	if got, want := settings.PlaywrightPolicy.DefaultBrowserMode, browserctl.BrowserModeHeaded; got != want {
		t.Fatalf("playwright default browser mode = %s, want %s", got, want)
	}
	if got, want := settings.PlaywrightPolicy.LoginMode, browserctl.LoginModePromote; got != want {
		t.Fatalf("playwright login mode = %s, want %s", got, want)
	}
	if got, want := settings.PlaywrightPolicy.IsolationScope, browserctl.IsolationScopeProject; got != want {
		t.Fatalf("playwright isolation scope = %s, want %s", got, want)
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

	if _, err := ParseEditableSettings(AIBackendOpenAIAPI, AIBackendOpenAIAPI, "sk-test-example", "", "", "", "", "", "", "", "", "/tmp/a", "", "", "", "yolo", "legacy", "headless", "manual", "task", "false", "false", "", "20m", "10m", "60s"); err == nil {
		t.Fatalf("expected validation error")
	}
}

func TestParseEditableSettingsRejectsInvalidCodexPreset(t *testing.T) {
	useTempHome(t)

	if _, err := ParseEditableSettings(AIBackendOpenAIAPI, AIBackendOpenAIAPI, "sk-test-example", "", "", "", "", "", "", "", "", "/tmp/a", "", "", "", "turbo", "legacy", "headless", "manual", "task", "false", "false", "", "20m", "2h", "60s"); err == nil {
		t.Fatalf("expected codex preset validation error")
	}
}

func TestParseEditableSettingsAllowsMissingOpenAIAPIKeyForNonAPIBackends(t *testing.T) {
	useTempHome(t)

	settings, err := ParseEditableSettings(AIBackendCodex, AIBackendUnset, "", "", "", "", "", "", "", "", "", "/tmp/a", "", "", "", "yolo", "legacy", "headless", "manual", "task", "false", "false", "", "20m", "2h", "60s")
	if err != nil {
		t.Fatalf("ParseEditableSettings() error = %v", err)
	}
	if settings.AIBackend != AIBackendCodex {
		t.Fatalf("ai backend = %s, want %s", settings.AIBackend, AIBackendCodex)
	}
}

func TestSaveEditableSettingsWritesReadableTOML(t *testing.T) {
	useTempHome(t)
	configPath := filepath.Join(t.TempDir(), "config.toml")

	err := SaveEditableSettings(configPath, EditableSettings{
		AIBackend:                 AIBackendOpenAIAPI,
		BossChatBackend:           AIBackendOpenAIAPI,
		BossHelmModel:             "gpt-5.5",
		BossUtilityModel:          "gpt-5.4-mini",
		OpenAIAPIKey:              "sk-test-example",
		MLXBaseURL:                "http://127.0.0.1:8080/v1",
		MLXAPIKey:                 "mlx",
		MLXModel:                  "mlx-community/Qwen3.5-9B-MLX-4bit",
		OllamaBaseURL:             "http://127.0.0.1:11434/v1",
		OllamaAPIKey:              "ollama",
		OllamaModel:               "qwen3.5:latest",
		IncludePaths:              []string{"/tmp/a", "/tmp/b"},
		ExcludePaths:              []string{"/tmp/skip"},
		ExcludeProjectPatterns:    []string{"quickgame_*", "secret-demo"},
		EmbeddedCodexModel:        "gpt-5.4",
		EmbeddedCodexReasoning:    "high",
		EmbeddedClaudeModel:       "sonnet",
		EmbeddedClaudeReasoning:   "max",
		EmbeddedOpenCodeModel:     "openai/gpt-5.4",
		EmbeddedOpenCodeReasoning: "medium",
		CodexLaunchPreset:         codexcli.PresetFullAuto,
		PlaywrightPolicy: browserctl.Policy{
			ManagementMode:     browserctl.ManagementModeObserve,
			DefaultBrowserMode: browserctl.BrowserModeHeaded,
			LoginMode:          browserctl.LoginModePromote,
			IsolationScope:     browserctl.IsolationScopeProject,
		},
		ScanInterval:    45 * time.Second,
		ActiveThreshold: 15 * time.Minute,
		StuckThreshold:  3 * time.Hour,
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
	if !strings.Contains(text, "ai_backend = \"openai_api\"") {
		t.Fatalf("saved config should include ai_backend: %q", text)
	}
	if !strings.Contains(text, "boss_chat_backend = \"openai_api\"") {
		t.Fatalf("saved config should include boss_chat_backend: %q", text)
	}
	if !strings.Contains(text, "boss_helm_model = \"gpt-5.5\"") {
		t.Fatalf("saved config should include boss_helm_model: %q", text)
	}
	if !strings.Contains(text, "boss_utility_model = \"gpt-5.4-mini\"") {
		t.Fatalf("saved config should include boss_utility_model: %q", text)
	}
	if !strings.Contains(text, "openai_api_key = \"sk-test-example\"") {
		t.Fatalf("saved config should include openai api key: %q", text)
	}
	for _, want := range []string{
		"mlx_base_url = \"http://127.0.0.1:8080/v1\"",
		"mlx_api_key = \"mlx\"",
		"mlx_model = \"mlx-community/Qwen3.5-9B-MLX-4bit\"",
		"ollama_base_url = \"http://127.0.0.1:11434/v1\"",
		"ollama_api_key = \"ollama\"",
		"ollama_model = \"qwen3.5:latest\"",
	} {
		if !strings.Contains(text, want) {
			t.Fatalf("saved config should include %q: %q", want, text)
		}
	}
	if !strings.Contains(text, "exclude_paths = [") {
		t.Fatalf("saved config should include exclude_paths array: %q", text)
	}
	if !strings.Contains(text, "exclude_project_patterns = [") {
		t.Fatalf("saved config should include exclude_project_patterns array: %q", text)
	}
	for _, want := range []string{
		"playwright_management_mode = \"observe\"",
		"playwright_default_browser_mode = \"headed\"",
		"playwright_login_mode = \"promote\"",
		"playwright_isolation_scope = \"project\"",
	} {
		if !strings.Contains(text, want) {
			t.Fatalf("saved config should include %q: %q", want, text)
		}
	}
	if !strings.Contains(text, "embedded_codex_model = \"gpt-5.4\"") {
		t.Fatalf("saved config should include embedded codex model: %q", text)
	}
	if !strings.Contains(text, "embedded_codex_reasoning_effort = \"high\"") {
		t.Fatalf("saved config should include embedded codex reasoning: %q", text)
	}
	if !strings.Contains(text, "embedded_claude_model = \"sonnet\"") {
		t.Fatalf("saved config should include embedded claude model: %q", text)
	}
	if !strings.Contains(text, "embedded_claude_reasoning_effort = \"max\"") {
		t.Fatalf("saved config should include embedded claude reasoning: %q", text)
	}
	if !strings.Contains(text, "embedded_opencode_model = \"openai/gpt-5.4\"") {
		t.Fatalf("saved config should include embedded opencode model: %q", text)
	}
	if !strings.Contains(text, "embedded_opencode_reasoning_effort = \"medium\"") {
		t.Fatalf("saved config should include embedded opencode reasoning: %q", text)
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
	info, err := os.Stat(configPath)
	if err != nil {
		t.Fatalf("stat config file: %v", err)
	}
	if got, want := info.Mode().Perm(), os.FileMode(0o600); got != want {
		t.Fatalf("config file mode = %o, want %o", got, want)
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

func TestParseLeavesLegacyStateUntouched(t *testing.T) {
	home := useTempHome(t)

	legacyDir := filepath.Join(home, ".batondeck")
	if err := os.MkdirAll(legacyDir, 0o755); err != nil {
		t.Fatalf("create legacy dir: %v", err)
	}

	legacyDB := filepath.Join(legacyDir, "batondeck.sqlite")
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
		t.Fatalf("expected no TOML config file to be created, err=%v", err)
	}
	if _, err := os.Stat(preferredDB); !os.IsNotExist(err) {
		t.Fatalf("expected preferred db to remain absent, err=%v", err)
	}
	if _, err := os.Stat(legacyDB); err != nil {
		t.Fatalf("expected legacy db to remain untouched, err=%v", err)
	}
}
