package cli

import (
	"testing"

	"lcroom/internal/agentquery"
	"lcroom/internal/control"
)

func TestParseRuntimeMCPOptionsKeepsBrowserAndRuntimeSessionKeysDistinct(t *testing.T) {
	opts, err := parseRuntimeMCPOptions([]string{
		"--project-path", "/tmp/demo",
		"--session-key", "todo-session",
		"--browser-session-key", "browser-session",
		"--claude-approval-socket", "/tmp/approval.sock",
	})
	if err != nil {
		t.Fatalf("parseRuntimeMCPOptions() error = %v", err)
	}
	if got, want := opts.sessionKey, "todo-session"; got != want {
		t.Fatalf("session key = %q, want %q", got, want)
	}
	if got, want := opts.browserSessionKey, "browser-session"; got != want {
		t.Fatalf("browser session key = %q, want %q", got, want)
	}
	if got, want := opts.claudeApprovalSocket, "/tmp/approval.sock"; got != want {
		t.Fatalf("Claude approval socket = %q, want %q", got, want)
	}
}

func TestParseRuntimeMCPOptionsParsesQueryScope(t *testing.T) {
	opts, err := parseRuntimeMCPOptions([]string{
		"--project-path", "/tmp/demo",
		"--query-scope", "portfolio",
	})
	if err != nil {
		t.Fatalf("parseRuntimeMCPOptions() error = %v", err)
	}
	if opts.queryScope != agentquery.ScopePortfolio {
		t.Fatalf("query scope = %q, want portfolio", opts.queryScope)
	}
	if _, err := parseRuntimeMCPOptions([]string{
		"--project-path", "/tmp/demo",
		"--query-scope", "everything",
	}); err == nil {
		t.Fatal("invalid query scope was accepted")
	}
}

func TestParseRuntimeMCPOptionsAllowsLegacySessionKeyOnly(t *testing.T) {
	opts, err := parseRuntimeMCPOptions([]string{
		"--project-path", "/tmp/demo",
		"--session-key", "shared-session",
	})
	if err != nil {
		t.Fatalf("parseRuntimeMCPOptions() error = %v", err)
	}
	if got, want := opts.sessionKey, "shared-session"; got != want {
		t.Fatalf("session key = %q, want %q", got, want)
	}
	if opts.browserSessionKey != "" {
		t.Fatalf("browser session key = %q, want empty legacy fallback", opts.browserSessionKey)
	}
}

func TestParseRuntimeMCPOptionsParsesControlScope(t *testing.T) {
	opts, err := parseRuntimeMCPOptions([]string{
		"--project-path", "/tmp/demo",
		"--control-scope", "portfolio",
	})
	if err != nil {
		t.Fatalf("parseRuntimeMCPOptions() error = %v", err)
	}
	if opts.controlScope != control.AuthorityScopePortfolio {
		t.Fatalf("control scope = %q, want portfolio", opts.controlScope)
	}
	if _, err := parseRuntimeMCPOptions([]string{
		"--project-path", "/tmp/demo",
		"--control-scope", "everything",
	}); err == nil {
		t.Fatal("invalid control scope was accepted")
	}
}
