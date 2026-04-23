package keyedmutex

import "sync"

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

	current.mu.Lock()

	released := false
	return func() {
		if released {
			return
		}
		released = true
		current.mu.Unlock()

		l.mu.Lock()
		defer l.mu.Unlock()
		current.refs--
		if current.refs == 0 {
			delete(l.locks, key)
		}
	}
}
