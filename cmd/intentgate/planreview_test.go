package main

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/autox-my/intent-gate/internal/lock"
	"github.com/autox-my/intent-gate/internal/planreview"
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
	{Kind: planreview.Addition, Quote: "q-" + findingMarker + "-4", Text: "t-" + findingMarker, Confidence: 0.6},
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
		}), "complete", [3]int{2, 1, 1}},
		{"partial", fakeReviewer(func(ctx context.Context, _, _ string) ([]planreview.Finding, error) {
			<-ctx.Done()
			return markedFindings[:3], ctx.Err()
		}), "partial", [3]int{2, 1, 0}},
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
			wantSuffix := fmt.Sprintf(" (plan review: %s; %d omissions, %d additions, %d weakenings)", c.wantStatus, c.wantCounts[0], c.wantCounts[1], c.wantCounts[2])
			if !strings.HasSuffix(obj["systemMessage"], wantSuffix) {
				t.Errorf("post systemMessage = %q, want suffix %q", obj["systemMessage"], wantSuffix)
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
		code, obj := e.run("post", e.input("PostToolUse", planText, "ExitPlanMode"))
		if code != 0 || !strings.Contains(obj["systemMessage"], "intent lock written") {
			t.Fatalf("post: %d %v", code, obj)
		}
		l, _ := e.onlyLock()
		if l.PlanReview != nil {
			t.Errorf("plan_review = %+v, want none", l.PlanReview)
		}
		if want := "IntentGate: intent lock written: .intent/" + l.ID + ".yml"; obj["systemMessage"] != want {
			t.Errorf("post systemMessage = %q, want exactly %q", obj["systemMessage"], want)
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
	if pr == nil || pr.Status != "complete" || *pr.Omissions != 2 || *pr.Additions != 1 || *pr.Weakenings != 1 {
		t.Errorf("plan_review = %+v, want complete 2/1/1", pr)
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
	// start makes the environment and the command line for one way of locking.
	type start func(t *testing.T) (*env, []string)
	fromPlan := func(t *testing.T) (*env, []string) {
		e := newEnv(t, "dev@example.com")
		e.terminals(true, true)
		return e, []string{"lock", "--from-plan", e.write("plan.md", planText), "--prompt", e.write("prompt.txt", rootIntent)}
	}
	fromIssue := func(t *testing.T) (*env, []string) {
		e, _ := issueEnv(t)
		return e, []string{"lock", "--issue", "7"}
	}
	for _, how := range []struct {
		name string
		start
	}{{"--from-plan", fromPlan}, {"--issue", fromIssue}} {
		for name, c := range map[string]struct {
			r          planreview.Reviewer
			wantStatus string
		}{
			"hang": {fakeReviewer(func(context.Context, string, string) ([]planreview.Finding, error) {
				time.Sleep(3 * time.Second)
				return markedFindings, nil
			}), "unavailable"},
			"panic": {fakeReviewer(func(context.Context, string, string) ([]planreview.Finding, error) { panic("x") }), "error"},
			"nil":   {nil, "unavailable"},
		} {
			t.Run(how.name+"/"+name, func(t *testing.T) {
				e, args := how.start(t)
				e.deps.PlanReviewBudget = testBudget
				e.deps.PlanReviewer = c.r
				begin := time.Now()
				code, stdout, stderr := e.do(args, "yes\n")
				if code != 0 || time.Since(begin) > testBudget+time.Second {
					t.Fatalf("exit %d after %v, stderr %q, stdout %q", code, time.Since(begin), stderr, stdout)
				}
				l, _ := e.onlyLock()
				if l.PlanReview == nil || l.PlanReview.Status != c.wantStatus {
					t.Errorf("plan_review = %+v, want status %s", l.PlanReview, c.wantStatus)
				}
			})
		}
		t.Run(how.name+"/findings of each kind", func(t *testing.T) {
			e, args := how.start(t)
			e.deps.PlanReviewBudget = testBudget
			e.deps.PlanReviewer = fakeReviewer(func(context.Context, string, string) ([]planreview.Finding, error) { return markedFindings, nil })
			code, stdout, stderr := e.do(args, "yes\n")
			if code != 0 {
				t.Fatalf("exit %d, stderr %q, stdout %q", code, stderr, stdout)
			}
			l, path := e.onlyLock()
			pr := l.PlanReview
			if pr == nil || pr.Status != "complete" || *pr.Omissions != 2 || *pr.Additions != 1 || *pr.Weakenings != 1 {
				t.Errorf("plan_review = %+v, want complete 2/1/1", pr)
			}
			assertNoFindingText(t, e, path)
		})
	}
}

// `lock --issue` has no plan file: what the review weighs against the issue is the criterion
// texts, one per line, without footprints (review 1 of S-11).
func TestLock_IssuePlanForReviewIsCriterionTexts(t *testing.T) {
	e, _ := issueEnv(t)
	var gotPlan string
	e.deps.PlanReviewer = fakeReviewer(func(_ context.Context, _, plan string) ([]planreview.Finding, error) {
		gotPlan = plan
		return nil, nil
	})
	if code, out, errOut := e.do([]string{"lock", "--issue", "7"}, "yes\n"); code != 0 {
		t.Fatalf("exit %d: %s %s", code, out, errOut)
	}
	want := []string{"Findings are published as SARIF", "The report package has tests"}
	got := strings.Split(strings.TrimRight(gotPlan, "\n"), "\n")
	if len(got) != len(want) || got[0] != want[0] || got[1] != want[1] {
		t.Errorf("the plan given to the review is %q, want the criterion texts one per line: %q", gotPlan, want)
	}
	for _, leak := range []string{"footprint", "internal/report", "C1", "C2"} {
		if strings.Contains(gotPlan, leak) {
			t.Errorf("the plan given to the review holds %q: %q", leak, gotPlan)
		}
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
		if pr := after.PlanReview; pr == nil || pr.Status != "complete" || *pr.Omissions != 2 || *pr.Additions != 1 || *pr.Weakenings != 1 {
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
