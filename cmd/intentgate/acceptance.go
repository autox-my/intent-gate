package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/autox-my/intent-gate/internal/codeowners"
	"github.com/autox-my/intent-gate/internal/forge/github"
	"github.com/autox-my/intent-gate/internal/gitx"
	"github.com/autox-my/intent-gate/internal/lock"
)

// `acceptance` says, for each lock of the pull request whose root intent is a committed intent,
// whether that intent was accepted: it reached the base branch through a pull request approved by
// a code owner of the file (pr-check.md §7, ADR 0006, REQ-0045). It reads verify.json and git, asks
// GitHub's review API, and writes acceptance.json next to them. It is a report: no status is an
// error, and verify.json is never touched.
//
// Exit codes: 0 acceptance.json was written, whatever it says; 2 usage or no GITHUB_TOKEN; 3 it
// cannot be written: no verify.json, or git cannot resolve the base or the head.

const acceptanceUsage = "usage: intentgate acceptance --out-dir <dir> --repo <owner/name> --base <sha> --head <sha>\n"

const (
	acceptanceSchema = "intentgate.acceptance/1"
	acceptanceFile   = "acceptance.json"

	statusAccepted    = "accepted"
	statusNotAccepted = "not-accepted"
	statusUnknown     = "unknown"
)

// acceptanceDoc is acceptance.json; the field order is the key order.
type acceptanceDoc struct {
	Schema  string             `json:"schema"`
	Intents []acceptanceIntent `json:"intents"`
}

type acceptanceIntent struct {
	LockID string `json:"lock_id"`
	Path   string `json:"path"`
	Blob   string `json:"blob"`
	Status string `json:"status"`
	Reason string `json:"reason"`
	PR     int    `json:"pr"`
}

func runAcceptance(args []string, stdout io.Writer, deps Deps) int {
	t := newTerm("acceptance", nil, stdout, deps)
	fs := newFlags("acceptance")
	outDir := fs.String("out-dir", "", "")
	repoFlag := fs.String("repo", "", "")
	base := fs.String("base", "", "")
	head := fs.String("head", "", "")
	if err := fs.Parse(args); err != nil || fs.NArg() > 0 {
		return t.usage(acceptanceUsage, "")
	}
	owner, name, ok := splitRepo(*repoFlag)
	switch {
	case *outDir == "":
		return t.usage(acceptanceUsage, "--out-dir is required")
	case !ok || !validRepo(*repoFlag):
		return t.usage(acceptanceUsage, "--repo must be owner/name")
	case *base == "" || *head == "":
		return t.usage(acceptanceUsage, "--base and --head are required")
	}
	token := strings.TrimSpace(os.Getenv("GITHUB_TOKEN"))
	if token == "" {
		return t.fail(exitUsage, "intentgate acceptance needs a GitHub token in GITHUB_TOKEN")
	}
	dir := resolve(deps.Dir, *outDir)

	lockPaths, err := verifyLockPaths(dir)
	if err != nil {
		return t.fail(exitUnverifiable, "intentgate acceptance: %v", err)
	}
	baseSHA, err := gitx.Resolve(deps.Dir, *base)
	if err != nil {
		return t.fail(exitUnverifiable, "intentgate acceptance: %v", err)
	}
	headSHA, err := gitx.Resolve(deps.Dir, *head)
	if err != nil {
		return t.fail(exitUnverifiable, "intentgate acceptance: %v", err)
	}

	api := os.Getenv("GITHUB_API_URL")
	if api == "" {
		api = defaultAPIURL
	}
	j := &judge{
		ctx: context.Background(), client: &github.Client{BaseURL: api, Token: token}, dir: deps.Dir,
		owner: owner, repo: name, base: baseSHA, errOut: t.errOut,
		reviews: map[int]reviewsResult{},
	}
	doc := acceptanceDoc{Schema: acceptanceSchema, Intents: []acceptanceIntent{}}
	for _, p := range lockPaths {
		l, ok := committedLockAt(deps.Dir, headSHA, p)
		if !ok {
			continue
		}
		doc.Intents = append(doc.Intents, j.judge(l))
	}
	sort.SliceStable(doc.Intents, func(a, b int) bool { return doc.Intents[a].LockID < doc.Intents[b].LockID })

	var buf bytes.Buffer
	enc := json.NewEncoder(&buf)
	enc.SetEscapeHTML(false)
	enc.SetIndent("", "  ")
	if err := enc.Encode(doc); err != nil {
		return t.fail(exitUnverifiable, "intentgate acceptance: %v", err)
	}
	if err := os.WriteFile(filepath.Join(dir, acceptanceFile), buf.Bytes(), 0o644); err != nil {
		return t.fail(exitUnverifiable, "intentgate acceptance: cannot write %s: %v", acceptanceFile, errors.Unwrap(err))
	}
	return exitOK
}

