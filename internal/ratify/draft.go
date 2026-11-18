// Package ratify is the shared ratification pipeline (docs/design/ratification.md §1): draft the
// criteria from a plan, mask secrets in the root intent, build and hash the lock, and write the lock
// plus the root intent record. Everything is pure except Store, which takes its directories as
// arguments; ids and times are passed in (no clock, no randomness, no home directory in here).
package ratify

import "intentgate/internal/lock"

// DraftCriteria parses the plan's one `intentgate` block into criteria (stub).
func DraftCriteria(plan string) ([]lock.Criterion, error) { return nil, nil }
