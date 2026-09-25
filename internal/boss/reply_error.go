package boss

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"
)

type chatReplyError struct {
	Stage string
	Err   error
}

func (e chatReplyError) Error() string { return e.Stage + ": " + e.Err.Error() }
func (e chatReplyError) Unwrap() error { return e.Err }

func chatReplyFailure(err error, elapsed time.Duration) (string, string) {
	stage := "processing the reply"
	var failure chatReplyError
	if errors.As(err, &failure) && strings.TrimSpace(failure.Stage) != "" {
		stage = failure.Stage
	}
	if errors.Is(err, context.DeadlineExceeded) {
		duration := ""
		if elapsed > 0 {
			duration = " after " + elapsed.Round(time.Second).String()
		}
		return fmt.Sprintf("Chat timed out%s while %s.", duration, stage), "Chat timed out"
	}
	if errors.Is(err, context.Canceled) {
		return "Chat was interrupted while " + stage + ".", "Chat interrupted"
	}
	return "Chat could not finish the reply: " + err.Error(), "Chat could not answer"
}

// Use the user/host cancellation context, not the reply deadline: a deadline
// must not discard the final error's stage, usage and already retrieved sources.
func sendAssistantStreamReply(ctx context.Context, events chan<- assistantStreamEnvelope, reply assistantStreamEnvelope) {
	select {
	case events <- reply:
	case <-ctx.Done():
	}
}
