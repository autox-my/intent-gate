package planreview

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"testing"
	"time"
	"unicode/utf8"
)

// fake is a Reviewer made of a function.
type fake func(ctx context.Context, rootIntent, plan string) ([]Finding, error)

func (f fake) Review(ctx context.Context, rootIntent, plan string) ([]Finding, error) {
	return f(ctx, rootIntent, plan)
}

var oneOfEach = []Finding{
	{Kind: Omission, Quote: "add SARIF output", Text: "the plan never mentions SARIF", Confidence: 0.9},
	{Kind: Addition, Quote: "rewrite the CLI", Text: "nobody asked for a rewrite", Confidence: 0.8},
	{Kind: Weakening, Quote: "keep it small", Text: "the plan drops the size limit", Confidence: 0.7},
}

// REQ-0016, REQ-0017, REQ-0018
func TestRun_ReportsEachKind(t *testing.T) {
	r := fake(func(context.Context, string, string) ([]Finding, error) { return oneOfEach, nil })
	res := Run(context.Background(), r, "root", "plan", time.Second)
	if res.Status != Complete {
		t.Fatalf("status = %q, want complete", res.Status)
	}
	if len(res.Findings) != 3 {
		t.Fatalf("findings = %v, want all three", res.Findings)
	}
	if want := (Counts{Omissions: 1, Additions: 1, Weakenings: 1}); res.Counts != want {
		t.Errorf("counts = %+v, want %+v", res.Counts, want)
	}
	kinds := map[Kind]bool{}
	for _, f := range res.Findings {
		kinds[f.Kind] = true
	}
	if !kinds[Omission] || !kinds[Addition] || !kinds[Weakening] {
		t.Errorf("kinds reported: %v", kinds)
	}
}

// REQ-0016, REQ-0017, REQ-0018
func TestDisplay_EachKind(t *testing.T) {
	r := fake(func(context.Context, string, string) ([]Finding, error) { return oneOfEach, nil })
	out := Display(Run(context.Background(), r, "root", "plan", time.Second))
	for _, want := range []string{
		"Plan review: complete",
		"Omissions", "add SARIF output", "the plan never mentions SARIF",
		"Additions", "rewrite the CLI",
		"Weakenings", "keep it small",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("display lacks %q:\n%s", want, out)
		}
	}
}

// REQ-0019
func TestRun_Budget(t *testing.T) {
	hang := fake(func(context.Context, string, string) ([]Finding, error) {
		time.Sleep(30 * time.Second) // ignores its context
		return oneOfEach, nil
	})
	for _, budget := range []time.Duration{500 * time.Millisecond, 5 * time.Second} {
		t.Run(budget.String(), func(t *testing.T) {
			start := time.Now()
			res := Run(context.Background(), hang, "root", "plan", budget)
			if took := time.Since(start); took > budget+100*time.Millisecond {
				t.Errorf("Run took %v, want at most %v", took, budget+100*time.Millisecond)
			}
			if res.Status != Unavail || len(res.Findings) != 0 {
				t.Errorf("result = %+v, want unavailable with no findings", res)
			}
		})
	}
	t.Run("findings, then the deadline", func(t *testing.T) {
		two := fake(func(ctx context.Context, _, _ string) ([]Finding, error) {
			<-ctx.Done()
			return oneOfEach[:2], ctx.Err()
		})
		start := time.Now()
		res := Run(context.Background(), two, "root", "plan", 500*time.Millisecond)
		if took := time.Since(start); took > 600*time.Millisecond {
			t.Errorf("Run took %v", took)
		}
		if res.Status != Partial || len(res.Findings) != 2 {
			t.Errorf("result = %+v, want partial with both findings", res)
		}
		if res.Counts.Omissions != 1 || res.Counts.Additions != 1 || res.Counts.Weakenings != 0 {
			t.Errorf("counts = %+v", res.Counts)
		}
	})
}

func TestRun_Statuses(t *testing.T) {
	cases := []struct {
		name         string
		r            Reviewer
		want         Status
		wantFindings int
	}{
		{"complete", fake(func(context.Context, string, string) ([]Finding, error) { return oneOfEach, nil }), Complete, 3},
		{"complete with nothing to say", fake(func(context.Context, string, string) ([]Finding, error) { return nil, nil }), Complete, 0},
		{"error keeps findings", fake(func(context.Context, string, string) ([]Finding, error) {
			return oneOfEach[:1], errors.New("boom")
		}), ErrorStatus, 1},
		{"panic", fake(func(context.Context, string, string) ([]Finding, error) { panic("provider bug") }), ErrorStatus, 0},
		{"unavailable provider", Unavailable{}, Unavail, 0},
		{"nil provider", nil, Unavail, 0},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			res := Run(context.Background(), c.r, "root", "plan", time.Second)
			if res.Status != c.want || len(res.Findings) != c.wantFindings {
				t.Errorf("status %q with %d findings, want %q with %d", res.Status, len(res.Findings), c.want, c.wantFindings)
			}
		})
	}
	t.Run("a panic's value is not kept", func(t *testing.T) {
		r := fake(func(context.Context, string, string) ([]Finding, error) { panic("QUOTED-INTENT") })
		if res := Run(context.Background(), r, "root", "plan", time.Second); strings.Contains(res.Err, "QUOTED-INTENT") || res.Err == "" {
			t.Errorf("Err = %q", res.Err)
		}
	})
}

