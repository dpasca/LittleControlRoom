package config

import (
	"errors"
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"lcroom/internal/brand"
	"lcroom/internal/browserctl"
	"lcroom/internal/codexcli"

	toml "github.com/pelletier/go-toml/v2"
)

type AppConfig struct {
	AIBackend                 AIBackend
	BossChatBackend           AIBackend
	BossChatModel             string
	BossHelmModel             string
	BossUtilityModel          string
	OpenAIAPIKey              string
	OpenRouterAPIKey          string
	DeepSeekAPIKey            string
	MoonshotAPIKey            string
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
	ScratchRoot               string
	CodexHome                 string
	OpenCodeHome              string
	ClaudeCodeHome            string
	LCAgentPath               string
	LCAgentEnvFile            string
	LCAgentRoutePreset        string
	LCAgentProvider           string
	LCAgentAuto               string
	LCAgentAdminWrite         bool
	LCAgentToolProfile        string
	LCAgentContextProfile     string
	LCAgentRequestTimeout     time.Duration
	LCAgentWebSearchBackend   string
	LCAgentWebSearchAPIKey    string
	LCAgentWebSearchEngineID  string
	LCAgentWebSearchURL       string
	CodexLaunchPreset         codexcli.Preset
	PlaywrightPolicy          browserctl.Policy
	DataDir                   string
	DBPath                    string
	ConfigPath                string
	ConfigLoaded              bool
	DoctorScan                bool
	SnapshotLimit             int
	SnapshotProject           string
	SnapshotSessionID         string
	ScanInterval              time.Duration
	ActiveThreshold           time.Duration
	StuckThreshold            time.Duration
	AllowMultipleInstances    bool
	SanitizeApply             bool
	SanitizeDryRun            bool
	SanitizeProject           string
	SanitizeSessionID         string
	HideReasoningSections     bool
	PrivacyMode               bool
}

const (
	DefaultBossHelmModel    = "gpt-5.5"
	DefaultBossUtilityModel = "gpt-5.4-mini"
)

func (c AppConfig) EffectiveAIBackend() AIBackend {
	return ResolveAIBackend(c.AIBackend, c.OpenAIAPIKey)
}

func (c AppConfig) EffectiveBossChatBackend() AIBackend {
	return ResolveBossChatBackend(c.BossChatBackend, c.OpenAIAPIKey)
}

func (c AppConfig) OpenAICompatibleBaseURL(backend AIBackend) string {
	switch backend {
	case AIBackendMLX:
		return trimmedOrDefault(c.MLXBaseURL, backend.DefaultOpenAICompatibleBaseURL())
	case AIBackendOllama:
		return trimmedOrDefault(c.OllamaBaseURL, backend.DefaultOpenAICompatibleBaseURL())
	default:
		return ""
	}
}

func (c AppConfig) OpenAICompatibleAPIKey(backend AIBackend) string {
	switch backend {
	case AIBackendMLX:
		return trimmedOrDefault(c.MLXAPIKey, backend.DefaultOpenAICompatibleAPIKey())
	case AIBackendOllama:
		return trimmedOrDefault(c.OllamaAPIKey, backend.DefaultOpenAICompatibleAPIKey())
	default:
		return ""
	}
}

func (c AppConfig) OpenAICompatibleModel(backend AIBackend) string {
	switch backend {
	case AIBackendMLX:
		return strings.TrimSpace(c.MLXModel)
	case AIBackendOllama:
		return strings.TrimSpace(c.OllamaModel)
	default:
		return ""
	}
}