// verifyLockPaths reads the lock paths listed in verify.json.
func verifyLockPaths(dir string) ([]string, error) {
	data, err := os.ReadFile(filepath.Join(dir, "verify.json"))
	if err != nil {
		return nil, fmt.Errorf("cannot read verify.json: %w", errors.Unwrap(err))
	}
	var doc struct {
		Locks []struct {
			Path string `json:"path"`
		} `json:"locks"`
	}
	if err := json.Unmarshal(data, &doc); err != nil {
		return nil, fmt.Errorf("verify.json is not valid: %v", err)
	}
	var paths []string
	for _, l := range doc.Locks {
		paths = append(paths, l.Path)
	}
	return paths, nil
}

// committedLockAt reads the lock at path in head and returns it when it validates, sits at
// .intent/<id>.yml and its root intent is a committed intent. Any other lock is not judged here:
// verify.json already reports a lock that is deleted or does not validate.
func committedLockAt(dir, head, path string) (lock.Lock, bool) {
	if !isLockPath(path) {
		return lock.Lock{}, false
	}
	data, found, err := gitx.ShowFile(dir, head, path)
	if err != nil || !found {
		return lock.Lock{}, false
	}
	l, err := lock.Parse(data)
	if err != nil || len(lock.Validate(l)) > 0 || path != lockDirPrefix+l.ID+".yml" {
		return lock.Lock{}, false
	}
	if l.RootIntent.Source != lock.SourceCommittedIntent {
		return lock.Lock{}, false
	}
	return l, true
}

type reviewsResult struct {
	approvers map[string]bool // lower-case logins
	err       error
}

// judge applies the six rules of pr-check.md §7 to one committed intent, in order.
type judge struct {
	ctx         context.Context
	client      *github.Client
	dir         string
	owner, repo string
	base        string
	errOut      io.Writer

	reviews map[int]reviewsResult
	owners  *codeowners.File // parsed once; tried says whether the lookup ran
	ownErr  error
	tried   bool
}

