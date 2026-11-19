package main

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
	"time"

	"github.com/autox-my/intent-gate/internal/ratify"
)

// The init tests run `intentgate init` against temporary git repositories (S-16).

const (
	markBegin = "<!-- intentgate:begin -->"
	markEnd   = "<!-- intentgate:end -->"
)

func initRun(t *testing.T, dir string, args ...string) (code int, stdout, stderr string) {
	t.Helper()
	var out, errb bytes.Buffer
	deps := Deps{
		Now:    func() time.Time { t.Error("init read the clock"); return time.Time{} },
		Dir:    dir,
		Stderr: &errb,
	}
	code = Run(append([]string{"init"}, args...), strings.NewReader(""), &out, deps)
	return code, out.String(), errb.String()
}

func (r *vrepo) read(name string) string {
	r.t.Helper()
	b, err := os.ReadFile(filepath.Join(r.dir, name))
	if err != nil {
		r.t.Fatal(err)
	}
	return string(b)
}

func (r *vrepo) exists(name string) bool {
	_, err := os.Stat(filepath.Join(r.dir, name))
	return err == nil
}

func (r *vrepo) settings() map[string]any {
	r.t.Helper()
	var m map[string]any
	if err := json.Unmarshal([]byte(r.read(".claude/settings.json")), &m); err != nil {
		r.t.Fatalf(".claude/settings.json is not JSON: %v", err)
	}
	return m
}

func lineWith(out, status, path string) bool {
	for _, l := range strings.Split(out, "\n") {
		if strings.HasPrefix(l, status+" ") && strings.Contains(l, path) {
			return true
		}
	}
	return false
}

// hookCommands lists every command under hooks.<event> entries, leaving out the AskUserQuestion
// entries (S-18): TestInit_RegistersAskUserQuestion checks those.
func hookCommands(t *testing.T, m map[string]any, event string) []string {
	t.Helper()
	var cmds []string
	hooks, _ := m["hooks"].(map[string]any)
	entries, _ := hooks[event].([]any)
	for _, e := range entries {
		em, _ := e.(map[string]any)
		if em["matcher"] == "AskUserQuestion" {
			continue
		}
		hs, _ := em["hooks"].([]any)
		for _, h := range hs {
			hm, _ := h.(map[string]any)
			if c, ok := hm["command"].(string); ok {
				cmds = append(cmds, c)
			}
		}
	}
	return cmds
}

func TestInit_FreshRepo(t *testing.T) {
	r := newVRepo(t)
	code, out, errs := initRun(t, r.dir)
	if code != 0 {
		t.Fatalf("exit %d, want 0; stderr: %s", code, errs)
	}
	m := r.settings()
	if got := hookCommands(t, m, "PreToolUse"); len(got) != 1 || got[0] != "intentgate hook claude-code pre" {
		t.Errorf("PreToolUse commands = %q", got)
	}
	if got := hookCommands(t, m, "PostToolUse"); len(got) != 1 || got[0] != "intentgate hook claude-code post" {
		t.Errorf("PostToolUse commands = %q", got)
	}
	perms, _ := m["permissions"].(map[string]any)
	if perms["defaultMode"] != "plan" {
		t.Errorf("permissions.defaultMode = %v, want plan", perms["defaultMode"])
	}
	raw := r.read(".claude/settings.json")
	if !strings.HasSuffix(raw, "}\n") || !strings.Contains(raw, "\n  \"") {
		t.Errorf("settings.json is not 2-space JSON with a trailing newline:\n%s", raw)
	}
	agents := r.read("AGENTS.md")
	if strings.Count(agents, markBegin) != 1 || strings.Count(agents, markEnd) != 1 {
		t.Fatalf("AGENTS.md should hold one marked section:\n%s", agents)
	}
	for _, want := range []string{"criteria:\n  - text: ", "\n    footprint: ", "Start work in plan mode; approving the plan writes the intent lock."} {
		if !strings.Contains(agents, want) {
			t.Errorf("AGENTS.md lacks %q:\n%s", want, agents)
		}
	}
	if r.exists("CLAUDE.md") {
		t.Error("CLAUDE.md was created; AGENTS.md is the default")
	}
	if got := r.read(".intentgate.yml"); got != "version: 1\n" {
		t.Errorf(".intentgate.yml = %q", got)
	}
	for _, p := range []string{".claude/settings.json", "AGENTS.md", ".intentgate.yml"} {
		if !lineWith(out, "created", p) {
			t.Errorf("no `created %s` line in:\n%s", p, out)
		}
	}
	if r.exists(".intent") {
		t.Error("init wrote a lock directory")
	}
}

