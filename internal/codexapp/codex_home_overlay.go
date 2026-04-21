package codexapp

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"lcroom/internal/appfs"
	"lcroom/internal/browserctl"
	"lcroom/internal/codexstate"
)

const shadowPlaywrightSkillMarkdown = `---
name: "playwright"
description: "Use the embedded Playwright MCP tools already wired through Little Control Room. Do not launch a standalone Playwright CLI/browser from this embedded session."
---

# Embedded Playwright Skill

This embedded Codex session already has a ` + "`playwright`" + ` MCP server registered through Little Control Room.
Use the Playwright MCP tools directly.

Do not shell out to ` + "`npx @playwright/mcp`" + `, ` + "`playwright-mcp`" + `, ` + "`playwright_cli.sh`" + `, or any standalone Playwright browser launcher from the terminal unless the user explicitly asks to debug that wrapper itself.

## When the user needs to interact with the page

- Ask Little Control Room to show or reveal the managed browser window for this session.
- Do not open the same URL in a separate desktop browser for login or MFA; that creates a disconnected browser context.

## Guardrails

- Prefer the existing ` + "`playwright/...`" + ` tools over shell commands.
- Treat terminal Playwright CLI commands as a last resort for debugging the wrapper itself.
- If a separate CLI launch would create a second browser context, stop and explain the limitation instead.
`

const shadowPlaywrightWrapperScript = `#!/usr/bin/env bash
set -euo pipefail

echo "This embedded Little Control Room session already has a managed Playwright browser." >&2
echo "Use the embedded Playwright MCP tools or reveal the managed browser window instead of launching a standalone CLI browser." >&2
exit 2
`

func prepareCodexHomeOverlay(dataDir, requestedHome string) (string, error) {
	sourceHome, err := effectiveCodexHome(requestedHome)
	if err != nil {
		return "", err
	}

	overlayRoot, err := appfs.CreateInternalWorkspace(dataDir, "lcroom-codex-home-*")
	if err != nil {
		return "", fmt.Errorf("create codex home overlay: %w", err)
	}
	if err := populateCodexHomeOverlay(overlayRoot, sourceHome); err != nil {
		_ = os.RemoveAll(overlayRoot)
		return "", err
	}
	return overlayRoot, nil
}

func populateCodexHomeOverlay(overlayRoot, sourceHome string) error {
	if err := os.MkdirAll(overlayRoot, 0o700); err != nil {
		return fmt.Errorf("mkdir codex overlay root: %w", err)
	}
	if err := mirrorCodexHomeEntries(overlayRoot, sourceHome); err != nil {
		return err
	}
	if err := installShadowPlaywrightSkill(overlayRoot, sourceHome); err != nil {
		return err
	}
	return nil
}

func mirrorCodexHomeEntries(overlayRoot, sourceHome string) error {
	info, err := os.Stat(sourceHome)
	if err != nil {
		if os.IsNotExist(err) {
			return nil
		}
		return fmt.Errorf("stat codex home: %w", err)
	}
	if !info.IsDir() {
		return fmt.Errorf("codex home is not a directory: %s", sourceHome)
	}

	entries, err := os.ReadDir(sourceHome)
	if err != nil {
		return fmt.Errorf("read codex home: %w", err)
	}
	for _, entry := range entries {
		name := strings.TrimSpace(entry.Name())
		if name == "" || name == "skills" {
			continue
		}
		sourcePath := filepath.Join(sourceHome, name)
		targetPath := filepath.Join(overlayRoot, name)
		if err := os.Symlink(sourcePath, targetPath); err != nil {
			return fmt.Errorf("symlink %s: %w", name, err)
		}
	}
	return nil
}

func installShadowPlaywrightSkill(overlayRoot, sourceHome string) error {
	overlaySkillsDir := filepath.Join(overlayRoot, "skills")
	if err := os.MkdirAll(overlaySkillsDir, 0o700); err != nil {
		return fmt.Errorf("mkdir overlay skills dir: %w", err)
	}

	sourceSkillsDir := filepath.Join(sourceHome, "skills")
	sourceEntries, err := os.ReadDir(sourceSkillsDir)
	if err != nil && !os.IsNotExist(err) {
		return fmt.Errorf("read source skills dir: %w", err)
	}
	for _, entry := range sourceEntries {
		name := strings.TrimSpace(entry.Name())
		if name == "" || name == "playwright" {
			continue
		}
		sourcePath := filepath.Join(sourceSkillsDir, name)
		targetPath := filepath.Join(overlaySkillsDir, name)
		if err := os.Symlink(sourcePath, targetPath); err != nil {
			return fmt.Errorf("symlink skill %s: %w", name, err)
		}
	}

	overlayPlaywrightDir := filepath.Join(overlaySkillsDir, "playwright")
	overlayScriptsDir := filepath.Join(overlayPlaywrightDir, "scripts")
	if err := os.MkdirAll(overlayScriptsDir, 0o700); err != nil {
		return fmt.Errorf("mkdir overlay playwright scripts dir: %w", err)
	}
	if err := os.WriteFile(filepath.Join(overlayPlaywrightDir, "SKILL.md"), []byte(shadowPlaywrightSkillMarkdown), 0o644); err != nil {
		return fmt.Errorf("write overlay Playwright SKILL.md: %w", err)
	}
	if err := os.WriteFile(filepath.Join(overlayScriptsDir, "playwright_cli.sh"), []byte(shadowPlaywrightWrapperScript), 0o755); err != nil {
		return fmt.Errorf("write overlay Playwright wrapper: %w", err)
	}
	return nil
}

func effectiveCodexHome(requestedHome string) (string, error) {
	if trimmed := strings.TrimSpace(requestedHome); trimmed != "" {
		return codexstate.ResolveHomeRoot(trimmed), nil
	}
	if envHome := strings.TrimSpace(os.Getenv("CODEX_HOME")); envHome != "" {
		return codexstate.ResolveHomeRoot(envHome), nil
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return "", fmt.Errorf("resolve user home: %w", err)
	}
	return codexstate.ResolveHomeRoot(filepath.Join(home, ".codex")), nil
}

func shouldShadowPlaywrightSkill(policy browserctl.Policy) bool {
	return !policy.Normalize().UsesLegacyLaunchBehavior()
}

func withEnvOverride(base []string, key, value string) []string {
	prefix := key + "="
	out := make([]string, 0, len(base)+1)
	for _, entry := range base {
		if strings.HasPrefix(entry, prefix) {
			continue
		}
		out = append(out, entry)
	}
	out = append(out, key+"="+value)
	return out
}
