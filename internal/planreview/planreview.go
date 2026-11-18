// Package planreview compares a drafted plan with the root intent and reports omissions,
// additions and weakenings to the human at ratification (docs/design/plan-review.md). It is
// advisory: no status stops or delays a lock beyond the budget (REQ-0019, REQ-0020).
package planreview

import (
	"context"
	"errors"
	"time"
)

// Kind is what a finding says about the plan.
type Kind string

const (
	Omission  Kind = "omission"
	Addition  Kind = "addition"
	Weakening Kind = "weakening"
)

// Finding is one thing the review noticed, with the text it is about.
type Finding struct {
	Kind       Kind
	Quote      string
	Text       string
	Confidence float64
}

// Status is how the review ended.
type Status string

const (
	Complete    Status = "complete"
	Partial     Status = "partial"
	Unavail     Status = "unavailable"
	ErrorStatus Status = "error"
)

// Counts is the number of findings per kind.
type Counts struct{ Omissions, Additions, Weakenings int }

// Result is the outcome of one review.
type Result struct {
	Status   Status
	Findings []Finding
	Counts   Counts
	Err      string // why the status is error; shown to the human, never stored
}

// Reviewer judges a plan against the root intent. On ctx done it returns the findings gathered
// so far with ctx.Err().
type Reviewer interface {
	Review(ctx context.Context, rootIntent, plan string) ([]Finding, error)
}

// ErrUnavailable is what a provider returns when it cannot review at all.
var ErrUnavailable = errors.New("plan review unavailable")

// Unavailable is the provider used when no model is configured.
type Unavailable struct{}

// Review returns no findings and ErrUnavailable.
func (Unavailable) Review(context.Context, string, string) ([]Finding, error) {
	return nil, ErrUnavailable
}

// Run is a stub.
func Run(ctx context.Context, r Reviewer, rootIntent, plan string, budget time.Duration) Result {
	return Result{Status: Unavail}
}

// Failed is a Result for a review that could not even start.
func Failed(msg string) Result { return Result{Status: ErrorStatus, Err: msg} }

// Display is a stub.
func Display(res Result) string { return "" }

// Select is a stub.
func Select(getenv func(string) string) Reviewer { return Unavailable{} }
