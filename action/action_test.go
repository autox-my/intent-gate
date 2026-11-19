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
	for _, name := range []string{"check.yml", "intentgate.yml"} {
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
