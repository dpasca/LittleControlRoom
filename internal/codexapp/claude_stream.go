package codexapp

import (
	"context"
	"crypto/rand"
	"encoding/json"
	"fmt"
	"io"
	"strings"
	"time"
)

const claudeStreamInitializeID = "lcr-stream-initialize"

func (s *claudeCodeSession) claudeStagedChangeTimingLocked() string {
	if s.managedStream && s.cmd != nil {
		return "on the next prompt after current work finishes"
	}
	return "on the next prompt"
}

func newClaudeMessageID() string {
	var id [16]byte
	_, _ = rand.Read(id[:])
	id[6] = (id[6] & 0x0f) | 0x40
	id[8] = (id[8] & 0x3f) | 0x80
	return fmt.Sprintf("%x-%x-%x-%x-%x", id[:4], id[4:6], id[6:8], id[8:10], id[10:])
}

func initializeClaudeStream(ctx context.Context, stdin io.Writer, result <-chan error) error {
	request := `{"type":"control_request","request_id":"` + claudeStreamInitializeID + `","request":{"subtype":"initialize"}}` + "\n"
	if _, err := io.WriteString(stdin, request); err != nil {
		return fmt.Errorf("initialize Claude stream: %w", err)
	}
	timer := time.NewTimer(30 * time.Second)
	defer timer.Stop()
	select {
	case err := <-result:
		return err
	case <-ctx.Done():
		return ctx.Err()
	case <-timer.C:
		return fmt.Errorf("Claude Code did not initialize its stream within 30 seconds")
	}
}

func (s *claudeCodeSession) handleClaudeControlResponseLocked(raw json.RawMessage) {
	var response struct {
		RequestID string `json:"request_id"`
		Subtype   string `json:"subtype"`
		Error     string `json:"error"`
	}
	if json.Unmarshal(raw, &response) != nil || response.RequestID != claudeStreamInitializeID || s.streamInitResult == nil {
		return
	}
	var err error
	if response.Subtype != "success" {
		err = fmt.Errorf("Claude stream initialization failed: %s", response.Error)
	}
	select {
	case s.streamInitResult <- err:
	default:
	}
	s.streamInitResult = nil
}

func (s *claudeCodeSession) addClaudeDeliveryLocked(id, text string, at time.Time) {
	// Keep every outstanding message and a bounded recent receipt history.
	if len(s.messageDeliveries) >= 20 {
		kept := s.messageDeliveries[:0]
		for i, d := range s.messageDeliveries {
			if d.Pending() || i >= len(s.messageDeliveries)-19 {
				kept = append(kept, d)
			}
		}
		s.messageDeliveries = kept
	}
	preview := []rune(strings.Join(strings.Fields(text), " "))
	if len(preview) > 160 {
		preview = append(preview[:157], '.', '.', '.')
	}
	s.messageDeliveries = append(s.messageDeliveries, MessageDeliverySnapshot{
		ID: id, Preview: string(preview), State: "sending", SubmittedAt: at, UpdatedAt: at,
	})
}

func (s *claudeCodeSession) updateClaudeDeliveryLocked(id, state string, at time.Time) {
	if id == "" {
		return
	}
	switch state {
	case "sent", "received", "queued", "started", "completed", "cancelled", "discarded", "refused", "failed", "interrupted", "unknown":
	default:
		return
	}
	for i := range s.messageDeliveries {
		d := &s.messageDeliveries[i]
		if d.ID != id || !d.Pending() || d.State == state {
			continue
		}
		// A write or replay acknowledgment can arrive after the lifecycle
		// event. Never regress a provider-confirmed receipt.
		if state == "sent" && d.State != "sending" {
			return
		}
		if (state == "received" || state == "queued") && (d.State == "started" || d.State == "queued") {
			return
		}
		d.State, d.UpdatedAt = state, at
		if state == "started" {
			s.lastParentActivityAt = at
		}
		if s.managedStream && !d.Pending() && s.pendingSubmissions > 0 {
			s.pendingSubmissions--
			if s.pendingSubmissions == 0 {
				s.latestSubmittedAt = time.Time{}
			}
		}
		return
	}
}

func (s *claudeCodeSession) finishClaudeDeliveriesLocked(interrupted bool) {
	state := "unknown"
	if interrupted {
		state = "interrupted"
	}
	for i := range s.messageDeliveries {
		if s.messageDeliveries[i].Pending() {
			s.messageDeliveries[i].State = state
			s.messageDeliveries[i].UpdatedAt = time.Now()
		}
	}
}

func (s *claudeCodeSession) handleClaudeSessionStateLocked(state string, at time.Time) io.WriteCloser {
	if !s.managedStream {
		return nil
	}
	switch state {
	case "running", "requires_action":
		s.streamState = state
		s.busy = true
		if s.busySince.IsZero() {
			s.busySince = at
		}
		return nil
	case "idle":
		s.streamState = state
	default:
		return nil
	}
	if s.pendingSubmissions > 0 || s.runningBackgroundTaskCountLocked() > 0 || s.pendingClaudeInput != nil {
		return nil
	}
	if s.browserHandoffPending {
		s.busy = false
		s.busySince = time.Time{}
		return nil
	}
	// Only an idle event can release the stream. A task terminal event may be
	// immediately followed by a parent turn that handles its completion; the
	// previous idle state is insufficient evidence to close stdin then.
	stdin := s.stdin
	s.stdin = nil
	s.latestTurnCompleted = true
	s.latestTurnStateKnown = true
	s.latestTurnVerified = true
	s.latestTurnStateAt = at
	s.setClaudeBrowserActivityIdleLocked()
	return stdin
}

func (s *claudeCodeSession) claudeParentStatusLocked() string {
	if s.cmd == nil || !s.managedStream {
		return ""
	}
	switch s.streamState {
	case "idle":
		if s.runningBackgroundTaskCountLocked() > 0 {
			return "Parent available · background work running"
		}
		return "Parent idle"
	case "requires_action":
		return "Parent waiting for input"
	case "running":
		return "Parent working"
	default:
		return "Parent starting"
	}
}
