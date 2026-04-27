package slashcmd

import "strings"

type Spec struct {
	Name    string
	Usage   string
	Summary string
	Hidden  bool
}

type Suggestion struct {
	Insert  string
	Display string
	Summary string
}

type Choice struct {
	Value   string
	Summary string
}

func NewChoice(value, summary string) Choice {
	return Choice{Value: value, Summary: summary}
}

func NameSuggestions(specs []Spec, prefix string) []Suggestion {
	out := make([]Suggestion, 0, len(specs))
	for _, spec := range specs {
		if prefix == "" && spec.Hidden {
			continue
		}
		if prefix != "" && !strings.HasPrefix(spec.Name, prefix) {
			continue
		}
		out = append(out, Suggestion{
			Insert:  "/" + spec.Name,
			Display: spec.Usage,
			Summary: spec.Summary,
		})
	}
	return out
}

func EnumSuggestions(prefix, argPrefix string, choices ...Choice) []Suggestion {
	out := make([]Suggestion, 0, len(choices))
	for _, ch := range choices {
		if argPrefix != "" && !strings.HasPrefix(ch.Value, argPrefix) {
			continue
		}
		insert := prefix + ch.Value
		out = append(out, Suggestion{
			Insert:  insert,
			Display: insert,
			Summary: ch.Summary,
		})
	}
	return out
}

func SplitCommandBody(body string) (string, string) {
	for i, r := range body {
		if r == ' ' || r == '\t' {
			return body[:i], strings.TrimSpace(body[i+1:])
		}
	}
	return body, ""
}

func CanonicalCommand(name, rawArgs string) string {
	args := strings.TrimSpace(rawArgs)
	if args == "" {
		return "/" + name
	}
	return "/" + name + " " + args
}

func SuggestionIndex(suggestions []Suggestion, raw string) int {
	for i, suggestion := range suggestions {
		if strings.EqualFold(strings.TrimSpace(suggestion.Insert), strings.TrimSpace(raw)) {
			return i
		}
	}
	return -1
}

func SuggestionWindow(selected, total, limit int) (int, int) {
	if total <= 0 {
		return 0, 0
	}
	if limit <= 0 || limit > total {
		limit = total
	}
	start := 0
	if selected >= limit {
		start = selected - limit + 1
	}
	maxStart := total - limit
	if start > maxStart {
		start = maxStart
	}
	if start < 0 {
		start = 0
	}
	return start, start + limit
}

func CycleSuggestion(current string, selected int, suggestions, all []Suggestion, delta int) (Suggestion, int, bool) {
	if len(suggestions) == 0 {
		return Suggestion{}, 0, false
	}
	if selected < 0 || selected >= len(suggestions) {
		selected = 0
	}
	current = strings.TrimSpace(current)
	if index := SuggestionIndex(suggestions, current); index >= 0 {
		selected = index
	}
	if len(suggestions) == 1 && len(all) > 1 {
		if index := SuggestionIndex(all, current); index >= 0 {
			suggestions = all
			selected = index
		}
	}
	if len(suggestions) > 1 && strings.EqualFold(current, suggestions[selected].Insert) {
		selected += delta
		if selected < 0 {
			selected = len(suggestions) - 1
		}
		if selected >= len(suggestions) {
			selected = 0
		}
	}
	return suggestions[selected], selected, true
}

func ResolveInput(raw string, selected Suggestion, hasSelected bool, parseOK func(string) bool) string {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return raw
	}
	if hasSelected {
		insert := strings.TrimSpace(selected.Insert)
		if strings.HasPrefix(strings.ToLower(insert), strings.ToLower(raw)) && !strings.EqualFold(insert, raw) {
			return selected.Insert
		}
	}
	if parseOK != nil && parseOK(raw) {
		return raw
	}
	if !hasSelected {
		return raw
	}
	if strings.HasPrefix(strings.ToLower(selected.Insert), strings.ToLower(raw)) {
		return selected.Insert
	}
	return raw
}
