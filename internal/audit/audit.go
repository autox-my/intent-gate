// Package audit builds the intentgate.audit/1 event (audit-metrics.md §1). Stub: the red commit of S-10.
package audit

import (
	"time"

	"intentgate/internal/lock"
)

// SchemaID is the value of an event's schema key.
const SchemaID = "intentgate.audit/1"

// Event kinds.
const (
	EventVerify = "verify"
	EventLock   = "lock"
	EventAmend  = "amend"
)

// LockEntry is one lock an event is about.
type LockEntry struct {
	ID                 string `json:"id"`
	Revision           int    `json:"revision"`
	Hash               string `json:"hash"`
	RatificationMethod string `json:"ratification_method"`
	ReviewDurationMS   *int   `json:"review_duration_ms"`
}

// Event is one audit event.
type Event struct {
	Schema            string      `json:"schema"`
	Event             string      `json:"event"`
	At                string      `json:"at"`
	Repo              string      `json:"repo"`
	PR                int         `json:"pr"`
	BaseSHA           string      `json:"base_sha"`
	HeadSHA           string      `json:"head_sha"`
	BaseBranch        string      `json:"base_branch"`
	Author            string      `json:"author"`
	AuthorType        string      `json:"author_type"`
	IntentGateVersion string      `json:"intentgate_version"`
	ConfigSHA256      string      `json:"config_sha256"`
	VerifySHA256      string      `json:"verify_sha256"`
	Locks             []LockEntry `json:"locks"`
	Outcome           string      `json:"outcome"`
	ExitCode          int         `json:"exit_code"`
	Conclusion        string      `json:"conclusion"`
}

// Verify is what a verify event records besides its time, version, repository and pull request.
type Verify struct {
	BaseSHA, HeadSHA, BaseBranch, Author, AuthorType string
	ConfigSHA256, VerifySHA256                       string
	Locks                                            []lock.Lock
	Outcome                                          string
	ExitCode                                         int
	Conclusion                                       string
}

// NewVerifyEvent is the event of one `verify` run.
func NewVerifyEvent(at time.Time, version, repo string, pr int, v Verify) Event { return Event{} }

// NewLockEvent is the event of one written lock or amendment.
func NewLockEvent(event string, l lock.Lock, at time.Time, version, repo string) Event {
	return Event{}
}

// JSON renders the event on one line with a trailing newline.
func JSON(e Event) []byte { return nil }
