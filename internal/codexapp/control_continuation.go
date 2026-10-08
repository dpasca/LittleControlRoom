package codexapp

import (
	"fmt"
	"time"
)

// ControlInputState is live host state, never reconstructed from assistant prose
// or an idle transcript. Every input attempt and explicit stop invalidates older
// continuations, including a steer within the same provider turn.
type ControlInputState struct {
	Revision    uint64
	SubmittedAt time.Time
	Stopped     bool
}

// accept runs under the provider's session mutex, at the input boundary shared
// by desktop, mobile and host delivery. The guard cannot race a newer input.
func (s *ControlInputState) accept(input Submission) error {
	if expected := input.ExpectedControlInput; expected != nil {
		if expected.Revision == 0 || s.Revision != expected.Revision || !s.SubmittedAt.Equal(expected.SubmittedAt) || s.Stopped {
			return fmt.Errorf("%w: the waiting workflow was stopped or superseded", ErrSessionChanged)
		}
	}
	s.Revision++
	s.SubmittedAt = time.Now()
	s.Stopped = false
	return nil
}

func (s *ControlInputState) stop() {
	s.Revision++
	s.Stopped = true
}

// Observed provider input/turn boundaries invalidate a wait without revoking
// an explicit stop. Only a new host submission may clear that stop.
func (s *ControlInputState) observeInput() {
	s.Revision++
	s.SubmittedAt = time.Now()
}
