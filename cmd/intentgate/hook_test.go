package main

import (
	"bytes"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/autox-my/intent-gate/internal/ids"
	"github.com/autox-my/intent-gate/internal/lock"
)

// The transcript and the plan are synthetic: committed fixtures never hold real session content.

var t0 = time.Date(2026, 11, 18, 9, 14, 3, 0, time.UTC)

const planText = "# Plan\n\nAdd SARIF output.\n\n```intentgate\ncriteria:\n" +
	"  - text: \"Findings are published as SARIF\"\n    footprint: [\"internal/report/**\"]\n" +
	"  - text: \"The report package has tests\"\n    footprint: [\"internal/report/**\", \"!internal/report/testdata/**\"]\n```\n"

const otherPlanText = "# Plan B\n\n```intentgate\ncriteria:\n" +
	"  - text: \"Only this one\"\n    footprint: [\"cmd/**\"]\n```\n"

var transcript = strings.Join([]string{
	`{"type":"user","message":{"role":"user","content":"Please add SARIF output."}}`,
	`{"type":"assistant","message":{"role":"assistant","model":"claude-opus-5-5","content":[{"type":"text","text":"Here is a plan."}]}}`,
	`{"type":"user","isMeta":true,"message":{"role":"user","content":"meta"}}`,
	`{"type":"user","message":{"role":"user","content":[{"type":"text","text":"Keep it small."}]}}`,
}, "\n") + "\n"

const wantRootIntent = "Please add SARIF output.\n\n---\n\nKeep it small."

// env is one temp repository, one state directory, and the hook's injected dependencies.
type env struct {
	t     *testing.T
	repo  string
	state string
	now   time.Time
	deps  Deps
	tpath string
}

func gitIn(t *testing.T, dir string, args ...string) {
	t.Helper()
	cmd := exec.Command("git", args...)
	cmd.Dir = dir
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("git %s: %v\n%s", strings.Join(args, " "), err, out)
	}
}

// newEnv makes a repository on main with a first commit. email "" leaves user.email unset.
func newEnv(t *testing.T, email string) *env {
	t.Helper()
	t.Setenv("GIT_CONFIG_GLOBAL", os.DevNull)
	t.Setenv("GIT_CONFIG_NOSYSTEM", "1")
	t.Setenv("INTENTGATE_BASE_REF", "")
	e := &env{t: t, repo: t.TempDir(), state: t.TempDir(), now: t0}
	gitIn(t, e.repo, "init", "-q", "-b", "main")
	gitIn(t, e.repo, "-c", "user.name=t", "-c", "user.email=t@example.com", "commit", "-q", "--allow-empty", "-m", "first")
	if email != "" {
		gitIn(t, e.repo, "config", "user.email", email)
	}
	if err := os.MkdirAll(filepath.Join(e.repo, "sub"), 0o755); err != nil {
		t.Fatal(err)
	}
	e.tpath = filepath.Join(t.TempDir(), "transcript.jsonl")
	if err := os.WriteFile(e.tpath, []byte(transcript), 0o600); err != nil {
		t.Fatal(err)
	}
	e.deps = Deps{
		Now:      func() time.Time { return e.now },
		Rand:     bytes.NewReader(bytes.Repeat([]byte{0x2a}, 1000)),
		StateDir: e.state,
		Git:      gitCLI{},
		Stderr:   &bytes.Buffer{},
	}
	return e
}

func (e *env) input(event, plan string, tool string) string {
	in := map[string]any{
		"session_id": "sess-1", "transcript_path": e.tpath, "cwd": filepath.Join(e.repo, "sub"),
		"hook_event_name": event, "tool_name": tool, "tool_use_id": "toolu_01",
	}
	if event == "PreToolUse" {
		in["tool_input"] = map[string]any{"plan": plan, "planFilePath": "/x/plan.md"}
	} else {
		in["tool_input"] = map[string]any{"plan": "ignored"}
		in["tool_response"] = map[string]any{"plan": plan, "filePath": "/x/plan.md"}
	}
	b, _ := json.Marshal(in)
	return string(b)
}

// run runs `intentgate hook claude-code <which>` and returns the exit code and the one JSON object.
func (e *env) run(which, stdin string) (int, map[string]string) {
	e.t.Helper()
	var out bytes.Buffer
	code := Run([]string{"hook", "claude-code", which}, strings.NewReader(stdin), &out, e.deps)
	var obj map[string]string
	dec := json.NewDecoder(bytes.NewReader(out.Bytes()))
	if err := dec.Decode(&obj); err != nil {
		e.t.Fatalf("stdout %q is not a JSON object: %v", out.String(), err)
	}
	if dec.More() {
		e.t.Fatalf("stdout %q holds more than one JSON value", out.String())
	}
	if e.deps.Stderr.(*bytes.Buffer).Len() != 0 {
		e.t.Errorf("stderr: %q", e.deps.Stderr.(*bytes.Buffer).String())
	}
	return code, obj
}

