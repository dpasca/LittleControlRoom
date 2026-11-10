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

// Only an explicitly requested checkpoint may resume the stop it initiated.
// A second stop, close or input invalidates it at the same atomic boundary as
// other submissions; an ordinary continuation can never clear a stop.
func (s *ControlInputState) acceptCheckpoint(input Submission) error {
	expected := input.ExpectedControlInput
	if expected == nil || expected.Revision == 0 || !expected.Stopped ||
		s.Revision != expected.Revision || !s.SubmittedAt.Equal(expected.SubmittedAt) || !s.Stopped {
		return fmt.Errorf("%w: checkpoint was stopped or superseded", ErrSessionChanged)
	}
	s.Revision++
	s.SubmittedAt = time.Now()
	s.Stopped = false
	return nil
}

// Observed provider input/turn boundaries invalidate a wait without revoking
// an explicit stop. Only a new host submission may clear that stop.
func (s *ControlInputState) observeInput() {
	s.Revision++
	s.SubmittedAt = time.Now()
}
