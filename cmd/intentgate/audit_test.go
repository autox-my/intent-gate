package main

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"intentgate/internal/audit"
	"intentgate/internal/lock"
)

// The audit tests run the real command line against temporary repositories and a temporary state
// directory (INTENTGATE_HOME's role); nothing here touches the real ~/.intentgate.

const configYML = "version: 1\n"

// unsetCI removes $CI for the test: a run on a developer's machine, not in CI.
func unsetCI(t *testing.T) {
	t.Helper()
	t.Setenv("CI", "")
	if err := os.Unsetenv("CI"); err != nil {
		t.Fatal(err)
	}
}

type fakeClock struct {
	at    time.Time
	reads int
}

func (c *fakeClock) now() time.Time { c.reads++; return c.at }

func sha256hex(b []byte) string { s := sha256.Sum256(b); return hex.EncodeToString(s[:]) }

// auditRepo is a repository whose feature branch adds one valid lock, a config and a source file.
func auditRepo(t *testing.T) (*vrepo, lock.Lock) {
	t.Helper()
	r := newVRepo(t)
	r.feature()
	l, data := mkLock(t, lockA, "main", ratifiedOK, crit("C1", "The command is implemented", "src/**", ".intentgate.yml"))
	r.write(lockPath(lockA), string(data))
	r.write(".intentgate.yml", configYML)
	r.write("src/a.go", "package src\n")
	r.commit("head", headDate)
	return r, l
}

// verifyAt runs verify in the repository with the given clock and state directory.
func (r *vrepo) verifyAt(clock *fakeClock, state string, args ...string) (code int, stdout, stderr string) {
	r.t.Helper()
	var out, errb bytes.Buffer
	deps := Deps{Now: clock.now, StateDir: state, Dir: r.dir, Stderr: &errb}
	code = Run(append([]string{"verify"}, args...), strings.NewReader(""), &out, deps)
	return code, out.String(), errb.String()
}

func (r *vrepo) args(outDir string, extra ...string) []string {
	args := []string{"--base", "main", "--head", "feature", "--base-branch", "main", "--author", "octocat", "--author-type", "User"}
	if outDir != "" {
		args = append(args, "--out-dir", outDir)
	}
	return append(args, extra...)
}

func decodeEvent(t *testing.T, data []byte) audit.Event {
	t.Helper()
	var e audit.Event
	if err := json.Unmarshal(data, &e); err != nil {
		t.Fatalf("event %q: %v", data, err)
	}
	return e
}

// auditLog reads <state>/audit.jsonl: one event per line, file mode 0600.
func auditLog(t *testing.T, state string) []audit.Event {
	t.Helper()
	path := filepath.Join(state, "audit.jsonl")
	data, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return nil
		}
		t.Fatal(err)
	}
	if info, err := os.Stat(path); err != nil || info.Mode().Perm() != 0o600 {
		t.Errorf("%s mode %v, %v; want 0600", path, info.Mode(), err)
	}
	if !bytes.HasSuffix(data, []byte("\n")) {
		t.Errorf("audit.jsonl does not end in a newline: %q", data)
	}
	var events []audit.Event
	for _, line := range strings.Split(strings.TrimSuffix(string(data), "\n"), "\n") {
		events = append(events, decodeEvent(t, []byte(line)))
	}
	return events
}

