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
	if len(got) != 1 || got[0] != "/style Terse" {
		t.Fatalf("suggestions = %v, want only /style Terse", got)
	}
}

func TestOutputStyleSuggestionsListAllWithoutPrefix(t *testing.T) {
	got := styleInserts(OutputStyleSuggestions("", styleChoices("default", "Terse")))
	if len(got) != 2 || got[0] != "/style default" || got[1] != "/style Terse" {
		t.Fatalf("suggestions = %v, want every discovered style", got)
	}
}

func TestOutputStyleSuggestionsFallBackWhenNothingMatches(t *testing.T) {
	got := styleInserts(OutputStyleSuggestions("zzz", styleChoices("default", "Terse")))
	if len(got) == 0 {
		t.Fatal("suggestions = none, want the command to stay discoverable")
	}
}

func TestOutputStyleSuggestionsForInputNeedsTrailingSpace(t *testing.T) {
	names := styleChoices("default", "Terse")
	if _, ok := OutputStyleSuggestionsForInput("/sty", names); ok {
		t.Fatal("completing the command name must not switch to argument completion")
	}
	if _, ok := OutputStyleSuggestionsForInput("/style ", names); !ok {
		t.Fatal("a trailing space should start argument completion")
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
	if len(got) != 1 || got[0].Summary != "Answer first, minimal prose" {
		t.Fatalf("suggestions = %+v, want the style description as the hint", got)
	}
}

func TestOutputStyleSuggestionsFallBackWhenDescriptionMissing(t *testing.T) {
	got := OutputStyleSuggestions("", styleChoices("Terse"))
	if len(got) != 1 || got[0].Summary == "" {
		t.Fatalf("suggestions = %+v, want a usable hint even without a description", got)
	}
}
