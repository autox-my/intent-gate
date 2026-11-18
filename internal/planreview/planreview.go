// Package planreview compares a drafted plan with the root intent and reports omissions,
// additions and weakenings to the human at ratification (docs/design/plan-review.md). It is
// advisory: no status stops or delays a lock beyond the budget (REQ-0019, REQ-0020).
package planreview

import (
	"context"
	"errors"
	"fmt"
	"sort"
	"strings"
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

// grace is how much of the budget the provider's own deadline leaves for Run to wrap up.
const grace = 250 * time.Millisecond

// maxShown is how many findings Display shows per kind.
const maxShown = 5

type outcome struct {
	findings []Finding
	err      error
	panicked bool
}

// Run asks r for findings and returns no later than budget after the call, whatever r does
// (REQ-0019). r.Review runs in a goroutine under a context deadline of budget − 250 ms; a
// provider that ignores its context is abandoned and its late result is discarded.
//
// Status: no error is complete; a context error with findings is partial, without is
// unavailable; ErrUnavailable is unavailable; any other error is error (findings kept); a
// provider panic is recovered and is error. A nil r is Unavailable.
func Run(ctx context.Context, r Reviewer, rootIntent, plan string, budget time.Duration) Result {
	if r == nil {
		r = Unavailable{}
	}
	inner := budget - grace
	if inner <= 0 {
		inner = budget / 2
	}
	rctx, cancel := context.WithTimeout(ctx, inner)
	defer cancel()

	done := make(chan outcome, 1) // buffered: an abandoned provider never blocks on it
	go func() {
		var o outcome
		defer func() {
			if recover() != nil {
				o = outcome{panicked: true} // the value is not kept: it could quote the intent
			}
			done <- o
		}()
		o.findings, o.err = r.Review(rctx, rootIntent, plan)
	}()

	timer := time.NewTimer(budget)
	defer timer.Stop()
	select {
	case o := <-done:
		return classify(o)
	case <-timer.C:
	case <-ctx.Done():
	}
	// The provider outlived the budget or the caller's context. Take what it has already
	// finished, if anything; its goroutine is abandoned.
	select {
	case o := <-done:
		return classify(o)
	default:
		return Result{Status: Unavail}
	}
}

func classify(o outcome) Result {
	if o.panicked {
		return Result{Status: ErrorStatus, Err: "the provider panicked"}
	}
	res := Result{Findings: valid(o.findings)}
	res.Counts = count(res.Findings)
	switch {
	case o.err == nil:
		res.Status = Complete
	case errors.Is(o.err, ErrUnavailable):
		res.Status = Unavail
	case errors.Is(o.err, context.DeadlineExceeded), errors.Is(o.err, context.Canceled):
		res.Status = Partial
		if len(res.Findings) == 0 {
			res.Status = Unavail
		}
	default:
		res.Status = ErrorStatus
		res.Err = o.err.Error()
	}
	return res
}

// valid keeps the findings of a known kind, in the order given.
func valid(in []Finding) []Finding {
	var out []Finding
	for _, f := range in {
		switch f.Kind {
		case Omission, Addition, Weakening:
			out = append(out, f)
		}
	}
	return out
}

func count(fs []Finding) Counts {
	var c Counts
	for _, f := range fs {
		switch f.Kind {
		case Omission:
			c.Omissions++
		case Addition:
			c.Additions++
		case Weakening:
			c.Weakenings++
		}
	}
	return c
}

// Failed is a Result for a review that could not even start.
func Failed(msg string) Result { return Result{Status: ErrorStatus, Err: msg} }

// Valid reports whether s is one of the four statuses.
func (s Status) Valid() bool {
	switch s {
	case Complete, Partial, Unavail, ErrorStatus:
		return true
	}
	return false
}

// Display renders the result for the human: a status line, then per kind at most 5 findings by
// confidence (ties by quote, then text), each with its quote, and "+N more" for the rest. It is
// pure: the same Result gives the same text.
func Display(res Result) string {
	var b strings.Builder
	b.WriteString("Plan review: ")
	switch res.Status {
	case Complete:
		b.WriteString("complete")
	case Partial:
		b.WriteString("partial: the 5 s budget ran out")
	case Unavail:
		b.WriteString("unavailable")
	default:
		b.WriteString("error")
		if res.Err != "" {
			b.WriteString(": " + res.Err)
		}
	}
	b.WriteByte('\n')
	for _, g := range []struct {
		kind  Kind
		title string
	}{{Omission, "Omissions (the plan drops what you asked for)"}, {Addition, "Additions (the plan adds what you didn't ask for)"}, {Weakening, "Weakenings (the plan loosens what you asked for)"}} {
		var fs []Finding
		for _, f := range res.Findings {
			if f.Kind == g.kind {
				fs = append(fs, f)
			}
		}
		if len(fs) == 0 {
			continue
		}
		sort.SliceStable(fs, func(i, j int) bool {
			a, c := fs[i], fs[j]
			switch {
			case a.Confidence != c.Confidence:
				return a.Confidence > c.Confidence
			case a.Quote != c.Quote:
				return a.Quote < c.Quote
			}
			return a.Text < c.Text
		})
		fmt.Fprintf(&b, "%s:\n", g.title)
		for i, f := range fs {
			if i == maxShown {
				fmt.Fprintf(&b, "  +%d more\n", len(fs)-maxShown)
				break
			}
			fmt.Fprintf(&b, "  - %q: %s\n", f.Quote, f.Text)
		}
	}
	return b.String()
}

// Select chooses the provider from the environment of the machine that ratifies.
// INTENTGATE_PLAN_REVIEW=off is Unavailable; in this slice so is everything else (S-12 adds Jev).
func Select(getenv func(string) string) Reviewer {
	if getenv != nil && getenv("INTENTGATE_PLAN_REVIEW") == "off" {
		return Unavailable{}
	}
	return Unavailable{}
}
