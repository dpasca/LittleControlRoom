package model

import "testing"

func TestEstimateLLMCostUSD(t *testing.T) {
	t.Parallel()

	cost, ok := EstimateLLMCostUSD("gpt-5-mini-2025-08-07", LLMUsage{
		InputTokens:       1_000_000,
		CachedInputTokens: 100_000,
		OutputTokens:      10_000,
	})
	if !ok {
		t.Fatalf("expected pricing lookup to succeed")
	}

	want := 0.9*0.25 + 0.1*0.025 + 0.01*2.00
	if cost != want {
		t.Fatalf("cost = %f, want %f", cost, want)
	}
}

func TestEstimateLLMCostUSDGPT55(t *testing.T) {
	t.Parallel()

	cost, ok := EstimateLLMCostUSD("gpt-5.5", LLMUsage{
		InputTokens:       1_000_000,
		CachedInputTokens: 100_000,
		OutputTokens:      10_000,
	})
	if !ok {
		t.Fatalf("expected pricing lookup to succeed")
	}

	want := 0.9*5.00 + 0.1*0.50 + 0.01*30.00
	if cost != want {
		t.Fatalf("cost = %f, want %f", cost, want)
	}
}

func TestEstimateLLMCostUSDDeepSeekV4Pro(t *testing.T) {
	t.Parallel()

	cost, ok := EstimateLLMCostUSD("deepseek-v4-pro", LLMUsage{
		InputTokens:       1_000_000,
		CachedInputTokens: 100_000,
		OutputTokens:      10_000,
	})
	if !ok {
		t.Fatalf("expected pricing lookup to succeed")
	}

	want := 0.9*0.435 + 0.1*0.003625 + 0.01*0.87
	if cost != want {
		t.Fatalf("cost = %f, want %f", cost, want)
	}
}

func TestEstimateLLMCostUSDKimiK26(t *testing.T) {
	t.Parallel()

	cost, ok := EstimateLLMCostUSD("kimi-k2.6", LLMUsage{
		InputTokens:       1_000_000,
		CachedInputTokens: 100_000,
		OutputTokens:      10_000,
	})
	if !ok {
		t.Fatalf("expected pricing lookup to succeed")
	}

	want := 0.9*0.95 + 0.1*0.16 + 0.01*4.00
	if cost != want {
		t.Fatalf("cost = %f, want %f", cost, want)
	}
}

func TestEstimateLLMCostUSDKimiK27Code(t *testing.T) {
	t.Parallel()

	cost, ok := EstimateLLMCostUSD("kimi-k2.7-code", LLMUsage{
		InputTokens:       1_000_000,
		CachedInputTokens: 100_000,
		OutputTokens:      10_000,
	})
	if !ok {
		t.Fatalf("expected pricing lookup to succeed")
	}

	want := 0.9*0.95 + 0.1*0.19 + 0.01*4.00
	if cost != want {
		t.Fatalf("cost = %f, want %f", cost, want)
	}
}

func TestEstimateLLMCostUSDGLM51(t *testing.T) {
	t.Parallel()

	cost, ok := EstimateLLMCostUSD("z-ai/glm-5.1", LLMUsage{
		InputTokens:       1_000_000,
		CachedInputTokens: 100_000,
		OutputTokens:      10_000,
	})
	if !ok {
		t.Fatalf("expected pricing lookup to succeed")
	}

	want := 0.9*1.05 + 0.1*0.525 + 0.01*3.50
	if cost != want {
		t.Fatalf("cost = %f, want %f", cost, want)
	}
}

func TestEstimateLLMCostUSDUnknownModel(t *testing.T) {
	t.Parallel()

	if cost, ok := EstimateLLMCostUSD("gpt-test-mini", LLMUsage{InputTokens: 123}); ok || cost != 0 {
		t.Fatalf("EstimateLLMCostUSD() = (%f, %v), want (0, false)", cost, ok)
	}
}
