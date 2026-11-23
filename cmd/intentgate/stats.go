package main

import (
	"bytes"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/autox-my/intent-gate/internal/audit"
	"github.com/autox-my/intent-gate/internal/gitx"
	"github.com/autox-my/intent-gate/internal/lock"
)

// `intentgate stats` (audit-metrics.md §2, REQ-0041): the footprint-amendment rate and the other
// Phase 0 signals that git history alone can answer. With `--artifacts <dir>`, it also aggregates
// downloaded CI artifacts offline. The window ends at HEAD's committer time, so the same HEAD gives
// the same bytes; the command reads no clock.
//
// Exit codes: 0; 2 usage; 3 git or artifacts cannot be read.

const (
	statsUsage       = "usage: intentgate stats [--since <N>d] [--format text|json] [--artifacts <dir>]\n"
	statsSchema      = "intentgate.stats/1"
	notAvailable     = "n/a"
	maxArtifactBytes = 10 << 20
)

// statsDoc is the intentgate.stats/1 document; the field order is the key order.
type statsDoc struct {
	Schema                 string                 `json:"schema"`
	SinceDays              int                    `json:"since_days"`
	WindowStart            string                 `json:"window_start"`
	WindowEnd              string                 `json:"window_end"`
	Head                   string                 `json:"head"`
	Locks                  int                    `json:"locks"`
	Invalid                int                    `json:"invalid"`
	Amended                int                    `json:"amended"`
	AmendmentRate          string                 `json:"amendment_rate"`
	AmendmentRatio         string                 `json:"amendment_ratio"`
	MergedPRs              int                    `json:"merged_prs"`
	MergedPRsWithLock      int                    `json:"merged_prs_with_lock"`
	LockCoverage           string                 `json:"lock_coverage"`
	LockCoverageRatio      string                 `json:"lock_coverage_ratio"`
	Methods                []methodCount          `json:"methods"`
	MedianReviewDurationMS *int                   `json:"median_review_duration_ms"`
	Artifacts              *audit.ArtifactSummary `json:"artifacts,omitempty"`
}

type methodCount struct {
	Method string `json:"method"`
	Count  int    `json:"count"`
}

func runStats(args []string, stdout io.Writer, deps Deps) int {
	t := newTerm("stats", nil, stdout, deps)
	fs := newFlags("stats")
	since := fs.String("since", "30d", "")
	format := fs.String("format", "text", "")
	artifacts := fs.String("artifacts", "", "")
	if err := fs.Parse(args); err != nil || fs.NArg() > 0 {
		return t.usage(statsUsage, "")
	}
	set := map[string]bool{}
	fs.Visit(func(f *flag.Flag) { set[f.Name] = true })
	if set["artifacts"] && *artifacts == "" {
		return t.usage(statsUsage, "--artifacts must name a directory")
	}
	days, ok := parseDays(*since)
	if !ok {
		return t.usage(statsUsage, fmt.Sprintf("--since must be a number of days such as 30d, got %q", *since))
	}
	if *format != "text" && *format != "json" {
		return t.usage(statsUsage, fmt.Sprintf("unknown --format %q", *format))
	}

	doc, err := computeStats(deps.Dir, days, *artifacts)
	if err != nil {
		return t.fail(exitUnverifiable, "intentgate stats: %v", err)
	}
	if *format == "json" {
		var buf bytes.Buffer
		enc := json.NewEncoder(&buf)
		enc.SetEscapeHTML(false)
		enc.SetIndent("", "  ")
		if err := enc.Encode(doc); err != nil {
			return t.fail(exitUnverifiable, "intentgate stats: %v", err)
		}
		_, _ = stdout.Write(buf.Bytes())
		return exitOK
	}
	_, _ = io.WriteString(stdout, statsText(doc))
	return exitOK
}

// parseDays reads "<N>d" with N a positive integer.
func parseDays(s string) (int, bool) {
	num, ok := strings.CutSuffix(s, "d")
	if !ok || num == "" || strings.Trim(num, "0123456789") != "" {
		return 0, false
	}
	n, err := strconv.Atoi(num)
	if err != nil || n < 1 || n > 36500 {
		return 0, false
	}
	return n, true
}

