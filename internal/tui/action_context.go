package tui

import (
	"context"
	"errors"
	"fmt"
	"time"
)

const (
	tuiQuickActionTimeout   = 8 * time.Second
	tuiProjectActionTimeout = 20 * time.Second
	tuiGitActionTimeout     = 60 * time.Second
	// Push can run repository hooks, including native release builds.
	tuiGitPushActionTimeout = 5 * time.Minute
)

func (m Model) actionContext(timeout time.Duration) (context.Context, context.CancelFunc) {
	ctx := m.ctx
	if ctx == nil {
		ctx = context.Background()
	}
	return context.WithTimeout(ctx, timeout)
}

func timeoutActionError(err error, timeout time.Duration, action string) error {
	if !errors.Is(err, context.DeadlineExceeded) {
		return err
	}
	return fmt.Errorf("timed out after %s while %s: %w", timeout.Round(time.Millisecond), action, err)
}
