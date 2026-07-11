package codexapp

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
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

This embedded Little Control Room session already has a ` + "`playwright`" + ` MCP server registered through Little Control Room.
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

const shadowRuntimeSkillMarkdown = `---
name: "runtime"
description: "Use Little Control Room runtime MCP tools for local dev servers, watchers, and project-local port checks. Do not launch duplicate long-running server processes from the shell."
---

# Embedded Runtime Skill

This embedded Little Control Room session has an ` + "`lcr_runtime`" + ` MCP server registered.
Use its runtime tools for local app/server/watch processes:

- Call ` + "`list_processes`" + ` before starting a local server or watcher when a matching process may already be active.
- Call ` + "`start_process`" + ` for long-running foreground server/watch commands that should stay inspectable after the tool returns.
- Leave ` + "`create_new`" + ` false for ordinary launch and verification work.
- Set ` + "`create_new`" + ` true only when the user needs another concurrent copy of the same command/cwd.
- Set ` + "`replace_existing`" + ` true only when a fresh managed instance is needed.
- Call ` + "`stop_process`" + ` only when the user asks to stop a managed runtime or when cleaning up a temporary process you started.

Do not use shell backgrounding, ad-hoc port hopping, or a bounded terminal command for dev servers/watchers when the runtime MCP tools are available.
`

const codexDirectRMRuleFilename = "lcroom-no-direct-rm.rules"

const codexDirectRMExecPolicy = `# Managed by Little Control Room.
# Keep broad agent filesystem access available while denying the direct rm
# executable. More targeted editing and deletion tools remain available.
prefix_rule(
    pattern = [["rm", "/bin/rm", "/usr/bin/rm"]],
    decision = "forbidden",
    justification = "Little Control Room disables direct rm commands in embedded agent sessions. Use targeted file or patch tools, or ask the user to run intentional cleanup manually.",
    match = ["rm file.txt", "rm -rf /Users/example", "/bin/rm -fr build"],
    not_match = ["rmdir empty-dir", "echo rm"],
)

prefix_rule(
    pattern = [["command", "builtin", "exec", "nohup", "sudo", "env", "/usr/bin/env"], ["rm", "/bin/rm", "/usr/bin/rm"]],
    decision = "forbidden",
    justification = "Little Control Room disables direct rm commands in embedded agent sessions. Use targeted file or patch tools, or ask the user to run intentional cleanup manually.",
    match = ["command rm file.txt", "sudo /bin/rm -rf build", "env rm file.txt"],
    not_match = ["command echo rm", "sudo echo rm", "env MODE=rm echo ok"],
)
`

const codexDirectRMShim = `#!/bin/sh
recursive=0
force=0
scan_options=1
for arg
do
    if [ "$scan_options" -eq 0 ]; then
        continue
    fi
    case "$arg" in
        --)
            scan_options=0
            ;;
        --recursive)
            recursive=1
            ;;
        --force)
            force=1
            ;;
        --*)
            ;;
        -?*)
            flags=${arg#-}
            case "$flags" in *r*|*R*) recursive=1 ;; esac
            case "$flags" in *f*) force=1 ;; esac
            ;;
    esac
done

if [ "$recursive" -eq 1 ] && [ "$force" -eq 1 ]; then
    printf '%s\n' 'Blocked by Little Control Room: recursive forced rm is disabled in embedded agent sessions.' >&2
    printf '%s\n' 'Use targeted file or patch tools, or ask the user to run intentional cleanup manually.' >&2
    exit 126
fi

if [ -x /bin/rm ]; then
    exec /bin/rm "$@"
fi
if [ -x /usr/bin/rm ]; then
    exec /usr/bin/rm "$@"
fi
printf '%s\n' 'Little Control Room could not find the system rm executable.' >&2
exit 127
`

func prepareCodexHomeOverlay(dataDir, requestedHome string) (string, error) {
	return prepareCodexHomeOverlayWithOptions(dataDir, requestedHome, true)
}

func prepareCodexHomeOverlayWithOptions(dataDir, requestedHome string, shadowSkills bool) (string, error) {
	sourceHome, err := effectiveCodexHome(requestedHome)
	if err != nil {
		return "", err
	}

	overlayRoot, err := appfs.CreateInternalWorkspace(dataDir, "lcroom-codex-home-*")
	if err != nil {
		return "", fmt.Errorf("create codex home overlay: %w", err)
	}
	if err := populateCodexHomeOverlay(overlayRoot, sourceHome, shadowSkills); err != nil {
		_ = os.RemoveAll(overlayRoot)
		return "", err
	}
	return overlayRoot, nil
}

func populateCodexHomeOverlay(overlayRoot, sourceHome string, shadowSkills bool) error {
	if err := os.MkdirAll(overlayRoot, 0o700); err != nil {
		return fmt.Errorf("mkdir codex overlay root: %w", err)
	}
	if err := mirrorCodexHomeEntries(overlayRoot, sourceHome, shadowSkills); err != nil {
		return err
	}
	if shadowSkills {
		if err := installShadowPlaywrightSkill(overlayRoot, sourceHome); err != nil {
			return err
		}
	}
	if err := installCodexDirectRMGuard(overlayRoot, sourceHome); err != nil {
		return err
	}
	return nil
}

