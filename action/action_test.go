// Package action holds the static checks on action.yml and its README: the composite Action is
// pure wiring, so what can be tested without GitHub is tested here (S-09).
package action

import (
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"testing"

	"gopkg.in/yaml.v3"
)

type step struct {
	ID              string            `yaml:"id"`
	Name            string            `yaml:"name"`
	If              string            `yaml:"if"`
	Uses            string            `yaml:"uses"`
	With            map[string]any    `yaml:"with"`
	Run             string            `yaml:"run"`
	Shell           string            `yaml:"shell"`
	Env             map[string]string `yaml:"env"`
	ContinueOnError any               `yaml:"continue-on-error"`
}

type actionFile struct {
	Name   string `yaml:"name"`
	Inputs map[string]struct {
		Default string `yaml:"default"`
	} `yaml:"inputs"`
	Outputs map[string]struct {
		Value string `yaml:"value"`
	} `yaml:"outputs"`
	Runs struct {
		Using string `yaml:"using"`
		Steps []step `yaml:"steps"`
	} `yaml:"runs"`
}

func load(t *testing.T) actionFile {
	t.Helper()
	data, err := os.ReadFile("action.yml")
	if err != nil {
		t.Fatal(err)
	}
	var a actionFile
	if err := yaml.Unmarshal(data, &a); err != nil {
		t.Fatalf("action.yml does not parse: %v", err)
	}
	return a
}

func stepByID(t *testing.T, a actionFile, id string) step {
	t.Helper()
	for _, s := range a.Runs.Steps {
		if s.ID == id {
			return s
		}
	}
	t.Fatalf("no step with id %q", id)
	return step{}
}

var pinned = regexp.MustCompile(`^[A-Za-z0-9_.-]+/[A-Za-z0-9_.-]+(/[A-Za-z0-9_./-]+)?@[0-9a-f]{40}$`)

