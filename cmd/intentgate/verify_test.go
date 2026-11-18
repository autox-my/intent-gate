package main

import (
	"bytes"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"sort"
	"strings"
	"testing"
	"time"

	"intentgate/internal/lock"
	"intentgate/internal/ratify"
)

// The `verify` tests run the real command line against temporary git repositories. Every commit
// has a fixed author and committer date, so a lock's age at the head commit is part of the test.

const (
	lockA = "01JD8Z6Q4M7R2T9X3B5N8K1C6F"
	lockB = "01JD8Z6Q4M7R2T9X3B5N8K1C7G"
)

var (
	baseDate = time.Date(2026, 11, 18, 9, 0, 0, 0, time.UTC)
	headDate = time.Date(2026, 11, 18, 10, 0, 0, 0, time.UTC)
	// ratified within 14 days of headDate
	ratifiedOK = time.Date(2026, 11, 18, 9, 14, 3, 0, time.UTC)
)

type vrepo struct {
	t   *testing.T
	dir string
}

func newVRepo(t *testing.T) *vrepo {
	t.Helper()
	// The fixture's own git calls isolate themselves (gitAt), not through the process
	// environment: the process environment is what the determinism variants change.
	t.Setenv("INTENTGATE_BASE_REF", "")
	r := &vrepo{t: t, dir: t.TempDir()}
	r.git("init", "-q", "-b", "main")
	r.git("config", "user.email", "dev@example.com")
	r.git("config", "user.name", "dev")
	r.write("README.md", "# readme\n")
	r.commit("base", baseDate)
	return r
}

func (r *vrepo) git(args ...string) string {
	r.t.Helper()
	return r.gitAt(time.Time{}, args...)
}

func (r *vrepo) gitAt(date time.Time, args ...string) string {
	r.t.Helper()
	cmd := exec.Command("git", args...)
	cmd.Dir = r.dir
	cmd.Env = append(os.Environ(), "GIT_CONFIG_GLOBAL="+os.DevNull, "GIT_CONFIG_NOSYSTEM=1")
	if !date.IsZero() {
		d := date.Format(time.RFC3339)
		cmd.Env = append(cmd.Env, "GIT_COMMITTER_DATE="+d, "GIT_AUTHOR_DATE="+d)
	}
	out, err := cmd.CombinedOutput()
	if err != nil {
		r.t.Fatalf("git %s: %v\n%s", strings.Join(args, " "), err, out)
	}
	return strings.TrimSpace(string(out))
}

func (r *vrepo) write(name, content string) {
	r.t.Helper()
	p := filepath.Join(r.dir, name)
	if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
		r.t.Fatal(err)
	}
	if err := os.WriteFile(p, []byte(content), 0o644); err != nil {
		r.t.Fatal(err)
	}
}

func (r *vrepo) mkdir(name string) {
	r.t.Helper()
	if err := os.MkdirAll(filepath.Join(r.dir, name), 0o755); err != nil {
		r.t.Fatal(err)
	}
}

func (r *vrepo) commit(msg string, date time.Time) string {
	r.t.Helper()
	r.git("add", "-A")
	r.gitAt(date, "commit", "-q", "-m", msg)
	return r.git("rev-parse", "HEAD")
}

// feature starts the branch the pull request is made of; the base is main.
func (r *vrepo) feature() { r.git("checkout", "-q", "-b", "feature") }

// noScan is a secret scan that finds nothing, so building a lock needs no gitleaks run.
func noScan(string) ([]ratify.Finding, error) { return nil, nil }

// mkLock builds a ratified lock with ratify and returns it with its file content.
func mkLock(t *testing.T, id, baseRef string, ratified time.Time, criteria ...lock.Criterion) (lock.Lock, []byte) {
	t.Helper()
	l, _, err := ratify.MaskAndBuild(noScan, "Add the thing.", ratify.BuildInput{
		ID: id, BaseRef: baseRef, Source: "claude-code-session", Criteria: criteria,
		Ratifier: "github:octocat", RatifiedAt: ratified, Method: "hook", ReviewDurationMS: 48210,
	})
	if err != nil {
		t.Fatal(err)
	}
	return l, marshalLock(t, l)
}

func marshalLock(t *testing.T, l lock.Lock) []byte {
	t.Helper()
	data, err := lock.Marshal(l)
	if err != nil {
		t.Fatal(err)
	}
	return data
}

func crit(id, text string, fp ...string) lock.Criterion {
	return lock.Criterion{ID: id, Text: text, Footprint: fp}
}

