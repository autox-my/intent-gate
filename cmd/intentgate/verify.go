package main

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"strings"

	"intentgate/internal/audit"
	"intentgate/internal/footprint"
	"intentgate/internal/gitx"
	"intentgate/internal/lock"
	"intentgate/internal/policy"
	"intentgate/internal/report"
	"intentgate/internal/verify"
)

// `verify` runs the PR check locally or in CI (pr-check.md §1-§2): it reads the inputs from git at
// the head revision, runs the verify core and prints or writes its outputs. It has no flag for the
// pull request's title, description or commits (REQ-0032).
//
// Exit codes: the core's 0, 1 or 3; 2 for usage; 3 when git fails or an output cannot be written.

const verifyUsage = "usage: intentgate verify --base <rev> --head <rev> --base-branch <name> [--author <login>]\n" +
	"                         [--author-type <type>] [--format text|json|sarif|summary|comment] [--out-dir <dir>]\n" +
	"                         [--repo <owner/name>] [--pr <n>]\n" +
	"       intentgate verify --replay <event.json>\n"

const (
	lockDirPrefix  = ".intent/"
	configFile     = ".intentgate.yml"
	attributesFile = ".gitattributes"
)

func runVerify(args []string, stdout io.Writer, deps Deps) int {
	t := newTerm("verify", nil, stdout, deps)
	fs := newFlags("verify")
	base := fs.String("base", "", "")
	head := fs.String("head", "", "")
	baseBranch := fs.String("base-branch", "", "")
	author := fs.String("author", "", "")
	authorType := fs.String("author-type", "", "")
	format := fs.String("format", "text", "")
	outDir := fs.String("out-dir", "", "")
	repoFlag := fs.String("repo", "", "")
	pr := fs.Int("pr", 0, "")
	replayPath := fs.String("replay", "", "")
	if err := fs.Parse(args); err != nil || fs.NArg() > 0 {
		return t.usage(verifyUsage, "")
	}
	set := map[string]bool{}
	fs.Visit(func(f *flag.Flag) { set[f.Name] = true })
	if set["replay"] {
		if len(set) > 1 || *replayPath == "" {
			return t.usage(verifyUsage, "--replay takes an event file and no other flag")
		}
		return runReplay(t, *replayPath, deps)
	}
	switch *format {
	case "text", "json", "sarif", "summary", "comment":
	default:
		return t.usage(verifyUsage, fmt.Sprintf("unknown --format %q", *format))
	}
	if *base == "" || *head == "" || *baseBranch == "" || (set["out-dir"] && *outDir == "") {
		return t.usage(verifyUsage, "--base, --head and --base-branch are required")
	}
	if *pr < 0 || (set["repo"] && !validRepo(*repoFlag)) {
		return t.usage(verifyUsage, "--repo is owner/name and --pr a pull request number")
	}

	in, configFound, err := readVerifyInput(deps.Dir, *base, *head, *baseBranch, *author, *authorType)
	if err != nil {
		return t.fail(exitUnverifiable, "intentgate verify: %v", err)
	}
	res := verify.Run(in)
	dec := policy.Decide(res)

	outputs := map[string][]byte{
		"text":    []byte(report.Text(res)),
		"json":    report.JSON(res),
		"sarif":   report.SARIF(res, version),
		"summary": []byte(report.CheckSummary(res, dec)),
		"comment": []byte(report.Comment(res, dec)),
	}

	// The audit event is the one thing here that reads the clock; no other output depends on it.
	localLog := !inCI() && deps.StateDir != ""
	var auditJSON []byte
	var event audit.Event
	if *outDir != "" || localLog {
		baseSHA, err := gitx.Resolve(deps.Dir, *base)
		if err != nil {
			return t.fail(exitUnverifiable, "intentgate verify: %v", err)
		}
		headSHA, err := gitx.Resolve(deps.Dir, *head)
		if err != nil {
			return t.fail(exitUnverifiable, "intentgate verify: %v", err)
		}
		locks, unverifiable := auditLocks(in)
		configSHA := "" // an absent config has no hash; an empty one has the hash of nothing
		if configFound {
			configSHA = sha256Hex(in.Config)
		}
		event = audit.NewVerifyEvent(deps.Now(), version, *repoFlag, *pr, audit.Verify{
			BaseSHA: baseSHA, HeadSHA: headSHA, BaseBranch: *baseBranch, Author: *author, AuthorType: *authorType,
			ConfigSHA256: configSHA, VerifySHA256: sha256Hex(outputs["json"]),
			Locks: locks, UnverifiableLocks: unverifiable, Outcome: string(res.Outcome), ExitCode: res.ExitCode, Conclusion: dec.Conclusion,
		})
		auditJSON = audit.JSON(event)
	}
	if *outDir != "" {
		if err := writeOutDir(resolve(deps.Dir, *outDir), outputs, dec, auditJSON); err != nil {
			return t.fail(exitUnverifiable, "intentgate verify: %v", err)
		}
	}
	if localLog {
		appendAudit(deps.StateDir, event, t.errOut)
	}
	_, _ = stdout.Write(outputs[*format])
	return res.ExitCode
}

