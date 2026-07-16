package cli

import (
	"context"
	"flag"
	"fmt"
	"os"
	"os/signal"
	"strings"
	"syscall"

	"lcroom/internal/runtimemcp"
	"lcroom/internal/todocapture"
)

type runtimeMCPOptions struct {
	projectPath     string
	provider        string
	dataDir         string
	sessionKey      string
	dbPath          string
	todoCaptureMode todocapture.CaptureMode
}

func runRuntimeMCP(args []string) int {
	opts, err := parseRuntimeMCPOptions(args)
	if err != nil {
		fmt.Fprintf(os.Stderr, "runtime-mcp config error: %v\n", err)
		return 2
	}
	ctx, cancel := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer cancel()
	if err := runtimemcp.Run(ctx, runtimemcp.Options{
		ProjectPath:     opts.projectPath,
		Provider:        opts.provider,
		DataDir:         opts.dataDir,
		SessionKey:      opts.sessionKey,
		DBPath:          opts.dbPath,
		TodoCaptureMode: opts.todoCaptureMode,
	}); err != nil {
		fmt.Fprintf(os.Stderr, "runtime-mcp error: %v\n", err)
		return 1
	}
	return 0
}

func parseRuntimeMCPOptions(args []string) (runtimeMCPOptions, error) {
	fs := flag.NewFlagSet("runtime-mcp", flag.ContinueOnError)
	projectPath := fs.String("project-path", "", "project path")
	provider := fs.String("provider", "codex", "embedded provider")
	dataDir := fs.String("data-dir", "", "LCR data dir")
	sessionKey := fs.String("session-key", "", "runtime MCP session key")
	dbPath := fs.String("db-path", "", "LCR SQLite database path for project TODO capture")
	todoCaptureMode := fs.String("todo-capture-mode", string(todocapture.ModeOff), "project TODO capture mode")
	if err := fs.Parse(args); err != nil {
		return runtimeMCPOptions{}, err
	}
	parsedMode, err := todocapture.ParseCaptureMode(*todoCaptureMode)
	if err != nil {
		return runtimeMCPOptions{}, fmt.Errorf("--todo-capture-mode: %w", err)
	}
	opts := runtimeMCPOptions{
		projectPath:     strings.TrimSpace(*projectPath),
		provider:        strings.TrimSpace(*provider),
		dataDir:         strings.TrimSpace(*dataDir),
		sessionKey:      strings.TrimSpace(*sessionKey),
		dbPath:          strings.TrimSpace(*dbPath),
		todoCaptureMode: parsedMode,
	}
	if opts.projectPath == "" {
		return runtimeMCPOptions{}, fmt.Errorf("--project-path is required")
	}
	if opts.todoCaptureMode.Enabled() && opts.dbPath == "" {
		return runtimeMCPOptions{}, fmt.Errorf("--db-path is required when TODO capture is enabled")
	}
	return opts, nil
}