type fileConfig struct {
	AIBackend                 string    `toml:"ai_backend"`
	BossChatBackend           string    `toml:"boss_chat_backend"`
	BossChatModel             *string   `toml:"boss_chat_model"`
	BossHelmModel             *string   `toml:"boss_helm_model"`
	BossUtilityModel          *string   `toml:"boss_utility_model"`
	OpenAIAPIKey              *string   `toml:"openai_api_key"`
	OpenRouterAPIKey          *string   `toml:"openrouter_api_key"`
	DeepSeekAPIKey            *string   `toml:"deepseek_api_key"`
	MoonshotAPIKey            *string   `toml:"moonshot_api_key"`
	MLXBaseURL                *string   `toml:"mlx_base_url"`
	MLXAPIKey                 *string   `toml:"mlx_api_key"`
	MLXModel                  *string   `toml:"mlx_model"`
	OllamaBaseURL             *string   `toml:"ollama_base_url"`
	OllamaAPIKey              *string   `toml:"ollama_api_key"`
	OllamaModel               *string   `toml:"ollama_model"`
	IncludePaths              *[]string `toml:"include_paths"`
	ExcludePaths              *[]string `toml:"exclude_paths"`
	ExcludeProjectPatterns    *[]string `toml:"exclude_project_patterns"`
	PrivacyPatterns           *[]string `toml:"privacy_patterns"`
	EmbeddedCodexModel        *string   `toml:"embedded_codex_model"`
	EmbeddedCodexReasoning    *string   `toml:"embedded_codex_reasoning_effort"`
	EmbeddedClaudeModel       *string   `toml:"embedded_claude_model"`
	EmbeddedClaudeReasoning   *string   `toml:"embedded_claude_reasoning_effort"`
	EmbeddedOpenCodeModel     *string   `toml:"embedded_opencode_model"`
	EmbeddedOpenCodeReasoning *string   `toml:"embedded_opencode_reasoning_effort"`
	EmbeddedLCAgentModel      *string   `toml:"embedded_lcagent_model"`
	EmbeddedLCAgentReasoning  *string   `toml:"embedded_lcagent_reasoning_effort"`
	OpenCodeModelTier         *string   `toml:"opencode_model_tier"`
	RecentCodexModels         *[]string `toml:"recent_codex_models"`
	RecentClaudeModels        *[]string `toml:"recent_claude_models"`
	RecentOpenCodeModels      *[]string `toml:"recent_opencode_models"`
	RecentLCAgentModels       *[]string `toml:"recent_lcagent_models"`
	LCAgentPath               *string   `toml:"lcagent_path"`
	LCAgentEnvFile            *string   `toml:"lcagent_env_file"`
	LCAgentRoutePreset        *string   `toml:"lcagent_route_preset"`
	LCAgentProvider           *string   `toml:"lcagent_provider"`
	LCAgentAuto               *string   `toml:"lcagent_auto"`
	LCAgentAdminWrite         *bool     `toml:"lcagent_admin_write"`
	LCAgentToolProfile        *string   `toml:"lcagent_tool_profile"`
	LCAgentContextProfile     *string   `toml:"lcagent_context_profile"`
	LCAgentRequestTimeout     *string   `toml:"lcagent_request_timeout"`
	LCAgentWebSearchBackend   *string   `toml:"lcagent_web_search_backend"`
	LCAgentWebSearchAPIKey    *string   `toml:"lcagent_web_search_api_key"`
	LCAgentWebSearchEngineID  *string   `toml:"lcagent_web_search_engine_id"`
	LCAgentWebSearchURL       *string   `toml:"lcagent_web_search_url"`
	CodexLaunchPreset         string    `toml:"codex_launch_preset"`
	PlaywrightManagementMode  *string   `toml:"playwright_management_mode"`
	PlaywrightDefaultBrowser  *string   `toml:"playwright_default_browser_mode"`
	PlaywrightLoginMode       *string   `toml:"playwright_login_mode"`
	PlaywrightIsolationScope  *string   `toml:"playwright_isolation_scope"`
	ScanInterval              string    `toml:"interval"`
	ActiveThreshold           string    `toml:"active-threshold"`
	StuckThreshold            string    `toml:"stuck-threshold"`
	HideReasoningSections     *bool     `toml:"hide_reasoning_sections"`
	PrivacyMode               *bool     `toml:"privacy_mode"`
}

func Default() AppConfig {
	home, _ := os.UserHomeDir()
	dataDir := filepath.Join(home, brand.DataDirName)
	return AppConfig{
		IncludePaths:            []string{filepath.Join(home, "dev", "repos")},
		ScratchRoot:             filepath.Join(home, "LittleControlRoom", "tasks"),
		CodexHome:               filepath.Join(home, ".codex"),
		OpenCodeHome:            filepath.Join(home, ".local", "share", "opencode"),
		ClaudeCodeHome:          filepath.Join(home, ".claude"),
		LCAgentProvider:         "openrouter",
		LCAgentAuto:             "low",
		LCAgentToolProfile:      "balanced",
		LCAgentContextProfile:   "balanced",
		LCAgentRequestTimeout:   10 * time.Minute,
		LCAgentWebSearchBackend: "off",
		CodexLaunchPreset:       codexcli.DefaultPreset(),
		PlaywrightPolicy:        browserctl.DefaultPolicy(),
		DataDir:                 dataDir,
		DBPath:                  filepath.Join(dataDir, brand.DBFileName),
		ConfigPath:              filepath.Join(dataDir, brand.ConfigFileName),
		SnapshotLimit:           3,
		ScanInterval:            60 * time.Second,
		ActiveThreshold:         20 * time.Minute,
		StuckThreshold:          4 * time.Hour,
		HideReasoningSections:   true,
		PrivacyMode:             false,
	}
}

