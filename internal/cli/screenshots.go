package cli

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"lcroom/internal/config"
	"lcroom/internal/service"
	"lcroom/internal/tui"
)

func runScreenshots(ctx context.Context, svc *service.Service, rawArgs []string) int {
	args, screenshotConfigPath, err := stripPathFlagArg(rawArgs, "--screenshot-config")
	if err != nil {
		fmt.Fprintf(os.Stderr, "screenshots flag error: %v\n", err)
		return 2
	}
	_, outputOverride, err := stripPathFlagArg(args, "--output-dir")
	if err != nil {
		fmt.Fprintf(os.Stderr, "screenshots flag error: %v\n", err)
		return 2
	}

	cfg, err := config.ParseScreenshotConfig(screenshotConfigPath)
	if err != nil {
		fmt.Fprintf(os.Stderr, "screenshot config error: %v\n", err)
		return 2
	}
	if strings.TrimSpace(outputOverride) != "" {
		outputDir := strings.TrimSpace(outputOverride)
		if !filepath.IsAbs(outputDir) {
			cwd, cwdErr := os.Getwd()
			if cwdErr != nil {
				fmt.Fprintf(os.Stderr, "resolve output dir: %v\n", cwdErr)
				return 1
			}
			outputDir = filepath.Join(cwd, outputDir)
		}
		cfg.OutputDir = filepath.Clean(outputDir)
	}

	if _, err := svc.ScanOnce(ctx); err != nil {
		fmt.Fprintf(os.Stderr, "screenshots scan failed: %v\n", err)
		return 1
	}

	report, err := tui.GenerateScreenshots(ctx, svc, cfg)
	if err != nil {
		fmt.Fprintf(os.Stderr, "generate screenshots failed: %v\n", err)
		return 1
	}

	if err := os.MkdirAll(cfg.OutputDir, 0o755); err != nil {
		fmt.Fprintf(os.Stderr, "create output dir: %v\n", err)
		return 1
	}

	browserPath, err := resolveScreenshotBrowser(cfg.BrowserPath)
	if err != nil {
		fmt.Fprintf(os.Stderr, "resolve screenshot browser: %v\n", err)
		return 1
	}

	tempDir, err := os.MkdirTemp("", "lcroom-screenshots-*")
	if err != nil {
		fmt.Fprintf(os.Stderr, "create screenshot temp dir: %v\n", err)
		return 1
	}
	keepTemp := false
	defer func() {
		if !keepTemp {
			_ = os.RemoveAll(tempDir)
		}
	}()

	fmt.Printf("screenshots written to %s\n", cfg.OutputDir)
	for _, asset := range report.Assets {
		htmlPath := filepath.Join(tempDir, asset.Name+".html")
		if err := os.WriteFile(htmlPath, []byte(asset.HTML), 0o644); err != nil {
			fmt.Fprintf(os.Stderr, "write %s: %v\n", htmlPath, err)
			keepTemp = true
			return 1
		}
		path := filepath.Join(cfg.OutputDir, asset.Name+".png")
		if err := captureBrowserScreenshot(ctx, browserPath, htmlPath, path, asset.Width, asset.Height); err != nil {
			fmt.Fprintf(os.Stderr, "render %s: %v\n", path, err)
			fmt.Fprintf(os.Stderr, "debug html left at %s\n", htmlPath)
			keepTemp = true
			return 1
		}
		for _, legacyPath := range []string{
			filepath.Join(cfg.OutputDir, asset.Name+".svg"),
			filepath.Join(cfg.OutputDir, asset.Name+".html"),
		} {
			if err := os.Remove(legacyPath); err != nil && !errors.Is(err, os.ErrNotExist) {
				fmt.Fprintf(os.Stderr, "remove legacy artifact %s: %v\n", legacyPath, err)
				return 1
			}
		}
		fmt.Printf("  - %s\n", path)
	}
	if len(report.MatchedProjects) > 0 {
		fmt.Printf("matched projects: %s\n", strings.Join(report.MatchedProjects, ", "))
	}
	if len(report.MissingProjectFilters) > 0 {
		fmt.Printf("missing project filters: %s\n", strings.Join(report.MissingProjectFilters, ", "))
	}
	for _, warning := range report.Warnings {
		fmt.Printf("warning: %s\n", warning)
	}
	return 0
}

func stripPathFlagArg(args []string, flagName string) ([]string, string, error) {
	if len(args) == 0 {
		return nil, "", nil
	}

	out := make([]string, 0, len(args))
	value := ""
	for i := 0; i < len(args); i++ {
		arg := args[i]
		prefix := flagName + "="
		switch {
		case strings.HasPrefix(arg, prefix):
			raw := strings.TrimSpace(strings.TrimPrefix(arg, prefix))
			if raw == "" {
				return nil, "", fmt.Errorf("%s requires a value", flagName)
			}
			value = raw
		case arg == flagName:
			if i+1 >= len(args) {
				return nil, "", fmt.Errorf("%s requires a value", flagName)
			}
			raw := strings.TrimSpace(args[i+1])
			if raw == "" {
				return nil, "", fmt.Errorf("%s requires a value", flagName)
			}
			value = raw
			i++
		default:
			out = append(out, arg)
		}
	}
	return out, value, nil
}