func TestAction_Static(t *testing.T) {
	t.Run("shape: a composite Action named IntentGate with the three inputs", func(t *testing.T) {
		a := load(t)
		if a.Name != "IntentGate" || a.Runs.Using != "composite" {
			t.Errorf("name %q, using %q; want IntentGate, composite", a.Name, a.Runs.Using)
		}
		if a.Inputs["go-version"].Default != "stable" || a.Inputs["token"].Default != "${{ github.token }}" || a.Inputs["bot-login"].Default != "github-actions[bot]" {
			t.Errorf("inputs = %+v", a.Inputs)
		}
		for _, id := range []string{"build", "verify", "summary", "publish"} {
			stepByID(t, a, id)
		}
		if v := a.Outputs["exit-code"].Value; !strings.Contains(v, "steps.verify.outputs.") {
			t.Errorf("output exit-code = %q, want the verify step's exit code", v)
		}
	})

	t.Run("every uses: is pinned to a full commit SHA", func(t *testing.T) {
		a := load(t)
		var seen []string
		for _, s := range a.Runs.Steps {
			if s.Uses == "" {
				continue
			}
			seen = append(seen, s.Uses)
			if !pinned.MatchString(s.Uses) {
				t.Errorf("uses %q is not pinned to a 40-hex commit SHA", s.Uses)
			}
		}
		for _, want := range []string{"actions/setup-go@", "github/codeql-action/upload-sarif@", "actions/upload-artifact@"} {
			found := false
			for _, u := range seen {
				found = found || strings.HasPrefix(u, want)
			}
			if !found {
				t.Errorf("no step uses %s…; saw %v", want, seen)
			}
		}
	})

	t.Run("pull_request only: every step is guarded, and any other event gets one warning and nothing else", func(t *testing.T) {
		a := load(t)
		const guard = "github.event_name == 'pull_request'"
		warnings := 0
		for _, s := range a.Runs.Steps {
			if strings.Contains(s.If, "pull_request_target") {
				t.Errorf("step %q mentions pull_request_target in its if: %q", s.ID, s.If)
			}
			if s.If == "github.event_name != 'pull_request'" {
				warnings++
				if !strings.Contains(s.Run, "::warning") || !strings.Contains(s.Run, "IntentGate supports pull_request only") {
					t.Errorf("the other-event step does not warn %q: %q", "IntentGate supports pull_request only", s.Run)
				}
				if s.Uses != "" || strings.Contains(s.Run, "intentgate") && strings.Contains(s.Run, "publish") {
					t.Errorf("the other-event step does more than warn: %+v", s)
				}
				continue
			}
			if !strings.HasPrefix(s.If, guard) {
				t.Errorf("step %q (%q): if = %q, want it to start with %q", s.ID, s.Name, s.If, guard)
			}
		}
		if warnings != 1 {
			t.Errorf("%d warning steps for other events, want exactly 1", warnings)
		}
	})

	t.Run("the warning step for another event exits 0 and warns", func(t *testing.T) {
		if _, err := exec.LookPath("bash"); err != nil {
			t.Skip("bash not on PATH")
		}
		for _, s := range load(t).Runs.Steps {
			if s.If != "github.event_name != 'pull_request'" {
				continue
			}
			cmd := exec.Command("bash", "-eo", "pipefail", "-c", s.Run)
			cmd.Env = []string{"PATH=" + os.Getenv("PATH")}
			out, err := cmd.CombinedOutput()
			if err != nil || !strings.Contains(string(out), "::warning") || !strings.Contains(string(out), "IntentGate supports pull_request only") {
				t.Errorf("err = %v, out = %q", err, out)
			}
			return
		}
		t.Error("no warning step for other events")
	})

	t.Run("no run: script holds an expression, values arrive through env:", func(t *testing.T) {
		a := load(t)
		runs := 0
		for _, s := range a.Runs.Steps {
			if s.Run == "" {
				continue
			}
			runs++
			if strings.Contains(s.Run, "${{") {
				t.Errorf("step %q: run contains ${{", s.ID)
			}
			if s.Shell != "bash" {
				t.Errorf("step %q: shell = %q, want bash", s.ID, s.Shell)
			}
		}
		if runs < 5 {
			t.Errorf("%d run steps, want at least the warning, build, verify, summary and publish", runs)
		}
	})

	t.Run("nothing can fail the job: every step continues on error", func(t *testing.T) {
		a := load(t)
		if len(a.Runs.Steps) == 0 {
			t.Fatal("no steps")
		}
		for _, s := range a.Runs.Steps {
			if s.ContinueOnError != true {
				t.Errorf("step %q (%q): continue-on-error = %v, want true", s.ID, s.Name, s.ContinueOnError)
			}
		}
	})

	t.Run("the build passes the version and builds from the action's own directory", func(t *testing.T) {
		b := stepByID(t, load(t), "build")
		if !strings.Contains(b.Run, "-X main.version=") || !strings.Contains(b.Run, `$RUNNER_TEMP`) {
			t.Errorf("build run = %q", b.Run)
		}
		if !strings.Contains(b.Env["ACTION_PATH"], "github.action_path") {
			t.Errorf("build env = %v, want ACTION_PATH from github.action_path", b.Env)
		}
	})

	t.Run("verify takes everything from the pull request event through env", func(t *testing.T) {
		v := stepByID(t, load(t), "verify")
		want := map[string]string{
			"BASE_SHA":    "${{ github.event.pull_request.base.sha }}",
			"HEAD_SHA":    "${{ github.event.pull_request.head.sha }}",
			"BASE_REF":    "${{ github.event.pull_request.base.ref }}",
			"AUTHOR":      "${{ github.event.pull_request.user.login }}",
			"AUTHOR_TYPE": "${{ github.event.pull_request.user.type }}",
			"PR_NUMBER":   "${{ github.event.pull_request.number }}",
		}
		for k, val := range want {
			if v.Env[k] != val {
				t.Errorf("verify env %s = %q, want %q", k, v.Env[k], val)
			}
		}
		for _, frag := range []string{`verify`, `--base "$BASE_SHA"`, `--head "$HEAD_SHA"`, `--base-branch "$BASE_REF"`,
			`--author "$AUTHOR"`, `--author-type "$AUTHOR_TYPE"`, `--out-dir "$RUNNER_TEMP/intentgate"`, `--repo "$GITHUB_REPOSITORY"`, `--pr "$PR_NUMBER"`, `exit_code=`} {
			if !strings.Contains(v.Run, frag) {
				t.Errorf("verify run lacks %s", frag)
			}
		}
	})

	t.Run("publish gets the token from the input and warns instead of failing", func(t *testing.T) {
		p := stepByID(t, load(t), "publish")
		if p.Env["GITHUB_TOKEN"] != "${{ inputs.token }}" || p.Env["PR_NUMBER"] != "${{ github.event.pull_request.number }}" || p.Env["BOT_LOGIN"] != "${{ inputs.bot-login }}" {
			t.Errorf("publish env = %v", p.Env)
		}
		for _, frag := range []string{`publish`, `--repo "$GITHUB_REPOSITORY"`, `--pr "$PR_NUMBER"`, `--head-sha "$HEAD_SHA"`, `--bot-login "$BOT_LOGIN"`, `::warning`} {
			if !strings.Contains(p.Run, frag) {
				t.Errorf("publish run lacks %s", frag)
			}
		}
	})

	t.Run("every run: summary.md goes to the job summary and the out dir is uploaded as the intentgate artifact", func(t *testing.T) {
		a := load(t)
		sum := stepByID(t, a, "summary")
		if !strings.Contains(sum.Run, "$GITHUB_STEP_SUMMARY") || !strings.Contains(sum.Run, "summary.md") {
			t.Errorf("summary run = %q", sum.Run)
		}
		var up *step
		for i, s := range a.Runs.Steps {
			if strings.HasPrefix(s.Uses, "actions/upload-artifact@") {
				up = &a.Runs.Steps[i]
			}
		}
		if up == nil {
			t.Fatal("no upload-artifact step")
		}
		if up.With["name"] != "intentgate" || up.With["if-no-files-found"] != "ignore" {
			t.Errorf("upload-artifact with = %v, want name intentgate and if-no-files-found ignore", up.With)
		}
		if p, _ := up.With["path"].(string); !strings.HasSuffix(p, "/intentgate") || !strings.Contains(p, "runner.temp") {
			t.Errorf("upload-artifact path = %q, want the out dir under runner.temp", p)
		}
		if up.ContinueOnError != true {
			t.Errorf("upload-artifact continue-on-error = %v", up.ContinueOnError)
		}
		// They run whatever the decision: neither waits for upload=true.
		for _, s := range []step{sum, *up} {
			if strings.Contains(s.If, "outputs.upload") {
				t.Errorf("step %q is conditional on the SARIF decision: %q", s.ID, s.If)
			}
		}
	})

	t.Run("the summary step appends summary.md and exits 0 with or without it", func(t *testing.T) {
		if _, err := exec.LookPath("bash"); err != nil {
			t.Skip("bash not on PATH")
		}
		sum := stepByID(t, load(t), "summary")
		for _, present := range []bool{true, false} {
			tmp := t.TempDir()
			if present {
				if err := os.MkdirAll(filepath.Join(tmp, "intentgate"), 0o755); err != nil {
					t.Fatal(err)
				}
				if err := os.WriteFile(filepath.Join(tmp, "intentgate", "summary.md"), []byte("## IntentGate\nbody\n"), 0o644); err != nil {
					t.Fatal(err)
				}
			}
			ghSum := filepath.Join(tmp, "step_summary")
			cmd := exec.Command("bash", "-eo", "pipefail", "-c", sum.Run)
			cmd.Env = []string{"PATH=" + os.Getenv("PATH"), "RUNNER_TEMP=" + tmp, "GITHUB_STEP_SUMMARY=" + ghSum}
			if out, err := cmd.CombinedOutput(); err != nil {
				t.Errorf("summary present=%v: %v\n%s", present, err, out)
			}
			got, _ := os.ReadFile(ghSum)
			if present && string(got) != "## IntentGate\nbody\n" || !present && len(got) != 0 {
				t.Errorf("summary present=%v: job summary = %q", present, got)
			}
		}
	})

	t.Run("SARIF upload: category intentgate, only when there is a file and a decision to publish", func(t *testing.T) {
		a := load(t)
		var up *step
		for i, s := range a.Runs.Steps {
			if strings.HasPrefix(s.Uses, "github/codeql-action/upload-sarif@") {
				up = &a.Runs.Steps[i]
			}
		}
		if up == nil {
			t.Fatal("no upload-sarif step")
		}
		if up.With["category"] != "intentgate" || !strings.Contains(up.If, "steps.verify.outputs.") {
			t.Errorf("upload with = %v, if = %q", up.With, up.If)
		}
		if f, _ := up.With["sarif_file"].(string); !strings.Contains(f, "results.sarif") {
			t.Errorf("sarif_file = %q", f)
		}
	})

	t.Run("the verify and publish scripts exit 0 whatever the binary does, and pass it the right arguments", func(t *testing.T) {
		if _, err := exec.LookPath("bash"); err != nil {
			t.Skip("bash not on PATH")
		}
		a := load(t)
		decision := `{"publish": true, "conclusion": "neutral", "comment": "upsert", "title": "T"}`
		for _, tc := range []struct {
			fakeExit, fakeDecision, wantUpload string
		}{
			{"0", decision, "upload=true"},
			{"1", decision, "upload=true"},
			{"3", "", "upload=false"},
			{"0", `{"publish": false, "conclusion": "neutral", "comment": "none", "title": ""}`, "upload=false"},
		} {
			tmp := t.TempDir()
			bin := filepath.Join(tmp, "intentgate-bin")
			if err := os.MkdirAll(bin, 0o755); err != nil {
				t.Fatal(err)
			}
			fake := "#!/bin/sh\nmode=$1\nprintf '%s\\n' \"$@\" > \"$RUNNER_TEMP/args-$mode\"\n" +
				"if [ \"$mode\" = verify ] && [ -n \"$FAKE_DECISION\" ]; then\n" +
				"  while [ $# -gt 0 ]; do [ \"$1\" = --out-dir ] && out=$2; shift; done\n" +
				"  mkdir -p \"$out\"; printf '%s' \"$FAKE_DECISION\" > \"$out/decision.json\"; echo '{}' > \"$out/results.sarif\"\n" +
				"fi\nexit \"$FAKE_EXIT\"\n"
			if err := os.WriteFile(filepath.Join(bin, "intentgate"), []byte(fake), 0o755); err != nil {
				t.Fatal(err)
			}
			ghOut := filepath.Join(tmp, "github_output")
			for _, id := range []string{"verify", "publish"} {
				s := stepByID(t, a, id)
				cmd := exec.Command("bash", "-eo", "pipefail", "-c", s.Run)
				cmd.Env = []string{"PATH=" + os.Getenv("PATH"), "RUNNER_TEMP=" + tmp, "GITHUB_OUTPUT=" + ghOut,
					"GITHUB_REPOSITORY=octo/repo", "FAKE_EXIT=" + tc.fakeExit, "FAKE_DECISION=" + tc.fakeDecision}
				for k, v := range s.Env {
					if strings.Contains(v, "${{") {
						v = "value-of-" + k
					}
					cmd.Env = append(cmd.Env, k+"="+v)
				}
				out, err := cmd.CombinedOutput()
				if err != nil {
					t.Errorf("%s with the binary exiting %s: the script failed: %v\n%s", id, tc.fakeExit, err, out)
				}
				if id == "publish" && tc.fakeExit != "0" && !strings.Contains(string(out), "::warning") {
					t.Errorf("publish failing printed no workflow warning: %q", out)
				}
			}
			outputs, _ := os.ReadFile(ghOut)
			if !strings.Contains(string(outputs), "exit_code="+tc.fakeExit) || !strings.Contains(string(outputs), tc.wantUpload) {
				t.Errorf("exit %s, decision %q: GITHUB_OUTPUT = %q, want exit_code=%s and %s", tc.fakeExit, tc.fakeDecision, outputs, tc.fakeExit, tc.wantUpload)
			}
			args, _ := os.ReadFile(filepath.Join(tmp, "args-verify"))
			want := "verify\n--base\nvalue-of-BASE_SHA\n--head\nvalue-of-HEAD_SHA\n--base-branch\nvalue-of-BASE_REF\n--author\nvalue-of-AUTHOR\n--author-type\nvalue-of-AUTHOR_TYPE\n--out-dir\n" + tmp + "/intentgate\n--repo\nocto/repo\n--pr\nvalue-of-PR_NUMBER\n"
			if string(args) != want {
				t.Errorf("verify argv = %q, want %q", args, want)
			}
			pargs, _ := os.ReadFile(filepath.Join(tmp, "args-publish"))
			wantP := "publish\n--out-dir\n" + tmp + "/intentgate\n--repo\nocto/repo\n--pr\nvalue-of-PR_NUMBER\n--head-sha\nvalue-of-HEAD_SHA\n--bot-login\nvalue-of-BOT_LOGIN\n"
			if string(pargs) != wantP {
				t.Errorf("publish argv = %q, want %q", pargs, wantP)
			}
		}
	})
}