func TestInit_MergesExistingSettings(t *testing.T) {
	r := newVRepo(t)
	r.write(".claude/settings.json", `{
  "model": "opus",
  "permissions": {"allow": ["Bash(go test:*)"], "defaultMode": "acceptEdits"},
  "hooks": {
    "PreToolUse": [{"matcher": "Bash", "hooks": [{"type": "command", "command": "echo hi"}]}],
    "Stop": [{"hooks": [{"type": "command", "command": "echo done"}]}]
  }
}
`)
	code, out, errs := initRun(t, r.dir)
	if code != 0 {
		t.Fatalf("exit %d; stderr: %s", code, errs)
	}
	m := r.settings()
	if m["model"] != "opus" {
		t.Errorf("model key lost: %v", m)
	}
	perms, _ := m["permissions"].(map[string]any)
	if perms["defaultMode"] != "acceptEdits" {
		t.Errorf("defaultMode = %v, want acceptEdits kept", perms["defaultMode"])
	}
	if allow, _ := perms["allow"].([]any); len(allow) != 1 {
		t.Errorf("permissions.allow lost: %v", perms)
	}
	pre := hookCommands(t, m, "PreToolUse")
	if len(pre) != 2 || pre[0] != "echo hi" || pre[1] != "intentgate hook claude-code pre" {
		t.Errorf("PreToolUse commands = %q", pre)
	}
	if got := hookCommands(t, m, "Stop"); len(got) != 1 || got[0] != "echo done" {
		t.Errorf("Stop hook lost: %q", got)
	}
	if !strings.Contains(out, "left defaultMode=acceptEdits as is; set it to plan to start sessions in plan mode") {
		t.Errorf("the kept defaultMode is not reported:\n%s", out)
	}
	if !lineWith(out, "updated", ".claude/settings.json") {
		t.Errorf("no `updated .claude/settings.json` line:\n%s", out)
	}

	files := []string{".claude/settings.json", "AGENTS.md", ".intentgate.yml"}
	before := map[string]string{}
	for _, f := range files {
		before[f] = r.read(f)
	}
	code, out, errs = initRun(t, r.dir)
	if code != 0 {
		t.Fatalf("second run: exit %d; stderr: %s", code, errs)
	}
	for _, f := range files {
		if got := r.read(f); got != before[f] {
			t.Errorf("%s changed on the second run:\n%s\n--- was ---\n%s", f, got, before[f])
		}
	}
	for _, p := range []string{".claude/settings.json", "AGENTS.md"} {
		if !lineWith(out, "unchanged", p) {
			t.Errorf("second run: no `unchanged %s` line in:\n%s", p, out)
		}
	}
	if !lineWith(out, "kept", ".intentgate.yml") {
		t.Errorf("second run: no `kept .intentgate.yml` line in:\n%s", out)
	}
}

func TestInit_ExistingIntentgateHooksNotDuplicated(t *testing.T) {
	r := newVRepo(t)
	r.write(".claude/settings.json", `{"hooks": {"PreToolUse": [{"matcher": "ExitPlanMode", "hooks": [{"type": "command", "command": "intentgate hook claude-code pre", "timeout": 5}]}]}}`)
	if code, _, errs := initRun(t, r.dir); code != 0 {
		t.Fatalf("exit %d; stderr: %s", code, errs)
	}
	m := r.settings()
	if got := hookCommands(t, m, "PreToolUse"); len(got) != 1 {
		t.Errorf("PreToolUse commands = %q, want the existing one only", got)
	}
	if got := hookCommands(t, m, "PostToolUse"); len(got) != 1 {
		t.Errorf("PostToolUse commands = %q, want one added", got)
	}
}

func TestInit_InvalidSettings(t *testing.T) {
	r := newVRepo(t)
	const broken = `{"hooks": [`
	r.write(".claude/settings.json", broken)
	code, _, errs := initRun(t, r.dir)
	if code != 3 {
		t.Fatalf("exit %d, want 3; stderr: %s", code, errs)
	}
	if !strings.Contains(errs, ".claude/settings.json") {
		t.Errorf("stderr does not name the file: %s", errs)
	}
	if got := r.read(".claude/settings.json"); got != broken {
		t.Errorf("the invalid file was touched: %q", got)
	}
	if r.exists("AGENTS.md") || r.exists(".intentgate.yml") {
		t.Error("init wrote other files after finding an invalid one")
	}
}