func Parse(subcmd string, args []string) (AppConfig, error) {
	cfg := Default()
	configPathHint, err := extractPathFlagArg(args, "--config", cfg.ConfigPath)
	if err != nil {
		return AppConfig{}, err
	}
	cfg.ConfigPath, err = expandHome(configPathHint)
	if err != nil {
		return AppConfig{}, err
	}
	dbPathHint, err := extractPathFlagArg(args, "--db", cfg.DBPath)
	if err != nil {
		return AppConfig{}, err
	}
	cfg.DBPath, err = expandHome(dbPathHint)
	if err != nil {
		return AppConfig{}, err
	}
	if err := applyConfigFile(&cfg); err != nil {
		return AppConfig{}, err
	}

	fs := flag.NewFlagSet(subcmd, flag.ContinueOnError)
	configPath := fs.String("config", cfg.ConfigPath, fmt.Sprintf("Path to %s config TOML file", brand.Name))
	includePaths := fs.String("include-paths", strings.Join(cfg.IncludePaths, ","), "Comma-separated included project path prefixes")
	excludePaths := fs.String("exclude-paths", strings.Join(cfg.ExcludePaths, ","), "Comma-separated excluded project path prefixes")
	excludeProjectPatterns := fs.String("exclude-project-patterns", strings.Join(cfg.ExcludeProjectPatterns, ","), "Comma-separated project-name exclude patterns (supports '*' wildcard)")
	codexHome := fs.String("codex-home", cfg.CodexHome, "Path to Codex home directory")
	opencodeHome := fs.String("opencode-home", cfg.OpenCodeHome, "Path to OpenCode data directory")
	claudeCodeHome := fs.String("claude-code-home", cfg.ClaudeCodeHome, "Path to Claude Code home directory")
	lcagentPath := fs.String("lcagent-path", cfg.LCAgentPath, "Path to lcagent executable")
	lcagentEnvFile := fs.String("lcagent-env-file", cfg.LCAgentEnvFile, "Path to lcagent env file containing provider credentials")
	lcagentRoutePreset := fs.String("lcagent-route-preset", cfg.LCAgentRoutePreset, "LCAgent coding route preset: blank, balanced, quality, or cheap-scout")
	lcagentProvider := fs.String("lcagent-provider", cfg.LCAgentProvider, "LCAgent provider: openrouter, openai, deepseek, or moonshot")
	lcagentAuto := fs.String("lcagent-auto", cfg.LCAgentAuto, "LCAgent autonomy level: off, low, or medium")
	lcagentAdminWrite := fs.Bool("lcagent-admin-write", cfg.LCAgentAdminWrite, "Allow LCAgent write tools to edit absolute paths outside the workspace")
	lcagentToolProfile := fs.String("lcagent-tool-profile", cfg.LCAgentToolProfile, "LCAgent file tool budget profile: balanced or generous")
	lcagentContextProfile := fs.String("lcagent-context-profile", cfg.LCAgentContextProfile, "LCAgent provider loop context profile: balanced or large")
	lcagentRequestTimeout := fs.Duration("lcagent-request-timeout", cfg.LCAgentRequestTimeout, "LCAgent provider HTTP request timeout")
	lcagentWebSearchBackend := fs.String("lcagent-web-search-backend", cfg.LCAgentWebSearchBackend, "LCAgent web search backend: off, exa, google, or searxng")
	lcagentWebSearchAPIKey := fs.String("lcagent-web-search-api-key", cfg.LCAgentWebSearchAPIKey, "LCAgent web search API key for Exa or Google")
	lcagentWebSearchEngineID := fs.String("lcagent-web-search-engine-id", cfg.LCAgentWebSearchEngineID, "LCAgent Google Programmable Search engine ID")
	lcagentWebSearchURL := fs.String("lcagent-web-search-url", cfg.LCAgentWebSearchURL, "LCAgent web search endpoint URL, used by SearXNG")
	codexLaunchPreset := fs.String("codex-launch-preset", string(cfg.CodexLaunchPreset), "Codex launch preset: yolo, full-auto, or safe")
	dbPath := fs.String("db", cfg.DBPath, fmt.Sprintf("Path to %s SQLite database", brand.Name))
	scanInterval := fs.Duration("interval", cfg.ScanInterval, "Scan interval")
	active := fs.Duration("active-threshold", cfg.ActiveThreshold, "Active status threshold")
	stuck := fs.Duration("stuck-threshold", cfg.StuckThreshold, "Possibly stuck status threshold")
	allowMultipleInstances := fs.Bool("allow-multiple-instances", cfg.AllowMultipleInstances, "Allow multiple long-lived lcroom runtimes to share the same DB")
	var doctorScan *bool
	var snapshotLimit *int
	var snapshotProject *string
	var snapshotSessionID *string
	if subcmd == "doctor" {
		doctorScan = fs.Bool("scan", false, "Refresh state before printing the doctor report")
	}
	if subcmd == "snapshot" {
		snapshotLimit = fs.Int("limit", cfg.SnapshotLimit, "Maximum number of recent OpenCode snapshots to dump")
		snapshotProject = fs.String("project", cfg.SnapshotProject, "Only dump snapshots for this project path")
		snapshotSessionID = fs.String("session-id", cfg.SnapshotSessionID, "Only dump this session ID")
	}

	var sanitizeProject *string
	var sanitizeSessionID *string
	var sanitizeApply *bool
	var sanitizeDryRun *bool
	if subcmd == "sanitize-summaries" {
		sanitizeProject = fs.String("project", cfg.SanitizeProject, "Only sanitize summaries for this project path")
		sanitizeSessionID = fs.String("session-id", cfg.SanitizeSessionID, "Only sanitize this session ID")
		sanitizeApply = fs.Bool("apply", false, "Apply updated summaries instead of reporting")
		sanitizeDryRun = fs.Bool("dry-run", true, "Only report changes (default)")
	}

	if err := fs.Parse(args); err != nil {
		return AppConfig{}, err
	}

	cfg.ConfigPath, err = expandHome(*configPath)
	if err != nil {
		return AppConfig{}, err
	}

	expandedIncludePaths, err := expandAndSplitPaths(*includePaths)
	if err != nil {
		return AppConfig{}, err
	}
	cfg.IncludePaths = expandedIncludePaths
	expandedExcludePaths, err := expandAndSplitPaths(*excludePaths)
	if err != nil {
		return AppConfig{}, err
	}
	cfg.ExcludePaths = expandedExcludePaths
	cfg.ExcludeProjectPatterns = normalizeProjectPatterns(strings.Split(*excludeProjectPatterns, ","))
	cfg.CodexHome, err = expandHome(*codexHome)
	if err != nil {
		return AppConfig{}, err
	}
	cfg.OpenCodeHome, err = expandHome(*opencodeHome)
	if err != nil {
		return AppConfig{}, err
	}
	cfg.ClaudeCodeHome, err = expandHome(*claudeCodeHome)
	if err != nil {
		return AppConfig{}, err
	}
	cfg.LCAgentPath, err = expandHome(strings.TrimSpace(*lcagentPath))
	if err != nil {
		return AppConfig{}, err
	}
	cfg.LCAgentEnvFile, err = expandHome(strings.TrimSpace(*lcagentEnvFile))
	if err != nil {
		return AppConfig{}, err
	}
	cfg.LCAgentRoutePreset, err = parseLCAgentRoutePreset(*lcagentRoutePreset)
	if err != nil {
		return AppConfig{}, err
	}
	cfg.LCAgentProvider, err = parseLCAgentProvider(*lcagentProvider)
	if err != nil {
		return AppConfig{}, err
	}
	cfg.LCAgentAuto, err = parseLCAgentAuto(*lcagentAuto)
	if err != nil {
		return AppConfig{}, err
	}
	cfg.LCAgentAdminWrite = *lcagentAdminWrite
	cfg.LCAgentToolProfile, err = parseLCAgentToolProfile(*lcagentToolProfile)
	if err != nil {
		return AppConfig{}, err
	}
	cfg.LCAgentContextProfile, err = parseLCAgentContextProfile(*lcagentContextProfile)
	if err != nil {
		return AppConfig{}, err
	}
	cfg.LCAgentRequestTimeout = *lcagentRequestTimeout
	cfg.LCAgentWebSearchBackend, err = parseLCAgentWebSearchBackend(*lcagentWebSearchBackend)
	if err != nil {
		return AppConfig{}, err
	}
	cfg.LCAgentWebSearchAPIKey = strings.TrimSpace(*lcagentWebSearchAPIKey)
	cfg.LCAgentWebSearchEngineID = strings.TrimSpace(*lcagentWebSearchEngineID)
	cfg.LCAgentWebSearchURL = strings.TrimSpace(*lcagentWebSearchURL)
	cfg.CodexLaunchPreset, err = codexcli.ParsePreset(*codexLaunchPreset)
	if err != nil {
		return AppConfig{}, fmt.Errorf("codex-launch-preset: %w", err)
	}
	cfg.DBPath, err = expandHome(*dbPath)
	if err != nil {
		return AppConfig{}, err
	}
	cfg.DataDir = filepath.Dir(cfg.DBPath)
	cfg.ScanInterval = *scanInterval
	cfg.ActiveThreshold = *active
	cfg.StuckThreshold = *stuck
	cfg.AllowMultipleInstances = *allowMultipleInstances
	if doctorScan != nil {
		cfg.DoctorScan = *doctorScan
	}
	if snapshotLimit != nil {
		cfg.SnapshotLimit = *snapshotLimit
	}
	if snapshotProject != nil {
		cfg.SnapshotProject = strings.TrimSpace(*snapshotProject)
	}
	if snapshotSessionID != nil {
		cfg.SnapshotSessionID = strings.TrimSpace(*snapshotSessionID)
	}
	if sanitizeProject != nil {
		cfg.SanitizeProject = strings.TrimSpace(*sanitizeProject)
	}
	if sanitizeSessionID != nil {
		cfg.SanitizeSessionID = strings.TrimSpace(*sanitizeSessionID)
	}
	if sanitizeApply != nil {
		cfg.SanitizeApply = *sanitizeApply
	}
	if sanitizeDryRun != nil {
		cfg.SanitizeDryRun = *sanitizeDryRun
	}

	if err := validate(cfg); err != nil {
		return AppConfig{}, err
	}

	return cfg, nil
}

