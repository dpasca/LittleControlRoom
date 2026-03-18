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

func TestEstimateLLMCostUSDUnknownModel(t *testing.T) {
	t.Parallel()

	if cost, ok := EstimateLLMCostUSD("gpt-test-mini", LLMUsage{InputTokens: 123}); ok || cost != 0 {
		t.Fatalf("EstimateLLMCostUSD() = (%f, %v), want (0, false)", cost, ok)
	}
}
