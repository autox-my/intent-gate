package action

// S-21: the release workflow, the pinned release file and the fetch script that downloads and
// verifies the binary the Action runs.

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"testing"

	"gopkg.in/yaml.v3"
)

const testTag = "v1.2.3"

func sum(b []byte) string {
	h := sha256.Sum256(b)
	return hex.EncodeToString(h[:])
}

// fetchEnv is one run of fetch.sh: a release directory laid out like a release's download URLs.
type fetchEnv struct {
	dir  string // holds the served files, release.txt and the destination
	base string // BASE_URL
	dest string
	rel  string // release.txt
}

func newFetchEnv(t *testing.T) fetchEnv {
	t.Helper()
	dir := t.TempDir()
	if err := os.MkdirAll(filepath.Join(dir, "srv", testTag), 0o755); err != nil {
		t.Fatal(err)
	}
	return fetchEnv{
		dir:  dir,
		base: "file://" + filepath.Join(dir, "srv"),
		dest: filepath.Join(dir, "out", "bin", "intentgate"),
		rel:  filepath.Join(dir, "release.txt"),
	}
}

func (e fetchEnv) serve(t *testing.T, asset string, body []byte) {
	t.Helper()
	if err := os.WriteFile(filepath.Join(e.dir, "srv", testTag, asset), body, 0o644); err != nil {
		t.Fatal(err)
	}
}