func TestInit_InstructionsFile(t *testing.T) {
	t.Run("prefers AGENTS.md", func(t *testing.T) {
		r := newVRepo(t)
		r.write("AGENTS.md", "# Agents\n")
		r.write("CLAUDE.md", "# Claude\n")
		if code, _, errs := initRun(t, r.dir); code != 0 {
			t.Fatalf("exit %d; %s", code, errs)
		}
		if !strings.Contains(r.read("AGENTS.md"), markBegin) {
			t.Error("AGENTS.md has no section")
		}
		if got := r.read("CLAUDE.md"); got != "# Claude\n" {
			t.Errorf("CLAUDE.md changed: %q", got)
		}
	})
	t.Run("then CLAUDE.md", func(t *testing.T) {
		r := newVRepo(t)
		r.write("CLAUDE.md", "# Claude\n")
		if code, _, errs := initRun(t, r.dir); code != 0 {
			t.Fatalf("exit %d; %s", code, errs)
		}
		if !strings.Contains(r.read("CLAUDE.md"), markBegin) {
			t.Error("CLAUDE.md has no section")
		}
		if r.exists("AGENTS.md") {
			t.Error("AGENTS.md was created though CLAUDE.md exists")
		}
	})
	t.Run("replaces its own section in place", func(t *testing.T) {
		r := newVRepo(t)
		const head, tail = "# Agents\n\nBefore.\n\n", "\n\n## After\n\nKept as is.\n"
		r.write("AGENTS.md", head+markBegin+"\nold text\n"+markEnd+tail)
		if code, _, errs := initRun(t, r.dir); code != 0 {
			t.Fatalf("exit %d; %s", code, errs)
		}
		got := r.read("AGENTS.md")
		if !strings.HasPrefix(got, head+markBegin+"\n") || !strings.HasSuffix(got, markEnd+tail) {
			t.Errorf("the rest of the file changed:\n%s", got)
		}
		if strings.Contains(got, "old text") || strings.Count(got, markBegin) != 1 {
			t.Errorf("the section was not replaced:\n%s", got)
		}
		again := got
		if code, _, errs := initRun(t, r.dir); code != 0 {
			t.Fatalf("second run: exit %d; %s", code, errs)
		}
		if r.read("AGENTS.md") != again {
			t.Error("a second run changed AGENTS.md")
		}
	})
	t.Run("appends to a file without a section", func(t *testing.T) {
		r := newVRepo(t)
		r.write("AGENTS.md", "# Agents\n\nRules.")
		if code, _, errs := initRun(t, r.dir); code != 0 {
			t.Fatalf("exit %d; %s", code, errs)
		}
		got := r.read("AGENTS.md")
		if !strings.HasPrefix(got, "# Agents\n\nRules.\n") || !strings.Contains(got, "\n"+markBegin+"\n") || !strings.HasSuffix(got, markEnd+"\n") {
			t.Errorf("unexpected AGENTS.md:\n%s", got)
		}
	})
}

func TestInit_Generic(t *testing.T) {
	r := newVRepo(t)
	code, out, errs := initRun(t, r.dir, "--agent", "generic")
	if code != 0 {
		t.Fatalf("exit %d; %s", code, errs)
	}
	if r.exists(".claude") {
		t.Error("--agent generic wrote .claude/")
	}
	agents := r.read("AGENTS.md")
	if !strings.Contains(agents, "intentgate lock --from-plan") || strings.Contains(agents, "Start work in plan mode") {
		t.Errorf("the generic section is wrong:\n%s", agents)
	}
	if !r.exists(".intentgate.yml") {
		t.Error(".intentgate.yml missing")
	}
	if strings.Contains(out, ".claude") {
		t.Errorf("output mentions .claude:\n%s", out)
	}
}

