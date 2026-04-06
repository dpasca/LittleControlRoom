package llm

import (
	"encoding/json"
	"errors"
	"fmt"
	"strings"
)

const jsonOutputPreviewLimit = 120

func DecodeJSONObjectOutput(outputText string, decoded any) error {
	sanitized := strings.TrimSpace(outputText)
	if sanitized == "" {
		return errors.New("empty JSON output")
	}

	candidates := make([]string, 0, 4)
	candidates = append(candidates, sanitized)
	if thinkingStripped := StripThinkingBlocks(sanitized); thinkingStripped != "" && thinkingStripped != sanitized {
		candidates = append(candidates, thinkingStripped)
	}
	if fenced := StripMarkdownCodeBlock(sanitized); fenced != "" && fenced != sanitized {
		candidates = append(candidates, fenced)
	}
	for _, extracted := range extractJSONObjectCandidates(sanitized) {
		if extracted != sanitized {
			candidates = append(candidates, extracted)
		}
	}
	if len(candidates) > 1 {
		for _, extracted := range extractJSONObjectCandidates(candidates[1]) {
			if extracted != candidates[1] {
				candidates = append(candidates, extracted)
			}
		}
	}

	var firstErr error
	seen := make(map[string]struct{}, len(candidates))
	for _, candidate := range candidates {
		candidate = strings.TrimSpace(candidate)
		if candidate == "" {
			continue
		}
		if _, ok := seen[candidate]; ok {
			continue
		}
		seen[candidate] = struct{}{}
		if err := json.Unmarshal([]byte(candidate), decoded); err == nil {
			return nil
		} else if firstErr == nil {
			firstErr = err
		}
	}

	preview := clippedSingleLinePreview(sanitized, jsonOutputPreviewLimit)
	if firstErr == nil {
		return fmt.Errorf("failed to decode JSON output (preview=%q)", preview)
	}
	return fmt.Errorf("failed to decode JSON output (preview=%q): %w", preview, firstErr)
}

func StripMarkdownCodeBlock(text string) string {
	text = strings.TrimSpace(text)
	if !strings.HasPrefix(text, "```") {
		return text
	}
	text = strings.TrimPrefix(text, "```")
	text = strings.TrimSpace(text)
	if newline := strings.IndexByte(text, '\n'); newline >= 0 {
		firstLine := strings.TrimSpace(text[:newline])
		if !strings.HasPrefix(firstLine, "{") && !strings.HasPrefix(firstLine, "[") {
			text = text[newline+1:]
		}
	}
	if end := strings.LastIndex(text, "```"); end >= 0 {
		text = text[:end]
	}
	return strings.TrimSpace(text)
}

func StripThinkingBlocks(text string) string {
	text = strings.TrimSpace(text)
	for {
		if !strings.HasPrefix(text, "<think>") {
			return text
		}
		end := strings.Index(text, "</think>")
		if end == -1 {
			return text
		}
		text = strings.TrimSpace(text[end+len("</think>"):])
	}
}

func extractJSONObjectCandidates(text string) []string {
	start := -1
	depth := 0
	inString := false
	escaped := false
	candidates := make([]string, 0, 2)

	for i, r := range text {
		if escaped {
			escaped = false
			continue
		}
		if r == '\\' {
			escaped = inString
			continue
		}
		if r == '"' {
			inString = !inString
			continue
		}
		if inString {
			continue
		}
		switch r {
		case '{':
			if depth == 0 {
				start = i
			}
			depth++
		case '}':
			if depth == 0 {
				continue
			}
			depth--
			if depth == 0 && start >= 0 {
				candidates = append(candidates, strings.TrimSpace(text[start:i+len(string(r))]))
				start = -1
			}
		}
	}

	return candidates
}

func clippedSingleLinePreview(text string, limit int) string {
	text = strings.TrimSpace(text)
	text = strings.Join(strings.Fields(text), " ")
	if limit <= 0 || len(text) <= limit {
		return text
	}
	if limit <= 3 {
		return text[:limit]
	}
	return text[:limit-3] + "..."
}
