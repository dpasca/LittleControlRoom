package pasteplaceholder

import "strings"

// Strip removes LCR's collapsed multi-line paste display placeholders.
func Strip(text string) string {
	if text == "" {
		return ""
	}
	var b strings.Builder
	for i := 0; i < len(text); {
		if text[i] == '[' {
			if end, ok := consume(text, i); ok {
				b.WriteByte(' ')
				i = end
				continue
			}
		}
		b.WriteByte(text[i])
		i++
	}
	return b.String()
}

func consume(text string, start int) (int, bool) {
	if start < 0 || start >= len(text) || text[start] != '[' {
		return start, false
	}
	relativeEnd := strings.IndexByte(text[start:], ']')
	if relativeEnd < 0 {
		return start, false
	}
	end := start + relativeEnd
	if !isBody(text[start+1 : end]) {
		return start, false
	}
	return end + 1, true
}

func isBody(body string) bool {
	parts := strings.Fields(body)
	if len(parts) != 3 || parts[2] != "pasted" {
		return false
	}
	switch parts[1] {
	case "line":
		return parts[0] == "1"
	case "lines":
		return positiveDecimal(parts[0]) && parts[0] != "1"
	default:
		return false
	}
}

func positiveDecimal(value string) bool {
	if value == "" {
		return false
	}
	for _, r := range value {
		if r < '0' || r > '9' {
			return false
		}
	}
	return strings.TrimLeft(value, "0") != ""
}