// pre runs a successful PreToolUse for planText, so a later post has its pending record.
func (e *env) pre() {
	e.t.Helper()
	if code, obj := e.run("pre", e.input("PreToolUse", planText, "ExitPlanMode")); code != 0 || len(e.pendingFiles()) != 1 {
		e.t.Fatalf("setup pre: exit %d, %v, pending %v", code, obj, e.pendingFiles())
	}
}

func (e *env) locks() []string {
	m, _ := filepath.Glob(filepath.Join(e.repo, ".intent", "*.yml"))
	return m
}

func (e *env) nothingWritten() {
	e.t.Helper()
	if _, err := os.Stat(filepath.Join(e.repo, ".intent")); err == nil {
		e.t.Error(".intent exists, want nothing written")
	}
	if _, err := os.Stat(filepath.Join(e.state, "locks")); err == nil {
		e.t.Error("a root intent record exists, want nothing written")
	}
}

func (e *env) pendingFiles() []string {
	var files []string
	_ = filepath.WalkDir(filepath.Join(e.state, "pending"), func(p string, d os.DirEntry, err error) error {
		if err == nil && !d.IsDir() {
			files = append(files, p)
		}
		return nil
	})
	return files
}

func TestHook_ApprovedPlanWritesLock(t *testing.T) {
	e := newEnv(t, "dev@example.com")

	code, obj := e.run("pre", e.input("PreToolUse", planText, "ExitPlanMode"))
	if code != 0 {
		t.Fatalf("pre exit = %d", code)
	}
	for _, want := range []string{"IntentGate criteria for this plan:", "Findings are published as SARIF", "internal/report/**"} {
		if !strings.Contains(obj["systemMessage"], want) {
			t.Errorf("pre systemMessage %q lacks %q", obj["systemMessage"], want)
		}
	}
	if msg := obj["systemMessage"]; !strings.HasPrefix(msg, "IntentGate criteria for this plan:") || strings.Contains(msg, "if you approve") {
		t.Errorf("pre systemMessage = %q, want it to start \"IntentGate criteria for this plan:\" and not promise a lock", msg)
	}
	if got := e.pendingFiles(); len(got) != 1 || !strings.HasSuffix(got[0], filepath.Join("pending", "sess-1", "toolu_01.json")) {
		t.Fatalf("pending records after pre: %v", got)
	}
	e.nothingWritten()

	e.now = t0.Add(12345 * time.Millisecond)
	code, obj = e.run("post", e.input("PostToolUse", planText, "ExitPlanMode"))
	if code != 0 {
		t.Fatalf("post exit = %d", code)
	}

	wantID, err := ids.NewULID(e.now, bytes.NewReader(bytes.Repeat([]byte{0x2a}, 1000)))
	if err != nil {
		t.Fatal(err)
	}
	if want := "IntentGate: intent lock written: .intent/" + wantID + ".yml (plan review: unavailable; 0 omissions, 0 additions, 0 weakenings)\nroot intent: 2 human turn(s) whole session (no earlier approved plan)"; obj["systemMessage"] != want {
		t.Errorf("post systemMessage = %q, want %q", obj["systemMessage"], want)
	}
	files := e.locks()
	if len(files) != 1 || filepath.Base(files[0]) != wantID+".yml" {
		t.Fatalf("locks = %v, want one named %s.yml", files, wantID)
	}
	data, err := os.ReadFile(files[0])
	if err != nil {
		t.Fatal(err)
	}
	l, err := lock.Parse(data)
	if err != nil {
		t.Fatal(err)
	}
	if ps := lock.Validate(l); len(ps) != 0 {
		t.Errorf("lock.Validate: %v", ps)
	}
	if !lock.VerifyHash(l) {
		t.Error("the hash does not verify")
	}
	if l.Ratification.Method != "hook" || l.RootIntent.Source != "claude-code-session" || l.Ratification.Ratifier != "git:dev@example.com" {
		t.Errorf("ratification: %+v, source %q", l.Ratification, l.RootIntent.Source)
	}
	if l.Ratification.ReviewDurationMS == nil || *l.Ratification.ReviewDurationMS != 12345 {
		t.Errorf("review_duration_ms = %v, want 12345", l.Ratification.ReviewDurationMS)
	}
	if l.Ratification.DraftedBy == nil || *l.Ratification.DraftedBy != "claude-code/claude-opus-5-5" {
		t.Errorf("drafted_by = %v", l.Ratification.DraftedBy)
	}
	if l.BaseRef != "main" || l.Revision != 1 || len(l.Criteria) != 2 || l.Criteria[1].ID != "C2" {
		t.Errorf("lock: base %q revision %d criteria %+v", l.BaseRef, l.Revision, l.Criteria)
	}
	rec, err := os.ReadFile(filepath.Join(e.state, "locks", wantID, "root-intent.md"))
	if err != nil {
		t.Fatal(err)
	}
	if string(rec) != wantRootIntent {
		t.Errorf("root intent record = %q, want %q", rec, wantRootIntent)
	}
	if strings.Contains(string(data), "Please add SARIF output") {
		t.Error("the committed lock holds root intent text")
	}
	if got := e.pendingFiles(); len(got) != 0 {
		t.Errorf("pending records after post: %v", got)
	}
}

