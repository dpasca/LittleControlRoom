package codexapp

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func TestPrepareOpenCodeConfigOverlayShadowsPlaywrightSkillAndSymlinksRest(t *testing.T) {
	sourceRoot := filepath.Join(t.TempDir(), "opencode")
	sourceSkillsDir := filepath.Join(sourceRoot, "skills")
	if err := os.MkdirAll(filepath.Join(sourceSkillsDir, "playwright"), 0o755); err != nil {
		t.Fatalf("mkdir playwright skill: %v", err)
	}
	if err := os.MkdirAll(filepath.Join(sourceSkillsDir, "other"), 0o755); err != nil {
		t.Fatalf("mkdir other skill: %v", err)
	}
	if err := os.WriteFile(filepath.Join(sourceSkillsDir, "playwright", "SKILL.md"), []byte("original playwright skill"), 0o644); err != nil {
		t.Fatalf("write original playwright skill: %v", err)
	}
	if err := os.WriteFile(filepath.Join(sourceSkillsDir, "other", "SKILL.md"), []byte("other skill"), 0o644); err != nil {
		t.Fatalf("write other skill: %v", err)
	}
	if err := os.WriteFile(filepath.Join(sourceRoot, "config.json"), []byte(`{"demo":true}`), 0o644); err != nil {
		t.Fatalf("write config.json: %v", err)
	}

	overlayRoot, err := prepareOpenCodeConfigOverlay(t.TempDir(), sourceRoot)
	if err != nil {
		t.Fatalf("prepareOpenCodeConfigOverlay() error = %v", err)
	}

	configPath := filepath.Join(overlayRoot, "opencode", "config.json")
	if target, err := os.Readlink(configPath); err != nil {
		t.Fatalf("readlink config.json: %v", err)
	} else if target != filepath.Join(sourceRoot, "config.json") {
		t.Fatalf("config.json symlink = %q, want %q", target, filepath.Join(sourceRoot, "config.json"))
	}

	otherSkillPath := filepath.Join(overlayRoot, "opencode", "skills", "other")
	if target, err := os.Readlink(otherSkillPath); err != nil {
		t.Fatalf("readlink other skill: %v", err)
	} else if target != filepath.Join(sourceRoot, "skills", "other") {
		t.Fatalf("other skill symlink = %q, want %q", target, filepath.Join(sourceRoot, "skills", "other"))
	}

	playwrightSkillPath := filepath.Join(overlayRoot, "opencode", "skills", "playwright", "SKILL.md")
	raw, err := os.ReadFile(playwrightSkillPath)
	if err != nil {
		t.Fatalf("read overlay playwright skill: %v", err)
	}
	text := string(raw)
	if !strings.Contains(text, "Use the embedded Playwright MCP tools already wired through Little Control Room") {
		t.Fatalf("overlay Playwright skill text missing managed browser guidance: %s", text)
	}
}

func TestOpenCodeConfigOverlayDebugSkillShowsShadowPlaywrightSkill(t *testing.T) {
	if _, err := exec.LookPath("opencode"); err != nil {
		t.Skip("opencode not installed")
	}

	sourceRoot := filepath.Join(t.TempDir(), "opencode")
	sourceSkillsDir := filepath.Join(sourceRoot, "skills")
	if err := os.MkdirAll(filepath.Join(sourceSkillsDir, "helper"), 0o755); err != nil {
		t.Fatalf("mkdir helper skill: %v", err)
	}
	if err := os.WriteFile(filepath.Join(sourceSkillsDir, "helper", "SKILL.md"), []byte("---\nname: \"helper\"\ndescription: \"helper\"\n---\nhelper"), 0o644); err != nil {
		t.Fatalf("write helper skill: %v", err)
	}

	overlayRoot, err := prepareOpenCodeConfigOverlay(t.TempDir(), sourceRoot)
	if err != nil {
		t.Fatalf("prepareOpenCodeConfigOverlay() error = %v", err)
	}

	cmd := exec.Command("opencode", "debug", "skill")
	cmd.Env = withEnvOverride(os.Environ(), "XDG_CONFIG_HOME", overlayRoot)
	output, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("opencode debug skill error = %v\n%s", err, output)
	}
	text := string(output)
	if !strings.Contains(text, "Use the embedded Playwright MCP tools already wired through Little Control Room") {
		t.Fatalf("debug skill output missing overlay playwright description: %s", text)
	}
	if !strings.Contains(text, filepath.Join(overlayRoot, "opencode", "skills", "playwright", "SKILL.md")) {
		t.Fatalf("debug skill output missing overlay playwright skill path: %s", text)
	}
}
