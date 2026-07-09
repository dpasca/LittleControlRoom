package codexcli

import (
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"runtime"
	"testing"
)

func TestParseCodexFeatureState(t *testing.T) {
	output := "" +
		"code_mode                            under development  false\n" +
		"code_mode_host                       stable             true\n" +
		"code_mode_only                       under development  false\n"

	if enabled, known := parseCodexFeatureState(output, codeModeHostFeature); !known || !enabled {
		t.Fatalf("parseCodexFeatureState() = enabled:%t known:%t, want true/true", enabled, known)
	}
	if enabled, known := parseCodexFeatureState(output, "code_mode"); !known || enabled {
		t.Fatalf("parseCodexFeatureState(code_mode) = enabled:%t known:%t, want false/true", enabled, known)
	}
	if enabled, known := parseCodexFeatureState(output, "missing"); known || enabled {
		t.Fatalf("parseCodexFeatureState(missing) = enabled:%t known:%t, want false/false", enabled, known)
	}
}

func TestApplyCodeModeHostFallbackDisablesEnabledMissingHost(t *testing.T) {
	cmd := exec.Command("codex", "app-server", "-c", "model=\"gpt-5\"")
	original := append([]string(nil), cmd.Args...)

	compatibility := applyCodeModeHostFallback(cmd, true, true, false)

	if !compatibility.CodeModeHostDisabled {
		t.Fatal("CodeModeHostDisabled = false, want true")
	}
	want := append(original, "--disable", codeModeHostFeature)
	if !reflect.DeepEqual(cmd.Args, want) {
		t.Fatalf("cmd.Args = %#v, want %#v", cmd.Args, want)
	}
}

func TestApplyCodeModeHostFallbackLeavesHealthyOrUnknownInstallAlone(t *testing.T) {
	tests := []struct {
		name          string
		enabled       bool
		known         bool
		hostAvailable bool
	}{
		{name: "feature disabled", enabled: false, known: true, hostAvailable: false},
		{name: "feature unknown", enabled: false, known: false, hostAvailable: false},
		{name: "host installed", enabled: true, known: true, hostAvailable: true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			cmd := exec.Command("codex", "app-server")
			original := append([]string(nil), cmd.Args...)

			compatibility := applyCodeModeHostFallback(cmd, tt.enabled, tt.known, tt.hostAvailable)

			if compatibility.CodeModeHostDisabled {
				t.Fatal("CodeModeHostDisabled = true, want false")
			}
			if !reflect.DeepEqual(cmd.Args, original) {
				t.Fatalf("cmd.Args = %#v, want unchanged %#v", cmd.Args, original)
			}
		})
	}
}

func TestApplyCodeModeHostCompatibilityProbesInstalledCodex(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("test fixture uses a POSIX shell script")
	}
	dir := t.TempDir()
	codexPath := filepath.Join(dir, "codex")
	script := "#!/bin/sh\n" +
		"printf '%s\\n' 'code_mode_host                       stable             true'\n"
	if err := os.WriteFile(codexPath, []byte(script), 0o755); err != nil {
		t.Fatalf("write fake codex: %v", err)
	}
	t.Setenv("PATH", dir)
	cmd := exec.Command(codexPath, "app-server")
	cmd.Env = os.Environ()

	compatibility := ApplyCodeModeHostCompatibility(cmd)

	if !compatibility.CodeModeHostDisabled {
		t.Fatal("CodeModeHostDisabled = false, want true")
	}
	want := []string{codexPath, "app-server", "--disable", codeModeHostFeature}
	if !reflect.DeepEqual(cmd.Args, want) {
		t.Fatalf("cmd.Args = %#v, want %#v", cmd.Args, want)
	}
}
