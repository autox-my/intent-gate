// Package gitx is the adapter to the git CLI: the few repository facts the hooks need.
package gitx

import (
	"bytes"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"time"
)

// run runs git in dir and returns its trimmed standard output.
func run(dir string, args ...string) (string, error) {
	cmd := exec.Command("git", args...)
	cmd.Dir = dir
	var out, errb bytes.Buffer
	cmd.Stdout, cmd.Stderr = &out, &errb
	if err := cmd.Run(); err != nil {
		if msg := strings.TrimSpace(errb.String()); msg != "" {
			return "", fmt.Errorf("git %s: %s", args[0], msg)
		}
		return "", fmt.Errorf("git %s: %w", args[0], err)
	}
	return strings.TrimSpace(out.String()), nil
}

// TopLevel returns the root of the working tree that contains dir.
func TopLevel(dir string) (string, error) {
	if dir == "" {
		return "", errors.New("no working directory")
	}
	top, err := run(dir, "rev-parse", "--show-toplevel")
	if err != nil {
		return "", err
	}
	if top == "" {
		return "", errors.New("git rev-parse --show-toplevel printed nothing")
	}
	return top, nil
}

// UserEmail returns git's user.email as seen from dir; empty is an error, since the ratifier is
// required.
func UserEmail(dir string) (string, error) {
	out, err := run(dir, "config", "user.email")
	if err != nil || out == "" {
		return "", errors.New("git user.email is not set")
	}
	return out, nil
}

// BaseRef returns the ref a lock's base_ref should name for the repository at dir:
// $INTENTGATE_BASE_REF if set; else the short name of refs/remotes/origin/HEAD without origin/;
// else main if a local branch main exists, else master; else an error.
func BaseRef(dir string) (string, error) {
	if v := os.Getenv("INTENTGATE_BASE_REF"); v != "" {
		return v, nil
	}
	if short, err := run(dir, "symbolic-ref", "--short", "refs/remotes/origin/HEAD"); err == nil && short != "" {
		if name := strings.TrimPrefix(short, "origin/"); name != "" {
			return name, nil
		}
	}
	for _, b := range []string{"main", "master"} {
		if _, err := run(dir, "show-ref", "--verify", "--quiet", "refs/heads/"+b); err == nil {
			return b, nil
		}
	}
	return "", errors.New("cannot tell the base branch: set INTENTGATE_BASE_REF")
}

// Change is one entry of `git diff --name-status`: Status is git's raw status (a letter, plus a
// similarity score for renames and copies, such as "R100"); OldPath is set for those two only.
type Change struct {
	Status        string
	Path, OldPath string
}

// rawEnv is the whole environment of a raw git call: a fresh one, never the caller's. Nothing else
// passes through, so GIT_DIR, GIT_WORK_TREE, GIT_INDEX_FILE, GIT_CONFIG_COUNT/KEY_n/VALUE_n,
// GIT_EXTERNAL_DIFF, the user's and the system's git configuration have no effect on the output.
func rawEnv() []string {
	return []string{
		"PATH=" + os.Getenv("PATH"),
		"LC_ALL=C",
		"TZ=UTC",
		"GIT_CONFIG_NOSYSTEM=1",
		"GIT_CONFIG_GLOBAL=/dev/null",
		"GIT_TERMINAL_PROMPT=0",
		"GIT_OPTIONAL_LOCKS=0",
	}
}

// rawConfig pins, on the command line, the repository-local settings that change what git prints
// (the repository's own config cannot be switched off).
func rawConfig(safeDirectory string) []string {
	var args []string
	for _, kv := range []string{
		"safe.directory=" + safeDirectory, // a CI checkout owned by another user
		"core.quotepath=false",
		"color.ui=false",
		"diff.renames=true",
		"diff.relative=false",
		"diff.noprefix=false",
		"diff.mnemonicPrefix=false",
		"diff.renameLimit=65535",
		"core.ignoreCase=false",
		"core.fsmonitor=false", // a repo-local fsmonitor hook would run a command
		"core.hooksPath=/dev/null",
	} {
		args = append(args, "-c", kv)
	}
	return args
}

// gitName is the git command of an argument list, for error messages: its first non-option word.
func gitName(args []string) string {
	for _, a := range args {
		if !strings.HasPrefix(a, "-") {
			return a
		}
	}
	return args[0]
}

// execRaw runs git in the fresh environment with -C at dir, pinned config and the arguments, and
// returns its standard output unchanged.
func execRaw(dir, safeDirectory string, args ...string) ([]byte, error) {
	full := append([]string{"--no-pager", "-C", dir}, rawConfig(safeDirectory)...)
	cmd := exec.Command("git", append(full, args...)...)
	cmd.Env = rawEnv()
	var out, errb bytes.Buffer
	cmd.Stdout, cmd.Stderr = &out, &errb
	if err := cmd.Run(); err != nil {
		if msg := strings.TrimSpace(errb.String()); msg != "" {
			return nil, fmt.Errorf("git %s: %s", gitName(args), msg)
		}
		return nil, fmt.Errorf("git %s: %w", gitName(args), err)
	}
	return out.Bytes(), nil
}