func expandAndSplitPaths(raw string) ([]string, error) {
	return normalizePaths(strings.Split(raw, ","))
}

func normalizePaths(parts []string) ([]string, error) {
	out := make([]string, 0, len(parts))
	seen := map[string]struct{}{}
	for _, p := range parts {
		p = strings.TrimSpace(p)
		if p == "" {
			continue
		}
		expanded, err := expandHome(p)
		if err != nil {
			return nil, err
		}
		clean := filepath.Clean(expanded)
		if _, ok := seen[clean]; ok {
			continue
		}
		seen[clean] = struct{}{}
		out = append(out, clean)
	}
	return out, nil
}

func extractPathFlagArg(args []string, flagName, fallback string) (string, error) {
	for i := 0; i < len(args); i++ {
		arg := args[i]
		prefix := flagName + "="
		if strings.HasPrefix(arg, prefix) {
			v := strings.TrimSpace(strings.TrimPrefix(arg, prefix))
			if v == "" {
				return "", fmt.Errorf("%s requires a value", flagName)
			}
			return v, nil
		}
		if arg == flagName {
			if i+1 >= len(args) {
				return "", fmt.Errorf("%s requires a value", flagName)
			}
			v := strings.TrimSpace(args[i+1])
			if v == "" {
				return "", fmt.Errorf("%s requires a value", flagName)
			}
			return v, nil
		}
	}
	return fallback, nil
}

