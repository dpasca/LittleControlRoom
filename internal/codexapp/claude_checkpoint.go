package codexapp

import (
	"context"
	"fmt"
	"strings"
	"time"
)

// CheckpointRequester interrupts current work and requests a bounded report in
// the same conversation. It does not accept the work or release its task lease.
type CheckpointRequester interface {
	RequestCheckpoint(context.Context, string) error
}

func (s *claudeCodeSession) RequestCheckpoint(ctx context.Context, note string) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	expected, tasks, err := s.interruptClaudeTurn(true)
	if err != nil {
		return err
	}
	defer func() {
		s.mu.Lock()
		s.checkpointPending = false
		s.mu.Unlock()
		s.notifyAsync()
	}()

	ticker := time.NewTicker(50 * time.Millisecond)
	defer ticker.Stop()
	for {
		if err := ctx.Err(); err != nil {
			return fmt.Errorf("checkpoint report was not started: %w", err)
		}
		s.mu.Lock()
		changed := s.closed || s.controlInput != expected
		settled := !s.busy && s.cmd == nil && !s.externalTurnActive
		s.mu.Unlock()
		if changed {
			return fmt.Errorf("%w: checkpoint canceled by a stop, close or newer input", ErrSessionChanged)
		}
		if settled {
			return s.submitInput(Submission{
				Text:                 claudeCheckpointPrompt(note, tasks),
				DisplayText:          strings.TrimSpace("/checkpoint " + note),
				ExpectedControlInput: &expected,
				RequireIdle:          true,
				BeforeStart:          ctx.Err,
			}, claudeSubmissionCheckpoint, nil)
		}
		select {
		case <-ctx.Done():
			return fmt.Errorf("checkpoint report was not started: %w", ctx.Err())
		case <-ticker.C:
		}
	}
}

func claudeCheckpointPrompt(note string, tasks []BackgroundTaskSnapshot) string {
	var out strings.Builder
	out.WriteString(`The operator requested an immediate checkpoint. The host interrupted the previous turn so this report would not wait behind a long tool call.

Preserve the current edits and existing evidence. Stop launching builds, tests, sweeps, workers or new implementation work. Do not commit, push, merge or claim incomplete validation passed.

Use existing evidence to report: changes made; acceptance criteria passed, failed and unverified; exact evidence paths and tested revisions; blockers; and the smallest remaining work. If a read is needed, keep it short and bounded. Do not wait for outstanding jobs or rerun validation to finish this report.

Reconcile job ownership: identify each job you started, its last known state, and whether it is still running. The interruption does not prove background or remote jobs stopped. Stop only your own redundant polling waiters where their identity is certain. Do not stop other sessions' jobs. Name any jobs left running and their IDs/receipts in the report.

If this is a structured LCR task, submit the current result with honest incomplete/blocked criteria and risks, then end the turn. Otherwise return this checkpoint report and end the turn. Wait for explicit instructions before resuming implementation or validation.
`)
	if len(tasks) > 0 {
		out.WriteString("\nHost-observed jobs before interruption (last known state, not proof of current activity):\n")
		for _, task := range tasks {
			fmt.Fprintf(&out, "- id=%q tool=%q status=%q output=%q description=%q\n", task.ID, task.Tool, task.Status, task.OutputPath, task.Description)
		}
	}
	if note = strings.TrimSpace(note); note != "" {
		out.WriteString("\nOperator checkpoint instructions:\n" + note)
	}
	return out.String()
}