// rawTopLevel resolves the top level of the working tree that contains dir, in the fresh
// environment. Only this call runs with safe.directory=*: it prints a path and reads no content, and
// the top level is not known before it.
func rawTopLevel(dir string) (string, error) {
	abs, err := filepath.Abs(dir)
	if err != nil {
		return "", err
	}
	out, err := execRaw(abs, "*", "rev-parse", "--show-toplevel")
	if err != nil {
		return "", err
	}
	top := strings.TrimRight(string(out), "\n")
	if top == "" {
		return "", errors.New("git rev-parse --show-toplevel printed nothing")
	}
	return top, nil
}

// repo runs git at the top level of the repository that contains dir, however dir is spelled and
// whatever directory the caller is in.
type repo struct{ top string }

func openRepo(dir string) (repo, error) {
	top, err := rawTopLevel(dir)
	return repo{top}, err
}

func (r repo) run(args ...string) ([]byte, error) { return execRaw(r.top, r.top, args...) }

// checkRev refuses a rev that git would read as an option.
func checkRev(rev string) error {
	if rev == "" || strings.HasPrefix(rev, "-") {
		return fmt.Errorf("invalid revision %q", rev)
	}
	return nil
}

// ChangedFiles lists what head changed relative to the merge base of base and head
// (`git diff base...head`), in git's order, with renames and copies detected.
func ChangedFiles(dir, base, head string) ([]Change, error) {
	if err := checkRev(base); err != nil {
		return nil, err
	}
	if err := checkRev(head); err != nil {
		return nil, err
	}
	r, err := openRepo(dir)
	if err != nil {
		return nil, err
	}
	out, err := r.run("diff", "--name-status", "-z", "--no-relative", "--no-ext-diff", "--no-textconv", "--find-renames", "--find-copies", base+"..."+head, "--")
	if err != nil {
		return nil, err
	}
	fields := strings.Split(string(out), "\x00")
	if n := len(fields); n > 0 && fields[n-1] == "" {
		fields = fields[:n-1]
	}
	changes := []Change{}
	for i := 0; i < len(fields); {
		status := fields[i]
		i++
		paths := 1
		if strings.HasPrefix(status, "R") || strings.HasPrefix(status, "C") {
			paths = 2
		}
		if status == "" || i+paths > len(fields) {
			return nil, fmt.Errorf("git diff: cannot parse the output near %q", status)
		}
		c := Change{Status: status, Path: fields[i]}
		if paths == 2 {
			c.OldPath, c.Path = fields[i], fields[i+1]
		}
		i += paths
		changes = append(changes, c)
	}
	return changes, nil
}

// ShowFile returns the content of the file at path (relative to the repository root) in rev. A
// path that does not exist in rev is not an error: found is false. A bad rev, a path that is not a
// file and any other git failure are errors.
func ShowFile(dir, rev, path string) (data []byte, found bool, err error) {
	if err := checkRev(rev); err != nil {
		return nil, false, err
	}
	r, err := openRepo(dir)
	if err != nil {
		return nil, false, err
	}
	// ls-tree tells "no such path" (empty output) from "no such rev" (an error)
	out, err := r.run("--literal-pathspecs", "ls-tree", "-z", "--full-tree", rev, "--", path)
	if err != nil {
		return nil, false, err
	}
	if len(out) == 0 {
		return nil, false, nil
	}
	if bytes.HasPrefix(out, []byte("120000 ")) {
		return nil, false, fmt.Errorf("%s is a symlink in %s; commit the file itself", path, rev)
	}
	if !bytes.HasPrefix(out, []byte("100")) || !bytes.Contains(out, []byte(" blob ")) {
		return nil, false, fmt.Errorf("git ls-tree: %s in %s is not a regular file", path, rev)
	}
	data, err = r.run("cat-file", "blob", rev+":"+path)
	if err != nil {
		return nil, false, err
	}
	if data == nil {
		data = []byte{}
	}
	return data, true, nil
}

// CommitTime returns the committer time of rev, in UTC.
func CommitTime(dir, rev string) (time.Time, error) {
	if err := checkRev(rev); err != nil {
		return time.Time{}, err
	}
	r, err := openRepo(dir)
	if err != nil {
		return time.Time{}, err
	}
	out, err := r.run("show", "-s", "--format=%ct", rev+"^{commit}", "--")
	if err != nil {
		return time.Time{}, err
	}
	secs, err := strconv.ParseInt(strings.TrimSpace(string(out)), 10, 64)
	if err != nil {
		return time.Time{}, fmt.Errorf("git show: cannot read the commit time of %s: %w", rev, err)
	}
	return time.Unix(secs, 0).UTC(), nil
}