// TestAction_Docs checks the README's workflow snippet carries what the design requires.
func TestAction_Docs(t *testing.T) {
	data, err := os.ReadFile("README.md")
	if err != nil {
		t.Fatal(err)
	}
	readme := string(data)
	m := regexp.MustCompile("(?s)```yaml\n(.*?)```").FindStringSubmatch(readme)
	if m == nil {
		t.Fatal("README.md has no yaml workflow snippet")
	}
	snippet := m[1]
	for _, frag := range []string{"pull_request", "fetch-depth: 0", "contents: read", "checks: write", "pull-requests: write", "security-events: write", "/action@"} {
		if !strings.Contains(snippet, frag) {
			t.Errorf("snippet lacks %q", frag)
		}
	}
	for _, frag := range []string{"concurrency:", "group: intentgate-${{ github.event.pull_request.number }}", "cancel-in-progress: true"} {
		if !strings.Contains(snippet, frag) {
			t.Errorf("snippet lacks %q", frag)
		}
	}
	for _, frag := range []string{"bot-login", "pull_request_target", "not supported", "artifact"} {
		if !strings.Contains(readme, frag) {
			t.Errorf("README does not mention %q", frag)
		}
	}
	if !strings.Contains(strings.ToLower(readme), "fork") {
		t.Error("README does not say what happens on a pull request from a fork")
	}
	lint(t, snippet, "")
}

