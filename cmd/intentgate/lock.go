package main

import (
	"bufio"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/autox-my/intent-gate/internal/audit"
	"github.com/autox-my/intent-gate/internal/ids"
	"github.com/autox-my/intent-gate/internal/lock"
	"github.com/autox-my/intent-gate/internal/planreview"
	"github.com/autox-my/intent-gate/internal/ratify"
)

// The ratification commands (ratification.md §3, footprint-check.md §5): `lock --from-plan` and
// `amend`. Both write only after a human types yes at a terminal (REQ-0005): there is no --yes flag
// and no environment variable that skips it.
//
// Exit codes (core-contracts.md §3): 0 written; 1 not confirmed, or a drafting error; 2 usage, or
// no terminal; 3 the scan failed, git can't resolve the base, or the lock to amend is invalid.

const (
	exitOK           = 0
	exitNotConfirmed = 1
	exitUsage        = 2
	exitUnverifiable = 3

	lockUsage = "usage: intentgate lock --from-plan <plan.md> [--prompt <file>]\n" +
		"       intentgate lock --issue <n> [--repo <owner/name>]\n"

	amendUsage = "usage: intentgate amend <id> (--criterion <Cn> --add <glob> | --reratify)\n"

	timeLayout = "2006-01-02T15:04:05Z"
)

// term is a command's view of the process: its streams, its dependencies and its stderr.
type term struct {
	cmd    string
	in     *bufio.Reader
	out    io.Writer
	deps   Deps
	errOut io.Writer
}

func newTerm(cmd string, stdin io.Reader, stdout io.Writer, deps Deps) *term {
	e := deps.Stderr
	if e == nil {
		e = io.Discard
	}
	return &term{cmd: cmd, in: bufio.NewReader(stdin), out: stdout, deps: deps, errOut: e}
}

func (t *term) fail(code int, format string, args ...any) int {
	fmt.Fprintf(t.errOut, format+"\n", args...)
	return code
}

func (t *term) usage(text string, why string) int {
	if why != "" {
		fmt.Fprintf(t.errOut, "intentgate %s: %s\n", t.cmd, why)
	}
	_, _ = io.WriteString(t.errOut, text)
	return exitUsage
}

// requireTerminal refuses unless stdin and stdout are both terminals (REQ-0005).
func (t *term) requireTerminal() bool {
	if f := t.deps.IsTerminal; f != nil && f(0) && f(1) {
		return true
	}
	fmt.Fprintf(t.errOut, "intentgate %s needs an interactive terminal; ratification is a human act\n", t.cmd)
	return false
}

// confirm prints prompt, reads one line and reports whether it is `yes` (after trimming spaces and
// the line ending), with the time the prompt was shown and the time the line was read.
func (t *term) confirm(prompt string) (yes bool, shown, answered time.Time) {
	fmt.Fprint(t.out, prompt+" ")
	shown = t.deps.Now()
	line, err := t.in.ReadString('\n')
	answered = t.deps.Now()
	if err != nil && line == "" {
		return false, shown, answered
	}
	return strings.TrimSpace(line) == "yes", shown, answered
}

// reviewMS is the review duration: shown to answered, never negative.
func reviewMS(shown, answered time.Time) int {
	if ms := answered.Sub(shown).Milliseconds(); ms > 0 {
		return int(ms)
	}
	return 0
}

func utcSeconds(t time.Time) string { return t.UTC().Truncate(time.Second).Format(timeLayout) }

// resolve makes p absolute against dir.
func resolve(dir, p string) string {
	if filepath.IsAbs(p) || dir == "" {
		return p
	}
	return filepath.Join(dir, p)
}

func newFlags(name string) *flag.FlagSet {
	fs := flag.NewFlagSet(name, flag.ContinueOnError)
	fs.SetOutput(io.Discard)
	return fs
}

// ---- lock ----

