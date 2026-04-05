package config

import (
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"lcroom/internal/codexcli"
)

type EditableSettings struct {
	AIBackend                 AIBackend
	OpenAIAPIKey              string
	MLXBaseURL                string
	MLXAPIKey                 string
	MLXModel                  string
	OllamaBaseURL             string
	OllamaAPIKey              string
	OllamaModel               string
	IncludePaths              []string
	ExcludePaths              []string
	ExcludeProjectPatterns    []string
	PrivacyPatterns           []string
	EmbeddedCodexModel        string
	EmbeddedCodexReasoning    string
	EmbeddedClaudeModel       string
	EmbeddedClaudeReasoning   string
	EmbeddedOpenCodeModel     string
	EmbeddedOpenCodeReasoning string
	OpenCodeModelTier         string
	RecentCodexModels         []string
	RecentClaudeModels        []string
	RecentOpenCodeModels      []string
	CodexLaunchPreset         codexcli.Preset
	ScanInterval              time.Duration
	ActiveThreshold           time.Duration
	StuckThreshold            time.Duration
	HideReasoningSections     bool
	PrivacyMode               bool
}

func EditableSettingsFromAppConfig(cfg AppConfig) EditableSettings {
	return EditableSettings{
		AIBackend:                 cfg.EffectiveAIBackend(),
		OpenAIAPIKey:              cfg.OpenAIAPIKey,
		MLXBaseURL:                cfg.MLXBaseURL,
		MLXAPIKey:                 cfg.MLXAPIKey,
		MLXModel:                  cfg.MLXModel,
		OllamaBaseURL:             cfg.OllamaBaseURL,
		OllamaAPIKey:              cfg.OllamaAPIKey,
		OllamaModel:               cfg.OllamaModel,
		IncludePaths:              append([]string(nil), cfg.IncludePaths...),
		ExcludePaths:              append([]string(nil), cfg.ExcludePaths...),
		ExcludeProjectPatterns:    append([]string(nil), cfg.ExcludeProjectPatterns...),
		PrivacyPatterns:           append([]string(nil), cfg.PrivacyPatterns...),
		EmbeddedCodexModel:        cfg.EmbeddedCodexModel,
		EmbeddedCodexReasoning:    cfg.EmbeddedCodexReasoning,
		EmbeddedClaudeModel:       cfg.EmbeddedClaudeModel,
		EmbeddedClaudeReasoning:   cfg.EmbeddedClaudeReasoning,
		EmbeddedOpenCodeModel:     cfg.EmbeddedOpenCodeModel,
		EmbeddedOpenCodeReasoning: cfg.EmbeddedOpenCodeReasoning,
		OpenCodeModelTier:         cfg.OpenCodeModelTier,
		RecentCodexModels:         append([]string(nil), cfg.RecentCodexModels...),
		RecentClaudeModels:        append([]string(nil), cfg.RecentClaudeModels...),
		RecentOpenCodeModels:      append([]string(nil), cfg.RecentOpenCodeModels...),
		CodexLaunchPreset:         cfg.CodexLaunchPreset,
		ScanInterval:              cfg.ScanInterval,
		ActiveThreshold:           cfg.ActiveThreshold,
		StuckThreshold:            cfg.StuckThreshold,
		HideReasoningSections:     cfg.HideReasoningSections,
		PrivacyMode:               cfg.PrivacyMode,
	}
}

func (s EditableSettings) OpenAICompatibleModel(backend AIBackend) string {
	switch backend {
	case AIBackendMLX:
		return strings.TrimSpace(s.MLXModel)
	case AIBackendOllama:
		return strings.TrimSpace(s.OllamaModel)
	default:
		return ""
	}
}

func (s *EditableSettings) SetOpenAICompatibleModel(backend AIBackend, model string) {
	if s == nil {
		return
	}
	model = strings.TrimSpace(model)
	switch backend {
	case AIBackendMLX:
		s.MLXModel = model
	case AIBackendOllama:
		s.OllamaModel = model
	}
}

