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

	"github.com/autox-my/intent-gate/internal/lock"
)

// S-18: the ratify card (ratification.md §2.1). Plans, questions and answers are synthetic.
//
// The answer shape follows Claude Code: PostToolUse's tool_response for AskUserQuestion is the
// tool's output object {questions, answers}, answers mapping each question's text to the chosen
// option label (hooks reference, "AskUserQuestion"; the same object is `toolUseResult` in a local
// transcript).

func sum(s string) string {
	h := sha256.Sum256([]byte(s))
	return hex.EncodeToString(h[:])
}

func cardQuestion(path, plan string) string {
	return "Ratify the plan in " + path + " (sha256 " + sum(plan) + ")?"
}

func cardQuestions(header, question string) []any {
	return []any{map[string]any{
		"question": question, "header": header, "multiSelect": false,
		"options": []any{
			map[string]any{"label": "Ratify", "description": "Write the intent lock for this plan"},
			map[string]any{"label": "Not yet", "description": "No lock"},
		},
	}}
}

// cardInput is a PreToolUse (answer "" and event PreToolUse) or PostToolUse input for AskUserQuestion.
func (e *env) cardInput(event string, questions []any, answer string) string {
	in := map[string]any{
		"session_id": "sess-1", "transcript_path": e.tpath, "cwd": filepath.Join(e.repo, "sub"),
		"hook_event_name": event, "tool_name": "AskUserQuestion", "tool_use_id": "toolu_card",
		"tool_input": map[string]any{"questions": questions},
	}
	if event == "PostToolUse" {
		answers := map[string]any{}
		if len(questions) > 0 {
			answers[questions[0].(map[string]any)["question"].(string)] = answer
		}
		in["tool_response"] = map[string]any{"questions": questions, "answers": answers}
	}
	b, _ := json.Marshal(in)
	return string(b)
}

func writeFile(t *testing.T, path, text string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(text), 0o600); err != nil {
		t.Fatal(err)
	}
}

func TestRatifyQuestion(t *testing.T) {
	e := newEnv(t, "dev@example.com")
	plan := filepath.Join(e.repo, "docs", "plan.md")
	writeFile(t, plan, planText)
	outside := filepath.Join(t.TempDir(), "plan.md")
	writeFile(t, outside, otherPlanText)

	type card struct {
		Header   string `json:"header"`
		Question string `json:"question"`
		Options  []struct {
			Label       string `json:"label"`
			Description string `json:"description"`
		} `json:"options"`
		MultiSelect bool `json:"multiSelect"`
	}
	run := func(dir string, args ...string) (int, string) {
		var out bytes.Buffer
		deps := e.deps
		deps.Dir = dir
		code := Run(append([]string{"hook", "claude-code", "ratify-question"}, args...), strings.NewReader(""), &out, deps)
		return code, out.String()
	}
	for _, c := range []struct {
		name, dir, arg, wantPath, wantPlan string
	}{
		{"absolute path in the repo is printed repo-relative", e.repo, plan, "docs/plan.md", planText},
		{"relative path from a subdirectory", filepath.Join(e.repo, "sub"), "../docs/plan.md", "docs/plan.md", planText},
		{"path outside the repo stays absolute", e.repo, outside, outside, otherPlanText},
	} {
		t.Run(c.name, func(t *testing.T) {
			code, out := run(c.dir, c.arg)
			if code != 0 {
				t.Fatalf("exit %d, output %q", code, out)
			}
			var got card
			dec := json.NewDecoder(strings.NewReader(out))
			dec.DisallowUnknownFields()
			if err := dec.Decode(&got); err != nil || dec.More() {
				t.Fatalf("output %q is not one card object: %v", out, err)
			}
			if got.Header != "IntentGate" || got.MultiSelect {
				t.Errorf("header %q multiSelect %v", got.Header, got.MultiSelect)
			}
			if want := cardQuestion(c.wantPath, c.wantPlan); got.Question != want {
				t.Errorf("question = %q, want %q", got.Question, want)
			}
			if len(got.Options) != 2 || got.Options[0].Label != "Ratify" || got.Options[0].Description != "Write the intent lock for this plan" ||
				got.Options[1].Label != "Not yet" || got.Options[1].Description != "No lock" {
				t.Errorf("options = %+v", got.Options)
			}
		})
	}
	t.Run("missing file", func(t *testing.T) {
		code, out := run(e.repo, filepath.Join(e.repo, "docs", "nope.md"))
		if code != 2 || out != "" {
			t.Errorf("exit %d, output %q; want 2 and nothing on stdout", code, out)
		}
	})
	t.Run("no path", func(t *testing.T) {
		if code, _ := run(e.repo); code != 2 {
			t.Errorf("exit %d, want 2", code)
		}
	})
}