func runLock(args []string, stdin io.Reader, stdout io.Writer, deps Deps) int {
	t := newTerm("lock", stdin, stdout, deps)
	fs := newFlags("lock")
	planPath := fs.String("from-plan", "", "")
	promptPath := fs.String("prompt", "", "")
	issueNum := fs.Int("issue", 0, "")
	repoFlag := fs.String("repo", "", "")
	if err := fs.Parse(args); err != nil || fs.NArg() > 0 {
		return t.usage(lockUsage, "")
	}
	set := map[string]bool{}
	fs.Visit(func(f *flag.Flag) { set[f.Name] = true })
	switch {
	case set["issue"] && !set["from-plan"] && !set["prompt"] && *issueNum > 0 && (!set["repo"] || validRepo(*repoFlag)):
		return runLockIssue(t, *issueNum, *repoFlag)
	case set["from-plan"] && !set["issue"] && !set["repo"] && *planPath != "" && (!set["prompt"] || *promptPath != ""):
		return runLockPlan(t, *planPath, *promptPath, set["prompt"])
	}
	return t.usage(lockUsage, "")
}

func runLockPlan(t *term, planPath, promptPath string, hasPrompt bool) int {
	deps := t.deps
	if !t.requireTerminal() {
		return exitUsage
	}

	plan, err := os.ReadFile(resolve(deps.Dir, planPath))
	if err != nil {
		return t.fail(exitNotConfirmed, "no lock: cannot read the plan: %v", err)
	}
	criteria, maskedPlan, err := draftAndMask(string(plan))
	if err != nil {
		return t.fail(draftCode(err), "%v", err)
	}

	var rootIntent string
	if hasPrompt {
		data, err := os.ReadFile(resolve(deps.Dir, promptPath))
		if err != nil {
			return t.fail(exitNotConfirmed, "no lock: cannot read the root intent: %v", err)
		}
		rootIntent = string(data)
	} else if rootIntent, err = t.pasteRootIntent(); err != nil {
		return t.fail(exitNotConfirmed, "no lock: %v", err)
	}
	if strings.TrimSpace(rootIntent) == "" {
		return t.fail(exitNotConfirmed, "no lock: the root intent is empty")
	}

	top, email, baseRef, err := gitFacts(deps)
	if err != nil {
		return t.fail(exitUnverifiable, "intentgate lock: %v", err)
	}
	return t.confirmAndWrite(pending{
		Top: top, BaseRef: baseRef, Criteria: criteria, RootIntent: rootIntent, Plan: maskedPlan,
		Source: "plan-file", Method: "cli-confirm", Ratifier: "git:" + email,
	})
}

// errScan is a failed secret scan; a drafting error is anything else.
var errScan = errors.New("secret scan failed, nothing written")

// draftMasked drafts criteria from a plan after the plan has been through the secret scan
// (ratification.md §5): criterion text and footprints are committed in the lock and printed, so a
// secret in the plan must never reach them.
func draftMasked(plan string) ([]lock.Criterion, error) {
	criteria, _, err := draftAndMask(plan)
	return criteria, err
}

// draftAndMask is draftMasked that also returns the masked plan, so the plan review reuses it:
// the plan is scanned once.
func draftAndMask(plan string) ([]lock.Criterion, string, error) {
	m, _, err := ratify.Mask(plan)
	if err != nil {
		return nil, "", fmt.Errorf("%w: %v", errScan, err)
	}
	criteria, err := ratify.DraftCriteria(m.String())
	return criteria, m.String(), err
}

// draftCode is the exit code for an error from draftMasked: 3 for a failed scan, else 1.
func draftCode(err error) int {
	if errors.Is(err, errScan) {
		return exitUnverifiable
	}
	return exitNotConfirmed
}

// pending is a lock ready to be shown to the human: everything but the confirmation.
type pending struct {
	Top, BaseRef string
	Criteria     []lock.Criterion
	RootIntent   string
	Plan         string // the masked text the criteria were drafted from; what the plan review compares with RootIntent
	Source       string // root_intent.source
	Method       string // ratification.method
	Ratifier     string
	Intro        string // printed before the criteria, if not empty
}

