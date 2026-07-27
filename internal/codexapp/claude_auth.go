package codexapp

import (
	"context"
	"errors"

	"lcroom/internal/claudecli"
)

var ErrClaudeCodeAuthenticationRequired = errors.New(
	"Claude Code sign-in is missing or expired. Run `claude auth login` in a terminal, complete sign-in, then retry this prompt.",
)

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
