package main

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/autox-my/intent-gate/internal/lock"
	"github.com/autox-my/intent-gate/internal/planreview"
)

// S-22 (intent-lock.md §6): a plan names a committed intent file with `intent:` in its block.
// Intent files and plans are synthetic.

const (
	intentPath = "intents/checkout-v2.md"
	intentText = "# Checkout v2\n\nWhat: one-page checkout. Why: fewer abandoned carts.\nConstraint: no new payment provider.\n"
)

// planWithIntent is a plan whose block names intent as the root intent ("" leaves the key out).
func planWithIntent(intent string) string {
	key := ""
	if intent != "" {
		key = "intent: " + intent + "\n"
	}
	return "# Plan\n\nAdd SARIF output.\n\n```intentgate\n" + key + "criteria:\n" +
		"  - text: \"Findings are published as SARIF\"\n    footprint: [\"internal/report/**\"]\n```\n"
}

// commitIntent writes the intent file at path and commits it.
func (e *env) commitIntent(path, text string) {
	e.t.Helper()
	e.write(path, text)
	gitIn(e.t, e.repo, "add", path)
	gitIn(e.t, e.repo, "-c", "user.name=t", "-c", "user.email=t@example.com", "commit", "-q", "-m", "add "+path)
}

// headBlob is `git rev-parse HEAD:<path>`.
func (e *env) headBlob(path string) string {
	e.t.Helper()
	cmd := exec.Command("git", "rev-parse", "HEAD:"+path)
	cmd.Dir = e.repo
	out, err := cmd.Output()
	if err != nil {
		e.t.Fatalf("git rev-parse HEAD:%s: %v", path, err)
	}
	return strings.TrimSpace(string(out))
}

// committedEnv is a repository with the intent file committed and a terminal.
func committedEnv(t *testing.T) *env {
	t.Helper()
	e := newEnv(t, "dev@example.com")
	e.terminals(true, true)
	e.commitIntent(intentPath, intentText)
	return e
}

func fileSum(s string) string {
	h := sha256.Sum256([]byte(s))
	return hex.EncodeToString(h[:])
}

// checkCommittedLock asserts the lock a committed intent gives and that no local record exists.
func checkCommittedLock(t *testing.T, e *env, l lock.Lock, wantMethod string) {
	t.Helper()
	if ps := lock.Validate(l); len(ps) != 0 {
		t.Errorf("lock.Validate: %v", ps)
	}
	if !lock.VerifyHash(l) {
		t.Error("the hash does not verify")
	}
	ri := l.RootIntent
	if ri.Source != "committed-intent" || ri.Path != intentPath || ri.Blob != e.headBlob(intentPath) || ri.SHA256 != fileSum(intentText) {
		t.Errorf("root_intent = %+v, want committed-intent, %s, blob %s, sha256 %s", ri, intentPath, e.headBlob(intentPath), fileSum(intentText))
	}
	if ri.Redactions == nil || *ri.Redactions != 0 {
		t.Errorf("redactions = %v, want 0", ri.Redactions)
	}
	if l.Ratification.Method != wantMethod {
		t.Errorf("method = %q, want %q", l.Ratification.Method, wantMethod)
	}
	if recs, _ := filepath.Glob(filepath.Join(e.state, "locks", "*", "root-intent.md")); len(recs) != 0 {
		t.Errorf("a root-intent.md was written for a committed intent: %v", recs)
	}
	if data, err := os.ReadFile(filepath.Join(e.repo, ".intent", l.ID+".yml")); err != nil || strings.Contains(string(data), "one-page checkout") {
		t.Errorf("the committed lock holds intent text (or cannot be read: %v)", err)
	}
}

