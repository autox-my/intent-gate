package main

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"intentgate/internal/lock"
	"intentgate/internal/planreview"
)

// S-11: the plan review's wiring. The providers are fakes; no test makes a network or model call.

const findingMarker = "FINDING-TEXT-7731" // never in the root intent or the plan, so it can only come from a finding

type fakeReviewer func(ctx context.Context, rootIntent, plan string) ([]planreview.Finding, error)

func (f fakeReviewer) Review(ctx context.Context, rootIntent, plan string) ([]planreview.Finding, error) {
	return f(ctx, rootIntent, plan)
}

var markedFindings = []planreview.Finding{
	{Kind: planreview.Omission, Quote: "q-" + findingMarker + "-1", Text: "t-" + findingMarker, Confidence: 0.9},
	{Kind: planreview.Omission, Quote: "q-" + findingMarker + "-2", Text: "t-" + findingMarker, Confidence: 0.8},
	{Kind: planreview.Weakening, Quote: "q-" + findingMarker + "-3", Text: "t-" + findingMarker, Confidence: 0.7},
}

const testBudget = 500 * time.Millisecond

// REQ-0020
func TestHook_PlanReviewNeverBlocks(t *testing.T) {
	cases := []struct {
		name       string
		r          planreview.Reviewer
		wantStatus string
		wantCounts [3]int // omissions, additions, weakenings
	}{
		{"complete with findings", fakeReviewer(func(context.Context, string, string) ([]planreview.Finding, error) {
			return markedFindings, nil
		}), "complete", [3]int{2, 0, 1}},
		{"partial", fakeReviewer(func(ctx context.Context, _, _ string) ([]planreview.Finding, error) {
			<-ctx.Done()
			return markedFindings[:2], ctx.Err()
		}), "partial", [3]int{2, 0, 0}},
		{"hang past the budget", fakeReviewer(func(context.Context, string, string) ([]planreview.Finding, error) {
			time.Sleep(3 * time.Second) // ignores its context
			return markedFindings, nil
		}), "unavailable", [3]int{0, 0, 0}},
		{"error", fakeReviewer(func(context.Context, string, string) ([]planreview.Finding, error) {
			return nil, os.ErrInvalid
		}), "error", [3]int{0, 0, 0}},
		{"panic", fakeReviewer(func(context.Context, string, string) ([]planreview.Finding, error) {
			panic("provider bug")
		}), "error", [3]int{0, 0, 0}},
		{"unavailable", planreview.Unavailable{}, "unavailable", [3]int{0, 0, 0}},
		{"no provider configured", nil, "unavailable", [3]int{0, 0, 0}},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			e := newEnv(t, "dev@example.com")
			e.deps.PlanReviewer = c.r
			e.deps.PlanReviewBudget = testBudget

			start := time.Now()
			code, obj := e.run("pre", e.input("PreToolUse", planText, "ExitPlanMode"))
			if took := time.Since(start); took > testBudget+time.Second {
				t.Errorf("pre took %v, want within the budget + 1 s", took)
			}
			msg := obj["systemMessage"]
			if code != 0 || !strings.Contains(msg, "Findings are published as SARIF") || !strings.Contains(msg, "Plan review: ") {
				t.Fatalf("pre: exit %d, message %q; want the criteria and a plan review status", code, msg)
			}
			if strings.Index(msg, "Plan review: ") < strings.Index(msg, "Findings are published as SARIF") {
				t.Errorf("the review must come after the criteria:\n%s", msg)
			}
			if c.wantStatus == "complete" && (!strings.Contains(msg, findingMarker) || !strings.Contains(msg, "Plan review: complete")) {
				t.Errorf("the findings are not shown:\n%s", msg)
			}
			for _, f := range e.pendingFiles() {
				if data, _ := os.ReadFile(f); strings.Contains(string(data), findingMarker) {
					t.Errorf("finding text in the pending record: %s", data)
				}
			}

			code, obj = e.run("post", e.input("PostToolUse", planText, "ExitPlanMode"))
			if code != 0 || !strings.Contains(obj["systemMessage"], "intent lock written") {
				t.Fatalf("post: exit %d, %v", code, obj)
			}
			l, path := e.onlyLock()
			if ps := lock.Validate(l); len(ps) != 0 || !lock.VerifyHash(l) {
				t.Fatalf("lock invalid: %v", ps)
			}
			pr := l.PlanReview
			if pr == nil || pr.Status != c.wantStatus {
				t.Fatalf("plan_review = %+v, want status %q", pr, c.wantStatus)
			}
			if got := [3]int{*pr.Omissions, *pr.Additions, *pr.Weakenings}; got != c.wantCounts {
				t.Errorf("counts = %v, want %v", got, c.wantCounts)
			}
			assertNoFindingText(t, e, path)
		})
	}

	t.Run("a pending record from before this slice gives a lock without plan_review", func(t *testing.T) {
		e := newEnv(t, "dev@example.com")
		e.pre()
		for _, f := range e.pendingFiles() {
			data := `{"finished_at":"` + e.now.UTC().Format(time.RFC3339Nano) + `","plan_sha256":"00"}`
			if err := os.WriteFile(f, []byte(data), 0o600); err != nil {
				t.Fatal(err)
			}
		}
		if code, obj := e.run("post", e.input("PostToolUse", planText, "ExitPlanMode")); code != 0 || !strings.Contains(obj["systemMessage"], "intent lock written") {
			t.Fatalf("post: %d %v", code, obj)
		}
		if l, _ := e.onlyLock(); l.PlanReview != nil {
			t.Errorf("plan_review = %+v, want none", l.PlanReview)
		}
	})
	t.Run("a tampered pending record's plan review is dropped, not written into the lock", func(t *testing.T) {
		e := newEnv(t, "dev@example.com")
		e.pre()
		for _, f := range e.pendingFiles() {
			data := `{"finished_at":"` + e.now.UTC().Format(time.RFC3339Nano) + `","plan_sha256":"00","plan_review":{"status":"bogus","omissions":-4,"additions":0,"weakenings":0}}`
			if err := os.WriteFile(f, []byte(data), 0o600); err != nil {
				t.Fatal(err)
			}
		}
		if code, obj := e.run("post", e.input("PostToolUse", planText, "ExitPlanMode")); code != 0 || !strings.Contains(obj["systemMessage"], "intent lock written") {
			t.Fatalf("post: %d %v", code, obj)
		}
		if l, _ := e.onlyLock(); l.PlanReview != nil {
			t.Errorf("plan_review = %+v, want none", l.PlanReview)
		}
	})
	t.Run("an unreadable transcript shows as review status error and the criteria still show", func(t *testing.T) {
		e := newEnv(t, "dev@example.com")
		e.deps.PlanReviewer = fakeReviewer(func(context.Context, string, string) ([]planreview.Finding, error) { return markedFindings, nil })
		e.deps.PlanReviewBudget = testBudget
		if err := os.Remove(e.tpath); err != nil {
			t.Fatal(err)
		}
		code, obj := e.run("pre", e.input("PreToolUse", planText, "ExitPlanMode"))
		msg := obj["systemMessage"]
		if code != 0 || !strings.Contains(msg, "Findings are published as SARIF") || !strings.Contains(msg, "Plan review: error") {
			t.Errorf("exit %d, message %q", code, msg)
		}
	})
}

