package config

import (
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"strconv"
	"strings"
	"time"

	"lcroom/internal/browserctl"
	"lcroom/internal/codexcli"
)

type EditableSettings struct {
	AIBackend                 AIBackend
	BossChatBackend           AIBackend
	BossChatModel             string
	BossHelmModel             string
	BossUtilityModel          string
	BossChatOllamaThinking    bool
	OpenAIAPIKey              string
	OpenRouterAPIKey          string
	OpenRouterModel           string
	DeepSeekAPIKey            string
	DeepSeekModel             string
	MoonshotAPIKey            string
	MoonshotModel             string
	XiaomiBaseURL             string
	XiaomiAPIKey              string
	XiaomiModel               string
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
	EmbeddedLCAgentModel      string
	EmbeddedLCAgentReasoning  string
	OpenCodeModelTier         string
	RecentCodexModels         []string
	RecentClaudeModels        []string
	RecentOpenCodeModels      []string
	RecentLCAgentModels       []string
	LCAgentPath               string
	LCAgentEnvFile            string
	LCAgentRoutePreset        string
	LCAgentProvider           string
	LCAgentAuto               string
	LCAgentAdminWrite         bool
	LCAgentToolProfile        string
	LCAgentContextProfile     string
	LCAgentRequestTimeout     time.Duration
	LCAgentUtilityProvider    string
	LCAgentUtilityModel       string
	LCAgentWebSearchBackend   string
	LCAgentWebSearchAPIKey    string
	LCAgentWebSearchEngineID  string
	LCAgentWebSearchURL       string
	CodexLaunchPreset         codexcli.Preset
	PlaywrightPolicy          browserctl.Policy
	ScanInterval              time.Duration
	ActiveThreshold           time.Duration
	StuckThreshold            time.Duration
	HideReasoningSections     bool
	PrivacyMode               bool
}

func EditableSettingsFromAppConfig(cfg AppConfig) EditableSettings {
	return EditableSettings{
		AIBackend:                 cfg.EffectiveAIBackend(),
		BossChatBackend:           cfg.EffectiveBossChatBackend(),
		BossChatModel:             cfg.BossChatModel,
		BossHelmModel:             firstNonEmptyTrimmed(cfg.BossHelmModel, cfg.BossChatModel),
		BossUtilityModel:          cfg.BossUtilityModel,
		BossChatOllamaThinking:    cfg.BossChatOllamaThinking,
		OpenAIAPIKey:              cfg.OpenAIAPIKey,
		OpenRouterAPIKey:          cfg.OpenRouterAPIKey,
		OpenRouterModel:           cfg.OpenRouterModel,
		DeepSeekAPIKey:            cfg.DeepSeekAPIKey,
		DeepSeekModel:             cfg.DeepSeekModel,
		MoonshotAPIKey:            cfg.MoonshotAPIKey,
		MoonshotModel:             cfg.MoonshotModel,
		XiaomiBaseURL:             cfg.XiaomiBaseURL,
		XiaomiAPIKey:              cfg.XiaomiAPIKey,
		XiaomiModel:               cfg.XiaomiModel,
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
		EmbeddedLCAgentModel:      cfg.EmbeddedLCAgentModel,
		EmbeddedLCAgentReasoning:  cfg.EmbeddedLCAgentReasoning,
		OpenCodeModelTier:         cfg.OpenCodeModelTier,
		RecentCodexModels:         append([]string(nil), cfg.RecentCodexModels...),
		RecentClaudeModels:        append([]string(nil), cfg.RecentClaudeModels...),
		RecentOpenCodeModels:      append([]string(nil), cfg.RecentOpenCodeModels...),
		RecentLCAgentModels:       append([]string(nil), cfg.RecentLCAgentModels...),
		LCAgentPath:               cfg.LCAgentPath,
		LCAgentEnvFile:            cfg.LCAgentEnvFile,
		LCAgentRoutePreset:        cfg.LCAgentRoutePreset,
		LCAgentProvider:           cfg.LCAgentProvider,
		LCAgentAuto:               cfg.LCAgentAuto,
		LCAgentAdminWrite:         cfg.LCAgentAdminWrite,
		LCAgentToolProfile:        cfg.LCAgentToolProfile,
		LCAgentContextProfile:     cfg.LCAgentContextProfile,
		LCAgentRequestTimeout:     cfg.LCAgentRequestTimeout,
		LCAgentUtilityProvider:    cfg.LCAgentUtilityProvider,
		LCAgentUtilityModel:       cfg.LCAgentUtilityModel,
		LCAgentWebSearchBackend:   cfg.LCAgentWebSearchBackend,
		LCAgentWebSearchAPIKey:    cfg.LCAgentWebSearchAPIKey,
		LCAgentWebSearchEngineID:  cfg.LCAgentWebSearchEngineID,
		LCAgentWebSearchURL:       cfg.LCAgentWebSearchURL,
		CodexLaunchPreset:         cfg.CodexLaunchPreset,
		PlaywrightPolicy:          cfg.PlaywrightPolicy.Normalize(),
		ScanInterval:              cfg.ScanInterval,
		ActiveThreshold:           cfg.ActiveThreshold,
		StuckThreshold:            cfg.StuckThreshold,
		HideReasoningSections:     cfg.HideReasoningSections,
		PrivacyMode:               cfg.PrivacyMode,
	}
}

