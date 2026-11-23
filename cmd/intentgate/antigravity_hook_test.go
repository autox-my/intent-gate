package main

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/autox-my/intent-gate/internal/lock"
)

func agyCardQuestion(path, plan string) string {
	return "IntentGate: Ratify the plan in " + path + " (sha256 " + sum(plan) + ")?"
}

func agyCardQuestions(question string) []any {
	return []any{map[string]any{
		"question":        question,
		"options":         []string{"Ratify", "Not yet"},
		"is_multi_select": false,
	}}
}

type agyEnv struct {
	*env
	artDir string
}

func newAgyEnv(t *testing.T) *agyEnv {
	t.Helper()
	e := newEnv(t, "dev@example.com")
	t.Setenv("HOME", t.TempDir())
	artDir := t.TempDir()
	lines := []string{
		agyUserLine(0, "USER_EXPLICIT", "Please add SARIF output."),
		agyPlannerLine(1, "gemini-pro-orchestrator-neon", ""),
		agyUserLine(2, "USER_EXPLICIT", "Keep it small."),
	}
	if err := os.WriteFile(e.tpath, []byte(strings.Join(lines, "\n")+"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	return &agyEnv{env: e, artDir: artDir}
}

func agyUserLine(step int, source, content string) string {
	b, _ := json.Marshal(map[string]any{
		"step_index": step, "source": source, "type": "USER_INPUT", "status": "DONE", "content": content,
	})
	return string(b)
}

func agyPlannerLine(step int, model, cardQ string) string {
	m := map[string]any{
		"step_index": step, "source": "MODEL", "type": "PLANNER_RESPONSE", "status": "DONE", "model": model,
	}
	if cardQ != "" {
		m["tool_calls"] = []any{map[string]any{
			"name": "ask_question",
			"args": map[string]any{"questions": agyCardQuestions(cardQ)},
		}}
	}
	b, _ := json.Marshal(m)
	return string(b)
}

func agyGenericLine(step int, status, content string) string {
	b, _ := json.Marshal(map[string]any{
		"step_index": step, "source": "MODEL", "type": "GENERIC", "status": status, "content": content,
	})
	return string(b)
}

func (a *agyEnv) preInput(step int, tool string, questions []any) string {
	in := map[string]any{
		"hookEventName":         "PreToolUse",
		"conversationId":        "conv-1",
		"transcriptPath":        a.tpath,
		"artifactDirectoryPath": a.artDir,
		"cwd":                   filepath.Join(a.repo, "sub"),
		"modelName":             "gemini-pro-orchestrator-neon",
		"stepIdx":               step,
		"toolCall": map[string]any{
			"id":   "call_1",
			"name": tool,
			"args": map[string]any{"questions": questions},
		},
	}
	b, _ := json.Marshal(in)
	return string(b)
}

func (a *agyEnv) postInvInput(inv, initSteps int) string {
	in := map[string]any{
		"hookEventName":         "PostInvocation",
		"conversationId":        "conv-1",
		"transcriptPath":        a.tpath,
		"artifactDirectoryPath": a.artDir,
		"cwd":                   filepath.Join(a.repo, "sub"),
		"modelName":             "gemini-pro-orchestrator-neon",
		"invocationNum":         inv,
		"initialNumSteps":       initSteps,
	}
	b, _ := json.Marshal(in)
	return string(b)
}

func (a *agyEnv) runHook(which, stdin string) (int, map[string]any) {
	a.t.Helper()
	var out bytes.Buffer
	code := Run([]string{"hook", "antigravity", which}, strings.NewReader(stdin), &out, a.deps)
	var obj map[string]any
	dec := json.NewDecoder(bytes.NewReader(out.Bytes()))
	if err := dec.Decode(&obj); err != nil {
		a.t.Fatalf("stdout %q is not a JSON object (stderr=%q): %v", out.String(), a.deps.Stderr.(*bytes.Buffer).String(), err)
	}
	if dec.More() {
		a.t.Fatalf("stdout %q holds more than one JSON value", out.String())
	}
	if a.deps.Stderr.(*bytes.Buffer).Len() != 0 {
		a.t.Errorf("stderr: %q", a.deps.Stderr.(*bytes.Buffer).String())
	}
	return code, obj
}

func injectMessage(t *testing.T, obj map[string]any) string {
	t.Helper()
	steps, _ := obj["injectSteps"].([]any)
	if len(steps) != 1 {
		return ""
	}
	step, _ := steps[0].(map[string]any)
	sm, _ := step["systemMessage"].(map[string]any)
	msg, _ := sm["systemMessage"].(string)
	meta, _ := sm["metadata"].(map[string]any)
	if md, _ := meta["user_markdown"].(string); md != msg {
		t.Errorf("user_markdown = %q, want %q", md, msg)
	}
	return msg
}

func TestAntigravityRatifyQuestion(t *testing.T) {
	e := newEnv(t, "dev@example.com")
	plan := filepath.Join(e.repo, "docs", "plan.md")
	writeFile(t, plan, planText)
	outside := filepath.Join(t.TempDir(), "plan.md")
	writeFile(t, outside, otherPlanText)

	type card struct {
		Question    string   `json:"question"`
		Options     []string `json:"options"`
		MultiSelect bool     `json:"is_multi_select"`
	}
	run := func(dir string, args ...string) (int, string) {
		var out bytes.Buffer
		deps := e.deps
		deps.Dir = dir
		code := Run(append([]string{"hook", "antigravity", "ratify-question"}, args...), strings.NewReader(""), &out, deps)
		return code, out.String()
	}
	for _, c := range []struct{ name, dir, arg, wantPath, wantPlan string }{
		{"repo-relative", e.repo, plan, "docs/plan.md", planText},
		{"subdirectory relative", filepath.Join(e.repo, "sub"), "../docs/plan.md", "docs/plan.md", planText},
		{"outside repo stays absolute", e.repo, outside, outside, otherPlanText},
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
			if got.MultiSelect || got.Question != agyCardQuestion(c.wantPath, c.wantPlan) {
				t.Errorf("got = %+v", got)
			}
			if len(got.Options) != 2 || got.Options[0] != "Ratify" || got.Options[1] != "Not yet" {
				t.Errorf("options = %v", got.Options)
			}
		})
	}
	t.Run("missing file exits 2", func(t *testing.T) {
		code, out := run(e.repo, filepath.Join(e.repo, "docs", "nope.md"))
		if code != 2 || out != "" {
			t.Errorf("exit %d, out %q; want 2, empty", code, out)
		}
	})
	t.Run("no path exits 2", func(t *testing.T) {
		if code, _ := run(e.repo); code != 2 {
			t.Errorf("exit %d, want 2", code)
		}
	})
}

