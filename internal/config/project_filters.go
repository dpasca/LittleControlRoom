package config

import (
	"errors"
	"fmt"
	"os"
	"strings"

	toml "github.com/pelletier/go-toml/v2"
)

func normalizeProjectPatterns(parts []string) []string {
	out := make([]string, 0, len(parts))
	seen := map[string]struct{}{}
	for _, part := range parts {
		pattern := strings.TrimSpace(part)
		if pattern == "" {
			continue
		}
		if _, ok := seen[pattern]; ok {
			continue
		}
		seen[pattern] = struct{}{}
		out = append(out, pattern)
	}
	return out
}

func LoadExcludeProjectPatterns(path string, fallback []string) ([]string, error) {
	patterns := append([]string(nil), fallback...)
	if strings.TrimSpace(path) == "" {
		return patterns, nil
	}

	raw, err := os.ReadFile(path)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return patterns, nil
		}
		return patterns, fmt.Errorf("read config file %s: %w", path, err)
	}

	var fc fileConfig
	if err := toml.Unmarshal(raw, &fc); err != nil {
		return patterns, fmt.Errorf("parse config file %s: %w", path, err)
	}
	if fc.ExcludeProjectPatterns == nil {
		return patterns, nil
	}
	return normalizeProjectPatterns(*fc.ExcludeProjectPatterns), nil
}

func ProjectNameExcluded(name string, patterns []string) bool {
	candidate := strings.ToLower(strings.TrimSpace(name))
	if candidate == "" || len(patterns) == 0 {
		return false
	}
	for _, pattern := range patterns {
		if wildcardMatch(strings.ToLower(strings.TrimSpace(pattern)), candidate) {
			return true
		}
	}
	return false
}

func wildcardMatch(pattern, value string) bool {
	pattern = strings.TrimSpace(pattern)
	if pattern == "" {
		return value == ""
	}

	patternIndex := 0
	valueIndex := 0
	starIndex := -1
	matchIndex := 0

	for valueIndex < len(value) {
		if patternIndex < len(pattern) && pattern[patternIndex] == value[valueIndex] {
			patternIndex++
			valueIndex++
			continue
		}
		if patternIndex < len(pattern) && pattern[patternIndex] == '*' {
			starIndex = patternIndex
			matchIndex = valueIndex
			patternIndex++
			continue
		}
		if starIndex >= 0 {
			patternIndex = starIndex + 1
			matchIndex++
			valueIndex = matchIndex
			continue
		}
		return false
	}

	for patternIndex < len(pattern) && pattern[patternIndex] == '*' {
		patternIndex++
	}
	return patternIndex == len(pattern)
}

func RedactPrivacy(name string, patterns []string) string {
	if name == "" || len(patterns) == 0 {
		return name
	}
	if ProjectNameExcluded(name, patterns) {
		return "********"
	}
	return name
}

func MatchesPrivacyPattern(name string, patterns []string) bool {
	return ProjectNameExcluded(name, patterns)
}