// REQ-0043
func TestLockFromPlan_CommittedIntent(t *testing.T) {
	t.Run("after yes the lock records the committed intent", func(t *testing.T) {
		e := committedEnv(t)
		plan := e.write("plan.md", planWithIntent(intentPath))
		code, stdout, stderr := e.do([]string{"lock", "--from-plan", plan}, "yes\n")
		if code != 0 {
			t.Fatalf("exit %d, stderr %q, stdout %q", code, stderr, stdout)
		}
		if strings.Contains(stdout, "Paste the root intent") {
			t.Error("asked for a root intent although the plan names a committed one")
		}
		l, _ := e.onlyLock()
		checkCommittedLock(t, e, l, "cli-confirm")
	})
	t.Run("the plan review reads the intent file's text", func(t *testing.T) {
		e := committedEnv(t)
		var gotRoot string
		e.deps.PlanReviewer = fakeReviewer(func(_ context.Context, rootIntent, _ string) ([]planreview.Finding, error) {
			gotRoot = rootIntent
			return nil, nil
		})
		plan := e.write("plan.md", planWithIntent(intentPath))
		if code, _, stderr := e.do([]string{"lock", "--from-plan", plan}, "yes\n"); code != 0 {
			t.Fatalf("exit %d: %s", code, stderr)
		}
		if !strings.Contains(gotRoot, "one-page checkout") {
			t.Errorf("the review saw the root intent %q, want the intent file's text", gotRoot)
		}
	})
	t.Run("a file edited but not committed is exit 3 and nothing is written", func(t *testing.T) {
		e := committedEnv(t)
		e.write(intentPath, intentText+"One more constraint.\n")
		plan := e.write("plan.md", planWithIntent(intentPath))
		code, _, stderr := e.do([]string{"lock", "--from-plan", plan}, "yes\n")
		if code != 3 || !strings.Contains(stderr, intentPath) {
			t.Errorf("exit %d, stderr %q; want 3 naming %s", code, stderr, intentPath)
		}
		e.nothingWritten()
	})
	t.Run("an untracked file is exit 3 and nothing is written", func(t *testing.T) {
		e := newEnv(t, "dev@example.com")
		e.terminals(true, true)
		e.write(intentPath, intentText)
		plan := e.write("plan.md", planWithIntent(intentPath))
		code, _, stderr := e.do([]string{"lock", "--from-plan", plan}, "yes\n")
		if code != 3 || !strings.Contains(stderr, intentPath) {
			t.Errorf("exit %d, stderr %q; want 3 naming %s", code, stderr, intentPath)
		}
		e.nothingWritten()
	})
	t.Run("a file staged but not committed is exit 3", func(t *testing.T) {
		e := newEnv(t, "dev@example.com")
		e.terminals(true, true)
		e.write(intentPath, intentText)
		gitIn(t, e.repo, "add", intentPath)
		plan := e.write("plan.md", planWithIntent(intentPath))
		if code, _, stderr := e.do([]string{"lock", "--from-plan", plan}, "yes\n"); code != 3 {
			t.Errorf("exit %d, stderr %q; want 3", code, stderr)
		}
		e.nothingWritten()
	})
	t.Run("a committed file deleted from the working tree is exit 3", func(t *testing.T) {
		e := committedEnv(t)
		if err := os.Remove(filepath.Join(e.repo, intentPath)); err != nil {
			t.Fatal(err)
		}
		plan := e.write("plan.md", planWithIntent(intentPath))
		if code, _, stderr := e.do([]string{"lock", "--from-plan", plan}, "yes\n"); code != 3 {
			t.Errorf("exit %d, stderr %q; want 3", code, stderr)
		}
		e.nothingWritten()
	})
	t.Run("a path that breaks the rule is exit 2", func(t *testing.T) {
		for _, v := range []string{"../x.md", "/etc/passwd", "intents/../x.md", "x.md", "docs/x.md", "intents/x.txt", "intents/X.md"} {
			e := committedEnv(t)
			plan := e.write("plan.md", planWithIntent(v))
			code, _, stderr := e.do([]string{"lock", "--from-plan", plan}, "yes\n")
			if code != 2 || !strings.Contains(stderr, "no lock: ") {
				t.Errorf("intent: %s: exit %d, stderr %q; want 2 and a no-lock reason", v, code, stderr)
			}
			e.nothingWritten()
		}
	})
	t.Run("--prompt together with intent: is exit 2", func(t *testing.T) {
		e := committedEnv(t)
		plan := e.write("plan.md", planWithIntent(intentPath))
		prompt := e.write("prompt.txt", rootIntent)
		code, _, stderr := e.do([]string{"lock", "--from-plan", plan, "--prompt", prompt}, "yes\n")
		if code != 2 || !strings.Contains(stderr, "no lock: ") {
			t.Errorf("exit %d, stderr %q; want 2", code, stderr)
		}
		e.nothingWritten()
	})
	t.Run("a secret in the file is exit 3 with the design's message", func(t *testing.T) {
		e := newEnv(t, "dev@example.com")
		e.terminals(true, true)
		e.commitIntent(intentPath, "# Checkout v2\n\nUse the token "+planSecret+" for the sandbox.\n")
		plan := e.write("plan.md", planWithIntent(intentPath))
		code, stdout, stderr := e.do([]string{"lock", "--from-plan", plan}, "yes\n")
		if code != 3 || !strings.Contains(stderr, "the committed intent contains what looks like a secret") {
			t.Errorf("exit %d, stderr %q; want 3 and the message", code, stderr)
		}
		if strings.Contains(stdout+stderr, planSecret) {
			t.Error("the secret was printed")
		}
		e.nothingWritten()
	})
	t.Run("a plan without the key behaves as before", func(t *testing.T) {
		e := committedEnv(t)
		plan := e.write("plan.md", planWithIntent(""))
		prompt := e.write("prompt.txt", rootIntent)
		if code, _, stderr := e.do([]string{"lock", "--from-plan", plan, "--prompt", prompt}, "yes\n"); code != 0 {
			t.Fatalf("exit %d: %s", code, stderr)
		}
		l, _ := e.onlyLock()
		if l.RootIntent.Source != "plan-file" || l.RootIntent.Path != "" || l.RootIntent.Blob != "" {
			t.Errorf("root_intent = %+v, want a plan-file source with no path or blob", l.RootIntent)
		}
	})
	t.Run("amend keeps the root intent", func(t *testing.T) {
		e := committedEnv(t)
		plan := e.write("plan.md", planWithIntent(intentPath))
		if code, _, stderr := e.do([]string{"lock", "--from-plan", plan}, "yes\n"); code != 0 {
			t.Fatalf("exit %d: %s", code, stderr)
		}
		before, path := e.onlyLock()
		e.now = e.now.Add(time.Minute)
		if code, _, stderr := e.do([]string{"amend", before.ID, "--criterion", "C1", "--add", "docs/**"}, "yes\n"); code != 0 {
			t.Fatalf("amend exit %d: %s", code, stderr)
		}
		after := e.readLock(path)
		a, b := after.RootIntent, before.RootIntent
		if a.Path != b.Path || a.Blob != b.Blob || a.Source != b.Source || a.SHA256 != b.SHA256 || *a.Redactions != *b.Redactions {
			t.Errorf("root_intent changed: %+v -> %+v", b, a)
		}
		if after.Revision != 2 || a.Source != "committed-intent" || a.Path != intentPath {
			t.Errorf("after amend: revision %d, root_intent %+v", after.Revision, a)
		}
		if ps := lock.Validate(after); len(ps) != 0 || !lock.VerifyHash(after) {
			t.Errorf("amended lock: %v", ps)
		}
	})
}

