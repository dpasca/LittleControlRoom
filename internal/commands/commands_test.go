package commands

import (
	"testing"
	"time"
)

func TestParse(t *testing.T) {
	tests := []struct {
		name  string
		raw   string
		check func(t *testing.T, inv Invocation)
	}{
		{
			name: "refresh",
			raw:  "/refresh",
			check: func(t *testing.T, inv Invocation) {
				if inv.Kind != KindRefresh {
					t.Fatalf("kind = %s, want %s", inv.Kind, KindRefresh)
				}
			},
		},
		{
			name: "sort recent",
			raw:  "/sort recent",
			check: func(t *testing.T, inv Invocation) {
				if inv.Sort != SortRecent {
					t.Fatalf("sort = %s, want %s", inv.Sort, SortRecent)
				}
			},
		},
		{
			name: "view all",
			raw:  "/view all",
			check: func(t *testing.T, inv Invocation) {
				if inv.View != ViewAll {
					t.Fatalf("view = %s, want %s", inv.View, ViewAll)
				}
			},
		},
		{
			name: "settings",
			raw:  "/settings",
			check: func(t *testing.T, inv Invocation) {
				if inv.Kind != KindSettings {
					t.Fatalf("kind = %s, want %s", inv.Kind, KindSettings)
				}
			},
		},
		{
			name: "new project",
			raw:  "/new-project",
			check: func(t *testing.T, inv Invocation) {
				if inv.Kind != KindNewProject {
					t.Fatalf("kind = %s, want %s", inv.Kind, KindNewProject)
				}
			},
		},
		{
			name: "open",
			raw:  "/open",
			check: func(t *testing.T, inv Invocation) {
				if inv.Kind != KindOpen {
					t.Fatalf("kind = %s, want %s", inv.Kind, KindOpen)
				}
			},
		},
		{
			name: "diff",
			raw:  "/diff",
			check: func(t *testing.T, inv Invocation) {
				if inv.Kind != KindDiff {
					t.Fatalf("kind = %s, want %s", inv.Kind, KindDiff)
				}
			},
		},
		{
			name: "note",
			raw:  "/note",
			check: func(t *testing.T, inv Invocation) {
				if inv.Kind != KindNote {
					t.Fatalf("kind = %s, want %s", inv.Kind, KindNote)
				}
				if inv.Clear {
					t.Fatalf("clear = true, want false")
				}
			},
		},
		{
			name: "clear note",
			raw:  "/note clear",
			check: func(t *testing.T, inv Invocation) {
				if inv.Kind != KindNote {
					t.Fatalf("kind = %s, want %s", inv.Kind, KindNote)
				}
				if !inv.Clear {
					t.Fatalf("clear = false, want true")
				}
				if inv.Canonical != "/note clear" {
					t.Fatalf("canonical = %q, want /note clear", inv.Canonical)
				}
			},
		},
		{
			name: "commit custom message",
			raw:  "/commit Improve command palette scrolling",
			check: func(t *testing.T, inv Invocation) {
				if inv.Kind != KindCommit {
					t.Fatalf("kind = %s, want %s", inv.Kind, KindCommit)
				}
				if inv.Message != "Improve command palette scrolling" {
					t.Fatalf("message = %q, want custom subject", inv.Message)
				}
			},
		},
		{
			name: "finish without message",
			raw:  "/finish",
			check: func(t *testing.T, inv Invocation) {
				if inv.Kind != KindFinish {
					t.Fatalf("kind = %s, want %s", inv.Kind, KindFinish)
				}
				if inv.Message != "" {
					t.Fatalf("message = %q, want empty", inv.Message)
				}
			},
		},
		{
			name: "codex smart resume prompt",
			raw:  "/codex continue with the latest session",
			check: func(t *testing.T, inv Invocation) {
				if inv.Kind != KindCodex {
					t.Fatalf("kind = %s, want %s", inv.Kind, KindCodex)
				}
				if inv.Prompt != "continue with the latest session" {
					t.Fatalf("prompt = %q, want Codex prompt", inv.Prompt)
				}
			},
		},
		{
			name: "codex new alias",
			raw:  "/codex-start summarize the repo",
			check: func(t *testing.T, inv Invocation) {
				if inv.Kind != KindCodexNew {
					t.Fatalf("kind = %s, want %s", inv.Kind, KindCodexNew)
				}
				if inv.Prompt != "summarize the repo" {
					t.Fatalf("prompt = %q, want Codex prompt", inv.Prompt)
				}
				if inv.Canonical != "/codex-new summarize the repo" {
					t.Fatalf("canonical = %q, want canonical codex-new form", inv.Canonical)
				}
			},
		},
		{
			name: "opencode smart resume prompt",
			raw:  "/opencode continue with the latest session",
			check: func(t *testing.T, inv Invocation) {
				if inv.Kind != KindOpenCode {
					t.Fatalf("kind = %s, want %s", inv.Kind, KindOpenCode)
				}
				if inv.Prompt != "continue with the latest session" {
					t.Fatalf("prompt = %q, want OpenCode prompt", inv.Prompt)
				}
			},
		},
		{
			name: "opencode new alias",
			raw:  "/oc-start summarize the repo",
			check: func(t *testing.T, inv Invocation) {
				if inv.Kind != KindOpenCodeNew {
					t.Fatalf("kind = %s, want %s", inv.Kind, KindOpenCodeNew)
				}
				if inv.Prompt != "summarize the repo" {
					t.Fatalf("prompt = %q, want OpenCode prompt", inv.Prompt)
				}
				if inv.Canonical != "/opencode-new summarize the repo" {
					t.Fatalf("canonical = %q, want canonical opencode-new form", inv.Canonical)
				}
			},
		},
		{
			name: "snooze default",
			raw:  "/snooze",
			check: func(t *testing.T, inv Invocation) {
				if inv.Duration != time.Hour {
					t.Fatalf("duration = %s, want 1h", inv.Duration)
				}
			},
		},
		{
			name: "snooze one day",
			raw:  "/snooze 1d",
			check: func(t *testing.T, inv Invocation) {
				if inv.Duration != 24*time.Hour {
					t.Fatalf("duration = %s, want 24h", inv.Duration)
				}
			},
		},
		{
			name: "sessions default toggle",
			raw:  "/sessions",
			check: func(t *testing.T, inv Invocation) {
				if inv.Toggle != ToggleToggle {
					t.Fatalf("toggle = %s, want %s", inv.Toggle, ToggleToggle)
				}
			},
		},
		{
			name: "focus detail alias",
			raw:  "/focus details",
			check: func(t *testing.T, inv Invocation) {
				if inv.Focus != FocusDetail {
					t.Fatalf("focus = %s, want %s", inv.Focus, FocusDetail)
				}
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			inv, err := Parse(tt.raw)
			if err != nil {
				t.Fatalf("Parse(%q) error = %v", tt.raw, err)
			}
			tt.check(t, inv)
		})
	}
}

