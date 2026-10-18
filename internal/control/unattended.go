package control

import (
	"fmt"
	"sort"
	"strings"
	"time"
)

const (
	DefaultWakeupGraceMinutes = 60
	MaxWakeupGraceMinutes     = 24 * 60
	MaxWakeupLeadTime         = 366 * 24 * time.Hour
	DefaultGrantValidMinutes  = 120
	MaxGrantValidMinutes      = 24 * 60
	maxGrantListEntries       = 32
	maxGrantNotesRunes        = 600
)

// Approval kinds an UnattendedGrant can answer. They mirror the embedded
// session approval kinds so the host can match without importing a provider.
const (
	ApprovalKindCommand    = "command"
	ApprovalKindFileChange = "file_change"
	ApprovalKindToolUse    = "tool_use"
)

// UnattendedGrant is what the operator pre-authorizes for one scheduled
// wakeup. The host enforces it only on approval requests the session raises;
// Notes is advisory text the engineer is told to honor. Anything the grant
// does not cover is left pending for the operator rather than guessed at.
type UnattendedGrant struct {
	// Commands accepts every command approval. CommandPrefixes is the narrower
	// alternative and is ignored when Commands is set.
	Commands        bool     `json:"commands,omitempty"`
	CommandPrefixes []string `json:"command_prefixes,omitempty"`
	FileChanges     bool     `json:"file_changes,omitempty"`
	// Tools lists tool names the session may use. A trailing * matches a prefix.
	Tools []string `json:"tools,omitempty"`
	// Notes states limits that approvals cannot express, such as "publish the
	// already-approved queue, nothing new". The engineer is told to stop and
	// report instead of exceeding it.
	Notes string `json:"notes,omitempty"`
	// ValidMinutes bounds how long after delivery the grant answers approvals.
	ValidMinutes int `json:"valid_minutes,omitempty"`
}

// IsZero reports whether the grant authorizes nothing.
func (g UnattendedGrant) IsZero() bool {
	return !g.Commands && len(g.CommandPrefixes) == 0 && !g.FileChanges && len(g.Tools) == 0 && strings.TrimSpace(g.Notes) == ""
}

func NormalizeUnattendedGrant(g UnattendedGrant) (UnattendedGrant, error) {
	g.CommandPrefixes = normalizeGrantList(g.CommandPrefixes)
	g.Tools = normalizeGrantList(g.Tools)
	if len(g.CommandPrefixes) > maxGrantListEntries || len(g.Tools) > maxGrantListEntries {
		return UnattendedGrant{}, fmt.Errorf("an unattended grant lists at most %d command prefixes and %d tools", maxGrantListEntries, maxGrantListEntries)
	}
	if g.Commands {
		g.CommandPrefixes = nil
	}
	for _, tool := range g.Tools {
		if strings.Trim(tool, "*") == "" {
			return UnattendedGrant{}, fmt.Errorf("unattended grant tool %q would match every tool; name the tools explicitly", tool)
		}
	}
	g.Notes = strings.Join(strings.Fields(g.Notes), " ")
	if runes := []rune(g.Notes); len(runes) > maxGrantNotesRunes {
		return UnattendedGrant{}, fmt.Errorf("unattended grant notes are limited to %d characters", maxGrantNotesRunes)
	}
	if g.ValidMinutes < 0 {
		return UnattendedGrant{}, fmt.Errorf("unattended grant valid_minutes cannot be negative")
	}
	if g.ValidMinutes == 0 {
		g.ValidMinutes = DefaultGrantValidMinutes
	}
	if g.ValidMinutes > MaxGrantValidMinutes {
		return UnattendedGrant{}, fmt.Errorf("unattended grant valid_minutes is limited to %d", MaxGrantValidMinutes)
	}
	return g, nil
}

func normalizeGrantList(values []string) []string {
	seen := make(map[string]struct{}, len(values))
	out := make([]string, 0, len(values))
	for _, value := range values {
		value = strings.TrimSpace(value)
		if value == "" {
			continue
		}
		if _, dup := seen[value]; dup {
			continue
		}
		seen[value] = struct{}{}
		out = append(out, value)
	}
	sort.Strings(out)
	return out
}

