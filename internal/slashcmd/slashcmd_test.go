package slashcmd

import "testing"

func TestCycleSuggestionCyclesAcrossFullListAfterCompletion(t *testing.T) {
	suggestions := []Suggestion{{Insert: "/new"}, {Insert: "/sessions"}}
	selected, index, ok := CycleSuggestion("/new", 0, []Suggestion{{Insert: "/new"}}, suggestions, 1)
	if !ok {
		t.Fatalf("CycleSuggestion() ok = false")
	}
	if selected.Insert != "/sessions" || index != 1 {
		t.Fatalf("selected = %#v index=%d, want /sessions index 1", selected, index)
	}
}

func TestResolveInputUsesSelectedPrefixBeforeParse(t *testing.T) {
	got := ResolveInput("/se", Suggestion{Insert: "/sessions"}, true, func(string) bool { return false })
	if got != "/sessions" {
		t.Fatalf("ResolveInput() = %q, want /sessions", got)
	}
}

func TestSuggestionWindowTracksSelection(t *testing.T) {
	start, end := SuggestionWindow(5, 10, 4)
	if start != 2 || end != 6 {
		t.Fatalf("window = %d:%d, want 2:6", start, end)
	}
}