func AppConfigFromEditableSettings(base AppConfig, settings EditableSettings) AppConfig {
	cfg := base
	settingsValue := reflect.ValueOf(settings)
	cfgValue := reflect.ValueOf(&cfg).Elem()
	settingsType := settingsValue.Type()
	for i := 0; i < settingsValue.NumField(); i++ {
		settingsField := settingsType.Field(i)
		cfgField := cfgValue.FieldByName(settingsField.Name)
		if !cfgField.IsValid() || !cfgField.CanSet() {
			continue
		}
		value := settingsValue.Field(i)
		if value.Type().AssignableTo(cfgField.Type()) {
			cfgField.Set(value)
		}
	}
	return cfg
}

func (s EditableSettings) OpenAICompatibleModel(backend AIBackend) string {
	switch backend {
	case AIBackendOpenRouter:
		return trimmedOrDefault(s.OpenRouterModel, backend.DefaultProjectModel())
	case AIBackendDeepSeek:
		return trimmedOrDefault(s.DeepSeekModel, backend.DefaultProjectModel())
	case AIBackendMoonshot:
		return trimmedOrDefault(s.MoonshotModel, backend.DefaultProjectModel())
	case AIBackendXiaomi:
		return trimmedOrDefault(s.XiaomiModel, backend.DefaultProjectModel())
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
	case AIBackendOpenRouter:
		s.OpenRouterModel = model
	case AIBackendDeepSeek:
		s.DeepSeekModel = model
	case AIBackendMoonshot:
		s.MoonshotModel = model
	case AIBackendXiaomi:
		s.XiaomiModel = model
	case AIBackendMLX:
		s.MLXModel = model
	case AIBackendOllama:
		s.OllamaModel = model
	}
}

func firstNonEmptyTrimmed(values ...string) string {
	for _, value := range values {
		if trimmed := strings.TrimSpace(value); trimmed != "" {
			return trimmed
		}
	}
	return ""
}

func NormalizeEditableSettings(settings EditableSettings) EditableSettings {
	settings.EmbeddedLCAgentModel = normalizeLCAgentModelForProvider(lcagentEffectiveMainProvider(settings.LCAgentRoutePreset, settings.LCAgentProvider), settings.EmbeddedLCAgentModel)
	settings.LCAgentUtilityModel = normalizeLCAgentModelForProvider(lcagentEffectiveUtilityProvider(settings.LCAgentRoutePreset, settings.LCAgentProvider, settings.LCAgentUtilityProvider), settings.LCAgentUtilityModel)
	return settings
}

func lcagentEffectiveMainProvider(routePreset, provider string) string {
	return firstNonEmptyTrimmed(lcagentRoutePresetProvider(routePreset), provider, "openrouter")
}

func lcagentEffectiveUtilityProvider(routePreset, mainProvider, utilityProvider string) string {
	value, err := parseLCAgentUtilityProvider(utilityProvider)
	if err != nil || value == "main" {
		return lcagentEffectiveMainProvider(routePreset, mainProvider)
	}
	return value
}

