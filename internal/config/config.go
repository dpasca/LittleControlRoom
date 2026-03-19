package config

import (
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"time"

	"lcroom/internal/brand"
	"lcroom/internal/codexcli"

	toml "github.com/pelletier/go-toml/v2"
)

type AppConfig struct {
	AIBackend              AIBackend
	OpenAIAPIKey           string
	IncludePaths           []string
	ExcludePaths           []string
	ExcludeProjectPatterns []string
	CodexHome              string
	OpenCodeHome           string
	CodexLaunchPreset      codexcli.Preset
	DataDir                string
	DBPath                 string
	ConfigPath             string
	ConfigLoaded           bool
	DoctorScan             bool
	SnapshotLimit          int
	SnapshotProject        string
	SnapshotSessionID      string
	ScanInterval           time.Duration
	ActiveThreshold        time.Duration
	StuckThreshold         time.Duration
	AllowMultipleInstances bool
}

func (c AppConfig) EffectiveAIBackend() AIBackend {
	return ResolveAIBackend(c.AIBackend, c.OpenAIAPIKey)
}

type fileConfig struct {
	AIBackend              string    `toml:"ai_backend"`
	OpenAIAPIKey           *string   `toml:"openai_api_key"`
	IncludePaths           *[]string `toml:"include_paths"`
	ExcludePaths           *[]string `toml:"exclude_paths"`
	ExcludeProjectPatterns *[]string `toml:"exclude_project_patterns"`
	CodexLaunchPreset      string    `toml:"codex_launch_preset"`
	ScanInterval           string    `toml:"interval"`
	ActiveThreshold        string    `toml:"active-threshold"`
	StuckThreshold         string    `toml:"stuck-threshold"`
}

func Default() AppConfig {
	home, _ := os.UserHomeDir()
	dataDir := filepath.Join(home, brand.DataDirName)
	return AppConfig{
		IncludePaths:      []string{filepath.Join(home, "dev", "repos")},
		CodexHome:         filepath.Join(home, ".codex"),
		OpenCodeHome:      filepath.Join(home, ".local", "share", "opencode"),
		CodexLaunchPreset: codexcli.DefaultPreset(),
		DataDir:           dataDir,
		DBPath:            filepath.Join(dataDir, brand.DBFileName),
		ConfigPath:        filepath.Join(dataDir, brand.ConfigFileName),
		SnapshotLimit:     3,
		ScanInterval:      60 * time.Second,
		ActiveThreshold:   20 * time.Minute,
		StuckThreshold:    4 * time.Hour,
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
	if shouldMigrateLegacyState(cfg.ConfigPath, cfg.DBPath) {
		if err := migrateLegacyState(&cfg); err != nil {
			return AppConfig{}, err
		}
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
	if fc.OpenAIAPIKey != nil {
		cfg.OpenAIAPIKey = strings.TrimSpace(*fc.OpenAIAPIKey)
	}
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
	if strings.TrimSpace(fc.CodexLaunchPreset) != "" {
		preset, err := codexcli.ParsePreset(fc.CodexLaunchPreset)
		if err != nil {
			return fmt.Errorf("config codex_launch_preset: %w", err)
		}
		cfg.CodexLaunchPreset = preset
	}
	if strings.TrimSpace(fc.ScanInterval) != "" {
		d, err := time.ParseDuration(strings.TrimSpace(fc.ScanInterval))
		if err != nil {
			return fmt.Errorf("config interval: %w", err)
		}
		cfg.ScanInterval = d
	}
	if strings.TrimSpace(fc.ActiveThreshold) != "" {
		d, err := time.ParseDuration(strings.TrimSpace(fc.ActiveThreshold))
		if err != nil {
			return fmt.Errorf("config active-threshold: %w", err)
		}
		cfg.ActiveThreshold = d
	}
	if strings.TrimSpace(fc.StuckThreshold) != "" {
		d, err := time.ParseDuration(strings.TrimSpace(fc.StuckThreshold))
		if err != nil {
			return fmt.Errorf("config stuck-threshold: %w", err)
		}
		cfg.StuckThreshold = d
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
	if _, err := ParseAIBackend(string(cfg.AIBackend)); err != nil {
		return err
	}
	return nil
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

func pathExists(path string) bool {
	if path == "" {
		return false
	}
	_, err := os.Stat(path)
	return err == nil
}

func shouldMigrateLegacyState(configPath, dbPath string) bool {
	home, err := os.UserHomeDir()
	if err != nil {
		return true
	}
	legacyDir := filepath.Join(home, brand.LegacyDataDirName)
	return !pathWithinDir(configPath, legacyDir) && !pathWithinDir(dbPath, legacyDir)
}

func pathWithinDir(path, dir string) bool {
	cleanPath := filepath.Clean(path)
	cleanDir := filepath.Clean(dir)
	if cleanPath == cleanDir {
		return true
	}
	return strings.HasPrefix(cleanPath, cleanDir+string(os.PathSeparator))
}

func migrateLegacyState(cfg *AppConfig) error {
	if cfg == nil || cfg.DataDir == "" {
		return nil
	}

	home, err := os.UserHomeDir()
	if err != nil {
		return fmt.Errorf("resolve home directory: %w", err)
	}

	legacyDir := filepath.Join(home, brand.LegacyDataDirName)
	if !pathExists(legacyDir) {
		return nil
	}
	if err := os.MkdirAll(cfg.DataDir, 0o755); err != nil {
		return fmt.Errorf("create %s data directory: %w", brand.ShortName, err)
	}

	legacyDBBase := filepath.Join(legacyDir, brand.LegacyDBFileName)
	preferredDBBase := filepath.Join(cfg.DataDir, brand.DBFileName)
	for _, suffix := range []string{"", "-wal", "-shm"} {
		if err := moveFileIfMissing(legacyDBBase+suffix, preferredDBBase+suffix); err != nil {
			return fmt.Errorf("migrate sqlite state%s: %w", suffix, err)
		}
	}

	return nil
}

func moveFileIfMissing(src, dst string) error {
	if !pathExists(src) || pathExists(dst) {
		return nil
	}
	if err := os.Rename(src, dst); err == nil {
		return nil
	}

	in, err := os.Open(src)
	if err != nil {
		return fmt.Errorf("open source %s: %w", src, err)
	}
	defer in.Close()

	info, err := in.Stat()
	if err != nil {
		return fmt.Errorf("stat source %s: %w", src, err)
	}

	tmp, err := os.CreateTemp(filepath.Dir(dst), filepath.Base(dst)+".tmp-*")
	if err != nil {
		return fmt.Errorf("create temp file for %s: %w", dst, err)
	}
	tmpPath := tmp.Name()
	defer func() {
		_ = tmp.Close()
		_ = os.Remove(tmpPath)
	}()

	if _, err := io.Copy(tmp, in); err != nil {
		return fmt.Errorf("copy %s to %s: %w", src, dst, err)
	}
	if err := tmp.Chmod(info.Mode()); err != nil {
		return fmt.Errorf("chmod temp file for %s: %w", dst, err)
	}
	if err := tmp.Close(); err != nil {
		return fmt.Errorf("close temp file for %s: %w", dst, err)
	}
	if err := os.Rename(tmpPath, dst); err != nil {
		return fmt.Errorf("install migrated file %s: %w", dst, err)
	}
	if err := os.Remove(src); err != nil && !errors.Is(err, os.ErrNotExist) {
		return fmt.Errorf("remove legacy file %s: %w", src, err)
	}
	return nil
}