func lockPath(id string) string { return ".intent/" + id + ".yml" }

// verifyRun runs `intentgate verify` in dir. The clock must not be read, except for the audit
// event's `at` (S-10), which exists only with --out-dir here (there is no state dir).
func verifyRun(t *testing.T, dir string, args ...string) (code int, stdout, stderr string) {
	t.Helper()
	var out, errb bytes.Buffer
	withOutDir := false
	for _, a := range args {
		withOutDir = withOutDir || a == "--out-dir"
	}
	deps := Deps{
		Now: func() time.Time {
			if !withOutDir {
				t.Error("verify read the clock")
			}
			return time.Date(2026, 11, 19, 8, 0, 0, 0, time.UTC)
		},
		Dir:    dir,
		Stderr: &errb,
	}
	code = Run(append([]string{"verify"}, args...), strings.NewReader(""), &out, deps)
	return code, out.String(), errb.String()
}

// verifyOut is verifyRun on the repository, from main to feature, with --out-dir; it returns the
// code and the out dir.
func (r *vrepo) verify(extra ...string) (code int, outDir string, stdout, stderr string) {
	r.t.Helper()
	return r.verifyIn(r.dir, extra...)
}

// verifyIn is verify run from dir, which may be a subdirectory of the repository.
func (r *vrepo) verifyIn(dir string, extra ...string) (code int, outDir string, stdout, stderr string) {
	r.t.Helper()
	outDir = filepath.Join(r.t.TempDir(), "out")
	args := []string{"--base", "main", "--head", "feature", "--base-branch", "main", "--out-dir", outDir}
	code, stdout, stderr = verifyRun(r.t, dir, append(args, extra...)...)
	return code, outDir, stdout, stderr
}

type verifyDoc struct {
	Schema   string   `json:"schema"`
	Outcome  string   `json:"outcome"`
	ExitCode int      `json:"exit_code"`
	Problems []string `json:"problems"`
	Locks    []struct {
		Path     string   `json:"path"`
		ID       string   `json:"id"`
		Suspect  []string `json:"suspect"`
		Problems []string `json:"problems"`
	} `json:"locks"`
	Findings []struct {
		Path   string `json:"path"`
		Status string `json:"status"`
		Fix    string `json:"fix"`
	} `json:"findings"`
}

func readOut(t *testing.T, dir, name string) []byte {
	t.Helper()
	data, err := os.ReadFile(filepath.Join(dir, name))
	if err != nil {
		t.Fatalf("the out dir has no %s: %v", name, err)
	}
	return data
}

func readDoc(t *testing.T, dir string) verifyDoc {
	t.Helper()
	var d verifyDoc
	if err := json.Unmarshal(readOut(t, dir, "verify.json"), &d); err != nil {
		t.Fatalf("verify.json: %v", err)
	}
	return d
}

type decisionDoc struct {
	Publish    bool   `json:"publish"`
	Conclusion string `json:"conclusion"`
	Comment    string `json:"comment"`
	Title      string `json:"title"`
}

func readDecision(t *testing.T, dir string) decisionDoc {
	t.Helper()
	var d decisionDoc
	if err := json.Unmarshal(readOut(t, dir, "decision.json"), &d); err != nil {
		t.Fatalf("decision.json: %v", err)
	}
	return d
}

func findingPaths(d verifyDoc) []string {
	paths := []string{}
	for _, f := range d.Findings {
		paths = append(paths, f.Path)
	}
	sort.Strings(paths)
	return paths
}

func suspectOf(t *testing.T, d verifyDoc, path string) []string {
	t.Helper()
	for _, l := range d.Locks {
		if l.Path == path {
			return l.Suspect
		}
	}
	t.Fatalf("verify.json has no lock %s: %+v", path, d.Locks)
	return nil
}

