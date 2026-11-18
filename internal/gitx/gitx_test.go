package gitx

import (
	"os"
	"os/exec"
	"strings"
	"testing"
)

// isolate keeps the developer's own git configuration out of the tests.
func isolate(t *testing.T) {
	t.Helper()
	t.Setenv("GIT_CONFIG_GLOBAL", os.DevNull)
	t.Setenv("GIT_CONFIG_NOSYSTEM", "1")
	t.Setenv("INTENTGATE_BASE_REF", "")
}

func git(t *testing.T, dir string, args ...string) string {
	t.Helper()
	cmd := exec.Command("git", args...)
	cmd.Dir = dir
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("git %s: %v\n%s", strings.Join(args, " "), err, out)
	}
	return strings.TrimSpace(string(out))
}

// repoOn makes a repository whose first commit is on branch.
func repoOn(t *testing.T, branch string) string {
	t.Helper()
	dir := t.TempDir()
	git(t, dir, "init", "-q", "-b", branch)
	git(t, dir, "-c", "user.name=t", "-c", "user.email=t@example.com", "commit", "-q", "--allow-empty", "-m", "first")
	return dir
}

func TestBaseRef(t *testing.T) {
	t.Run("the environment wins", func(t *testing.T) {
		isolate(t)
		dir := repoOn(t, "main")
		t.Setenv("INTENTGATE_BASE_REF", "release/1.x")
		if got, err := BaseRef(dir); err != nil || got != "release/1.x" {
			t.Errorf("BaseRef = %q, %v; want release/1.x", got, err)
		}
	})
	t.Run("origin/HEAD, without origin/, wins over main", func(t *testing.T) {
		isolate(t)
		dir := repoOn(t, "main")
		git(t, dir, "update-ref", "refs/remotes/origin/trunk", "HEAD")
		git(t, dir, "symbolic-ref", "refs/remotes/origin/HEAD", "refs/remotes/origin/trunk")
		if got, err := BaseRef(dir); err != nil || got != "trunk" {
			t.Errorf("BaseRef = %q, %v; want trunk", got, err)
		}
	})
	t.Run("main", func(t *testing.T) {
		isolate(t)
		if got, err := BaseRef(repoOn(t, "main")); err != nil || got != "main" {
			t.Errorf("BaseRef = %q, %v; want main", got, err)
		}
	})
	t.Run("master", func(t *testing.T) {
		isolate(t)
		if got, err := BaseRef(repoOn(t, "master")); err != nil || got != "master" {
			t.Errorf("BaseRef = %q, %v; want master", got, err)
		}
	})
	t.Run("none is an error", func(t *testing.T) {
		isolate(t)
		if got, err := BaseRef(repoOn(t, "develop")); err == nil {
			t.Errorf("BaseRef = %q, nil; want an error", got)
		}
	})
}

func TestTopLevelAndUserEmail(t *testing.T) {
	isolate(t)
	dir := repoOn(t, "main")
	sub := dir + "/a/b"
	if err := os.MkdirAll(sub, 0o755); err != nil {
		t.Fatal(err)
	}
	want, _ := exec.Command("git", "-C", dir, "rev-parse", "--show-toplevel").Output()
	if got, err := TopLevel(sub); err != nil || got != strings.TrimSpace(string(want)) {
		t.Errorf("TopLevel = %q, %v; want %q", got, err, strings.TrimSpace(string(want)))
	}
	if _, err := TopLevel(t.TempDir()); err == nil {
		t.Error("TopLevel outside a repository: want an error")
	}

	if got, err := UserEmail(dir); err == nil {
		t.Errorf("UserEmail with none set = %q, nil; want an error", got)
	}
	git(t, dir, "config", "user.email", "dev@example.com")
	if got, err := UserEmail(sub); err != nil || got != "dev@example.com" {
		t.Errorf("UserEmail = %q, %v; want dev@example.com", got, err)
	}
}