func sha256Hex(b []byte) string {
	sum := sha256.Sum256(b)
	return hex.EncodeToString(sum[:])
}

// auditLocks are the locks of the pull request that the event may list: the lock files at head that
// are not deleted, parse, validate and sit at .intent/<id>.yml. Every other changed lock file is
// only counted, so the event never quotes an id, hash or method from a file that did not validate;
// verify.json reports those files.
func auditLocks(in verify.Input) (locks []lock.Lock, unverifiable int) {
	for _, f := range in.LockFiles {
		if f.Status == footprint.Deleted || f.Data == nil {
			unverifiable++
			continue
		}
		l, err := lock.Parse(f.Data)
		if err != nil || len(lock.Validate(l)) > 0 || f.Path != lockDirPrefix+l.ID+".yml" {
			unverifiable++
			continue
		}
		locks = append(locks, l)
	}
	return locks, unverifiable
}

// runReplay re-runs the decision recorded in a verify audit event (core-contracts.md §1): the same
// refs, branch and author in the current repository, and compares the SHA-256 of the fresh
// verify.json with the event's. It reads no clock and writes no audit event.
//
// Exit codes: 0 identical; 1 differs (the fresh verify.json is on stdout); 3 the event cannot be
// read, or a ref in it is not in this repository.
func runReplay(t *term, path string, deps Deps) int {
	data, err := os.ReadFile(resolve(deps.Dir, path))
	if err != nil {
		return t.fail(exitUnverifiable, "intentgate verify: cannot read the event: %v", err)
	}
	var ev audit.Event
	if err := json.Unmarshal(data, &ev); err != nil {
		return t.fail(exitUnverifiable, "intentgate verify: %s is not an audit event: %v", path, err)
	}
	if ev.Schema != audit.SchemaID || ev.Event != audit.EventVerify || ev.BaseSHA == "" || ev.HeadSHA == "" || ev.BaseBranch == "" || ev.VerifySHA256 == "" {
		return t.fail(exitUnverifiable, "intentgate verify: %s is not a %s verify event with its refs and verify_sha256", path, audit.SchemaID)
	}
	for _, sha := range []string{ev.BaseSHA, ev.HeadSHA} {
		if err := pinnedCommit(deps.Dir, sha); err != nil {
			return t.fail(exitUnverifiable, "intentgate verify: %s: %v", path, err)
		}
	}
	in, _, err := readVerifyInput(deps.Dir, ev.BaseSHA, ev.HeadSHA, ev.BaseBranch, ev.Author, ev.AuthorType)
	if err != nil {
		return t.fail(exitUnverifiable, "intentgate verify: %v", err)
	}
	fresh := report.JSON(verify.Run(in))
	if sha256Hex(fresh) == ev.VerifySHA256 {
		fmt.Fprintln(t.out, "replay: identical")
		return exitOK
	}
	fmt.Fprintln(t.errOut, "replay: differs")
	_, _ = t.out.Write(fresh)
	return 1
}

var fullSHA = regexp.MustCompile(`^(?:[0-9a-f]{40}|[0-9a-f]{64})$`)

// pinnedCommit checks that sha is a full lower-case object name that resolves to itself as a
// commit. A replay is only a replay of the recorded decision when the event pins the exact commits:
// a branch name, a short sha, a tag or an expression would resolve to whatever they name today.
func pinnedCommit(dir, sha string) error {
	if !fullSHA.MatchString(sha) {
		return fmt.Errorf("%q is not a full commit sha", sha)
	}
	got, err := gitx.Resolve(dir, sha)
	if err != nil {
		return err
	}
	if got != sha {
		return fmt.Errorf("%s does not resolve to itself as a commit", sha)
	}
	return nil
}

