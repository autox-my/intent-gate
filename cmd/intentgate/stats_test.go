package main

import (
	"bytes"
	"encoding/json"
	"os"
	"strings"
	"testing"
	"time"

	"intentgate/internal/lock"
	"intentgate/internal/ratify"
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
