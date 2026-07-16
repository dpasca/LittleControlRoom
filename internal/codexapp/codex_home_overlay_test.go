package codexapp

import (
	"encoding/json"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
)

func TestPrepareCodexHomeOverlayShadowsPlaywrightSkillAndSymlinksRest(t *testing.T) {
	sourceHome := t.TempDir()
	if err := os.WriteFile(filepath.Join(sourceHome, "config.toml"), []byte("model = \"gpt-5\""), 0o644); err != nil {
		t.Fatalf("write config: %v", err)
	}
	sourceRulesDir := filepath.Join(sourceHome, "rules")
	if err := os.MkdirAll(sourceRulesDir, 0o755); err != nil {
		t.Fatalf("mkdir source rules: %v", err)
	}
	if err := os.WriteFile(filepath.Join(sourceRulesDir, "default.rules"), []byte(`prefix_rule(pattern=["git"], decision="allow")`), 0o644); err != nil {
		t.Fatalf("write source rule: %v", err)
	}
	sourceBinDir := filepath.Join(sourceHome, "bin")
	if err := os.MkdirAll(sourceBinDir, 0o755); err != nil {
		t.Fatalf("mkdir source bin: %v", err)
	}
	if err := os.WriteFile(filepath.Join(sourceBinDir, "helper"), []byte("original helper"), 0o755); err != nil {
		t.Fatalf("write source helper: %v", err)
	}
	if err := os.WriteFile(filepath.Join(sourceBinDir, "rm"), []byte("original rm"), 0o755); err != nil {
		t.Fatalf("write source rm: %v", err)
	}
	sourceSkillsDir := filepath.Join(sourceHome, "skills")
	if err := os.MkdirAll(filepath.Join(sourceSkillsDir, "screenshot"), 0o755); err != nil {
		t.Fatalf("mkdir screenshot skill: %v", err)
	}
	if err := os.MkdirAll(filepath.Join(sourceSkillsDir, "playwright"), 0o755); err != nil {
		t.Fatalf("mkdir playwright skill: %v", err)
	}
	if err := os.MkdirAll(filepath.Join(sourceSkillsDir, "runtime"), 0o755); err != nil {
		t.Fatalf("mkdir runtime skill: %v", err)
	}
	if err := os.WriteFile(filepath.Join(sourceSkillsDir, "playwright", "SKILL.md"), []byte("original skill"), 0o644); err != nil {
		t.Fatalf("write original playwright skill: %v", err)
	}
	if err := os.WriteFile(filepath.Join(sourceSkillsDir, "runtime", "SKILL.md"), []byte("original runtime skill"), 0o644); err != nil {
		t.Fatalf("write original runtime skill: %v", err)
	}

	overlay, err := prepareCodexHomeOverlay(t.TempDir(), sourceHome)
	if err != nil {
		t.Fatalf("prepareCodexHomeOverlay() error = %v", err)
	}

	assertSymlinkTo(t, filepath.Join(overlay, "config.toml"), filepath.Join(sourceHome, "config.toml"))
	assertSymlinkTo(t, filepath.Join(overlay, "skills", "screenshot"), filepath.Join(sourceSkillsDir, "screenshot"))
	assertSymlinkTo(t, filepath.Join(overlay, "rules", "default.rules"), filepath.Join(sourceRulesDir, "default.rules"))
	assertSymlinkTo(t, filepath.Join(overlay, "bin", "helper"), filepath.Join(sourceBinDir, "helper"))

	directRMRulePath := filepath.Join(overlay, "rules", codexDirectRMRuleFilename)
	directRMRuleInfo, err := os.Lstat(directRMRulePath)
	if err != nil {
		t.Fatalf("lstat direct rm rule: %v", err)
	}
	if directRMRuleInfo.Mode()&os.ModeSymlink != 0 {
		t.Fatalf("direct rm rule should be owned by the overlay, got symlink mode %v", directRMRuleInfo.Mode())
	}
	directRMRule, err := os.ReadFile(directRMRulePath)
	if err != nil {
		t.Fatalf("read direct rm rule: %v", err)
	}
	if !strings.Contains(string(directRMRule), `decision = "forbidden"`) || !strings.Contains(string(directRMRule), `"/bin/rm"`) {
		t.Fatalf("direct rm rule = %q", string(directRMRule))
	}

	shimPath := filepath.Join(overlay, "bin", "rm")
	shimInfo, err := os.Stat(shimPath)
	if err != nil {
		t.Fatalf("stat direct rm shim: %v", err)
	}
	if shimInfo.Mode().Perm()&0o111 == 0 {
		t.Fatalf("direct rm shim permissions = %v, want executable bit", shimInfo.Mode().Perm())
	}
	shimData, err := os.ReadFile(shimPath)
	if err != nil {
		t.Fatalf("read direct rm shim: %v", err)
	}
	if strings.Contains(string(shimData), "original rm") || !strings.Contains(string(shimData), "Blocked by Little Control Room") {
		t.Fatalf("direct rm shim = %q", string(shimData))
	}
	sourceRMData, err := os.ReadFile(filepath.Join(sourceBinDir, "rm"))
	if err != nil {
		t.Fatalf("read source rm: %v", err)
	}
	if string(sourceRMData) != "original rm" {
		t.Fatalf("source rm was modified: %q", string(sourceRMData))
	}

	blocked := exec.Command(shimPath, "-rf")
	blockedOutput, blockedErr := blocked.CombinedOutput()
	var exitErr *exec.ExitError
	if !errors.As(blockedErr, &exitErr) || exitErr.ExitCode() != 126 {
		t.Fatalf("targetless shim call error = %v output=%q, want exit 126", blockedErr, string(blockedOutput))
	}
	if !strings.Contains(string(blockedOutput), "validated /tmp descendants") {
		t.Fatalf("targetless shim output = %q", string(blockedOutput))
	}

	skillPath := filepath.Join(overlay, "skills", "playwright", "SKILL.md")
	data, err := os.ReadFile(skillPath)
	if err != nil {
		t.Fatalf("read overlay playwright skill: %v", err)
	}
	text := string(data)
	if !strings.Contains(text, "Use the Playwright MCP tools directly.") {
		t.Fatalf("overlay skill = %q, want embedded MCP guidance", text)
	}
	if !strings.Contains(text, "lcr_runtime/request_browser_attention") || !strings.Contains(text, "exact page") {
		t.Fatalf("overlay skill = %q, want structured browser-attention handoff guidance", text)
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

	runtimeSkillPath := filepath.Join(overlay, "skills", "runtime", "SKILL.md")
	runtimeData, err := os.ReadFile(runtimeSkillPath)
	if err != nil {
		t.Fatalf("read overlay runtime skill: %v", err)
	}
	runtimeText := string(runtimeData)
	if !strings.Contains(runtimeText, "lcr_runtime") || !strings.Contains(runtimeText, "start_process") || !strings.Contains(runtimeText, "request_browser_attention") {
		t.Fatalf("overlay runtime skill = %q, want runtime MCP guidance", runtimeText)
	}
	if strings.Contains(runtimeText, "original runtime skill") {
		t.Fatalf("overlay runtime skill should not mirror original contents: %q", runtimeText)
	}
}

func TestCodexDirectRMShimAllowsOnlyValidatedTmpDescendants(t *testing.T) {
	overlay, err := prepareCodexHomeOverlayWithOptions(t.TempDir(), t.TempDir(), false)
	if err != nil {
		t.Fatalf("prepareCodexHomeOverlayWithOptions() error = %v", err)
	}
	shimPath := filepath.Join(overlay, "bin", "rm")

	tmpRoot, err := os.MkdirTemp("/tmp", "lcroom-rm-shim-test-*")
	if err != nil {
		t.Skipf("create /tmp test root: %v", err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(tmpRoot) })

	t.Run("recursive forced descendant", func(t *testing.T) {
		target := filepath.Join(tmpRoot, "safe target")
		if err := os.MkdirAll(target, 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(target, "marker"), []byte("keep until removed"), 0o600); err != nil {
			t.Fatal(err)
		}
		assertRMShimAllowed(t, shimPath, "-rf", "--", target)
		if _, err := os.Lstat(target); !os.IsNotExist(err) {
			t.Fatalf("target stat error = %v, want removed", err)
		}
	})

	t.Run("separate recursive and force flags", func(t *testing.T) {
		first := filepath.Join(tmpRoot, "first")
		second := filepath.Join(tmpRoot, "second")
		for _, target := range []string{first, second} {
			if err := os.MkdirAll(target, 0o755); err != nil {
				t.Fatal(err)
			}
		}
		assertRMShimAllowed(t, shimPath, "-r", "-f", first, second)
		for _, target := range []string{first, second} {
			if _, err := os.Lstat(target); !os.IsNotExist(err) {
				t.Fatalf("target %q stat error = %v, want removed", target, err)
			}
		}
	})

	t.Run("nonexistent descendant", func(t *testing.T) {
		assertRMShimAllowed(t, shimPath, "-rf", filepath.Join(tmpRoot, "missing", "child"))
	})

	t.Run("non recursive rm remains blocked", func(t *testing.T) {
		target := filepath.Join(tmpRoot, "non-recursive")
		if err := os.WriteFile(target, []byte("keep"), 0o600); err != nil {
			t.Fatal(err)
		}
		assertRMShimBlocked(t, shimPath, "-f", target)
		if _, err := os.Stat(target); err != nil {
			t.Fatalf("blocked target stat error = %v", err)
		}
	})

	t.Run("dot dot component remains blocked", func(t *testing.T) {
		inside := filepath.Join(tmpRoot, "inside")
		kept := filepath.Join(tmpRoot, "kept")
		for _, target := range []string{inside, kept} {
			if err := os.MkdirAll(target, 0o755); err != nil {
				t.Fatal(err)
			}
		}
		assertRMShimBlocked(t, shimPath, "-rf", inside+"/../kept")
		if _, err := os.Stat(kept); err != nil {
			t.Fatalf("blocked target stat error = %v", err)
		}
	})

	outsideRoot, err := os.MkdirTemp("/var/tmp", "lcroom-rm-shim-outside-test-*")
	if err != nil {
		t.Skipf("create outside test root: %v", err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(outsideRoot) })

	tmpCanonical, err := filepath.EvalSymlinks("/tmp")
	if err != nil {
		t.Fatal(err)
	}
	outsideCanonical, err := filepath.EvalSymlinks(outsideRoot)
	if err != nil {
		t.Fatal(err)
	}
	if outsideCanonical == tmpCanonical || strings.HasPrefix(outsideCanonical, tmpCanonical+string(os.PathSeparator)) {
		t.Skipf("outside test root %q resolves below /tmp", outsideRoot)
	}

	t.Run("outside target remains blocked", func(t *testing.T) {
		target := filepath.Join(outsideRoot, "outside")
		if err := os.MkdirAll(target, 0o755); err != nil {
			t.Fatal(err)
		}
		assertRMShimBlocked(t, shimPath, "-rf", target)
		if _, err := os.Stat(target); err != nil {
			t.Fatalf("blocked target stat error = %v", err)
		}
	})

	t.Run("mixed targets remain blocked atomically", func(t *testing.T) {
		safeTarget := filepath.Join(tmpRoot, "mixed-safe")
		outsideTarget := filepath.Join(outsideRoot, "mixed-outside")
		for _, target := range []string{safeTarget, outsideTarget} {
			if err := os.MkdirAll(target, 0o755); err != nil {
				t.Fatal(err)
			}
		}
		assertRMShimBlocked(t, shimPath, "-rf", safeTarget, outsideTarget)
		for _, target := range []string{safeTarget, outsideTarget} {
			if _, err := os.Stat(target); err != nil {
				t.Fatalf("blocked target %q stat error = %v", target, err)
			}
		}
	})

	t.Run("symlinked parent cannot escape", func(t *testing.T) {
		outsideTarget := filepath.Join(outsideRoot, "through-link")
		if err := os.MkdirAll(outsideTarget, 0o755); err != nil {
			t.Fatal(err)
		}
		link := filepath.Join(tmpRoot, "escape-link")
		if err := os.Symlink(outsideRoot, link); err != nil {
			t.Skipf("create symlink: %v", err)
		}
		assertRMShimBlocked(t, shimPath, "-rf", filepath.Join(link, "through-link"))
		if _, err := os.Stat(outsideTarget); err != nil {
			t.Fatalf("symlink escape target stat error = %v", err)
		}
	})

	t.Run("final symlink is removed without following it", func(t *testing.T) {
		outsideTarget := filepath.Join(outsideRoot, "final-link-target")
		if err := os.MkdirAll(outsideTarget, 0o755); err != nil {
			t.Fatal(err)
		}
		link := filepath.Join(tmpRoot, "final-link")
		if err := os.Symlink(outsideTarget, link); err != nil {
			t.Skipf("create symlink: %v", err)
		}
		assertRMShimAllowed(t, shimPath, "-rf", link)
		if _, err := os.Lstat(link); !os.IsNotExist(err) {
			t.Fatalf("final symlink stat error = %v, want removed", err)
		}
		if _, err := os.Stat(outsideTarget); err != nil {
			t.Fatalf("final symlink target stat error = %v", err)
		}
	})

	t.Run("trailing slash on final symlink remains blocked", func(t *testing.T) {
		outsideTarget := filepath.Join(outsideRoot, "trailing-link-target")
		if err := os.MkdirAll(outsideTarget, 0o755); err != nil {
			t.Fatal(err)
		}
		link := filepath.Join(tmpRoot, "trailing-link")
		if err := os.Symlink(outsideTarget, link); err != nil {
			t.Skipf("create symlink: %v", err)
		}
		assertRMShimBlocked(t, shimPath, "-rf", link+"/")
		if _, err := os.Lstat(link); err != nil {
			t.Fatalf("blocked final symlink stat error = %v", err)
		}
		if _, err := os.Stat(outsideTarget); err != nil {
			t.Fatalf("blocked final symlink target stat error = %v", err)
		}
	})
}

func TestPrepareCodexHomeOverlayDoesNotAdvertiseUnavailableRuntimeHandoff(t *testing.T) {
	sourceHome := filepath.Join(t.TempDir(), ".codex")
	runtimeDir := filepath.Join(sourceHome, "skills", "runtime")
	if err := os.MkdirAll(runtimeDir, 0o755); err != nil {
		t.Fatalf("mkdir source runtime skill: %v", err)
	}
	if err := os.WriteFile(filepath.Join(runtimeDir, "SKILL.md"), []byte("original runtime skill"), 0o644); err != nil {
		t.Fatalf("write source runtime skill: %v", err)
	}

	overlay, err := prepareCodexHomeOverlayForLaunch(t.TempDir(), sourceHome, true, false)
	if err != nil {
		t.Fatalf("prepareCodexHomeOverlayForLaunch() error = %v", err)
	}
	playwrightRaw, err := os.ReadFile(filepath.Join(overlay, "skills", "playwright", "SKILL.md"))
	if err != nil {
		t.Fatalf("read overlay Playwright skill: %v", err)
	}
	if strings.Contains(string(playwrightRaw), "request_browser_attention") {
		t.Fatalf("Playwright skill advertises unavailable runtime handoff: %s", playwrightRaw)
	}
	runtimePath := filepath.Join(overlay, "skills", "runtime")
	if target, err := os.Readlink(runtimePath); err != nil {
		t.Fatalf("read runtime skill symlink: %v", err)
	} else if target != runtimeDir {
		t.Fatalf("runtime skill symlink = %q, want %q", target, runtimeDir)
	}
}

func TestPrepareCodexHomeOverlayRuntimeOnlyPreservesPlaywrightSkill(t *testing.T) {
	sourceHome := filepath.Join(t.TempDir(), ".codex")
	playwrightDir := filepath.Join(sourceHome, "skills", "playwright")
	runtimeDir := filepath.Join(sourceHome, "skills", "runtime")
	for _, dir := range []string{playwrightDir, runtimeDir} {
		if err := os.MkdirAll(dir, 0o755); err != nil {
			t.Fatalf("mkdir source skill: %v", err)
		}
	}
	if err := os.WriteFile(filepath.Join(playwrightDir, "SKILL.md"), []byte("original playwright skill"), 0o644); err != nil {
		t.Fatalf("write source playwright skill: %v", err)
	}
	if err := os.WriteFile(filepath.Join(runtimeDir, "SKILL.md"), []byte("original runtime skill"), 0o644); err != nil {
		t.Fatalf("write source runtime skill: %v", err)
	}

	overlay, err := prepareCodexHomeOverlayForLaunch(t.TempDir(), sourceHome, false, true)
	if err != nil {
		t.Fatalf("prepareCodexHomeOverlayForLaunch() error = %v", err)
	}
	assertSymlinkTo(t, filepath.Join(overlay, "skills", "playwright"), playwrightDir)
	runtimeRaw, err := os.ReadFile(filepath.Join(overlay, "skills", "runtime", "SKILL.md"))
	if err != nil {
		t.Fatalf("read overlay runtime skill: %v", err)
	}
	if strings.Contains(string(runtimeRaw), "request_browser_attention") {
		t.Fatalf("runtime-only overlay advertises unavailable managed-browser handoff: %s", runtimeRaw)
	}
}

func assertRMShimAllowed(t *testing.T, shimPath string, args ...string) {
	t.Helper()
	output, err := exec.Command(shimPath, args...).CombinedOutput()
	if err != nil {
		t.Fatalf("rm shim args %q error = %v output=%q, want allowed", args, err, string(output))
	}
}

func assertRMShimBlocked(t *testing.T, shimPath string, args ...string) {
	t.Helper()
	output, err := exec.Command(shimPath, args...).CombinedOutput()
	var exitErr *exec.ExitError
	if !errors.As(err, &exitErr) || exitErr.ExitCode() != 126 {
		t.Fatalf("rm shim args %q error = %v output=%q, want exit 126", args, err, string(output))
	}
	if !strings.Contains(string(output), "validated /tmp descendants") {
		t.Fatalf("rm shim args %q output = %q, want denial reason", args, string(output))
	}
}

func TestPrepareCodexHomeOverlayWithoutSkillShadowPreservesSourceSkills(t *testing.T) {
	sourceHome := t.TempDir()
	sourceSkillsDir := filepath.Join(sourceHome, "skills")
	if err := os.MkdirAll(sourceSkillsDir, 0o755); err != nil {
		t.Fatalf("mkdir source skills: %v", err)
	}
	overlay, err := prepareCodexHomeOverlayWithOptions(t.TempDir(), sourceHome, false)
	if err != nil {
		t.Fatalf("prepareCodexHomeOverlayWithOptions() error = %v", err)
	}

	assertSymlinkTo(t, filepath.Join(overlay, "skills"), sourceSkillsDir)
	if _, err := os.Stat(filepath.Join(overlay, "rules", codexDirectRMRuleFilename)); err != nil {
		t.Fatalf("stat direct rm rule: %v", err)
	}
	if _, err := os.Stat(filepath.Join(overlay, "bin", "rm")); err != nil {
		t.Fatalf("stat direct rm shim: %v", err)
	}
}

func TestCodexDirectRMExecPolicyDelegatesNamedRMAndForbidsBypasses(t *testing.T) {
	if _, err := exec.LookPath("codex"); err != nil {
		t.Skip("codex binary not available")
	}
	overlay, err := prepareCodexHomeOverlayWithOptions(t.TempDir(), t.TempDir(), false)
	if err != nil {
		t.Fatalf("prepareCodexHomeOverlayWithOptions() error = %v", err)
	}
	rulePath := filepath.Join(overlay, "rules", codexDirectRMRuleFilename)

	for _, tt := range []struct {
		name    string
		command []string
		want    string
	}{
		{name: "named outside", command: []string{"rm", "-rf", "/Users/example"}, want: ""},
		{name: "named tmp", command: []string{"rm", "-rf", "/tmp/example"}, want: ""},
		{name: "absolute", command: []string{"/bin/rm", "-fr", "build"}, want: "forbidden"},
		{name: "absolute tmp", command: []string{"/usr/bin/rm", "-rf", "/tmp/example"}, want: "forbidden"},
		{name: "wrapped", command: []string{"sudo", "rm", "-rf", "/tmp/example"}, want: "forbidden"},
		{name: "unrelated", command: []string{"rmdir", "empty-dir"}, want: ""},
	} {
		t.Run(tt.name, func(t *testing.T) {
			args := []string{"execpolicy", "check", "--rules", rulePath, "--"}
			args = append(args, tt.command...)
			output, err := exec.Command("codex", args...).CombinedOutput()
			if err != nil {
				t.Fatalf("codex execpolicy check error = %v\n%s", err, string(output))
			}
			var result struct {
				Decision string `json:"decision"`
			}
			if err := json.Unmarshal(output, &result); err != nil {
				t.Fatalf("decode execpolicy output: %v\n%s", err, string(output))
			}
			if result.Decision != tt.want {
				t.Fatalf("decision = %q, want %q\n%s", result.Decision, tt.want, string(output))
			}
		})
	}
}

func TestWithPathPrefixPreservesExistingPath(t *testing.T) {
	env := withPathPrefix([]string{"HOME=/tmp/example", "PATH=/usr/local/bin:/usr/bin"}, "/guard/bin")
	var pathValue string
	for _, entry := range env {
		if strings.HasPrefix(entry, "PATH=") {
			pathValue = strings.TrimPrefix(entry, "PATH=")
		}
	}
	want := "/guard/bin" + string(os.PathListSeparator) + "/usr/local/bin:/usr/bin"
	if pathValue != want {
		t.Fatalf("PATH = %q, want %q", pathValue, want)
	}
}

func TestCodexDirectRMShimIsFirstOnGuardedPath(t *testing.T) {
	overlay, err := prepareCodexHomeOverlayWithOptions(t.TempDir(), t.TempDir(), false)
	if err != nil {
		t.Fatalf("prepareCodexHomeOverlayWithOptions() error = %v", err)
	}
	cmd := exec.Command("/bin/sh", "-c", "command -v rm")
	cmd.Env = withPathPrefix(os.Environ(), filepath.Join(overlay, "bin"))
	output, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("resolve guarded rm path: %v\n%s", err, string(output))
	}
	want := filepath.Join(overlay, "bin", "rm")
	if got := strings.TrimSpace(string(output)); got != want {
		t.Fatalf("command -v rm = %q, want %q", got, want)
	}
}

