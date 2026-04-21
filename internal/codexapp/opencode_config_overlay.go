package codexapp

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"lcroom/internal/appfs"
)

func prepareOpenCodeConfigOverlay(dataDir, requestedConfigRoot string) (string, error) {
	sourceRoot, err := effectiveOpenCodeConfigRoot(requestedConfigRoot)
	if err != nil {
		return "", err
	}

	overlayXDGRoot, err := appfs.CreateInternalWorkspace(dataDir, "lcroom-opencode-config-*")
	if err != nil {
		return "", fmt.Errorf("create opencode config overlay: %w", err)
	}
	overlayConfigRoot := filepath.Join(overlayXDGRoot, "opencode")
	if err := populateOpenCodeConfigOverlay(overlayConfigRoot, sourceRoot); err != nil {
		_ = os.RemoveAll(overlayXDGRoot)
		return "", err
	}
	return overlayXDGRoot, nil
}

func populateOpenCodeConfigOverlay(overlayConfigRoot, sourceConfigRoot string) error {
	if err := os.MkdirAll(overlayConfigRoot, 0o700); err != nil {
		return fmt.Errorf("mkdir opencode overlay root: %w", err)
	}
	if err := mirrorOpenCodeConfigEntries(overlayConfigRoot, sourceConfigRoot); err != nil {
		return err
	}
	if err := installShadowPlaywrightSkill(overlayConfigRoot, sourceConfigRoot); err != nil {
		return err
	}
	return nil
}

func mirrorOpenCodeConfigEntries(overlayConfigRoot, sourceConfigRoot string) error {
	info, err := os.Stat(sourceConfigRoot)
	if err != nil {
		if os.IsNotExist(err) {
			return nil
		}
		return fmt.Errorf("stat opencode config root: %w", err)
	}
	if !info.IsDir() {
		return fmt.Errorf("opencode config root is not a directory: %s", sourceConfigRoot)
	}

	entries, err := os.ReadDir(sourceConfigRoot)
	if err != nil {
		return fmt.Errorf("read opencode config root: %w", err)
	}
	for _, entry := range entries {
		name := strings.TrimSpace(entry.Name())
		if name == "" || name == "skills" {
			continue
		}
		sourcePath := filepath.Join(sourceConfigRoot, name)
		targetPath := filepath.Join(overlayConfigRoot, name)
		if err := os.Symlink(sourcePath, targetPath); err != nil {
			return fmt.Errorf("symlink %s: %w", name, err)
		}
	}
	return nil
}

func effectiveOpenCodeConfigRoot(requestedConfigRoot string) (string, error) {
	if trimmed := strings.TrimSpace(requestedConfigRoot); trimmed != "" {
		return filepath.Clean(trimmed), nil
	}
	if xdgConfigHome := strings.TrimSpace(os.Getenv("XDG_CONFIG_HOME")); xdgConfigHome != "" {
		return filepath.Join(filepath.Clean(xdgConfigHome), "opencode"), nil
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return "", fmt.Errorf("resolve user home: %w", err)
	}
	return filepath.Join(home, ".config", "opencode"), nil
}