func TestParseRejectsUnknownCommand(t *testing.T) {
	if _, err := Parse("/wat"); err == nil {
		t.Fatalf("Parse(/wat) expected error")
	}
}

func TestSuggestionsCommandNames(t *testing.T) {
	got := Suggestions("/s")
	if len(got) == 0 {
		t.Fatalf("Suggestions(/s) returned none")
	}
	if got[0].Insert != "/sort" {
		t.Fatalf("first suggestion = %q, want /sort", got[0].Insert)
	}
}

func TestSuggestionsCommandArguments(t *testing.T) {
	got := Suggestions("/sort r")
	if len(got) != 1 {
		t.Fatalf("Suggestions(/sort r) len = %d, want 1", len(got))
	}
	if got[0].Insert != "/sort recent" {
		t.Fatalf("suggestion = %q, want /sort recent", got[0].Insert)
	}
}

func TestSuggestionsNoteClearArgument(t *testing.T) {
	got := Suggestions("/note c")
	if len(got) != 1 {
		t.Fatalf("Suggestions(/note c) len = %d, want 1", len(got))
	}
	if got[0].Insert != "/note clear" {
		t.Fatalf("suggestion = %q, want /note clear", got[0].Insert)
	}
}

func TestSuggestionsIncludeCommitWorkflowCommands(t *testing.T) {
	got := Suggestions("/f")
	if len(got) == 0 {
		t.Fatalf("Suggestions(/f) returned none")
	}
	if got[0].Insert != "/finish" {
		t.Fatalf("first /f suggestion = %q, want /finish", got[0].Insert)
	}
}

func TestSuggestionsIncludeSettingsCommand(t *testing.T) {
	got := Suggestions("/set")
	if len(got) == 0 {
		t.Fatalf("Suggestions(/set) returned none")
	}
	if got[0].Insert != "/settings" {
		t.Fatalf("first /set suggestion = %q, want /settings", got[0].Insert)
	}
}

func TestSuggestionsIncludeNewProjectCommand(t *testing.T) {
	got := Suggestions("/new")
	if len(got) == 0 {
		t.Fatalf("Suggestions(/new) returned none")
	}
	if got[0].Insert != "/new-project" {
		t.Fatalf("first /new suggestion = %q, want /new-project", got[0].Insert)
	}
}

func TestSuggestionsIncludeDiffCommand(t *testing.T) {
	got := Suggestions("/di")
	if len(got) == 0 {
		t.Fatalf("Suggestions(/di) returned none")
	}
	if got[0].Insert != "/diff" {
		t.Fatalf("first /di suggestion = %q, want /diff", got[0].Insert)
	}
}

func TestSuggestionsIncludeCodexCommands(t *testing.T) {
	got := Suggestions("/cod")
	if len(got) < 2 {
		t.Fatalf("Suggestions(/cod) len = %d, want at least 2", len(got))
	}
	if got[0].Insert != "/codex" {
		t.Fatalf("first /cod suggestion = %q, want /codex", got[0].Insert)
	}
	if got[1].Insert != "/codex-new" {
		t.Fatalf("second /cod suggestion = %q, want /codex-new", got[1].Insert)
	}
}

func TestSuggestionsIncludeOpenCodeCommands(t *testing.T) {
	got := Suggestions("/open")
	if len(got) < 3 {
		t.Fatalf("Suggestions(/open) len = %d, want at least 3", len(got))
	}
	if got[0].Insert != "/open" {
		t.Fatalf("first /open suggestion = %q, want /open", got[0].Insert)
	}
	if got[1].Insert != "/opencode" {
		t.Fatalf("second /open suggestion = %q, want /opencode", got[1].Insert)
	}
	if got[2].Insert != "/opencode-new" {
		t.Fatalf("third /open suggestion = %q, want /opencode-new", got[2].Insert)
	}
}