func (e fetchEnv) pin(t *testing.T, content string) {
	t.Helper()
	if err := os.WriteFile(e.rel, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}

// run runs fetch.sh and returns its exit code and combined output.
func (e fetchEnv) run(t *testing.T, base, runnerOS, runnerArch string) (int, string) {
	t.Helper()
	if _, err := exec.LookPath("bash"); err != nil {
		t.Skip("bash not on PATH")
	}
	cmd := exec.Command("bash", "fetch.sh")
	cmd.Env = []string{"PATH=" + os.Getenv("PATH"), "HOME=" + e.dir, "RELEASE_FILE=" + e.rel, "BASE_URL=" + base,
		"RUNNER_OS=" + runnerOS, "RUNNER_ARCH=" + runnerArch, "DEST=" + e.dest}
	out, err := cmd.CombinedOutput()
	var ee *exec.ExitError
	switch {
	case err == nil:
		return 0, string(out)
	case errors.As(err, &ee):
		return ee.ExitCode(), string(out)
	default:
		t.Fatalf("running fetch.sh: %v", err)
		return -1, ""
	}
}

func (e fetchEnv) nothingAtDest(t *testing.T) {
	t.Helper()
	if _, err := os.Stat(e.dest); err == nil {
		t.Errorf("a file exists at DEST %s", e.dest)
	}
	// No temp file is left beside it either.
	if entries, err := os.ReadDir(filepath.Dir(e.dest)); err == nil && len(entries) != 0 {
		t.Errorf("DEST's directory is not empty: %v", entries)
	}
}

func TestFetch(t *testing.T) {
	binary := []byte("#!/bin/sh\necho an intentgate binary\n")

	t.Run("a correct asset is installed at DEST with mode 0755", func(t *testing.T) {
		for _, tc := range []struct{ arch, plat string }{{"X64", "linux_amd64"}, {"ARM64", "linux_arm64"}} {
			e := newFetchEnv(t)
			asset := "intentgate_" + testTag + "_" + tc.plat
			e.serve(t, asset, binary)
			e.pin(t, "# pinned\nversion "+testTag+"\n"+sum(binary)+"  "+asset+"\n"+strings.Repeat("0", 64)+"  intentgate_"+testTag+"_other\n")
			code, out := e.run(t, e.base, "Linux", tc.arch)
			if code != 0 {
				t.Errorf("%s: exit = %d, want 0\n%s", tc.plat, code, out)
				continue
			}
			got, err := os.ReadFile(e.dest)
			if err != nil || string(got) != string(binary) {
				t.Errorf("%s: DEST = %q, %v; want the asset's bytes", tc.plat, got, err)
			}
			if fi, err := os.Stat(e.dest); err == nil && fi.Mode().Perm() != 0o755 {
				t.Errorf("%s: mode = %v, want 0755", tc.plat, fi.Mode().Perm())
			}
		}
	})

	t.Run("a tampered asset is refused (exit 7) and nothing is left at DEST", func(t *testing.T) {
		e := newFetchEnv(t)
		asset := "intentgate_" + testTag + "_linux_amd64"
		e.serve(t, asset, append([]byte("tampered "), binary...))
		e.pin(t, "version "+testTag+"\n"+sum(binary)+"  "+asset+"\n")
		if code, out := e.run(t, e.base, "Linux", "X64"); code != 7 {
			t.Errorf("exit = %d, want 7\n%s", code, out)
		}
		e.nothingAtDest(t)
	})

	t.Run("a missing asset is a download failure (exit 6)", func(t *testing.T) {
		e := newFetchEnv(t)
		asset := "intentgate_" + testTag + "_linux_amd64"
		e.pin(t, "version "+testTag+"\n"+sum(binary)+"  "+asset+"\n")
		if code, out := e.run(t, e.base, "Linux", "X64"); code != 6 {
			t.Errorf("exit = %d, want 6\n%s", code, out)
		}
		e.nothingAtDest(t)
	})

	t.Run("no version line means no pinned release (exit 5)", func(t *testing.T) {
		e := newFetchEnv(t)
		e.pin(t, "# how to fill this in\n\n")
		if code, out := e.run(t, e.base, "Linux", "X64"); code != 5 {
			t.Errorf("exit = %d, want 5\n%s", code, out)
		}
		e.nothingAtDest(t)
	})

	t.Run("an unsupported platform is exit 4", func(t *testing.T) {
		for _, p := range [][2]string{{"macOS", "ARM64"}, {"Windows", "X64"}, {"Linux", "X86"}} {
			e := newFetchEnv(t)
			asset := "intentgate_" + testTag + "_linux_amd64"
			e.serve(t, asset, binary)
			e.pin(t, "version "+testTag+"\n"+sum(binary)+"  "+asset+"\n")
			if code, out := e.run(t, e.base, p[0], p[1]); code != 4 {
				t.Errorf("%s/%s: exit = %d, want 4\n%s", p[0], p[1], code, out)
			}
			e.nothingAtDest(t)
		}
	})

	t.Run("a pin without a checksum for the asset is refused (exit 7)", func(t *testing.T) {
		e := newFetchEnv(t)
		asset := "intentgate_" + testTag + "_linux_amd64"
		e.serve(t, asset, binary)
		e.pin(t, "version "+testTag+"\n")
		if code, out := e.run(t, e.base, "Linux", "X64"); code != 7 {
			t.Errorf("exit = %d, want 7\n%s", code, out)
		}
		e.nothingAtDest(t)
	})

	t.Run("--proto refuses http:// even when the asset would verify (exit 6)", func(t *testing.T) {
		e := newFetchEnv(t)
		asset := "intentgate_" + testTag + "_linux_amd64"
		e.pin(t, "version "+testTag+"\n"+sum(binary)+"  "+asset+"\n")
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			_, _ = w.Write(binary)
		}))
		defer srv.Close()
		if code, out := e.run(t, srv.URL, "Linux", "X64"); code != 6 {
			t.Errorf("exit = %d, want 6 (plain http must be refused)\n%s", code, out)
		}
		e.nothingAtDest(t)
	})
}

func TestReleaseFile_NoVersionYet(t *testing.T) {
	data, err := os.ReadFile("release.txt")
	if err != nil {
		t.Fatal(err)
	}
	text := string(data)
	if !strings.Contains(text, "SHA256SUMS") {
		t.Error("release.txt has no header comment saying how to fill it from a release's SHA256SUMS")
	}
	hashLine := regexp.MustCompile(`^[0-9a-f]{64}  intentgate_v[0-9]+\.[0-9]+\.[0-9]+_(linux)_(amd64|arm64)$`)
	for i, line := range strings.Split(text, "\n") {
		line = strings.TrimSpace(line)
		switch {
		case line == "" || strings.HasPrefix(line, "#"):
		case strings.HasPrefix(line, "version "):
			t.Errorf("line %d: release.txt has a version line %q; no release exists yet in this slice", i+1, line)
		case !hashLine.MatchString(line):
			t.Errorf("line %d: %q is not a comment, a version line or a checksum line", i+1, line)
		}
	}
}

