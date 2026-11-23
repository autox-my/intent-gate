package audit

import "time"

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

// SummarizeArtifacts collapses valid in-window CI verify runs to the latest run per PR and
// computes per-PR findings rates and averages.
func SummarizeArtifacts(runs []ArtifactRun, start, end time.Time) ArtifactSummary {
	return ArtifactSummary{}
}
