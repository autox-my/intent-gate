package verify_test

import (
	"strings"
	"testing"

	"intentgate/internal/footprint"
	"intentgate/internal/verify"
)

// Review 1 of S-07: a changed path that is absolute, or has an empty, "." or ".." segment, makes
// the run unverifiable: such a path can't be matched against a footprint or addressed in a report.
func TestRun_UnsafeChangedPath(t *testing.T) {
	tests := []struct {
		name string
		file footprint.ChangedFile
		bad  string
	}{
		{"absolute", footprint.ChangedFile{Path: "/etc/passwd", Status: footprint.Modified}, "/etc/passwd"},
		{"empty", footprint.ChangedFile{Path: "", Status: footprint.Added}, ""},
		{"dot", footprint.ChangedFile{Path: ".", Status: footprint.Modified}, "."},
		{"dot segment", footprint.ChangedFile{Path: "internal/./report/report.go", Status: footprint.Modified}, "internal/./report/report.go"},
		{"dotdot", footprint.ChangedFile{Path: "..", Status: footprint.Modified}, ".."},
		{"dotdot segment", footprint.ChangedFile{Path: "internal/report/../../secret.txt", Status: footprint.Modified}, "internal/report/../../secret.txt"},
		{"leading dotdot", footprint.ChangedFile{Path: "../outside.go", Status: footprint.Added}, "../outside.go"},
		{"empty segment", footprint.ChangedFile{Path: "internal//report.go", Status: footprint.Modified}, "internal//report.go"},
		{"trailing slash", footprint.ChangedFile{Path: "internal/report/", Status: footprint.Modified}, "internal/report/"},
		{"old path of a rename", footprint.ChangedFile{Path: "pkg/b.go", OldPath: "../a.go", Status: footprint.Renamed}, "../a.go"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			in := base(t)
			in.Changed = append(in.Changed, tc.file)
			r := verify.Run(in)
			wantOutcome(t, r, verify.Unverifiable, 3)
			want := "unsafe changed path " + `"` + tc.bad + `"`
			found := false
			for _, p := range r.Problems {
				if p == want {
					found = true
				}
			}
			if !found {
				t.Errorf("problems = %q, want one equal to %q", r.Problems, want)
			}
			if r.Locks == nil || r.Findings == nil {
				t.Errorf("Locks and Findings must be non-nil: %+v", r)
			}
		})
	}

	t.Run("ordinary and dotfile paths are fine", func(t *testing.T) {
		in := base(t)
		in.Changed = modified("internal/report/report.go", ".github/workflows/ci.yml", "docs/a b#1.go", "internal/policy/..hidden", "internal/policy/.x")
		for _, r := range []verify.Result{verify.Run(in)} {
			if r.Outcome == verify.Unverifiable {
				t.Errorf("safe paths made the run unverifiable: %+v", r.Problems)
			}
		}
	})

	t.Run("the problems are sorted and listed once each", func(t *testing.T) {
		in := base(t)
		in.Changed = []footprint.ChangedFile{
			{Path: "z/../b.go", Status: footprint.Modified},
			{Path: "/a.go", Status: footprint.Modified},
			{Path: "/a.go", Status: footprint.Added},
		}
		r := verify.Run(in)
		want := []string{`unsafe changed path "/a.go"`, `unsafe changed path "z/../b.go"`}
		if strings.Join(r.Problems, "\n") != strings.Join(want, "\n") {
			t.Errorf("problems = %q, want %q", r.Problems, want)
		}
	})
}