// confirmAndWrite is the step the lock paths share (ratification.md §1, steps 5-7): show the
// criteria, ask for `yes`, then mask, build and write. Nothing is written before the yes.
func (t *term) confirmAndWrite(p pending) int {
	deps := t.deps
	if p.Intro != "" {
		fmt.Fprintln(t.out, p.Intro)
	}
	fmt.Fprintf(t.out, "Criteria to lock (base %s):\n", p.BaseRef)
	showCriteria(t.out, p.Criteria)
	// The advisory plan review runs here, before the prompt, within its budget; whatever it
	// does, the lock goes on (REQ-0020). review_duration_ms starts at the prompt, after it.
	res := reviewStep(deps, p.Plan, func() (string, error) { return p.RootIntent, nil })
	fmt.Fprint(t.out, planreview.Display(res))
	yes, shown, answered := t.confirm("Type yes to lock these criteria:")
	if !yes {
		return t.fail(exitNotConfirmed, "intentgate lock: not confirmed; nothing written")
	}

	id, err := ids.NewULID(answered, deps.Rand)
	if err != nil {
		return t.fail(exitUnverifiable, "intentgate lock: %v", err)
	}
	l, masked, err := ratify.MaskAndBuild(nil, p.RootIntent, ratify.BuildInput{
		ID:               id,
		BaseRef:          p.BaseRef,
		Source:           p.Source,
		Criteria:         p.Criteria,
		Ratifier:         p.Ratifier,
		RatifiedAt:       answered,
		Method:           p.Method,
		ReviewDurationMS: reviewMS(shown, answered),
		PlanReview:       planReviewRecord(res),
	})
	if err != nil {
		code := exitNotConfirmed
		if strings.Contains(err.Error(), "secret scan failed") {
			code = exitUnverifiable
		}
		return t.fail(code, "intentgate lock: %v", err)
	}
	if err := (ratify.Store{RepoRoot: p.Top, StateDir: deps.StateDir}).Write(l, masked); err != nil {
		return t.fail(exitUnverifiable, "intentgate lock: %v", err)
	}
	auditLockWritten(deps, audit.EventLock, l, answered, p.Top)
	fmt.Fprintf(t.out, "intent lock written: .intent/%s.yml\n", l.ID)
	return exitOK
}

// pasteRootIntent reads the root intent from the terminal, up to a line with only a dot.
func (t *term) pasteRootIntent() (string, error) {
	fmt.Fprintln(t.out, "Paste the root intent (the request the plan answers), then a line with only `.`:")
	var b strings.Builder
	for {
		line, err := t.in.ReadString('\n')
		if err != nil && line == "" {
			return "", errors.New("the root intent was not ended by a line with only `.`")
		}
		line = strings.TrimRight(line, "\r\n")
		if strings.TrimSpace(line) == "." {
			return b.String(), nil
		}
		b.WriteString(line)
		b.WriteByte('\n') // each pasted line ends in a newline, like the same lines in a --prompt file
	}
}

// criteriaAsPlan renders drafted criteria as the plan text for `lock --issue`, which has no plan
// file: the criterion texts only, one per line, without ids or footprints. The texts come from
// masked drafting, so the result is masked.
func criteriaAsPlan(criteria []lock.Criterion) string {
	var b strings.Builder
	for _, c := range criteria {
		b.WriteString(c.Text)
		b.WriteByte('\n')
	}
	return b.String()
}

func showCriteria(w io.Writer, criteria []lock.Criterion) {
	for _, c := range criteria {
		fmt.Fprintf(w, "  %s: %s\n      footprint: %s\n", c.ID, c.Text, strings.Join(c.Footprint, ", "))
	}
}

// gitFacts resolves the repository root, the ratifier's email and the base ref from deps.Dir.
func gitFacts(deps Deps) (top, email, baseRef string, err error) {
	if top, err = deps.Git.TopLevel(deps.Dir); err != nil {
		return
	}
	if email, err = deps.Git.UserEmail(deps.Dir); err != nil {
		return
	}
	baseRef, err = deps.Git.BaseRef(deps.Dir)
	return
}

// ---- amend ----

