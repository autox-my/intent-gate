package ratify

import (
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/autox-my/intent-gate/internal/lock"
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

// amendedFrom is old with revision +1, a glob added to C1 and the amendment recorded, re-hashed.
func amendedFrom(t *testing.T, old lock.Lock) lock.Lock {
	t.Helper()
	next := old
	next.Revision = old.Revision + 1
	next.Criteria = []lock.Criterion{{ID: "C1", Text: old.Criteria[0].Text, Footprint: append(append([]string(nil), old.Criteria[0].Footprint...), "docs/api/**")}}
	next.Amendments = append(append([]lock.Amendment(nil), old.Amendments...), lock.Amendment{
		Revision: next.Revision, Kind: "add-glob", Criterion: "C1", Glob: "docs/api/**",
		By: "git:dev@example.com", At: "2026-11-19T10:00:00Z", Method: "cli-confirm",
	})
	h, err := lock.Hash(next)
	if err != nil {
		t.Fatal(err)
	}
	next.Hash = h
	return next
}

func TestStore_Amend(t *testing.T) {
	const text = "Add SARIF output.\n"
	setup := func(t *testing.T) (Store, lock.Lock, string, string) {
		t.Helper()
		repo, state := emptyDirs(t)
		st := Store{RepoRoot: repo, StateDir: state}
		old := builtLock(t, text)
		if err := st.Write(old, masked(text)); err != nil {
			t.Fatal(err)
		}
		return st, old, filepath.Join(repo, ".intent", old.ID+".yml"), filepath.Join(state, "locks", old.ID, "root-intent.md")
	}

	t.Run("replaces the lock and leaves the root intent record alone", func(t *testing.T) {
		st, old, path, rec := setup(t)
		before, _ := os.ReadFile(rec)
		next := amendedFrom(t, old)
		if err := st.Amend(old, next); err != nil {
			t.Fatalf("Amend: %v", err)
		}
		data, err := os.ReadFile(path)
		if err != nil {
			t.Fatal(err)
		}
		got, err := lock.Parse(data)
		if err != nil {
			t.Fatal(err)
		}
		if !reflect.DeepEqual(got, next) || !lock.VerifyHash(got) || got.Revision != 2 {
			t.Errorf("the file holds %+v, want %+v", got, next)
		}
		after, _ := os.ReadFile(rec)
		if string(after) != text || string(before) != text {
			t.Errorf("root intent record %q (was %q), want %q", after, before, text)
		}
		entries, _ := os.ReadDir(filepath.Dir(path))
		if len(entries) != 1 {
			t.Errorf(".intent holds %d entries after Amend, want just the lock (no temp file)", len(entries))
		}
		if fi, err := os.Stat(path); err != nil || fi.Mode().Perm() != 0o644 {
			t.Errorf("lock mode = %v, %v; want 0644", fi.Mode().Perm(), err)
		}
	})

	refusals := map[string]func(t *testing.T, st Store, old lock.Lock, path string) lock.Lock{
		"a different id": func(t *testing.T, st Store, old lock.Lock, path string) lock.Lock {
			n := amendedFrom(t, old)
			n.ID = "01JD8Z6Q4M7R2T9X3B5N8K1C6G"
			n.Hash, _ = lock.Hash(n)
			return n
		},
		"a different root intent": func(t *testing.T, st Store, old lock.Lock, path string) lock.Lock {
			n := amendedFrom(t, old)
			n.RootIntent.SHA256 = strings.Repeat("c", 64)
			n.Hash, _ = lock.Hash(n)
			return n
		},
		"a revision that is not old + 1": func(t *testing.T, st Store, old lock.Lock, path string) lock.Lock {
			n := amendedFrom(t, old)
			n.Revision = 3
			n.Amendments[0].Revision = 3
			n.Hash, _ = lock.Hash(n)
			return n
		},
		"a lock that does not validate": func(t *testing.T, st Store, old lock.Lock, path string) lock.Lock {
			n := amendedFrom(t, old)
			n.Amendments[0].Glob = "docs/other/**" // not in the footprint
			n.Hash, _ = lock.Hash(n)
			return n
		},
		"a hash that does not verify": func(t *testing.T, st Store, old lock.Lock, path string) lock.Lock {
			n := amendedFrom(t, old)
			n.Hash = "sha256:" + strings.Repeat("0", 64)
			return n
		},
		"a file on disk that changed since old": func(t *testing.T, st Store, old lock.Lock, path string) lock.Lock {
			other := old
			other.Criteria = []lock.Criterion{{ID: "C1", Text: "Someone else amended this.", Footprint: []string{"x/**"}}}
			other.Hash, _ = lock.Hash(other)
			data, err := lock.Marshal(other)
			if err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(path, data, 0o644); err != nil {
				t.Fatal(err)
			}
			return amendedFrom(t, old)
		},
		"a file on disk that was edited without re-hashing": func(t *testing.T, st Store, old lock.Lock, path string) lock.Lock {
			data, _ := os.ReadFile(path)
			if err := os.WriteFile(path, []byte(strings.Replace(string(data), "Findings are", "Findings were", 1)), 0o644); err != nil {
				t.Fatal(err)
			}
			return amendedFrom(t, old)
		},
	}
	for name, mk := range refusals {
		t.Run("refuses "+name, func(t *testing.T) {
			st, old, path, rec := setup(t)
			next := mk(t, st, old, path)
			before, _ := os.ReadFile(path)
			recBefore, _ := os.ReadFile(rec)
			if err := st.Amend(old, next); err == nil {
				t.Fatal("Amend returned nil")
			}
			if after, _ := os.ReadFile(path); !reflect.DeepEqual(before, after) {
				t.Errorf("the lock file changed:\n%s", after)
			}
			if after, _ := os.ReadFile(rec); !reflect.DeepEqual(recBefore, after) {
				t.Error("the root intent record changed")
			}
			if entries, _ := os.ReadDir(filepath.Dir(path)); len(entries) != 1 {
				t.Errorf(".intent holds %d entries, want just the lock", len(entries))
			}
		})
	}

	// Review 1 of S-05: the amendments of next are old's, unchanged, plus exactly one new entry.
	// thirdFrom is second amended again (revision 3, glob docs/more/**), re-hashed.
	thirdFrom := func(t *testing.T, second lock.Lock, mutate func(n *lock.Lock)) lock.Lock {
		t.Helper()
		next := second
		next.Revision = second.Revision + 1
		next.Criteria = []lock.Criterion{{ID: "C1", Text: second.Criteria[0].Text, Footprint: append(append([]string(nil), second.Criteria[0].Footprint...), "docs/more/**")}}
		next.Amendments = append(append([]lock.Amendment(nil), second.Amendments...), lock.Amendment{
			Revision: next.Revision, Kind: "add-glob", Criterion: "C1", Glob: "docs/more/**",
			By: "git:dev@example.com", At: "2026-11-20T10:00:00Z", Method: "cli-confirm",
		})
		if mutate != nil {
			mutate(&next)
		}
		h, err := lock.Hash(next)
		if err != nil {
			t.Fatal(err)
		}
		next.Hash = h
		return next
	}
	secondRevision := func(t *testing.T) (Store, lock.Lock, string) {
		t.Helper()
		st, first, path, _ := setup(t)
		second := amendedFrom(t, first)
		if err := st.Amend(first, second); err != nil {
			t.Fatalf("first Amend: %v", err)
		}
		return st, second, path
	}
	t.Run("a second amendment that only appends is accepted", func(t *testing.T) {
		st, second, _ := secondRevision(t)
		if err := st.Amend(second, thirdFrom(t, second, nil)); err != nil {
			t.Fatalf("Amend: %v", err)
		}
	})
	for name, mutate := range map[string]func(n *lock.Lock){
		"an earlier amendment's by rewritten": func(n *lock.Lock) { n.Amendments[0].By = "git:someone-else@example.com" },
		"an earlier amendment's at rewritten": func(n *lock.Lock) { n.Amendments[0].At = "2026-11-19T10:00:01Z" },
		"an earlier amendment's method":       func(n *lock.Lock) { n.Amendments[0].Method = "hook" },
		"an earlier amendment dropped":        func(n *lock.Lock) { n.Amendments = n.Amendments[1:] },
		"the new entry's revision not new's":  func(n *lock.Lock) { n.Amendments[len(n.Amendments)-1].Revision = 2 },
	} {
		t.Run("refuses "+name+", even re-hashed", func(t *testing.T) {
			st, second, path := secondRevision(t)
			before, _ := os.ReadFile(path)
			if err := st.Amend(second, thirdFrom(t, second, mutate)); err == nil {
				t.Fatal("Amend returned nil")
			}
			if after, _ := os.ReadFile(path); !reflect.DeepEqual(before, after) {
				t.Errorf("the lock file changed:\n%s", after)
			}
		})
	}

	t.Run("refuses when there is no lock to replace", func(t *testing.T) {
		repo, state := emptyDirs(t)
		old := builtLock(t, text)
		if err := (Store{RepoRoot: repo, StateDir: state}).Amend(old, amendedFrom(t, old)); err == nil {
			t.Error("Amend returned nil")
		}
		if !isEmpty(t, repo) || !isEmpty(t, state) {
			t.Error("Amend wrote something")
		}
	})
}
