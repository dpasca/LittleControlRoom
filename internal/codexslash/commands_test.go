package codexslash

import (
	"strings"
	"testing"
)

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

func TestSuggestionsIncludeDevLCReviewCommandWhenPrefixed(t *testing.T) {
	suggestions := Suggestions("/dev")
	found := false
	for _, suggestion := range suggestions {
		if suggestion.Insert == "/dev-lcreview" {
			found = true
			break
		}
	}
	if !found {
		t.Fatalf("Suggestions(/dev) should include /dev-lcreview: %#v", suggestions)
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

func TestSuggestionsIncludeGoalCommand(t *testing.T) {
	suggestions := Suggestions("/")
	found := false
	for _, suggestion := range suggestions {
		if suggestion.Insert == "/goal" {
			found = true
			break
		}
	}
	if !found {
		t.Fatalf("Suggestions(/) should include /goal: %#v", suggestions)
	}
}

func TestGoalSuggestionsFilterSubcommandPrefix(t *testing.T) {
	suggestions := Suggestions("/goal cle")
	if len(suggestions) != 1 {
		t.Fatalf("Suggestions(/goal cle) returned %d suggestions, want 1: %#v", len(suggestions), suggestions)
	}
	if suggestions[0].Insert != "/goal clear" {
		t.Fatalf("Suggestions(/goal cle)[0].Insert = %q, want /goal clear", suggestions[0].Insert)
	}
}

func TestSuggestionsIncludeSettingsCommand(t *testing.T) {
	suggestions := Suggestions("/")
	found := false
	for _, suggestion := range suggestions {
		if suggestion.Insert == "/settings" {
			found = true
			break
		}
	}
	if !found {
		t.Fatalf("Suggestions(/) should include /settings: %#v", suggestions)
	}
}

func TestSuggestionsIncludeTerminalCommand(t *testing.T) {
	suggestions := Suggestions("/")
	found := false
	for _, suggestion := range suggestions {
		if suggestion.Insert == "/terminal" {
			found = true
			break
		}
	}
	if !found {
		t.Fatalf("Suggestions(/) should include /terminal: %#v", suggestions)
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

func TestParseTerminalCommand(t *testing.T) {
	inv, err := Parse("/terminal")
	if err != nil {
		t.Fatalf("Parse(/terminal) error = %v", err)
	}
	if inv.Kind != KindTerminal {
		t.Fatalf("Parse(/terminal) kind = %q, want %q", inv.Kind, KindTerminal)
	}
	if inv.Canonical != "/terminal" {
		t.Fatalf("Parse(/terminal) canonical = %q, want /terminal", inv.Canonical)
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

func TestParseShowStatusCommand(t *testing.T) {
	inv, err := Parse("/show-status")
	if err != nil {
		t.Fatalf("Parse(/show-status) error = %v", err)
	}
	if inv.Kind != KindShowStatus {
		t.Fatalf("Parse(/show-status) kind = %q, want %q", inv.Kind, KindShowStatus)
	}
	if inv.Canonical != "/show-status" {
		t.Fatalf("Parse(/show-status) canonical = %q, want /show-status", inv.Canonical)
	}
}

func TestParseDevShowStatusCommand(t *testing.T) {
	inv, err := Parse("/dev-show-status")
	if err != nil {
		t.Fatalf("Parse(/dev-show-status) error = %v", err)
	}
	if inv.Kind != KindShowStatus {
		t.Fatalf("Parse(/dev-show-status) kind = %q, want %q", inv.Kind, KindShowStatus)
	}
	if inv.Canonical != "/show-status" {
		t.Fatalf("Parse(/dev-show-status) canonical = %q, want /show-status", inv.Canonical)
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

func TestParseDevLCReviewCommand(t *testing.T) {
	inv, err := Parse("/dev-lcreview")
	if err != nil {
		t.Fatalf("Parse(/dev-lcreview) error = %v", err)
	}
	if inv.Kind != KindDevLCReview {
		t.Fatalf("Parse(/dev-lcreview) kind = %q, want %q", inv.Kind, KindDevLCReview)
	}
	if inv.Canonical != "/dev-lcreview" {
		t.Fatalf("Parse(/dev-lcreview) canonical = %q, want /dev-lcreview", inv.Canonical)
	}
}

func TestParsePermissionsCommand(t *testing.T) {
	inv, err := Parse("/permissions medium")
	if err != nil {
		t.Fatalf("Parse(/permissions medium) error = %v", err)
	}
	if inv.Kind != KindPermissions {
		t.Fatalf("Parse(/permissions medium) kind = %q, want %q", inv.Kind, KindPermissions)
	}
	if inv.PermissionLevel != "medium" {
		t.Fatalf("Parse(/permissions medium) level = %q, want medium", inv.PermissionLevel)
	}
	if inv.Canonical != "/permissions medium" {
		t.Fatalf("Parse(/permissions medium) canonical = %q, want /permissions medium", inv.Canonical)
	}
}

func TestParsePermissionsHelpCommand(t *testing.T) {
	inv, err := Parse("/permissions")
	if err != nil {
		t.Fatalf("Parse(/permissions) error = %v", err)
	}
	if inv.Kind != KindPermissions {
		t.Fatalf("Parse(/permissions) kind = %q, want %q", inv.Kind, KindPermissions)
	}
	if inv.PermissionLevel != "" {
		t.Fatalf("Parse(/permissions) level = %q, want empty", inv.PermissionLevel)
	}
	if inv.Canonical != "/permissions" {
		t.Fatalf("Parse(/permissions) canonical = %q, want /permissions", inv.Canonical)
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

func TestParseSettingsCommand(t *testing.T) {
	inv, err := Parse("/settings")
	if err != nil {
		t.Fatalf("Parse(/settings) error = %v", err)
	}
	if inv.Kind != KindSettings {
		t.Fatalf("Parse(/settings) kind = %q, want %q", inv.Kind, KindSettings)
	}
	if inv.Canonical != "/settings" {
		t.Fatalf("Parse(/settings) canonical = %q, want /settings", inv.Canonical)
	}
}

func TestParseGoalStatusCommand(t *testing.T) {
	inv, err := Parse("/goal status")
	if err != nil {
		t.Fatalf("Parse(/goal status) error = %v", err)
	}
	if inv.Kind != KindGoal {
		t.Fatalf("Parse(/goal status) kind = %q, want %q", inv.Kind, KindGoal)
	}
	if inv.GoalAction != GoalActionShow {
		t.Fatalf("Parse(/goal status) action = %q, want %q", inv.GoalAction, GoalActionShow)
	}
	if inv.Canonical != "/goal" {
		t.Fatalf("Parse(/goal status) canonical = %q, want /goal", inv.Canonical)
	}
}

func TestParseGoalClearCommand(t *testing.T) {
	inv, err := Parse("/goal clear")
	if err != nil {
		t.Fatalf("Parse(/goal clear) error = %v", err)
	}
	if inv.Kind != KindGoal {
		t.Fatalf("Parse(/goal clear) kind = %q, want %q", inv.Kind, KindGoal)
	}
	if inv.GoalAction != GoalActionClear {
		t.Fatalf("Parse(/goal clear) action = %q, want %q", inv.GoalAction, GoalActionClear)
	}
	if inv.Canonical != "/goal clear" {
		t.Fatalf("Parse(/goal clear) canonical = %q, want /goal clear", inv.Canonical)
	}
}

func TestParseGoalStopCommandClearsGoal(t *testing.T) {
	inv, err := Parse("/goal stop")
	if err != nil {
		t.Fatalf("Parse(/goal stop) error = %v", err)
	}
	if inv.Kind != KindGoal {
		t.Fatalf("Parse(/goal stop) kind = %q, want %q", inv.Kind, KindGoal)
	}
	if inv.GoalAction != GoalActionClear {
		t.Fatalf("Parse(/goal stop) action = %q, want %q", inv.GoalAction, GoalActionClear)
	}
	if inv.Canonical != "/goal clear" {
		t.Fatalf("Parse(/goal stop) canonical = %q, want /goal clear", inv.Canonical)
	}
}

func TestParsePartialGoalActionDoesNotBecomeObjective(t *testing.T) {
	_, err := Parse("/goal cle")
	if err == nil {
		t.Fatalf("Parse(/goal cle) error = nil, want partial action error")
	}
	if !strings.Contains(err.Error(), "did you mean /goal clear") {
		t.Fatalf("Parse(/goal cle) error = %v, want clear suggestion", err)
	}
}

func TestParseGoalResumeCommand(t *testing.T) {
	inv, err := Parse("/goal resume")
	if err != nil {
		t.Fatalf("Parse(/goal resume) error = %v", err)
	}
	if inv.Kind != KindGoal {
		t.Fatalf("Parse(/goal resume) kind = %q, want %q", inv.Kind, KindGoal)
	}
	if inv.GoalAction != GoalActionResume {
		t.Fatalf("Parse(/goal resume) action = %q, want %q", inv.GoalAction, GoalActionResume)
	}
	if inv.Canonical != "/goal resume" {
		t.Fatalf("Parse(/goal resume) canonical = %q, want /goal resume", inv.Canonical)
	}
}

func TestParseGoalPauseCommand(t *testing.T) {
	inv, err := Parse("/goal pause")
	if err != nil {
		t.Fatalf("Parse(/goal pause) error = %v", err)
	}
	if inv.Kind != KindGoal {
		t.Fatalf("Parse(/goal pause) kind = %q, want %q", inv.Kind, KindGoal)
	}
	if inv.GoalAction != GoalActionPause {
		t.Fatalf("Parse(/goal pause) action = %q, want %q", inv.GoalAction, GoalActionPause)
	}
	if inv.Canonical != "/goal pause" {
		t.Fatalf("Parse(/goal pause) canonical = %q, want /goal pause", inv.Canonical)
	}
}

func TestParseGoalSetCommandWithBudget(t *testing.T) {
	inv, err := Parse("/goal ship this feature --budget 5000")
	if err != nil {
		t.Fatalf("Parse(/goal ship this feature --budget 5000) error = %v", err)
	}
	if inv.Kind != KindGoal {
		t.Fatalf("Parse(/goal ...) kind = %q, want %q", inv.Kind, KindGoal)
	}
	if inv.GoalAction != GoalActionSet {
		t.Fatalf("Parse(/goal ...) action = %q, want %q", inv.GoalAction, GoalActionSet)
	}
	if inv.GoalObjective != "ship this feature" {
		t.Fatalf("Parse(/goal ...) objective = %q, want ship this feature", inv.GoalObjective)
	}
	if inv.GoalTokenBudget == nil || *inv.GoalTokenBudget != 5000 {
		t.Fatalf("Parse(/goal ...) token budget = %v, want 5000", inv.GoalTokenBudget)
	}
	if inv.Canonical != "/goal ship this feature --budget 5000" {
		t.Fatalf("Parse(/goal ...) canonical = %q, want /goal ship this feature --budget 5000", inv.Canonical)
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

func TestParseSessionsAliasReturnsResumeInvocation(t *testing.T) {
	inv, err := Parse("/sessions ses_demo")
	if err != nil {
		t.Fatalf("Parse(/sessions ses_demo) error = %v", err)
	}
	if inv.Kind != KindResume {
		t.Fatalf("Parse(/sessions ses_demo) kind = %q, want %q", inv.Kind, KindResume)
	}
	if inv.SessionID != "ses_demo" {
		t.Fatalf("Parse(/sessions ses_demo) session id = %q, want %q", inv.SessionID, "ses_demo")
	}
	if inv.Canonical != "/resume ses_demo" {
		t.Fatalf("Parse(/sessions ses_demo) canonical = %q, want /resume ses_demo", inv.Canonical)
	}
}

func TestSuggestionsExposeSessionAliasWhenPrefixMatches(t *testing.T) {
	suggestions := Suggestions("/sess")
	if len(suggestions) != 2 {
		t.Fatalf("Suggestions(/sess) returned %d suggestions, want 2", len(suggestions))
	}
	if suggestions[0].Insert != "/sessions" {
		t.Fatalf("Suggestions(/sess)[0].Insert = %q, want /sessions", suggestions[0].Insert)
	}
	if suggestions[1].Insert != "/session" {
		t.Fatalf("Suggestions(/sess)[1].Insert = %q, want /session", suggestions[1].Insert)
	}
}