func TestInit_DryRun(t *testing.T) {
	r := newVRepo(t)
	code, dry, errs := initRun(t, r.dir, "--dry-run")
	if code != 0 {
		t.Fatalf("exit %d; %s", code, errs)
	}
	for _, p := range []string{".claude", "AGENTS.md", ".intentgate.yml"} {
		if r.exists(p) {
			t.Errorf("--dry-run wrote %s", p)
		}
	}
	if !lineWith(dry, "created", ".claude/settings.json") {
		t.Errorf("no `created` line for settings:\n%s", dry)
	}
	code, real, errs := initRun(t, r.dir)
	if code != 0 {
		t.Fatalf("exit %d; %s", code, errs)
	}
	if dry != real {
		t.Errorf("dry-run output differs from the real run:\n--- dry ---\n%s--- real ---\n%s", dry, real)
	}
}

func TestInit_Usage(t *testing.T) {
	r := newVRepo(t)
	for _, args := range [][]string{{"--agent", "cursor"}, {"extra"}, {"--nope"}} {
		if code, _, _ := initRun(t, r.dir, args...); code != 2 {
			t.Errorf("init %v: exit %d, want 2", args, code)
		}
	}
	if code, _, _ := initRun(t, t.TempDir()); code != 2 {
		t.Errorf("init outside a git repository: exit %d, want 2", code)
	}
}

func TestInit_TemplateDrafts(t *testing.T) {
	r := newVRepo(t)
	if code, _, errs := initRun(t, r.dir); code != 0 {
		t.Fatalf("exit %d; %s", code, errs)
	}
	agents := r.read("AGENTS.md")
	m := regexp.MustCompile("(?s)```intentgate\n(.*?)```").FindStringSubmatch(agents)
	if m == nil {
		t.Fatalf("no intentgate block in the section:\n%s", agents)
	}
	var filled []string
	for _, l := range strings.Split(strings.TrimRight(m[1], "\n"), "\n") {
		switch {
		case strings.Contains(l, "text:"):
			l = l[:strings.Index(l, "text:")] + `text: "The command is implemented"`
		case strings.Contains(l, "footprint:"):
			l = l[:strings.Index(l, "footprint:")] + `footprint: ["cmd/**"]`
		}
		filled = append(filled, l)
	}
	plan := "# Plan\n\n```intentgate\n" + strings.Join(filled, "\n") + "\n```\n"
	cs, err := ratify.DraftCriteria(plan)
	if err != nil {
		t.Fatalf("the template does not draft: %v\n%s", err, plan)
	}
	if len(cs) != 1 || cs[0].Text != "The command is implemented" || len(cs[0].Footprint) != 1 {
		t.Errorf("criteria = %+v", cs)
	}
}

func TestUsage_ListsAllCommands(t *testing.T) {
	var errb bytes.Buffer
	if code := Run(nil, strings.NewReader(""), &bytes.Buffer{}, Deps{Stderr: &errb}); code != 2 {
		t.Fatalf("exit %d, want 2", code)
	}
	for _, c := range []string{"hook", "lock", "amend", "verify", "publish", "stats", "init"} {
		if !regexp.MustCompile(`intentgate ` + c + `\b`).MatchString(errb.String()) {
			t.Errorf("the usage text does not name %q:\n%s", c, errb.String())
		}
	}
}

// ---- review 1 of S-16 ----

func TestInit_FencedMarkersAreText(t *testing.T) {
	t.Run("a documented example pair is not replaced", func(t *testing.T) {
		r := newVRepo(t)
		doc := "# Agents\n\nHow to mark a section:\n\n```md\n" + markBegin + "\nexample\n" + markEnd + "\n```\n"
		r.write("AGENTS.md", doc)
		if code, _, errs := initRun(t, r.dir); code != 0 {
			t.Fatalf("exit %d; %s", code, errs)
		}
		got := r.read("AGENTS.md")
		if !strings.HasPrefix(got, doc) {
			t.Errorf("the fenced example changed:\n%s", got)
		}
		if strings.Count(got, markBegin) != 2 || !strings.Contains(got, "## IntentGate") {
			t.Errorf("the real section was not appended after the example:\n%s", got)
		}
	})
	t.Run("a fenced single marker is not unpaired", func(t *testing.T) {
		r := newVRepo(t)
		r.write("AGENTS.md", "~~~\n"+markBegin+"\n~~~\n")
		if code, _, errs := initRun(t, r.dir); code != 0 {
			t.Fatalf("exit %d, want 0; %s", code, errs)
		}
	})
	t.Run("the real pair is replaced and a fenced pair after it is kept", func(t *testing.T) {
		r := newVRepo(t)
		fenced := "````\n" + markBegin + "\nexample\n" + markEnd + "\n````\n"
		r.write("AGENTS.md", markBegin+"\nold text\n"+markEnd+"\n\n"+fenced)
		if code, _, errs := initRun(t, r.dir); code != 0 {
			t.Fatalf("exit %d; %s", code, errs)
		}
		got := r.read("AGENTS.md")
		if strings.Contains(got, "old text") || !strings.HasSuffix(got, "\n\n"+fenced) {
			t.Errorf("unexpected AGENTS.md:\n%s", got)
		}
	})
}