// REQ-0024
func TestVerify_CleanAndFindings(t *testing.T) {
	srcOnly := crit("C1", "The command is implemented", "src/**")

	t.Run("a file outside the footprint is a finding with its fix command", func(t *testing.T) {
		r := newVRepo(t)
		r.feature()
		_, data := mkLock(t, lockA, "main", ratifiedOK, srcOnly)
		r.write(lockPath(lockA), string(data))
		r.write("src/a.go", "package src\n")
		r.write("docs/outside.md", "outside\n")
		r.commit("head", headDate)

		code, out, stdout, stderr := r.verify()
		if code != 1 {
			t.Fatalf("exit %d, want 1; stderr %q, stdout %q", code, stderr, stdout)
		}
		d := readDoc(t, out)
		if d.Outcome != "findings" || d.ExitCode != 1 {
			t.Errorf("outcome %q exit %d, want findings 1", d.Outcome, d.ExitCode)
		}
		if got := findingPaths(d); !reflect.DeepEqual(got, []string{"docs/outside.md"}) {
			t.Fatalf("findings = %v, want only docs/outside.md", got)
		}
		if want := "intentgate amend " + lockA + " --criterion C1 --add "; !strings.HasPrefix(d.Findings[0].Fix, want) {
			t.Errorf("fix = %q, want it to start with %q", d.Findings[0].Fix, want)
		}
		if len(d.Locks) != 1 || len(d.Locks[0].Suspect) != 0 {
			t.Errorf("locks = %+v, want one lock that is not suspect", d.Locks)
		}
	})

	t.Run("only files inside the footprint is clean", func(t *testing.T) {
		r := newVRepo(t)
		r.feature()
		_, data := mkLock(t, lockA, "main", ratifiedOK, srcOnly)
		r.write(lockPath(lockA), string(data))
		r.write("src/a.go", "package src\n")
		r.write("src/deep/b.go", "package deep\n")
		r.commit("head", headDate)

		code, out, stdout, stderr := r.verify()
		if code != 0 {
			t.Fatalf("exit %d, want 0; stderr %q, stdout %q", code, stderr, stdout)
		}
		d := readDoc(t, out)
		if d.Outcome != "clean" || len(d.Findings) != 0 {
			t.Errorf("outcome %q findings %+v, want clean and none", d.Outcome, d.Findings)
		}
		if dec := readDecision(t, out); !dec.Publish || dec.Conclusion != "success" || dec.Comment != "update-if-exists" {
			t.Errorf("decision = %+v", dec)
		}
	})

	t.Run(".gitattributes and the config are read at head", func(t *testing.T) {
		// the .gitattributes file is itself a changed file, so the footprint names it
		_, data := mkLock(t, lockA, "main", ratifiedOK, crit("C1", "The command is implemented", "src/**", ".gitattributes"))
		onFeature := func(t *testing.T, files map[string]string) *vrepo {
			r := newVRepo(t)
			r.feature()
			r.write(lockPath(lockA), string(data))
			r.write("src/a.go", "package src\n")
			r.write("gen/out.txt", "generated\n")
			for name, content := range files {
				r.write(name, content)
			}
			r.commit("head", headDate)
			return r
		}

		if code, out, _, stderr := onFeature(t, nil).verify(); code != 1 || !reflect.DeepEqual(findingPaths(readDoc(t, out)), []string{"gen/out.txt"}) {
			t.Fatalf("without .gitattributes: exit %d, stderr %q; want 1 and gen/out.txt outside", code, stderr)
		}
		r := onFeature(t, map[string]string{".gitattributes": "gen/** linguist-generated\n"})
		if code, _, stdout, stderr := r.verify(); code != 0 {
			t.Errorf("with .gitattributes marking gen/ generated: exit %d, want 0; stderr %q, stdout %q", code, stderr, stdout)
		}

		r = onFeature(t, map[string]string{".intentgate.yml": "version: 1\nskip_authors: [octocat]\n"})
		if code, out, _, _ := r.verify("--author", "octocat"); code != 0 || readDoc(t, out).Outcome != "skipped" {
			t.Errorf("skip_authors in the config: exit %d, want 0 and skipped", code)
		}

		r = onFeature(t, map[string]string{".intentgate.yml": "bogus: 1\n"})
		code, out, _, _ := r.verify()
		if code != 3 {
			t.Fatalf("a malformed .intentgate.yml: exit %d, want 3", code)
		}
		if d := readDoc(t, out); d.Outcome != "unverifiable" || len(d.Problems) == 0 {
			t.Errorf("doc = %+v, want unverifiable with a problem", d)
		}
	})
}

// REQ-0033
func TestVerify_NoLock(t *testing.T) {
	r := newVRepo(t)
	r.feature()
	r.write("src/a.go", "package src\n")
	r.commit("head", headDate)

	code, out, stdout, stderr := r.verify()
	if code != 0 {
		t.Fatalf("exit %d, want 0; stderr %q", code, stderr)
	}
	if d := readDoc(t, out); d.Outcome != "no-lock" {
		t.Errorf("outcome = %q, want no-lock", d.Outcome)
	}
	if dec := readDecision(t, out); dec.Comment != "none" || !dec.Publish {
		t.Errorf("decision = %+v, want publish with comment none", dec)
	}
	if !strings.Contains(stdout, "no-lock") {
		t.Errorf("the default text format says %q", stdout)
	}
}