func ParseEditableSettings(aiBackend AIBackend, openAIAPIKeyRaw, mlxBaseURLRaw, mlxAPIKeyRaw, mlxModelRaw, ollamaBaseURLRaw, ollamaAPIKeyRaw, ollamaModelRaw, includeRaw, excludeRaw, excludeProjectPatternsRaw, privacyPatternsRaw, codexLaunchPresetRaw, hideReasoningSectionsRaw, privacyModeRaw, openCodeModelTierRaw, activeRaw, stuckRaw, intervalRaw string) (EditableSettings, error) {
	parsedBackend, err := ParseAIBackend(string(aiBackend))
	if err != nil {
		return EditableSettings{}, err
	}
	openAIAPIKey := strings.TrimSpace(openAIAPIKeyRaw)
	mlxBaseURL := strings.TrimSpace(mlxBaseURLRaw)
	mlxAPIKey := strings.TrimSpace(mlxAPIKeyRaw)
	mlxModel := strings.TrimSpace(mlxModelRaw)
	ollamaBaseURL := strings.TrimSpace(ollamaBaseURLRaw)
	ollamaAPIKey := strings.TrimSpace(ollamaAPIKeyRaw)
	ollamaModel := strings.TrimSpace(ollamaModelRaw)

	includePaths, err := expandAndSplitPaths(includeRaw)
	if err != nil {
		return EditableSettings{}, fmt.Errorf("include paths: %w", err)
	}
	excludePaths, err := expandAndSplitPaths(excludeRaw)
	if err != nil {
		return EditableSettings{}, fmt.Errorf("exclude paths: %w", err)
	}
	excludeProjectPatterns := normalizeProjectPatterns(strings.Split(excludeProjectPatternsRaw, ","))
	privacyPatterns := normalizeProjectPatterns(strings.Split(privacyPatternsRaw, ","))
	codexLaunchPreset, err := codexcli.ParsePreset(codexLaunchPresetRaw)
	if err != nil {
		return EditableSettings{}, fmt.Errorf("codex launch preset: %w", err)
	}

	hideReasoningSections := strings.EqualFold(strings.TrimSpace(hideReasoningSectionsRaw), "true")
	privacyMode := strings.EqualFold(strings.TrimSpace(privacyModeRaw), "true")

	active, err := parseConfigDuration(strings.TrimSpace(activeRaw), "active-threshold")
	if err != nil {
		return EditableSettings{}, err
	}

	stuck, err := parseConfigDuration(strings.TrimSpace(stuckRaw), "stuck-threshold")
	if err != nil {
		return EditableSettings{}, err
	}

	interval, err := parseConfigDuration(strings.TrimSpace(intervalRaw), "interval")
	if err != nil {
		return EditableSettings{}, err
	}

	settings := EditableSettings{
		AIBackend:              parsedBackend,
		OpenAIAPIKey:           openAIAPIKey,
		MLXBaseURL:             mlxBaseURL,
		MLXAPIKey:              mlxAPIKey,
		MLXModel:               mlxModel,
		OllamaBaseURL:          ollamaBaseURL,
		OllamaAPIKey:           ollamaAPIKey,
		OllamaModel:            ollamaModel,
		IncludePaths:           includePaths,
		ExcludePaths:           excludePaths,
		ExcludeProjectPatterns: excludeProjectPatterns,
		PrivacyPatterns:        privacyPatterns,
		CodexLaunchPreset:      codexLaunchPreset,
		OpenCodeModelTier:      strings.TrimSpace(openCodeModelTierRaw),
		HideReasoningSections:  hideReasoningSections,
		PrivacyMode:            privacyMode,
		ScanInterval:           interval,
		ActiveThreshold:        active,
		StuckThreshold:         stuck,
	}
	if err := validateEditableSettings(settings); err != nil {
		return EditableSettings{}, err
	}
	return settings, nil
}

func SaveEditableSettings(path string, settings EditableSettings) error {
	if err := validateEditableSettings(settings); err != nil {
		return err
	}
	if path == "" {
		return fmt.Errorf("config path is required")
	}
	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return fmt.Errorf("create config directory: %w", err)
	}

	tempFile, err := os.CreateTemp(dir, filepath.Base(path)+".tmp-*")
	if err != nil {
		return fmt.Errorf("create temp config file: %w", err)
	}
	tempPath := tempFile.Name()
	defer func() {
		_ = tempFile.Close()
		_ = os.Remove(tempPath)
	}()

	if _, err := tempFile.WriteString(renderEditableSettings(settings)); err != nil {
		return fmt.Errorf("write temp config file: %w", err)
	}
	if err := tempFile.Chmod(0o600); err != nil {
		return fmt.Errorf("chmod temp config file: %w", err)
	}
	if err := tempFile.Close(); err != nil {
		return fmt.Errorf("close temp config file: %w", err)
	}
	if err := os.Rename(tempPath, path); err != nil {
		return fmt.Errorf("install config file: %w", err)
	}
	return nil
}

