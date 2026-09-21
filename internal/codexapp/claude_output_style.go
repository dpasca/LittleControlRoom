package codexapp

import (
	"fmt"
	"strings"

	"lcroom/internal/claudestyle"
)

// Claude Code has no CLI flag for output styles and no mid-turn control for
// them, so LCR carries the selection in the per-turn `--settings` payload.
// Because LCR spawns a fresh `claude --resume` process per turn, a staged
// style takes effect on the next prompt, the same way a staged model or
// reasoning effort does.

// ListOutputStyles returns the styles discovered for this session, refreshing
// from disk so a style file added while the session is open becomes
// selectable without restarting LCR.
func (s *claudeCodeSession) ListOutputStyles() ([]claudestyle.Option, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	s.availableStyles = claudestyle.Discover(s.claudeHome, s.projectPath)
	return append([]claudestyle.Option(nil), s.availableStyles...), nil
}

// StageOutputStyle selects the output style for the next turn. The name is
// validated against the styles on disk because Claude Code accepts an unknown
// name and silently applies nothing, which would leave the sidebar reporting a
// style that is not in effect.
func (s *claudeCodeSession) StageOutputStyle(name string) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	if s.closed {
		return fmt.Errorf("Claude Code session is closed")
	}

	s.availableStyles = claudestyle.Discover(s.claudeHome, s.projectPath)
	name = strings.TrimSpace(name)

	if claudestyle.IsDefault(name) {
		s.pendingOutputStyle = claudestyle.DefaultName
		s.lastSystemNotice = "Claude Code will use its default output style on the next prompt."
		s.updateStatusLocked()
		s.notifyAsync()
		return nil
	}

	option, ambiguous, ok := claudestyle.Resolve(s.availableStyles, name)
	if !ok {
		if len(ambiguous) > 0 {
			return fmt.Errorf("output style %q matches more than one style: %s; type the exact name", name, strings.Join(claudestyle.Names(ambiguous), ", "))
		}
		return fmt.Errorf("unknown output style %q; available: %s", name, strings.Join(claudestyle.Names(s.availableStyles), ", "))
	}

	s.pendingOutputStyle = option.Name
	s.lastSystemNotice = "Claude Code will use the " + option.Name + " output style on the next prompt."
	if option.Name != name {
		// Say which style was actually selected so a loose spelling never
		// leaves the user guessing what is now in effect.
		s.lastSystemNotice = "Claude Code will use the " + option.Name + " output style on the next prompt (matched from \"" + name + "\")."
	}
	s.updateStatusLocked()
	s.notifyAsync()
	return nil
}

// effectiveOutputStyleLocked reports the style the next turn will request.
func (s *claudeCodeSession) effectiveOutputStyleLocked() string {
	if style := strings.TrimSpace(s.pendingOutputStyle); style != "" {
		return style
	}
	return strings.TrimSpace(s.outputStyle)
}

// applyPendingOutputStyleLocked promotes a staged style and rebuilds the
// settings payload the next `claude` process receives. A rebuild failure keeps
// the previous payload so the destructive-command hook is never dropped, and
// says so rather than leaving the UI claiming a style that is not applied.
func (s *claudeCodeSession) applyPendingOutputStyleLocked() {
	pending := strings.TrimSpace(s.pendingOutputStyle)
	if pending == "" {
		return
	}
	s.pendingOutputStyle = ""

	style := pending
	if claudestyle.IsDefault(style) {
		style = ""
	}
	settings, err := claudeSafetyHookSettings(s.safetyExecutable, style)
	if err != nil || strings.TrimSpace(settings) == "" {
		s.lastSystemNotice = "Little Control Room could not apply the " + pending + " output style; the previous style remains in effect."
		return
	}
	s.safetySettings = settings
	s.outputStyle = style
}

// observeOutputStyleLocked records the style Claude Code reported for the
// turn. Claude Code echoes back whatever name it was given without checking
// that a matching style exists, so a value LCR did not request is reported as
// an external override rather than trusted as confirmation.
func (s *claudeCodeSession) observeOutputStyleLocked(reported string) {
	reported = strings.TrimSpace(reported)
	if reported == "" {
		return
	}
	if claudestyle.IsDefault(reported) {
		s.outputStyle = ""
		return
	}
	if reported == strings.TrimSpace(s.outputStyle) {
		return
	}
	// LCR sends no style override unless one is selected, so a different value
	// here comes from the user's own Claude Code settings.
	s.outputStyle = reported
	if _, known := claudestyle.Find(s.availableStyles, reported); !known {
		s.lastSystemNotice = "Claude Code reported the " + reported + " output style, but no style file with that name was found. Claude Code accepts unknown style names and applies nothing."
	}
}