func lcagentRoutePresetProvider(preset string) string {
	switch strings.ToLower(strings.TrimSpace(preset)) {
	case "quality":
		return "openai"
	case "balanced", "cheap-scout", "cheap", "scout":
		return "deepseek"
	case "mimo-2.5-pro", "mimo-2.5-pro-low", "mimo-2.5-pro-high", "mimo-2.5-pro-max", "mimo", "mimo-pro", "mimo25pro", "mimo-25-pro", "xiaomi", "xiaomi-mimo":
		return "xiaomi"
	default:
		return ""
	}
}

func normalizeLCAgentModelForProvider(provider, model string) string {
	model = strings.TrimSpace(model)
	if model == "" {
		return ""
	}
	switch strings.ToLower(strings.TrimSpace(provider)) {
	case "openai":
		return trimLCAgentModelProviderPrefix(model, "openai/")
	case "deepseek":
		return trimLCAgentModelProviderPrefix(model, "deepseek/")
	case "moonshot":
		model = trimLCAgentModelProviderPrefix(model, "moonshot/")
		return trimLCAgentModelProviderPrefix(model, "moonshotai/")
	case "xiaomi":
		return trimLCAgentModelProviderPrefix(model, "xiaomi/")
	default:
		return model
	}
}

func trimLCAgentModelProviderPrefix(model, prefix string) string {
	if strings.HasPrefix(strings.ToLower(model), prefix) {
		return strings.TrimSpace(model[len(prefix):])
	}
	return model
}

