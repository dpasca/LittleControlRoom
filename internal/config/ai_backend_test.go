package config

import "testing"

func TestParseAIBackendAcceptsClaudeCode(t *testing.T) {
	t.Parallel()

	got, err := ParseAIBackend("claude_code")
	if err != nil {
		t.Fatalf("ParseAIBackend() error = %v", err)
	}
	if got != AIBackendClaude {
		t.Fatalf("ParseAIBackend() = %q, want %q", got, AIBackendClaude)
	}
}

func TestAIBackendClaudeLabel(t *testing.T) {
	t.Parallel()

	if got := AIBackendClaude.Label(); got != "Claude Code" {
		t.Fatalf("AIBackendClaude.Label() = %q, want Claude Code", got)
	}
}