func applyConfigFile(cfg *AppConfig) error {
	if cfg.ConfigPath == "" {
		return nil
	}
	raw, err := os.ReadFile(cfg.ConfigPath)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			cfg.ConfigLoaded = false
			return nil
		}
		return fmt.Errorf("read config file %s: %w", cfg.ConfigPath, err)
	}

	var fc fileConfig
	if err := toml.Unmarshal(raw, &fc); err != nil {
		return fmt.Errorf("parse config file %s: %w", cfg.ConfigPath, err)
	}
	if fc.IncludePaths != nil {
		includePaths, err := normalizePaths(*fc.IncludePaths)
		if err != nil {
			return fmt.Errorf("config include_paths: %w", err)
		}
		cfg.IncludePaths = includePaths
	}
	if strings.TrimSpace(fc.AIBackend) != "" {
		backend, err := ParseAIBackend(fc.AIBackend)
		if err != nil {
			return fmt.Errorf("config ai_backend: %w", err)
		}
		cfg.AIBackend = backend
	}
	if strings.TrimSpace(fc.BossChatBackend) != "" {
		backend, err := ParseBossChatBackend(fc.BossChatBackend)
		if err != nil {
			return fmt.Errorf("config boss_chat_backend: %w", err)
		}
		cfg.BossChatBackend = backend
	}
	applyOptionalTrimmedString(&cfg.BossChatModel, fc.BossChatModel)
	applyOptionalTrimmedString(&cfg.BossHelmModel, fc.BossHelmModel)
	applyOptionalTrimmedString(&cfg.BossUtilityModel, fc.BossUtilityModel)
	applyOptionalTrimmedString(&cfg.OpenAIAPIKey, fc.OpenAIAPIKey)
	applyOptionalTrimmedString(&cfg.OpenRouterAPIKey, fc.OpenRouterAPIKey)
	applyOptionalTrimmedString(&cfg.DeepSeekAPIKey, fc.DeepSeekAPIKey)
	applyOptionalTrimmedString(&cfg.MoonshotAPIKey, fc.MoonshotAPIKey)
	applyOptionalTrimmedString(&cfg.MLXBaseURL, fc.MLXBaseURL)
	applyOptionalTrimmedString(&cfg.MLXAPIKey, fc.MLXAPIKey)
	applyOptionalTrimmedString(&cfg.MLXModel, fc.MLXModel)
	applyOptionalTrimmedString(&cfg.OllamaBaseURL, fc.OllamaBaseURL)
	applyOptionalTrimmedString(&cfg.OllamaAPIKey, fc.OllamaAPIKey)
	applyOptionalTrimmedString(&cfg.OllamaModel, fc.OllamaModel)
	if fc.ExcludePaths != nil {
		excludePaths, err := normalizePaths(*fc.ExcludePaths)
		if err != nil {
			return fmt.Errorf("config exclude_paths: %w", err)
		}
		cfg.ExcludePaths = excludePaths
	}
	if fc.ExcludeProjectPatterns != nil {
		cfg.ExcludeProjectPatterns = normalizeProjectPatterns(*fc.ExcludeProjectPatterns)
	}
	if fc.PrivacyPatterns != nil {
		cfg.PrivacyPatterns = normalizeProjectPatterns(*fc.PrivacyPatterns)
	}
	applyOptionalTrimmedString(&cfg.EmbeddedCodexModel, fc.EmbeddedCodexModel)
	applyOptionalTrimmedString(&cfg.EmbeddedCodexReasoning, fc.EmbeddedCodexReasoning)
	applyOptionalTrimmedString(&cfg.EmbeddedClaudeModel, fc.EmbeddedClaudeModel)
	applyOptionalTrimmedString(&cfg.EmbeddedClaudeReasoning, fc.EmbeddedClaudeReasoning)
	applyOptionalTrimmedString(&cfg.EmbeddedOpenCodeModel, fc.EmbeddedOpenCodeModel)
	applyOptionalTrimmedString(&cfg.EmbeddedOpenCodeReasoning, fc.EmbeddedOpenCodeReasoning)
	applyOptionalTrimmedString(&cfg.EmbeddedLCAgentModel, fc.EmbeddedLCAgentModel)
	applyOptionalTrimmedString(&cfg.EmbeddedLCAgentReasoning, fc.EmbeddedLCAgentReasoning)
	applyOptionalTrimmedString(&cfg.OpenCodeModelTier, fc.OpenCodeModelTier)
	if fc.RecentCodexModels != nil {
		cfg.RecentCodexModels = trimStrings(*fc.RecentCodexModels)
	}
	if fc.RecentClaudeModels != nil {
		cfg.RecentClaudeModels = trimStrings(*fc.RecentClaudeModels)
	}
	if fc.RecentOpenCodeModels != nil {
		cfg.RecentOpenCodeModels = trimStrings(*fc.RecentOpenCodeModels)
	}
	if fc.RecentLCAgentModels != nil {
		cfg.RecentLCAgentModels = trimStrings(*fc.RecentLCAgentModels)
	}
	if fc.LCAgentPath != nil {
		value, err := expandHome(strings.TrimSpace(*fc.LCAgentPath))
		if err != nil {
			return fmt.Errorf("config lcagent_path: %w", err)
		}
		cfg.LCAgentPath = value
	}
	if fc.LCAgentEnvFile != nil {
		value, err := expandHome(strings.TrimSpace(*fc.LCAgentEnvFile))
		if err != nil {
			return fmt.Errorf("config lcagent_env_file: %w", err)
		}
		cfg.LCAgentEnvFile = value
	}
	if fc.LCAgentRoutePreset != nil {
		value, err := parseLCAgentRoutePreset(*fc.LCAgentRoutePreset)
		if err != nil {
			return fmt.Errorf("config lcagent_route_preset: %w", err)
		}
		cfg.LCAgentRoutePreset = value
	}
	if fc.LCAgentProvider != nil {
		value, err := parseLCAgentProvider(*fc.LCAgentProvider)
		if err != nil {
			return fmt.Errorf("config lcagent_provider: %w", err)
		}
		cfg.LCAgentProvider = value
	}
	if fc.LCAgentAuto != nil {
		value, err := parseLCAgentAuto(*fc.LCAgentAuto)
		if err != nil {
			return fmt.Errorf("config lcagent_auto: %w", err)
		}
		cfg.LCAgentAuto = value
	}
	if fc.LCAgentAdminWrite != nil {
		cfg.LCAgentAdminWrite = *fc.LCAgentAdminWrite
	}
	if fc.LCAgentToolProfile != nil {
		value, err := parseLCAgentToolProfile(*fc.LCAgentToolProfile)
		if err != nil {
			return fmt.Errorf("config lcagent_tool_profile: %w", err)
		}
		cfg.LCAgentToolProfile = value
	}
	if fc.LCAgentContextProfile != nil {
		value, err := parseLCAgentContextProfile(*fc.LCAgentContextProfile)
		if err != nil {
			return fmt.Errorf("config lcagent_context_profile: %w", err)
		}
		cfg.LCAgentContextProfile = value
	}
	if fc.LCAgentRequestTimeout != nil {
		value, err := parseConfigDuration(strings.TrimSpace(*fc.LCAgentRequestTimeout), "lcagent_request_timeout")
		if err != nil {
			return fmt.Errorf("config lcagent_request_timeout: %w", err)
		}
		cfg.LCAgentRequestTimeout = value
	}
	if fc.LCAgentWebSearchBackend != nil {
		value, err := parseLCAgentWebSearchBackend(*fc.LCAgentWebSearchBackend)
		if err != nil {
			return fmt.Errorf("config lcagent_web_search_backend: %w", err)
		}
		cfg.LCAgentWebSearchBackend = value
	}
	applyOptionalTrimmedString(&cfg.LCAgentWebSearchAPIKey, fc.LCAgentWebSearchAPIKey)
	applyOptionalTrimmedString(&cfg.LCAgentWebSearchEngineID, fc.LCAgentWebSearchEngineID)
	applyOptionalTrimmedString(&cfg.LCAgentWebSearchURL, fc.LCAgentWebSearchURL)
	if strings.TrimSpace(fc.CodexLaunchPreset) != "" {
		preset, err := codexcli.ParsePreset(fc.CodexLaunchPreset)
		if err != nil {
			return fmt.Errorf("config codex_launch_preset: %w", err)
		}
		cfg.CodexLaunchPreset = preset
	}
	if fc.PlaywrightManagementMode != nil {
		value, err := browserctl.ParseManagementMode(*fc.PlaywrightManagementMode)
		if err != nil {
			return fmt.Errorf("config playwright_management_mode: %w", err)
		}
		cfg.PlaywrightPolicy.ManagementMode = value
	}
	if fc.PlaywrightDefaultBrowser != nil {
		value, err := browserctl.ParseBrowserMode(*fc.PlaywrightDefaultBrowser)
		if err != nil {
			return fmt.Errorf("config playwright_default_browser_mode: %w", err)
		}
		cfg.PlaywrightPolicy.DefaultBrowserMode = value
	}
	if fc.PlaywrightLoginMode != nil {
		value, err := browserctl.ParseLoginMode(*fc.PlaywrightLoginMode)
		if err != nil {
			return fmt.Errorf("config playwright_login_mode: %w", err)
		}
		cfg.PlaywrightPolicy.LoginMode = value
	}
	if fc.PlaywrightIsolationScope != nil {
		value, err := browserctl.ParseIsolationScope(*fc.PlaywrightIsolationScope)
		if err != nil {
			return fmt.Errorf("config playwright_isolation_scope: %w", err)
		}
		cfg.PlaywrightPolicy.IsolationScope = value
	}
	if err := applyOptionalConfigDuration(&cfg.ScanInterval, fc.ScanInterval, "interval"); err != nil {
		return err
	}
	if err := applyOptionalConfigDuration(&cfg.ActiveThreshold, fc.ActiveThreshold, "active-threshold"); err != nil {
		return err
	}
	if err := applyOptionalConfigDuration(&cfg.StuckThreshold, fc.StuckThreshold, "stuck-threshold"); err != nil {
		return err
	}
	if fc.HideReasoningSections != nil {
		cfg.HideReasoningSections = *fc.HideReasoningSections
	}
	if fc.PrivacyMode != nil {
		cfg.PrivacyMode = *fc.PrivacyMode
	}
	cfg.ConfigLoaded = true
	return nil
}

