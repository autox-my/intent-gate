package main

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	"intentgate/internal/footprint"
	"intentgate/internal/ids"
	"intentgate/internal/lock"
	"intentgate/internal/ratify"
)

const (
	confirmDelay = 5 * time.Second
	wantPrompt   = "Type yes to lock these criteria:"
	rootIntent   = "Please add SARIF output.\n\nKeep it small."
)

// scriptedStdin serves one line per Read, so a bufio.Reader never reads ahead of the line asked
// for. The fake clock moves forward by delay when the last line is served: that line is the
// human's answer, and the move is the time they took to type it.
type scriptedStdin struct {
	lines []string
	next  int
	e     *env
	delay time.Duration
}

func (s *scriptedStdin) Read(p []byte) (int, error) {
	if s.next >= len(s.lines) {
		return 0, io.EOF
	}
	line := s.lines[s.next]
	s.next++
	if s.next == len(s.lines) {
		s.e.now = s.e.now.Add(s.delay)
	}
	return copy(p, line), nil
}

// terminals makes the env's fake terminal check say stdin and stdout are (not) terminals.
func (e *env) terminals(stdin, stdout bool) {
	e.deps.Dir = e.repo
	e.deps.IsTerminal = func(fd int) bool { return map[int]bool{0: stdin, 1: stdout}[fd] }
}

// do runs the intentgate command line with the given stdin lines.
func (e *env) do(args []string, lines ...string) (code int, stdout, stderr string) {
	e.t.Helper()
	var out bytes.Buffer
	e.deps.Stderr = &bytes.Buffer{}
	code = Run(args, &scriptedStdin{lines: lines, e: e, delay: confirmDelay}, &out, e.deps)
	return code, out.String(), e.deps.Stderr.(*bytes.Buffer).String()
}

func (e *env) write(name, content string) string {
	e.t.Helper()
	p := filepath.Join(e.repo, name)
	if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
		e.t.Fatal(err)
	}
	if err := os.WriteFile(p, []byte(content), 0o644); err != nil {
		e.t.Fatal(err)
	}
	return p
}

func (e *env) readLock(path string) lock.Lock {
	e.t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		e.t.Fatal(err)
	}
	l, err := lock.Parse(data)
	if err != nil {
		e.t.Fatal(err)
	}
	return l
}

func (e *env) onlyLock() (lock.Lock, string) {
	e.t.Helper()
	files := e.locks()
	if len(files) != 1 {
		e.t.Fatalf("locks = %v, want exactly one", files)
	}
	return e.readLock(files[0]), files[0]
}

