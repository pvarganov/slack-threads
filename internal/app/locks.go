package app

import (
	"fmt"
	"sync"
)

// keyAll is the lock key held by RefreshAll: it is exclusive against every
// per-thread sync, because it walks all of them itself.
const keyAll = "*"

// keyLock is a set of named locks. Every sync entry point takes one, so
// two refreshes of the same thread can never run at once: the second call
// fails fast with ErrBusy instead of queueing behind the first and
// duplicating Slack traffic and translation turns.
type keyLock struct {
	mu   sync.Mutex
	held map[string]struct{}
}

// newKeyLock returns an empty lock set.
func newKeyLock() *keyLock {
	return &keyLock{held: make(map[string]struct{})}
}

// acquire takes key unless it, or the exclusive key, is already held.
func (l *keyLock) acquire(key string) bool {
	l.mu.Lock()
	defer l.mu.Unlock()

	if _, busy := l.held[key]; busy {
		return false
	}

	if _, busy := l.held[keyAll]; busy {
		return false
	}

	l.held[key] = struct{}{}

	return true
}

// acquireAll takes the exclusive key, and only when nothing else is held.
func (l *keyLock) acquireAll() bool {
	l.mu.Lock()
	defer l.mu.Unlock()

	if len(l.held) > 0 {
		return false
	}

	l.held[keyAll] = struct{}{}

	return true
}

// release drops a previously acquired key.
func (l *keyLock) release(key string) {
	l.mu.Lock()
	defer l.mu.Unlock()

	delete(l.held, key)
}

// threadKey is the lock key of a tracked thread.
func threadKey(id int64) string {
	return fmt.Sprintf("thread:%d", id)
}

// urlKey is the lock key used while adding a thread, before its local ID
// is known: a double click on «Добавить» must not fetch twice.
func urlKey(rawURL string) string {
	return "url:" + rawURL
}
