package verify_test

import (
	"strings"
	"testing"

	"intentgate/internal/footprint"
	"intentgate/internal/verify"
)

// Review 1 of S-07: only added, modified, renamed and deleted are lock file statuses the check
// understands. Anything else makes that lock unverifiable, so a valid lock can never conclude
// clean through an unknown status.
func TestRun_UnsupportedLockStatus(t *testing.T) {
	for _, status := range []footprint.Status{"copied", "", "type-changed", "ADDED"} {
		t.Run("status "+string(status), func(t *testing.T) {
			in := base(t)
			in.LockFiles[0].Status = status
			r := verify.Run(in)
			wantOutcome(t, r, verify.Unverifiable, 3)
			if len(r.Locks) != 1 {
				t.Fatalf("locks = %d, want 1: %+v", len(r.Locks), r.Locks)
			}
			lr := r.Locks[0]
			if lr.Path != in.LockFiles[0].Path || len(lr.Criteria) != 0 {
				t.Errorf("lock result: %+v (unverifiable: its path, no criteria)", lr)
			}
			shown := string(status)
			if shown == "" {
				shown = `""`
			}
			want := "unsupported change status " + shown
			if len(lr.Problems) != 1 || lr.Problems[0] != want {
				t.Errorf("problems = %q, want [%q]", lr.Problems, want)
			}
		})
	}

	t.Run("one unsupported lock does not hide a good one", func(t *testing.T) {
		in := base(t)
		bad := verify.LockFile{Path: ".intent/" + idB + ".yml", Status: "copied", Data: lockYAML(t, lockSpec{id: idB})}
		in.LockFiles = []verify.LockFile{bad, added(t, lockSpec{})}
		r := verify.Run(in)
		wantOutcome(t, r, verify.Unverifiable, 3)
		if len(r.Locks) != 2 || len(r.Locks[0].Criteria) != 2 || len(r.Locks[1].Criteria) != 0 {
			t.Errorf("locks: %+v", r.Locks)
		}
	})

	t.Run("the supported statuses still work", func(t *testing.T) {
		in := base(t)
		in.LockFiles[0].Status = footprint.Added
		wantOutcome(t, verify.Run(in), verify.Clean, 0)
		in.LockFiles[0].Status = footprint.Deleted
		wantOutcome(t, verify.Run(in), verify.NoLock, 0)
	})
}

func TestRun_DuplicateLockFileEntry(t *testing.T) {
	two := func(a, b footprint.Status) verify.Input {
		in := base(t)
		f := added(t, lockSpec{})
		f.Status = a
		g := f
		g.Status = b
		in.LockFiles = []verify.LockFile{f, g}
		return in
	}
	for _, tc := range []struct{ a, b footprint.Status }{
		{footprint.Added, footprint.Added},
		{footprint.Added, footprint.Modified},
		{footprint.Deleted, footprint.Added},
	} {
		t.Run(string(tc.a)+" and "+string(tc.b), func(t *testing.T) {
			r := verify.Run(two(tc.a, tc.b))
			wantOutcome(t, r, verify.Unverifiable, 3)
			want := "duplicate lock file entry .intent/" + idA + ".yml"
			if len(r.Problems) != 1 || r.Problems[0] != want {
				t.Errorf("problems = %q, want [%q]", r.Problems, want)
			}
			if r.Locks == nil || r.Findings == nil {
				t.Errorf("Locks and Findings must be non-nil: %+v", r)
			}
		})
	}
	// the same two entries in the other order are the same result
	a, b := verify.Run(two(footprint.Added, footprint.Modified)), verify.Run(two(footprint.Modified, footprint.Added))
	if strings.Join(a.Problems, "|") != strings.Join(b.Problems, "|") || a.Outcome != b.Outcome {
		t.Errorf("order changed the result: %+v vs %+v", a, b)
	}
}