type releaseWorkflow struct {
	On          map[string]any    `yaml:"on"`
	Permissions map[string]string `yaml:"permissions"`
	Jobs        map[string]struct {
		Permissions map[string]string `yaml:"permissions"`
		Steps       []step            `yaml:"steps"`
	} `yaml:"jobs"`
}

func TestReleaseWorkflow(t *testing.T) {
	data, err := os.ReadFile(filepath.Join("..", ".github", "workflows", "release.yml"))
	if err != nil {
		t.Fatal(err)
	}
	raw := string(data)
	var w releaseWorkflow
	if err := yaml.Unmarshal(data, &w); err != nil {
		t.Fatalf("release.yml does not parse: %v", err)
	}

	t.Run("triggers on the push of a v* tag only", func(t *testing.T) {
		if len(w.On) != 1 {
			t.Fatalf("on = %v, want only push", w.On)
		}
		push, _ := w.On["push"].(map[string]any)
		tags, _ := push["tags"].([]any)
		if len(push) != 1 || len(tags) != 1 || tags[0] != "v*" {
			t.Errorf("on.push = %v, want only tags: [v*]", w.On["push"])
		}
	})

	t.Run("contents: write at job level and nothing else", func(t *testing.T) {
		if len(w.Permissions) != 0 {
			t.Errorf("workflow-level permissions = %v, want none", w.Permissions)
		}
		if len(w.Jobs) == 0 {
			t.Fatal("no jobs")
		}
		for name, j := range w.Jobs {
			if len(j.Permissions) != 1 || j.Permissions["contents"] != "write" {
				t.Errorf("job %s permissions = %v, want only contents: write", name, j.Permissions)
			}
		}
	})

	t.Run("every uses: is pinned to a full commit SHA", func(t *testing.T) {
		n := 0
		for _, j := range w.Jobs {
			for _, s := range j.Steps {
				if s.Uses == "" {
					continue
				}
				n++
				if !pinned.MatchString(s.Uses) {
					t.Errorf("uses %q is not pinned to a 40-hex commit SHA", s.Uses)
				}
			}
		}
		if n < 2 {
			t.Errorf("%d uses: steps, want at least checkout and setup-go", n)
		}
	})

	t.Run("the build, the four targets, the checksums and the publish", func(t *testing.T) {
		for _, frag := range []string{"go-version-file: go.mod", "CGO_ENABLED=0", "-trimpath", "-buildvcs=false", `-ldflags "-s -w -X main.version=`,
			"linux/amd64", "linux/arm64", "darwin/amd64", "darwin/arm64", "intentgate_", "SHA256SUMS", "sha256sum",
			"gh release create", "--verify-tag", `^v[0-9]+\.[0-9]+\.[0-9]+$`} {
			if !strings.Contains(raw, frag) {
				t.Errorf("release.yml lacks %q", frag)
			}
		}
		for _, bad := range []string{"git tag", "git push", "gh release upload"} {
			if strings.Contains(raw, bad) {
				t.Errorf("release.yml contains %q: pushing a tag is the human's act", bad)
			}
		}
	})

	t.Run("the tag and the token reach scripts only through env:", func(t *testing.T) {
		inEnv := 0
		tokenEnv := false
		for _, j := range w.Jobs {
			for _, s := range j.Steps {
				if strings.Contains(s.Run, "${{") {
					t.Errorf("step %q: run contains ${{", s.Name)
				}
				for k, v := range s.Env {
					if v == "${{ github.ref_name }}" {
						inEnv++
					}
					if k == "GH_TOKEN" && v == "${{ github.token }}" {
						tokenEnv = true
					}
				}
			}
		}
		if inEnv == 0 {
			t.Error("no step passes ${{ github.ref_name }} through env:")
		}
		if got := strings.Count(raw, "github.ref_name"); got != inEnv {
			t.Errorf("github.ref_name appears %d times, %d of them in env:", got, inEnv)
		}
		if !tokenEnv {
			t.Error("no step passes GH_TOKEN: ${{ github.token }} through env:")
		}
	})
}