func validate(cfg AppConfig) error {
	if cfg.ActiveThreshold <= 0 {
		return errors.New("active-threshold must be > 0")
	}
	if cfg.StuckThreshold <= cfg.ActiveThreshold {
		return errors.New("stuck-threshold must be greater than active-threshold")
	}
	if cfg.SnapshotLimit <= 0 {
		return errors.New("snapshot limit must be > 0")
	}
	if _, err := codexcli.ParsePreset(string(cfg.CodexLaunchPreset)); err != nil {
		return err
	}
	if err := cfg.PlaywrightPolicy.Validate(); err != nil {
		return err
	}
	if _, err := ParseAIBackend(string(cfg.AIBackend)); err != nil {
		return err
	}
	if _, err := ParseBossChatBackend(string(cfg.BossChatBackend)); err != nil {
		return err
	}
	if _, err := parseLCAgentProvider(cfg.LCAgentProvider); err != nil {
		return err
	}
	if _, err := parseLCAgentRoutePreset(cfg.LCAgentRoutePreset); err != nil {
		return err
	}
	if _, err := parseLCAgentAuto(cfg.LCAgentAuto); err != nil {
		return err
	}
	if _, err := parseLCAgentToolProfile(cfg.LCAgentToolProfile); err != nil {
		return err
	}
	if _, err := parseLCAgentContextProfile(cfg.LCAgentContextProfile); err != nil {
		return err
	}
	if cfg.LCAgentRequestTimeout <= 0 {
		return errors.New("lcagent-request-timeout must be > 0")
	}
	if _, err := parseLCAgentWebSearchBackend(cfg.LCAgentWebSearchBackend); err != nil {
		return err
	}
	return nil
}

