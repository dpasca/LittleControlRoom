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

func TestParseSessionAliasReturnsResumeInvocation(t *testing.T) {
	inv, err := Parse("/session ses_demo")
	if err != nil {
		t.Fatalf("Parse(/session ses_demo) error = %v", err)
	}
	if inv.Kind != KindResume {
		t.Fatalf("Parse(/session ses_demo) kind = %q, want %q", inv.Kind, KindResume)
	}
	if inv.SessionID != "ses_demo" {
		t.Fatalf("Parse(/session ses_demo) session id = %q, want %q", inv.SessionID, "ses_demo")
	}
	if inv.Canonical != "/resume ses_demo" {
		t.Fatalf("Parse(/session ses_demo) canonical = %q, want /resume ses_demo", inv.Canonical)
	}
}

func TestSuggestionsExposeSessionAliasWhenPrefixMatches(t *testing.T) {
	suggestions := Suggestions("/sess")
	if len(suggestions) != 1 {
		t.Fatalf("Suggestions(/sess) returned %d suggestions, want 1", len(suggestions))
	}
	if suggestions[0].Insert != "/session" {
		t.Fatalf("Suggestions(/sess)[0].Insert = %q, want /session", suggestions[0].Insert)
	}
}
