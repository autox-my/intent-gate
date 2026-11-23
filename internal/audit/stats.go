package audit

import (
	"fmt"
	"sort"
	"time"
)

// ArtifactRun is one CI `verify` run loaded from an `audit.json` and sibling `verify.json` pair.
type ArtifactRun struct {
	Event          Event
	At             time.Time
	VerifyOutcome  string
	FootprintCount int
	SuspectLocks   int
	Source         string
}

// ArtifactSummary holds per-PR findings metrics across CI artifacts in the stats window.
// The field order is the JSON key order.
type ArtifactSummary struct {
	Runs                int    `json:"runs"`
	PRs                 int    `json:"prs"`
	PRsWithLock         int    `json:"prs_with_lock"`
	PRsWithFindings     int    `json:"prs_with_findings"`
	FindingsPRRate      string `json:"findings_pr_rate"`
	FindingsPRRatio     string `json:"findings_pr_ratio"`
	TotalFindings       int    `json:"total_findings"`
	FindingsPerPR       string `json:"findings_per_pr"`
	MedianFindingsPerPR *int   `json:"median_findings_per_pr"`
	PRsWithSuspectLock  int    `json:"prs_with_suspect_lock"`
}

type prKey struct {
	repo string
	pr   int
}

// validArtifactRun reports whether r is a non-skipped CI PR verify event inside [start, end].
func validArtifactRun(r ArtifactRun, start, end time.Time) bool {
	if r.Event.Schema != SchemaID || r.Event.Event != EventVerify || r.Event.PR <= 0 {
		return false
	}
	if r.At.Before(start) || r.At.After(end) {
		return false
	}
	return r.VerifyOutcome != "skipped"
}

// newerArtifactRun orders candidate runs deterministically so collapsing to the latest run per PR
// does not depend on walk order or slice order.
func newerArtifactRun(a, b ArtifactRun) bool {
	if !a.At.Equal(b.At) {
		return a.At.After(b.At)
	}
	if a.Event.HeadSHA != b.Event.HeadSHA {
		return a.Event.HeadSHA > b.Event.HeadSHA
	}
	if a.Event.VerifySHA256 != b.Event.VerifySHA256 {
		return a.Event.VerifySHA256 > b.Event.VerifySHA256
	}
	return a.Source > b.Source
}

// SummarizeArtifacts collapses valid in-window CI verify runs to the latest run per PR and
// computes per-PR findings rates and averages.
func SummarizeArtifacts(runs []ArtifactRun, start, end time.Time) ArtifactSummary {
	latest := map[prKey]ArtifactRun{}
	keptRuns := 0
	for _, r := range runs {
		if !validArtifactRun(r, start, end) {
			continue
		}
		keptRuns++
		k := prKey{repo: r.Event.Repo, pr: r.Event.PR}
		if prev, seen := latest[k]; !seen || newerArtifactRun(r, prev) {
			latest[k] = r
		}
	}

	s := ArtifactSummary{
		Runs:           keptRuns,
		PRs:            len(latest),
		FindingsPRRate: "n/a",
		FindingsPerPR:  "n/a",
	}
	counts := make([]int, 0, len(latest))
	for _, r := range latest {
		if r.VerifyOutcome == "no-lock" {
			continue
		}
		s.PRsWithLock++
		if r.VerifyOutcome == "findings" {
			s.PRsWithFindings++
		}
		if r.SuspectLocks > 0 {
			s.PRsWithSuspectLock++
		}
		s.TotalFindings += r.FootprintCount
		counts = append(counts, r.FootprintCount)
	}
	s.FindingsPRRatio = fmt.Sprintf("%d/%d", s.PRsWithFindings, s.PRsWithLock)
	if s.PRsWithLock == 0 {
		return s
	}
	s.FindingsPRRate = fmt.Sprintf("%.4f", float64(s.PRsWithFindings)/float64(s.PRsWithLock))
	s.FindingsPerPR = fmt.Sprintf("%.4f", float64(s.TotalFindings)/float64(s.PRsWithLock))
	sort.Ints(counts)
	med := counts[(len(counts)-1)/2]
	s.MedianFindingsPerPR = &med
	return s
}