func TestHook_RejectedPlanWritesNothing(t *testing.T) {
	e := newEnv(t, "dev@example.com")
	if code, _ := e.run("pre", e.input("PreToolUse", planText, "ExitPlanMode")); code != 0 {
		t.Fatalf("pre exit = %d", code)
	}
	e.nothingWritten()
	if got := e.pendingFiles(); len(got) != 1 {
		t.Fatalf("pending records: %v, want the one from pre", got)
	}

	// Two old-session records: one 25 h old, one 1 h old (by finished_at, whatever the mtime says).
	put := func(session, use string, age time.Duration) string {
		dir := filepath.Join(e.state, "pending", session)
		if err := os.MkdirAll(dir, 0o700); err != nil {
			t.Fatal(err)
		}
		p := filepath.Join(dir, use+".json")
		rec := `{"finished_at":"` + t0.Add(-age).Format(time.RFC3339Nano) + `","plan_sha256":"` + strings.Repeat("a", 64) + `"}`
		if err := os.WriteFile(p, []byte(rec), 0o600); err != nil {
			t.Fatal(err)
		}
		return p
	}
	old := put("old-sess", "toolu_old", 25*time.Hour)
	fresh := put("fresh-sess", "toolu_fresh", time.Hour)
	// A fresh mtime must not save an old record.
	if err := os.Chtimes(old, time.Now(), time.Now()); err != nil {
		t.Fatal(err)
	}

	e.now = t0.Add(time.Minute)
	if code, _ := e.run("pre", e.input("PreToolUse", planText, "ExitPlanMode")); code != 0 {
		t.Fatalf("second pre exit = %d", code)
	}
	if _, err := os.Stat(old); err == nil {
		t.Error("the 25 h old pending record is still there")
	}
	if _, err := os.Stat(filepath.Dir(old)); err == nil {
		t.Error("the empty session directory is still there")
	}
	if _, err := os.Stat(fresh); err != nil {
		t.Errorf("the fresh pending record was deleted: %v", err)
	}
	e.nothingWritten()
}

