package ratify

import (
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"intentgate/internal/lock"
)

func builtLock(t *testing.T, text string) lock.Lock {
	t.Helper()
	in := testInput()
	in.RootIntent = masked(text)
	l, err := Build(in)
	if err != nil {
		t.Fatalf("Build: %v", err)
	}
	return l
}

// REQ-0006
func TestStore_RootIntentVerbatim(t *testing.T) {
	const text = "Ship the é feature 日本.\n\nKeep the trailing blank lines.\n\n"
	t.Run("the record's bytes are the masked text and its SHA-256 is the lock's", func(t *testing.T) {
		repo, state := emptyDirs(t)
		l := builtLock(t, text)
		if err := (Store{RepoRoot: repo, StateDir: state}).Write(l, masked(text)); err != nil {
			t.Fatalf("Write: %v", err)
		}
		recPath := filepath.Join(state, "locks", testID, "root-intent.md")
		rec, err := os.ReadFile(recPath)
		if err != nil {
			t.Fatal(err)
		}
		if string(rec) != text {
			t.Fatalf("record = %q, want %q", rec, text)
		}
		if sha(string(rec)) != l.RootIntent.SHA256 {
			t.Fatalf("sha256(record) = %s, lock says %s", sha(string(rec)), l.RootIntent.SHA256)
		}
		if fi, err := os.Stat(recPath); err != nil || fi.Mode().Perm() != 0o600 {
			t.Fatalf("record mode = %v, %v; want 0600", fi.Mode(), err)
		}
		if fi, err := os.Stat(filepath.Dir(recPath)); err != nil || fi.Mode().Perm() != 0o700 {
			t.Fatalf("record dir mode = %v, %v; want 0700", fi.Mode(), err)
		}
		committed, err := os.ReadFile(filepath.Join(repo, ".intent", testID+".yml"))
		if err != nil {
			t.Fatal(err)
		}
		got, err := lock.Parse(committed)
		if err != nil || !reflect.DeepEqual(got, l) {
			t.Fatalf("committed lock = %+v, %v; want %+v", got, err, l)
		}
		if strings.Contains(string(committed), "Ship the") {
			t.Fatal("the root intent text leaked into the committed lock")
		}
		// no temp files are left behind
		for _, dir := range []string{filepath.Join(repo, ".intent"), filepath.Dir(recPath)} {
			ents, _ := os.ReadDir(dir)
			if len(ents) != 1 {
				t.Errorf("%s has %d entries, want 1", dir, len(ents))
			}
		}
	})
	t.Run("a text that does not match root_intent.sha256 is refused and writes nothing", func(t *testing.T) {
		repo, state := emptyDirs(t)
		l := builtLock(t, text)
		for _, bad := range []string{text + "x", strings.TrimSuffix(text, "\n"), ""} {
			if err := (Store{RepoRoot: repo, StateDir: state}).Write(l, masked(bad)); err == nil {
				t.Fatalf("Write accepted a text of %d bytes that does not match the lock", len(bad))
			}
		}
		if !isEmpty(t, repo) || !isEmpty(t, state) {
			t.Fatal("a refused write left files behind")
		}
	})
	t.Run("an invalid lock or a tampered hash is refused", func(t *testing.T) {
		repo, state := emptyDirs(t)
		l := builtLock(t, text)
		l.BaseRef = "other" // hash no longer matches
		if err := (Store{RepoRoot: repo, StateDir: state}).Write(l, masked(text)); err == nil {
			t.Fatal("Write accepted a lock whose hash does not verify")
		}
		l = builtLock(t, text)
		l.ID = "../../escape"
		if err := (Store{RepoRoot: repo, StateDir: state}).Write(l, masked(text)); err == nil {
			t.Fatal("Write accepted a lock id that is not a ULID")
		}
		if !isEmpty(t, repo) || !isEmpty(t, state) {
			t.Fatal("a refused write left files behind")
		}
	})
	t.Run("an existing lock id is refused and nothing is overwritten", func(t *testing.T) {
		repo, state := emptyDirs(t)
		s := Store{RepoRoot: repo, StateDir: state}
		first := builtLock(t, text)
		if err := s.Write(first, masked(text)); err != nil {
			t.Fatal(err)
		}
		lockPath := filepath.Join(repo, ".intent", testID+".yml")
		recPath := filepath.Join(state, "locks", testID, "root-intent.md")
		beforeLock, _ := os.ReadFile(lockPath)

		second := builtLock(t, "different intent\n") // same id
		if err := s.Write(second, masked("different intent\n")); err == nil {
			t.Fatal("Write overwrote an existing lock id")
		}
		if err := s.Write(first, masked(text)); err == nil {
			t.Fatal("Write accepted the same lock twice")
		}
		afterLock, _ := os.ReadFile(lockPath)
		rec, _ := os.ReadFile(recPath)
		if string(afterLock) != string(beforeLock) || string(rec) != text {
			t.Fatal("a refused write changed the stored files")
		}
	})
	t.Run("the root-intent record is never overwritten, even when the lock file is gone", func(t *testing.T) {
		repo, state := emptyDirs(t)
		s := Store{RepoRoot: repo, StateDir: state}
		first := builtLock(t, text)
		if err := s.Write(first, masked(text)); err != nil {
			t.Fatal(err)
		}
		lockPath := filepath.Join(repo, ".intent", testID+".yml")
		recPath := filepath.Join(state, "locks", testID, "root-intent.md")
		if err := os.Remove(lockPath); err != nil {
			t.Fatal(err)
		}
		other := "different intent\n"
		if err := s.Write(builtLock(t, other), masked(other)); err == nil {
			t.Fatal("Write overwrote the root-intent record of an existing id")
		}
		if rec, _ := os.ReadFile(recPath); string(rec) != text {
			t.Fatalf("the record changed to %q", rec)
		}
		if _, err := os.Stat(lockPath); err == nil {
			t.Fatal("a lock was written beside a record that was not")
		}
	})
	t.Run("a failed lock write leaves no record behind", func(t *testing.T) {
		repo, state := emptyDirs(t)
		// .intent is read-only, so the lock cannot be created after the record was
		dir := filepath.Join(repo, ".intent")
		if err := os.Mkdir(dir, 0o500); err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() { _ = os.Chmod(dir, 0o700) })
		if err := (Store{RepoRoot: repo, StateDir: state}).Write(builtLock(t, text), masked(text)); err == nil {
			t.Fatal("Write succeeded")
		}
		if _, err := os.Stat(filepath.Join(state, "locks", testID, "root-intent.md")); err == nil {
			t.Fatal("the record of a lock that was not written is still there")
		}
	})
}
