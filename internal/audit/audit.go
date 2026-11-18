// Package audit builds the intentgate.audit/1 event (audit-metrics.md §1). It is pure: every
// value, the time included, is an argument; it reads no clock, file or environment. An event holds
// ids, hashes and counts only, never code, diffs, criterion text, footprints or root intent text.
package audit

import (
	"bytes"
	"encoding/json"
	"sort"
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

// LockEntry is one lock an event is about. ReviewDurationMS is null when the lock has none.
type LockEntry struct {
	ID                 string `json:"id"`
	Revision           int    `json:"revision"`
	Hash               string `json:"hash"`
	RatificationMethod string `json:"ratification_method"`
	ReviewDurationMS   *int   `json:"review_duration_ms"`
}

// Event is one audit event. The field order is the key order, and every key is always present: a
// value that does not apply to the event kind is "" (or 0 for pr and exit_code).
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
	UnverifiableLocks int         `json:"unverifiable_locks"`
	Outcome           string      `json:"outcome"`
	ExitCode          int         `json:"exit_code"`
	Conclusion        string      `json:"conclusion"`
}

const timeLayout = "2006-01-02T15:04:05Z"

// Verify is what a verify event records besides its time, version, repository and pull request.
type Verify struct {
	BaseSHA, HeadSHA, BaseBranch, Author, AuthorType string
	ConfigSHA256, VerifySHA256                       string
	Locks                                            []lock.Lock
	UnverifiableLocks                                int // changed lock files that are not in Locks; counted, never quoted
	Outcome                                          string
	ExitCode                                         int
	Conclusion                                       string
}

// NewVerifyEvent is the event of one `verify` run.
func NewVerifyEvent(at time.Time, version, repo string, pr int, v Verify) Event {
	return Event{
		Schema: SchemaID, Event: EventVerify, At: at.UTC().Truncate(time.Second).Format(timeLayout),
		Repo: repo, PR: pr, BaseSHA: v.BaseSHA, HeadSHA: v.HeadSHA, BaseBranch: v.BaseBranch,
		Author: v.Author, AuthorType: v.AuthorType, IntentGateVersion: version,
		ConfigSHA256: v.ConfigSHA256, VerifySHA256: v.VerifySHA256, Locks: entries(v.Locks...), UnverifiableLocks: v.UnverifiableLocks,
		Outcome: v.Outcome, ExitCode: v.ExitCode, Conclusion: v.Conclusion,
	}
}

// NewLockEvent is the event of one written lock or amendment: event is EventLock or EventAmend.
func NewLockEvent(event string, l lock.Lock, at time.Time, version, repo string) Event {
	return Event{
		Schema: SchemaID, Event: event, At: at.UTC().Truncate(time.Second).Format(timeLayout),
		Repo: repo, IntentGateVersion: version, Locks: entries(l), Outcome: "written",
	}
}

// entries lists the locks sorted by id, then revision, then hash.
func entries(ls ...lock.Lock) []LockEntry {
	out := make([]LockEntry, 0, len(ls))
	for _, l := range ls {
		e := LockEntry{ID: l.ID, Revision: l.Revision, Hash: l.Hash, RatificationMethod: l.Ratification.Method}
		if ms := l.Ratification.ReviewDurationMS; ms != nil {
			v := *ms
			e.ReviewDurationMS = &v
		}
		out = append(out, e)
	}
	sort.SliceStable(out, func(i, j int) bool {
		a, b := out[i], out[j]
		if a.ID != b.ID {
			return a.ID < b.ID
		}
		if a.Revision != b.Revision {
			return a.Revision < b.Revision
		}
		return a.Hash < b.Hash
	})
	return out
}

// JSON renders the event on one line with a trailing newline: the bytes of audit.json and one line
// of audit.jsonl.
func JSON(e Event) []byte {
	if e.Locks == nil {
		e.Locks = []LockEntry{}
	}
	var buf bytes.Buffer
	enc := json.NewEncoder(&buf)
	enc.SetEscapeHTML(false)
	if err := enc.Encode(e); err != nil {
		panic("audit: " + err.Error()) // strings and ints only: it always marshals
	}
	return buf.Bytes()
}