func TestDisplay_Caps(t *testing.T) {
	var fs []Finding
	for i := 0; i < 8; i++ {
		fs = append(fs, Finding{Kind: Omission, Quote: fmt.Sprintf("quote-%d", i), Text: "t", Confidence: float64(i) / 10})
	}
	r := fake(func(context.Context, string, string) ([]Finding, error) { return fs, nil })
	res := Run(context.Background(), r, "root", "plan", time.Second)
	if res.Counts.Omissions != 8 {
		t.Fatalf("counts = %+v, want 8 omissions (the count is not capped)", res.Counts)
	}
	out := Display(res)
	for i := 7; i >= 3; i-- { // the five most confident, highest first
		if !strings.Contains(out, fmt.Sprintf("quote-%d", i)) {
			t.Errorf("display lacks quote-%d:\n%s", i, out)
		}
	}
	for i := 0; i < 3; i++ {
		if strings.Contains(out, fmt.Sprintf("quote-%d", i)) {
			t.Errorf("display shows quote-%d, below the cap:\n%s", i, out)
		}
	}
	if !strings.Contains(out, "+3 more") {
		t.Errorf("display lacks \"+3 more\":\n%s", out)
	}
	if strings.Index(out, "quote-7") > strings.Index(out, "quote-6") || strings.Index(out, "quote-4") > strings.Index(out, "quote-3") {
		t.Errorf("not in confidence order:\n%s", out)
	}
}

func TestDisplay_StatusLines(t *testing.T) {
	cases := map[Status]string{
		Partial:     "Plan review: partial: the 5 s budget ran out",
		Unavail:     "Plan review: unavailable",
		ErrorStatus: "Plan review: error: it broke",
	}
	for st, want := range cases {
		if out := Display(Result{Status: st, Err: "it broke"}); !strings.Contains(out, want) {
			t.Errorf("Display(%s) = %q, want %q", st, out, want)
		}
	}
}

func TestDisplay_TiesAreOrderedByQuoteThenText(t *testing.T) {
	res := Result{Status: Complete, Counts: Counts{Omissions: 3}, Findings: []Finding{
		{Kind: Omission, Quote: "qb", Text: "text-z", Confidence: 0.5},
		{Kind: Omission, Quote: "qa", Text: "text-y", Confidence: 0.5},
		{Kind: Omission, Quote: "qa", Text: "text-x", Confidence: 0.5},
	}}
	out := Display(res)
	ix, iy, ib := strings.Index(out, "text-x"), strings.Index(out, "text-y"), strings.Index(out, "qb")
	if !(ix >= 0 && ix < iy && iy < ib) {
		t.Errorf("tie order wrong:\n%s", out)
	}
	if Display(res) != out {
		t.Error("Display is not pure")
	}
}

func TestSelect(t *testing.T) {
	for _, v := range []string{"", "off", "jev"} {
		r := Select(func(string) string { return v })
		if _, err := r.Review(context.Background(), "a", "b"); !errors.Is(err, ErrUnavailable) {
			t.Errorf("INTENTGATE_PLAN_REVIEW=%q: Review err = %v, want ErrUnavailable in this slice", v, err)
		}
	}
}

// A provider's error text is untrusted and may be long or span lines: the status line shows at
// most 200 bytes of it, on one line, cut on a UTF-8 boundary (review 1 of S-11).
func TestDisplay_ErrorLineIsBounded(t *testing.T) {
	const prefix = "Plan review: error: "
	for name, errText := range map[string]string{
		"long ascii":   strings.Repeat("x", 5000),
		"long unicode": strings.Repeat("é", 400),       // 2 bytes each: the cut at 200 bytes is on a boundary
		"cut mid-rune": "a" + strings.Repeat("€", 200), // 3 bytes each: byte 200 falls inside a rune
		"many lines":   "first line\nsecond line\r\nthird line",
	} {
		t.Run(name, func(t *testing.T) {
			out := Display(Result{Status: ErrorStatus, Err: errText})
			line, rest, _ := strings.Cut(out, "\n")
			if rest != "" {
				t.Errorf("the status line is followed by more text:\n%q", out)
			}
			if !strings.HasPrefix(line, prefix) {
				t.Fatalf("status line %q lacks the prefix", line)
			}
			shown := strings.TrimPrefix(line, prefix)
			if len(shown) > 200 {
				t.Errorf("the error text is %d bytes, want at most 200", len(shown))
			}
			if !utf8.ValidString(shown) {
				t.Errorf("the error text is cut inside a rune: %q", shown)
			}
			if strings.ContainsAny(shown, "\r\n") {
				t.Errorf("the error text spans lines: %q", shown)
			}
			if len(shown) == 0 {
				t.Errorf("the error text is gone")
			}
		})
	}
	if out := Display(Result{Status: ErrorStatus, Err: "provider said no"}); out != "Plan review: error: provider said no\n" {
		t.Errorf("a short error is changed: %q", out)
	}
}
