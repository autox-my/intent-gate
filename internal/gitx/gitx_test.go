package gitx

import (
	"bytes"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"sort"
	"strings"
	"testing"
	"time"
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

// commitAt commits everything in dir with the given author and committer date.
func commitAt(t *testing.T, dir, msg, date string) {
	t.Helper()
	git(t, dir, "add", "-A")
	cmd := exec.Command("git", "-c", "user.name=t", "-c", "user.email=t@example.com", "commit", "-q", "-m", msg)
	cmd.Dir = dir
	cmd.Env = append(os.Environ(), "GIT_COMMITTER_DATE="+date, "GIT_AUTHOR_DATE="+date)
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("git commit: %v\n%s", err, out)
	}
}

func put(t *testing.T, dir, name string, data []byte) {
	t.Helper()
	p := filepath.Join(dir, name)
	if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(p, data, 0o644); err != nil {
		t.Fatal(err)
	}
}

const body = "line one\nline two\nline three\nline four\nline five\nline six\n"

func TestGitx_ChangedFiles(t *testing.T) {
	isolate(t)
	dir := repoOn(t, "main")
	put(t, dir, "keep.txt", []byte("keep\n"))
	put(t, dir, "mod.txt", []byte("one\n"))
	put(t, dir, "gone.txt", []byte("gone\n"))
	put(t, dir, "old name.txt", []byte(body))
	put(t, dir, "orig.txt", []byte(body+"orig\n"))
	put(t, dir, "link.txt", []byte("target\n"))
	put(t, dir, "ünï/old.txt", []byte(body+"unicode\n"))
	commitAt(t, dir, "base", "2026-11-18T09:00:00Z")
	base := git(t, dir, "rev-parse", "HEAD")

	put(t, dir, "mod.txt", []byte("two\n"))
	if err := os.Remove(filepath.Join(dir, "gone.txt")); err != nil {
		t.Fatal(err)
	}
	put(t, dir, "added file.txt", []byte("added\n"))
	put(t, dir, "new\nline.txt", []byte("newline in the name\n"))
	put(t, dir, "ünï/日本語.txt", []byte("non-ascii\n"))
	git(t, dir, "mv", "old name.txt", "new name.txt")
	git(t, dir, "mv", "ünï/old.txt", "ünï/new.txt")
	// orig.txt changes, and its old content is copied: a copy is only found against a changed file
	put(t, dir, "orig.txt", []byte(body+"orig changed\n"))
	put(t, dir, "copy.txt", []byte(body+"orig\n"))
	if err := os.Remove(filepath.Join(dir, "link.txt")); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink("keep.txt", filepath.Join(dir, "link.txt")); err != nil {
		t.Fatal(err)
	}
	commitAt(t, dir, "head", "2026-11-18T10:00:00Z")
	head := git(t, dir, "rev-parse", "HEAD")

	got, err := ChangedFiles(dir, base, head)
	if err != nil {
		t.Fatal(err)
	}
	sort.Slice(got, func(i, j int) bool { return got[i].Path < got[j].Path })
	want := []Change{
		{Status: "A", Path: "added file.txt"},
		{Status: "C100", Path: "copy.txt", OldPath: "orig.txt"},
		{Status: "D", Path: "gone.txt"},
		{Status: "T", Path: "link.txt"},
		{Status: "M", Path: "mod.txt"},
		{Status: "R100", Path: "new name.txt", OldPath: "old name.txt"},
		{Status: "A", Path: "new\nline.txt"},
		{Status: "M", Path: "orig.txt"},
		{Status: "R100", Path: "ünï/new.txt", OldPath: "ünï/old.txt"},
		{Status: "A", Path: "ünï/日本語.txt"},
	}
	sort.Slice(want, func(i, j int) bool { return want[i].Path < want[j].Path })
	if !reflect.DeepEqual(got, want) {
		t.Errorf("ChangedFiles =\n%+v\nwant\n%+v", got, want)
	}

	t.Run("the three-dot range diffs against the merge base", func(t *testing.T) {
		git(t, dir, "checkout", "-q", "-b", "side", base)
		put(t, dir, "side.txt", []byte("side\n"))
		commitAt(t, dir, "side", "2026-11-18T10:30:00Z")
		side := git(t, dir, "rev-parse", "HEAD")
		// main moved on after the fork; the changes of main must not show up
		cs, err := ChangedFiles(dir, head, side)
		if err != nil {
			t.Fatal(err)
		}
		for _, c := range cs {
			if c.Path == "mod.txt" || c.Path == "added file.txt" {
				t.Errorf("%+v comes from the other branch", c)
			}
		}
		if len(cs) != 1 || cs[0] != (Change{Status: "A", Path: "side.txt"}) {
			t.Errorf("ChangedFiles = %+v, want only side.txt added", cs)
		}
	})

	t.Run("errors", func(t *testing.T) {
		if _, err := ChangedFiles(dir, "no-such-rev", head); err == nil {
			t.Error("a bad base rev gave no error")
		}
		if _, err := ChangedFiles(dir, base, "--output=x"); err == nil {
			t.Error("a rev that looks like an option gave no error")
		}
		if _, err := ChangedFiles(t.TempDir(), base, head); err == nil {
			t.Error("a directory that is not a repository gave no error")
		}
	})
}