func TestAntigravityPrintConfig(t *testing.T) {
	var out, errb bytes.Buffer
	code := Run([]string{"hook", "antigravity", "print-config"}, strings.NewReader(""), &out, Deps{Stderr: &errb})
	if code != 0 || errb.Len() != 0 {
		t.Fatalf("exit %d, stderr %q", code, errb.String())
	}
	var doc map[string]any
	if err := json.Unmarshal(out.Bytes(), &doc); err != nil {
		t.Fatalf("invalid JSON %q: %v", out.String(), err)
	}
	ig, _ := doc["intentgate"].(map[string]any)
	if ig["enabled"] != true {
		t.Errorf("enabled = %v", ig["enabled"])
	}
	pre, _ := ig["PreToolUse"].([]any)
	if len(pre) != 1 {
		t.Fatalf("PreToolUse = %v", pre)
	}
	preGroup, _ := pre[0].(map[string]any)
	if preGroup["matcher"] != "ask_question" {
		t.Errorf("matcher = %v", preGroup["matcher"])
	}
	preHooks, _ := preGroup["hooks"].([]any)
	if len(preHooks) != 1 || preHooks[0].(map[string]any)["command"] != "intentgate hook antigravity pre" || preHooks[0].(map[string]any)["timeout"] != float64(60) {
		t.Errorf("pre hooks = %v", preHooks)
	}
	postInv, _ := ig["PostInvocation"].([]any)
	if len(postInv) != 1 || postInv[0].(map[string]any)["command"] != "intentgate hook antigravity post-invocation" || postInv[0].(map[string]any)["timeout"] != float64(30) {
		t.Errorf("PostInvocation = %v", postInv)
	}
}

