package cli

import (
	"context"
	"flag"
	"fmt"
	"os"
	"os/signal"
	"strings"
	"syscall"

	"lcroom/internal/agentquery"
	"lcroom/internal/control"
	"lcroom/internal/runtimemcp"
	"lcroom/internal/todocapture"
)

type runtimeMCPOptions struct {
	projectPath          string
	provider             string
	dataDir              string
	sessionKey           string
	browserSessionKey    string
	claudeApprovalSocket string
	dbPath               string
	todoCaptureMode      todocapture.CaptureMode
	controlScope         control.AuthorityScope
	queryScope           agentquery.Scope
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
		ProjectPath:          opts.projectPath,
		Provider:             opts.provider,
		DataDir:              opts.dataDir,
		SessionKey:           opts.sessionKey,
		BrowserSessionKey:    opts.browserSessionKey,
		ClaudeApprovalSocket: opts.claudeApprovalSocket,
		DBPath:               opts.dbPath,
		TodoCaptureMode:      opts.todoCaptureMode,
		ControlScope:         opts.controlScope,
		QueryScope:           opts.queryScope,
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
	browserSessionKey := fs.String("browser-session-key", "", "managed browser session key")
	claudeApprovalSocket := fs.String("claude-approval-socket", "", "embedded Claude Code approval socket")
	dbPath := fs.String("db-path", "", "LCR SQLite database path for queries, controls, and project TODO capture")
	todoCaptureMode := fs.String("todo-capture-mode", string(todocapture.ModeOff), "project TODO capture mode")
	controlScope := fs.String("control-scope", string(control.AuthorityScopeProject), "control authority scope: project, portfolio, or host")
	queryScope := fs.String("query-scope", string(agentquery.ScopeProject), "read-only query scope: project or portfolio")
	if err := fs.Parse(args); err != nil {
		return runtimeMCPOptions{}, err
	}
	parsedMode, err := todocapture.ParseCaptureMode(*todoCaptureMode)
	if err != nil {
		return runtimeMCPOptions{}, fmt.Errorf("--todo-capture-mode: %w", err)
	}
	parsedControlScope := control.NormalizeAuthorityScope(*controlScope)
	if parsedControlScope == "" {
		return runtimeMCPOptions{}, fmt.Errorf("--control-scope must be project, portfolio, or host")
	}
	parsedQueryScope := agentquery.NormalizeScope(*queryScope)
	if parsedQueryScope == "" {
		return runtimeMCPOptions{}, fmt.Errorf("--query-scope must be project or portfolio")
	}
	opts := runtimeMCPOptions{
		projectPath:          strings.TrimSpace(*projectPath),
		provider:             strings.TrimSpace(*provider),
		dataDir:              strings.TrimSpace(*dataDir),
		sessionKey:           strings.TrimSpace(*sessionKey),
		browserSessionKey:    strings.TrimSpace(*browserSessionKey),
		claudeApprovalSocket: strings.TrimSpace(*claudeApprovalSocket),
		dbPath:               strings.TrimSpace(*dbPath),
		todoCaptureMode:      parsedMode,
		controlScope:         parsedControlScope,
		queryScope:           parsedQueryScope,
	}
	if opts.projectPath == "" {
		return runtimeMCPOptions{}, fmt.Errorf("--project-path is required")
	}
	if opts.todoCaptureMode.Enabled() && opts.dbPath == "" {
		return runtimeMCPOptions{}, fmt.Errorf("--db-path is required when TODO capture is enabled")
	}
	return opts, nil
}
