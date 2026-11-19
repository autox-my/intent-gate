// Package policy maps a verify result to a check-run conclusion and a comment decision. It is
// pure: no I/O, clock or randomness.
package policy

import "github.com/autox-my/intent-gate/internal/verify"

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

// Titles of the check run, one per outcome (pr-check.md §4).
const (
	TitleNoLock       = "No intent lock in this pull request"
	TitleClean        = "All criteria inside their footprints"
	TitleFindings     = "Changes outside the intent lock's footprints, or a suspect lock"
	TitleUnverifiable = "The intent lock could not be verified"
)

// Decide maps r to a decision (pr-check.md §4). Phase 0 is advisory: the conclusion is always
// success or neutral (REQ-0039), and a skipped pull request publishes nothing (REQ-0034).
func Decide(r verify.Result) Decision {
	switch r.Outcome {
	case verify.Skipped:
		return Decision{Publish: false, Conclusion: "neutral", Comment: None}
	case verify.NoLock:
		// REQ-0033: no lock, no comment
		return Decision{Publish: true, Conclusion: "neutral", Comment: None, Title: TitleNoLock}
	case verify.Clean:
		// REQ-0038: a clean result never creates a comment; an existing one is edited
		return Decision{Publish: true, Conclusion: "success", Comment: UpdateIfExists, Title: TitleClean}
	case verify.Findings:
		return Decision{Publish: true, Conclusion: "neutral", Comment: Upsert, Title: TitleFindings}
	case verify.Unverifiable:
		return Decision{Publish: true, Conclusion: "neutral", Comment: Upsert, Title: TitleUnverifiable}
	default:
		// an outcome this version does not know: stay advisory and say nothing in a comment
		return Decision{Publish: true, Conclusion: "neutral", Comment: None, Title: "IntentGate could not decide"}
	}
}
