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
			name: "ai stats",
			raw:  "/ai",
			check: func(t *testing.T, inv Invocation) {
				if inv.Kind != KindAIStats {
					t.Fatalf("kind = %s, want %s", inv.Kind, KindAIStats)
				}
				if inv.Canonical != "/ai" {
					t.Fatalf("canonical = %q, want /ai", inv.Canonical)
				}
			},
		},
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
			name: "filter dialog",
			raw:  "/filter",
			check: func(t *testing.T, inv Invocation) {
				if inv.Kind != KindFilter {
					t.Fatalf("kind = %s, want %s", inv.Kind, KindFilter)
				}
				if inv.Filter != "" {
					t.Fatalf("filter = %q, want empty", inv.Filter)
				}
				if inv.Clear {
					t.Fatalf("clear = true, want false")
				}
			},
		},
		{
			name: "filter text",
			raw:  "/filter little",
			check: func(t *testing.T, inv Invocation) {
				if inv.Kind != KindFilter {
					t.Fatalf("kind = %s, want %s", inv.Kind, KindFilter)
				}
				if inv.Filter != "little" {
					t.Fatalf("filter = %q, want little", inv.Filter)
				}
				if inv.Canonical != "/filter little" {
					t.Fatalf("canonical = %q, want /filter little", inv.Canonical)
				}
			},
		},
		{
			name: "filter clear",
			raw:  "/filter clear",
			check: func(t *testing.T, inv Invocation) {
				if inv.Kind != KindFilter {
					t.Fatalf("kind = %s, want %s", inv.Kind, KindFilter)
				}
				if !inv.Clear {
					t.Fatalf("clear = false, want true")
				}
				if inv.Canonical != "/filter clear" {
					t.Fatalf("canonical = %q, want /filter clear", inv.Canonical)
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
			name: "run with explicit command",
			raw:  "/run pnpm dev",
			check: func(t *testing.T, inv Invocation) {
				if inv.Kind != KindRun {
					t.Fatalf("kind = %s, want %s", inv.Kind, KindRun)
				}
				if inv.Command != "pnpm dev" {
					t.Fatalf("command = %q, want pnpm dev", inv.Command)
				}
			},
		},
		{
			name: "start alias with explicit command",
			raw:  "/start pnpm dev",
			check: func(t *testing.T, inv Invocation) {
				if inv.Kind != KindRun {
					t.Fatalf("kind = %s, want %s", inv.Kind, KindRun)
				}
				if inv.Command != "pnpm dev" {
					t.Fatalf("command = %q, want pnpm dev", inv.Command)
				}
				if inv.Canonical != "/run pnpm dev" {
					t.Fatalf("canonical = %q, want /run pnpm dev", inv.Canonical)
				}
			},
		},
		{
			name: "restart runtime",
			raw:  "/restart",
			check: func(t *testing.T, inv Invocation) {
				if inv.Kind != KindRestart {
					t.Fatalf("kind = %s, want %s", inv.Kind, KindRestart)
				}
				if inv.Canonical != "/restart" {
					t.Fatalf("canonical = %q, want /restart", inv.Canonical)
				}
			},
		},
		{
			name: "run edit",
			raw:  "/run-edit",
			check: func(t *testing.T, inv Invocation) {
				if inv.Kind != KindRunEdit {
					t.Fatalf("kind = %s, want %s", inv.Kind, KindRunEdit)
				}
			},
		},
		{
			name: "runtime inspector",
			raw:  "/runtime",
			check: func(t *testing.T, inv Invocation) {
				if inv.Kind != KindRuntime {
					t.Fatalf("kind = %s, want %s", inv.Kind, KindRuntime)
				}
			},
		},
		{
			name: "stop runtime",
			raw:  "/stop",
			check: func(t *testing.T, inv Invocation) {
				if inv.Kind != KindStop {
					t.Fatalf("kind = %s, want %s", inv.Kind, KindStop)
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
			name: "wt merge",
			raw:  "/wt merge",
			check: func(t *testing.T, inv Invocation) {
				if inv.Kind != KindWorktreeMerge {
					t.Fatalf("kind = %s, want %s", inv.Kind, KindWorktreeMerge)
				}
				if inv.Canonical != "/wt merge" {
					t.Fatalf("canonical = %q, want /wt merge", inv.Canonical)
				}
			},
		},
		{
			name: "worktree prune alias",
			raw:  "/worktree prune",
			check: func(t *testing.T, inv Invocation) {
				if inv.Kind != KindWorktreePrune {
					t.Fatalf("kind = %s, want %s", inv.Kind, KindWorktreePrune)
				}
				if inv.Canonical != "/wt prune" {
					t.Fatalf("canonical = %q, want /wt prune", inv.Canonical)
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
			name: "claude smart resume prompt",
			raw:  "/claude continue with the latest session",
			check: func(t *testing.T, inv Invocation) {
				if inv.Kind != KindClaude {
					t.Fatalf("kind = %s, want %s", inv.Kind, KindClaude)
				}
				if inv.Prompt != "continue with the latest session" {
					t.Fatalf("prompt = %q, want Claude prompt", inv.Prompt)
				}
			},
		},
		{
			name: "claude new alias",
			raw:  "/cc-start summarize the repo",
			check: func(t *testing.T, inv Invocation) {
				if inv.Kind != KindClaudeNew {
					t.Fatalf("kind = %s, want %s", inv.Kind, KindClaudeNew)
				}
				if inv.Prompt != "summarize the repo" {
					t.Fatalf("prompt = %q, want Claude prompt", inv.Prompt)
				}
				if inv.Canonical != "/claude-new summarize the repo" {
					t.Fatalf("canonical = %q, want canonical claude-new form", inv.Canonical)
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
			name: "snooze off",
			raw:  "/snooze off",
			check: func(t *testing.T, inv Invocation) {
				if inv.Kind != KindClearSnooze {
					t.Fatalf("kind = %s, want %s", inv.Kind, KindClearSnooze)
				}
				if inv.Canonical != "/snooze off" {
					t.Fatalf("canonical = %q, want /snooze off", inv.Canonical)
				}
			},
		},
		{
			name: "snooze clear",
			raw:  "/snooze clear",
			check: func(t *testing.T, inv Invocation) {
				if inv.Kind != KindClearSnooze {
					t.Fatalf("kind = %s, want %s", inv.Kind, KindClearSnooze)
				}
				if inv.Canonical != "/snooze off" {
					t.Fatalf("canonical = %q, want /snooze off", inv.Canonical)
				}
			},
		},
		{
			name: "snooze unsnooze",
			raw:  "/snooze unsnooze",
			check: func(t *testing.T, inv Invocation) {
				if inv.Kind != KindClearSnooze {
					t.Fatalf("kind = %s, want %s", inv.Kind, KindClearSnooze)
				}
				if inv.Canonical != "/snooze off" {
					t.Fatalf("canonical = %q, want /snooze off", inv.Canonical)
				}
			},
		},
		{
			name: "unsnooze alias",
			raw:  "/unsnooze",
			check: func(t *testing.T, inv Invocation) {
				if inv.Kind != KindClearSnooze {
					t.Fatalf("kind = %s, want %s", inv.Kind, KindClearSnooze)
				}
				if inv.Canonical != "/clear-snooze" {
					t.Fatalf("canonical = %q, want /clear-snooze", inv.Canonical)
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
			name: "ignore",
			raw:  "/ignore",
			check: func(t *testing.T, inv Invocation) {
				if inv.Kind != KindIgnore {
					t.Fatalf("kind = %s, want %s", inv.Kind, KindIgnore)
				}
				if inv.Canonical != "/ignore" {
					t.Fatalf("canonical = %q, want /ignore", inv.Canonical)
				}
			},
		},
		{
			name: "ignored",
			raw:  "/ignored",
			check: func(t *testing.T, inv Invocation) {
				if inv.Kind != KindIgnored {
					t.Fatalf("kind = %s, want %s", inv.Kind, KindIgnored)
				}
				if inv.Canonical != "/ignored" {
					t.Fatalf("canonical = %q, want /ignored", inv.Canonical)
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

func TestParseRejectsRemovedFinishCommand(t *testing.T) {
	if _, err := Parse("/finish"); err == nil {
		t.Fatalf("Parse(/finish) expected error after removing the ambiguous alias")
	}
}

func TestParseRejectsIncompleteWorktreeCommand(t *testing.T) {
	if _, err := Parse("/wt"); err == nil {
		t.Fatalf("Parse(/wt) expected usage error")
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

func TestSuggestionsSnoozeArgumentOff(t *testing.T) {
	got := Suggestions("/snooze o")
	if len(got) != 1 {
		t.Fatalf("Suggestions(/snooze o) len = %d, want 1", len(got))
	}
	if got[0].Insert != "/snooze off" {
		t.Fatalf("suggestion = %q, want /snooze off", got[0].Insert)
	}
}

func TestSuggestionsIncludeCommitWorkflowCommands(t *testing.T) {
	got := Suggestions("/f")
	if len(got) == 0 {
		t.Fatalf("Suggestions(/f) returned none")
	}
	if got[0].Insert != "/filter" {
		t.Fatalf("first /f suggestion = %q, want /filter", got[0].Insert)
	}
}

func TestSuggestionsIncludeAICommand(t *testing.T) {
	got := Suggestions("/a")
	if len(got) == 0 {
		t.Fatalf("Suggestions(/a) returned none")
	}
	if got[0].Insert != "/ai" {
		t.Fatalf("first /a suggestion = %q, want /ai", got[0].Insert)
	}
}

func TestSuggestionsFilterClearArgument(t *testing.T) {
	got := Suggestions("/filter c")
	if len(got) != 1 {
		t.Fatalf("Suggestions(/filter c) len = %d, want 1", len(got))
	}
	if got[0].Insert != "/filter clear" {
		t.Fatalf("suggestion = %q, want /filter clear", got[0].Insert)
	}
}

func TestSuggestionsWorktreeArguments(t *testing.T) {
	got := Suggestions("/wt m")
	if len(got) != 1 {
		t.Fatalf("Suggestions(/wt m) len = %d, want 1", len(got))
	}
	if got[0].Insert != "/wt merge" {
		t.Fatalf("suggestion = %q, want /wt merge", got[0].Insert)
	}
}

func TestSuggestionsWorktreeAliasArguments(t *testing.T) {
	got := Suggestions("/worktree ")
	if len(got) != 4 {
		t.Fatalf("Suggestions(/worktree ) len = %d, want 4", len(got))
	}
	if got[0].Insert != "/wt lanes" {
		t.Fatalf("first suggestion = %q, want /wt lanes", got[0].Insert)
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

func TestSuggestionsIncludeStartCommand(t *testing.T) {
	got := Suggestions("/sta")
	if len(got) == 0 {
		t.Fatalf("Suggestions(/sta) returned none")
	}
	if got[0].Insert != "/start" {
		t.Fatalf("first /sta suggestion = %q, want /start", got[0].Insert)
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

func TestSuggestionsIncludeClaudeCommands(t *testing.T) {
	got := Suggestions("/cla")
	if len(got) < 2 {
		t.Fatalf("Suggestions(/cla) len = %d, want at least 2", len(got))
	}
	if got[0].Insert != "/claude" {
		t.Fatalf("first /cla suggestion = %q, want /claude", got[0].Insert)
	}
	if got[1].Insert != "/claude-new" {
		t.Fatalf("second /cla suggestion = %q, want /claude-new", got[1].Insert)
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

func TestSuggestionsIncludeRuntimeCommand(t *testing.T) {
	got := Suggestions("/run")
	if len(got) < 3 {
		t.Fatalf("Suggestions(/run) len = %d, want at least 3", len(got))
	}
	if got[0].Insert != "/run" {
		t.Fatalf("first /run suggestion = %q, want /run", got[0].Insert)
	}
	if got[1].Insert != "/run-edit" {
		t.Fatalf("second /run suggestion = %q, want /run-edit", got[1].Insert)
	}
	if got[2].Insert != "/runtime" {
		t.Fatalf("third /run suggestion = %q, want /runtime", got[2].Insert)
	}
}
