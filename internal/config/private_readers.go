package config

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"slices"

	"github.com/pelletier/go-toml/v2"
)

// EmbeddedConfigPathEnv carries the host config path to the LCAgent subprocess.
const EmbeddedConfigPathEnv = "LCROOM_EMBEDDED_CONFIG_PATH"

type privateReaderSettings struct {
	Paths       []string `toml:"private_readers"`
	PrivacyMode bool     `toml:"privacy_mode"`
}

func readPrivateReaderSettings(path string) (privateReaderSettings, error) {
	raw, err := os.ReadFile(path)
	if errors.Is(err, os.ErrNotExist) {
		return privateReaderSettings{}, nil
	}
	if err != nil {
		return privateReaderSettings{}, fmt.Errorf("read config file %s: %w", path, err)
	}
	var settings privateReaderSettings
	if err := toml.Unmarshal(raw, &settings); err != nil {
		return privateReaderSettings{}, fmt.Errorf("parse config file %s: %w", path, err)
	}
	return settings, nil
}

// AllowsPrivateReads checks the current config for an exact project path.
func AllowsPrivateReads(configPath, projectPath string) bool {
	if configPath == "" || !filepath.IsAbs(projectPath) {
		return false
	}
	settings, err := readPrivateReaderSettings(configPath)
	if err != nil || settings.PrivacyMode {
		return false
	}
	paths, err := normalizePaths(settings.Paths)
	return err == nil && slices.Contains(paths, filepath.Clean(projectPath))
}