func ParseEditableSettings(aiBackend AIBackend, bossChatBackend AIBackend, openAIAPIKeyRaw, openRouterAPIKeyRaw, deepSeekAPIKeyRaw, moonshotAPIKeyRaw, xiaomiBaseURLRaw, xiaomiAPIKeyRaw, xiaomiModelRaw, bossHelmModelRaw, bossUtilityModelRaw, bossChatOllamaThinkingRaw, mlxBaseURLRaw, mlxAPIKeyRaw, mlxModelRaw, ollamaBaseURLRaw, ollamaAPIKeyRaw, ollamaModelRaw, includeRaw, excludeRaw, excludeProjectPatternsRaw, privacyPatternsRaw, codexLaunchPresetRaw, playwrightManagementModeRaw, playwrightDefaultBrowserRaw, playwrightLoginModeRaw, playwrightIsolationScopeRaw, hideReasoningSectionsRaw, privacyModeRaw, openCodeModelTierRaw, lcagentPathRaw, lcagentEnvFileRaw, lcagentRoutePresetRaw, lcagentProviderRaw, lcagentAutoRaw, lcagentAdminWriteRaw, lcagentToolProfileRaw, lcagentContextProfileRaw, lcagentRequestTimeoutRaw, lcagentUtilityProviderRaw, lcagentUtilityModelRaw, lcagentWebSearchBackendRaw, lcagentWebSearchAPIKeyRaw, lcagentWebSearchEngineIDRaw, lcagentWebSearchURLRaw, activeRaw, stuckRaw, intervalRaw string) (EditableSettings, error) {
	parsedBackend, err := ParseAIBackend(string(aiBackend))
	if err != nil {
		return EditableSettings{}, err
	}
	parsedBossChatBackend, err := ParseBossChatBackend(string(bossChatBackend))
	if err != nil {
		return EditableSettings{}, err
	}
	openAIAPIKey := strings.TrimSpace(openAIAPIKeyRaw)
	openRouterAPIKey := strings.TrimSpace(openRouterAPIKeyRaw)
	deepSeekAPIKey := strings.TrimSpace(deepSeekAPIKeyRaw)
	moonshotAPIKey := strings.TrimSpace(moonshotAPIKeyRaw)
	xiaomiBaseURL := strings.TrimSpace(xiaomiBaseURLRaw)
	xiaomiAPIKey := strings.TrimSpace(xiaomiAPIKeyRaw)
	xiaomiModel := strings.TrimSpace(xiaomiModelRaw)
	bossHelmModel := strings.TrimSpace(bossHelmModelRaw)
	bossUtilityModel := strings.TrimSpace(bossUtilityModelRaw)
	bossChatOllamaThinking := Default().BossChatOllamaThinking
	if strings.TrimSpace(bossChatOllamaThinkingRaw) != "" {
		var err error
		bossChatOllamaThinking, err = parseOptionalConfigBool(bossChatOllamaThinkingRaw, "boss_chat_ollama_thinking")
		if err != nil {
			return EditableSettings{}, err
		}
	}
	mlxBaseURL := strings.TrimSpace(mlxBaseURLRaw)
	mlxAPIKey := strings.TrimSpace(mlxAPIKeyRaw)
	mlxModel := strings.TrimSpace(mlxModelRaw)
	ollamaBaseURL := strings.TrimSpace(ollamaBaseURLRaw)
	ollamaAPIKey := strings.TrimSpace(ollamaAPIKeyRaw)
	ollamaModel := strings.TrimSpace(ollamaModelRaw)
	lcagentPath, err := expandHome(strings.TrimSpace(lcagentPathRaw))
	if err != nil {
		return EditableSettings{}, fmt.Errorf("lcagent path: %w", err)
	}
	lcagentEnvFile, err := expandHome(strings.TrimSpace(lcagentEnvFileRaw))
	if err != nil {
		return EditableSettings{}, fmt.Errorf("lcagent env file: %w", err)
	}
	lcagentRoutePreset, err := parseLCAgentRoutePreset(lcagentRoutePresetRaw)
	if err != nil {
		return EditableSettings{}, err
	}
	lcagentProvider, err := parseLCAgentProvider(lcagentProviderRaw)
	if err != nil {
		return EditableSettings{}, err
	}
	lcagentAuto, err := parseLCAgentAuto(lcagentAutoRaw)
	if err != nil {
		return EditableSettings{}, err
	}
	lcagentAdminWrite, err := parseOptionalConfigBool(lcagentAdminWriteRaw, "lcagent_admin_write")
	if err != nil {
		return EditableSettings{}, err
	}
	lcagentToolProfile, err := parseLCAgentToolProfile(lcagentToolProfileRaw)
	if err != nil {
		return EditableSettings{}, err
	}
	lcagentContextProfile, err := parseLCAgentContextProfile(lcagentContextProfileRaw)
	if err != nil {
		return EditableSettings{}, err
	}
	lcagentRequestTimeout, err := parseConfigDuration(strings.TrimSpace(lcagentRequestTimeoutRaw), "lcagent_request_timeout")
	if err != nil {
		return EditableSettings{}, err
	}
	lcagentUtilityProvider, err := parseLCAgentUtilityProvider(lcagentUtilityProviderRaw)
	if err != nil {
		return EditableSettings{}, err
	}
	lcagentWebSearchBackend, err := parseLCAgentWebSearchBackend(lcagentWebSearchBackendRaw)
	if err != nil {
		return EditableSettings{}, err
	}

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
	playwrightManagementMode, err := browserctl.ParseManagementMode(playwrightManagementModeRaw)
	if err != nil {
		return EditableSettings{}, err
	}
	playwrightDefaultBrowser, err := browserctl.ParseBrowserMode(playwrightDefaultBrowserRaw)
	if err != nil {
		return EditableSettings{}, err
	}
	playwrightLoginMode, err := browserctl.ParseLoginMode(playwrightLoginModeRaw)
	if err != nil {
		return EditableSettings{}, err
	}
	playwrightIsolationScope, err := browserctl.ParseIsolationScope(playwrightIsolationScopeRaw)
	if err != nil {
		return EditableSettings{}, err
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
		BossChatBackend:        parsedBossChatBackend,
		BossHelmModel:          bossHelmModel,
		BossUtilityModel:       bossUtilityModel,
		BossChatOllamaThinking: bossChatOllamaThinking,
		OpenAIAPIKey:           openAIAPIKey,
		OpenRouterAPIKey:       openRouterAPIKey,
		DeepSeekAPIKey:         deepSeekAPIKey,
		MoonshotAPIKey:         moonshotAPIKey,
		XiaomiBaseURL:          xiaomiBaseURL,
		XiaomiAPIKey:           xiaomiAPIKey,
		XiaomiModel:            xiaomiModel,
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
		PlaywrightPolicy: browserctl.Policy{
			ManagementMode:     playwrightManagementMode,
			DefaultBrowserMode: playwrightDefaultBrowser,
			LoginMode:          playwrightLoginMode,
			IsolationScope:     playwrightIsolationScope,
		},
		OpenCodeModelTier:        strings.TrimSpace(openCodeModelTierRaw),
		LCAgentPath:              lcagentPath,
		LCAgentEnvFile:           lcagentEnvFile,
		LCAgentRoutePreset:       lcagentRoutePreset,
		LCAgentProvider:          lcagentProvider,
		LCAgentAuto:              lcagentAuto,
		LCAgentAdminWrite:        lcagentAdminWrite,
		LCAgentToolProfile:       lcagentToolProfile,
		LCAgentContextProfile:    lcagentContextProfile,
		LCAgentRequestTimeout:    lcagentRequestTimeout,
		LCAgentUtilityProvider:   lcagentUtilityProvider,
		LCAgentUtilityModel:      strings.TrimSpace(lcagentUtilityModelRaw),
		LCAgentWebSearchBackend:  lcagentWebSearchBackend,
		LCAgentWebSearchAPIKey:   strings.TrimSpace(lcagentWebSearchAPIKeyRaw),
		LCAgentWebSearchEngineID: strings.TrimSpace(lcagentWebSearchEngineIDRaw),
		LCAgentWebSearchURL:      strings.TrimSpace(lcagentWebSearchURLRaw),
		HideReasoningSections:    hideReasoningSections,
		PrivacyMode:              privacyMode,
		ScanInterval:             interval,
		ActiveThreshold:          active,
		StuckThreshold:           stuck,
	}
	if err := validateEditableSettings(settings); err != nil {
		return EditableSettings{}, err
	}
	return NormalizeEditableSettings(settings), nil
}

