package main

import (
	"bytes"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"

	"intentgate/internal/footprint"
	"intentgate/internal/gitx"
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
	"                         [--author-type <type>] [--format text|json|sarif|summary|comment] [--out-dir <dir>]\n"

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
	_ = fs.String("repo", "", "")   // S-10 stub
	_ = fs.Int("pr", 0, "")         // S-10 stub
	_ = fs.String("replay", "", "") // S-10 stub
	if err := fs.Parse(args); err != nil || fs.NArg() > 0 {
		return t.usage(verifyUsage, "")
	}
	switch *format {
	case "text", "json", "sarif", "summary", "comment":
	default:
		return t.usage(verifyUsage, fmt.Sprintf("unknown --format %q", *format))
	}
	set := map[string]bool{}
	fs.Visit(func(f *flag.Flag) { set[f.Name] = true })
	if *base == "" || *head == "" || *baseBranch == "" || (set["out-dir"] && *outDir == "") {
		return t.usage(verifyUsage, "--base, --head and --base-branch are required")
	}

	in, err := readVerifyInput(deps.Dir, *base, *head, *baseBranch, *author, *authorType)
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
	if *outDir != "" {
		if err := writeOutDir(resolve(deps.Dir, *outDir), outputs, dec); err != nil {
			return t.fail(exitUnverifiable, "intentgate verify: %v", err)
		}
	}
	_, _ = stdout.Write(outputs[*format])
	return res.ExitCode
}

// readVerifyInput reads everything the check looks at, at head, through git (verify.Input).
func readVerifyInput(dir, base, head, baseBranch, author, authorType string) (verify.Input, error) {
	in := verify.Input{BaseBranch: baseBranch, Author: author, AuthorType: authorType}
	var err error
	if in.EvalTime, err = gitx.CommitTime(dir, head); err != nil {
		return in, err
	}
	changes, err := gitx.ChangedFiles(dir, base, head)
	if err != nil {
		return in, err
	}
	for _, c := range changes {
		cf, lf, err := mapChange(c)
		if err != nil {
			return in, err
		}
		in.Changed = append(in.Changed, cf)
		if lf == nil {
			continue
		}
		if lf.Status != footprint.Deleted {
			data, found, err := gitx.ShowFile(dir, head, lf.Path)
			if err != nil {
				return in, err
			}
			if !found {
				return in, fmt.Errorf("%s is changed but not in %s", lf.Path, head)
			}
			lf.Data = data
		}
		in.LockFiles = append(in.LockFiles, *lf)
	}
	if in.Config, _, err = gitx.ShowFile(dir, head, configFile); err != nil {
		return in, err
	}
	if in.GitAttributes, _, err = gitx.ShowFile(dir, head, attributesFile); err != nil {
		return in, err
	}
	return in, nil
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

// writeOutDir creates dir and writes the five output files into it, overwriting those five only.
func writeOutDir(dir string, outputs map[string][]byte, dec policy.Decision) error {
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
