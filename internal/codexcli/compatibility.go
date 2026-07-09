package codexcli

import (
	"bufio"
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"time"
)

const (
	codeModeHostFeature       = "code_mode_host"
	codeModeHostExecutable    = "codex-code-mode-host"
	compatibilityProbeTimeout = 2 * time.Second
)

// Compatibility records per-process safeguards applied to a Codex command.
type Compatibility struct {
	CodeModeHostDisabled bool
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

	ctx, cancel := context.WithTimeout(context.Background(), compatibilityProbeTimeout)
	defer cancel()
	enabled, known := codexFeatureEnabled(ctx, cmd, codeModeHostFeature)
	return applyCodeModeHostFallback(cmd, enabled, known, hostAvailable)
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
