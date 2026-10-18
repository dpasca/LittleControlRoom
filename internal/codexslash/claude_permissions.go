package codexslash

import (
	"fmt"
	"strings"

	"lcroom/internal/claudecli"
	"lcroom/internal/slashcmd"
)

const ClaudePermissionsUsage = "/permissions [mode <auto|manual|acceptEdits|dontAsk|plan|bypassPermissions>|allow|ask|deny <rule>|remove <allow|ask|deny> <rule>]"

// ParseClaude keeps native rule strings intact, including spaces and case.
func ParseClaude(input string) (Invocation, error) {
	body := strings.TrimSpace(input)
	if !strings.HasPrefix(body, "/") {
		return Parse(input)
	}
	name, args := slashcmd.SplitCommandBody(strings.TrimPrefix(body, "/"))
	switch strings.ToLower(name) {
	case "permissions", "permission", "perms":
	default:
		return Parse(input)
	}
	inv := Invocation{Kind: KindPermissions, Canonical: "/permissions"}
	args = strings.TrimSpace(args)
	if args == "" {
		return inv, nil
	}
	action, rest := slashcmd.SplitCommandBody(args)
	if action == "mode" {
		if len(strings.Fields(rest)) != 1 || strings.ContainsAny(rest, "\r\n\x00") {
			return Invocation{}, fmt.Errorf("usage: %s", ClaudePermissionsUsage)
		}
		mode, err := claudecli.ParsePermissionMode(rest)
		if err != nil {
			return Invocation{}, err
		}
		inv.PermissionMode = string(mode)
		inv.Canonical += " mode " + string(mode)
		return inv, nil
	}
	if action == "remove" {
		inv.PermissionRemove = true
		action, rest = slashcmd.SplitCommandBody(strings.TrimSpace(rest))
	}
	if action != "allow" && action != "ask" && action != "deny" {
		return Invocation{}, fmt.Errorf("usage: %s", ClaudePermissionsUsage)
	}
	rule := strings.TrimSpace(rest)
	if rule == "" || strings.ContainsAny(rule, "\r\n\x00") {
		return Invocation{}, fmt.Errorf("usage: %s", ClaudePermissionsUsage)
	}
	inv.PermissionAction, inv.PermissionRule = action, rule
	inv.Canonical += " " + args
	return inv, nil
}

func ClaudePermissionSuggestions(input string, suggestions []Suggestion) []Suggestion {
	body := strings.TrimPrefix(strings.TrimSpace(input), "/")
	name, args := slashcmd.SplitCommandBody(body)
	switch strings.ToLower(name) {
	case "permissions", "permission", "perms":
		if action, prefix := slashcmd.SplitCommandBody(args); action == "mode" {
			var choices []Suggestion
			for _, mode := range []claudecli.PermissionMode{claudecli.PermissionModeAuto, claudecli.PermissionModeManual, claudecli.PermissionModeAcceptEdits, claudecli.PermissionModeDontAsk, claudecli.PermissionModePlan, claudecli.PermissionModeBypassPermissions} {
				if strings.HasPrefix(string(mode), prefix) {
					command := "/permissions mode " + string(mode)
					choices = append(choices, Suggestion{Insert: command, Display: command, Summary: "Use " + mode.DisplayName() + " for this session"})
				}
			}
			return choices
		}
		actions := []string{"allow", "ask", "deny", "remove allow", "remove ask", "remove deny"}
		for _, action := range actions {
			if args == action {
				args = ""
				break
			}
		}
		choices := []Suggestion{{Insert: "/permissions", Display: "/permissions", Summary: "Show Claude Code mode and saved permission rules"}}
		for _, action := range actions {
			if args == "" || strings.HasPrefix(action, args) {
				choices = append(choices, Suggestion{Insert: "/permissions " + action, Display: "/permissions " + action + " <rule>", Summary: "Edit this worktree's Claude permission rules; reconnect to apply"})
			}
		}
		if args == "" || strings.HasPrefix("mode", args) {
			choices = append(choices, Suggestion{Insert: "/permissions mode", Display: "/permissions mode <mode>", Summary: "Change permission mode for this session"})
		}
		// Do not complete a fully typed rule back to the bare status command.
		if len(choices) == 1 && strings.Contains(strings.TrimSpace(args), " ") {
			return nil
		}
		return choices
	}
	out := append([]Suggestion(nil), suggestions...)
	for i := range out {
		if strings.TrimSpace(out[i].Insert) == "/permissions" {
			out[i].Display = "/permissions"
			out[i].Summary = "Show permissions, change session mode, or edit worktree rules"
		}
	}
	return out
}
