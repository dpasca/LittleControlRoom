package fuzzyfilter

import (
	"strings"
	"unicode"
)

// Match reports whether every query token matches at least one candidate.
// Matching is case-insensitive and accepts exact fragments, normalized
// fragments, and ordered-character fuzzy matches.
func Match(query string, candidates ...string) bool {
	tokens := queryTokens(query)
	if len(tokens) == 0 {
		return true
	}
	prepared := make([]preparedCandidate, 0, len(candidates))
	for _, candidate := range candidates {
		folded := strings.ToLower(strings.TrimSpace(candidate))
		normalized := normalize(candidate)
		if folded == "" && normalized == "" {
			continue
		}
		prepared = append(prepared, preparedCandidate{folded: folded, normalized: normalized})
	}
	if len(prepared) == 0 {
		return false
	}
	for _, token := range tokens {
		if !tokenMatchesAny(token, prepared) {
			return false
		}
	}
	return true
}

type queryToken struct {
	folded     string
	normalized string
}

type preparedCandidate struct {
	folded     string
	normalized string
}

func queryTokens(query string) []queryToken {
	fields := strings.Fields(strings.TrimSpace(query))
	if len(fields) == 0 {
		return nil
	}
	tokens := make([]queryToken, 0, len(fields))
	for _, field := range fields {
		folded := strings.ToLower(strings.TrimSpace(field))
		normalized := normalize(field)
		if folded == "" && normalized == "" {
			continue
		}
		tokens = append(tokens, queryToken{folded: folded, normalized: normalized})
	}
	return tokens
}

func tokenMatchesAny(token queryToken, candidates []preparedCandidate) bool {
	for _, candidate := range candidates {
		if tokenMatchesCandidate(token, candidate) {
			return true
		}
	}
	return false
}

func tokenMatchesCandidate(token queryToken, candidate preparedCandidate) bool {
	if token.folded != "" && candidate.folded != "" && strings.Contains(candidate.folded, token.folded) {
		return true
	}
	if token.normalized == "" || candidate.normalized == "" {
		return false
	}
	return strings.Contains(candidate.normalized, token.normalized) || isSubsequence(token.normalized, candidate.normalized)
}

func normalize(value string) string {
	var out strings.Builder
	out.Grow(len(value))
	for _, r := range strings.ToLower(strings.TrimSpace(value)) {
		if unicode.IsLetter(r) || unicode.IsDigit(r) {
			out.WriteRune(r)
		}
	}
	return out.String()
}

func isSubsequence(needle, haystack string) bool {
	if needle == "" {
		return true
	}
	needleRunes := []rune(needle)
	needleIndex := 0
	for _, r := range haystack {
		if r != needleRunes[needleIndex] {
			continue
		}
		needleIndex++
		if needleIndex == len(needleRunes) {
			return true
		}
	}
	return false
}
