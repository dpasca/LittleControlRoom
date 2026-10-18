package codexapp

import (
	"context"
	"errors"

	"lcroom/internal/claudecli"
)

var ErrClaudeCodeAuthenticationRequired = errors.New(
	"Claude Code authentication is missing or expired. Open /claude-auth to connect or replace a subscription token, or run `claude auth login` in a terminal. Use /reconnect for an existing Claude session, then retry this prompt.",
)

var applyClaudeSubscriptionToken = claudecli.ApplySubscriptionToken

func CheckClaudeCodeAuthentication(ctx context.Context) error {
	status, err := claudecli.ReadAuthenticationStatus(ctx)
	if err != nil {
		// Older or customized Claude Code installations may not expose the
		// structured auth command. Let the real turn proceed when auth state is
		// inconclusive instead of rejecting an otherwise valid installation.
		return nil
	}
	if !status.LoggedIn {
		return ErrClaudeCodeAuthenticationRequired
	}
	return nil
}

func claudeAPIErrorDisplayText(code, text string) string {
	// These are SDK error codes, not guesses based on the provider's prose.
	switch code {
	case "authentication_failed", "authentication_error":
		return text + "\n\nOpen /claude-auth to connect or replace your subscription token, then reconnect the Claude session and retry. If you use native login, run `claude auth login` in a terminal instead."
	default:
		return ""
	}
}