func computeStats(dir string, days int, artifactsDir string) (statsDoc, error) {
	headTime, err := gitx.CommitTime(dir, "HEAD")
	if err != nil {
		return statsDoc{}, err
	}
	head, err := gitx.Resolve(dir, "HEAD")
	if err != nil {
		return statsDoc{}, err
	}
	start := headTime.AddDate(0, 0, -days)
	adds, err := gitx.LockAdditions(dir, "HEAD")
	if err != nil {
		return statsDoc{}, err
	}

	merges, err := gitx.FirstParentMerges(dir, "HEAD")
	if err != nil {
		return statsDoc{}, err
	}

	doc := statsDoc{
		Schema: statsSchema, SinceDays: days, WindowStart: utcSeconds(start), WindowEnd: utcSeconds(headTime), Head: head,
		Methods: []methodCount{},
	}
	methods := map[string]int{}
	lockCommits := map[string]bool{} // the adding commits of the locks counted
	var durations []int
	for _, a := range adds {
		if a.Time.Before(start) || a.Time.After(headTime) {
			continue
		}
		data, found, err := gitx.ShowFile(dir, "HEAD", a.Path)
		if err != nil {
			return statsDoc{}, err
		}
		if !found {
			continue // removed since
		}
		l, err := lock.Parse(data)
		if err != nil || len(lock.Validate(l)) > 0 {
			doc.Invalid++
			continue
		}
		doc.Locks++
		lockCommits[a.Commit] = true
		for _, am := range l.Amendments {
			if am.Kind == lock.KindAddGlob {
				doc.Amended++
				break
			}
		}
		methods[l.Ratification.Method]++
		if ms := l.Ratification.ReviewDurationMS; ms != nil {
			durations = append(durations, *ms)
		}
	}

	doc.AmendmentRatio = fmt.Sprintf("%d/%d", doc.Amended, doc.Locks)
	doc.AmendmentRate = notAvailable
	if doc.Locks > 0 {
		doc.AmendmentRate = fmt.Sprintf("%.4f", float64(doc.Amended)/float64(doc.Locks))
	}
	for _, m := range merges {
		if m.Time.Before(start) || m.Time.After(headTime) {
			continue
		}
		doc.MergedPRs++
		if lockCommits[m.Commit] {
			doc.MergedPRsWithLock++
		}
	}
	doc.LockCoverageRatio = fmt.Sprintf("%d/%d", doc.MergedPRsWithLock, doc.MergedPRs)
	doc.LockCoverage = notAvailable
	if doc.MergedPRs > 0 {
		doc.LockCoverage = fmt.Sprintf("%.4f", float64(doc.MergedPRsWithLock)/float64(doc.MergedPRs))
	}
	for m, n := range methods {
		doc.Methods = append(doc.Methods, methodCount{m, n})
	}
	sort.Slice(doc.Methods, func(i, j int) bool { return doc.Methods[i].Method < doc.Methods[j].Method })
	if len(durations) > 0 {
		sort.Ints(durations)
		m := durations[(len(durations)-1)/2] // the lower middle for an even count
		doc.MedianReviewDurationMS = &m
	}
	if artifactsDir != "" {
		summary, err := readArtifactSummary(resolve(dir, artifactsDir), start, headTime)
		if err != nil {
			return statsDoc{}, err
		}
		doc.Artifacts = &summary
	}
	return doc, nil
}

func readArtifactSummary(dir string, start, end time.Time) (audit.ArtifactSummary, error) {
	info, err := os.Stat(dir)
	if err != nil {
		return audit.ArtifactSummary{}, err
	}
	if !info.IsDir() {
		return audit.ArtifactSummary{}, fmt.Errorf("%s is not a directory", dir)
	}
	var runs []audit.ArtifactRun
	err = filepath.WalkDir(dir, func(path string, d os.DirEntry, werr error) error {
		if werr != nil {
			return werr
		}
		if d.Name() != "audit.json" {
			return nil
		}
		run, ok, err := loadArtifactPair(dir, path, start, end)
		if err != nil {
			return err
		}
		if ok {
			runs = append(runs, run)
		}
		return nil
	})
	if err != nil {
		return audit.ArtifactSummary{}, err
	}
	return audit.SummarizeArtifacts(runs, start, end), nil
}

func loadArtifactPair(rootDir, auditPath string, start, end time.Time) (audit.ArtifactRun, bool, error) {
	ab, err := readRegularBounded(auditPath, maxArtifactBytes)
	if err != nil {
		return audit.ArtifactRun{}, false, err
	}
	var ev audit.Event
	if err := json.Unmarshal(ab, &ev); err != nil || ev.Schema != audit.SchemaID {
		return audit.ArtifactRun{}, false, fmt.Errorf("%s is not a %s event", auditPath, audit.SchemaID)
	}
	if ev.Event != audit.EventVerify {
		return audit.ArtifactRun{}, false, nil
	}
	at, err := time.Parse(time.RFC3339, ev.At)
	if err != nil {
		return audit.ArtifactRun{}, false, fmt.Errorf("%s has invalid timestamp %q: %w", auditPath, ev.At, err)
	}
	if ev.PR <= 0 || at.Before(start) || at.After(end) {
		return audit.ArtifactRun{}, false, nil
	}
	verifyPath := filepath.Join(filepath.Dir(auditPath), "verify.json")
	vb, err := readRegularBounded(verifyPath, maxArtifactBytes)
	if err != nil {
		return audit.ArtifactRun{}, false, err
	}
	if ev.VerifySHA256 != "" && sha256Hex(vb) != ev.VerifySHA256 {
		return audit.ArtifactRun{}, false, fmt.Errorf("%s does not match verify_sha256 in %s", verifyPath, auditPath)
	}
	outcome, findingsCount, suspectLocks, err := parseVerifyArtifact(verifyPath, vb, ev.Outcome)
	if err != nil {
		return audit.ArtifactRun{}, false, err
	}
	rel, err := filepath.Rel(rootDir, auditPath)
	if err != nil {
		rel = auditPath
	}
	return audit.ArtifactRun{
		Event:          ev,
		At:             at,
		VerifyOutcome:  outcome,
		FootprintCount: findingsCount,
		SuspectLocks:   suspectLocks,
		Source:         filepath.ToSlash(rel),
	}, true, nil
}

