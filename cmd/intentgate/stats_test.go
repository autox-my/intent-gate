package main

import (
	"bytes"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/autox-my/intent-gate/internal/audit"
	"github.com/autox-my/intent-gate/internal/lock"
	"github.com/autox-my/intent-gate/internal/ratify"
)

// The stats tests run `intentgate stats` against a temporary repository whose commits have fixed
// committer dates. The window ends at HEAD's date, so the wall clock is never read.

const (
	statsOutside   = "01JD8Z6Q4M7R2T9X3B5N8K1C8H"
	statsAddGlob   = "01JD8Z6Q4M7R2T9X3B5N8K1C9J"
	statsPlain     = "01JD8Z6Q4M7R2T9X3B5N8K1CAK"
	statsReratify  = "01JD8Z6Q4M7R2T9X3B5N8K1CBM"
	statsInvalidID = "01JD8Z6Q4M7R2T9X3B5N8K1CCN"
)

var (
	statsHead    = time.Date(2026, 12, 20, 12, 0, 0, 0, time.UTC)
	statsOldDate = time.Date(2026, 10, 1, 9, 0, 0, 0, time.UTC) // 80 days before HEAD
)

// statsLock builds a valid lock with a method and review duration; amend then changes it.
func statsLock(t *testing.T, id, method string, ms int, amend func(l *lock.Lock)) []byte {
	t.Helper()
	l, _, err := ratify.MaskAndBuild(noScan, "Add the thing.", ratify.BuildInput{
		ID: id, BaseRef: "main", Source: "claude-code-session",
		Criteria: []lock.Criterion{crit("C1", "The command is implemented", "src/**")},
		Ratifier: "github:octocat", RatifiedAt: ratifiedOK, Method: method, ReviewDurationMS: ms,
	})
	if err != nil {
		t.Fatal(err)
	}
	if amend != nil {
		amend(&l)
		h, err := lock.Hash(l)
		if err != nil {
			t.Fatal(err)
		}
		l.Hash = h
		if ps := lock.Validate(l); len(ps) != 0 {
			t.Fatalf("the amended test lock is invalid: %v", ps)
		}
	}
	return marshalLock(t, l)
}

func addGlob(l *lock.Lock) {
	l.Revision = 2
	l.Criteria[0].Footprint = append(l.Criteria[0].Footprint, "docs/**")
	l.Amendments = append(l.Amendments, lock.Amendment{Revision: 2, Kind: lock.KindAddGlob, Criterion: "C1", Glob: "docs/**",
		By: "git:dev@example.com", At: "2026-11-26T10:00:00Z", Method: "cli-confirm"})
}

func reratify(l *lock.Lock) {
	l.Revision = 2
	l.Amendments = append(l.Amendments, lock.Amendment{Revision: 2, Kind: lock.KindReratify,
		By: "git:dev@example.com", At: "2026-12-01T10:00:00Z", Method: "cli-confirm"})
}

// statsRepo has one lock outside the 30-day window, three valid ones and one invalid one inside it.
func statsRepo(t *testing.T) *vrepo {
	t.Helper()
	r := newVRepo(t)
	r.write(lockPath(statsOutside), string(statsLock(t, statsOutside, "hook", 500, nil)))
	r.commit("old lock", statsOldDate)
	r.write(lockPath(statsAddGlob), string(statsLock(t, statsAddGlob, "hook", 1000, addGlob)))
	r.commit("lock with an add-glob", time.Date(2026, 11, 25, 10, 0, 0, 0, time.UTC))
	r.write(lockPath(statsPlain), string(statsLock(t, statsPlain, "cli-confirm", 3000, nil)))
	r.commit("plain lock", time.Date(2026, 11, 26, 10, 0, 0, 0, time.UTC))
	r.write(lockPath(statsReratify), string(statsLock(t, statsReratify, "hook", 2000, reratify)))
	r.commit("re-ratified lock", time.Date(2026, 12, 1, 10, 0, 0, 0, time.UTC))
	r.write(lockPath(statsInvalidID), "not: [a lock\n")
	r.commit("invalid lock", time.Date(2026, 12, 2, 10, 0, 0, 0, time.UTC))
	r.write("README.md", "# readme, edited\n")
	r.commit("head", statsHead)
	return r
}

func (r *vrepo) stats(args ...string) (code int, stdout, stderr string) {
	r.t.Helper()
	var out, errb bytes.Buffer
	deps := Deps{
		Now:    func() time.Time { r.t.Error("stats read the clock"); return time.Time{} },
		Dir:    r.dir,
		Stderr: &errb,
	}
	code = Run(append([]string{"stats"}, args...), strings.NewReader(""), &out, deps)
	return code, out.String(), errb.String()
}

type statsOut struct {
	Schema         string `json:"schema"`
	SinceDays      int    `json:"since_days"`
	WindowStart    string `json:"window_start"`
	WindowEnd      string `json:"window_end"`
	Head           string `json:"head"`
	Locks          int    `json:"locks"`
	Invalid        int    `json:"invalid"`
	Amended        int    `json:"amended"`
	AmendmentRate  string `json:"amendment_rate"`
	AmendmentRatio string `json:"amendment_ratio"`
	Methods        []struct {
		Method string `json:"method"`
		Count  int    `json:"count"`
	} `json:"methods"`
	MedianReviewDurationMS *int `json:"median_review_duration_ms"`
}