func SaveEditableSettings(path string, settings EditableSettings) error {
	settings = NormalizeEditableSettings(settings)
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
	if err := backupExistingEditableSettings(path); err != nil {
		return fmt.Errorf("backup existing config file: %w", err)
	}
	if err := os.Rename(tempPath, path); err != nil {
		return fmt.Errorf("install config file: %w", err)
	}
	return nil
}

func backupExistingEditableSettings(path string) error {
	raw, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return nil
		}
		return err
	}
	backupPath := fmt.Sprintf("%s.%s.bak", path, time.Now().UTC().Format("20060102-150405.000000000"))
	return os.WriteFile(backupPath, raw, 0o600)
}

func validateEditableSettings(settings EditableSettings) error {
	cfg := AppConfigFromEditableSettings(Default(), settings)
	cfg.BossChatModel = strings.TrimSpace(settings.BossChatModel)
	cfg.BossHelmModel = strings.TrimSpace(settings.BossHelmModel)
	cfg.BossUtilityModel = strings.TrimSpace(settings.BossUtilityModel)
	cfg.OpenRouterModel = strings.TrimSpace(settings.OpenRouterModel)
	cfg.DeepSeekModel = strings.TrimSpace(settings.DeepSeekModel)
	cfg.MoonshotModel = strings.TrimSpace(settings.MoonshotModel)
	cfg.XiaomiBaseURL = strings.TrimSpace(settings.XiaomiBaseURL)
	cfg.XiaomiModel = strings.TrimSpace(settings.XiaomiModel)
	cfg.EmbeddedCodexModel = strings.TrimSpace(settings.EmbeddedCodexModel)
	cfg.EmbeddedCodexReasoning = strings.TrimSpace(settings.EmbeddedCodexReasoning)
	cfg.EmbeddedClaudeModel = strings.TrimSpace(settings.EmbeddedClaudeModel)
	cfg.EmbeddedClaudeReasoning = strings.TrimSpace(settings.EmbeddedClaudeReasoning)
	cfg.EmbeddedOpenCodeModel = strings.TrimSpace(settings.EmbeddedOpenCodeModel)
	cfg.EmbeddedOpenCodeReasoning = strings.TrimSpace(settings.EmbeddedOpenCodeReasoning)
	cfg.EmbeddedLCAgentModel = strings.TrimSpace(settings.EmbeddedLCAgentModel)
	cfg.EmbeddedLCAgentReasoning = strings.TrimSpace(settings.EmbeddedLCAgentReasoning)
	cfg.OpenCodeModelTier = strings.TrimSpace(settings.OpenCodeModelTier)
	cfg.LCAgentPath = strings.TrimSpace(settings.LCAgentPath)
	cfg.LCAgentEnvFile = strings.TrimSpace(settings.LCAgentEnvFile)
	cfg.LCAgentRoutePreset = strings.TrimSpace(settings.LCAgentRoutePreset)
	cfg.LCAgentProvider = strings.TrimSpace(settings.LCAgentProvider)
	cfg.LCAgentAuto = strings.TrimSpace(settings.LCAgentAuto)
	cfg.LCAgentAdminWrite = settings.LCAgentAdminWrite
	cfg.LCAgentToolProfile = strings.TrimSpace(settings.LCAgentToolProfile)
	cfg.LCAgentContextProfile = strings.TrimSpace(settings.LCAgentContextProfile)
	cfg.LCAgentRequestTimeout = settings.LCAgentRequestTimeout
	if cfg.LCAgentRequestTimeout <= 0 {
		cfg.LCAgentRequestTimeout = Default().LCAgentRequestTimeout
	}
	cfg.LCAgentUtilityProvider = strings.TrimSpace(settings.LCAgentUtilityProvider)
	cfg.LCAgentUtilityModel = strings.TrimSpace(settings.LCAgentUtilityModel)
	cfg.LCAgentWebSearchBackend = strings.TrimSpace(settings.LCAgentWebSearchBackend)
	cfg.LCAgentWebSearchAPIKey = strings.TrimSpace(settings.LCAgentWebSearchAPIKey)
	cfg.LCAgentWebSearchEngineID = strings.TrimSpace(settings.LCAgentWebSearchEngineID)
	cfg.LCAgentWebSearchURL = strings.TrimSpace(settings.LCAgentWebSearchURL)
	cfg.PlaywrightPolicy = settings.PlaywrightPolicy.Normalize()
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

func parseOptionalConfigBool(raw, label string) (bool, error) {
	switch strings.ToLower(strings.TrimSpace(raw)) {
	case "":
		return false, nil
	case "true":
		return true, nil
	case "false":
		return false, nil
	default:
		return false, fmt.Errorf("%s must be true or false", label)
	}
}

func renderEditableSettings(settings EditableSettings) string {
	lines := []string{}
	if settings.AIBackend != AIBackendUnset {
		lines = append(lines, fmt.Sprintf("ai_backend = %s", strconv.Quote(string(settings.AIBackend))), "")
	}
	if settings.BossChatBackend != AIBackendUnset {
		lines = append(lines, fmt.Sprintf("boss_chat_backend = %s", strconv.Quote(string(settings.BossChatBackend))))
	}
	if value := strings.TrimSpace(firstNonEmptyTrimmed(settings.BossHelmModel, settings.BossChatModel)); value != "" {
		lines = append(lines, fmt.Sprintf("boss_helm_model = %s", strconv.Quote(value)))
	}
	if value := strings.TrimSpace(settings.BossUtilityModel); value != "" {
		lines = append(lines, fmt.Sprintf("boss_utility_model = %s", strconv.Quote(value)))
	}
	bossChatOllamaThinkingDiffersFromDefault := settings.BossChatOllamaThinking != Default().BossChatOllamaThinking
	if settings.BossChatBackend == AIBackendOllama || bossChatOllamaThinkingDiffersFromDefault {
		lines = append(lines, fmt.Sprintf("boss_chat_ollama_thinking = %t", settings.BossChatOllamaThinking))
	}
	if settings.BossChatBackend != AIBackendUnset ||
		strings.TrimSpace(settings.BossChatModel) != "" ||
		strings.TrimSpace(settings.BossHelmModel) != "" ||
		strings.TrimSpace(settings.BossUtilityModel) != "" ||
		bossChatOllamaThinkingDiffersFromDefault {
		lines = append(lines, "")
	}
	if settings.OpenAIAPIKey != "" {
		lines = append(lines, fmt.Sprintf("openai_api_key = %s", strconv.Quote(settings.OpenAIAPIKey)))
	}
	if settings.OpenRouterAPIKey != "" {
		lines = append(lines, fmt.Sprintf("openrouter_api_key = %s", strconv.Quote(settings.OpenRouterAPIKey)))
	}
	if value := strings.TrimSpace(settings.OpenRouterModel); value != "" {
		lines = append(lines, fmt.Sprintf("openrouter_model = %s", strconv.Quote(value)))
	}
	if settings.DeepSeekAPIKey != "" {
		lines = append(lines, fmt.Sprintf("deepseek_api_key = %s", strconv.Quote(settings.DeepSeekAPIKey)))
	}
	if value := strings.TrimSpace(settings.DeepSeekModel); value != "" {
		lines = append(lines, fmt.Sprintf("deepseek_model = %s", strconv.Quote(value)))
	}
	if settings.MoonshotAPIKey != "" {
		lines = append(lines, fmt.Sprintf("moonshot_api_key = %s", strconv.Quote(settings.MoonshotAPIKey)))
	}
	if value := strings.TrimSpace(settings.MoonshotModel); value != "" {
		lines = append(lines, fmt.Sprintf("moonshot_model = %s", strconv.Quote(value)))
	}
	if value := strings.TrimSpace(settings.XiaomiBaseURL); value != "" {
		lines = append(lines, fmt.Sprintf("xiaomi_base_url = %s", strconv.Quote(value)))
	}
	if settings.XiaomiAPIKey != "" {
		lines = append(lines, fmt.Sprintf("xiaomi_api_key = %s", strconv.Quote(settings.XiaomiAPIKey)))
	}
	if value := strings.TrimSpace(settings.XiaomiModel); value != "" {
		lines = append(lines, fmt.Sprintf("xiaomi_model = %s", strconv.Quote(value)))
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
		strings.TrimSpace(settings.OpenRouterAPIKey) != "" ||
		strings.TrimSpace(settings.OpenRouterModel) != "" ||
		strings.TrimSpace(settings.DeepSeekAPIKey) != "" ||
		strings.TrimSpace(settings.DeepSeekModel) != "" ||
		strings.TrimSpace(settings.MoonshotAPIKey) != "" ||
		strings.TrimSpace(settings.MoonshotModel) != "" ||
		strings.TrimSpace(settings.XiaomiBaseURL) != "" ||
		strings.TrimSpace(settings.XiaomiAPIKey) != "" ||
		strings.TrimSpace(settings.XiaomiModel) != "" ||
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
	if value := strings.TrimSpace(settings.EmbeddedLCAgentModel); value != "" {
		lines = append(lines, fmt.Sprintf("embedded_lcagent_model = %s", strconv.Quote(value)))
	}
	if value := strings.TrimSpace(settings.EmbeddedLCAgentReasoning); value != "" {
		lines = append(lines, fmt.Sprintf("embedded_lcagent_reasoning_effort = %s", strconv.Quote(value)))
	}
	if strings.TrimSpace(settings.EmbeddedCodexModel) != "" ||
		strings.TrimSpace(settings.EmbeddedCodexReasoning) != "" ||
		strings.TrimSpace(settings.EmbeddedClaudeModel) != "" ||
		strings.TrimSpace(settings.EmbeddedClaudeReasoning) != "" ||
		strings.TrimSpace(settings.EmbeddedOpenCodeModel) != "" ||
		strings.TrimSpace(settings.EmbeddedOpenCodeReasoning) != "" ||
		strings.TrimSpace(settings.EmbeddedLCAgentModel) != "" ||
		strings.TrimSpace(settings.EmbeddedLCAgentReasoning) != "" {
		lines = append(lines, "")
	}
	wroteLCAgentConfig := false
	if value := strings.TrimSpace(settings.LCAgentPath); value != "" {
		lines = append(lines, fmt.Sprintf("lcagent_path = %s", strconv.Quote(value)))
		wroteLCAgentConfig = true
	}
	if value := strings.TrimSpace(settings.LCAgentEnvFile); value != "" {
		lines = append(lines, fmt.Sprintf("lcagent_env_file = %s", strconv.Quote(value)))
		wroteLCAgentConfig = true
	}
	if value, err := parseLCAgentRoutePreset(settings.LCAgentRoutePreset); err == nil && value != "" {
		lines = append(lines, fmt.Sprintf("lcagent_route_preset = %s", strconv.Quote(value)))
		wroteLCAgentConfig = true
	}
	if value, err := parseLCAgentProvider(settings.LCAgentProvider); err == nil && value != "" {
		lines = append(lines, fmt.Sprintf("lcagent_provider = %s", strconv.Quote(value)))
		wroteLCAgentConfig = true
	}
	if value, err := parseLCAgentAuto(settings.LCAgentAuto); err == nil && value != "" {
		lines = append(lines, fmt.Sprintf("lcagent_auto = %s", strconv.Quote(value)))
		wroteLCAgentConfig = true
	}
	if settings.LCAgentAdminWrite {
		lines = append(lines, "lcagent_admin_write = true")
		wroteLCAgentConfig = true
	}
	if value, err := parseLCAgentToolProfile(settings.LCAgentToolProfile); err == nil && value != "" {
		lines = append(lines, fmt.Sprintf("lcagent_tool_profile = %s", strconv.Quote(value)))
		wroteLCAgentConfig = true
	}
	if value, err := parseLCAgentContextProfile(settings.LCAgentContextProfile); err == nil && value != "" {
		lines = append(lines, fmt.Sprintf("lcagent_context_profile = %s", strconv.Quote(value)))
		wroteLCAgentConfig = true
	}
	if settings.LCAgentRequestTimeout > 0 {
		lines = append(lines, fmt.Sprintf("lcagent_request_timeout = %s", strconv.Quote(formatConfigDuration(settings.LCAgentRequestTimeout))))
		wroteLCAgentConfig = true
	}
	if value, err := parseLCAgentUtilityProvider(settings.LCAgentUtilityProvider); err == nil && value != "" {
		lines = append(lines, fmt.Sprintf("lcagent_utility_provider = %s", strconv.Quote(value)))
		wroteLCAgentConfig = true
	}
	if value := strings.TrimSpace(settings.LCAgentUtilityModel); value != "" {
		lines = append(lines, fmt.Sprintf("lcagent_utility_model = %s", strconv.Quote(value)))
		wroteLCAgentConfig = true
	}
	if value, err := parseLCAgentWebSearchBackend(settings.LCAgentWebSearchBackend); err == nil && value != "" {
		lines = append(lines, fmt.Sprintf("lcagent_web_search_backend = %s", strconv.Quote(value)))
		wroteLCAgentConfig = true
	}
	if value := strings.TrimSpace(settings.LCAgentWebSearchAPIKey); value != "" {
		lines = append(lines, fmt.Sprintf("lcagent_web_search_api_key = %s", strconv.Quote(value)))
		wroteLCAgentConfig = true
	}
	if value := strings.TrimSpace(settings.LCAgentWebSearchEngineID); value != "" {
		lines = append(lines, fmt.Sprintf("lcagent_web_search_engine_id = %s", strconv.Quote(value)))
		wroteLCAgentConfig = true
	}
	if value := strings.TrimSpace(settings.LCAgentWebSearchURL); value != "" {
		lines = append(lines, fmt.Sprintf("lcagent_web_search_url = %s", strconv.Quote(value)))
		wroteLCAgentConfig = true
	}
	if wroteLCAgentConfig {
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
	if len(settings.RecentLCAgentModels) > 0 {
		lines = append(lines, "recent_lcagent_models = [")
		for _, model := range settings.RecentLCAgentModels {
			lines = append(lines, fmt.Sprintf("  %s,", strconv.Quote(model)))
		}
		lines = append(lines, "]")
		lines = append(lines, "")
	}
	lines = append(lines, fmt.Sprintf("codex_launch_preset = %s", strconv.Quote(string(settings.CodexLaunchPreset))))
	lines = append(lines, "")
	normalizedPolicy := settings.PlaywrightPolicy.Normalize()
	lines = append(lines, fmt.Sprintf("playwright_management_mode = %s", strconv.Quote(string(normalizedPolicy.ManagementMode))))
	lines = append(lines, fmt.Sprintf("playwright_default_browser_mode = %s", strconv.Quote(string(normalizedPolicy.DefaultBrowserMode))))
	lines = append(lines, fmt.Sprintf("playwright_login_mode = %s", strconv.Quote(string(normalizedPolicy.LoginMode))))
	lines = append(lines, fmt.Sprintf("playwright_isolation_scope = %s", strconv.Quote(string(normalizedPolicy.IsolationScope))))
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
