package codexapp

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func TestPrepareCodexHomeOverlayShadowsPlaywrightSkillAndSymlinksRest(t *testing.T) {
	sourceHome := t.TempDir()
	if err := os.WriteFile(filepath.Join(sourceHome, "config.toml"), []byte("model = \"gpt-5\""), 0o644); err != nil {
		t.Fatalf("write config: %v", err)
	}
	sourceSkillsDir := filepath.Join(sourceHome, "skills")
	if err := os.MkdirAll(filepath.Join(sourceSkillsDir, "screenshot"), 0o755); err != nil {
		t.Fatalf("mkdir screenshot skill: %v", err)
	}
	if err := os.MkdirAll(filepath.Join(sourceSkillsDir, "playwright"), 0o755); err != nil {
		t.Fatalf("mkdir playwright skill: %v", err)
	}
	if err := os.WriteFile(filepath.Join(sourceSkillsDir, "playwright", "SKILL.md"), []byte("original skill"), 0o644); err != nil {
		t.Fatalf("write original playwright skill: %v", err)
	}

	overlay, err := prepareCodexHomeOverlay(t.TempDir(), sourceHome)
	if err != nil {
		t.Fatalf("prepareCodexHomeOverlay() error = %v", err)
	}

	assertSymlinkTo(t, filepath.Join(overlay, "config.toml"), filepath.Join(sourceHome, "config.toml"))
	assertSymlinkTo(t, filepath.Join(overlay, "skills", "screenshot"), filepath.Join(sourceSkillsDir, "screenshot"))

	skillPath := filepath.Join(overlay, "skills", "playwright", "SKILL.md")
	data, err := os.ReadFile(skillPath)
	if err != nil {
		t.Fatalf("read overlay playwright skill: %v", err)
	}
	text := string(data)
	if !strings.Contains(text, "Use the Playwright MCP tools directly.") {
		t.Fatalf("overlay skill = %q, want embedded MCP guidance", text)
	}
	if strings.Contains(text, "original skill") {
		t.Fatalf("overlay skill should not mirror original Playwright skill contents: %q", text)
	}

	scriptPath := filepath.Join(overlay, "skills", "playwright", "scripts", "playwright_cli.sh")
	scriptInfo, err := os.Stat(scriptPath)
	if err != nil {
		t.Fatalf("stat overlay playwright wrapper: %v", err)
	}
	if scriptInfo.Mode().Perm()&0o111 == 0 {
		t.Fatalf("overlay wrapper permissions = %v, want executable bit", scriptInfo.Mode().Perm())
	}
}

func TestPrepareCodexHomeOverlayCreatesStandaloneShadowSkillWhenSourceHomeMissing(t *testing.T) {
	missingSource := filepath.Join(t.TempDir(), "missing-codex-home")
	overlay, err := prepareCodexHomeOverlay(t.TempDir(), missingSource)
	if err != nil {
		t.Fatalf("prepareCodexHomeOverlay() error = %v", err)
	}

	skillPath := filepath.Join(overlay, "skills", "playwright", "SKILL.md")
	data, err := os.ReadFile(skillPath)
	if err != nil {
		t.Fatalf("read overlay skill: %v", err)
	}
	if !strings.Contains(string(data), "Little Control Room") {
		t.Fatalf("overlay skill = %q, want LCR guidance", string(data))
	}
}

func TestCodexHomeOverlayPromptInputShowsShadowPlaywrightSkill(t *testing.T) {
	if _, err := exec.LookPath("codex"); err != nil {
		t.Skip("codex binary not available")
	}

	sourceHome := filepath.Join(t.TempDir(), ".codex")
	if err := os.MkdirAll(sourceHome, 0o755); err != nil {
		t.Fatalf("mkdir source home: %v", err)
	}
	overlay, err := prepareCodexHomeOverlay(t.TempDir(), sourceHome)
	if err != nil {
		t.Fatalf("prepareCodexHomeOverlay() error = %v", err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(overlay) })

	cmd := exec.Command("codex", "debug", "prompt-input", "please use playwright")
	cmd.Env = withEnvOverride(os.Environ(), "CODEX_HOME", overlay)
	cmd.Dir = t.TempDir()
	output, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("codex debug prompt-input error = %v\n%s", err, string(output))
	}
	text := string(output)
	if !strings.Contains(text, "Use the embedded Playwright MCP tools already wired through Little Control Room") {
		t.Fatalf("prompt-input missing overlay playwright skill description: %s", text)
	}
	if !strings.Contains(text, filepath.Join(overlay, "skills", "playwright", "SKILL.md")) {
		t.Fatalf("prompt-input missing overlay playwright skill path: %s", text)
	}
}

func assertSymlinkTo(t *testing.T, path, wantTarget string) {
	t.Helper()
	info, err := os.Lstat(path)
	if err != nil {
		t.Fatalf("lstat %s: %v", path, err)
	}
	if info.Mode()&os.ModeSymlink == 0 {
		t.Fatalf("%s mode = %v, want symlink", path, info.Mode())
	}
	target, err := os.Readlink(path)
	if err != nil {
		t.Fatalf("readlink %s: %v", path, err)
	}
	if target != wantTarget {
		t.Fatalf("readlink %s = %q, want %q", path, target, wantTarget)
	}
}
