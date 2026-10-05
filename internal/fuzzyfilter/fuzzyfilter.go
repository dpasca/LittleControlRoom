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
	prepared := prepareCandidates(candidates)
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

// MatchWithFragments is Match with a second, stricter candidate group. Tokens
// may fuzzy-match candidates, but fragmentOnly candidates (long text such as a
// folder path, where ordered-character matches are nearly always accidental)
// only match when they contain the token as an exact or normalized fragment.
func MatchWithFragments(query string, candidates, fragmentOnly []string) bool {
	tokens := queryTokens(query)
	if len(tokens) == 0 {
		return true
	}
	fuzzy := prepareCandidates(candidates)
	strict := prepareCandidates(fragmentOnly)
	for _, token := range tokens {
		if tokenMatchesAny(token, fuzzy) {
			continue
		}
		matched := false
		for _, candidate := range strict {
			if (token.folded != "" && strings.Contains(candidate.folded, token.folded)) ||
				(token.normalized != "" && strings.Contains(candidate.normalized, token.normalized)) {
				matched = true
				break
			}
		}
		if !matched {
			return false
		}
	}
	return true
}

func prepareCandidates(candidates []string) []preparedCandidate {
	prepared := make([]preparedCandidate, 0, len(candidates))
	for _, candidate := range candidates {
		folded := strings.ToLower(strings.TrimSpace(candidate))
		normalized := normalize(candidate)
		if folded == "" && normalized == "" {
			continue
		}
		prepared = append(prepared, preparedCandidate{folded: folded, normalized: normalized})
	}
	return prepared
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
