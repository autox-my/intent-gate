// Package verify is the pure decision core of the PR check: from the pull request's inputs, as
// values, it decides skip / no lock / lifecycle / footprint / outcome. It does no I/O, reads no
// clock, uses no randomness and makes no network call (REQ-0040).
package verify

import (
	"bytes"
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/autox-my/intent-gate/internal/footprint"
	"github.com/autox-my/intent-gate/internal/lock"
)

// LockFile is one changed path under .intent/ ending in .yml, with its content at head (nil when
// the file was deleted).
type LockFile struct {
	Path    string
	Status  footprint.Status
	OldPath string
	Data    []byte
}

// Input is everything the check looks at. It has no field for the pull request's title,
// description or commit messages, so criteria can't come from them (REQ-0032).
type Input struct {
	BaseBranch, Author, AuthorType string
	EvalTime                       time.Time
	Changed                        []footprint.ChangedFile
	LockFiles                      []LockFile
	Config, GitAttributes          []byte
}

// Outcome is the verdict of one run.
type Outcome string

// The outcomes, in the order of pr-check.md §4.
const (
	Skipped      Outcome = "skipped"
	NoLock       Outcome = "no-lock"
	Clean        Outcome = "clean"
	Findings     Outcome = "findings"
	Unverifiable Outcome = "unverifiable"
)

// Suspect reasons (pr-check.md §2, §3).
const (
	ReasonExpired      = "expired"
	ReasonHashMismatch = "hash-mismatch"
	ReasonModifiesLock = "modifies-existing-lock"
	ReasonRetargeted   = "retargeted"
	ReasonRepoWide     = "repo-wide-footprint"
)

// Criterion is one criterion of a usable lock, as shown in the report.
type Criterion struct {
	ID        string   `json:"id"`
	Text      string   `json:"text"`
	Footprint []string `json:"footprint"`
}

// LockResult is what the check found out about one lock file.
type LockResult struct {
	Path     string      `json:"path"`
	ID       string      `json:"id"`
	Revision int         `json:"revision"`
	Suspect  []string    `json:"suspect"`
	Problems []string    `json:"problems"`
	Criteria []Criterion `json:"criteria"`
}

// Result is the outcome of Run. Problems are document-level problems (a malformed config).
type Result struct {
	Outcome  Outcome
	ExitCode int
	Problems []string
	Locks    []LockResult
	Findings []footprint.Finding
}

// defaultSkipAuthors are the bot logins skipped without configuration (pr-check.md §1).
var defaultSkipAuthors = []string{"dependabot[bot]", "renovate[bot]"}

const lockDir = ".intent/"

// Run decides the outcome for in (pr-check.md §1, core-contracts.md §3). It never mutates in.
func Run(in Input) Result {
	// 1. skip bots (REQ-0034); the author type is checked before the config is parsed
	if in.AuthorType == "Bot" || contains(defaultSkipAuthors, in.Author) {
		return Result{Outcome: Skipped, Problems: []string{}, Locks: []LockResult{}, Findings: []footprint.Finding{}}
	}

	// 2. config
	cfg, err := footprint.ParseConfig(in.Config)
	var implicit footprint.Implicit
	if err == nil {
		implicit, err = footprint.NewImplicit(cfg, in.GitAttributes)
	}
	if err != nil {
		return Result{Outcome: Unverifiable, ExitCode: 3, Problems: []string{err.Error()}, Locks: []LockResult{}, Findings: []footprint.Finding{}}
	}
	if contains(cfg.SkipAuthors, in.Author) {
		return Result{Outcome: Skipped, Problems: []string{}, Locks: []LockResult{}, Findings: []footprint.Finding{}}
	}

	// 3. the input itself must be sound: one entry per lock file path
	if problems := inputProblems(in); len(problems) > 0 {
		return Result{Outcome: Unverifiable, ExitCode: 3, Problems: problems, Locks: []LockResult{}, Findings: []footprint.Finding{}}
	}

	// discovery: added files are this PR's locks, modified or renamed ones are existing locks
	// being changed, deleted ones are ignored, any other status is not understood
	files := make([]LockFile, 0, len(in.LockFiles))
	for _, f := range in.LockFiles {
		if f.Status != footprint.Deleted {
			files = append(files, f)
		}
	}
	sort.SliceStable(files, func(i, j int) bool { return lockFileLess(files[i], files[j]) })
	if len(files) == 0 {
		return Result{Outcome: NoLock, Problems: []string{}, Locks: []LockResult{}, Findings: []footprint.Finding{}}
	}

	// 4. lifecycle
	res := Result{Problems: []string{}, Locks: make([]LockResult, 0, len(files)), Findings: []footprint.Finding{}}
	var criteria []footprint.Criterion
	unverifiable, suspect := false, false
	for _, f := range files {
		lr, l, ok := checkLock(in, f)
		res.Locks = append(res.Locks, lr)
		if !ok {
			unverifiable = true
			continue
		}
		if len(lr.Suspect) > 0 {
			suspect = true
		}
		for _, c := range l.Criteria {
			criteria = append(criteria, footprint.Criterion{LockID: l.ID, ID: c.ID, Footprint: c.Footprint})
		}
	}

	// 5. footprint: with no usable criterion every path would be "outside", which says nothing
	if len(criteria) > 0 {
		changed := append([]footprint.ChangedFile(nil), in.Changed...)
		sort.SliceStable(changed, func(i, j int) bool {
			if changed[i].Path != changed[j].Path {
				return changed[i].Path < changed[j].Path
			}
			if changed[i].OldPath != changed[j].OldPath {
				return changed[i].OldPath < changed[j].OldPath
			}
			return changed[i].Status < changed[j].Status
		})
		res.Findings = footprint.Check(changed, criteria, implicit)
	}

	// 6. outcome
	switch {
	case unverifiable:
		res.Outcome, res.ExitCode = Unverifiable, 3
	case suspect || len(res.Findings) > 0:
		res.Outcome, res.ExitCode = Findings, 1
	default:
		res.Outcome = Clean
	}
	return res
}