// TestAction_Actionlint lints a sample workflow that uses the Action. It is skipped without actionlint.
func TestAction_Actionlint(t *testing.T) {
	sample := "name: IntentGate\non:\n  pull_request:\npermissions:\n  contents: read\n  checks: write\n  pull-requests: write\n  security-events: write\n" +
		"jobs:\n  intentgate:\n    runs-on: ubuntu-latest\n    steps:\n" +
		"      - uses: actions/checkout@0123456789abcdef0123456789abcdef01234567\n        with:\n          fetch-depth: 0\n" +
		"      - uses: ./action\n        with:\n          go-version: stable\n"
	lint(t, sample, "action.yml")
}

// TestWorkflows_Actionlint runs actionlint over this repository's own workflows. It is skipped
// without actionlint.
func TestWorkflows_Actionlint(t *testing.T) {
	bin, err := exec.LookPath("actionlint")
	if err != nil {
		t.Skip("actionlint not on PATH")
	}
	files, err := filepath.Glob(filepath.Join("..", ".github", "workflows", "*.yml"))
	if err != nil || len(files) == 0 {
		t.Fatalf("no workflows found under .github/workflows: %v", err)
	}
	for _, name := range []string{"check.yml", "intentgate.yml", "release.yml"} {
		if _, err := os.Stat(filepath.Join("..", ".github", "workflows", name)); err != nil {
			t.Errorf("missing workflow %s: %v", name, err)
		}
	}
	cmd := exec.Command(bin, append([]string{"-no-color"}, files...)...)
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Errorf("actionlint: %v\n%s", err, out)
	}
}

