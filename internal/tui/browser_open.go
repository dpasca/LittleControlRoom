package tui

import (
	"fmt"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
)

var externalBrowserOpener = openExternalBrowserURL

func openProjectDirInBrowser(path string) error {
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

	return openBrowserURL(directoryFileURL(absPath), "open project in browser")
}

func directoryFileURL(path string) string {
	cleanPath := filepath.ToSlash(filepath.Clean(path))
	if !strings.HasPrefix(cleanPath, "/") {
		cleanPath = "/" + cleanPath
	}
	if !strings.HasSuffix(cleanPath, "/") {
		cleanPath += "/"
	}
	return (&url.URL{Scheme: "file", Path: cleanPath}).String()
}

func openRuntimeURLInBrowser(rawURL string) error {
	rawURL = strings.TrimSpace(rawURL)
	if rawURL == "" {
		return fmt.Errorf("runtime URL is required")
	}
	return openBrowserURL(rawURL, "open runtime URL in browser")
}

func openBrowserURL(rawURL, action string) error {
	rawURL = strings.TrimSpace(rawURL)
	if rawURL == "" {
		return fmt.Errorf("browser URL is required")
	}
	if err := externalBrowserOpener(rawURL); err != nil {
		return fmt.Errorf("%s: %w", action, err)
	}
	return nil
}

func openExternalBrowserURL(rawURL string) error {
	var cmd *exec.Cmd
	switch runtime.GOOS {
	case "darwin":
		cmd = exec.Command("open", rawURL)
	case "windows":
		cmd = exec.Command("cmd", "/c", "start", "", rawURL)
	default:
		cmd = exec.Command("xdg-open", rawURL)
	}
	return cmd.Run()
}