func parseLCAgentRoutePreset(raw string) (string, error) {
	value := strings.ToLower(strings.TrimSpace(raw))
	value = strings.ReplaceAll(value, "_", "-")
	switch value {
	case "":
		return "", nil
	case "scout", "cheap", "cheapscout":
		return "cheap-scout", nil
	case "balanced", "quality", "cheap-scout":
		return value, nil
	default:
		return "", fmt.Errorf("lcagent-route-preset must be blank or one of: balanced, quality, cheap-scout")
	}
}

func parseLCAgentProvider(raw string) (string, error) {
	value := strings.ToLower(strings.TrimSpace(raw))
	if value == "" {
		return "openrouter", nil
	}
	switch value {
	case "openrouter", "openai", "deepseek", "moonshot":
		return value, nil
	default:
		return "", fmt.Errorf("lcagent-provider must be one of: openrouter, openai, deepseek, moonshot")
	}
}

func parseLCAgentAuto(raw string) (string, error) {
	value := strings.ToLower(strings.TrimSpace(raw))
	if value == "" {
		return "low", nil
	}
	switch value {
	case "off", "low", "medium":
		return value, nil
	default:
		return "", fmt.Errorf("lcagent-auto must be one of: off, low, medium")
	}
}

func parseLCAgentToolProfile(raw string) (string, error) {
	value := strings.ToLower(strings.TrimSpace(raw))
	if value == "" {
		return "balanced", nil
	}
	switch value {
	case "balanced", "generous":
		return value, nil
	default:
		return "", fmt.Errorf("lcagent-tool-profile must be one of: balanced, generous")
	}
}

