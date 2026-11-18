// Package policy maps a verify result to a check-run conclusion and a comment decision. It is
// pure: no I/O, clock or randomness.
package policy

import "intentgate/internal/verify"

// CommentMode says what the comment step does.
type CommentMode string

const (
	// None posts and edits nothing.
	None CommentMode = "none"
	// Upsert creates the comment, or edits the existing one.
	Upsert CommentMode = "upsert"
	// UpdateIfExists edits an existing comment and never creates one.
	UpdateIfExists CommentMode = "update-if-exists"
)

// Decision is what to publish.
type Decision struct {
	Publish    bool
	Conclusion string
	Comment    CommentMode
	Title      string
}

// Decide maps r to a decision (pr-check.md §4).
func Decide(r verify.Result) Decision {
	return Decision{}
}