func TestGitx_ChangedFiles_NoChange(t *testing.T) {
	isolate(t)
	dir := repoOn(t, "main")
	head := git(t, dir, "rev-parse", "HEAD")
	got, err := ChangedFiles(dir, head, head)
	if err != nil || len(got) != 0 {
		t.Errorf("ChangedFiles = %+v, %v; want none", got, err)
	}
}

func TestGitx_ShowFile(t *testing.T) {
	isolate(t)
	dir := repoOn(t, "main")
	raw := []byte("no trailing newline\x00binary\r\n  ")
	put(t, dir, "a b/c.txt", raw)
	put(t, dir, "empty.txt", nil)
	put(t, dir, "glob*.txt", []byte("star\n"))
	put(t, dir, "globx.txt", []byte("x\n"))
	commitAt(t, dir, "c", "2026-11-18T09:00:00Z")
	head := git(t, dir, "rev-parse", "HEAD")
	put(t, dir, "later.txt", []byte("later\n"))
	commitAt(t, dir, "later", "2026-11-18T10:00:00Z")
	sub := filepath.Join(dir, "a b")

	got, found, err := ShowFile(dir, head, "a b/c.txt")
	if err != nil || !found || !bytes.Equal(got, raw) {
		t.Errorf("ShowFile = %q, %v, %v; want the exact bytes %q", got, found, err, raw)
	}
	// paths are relative to the repository root, whatever directory is passed
	if got, found, err := ShowFile(sub, head, "a b/c.txt"); err != nil || !found || !bytes.Equal(got, raw) {
		t.Errorf("from a subdirectory: %q, %v, %v", got, found, err)
	}
	if got, found, err := ShowFile(dir, head, "empty.txt"); err != nil || !found || len(got) != 0 {
		t.Errorf("empty file: %q, %v, %v; want found and empty", got, found, err)
	}
	if got, found, err := ShowFile(dir, head, "glob*.txt"); err != nil || !found || string(got) != "star\n" {
		t.Errorf("a path with a glob character: %q, %v, %v", got, found, err)
	}
	if got, found, err := ShowFile(dir, head, "missing.txt"); err != nil || found || got != nil {
		t.Errorf("absent file: %q, %v, %v; want nil, false, nil", got, found, err)
	}
	if _, found, err := ShowFile(dir, head, "later.txt"); err != nil || found {
		t.Errorf("a file added after the rev: found %v, err %v; want absent", found, err)
	}
	if _, _, err := ShowFile(dir, "no-such-rev", "empty.txt"); err == nil {
		t.Error("a bad rev gave no error: it must not read as an absent file")
	}
	if _, _, err := ShowFile(t.TempDir(), head, "empty.txt"); err == nil {
		t.Error("a directory that is not a repository gave no error")
	}
	if _, _, err := ShowFile(dir, head, "a b"); err == nil {
		t.Error("a directory is not a file: want an error")
	}
}

func TestGitx_CommitTime(t *testing.T) {
	isolate(t)
	dir := repoOn(t, "main")
	put(t, dir, "x.txt", []byte("x\n"))
	// the committer date is the one that counts; the author date is different on purpose
	git(t, dir, "add", "-A")
	cmd := exec.Command("git", "-c", "user.name=t", "-c", "user.email=t@example.com", "commit", "-q", "-m", "x")
	cmd.Dir = dir
	cmd.Env = append(os.Environ(), "GIT_COMMITTER_DATE=2026-11-18T09:20:00+07:00", "GIT_AUTHOR_DATE=2020-01-01T00:00:00Z")
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("%v\n%s", err, out)
	}
	got, err := CommitTime(dir, "HEAD")
	if err != nil {
		t.Fatal(err)
	}
	if want := time.Date(2026, 11, 18, 2, 20, 0, 0, time.UTC); !got.Equal(want) || got.Location() != time.UTC {
		t.Errorf("CommitTime = %v, want %v in UTC", got, want)
	}
	if _, err := CommitTime(dir, "no-such-rev"); err == nil {
		t.Error("a bad rev gave no error")
	}
	if _, err := CommitTime(dir, "--all"); err == nil {
		t.Error("a rev that looks like an option gave no error")
	}
}

