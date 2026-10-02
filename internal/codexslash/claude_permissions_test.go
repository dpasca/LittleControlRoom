package codexslash

import "testing"

func TestParseClaudePermissions(t *testing.T) {
	for _, input := range []string{"/permissions allow Bash(huggingface-cli upload:*)", "/perms remove deny Bash(Git Push:*)"} {
		inv, err := ParseClaude(input)
		if err != nil {
			t.Fatal(err)
		}
		if inv.Kind != KindPermissions || inv.PermissionRule == "" {
			t.Fatalf("inv = %#v", inv)
		}
		if inv.PermissionRemove && inv.PermissionRule != "Bash(Git Push:*)" {
			t.Fatal("changed rule case")
		}
		if _, err := Parse(input); err == nil {
			t.Fatal("Claude syntax leaked into other providers")
		}
	}
	for _, input := range []string{"/permissions low", "/permissions allow", "/permissions remove allow"} {
		if _, err := ParseClaude(input); err == nil {
			t.Fatalf("accepted %s", input)
		}
	}
	inv, err := ParseClaude("/permissions")
	if err != nil || inv.Kind != KindPermissions || inv.PermissionAction != "" {
		t.Fatalf("show: %#v %v", inv, err)
	}
	if inv, err := ParseClaude("/style Terse"); err != nil || inv.OutputStyle != "Terse" {
		t.Fatalf("other command changed: %#v %v", inv, err)
	}
}

func TestClaudePermissionSuggestions(t *testing.T) {
	for _, input := range []string{"/", "/perm", "/permissions", "/permissions "} {
		suggestions := ClaudePermissionSuggestions(input, Suggestions(input))
		found := false
		for _, suggestion := range suggestions {
			if suggestion.Insert == "/permissions" {
				found = true
			}
			if suggestion.Insert == "/permissions low" {
				t.Fatal("LCAgent suggestion in Claude pane")
			}
		}
		if !found {
			t.Fatalf("no permissions suggestion for %q", input)
		}
	}
	if got := ClaudePermissionSuggestions("/permissions allow Bash(git push:*)", nil); len(got) != 0 {
		t.Fatalf("completed rule must stay intact: %#v", got)
	}
}