// REQ-0021 REQ-0022 REQ-0023
func TestVerify_Lifecycle(t *testing.T) {
	inside := crit("C1", "The command is implemented", "src/**")

	// build puts one lock on the feature branch and runs the check.
	build := func(t *testing.T, id string, data []byte, head time.Time, setup func(r *vrepo)) (int, verifyDoc) {
		t.Helper()
		r := newVRepo(t)
		if setup != nil {
			setup(r)
		}
		r.feature()
		r.write(lockPath(id), string(data))
		r.write("src/a.go", "package src\n")
		r.commit("head", head)
		code, out, stdout, stderr := r.verify()
		if code == 2 || code == 3 {
			t.Fatalf("exit %d; stderr %q, stdout %q", code, stderr, stdout)
		}
		return code, readDoc(t, out)
	}

	t.Run("retargeted", func(t *testing.T) {
		_, data := mkLock(t, lockA, "release", ratifiedOK, inside)
		code, d := build(t, lockA, data, headDate, nil)
		if code != 1 || !reflect.DeepEqual(suspectOf(t, d, lockPath(lockA)), []string{"retargeted"}) {
			t.Errorf("exit %d, suspect %v; want 1 and [retargeted]", code, d.Locks)
		}
	})

	t.Run("hand-edited lock", func(t *testing.T) {
		l, _ := mkLock(t, lockA, "main", ratifiedOK, inside)
		l.Criteria[0].Text = "Edited by hand after ratification"
		code, d := build(t, lockA, marshalLock(t, l), headDate, nil)
		if code != 1 || !reflect.DeepEqual(suspectOf(t, d, lockPath(lockA)), []string{"hash-mismatch"}) {
			t.Errorf("exit %d, suspect %v; want 1 and [hash-mismatch]", code, d.Locks)
		}
	})

	t.Run("older than 14 days at the head commit time", func(t *testing.T) {
		_, data := mkLock(t, lockA, "main", headDate.Add(-15*24*time.Hour), inside)
		code, d := build(t, lockA, data, headDate, nil)
		if code != 1 || !reflect.DeepEqual(suspectOf(t, d, lockPath(lockA)), []string{"expired"}) {
			t.Errorf("exit %d, suspect %v; want 1 and [expired]", code, d.Locks)
		}
	})

	t.Run("13 days old is still valid, by the head commit time and not by the clock", func(t *testing.T) {
		_, data := mkLock(t, lockA, "main", headDate.Add(-13*24*time.Hour), inside)
		code, d := build(t, lockA, data, headDate, nil)
		if code != 0 || len(suspectOf(t, d, lockPath(lockA))) != 0 {
			t.Errorf("exit %d, suspect %v; want 0 and none", code, d.Locks)
		}
	})

	t.Run("a lock that is modified", func(t *testing.T) {
		_, orig := mkLock(t, lockA, "main", ratifiedOK, inside)
		_, changed := mkLock(t, lockA, "main", ratifiedOK, inside, crit("C2", "Docs", "docs/**"))
		code, d := build(t, lockA, changed, headDate, func(r *vrepo) {
			r.write(lockPath(lockA), string(orig))
			r.commit("existing lock", baseDate.Add(time.Minute))
		})
		if code != 1 || !reflect.DeepEqual(suspectOf(t, d, lockPath(lockA)), []string{"modifies-existing-lock"}) {
			t.Errorf("exit %d, suspect %v; want 1 and [modifies-existing-lock]", code, d.Locks)
		}
	})

	t.Run("a deleted lock is ignored", func(t *testing.T) {
		_, orig := mkLock(t, lockA, "main", ratifiedOK, inside)
		r := newVRepo(t)
		r.write(lockPath(lockA), string(orig))
		r.commit("existing lock", baseDate.Add(time.Minute))
		r.feature()
		r.git("rm", "-q", lockPath(lockA))
		r.write("src/a.go", "package src\n")
		r.commit("head", headDate)
		code, out, stdout, stderr := r.verify()
		if code != 0 || readDoc(t, out).Outcome != "no-lock" {
			t.Errorf("exit %d, outcome %q; want 0 and no-lock; stderr %q, stdout %q", code, readDoc(t, out).Outcome, stderr, stdout)
		}
	})
}

