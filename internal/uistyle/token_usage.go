package uistyle

import (
	"fmt"
	"strings"
)

func FormatTokenCount(v int64) string {
	switch {
	case v >= 1_000_000:
		return fmt.Sprintf("%.1fM", float64(v)/1_000_000)
	case v >= 10_000:
		return fmt.Sprintf("%dk", v/1_000)
	case v >= 1_000:
		return fmt.Sprintf("%.1fk", float64(v)/1_000)
	default:
		return fmt.Sprintf("%d", v)
	}
}

func FormatCompactTokenUsage(inputTokens, outputTokens, cachedInputTokens, reasoningTokens, totalTokens int64, includeTotal bool) string {
	parts := make([]string, 0, 5)
	if inputTokens > 0 {
		parts = append(parts, "i"+FormatTokenCount(inputTokens))
	}
	if outputTokens > 0 {
		parts = append(parts, "o"+FormatTokenCount(outputTokens))
	}
	if cachedInputTokens > 0 {
		parts = append(parts, "c"+FormatTokenCount(cachedInputTokens))
	}
	if reasoningTokens > 0 {
		parts = append(parts, "r"+FormatTokenCount(reasoningTokens))
	}
	if includeTotal && totalTokens > 0 {
		parts = append(parts, "t"+FormatTokenCount(totalTokens))
	}
	return strings.Join(parts, " ")
}

func FormatCompactTokenBreakdown(inputTokens, outputTokens, cachedInputTokens, reasoningTokens int64) string {
	parts := make([]string, 0, 4)
	if inputTokens > 0 {
		parts = append(parts, "i"+FormatTokenCount(inputTokens))
		parts = append(parts, fmt.Sprintf("c%d%%", CachedInputPercent(inputTokens, cachedInputTokens)))
	} else if cachedInputTokens > 0 {
		parts = append(parts, "c0%")
	}
	if reasoningTokens > 0 {
		parts = append(parts, "r"+FormatTokenCount(reasoningTokens))
	}
	if outputTokens > 0 {
		parts = append(parts, "o"+FormatTokenCount(outputTokens))
	}
	return strings.Join(parts, " ")
}

func CachedInputPercent(inputTokens, cachedInputTokens int64) int {
	if inputTokens <= 0 || cachedInputTokens <= 0 {
		return 0
	}
	if cachedInputTokens >= inputTokens {
		return 100
	}
	return int((cachedInputTokens*100 + inputTokens/2) / inputTokens)
}