func TestInit_AllOrNothing(t *testing.T) {
	if os.Geteuid() == 0 {
		t.Skip("root ignores file permissions")
	}
	r := newVRepo(t)
	r.write("AGENTS.md", "# Agents\n")
	if err := os.Chmod(filepath.Join(r.dir, "AGENTS.md"), 0o444); err != nil {
		t.Fatal(err)
	}
	code, _, errs := initRun(t, r.dir)
	if code != 3 {
		t.Fatalf("exit %d, want 3; stderr: %s", code, errs)
	}
	if !strings.Contains(errs, "AGENTS.md") {
		t.Errorf("stderr does not name the file: %s", errs)
	}
	for _, p := range []string{".claude", ".intentgate.yml"} {
		if r.exists(p) {
			t.Errorf("%s was written although AGENTS.md could not be", p)
		}
	}
	if got := r.read("AGENTS.md"); got != "# Agents\n" {
		t.Errorf("AGENTS.md changed: %q", got)
	}
	left, _ := filepath.Glob(filepath.Join(r.dir, "*.tmp*"))
	if len(left) > 0 {
		t.Errorf("temp files left behind: %v", left)
	}
}

func TestInit_Symlinks(t *testing.T) {
	t.Run("outside the top level is exit 3", func(t *testing.T) {
		r := newVRepo(t)
		outside := filepath.Join(t.TempDir(), "elsewhere.md")
		if err := os.WriteFile(outside, []byte("# Elsewhere\n"), 0o644); err != nil {
			t.Fatal(err)
		}
		if err := os.Symlink(outside, filepath.Join(r.dir, "AGENTS.md")); err != nil {
			t.Skipf("no symlinks: %v", err)
		}
		code, _, errs := initRun(t, r.dir)
		if code != 3 || !strings.Contains(errs, "AGENTS.md") {
			t.Errorf("exit %d, want 3 naming AGENTS.md; stderr: %s", code, errs)
		}
		if b, _ := os.ReadFile(outside); string(b) != "# Elsewhere\n" {
			t.Errorf("the file outside the repository was written: %q", b)
		}
		if r.exists(".claude") || r.exists(".intentgate.yml") {
			t.Error("init wrote other files after refusing the symlink")
		}
	})
	t.Run("dangling is exit 3", func(t *testing.T) {
		r := newVRepo(t)
		if err := os.Symlink("nowhere.md", filepath.Join(r.dir, "AGENTS.md")); err != nil {
			t.Skipf("no symlinks: %v", err)
		}
		code, _, errs := initRun(t, r.dir)
		if code != 3 || !strings.Contains(errs, "AGENTS.md") {
			t.Errorf("exit %d, want 3 naming AGENTS.md; stderr: %s", code, errs)
		}
		if fi, err := os.Lstat(filepath.Join(r.dir, "AGENTS.md")); err != nil || fi.Mode()&os.ModeSymlink == 0 {
			t.Errorf("the dangling symlink was replaced: %v", err)
		}
	})
	t.Run("inside is written through", func(t *testing.T) {
		r := newVRepo(t)
		r.write("docs/agents.md", "# Agents\n")
		if err := os.Symlink("docs/agents.md", filepath.Join(r.dir, "AGENTS.md")); err != nil {
			t.Skipf("no symlinks: %v", err)
		}
		if code, _, errs := initRun(t, r.dir); code != 0 {
			t.Fatalf("exit %d; %s", code, errs)
		}
		if fi, err := os.Lstat(filepath.Join(r.dir, "AGENTS.md")); err != nil || fi.Mode()&os.ModeSymlink == 0 {
			t.Errorf("the symlink was replaced by a regular file: %v", err)
		}
		if !strings.Contains(r.read("docs/agents.md"), markBegin) {
			t.Error("the section was not written through to docs/agents.md")
		}
	})
}