func parseLCAgentContextProfile(raw string) (string, error) {
	value := strings.ToLower(strings.TrimSpace(raw))
	if value == "" {
		return "balanced", nil
	}
	switch value {
	case "balanced", "large":
		return value, nil
	default:
		return "", fmt.Errorf("lcagent-context-profile must be one of: balanced, large")
	}
}

func parseLCAgentWebSearchBackend(raw string) (string, error) {
	value := strings.ToLower(strings.TrimSpace(raw))
	if value == "" {
		return "off", nil
	}
	switch value {
	case "off", "exa", "google", "searxng":
		return value, nil
	default:
		return "", fmt.Errorf("lcagent-web-search-backend must be one of: off, exa, google, searxng")
	}
}

func expandHome(path string) (string, error) {
	if path == "" {
		return path, nil
	}
	if path == "~" || strings.HasPrefix(path, "~/") {
		home, err := os.UserHomeDir()
		if err != nil {
			return "", fmt.Errorf("resolve home directory: %w", err)
		}
		if path == "~" {
			return home, nil
		}
		return filepath.Join(home, strings.TrimPrefix(path, "~/")), nil
	}
	return path, nil
}

func applyOptionalTrimmedString(dst, src *string) {
	if dst == nil || src == nil {
		return
	}
	*dst = strings.TrimSpace(*src)
}

func applyOptionalConfigDuration(dst *time.Duration, raw, label string) error {
	if dst == nil {
		return nil
	}
	trimmed := strings.TrimSpace(raw)
	if trimmed == "" {
		return nil
	}
	d, err := parseConfigDuration(trimmed, label)
	if err != nil {
		return fmt.Errorf("config %w", err)
	}
	*dst = d
	return nil
}

func trimStrings(items []string) []string {
	out := make([]string, 0, len(items))
	for _, item := range items {
		if trimmed := strings.TrimSpace(item); trimmed != "" {
			out = append(out, trimmed)
		}
	}
	return out
}

func trimmedOrDefault(value, fallback string) string {
	if trimmed := strings.TrimSpace(value); trimmed != "" {
		return trimmed
	}
	return strings.TrimSpace(fallback)
}
