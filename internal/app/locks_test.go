package app

import "testing"

func TestKeyLockIsPerKey(t *testing.T) {
	l := newKeyLock()

	if !l.acquire(threadKey(1)) {
		t.Fatal("first acquire failed")
	}

	if l.acquire(threadKey(1)) {
		t.Error("the same key was handed out twice")
	}

	if !l.acquire(threadKey(2)) {
		t.Error("another key was blocked")
	}

	l.release(threadKey(1))

	if !l.acquire(threadKey(1)) {
		t.Error("a released key stayed locked")
	}
}

func TestAcquireAllIsExclusive(t *testing.T) {
	l := newKeyLock()

	if !l.acquire(threadKey(1)) {
		t.Fatal("first acquire failed")
	}

	if l.acquireAll() {
		t.Error("acquireAll succeeded while a thread was locked")
	}

	l.release(threadKey(1))

	if !l.acquireAll() {
		t.Fatal("acquireAll failed on an idle lock set")
	}

	if l.acquire(threadKey(1)) {
		t.Error("a thread lock was handed out during an exclusive hold")
	}

	l.release(keyAll)

	if !l.acquire(threadKey(1)) {
		t.Error("the lock set stayed exclusive after release")
	}
}

func TestURLKeyDistinguishesLinks(t *testing.T) {
	if urlKey("a") == urlKey("b") {
		t.Error("different links share a lock key")
	}

	if urlKey("a") == threadKey(1) {
		t.Error("a link and a thread share a lock key")
	}
}