func TestHook_FailsOpen(t *testing.T) {
	noLock := func(t *testing.T, e *env, code int, obj map[string]string) {
		t.Helper()
		if code != 0 {
			t.Errorf("exit = %d, want 0", code)
		}
		if msg := obj["systemMessage"]; !strings.Contains(msg, "no lock:") || strings.Count(msg, "no lock:") != 1 {
			t.Errorf("systemMessage = %q, want one \"no lock:\"", msg)
		}
		if len(obj) != 1 {
			t.Errorf("object %v has more than systemMessage", obj)
		}
		e.nothingWritten()
	}

	t.Run("plan without a block", func(t *testing.T) {
		e := newEnv(t, "dev@example.com")
		code, obj := e.run("pre", e.input("PreToolUse", "# A plan with no block\n", "ExitPlanMode"))
		noLock(t, e, code, obj)
		if got := e.pendingFiles(); len(got) != 0 {
			t.Errorf("pending records: %v", got)
		}
		code, obj = e.run("post", e.input("PostToolUse", "# A plan with no block\n", "ExitPlanMode"))
		noLock(t, e, code, obj)
	})
	t.Run("missing transcript", func(t *testing.T) {
		e := newEnv(t, "dev@example.com")
		e.pre()
		e.tpath += ".missing"
		code, obj := e.run("post", e.input("PostToolUse", planText, "ExitPlanMode"))
		noLock(t, e, code, obj)
	})
	t.Run("transcript with no human turns", func(t *testing.T) {
		e := newEnv(t, "dev@example.com")
		e.pre()
		if err := os.WriteFile(e.tpath, []byte(`{"type":"assistant","message":{"model":"m"}}`+"\n"), 0o600); err != nil {
			t.Fatal(err)
		}
		code, obj := e.run("post", e.input("PostToolUse", planText, "ExitPlanMode"))
		noLock(t, e, code, obj)
	})
	t.Run("empty user.email", func(t *testing.T) {
		e := newEnv(t, "")
		e.pre()
		code, obj := e.run("post", e.input("PostToolUse", planText, "ExitPlanMode"))
		noLock(t, e, code, obj)
	})
	t.Run("no base ref", func(t *testing.T) {
		e := newEnv(t, "dev@example.com")
		e.pre()
		gitIn(t, e.repo, "branch", "-m", "main", "develop")
		code, obj := e.run("post", e.input("PostToolUse", planText, "ExitPlanMode"))
		noLock(t, e, code, obj)
	})
	t.Run("cwd outside a repository", func(t *testing.T) {
		e := newEnv(t, "dev@example.com")
		e.pre()
		in := strings.Replace(e.input("PostToolUse", planText, "ExitPlanMode"), filepath.Join(e.repo, "sub"), t.TempDir(), 1)
		code, obj := e.run("post", in)
		noLock(t, e, code, obj)
	})
	t.Run("unparseable stdin", func(t *testing.T) {
		for _, which := range []string{"pre", "post"} {
			e := newEnv(t, "dev@example.com")
			code, obj := e.run(which, "this is not json")
			noLock(t, e, code, obj)
			code, obj = e.run(which, "")
			noLock(t, e, code, obj)
		}
	})
	t.Run("an id that could escape the state directory", func(t *testing.T) {
		e := newEnv(t, "dev@example.com")
		in := strings.Replace(e.input("PreToolUse", planText, "ExitPlanMode"), `"sess-1"`, `"../../evil"`, 1)
		code, obj := e.run("pre", in)
		noLock(t, e, code, obj)
		if _, err := os.Stat(filepath.Join(e.state, "..", "..", "evil")); err == nil {
			t.Error("a record escaped the state directory")
		}
	})
	t.Run("a lock id that already exists", func(t *testing.T) {
		e := newEnv(t, "dev@example.com")
		e.pre()
		e.deps.Rand = bytes.NewReader(bytes.Repeat([]byte{0x2a}, 1000))
		if code, obj := e.run("post", e.input("PostToolUse", planText, "ExitPlanMode")); code != 0 || len(e.locks()) != 1 {
			t.Fatalf("first post failed: %d %v", code, obj)
		}
		e.pre() // a second approval with a pending record, but the same ULID
		e.deps.Rand = bytes.NewReader(bytes.Repeat([]byte{0x2a}, 1000))
		code, obj := e.run("post", e.input("PostToolUse", planText, "ExitPlanMode"))
		if code != 0 || !strings.Contains(obj["systemMessage"], "no lock:") {
			t.Errorf("second post: exit %d, %v", code, obj)
		}
		if len(e.locks()) != 1 {
			t.Errorf("locks: %v", e.locks())
		}
	})
	t.Run("another tool prints {}", func(t *testing.T) {
		for _, which := range []string{"pre", "post"} {
			e := newEnv(t, "dev@example.com")
			event := map[string]string{"pre": "PreToolUse", "post": "PostToolUse"}[which]
			code, obj := e.run(which, e.input(event, planText, "Bash"))
			if code != 0 || len(obj) != 0 {
				t.Errorf("%s with Bash: exit %d, %v; want 0 and {}", which, code, obj)
			}
			e.nothingWritten()
			if got := e.pendingFiles(); len(got) != 0 {
				t.Errorf("pending records: %v", got)
			}
		}
	})
}

func TestHook_ApprovedPlanDiffersFromPre(t *testing.T) {
	e := newEnv(t, "dev@example.com")
	e.run("pre", e.input("PreToolUse", planText, "ExitPlanMode"))
	e.now = t0.Add(2 * time.Second)
	code, obj := e.run("post", e.input("PostToolUse", otherPlanText, "ExitPlanMode"))
	if code != 0 {
		t.Fatalf("post exit = %d: %v", code, obj)
	}
	files := e.locks()
	if len(files) != 1 {
		t.Fatalf("locks = %v", files)
	}
	data, _ := os.ReadFile(files[0])
	l, err := lock.Parse(data)
	if err != nil {
		t.Fatal(err)
	}
	if len(l.Criteria) != 1 || l.Criteria[0].Text != "Only this one" || l.Criteria[0].Footprint[0] != "cmd/**" {
		t.Errorf("criteria = %+v, want the approved plan's", l.Criteria)
	}
	if l.Ratification.ReviewDurationMS == nil || *l.Ratification.ReviewDurationMS != 2000 {
		t.Errorf("review_duration_ms = %v, want 2000", l.Ratification.ReviewDurationMS)
	}
}