// REQ-0004
func TestLock_FromPlan(t *testing.T) {
	check := func(t *testing.T, e *env, code int, stdout, stderr, wantRoot string) {
		t.Helper()
		if code != 0 {
			t.Fatalf("exit %d, stderr %q, stdout %q", code, stderr, stdout)
		}
		l, path := e.onlyLock()
		if ps := lock.Validate(l); len(ps) != 0 {
			t.Errorf("lock.Validate: %v", ps)
		}
		if !lock.VerifyHash(l) {
			t.Error("the hash does not verify")
		}
		if l.Ratification.Method != "cli-confirm" || l.RootIntent.Source != "plan-file" || l.Ratification.Ratifier != "git:dev@example.com" {
			t.Errorf("ratification %+v, source %q", l.Ratification, l.RootIntent.Source)
		}
		if l.Ratification.ReviewDurationMS == nil || *l.Ratification.ReviewDurationMS != int(confirmDelay.Milliseconds()) {
			t.Errorf("review_duration_ms = %v, want %d", l.Ratification.ReviewDurationMS, confirmDelay.Milliseconds())
		}
		if want := t0.Add(confirmDelay).Format("2006-01-02T15:04:05Z"); l.Ratification.RatifiedAt != want {
			t.Errorf("ratified_at = %q, want %q", l.Ratification.RatifiedAt, want)
		}
		if l.Ratification.DraftedBy != nil {
			t.Errorf("drafted_by = %v, want none for a plan file", *l.Ratification.DraftedBy)
		}
		if l.BaseRef != "main" || l.Revision != 1 || len(l.Amendments) != 0 {
			t.Errorf("base %q, revision %d, amendments %v", l.BaseRef, l.Revision, l.Amendments)
		}
		wantCriteria := []lock.Criterion{
			{ID: "C1", Text: "Findings are published as SARIF", Footprint: []string{"internal/report/**"}},
			{ID: "C2", Text: "The report package has tests", Footprint: []string{"internal/report/**", "!internal/report/testdata/**"}},
		}
		if !reflect.DeepEqual(l.Criteria, wantCriteria) {
			t.Errorf("criteria = %+v, want %+v", l.Criteria, wantCriteria)
		}
		if filepath.Base(path) != l.ID+".yml" || !strings.Contains(stdout, "intent lock written: .intent/"+l.ID+".yml") {
			t.Errorf("file %s, stdout %q", path, stdout)
		}
		rec, err := os.ReadFile(filepath.Join(e.state, "locks", l.ID, "root-intent.md"))
		if err != nil {
			t.Fatal(err)
		}
		sum := sha256.Sum256(rec)
		if string(rec) != wantRoot || hex.EncodeToString(sum[:]) != l.RootIntent.SHA256 {
			t.Errorf("root intent record %q (sha256 %s), want %q matching the lock's %s", rec, hex.EncodeToString(sum[:]), wantRoot, l.RootIntent.SHA256)
		}
		if data, _ := os.ReadFile(path); strings.Contains(string(data), "SARIF output") {
			t.Error("the committed lock holds root intent text")
		}
		for _, want := range []string{wantPrompt, "C1", "Findings are published as SARIF", "internal/report/**", "!internal/report/testdata/**"} {
			if !strings.Contains(stdout, want) {
				t.Errorf("stdout lacks %q:\n%s", want, stdout)
			}
		}
	}

	t.Run("root intent from --prompt", func(t *testing.T) {
		e := newEnv(t, "dev@example.com")
		e.terminals(true, true)
		plan := e.write("plan.md", planText)
		prompt := e.write("prompt.txt", rootIntent)
		code, stdout, stderr := e.do([]string{"lock", "--from-plan", plan, "--prompt", prompt}, "yes\n")
		check(t, e, code, stdout, stderr, rootIntent)
		if strings.Contains(stdout, "Paste the root intent") {
			t.Error("asked for a root intent although --prompt was given")
		}
	})
	t.Run("root intent pasted, ended by a line with only a dot", func(t *testing.T) {
		e := newEnv(t, "dev@example.com")
		e.terminals(true, true)
		plan := e.write("plan.md", planText)
		code, stdout, stderr := e.do([]string{"lock", "--from-plan", plan},
			"Please add SARIF output.\n", "\n", "Keep it small.\n", ".\n", "yes\n")
		check(t, e, code, stdout, stderr, rootIntent+"\n") // review 1 of S-05: every pasted line ends in a newline
		if !strings.Contains(stdout, "Paste the root intent (the request the plan answers), then a line with only `.`:") {
			t.Errorf("stdout lacks the paste prompt:\n%s", stdout)
		}
	})
	t.Run("a pasted root intent and a --prompt file with the same lines hash the same", func(t *testing.T) {
		hashOf := func(args func(e *env, plan string) []string, lines ...string) string {
			e := newEnv(t, "dev@example.com")
			e.terminals(true, true)
			plan := e.write("plan.md", planText)
			code, _, stderr := e.do(args(e, plan), lines...)
			if code != 0 {
				t.Fatalf("exit %d: %s", code, stderr)
			}
			l, _ := e.onlyLock()
			return l.RootIntent.SHA256
		}
		pasted := hashOf(func(e *env, plan string) []string { return []string{"lock", "--from-plan", plan} },
			"Please add SARIF output.\n", "\n", "Keep it small.\n", ".\n", "yes\n")
		fromFile := hashOf(func(e *env, plan string) []string {
			return []string{"lock", "--from-plan", plan, "--prompt", e.write("prompt.txt", rootIntent+"\n")}
		}, "yes\n")
		if pasted != fromFile {
			t.Errorf("pasted root_intent.sha256 %s != --prompt file %s", pasted, fromFile)
		}
	})
	t.Run("a relative plan path is resolved from the working directory", func(t *testing.T) {
		e := newEnv(t, "dev@example.com")
		e.terminals(true, true)
		e.write("plan.md", planText)
		e.write("prompt.txt", rootIntent)
		code, stdout, stderr := e.do([]string{"lock", "--from-plan", "plan.md", "--prompt", "prompt.txt"}, "yes\n")
		check(t, e, code, stdout, stderr, rootIntent)
	})
	t.Run("secrets in the root intent are masked before anything is written", func(t *testing.T) {
		e := newEnv(t, "dev@example.com")
		e.terminals(true, true)
		plan := e.write("plan.md", planText)
		secret := "ghp_" + strings.Repeat("aB3dE5gH7j", 3) + "aB3dE6"
		prompt := e.write("prompt.txt", "Use the token github_token = \""+secret+"\" to publish.")
		if code, _, stderr := e.do([]string{"lock", "--from-plan", plan, "--prompt", prompt}, "yes\n"); code != 0 {
			t.Fatalf("exit %d: %s", code, stderr)
		}
		l, _ := e.onlyLock()
		rec, _ := os.ReadFile(filepath.Join(e.state, "locks", l.ID, "root-intent.md"))
		if strings.Contains(string(rec), secret) || l.RootIntent.Redactions == nil || *l.RootIntent.Redactions < 1 {
			t.Errorf("record %q, redactions %v", rec, l.RootIntent.Redactions)
		}
	})
	t.Run("drafting errors exit 1 with no lock", func(t *testing.T) {
		for name, tc := range map[string]struct {
			plan  string
			lines []string
		}{
			"plan without a block":    {"# a plan\n", []string{"yes\n"}},
			"empty root intent":       {planText, []string{".\n", "yes\n"}},
			"root intent never ended": {planText, []string{"Please add SARIF output.\n"}},
		} {
			t.Run(name, func(t *testing.T) {
				e := newEnv(t, "dev@example.com")
				e.terminals(true, true)
				plan := e.write("plan.md", tc.plan)
				code, _, stderr := e.do([]string{"lock", "--from-plan", plan}, tc.lines...)
				if code != 1 || !strings.Contains(stderr, "no lock:") {
					t.Errorf("exit %d, stderr %q; want 1 and \"no lock:\"", code, stderr)
				}
				e.nothingWritten()
			})
		}
	})
	t.Run("git cannot resolve the base: exit 3", func(t *testing.T) {
		e := newEnv(t, "dev@example.com")
		e.terminals(true, true)
		plan := e.write("plan.md", planText)
		prompt := e.write("prompt.txt", rootIntent)
		gitIn(t, e.repo, "branch", "-m", "main", "develop")
		if code, _, stderr := e.do([]string{"lock", "--from-plan", plan, "--prompt", prompt}, "yes\n"); code != 3 {
			t.Errorf("exit %d, stderr %q; want 3", code, stderr)
		}
		e.nothingWritten()
	})
	t.Run("usage errors exit 2", func(t *testing.T) {
		e := newEnv(t, "dev@example.com")
		e.terminals(true, true)
		plan := e.write("plan.md", planText)
		for _, args := range [][]string{
			{"lock"},
			{"lock", "--prompt", plan},
			{"lock", "--from-plan"},
			{"lock", "--from-plan", plan, "extra"},
			{"lock", "--from-plan", plan, "--bogus"},
		} {
			if code, _, stderr := e.do(args, "yes\n"); code != 2 || !strings.Contains(stderr, "usage:") {
				t.Errorf("%v: exit %d, stderr %q; want 2 and usage", args, code, stderr)
			}
		}
		e.nothingWritten()
	})
}