// ValidFor is how long after delivery the grant answers approvals.
func (g UnattendedGrant) ValidFor() time.Duration {
	minutes := g.ValidMinutes
	if minutes <= 0 {
		minutes = DefaultGrantValidMinutes
	}
	return time.Duration(minutes) * time.Minute
}

// Allows reports whether an approval request is covered. detail is the command
// line for commands and the tool name for tool use; file changes need none.
func (g UnattendedGrant) Allows(kind, detail string) bool {
	detail = strings.TrimSpace(detail)
	switch kind {
	case ApprovalKindCommand:
		if g.Commands {
			return true
		}
		for _, prefix := range g.CommandPrefixes {
			if commandHasPrefix(detail, prefix) {
				return true
			}
		}
	case ApprovalKindFileChange:
		return g.FileChanges
	case ApprovalKindToolUse:
		for _, tool := range g.Tools {
			if strings.HasSuffix(tool, "*") {
				if strings.HasPrefix(detail, strings.TrimSuffix(tool, "*")) {
					return true
				}
			} else if detail == tool {
				return true
			}
		}
	}
	return false
}

// commandHasPrefix matches on a word boundary so "go test" does not cover
// "go testing-tool", and refuses command lines that chain further commands.
func commandHasPrefix(command, prefix string) bool {
	command, prefix = strings.TrimSpace(command), strings.TrimSpace(prefix)
	if prefix == "" || !strings.HasPrefix(command, prefix) {
		return false
	}
	if strings.ContainsAny(command, ";&|`\n<>") || strings.Contains(command, "$(") {
		return false
	}
	rest := command[len(prefix):]
	return rest == "" || rest[0] == ' ' || rest[0] == '\t'
}

// Summary is the operator-facing statement of what runs unattended.
func (g UnattendedGrant) Summary() string {
	var parts []string
	switch {
	case g.Commands:
		parts = append(parts, "any command")
	case len(g.CommandPrefixes) > 0:
		parts = append(parts, "commands starting "+strings.Join(quoteAll(g.CommandPrefixes), ", "))
	}
	if g.FileChanges {
		parts = append(parts, "file changes")
	}
	if len(g.Tools) > 0 {
		parts = append(parts, "tools "+strings.Join(g.Tools, ", "))
	}
	if len(parts) == 0 {
		parts = append(parts, "nothing that needs approval")
	}
	summary := "may run unattended with: " + strings.Join(parts, "; ")
	if g.Notes != "" {
		summary += ". Limits: " + g.Notes
	}
	return summary + fmt.Sprintf(" (valid %d min after delivery; anything else waits for you)", int(g.ValidFor()/time.Minute))
}

func quoteAll(values []string) []string {
	out := make([]string, len(values))
	for i, value := range values {
		out[i] = fmt.Sprintf("%q", value)
	}
	return out
}

// WakeupPromptPreamble tells the woken engineer what authority it has, so a
// session that is not approval-gated still stops at the declared limits.
func WakeupPromptPreamble(scheduledFor time.Time, grant UnattendedGrant) string {
	lines := []string{
		fmt.Sprintf("This is a scheduled wakeup from Little Control Room (scheduled for %s). The operator may be away.", scheduledFor.Format(time.RFC3339)),
		"Authority: you " + grant.Summary() + ".",
		"Stay inside that authority. Do not publish, push, delete, spend money or take any other irreversible action it does not name. If you need something outside it, stop, say exactly what is needed and why, and wait for the operator.",
		"Report plainly at the end: what you did, what you verified, and anything blocked.",
	}
	return strings.Join(lines, "\n")
}

// ParseWakeupTime parses a canonical RFC 3339 wakeup time.
func ParseWakeupTime(value string) (time.Time, error) {
	value = strings.TrimSpace(value)
	if value == "" {
		return time.Time{}, nil
	}
	at, err := time.Parse(time.RFC3339, value)
	if err != nil {
		return time.Time{}, fmt.Errorf("deliver_at must be an RFC 3339 time with an offset such as 2026-10-18T18:10:00+09:00: %w", err)
	}
	return at, nil
}
