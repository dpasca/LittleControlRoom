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