// REQ-0042, REQ-0040
func TestVerify_AuditEvent(t *testing.T) {
	unsetCI(t)
	r, l := auditRepo(t)
	base, head := r.git("rev-parse", "main"), r.git("rev-parse", "feature")
	state := filepath.Join(t.TempDir(), "state")
	c1 := &fakeClock{at: time.Date(2026, 11, 19, 8, 0, 0, 500, time.UTC)}
	out1 := filepath.Join(t.TempDir(), "out")

	code, stdout1, stderr := r.verifyAt(c1, state, r.args(out1, "--repo", "owner/name", "--pr", "42")...)
	if code != 0 || stderr != "" {
		t.Fatalf("exit %d, stderr %q, stdout %q; want 0 and no stderr", code, stderr, stdout1)
	}
	auditJSON := readOut(t, out1, "audit.json")
	ev := decodeEvent(t, auditJSON)
	verifyBytes := readOut(t, out1, "verify.json")

	t.Run("audit.json records the refs, the locks' methods and the hash of verify.json", func(t *testing.T) {
		if ev.Schema != "intentgate.audit/1" || ev.Event != "verify" || ev.At != "2026-11-19T08:00:00Z" {
			t.Errorf("schema %q, event %q, at %q", ev.Schema, ev.Event, ev.At)
		}
		if ev.Repo != "owner/name" || ev.PR != 42 || ev.BaseSHA != base || ev.HeadSHA != head || ev.BaseBranch != "main" ||
			ev.Author != "octocat" || ev.AuthorType != "User" {
			t.Errorf("event = %+v", ev)
		}
		if ev.VerifySHA256 != sha256hex(verifyBytes) {
			t.Errorf("verify_sha256 = %s, want the SHA-256 of verify.json, %s", ev.VerifySHA256, sha256hex(verifyBytes))
		}
		if ev.ConfigSHA256 != sha256hex([]byte(configYML)) {
			t.Errorf("config_sha256 = %s, want the SHA-256 of .intentgate.yml", ev.ConfigSHA256)
		}
		if ev.Outcome != "clean" || ev.ExitCode != 0 || ev.Conclusion != "success" || ev.IntentGateVersion != version {
			t.Errorf("outcome %q exit %d conclusion %q version %q", ev.Outcome, ev.ExitCode, ev.Conclusion, ev.IntentGateVersion)
		}
		if len(ev.Locks) != 1 {
			t.Fatalf("locks = %+v, want one", ev.Locks)
		}
		got := ev.Locks[0]
		if got.ID != lockA || got.Revision != 1 || got.Hash != l.Hash || got.RatificationMethod != "hook" ||
			got.ReviewDurationMS == nil || *got.ReviewDurationMS != 48210 {
			t.Errorf("lock entry = %+v, want %s revision 1 %s method hook 48210 ms", got, lockA, l.Hash)
		}
		if bytes.Contains(auditJSON, []byte("The command is implemented")) || bytes.Contains(auditJSON, []byte("src/**")) {
			t.Errorf("audit.json holds criterion text or a footprint: %s", auditJSON)
		}
	})

	t.Run("CI unset: the same event is appended as one line to <state>/audit.jsonl", func(t *testing.T) {
		data, err := os.ReadFile(filepath.Join(state, "audit.jsonl"))
		if err != nil {
			t.Fatal(err)
		}
		if !bytes.Equal(data, auditJSON) {
			t.Errorf("audit.jsonl = %q, want the audit.json bytes %q", data, auditJSON)
		}
		if info, err := os.Stat(state); err != nil || info.Mode().Perm() != 0o700 {
			t.Errorf("state dir mode %v, %v; want 0700", info.Mode(), err)
		}
		if log := auditLog(t, state); len(log) != 1 {
			t.Errorf("audit.jsonl has %d events, want 1", len(log))
		}
	})

	t.Run("--repo and --pr change no decision and no output; only the clock moves audit.json", func(t *testing.T) {
		c2 := &fakeClock{at: c1.at.Add(90 * time.Minute)}
		out2 := filepath.Join(t.TempDir(), "out")
		code, stdout2, stderr := r.verifyAt(c2, state, r.args(out2)...)
		if code != 0 || stderr != "" {
			t.Fatalf("exit %d, stderr %q", code, stderr)
		}
		for _, f := range []string{"verify.json", "results.sarif", "summary.md", "comment.md", "decision.json"} {
			if !bytes.Equal(readOut(t, out1, f), readOut(t, out2, f)) {
				t.Errorf("%s differs between two runs", f)
			}
		}
		if stdout1 != stdout2 {
			t.Errorf("stdout differs:\n%s\n---\n%s", stdout1, stdout2)
		}
		ev2 := decodeEvent(t, readOut(t, out2, "audit.json"))
		if ev2.At != "2026-11-19T09:30:00Z" || ev2.At == ev.At {
			t.Errorf("second at = %q, first %q; want them to differ", ev2.At, ev.At)
		}
		ev2.At, ev2.Repo, ev2.PR = ev.At, ev.Repo, ev.PR
		if !bytes.Equal(audit.JSON(ev2), auditJSON) {
			t.Errorf("audit.json differs in more than at, repo and pr:\n%s\n%s", audit.JSON(ev2), auditJSON)
		}
		if log := auditLog(t, state); len(log) != 2 {
			t.Errorf("audit.jsonl has %d events after two runs, want 2", len(log))
		}
	})

	t.Run("without --out-dir the event still goes to the log, and verify.json is not written", func(t *testing.T) {
		s := filepath.Join(t.TempDir(), "state")
		code, _, stderr := r.verifyAt(&fakeClock{at: c1.at}, s, r.args("")...)
		if code != 0 || stderr != "" {
			t.Fatalf("exit %d, stderr %q", code, stderr)
		}
		if log := auditLog(t, s); len(log) != 1 || log[0].VerifySHA256 != ev.VerifySHA256 {
			t.Errorf("audit.jsonl = %+v, want the one event with verify_sha256 %s", log, ev.VerifySHA256)
		}
	})

	t.Run("CI=true: audit.json is written, audit.jsonl is not", func(t *testing.T) {
		t.Setenv("CI", "true")
		s := filepath.Join(t.TempDir(), "state")
		out := filepath.Join(t.TempDir(), "out")
		if code, _, stderr := r.verifyAt(&fakeClock{at: c1.at}, s, r.args(out)...); code != 0 || stderr != "" {
			t.Fatalf("exit %d, stderr %q", code, stderr)
		}
		readOut(t, out, "audit.json")
		if _, err := os.Stat(filepath.Join(s, "audit.jsonl")); err == nil {
			t.Error("audit.jsonl exists in CI")
		}
	})

	t.Run("a log that cannot be appended to is a warning, never a different exit code", func(t *testing.T) {
		s := filepath.Join(t.TempDir(), "state")
		if err := os.MkdirAll(filepath.Join(s, "audit.jsonl"), 0o700); err != nil { // a directory where the log should be
			t.Fatal(err)
		}
		out := filepath.Join(t.TempDir(), "out")
		code, stdout, stderr := r.verifyAt(&fakeClock{at: c1.at}, s, r.args(out)...)
		if code != 0 || stdout != stdout1 {
			t.Errorf("exit %d, stdout %q; want 0 and the usual output", code, stdout)
		}
		if !strings.Contains(stderr, "audit") {
			t.Errorf("stderr %q has no warning about the audit log", stderr)
		}
		readOut(t, out, "audit.json")
	})
}

