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

func TestSuggestionsIncludeReconnectCommand(t *testing.T) {
	suggestions := Suggestions("/")
	found := false
	for _, suggestion := range suggestions {
		if suggestion.Insert == "/reconnect" {
			found = true
			break
		}
	}
	if !found {
		t.Fatalf("Suggestions(/) should include /reconnect: %#v", suggestions)
	}
}

func TestSuggestionsIncludeReviewCommand(t *testing.T) {
	suggestions := Suggestions("/")
	found := false
	for _, suggestion := range suggestions {
		if suggestion.Insert == "/review" {
			found = true
			break
		}
	}
	if !found {
		t.Fatalf("Suggestions(/) should include /review: %#v", suggestions)
	}
}

func TestSuggestionsIncludeBossCommand(t *testing.T) {
	suggestions := Suggestions("/")
	found := false
	for _, suggestion := range suggestions {
		if suggestion.Insert == "/boss" {
			found = true
			break
		}
	}
	if !found {
		t.Fatalf("Suggestions(/) should include /boss: %#v", suggestions)
	}
}

func TestSuggestionsIncludeSkillsCommand(t *testing.T) {
	suggestions := Suggestions("/")
	found := false
	for _, suggestion := range suggestions {
		if suggestion.Insert == "/skills" {
			found = true
			break
		}
	}
	if !found {
		t.Fatalf("Suggestions(/) should include /skills: %#v", suggestions)
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

func TestParseReconnectCommand(t *testing.T) {
	inv, err := Parse("/reconnect")
	if err != nil {
		t.Fatalf("Parse(/reconnect) error = %v", err)
	}
	if inv.Kind != KindReconnect {
		t.Fatalf("Parse(/reconnect) kind = %q, want %q", inv.Kind, KindReconnect)
	}
	if inv.Canonical != "/reconnect" {
		t.Fatalf("Parse(/reconnect) canonical = %q, want /reconnect", inv.Canonical)
	}
}

func TestParseReviewCommand(t *testing.T) {
	inv, err := Parse("/review")
	if err != nil {
		t.Fatalf("Parse(/review) error = %v", err)
	}
	if inv.Kind != KindReview {
		t.Fatalf("Parse(/review) kind = %q, want %q", inv.Kind, KindReview)
	}
	if inv.Canonical != "/review" {
		t.Fatalf("Parse(/review) canonical = %q, want /review", inv.Canonical)
	}
}

func TestParseBossCommand(t *testing.T) {
	inv, err := Parse("/boss")
	if err != nil {
		t.Fatalf("Parse(/boss) error = %v", err)
	}
	if inv.Kind != KindBoss {
		t.Fatalf("Parse(/boss) kind = %q, want %q", inv.Kind, KindBoss)
	}
	if inv.Canonical != "/boss" {
		t.Fatalf("Parse(/boss) canonical = %q, want /boss", inv.Canonical)
	}
}

func TestParseSkillsCommand(t *testing.T) {
	inv, err := Parse("/skills")
	if err != nil {
		t.Fatalf("Parse(/skills) error = %v", err)
	}
	if inv.Kind != KindSkills {
		t.Fatalf("Parse(/skills) kind = %q, want %q", inv.Kind, KindSkills)
	}
	if inv.Canonical != "/skills" {
		t.Fatalf("Parse(/skills) canonical = %q, want /skills", inv.Canonical)
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
