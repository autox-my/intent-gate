package main

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"intentgate/internal/planreview"
)

// Review 1 of S-11: the budget covers the whole review step (reading the transcript, extracting
// the human turns, masking, the provider), not just the provider call. The slow input is a
// transcript or prompt of keyword-rich prose, which makes the secret scan slow; it is generated
// in a temp dir and never committed.

// slowTurn is one human turn whose text makes the secret scan work hard (many keyword hits).
const slowTurn = "Set api_key = x1, password: y2, token=z3 and secret_key=q4; keep it small. "

// slowText is at least size bytes of slow prose.
func slowText(size int) string {
	var b strings.Builder
	line := strings.Repeat(slowTurn, 14)
	for b.Len() < size {
		b.WriteString(line)
		b.WriteByte('\n')
	}
	return b.String()
}

// slowTranscript writes a transcript of at least size bytes, one human turn per line.
func slowTranscript(t *testing.T, size int) string {
	t.Helper()
	line := `{"type":"user","message":{"role":"user","content":"` + strings.Repeat(slowTurn, 14) + `"}}` + "\n"
	var b strings.Builder
	for b.Len() < size {
		b.WriteString(line)
	}
	p := filepath.Join(t.TempDir(), "big-transcript.jsonl")
	if err := os.WriteFile(p, []byte(b.String()), 0o600); err != nil {
		t.Fatal(err)
	}
	return p
}

// REQ-0019, REQ-0020
func TestHook_PlanReviewBudgetCoversPreparation(t *testing.T) {
	const realBudget = 5 * time.Second // PlanReviewBudget unset: the production budget
	for _, c := range []struct {
		name       string
		prepare    func(t *testing.T, e *env)
		r          planreview.Reviewer
		wantStatus string
	}{
		{"50 MB transcript", func(t *testing.T, e *env) { e.tpath = slowTranscript(t, 50<<20) },
			fakeReviewer(func(context.Context, string, string) ([]planreview.Finding, error) { return markedFindings, nil }), "unavailable"},
		{"a provider that hangs", func(*testing.T, *env) {},
			fakeReviewer(func(context.Context, string, string) ([]planreview.Finding, error) {
				time.Sleep(30 * time.Second) // ignores its context
				return markedFindings, nil
			}), "unavailable"},
	} {
		t.Run(c.name, func(t *testing.T) {
			e := newEnv(t, "dev@example.com")
			c.prepare(t, e)
			e.deps.PlanReviewer = c.r // e.deps.PlanReviewBudget stays 0: the real 5 s

			start := time.Now()
			code, obj := e.run("pre", e.input("PreToolUse", planText, "ExitPlanMode"))
			took := time.Since(start)
			t.Logf("pre took %v", took)
			if took > realBudget+time.Second {
				t.Errorf("pre took %v, want within 5 s + 1 s", took)
			}
			msg := obj["systemMessage"]
			if code != 0 || !strings.Contains(msg, "Findings are published as SARIF") {
				t.Fatalf("pre: exit %d, message %q; want the criteria", code, msg)
			}
			if !strings.Contains(msg, "Plan review: "+c.wantStatus) {
				t.Errorf("want review status %s:\n%s", c.wantStatus, msg)
			}
			if len(e.pendingFiles()) != 1 {
				t.Errorf("pending records = %v, want one", e.pendingFiles())
			}
		})
	}
}

// REQ-0019, REQ-0020: the same holds for the CLI, where the prompt is read, masked and reviewed
// before the yes prompt.
func TestLock_PlanReviewBudgetCoversPreparation(t *testing.T) {
	e := newEnv(t, "dev@example.com")
	e.terminals(true, true)
	e.deps.PlanReviewBudget = testBudget
	e.deps.PlanReviewer = fakeReviewer(func(context.Context, string, string) ([]planreview.Finding, error) { return markedFindings, nil })
	plan := e.write("plan.md", planText)
	prompt := e.write("prompt.txt", slowText(8<<20))

	start := time.Now()
	// "no": the lock is not written, so what is timed is the review step and the prompt.
	code, stdout, _ := e.do([]string{"lock", "--from-plan", plan, "--prompt", prompt}, "no\n")
	took := time.Since(start)
	t.Logf("lock took %v", took)
	if took > testBudget+time.Second {
		t.Errorf("the prompt came after %v, want within the budget + 1 s", took)
	}
	if code != exitNotConfirmed || !strings.Contains(stdout, "Type yes") || !strings.Contains(stdout, "Plan review: unavailable") {
		t.Errorf("exit %d, stdout:\n%s", code, stdout)
	}
}