// REQ-0005
func TestLock_RequiresTerminalAndYes(t *testing.T) {
	setup := func(t *testing.T) (*env, []string) {
		e := newEnv(t, "dev@example.com")
		e.terminals(true, true)
		plan := e.write("plan.md", planText)
		prompt := e.write("prompt.txt", rootIntent)
		return e, []string{"lock", "--from-plan", plan, "--prompt", prompt}
	}

	t.Run("not both a terminal: exit 2, nothing written", func(t *testing.T) {
		for name, tty := range map[string][2]bool{"stdin": {false, true}, "stdout": {true, false}, "neither": {false, false}} {
			t.Run(name, func(t *testing.T) {
				e, args := setup(t)
				e.terminals(tty[0], tty[1])
				code, _, stderr := e.do(args, "yes\n")
				if code != 2 || !strings.Contains(stderr, "intentgate lock needs an interactive terminal; ratification is a human act") {
					t.Errorf("exit %d, stderr %q", code, stderr)
				}
				e.nothingWritten()
			})
		}
		t.Run("no terminal check at all", func(t *testing.T) {
			e, args := setup(t)
			e.deps.IsTerminal = nil
			if code, _, _ := e.do(args, "yes\n"); code != 2 {
				t.Errorf("exit %d, want 2", code)
			}
			e.nothingWritten()
		})
	})

	t.Run("anything but yes: exit 1, nothing written", func(t *testing.T) {
		for _, answer := range [][]string{{"no\n"}, {"y\n"}, {"YES \n"}, {"Yes\n"}, {"yes please\n"}, {"\n"}, {}} {
			e, args := setup(t)
			code, stdout, _ := e.do(args, answer...)
			if code != 1 {
				t.Errorf("answer %q: exit %d, want 1", answer, code)
			}
			if !strings.Contains(stdout, wantPrompt) {
				t.Errorf("answer %q: the prompt was not shown:\n%s", answer, stdout)
			}
			e.nothingWritten()
		}
	})

	t.Run("yes, with spaces and a CRLF line ending around it, writes", func(t *testing.T) {
		for _, answer := range []string{"yes\n", "  yes  \n", "yes\r\n", "yes"} {
			e, args := setup(t)
			if code, _, stderr := e.do(args, answer); code != 0 {
				t.Errorf("answer %q: exit %d, stderr %q", answer, code, stderr)
			} else if len(e.locks()) != 1 {
				t.Errorf("answer %q: locks %v", answer, e.locks())
			}
		}
	})

	t.Run("there is no --yes flag and no environment variable that skips the prompt", func(t *testing.T) {
		e, args := setup(t)
		if code, _, _ := e.do(append(args, "--yes"), "yes\n"); code != 2 {
			t.Errorf("--yes: exit %d, want 2", code)
		}
		e.nothingWritten()
		for _, v := range []string{"INTENTGATE_YES", "INTENTGATE_ASSUME_YES", "YES", "CI"} {
			t.Setenv(v, "1")
		}
		if code, _, _ := e.do(args, "no\n"); code != 1 {
			t.Errorf("with env vars set, answer no: exit %d, want 1", code)
		}
		e.nothingWritten()
	})
}