// REQ-0043
func TestHook_CommittedIntent(t *testing.T) {
	t.Run("an approved ExitPlanMode plan", func(t *testing.T) {
		e := committedEnv(t)
		code, obj := e.run("pre", e.input("PreToolUse", planWithIntent(intentPath), "ExitPlanMode"))
		if code != 0 || len(e.pendingFiles()) != 1 {
			t.Fatalf("pre: exit %d, %v, pending %v", code, obj, e.pendingFiles())
		}
		e.now = t0.Add(9 * time.Second)
		code, obj = e.run("post", e.input("PostToolUse", planWithIntent(intentPath), "ExitPlanMode"))
		if code != 0 || !strings.Contains(obj["systemMessage"], "IntentGate: intent lock written: .intent/") {
			t.Fatalf("post: exit %d, %v", code, obj)
		}
		l, _ := e.onlyLock()
		checkCommittedLock(t, e, l, "hook")
		if len(e.pendingFiles()) != 0 {
			t.Errorf("pending after post: %v", e.pendingFiles())
		}
	})
	t.Run("the ratify card", func(t *testing.T) {
		e := committedEnv(t)
		t.Setenv("HOME", t.TempDir())
		plan := planWithIntent(intentPath)
		writeFile(t, filepath.Join(e.repo, "docs", "plan.md"), plan)
		q := cardQuestion("docs/plan.md", plan)
		code, obj := e.run("pre", e.cardInput("PreToolUse", cardQuestions("IntentGate", q), ""))
		if code != 0 || len(e.pendingFiles()) != 1 {
			t.Fatalf("pre: exit %d, %v, pending %v", code, obj, e.pendingFiles())
		}
		e.now = t0.Add(7 * time.Second)
		code, obj = e.run("post", e.cardInput("PostToolUse", cardQuestions("IntentGate", q), "Ratify"))
		if code != 0 || !strings.Contains(obj["systemMessage"], "IntentGate: intent lock written: .intent/") {
			t.Fatalf("post: exit %d, %v", code, obj)
		}
		l, _ := e.onlyLock()
		checkCommittedLock(t, e, l, "hook")
	})
	t.Run("an intent edited after pre is no lock", func(t *testing.T) {
		e := committedEnv(t)
		e.run("pre", e.input("PreToolUse", planWithIntent(intentPath), "ExitPlanMode"))
		e.write(intentPath, intentText+"Sneaked in.\n")
		_, obj := e.run("post", e.input("PostToolUse", planWithIntent(intentPath), "ExitPlanMode"))
		if !strings.HasPrefix(obj["systemMessage"], "IntentGate: no lock: ") || !strings.Contains(obj["systemMessage"], intentPath) {
			t.Errorf("post: %v", obj)
		}
		e.nothingWritten()
	})
	t.Run("an untracked intent is no lock, already at pre", func(t *testing.T) {
		e := newEnv(t, "dev@example.com")
		e.write(intentPath, intentText)
		_, obj := e.run("pre", e.input("PreToolUse", planWithIntent(intentPath), "ExitPlanMode"))
		if !strings.HasPrefix(obj["systemMessage"], "IntentGate: no lock: ") || !strings.Contains(obj["systemMessage"], intentPath) || len(e.pendingFiles()) != 0 {
			t.Errorf("pre: %v, pending %v", obj, e.pendingFiles())
		}
		e.nothingWritten()
	})
	t.Run("a path that breaks the rule is no lock", func(t *testing.T) {
		e := committedEnv(t)
		_, obj := e.run("pre", e.input("PreToolUse", planWithIntent("../x.md"), "ExitPlanMode"))
		if !strings.HasPrefix(obj["systemMessage"], "IntentGate: no lock: ") || len(e.pendingFiles()) != 0 {
			t.Errorf("pre: %v, pending %v", obj, e.pendingFiles())
		}
		e.nothingWritten()
	})
	t.Run("a secret in the file is no lock", func(t *testing.T) {
		e := newEnv(t, "dev@example.com")
		e.commitIntent(intentPath, "Use "+planSecret+"\n")
		_, obj := e.run("pre", e.input("PreToolUse", planWithIntent(intentPath), "ExitPlanMode"))
		if !strings.Contains(obj["systemMessage"], "the committed intent contains what looks like a secret") || strings.Contains(obj["systemMessage"], planSecret) {
			t.Errorf("pre: %v", obj)
		}
		e.nothingWritten()
	})
}