// lint runs actionlint on a workflow in a scratch repository; withAction names the Action file to
// copy to ./action/action.yml there so `uses: ./action` resolves.
func lint(t *testing.T, workflow, withAction string) {
	t.Helper()
	bin, err := exec.LookPath("actionlint")
	if err != nil {
		t.Skip("actionlint not on PATH")
	}
	root := t.TempDir()
	wf := filepath.Join(root, ".github", "workflows")
	if err := os.MkdirAll(wf, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(wf, "intentgate.yml"), []byte(workflow), 0o644); err != nil {
		t.Fatal(err)
	}
	if withAction != "" {
		data, err := os.ReadFile(withAction)
		if err != nil {
			t.Fatal(err)
		}
		if err := os.MkdirAll(filepath.Join(root, "action"), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(root, "action", "action.yml"), data, 0o644); err != nil {
			t.Fatal(err)
		}
	}
	cmd := exec.Command(bin, "-no-color", ".github/workflows/intentgate.yml")
	cmd.Dir = root
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Errorf("actionlint: %v\n%s", err, out)
	}
}

// TestAction_Source covers S-21: the Action downloads the pinned release by default and builds
// from source only when asked, or when no release is pinned yet.
func TestAction_Source(t *testing.T) {
	t.Run("the source input defaults to release", func(t *testing.T) {
		if d := load(t).Inputs["source"].Default; d != "release" {
			t.Errorf("source default = %q, want release", d)
		}
	})

	t.Run("the fetch step runs only for release, from the Action's own directory", func(t *testing.T) {
		f := stepByID(t, load(t), "fetch")
		if !strings.Contains(f.If, "inputs.source == 'release'") {
			t.Errorf("fetch if = %q", f.If)
		}
		if !strings.Contains(f.Run, "fetch.sh") || !strings.Contains(f.Run, "release.txt") || !strings.Contains(f.Run, "$RUNNER_TEMP/intentgate-bin/intentgate") {
			t.Errorf("fetch run = %q", f.Run)
		}
		if !strings.Contains(f.Env["ACTION_PATH"], "github.action_path") {
			t.Errorf("fetch env = %v, want ACTION_PATH from github.action_path", f.Env)
		}
	})

	t.Run("setup-go and the build run only for build, or after exit 5 from fetch", func(t *testing.T) {
		a := load(t)
		g := stepByID(t, a, "go")
		if !strings.Contains(g.If, "inputs.source == 'build'") || !strings.Contains(g.If, "steps.fetch.outputs.exit_code == '5'") {
			t.Errorf("go if = %q", g.If)
		}
		if strings.Contains(g.If, "inputs.source != ") {
			t.Errorf("go if = %q: an unknown source must do nothing", g.If)
		}
		if b := stepByID(t, a, "build"); !strings.Contains(b.If, "steps.go.outcome == 'success'") {
			t.Errorf("build if = %q", b.If)
		}
	})

	t.Run("verify needs a binary from either path", func(t *testing.T) {
		v := stepByID(t, load(t), "verify")
		if !strings.Contains(v.If, "steps.build.outcome == 'success'") || !strings.Contains(v.If, "steps.fetch.outputs.exit_code == '0'") {
			t.Errorf("verify if = %q", v.If)
		}
	})

	t.Run("an unknown source value warns and nothing more runs", func(t *testing.T) {
		a := load(t)
		var warn *step
		for i, s := range a.Runs.Steps {
			if strings.Contains(s.If, "inputs.source != 'release'") && strings.Contains(s.If, "inputs.source != 'build'") {
				warn = &a.Runs.Steps[i]
			}
		}
		if warn == nil {
			t.Fatal("no step warns about an unknown source")
		}
		if !strings.Contains(warn.Run, "::warning") || warn.Uses != "" {
			t.Errorf("warning step = %+v", *warn)
		}
		// Every other path is gated on a known source, directly or through the steps it needs.
		for _, id := range []string{"fetch", "go"} {
			if !strings.Contains(stepByID(t, a, id).If, "inputs.source == ") {
				t.Errorf("step %q is not gated on a known source", id)
			}
		}
	})

	t.Run("the fetch step: exit 5 is a notice, any other failure a warning, and it never fails", func(t *testing.T) {
		if _, err := exec.LookPath("bash"); err != nil {
			t.Skip("bash not on PATH")
		}
		f := stepByID(t, load(t), "fetch")
		for _, tc := range []struct{ code, want string }{{"0", ""}, {"4", "::warning"}, {"5", "::notice"}, {"6", "::warning"}, {"7", "::warning"}} {
			tmp := t.TempDir()
			actionDir := filepath.Join(tmp, "action")
			if err := os.MkdirAll(actionDir, 0o755); err != nil {
				t.Fatal(err)
			}
			fake := "#!/usr/bin/env bash\nexit " + tc.code + "\n"
			if err := os.WriteFile(filepath.Join(actionDir, "fetch.sh"), []byte(fake), 0o755); err != nil {
				t.Fatal(err)
			}
			ghOut := filepath.Join(tmp, "github_output")
			cmd := exec.Command("bash", "-eo", "pipefail", "-c", f.Run)
			cmd.Env = []string{"PATH=" + os.Getenv("PATH"), "RUNNER_TEMP=" + tmp, "GITHUB_OUTPUT=" + ghOut, "ACTION_PATH=" + actionDir}
			out, err := cmd.CombinedOutput()
			if err != nil {
				t.Errorf("fetch.sh exit %s: the step failed: %v\n%s", tc.code, err, out)
			}
			if tc.want == "" && strings.Contains(string(out), "::") || tc.want != "" && !strings.Contains(string(out), tc.want) {
				t.Errorf("fetch.sh exit %s: output %q, want %q", tc.code, out, tc.want)
			}
			if tc.code != "5" && strings.Contains(string(out), "::notice") {
				t.Errorf("fetch.sh exit %s: only exit 5 falls back, got %q", tc.code, out)
			}
			if got, _ := os.ReadFile(ghOut); !strings.Contains(string(got), "exit_code="+tc.code) {
				t.Errorf("fetch.sh exit %s: GITHUB_OUTPUT = %q", tc.code, got)
			}
		}
	})

	t.Run("the dogfood workflow tests this pull request's code: source: build", func(t *testing.T) {
		data, err := os.ReadFile(filepath.Join("..", ".github", "workflows", "intentgate.yml"))
		if err != nil {
			t.Fatal(err)
		}
		var w struct {
			Jobs map[string]struct {
				Steps []step `yaml:"steps"`
			} `yaml:"jobs"`
		}
		if err := yaml.Unmarshal(data, &w); err != nil {
			t.Fatal(err)
		}
		for _, j := range w.Jobs {
			for _, s := range j.Steps {
				if s.Uses == "./action" {
					if s.With["source"] != "build" {
						t.Errorf("./action with = %v, want source: build", s.With)
					}
					return
				}
			}
		}
		t.Error("no ./action step in intentgate.yml")
	})

	t.Run("the README documents source, the pin and how to update it", func(t *testing.T) {
		data, err := os.ReadFile("README.md")
		if err != nil {
			t.Fatal(err)
		}
		readme := string(data)
		for _, frag := range []string{"`source`", "release.txt", "SHA-256", "re-pin"} {
			if !strings.Contains(readme, frag) {
				t.Errorf("README does not mention %q", frag)
			}
		}
	})
}

