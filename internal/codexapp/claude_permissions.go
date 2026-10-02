package codexapp

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"lcroom/internal/claudecli"
)

func (s *claudeCodeSession) ShowPermissions() error {
	s.mu.Lock()
	if s.closed {
		s.mu.Unlock()
		return fmt.Errorf("Claude Code session is closed")
	}
	mode, project, home := s.effectivePermissionModeLocked(), s.projectPath, s.claudeHome
	s.mu.Unlock()
	if configured := strings.TrimSpace(os.Getenv("CLAUDE_CONFIG_DIR")); configured != "" {
		home = configured
	}
	lines := []string{"Embedded Claude Code permissions", "Current mode: " + mode.DisplayName(), "Saved rules (not a live effective-policy report):"}
	for _, source := range []struct{ label, path string }{
		{"User", filepath.Join(home, "settings.json")},
		{"Project", filepath.Join(project, ".claude", "settings.json")},
		{"Worktree local (editable)", filepath.Join(project, ".claude", "settings.local.json")},
	} {
		rules, err := claudecli.ReadPermissionRules(source.path)
		lines = append(lines, source.label+": "+source.path)
		if err != nil {
			lines = append(lines, "  Cannot read rules: "+err.Error())
			continue
		}
		count := 0
		for _, action := range []string{"deny", "ask", "allow"} {
			for _, rule := range rules[action] {
				lines = append(lines, "  "+action+": "+rule)
				count++
			}
		}
		if count == 0 {
			lines = append(lines, "  (no saved rules)")
		}
	}
	lines = append(lines,
		"Claude also applies inherited repository/parent settings, managed policy, launch settings, and hooks; these are not enumerated here. Deny and ask rules can override allow rules.",
		"Edit worktree-local rules: /permissions allow|ask|deny <rule>",
		"Remove an exact local rule: /permissions remove allow|ask|deny <rule>",
		"Example: /permissions allow Bash(huggingface-cli upload:*)",
		"After saving, finish current work and use /reconnect before retrying. Mode defaults are in /settings.")
	s.mu.Lock()
	defer s.mu.Unlock()
	s.appendEntryLocked(TranscriptEntry{Kind: TranscriptStatus, Text: strings.Join(lines, "\n")})
	s.notifyAsync()
	return nil
}

func (s *claudeCodeSession) EditPermissionRule(action, rule string, remove bool) error {
	s.submitMu.Lock()
	defer s.submitMu.Unlock()
	s.mu.Lock()
	if s.closed {
		s.mu.Unlock()
		return fmt.Errorf("Claude Code session is closed")
	}
	if s.busy || s.busyExternal || s.externalTurnActive || s.pendingApproval != nil || s.pendingToolInput != nil {
		s.mu.Unlock()
		return fmt.Errorf("wait for Claude Code's current work to finish before editing permission rules")
	}
	project := s.projectPath
	s.mu.Unlock()
	if err := claudecli.EditPermissionRule(project, action, rule, remove); err != nil {
		return err
	}
	verb := "Saved "
	if remove {
		verb = "Removed "
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	s.appendSystemNoticeLocked(verb + action + " rule " + rule + " in " + filepath.Join(project, ".claude", "settings.local.json") + ". Use /reconnect before retrying; the running process has not been verified to reload this change.")
	s.notifyAsync()
	return nil
}