// Review 1 of S-04: post without a pending record for the same session and tool use writes no lock.
func TestHook_PostWithoutPendingRecord(t *testing.T) {
	t.Run("no pre at all", func(t *testing.T) {
		e := newEnv(t, "dev@example.com")
		code, obj := e.run("post", e.input("PostToolUse", planText, "ExitPlanMode"))
		if code != 0 {
			t.Errorf("exit = %d, want 0", code)
		}
		want := "IntentGate: no lock: no pending record; was the plan shown by the PreToolUse hook?"
		if obj["systemMessage"] != want || len(obj) != 1 {
			t.Errorf("systemMessage = %q, want %q", obj["systemMessage"], want)
		}
		e.nothingWritten()
	})
	t.Run("a pending record for another tool use", func(t *testing.T) {
		e := newEnv(t, "dev@example.com")
		e.pre()
		in := strings.Replace(e.input("PostToolUse", planText, "ExitPlanMode"), `"toolu_01"`, `"toolu_02"`, 1)
		code, obj := e.run("post", in)
		if code != 0 || !strings.Contains(obj["systemMessage"], "no lock: no pending record") {
			t.Errorf("exit %d, %v", code, obj)
		}
		e.nothingWritten()
	})
	t.Run("an unparseable pending record", func(t *testing.T) {
		e := newEnv(t, "dev@example.com")
		e.pre()
		if err := os.WriteFile(e.pendingFiles()[0], []byte("not json"), 0o600); err != nil {
			t.Fatal(err)
		}
		code, obj := e.run("post", e.input("PostToolUse", planText, "ExitPlanMode"))
		if code != 0 || !strings.Contains(obj["systemMessage"], "no lock: no pending record") {
			t.Errorf("exit %d, %v", code, obj)
		}
		e.nothingWritten()
	})
	t.Run("a pending record in the future gives 0 ms", func(t *testing.T) {
		e := newEnv(t, "dev@example.com")
		e.pre()
		e.now = t0.Add(-time.Hour) // the clock went back: now − finished_at is negative
		code, obj := e.run("post", e.input("PostToolUse", planText, "ExitPlanMode"))
		if code != 0 || len(e.locks()) != 1 {
			t.Fatalf("exit %d, %v", code, obj)
		}
		data, _ := os.ReadFile(e.locks()[0])
		l, _ := lock.Parse(data)
		if l.Ratification.ReviewDurationMS == nil || *l.Ratification.ReviewDurationMS != 0 {
			t.Errorf("review_duration_ms = %v, want 0", l.Ratification.ReviewDurationMS)
		}
	})
}

func TestHook_PrintConfig(t *testing.T) {
	var out bytes.Buffer
	if code := Run([]string{"hook", "claude-code", "print-config"}, strings.NewReader(""), &out, Deps{Stderr: &bytes.Buffer{}}); code != 0 {
		t.Fatalf("exit %d", code)
	}
	var cfg struct {
		Hooks map[string][]struct {
			Matcher string `json:"matcher"`
			Hooks   []struct {
				Type    string `json:"type"`
				Command string `json:"command"`
				Timeout int    `json:"timeout"`
			} `json:"hooks"`
		} `json:"hooks"`
	}
	if err := json.Unmarshal(out.Bytes(), &cfg); err != nil {
		t.Fatalf("not JSON: %v\n%s", err, out.String())
	}
	for event, want := range map[string]struct {
		cmd     string
		timeout int
	}{"PreToolUse": {"intentgate hook claude-code pre", 60}, "PostToolUse": {"intentgate hook claude-code post", 30}} {
		entries := cfg.Hooks[event]
		if len(entries) == 0 || entries[0].Matcher != "ExitPlanMode" || len(entries[0].Hooks) != 1 {
			t.Errorf("%s: %+v", event, entries)
			continue
		}
		for _, e := range entries[1:] { // S-18: the same hook for the ratify card
			if e.Matcher != "AskUserQuestion" || len(e.Hooks) != 1 || e.Hooks[0].Command != want.cmd || e.Hooks[0].Timeout != want.timeout {
				t.Errorf("%s: extra entry %+v", event, e)
			}
		}
		h := entries[0].Hooks[0]
		if h.Type != "command" || h.Command != want.cmd || h.Timeout != want.timeout {
			t.Errorf("%s: %+v, want command %q timeout %d", event, h, want.cmd, want.timeout)
		}
	}
}

// Review 1 of S-04: pre/post always exit 0 with one JSON object, even on a bad command line;
// every other usage error exits 2 with usage on stderr.
func TestRun_ExitCodes(t *testing.T) {
	t.Run("pre and post with extra arguments fail open", func(t *testing.T) {
		for _, args := range [][]string{
			{"hook", "claude-code", "pre", "extra"},
			{"hook", "claude-code", "post", "a", "b"},
		} {
			e := newEnv(t, "dev@example.com")
			var out bytes.Buffer
			code := Run(args, strings.NewReader(e.input("PreToolUse", planText, "ExitPlanMode")), &out, e.deps)
			var obj map[string]string
			if err := json.Unmarshal(out.Bytes(), &obj); err != nil {
				t.Errorf("%v: stdout %q is not a JSON object: %v", args, out.String(), err)
				continue
			}
			if code != 0 || !strings.HasPrefix(obj["systemMessage"], "IntentGate: no lock: ") || len(obj) != 1 {
				t.Errorf("%v: exit %d, %v; want exit 0 and a \"no lock:\" systemMessage", args, code, obj)
			}
			if got := e.deps.Stderr.(*bytes.Buffer).String(); got != "" {
				t.Errorf("%v: stderr %q, want empty", args, got)
			}
			e.nothingWritten()
			if got := e.pendingFiles(); len(got) != 0 {
				t.Errorf("%v: pending records %v", args, got)
			}
		}
	})
	t.Run("every other usage error exits 2 with usage on stderr", func(t *testing.T) {
		for _, args := range [][]string{
			nil,
			{"bogus"},
			{"hook"},
			{"hook", "claude-code"},
			{"hook", "claude-code", "bogus"},
			{"hook", "other-agent", "pre"},
			{"hook", "claude-code", "print-config", "extra"},
		} {
			var out bytes.Buffer
			stderr := &bytes.Buffer{}
			code := Run(args, strings.NewReader(""), &out, Deps{Stderr: stderr})
			if code != 2 {
				t.Errorf("%v: exit %d, want 2", args, code)
			}
			if !strings.Contains(stderr.String(), "usage:") {
				t.Errorf("%v: stderr %q lacks usage", args, stderr.String())
			}
			if out.Len() != 0 {
				t.Errorf("%v: stdout %q, want empty", args, out.String())
			}
		}
	})
}

