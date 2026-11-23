package audit

import (
	"reflect"
	"testing"
	"time"
)

func revRuns(in []ArtifactRun) []ArtifactRun {
	out := make([]ArtifactRun, len(in))
	for i, r := range in {
		out[len(in)-1-i] = r
	}
	return out
}

func makeRun(repo string, pr int, at time.Time, head, vsha, outcome string, findings, suspect int, src string) ArtifactRun {
	return ArtifactRun{
		Event: Event{
			Schema: SchemaID, Event: EventVerify, Repo: repo, PR: pr,
			HeadSHA: head, VerifySHA256: vsha, Outcome: outcome,
		},
		At:             at,
		VerifyOutcome:  outcome,
		FootprintCount: findings,
		SuspectLocks:   suspect,
		Source:         src,
	}
}

// REQ-0040, REQ-0041, REQ-0042
func TestSummarizeArtifacts(t *testing.T) {
	start := time.Date(2026, 11, 20, 12, 0, 0, 0, time.UTC)
	end := time.Date(2026, 12, 20, 12, 0, 0, 0, time.UTC)
	t1 := time.Date(2026, 11, 25, 10, 0, 0, 0, time.UTC)
	t2 := time.Date(2026, 11, 26, 10, 0, 0, 0, time.UTC)

	t.Run("JSON key order matches the spec", func(t *testing.T) {
		want := []string{
			"runs", "prs", "prs_with_lock", "prs_with_findings",
			"findings_pr_rate", "findings_pr_ratio",
			"total_findings", "findings_per_pr", "median_findings_per_pr",
			"prs_with_suspect_lock",
		}
		if got := jsonKeys(reflect.TypeOf(ArtifactSummary{})); !reflect.DeepEqual(got, want) {
			t.Errorf("ArtifactSummary keys = %v, want %v", got, want)
		}
	})

	t.Run("window bounds, filtering, latest-run collapse, no-lock and suspect-lock semantics", func(t *testing.T) {
		runs := []ArtifactRun{
			// Excluded: before start, after end, PR 0, non-verify, bad schema, skipped bot
			makeRun("o/r", 90, start.Add(-time.Second), "h1", "v1", "findings", 9, 1, "old/audit.json"),
			makeRun("o/r", 91, end.Add(time.Second), "h1", "v1", "findings", 9, 1, "future/audit.json"),
			makeRun("o/r", 0, t1, "h1", "v1", "findings", 9, 1, "local/audit.json"),
			{Event: Event{Schema: SchemaID, Event: EventLock, Repo: "o/r", PR: 92}, At: t1, VerifyOutcome: "clean", Source: "lock/audit.json"},
			{Event: Event{Schema: "wrong/1", Event: EventVerify, Repo: "o/r", PR: 93}, At: t1, VerifyOutcome: "findings", FootprintCount: 5, Source: "bad/audit.json"},
			makeRun("o/r", 94, t1, "h1", "v1", "skipped", 0, 0, "bot/audit.json"),

			// Kept boundary PRs: exact start & exact end
			// o/r PR 1: two runs in window (earlier findings=5 at start -> later clean=0 at t1)
			makeRun("o/r", 1, start, "aaaa", "v1", "findings", 5, 0, "1a/audit.json"),
			makeRun("o/r", 1, t1, "bbbb", "v2", "clean", 0, 0, "1b/audit.json"),
			// o/r PR 2: tie on At, HeadSHA breaks tie ("ffff" > "1111")
			makeRun("o/r", 2, t2, "1111", "v1", "clean", 0, 0, "2a/audit.json"),
			makeRun("o/r", 2, t2, "ffff", "v2", "findings", 2, 0, "2b/audit.json"),
			// o/r PR 3: tie on At and HeadSHA, VerifySHA256 breaks tie ("zzzz" > "aaaa")
			makeRun("o/r", 3, t2, "same", "aaaa", "clean", 0, 0, "3a/audit.json"),
			makeRun("o/r", 3, t2, "same", "zzzz", "findings", 0, 1, "3b/audit.json"), // suspect-only finding
			// o/r PR 4: tie on At, HeadSHA, VerifySHA256; Source breaks tie ("4b" > "4a") at end bound
			makeRun("o/r", 4, end, "same", "same", "clean", 0, 0, "4a/audit.json"),
			makeRun("o/r", 4, end, "same", "same", "findings", 4, 1, "4b/audit.json"),
			// o/r PR 5: no-lock -> counts in PRs only
			makeRun("o/r", 5, t1, "h5", "v5", "no-lock", 0, 0, "5/audit.json"),
			// other/r PR 1: same PR number in different repo stays separate, unverifiable -> counts in PRsWithLock only
			makeRun("other/r", 1, t1, "h6", "v6", "unverifiable", 0, 0, "6/audit.json"),
		}
		// Locked PR footprint counts: PR1=0, PR2=2, PR3=0, PR4=4, other#1=0 -> sorted [0,0,0,2,4], median=0, total=6
		// Wait: 5 locked PRs (PR1 clean, PR2 findings=2, PR3 findings=0 suspect=1, PR4 findings=4 suspect=1, other#1 unverifiable)
		want := ArtifactSummary{
			Runs:                10,
			PRs:                 6,
			PRsWithLock:         5,
			PRsWithFindings:     3,
			FindingsPRRate:      "0.6000",
			FindingsPRRatio:     "3/5",
			TotalFindings:       6,
			FindingsPerPR:       "1.2000",
			MedianFindingsPerPR: intp(0),
			PRsWithSuspectLock:  2,
		}
		for label, in := range map[string][]ArtifactRun{"forward": runs, "reversed": revRuns(runs)} {
			got := SummarizeArtifacts(in, start, end)
			if !reflect.DeepEqual(got, want) {
				t.Errorf("%s: got %+v (median=%v), want %+v (median=%d)", label, got, got.MedianFindingsPerPR, want, *want.MedianFindingsPerPR)
			}
		}
	})

	t.Run("even count lower-middle median and suspect lock on unverifiable", func(t *testing.T) {
		// Counts: 0, 1, 2, 4 -> lower middle index (4-1)/2 = 1 -> median = 1
		runs := []ArtifactRun{
			makeRun("o/r", 1, t1, "h1", "v1", "clean", 0, 0, "1"),
			makeRun("o/r", 2, t1, "h2", "v2", "findings", 1, 0, "2"),
			makeRun("o/r", 3, t1, "h3", "v3", "findings", 2, 0, "3"),
			makeRun("o/r", 4, t1, "h4", "v4", "findings", 4, 0, "4"),
		}
		got := SummarizeArtifacts(runs, start, end)
		if got.MedianFindingsPerPR == nil || *got.MedianFindingsPerPR != 1 {
			t.Fatalf("median = %v, want 1", got.MedianFindingsPerPR)
		}
		if got.FindingsPerPR != "1.7500" || got.FindingsPRRate != "0.7500" || got.FindingsPRRatio != "3/4" {
			t.Errorf("summary = %+v", got)
		}
	})

	t.Run("odd non-zero median", func(t *testing.T) {
		runs := []ArtifactRun{
			makeRun("o/r", 1, t1, "h1", "v1", "findings", 5, 0, "1"),
			makeRun("o/r", 2, t1, "h2", "v2", "clean", 0, 0, "2"),
			makeRun("o/r", 3, t1, "h3", "v3", "findings", 2, 0, "3"),
		}
		got := SummarizeArtifacts(runs, start, end)
		if got.MedianFindingsPerPR == nil || *got.MedianFindingsPerPR != 2 {
			t.Fatalf("median = %v, want 2", got.MedianFindingsPerPR)
		}
	})

	t.Run("no locked PRs yields n/a and nil median even with no-lock PR", func(t *testing.T) {
		runs := []ArtifactRun{makeRun("o/r", 1, t1, "h1", "v1", "no-lock", 0, 0, "1")}
		got := SummarizeArtifacts(runs, start, end)
		want := ArtifactSummary{
			Runs: 1, PRs: 1, PRsWithLock: 0, PRsWithFindings: 0,
			FindingsPRRate: "n/a", FindingsPRRatio: "0/0",
			TotalFindings: 0, FindingsPerPR: "n/a", MedianFindingsPerPR: nil, PRsWithSuspectLock: 0,
		}
		if !reflect.DeepEqual(got, want) {
			t.Errorf("with no-lock only: got %+v, want %+v", got, want)
		}
		empty := SummarizeArtifacts(nil, start, end)
		wantEmpty := want
		wantEmpty.Runs, wantEmpty.PRs = 0, 0
		if !reflect.DeepEqual(empty, wantEmpty) {
			t.Errorf("empty: got %+v, want %+v", empty, wantEmpty)
		}
	})
}