// REQ-0002
func TestAntigravityHook_RatifyCardWritesLock(t *testing.T) {
	appendTranscript := func(t *testing.T, a *agyEnv, lines ...string) {
		t.Helper()
		b, err := os.ReadFile(a.tpath)
		if err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(a.tpath, append(b, []byte(strings.Join(lines, "\n")+"\n")...), 0o600); err != nil {
			t.Fatal(err)
		}
	}

	t.Run("Ratify and Ratify with note write lock", func(t *testing.T) {
		for _, ans := range []string{"A1: Ratify", "A1: Ratify, yes"} {
			a := newAgyEnv(t)
			writeFile(t, filepath.Join(a.repo, "docs", "plan.md"), planText)
			q := agyCardQuestion("docs/plan.md", planText)
			appendTranscript(t, a, agyPlannerLine(3, "gemini-pro-orchestrator-neon", q))

			code, obj := a.runHook("pre", a.preInput(4, "ask_question", agyCardQuestions(q)))
			if code != 0 || obj["decision"] != "allow" || len(a.pendingFiles()) != 1 {
				t.Fatalf("%q pre: exit %d, %v, pending %v", ans, code, obj, a.pendingFiles())
			}
			a.nothingWritten()

			appendTranscript(t, a, agyGenericLine(4, "DONE", "Created At: x\nCompleted At: y\n"+ans))
			a.now = t0.Add(7 * time.Second)
			code, obj = a.runHook("post-invocation", a.postInvInput(2, 3))
			msg := injectMessage(t, obj)
			if code != 0 || !strings.Contains(msg, "IntentGate: intent lock written: .intent/") || !strings.Contains(msg, "root intent:") {
				t.Fatalf("%q post-invocation: exit %d, %v", ans, code, obj)
			}
			files := a.locks()
			if len(files) != 1 || len(a.pendingFiles()) != 0 {
				t.Fatalf("locks=%v pending=%v", files, a.pendingFiles())
			}
			data, _ := os.ReadFile(files[0])
			l, err := lock.Parse(data)
			if err != nil {
				t.Fatal(err)
			}
			if ps := lock.Validate(l); len(ps) != 0 || !lock.VerifyHash(l) {
				t.Errorf("invalid lock: %v", ps)
			}
			if l.Ratification.Method != "hook" || l.RootIntent.Source != "antigravity-session" {
				t.Errorf("ratification=%+v source=%q", l.Ratification, l.RootIntent.Source)
			}
			if l.Ratification.DraftedBy == nil || *l.Ratification.DraftedBy != "antigravity/gemini-pro-orchestrator-neon" {
				t.Errorf("drafted_by=%v", l.Ratification.DraftedBy)
			}
			if l.Ratification.ReviewDurationMS == nil || *l.Ratification.ReviewDurationMS != 7000 {
				t.Errorf("review_duration_ms=%v", l.Ratification.ReviewDurationMS)
			}
			if len(l.Criteria) != 2 {
				t.Errorf("criteria=%+v", l.Criteria)
			}
			recs, _ := filepath.Glob(filepath.Join(a.state, "locks", "*", "root-intent.md"))
			if len(recs) != 1 {
				t.Fatalf("root intent recs=%v", recs)
			}
			if rec, _ := os.ReadFile(recs[0]); string(rec) != wantRootIntent {
				t.Errorf("root intent = %q, want %q", rec, wantRootIntent)
			}
		}
	})

	t.Run("Not yet writes nothing", func(t *testing.T) {
		a := newAgyEnv(t)
		writeFile(t, filepath.Join(a.repo, "docs", "plan.md"), planText)
		q := agyCardQuestion("docs/plan.md", planText)
		appendTranscript(t, a, agyPlannerLine(3, "gemini-pro-orchestrator-neon", q))
		a.runHook("pre", a.preInput(4, "ask_question", agyCardQuestions(q)))
		appendTranscript(t, a, agyGenericLine(4, "DONE", "A1: Not yet"))
		code, obj := a.runHook("post-invocation", a.postInvInput(2, 3))
		if code != 0 || injectMessage(t, obj) != "IntentGate: no lock (not ratified)" {
			t.Errorf("exit %d, %v", code, obj)
		}
		a.nothingWritten()
		if len(a.pendingFiles()) != 0 {
			t.Errorf("pending left behind: %v", a.pendingFiles())
		}
	})

	t.Run("plan edited between pre and post-invocation", func(t *testing.T) {
		a := newAgyEnv(t)
		p := filepath.Join(a.repo, "docs", "plan.md")
		writeFile(t, p, planText)
		q := agyCardQuestion("docs/plan.md", planText)
		appendTranscript(t, a, agyPlannerLine(3, "gemini-pro-orchestrator-neon", q))
		a.runHook("pre", a.preInput(4, "ask_question", agyCardQuestions(q)))
		writeFile(t, p, otherPlanText)
		appendTranscript(t, a, agyGenericLine(4, "DONE", "A1: Ratify"))
		_, obj := a.runHook("post-invocation", a.postInvInput(2, 3))
		if msg := injectMessage(t, obj); !strings.Contains(msg, "the plan changed since the question") {
			t.Errorf("got %q", msg)
		}
		a.nothingWritten()
	})

	t.Run("ordinary ask_question writes no pending and returns empty post-invocation", func(t *testing.T) {
		a := newAgyEnv(t)
		qs := []any{map[string]any{"question": "Pick one?", "options": []string{"A", "B"}, "is_multi_select": false}}
		code, obj := a.runHook("pre", a.preInput(4, "ask_question", qs))
		if code != 0 || obj["decision"] != "allow" || len(a.pendingFiles()) != 0 {
			t.Fatalf("pre: exit %d, %v, pending %v", code, obj, a.pendingFiles())
		}
		code, obj = a.runHook("post-invocation", a.postInvInput(2, 3))
		if code != 0 || len(obj) != 0 {
			t.Errorf("post-invocation: exit %d, %v", code, obj)
		}
	})

	t.Run("out-of-scope plan path rejected before filesystem access", func(t *testing.T) {
		a := newAgyEnv(t)
		outside := filepath.Join(t.TempDir(), "plan.md")
		writeFile(t, outside, planText)
		missingOutside := filepath.Join(t.TempDir(), "nope.md")
		for _, p := range []string{outside, missingOutside} {
			q := agyCardQuestion(p, planText)
			step := 4
			a.runHook("pre", a.preInput(step, "ask_question", agyCardQuestions(q)))
			appendTranscript(t, a, agyGenericLine(step, "DONE", "A1: Ratify"))
			_, obj := a.runHook("post-invocation", a.postInvInput(2, 3))
			if msg := injectMessage(t, obj); !strings.Contains(msg, "plan path outside the repo and the artifact directory") {
				t.Errorf("%s: got %q", p, msg)
			}
			a.nothingWritten()
		}
	})

	t.Run("plan inside artifactDirectoryPath writes lock", func(t *testing.T) {
		a := newAgyEnv(t)
		p := filepath.Join(a.artDir, "implementation_plan.md")
		writeFile(t, p, planText)
		q := agyCardQuestion(p, planText)
		appendTranscript(t, a, agyPlannerLine(3, "gemini-pro-orchestrator-neon", q))
		a.runHook("pre", a.preInput(4, "ask_question", agyCardQuestions(q)))
		appendTranscript(t, a, agyGenericLine(4, "DONE", "A1: Ratify"))
		_, obj := a.runHook("post-invocation", a.postInvInput(2, 3))
		if msg := injectMessage(t, obj); !strings.Contains(msg, "intent lock written") || len(a.locks()) != 1 {
			t.Errorf("got %q, locks %v", msg, a.locks())
		}
	})

	t.Run("committed intent writes committed-intent lock without local root-intent", func(t *testing.T) {
		a := newAgyEnv(t)
		a.commitIntent(intentPath, intentText)
		plan := planWithIntent(intentPath)
		writeFile(t, filepath.Join(a.repo, "docs", "plan.md"), plan)
		q := agyCardQuestion("docs/plan.md", plan)
		appendTranscript(t, a, agyPlannerLine(3, "gemini-pro-orchestrator-neon", q))
		a.runHook("pre", a.preInput(4, "ask_question", agyCardQuestions(q)))
		appendTranscript(t, a, agyGenericLine(4, "DONE", "A1: Ratify"))
		_, obj := a.runHook("post-invocation", a.postInvInput(2, 3))
		msg := injectMessage(t, obj)
		if !strings.Contains(msg, "intent lock written") || len(a.locks()) != 1 {
			t.Fatalf("got %q, locks %v", msg, a.locks())
		}
		data, _ := os.ReadFile(a.locks()[0])
		l, err := lock.Parse(data)
		if err != nil {
			t.Fatal(err)
		}
		checkCommittedLock(t, a.env, l, "hook")
	})

	t.Run("repo-wide footprint warns and writes lock", func(t *testing.T) {
		a := newAgyEnv(t)
		writeFile(t, filepath.Join(a.repo, "docs", "plan.md"), repoWidePlanText)
		q := agyCardQuestion("docs/plan.md", repoWidePlanText)
		appendTranscript(t, a, agyPlannerLine(3, "gemini-pro-orchestrator-neon", q))
		a.runHook("pre", a.preInput(4, "ask_question", agyCardQuestions(q)))
		appendTranscript(t, a, agyGenericLine(4, "DONE", "A1: Ratify"))
		_, obj := a.runHook("post-invocation", a.postInvInput(2, 3))
		if msg := injectMessage(t, obj); !strings.Contains(msg, "intent lock written") || !strings.Contains(msg, repoWideWarning) || len(a.locks()) != 1 {
			t.Errorf("got %q, locks %v", msg, a.locks())
		}
	})

	t.Run("fail open on malformed input, wrong event, missing StateDir, extra args", func(t *testing.T) {
		a := newAgyEnv(t)
		for _, bad := range []string{"{not json", `{"hookEventName":"PostInvocation","toolCall":{"name":"ask_question"}}`} {
			code, obj := a.runHook("pre", bad)
			if code != 0 || obj["decision"] != "allow" {
				t.Errorf("pre %q: exit %d, %v", bad, code, obj)
			}
		}
		aNoState := newAgyEnv(t)
		aNoState.deps.StateDir = ""
		code, obj := aNoState.runHook("pre", aNoState.preInput(4, "ask_question", agyCardQuestions(agyCardQuestion("docs/plan.md", planText))))
		if code != 0 || obj["decision"] != "allow" {
			t.Errorf("pre no StateDir: exit %d, %v", code, obj)
		}
		code, obj = a.runHook("post-invocation", "{not json")
		if code != 0 || obj == nil {
			t.Errorf("post-invocation bad JSON: exit %d, %v", code, obj)
		}
		var extraOut bytes.Buffer
		if code := Run([]string{"hook", "antigravity", "pre", "extra"}, strings.NewReader(""), &extraOut, a.deps); code != 0 || strings.TrimSpace(extraOut.String()) != `{"decision":"allow"}` {
			t.Errorf("pre extra: exit %d, %q", code, extraOut.String())
		}
		extraOut.Reset()
		if code := Run([]string{"hook", "antigravity", "post-invocation", "extra"}, strings.NewReader(""), &extraOut, a.deps); code != 0 || !json.Valid(extraOut.Bytes()) {
			t.Errorf("post-invocation extra: exit %d, %q", code, extraOut.String())
		}
	})
}

func TestAntigravityHook_PostInvocationStalePending(t *testing.T) {
	a := newAgyEnv(t)
	writeFile(t, filepath.Join(a.repo, "docs", "plan.md"), planText)
	q := agyCardQuestion("docs/plan.md", planText)

	// Step 4 (< initialNumSteps=5) is absent from transcript -> pruned quietly.
	a.runHook("pre", a.preInput(4, "ask_question", agyCardQuestions(q)))
	// Step 6 (>= initialNumSteps=5) is absent from transcript -> kept.
	a.runHook("pre", a.preInput(6, "ask_question", agyCardQuestions(q)))
	if len(a.pendingFiles()) != 2 {
		t.Fatalf("pending = %v, want 2", a.pendingFiles())
	}

	code, obj := a.runHook("post-invocation", a.postInvInput(3, 5))
	if code != 0 || len(obj) != 0 {
		t.Errorf("exit %d, %v; want {}", code, obj)
	}
	left := a.pendingFiles()
	if len(left) != 1 || filepath.Base(left[0]) != "step-6.json" {
		t.Errorf("pending left = %v, want only step-6.json", left)
	}
}
