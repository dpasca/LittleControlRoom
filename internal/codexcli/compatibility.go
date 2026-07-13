package codexcli

import (
	"bufio"
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"sync"
	"time"
)

const (
	codeModeHostFeature    = "code_mode_host"
	codeModeHostExecutable = "codex-code-mode-host"
)

var compatibilityProbeTimeout = 2 * time.Second

// Compatibility records per-process safeguards applied to a Codex command.
type Compatibility struct {
	CodeModeHostDisabled bool
}

type featureProbeResult struct {
	enabled bool
	known   bool
}

var featureProbeCache = struct {
	sync.Mutex
	results map[string]featureProbeResult
}{
	results: make(map[string]featureProbeResult),
}

// ApplyCodeModeHostCompatibility keeps a packaging mismatch in the optional
// code-mode host from disabling tools in an otherwise healthy Codex install.
func ApplyCodeModeHostCompatibility(cmd *exec.Cmd) Compatibility {
	if cmd == nil || cmd.Err != nil {
		return Compatibility{}
	}
	hostAvailable := codeModeHostAvailable(cmd.Path)
	if hostAvailable {
		return Compatibility{}
	}

	cacheKey := codexFeatureProbeCacheKey(cmd, codeModeHostFeature)
	featureProbeCache.Lock()
	defer featureProbeCache.Unlock()
	result := featureProbeResult{}
	if cached, ok := featureProbeCache.results[cacheKey]; ok {
		result = cached
	} else {
		ctx, cancel := context.WithTimeout(context.Background(), compatibilityProbeTimeout)
		defer cancel()
		result.enabled, result.known = codexFeatureEnabled(ctx, cmd, codeModeHostFeature)
		if result.known {
			featureProbeCache.results[cacheKey] = result
		}
	}
	return applyCodeModeHostFallback(cmd, result.enabled, result.known, hostAvailable)
}

func codexFeatureProbeCacheKey(cmd *exec.Cmd, feature string) string {
	executable := ""
	if cmd != nil {
		executable = strings.TrimSpace(cmd.Path)
		if executable == "" && len(cmd.Args) > 0 {
			executable = strings.TrimSpace(cmd.Args[0])
		}
	}
	parts := []string{feature, fileFingerprint(executable)}
	if home := commandEnvValue(cmd, "CODEX_HOME"); home != "" {
		parts = append(parts, fileFingerprint(filepath.Join(home, "config.toml")))
	}
	return strings.Join(parts, "|")
}

func commandEnvValue(cmd *exec.Cmd, name string) string {
	if cmd == nil || strings.TrimSpace(name) == "" {
		return ""
	}
	prefix := name + "="
	for i := len(cmd.Env) - 1; i >= 0; i-- {
		if strings.HasPrefix(cmd.Env[i], prefix) {
			return strings.TrimSpace(strings.TrimPrefix(cmd.Env[i], prefix))
		}
	}
	return strings.TrimSpace(os.Getenv(name))
}

func fileFingerprint(path string) string {
	path = strings.TrimSpace(path)
	if path == "" {
		return ""
	}
	resolved := path
	if target, err := filepath.EvalSymlinks(path); err == nil {
		resolved = target
	}
	info, err := os.Stat(resolved)
	if err != nil {
		return resolved
	}
	return fmt.Sprintf("%s:%d:%d", resolved, info.Size(), info.ModTime().UnixNano())
}

func codexFeatureEnabled(ctx context.Context, target *exec.Cmd, feature string) (bool, bool) {
	if target == nil {
		return false, false
	}
	executable := strings.TrimSpace(target.Path)
	if executable == "" && len(target.Args) > 0 {
		executable = strings.TrimSpace(target.Args[0])
	}
	if executable == "" {
		return false, false
	}

	probe := exec.CommandContext(ctx, executable, "features", "list")
	probe.Dir = target.Dir
	probe.Env = target.Env
	output, err := probe.Output()
	if err != nil {
		return false, false
	}
	return parseCodexFeatureState(string(output), feature)
}

func parseCodexFeatureState(output, feature string) (bool, bool) {
	feature = strings.TrimSpace(feature)
	if feature == "" {
		return false, false
	}
	scanner := bufio.NewScanner(strings.NewReader(output))
	for scanner.Scan() {
		fields := strings.Fields(scanner.Text())
		if len(fields) < 2 || fields[0] != feature {
			continue
		}
		enabled, err := strconv.ParseBool(fields[len(fields)-1])
		if err != nil {
			return false, false
		}
		return enabled, true
	}
	return false, false
}

func applyCodeModeHostFallback(cmd *exec.Cmd, enabled, known, hostAvailable bool) Compatibility {
	if cmd == nil || hostAvailable || !known || !enabled {
		return Compatibility{}
	}
	cmd.Args = append(cmd.Args, "--disable", codeModeHostFeature)
	return Compatibility{CodeModeHostDisabled: true}
}

func codeModeHostAvailable(codexPath string) bool {
	if _, err := exec.LookPath(codeModeHostExecutable); err == nil {
		return true
	}

	candidates := []string{strings.TrimSpace(codexPath)}
	if resolved, err := filepath.EvalSymlinks(strings.TrimSpace(codexPath)); err == nil {
		candidates = append(candidates, resolved)
	}
	hostName := codeModeHostExecutable
	if runtime.GOOS == "windows" {
		hostName += ".exe"
	}
	for _, candidate := range candidates {
		if candidate == "" {
			continue
		}
		if executableFile(filepath.Join(filepath.Dir(candidate), hostName)) {
			return true
		}
	}
	return false
}

func executableFile(path string) bool {
	info, err := os.Stat(path)
	if err != nil || info.IsDir() {
		return false
	}
	return runtime.GOOS == "windows" || info.Mode().Perm()&0o111 != 0
}
