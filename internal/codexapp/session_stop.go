package codexapp

import (
	"fmt"
	"strings"
	"time"
)

// StopProjectSession serializes a confirmed stop with session launch. The
// callbacks let the host verify ownership and cancel the current task run
// before another queued launch can acquire this project's operation lock.
func (m *Manager) StopProjectSession(path string, verify func(Snapshot) error, stopped func() error, timeout time.Duration) error {
	path = strings.TrimSpace(path)
	unlock := m.opLocks.Lock(path)
	defer unlock()
	m.mu.Lock()
	session := m.sessions[path]
	m.mu.Unlock()
	if session != nil {
		snapshot := sessionStateSnapshot(session)
		if snapshot.BusyExternal {
			return fmt.Errorf("cannot stop an externally owned session")
		}
		if err := verify(snapshot); err != nil {
			return err
		}
		if snapshot.Busy || snapshot.ActiveTurnID != "" || snapshot.Compacting || snapshot.PendingApproval != nil || snapshot.PendingToolInput != nil {
			if err := session.Interrupt(); err != nil {
				return fmt.Errorf("interrupt worker: %w", err)
			}
		}
		if err := session.Close(); err != nil {
			return fmt.Errorf("close worker: %w", err)
		}
		if waiter, ok := session.(closeWaiter); ok && !waiter.WaitClosed(timeout) {
			return fmt.Errorf("worker has not finished stopping; its repository ownership is retained")
		}
		if !sessionStateSnapshot(session).Closed {
			return fmt.Errorf("worker did not confirm closure")
		}
	}
	if err := stopped(); err != nil {
		return err
	}
	m.mu.Lock()
	delete(m.sessions, path)
	delete(m.sessionProviders, path)
	delete(m.idleProtected, path)
	m.mu.Unlock()
	m.notify(path)
	return nil
}