func TestApplyCodexDirectRMGuardEnvironmentPinsShellPolicyPath(t *testing.T) {
	overlay := t.TempDir()
	cmd := exec.Command("codex", "app-server")
	cmd.Env = []string{"HOME=/tmp/example", "PATH=/usr/local/bin:/usr/bin"}
	applyCodexDirectRMGuardEnvironment(cmd, overlay)

	guardedPath := filepath.Join(overlay, "bin") + string(os.PathListSeparator) + "/usr/local/bin:/usr/bin"
	if got := envValue(cmd.Env, "PATH"); got != guardedPath {
		t.Fatalf("guarded PATH = %q, want %q", got, guardedPath)
	}
	wantOverride := "shell_environment_policy.set.PATH=" + strconv.Quote(guardedPath)
	if len(cmd.Args) < 2 || cmd.Args[len(cmd.Args)-2] != "-c" || cmd.Args[len(cmd.Args)-1] != wantOverride {
		t.Fatalf("cmd.Args = %#v, want trailing -c %q", cmd.Args, wantOverride)
	}
}

func TestPrepareCodexHomeOverlayCreatesStandaloneShadowSkillWhenSourceHomeMissing(t *testing.T) {
	missingSource := filepath.Join(t.TempDir(), "missing-codex-home")
	overlay, err := prepareCodexHomeOverlay(t.TempDir(), missingSource)
	if err != nil {
		t.Fatalf("prepareCodexHomeOverlay() error = %v", err)
	}

	skillPath := filepath.Join(overlay, "skills", "runtime", "SKILL.md")
	data, err := os.ReadFile(skillPath)
	if err != nil {
		t.Fatalf("read overlay skill: %v", err)
	}
	if !strings.Contains(string(data), "Little Control Room") || !strings.Contains(string(data), "lcr_runtime") || !strings.Contains(string(data), "request_browser_attention") {
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