func runAmend(args []string, stdin io.Reader, stdout io.Writer, deps Deps) int {
	t := newTerm("amend", stdin, stdout, deps)
	if len(args) == 0 || strings.HasPrefix(args[0], "-") {
		return t.usage(amendUsage, "")
	}
	id := args[0]
	fs := newFlags("amend")
	criterion := fs.String("criterion", "", "")
	glob := fs.String("add", "", "")
	reratify := fs.Bool("reratify", false, "")
	if err := fs.Parse(args[1:]); err != nil || fs.NArg() > 0 {
		return t.usage(amendUsage, "")
	}
	set := map[string]bool{}
	fs.Visit(func(f *flag.Flag) { set[f.Name] = true })
	switch {
	case *reratify && (set["add"] || set["criterion"]), !*reratify && !(set["add"] && set["criterion"]):
		return t.usage(amendUsage, "give --criterion with --add, or --reratify, and nothing else")
	case !lock.ValidID(id):
		return t.usage(amendUsage, fmt.Sprintf("%q is not a lock id (give the full 26-character id)", id))
	}
	if set["add"] {
		if err := lock.CheckGlob(*glob); err != nil {
			return t.usage(amendUsage, err.Error())
		}
	}
	if !t.requireTerminal() {
		return exitUsage
	}

	top, err := deps.Git.TopLevel(deps.Dir)
	if err != nil {
		return t.fail(exitUnverifiable, "intentgate amend: %v", err)
	}
	path := filepath.Join(top, ".intent", id+".yml")
	data, err := os.ReadFile(path)
	if err != nil {
		return t.fail(exitUnverifiable, "intentgate amend: cannot read the lock: %v", err)
	}
	old, err := lock.Parse(data)
	if err != nil {
		return t.fail(exitUnverifiable, "intentgate amend: %s: %v", path, err)
	}
	if ps := lock.Validate(old); len(ps) > 0 {
		return t.fail(exitUnverifiable, "intentgate amend: %s is not a valid lock: %s", path, ps[0])
	}
	if !lock.VerifyHash(old) {
		return t.fail(exitUnverifiable, "intentgate amend: %s fails its hash; it was edited or is corrupt", path)
	}
	if old.ID != id {
		return t.fail(exitUnverifiable, "intentgate amend: %s holds lock %s, not %s", path, old.ID, id)
	}

	var ci int // the criterion to extend (add-glob only)
	if set["add"] {
		ci = -1
		for i, c := range old.Criteria {
			if c.ID == *criterion {
				ci = i
			}
		}
		if ci < 0 {
			return t.fail(exitUsage, "intentgate amend: lock %s has no criterion %q", id, *criterion)
		}
		for _, g := range old.Criteria[ci].Footprint {
			if g == *glob {
				return t.fail(exitUsage, "intentgate amend: %s already has %q in its footprint", *criterion, *glob)
			}
		}
	}
	email, err := deps.Git.UserEmail(deps.Dir)
	if err != nil {
		return t.fail(exitUnverifiable, "intentgate amend: %v", err)
	}
	by := "git:" + email

	next := old
	next.Revision = old.Revision + 1
	next.Criteria = make([]lock.Criterion, len(old.Criteria))
	for i, c := range old.Criteria {
		c.Footprint = append([]string(nil), c.Footprint...)
		next.Criteria[i] = c
	}
	next.Amendments = append([]lock.Amendment(nil), old.Amendments...)

	var prompt string
	fmt.Fprintf(t.out, "Lock %s, revision %d -> %d:\n", id, old.Revision, next.Revision)
	if set["add"] {
		c := &next.Criteria[ci]
		c.Footprint = append(c.Footprint, *glob)
		fmt.Fprintf(t.out, "  add to %s (%s): %s\n", c.ID, c.Text, *glob)
		prompt = "Type yes to amend this lock:"
	} else {
		fmt.Fprintf(t.out, "  re-ratify as %s: ratified_at was %s; the new ratified_at is the time of confirmation; the criteria stay:\n", by, old.Ratification.RatifiedAt)
		showCriteria(t.out, old.Criteria)
		prompt = "Type yes to re-ratify this lock:"
	}
	yes, shown, answered := t.confirm(prompt)
	if !yes {
		return t.fail(exitNotConfirmed, "intentgate amend: not confirmed; nothing written")
	}

	am := lock.Amendment{Revision: next.Revision, By: by, At: utcSeconds(answered), Method: "cli-confirm"}
	if set["add"] {
		am.Kind, am.Criterion, am.Glob = lock.KindAddGlob, *criterion, *glob
	} else {
		am.Kind = lock.KindReratify
		ms := reviewMS(shown, answered)
		next.Ratification = lock.Ratification{Ratifier: by, RatifiedAt: am.At, Method: "cli-confirm", ReviewDurationMS: &ms}
	}
	next.Amendments = append(next.Amendments, am)
	if next.Hash, err = lock.Hash(next); err != nil {
		return t.fail(exitUnverifiable, "intentgate amend: %v", err)
	}
	if err := (ratify.Store{RepoRoot: top, StateDir: deps.StateDir}).Amend(old, next); err != nil {
		return t.fail(exitUnverifiable, "intentgate amend: %v", err)
	}
	auditLockWritten(deps, audit.EventAmend, next, answered, top)
	fmt.Fprintf(t.out, "intent lock amended: .intent/%s.yml (revision %d)\n", id, next.Revision)
	return exitOK
}
