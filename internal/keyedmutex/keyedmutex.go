package keyedmutex

import (
	"context"
	"sync"
	"time"
)

// Locker serializes work for the same key while allowing different keys to
// proceed independently.
type Locker struct {
	mu    sync.Mutex
	locks map[string]*entry
}

type entry struct {
	mu   sync.Mutex
	refs int
}

func (l *Locker) Lock(key string) func() {
	if key == "" {
		return func() {}
	}

	unlock, _ := l.LockContext(context.Background(), key)
	return unlock
}

// LockContext waits for the lock for key until ctx is done.
func (l *Locker) LockContext(ctx context.Context, key string) (func(), error) {
	if key == "" {
		return func() {}, nil
	}
	if ctx == nil {
		ctx = context.Background()
	}

	l.mu.Lock()
	if l.locks == nil {
		l.locks = make(map[string]*entry)
	}
	current := l.locks[key]
	if current == nil {
		current = &entry{}
		l.locks[key] = current
	}
	current.refs++
	l.mu.Unlock()

	for {
		if current.mu.TryLock() {
			break
		}
		select {
		case <-ctx.Done():
			l.releaseRef(key, current)
			return nil, ctx.Err()
		case <-time.After(10 * time.Millisecond):
		}
	}

	released := false
	return func() {
		if released {
			return
		}
		released = true
		current.mu.Unlock()
		l.releaseRef(key, current)
	}, nil
}

func (l *Locker) releaseRef(key string, current *entry) {
	l.mu.Lock()
	defer l.mu.Unlock()
	current.refs--
	if current.refs == 0 {
		delete(l.locks, key)
	}
}