// assertNoFindingText fails if the marker is in the lock file, anything under the state directory
// (pending and root-intent records) or the audit log.
func assertNoFindingText(t *testing.T, e *env, lockPath string) {
	t.Helper()
	paths := []string{lockPath, filepath.Join(e.state, "audit.jsonl")}
	_ = filepath.WalkDir(e.state, func(p string, d os.DirEntry, err error) error {
		if err == nil && !d.IsDir() {
			paths = append(paths, p)
		}
		return nil
	})
	for _, p := range paths {
		if data, err := os.ReadFile(p); err == nil && strings.Contains(string(data), findingMarker) {
			t.Errorf("finding text in %s", p)
		}
	}
}

func TestLock_PlanReviewShownBeforePrompt(t *testing.T) {
	e := newEnv(t, "dev@example.com")
	e.terminals(true, true)
	e.deps.PlanReviewBudget = testBudget
	e.deps.PlanReviewer = fakeReviewer(func(context.Context, string, string) ([]planreview.Finding, error) {
		return markedFindings, nil
	})
	plan := e.write("plan.md", planText)
	prompt := e.write("prompt.txt", rootIntent)
	code, stdout, stderr := e.do([]string{"lock", "--from-plan", plan, "--prompt", prompt}, "yes\n")
	if code != 0 {
		t.Fatalf("exit %d, stderr %q, stdout %q", code, stderr, stdout)
	}
	at, prompted := strings.Index(stdout, findingMarker), strings.Index(stdout, "Type yes")
	if at < 0 || prompted < 0 || at > prompted {
		t.Errorf("the findings must print before the prompt:\n%s", stdout)
	}
	if !strings.Contains(stdout, "Plan review: complete") {
		t.Errorf("no status line:\n%s", stdout)
	}
	l, path := e.onlyLock()
	if ps := lock.Validate(l); len(ps) != 0 || !lock.VerifyHash(l) {
		t.Fatalf("lock invalid: %v", ps)
	}
	pr := l.PlanReview
	if pr == nil || pr.Status != "complete" || *pr.Omissions != 2 || *pr.Additions != 0 || *pr.Weakenings != 1 {
		t.Errorf("plan_review = %+v, want complete 2/0/1", pr)
	}
	if ms := l.Ratification.ReviewDurationMS; ms == nil || *ms != int(confirmDelay.Milliseconds()) {
		t.Errorf("review_duration_ms = %v, want the time from the prompt to the answer (%d)", ms, confirmDelay.Milliseconds())
	}
	assertNoFindingText(t, e, path)
	if data, _ := os.ReadFile(filepath.Join(e.state, "audit.jsonl")); strings.Contains(string(data), "plan_review") {
		t.Errorf("the audit log carries the plan review: %s", data)
	}
}