func TestInit_ExistingHookUnderAnyMatcher(t *testing.T) {
	for _, matcher := range []string{"Bash", "", "ExitPlanMode|Bash"} {
		r := newVRepo(t)
		r.write(".claude/settings.json", `{"hooks": {
  "PreToolUse": [{"matcher": "`+matcher+`", "hooks": [{"type": "command", "command": "/usr/local/bin/intentgate hook claude-code pre"}]}],
  "PostToolUse": [{"matcher": "`+matcher+`", "hooks": [{"type": "command", "command": "intentgate hook claude-code post"}]}]
}}`)
		if code, _, errs := initRun(t, r.dir); code != 0 {
			t.Fatalf("matcher %q: exit %d; %s", matcher, code, errs)
		}
		m := r.settings()
		for _, ev := range []string{"PreToolUse", "PostToolUse"} {
			if got := hookCommands(t, m, ev); len(got) != 1 {
				t.Errorf("matcher %q: %s commands = %q, want the existing one only", matcher, ev, got)
			}
		}
	}
}

func TestInit_LineEndings(t *testing.T) {
	bare := regexp.MustCompile(`[^\r]\n`)
	t.Run("appended section follows CRLF", func(t *testing.T) {
		r := newVRepo(t)
		r.write("AGENTS.md", "# Agents\r\n\r\nRules.\r\n")
		if code, _, errs := initRun(t, r.dir); code != 0 {
			t.Fatalf("exit %d; %s", code, errs)
		}
		got := r.read("AGENTS.md")
		if bare.MatchString(got) || !strings.Contains(got, markBegin+"\r\n## IntentGate\r\n") {
			t.Errorf("mixed line endings:\n%q", got)
		}
	})
	t.Run("replaced section follows CRLF and is stable", func(t *testing.T) {
		r := newVRepo(t)
		r.write("AGENTS.md", "# Agents\r\n\r\n"+markBegin+"\r\nold\r\n"+markEnd+"\r\n")
		if code, _, errs := initRun(t, r.dir); code != 0 {
			t.Fatalf("exit %d; %s", code, errs)
		}
		got := r.read("AGENTS.md")
		if bare.MatchString(got) || strings.Contains(got, "old") {
			t.Errorf("mixed line endings or not replaced:\n%q", got)
		}
		if code, out, errs := initRun(t, r.dir); code != 0 || r.read("AGENTS.md") != got || !lineWith(out, "unchanged", "AGENTS.md") {
			t.Errorf("second run changed the file (exit %d): %s\n%s", code, errs, out)
		}
	})
	t.Run("LF files stay LF", func(t *testing.T) {
		r := newVRepo(t)
		r.write("AGENTS.md", "# Agents\n")
		if code, _, errs := initRun(t, r.dir); code != 0 {
			t.Fatalf("exit %d; %s", code, errs)
		}
		if strings.Contains(r.read("AGENTS.md"), "\r") {
			t.Error("a CR appeared in an LF file")
		}
	})
}

func TestInit_NewHookEntryKeyOrder(t *testing.T) {
	r := newVRepo(t)
	if code, _, errs := initRun(t, r.dir); code != 0 {
		t.Fatalf("exit %d; %s", code, errs)
	}
	type fragment struct {
		Hooks map[string][]json.RawMessage `json:"hooks"`
	}
	var want, got fragment
	if err := json.Unmarshal(printConfig(), &want); err != nil {
		t.Fatal(err)
	}
	if err := json.Unmarshal([]byte(r.read(".claude/settings.json")), &got); err != nil {
		t.Fatal(err)
	}
	compact := func(b json.RawMessage) string {
		var c bytes.Buffer
		if err := json.Compact(&c, b); err != nil {
			t.Fatal(err)
		}
		return c.String()
	}
	for _, ev := range []string{"PreToolUse", "PostToolUse"} {
		if len(got.Hooks[ev]) != len(want.Hooks[ev]) || len(want.Hooks[ev]) == 0 {
			t.Fatalf("%s: got %d entries, want print-config's %d", ev, len(got.Hooks[ev]), len(want.Hooks[ev]))
		}
		for i := range want.Hooks[ev] {
			if g, w := compact(got.Hooks[ev][i]), compact(want.Hooks[ev][i]); g != w {
				t.Errorf("%s entry %d differs from print-config's:\n got  %s\n want %s", ev, i, g, w)
			}
		}
	}
}