// REQ-0041
func TestStats_AmendmentRate(t *testing.T) {
	r := statsRepo(t)

	t.Run("json: 1 of 3 valid locks in the window has an add-glob amendment", func(t *testing.T) {
		code, stdout, stderr := r.stats("--format", "json")
		if code != 0 || stderr != "" {
			t.Fatalf("exit %d, stderr %q, stdout %q", code, stderr, stdout)
		}
		var d statsOut
		if err := json.Unmarshal([]byte(stdout), &d); err != nil {
			t.Fatalf("stdout %q: %v", stdout, err)
		}
		if d.Schema != "intentgate.stats/1" || d.SinceDays != 30 || d.Head != r.git("rev-parse", "HEAD") {
			t.Errorf("schema %q, since_days %d, head %q", d.Schema, d.SinceDays, d.Head)
		}
		if d.WindowEnd != "2026-12-20T12:00:00Z" || d.WindowStart != "2026-11-20T12:00:00Z" {
			t.Errorf("window %s .. %s, want 2026-11-20T12:00:00Z .. 2026-12-20T12:00:00Z (HEAD's committer time minus 30 days)", d.WindowStart, d.WindowEnd)
		}
		if d.Locks != 3 || d.Invalid != 1 || d.Amended != 1 {
			t.Errorf("locks %d invalid %d amended %d, want 3 1 1", d.Locks, d.Invalid, d.Amended)
		}
		if d.AmendmentRate != "0.3333" || d.AmendmentRatio != "1/3" {
			t.Errorf("rate %q ratio %q, want 0.3333 and 1/3", d.AmendmentRate, d.AmendmentRatio)
		}
		if len(d.Methods) != 2 || d.Methods[0].Method != "cli-confirm" || d.Methods[0].Count != 1 || d.Methods[1].Method != "hook" || d.Methods[1].Count != 2 {
			t.Errorf("methods = %+v, want cli-confirm 1, hook 2", d.Methods)
		}
		if d.MedianReviewDurationMS == nil || *d.MedianReviewDurationMS != 2000 {
			t.Errorf("median = %v, want 2000", d.MedianReviewDurationMS)
		}
	})

	t.Run("text is the default format", func(t *testing.T) {
		code, stdout, stderr := r.stats()
		if code != 0 || stderr != "" {
			t.Fatalf("exit %d, stderr %q", code, stderr)
		}
		for _, want := range []string{
			"window: 2026-11-20T12:00:00Z to 2026-12-20T12:00:00Z (30d)",
			"locks: 3 valid, 1 invalid",
			"footprint-amendment rate: 0.3333 (1/3)",
			"ratification methods: cli-confirm 1, hook 2",
			"median review duration: 2000 ms",
		} {
			if !strings.Contains(stdout, want+"\n") {
				t.Errorf("stdout lacks the line %q:\n%s", want, stdout)
			}
		}
	})

	t.Run("a wider window takes in the old lock", func(t *testing.T) {
		code, stdout, _ := r.stats("--since", "90d", "--format", "json")
		var d statsOut
		if err := json.Unmarshal([]byte(stdout), &d); code != 0 || err != nil {
			t.Fatalf("exit %d, %v", code, err)
		}
		if d.Locks != 4 || d.Amended != 1 || d.AmendmentRatio != "1/4" || d.AmendmentRate != "0.2500" {
			t.Errorf("locks %d amended %d ratio %q rate %q, want 4 1 1/4 0.2500", d.Locks, d.Amended, d.AmendmentRatio, d.AmendmentRate)
		}
	})

	t.Run("a lock deleted since it was added is not counted", func(t *testing.T) {
		r := statsRepo(t)
		r.git("rm", "-q", lockPath(statsPlain))
		r.commit("remove", statsHead.Add(time.Hour))
		_, stdout, _ := r.stats("--format", "json")
		var d statsOut
		if err := json.Unmarshal([]byte(stdout), &d); err != nil || d.Locks != 2 || d.AmendmentRatio != "1/2" {
			t.Errorf("stdout %q: locks %d ratio %q, %v; want 2 locks and 1/2", stdout, d.Locks, d.AmendmentRatio, err)
		}
	})

	t.Run("no lock in the window: the rate is n/a, not 0", func(t *testing.T) {
		code, stdout, stderr := r.stats("--since", "1d", "--format", "json")
		var d statsOut
		if err := json.Unmarshal([]byte(stdout), &d); code != 0 || err != nil {
			t.Fatalf("exit %d, stderr %q, %v", code, stderr, err)
		}
		if d.Locks != 0 || d.AmendmentRate != "n/a" || d.MedianReviewDurationMS != nil || len(d.Methods) != 0 {
			t.Errorf("doc = %+v, want no locks, rate n/a, no median, no methods", d)
		}
		_, text, _ := r.stats("--since", "1d")
		for _, want := range []string{"footprint-amendment rate: n/a", "median review duration: n/a"} {
			if !strings.Contains(text, want) {
				t.Errorf("text lacks %q:\n%s", want, text)
			}
		}
	})

	t.Run("an even count takes the lower middle", func(t *testing.T) {
		r := newVRepo(t)
		for i, ms := range []int{4000, 1000, 3000, 2000} {
			id := []string{statsAddGlob, statsPlain, statsReratify, statsOutside}[i]
			r.write(lockPath(id), string(statsLock(t, id, "hook", ms, nil)))
			r.commit("lock", statsHead.Add(time.Duration(i-4)*time.Hour))
		}
		_, stdout, _ := r.stats("--format", "json")
		var d statsOut
		if err := json.Unmarshal([]byte(stdout), &d); err != nil || d.MedianReviewDurationMS == nil || *d.MedianReviewDurationMS != 2000 {
			t.Errorf("stdout %q: median %v, %v; want 2000 (the lower middle of 1000 2000 3000 4000)", stdout, d.MedianReviewDurationMS, err)
		}
	})

	t.Run("usage errors exit 2", func(t *testing.T) {
		for _, args := range [][]string{{"--since", "30"}, {"--since", "0d"}, {"--since", "-3d"}, {"--since", "xd"}, {"--format", "xml"}, {"extra"}} {
			if code, _, stderr := r.stats(args...); code != 2 {
				t.Errorf("stats %v: exit %d, stderr %q; want 2", args, code, stderr)
			}
		}
	})

	t.Run("outside a repository: exit 3", func(t *testing.T) {
		t.Setenv("GIT_CEILING_DIRECTORIES", os.TempDir())
		var out, errb bytes.Buffer
		code := Run([]string{"stats"}, strings.NewReader(""), &out, Deps{Dir: t.TempDir(), Stderr: &errb})
		if code != 3 {
			t.Errorf("exit %d, stderr %q; want 3", code, errb.String())
		}
	})
}