func TestLock_PlanReviewDoesNotBlock(t *testing.T) {
	for name, r := range map[string]planreview.Reviewer{
		"hang": fakeReviewer(func(context.Context, string, string) ([]planreview.Finding, error) {
			time.Sleep(3 * time.Second)
			return markedFindings, nil
		}),
		"panic": fakeReviewer(func(context.Context, string, string) ([]planreview.Finding, error) { panic("x") }),
		"nil":   nil,
	} {
		t.Run(name, func(t *testing.T) {
			e := newEnv(t, "dev@example.com")
			e.terminals(true, true)
			e.deps.PlanReviewBudget = testBudget
			e.deps.PlanReviewer = r
			plan := e.write("plan.md", planText)
			prompt := e.write("prompt.txt", rootIntent)
			start := time.Now()
			code, stdout, stderr := e.do([]string{"lock", "--from-plan", plan, "--prompt", prompt}, "yes\n")
			if code != 0 || time.Since(start) > testBudget+time.Second {
				t.Fatalf("exit %d after %v, stderr %q, stdout %q", code, time.Since(start), stderr, stdout)
			}
			l, _ := e.onlyLock()
			want := "unavailable"
			if name == "panic" {
				want = "error"
			}
			if l.PlanReview == nil || l.PlanReview.Status != want {
				t.Errorf("plan_review = %+v, want status %s", l.PlanReview, want)
			}
		})
	}
}

// Both texts are masked before the provider sees them (ratification.md §5).
func TestPlanReview_ProviderGetsMaskedText(t *testing.T) {
	var mu sync.Mutex
	var gotRoot, gotPlan string
	spy := fakeReviewer(func(_ context.Context, root, plan string) ([]planreview.Finding, error) {
		mu.Lock()
		defer mu.Unlock()
		gotRoot, gotPlan = root, plan
		return nil, nil
	})
	check := func(t *testing.T) {
		t.Helper()
		mu.Lock()
		defer mu.Unlock()
		if gotRoot == "" || gotPlan == "" {
			t.Fatalf("the provider got root %q, plan %q", gotRoot, gotPlan)
		}
		if strings.Contains(gotRoot, planSecret) || strings.Contains(gotPlan, planSecret) {
			t.Errorf("the secret reached the provider:\nroot %q\nplan %q", gotRoot, gotPlan)
		}
	}
	t.Run("lock --from-plan", func(t *testing.T) {
		e := newEnv(t, "dev@example.com")
		e.terminals(true, true)
		e.deps.PlanReviewer = spy
		plan := e.write("plan.md", secretPlan)
		prompt := e.write("prompt.txt", "Rotate the deploy key "+planSecret+" now.")
		if code, out, errOut := e.do([]string{"lock", "--from-plan", plan, "--prompt", prompt}, "yes\n"); code != 0 {
			t.Fatalf("exit %d: %s %s", code, out, errOut)
		}
		check(t)
	})
	t.Run("hook pre", func(t *testing.T) {
		e := newEnv(t, "dev@example.com")
		e.deps.PlanReviewer = spy
		tr := `{"type":"user","message":{"role":"user","content":"Rotate the deploy key ` + planSecret + ` now."}}` + "\n"
		if err := os.WriteFile(e.tpath, []byte(tr), 0o600); err != nil {
			t.Fatal(err)
		}
		if code, obj := e.run("pre", e.input("PreToolUse", secretPlan, "ExitPlanMode")); code != 0 {
			t.Fatalf("pre: %d %v", code, obj)
		}
		check(t)
	})
}

// amend runs no review and keeps the lock's plan_review as it was.
func TestAmend_KeepsPlanReview(t *testing.T) {
	e := newEnv(t, "dev@example.com")
	e.terminals(true, true)
	e.deps.PlanReviewBudget = testBudget
	calls := 0
	e.deps.PlanReviewer = fakeReviewer(func(context.Context, string, string) ([]planreview.Finding, error) {
		calls++
		return markedFindings, nil
	})
	plan := e.write("plan.md", planText)
	prompt := e.write("prompt.txt", rootIntent)
	if code, out, errOut := e.do([]string{"lock", "--from-plan", plan, "--prompt", prompt}, "yes\n"); code != 0 {
		t.Fatalf("lock: %d %s %s", code, out, errOut)
	}
	before, _ := e.onlyLock()
	callsAfterLock := calls
	for _, args := range [][]string{
		{"amend", before.ID, "--criterion", "C1", "--add", "docs/**"},
		{"amend", before.ID, "--reratify"},
	} {
		if code, out, errOut := e.do(args, "yes\n"); code != 0 {
			t.Fatalf("%v: %d %s %s", args, code, out, errOut)
		}
		after, _ := e.onlyLock()
		if after.PlanReview == nil || *after.PlanReview.Omissions != 2 || after.PlanReview.Status != "complete" {
			t.Errorf("%v: plan_review = %+v, want it unchanged", args, after.PlanReview)
		}
		if !lock.VerifyHash(after) {
			t.Errorf("%v: hash does not verify", args)
		}
	}
	if calls != callsAfterLock {
		t.Errorf("amend ran the review (%d calls after the lock's %d)", calls, callsAfterLock)
	}
}
