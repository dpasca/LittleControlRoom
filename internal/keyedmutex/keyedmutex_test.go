package keyedmutex

import (
	"context"
	"errors"
	"testing"
	"time"
)

func TestLockContextRespectsDeadlineWhileKeyHeld(t *testing.T) {
	var locker Locker
	unlock := locker.Lock("project")
	defer unlock()

	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Millisecond)
	defer cancel()

	waitedAt := time.Now()
	gotUnlock, err := locker.LockContext(ctx, "project")
	if gotUnlock != nil {
		gotUnlock()
	}
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("LockContext() error = %v, want deadline exceeded", err)
	}
	if time.Since(waitedAt) > 250*time.Millisecond {
		t.Fatalf("LockContext() waited too long after deadline: %s", time.Since(waitedAt))
	}
}

func TestLockContextSerializesSameKey(t *testing.T) {
	var locker Locker
	unlock := locker.Lock("project")

	done := make(chan struct{})
	go func() {
		nextUnlock, err := locker.LockContext(context.Background(), "project")
		if err != nil {
			t.Errorf("LockContext() error = %v", err)
			close(done)
			return
		}
		nextUnlock()
		close(done)
	}()

	select {
	case <-done:
		t.Fatal("same-key lock was acquired before first lock released")
	case <-time.After(25 * time.Millisecond):
	}

	unlock()
	select {
	case <-done:
	case <-time.After(250 * time.Millisecond):
		t.Fatal("same-key lock was not acquired after release")
	}
}