func readRegularBounded(path string, maxBytes int64) ([]byte, error) {
	f, err := os.OpenFile(path, os.O_RDONLY|auditOpenFlags, 0)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	info, err := f.Stat()
	if err != nil {
		return nil, err
	}
	if !info.Mode().IsRegular() {
		return nil, fmt.Errorf("%s is not a regular file", path)
	}
	data, err := io.ReadAll(io.LimitReader(f, maxBytes+1))
	if err != nil {
		return nil, err
	}
	if int64(len(data)) > maxBytes {
		return nil, fmt.Errorf("%s exceeds %d bytes", path, maxBytes)
	}
	return data, nil
}

type verifyArtifact struct {
	Schema  string `json:"schema"`
	Outcome string `json:"outcome"`
	Locks   []struct {
		Suspect []string `json:"suspect"`
	} `json:"locks"`
	Findings []json.RawMessage `json:"findings"`
}

func parseVerifyArtifact(path string, vb []byte, wantOutcome string) (outcome string, findings, suspect int, err error) {
	var v verifyArtifact
	if err := json.Unmarshal(vb, &v); err != nil {
		return "", 0, 0, fmt.Errorf("%s: invalid JSON: %w", path, err)
	}
	if v.Schema != "intentgate.verify/1" || !validVerifyOutcome(v.Outcome) {
		return "", 0, 0, fmt.Errorf("%s is not a valid intentgate.verify/1 document", path)
	}
	if wantOutcome != "" && v.Outcome != wantOutcome {
		return "", 0, 0, fmt.Errorf("%s outcome %q does not match audit.json outcome %q", path, v.Outcome, wantOutcome)
	}
	for _, l := range v.Locks {
		if len(l.Suspect) > 0 {
			suspect++
		}
	}
	return v.Outcome, len(v.Findings), suspect, nil
}

func validVerifyOutcome(s string) bool {
	switch s {
	case "skipped", "no-lock", "clean", "findings", "unverifiable":
		return true
	}
	return false
}

func statsText(d statsDoc) string {
	var b strings.Builder
	fmt.Fprintf(&b, "window: %s to %s (%dd)\n", d.WindowStart, d.WindowEnd, d.SinceDays)
	fmt.Fprintf(&b, "locks: %d valid, %d invalid\n", d.Locks, d.Invalid)
	if d.AmendmentRate == notAvailable {
		fmt.Fprintf(&b, "footprint-amendment rate: %s (%s)\n", notAvailable, d.AmendmentRatio)
	} else {
		fmt.Fprintf(&b, "footprint-amendment rate: %s (%s)\n", d.AmendmentRate, d.AmendmentRatio)
	}
	fmt.Fprintf(&b, "merged PRs: %d, with a lock: %d (coverage %s)\n", d.MergedPRs, d.MergedPRsWithLock, d.LockCoverage)
	parts := []string{}
	for _, m := range d.Methods {
		parts = append(parts, fmt.Sprintf("%s %d", m.Method, m.Count))
	}
	if len(parts) == 0 {
		parts = append(parts, "none")
	}
	fmt.Fprintf(&b, "ratification methods: %s\n", strings.Join(parts, ", "))
	if d.MedianReviewDurationMS == nil {
		fmt.Fprintf(&b, "median review duration: %s\n", notAvailable)
	} else {
		fmt.Fprintf(&b, "median review duration: %d ms\n", *d.MedianReviewDurationMS)
	}
	if a := d.Artifacts; a != nil {
		med := notAvailable
		if a.MedianFindingsPerPR != nil {
			med = strconv.Itoa(*a.MedianFindingsPerPR)
		}
		fmt.Fprintf(&b, "artifact PRs: %d (%d with a lock, %d runs)\n", a.PRs, a.PRsWithLock, a.Runs)
		fmt.Fprintf(&b, "PRs with findings: %s (%s, %d with suspect lock)\n", a.FindingsPRRate, a.FindingsPRRatio, a.PRsWithSuspectLock)
		fmt.Fprintf(&b, "findings per PR: %s (total %d, median %s)\n", a.FindingsPerPR, a.TotalFindings, med)
	}
	return b.String()
}