// REQ-0024
func TestVerify_Renames(t *testing.T) {
	const content = "package src\n\nfunc a() {}\nfunc b() {}\nfunc c() {}\nfunc d() {}\nfunc e() {}\n"
	inside := crit("C1", "The command is implemented", "src/**")

	t.Run("a file renamed out of the footprint checks its new path", func(t *testing.T) {
		r := newVRepo(t)
		r.write("src/in.go", content)
		r.commit("file", baseDate.Add(time.Minute))
		r.feature()
		_, data := mkLock(t, lockA, "main", ratifiedOK, inside)
		r.write(lockPath(lockA), string(data))
		r.mkdir("other")
		r.git("mv", "src/in.go", "other/out.go")
		r.commit("head", headDate)
		code, out, stdout, stderr := r.verify()
		if code != 1 {
			t.Fatalf("exit %d, want 1; stderr %q, stdout %q", code, stderr, stdout)
		}
		d := readDoc(t, out)
		if got := findingPaths(d); !reflect.DeepEqual(got, []string{"other/out.go"}) || d.Findings[0].Status != "renamed" {
			t.Errorf("findings = %+v, want other/out.go renamed", d.Findings)
		}
	})

	t.Run("a file renamed into the footprint checks its old path", func(t *testing.T) {
		r := newVRepo(t)
		r.write("old/out.go", content)
		r.commit("file", baseDate.Add(time.Minute))
		r.feature()
		_, data := mkLock(t, lockA, "main", ratifiedOK, inside)
		r.write(lockPath(lockA), string(data))
		r.mkdir("src")
		r.git("mv", "old/out.go", "src/in.go")
		r.commit("head", headDate)
		code, out, stdout, stderr := r.verify()
		if code != 1 {
			t.Fatalf("exit %d, want 1; stderr %q, stdout %q", code, stderr, stdout)
		}
		d := readDoc(t, out)
		if got := findingPaths(d); !reflect.DeepEqual(got, []string{"old/out.go"}) {
			t.Errorf("findings = %+v, want old/out.go (the old path)", d.Findings)
		}
	})

	t.Run("a renamed lock modifies an existing lock", func(t *testing.T) {
		_, orig := mkLock(t, lockA, "main", ratifiedOK, inside)
		r := newVRepo(t)
		r.write(lockPath(lockA), string(orig))
		r.commit("existing lock", baseDate.Add(time.Minute))
		r.feature()
		r.git("mv", lockPath(lockA), ".intent/"+strings.ToLower(lockA)+".yml")
		r.commit("head", headDate)
		code, out, stdout, stderr := r.verify()
		if code != 3 { // the file name no longer matches the id
			t.Fatalf("exit %d, want 3; stderr %q, stdout %q", code, stderr, stdout)
		}
		d := readDoc(t, out)
		if len(d.Locks) != 1 || d.Locks[0].Path != ".intent/"+strings.ToLower(lockA)+".yml" {
			t.Errorf("locks = %+v, want one lock at the new path", d.Locks)
		}
	})

	t.Run("a copied lock is unverifiable", func(t *testing.T) {
		_, orig := mkLock(t, lockA, "main", ratifiedOK, inside)
		_, changed := mkLock(t, lockA, "main", ratifiedOK, inside, crit("C2", "Docs", "docs/**"))
		r := newVRepo(t)
		r.write(lockPath(lockA), string(orig))
		r.commit("existing lock", baseDate.Add(time.Minute))
		r.feature()
		// the original is changed and its old content appears under a new name: a copy
		r.write(lockPath(lockA), string(changed))
		r.write(lockPath(lockB), string(orig))
		r.commit("head", headDate)
		code, out, stdout, stderr := r.verify()
		if code != 3 {
			t.Fatalf("exit %d, want 3; stderr %q, stdout %q", code, stderr, stdout)
		}
		d := readDoc(t, out)
		if d.Outcome != "unverifiable" {
			t.Errorf("outcome = %q", d.Outcome)
		}
		found := false
		for _, l := range d.Locks {
			if l.Path == lockPath(lockB) {
				found = true
				if len(l.Problems) != 1 || !strings.Contains(l.Problems[0], "copied") {
					t.Errorf("problems of the copy = %v, want one that says copied", l.Problems)
				}
			}
		}
		if !found {
			t.Errorf("the copy %s is not in locks: %+v", lockPath(lockB), d.Locks)
		}
	})
}

