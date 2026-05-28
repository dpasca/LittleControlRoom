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

func TestParseAIBackendAcceptsSharedCloudAPIs(t *testing.T) {
	t.Parallel()

	tests := []struct {
		raw  string
		want AIBackend
	}{
		{raw: "openrouter", want: AIBackendOpenRouter},
		{raw: "deepseek", want: AIBackendDeepSeek},
		{raw: "moonshot", want: AIBackendMoonshot},
		{raw: "xiaomi", want: AIBackendXiaomi},
	}
	for _, tt := range tests {
		got, err := ParseAIBackend(tt.raw)
		if err != nil {
			t.Fatalf("ParseAIBackend(%q) error = %v", tt.raw, err)
		}
		if got != tt.want {
			t.Fatalf("ParseAIBackend(%q) = %q, want %q", tt.raw, got, tt.want)
		}
		boss, err := ParseBossChatBackend(tt.raw)
		if err != nil {
			t.Fatalf("ParseBossChatBackend(%q) error = %v", tt.raw, err)
		}
		if boss != tt.want {
			t.Fatalf("ParseBossChatBackend(%q) = %q, want %q", tt.raw, boss, tt.want)
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

func TestOpenAICompatibleModelUsesCloudOverrides(t *testing.T) {
	t.Parallel()

	cfg := Default()
	if got := cfg.OpenAICompatibleModel(AIBackendDeepSeek); got != DefaultDeepSeekModel {
		t.Fatalf("default DeepSeek project model = %q, want %q", got, DefaultDeepSeekModel)
	}
	cfg.DeepSeekModel = DefaultDeepSeekProModel
	if got := cfg.OpenAICompatibleModel(AIBackendDeepSeek); got != DefaultDeepSeekProModel {
		t.Fatalf("configured DeepSeek project model = %q, want %q", got, DefaultDeepSeekProModel)
	}
	if got := cfg.OpenAICompatibleModel(AIBackendXiaomi); got != DefaultXiaomiModel {
		t.Fatalf("default Xiaomi project model = %q, want %q", got, DefaultXiaomiModel)
	}
	if DefaultXiaomiModel != DefaultXiaomiProModel {
		t.Fatalf("default Xiaomi project model = %q, want pro default %q", DefaultXiaomiModel, DefaultXiaomiProModel)
	}
	cfg.XiaomiModel = "mimo-v2.5-pro-preview"
	if got := cfg.OpenAICompatibleModel(AIBackendXiaomi); got != "mimo-v2.5-pro-preview" {
		t.Fatalf("configured Xiaomi project model = %q", got)
	}
}

func TestBackendDefaultBossModelsSplitFastAndPro(t *testing.T) {
	t.Parallel()

	if got := AIBackendXiaomi.DefaultBossHelmModel(); got != DefaultXiaomiProModel {
		t.Fatalf("Xiaomi helm default = %q, want %q", got, DefaultXiaomiProModel)
	}
	if got := AIBackendXiaomi.DefaultBossUtilityModel(); got != DefaultXiaomiModel {
		t.Fatalf("Xiaomi utility default = %q, want %q", got, DefaultXiaomiModel)
	}
}

func TestXiaomiTokenPlanHints(t *testing.T) {
	t.Parallel()

	for _, key := range []string{"TC_example", "tp-example", "tp_example"} {
		if !LooksLikeXiaomiTokenPlanAPIKey(key) {
			t.Fatalf("LooksLikeXiaomiTokenPlanAPIKey(%q) = false, want true", key)
		}
	}
	if LooksLikeXiaomiTokenPlanAPIKey("sk-example") {
		t.Fatalf("LooksLikeXiaomiTokenPlanAPIKey() accepted regular key")
	}
	if !LooksLikeRegularXiaomiBaseURL("") || !LooksLikeRegularXiaomiBaseURL("https://api.xiaomimimo.com/v1/") {
		t.Fatalf("regular Xiaomi base URL was not recognized")
	}
	if !LooksLikeXiaomiTokenPlanBaseURL("https://token-plan-sgp.xiaomimimo.com/v1") {
		t.Fatalf("token-plan Xiaomi base URL was not recognized")
	}
}

func TestResolveBossChatBackendIsSeparateFromProjectBackend(t *testing.T) {
	t.Parallel()

	if got := ResolveBossChatBackend(AIBackendUnset, "sk-test"); got != AIBackendOpenAIAPI {
		t.Fatalf("ResolveBossChatBackend(unset, key) = %q, want %q", got, AIBackendOpenAIAPI)
	}
	if got := ResolveBossChatBackend(AIBackendDisabled, "sk-test"); got != AIBackendDisabled {
		t.Fatalf("ResolveBossChatBackend(disabled, key) = %q, want disabled", got)
	}
	if got := ResolveBossChatBackend(AIBackendMLX, ""); got != AIBackendMLX {
		t.Fatalf("ResolveBossChatBackend(mlx, no key) = %q, want mlx", got)
	}
	if got := ResolveBossChatBackend(AIBackendOllama, ""); got != AIBackendOllama {
		t.Fatalf("ResolveBossChatBackend(ollama, no key) = %q, want ollama", got)
	}
	if got := ResolveBossChatBackend(AIBackendDeepSeek, ""); got != AIBackendDeepSeek {
		t.Fatalf("ResolveBossChatBackend(deepseek, no key) = %q, want deepseek", got)
	}
	if _, err := ParseBossChatBackend("opencode"); err == nil {
		t.Fatalf("ParseBossChatBackend(opencode) error = nil, want unsupported backend error")
	}
}