// REQ-0040
func TestStats_Deterministic(t *testing.T) {
	r := statsRepo(t)
	run := func(format string) string {
		t.Helper()
		code, stdout, stderr := r.stats("--format", format)
		if code != 0 || stdout == "" || stderr != "" {
			t.Fatalf("--format %s: exit %d, stdout %q, stderr %q", format, code, stdout, stderr)
		}
		return stdout
	}
	first := map[string]string{"text": run("text"), "json": run("json")}
	for format, want := range first {
		if got := run(format); got != want {
			t.Errorf("--format %s differs between two runs:\n%s\n---\n%s", format, want, got)
		}
	}
	t.Setenv("TZ", "Pacific/Auckland")
	t.Setenv("LC_ALL", "tr_TR.UTF-8")
	t.Setenv("LANG", "tr_TR.UTF-8")
	for format, want := range first {
		if got := run(format); got != want {
			t.Errorf("--format %s differs under another TZ and LC_ALL:\n%s\n---\n%s", format, want, got)
		}
	}
}

// REQ-0041
func TestStats_RenamedLockIsNotNew(t *testing.T) {
	r := newVRepo(t)
	r.write(lockPath(statsOutside), string(statsLock(t, statsOutside, "hook", 500, nil))) // added 80 days before HEAD
	r.commit("old lock", statsOldDate)
	r.git("mv", lockPath(statsOutside), ".intent/renamed.yml")
	r.commit("rename inside the window", time.Date(2026, 12, 10, 10, 0, 0, 0, time.UTC))
	r.git("mv", ".intent/renamed.yml", ".intent/renamed-again.yml")
	r.commit("rename again", time.Date(2026, 12, 15, 10, 0, 0, 0, time.UTC))
	r.write("README.md", "# readme, edited\n")
	r.commit("head", statsHead)

	run := func(since string) statsOut {
		t.Helper()
		code, stdout, stderr := r.stats("--since", since, "--format", "json")
		if code != 0 {
			t.Fatalf("exit %d, stderr %q", code, stderr)
		}
		var got statsOut
		if err := json.Unmarshal([]byte(stdout), &got); err != nil {
			t.Fatal(err)
		}
		return got
	}
	if got := run("30d"); got.Locks != 0 || got.Invalid != 0 || got.AmendmentRate != "n/a" {
		t.Errorf("30d: locks %d, invalid %d, rate %q; want a renamed old lock not counted as new", got.Locks, got.Invalid, got.AmendmentRate)
	}
	if got := run("100d"); got.Locks != 1 {
		t.Errorf("100d: locks %d, want 1 (the lock keeps its original add time)", got.Locks)
	}
}

// mergePR makes a side branch with one commit that writes files (name -> content), then merges it
// into main with a merge commit at mergeAt.
func (r *vrepo) mergePR(name string, files map[string]string, sideAt, mergeAt time.Time) {
	r.t.Helper()
	r.git("checkout", "-q", "-b", name)
	if len(files) == 0 {
		files = map[string]string{name + ".txt": name + "\n"}
	}
	for f, c := range files {
		r.write(f, c)
	}
	r.commit(name, sideAt)
	r.git("checkout", "-q", "main")
	r.git("add", "-A")
	r.gitAt(mergeAt, "merge", "-q", "--no-ff", name, "-m", "merge "+name)
}