// Review 1 of S-05: the real terminal check. /dev/null is a character device but not a terminal, so
// `lock` and `amend` with stdin and stdout on /dev/null, or on pipes, exit 2 and write nothing. It
// runs the built binary with a temp INTENTGATE_HOME and temp repositories.
func TestLock_BuiltBinaryNeedsRealTerminal(t *testing.T) {
	bin := filepath.Join(t.TempDir(), "intentgate")
	if out, err := exec.Command("go", "build", "-o", bin, ".").CombinedOutput(); err != nil {
		t.Fatalf("go build: %v\n%s", err, out)
	}
	t.Setenv("HOME", t.TempDir()) // so nothing can fall back to the real home

	devNull := func(t *testing.T) *os.File {
		f, err := os.OpenFile(os.DevNull, os.O_RDWR, 0)
		if err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() { f.Close() })
		return f
	}
	wirings := map[string]func(t *testing.T, cmd *exec.Cmd){
		"stdin and stdout on /dev/null": func(t *testing.T, cmd *exec.Cmd) { cmd.Stdin, cmd.Stdout = devNull(t), devNull(t) },
		"stdin on /dev/null, stdout a pipe": func(t *testing.T, cmd *exec.Cmd) {
			cmd.Stdin, cmd.Stdout = devNull(t), &bytes.Buffer{}
		},
		"stdin a pipe, stdout on /dev/null": func(t *testing.T, cmd *exec.Cmd) {
			cmd.Stdin, cmd.Stdout = strings.NewReader("yes\n"), devNull(t)
		},
		"stdin and stdout pipes": func(t *testing.T, cmd *exec.Cmd) {
			cmd.Stdin, cmd.Stdout = strings.NewReader("yes\n"), &bytes.Buffer{}
		},
	}
	run := func(t *testing.T, e *env, wire func(*testing.T, *exec.Cmd), args ...string) (int, string) {
		t.Helper()
		cmd := exec.Command(bin, args...)
		cmd.Dir = e.repo
		cmd.Env = append(os.Environ(), "INTENTGATE_HOME="+e.state)
		var stderr bytes.Buffer
		cmd.Stderr = &stderr
		wire(t, cmd)
		code := 0
		if err := cmd.Run(); err != nil {
			ee, ok := err.(*exec.ExitError)
			if !ok {
				t.Fatal(err)
			}
			code = ee.ExitCode()
		}
		return code, stderr.String()
	}

	for name, wire := range wirings {
		t.Run("lock with "+name, func(t *testing.T) {
			e := newEnv(t, "dev@example.com")
			plan := e.write("plan.md", planText)
			prompt := e.write("prompt.txt", rootIntent)
			code, stderr := run(t, e, wire, "lock", "--from-plan", plan, "--prompt", prompt)
			want := "intentgate lock needs an interactive terminal; ratification is a human act"
			if code != 2 || !strings.Contains(stderr, want) {
				t.Errorf("exit %d, stderr %q; want 2 and %q", code, stderr, want)
			}
			e.nothingWritten()
		})
		t.Run("amend with "+name, func(t *testing.T) {
			e, id, path := lockedEnv(t)
			before, _ := os.ReadFile(path)
			code, stderr := run(t, e, wire, "amend", id, "--reratify")
			want := "intentgate amend needs an interactive terminal; ratification is a human act"
			if code != 2 || !strings.Contains(stderr, want) {
				t.Errorf("exit %d, stderr %q; want 2 and %q", code, stderr, want)
			}
			if after, _ := os.ReadFile(path); !bytes.Equal(before, after) {
				t.Error("the lock file changed")
			}
		})
	}
}

