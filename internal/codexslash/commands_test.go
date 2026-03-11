package codexslash

import "testing"

func TestSuggestionsIncludeModelCommand(t *testing.T) {
	suggestions := Suggestions("/")
	found := false
	for _, suggestion := range suggestions {
		if suggestion.Insert == "/model" {
			found = true
			break
		}
	}
	if !found {
		t.Fatalf("Suggestions(/) should include /model: %#v", suggestions)
	}
}

func TestParseModelCommand(t *testing.T) {
	inv, err := Parse("/model")
	if err != nil {
		t.Fatalf("Parse(/model) error = %v", err)
	}
	if inv.Kind != KindModel {
		t.Fatalf("Parse(/model) kind = %q, want %q", inv.Kind, KindModel)
	}
	if inv.Canonical != "/model" {
		t.Fatalf("Parse(/model) canonical = %q, want /model", inv.Canonical)
	}
}