// REQ-0040
func TestVerify_Deterministic(t *testing.T) {
	r := newVRepo(t)
	r.feature()
	_, data := mkLock(t, lockA, "release", ratifiedOK, crit("C1", "The command is implemented", "src/**"))
	r.write(lockPath(lockA), string(data))
	r.write("src/a.go", "package src\n")
	r.write("docs/b.md", "b\n")
	r.write("other/c.go", "package other\n")
	r.write("ünï/日本語 file.txt", "x\n")
	r.commit("head", headDate)

	files := []string{"verify.json", "results.sarif", "summary.md", "comment.md", "decision.json"}
	formats := []string{"text", "json", "sarif", "summary", "comment"}
	type outputs struct {
		files   map[string][]byte
		formats map[string]string
	}
	var collectIn func(t *testing.T, cwd string) outputs
	collect := func(t *testing.T) outputs { return collectIn(t, r.dir) }
	collectIn = func(t *testing.T, cwd string) outputs {
		t.Helper()
		o := outputs{files: map[string][]byte{}, formats: map[string]string{}}
		code, dir, stdout, stderr := r.verifyIn(cwd)
		if code != 1 {
			t.Fatalf("exit %d, want 1; stderr %q, stdout %q", code, stderr, stdout)
		}
		for _, f := range files {
			o.files[f] = readOut(t, dir, f)
			if len(o.files[f]) == 0 {
				t.Errorf("%s is empty", f)
			}
		}
		for _, f := range formats {
			code, stdout, stderr := verifyRun(t, cwd, "--base", "main", "--head", "feature", "--base-branch", "main", "--format", f)
			if code != 1 || stdout == "" || stderr != "" {
				t.Fatalf("--format %s: exit %d, stdout %q, stderr %q", f, code, stdout, stderr)
			}
			o.formats[f] = stdout
		}
		return o
	}
	compare := func(t *testing.T, name string, a, b outputs) {
		t.Helper()
		for _, f := range files {
			if !bytes.Equal(a.files[f], b.files[f]) {
				t.Errorf("%s: %s differs:\n%s\n---\n%s", name, f, a.files[f], b.files[f])
			}
		}
		for _, f := range formats {
			if a.formats[f] != b.formats[f] {
				t.Errorf("%s: --format %s differs:\n%s\n---\n%s", name, f, a.formats[f], b.formats[f])
			}
		}
	}

	first := collect(t)
	compare(t, "second run", first, collect(t))

	t.Setenv("TZ", "Pacific/Auckland")
	t.Setenv("LC_ALL", "tr_TR.UTF-8")
	t.Setenv("LANG", "tr_TR.UTF-8")
	compare(t, "other TZ and LC_ALL", first, collect(t))

	// the caller's directory, git environment and git configuration change nothing
	sub := filepath.Join(r.dir, "src")
	t.Run("from a subdirectory", func(t *testing.T) {
		compare(t, "subdirectory", first, collectIn(t, sub))
	})
	t.Run("GIT_DIR pointing at another repository", func(t *testing.T) {
		other := t.TempDir()
		cmd := exec.Command("git", "init", "-q", "-b", "main", other)
		cmd.Env = append(os.Environ(), "GIT_CONFIG_GLOBAL="+os.DevNull, "GIT_CONFIG_NOSYSTEM=1")
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("git init: %v\n%s", err, out)
		}
		t.Setenv("GIT_DIR", filepath.Join(other, ".git"))
		t.Setenv("GIT_WORK_TREE", other)
		compare(t, "GIT_DIR", first, collectIn(t, r.dir))
		compare(t, "GIT_DIR from a subdirectory", first, collectIn(t, sub))
	})
	t.Run("GIT_CONFIG_COUNT sets diff.relative", func(t *testing.T) {
		t.Setenv("GIT_CONFIG_COUNT", "1")
		t.Setenv("GIT_CONFIG_KEY_0", "diff.relative")
		t.Setenv("GIT_CONFIG_VALUE_0", "true")
		compare(t, "GIT_CONFIG_COUNT", first, collectIn(t, sub))
	})
	t.Run("a global config file sets diff.relative", func(t *testing.T) {
		global := filepath.Join(t.TempDir(), "gitconfig")
		if err := os.WriteFile(global, []byte("[diff]\n\trelative = true\n\tnoprefix = true\n\trenames = false\n"), 0o644); err != nil {
			t.Fatal(err)
		}
		t.Setenv("GIT_CONFIG_GLOBAL", global)
		compare(t, "global config", first, collectIn(t, sub))
	})
	t.Run("the repository's own config sets diff.relative", func(t *testing.T) {
		r.git("config", "diff.relative", "true")
		r.git("config", "diff.noprefix", "true")
		t.Cleanup(func() {
			r.git("config", "--unset", "diff.relative")
			r.git("config", "--unset", "diff.noprefix")
		})
		compare(t, "repository config", first, collectIn(t, sub))
	})

	// each format goes to stdout as written to its file
	for format, file := range map[string]string{"json": "verify.json", "sarif": "results.sarif", "summary": "summary.md", "comment": "comment.md"} {
		if first.formats[format] != string(first.files[file]) {
			t.Errorf("--format %s differs from %s", format, file)
		}
	}

	t.Run("only the five files are written", func(t *testing.T) {
		out := filepath.Join(t.TempDir(), "a", "b")
		if err := os.MkdirAll(out, 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(out, "keep.txt"), []byte("mine"), 0o644); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(out, "verify.json"), []byte("stale"), 0o644); err != nil {
			t.Fatal(err)
		}
		if code, _, stderr := verifyRun(t, r.dir, "--base", "main", "--head", "feature", "--base-branch", "main", "--out-dir", out); code != 1 {
			t.Fatalf("exit %d, stderr %q", code, stderr)
		}
		if got, _ := os.ReadFile(filepath.Join(out, "keep.txt")); string(got) != "mine" {
			t.Errorf("keep.txt = %q", got)
		}
		if !bytes.Equal(readOut(t, out, "verify.json"), first.files["verify.json"]) {
			t.Error("verify.json was not overwritten")
		}
		entries, _ := os.ReadDir(out)
		if len(entries) != 6 {
			t.Errorf("%d entries in the out dir, want the five files and keep.txt", len(entries))
		}
	})

	t.Run("decision.json has a fixed key order", func(t *testing.T) {
		got := string(first.files["decision.json"])
		want := "{\n  \"publish\": true,\n  \"conclusion\": \"neutral\",\n  \"comment\": \"upsert\",\n  \"title\": "
		if !strings.HasPrefix(got, want) || !strings.HasSuffix(got, "\n") {
			t.Errorf("decision.json = %q, want it to start with %q", got, want)
		}
	})
}

