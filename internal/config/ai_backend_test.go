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

func TestParseAIBackendAcceptsMLXAndOllama(t *testing.T) {
	t.Parallel()

	tests := []struct {
		raw  string
		want AIBackend
	}{
		{raw: "mlx", want: AIBackendMLX},
		{raw: "ollama", want: AIBackendOllama},
	}
	for _, tt := range tests {
		got, err := ParseAIBackend(tt.raw)
		if err != nil {
			t.Fatalf("ParseAIBackend(%q) error = %v", tt.raw, err)
		}
		if got != tt.want {
			t.Fatalf("ParseAIBackend(%q) = %q, want %q", tt.raw, got, tt.want)
		}
	}
}

func TestAIBackendLocalProviderHelpers(t *testing.T) {
	t.Parallel()

	if !AIBackendMLX.UsesLocalProviderPath() {
		t.Fatalf("AIBackendMLX should use local provider path")
	}
	if !AIBackendOllama.UsesLocalProviderPath() {
		t.Fatalf("AIBackendOllama should use local provider path")
	}
	if got := AIBackendMLX.DefaultOpenAICompatibleBaseURL(); got != "http://127.0.0.1:8080/v1" {
		t.Fatalf("AIBackendMLX.DefaultOpenAICompatibleBaseURL() = %q", got)
	}
	if got := AIBackendOllama.DefaultOpenAICompatibleBaseURL(); got != "http://127.0.0.1:11434/v1" {
		t.Fatalf("AIBackendOllama.DefaultOpenAICompatibleBaseURL() = %q", got)
	}
}
