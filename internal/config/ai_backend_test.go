package config

import (
	"strings"
	"testing"
)

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
		{raw: "zai", want: AIBackendZai},
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
	if got := cfg.OpenAICompatibleModel(AIBackendOpenAIAPI); got != DefaultOpenAIProjectModel {
		t.Fatalf("default OpenAI project model = %q, want %q", got, DefaultOpenAIProjectModel)
	}
	if got := cfg.OpenAICompatibleModel(AIBackendDeepSeek); got != DefaultDeepSeekModel {
		t.Fatalf("default DeepSeek project model = %q, want %q", got, DefaultDeepSeekModel)
	}
	if got := cfg.OpenAICompatibleModel(AIBackendMoonshot); got != DefaultMoonshotModel {
		t.Fatalf("default Moonshot project model = %q, want %q", got, DefaultMoonshotModel)
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
	if got := cfg.OpenAICompatibleModel(AIBackendZai); got != DefaultZaiModel {
		t.Fatalf("default Z.ai project model = %q, want %q", got, DefaultZaiModel)
	}
	cfg.ZaiModel = "glm-5.3-flash"
	if got := cfg.OpenAICompatibleModel(AIBackendZai); got != "glm-5.3-flash" {
		t.Fatalf("configured Z.ai project model = %q", got)
	}
}

func TestBackendDefaultBossModelsSplitFastAndPro(t *testing.T) {
	t.Parallel()

	if got := AIBackendOpenAIAPI.DefaultBossHelmModel(); got != "gpt-5.6" {
		t.Fatalf("OpenAI helm default = %q, want gpt-5.6", got)
	}
	if got := AIBackendOpenAIAPI.DefaultBossUtilityModel(); got != "gpt-5.6-luna" {
		t.Fatalf("OpenAI utility default = %q, want gpt-5.6-luna", got)
	}
	if got := AIBackendXiaomi.DefaultBossHelmModel(); got != DefaultXiaomiProModel {
		t.Fatalf("Xiaomi helm default = %q, want %q", got, DefaultXiaomiProModel)
	}
	if got := AIBackendXiaomi.DefaultBossUtilityModel(); got != DefaultXiaomiModel {
		t.Fatalf("Xiaomi utility default = %q, want %q", got, DefaultXiaomiModel)
	}
	if got := AIBackendZai.DefaultBossHelmModel(); got != DefaultZaiProModel {
		t.Fatalf("Z.ai helm default = %q, want %q", got, DefaultZaiProModel)
	}
	if got := AIBackendZai.DefaultBossUtilityModel(); got != DefaultZaiModel {
		t.Fatalf("Z.ai utility default = %q, want %q", got, DefaultZaiModel)
	}
}

func TestZaiProviderDefaults(t *testing.T) {
	t.Parallel()

	cfg := Default()
	if got, want := cfg.OpenAICompatibleBaseURL(AIBackendZai), "https://api.z.ai/api/paas/v4"; got != want {
		t.Fatalf("Z.ai base URL = %q, want %q", got, want)
	}
	cfg.ZaiBaseURL = ZaiCodingPlanBaseURL
	if got, want := cfg.OpenAICompatibleBaseURL(AIBackendZai), "https://api.z.ai/api/coding/paas/v4"; got != want {
		t.Fatalf("configured Z.ai base URL = %q, want %q", got, want)
	}
	if got := cfg.OpenAICompatibleAPIKey(AIBackendZai); got != "" {
		t.Fatalf("default Z.ai API key = %q, want empty", got)
	}
	if got := AIBackendZai.Label(); got != "Z.ai GLM" {
		t.Fatalf("Z.ai label = %q, want Z.ai GLM", got)
	}
	if !AIBackendZai.UsesOpenAICompatibleAPI() || !AIBackendZai.UsesCloudAPIKey() {
		t.Fatalf("Z.ai should use the OpenAI-compatible cloud path")
	}
	if hint := ZaiCodingPlanBaseURLHint(); !strings.Contains(hint, ZaiCodingPlanBaseURL) {
		t.Fatalf("Z.ai coding-plan hint %q does not name %q", hint, ZaiCodingPlanBaseURL)
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
