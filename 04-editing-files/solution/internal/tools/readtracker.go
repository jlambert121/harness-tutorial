package tools

import (
	"fmt"
	"os"
	"sync"
	"time"
)

// ReadTracker remembers which files the model has seen, and their
// modification time when it saw them, so edits can require a fresh read.
type ReadTracker struct {
	mu    sync.Mutex
	seen  map[string]time.Time
	locks map[string]*sync.Mutex
}

func NewReadTracker() *ReadTracker {
	return &ReadTracker{seen: map[string]time.Time{}, locks: map[string]*sync.Mutex{}}
}

func (t *ReadTracker) MarkRead(path string, mtime time.Time) {
	if t == nil {
		return
	}
	t.mu.Lock()
	defer t.mu.Unlock()
	t.seen[path] = mtime
}

// Check returns an error unless path was read and has not changed since.
func (t *ReadTracker) Check(path string) error {
	if t == nil {
		return nil
	}
	t.mu.Lock()
	seen, ok := t.seen[path]
	t.mu.Unlock()
	if !ok {
		return fmt.Errorf("you must read the file first: use read on %s before editing it", path)
	}
	fi, err := os.Stat(path)
	if err != nil {
		return err
	}
	if !fi.ModTime().Equal(seen) {
		return fmt.Errorf("%s was modified since you last read it; read it again before editing", path)
	}
	return nil
}

// Lock serializes mutations of one file.
func (t *ReadTracker) Lock(path string) func() {
	if t == nil {
		return func() {}
	}
	t.mu.Lock()
	l, ok := t.locks[path]
	if !ok {
		l = &sync.Mutex{}
		t.locks[path] = l
	}
	t.mu.Unlock()
	l.Lock()
	return l.Unlock
}
