package bossslash

import "testing"

func TestSuggestionsIncludeSessionCommands(t *testing.T) {
	suggestions := Suggestions("/")
	for _, want := range []string{"/new", "/sessions", "/help", "/boss"} {
		found := false
		for _, suggestion := range suggestions {
			if suggestion.Insert == want {
				found = true
				break
			}
		}
		if !found {
			t.Fatalf("Suggestions(/) missing %s: %#v", want, suggestions)
		}
	}
}

func TestSuggestionsExposeHiddenAliasesByPrefix(t *testing.T) {
	suggestions := Suggestions("/sess")
	found := false
	for _, suggestion := range suggestions {
		if suggestion.Insert == "/session" {
			found = true
			break
		}
	}
	if !found {
		t.Fatalf("Suggestions(/sess) = %#v, want hidden /session alias", suggestions)
	}
}

func TestParseNewWithPrompt(t *testing.T) {
	inv, err := Parse("/new what matters now?")
	if err != nil {
		t.Fatalf("Parse(/new prompt) error = %v", err)
	}
	if inv.Kind != KindNew || inv.Prompt != "what matters now?" {
		t.Fatalf("invocation = %#v, want new prompt", inv)
	}
	if inv.Canonical != "/new what matters now?" {
		t.Fatalf("canonical = %q", inv.Canonical)
	}
}

func TestParseSessionAliases(t *testing.T) {
	for _, raw := range []string{"/sessions boss_123", "/session boss_123", "/resume boss_123"} {
		inv, err := Parse(raw)
		if err != nil {
			t.Fatalf("Parse(%q) error = %v", raw, err)
		}
		if inv.Kind != KindSessions || inv.SessionID != "boss_123" {
			t.Fatalf("Parse(%q) = %#v, want sessions boss_123", raw, inv)
		}
		if inv.Canonical != "/sessions boss_123" {
			t.Fatalf("Parse(%q) canonical = %q, want /sessions boss_123", raw, inv.Canonical)
		}
	}
}

func TestParseBossOffCloses(t *testing.T) {
	inv, err := Parse("/boss off")
	if err != nil {
		t.Fatalf("Parse(/boss off) error = %v", err)
	}
	if inv.Kind != KindClose {
		t.Fatalf("kind = %q, want close", inv.Kind)
	}
}