// checkLock parses and validates one lock file and works out its suspect reasons. ok is false when
// the lock is unverifiable; it then contributes no criteria.
func checkLock(in Input, f LockFile) (LockResult, lock.Lock, bool) {
	lr := LockResult{Path: f.Path, Suspect: []string{}, Problems: []string{}, Criteria: []Criterion{}}
	switch f.Status {
	case footprint.Added, footprint.Modified, footprint.Renamed:
	default:
		shown := string(f.Status)
		if shown == "" {
			shown = `""`
		}
		lr.Problems = []string{"unsupported change status " + shown}
		return lr, lock.Lock{}, false
	}
	l, err := lock.Parse(f.Data)
	if err != nil {
		lr.Problems = []string{err.Error()}
		return lr, l, false
	}
	lr.ID, lr.Revision = l.ID, l.Revision
	for _, p := range lock.Validate(l) {
		lr.Problems = append(lr.Problems, p.String())
	}
	if want := lockDir + l.ID + ".yml"; f.Path != want {
		lr.Problems = append(lr.Problems, fmt.Sprintf("file name: a lock must be stored as %s, not %s", want, f.Path))
	}
	if len(lr.Problems) > 0 {
		sort.Strings(lr.Problems)
		return lr, l, false
	}

	if !lock.ValidAt(l, in.EvalTime) {
		lr.Suspect = append(lr.Suspect, ReasonExpired)
	}
	if !lock.VerifyHash(l) {
		lr.Suspect = append(lr.Suspect, ReasonHashMismatch)
	}
	if f.Status == footprint.Modified || f.Status == footprint.Renamed {
		lr.Suspect = append(lr.Suspect, ReasonModifiesLock)
	}
	if l.BaseRef != in.BaseBranch {
		lr.Suspect = append(lr.Suspect, ReasonRetargeted)
	}
	if repoWide(l) {
		lr.Suspect = append(lr.Suspect, ReasonRepoWide)
	}
	sort.Strings(lr.Suspect)
	for _, c := range l.Criteria {
		fp := append([]string{}, c.Footprint...)
		lr.Criteria = append(lr.Criteria, Criterion{ID: c.ID, Text: c.Text, Footprint: fp})
	}
	return lr, l, true
}

// repoWide reports whether any criterion's footprint holds a repo-wide glob (REQ-0046). The
// footprint already includes globs added by amendments.
func repoWide(l lock.Lock) bool {
	for _, c := range l.Criteria {
		for _, g := range c.Footprint {
			if footprint.RepoWide(g) {
				return true
			}
		}
	}
	return false
}

// inputProblems lists what is wrong with the input as a whole, sorted, once each: two lock file
// entries for one path, and a changed path that is absolute or has an empty, "." or ".." segment.
func inputProblems(in Input) []string {
	seen := make(map[string]bool, len(in.LockFiles))
	var problems []string
	for _, f := range in.LockFiles {
		if seen[f.Path] {
			problems = append(problems, "duplicate lock file entry "+f.Path)
		}
		seen[f.Path] = true
	}
	for _, c := range in.Changed {
		for _, p := range []string{c.Path, c.OldPath} {
			if p != c.Path && p == "" { // an empty OldPath only says "not a rename"
				continue
			}
			if !safePath(p) {
				problems = append(problems, fmt.Sprintf("unsafe changed path %q", p))
			}
		}
	}
	sort.Strings(problems)
	return dedupe(problems)
}

// safePath reports whether p is a repo-relative path made of non-empty segments other than "."
// and "..".
func safePath(p string) bool {
	if p == "" || strings.HasPrefix(p, "/") {
		return false
	}
	for _, seg := range strings.Split(p, "/") {
		if seg == "" || seg == "." || seg == ".." {
			return false
		}
	}
	return true
}

// dedupe drops repeats from a sorted list.
func dedupe(ss []string) []string {
	out := ss[:0]
	for i, s := range ss {
		if i == 0 || s != ss[i-1] {
			out = append(out, s)
		}
	}
	return out
}

// lockFileLess is a total order over lock files: every field breaks a tie.
func lockFileLess(a, b LockFile) bool {
	switch {
	case a.Path != b.Path:
		return a.Path < b.Path
	case a.Status != b.Status:
		return a.Status < b.Status
	case a.OldPath != b.OldPath:
		return a.OldPath < b.OldPath
	}
	return bytes.Compare(a.Data, b.Data) < 0
}

func contains(set []string, s string) bool {
	for _, v := range set {
		if v == s {
			return true
		}
	}
	return false
}