// A symlink at head is not followed: its target is another path, possibly outside the repository.
func TestVerify_SymlinkedConfig(t *testing.T) {
	for _, name := range []string{".intentgate.yml", ".gitattributes"} {
		t.Run(name, func(t *testing.T) {
			r := newVRepo(t)
			r.feature()
			_, data := mkLock(t, lockA, "main", ratifiedOK, crit("C1", "The command is implemented", "src/**"))
			r.write(lockPath(lockA), string(data))
			r.write("src/a.go", "package src\n")
			r.write("real-config", "x: 1\n")
			if err := os.Symlink("real-config", filepath.Join(r.dir, name)); err != nil {
				t.Fatal(err)
			}
			r.commit("head", headDate)
			code, _, stdout, stderr := r.verify()
			want := name + " is a symlink in feature; commit the file itself"
			if code != 3 || stdout != "" || !strings.Contains(stderr, want) {
				t.Errorf("exit %d, stdout %q, stderr %q; want 3, empty stdout and %q", code, stdout, stderr, want)
			}
		})
	}
}

func TestVerify_Usage(t *testing.T) {
	r := newVRepo(t)
	r.feature()
	r.write("src/a.go", "package src\n")
	r.commit("head", headDate)

	ok := []string{"--base", "main", "--head", "feature", "--base-branch", "main"}
	without := func(flag string) []string {
		var out []string
		for i := 0; i < len(ok); i += 2 {
			if ok[i] != flag {
				out = append(out, ok[i], ok[i+1])
			}
		}
		return out
	}
	cases := map[string][]string{
		"nothing":                nil,
		"no --base":              without("--base"),
		"no --head":              without("--head"),
		"no --base-branch":       without("--base-branch"),
		"empty --base":           {"--base", "", "--head", "feature", "--base-branch", "main"},
		"unknown flag":           append(append([]string{}, ok...), "--title", "x"),
		"no PR text flag":        append(append([]string{}, ok...), "--body", "x"),
		"bad format":             append(append([]string{}, ok...), "--format", "xml"),
		"empty format":           append(append([]string{}, ok...), "--format", ""),
		"a stray argument":       append(append([]string{}, ok...), "extra"),
		"a flag without a value": append(append([]string{}, ok...), "--author"),
	}
	for name, args := range cases {
		t.Run(name, func(t *testing.T) {
			code, stdout, stderr := verifyRun(t, r.dir, args...)
			if code != 2 || stdout != "" || !strings.Contains(stderr, "usage: intentgate verify") {
				t.Errorf("exit %d, stdout %q, stderr %q; want 2, nothing on stdout and the usage", code, stdout, stderr)
			}
		})
	}

	t.Run("a bad rev is exit 3 with the reason on stderr", func(t *testing.T) {
		for _, args := range [][]string{
			{"--base", "no-such-rev", "--head", "feature", "--base-branch", "main"},
			{"--base", "main", "--head", "no-such-rev", "--base-branch", "main"},
			{"--base", "-x", "--head", "feature", "--base-branch", "main"},
		} {
			code, stdout, stderr := verifyRun(t, r.dir, args...)
			if code != 3 || stdout != "" || strings.TrimSpace(stderr) == "" {
				t.Errorf("%v: exit %d, stdout %q, stderr %q; want 3, empty stdout and a reason", args, code, stdout, stderr)
			}
		}
	})

	t.Run("not a repository is exit 3", func(t *testing.T) {
		dir := t.TempDir()
		t.Setenv("GIT_CEILING_DIRECTORIES", filepath.Dir(dir))
		code, stdout, stderr := verifyRun(t, dir, ok...)
		if code != 3 || stdout != "" || strings.TrimSpace(stderr) == "" {
			t.Errorf("exit %d, stdout %q, stderr %q; want 3, empty stdout and a reason", code, stdout, stderr)
		}
	})

	t.Run("an out dir that cannot be written is exit 3", func(t *testing.T) {
		file := filepath.Join(t.TempDir(), "file")
		if err := os.WriteFile(file, []byte("x"), 0o644); err != nil {
			t.Fatal(err)
		}
		code, stdout, stderr := verifyRun(t, r.dir, append(append([]string{}, ok...), "--out-dir", filepath.Join(file, "out"))...)
		if code != 3 || stdout != "" || strings.TrimSpace(stderr) == "" {
			t.Errorf("exit %d, stdout %q, stderr %q; want 3, empty stdout and a reason", code, stdout, stderr)
		}
	})

	t.Run("a relative --out-dir is relative to the working directory", func(t *testing.T) {
		code, stdout, stderr := verifyRun(t, r.dir, append(append([]string{}, ok...), "--out-dir", "rel/out")...)
		if code != 0 {
			t.Fatalf("exit %d, stdout %q, stderr %q", code, stdout, stderr)
		}
		readOut(t, filepath.Join(r.dir, "rel", "out"), "verify.json")
	})

	t.Run("--version prints the version", func(t *testing.T) {
		var out bytes.Buffer
		if code := Run([]string{"--version"}, strings.NewReader(""), &out, Deps{}); code != 0 || !strings.Contains(out.String(), version) {
			t.Errorf("exit %d, stdout %q, want it to contain %q", code, out.String(), version)
		}
	})
}

// REQ-0034
func TestVerify_BotSkipped(t *testing.T) {
	r := newVRepo(t)
	r.feature()
	// a lock that would be a finding for anyone else
	_, data := mkLock(t, lockA, "main", ratifiedOK, crit("C1", "The command is implemented", "src/**"))
	r.write(lockPath(lockA), string(data))
	r.write("docs/outside.md", "outside\n")
	r.commit("head", headDate)

	for _, args := range [][]string{
		{"--author-type", "Bot"},
		{"--author", "dependabot[bot]"},
	} {
		code, out, stdout, stderr := r.verify(args...)
		if code != 0 {
			t.Fatalf("%v: exit %d; stderr %q, stdout %q", args, code, stderr, stdout)
		}
		if dec := readDecision(t, out); dec.Publish {
			t.Errorf("%v: decision = %+v, want publish false", args, dec)
		}
		if d := readDoc(t, out); d.Outcome != "skipped" {
			t.Errorf("%v: outcome = %q, want skipped", args, d.Outcome)
		}
	}

	if code, out, _, _ := r.verify("--author", "octocat", "--author-type", "User"); code != 1 || !readDecision(t, out).Publish {
		t.Errorf("a human author: exit %d, want 1 and publish", code)
	}
}
