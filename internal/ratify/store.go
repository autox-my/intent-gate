package ratify

import "intentgate/internal/lock"

// Store writes a lock and its root intent record (stub).
type Store struct {
	RepoRoot string
	StateDir string
}

// Write stores the root intent record, then the lock (stub).
func (s Store) Write(l lock.Lock, maskedRootIntent string) error { return nil }
