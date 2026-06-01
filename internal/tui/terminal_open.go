package tui

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
)

var externalTerminalOpener = openExternalTerminal

func openProjectDirInTerminal(path string) error {
	if strings.TrimSpace(path) == "" {
		return fmt.Errorf("project path is required")
	}

	absPath, err := filepath.Abs(path)
	if err != nil {
		return fmt.Errorf("resolve project path: %w", err)
	}
	info, err := os.Stat(absPath)
	if err != nil {
		return fmt.Errorf("inspect project path: %w", err)
	}
	if !info.IsDir() {
		return fmt.Errorf("project path is not a directory")
	}

	return externalTerminalOpener(absPath)
}

func openExternalTerminal(path string) error {
	switch runtime.GOOS {
	case "darwin":
		app := strings.TrimSpace(os.Getenv("LCR_TERMINAL_APP"))
		if app == "" {
			app = "Terminal"
		}
		return startDetached(exec.Command("open", "-a", app, path))
	case "windows":
		return startDetached(exec.Command("cmd", "/c", "start", "", "cmd", "/K", "cd", "/d", path))
	default:
		return openExternalTerminalUnix(path)
	}
}

func openExternalTerminalUnix(path string) error {
	if terminal := strings.TrimSpace(os.Getenv("TERMINAL")); terminal != "" {
		cmd := exec.Command(terminal)
		cmd.Dir = path
		return startDetached(cmd)
	}

	candidates := []struct {
		name string
		args []string
	}{
		{name: "x-terminal-emulator"},
		{name: "gnome-terminal", args: []string{"--working-directory=" + path}},
		{name: "konsole", args: []string{"--workdir", path}},
		{name: "xfce4-terminal", args: []string{"--working-directory", path}},
		{name: "alacritty", args: []string{"--working-directory", path}},
		{name: "kitty", args: []string{"--directory", path}},
		{name: "wezterm", args: []string{"start", "--cwd", path}},
		{name: "foot", args: []string{"--working-directory", path}},
		{name: "xterm"},
	}
	for _, candidate := range candidates {
		exe, err := exec.LookPath(candidate.name)
		if err != nil {
			continue
		}
		cmd := exec.Command(exe, candidate.args...)
		cmd.Dir = path
		return startDetached(cmd)
	}
	return fmt.Errorf("no supported terminal launcher found; set TERMINAL")
}

func startDetached(cmd *exec.Cmd) error {
	if err := cmd.Start(); err != nil {
		return err
	}
	if cmd.Process == nil {
		return nil
	}
	return cmd.Process.Release()
}