// REQ-0045 (pr-check.md §7)
func TestAction_Acceptance(t *testing.T) {
	a := load(t)
	index := map[string]int{}
	for i, s := range a.Runs.Steps {
		index[s.ID] = i
	}
	acc := stepByID(t, a, "acceptance")

	t.Run("it runs after verify and before the summary and publish steps", func(t *testing.T) {
		if !(index["verify"] < index["acceptance"] && index["acceptance"] < index["summary"] && index["acceptance"] < index["publish"]) {
			t.Errorf("step order: verify %d, acceptance %d, summary %d, publish %d", index["verify"], index["acceptance"], index["summary"], index["publish"])
		}
	})

	t.Run("it is guarded like the others and runs only when verify succeeded", func(t *testing.T) {
		if !strings.HasPrefix(acc.If, "github.event_name == 'pull_request'") || !strings.Contains(acc.If, "steps.verify.outcome == 'success'") || strings.Contains(acc.If, "pull_request_target") {
			t.Errorf("if = %q", acc.If)
		}
	})

	t.Run("it continues on error and a failure is only a warning", func(t *testing.T) {
		if acc.ContinueOnError != true {
			t.Errorf("continue-on-error = %v, want true", acc.ContinueOnError)
		}
		if !strings.Contains(acc.Run, "::warning") || acc.Shell != "bash" {
			t.Errorf("run = %q, shell = %q", acc.Run, acc.Shell)
		}
	})

	t.Run("values arrive only through env: and no expression is in the script", func(t *testing.T) {
		want := map[string]string{
			"GITHUB_TOKEN": "${{ inputs.token }}",
			"BASE_SHA":     "${{ github.event.pull_request.base.sha }}",
			"HEAD_SHA":     "${{ github.event.pull_request.head.sha }}",
		}
		for k, v := range want {
			if acc.Env[k] != v {
				t.Errorf("env %s = %q, want %q", k, acc.Env[k], v)
			}
		}
		if strings.Contains(acc.Run, "${{") {
			t.Errorf("run contains an expression: %q", acc.Run)
		}
		for _, frag := range []string{"acceptance", `--out-dir "$RUNNER_TEMP/intentgate"`, `--repo "$GITHUB_REPOSITORY"`, `--base "$BASE_SHA"`, `--head "$HEAD_SHA"`} {
			if !strings.Contains(acc.Run, frag) {
				t.Errorf("run lacks %s", frag)
			}
		}
	})

	t.Run("a failing intentgate acceptance warns and the script still exits 0", func(t *testing.T) {
		if _, err := exec.LookPath("bash"); err != nil {
			t.Skip("bash not on PATH")
		}
		tmp := t.TempDir()
		bin := filepath.Join(tmp, "intentgate-bin")
		if err := os.MkdirAll(bin, 0o755); err != nil {
			t.Fatal(err)
		}
		fake := "#!/bin/sh\nprintf '%s\\n' \"$@\" > \"$RUNNER_TEMP/args-acceptance\"\nexit \"$FAKE_EXIT\"\n"
		if err := os.WriteFile(filepath.Join(bin, "intentgate"), []byte(fake), 0o755); err != nil {
			t.Fatal(err)
		}
		for _, code := range []string{"0", "2", "3"} {
			cmd := exec.Command("bash", "-eo", "pipefail", "-c", acc.Run)
			cmd.Env = []string{"PATH=" + os.Getenv("PATH"), "RUNNER_TEMP=" + tmp, "GITHUB_REPOSITORY=octo/repo", "FAKE_EXIT=" + code}
			for k, v := range acc.Env {
				if strings.Contains(v, "${{") {
					v = "value-of-" + k
				}
				cmd.Env = append(cmd.Env, k+"="+v)
			}
			out, err := cmd.CombinedOutput()
			if err != nil {
				t.Errorf("exit %s: the script failed: %v\n%s", code, err, out)
			}
			if warned := strings.Contains(string(out), "::warning"); warned != (code != "0") {
				t.Errorf("exit %s: warning = %v, output %q", code, warned, out)
			}
			args, _ := os.ReadFile(filepath.Join(tmp, "args-acceptance"))
			want := "acceptance\n--out-dir\n" + tmp + "/intentgate\n--repo\nocto/repo\n--base\nvalue-of-BASE_SHA\n--head\nvalue-of-HEAD_SHA\n"
			if string(args) != want {
				t.Errorf("argv = %q, want %q", args, want)
			}
		}
	})

	t.Run("acceptance.json rides in the intentgate artifact: the whole out dir is uploaded", func(t *testing.T) {
		for _, s := range a.Runs.Steps {
			if strings.HasPrefix(s.Uses, "actions/upload-artifact@") {
				if p, _ := s.With["path"].(string); !strings.HasSuffix(p, "/intentgate") || strings.Contains(p, "acceptance") {
					t.Errorf("upload-artifact path = %q, want the out dir", p)
				}
				if index["acceptance"] > index[s.ID] {
					t.Errorf("acceptance runs after the artifact upload")
				}
			}
		}
	})
}