// Review 1 of S-04: the pending record is written temp file + rename, so a reader never sees a
// half-written record (rename gives the path a new inode; an in-place write would keep it).
func TestHook_PendingRecordIsWrittenByRename(t *testing.T) {
	e := newEnv(t, "dev@example.com")
	e.pre()
	path := e.pendingFiles()[0]
	before, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	e.now = t0.Add(time.Second)
	e.pre()
	after, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	if os.SameFile(before, after) {
		t.Error("the record was rewritten in place, want a new file renamed over it")
	}
	if after.Mode().Perm() != 0o600 {
		t.Errorf("mode = %v, want 0600", after.Mode().Perm())
	}
	if got := e.pendingFiles(); len(got) != 1 {
		t.Errorf("files in pending: %v, want only the record (no temp file left)", got)
	}
}

// Review 1 of S-04: prune on |now − finished_at| > 24 h, and an unparseable record only once its
// mtime is older than 24 h.
func TestHook_PruneRules(t *testing.T) {
	e := newEnv(t, "dev@example.com")
	put := func(use, content string, mtime time.Time) string {
		dir := filepath.Join(e.state, "pending", "s")
		if err := os.MkdirAll(dir, 0o700); err != nil {
			t.Fatal(err)
		}
		p := filepath.Join(dir, use+".json")
		if err := os.WriteFile(p, []byte(content), 0o600); err != nil {
			t.Fatal(err)
		}
		if err := os.Chtimes(p, mtime, mtime); err != nil {
			t.Fatal(err)
		}
		return p
	}
	rec := func(at time.Time) string {
		return `{"finished_at":"` + at.Format(time.RFC3339Nano) + `","plan_sha256":"` + strings.Repeat("a", 64) + `"}`
	}
	cases := []struct {
		name string
		path string
		kept bool
	}{
		{"23 h old", put("toolu_23h", rec(t0.Add(-23*time.Hour)), t0), true},
		{"25 h old", put("toolu_25h", rec(t0.Add(-25*time.Hour)), t0), false},
		{"23 h in the future", put("toolu_f23h", rec(t0.Add(23*time.Hour)), t0), true},
		{"25 h in the future", put("toolu_f25h", rec(t0.Add(25*time.Hour)), t0), false},
		{"a year in the future", put("toolu_f365d", rec(t0.Add(365*24*time.Hour)), t0), false},
		{"unparseable, fresh mtime", put("toolu_bad_fresh", "not json", t0.Add(-time.Hour)), true},
		{"unparseable, mtime 25 h old", put("toolu_bad_old", "not json", t0.Add(-25*time.Hour)), false},
		{"bad finished_at, fresh mtime", put("toolu_badtime_fresh", `{"finished_at":"yesterday"}`, t0), true},
		{"bad finished_at, mtime 25 h old", put("toolu_badtime_old", `{"finished_at":"yesterday"}`, t0.Add(-25*time.Hour)), false},
	}
	if code, _ := e.run("pre", e.input("PreToolUse", "# no block\n", "ExitPlanMode")); code != 0 {
		t.Fatalf("exit %d", code)
	}
	for _, c := range cases {
		_, err := os.Stat(c.path)
		if kept := err == nil; kept != c.kept {
			t.Errorf("%s: kept = %v, want %v", c.name, kept, c.kept)
		}
	}
}