// TestGitx_IgnoresCallerEnvironment: the answers of ChangedFiles, ShowFile and CommitTime do not
// depend on the caller's directory, git environment variables or git configuration (S-08,
// review 1 amendments).
func TestGitx_IgnoresCallerEnvironment(t *testing.T) {
	isolate(t)
	dir := repoOn(t, "main")
	put(t, dir, "src/a.txt", []byte("a\n"))
	put(t, dir, "top.txt", []byte("top\n"))
	commitAt(t, dir, "base", "2026-11-18T09:00:00Z")
	base := git(t, dir, "rev-parse", "HEAD")
	put(t, dir, "src/a.txt", []byte("a changed\n"))
	put(t, dir, "top.txt", []byte("top changed\n"))
	put(t, dir, "docs/new.md", []byte("new\n"))
	commitAt(t, dir, "head", "2026-11-18T10:00:00Z")
	head := git(t, dir, "rev-parse", "HEAD")
	sub := filepath.Join(dir, "src")

	want := []Change{
		{Status: "A", Path: "docs/new.md"},
		{Status: "M", Path: "src/a.txt"},
		{Status: "M", Path: "top.txt"},
	}
	wantTime := time.Date(2026, 11, 18, 10, 0, 0, 0, time.UTC)
	check := func(t *testing.T, from string) {
		t.Helper()
		got, err := ChangedFiles(from, base, head)
		if err != nil || !reflect.DeepEqual(got, want) {
			t.Errorf("ChangedFiles = %+v, %v; want %+v", got, err, want)
		}
		data, found, err := ShowFile(from, head, "src/a.txt")
		if err != nil || !found || string(data) != "a changed\n" {
			t.Errorf("ShowFile = %q, %v, %v", data, found, err)
		}
		if tm, err := CommitTime(from, head); err != nil || !tm.Equal(wantTime) {
			t.Errorf("CommitTime = %v, %v; want %v", tm, err, wantTime)
		}
	}

	t.Run("from the top level", func(t *testing.T) { check(t, dir) })
	t.Run("from a subdirectory", func(t *testing.T) { check(t, sub) })
	t.Run("GIT_DIR pointing at another repository", func(t *testing.T) {
		other := repoOn(t, "main")
		t.Setenv("GIT_DIR", filepath.Join(other, ".git"))
		t.Setenv("GIT_WORK_TREE", other)
		check(t, sub)
	})
	t.Run("GIT_CONFIG_COUNT sets diff.relative", func(t *testing.T) {
		t.Setenv("GIT_CONFIG_COUNT", "1")
		t.Setenv("GIT_CONFIG_KEY_0", "diff.relative")
		t.Setenv("GIT_CONFIG_VALUE_0", "true")
		check(t, sub)
	})
	t.Run("a global config file sets diff.relative", func(t *testing.T) {
		global := filepath.Join(t.TempDir(), "gitconfig")
		put(t, filepath.Dir(global), "gitconfig", []byte("[diff]\n\trelative = true\n\tnoprefix = true\n"))
		t.Setenv("GIT_CONFIG_GLOBAL", global)
		check(t, sub)
	})
	t.Run("the repository's own config sets diff.relative", func(t *testing.T) {
		git(t, dir, "config", "diff.relative", "true")
		git(t, dir, "config", "diff.noprefix", "true")
		defer git(t, dir, "config", "--unset", "diff.relative")
		defer git(t, dir, "config", "--unset", "diff.noprefix")
		check(t, sub)
	})
}

func TestGitx_ShowFile_Symlink(t *testing.T) {
	isolate(t)
	dir := repoOn(t, "main")
	put(t, dir, "real.yml", []byte("x: 1\n"))
	if err := os.Symlink("real.yml", filepath.Join(dir, "link.yml")); err != nil {
		t.Fatal(err)
	}
	commitAt(t, dir, "c", "2026-11-18T09:00:00Z")
	_, found, err := ShowFile(dir, "HEAD", "link.yml")
	if err == nil || found || !strings.Contains(err.Error(), "link.yml is a symlink in HEAD; commit the file itself") {
		t.Errorf("ShowFile of a symlink = found %v, err %v; want the symlink error", found, err)
	}
}
