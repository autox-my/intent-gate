package report_test

import (
	"bytes"
	"math/rand"
	"testing"

	"intentgate/internal/footprint"
	"intentgate/internal/policy"
	"intentgate/internal/report"
	"intentgate/internal/verify"
)

// equalKeyResult has entries that agree on their primary sort key (the path) and differ only in
// later fields, so a sort that stops at the path leaves their order to the input order.
func equalKeyResult() verify.Result {
	crit := func(id string) []verify.Criterion {
		return []verify.Criterion{{ID: id, Text: "text " + id, Footprint: []string{"a/**"}}}
	}
	return verify.Result{
		Outcome: verify.Unverifiable, ExitCode: 3,
		Problems: []string{"b problem", "a problem"},
		Locks: []verify.LockResult{
			{Path: ".intent/x.yml", ID: lockID, Revision: 2, Suspect: []string{"retargeted"}, Problems: []string{}, Criteria: crit("C1")},
			{Path: ".intent/x.yml", ID: lockID, Revision: 1, Suspect: []string{"expired"}, Problems: []string{}, Criteria: crit("C1")},
			{Path: ".intent/x.yml", ID: lockID, Revision: 1, Suspect: []string{"expired"}, Problems: []string{}, Criteria: crit("C2")},
			{Path: ".intent/x.yml", ID: "other", Revision: 1, Suspect: []string{}, Problems: []string{"p2"}, Criteria: []verify.Criterion{}},
			{Path: ".intent/x.yml", ID: "other", Revision: 1, Suspect: []string{}, Problems: []string{"p1"}, Criteria: []verify.Criterion{}},
			{Path: ".intent/a.yml", ID: lockID, Revision: 1, Suspect: []string{}, Problems: []string{}, Criteria: crit("C1")},
		},
		Findings: []footprint.Finding{
			{Path: "p.go", Status: footprint.Modified, NearestCriterion: "C1", LockID: lockID, Fix: "fix 2"},
			{Path: "p.go", Status: footprint.Modified, NearestCriterion: "C1", LockID: lockID, Fix: "fix 1"},
			{Path: "p.go", Status: footprint.Modified, NearestCriterion: "C1", LockID: "other", Fix: "fix 1"},
			{Path: "p.go", Status: footprint.Added, NearestCriterion: "C2", LockID: lockID, Fix: "fix 1"},
			{Path: "p.go", Status: footprint.Added, NearestCriterion: "C1", LockID: lockID, Fix: "fix 1"},
			{Path: "a.go", Status: footprint.Added, NearestCriterion: "C1", LockID: lockID, Fix: "fix 1"},
		},
	}
}

func renderAll(r verify.Result) [][]byte {
	d := policy.Decide(r)
	return [][]byte{
		report.JSON(r), report.SARIF(r, "dev"), []byte(report.Text(r)),
		[]byte(report.CheckSummary(r, d)), []byte(report.Comment(r, d)),
	}
}

// Review 1 of S-07: every sort over output has a total order, so the order the lists arrive in
// never reaches the bytes.
func TestOutput_TotalOrderUnderPermutation(t *testing.T) {
	r := equalKeyResult()
	want := renderAll(r)
	rng := rand.New(rand.NewSource(20261118))
	for i := 0; i < 40; i++ {
		p := r
		p.Locks = append([]verify.LockResult(nil), r.Locks...)
		p.Findings = append([]footprint.Finding(nil), r.Findings...)
		p.Problems = append([]string(nil), r.Problems...)
		rng.Shuffle(len(p.Locks), func(a, b int) { p.Locks[a], p.Locks[b] = p.Locks[b], p.Locks[a] })
		rng.Shuffle(len(p.Findings), func(a, b int) { p.Findings[a], p.Findings[b] = p.Findings[b], p.Findings[a] })
		rng.Shuffle(len(p.Problems), func(a, b int) { p.Problems[a], p.Problems[b] = p.Problems[b], p.Problems[a] })
		for k, got := range renderAll(p) {
			if !bytes.Equal(got, want[k]) {
				t.Fatalf("permutation %d: output %d differs.\n got:\n%s\nwant:\n%s", i, k, got, want[k])
			}
		}
	}
}
