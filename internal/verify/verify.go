// Package verify is the pure decision core of the PR check: from the pull request's inputs, as
// values, it decides skip / no lock / lifecycle / footprint / outcome. It does no I/O, reads no
// clock, uses no randomness and makes no network call (REQ-0040).
package verify

import (
	"time"

	"intentgate/internal/footprint"
)

// LockFile is one changed path under .intent/ ending in .yml, with its content at head (nil when
// the file was deleted).
type LockFile struct {
	Path    string
	Status  footprint.Status
	OldPath string
	Data    []byte
}

// Input is everything the check looks at. It has no field for the pull request's title,
// description or commit messages, so criteria can't come from them (REQ-0032).
type Input struct {
	BaseBranch, Author, AuthorType string
	EvalTime                       time.Time
	Changed                        []footprint.ChangedFile
	LockFiles                      []LockFile
	Config, GitAttributes          []byte
}

// Outcome is the verdict of one run.
type Outcome string

// The outcomes, in the order of pr-check.md §4.
const (
	Skipped      Outcome = "skipped"
	NoLock       Outcome = "no-lock"
	Clean        Outcome = "clean"
	Findings     Outcome = "findings"
	Unverifiable Outcome = "unverifiable"
)

// Suspect reasons (pr-check.md §2, §3).
const (
	ReasonExpired      = "expired"
	ReasonHashMismatch = "hash-mismatch"
	ReasonModifiesLock = "modifies-existing-lock"
	ReasonRetargeted   = "retargeted"
)

// Criterion is one criterion of a usable lock, as shown in the report.
type Criterion struct {
	ID        string   `json:"id"`
	Text      string   `json:"text"`
	Footprint []string `json:"footprint"`
}

// LockResult is what the check found out about one lock file.
type LockResult struct {
	Path     string      `json:"path"`
	ID       string      `json:"id"`
	Revision int         `json:"revision"`
	Suspect  []string    `json:"suspect"`
	Problems []string    `json:"problems"`
	Criteria []Criterion `json:"criteria"`
}

// Result is the outcome of Run. Problems are document-level problems (a malformed config).
type Result struct {
	Outcome  Outcome
	ExitCode int
	Problems []string
	Locks    []LockResult
	Findings []footprint.Finding
}

// Run decides the outcome for in. It never mutates in.
func Run(in Input) Result {
	return Result{}
}