func (j *judge) judge(l lock.Lock) acceptanceIntent {
	path := l.RootIntent.Path
	out := acceptanceIntent{LockID: l.ID, Path: path, Blob: l.RootIntent.Blob}
	set := func(status, reason string) acceptanceIntent {
		out.Status, out.Reason = status, reason
		return out
	}

	// 1. this version of the intent is on the base branch
	blob, found, err := gitx.BlobAt(j.dir, j.base, path)
	if err != nil {
		j.note(l.ID, err)
		return set(statusUnknown, "cannot read "+path+" at the base branch")
	}
	if !found || blob != l.RootIntent.Blob {
		return set(statusNotAccepted, "this version of the intent is not on the base branch")
	}

	// 2. the commit that last changed it on the base branch
	sha, err := gitx.LastCommit(j.dir, j.base, path)
	if err != nil || sha == "" {
		if err != nil {
			j.note(l.ID, err)
		}
		return set(statusUnknown, "cannot find the commit that last changed "+path+" on the base branch")
	}

	// 3. its pull request: the lowest-numbered merged one
	pulls, err := j.client.CommitPulls(j.ctx, j.owner, j.repo, sha)
	if err != nil {
		j.note(l.ID, err)
		return set(statusUnknown, askFailed("the pull requests of the commit that last changed the intent", err))
	}
	pr := 0
	for _, p := range pulls {
		if p.Merged && (pr == 0 || p.Number < pr) {
			pr = p.Number
		}
	}
	if pr == 0 {
		return set(statusNotAccepted, "reached the base branch without a pull request")
	}
	out.PR = pr

	// 4. its approvals
	rv := j.approvalsOf(pr)
	if rv.err != nil {
		j.note(l.ID, rv.err)
		return set(statusUnknown, askFailed(fmt.Sprintf("the reviews of #%d", pr), rv.err))
	}

	// 5. the code owners of the path, as of the base branch
	owners, err := j.ownersOf(path)
	if err != nil {
		j.note(l.ID, err)
		return set(statusUnknown, "cannot read CODEOWNERS at the base branch")
	}
	if len(owners) == 0 {
		return set(statusUnknown, "no code owner for "+path)
	}

	// 6. an approver is an owner
	for _, o := range owners {
		if o.Kind == codeowners.User && rv.approvers[strings.ToLower(o.Name)] {
			return set(statusAccepted, fmt.Sprintf("approved in #%d by its code owner @%s", pr, o.Name))
		}
	}
	for _, o := range owners {
		if o.Kind != codeowners.User {
			return set(statusUnknown, "owned by a team or an email; checking membership needs more than the Action's token")
		}
	}
	return set(statusNotAccepted, "approved by none of its code owners")
}

// approvalsOf is the set of reviewers whose latest APPROVED, CHANGES_REQUESTED or DISMISSED review
// of pull request n is APPROVED. A comment or a pending review changes nothing.
func (j *judge) approvalsOf(n int) reviewsResult {
	if r, ok := j.reviews[n]; ok {
		return r
	}
	res := reviewsResult{approvers: map[string]bool{}}
	revs, err := j.client.PullReviews(j.ctx, j.owner, j.repo, n)
	if err != nil {
		res.err = err
	}
	for _, r := range revs {
		if r.Login == "" {
			continue
		}
		switch r.State {
		case "APPROVED":
			res.approvers[strings.ToLower(r.Login)] = true
		case "CHANGES_REQUESTED", "DISMISSED":
			res.approvers[strings.ToLower(r.Login)] = false
		}
	}
	j.reviews[n] = res
	return res
}

// ownersOf reads the first of codeowners.Locations that exists at the base branch, once.
func (j *judge) ownersOf(path string) ([]codeowners.Owner, error) {
	if !j.tried {
		j.tried = true
		for _, loc := range codeowners.Locations {
			data, found, err := gitx.ShowFile(j.dir, j.base, loc)
			if err != nil {
				j.ownErr = err
				break
			}
			if found {
				j.owners = codeowners.Parse(data)
				break
			}
		}
	}
	if j.ownErr != nil {
		return nil, j.ownErr
	}
	if j.owners == nil {
		return nil, nil
	}
	return j.owners.Owners(path), nil
}

// note prints a diagnostic to stderr; the error carries no token.
func (j *judge) note(lockID string, err error) {
	fmt.Fprintf(j.errOut, "intentgate acceptance: %s: %v\n", lockID, err)
}

// askFailed is the reason of an unknown caused by the API: the status when there is one.
func askFailed(what string, err error) string {
	var se *github.StatusError
	if errors.As(err, &se) {
		return fmt.Sprintf("could not ask GitHub about %s: it answered %d", what, se.Status)
	}
	return fmt.Sprintf("could not ask GitHub about %s: the request failed", what)
}
