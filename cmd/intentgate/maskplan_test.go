package main

import (
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/autox-my/intent-gate/internal/lock"
)

// Review 1 of S-06: a plan is masked before criteria are drafted from it, on every path that does so
// (`lock --issue`, `lock --from-plan` and the S-04 hook), because criterion text is committed in
// the lock and printed. The secret below is synthetic.

const planSecret = "ghp_" + "Zq4Wn8Tb2Lk6Vd0Xs9Ym3Hc7Fj1Pa5Ru8Ge2"

const secretPlan = "# Plan\n\nRotate the deploy key.\n\n```intentgate\ncriteria:\n" +
	"  - text: \"Rotate the deploy key " + planSecret + " out of config\"\n    footprint: [\"config/**\"]\n```\n"

// assertNoSecret fails if the secret is in any of the outputs, in a lock file under the repo's
// .intent directory, or in anything under the state directory (the root intent records, pending
// records); it returns the one lock written, after checking it is valid and its criterion masked.
func assertNoSecret(t *testing.T, e *env, outputs map[string]string) lock.Lock {
	t.Helper()
	for name, out := range outputs {
		if strings.Contains(out, planSecret) {
			t.Errorf("the secret is in %s:\n%s", name, out)
		}
	}
	for _, root := range []string{filepath.Join(e.repo, ".intent"), e.state} {
		_ = filepath.WalkDir(root, func(p string, d fs.DirEntry, err error) error {
			if err != nil || d.IsDir() {
				return nil
			}
			if data, _ := os.ReadFile(p); strings.Contains(string(data), planSecret) {
				t.Errorf("the secret is in %s", p)
			}
			return nil
		})
	}
	l, _ := e.onlyLock()
	if ps := lock.Validate(l); len(ps) != 0 {
		t.Errorf("lock.Validate: %v", ps)
	}
	if !lock.VerifyHash(l) {
		t.Error("the hash does not verify")
	}
	if len(l.Criteria) != 1 || !strings.Contains(l.Criteria[0].Text, "Rotate the deploy key [REDACTED:github-pat] out of config") {
		t.Errorf("criteria = %+v, want the secret replaced by [REDACTED:github-pat]", l.Criteria)
	}
	return l
}

func TestLock_PlanSecretIsMasked_FromIssue(t *testing.T) {
	e, api := issueEnv(t)
	api.issue = fmt.Sprintf(`{"number":7,"title":"Rotate","body":%q,"user":{"login":"alice"}}`, secretPlan)
	api.comments = `[]`
	code, stdout, stderr := e.do([]string{"lock", "--issue", "7"}, "yes\n")
	if code != 0 {
		t.Fatalf("exit %d: %s", code, stderr)
	}
	l := assertNoSecret(t, e, map[string]string{"stdout": stdout, "stderr": stderr})
	if !strings.Contains(stdout, "[REDACTED:github-pat]") {
		t.Errorf("stdout shows no redaction:\n%s", stdout)
	}
	if rec, _ := os.ReadFile(filepath.Join(e.state, "locks", l.ID, "root-intent.md")); !strings.Contains(string(rec), "[REDACTED:github-pat]") {
		t.Errorf("root intent record %q holds no redaction", rec)
	}
}

func TestLock_PlanSecretIsMasked_FromPlan(t *testing.T) {
	e := newEnv(t, "dev@example.com")
	e.terminals(true, true)
	plan := e.write("plan.md", secretPlan)
	prompt := e.write("prompt.txt", "Rotate the deploy key.\n")
	code, stdout, stderr := e.do([]string{"lock", "--from-plan", plan, "--prompt", prompt}, "yes\n")
	if code != 0 {
		t.Fatalf("exit %d: %s", code, stderr)
	}
	assertNoSecret(t, e, map[string]string{"stdout": stdout, "stderr": stderr})
	if !strings.Contains(stdout, "[REDACTED:github-pat]") {
		t.Errorf("stdout shows no redaction:\n%s", stdout)
	}
}

func TestHook_PlanSecretIsMasked(t *testing.T) {
	e := newEnv(t, "dev@example.com")
	code, pre := e.run("pre", e.input("PreToolUse", secretPlan, "ExitPlanMode"))
	if code != 0 {
		t.Fatalf("pre exit %d", code)
	}
	code, post := e.run("post", e.input("PostToolUse", secretPlan, "ExitPlanMode"))
	if code != 0 {
		t.Fatalf("post exit %d", code)
	}
	assertNoSecret(t, e, map[string]string{"pre systemMessage": pre["systemMessage"], "post systemMessage": post["systemMessage"]})
	if !strings.Contains(pre["systemMessage"], "[REDACTED:github-pat]") {
		t.Errorf("pre systemMessage shows no redaction: %q", pre["systemMessage"])
	}
}