// ---- amend ----

const (
	oldRatifier = "git:old@example.com"
	oldSecret   = "Add SARIF output to the report package.\n"
)

// lockedEnv is an env with a lock already committed (as `lock` or the hook would have): C1 covers
// internal/report/**, C2 covers docs/guide/**. It returns the lock's id and its path.
func lockedEnv(t *testing.T) (*env, string, string) {
	t.Helper()
	e := newEnv(t, "dev@example.com")
	e.terminals(true, true)
	id, err := ids.NewULID(e.now, bytes.NewReader(bytes.Repeat([]byte{0x11}, 100)))
	if err != nil {
		t.Fatal(err)
	}
	drafted := "claude-code/claude-opus-5-5"
	l, masked, err := ratify.MaskAndBuild(nil, oldSecret, ratify.BuildInput{
		ID: id, BaseRef: "main", Source: "claude-code-session",
		Criteria: []lock.Criterion{
			{ID: "C1", Text: "Findings are published as SARIF", Footprint: []string{"internal/report/**"}},
			{ID: "C2", Text: "The guide explains SARIF", Footprint: []string{"docs/guide/**"}},
		},
		Ratifier: oldRatifier, RatifiedAt: e.now, Method: "hook", ReviewDurationMS: 1234, DraftedBy: drafted,
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := (ratify.Store{RepoRoot: e.repo, StateDir: e.state}).Write(l, masked); err != nil {
		t.Fatal(err)
	}
	return e, id, filepath.Join(e.repo, ".intent", id+".yml")
}

func (e *env) record(id string) []byte {
	e.t.Helper()
	b, err := os.ReadFile(filepath.Join(e.state, "locks", id, "root-intent.md"))
	if err != nil {
		e.t.Fatal(err)
	}
	return b
}

// shellSplit splits a POSIX shell command line the way a shell would for the quoting footprint
// findings use: single quotes, and nothing else.
func shellSplit(t *testing.T, s string) []string {
	t.Helper()
	var args []string
	var cur strings.Builder
	inWord, inQuote := false, false
	for _, r := range s {
		switch {
		case inQuote && r == '\'':
			inQuote = false
		case inQuote:
			cur.WriteRune(r)
		case r == '\'':
			inQuote, inWord = true, true
		case r == ' ':
			if inWord {
				args = append(args, cur.String())
				cur.Reset()
				inWord = false
			}
		default:
			cur.WriteRune(r)
			inWord = true
		}
	}
	if inQuote {
		t.Fatalf("unterminated quote in %q", s)
	}
	if inWord {
		args = append(args, cur.String())
	}
	return args
}

// fixCommand is the exact command a footprint finding prints for a changed path.
func fixCommand(t *testing.T, e *env, id, changed string) string {
	t.Helper()
	l := e.readLock(filepath.Join(e.repo, ".intent", id+".yml"))
	var cs []footprint.Criterion
	for _, c := range l.Criteria {
		cs = append(cs, footprint.Criterion{LockID: l.ID, ID: c.ID, Footprint: c.Footprint})
	}
	implicit, err := footprint.NewImplicit(footprint.Config{}, nil)
	if err != nil {
		t.Fatal(err)
	}
	fs := footprint.Check([]footprint.ChangedFile{{Path: changed, Status: footprint.Modified}}, cs, implicit)
	if len(fs) != 1 || fs[0].Fix == "" {
		t.Fatalf("footprint findings for %s: %+v", changed, fs)
	}
	return fs[0].Fix
}

// REQ-0030
func TestAmend_AddGlob(t *testing.T) {
	e, id, path := lockedEnv(t)
	old := e.readLock(path)
	recBefore := e.record(id)

	fix := fixCommand(t, e, id, "docs/api/overview.md")
	if want := "intentgate amend " + id + " --criterion C2 --add 'docs/api/**'"; fix != want {
		t.Fatalf("the finding's fix = %q, want %q", fix, want)
	}
	args := shellSplit(t, fix)[1:] // drop the program name
	e.now = e.now.Add(time.Hour)
	code, stdout, stderr := e.do(args, "yes\n")
	if code != 0 {
		t.Fatalf("exit %d, stderr %q, stdout %q", code, stderr, stdout)
	}
	if !strings.Contains(stdout, "Type yes to amend this lock:") || !strings.Contains(stdout, "docs/api/**") || !strings.Contains(stdout, "C2") {
		t.Errorf("stdout does not show the change and the prompt:\n%s", stdout)
	}

	l := e.readLock(path)
	if ps := lock.Validate(l); len(ps) != 0 {
		t.Errorf("lock.Validate: %v", ps)
	}
	if !lock.VerifyHash(l) || l.Hash == old.Hash {
		t.Errorf("hash %s (old %s) does not verify or did not change", l.Hash, old.Hash)
	}
	if l.Revision != 2 {
		t.Errorf("revision = %d, want 2", l.Revision)
	}
	if want := []string{"docs/guide/**", "docs/api/**"}; !reflect.DeepEqual(l.Criteria[1].Footprint, want) {
		t.Errorf("C2 footprint = %v, want %v", l.Criteria[1].Footprint, want)
	}
	if !reflect.DeepEqual(l.Criteria[0], old.Criteria[0]) {
		t.Errorf("C1 changed: %+v", l.Criteria[0])
	}
	wantAmendment := lock.Amendment{
		Revision: 2, Kind: "add-glob", Criterion: "C2", Glob: "docs/api/**",
		By: "git:dev@example.com", At: e.now.Format("2006-01-02T15:04:05Z"), Method: "cli-confirm", // e.now is the moment of the answer
	}
	if !reflect.DeepEqual(l.Amendments, []lock.Amendment{wantAmendment}) {
		t.Errorf("amendments = %+v, want [%+v]", l.Amendments, wantAmendment)
	}
	if !reflect.DeepEqual(l.Ratification, old.Ratification) || !reflect.DeepEqual(l.RootIntent, old.RootIntent) || l.ID != id {
		t.Errorf("ratification, root intent or id changed: %+v", l)
	}
	if got := e.record(id); !bytes.Equal(got, recBefore) {
		t.Errorf("the root intent record changed: %q", got)
	}
	if entries, _ := os.ReadDir(filepath.Dir(path)); len(entries) != 1 {
		t.Errorf(".intent holds %d entries, want just the lock", len(entries))
	}

	t.Run("a footprint finding for the amended path is gone", func(t *testing.T) {
		implicit, _ := footprint.NewImplicit(footprint.Config{}, nil)
		var cs []footprint.Criterion
		for _, c := range l.Criteria {
			cs = append(cs, footprint.Criterion{LockID: l.ID, ID: c.ID, Footprint: c.Footprint})
		}
		if fs := footprint.Check([]footprint.ChangedFile{{Path: "docs/api/overview.md", Status: footprint.Modified}}, cs, implicit); len(fs) != 0 {
			t.Errorf("findings after the amendment: %+v", fs)
		}
	})

	t.Run("refusals leave the file unchanged", func(t *testing.T) {
		before, _ := os.ReadFile(path)
		for name, tc := range map[string][]string{
			"duplicate glob":               {"amend", id, "--criterion", "C2", "--add", "docs/api/**"},
			"duplicate of the original":    {"amend", id, "--criterion", "C1", "--add", "internal/report/**"},
			"unknown criterion":            {"amend", id, "--criterion", "C9", "--add", "docs/x/**"},
			"bad glob":                     {"amend", id, "--criterion", "C1", "--add", "docs/[x"},
			"empty glob":                   {"amend", id, "--criterion", "C1", "--add", ""},
			"bare !":                       {"amend", id, "--criterion", "C1", "--add", "!"},
			"--add without --criterion":    {"amend", id, "--add", "docs/x/**"},
			"--criterion without --add":    {"amend", id, "--criterion", "C1"},
			"neither --add nor --reratify": {"amend", id},
			"--add and --reratify":         {"amend", id, "--criterion", "C1", "--add", "docs/x/**", "--reratify"},
			"id that is not a ULID":        {"amend", "../" + id, "--reratify"},
			"id prefix":                    {"amend", id[:8], "--reratify"},
			"unknown flag":                 {"amend", id, "--reratify", "--yes"},
		} {
			code, _, stderr := e.do(tc, "yes\n")
			if code != 2 {
				t.Errorf("%s: exit %d, stderr %q; want 2", name, code, stderr)
			}
			if after, _ := os.ReadFile(path); !bytes.Equal(before, after) {
				t.Fatalf("%s: the lock file changed", name)
			}
		}
	})

	t.Run("a second amendment is revision 3", func(t *testing.T) {
		if code, _, stderr := e.do([]string{"amend", id, "--criterion", "C1", "--add", "!internal/report/legacy/**"}, "yes\n"); code != 0 {
			t.Fatalf("exit %d: %s", code, stderr)
		}
		l := e.readLock(path)
		if l.Revision != 3 || len(l.Amendments) != 2 || l.Amendments[1].Revision != 3 || !lock.VerifyHash(l) || len(lock.Validate(l)) != 0 {
			t.Errorf("lock after the second amendment: %+v, problems %v", l, lock.Validate(l))
		}
	})

	t.Run("a lock that does not exist is an error", func(t *testing.T) {
		other, _ := ids.NewULID(t0.Add(time.Hour*24), bytes.NewReader(bytes.Repeat([]byte{0x22}, 100)))
		if code, _, _ := e.do([]string{"amend", other, "--reratify"}, "yes\n"); code != 3 {
			t.Errorf("exit %d, want 3", code)
		}
	})
}

func TestAmend_Reratify(t *testing.T) {
	e, id, path := lockedEnv(t)
	old := e.readLock(path)
	recBefore := e.record(id)

	e.now = t0.Add(15 * 24 * time.Hour)
	if lock.ValidAt(old, e.now) {
		t.Fatal("setup: the lock is still valid after 15 days")
	}
	gitIn(t, e.repo, "config", "user.email", "new@example.com")

	code, stdout, stderr := e.do([]string{"amend", id, "--reratify"}, "yes\n")
	if code != 0 {
		t.Fatalf("exit %d, stderr %q, stdout %q", code, stderr, stdout)
	}
	if !strings.Contains(stdout, "Type yes to re-ratify this lock:") {
		t.Errorf("stdout lacks the prompt:\n%s", stdout)
	}
	// Review 1 of S-05: the old time is shown as the old one; the new one is the time of confirmation.
	if !strings.Contains(stdout, "ratified_at was "+old.Ratification.RatifiedAt) ||
		!strings.Contains(stdout, "the new ratified_at is the time of confirmation") ||
		strings.Contains(stdout, "ratified_at is now") {
		t.Errorf("stdout does not show the old ratified_at as old and the new as the time of confirmation:\n%s", stdout)
	}
	l := e.readLock(path)
	if ps := lock.Validate(l); len(ps) != 0 || !lock.VerifyHash(l) {
		t.Errorf("problems %v, hash ok %v", ps, lock.VerifyHash(l))
	}
	if !lock.ValidAt(l, e.now) {
		t.Errorf("the lock is not valid at %s: ratified_at %s", e.now, l.Ratification.RatifiedAt)
	}
	wantAt := t0.Add(15*24*time.Hour + confirmDelay).Format("2006-01-02T15:04:05Z")
	r := l.Ratification
	if r.RatifiedAt != wantAt || r.Ratifier != "git:new@example.com" || r.Method != "cli-confirm" {
		t.Errorf("ratification = %+v, want ratified_at %s by git:new@example.com", r, wantAt)
	}
	if r.ReviewDurationMS == nil || *r.ReviewDurationMS != int(confirmDelay.Milliseconds()) {
		t.Errorf("review_duration_ms = %v, want %d", r.ReviewDurationMS, confirmDelay.Milliseconds())
	}
	if r.DraftedBy != nil {
		t.Errorf("drafted_by = %q, want it dropped", *r.DraftedBy)
	}
	if l.Revision != 2 || !reflect.DeepEqual(l.Criteria, old.Criteria) || !reflect.DeepEqual(l.RootIntent, old.RootIntent) {
		t.Errorf("revision %d, criteria %+v, root intent %+v", l.Revision, l.Criteria, l.RootIntent)
	}
	want := lock.Amendment{Revision: 2, Kind: "reratify", By: "git:new@example.com", At: wantAt, Method: "cli-confirm"}
	if !reflect.DeepEqual(l.Amendments, []lock.Amendment{want}) {
		t.Errorf("amendments = %+v, want [%+v]", l.Amendments, want)
	}
	if got := e.record(id); !bytes.Equal(got, recBefore) {
		t.Errorf("the root intent record changed: %q", got)
	}
}

// REQ-0005
func TestAmend_RequiresTerminalAndYes(t *testing.T) {
	for _, mode := range []string{"add-glob", "reratify"} {
		t.Run(mode, func(t *testing.T) {
			e, id, path := lockedEnv(t)
			before, _ := os.ReadFile(path)
			args := []string{"amend", id, "--reratify"}
			if mode == "add-glob" {
				args = []string{"amend", id, "--criterion", "C1", "--add", "internal/other/**"}
			}
			unchanged := func(what string) {
				t.Helper()
				if after, _ := os.ReadFile(path); !bytes.Equal(before, after) {
					t.Errorf("%s: the lock file changed", what)
				}
			}

			for name, tty := range map[string][2]bool{"stdin": {false, true}, "stdout": {true, false}, "neither": {false, false}} {
				e.terminals(tty[0], tty[1])
				code, _, stderr := e.do(args, "yes\n")
				if code != 2 || !strings.Contains(stderr, "intentgate amend needs an interactive terminal; ratification is a human act") {
					t.Errorf("%s not a terminal: exit %d, stderr %q", name, code, stderr)
				}
				unchanged(name + " not a terminal")
			}
			e.deps.IsTerminal = nil
			if code, _, _ := e.do(args, "yes\n"); code != 2 {
				t.Errorf("no terminal check: exit %d, want 2", code)
			}
			unchanged("no terminal check")

			e.terminals(true, true)
			for _, answer := range [][]string{{"no\n"}, {"y\n"}, {"YES \n"}, {"\n"}, {}} {
				if code, _, _ := e.do(args, answer...); code != 1 {
					t.Errorf("answer %q: exit %d, want 1", answer, code)
				}
				unchanged(fmt.Sprintf("answer %q", answer))
			}

			if code, _, stderr := e.do(args, "yes\n"); code != 0 {
				t.Fatalf("yes: exit %d, stderr %q", code, stderr)
			}
			if l := e.readLock(path); l.Revision != 2 {
				t.Errorf("revision after yes = %d, want 2", l.Revision)
			}
		})
	}
}

func TestAmend_RefusesTamperedLock(t *testing.T) {
	for name, edit := range map[string]func(string) string{
		"criterion text edited": func(s string) string { return strings.Replace(s, "published as SARIF", "published as JSON", 1) },
		"ratified_at moved":     func(s string) string { return strings.Replace(s, "2026-11-18T09:14:03Z", "2026-11-30T09:14:03Z", 1) },
		"hash edited":           func(s string) string { return strings.Replace(s, "hash: sha256:", "hash: sha256:0", 1) },
		"does not validate":     func(s string) string { return strings.Replace(s, "method: hook", "method: telepathy", 1) },
		"not a lock":            func(s string) string { return "not: a lock\n" },
	} {
		for _, args := range [][]string{{"--reratify"}, {"--criterion", "C1", "--add", "docs/x/**"}} {
			t.Run(name+" "+args[0], func(t *testing.T) {
				e, id, path := lockedEnv(t)
				data, _ := os.ReadFile(path)
				tampered := edit(string(data))
				if tampered == string(data) {
					t.Fatalf("the edit changed nothing in:\n%s", data)
				}
				if err := os.WriteFile(path, []byte(tampered), 0o644); err != nil {
					t.Fatal(err)
				}
				recBefore := e.record(id)
				code, _, stderr := e.do(append([]string{"amend", id}, args...), "yes\n")
				if code != 3 {
					t.Errorf("exit %d, stderr %q; want 3", code, stderr)
				}
				if after, _ := os.ReadFile(path); string(after) != tampered {
					t.Errorf("the lock file changed:\n%s", after)
				}
				if !bytes.Equal(e.record(id), recBefore) {
					t.Error("the root intent record changed")
				}
			})
		}
	}
}
