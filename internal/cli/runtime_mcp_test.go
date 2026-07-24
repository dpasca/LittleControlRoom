package cli

import "testing"

func TestParseRuntimeMCPOptionsKeepsBrowserAndRuntimeSessionKeysDistinct(t *testing.T) {
	opts, err := parseRuntimeMCPOptions([]string{
		"--project-path", "/tmp/demo",
		"--session-key", "todo-session",
		"--browser-session-key", "browser-session",
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