// readVerifyInput reads everything the check looks at, at head, through git (verify.Input). It also
// reports whether .intentgate.yml exists at head, which verify.Input does not tell apart from empty.
func readVerifyInput(dir, base, head, baseBranch, author, authorType string) (in verify.Input, configFound bool, err error) {
	in = verify.Input{BaseBranch: baseBranch, Author: author, AuthorType: authorType}
	if in.EvalTime, err = gitx.CommitTime(dir, head); err != nil {
		return in, false, err
	}
	changes, err := gitx.ChangedFiles(dir, base, head)
	if err != nil {
		return in, false, err
	}
	for _, c := range changes {
		cf, lf, err := mapChange(c)
		if err != nil {
			return in, false, err
		}
		in.Changed = append(in.Changed, cf)
		if lf == nil {
			continue
		}
		if lf.Status != footprint.Deleted {
			data, found, err := gitx.ShowFile(dir, head, lf.Path)
			if err != nil {
				return in, false, err
			}
			if !found {
				return in, false, fmt.Errorf("%s is changed but not in %s", lf.Path, head)
			}
			lf.Data = data
		}
		in.LockFiles = append(in.LockFiles, *lf)
	}
	if in.Config, configFound, err = gitx.ShowFile(dir, head, configFile); err != nil {
		return in, false, err
	}
	if in.GitAttributes, _, err = gitx.ShowFile(dir, head, attributesFile); err != nil {
		return in, false, err
	}
	return in, configFound, nil
}

func isLockPath(p string) bool {
	return strings.HasPrefix(p, lockDirPrefix) && strings.HasSuffix(p, ".yml")
}

// mapChange turns one git status into the changed file for the footprint check and, for a lock
// file, the lock file entry for the core (Data is filled by the caller). A copy counts as an added
// file for the footprint check; for a lock file it keeps the status "copied" so the core rejects it.
// A status git has but this version does not map is an error for a file that is not a lock file.
func mapChange(c gitx.Change) (footprint.ChangedFile, *verify.LockFile, error) {
	letter := ""
	if c.Status != "" {
		letter = c.Status[:1]
	}
	var status footprint.Status
	switch letter {
	case "A":
		status = footprint.Added
	case "M", "T":
		status = footprint.Modified
	case "D":
		status = footprint.Deleted
	case "R":
		status = footprint.Renamed
	case "C":
		status = footprint.Added
	}
	cf := footprint.ChangedFile{Path: c.Path, Status: status}
	if letter == "R" {
		cf.OldPath = c.OldPath
	}
	if !isLockPath(c.Path) {
		if status == "" {
			return cf, nil, fmt.Errorf("git reports status %q for %s, which this version does not understand", c.Status, c.Path)
		}
		return cf, nil, nil
	}
	lf := &verify.LockFile{Path: c.Path, Status: status}
	switch letter {
	case "R":
		if isLockPath(c.OldPath) {
			lf.OldPath = c.OldPath
		} else {
			lf.Status = footprint.Added // a file moved into .intent/ is a new lock
		}
	case "C":
		lf.Status = "copied"
	case "A", "M", "T", "D":
	default:
		lf.Status = footprint.Status(letter)
	}
	return cf, lf, nil
}

// decisionFile is decision.json; the field order is the key order.
type decisionFile struct {
	Publish    bool   `json:"publish"`
	Conclusion string `json:"conclusion"`
	Comment    string `json:"comment"`
	Title      string `json:"title"`
}

// writeOutDir creates dir and writes the output files into it (the five, and audit.json), overwriting those only.
func writeOutDir(dir string, outputs map[string][]byte, dec policy.Decision, auditJSON []byte) error {
	var buf bytes.Buffer
	enc := json.NewEncoder(&buf)
	enc.SetEscapeHTML(false)
	enc.SetIndent("", "  ")
	if err := enc.Encode(decisionFile{dec.Publish, dec.Conclusion, string(dec.Comment), dec.Title}); err != nil {
		return err
	}
	files := []struct {
		name string
		data []byte
	}{
		{"verify.json", outputs["json"]},
		{"results.sarif", outputs["sarif"]},
		{"summary.md", outputs["summary"]},
		{"comment.md", outputs["comment"]},
		{"decision.json", buf.Bytes()},
	}
	if auditJSON != nil {
		files = append(files, struct {
			name string
			data []byte
		}{"audit.json", auditJSON})
	}
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return fmt.Errorf("cannot create the out dir: %w", err)
	}
	for _, f := range files {
		if err := os.WriteFile(filepath.Join(dir, f.name), f.data, 0o644); err != nil {
			return fmt.Errorf("cannot write %s: %w", f.name, err)
		}
	}
	return nil
}