// Review 1 of S-04: the built binary prints exactly one JSON object on stdout and nothing on
// stderr for pre and post, and gives the documented exit codes. It uses a temp INTENTGATE_HOME
// and a temp repository; the real ~/.intentgate is never touched.
func TestHook_BuiltBinary(t *testing.T) {
	e := newEnv(t, "dev@example.com")
	bin := filepath.Join(t.TempDir(), "intentgate")
	if out, err := exec.Command("go", "build", "-o", bin, ".").CombinedOutput(); err != nil {
		t.Fatalf("go build: %v\n%s", err, out)
	}
	t.Setenv("INTENTGATE_HOME", e.state)
	t.Setenv("HOME", t.TempDir()) // so nothing can fall back to the real home

	call := func(stdin string, args ...string) (int, string, string) {
		t.Helper()
		cmd := exec.Command(bin, args...)
		cmd.Dir = e.repo
		cmd.Stdin = strings.NewReader(stdin)
		var stdout, stderr bytes.Buffer
		cmd.Stdout, cmd.Stderr = &stdout, &stderr
		err := cmd.Run()
		code := 0
		if ee, ok := err.(*exec.ExitError); ok {
			code = ee.ExitCode()
		} else if err != nil {
			t.Fatalf("%v: %v", args, err)
		}
		return code, stdout.String(), stderr.String()
	}
	oneObject := func(what, stdout, stderr string) map[string]string {
		t.Helper()
		var obj map[string]string
		dec := json.NewDecoder(strings.NewReader(stdout))
		if err := dec.Decode(&obj); err != nil || dec.More() || !strings.HasSuffix(stdout, "\n") || strings.Count(stdout, "\n") != 1 {
			t.Fatalf("%s: stdout %q is not exactly one JSON object line (%v)", what, stdout, err)
		}
		if stderr != "" {
			t.Errorf("%s: stderr %q, want empty", what, stderr)
		}
		return obj
	}

	code, stdout, stderr := call(e.input("PreToolUse", planText, "ExitPlanMode"), "hook", "claude-code", "pre")
	obj := oneObject("pre", stdout, stderr)
	if code != 0 || !strings.Contains(obj["systemMessage"], "IntentGate criteria for this plan:") {
		t.Errorf("pre: exit %d, %v", code, obj)
	}
	code, stdout, stderr = call(e.input("PostToolUse", planText, "ExitPlanMode"), "hook", "claude-code", "post")
	obj = oneObject("post", stdout, stderr)
	if code != 0 || !strings.HasPrefix(obj["systemMessage"], "IntentGate: intent lock written: .intent/") {
		t.Errorf("post: exit %d, %v", code, obj)
	}
	if len(e.locks()) != 1 {
		t.Errorf("locks: %v", e.locks())
	}
	// A failure is still one object, exit 0, quiet stderr.
	for _, which := range []string{"pre", "post"} {
		code, stdout, stderr = call("not json", "hook", "claude-code", which)
		obj = oneObject(which+" with bad input", stdout, stderr)
		if code != 0 || !strings.HasPrefix(obj["systemMessage"], "IntentGate: no lock: ") {
			t.Errorf("%s with bad input: exit %d, %v", which, code, obj)
		}
	}
	code, stdout, stderr = call("", "hook", "claude-code", "pre", "extra")
	obj = oneObject("pre extra", stdout, stderr)
	if code != 0 || !strings.HasPrefix(obj["systemMessage"], "IntentGate: no lock: ") {
		t.Errorf("pre extra: exit %d, %v", code, obj)
	}
	if code, stdout, stderr = call("", "bogus"); code != 2 || stdout != "" || !strings.Contains(stderr, "usage:") {
		t.Errorf("bogus: exit %d, stdout %q, stderr %q; want 2, empty, usage", code, stdout, stderr)
	}
}

// A session that approved a plan earlier gives the next lock only the turns since that plan
// (ratification.md §2, "Root intent scope"): `pre` and `post` pass the hook input's tool_use_id.
func TestHook_RootIntentScoped(t *testing.T) {
	e := newEnv(t, "dev@example.com")
	scoped := strings.Join([]string{
		`{"type":"user","message":{"role":"user","content":"Earlier slice ask."}}`,
		`{"type":"assistant","message":{"role":"assistant","model":"claude-opus-5-5","content":[{"type":"tool_use","id":"toolu_00","name":"ExitPlanMode","input":{}}]}}`,
		`{"type":"user","message":{"role":"user","content":[{"type":"tool_result","tool_use_id":"toolu_00","content":"approved"}]}}`,
		`{"type":"user","message":{"role":"user","content":"Please add SARIF output."}}`,
		`{"type":"assistant","message":{"role":"assistant","model":"claude-opus-5-5","content":[{"type":"tool_use","id":"toolu_01","name":"ExitPlanMode","input":{}}]}}`,
		`{"type":"user","message":{"role":"user","content":[{"type":"text","text":"Keep it small."}]}}`,
	}, "\n") + "\n"
	if err := os.WriteFile(e.tpath, []byte(scoped), 0o600); err != nil {
		t.Fatal(err)
	}
	e.pre()
	e.now = t0.Add(time.Second)
	if code, obj := e.run("post", e.input("PostToolUse", planText, "ExitPlanMode")); code != 0 || !strings.Contains(obj["systemMessage"], "intent lock written") {
		t.Fatalf("post: exit %d, %v", code, obj)
	}
	recs, _ := filepath.Glob(filepath.Join(e.state, "locks", "*", "root-intent.md"))
	if len(recs) != 1 {
		t.Fatalf("root intent records: %v", recs)
	}
	rec, err := os.ReadFile(recs[0])
	if err != nil {
		t.Fatal(err)
	}
	if string(rec) != wantRootIntent {
		t.Errorf("root intent record = %q, want only the turns since the approved plan: %q", rec, wantRootIntent)
	}
}