type coverageOut struct {
	MergedPRs         int    `json:"merged_prs"`
	MergedPRsWithLock int    `json:"merged_prs_with_lock"`
	LockCoverage      string `json:"lock_coverage"`
	LockCoverageRatio string `json:"lock_coverage_ratio"`
	Locks             int    `json:"locks"`
}

func (r *vrepo) coverage(args ...string) (coverageOut, string) {
	r.t.Helper()
	code, stdout, stderr := r.stats(append(args, "--format", "json")...)
	if code != 0 {
		r.t.Fatalf("exit %d, stderr %q", code, stderr)
	}
	var d coverageOut
	if err := json.Unmarshal([]byte(stdout), &d); err != nil {
		r.t.Fatalf("stdout %q: %v", stdout, err)
	}
	return d, stdout
}

// REQ-0041
func TestStats_MergedPRCoverage(t *testing.T) {
	day := func(m time.Month, d int) time.Time { return time.Date(2026, m, d, 10, 0, 0, 0, time.UTC) }
	lockFile := func(id string, amend func(*lock.Lock)) map[string]string {
		return map[string]string{lockPath(id): string(statsLock(t, id, "hook", 1000, amend))}
	}

	repo := func(t *testing.T) *vrepo {
		t.Helper()
		r := newVRepo(t)
		r.mergePR("old", lockFile(statsOutside, nil), day(9, 29), statsOldDate) // before the window, with a lock
		r.mergePR("one", lockFile(statsAddGlob, addGlob), day(11, 24), day(11, 25))
		r.mergePR("two", lockFile(statsPlain, nil), day(11, 30), day(12, 1))
		r.mergePR("three", nil, day(12, 4), day(12, 5)) // no lock
		r.write(lockPath(statsReratify), string(statsLock(t, statsReratify, "hook", 2000, reratify)))
		r.commit("a lock straight on main", day(12, 10))
		r.write("README.md", "# readme, edited\n")
		r.commit("head", statsHead)
		return r
	}

	t.Run("3 merges in the window, 2 bring a valid lock; a direct lock is in neither PR field", func(t *testing.T) {
		d, _ := repo(t).coverage()
		if d.MergedPRs != 3 || d.MergedPRsWithLock != 2 || d.LockCoverage != "0.6667" || d.LockCoverageRatio != "2/3" {
			t.Errorf("merged_prs %d with_lock %d coverage %q ratio %q, want 3 2 0.6667 2/3", d.MergedPRs, d.MergedPRsWithLock, d.LockCoverage, d.LockCoverageRatio)
		}
		if d.Locks != 3 {
			t.Errorf("locks %d, want 3 (the two merged ones and the direct one)", d.Locks)
		}
	})

	t.Run("a wider window takes in the old merge", func(t *testing.T) {
		d, _ := repo(t).coverage("--since", "90d")
		if d.MergedPRs != 4 || d.MergedPRsWithLock != 3 || d.LockCoverage != "0.7500" || d.LockCoverageRatio != "3/4" {
			t.Errorf("merged_prs %d with_lock %d coverage %q ratio %q, want 4 3 0.7500 3/4", d.MergedPRs, d.MergedPRsWithLock, d.LockCoverage, d.LockCoverageRatio)
		}
	})

	t.Run("a merge whose only lock is invalid is not with a lock", func(t *testing.T) {
		r := newVRepo(t)
		r.mergePR("bad", map[string]string{lockPath(statsInvalidID): "not: [a lock\n"}, day(12, 1), day(12, 2))
		r.write("README.md", "# readme, edited\n")
		r.commit("head", statsHead)
		d, _ := r.coverage()
		if d.MergedPRs != 1 || d.MergedPRsWithLock != 0 || d.LockCoverage != "0.0000" || d.LockCoverageRatio != "0/1" {
			t.Errorf("merged_prs %d with_lock %d coverage %q ratio %q, want 1 0 0.0000 0/1", d.MergedPRs, d.MergedPRsWithLock, d.LockCoverage, d.LockCoverageRatio)
		}
	})

	t.Run("no merge in the window: the coverage is n/a, not 0", func(t *testing.T) {
		r := repo(t)
		d, _ := r.coverage("--since", "1d")
		if d.MergedPRs != 0 || d.MergedPRsWithLock != 0 || d.LockCoverage != "n/a" || d.LockCoverageRatio != "0/0" {
			t.Errorf("merged_prs %d with_lock %d coverage %q ratio %q, want 0 0 n/a 0/0", d.MergedPRs, d.MergedPRsWithLock, d.LockCoverage, d.LockCoverageRatio)
		}
		_, text, _ := r.stats("--since", "1d")
		if want := "merged PRs: 0, with a lock: 0 (coverage n/a)\n"; !strings.Contains(text, want) {
			t.Errorf("text lacks %q:\n%s", want, text)
		}
	})

	t.Run("JSON keys follow amendment_ratio in order, and the text line follows the amendment rate", func(t *testing.T) {
		r := repo(t)
		_, js := r.coverage()
		last := strings.Index(js, `"amendment_ratio"`)
		if last < 0 {
			t.Fatalf("no amendment_ratio in %s", js)
		}
		for _, key := range []string{`"merged_prs"`, `"merged_prs_with_lock"`, `"lock_coverage"`, `"lock_coverage_ratio"`, `"methods"`} {
			i := strings.Index(js, key)
			if i < last {
				t.Fatalf("key %s is missing or out of order in:\n%s", key, js)
			}
			last = i
		}
		_, text, _ := r.stats()
		lines := strings.Split(text, "\n")
		for i, l := range lines {
			if strings.HasPrefix(l, "footprint-amendment rate: ") {
				if i+1 >= len(lines) || lines[i+1] != "merged PRs: 3, with a lock: 2 (coverage 0.6667)" {
					t.Errorf("the line after the amendment rate is not the merged-PR line:\n%s", text)
				}
				return
			}
		}
		t.Errorf("no amendment-rate line in:\n%s", text)
	})
}

