package config

import (
	"fmt"
	"strings"
)

type ConflictResolverProvider string

const (
	ConflictResolverProviderCodex      ConflictResolverProvider = "codex"
	ConflictResolverProviderOpenCode   ConflictResolverProvider = "opencode"
	ConflictResolverProviderClaudeCode ConflictResolverProvider = "claude_code"
	ConflictResolverProviderLCAgent    ConflictResolverProvider = "lcagent"
)

func ParseConflictResolverProvider(raw string) (ConflictResolverProvider, error) {
	switch strings.ToLower(strings.TrimSpace(raw)) {
	case "", string(ConflictResolverProviderCodex):
		return ConflictResolverProviderCodex, nil
	case string(ConflictResolverProviderOpenCode), "open-code":
		return ConflictResolverProviderOpenCode, nil
	case string(ConflictResolverProviderClaudeCode), "claude", "claude-code":
		return ConflictResolverProviderClaudeCode, nil
	case string(ConflictResolverProviderLCAgent), "lc-agent", "lc_agent":
		return ConflictResolverProviderLCAgent, nil
	default:
		return "", fmt.Errorf("conflict resolver provider must be one of: codex, opencode, claude_code, lcagent")
	}
}

func NormalizeConflictResolverProvider(provider ConflictResolverProvider) ConflictResolverProvider {
	normalized, err := ParseConflictResolverProvider(string(provider))
	if err != nil {
		return ConflictResolverProvider(strings.ToLower(strings.TrimSpace(string(provider))))
	}
	return normalized
}