func validateEditableSettings(settings EditableSettings) error {
	cfg := Default()
	cfg.AIBackend = settings.AIBackend
	cfg.OpenAIAPIKey = settings.OpenAIAPIKey
	cfg.MLXBaseURL = settings.MLXBaseURL
	cfg.MLXAPIKey = settings.MLXAPIKey
	cfg.MLXModel = settings.MLXModel
	cfg.OllamaBaseURL = settings.OllamaBaseURL
	cfg.OllamaAPIKey = settings.OllamaAPIKey
	cfg.OllamaModel = settings.OllamaModel
	cfg.IncludePaths = append([]string(nil), settings.IncludePaths...)
	cfg.ExcludePaths = append([]string(nil), settings.ExcludePaths...)
	cfg.ExcludeProjectPatterns = append([]string(nil), settings.ExcludeProjectPatterns...)
	cfg.PrivacyPatterns = append([]string(nil), settings.PrivacyPatterns...)
	cfg.EmbeddedCodexModel = strings.TrimSpace(settings.EmbeddedCodexModel)
	cfg.EmbeddedCodexReasoning = strings.TrimSpace(settings.EmbeddedCodexReasoning)
	cfg.EmbeddedClaudeModel = strings.TrimSpace(settings.EmbeddedClaudeModel)
	cfg.EmbeddedClaudeReasoning = strings.TrimSpace(settings.EmbeddedClaudeReasoning)
	cfg.EmbeddedOpenCodeModel = strings.TrimSpace(settings.EmbeddedOpenCodeModel)
	cfg.EmbeddedOpenCodeReasoning = strings.TrimSpace(settings.EmbeddedOpenCodeReasoning)
	cfg.OpenCodeModelTier = strings.TrimSpace(settings.OpenCodeModelTier)
	cfg.RecentCodexModels = append([]string(nil), settings.RecentCodexModels...)
	cfg.RecentClaudeModels = append([]string(nil), settings.RecentClaudeModels...)
	cfg.RecentOpenCodeModels = append([]string(nil), settings.RecentOpenCodeModels...)
	cfg.CodexLaunchPreset = settings.CodexLaunchPreset
	cfg.ScanInterval = settings.ScanInterval
	cfg.ActiveThreshold = settings.ActiveThreshold
	cfg.StuckThreshold = settings.StuckThreshold
	cfg.HideReasoningSections = settings.HideReasoningSections
	return validate(cfg)
}

func parseConfigDuration(raw, label string) (time.Duration, error) {
	if raw == "" {
		return 0, fmt.Errorf("%s is required", label)
	}
	d, err := time.ParseDuration(raw)
	if err != nil {
		return 0, fmt.Errorf("%s: %w", label, err)
	}
	return d, nil
}