func mirrorCodexHomeEntries(overlayRoot, sourceHome string, shadowSkills bool) error {
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
		if name == "" || name == "bin" || name == "rules" || (shadowSkills && name == "skills") {
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

func installCodexDirectRMGuard(overlayRoot, sourceHome string) error {
	overlayRulesDir := filepath.Join(overlayRoot, "rules")
	if err := os.MkdirAll(overlayRulesDir, 0o700); err != nil {
		return fmt.Errorf("mkdir overlay rules dir: %w", err)
	}

	sourceRulesDir := filepath.Join(sourceHome, "rules")
	sourceEntries, err := os.ReadDir(sourceRulesDir)
	if err != nil && !os.IsNotExist(err) {
		return fmt.Errorf("read source rules dir: %w", err)
	}
	for _, entry := range sourceEntries {
		name := strings.TrimSpace(entry.Name())
		if name == "" || name == codexDirectRMRuleFilename {
			continue
		}
		if err := os.Symlink(filepath.Join(sourceRulesDir, name), filepath.Join(overlayRulesDir, name)); err != nil {
			return fmt.Errorf("symlink rule %s: %w", name, err)
		}
	}
	if err := os.WriteFile(filepath.Join(overlayRulesDir, codexDirectRMRuleFilename), []byte(codexDirectRMExecPolicy), 0o600); err != nil {
		return fmt.Errorf("write direct rm execpolicy: %w", err)
	}

	overlayBinDir := filepath.Join(overlayRoot, "bin")
	if err := os.MkdirAll(overlayBinDir, 0o700); err != nil {
		return fmt.Errorf("mkdir overlay bin dir: %w", err)
	}
	sourceBinDir := filepath.Join(sourceHome, "bin")
	sourceBinEntries, err := os.ReadDir(sourceBinDir)
	if err != nil && !os.IsNotExist(err) {
		return fmt.Errorf("read source bin dir: %w", err)
	}
	for _, entry := range sourceBinEntries {
		name := strings.TrimSpace(entry.Name())
		if name == "" || name == "rm" {
			continue
		}
		if err := os.Symlink(filepath.Join(sourceBinDir, name), filepath.Join(overlayBinDir, name)); err != nil {
			return fmt.Errorf("symlink bin entry %s: %w", name, err)
		}
	}
	if err := os.WriteFile(filepath.Join(overlayBinDir, "rm"), []byte(codexDirectRMShim), 0o755); err != nil {
		return fmt.Errorf("write direct rm shim: %w", err)
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
		if name == "" || name == "playwright" || name == "runtime" {
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
	overlayRuntimeDir := filepath.Join(overlaySkillsDir, "runtime")
	if err := os.MkdirAll(overlayRuntimeDir, 0o700); err != nil {
		return fmt.Errorf("mkdir overlay runtime skill dir: %w", err)
	}
	if err := os.WriteFile(filepath.Join(overlayRuntimeDir, "SKILL.md"), []byte(shadowRuntimeSkillMarkdown), 0o644); err != nil {
		return fmt.Errorf("write overlay runtime SKILL.md: %w", err)
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

func shouldShadowRuntimeSkill(req LaunchRequest) bool {
	_, _, ok := runtimeMCPCommand(req)
	return ok
}

func shouldPrepareEmbeddedSkillOverlay(req LaunchRequest) bool {
	return shouldShadowPlaywrightSkill(req.PlaywrightPolicy) || shouldShadowRuntimeSkill(req)
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

func withPathPrefix(base []string, pathPrefix string) []string {
	pathPrefix = strings.TrimSpace(pathPrefix)
	if pathPrefix == "" {
		return base
	}
	pathValue := os.Getenv("PATH")
	for _, entry := range base {
		if strings.HasPrefix(entry, "PATH=") {
			pathValue = strings.TrimPrefix(entry, "PATH=")
		}
	}
	if strings.TrimSpace(pathValue) == "" {
		return withEnvOverride(base, "PATH", pathPrefix)
	}
	return withEnvOverride(base, "PATH", pathPrefix+string(os.PathListSeparator)+pathValue)
}

func applyCodexDirectRMGuardEnvironment(cmd *exec.Cmd, overlayRoot string) {
	if cmd == nil {
		return
	}
	guardBin := filepath.Join(overlayRoot, "bin")
	cmd.Env = withPathPrefix(cmd.Env, guardBin)
	guardedPath := envValue(cmd.Env, "PATH")
	if guardedPath == "" {
		return
	}
	// Codex applies shell_environment_policy.set after loading its login-shell
	// snapshot. The explicit override keeps the guard first even when a user's
	// shell profile rewrites PATH during startup.
	cmd.Args = append(cmd.Args, "-c", "shell_environment_policy.set.PATH="+strconv.Quote(guardedPath))
}

func envValue(env []string, key string) string {
	prefix := key + "="
	for i := len(env) - 1; i >= 0; i-- {
		if strings.HasPrefix(env[i], prefix) {
			return strings.TrimPrefix(env[i], prefix)
		}
	}
	return ""
}
