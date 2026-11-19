package main

import (
	"context"
	"errors"
	"os"
	"strings"
	"time"

	"github.com/autox-my/intent-gate/internal/hooks/claudecode"
	"github.com/autox-my/intent-gate/internal/lock"
	"github.com/autox-my/intent-gate/internal/planreview"
	"github.com/autox-my/intent-gate/internal/ratify"
)

// The plan review's wiring (plan-review.md §4, §5): it runs on the ratifier's machine, on the
// masked root intent and the masked plan, is advisory, and never stops or delays a lock beyond
// its budget. Only its status and counts are kept; finding text is shown and then dropped.

// planReviewBudget is the review's time budget in production (REQ-0019).
const planReviewBudget = 5 * time.Second

// reviewer is the configured provider: the injected one, else the one the environment selects.
func (d Deps) reviewer() planreview.Reviewer {
	if d.PlanReviewer != nil {
		return d.PlanReviewer
	}
	return planreview.Select(os.Getenv)
}

// reviewBudget is deps.PlanReviewBudget, or 5 s when it is unset.
func (d Deps) reviewBudget() time.Duration {
	if d.PlanReviewBudget > 0 {
		return d.PlanReviewBudget
	}
	return planReviewBudget
}

// reviewSlack is kept back from the deadline so that the review step's own result, not the
// outer timeout, is what normally ends the wait.
const reviewSlack = 50 * time.Millisecond

// reviewStep is the whole review step under one deadline (REQ-0019, review 1 of S-11): getting
// the root intent, masking it, and the provider call all run in one goroutine, and the step
// returns no later than the budget after the call, whatever any of them does. If the deadline
// passes during preparation the review is unavailable. The goroutine is abandoned then; it
// writes nothing shared, only its buffered result channel.
//
// maskedPlan has already been through the secret scan (it is also what the criteria were drafted
// from, so it is masked once). rootIntent returns the raw root intent, or an error that is shown
// as the review's status error and must not quote the intent.
func reviewStep(deps Deps, maskedPlan string, rootIntent func() (string, error)) planreview.Result {
	budget := deps.reviewBudget()
	reviewer := deps.reviewer() // read here, not in the goroutine: deps is not shared with it
	deadline := time.Now().Add(budget)

	done := make(chan planreview.Result, 1)
	go func() {
		defer func() {
			if recover() != nil { // the value is not kept: it could quote the intent
				done <- planreview.Failed("the review step panicked")
			}
		}()
		raw, err := rootIntent()
		if err != nil {
			done <- planreview.Failed(err.Error())
			return
		}
		root, _, err := ratify.Mask(raw)
		if err != nil {
			done <- planreview.Failed("the secret scan of the root intent failed")
			return
		}
		remaining := time.Until(deadline) - reviewSlack
		if remaining <= 0 {
			done <- planreview.Result{Status: planreview.Unavail}
			return
		}
		done <- planreview.Run(context.Background(), reviewer, root.String(), maskedPlan, remaining)
	}()

	timer := time.NewTimer(budget)
	defer timer.Stop()
	select {
	case res := <-done:
		return res
	case <-timer.C:
		return planreview.Result{Status: planreview.Unavail}
	}
}

// hookPlanReview is the review for the hook's PreToolUse: the transcript is read, and the human
// turns taken from it, inside the review step's budget. Anything that goes wrong shows as
// status error. It also returns the line saying which scope the root intent used, or "" when the
// transcript was not read in time.
func hookPlanReview(in claudecode.Input, maskedPlan string, deps Deps) (planreview.Result, string) {
	scopeLines := make(chan string, 1) // the step's goroutine may outlive the budget: it only writes here
	res := reviewStep(deps, maskedPlan, func() (string, error) {
		data, err := os.ReadFile(in.TranscriptPath)
		if err != nil {
			return "", errors.New("cannot read the transcript")
		}
		scope, err := claudecode.RootIntentScope(data, in.ToolUseID)
		if err != nil {
			return "", errors.New("cannot read the human turns from the transcript")
		}
		scopeLines <- scope.Describe()
		if strings.TrimSpace(scope.Text) == "" {
			return "", errors.New("the transcript has no human turns")
		}
		return scope.Text, nil
	})
	select {
	case line := <-scopeLines:
		return res, line
	default:
		return res, ""
	}
}

// planReviewRecord is the status and counts of res, the only part of a review that is stored.
func planReviewRecord(res planreview.Result) *lock.PlanReview {
	o, a, w := res.Counts.Omissions, res.Counts.Additions, res.Counts.Weakenings
	return &lock.PlanReview{Status: string(res.Status), Omissions: &o, Additions: &a, Weakenings: &w}
}

// usablePlanReview reports whether a record read back from disk is well formed; one that is not
// is dropped rather than written into a lock.
func usablePlanReview(p *lock.PlanReview) bool {
	return p != nil && planreview.Status(p.Status).Valid() &&
		p.Omissions != nil && *p.Omissions >= 0 && p.Additions != nil && *p.Additions >= 0 && p.Weakenings != nil && *p.Weakenings >= 0
}
