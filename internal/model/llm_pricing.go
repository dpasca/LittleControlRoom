package model

import "strings"

type llmPriceCard struct {
	InputUSDPerMTokens       float64
	CachedInputUSDPerMTokens float64
	OutputUSDPerMTokens      float64
}

func EstimateLLMCostUSD(modelName string, usage LLMUsage) (float64, bool) {
	price, ok := lookupLLMPriceCard(modelName)
	if !ok {
		return 0, false
	}

	inputTokens := usage.InputTokens
	if inputTokens < 0 {
		inputTokens = 0
	}
	cachedInputTokens := usage.CachedInputTokens
	if cachedInputTokens < 0 {
		cachedInputTokens = 0
	}
	if cachedInputTokens > inputTokens {
		cachedInputTokens = inputTokens
	}
	outputTokens := usage.OutputTokens
	if outputTokens < 0 {
		outputTokens = 0
	}

	uncachedInputTokens := inputTokens - cachedInputTokens
	costUSD := (float64(uncachedInputTokens) * price.InputUSDPerMTokens / 1_000_000) +
		(float64(cachedInputTokens) * price.CachedInputUSDPerMTokens / 1_000_000) +
		(float64(outputTokens) * price.OutputUSDPerMTokens / 1_000_000)
	return costUSD, true
}

func lookupLLMPriceCard(modelName string) (llmPriceCard, bool) {
	name := strings.ToLower(strings.TrimSpace(modelName))
	switch {
	case name == "gpt-5.5" || strings.HasPrefix(name, "gpt-5.5-"):
		return llmPriceCard{
			InputUSDPerMTokens:       5.00,
			CachedInputUSDPerMTokens: 0.50,
			OutputUSDPerMTokens:      30.00,
		}, true
	case name == "gpt-5-mini" || strings.HasPrefix(name, "gpt-5-mini-"):
		return llmPriceCard{
			InputUSDPerMTokens:       0.25,
			CachedInputUSDPerMTokens: 0.025,
			OutputUSDPerMTokens:      2.00,
		}, true
	case name == "gpt-5.4-mini" || strings.HasPrefix(name, "gpt-5.4-mini-"):
		return llmPriceCard{
			InputUSDPerMTokens:       0.75,
			CachedInputUSDPerMTokens: 0.075,
			OutputUSDPerMTokens:      4.50,
		}, true
	case name == "gpt-5-nano" || strings.HasPrefix(name, "gpt-5-nano-"):
		return llmPriceCard{
			InputUSDPerMTokens:       0.05,
			CachedInputUSDPerMTokens: 0.005,
			OutputUSDPerMTokens:      0.40,
		}, true
	case name == "gpt-5.4-nano" || strings.HasPrefix(name, "gpt-5.4-nano-"):
		return llmPriceCard{
			InputUSDPerMTokens:       0.20,
			CachedInputUSDPerMTokens: 0.02,
			OutputUSDPerMTokens:      1.25,
		}, true
	default:
		return llmPriceCard{}, false
	}
}
