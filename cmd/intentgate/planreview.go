package main

import (
	"context"
	"os"
	"strings"
	"time"

	"intentgate/internal/hooks/claudecode"
	"intentgate/internal/lock"
	"intentgate/internal/planreview"
	"intentgate/internal/ratify"
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

// runPlanReview masks both texts, then runs the review within the budget. A scan failure is a
// review status of error: it never stops the caller.
func runPlanReview(deps Deps, rawRootIntent, rawPlan string) planreview.Result {
	root, _, err := ratify.Mask(rawRootIntent)
	if err != nil {
		return planreview.Failed("the secret scan of the root intent failed")
	}
	plan, _, err := ratify.Mask(rawPlan)
	if err != nil {
		return planreview.Failed("the secret scan of the plan failed")
	}
	return planreview.Run(context.Background(), deps.reviewer(), root.String(), plan.String(), deps.reviewBudget())
}

// hookPlanReview is the review for the hook's PreToolUse: it reads the transcript for the root
// intent. Anything that goes wrong shows as status error.
func hookPlanReview(in claudecode.Input, plan string, deps Deps) planreview.Result {
	data, err := os.ReadFile(in.TranscriptPath)
	if err != nil {
		return planreview.Failed("cannot read the transcript")
	}
	rootIntent, _, err := claudecode.RootIntent(data)
	if err != nil {
		return planreview.Failed("cannot read the human turns from the transcript")
	}
	if strings.TrimSpace(rootIntent) == "" {
		return planreview.Failed("the transcript has no human turns")
	}
	return runPlanReview(deps, rootIntent, plan)
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
