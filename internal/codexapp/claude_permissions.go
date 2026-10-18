package codexapp

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"time"

	"lcroom/internal/claudecli"
)

type claudePermissionModeChange struct {
	requestID string
	mode      claudecli.PermissionMode
	result    chan error
}

// SetPermissionMode changes only this open session. Live streams acknowledge
// the control request before the requested mode or badge is changed.
func (s *claudeCodeSession) SetPermissionMode(raw string) error {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	return s.setPermissionMode(ctx, raw)
}

func (s *claudeCodeSession) setPermissionMode(ctx context.Context, raw string) error {
	mode, err := claudecli.ParsePermissionMode(raw)
	if err != nil {
		return err
	}
	if strings.TrimSpace(raw) == "" {
		return fmt.Errorf("Claude Code permission mode required")
	}
	s.submitMu.Lock()
	defer s.submitMu.Unlock()
	if err := ctx.Err(); err != nil {
		return err
	}
	s.mu.Lock()
	switch {
	case s.closed:
		err = fmt.Errorf("Claude Code session is closed")
	case s.readOnlySubagent:
		err = fmt.Errorf("cannot change permission mode for a read-only Claude Code worker")
	case s.busyExternal || s.externalTurnActive:
		err = fmt.Errorf("cannot change permission mode while Claude Code is active in another process")
	case s.pendingApproval != nil || s.pendingToolInput != nil:
		err = fmt.Errorf("answer Claude Code's pending request before changing permission mode")
	case s.permissionModeChange != nil:
		err = fmt.Errorf("Claude Code has not confirmed the previous permission mode change; wait or use /reconnect")
	case mode.UsesApprovalBridge() && strings.TrimSpace(s.mcpOptions.PermissionPromptTool) == "":
		err = fmt.Errorf("Claude Code approval callbacks are unavailable; use /reconnect before switching to %s", mode.DisplayName())
	}
	if err != nil {
		s.mu.Unlock()
		return err
	}
	if s.cmd == nil || mode == s.effectivePermissionModeLocked() {
		s.applyClaudePermissionModeLocked(mode)
		s.mu.Unlock()
		s.notifyAsync()
		return nil
	}
	if s.stdin == nil || s.interruptPending {
		s.mu.Unlock()
		return fmt.Errorf("wait for Claude Code to finish its current turn before changing permission mode")
	}
	if !s.managedStream || s.streamInitResult != nil {
		s.mu.Unlock()
		return fmt.Errorf("this Claude Code stream cannot change permission mode yet; wait for the current turn to finish")
	}
	change := &claudePermissionModeChange{
		requestID: "lcr-permission-mode-" + newClaudeMessageID(),
		mode:      mode,
		result:    make(chan error, 1),
	}
	// The stream protocol uses "default" for the CLI's Manual mode.
	wireMode := string(mode)
	if mode == claudecli.PermissionModeManual {
		wireMode = "default"
	}
	payload, err := json.Marshal(map[string]any{
		"type":       "control_request",
		"request_id": change.requestID,
		"request":    map[string]string{"subtype": "set_permission_mode", "mode": wireMode},
	})
	if err != nil {
		s.mu.Unlock()
		return err
	}
	s.permissionModeChange = change
	stdin := s.stdin
	s.mu.Unlock()
	if _, err := io.WriteString(stdin, string(payload)+"\n"); err != nil {
		s.mu.Lock()
		s.completeClaudePermissionModeChangeLocked(fmt.Errorf("send Claude Code permission mode change: %w", err))
		s.mu.Unlock()
	}
	select {
	case err := <-change.result:
		return err
	case <-ctx.Done():
		s.mu.Lock()
		if s.permissionModeChange != change {
			s.mu.Unlock()
			return <-change.result
		}
		// Retain the request so a late acknowledgment still updates the badge.
		// Further changes are blocked until it resolves or the session closes.
		err := fmt.Errorf("Claude Code has not confirmed the permission mode change: %w; wait or use /reconnect before retrying", ctx.Err())
		s.appendSystemNoticeLocked(err.Error())
		s.mu.Unlock()
		s.notifyAsync()
		return err
	}
}

func (s *claudeCodeSession) applyClaudePermissionModeLocked(mode claudecli.PermissionMode) {
	s.requestedPermissionMode, s.permissionMode = mode, mode
	s.modeNoticeShown, s.modeFallbackShown = false, false
	s.appendSystemNoticeLocked("Claude Code permission mode: " + mode.DisplayName() + " (this session).")
	s.touchLocked()
}

func (s *claudeCodeSession) completeClaudePermissionModeChangeLocked(err error) {
	if change := s.permissionModeChange; change != nil {
		s.permissionModeChange = nil
		change.result <- err
	}
}

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
		"Change this session's mode: /permissions mode auto|manual|acceptEdits|dontAsk|plan|bypassPermissions",
		"Mode changes apply to this open session; /reconnect or reopening uses the default in /settings.",
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
