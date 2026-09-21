package codexslash

import (
	"testing"

	"lcroom/internal/slashcmd"
)

func styleChoices(pairs ...string) []slashcmd.Choice {
	out := make([]slashcmd.Choice, 0, len(pairs))
	for _, name := range pairs {
		out = append(out, slashcmd.NewChoice(name, ""))
	}
	return out
}

func styleInserts(suggestions []Suggestion) []string {
	out := make([]string, 0, len(suggestions))
	for _, s := range suggestions {
		out = append(out, s.Insert)
	}
	return out
}

// Typing a lowercase prefix must complete to the exact stored name, because
// Claude Code matches style names case-sensitively.
func TestOutputStyleSuggestionsCompleteCaseInsensitively(t *testing.T) {
	got := styleInserts(OutputStyleSuggestions("te", styleChoices("default", "Terse", "Verbose")))
	// The trailing entry is the bare /style status form, always kept last.
	if len(got) != 2 || got[0] != "/style Terse" {
		t.Fatalf("suggestions = %v, want /style Terse then the status form", got)
	}
}

func TestOutputStyleSuggestionsListAllWithoutPrefix(t *testing.T) {
	got := styleInserts(OutputStyleSuggestions("", styleChoices("default", "Terse")))
	if len(got) != 3 || got[0] != "/style default" || got[1] != "/style Terse" || got[2] != "/style" {
		t.Fatalf("suggestions = %v, want every discovered style then the status form", got)
	}
}

func TestOutputStyleSuggestionsFallBackWhenNothingMatches(t *testing.T) {
	got := styleInserts(OutputStyleSuggestions("zzz", styleChoices("default", "Terse")))
	if len(got) == 0 {
		t.Fatal("suggestions = none, want the command to stay discoverable")
	}
}

func TestOutputStyleSuggestionsForInputTriggersOnTheCompleteCommand(t *testing.T) {
	names := styleChoices("default", "Terse")
	if _, ok := OutputStyleSuggestionsForInput("/sty", names); ok {
		t.Fatal("a partial command name must keep plain name completion")
	}
	// A bare /style is already an unambiguous command, so offering the style
	// names is the only way an unfamiliar user learns what exists.
	if _, ok := OutputStyleSuggestionsForInput("/style", names); !ok {
		t.Fatal("a bare /style should offer the style names")
	}
	if _, ok := OutputStyleSuggestionsForInput("/style ", names); !ok {
		t.Fatal("a trailing space should offer the style names")
	}
	if _, ok := OutputStyleSuggestionsForInput("/style Te", names); !ok {
		t.Fatal("a typed argument should be completed")
	}
}

func TestOutputStyleSuggestionsForInputIgnoresOtherCommands(t *testing.T) {
	if _, ok := OutputStyleSuggestionsForInput("/model ", styleChoices("Terse")); ok {
		t.Fatal("/model must keep its own suggestions")
	}
}

// A pane with no discovered styles (non-Claude, or no style files) keeps the
// generic suggestion list rather than showing an argument completer.
func TestOutputStyleSuggestionsForInputIgnoresEmptyNameList(t *testing.T) {
	if _, ok := OutputStyleSuggestionsForInput("/style ", nil); ok {
		t.Fatal("no discovered styles should leave the generic suggestions in place")
	}
}

// A style's frontmatter description is the most useful hint available, so the
// completion list shows it instead of generic text.
func TestOutputStyleSuggestionsUseDescriptionAsHint(t *testing.T) {
	choices := []slashcmd.Choice{slashcmd.NewChoice("Terse", "Answer first, minimal prose")}
	got := OutputStyleSuggestions("", choices)
	if len(got) == 0 || got[0].Summary != "Answer first, minimal prose" {
		t.Fatalf("suggestions = %+v, want the style description as the hint", got)
	}
}

func TestOutputStyleSuggestionsFallBackWhenDescriptionMissing(t *testing.T) {
	got := OutputStyleSuggestions("", styleChoices("Terse"))
	if len(got) == 0 || got[0].Summary == "" {
		t.Fatalf("suggestions = %+v, want a usable hint even without a description", got)
	}
}

// Simulates pressing Tab repeatedly: each cycle feeds the applied insert back
// in as the new input. Every style must be reachable. Filtering on an exact
// name previously collapsed the list so only "default" and the bare command
// could ever be selected.
func TestOutputStyleSuggestionsTabCycleReachesEveryStyle(t *testing.T) {
	names := styleChoices("default", "Terse", "Verbose")
	current := "/style"
	seen := map[string]bool{}

	for i := 0; i < 8; i++ {
		suggestions, ok := OutputStyleSuggestionsForInput(current, names)
		if !ok {
			t.Fatalf("iteration %d: %q produced no style suggestions", i, current)
		}
		next, _, cycled := slashcmd.CycleSuggestion(current, 0, suggestions, suggestions, 1)
		if !cycled {
			t.Fatalf("iteration %d: cycling stopped at %q", i, current)
		}
		current = next.Insert
		seen[current] = true
	}

	for _, want := range []string{"/style default", "/style Terse", "/style Verbose"} {
		if !seen[want] {
			t.Fatalf("Tab cycling never reached %q; visited %v", want, seen)
		}
	}
}

// A partial prefix must still narrow the list; only a complete name stops
// filtering.
func TestOutputStyleSuggestionsStillFilterOnPartialPrefix(t *testing.T) {
	got := styleInserts(OutputStyleSuggestions("ter", styleChoices("default", "Terse", "Verbose")))
	if len(got) != 2 || got[0] != "/style Terse" {
		t.Fatalf("suggestions = %v, want only Terse plus the status form", got)
	}
}

func TestOutputStyleSuggestionsExactNameShowsAllForCycling(t *testing.T) {
	got := styleInserts(OutputStyleSuggestions("default", styleChoices("default", "Terse")))
	if len(got) != 3 {
		t.Fatalf("suggestions = %v, want the full list so Tab can move off default", got)
	}
}