func TestHook_RatifyCardWritesLock(t *testing.T) {
	// setup is a repository with its plan at docs/plan.md and a home with a plans folder.
	setup := func(t *testing.T) (*env, string) {
		e := newEnv(t, "dev@example.com")
		t.Setenv("HOME", t.TempDir())
		writeFile(t, filepath.Join(e.repo, "docs", "plan.md"), planText)
		return e, cardQuestion("docs/plan.md", planText)
	}
	preOK := func(t *testing.T, e *env, q string) {
		t.Helper()
		code, obj := e.run("pre", e.cardInput("PreToolUse", cardQuestions("IntentGate", q), ""))
		if code != 0 || len(e.pendingFiles()) != 1 || !strings.Contains(obj["systemMessage"], "Findings are published as SARIF") {
			t.Fatalf("pre: exit %d, %v, pending %v", code, obj, e.pendingFiles())
		}
		e.nothingWritten()
	}

	t.Run("Ratify writes the lock", func(t *testing.T) {
		e, q := setup(t)
		preOK(t, e, q)
		e.now = t0.Add(7 * time.Second)
		code, obj := e.run("post", e.cardInput("PostToolUse", cardQuestions("IntentGate", q), "Ratify"))
		if code != 0 || !strings.Contains(obj["systemMessage"], "IntentGate: intent lock written: .intent/") {
			t.Fatalf("post: exit %d, %v", code, obj)
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
		if ps := lock.Validate(l); len(ps) != 0 || !lock.VerifyHash(l) {
			t.Errorf("lock invalid: %v", ps)
		}
		if l.Ratification.Method != "hook" || l.RootIntent.Source != "claude-code-session" || l.Ratification.Ratifier != "git:dev@example.com" {
			t.Errorf("ratification %+v, source %q", l.Ratification, l.RootIntent.Source)
		}
		if len(l.Criteria) != 2 || l.Criteria[0].Text != "Findings are published as SARIF" || l.Criteria[1].ID != "C2" {
			t.Errorf("criteria = %+v", l.Criteria)
		}
		if l.Ratification.ReviewDurationMS == nil || *l.Ratification.ReviewDurationMS != 7000 {
			t.Errorf("review_duration_ms = %v, want 7000", l.Ratification.ReviewDurationMS)
		}
		if len(e.pendingFiles()) != 0 {
			t.Errorf("pending after post: %v", e.pendingFiles())
		}
		recs, _ := filepath.Glob(filepath.Join(e.state, "locks", "*", "root-intent.md"))
		if len(recs) != 1 {
			t.Fatalf("root intent records: %v", recs)
		}
		if rec, _ := os.ReadFile(recs[0]); string(rec) != wantRootIntent {
			t.Errorf("root intent = %q, want %q", rec, wantRootIntent)
		}
	})

	t.Run("a plan in the plans folder", func(t *testing.T) {
		e, _ := setup(t)
		home, _ := os.UserHomeDir()
		abs := filepath.Join(home, ".claude", "plans", "p.md")
		writeFile(t, abs, planText)
		q := cardQuestion(abs, planText)
		preOK(t, e, q)
		e.now = t0.Add(time.Second)
		if _, obj := e.run("post", e.cardInput("PostToolUse", cardQuestions("IntentGate", q), "Ratify")); !strings.Contains(obj["systemMessage"], "intent lock written") || len(e.locks()) != 1 {
			t.Errorf("post: %v, locks %v", obj, e.locks())
		}
	})

	t.Run("Not yet writes nothing", func(t *testing.T) {
		e, q := setup(t)
		preOK(t, e, q)
		e.now = t0.Add(time.Second)
		code, obj := e.run("post", e.cardInput("PostToolUse", cardQuestions("IntentGate", q), "Not yet"))
		if code != 0 || obj["systemMessage"] != "IntentGate: no lock (not ratified)" {
			t.Errorf("post: exit %d, %v", code, obj)
		}
		e.nothingWritten()
	})

	t.Run("another answer writes nothing", func(t *testing.T) {
		e, q := setup(t)
		preOK(t, e, q)
		_, obj := e.run("post", e.cardInput("PostToolUse", cardQuestions("IntentGate", q), "Ratify, and also push"))
		if obj["systemMessage"] != "IntentGate: no lock (not ratified)" {
			t.Errorf("post: %v", obj)
		}
		e.nothingWritten()
	})

	t.Run("a plan edited between pre and post", func(t *testing.T) {
		e, q := setup(t)
		preOK(t, e, q)
		writeFile(t, filepath.Join(e.repo, "docs", "plan.md"), otherPlanText)
		_, obj := e.run("post", e.cardInput("PostToolUse", cardQuestions("IntentGate", q), "Ratify"))
		if !strings.HasPrefix(obj["systemMessage"], "IntentGate: no lock: ") || !strings.Contains(obj["systemMessage"], "the plan changed since the question") {
			t.Errorf("post: %v", obj)
		}
		e.nothingWritten()
	})

	t.Run("a plan edited before pre", func(t *testing.T) {
		e, q := setup(t)
		writeFile(t, filepath.Join(e.repo, "docs", "plan.md"), otherPlanText)
		_, obj := e.run("pre", e.cardInput("PreToolUse", cardQuestions("IntentGate", q), ""))
		if !strings.Contains(obj["systemMessage"], "the plan changed since the question") || len(e.pendingFiles()) != 0 {
			t.Errorf("pre: %v, pending %v", obj, e.pendingFiles())
		}
		e.nothingWritten()
	})

	t.Run("post without pre", func(t *testing.T) {
		e, q := setup(t)
		_, obj := e.run("post", e.cardInput("PostToolUse", cardQuestions("IntentGate", q), "Ratify"))
		if !strings.HasPrefix(obj["systemMessage"], "IntentGate: no lock: ") {
			t.Errorf("post: %v", obj)
		}
		e.nothingWritten()
	})

	t.Run("unmarked questions are none of our business", func(t *testing.T) {
		e, q := setup(t)
		two := append(cardQuestions("IntentGate", q), cardQuestions("Other", "Which one?")...)
		for _, c := range []struct {
			name string
			qs   []any
		}{
			{"another header", cardQuestions("Intentgate", q)},
			{"another question", cardQuestions("IntentGate", "Which framework?")},
			{"a trailing space", cardQuestions("IntentGate", q+" ")},
			{"two questions", two},
			{"no questions", []any{}},
		} {
			for _, ev := range []string{"PreToolUse", "PostToolUse"} {
				which := map[string]string{"PreToolUse": "pre", "PostToolUse": "post"}[ev]
				code, obj := e.run(which, e.cardInput(ev, c.qs, "Ratify"))
				if code != 0 || len(obj) != 0 {
					t.Errorf("%s %s: exit %d, %v; want {}", c.name, which, code, obj)
				}
			}
		}
		e.nothingWritten()
		if len(e.pendingFiles()) != 0 {
			t.Errorf("pending: %v", e.pendingFiles())
		}
	})

	t.Run("a path outside the repo and the plans folder", func(t *testing.T) {
		e, _ := setup(t)
		outside := filepath.Join(t.TempDir(), "plan.md")
		writeFile(t, outside, planText)
		q := cardQuestion(outside, planText)
		for _, c := range []struct{ which, event, answer string }{{"pre", "PreToolUse", ""}, {"post", "PostToolUse", "Ratify"}} {
			_, obj := e.run(c.which, e.cardInput(c.event, cardQuestions("IntentGate", q), c.answer))
			if !strings.HasPrefix(obj["systemMessage"], "IntentGate: no lock: ") || !strings.Contains(obj["systemMessage"], "plan path outside the repo and the plans folder") {
				t.Errorf("%s: %v", c.which, obj)
			}
		}
		e.nothingWritten()
		if len(e.pendingFiles()) != 0 {
			t.Errorf("pending: %v", e.pendingFiles())
		}
	})

	t.Run("a path that climbs out of the repo", func(t *testing.T) {
		e, _ := setup(t)
		outside := filepath.Join(t.TempDir(), "plan.md")
		writeFile(t, outside, planText)
		rel, err := filepath.Rel(e.repo, outside)
		if err != nil {
			t.Skip(err)
		}
		_, obj := e.run("pre", e.cardInput("PreToolUse", cardQuestions("IntentGate", cardQuestion(filepath.ToSlash(rel), planText)), ""))
		if !strings.Contains(obj["systemMessage"], "plan path outside the repo and the plans folder") {
			t.Errorf("pre: %v", obj)
		}
		e.nothingWritten()
	})
}

func TestInit_RegistersAskUserQuestion(t *testing.T) {
	r := newVRepo(t)
	if code, _, errs := initRun(t, r.dir); code != 0 {
		t.Fatalf("exit %d; %s", code, errs)
	}
	type hook struct {
		Command string `json:"command"`
		Timeout int    `json:"timeout"`
	}
	type entry struct {
		Matcher string `json:"matcher"`
		Hooks   []hook `json:"hooks"`
	}
	read := func() map[string][]entry {
		var s struct {
			Hooks map[string][]entry `json:"hooks"`
		}
		if err := json.Unmarshal([]byte(r.read(".claude/settings.json")), &s); err != nil {
			t.Fatal(err)
		}
		return s.Hooks
	}
	hooks := read()
	for ev, want := range map[string]hook{
		"PreToolUse":  {"intentgate hook claude-code pre", 60},
		"PostToolUse": {"intentgate hook claude-code post", 30},
	} {
		n := 0
		for _, e := range hooks[ev] {
			if e.Matcher == "AskUserQuestion" {
				n++
				if len(e.Hooks) != 1 || e.Hooks[0] != want {
					t.Errorf("%s AskUserQuestion entry = %+v, want %+v", ev, e.Hooks, want)
				}
			}
		}
		if n != 1 {
			t.Errorf("%s: %d AskUserQuestion entries, want 1: %+v", ev, n, hooks[ev])
		}
	}
	before := r.read(".claude/settings.json")
	code, out, errs := initRun(t, r.dir)
	if code != 0 || r.read(".claude/settings.json") != before || !lineWith(out, "unchanged", ".claude/settings.json") {
		t.Errorf("second run: exit %d (%s), settings changed or not reported unchanged:\n%s", code, errs, out)
	}

	t.Run("an existing ExitPlanMode hook still gets the card entries", func(t *testing.T) {
		r := newVRepo(t)
		r.write(".claude/settings.json", `{"hooks": {
  "PreToolUse": [{"matcher": "ExitPlanMode", "hooks": [{"type": "command", "command": "intentgate hook claude-code pre"}]}],
  "PostToolUse": [{"matcher": "ExitPlanMode", "hooks": [{"type": "command", "command": "intentgate hook claude-code post"}]}]
}}`)
		if code, _, errs := initRun(t, r.dir); code != 0 {
			t.Fatalf("exit %d; %s", code, errs)
		}
		for ev, n := range map[string]int{"PreToolUse": 2, "PostToolUse": 2} {
			if got := len(read()[ev]); got != n {
				t.Errorf("%s has %d entries, want %d", ev, got, n)
			}
		}
	})
	t.Run("a catch-all entry already covers the card", func(t *testing.T) {
		r := newVRepo(t)
		r.write(".claude/settings.json", `{"hooks": {
  "PreToolUse": [{"matcher": "", "hooks": [{"type": "command", "command": "intentgate hook claude-code pre"}]}],
  "PostToolUse": [{"matcher": "", "hooks": [{"type": "command", "command": "intentgate hook claude-code post"}]}]
}}`)
		if code, _, errs := initRun(t, r.dir); code != 0 {
			t.Fatalf("exit %d; %s", code, errs)
		}
		for _, ev := range []string{"PreToolUse", "PostToolUse"} {
			if got := len(read()[ev]); got != 1 {
				t.Errorf("%s has %d entries, want the existing one only", ev, got)
			}
		}
	})
}