// Review 1 of S-17: the pre and post messages say which scope the root intent used.
func TestHook_ScopeLine(t *testing.T) {
	approved := []string{
		`{"type":"user","message":{"role":"user","content":"Earlier slice ask."}}`,
		`{"type":"assistant","message":{"role":"assistant","model":"claude-opus-5-5","content":[{"type":"tool_use","id":"toolu_00","name":"ExitPlanMode","input":{}}]}}`,
		`{"type":"user","message":{"role":"user","content":[{"type":"tool_result","tool_use_id":"toolu_00","content":"approved"}]}}`,
	}
	current := `{"type":"assistant","message":{"role":"assistant","model":"claude-opus-5-5","content":[{"type":"tool_use","id":"toolu_01","name":"ExitPlanMode","input":{}}]}}`
	for _, c := range []struct {
		name       string
		lines      []string
		want       string
		wantIntent string
	}{
		{"since", append(append([]string{}, approved...), `{"type":"user","message":{"role":"user","content":"Please add SARIF output."}}`, current,
			`{"type":"user","message":{"role":"user","content":"Keep it small."}}`),
			"root intent: 2 human turn(s) since the previous approved plan", wantRootIntent},
		{"reused", append(append([]string{}, approved...), current),
			"root intent: 1 human turn(s) reused from the previous plan (no new turns)", "Earlier slice ask."},
		{"whole", []string{strings.TrimSpace(transcript)},
			"root intent: 2 human turn(s) whole session (no earlier approved plan)", wantRootIntent},
	} {
		t.Run(c.name, func(t *testing.T) {
			e := newEnv(t, "dev@example.com")
			if err := os.WriteFile(e.tpath, []byte(strings.Join(c.lines, "\n")+"\n"), 0o600); err != nil {
				t.Fatal(err)
			}
			code, obj := e.run("pre", e.input("PreToolUse", planText, "ExitPlanMode"))
			if code != 0 || !strings.Contains(obj["systemMessage"], c.want) {
				t.Errorf("pre: exit %d, message %q, want it to contain %q", code, obj["systemMessage"], c.want)
			}
			e.now = t0.Add(time.Second)
			code, obj = e.run("post", e.input("PostToolUse", planText, "ExitPlanMode"))
			if code != 0 || !strings.Contains(obj["systemMessage"], "intent lock written") || !strings.Contains(obj["systemMessage"], c.want) {
				t.Errorf("post: exit %d, message %q, want an intent lock and %q", code, obj["systemMessage"], c.want)
			}
			recs, _ := filepath.Glob(filepath.Join(e.state, "locks", "*", "root-intent.md"))
			if len(recs) != 1 {
				t.Fatalf("root intent records: %v", recs)
			}
			if rec, _ := os.ReadFile(recs[0]); string(rec) != c.wantIntent {
				t.Errorf("root intent record = %q, want %q", rec, c.wantIntent)
			}
		})
	}
}

// REQ-0047
func TestHook_RepoWideWarning(t *testing.T) {
	e := newEnv(t, "dev@example.com")
	code, obj := e.run("pre", e.input("PreToolUse", repoWidePlanText, "ExitPlanMode"))
	if code != 0 {
		t.Fatalf("pre exit = %d", code)
	}
	if !strings.Contains(obj["systemMessage"], repoWideWarning) {
		t.Errorf("pre systemMessage %q lacks the warning", obj["systemMessage"])
	}
	e.now = t0.Add(time.Second)
	code, obj = e.run("post", e.input("PostToolUse", repoWidePlanText, "ExitPlanMode"))
	if code != 0 {
		t.Fatalf("post exit = %d", code)
	}
	if msg := obj["systemMessage"]; !strings.Contains(msg, repoWideWarning) || !strings.Contains(msg, "intent lock written") {
		t.Errorf("post systemMessage = %q, want the lock written and the warning %q", msg, repoWideWarning)
	}
	if files := e.locks(); len(files) != 1 {
		t.Errorf("locks = %v, want one: the lock is still written", files)
	}

	e = newEnv(t, "dev@example.com")
	e.pre()
	e.now = t0.Add(time.Second)
	if _, obj = e.run("post", e.input("PostToolUse", planText, "ExitPlanMode")); strings.Contains(obj["systemMessage"], "matches every path") {
		t.Errorf("a narrow plan got a warning: %q", obj["systemMessage"])
	}
}
