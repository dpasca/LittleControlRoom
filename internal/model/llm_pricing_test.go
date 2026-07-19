package model

import (
	"math"
	"testing"
)

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

func TestEstimateLLMCostUSDGPT56Family(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name   string
		model  string
		input  float64
		cache  float64
		output float64
	}{
		{name: "alias", model: "gpt-5.6", input: 5.00, cache: 0.50, output: 30.00},
		{name: "sol", model: "openai/gpt-5.6-sol-2026-07-13", input: 5.00, cache: 0.50, output: 30.00},
		{name: "terra", model: "gpt-5.6-terra", input: 2.50, cache: 0.25, output: 15.00},
		{name: "luna", model: "openai/gpt-5.6-luna", input: 1.00, cache: 0.10, output: 6.00},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			cost, ok := EstimateLLMCostUSD(tt.model, LLMUsage{
				InputTokens:       1_000_000,
				CachedInputTokens: 100_000,
				OutputTokens:      10_000,
			})
			if !ok {
				t.Fatalf("expected pricing lookup for %s to succeed", tt.model)
			}
			want := 0.9*tt.input + 0.1*tt.cache + 0.01*tt.output
			if math.Abs(cost-want) > 1e-12 {
				t.Fatalf("cost = %f, want %f", cost, want)
			}
		})
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