func renderEditableSettings(settings EditableSettings) string {
	lines := []string{}
	if settings.AIBackend != AIBackendUnset {
		lines = append(lines, fmt.Sprintf("ai_backend = %s", strconv.Quote(string(settings.AIBackend))), "")
	}
	if settings.OpenAIAPIKey != "" {
		lines = append(lines, fmt.Sprintf("openai_api_key = %s", strconv.Quote(settings.OpenAIAPIKey)), "")
	}
	if value := strings.TrimSpace(settings.MLXBaseURL); value != "" {
		lines = append(lines, fmt.Sprintf("mlx_base_url = %s", strconv.Quote(value)))
	}
	if value := strings.TrimSpace(settings.MLXAPIKey); value != "" {
		lines = append(lines, fmt.Sprintf("mlx_api_key = %s", strconv.Quote(value)))
	}
	if value := strings.TrimSpace(settings.MLXModel); value != "" {
		lines = append(lines, fmt.Sprintf("mlx_model = %s", strconv.Quote(value)))
	}
	if value := strings.TrimSpace(settings.OllamaBaseURL); value != "" {
		lines = append(lines, fmt.Sprintf("ollama_base_url = %s", strconv.Quote(value)))
	}
	if value := strings.TrimSpace(settings.OllamaAPIKey); value != "" {
		lines = append(lines, fmt.Sprintf("ollama_api_key = %s", strconv.Quote(value)))
	}
	if value := strings.TrimSpace(settings.OllamaModel); value != "" {
		lines = append(lines, fmt.Sprintf("ollama_model = %s", strconv.Quote(value)))
	}
	if settings.OpenAIAPIKey != "" ||
		strings.TrimSpace(settings.MLXBaseURL) != "" ||
		strings.TrimSpace(settings.MLXAPIKey) != "" ||
		strings.TrimSpace(settings.MLXModel) != "" ||
		strings.TrimSpace(settings.OllamaBaseURL) != "" ||
		strings.TrimSpace(settings.OllamaAPIKey) != "" ||
		strings.TrimSpace(settings.OllamaModel) != "" {
		lines = append(lines, "")
	}
	lines = append(lines, "include_paths = [")
	for _, path := range settings.IncludePaths {
		lines = append(lines, fmt.Sprintf("  %s,", strconv.Quote(path)))
	}
	lines = append(lines, "]")
	lines = append(lines, "")
	lines = append(lines, "exclude_paths = [")
	for _, path := range settings.ExcludePaths {
		lines = append(lines, fmt.Sprintf("  %s,", strconv.Quote(path)))
	}
	lines = append(lines, "]")
	lines = append(lines, "")
	lines = append(lines, "exclude_project_patterns = [")
	for _, pattern := range settings.ExcludeProjectPatterns {
		lines = append(lines, fmt.Sprintf("  %s,", strconv.Quote(pattern)))
	}
	lines = append(lines, "]")
	lines = append(lines, "")
	lines = append(lines, "privacy_patterns = [")
	for _, pattern := range settings.PrivacyPatterns {
		lines = append(lines, fmt.Sprintf("  %s,", strconv.Quote(pattern)))
	}
	lines = append(lines, "]")
	lines = append(lines, "")
	if value := strings.TrimSpace(settings.EmbeddedCodexModel); value != "" {
		lines = append(lines, fmt.Sprintf("embedded_codex_model = %s", strconv.Quote(value)))
	}
	if value := strings.TrimSpace(settings.EmbeddedCodexReasoning); value != "" {
		lines = append(lines, fmt.Sprintf("embedded_codex_reasoning_effort = %s", strconv.Quote(value)))
	}
	if value := strings.TrimSpace(settings.EmbeddedClaudeModel); value != "" {
		lines = append(lines, fmt.Sprintf("embedded_claude_model = %s", strconv.Quote(value)))
	}
	if value := strings.TrimSpace(settings.EmbeddedClaudeReasoning); value != "" {
		lines = append(lines, fmt.Sprintf("embedded_claude_reasoning_effort = %s", strconv.Quote(value)))
	}
	if value := strings.TrimSpace(settings.EmbeddedOpenCodeModel); value != "" {
		lines = append(lines, fmt.Sprintf("embedded_opencode_model = %s", strconv.Quote(value)))
	}
	if value := strings.TrimSpace(settings.EmbeddedOpenCodeReasoning); value != "" {
		lines = append(lines, fmt.Sprintf("embedded_opencode_reasoning_effort = %s", strconv.Quote(value)))
	}
	if strings.TrimSpace(settings.EmbeddedCodexModel) != "" ||
		strings.TrimSpace(settings.EmbeddedCodexReasoning) != "" ||
		strings.TrimSpace(settings.EmbeddedClaudeModel) != "" ||
		strings.TrimSpace(settings.EmbeddedClaudeReasoning) != "" ||
		strings.TrimSpace(settings.EmbeddedOpenCodeModel) != "" ||
		strings.TrimSpace(settings.EmbeddedOpenCodeReasoning) != "" {
		lines = append(lines, "")
	}
	if value := strings.TrimSpace(settings.OpenCodeModelTier); value != "" {
		lines = append(lines, fmt.Sprintf("opencode_model_tier = %s", strconv.Quote(value)))
	}
	if len(settings.RecentCodexModels) > 0 {
		lines = append(lines, "recent_codex_models = [")
		for _, model := range settings.RecentCodexModels {
			lines = append(lines, fmt.Sprintf("  %s,", strconv.Quote(model)))
		}
		lines = append(lines, "]")
		lines = append(lines, "")
	}
	if len(settings.RecentClaudeModels) > 0 {
		lines = append(lines, "recent_claude_models = [")
		for _, model := range settings.RecentClaudeModels {
			lines = append(lines, fmt.Sprintf("  %s,", strconv.Quote(model)))
		}
		lines = append(lines, "]")
		lines = append(lines, "")
	}
	if len(settings.RecentOpenCodeModels) > 0 {
		lines = append(lines, "recent_opencode_models = [")
		for _, model := range settings.RecentOpenCodeModels {
			lines = append(lines, fmt.Sprintf("  %s,", strconv.Quote(model)))
		}
		lines = append(lines, "]")
		lines = append(lines, "")
	}
	lines = append(lines, fmt.Sprintf("codex_launch_preset = %s", strconv.Quote(string(settings.CodexLaunchPreset))))
	lines = append(lines, "")
	lines = append(lines, fmt.Sprintf("hide_reasoning_sections = %t", settings.HideReasoningSections))
	lines = append(lines, "")
	lines = append(lines, fmt.Sprintf("privacy_mode = %t", settings.PrivacyMode))
	lines = append(lines, "")
	lines = append(lines, fmt.Sprintf("interval = %s", strconv.Quote(formatConfigDuration(settings.ScanInterval))))
	lines = append(lines, fmt.Sprintf("active-threshold = %s", strconv.Quote(formatConfigDuration(settings.ActiveThreshold))))
	lines = append(lines, fmt.Sprintf("stuck-threshold = %s", strconv.Quote(formatConfigDuration(settings.StuckThreshold))))
	lines = append(lines, "")
	return strings.Join(lines, "\n")
}

func formatConfigDuration(d time.Duration) string {
	switch {
	case d == 0:
		return "0s"
	case d%(24*time.Hour) == 0:
		return fmt.Sprintf("%dh", int(d/time.Hour))
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
