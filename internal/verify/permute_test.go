package verify_test

import (
	"bytes"
	"math/rand"
	"testing"

	"intentgate/internal/footprint"
	"intentgate/internal/lock"
	"intentgate/internal/verify"
)

// permuteCases are the golden cases plus cases whose Changed and LockFiles entries agree on their
// primary keys (same path, different status or old path; same lock file path twice).
func permuteCases() []goldenCase {
	cases := goldenCases()
	cases = append(cases,
		goldenCase{"equal-changed-keys", func(t *testing.T) verify.Input {
			in := base(t)
			in.Changed = []footprint.ChangedFile{
				{Path: "pkg/b.go", OldPath: "pkg/a.go", Status: footprint.Renamed},
				{Path: "pkg/b.go", OldPath: "pkg/z.go", Status: footprint.Renamed},
				{Path: "pkg/b.go", Status: footprint.Modified},
				{Path: "pkg/b.go", Status: footprint.Added},
				{Path: "pkg/a.go", Status: footprint.Deleted},
				{Path: "pkg/a.go", Status: footprint.Modified},
				{Path: "internal/report/report.go", Status: footprint.Modified},
			}
			return in
		}},
		goldenCase{"equal-lock-file-keys", func(t *testing.T) verify.Input {
			in := base(t)
			dup := added(t, lockSpec{})
			dup.Status = footprint.Modified
			other := verify.LockFile{Path: dup.Path, Status: footprint.Added, Data: lockYAML(t, lockSpec{tamper: func(l *lock.Lock) { l.Revision = 2 }})}
			in.LockFiles = []verify.LockFile{added(t, lockSpec{}), dup, other,
				{Path: ".intent/" + idB + ".yml", Status: "copied", Data: lockYAML(t, lockSpec{id: idB})},
				{Path: ".intent/" + idB + ".yml", Status: footprint.Added, Data: []byte("{")}}
			return in
		}},
		goldenCase{"equal-lock-file-keys-distinct-paths", func(t *testing.T) verify.Input {
			in := base(t)
			in.BaseBranch = "develop"
			in.LockFiles = []verify.LockFile{
				added(t, lockSpec{}),
				{Path: ".intent/" + idB + ".yml", Status: footprint.Added, Data: lockYAML(t, lockSpec{id: idB})},
				{Path: ".intent/" + idB + "x.yml", Status: footprint.Added, Data: lockYAML(t, lockSpec{id: idB})},
			}
			return in
		}},
	)
	return cases
}

// Review 1 of S-07: every output sort is total, so ≥ 20 seeded permutations of Changed and
// LockFiles, including entries with equal primary keys, give the bytes of the original order.
func TestRun_OutputIsIndependentOfInputOrder(t *testing.T) {
	for _, tc := range permuteCases() {
		t.Run(tc.name, func(t *testing.T) {
			in := tc.in(t)
			want := render(in)
			rng := rand.New(rand.NewSource(20261118))
			for i := 0; i < 25; i++ {
				p := in
				p.Changed = append([]footprint.ChangedFile(nil), in.Changed...)
				p.LockFiles = append([]verify.LockFile(nil), in.LockFiles...)
				rng.Shuffle(len(p.Changed), func(a, b int) { p.Changed[a], p.Changed[b] = p.Changed[b], p.Changed[a] })
				rng.Shuffle(len(p.LockFiles), func(a, b int) { p.LockFiles[a], p.LockFiles[b] = p.LockFiles[b], p.LockFiles[a] })
				got := render(p)
				for _, sfx := range suffixes() {
					if !bytes.Equal(got[sfx], want[sfx]) {
						t.Fatalf("permutation %d: %s differs.\n got:\n%s\nwant:\n%s", i, sfx, got[sfx], want[sfx])
					}
				}
			}
		})
	}
}