// REQ-0042
func TestVerify_Replay(t *testing.T) {
	unsetCI(t)
	r, _ := auditRepo(t)
	out := filepath.Join(t.TempDir(), "out")
	if code, _, stderr := r.verifyAt(&fakeClock{at: time.Date(2026, 11, 19, 8, 0, 0, 0, time.UTC)}, "", r.args(out)...); code != 0 {
		t.Fatalf("recording run: exit %d, stderr %q", code, stderr)
	}
	eventPath := filepath.Join(out, "audit.json")
	ev := decodeEvent(t, readOut(t, out, "audit.json"))
	writeEvent := func(t *testing.T, e audit.Event) string {
		t.Helper()
		p := filepath.Join(t.TempDir(), "event.json")
		if err := os.WriteFile(p, audit.JSON(e), 0o644); err != nil {
			t.Fatal(err)
		}
		return p
	}
	replay := func(path string, extra ...string) (code int, stdout, stderr string, clock *fakeClock, state string) {
		clock = &fakeClock{at: time.Date(2030, 1, 1, 0, 0, 0, 0, time.UTC)}
		state = filepath.Join(t.TempDir(), "state")
		code, stdout, stderr = r.verifyAt(clock, state, append([]string{"--replay", path}, extra...)...)
		return
	}

	t.Run("a recorded event reproduces: exit 0, and no clock, no new event", func(t *testing.T) {
		code, stdout, stderr, clock, state := replay(eventPath)
		if code != 0 || !strings.Contains(stdout, "replay: identical") {
			t.Errorf("exit %d, stdout %q, stderr %q; want 0 and \"replay: identical\"", code, stdout, stderr)
		}
		if clock.reads != 0 {
			t.Errorf("replay read the clock %d times", clock.reads)
		}
		if _, err := os.Stat(filepath.Join(state, "audit.jsonl")); err == nil {
			t.Error("replay wrote an audit event")
		}
	})

	t.Run("a tampered verify_sha256 differs: exit 1, the fresh verify.json on stdout", func(t *testing.T) {
		bad := ev
		bad.VerifySHA256 = strings.Repeat("0", 64)
		code, stdout, stderr, _, _ := replay(writeEvent(t, bad))
		if code != 1 || !strings.Contains(stderr, "replay: differs") {
			t.Errorf("exit %d, stderr %q; want 1 and \"replay: differs\"", code, stderr)
		}
		if want := string(readOut(t, out, "verify.json")); stdout != want {
			t.Errorf("stdout = %q, want the fresh verify.json %q", stdout, want)
		}
	})

	t.Run("an event naming a sha the repository lacks: exit 3", func(t *testing.T) {
		bad := ev
		bad.HeadSHA = strings.Repeat("0", 40)
		if code, _, stderr, _, _ := replay(writeEvent(t, bad)); code != 3 {
			t.Errorf("exit %d, stderr %q; want 3", code, stderr)
		}
	})

	t.Run("a file that is not a verify event: exit 3", func(t *testing.T) {
		notJSON := filepath.Join(t.TempDir(), "x.json")
		if err := os.WriteFile(notJSON, []byte("nope"), 0o644); err != nil {
			t.Fatal(err)
		}
		lockEv := ev
		lockEv.Event = "lock"
		for name, path := range map[string]string{"not JSON": notJSON, "a lock event": writeEvent(t, lockEv), "missing": filepath.Join(t.TempDir(), "none.json")} {
			if code, _, stderr, _, _ := replay(path); code != 3 {
				t.Errorf("%s: exit %d, stderr %q; want 3", name, code, stderr)
			}
		}
	})

	t.Run("--replay excludes every other input flag: exit 2", func(t *testing.T) {
		for _, extra := range [][]string{
			{"--base", "main"}, {"--head", "feature"}, {"--base-branch", "main"}, {"--author", "x"}, {"--author-type", "User"},
			{"--format", "json"}, {"--out-dir", filepath.Join(t.TempDir(), "o")}, {"--repo", "o/n"}, {"--pr", "1"},
		} {
			if code, _, stderr, _, _ := replay(eventPath, extra...); code != 2 {
				t.Errorf("--replay with %v: exit %d, stderr %q; want 2", extra, code, stderr)
			}
		}
	})
}