// LockAddition is a lock file and the first-parent commit that added it.
type LockAddition struct {
	Path   string // .intent/<name>.yml
	Commit string
	Time   time.Time // the commit's committer time, UTC
}

// LockAdditions lists the lock files (.intent/<name>.yml) at the tip of the first-parent history of
// rev, each with the commit that added it and that commit's committer time. A merge commit counts
// for the locks its side branch brought in, at the merge commit's own time. A rename inside
// .intent/ carries the original addition to the new path, so a moved lock is never new; a file moved
// into .intent/ from elsewhere is added by the move. A file that was removed and added again is
// listed once, at its newest addition. A path that no longer exists at rev may still be listed:
// the caller reads the file. The list is newest first, then by path.
func LockAdditions(dir, rev string) ([]LockAddition, error) {
	if err := checkRev(rev); err != nil {
		return nil, err
	}
	r, err := openRepo(dir)
	if err != nil {
		return nil, err
	}
	out, err := r.run("log", "--first-parent", "--diff-merges=first-parent", "--find-renames", "--diff-filter=AR", "--reverse",
		"--name-status", "--no-ext-diff", "--no-textconv", "--format=%H%x00%ct", "-z", rev, "--", ".intent/")
	if err != nil {
		return nil, err
	}
	// Oldest commit first. Per commit: <hash> NUL <ct> NUL LF then, per file, <status> NUL <path> NUL
	// (a rename has <status> NUL <old> NUL <new> NUL). A status is A or R and digits, which a hash,
	// a time or (being consumed in place) a path cannot be mistaken for.
	toks := strings.Split(string(out), "\x00")
	cur := map[string]LockAddition{}
	var commit string
	var when time.Time
	next := func(i int) (string, bool) {
		if i >= len(toks) {
			return "", false
		}
		return toks[i], true
	}
	for i := 0; i < len(toks); i++ {
		tok := strings.TrimPrefix(toks[i], "\n")
		switch {
		case tok == "":
		case tok[0] == 'A' && strings.Trim(tok[1:], "0123456789") == "":
			p, ok := next(i + 1)
			if !ok || commit == "" {
				return nil, fmt.Errorf("git log: cannot parse the output near %q", tok)
			}
			cur[p] = LockAddition{Path: p, Commit: commit, Time: when}
			i++
		case tok[0] == 'R' && strings.Trim(tok[1:], "0123456789") == "":
			from, ok1 := next(i + 1)
			to, ok2 := next(i + 2)
			if !ok1 || !ok2 || commit == "" {
				return nil, fmt.Errorf("git log: cannot parse the output near %q", tok)
			}
			add, tracked := cur[from]
			if !tracked {
				add = LockAddition{Commit: commit, Time: when} // moved in from outside .intent/
			}
			delete(cur, from)
			add.Path = to
			cur[to] = add
			i += 2
		default:
			secs, ok := next(i + 1)
			if !ok {
				return nil, fmt.Errorf("git log: cannot parse the output near %q", tok)
			}
			n, err := strconv.ParseInt(secs, 10, 64)
			if err != nil {
				return nil, fmt.Errorf("git log: cannot read the commit time of %s: %w", tok, err)
			}
			commit, when = tok, time.Unix(n, 0).UTC()
			i++
		}
	}
	adds := []LockAddition{}
	for p, a := range cur {
		if name, ok := strings.CutPrefix(p, ".intent/"); ok && strings.HasSuffix(name, ".yml") && !strings.Contains(name, "/") {
			adds = append(adds, a)
		}
	}
	sort.SliceStable(adds, func(i, j int) bool {
		if !adds[i].Time.Equal(adds[j].Time) {
			return adds[i].Time.After(adds[j].Time)
		}
		return adds[i].Path < adds[j].Path
	})
	return adds, nil
}

// Resolve returns the full object name of the commit rev names.
func Resolve(dir, rev string) (string, error) {
	if err := checkRev(rev); err != nil {
		return "", err
	}
	r, err := openRepo(dir)
	if err != nil {
		return "", err
	}
	out, err := r.run("rev-parse", "--verify", "--end-of-options", rev+"^{commit}")
	if err != nil {
		return "", err
	}
	return strings.TrimSpace(string(out)), nil
}

// Merge is a commit with two or more parents on a first-parent history.
type Merge struct {
	Commit string
	Time   time.Time // the commit's committer time, UTC
}

// FirstParentMerges lists the merge commits on the first-parent history of rev, newest first.
func FirstParentMerges(dir, rev string) ([]Merge, error) {
	return nil, nil // stub: the S-19 tests fail until this is written
}