type artifactsOut struct {
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

func writeArtifactPair(t *testing.T, dir, repo string, pr int, at, headSHA, outcome string, findings int, suspect []string, locksExtra int) (auditPath, verifyPath string) {
	t.Helper()
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	var fs []map[string]any
	for i := 0; i < findings; i++ {
		fs = append(fs, map[string]any{
			"path":              fmt.Sprintf("file%d.go", i),
			"status":            "modified",
			"nearest_criterion": "C1",
			"lock_id":           statsPlain,
			"fix":               "intentgate amend",
		})
	}
	if fs == nil {
		fs = []map[string]any{}
	}
	var ls []map[string]any
	if outcome != "no-lock" && outcome != "skipped" {
		if suspect == nil {
			suspect = []string{}
		}
		ls = append(ls, map[string]any{
			"path": lockPath(statsPlain), "id": statsPlain, "revision": 1,
			"suspect": suspect, "problems": []string{}, "criteria": []any{},
		})
		for i := 0; i < locksExtra; i++ {
			ls = append(ls, map[string]any{
				"path": lockPath(statsAddGlob), "id": statsAddGlob, "revision": 1,
				"suspect": []string{}, "problems": []string{}, "criteria": []any{},
			})
		}
	} else {
		ls = []map[string]any{}
	}
	vdoc := map[string]any{
		"schema":    "intentgate.verify/1",
		"outcome":   outcome,
		"exit_code": map[string]int{"clean": 0, "no-lock": 0, "skipped": 0, "findings": 1, "unverifiable": 3}[outcome],
		"problems":  []string{},
		"locks":     ls,
		"findings":  fs,
	}
	vb, err := json.Marshal(vdoc)
	if err != nil {
		t.Fatal(err)
	}
	ev := audit.Event{
		Schema: audit.SchemaID, Event: audit.EventVerify, At: at,
		Repo: repo, PR: pr, BaseSHA: strings.Repeat("a", 40), HeadSHA: headSHA,
		BaseBranch: "main", Author: "octocat", AuthorType: "User",
		IntentGateVersion: "0.3.0", VerifySHA256: sha256Hex(vb),
		Locks: []audit.LockEntry{}, Outcome: outcome,
		ExitCode: vdoc["exit_code"].(int), Conclusion: "neutral",
	}
	verifyPath = filepath.Join(dir, "verify.json")
	auditPath = filepath.Join(dir, "audit.json")
	if err := os.WriteFile(verifyPath, vb, 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(auditPath, audit.JSON(ev), 0o644); err != nil {
		t.Fatal(err)
	}
	return auditPath, verifyPath
}

// REQ-0040, REQ-0041, REQ-0042
func TestStats_ArtifactsFindingsPerPR(t *testing.T) {
	// Populate a separate repo so a real `intentgate verify --out-dir` run feeds PR #2 inside r's window.
	buildRealVerifyPR2 := func(t *testing.T, outDir string) {
		t.Helper()
		vr := newVRepo(t)
		vr.feature()
		_, ldata := mkLock(t, lockA, "main", ratifiedOK, crit("C1", "Docs only", "docs/**"))
		vr.write(lockPath(lockA), string(ldata))
		vr.write("a.go", "package main\n")
		vr.write("b.go", "package main\n")
		vr.commit("two findings outside footprint", headDate)
		var out, errb bytes.Buffer
		deps := Deps{
			Now: func() time.Time { return time.Date(2026, 11, 26, 12, 0, 0, 0, time.UTC) },
			Dir: vr.dir, Stderr: &errb,
		}
		code := Run([]string{
			"verify", "--base", "main", "--head", "feature", "--base-branch", "main",
			"--author", "octocat", "--author-type", "User",
			"--out-dir", outDir, "--repo", "owner/name", "--pr", "2",
		}, strings.NewReader(""), &out, deps)
		if code != 1 {
			t.Fatalf("real verify exit = %d, want 1; stderr=%q", code, errb.String())
		}
	}

	buildArtifactsDir := func(t *testing.T) string {
		t.Helper()
		dir := t.TempDir()
		h := strings.Repeat("1", 40)
		// PR 1: 2 runs in window, earlier findings=3 -> later clean=0 (latest wins)
		writeArtifactPair(t, filepath.Join(dir, "pr1", "run1"), "owner/name", 1, "2026-11-24T10:00:00Z", h, "findings", 3, nil, 0)
		writeArtifactPair(t, filepath.Join(dir, "pr1", "run2"), "owner/name", 1, "2026-11-25T10:00:00Z", strings.Repeat("2", 40), "clean", 0, nil, 0)
		// PR 2: real verify run -> findings=2, suspect=0
		buildRealVerifyPR2(t, filepath.Join(dir, "pr2"))
		// PR 3: findings=0, suspect=1
		writeArtifactPair(t, filepath.Join(dir, "pr3"), "owner/name", 3, "2026-11-28T10:00:00Z", h, "findings", 0, []string{"expired"}, 0)
		// PR 4: findings=4, suspect=1
		writeArtifactPair(t, filepath.Join(dir, "nested", "pr4"), "owner/name", 4, "2026-12-01T10:00:00Z", h, "findings", 4, []string{"repo-wide-footprint"}, 1)
		// PR 5: no-lock
		writeArtifactPair(t, filepath.Join(dir, "pr5"), "owner/name", 5, "2026-12-02T10:00:00Z", h, "no-lock", 0, nil, 0)
		// PR 6: bot skipped
		writeArtifactPair(t, filepath.Join(dir, "pr6"), "owner/name", 6, "2026-12-03T10:00:00Z", h, "skipped", 0, nil, 0)
		// PR 7: outside 30d window
		writeArtifactPair(t, filepath.Join(dir, "pr7"), "owner/name", 7, "2026-10-15T10:00:00Z", h, "findings", 9, []string{"expired"}, 0)
		// Ignored: pr=0 and non-verify lock event
		writeArtifactPair(t, filepath.Join(dir, "local"), "owner/name", 0, "2026-12-04T10:00:00Z", h, "findings", 5, nil, 0)
		if err := os.MkdirAll(filepath.Join(dir, "lockonly"), 0o755); err != nil {
			t.Fatal(err)
		}
		lockEv := audit.NewLockEvent(audit.EventLock, lock.Lock{ID: statsPlain, Revision: 1, Hash: "sha256:aa", Ratification: lock.Ratification{Method: "hook"}}, time.Date(2026, 12, 4, 10, 0, 0, 0, time.UTC), "0.3.0", "owner/name")
		if err := os.WriteFile(filepath.Join(dir, "lockonly", "audit.json"), audit.JSON(lockEv), 0o644); err != nil {
			t.Fatal(err)
		}
		return dir
	}

	t.Run("computes exact JSON and text metrics with --artifacts", func(t *testing.T) {
		r := statsRepo(t)
		art := buildArtifactsDir(t)

		code, stdout, stderr := r.stats("--artifacts", art, "--format", "json")
		if code != 0 || stderr != "" {
			t.Fatalf("json exit %d, stderr %q, stdout %q", code, stderr, stdout)
		}
		var doc struct {
			statsOut
			Artifacts *artifactsOut `json:"artifacts"`
		}
		if err := json.Unmarshal([]byte(stdout), &doc); err != nil {
			t.Fatalf("unmarshal %q: %v", stdout, err)
		}
		if doc.Artifacts == nil {
			t.Fatalf("artifacts missing from JSON: %s", stdout)
		}
		a := doc.Artifacts
		if a.Runs != 6 || a.PRs != 5 || a.PRsWithLock != 4 || a.PRsWithFindings != 3 ||
			a.FindingsPRRate != "0.7500" || a.FindingsPRRatio != "3/4" ||
			a.TotalFindings != 6 || a.FindingsPerPR != "1.5000" ||
			a.MedianFindingsPerPR == nil || *a.MedianFindingsPerPR != 0 || a.PRsWithSuspectLock != 2 {
			t.Errorf("artifacts = %+v (median=%v)", a, a.MedianFindingsPerPR)
		}

		// Key order check inside JSON.
		last := strings.Index(stdout, `"median_review_duration_ms"`)
		for _, k := range []string{
			`"artifacts"`, `"runs"`, `"prs"`, `"prs_with_lock"`, `"prs_with_findings"`,
			`"findings_pr_rate"`, `"findings_pr_ratio"`, `"total_findings"`, `"findings_per_pr"`,
			`"median_findings_per_pr"`, `"prs_with_suspect_lock"`,
		} {
			idx := strings.Index(stdout, k)
			if idx <= last {
				t.Fatalf("key %s out of order in:\n%s", k, stdout)
			}
			last = idx
		}

		// Text output lines.
		code, text, stderr := r.stats("--artifacts", art)
		if code != 0 || stderr != "" {
			t.Fatalf("text exit %d, stderr %q", code, stderr)
		}
		for _, want := range []string{
			"artifact PRs: 5 (4 with a lock, 6 runs)\n",
			"PRs with findings: 0.7500 (3/4, 2 with suspect lock)\n",
			"findings per PR: 1.5000 (total 6, median 0)\n",
		} {
			if !strings.Contains(text, want) {
				t.Errorf("text lacks %q:\n%s", want, text)
			}
		}

		// Deterministic across TZ/LC_ALL and identical for relative path vs absolute path.
		rel, err := filepath.Rel(r.dir, art)
		if err != nil {
			rel = art
		}
		t.Setenv("TZ", "Pacific/Auckland")
		t.Setenv("LC_ALL", "tr_TR.UTF-8")
		t.Setenv("LANG", "tr_TR.UTF-8")
		_, stdout2, _ := r.stats("--artifacts", rel, "--format", "json")
		_, text2, _ := r.stats("--artifacts", rel)
		if stdout2 != stdout || text2 != text {
			t.Errorf("not deterministic across TZ/LC_ALL/relative path")
		}
	})

	t.Run("without --artifacts, JSON and text omit artifact fields", func(t *testing.T) {
		r := statsRepo(t)
		_, js, _ := r.stats("--format", "json")
		_, txt, _ := r.stats()
		if strings.Contains(js, `"artifacts"`) || strings.Contains(txt, "artifact PRs:") {
			t.Errorf("unexpected artifact output:\n%s\n%s", js, txt)
		}
	})

	t.Run("non-zero median (odd and even lower-middle) and empty window", func(t *testing.T) {
		r := statsRepo(t)
		dOdd := t.TempDir()
		h := strings.Repeat("1", 40)
		writeArtifactPair(t, filepath.Join(dOdd, "1"), "o/r", 1, "2026-12-01T10:00:00Z", h, "findings", 1, nil, 0)
		writeArtifactPair(t, filepath.Join(dOdd, "2"), "o/r", 2, "2026-12-02T10:00:00Z", h, "findings", 2, nil, 0)
		writeArtifactPair(t, filepath.Join(dOdd, "3"), "o/r", 3, "2026-12-03T10:00:00Z", h, "findings", 4, nil, 0)
		_, jsOdd, _ := r.stats("--artifacts", dOdd, "--format", "json")
		if !strings.Contains(jsOdd, `"median_findings_per_pr": 2`) {
			t.Errorf("odd median wrong:\n%s", jsOdd)
		}

		dEven := t.TempDir()
		writeArtifactPair(t, filepath.Join(dEven, "1"), "o/r", 1, "2026-12-01T10:00:00Z", h, "findings", 1, nil, 0)
		writeArtifactPair(t, filepath.Join(dEven, "2"), "o/r", 2, "2026-12-02T10:00:00Z", h, "findings", 3, nil, 0)
		_, jsEven, _ := r.stats("--artifacts", dEven, "--format", "json")
		if !strings.Contains(jsEven, `"median_findings_per_pr": 1`) {
			t.Errorf("even lower-middle median wrong:\n%s", jsEven)
		}

		empty := t.TempDir()
		code, jsEmpty, _ := r.stats("--artifacts", empty, "--format", "json")
		if code != 0 || !strings.Contains(jsEmpty, `"findings_pr_rate": "n/a"`) || !strings.Contains(jsEmpty, `"median_findings_per_pr": null`) {
			t.Errorf("empty artifacts exit %d:\n%s", code, jsEmpty)
		}
		_, txt1d, _ := r.stats("--artifacts", dEven, "--since", "1d")
		if !strings.Contains(txt1d, "findings per PR: n/a (total 0, median n/a)\n") {
			t.Errorf("1d text wrong:\n%s", txt1d)
		}
	})

	t.Run("error cases: usage exit 2 and corrupt or untrusted artifacts exit 3", func(t *testing.T) {
		r := statsRepo(t)
		if code, _, _ := r.stats("--artifacts", ""); code != 2 {
			t.Errorf("--artifacts \"\": exit %d, want 2", code)
		}
		notDir := filepath.Join(t.TempDir(), "file")
		if err := os.WriteFile(notDir, []byte("x"), 0o644); err != nil {
			t.Fatal(err)
		}
		badCases := map[string]func(t *testing.T) string{
			"missing dir":  func(*testing.T) string { return filepath.Join(t.TempDir(), "none") },
			"file not dir": func(*testing.T) string { return notDir },
			"corrupt audit": func(t *testing.T) string {
				d := t.TempDir()
				_ = os.WriteFile(filepath.Join(d, "audit.json"), []byte("{"), 0o644)
				return d
			},
			"wrong audit schema": func(t *testing.T) string {
				d := t.TempDir()
				_ = os.WriteFile(filepath.Join(d, "audit.json"), []byte(`{"schema":"other/1"}`), 0o644)
				return d
			},
			"bad at timestamp": func(t *testing.T) string {
				d := t.TempDir()
				_ = os.WriteFile(filepath.Join(d, "audit.json"), []byte(`{"schema":"intentgate.audit/1","event":"verify","at":"not-a-time","pr":1}`), 0o644)
				return d
			},
			"missing verify.json": func(t *testing.T) string {
				d := t.TempDir()
				ap, vp := writeArtifactPair(t, d, "o/r", 1, "2026-12-01T10:00:00Z", strings.Repeat("1", 40), "clean", 0, nil, 0)
				_ = ap
				_ = os.Remove(vp)
				return d
			},
			"verify_sha256 mismatch": func(t *testing.T) string {
				d := t.TempDir()
				_, vp := writeArtifactPair(t, d, "o/r", 1, "2026-12-01T10:00:00Z", strings.Repeat("1", 40), "clean", 0, nil, 0)
				_ = os.WriteFile(vp, []byte(`{"schema":"intentgate.verify/1","outcome":"clean","locks":[],"findings":[]}`), 0o644)
				return d
			},
			"corrupt verify.json": func(t *testing.T) string {
				d := t.TempDir()
				ap, vp := writeArtifactPair(t, d, "o/r", 1, "2026-12-01T10:00:00Z", strings.Repeat("1", 40), "clean", 0, nil, 0)
				bad := []byte("{")
				_ = os.WriteFile(vp, bad, 0o644)
				ev := audit.Event{Schema: audit.SchemaID, Event: audit.EventVerify, At: "2026-12-01T10:00:00Z", PR: 1, VerifySHA256: sha256Hex(bad), Outcome: "clean", Locks: []audit.LockEntry{}}
				_ = os.WriteFile(ap, audit.JSON(ev), 0o644)
				return d
			},
			"wrong verify schema": func(t *testing.T) string {
				d := t.TempDir()
				ap, vp := writeArtifactPair(t, d, "o/r", 1, "2026-12-01T10:00:00Z", strings.Repeat("1", 40), "clean", 0, nil, 0)
				bad := []byte(`{"schema":"wrong/1","outcome":"clean","locks":[],"findings":[]}`)
				_ = os.WriteFile(vp, bad, 0o644)
				ev := audit.Event{Schema: audit.SchemaID, Event: audit.EventVerify, At: "2026-12-01T10:00:00Z", PR: 1, VerifySHA256: sha256Hex(bad), Outcome: "clean", Locks: []audit.LockEntry{}}
				_ = os.WriteFile(ap, audit.JSON(ev), 0o644)
				return d
			},
			"invalid verify outcome": func(t *testing.T) string {
				d := t.TempDir()
				ap, vp := writeArtifactPair(t, d, "o/r", 1, "2026-12-01T10:00:00Z", strings.Repeat("1", 40), "clean", 0, nil, 0)
				bad := []byte(`{"schema":"intentgate.verify/1","outcome":"bogus","locks":[],"findings":[]}`)
				_ = os.WriteFile(vp, bad, 0o644)
				ev := audit.Event{Schema: audit.SchemaID, Event: audit.EventVerify, At: "2026-12-01T10:00:00Z", PR: 1, VerifySHA256: sha256Hex(bad), Outcome: "bogus", Locks: []audit.LockEntry{}}
				_ = os.WriteFile(ap, audit.JSON(ev), 0o644)
				return d
			},
			"outcome mismatch": func(t *testing.T) string {
				d := t.TempDir()
				ap, vp := writeArtifactPair(t, d, "o/r", 1, "2026-12-01T10:00:00Z", strings.Repeat("1", 40), "clean", 0, nil, 0)
				v := []byte(`{"schema":"intentgate.verify/1","outcome":"findings","locks":[],"findings":[]}`)
				_ = os.WriteFile(vp, v, 0o644)
				ev := audit.Event{Schema: audit.SchemaID, Event: audit.EventVerify, At: "2026-12-01T10:00:00Z", PR: 1, VerifySHA256: sha256Hex(v), Outcome: "clean", Locks: []audit.LockEntry{}}
				_ = os.WriteFile(ap, audit.JSON(ev), 0o644)
				return d
			},
			"symlinked audit.json": func(t *testing.T) string {
				d := t.TempDir()
				realDir := t.TempDir()
				ap, _ := writeArtifactPair(t, realDir, "o/r", 1, "2026-12-01T10:00:00Z", strings.Repeat("1", 40), "clean", 0, nil, 0)
				_ = os.Symlink(ap, filepath.Join(d, "audit.json"))
				return d
			},
			"symlinked verify.json": func(t *testing.T) string {
				d := t.TempDir()
				realDir := t.TempDir()
				ap, vp := writeArtifactPair(t, realDir, "o/r", 1, "2026-12-01T10:00:00Z", strings.Repeat("1", 40), "clean", 0, nil, 0)
				data, _ := os.ReadFile(ap)
				_ = os.WriteFile(filepath.Join(d, "audit.json"), data, 0o644)
				_ = os.Symlink(vp, filepath.Join(d, "verify.json"))
				return d
			},
			"directory named audit.json": func(t *testing.T) string {
				d := t.TempDir()
				_ = os.MkdirAll(filepath.Join(d, "audit.json"), 0o755)
				return d
			},
		}
		for name, mk := range badCases {
			t.Run(name, func(t *testing.T) {
				if code, _, stderr := r.stats("--artifacts", mk(t)); code != 3 || stderr == "" {
					t.Errorf("%s: exit %d, stderr %q; want 3 and error message", name, code, stderr)
				}
			})
		}
	})
}