// REQ-0042
func TestLockAndAmend_AuditLog(t *testing.T) {
	t.Run("lock --from-plan appends one event with the lock's method, then amend appends another", func(t *testing.T) {
		e := newEnv(t, "dev@example.com")
		e.terminals(true, true)
		plan := e.write("plan.md", planText)
		prompt := e.write("prompt.txt", rootIntent)
		if code, _, stderr := e.do([]string{"lock", "--from-plan", plan, "--prompt", prompt}, "yes\n"); code != 0 || stderr != "" {
			t.Fatalf("lock: exit %d, stderr %q", code, stderr)
		}
		l, _ := e.onlyLock()
		log := auditLog(t, e.state)
		if len(log) != 1 {
			t.Fatalf("audit.jsonl = %+v, want one event", log)
		}
		ev := log[0]
		if ev.Schema != "intentgate.audit/1" || ev.Event != "lock" || ev.Outcome != "written" || ev.Repo != "" || ev.At != l.Ratification.RatifiedAt {
			t.Errorf("event = %+v (ratified_at %s)", ev, l.Ratification.RatifiedAt)
		}
		if len(ev.Locks) != 1 || ev.Locks[0].ID != l.ID || ev.Locks[0].Revision != 1 || ev.Locks[0].Hash != l.Hash ||
			ev.Locks[0].RatificationMethod != "cli-confirm" || ev.Locks[0].ReviewDurationMS == nil || *ev.Locks[0].ReviewDurationMS != int(confirmDelay.Milliseconds()) {
			t.Errorf("lock entries = %+v, want %s (%s) cli-confirm %d ms", ev.Locks, l.ID, l.Hash, confirmDelay.Milliseconds())
		}

		gitIn(t, e.repo, "remote", "add", "origin", "git@github.com:owner/name.git")
		if code, _, stderr := e.do([]string{"amend", l.ID, "--reratify"}, "yes\n"); code != 0 || stderr != "" {
			t.Fatalf("amend: exit %d, stderr %q", code, stderr)
		}
		next := e.readLock(filepath.Join(e.repo, ".intent", l.ID+".yml"))
		log = auditLog(t, e.state)
		if len(log) != 2 {
			t.Fatalf("audit.jsonl has %d events, want 2", len(log))
		}
		am := log[1]
		if am.Event != "amend" || am.Repo != "owner/name" || am.Outcome != "written" || am.At != next.Amendments[0].At ||
			len(am.Locks) != 1 || am.Locks[0].Revision != 2 || am.Locks[0].Hash != next.Hash || am.Locks[0].RatificationMethod != "cli-confirm" {
			t.Errorf("amend event = %+v, lock %+v", am, next)
		}
	})

	t.Run("amend --add keeps the lock's own method", func(t *testing.T) {
		e, id, path := lockedEnv(t)
		if code, _, stderr := e.do([]string{"amend", id, "--criterion", "C1", "--add", "docs/api/**"}, "yes\n"); code != 0 {
			t.Fatalf("exit %d, stderr %q", code, stderr)
		}
		log := auditLog(t, e.state)
		next := e.readLock(path)
		if len(log) != 1 || log[0].Event != "amend" || len(log[0].Locks) != 1 || log[0].Locks[0].RatificationMethod != "hook" ||
			log[0].Locks[0].Revision != 2 || log[0].Locks[0].Hash != next.Hash {
			t.Errorf("audit.jsonl = %+v, want one amend event for revision 2 (%s) with method hook", log, next.Hash)
		}
	})

	t.Run("the hook's post appends one event with method hook", func(t *testing.T) {
		e := newEnv(t, "dev@example.com")
		e.pre()
		e.now = t0.Add(12345 * time.Millisecond)
		if code, _ := e.run("post", e.input("PostToolUse", planText, "ExitPlanMode")); code != 0 {
			t.Fatalf("post exit %d", code)
		}
		l, _ := e.onlyLock()
		log := auditLog(t, e.state)
		if len(log) != 1 || log[0].Event != "lock" || len(log[0].Locks) != 1 {
			t.Fatalf("audit.jsonl = %+v, want one lock event", log)
		}
		got := log[0].Locks[0]
		if got.ID != l.ID || got.Hash != l.Hash || got.RatificationMethod != "hook" || got.ReviewDurationMS == nil || *got.ReviewDurationMS != 12345 {
			t.Errorf("lock entry = %+v, want %s %s hook 12345 ms", got, l.ID, l.Hash)
		}
	})

	t.Run("a log that cannot be appended to is a warning: exit code and hook stdout unchanged", func(t *testing.T) {
		blockLog := func(t *testing.T, e *env) {
			t.Helper()
			if err := os.MkdirAll(filepath.Join(e.state, "audit.jsonl"), 0o700); err != nil { // a directory where the log should be
				t.Fatal(err)
			}
		}
		t.Run("lock", func(t *testing.T) {
			e := newEnv(t, "dev@example.com")
			e.terminals(true, true)
			blockLog(t, e)
			plan := e.write("plan.md", planText)
			prompt := e.write("prompt.txt", rootIntent)
			code, stdout, stderr := e.do([]string{"lock", "--from-plan", plan, "--prompt", prompt}, "yes\n")
			if code != 0 || !strings.Contains(stdout, "intent lock written") || !strings.Contains(stderr, "audit") {
				t.Errorf("exit %d, stdout %q, stderr %q; want 0, the lock written and a warning about the audit log", code, stdout, stderr)
			}
			e.onlyLock()
		})
		t.Run("amend", func(t *testing.T) {
			e, id, _ := lockedEnv(t)
			blockLog(t, e)
			code, stdout, stderr := e.do([]string{"amend", id, "--reratify"}, "yes\n")
			if code != 0 || !strings.Contains(stdout, "intent lock amended") || !strings.Contains(stderr, "audit") {
				t.Errorf("exit %d, stdout %q, stderr %q; want 0, the lock amended and a warning", code, stdout, stderr)
			}
		})
		t.Run("hook post", func(t *testing.T) {
			e := newEnv(t, "dev@example.com")
			e.pre()
			blockLog(t, e)
			var out bytes.Buffer
			errb := &bytes.Buffer{}
			e.deps.Stderr = errb
			code := Run([]string{"hook", "claude-code", "post"}, strings.NewReader(e.input("PostToolUse", planText, "ExitPlanMode")), &out, e.deps)
			var obj map[string]string
			dec := json.NewDecoder(bytes.NewReader(out.Bytes()))
			if err := dec.Decode(&obj); err != nil || dec.More() {
				t.Fatalf("stdout %q is not one JSON object: %v", out.String(), err)
			}
			if code != 0 || !strings.HasPrefix(obj["systemMessage"], "IntentGate: intent lock written") || !strings.Contains(errb.String(), "audit") {
				t.Errorf("exit %d, stdout %q, stderr %q; want 0, the lock written and a warning", code, out.String(), errb.String())
			}
			e.onlyLock()
		})
	})
}
