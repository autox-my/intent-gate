package verify_test

import (
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/autox-my/intent-gate/internal/footprint"
	"github.com/autox-my/intent-gate/internal/lock"
	"github.com/autox-my/intent-gate/internal/verify"
)

const (
	idA = "01JD8Z6Q4M7R2T9X3B5N8K1C6F"
	idB = "01JD8Z6Q4M7R2T9X3B5N8K1C7G"
)

var ratifiedAt = time.Date(2026, 11, 18, 9, 14, 3, 0, time.UTC)

// lockSpec describes a lock for a test. tamper edits the lock after it is hashed.
type lockSpec struct {
	id, baseRef, ratifiedAt string
	criteria                []lock.Criterion
	tamper                  func(*lock.Lock)
}

func intp(n int) *int { return &n }

// lockYAML renders a valid, correctly hashed lock (then tampered, if asked).
func lockYAML(t *testing.T, s lockSpec) []byte {
	t.Helper()
	if s.id == "" {
		s.id = idA
	}
	if s.baseRef == "" {
		s.baseRef = "main"
	}
	if s.ratifiedAt == "" {
		s.ratifiedAt = "2026-11-18T09:14:03Z"
	}
	if s.criteria == nil {
		s.criteria = []lock.Criterion{
			{ID: "C1", Text: "Findings are published as SARIF.", Footprint: []string{"internal/report/**", "!internal/report/legacy/**"}},
			{ID: "C2", Text: "Policy maps outcomes to conclusions.", Footprint: []string{"internal/policy/**"}},
		}
	}
	l := lock.Lock{
		Schema:   lock.SchemaID,
		ID:       s.id,
		Revision: 1,
		BaseRef:  s.baseRef,
		RootIntent: lock.RootIntent{
			SHA256:     strings.Repeat("ab", 32),
			Source:     "claude-code-session",
			Redactions: intp(0),
		},
		Criteria: s.criteria,
		Ratification: lock.Ratification{
			Ratifier:         "github:octocat",
			RatifiedAt:       s.ratifiedAt,
			Method:           "hook",
			ReviewDurationMS: intp(48210),
		},
	}
	h, err := lock.Hash(l)
	if err != nil {
		t.Fatalf("lock.Hash: %v", err)
	}
	l.Hash = h
	if s.tamper != nil {
		s.tamper(&l)
	}
	data, err := lock.Marshal(l)
	if err != nil {
		t.Fatalf("lock.Marshal: %v", err)
	}
	if problems := lock.Validate(l); s.tamper == nil && len(problems) > 0 {
		t.Fatalf("test lock is invalid: %v", problems)
	}
	return data
}

func added(t *testing.T, s lockSpec) verify.LockFile {
	t.Helper()
	id := s.id
	if id == "" {
		id = idA
	}
	return verify.LockFile{Path: ".intent/" + id + ".yml", Status: footprint.Added, Data: lockYAML(t, s)}
}

func modified(paths ...string) []footprint.ChangedFile {
	cs := make([]footprint.ChangedFile, len(paths))
	for i, p := range paths {
		cs[i] = footprint.ChangedFile{Path: p, Status: footprint.Modified}
	}
	return cs
}

// base is a PR input with one valid lock, evaluated a day after ratification.
func base(t *testing.T) verify.Input {
	t.Helper()
	return verify.Input{
		BaseBranch: "main",
		Author:     "octocat",
		AuthorType: "User",
		EvalTime:   ratifiedAt.Add(24 * time.Hour),
		Changed:    modified("internal/report/report.go", "internal/policy/policy.go"),
		LockFiles:  []verify.LockFile{added(t, lockSpec{})},
	}
}

func suspectOf(t *testing.T, r verify.Result, idx int) []string {
	t.Helper()
	if len(r.Locks) <= idx {
		t.Fatalf("Locks = %d, want at least %d: %+v", len(r.Locks), idx+1, r)
	}
	return r.Locks[idx].Suspect
}

func wantOutcome(t *testing.T, r verify.Result, o verify.Outcome, code int) {
	t.Helper()
	if r.Outcome != o || r.ExitCode != code {
		t.Fatalf("outcome = %q exit %d, want %q exit %d; result: %+v", r.Outcome, r.ExitCode, o, code, r)
	}
}

func TestRun_Clean(t *testing.T) {
	r := verify.Run(base(t))
	wantOutcome(t, r, verify.Clean, 0)
	if len(r.Locks) != 1 || len(r.Locks[0].Criteria) != 2 || len(r.Locks[0].Suspect) != 0 || len(r.Findings) != 0 {
		t.Errorf("result: %+v", r)
	}
}

func TestRun_NoLock(t *testing.T) {
	in := base(t)
	in.LockFiles = nil
	wantOutcome(t, verify.Run(in), verify.NoLock, 0)

	// a deleted lock file is ignored
	in.LockFiles = []verify.LockFile{{Path: ".intent/" + idA + ".yml", Status: footprint.Deleted}}
	wantOutcome(t, verify.Run(in), verify.NoLock, 0)
}

// REQ-0021
func TestRun_Retargeted(t *testing.T) {
	tests := []struct {
		name        string
		baseBranch  string
		wantOut     verify.Outcome
		wantCode    int
		wantSuspect []string
	}{
		{"same base", "main", verify.Clean, 0, nil},
		{"retargeted to develop", "develop", verify.Findings, 1, []string{"retargeted"}},
		{"case differs", "Main", verify.Findings, 1, []string{"retargeted"}},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			in := base(t)
			in.BaseBranch = tc.baseBranch
			r := verify.Run(in)
			wantOutcome(t, r, tc.wantOut, tc.wantCode)
			got := suspectOf(t, r, 0)
			if len(got) != len(tc.wantSuspect) || (len(got) > 0 && !reflect.DeepEqual(got, tc.wantSuspect)) {
				t.Errorf("suspect = %v, want %v", got, tc.wantSuspect)
			}
		})
	}
}

// REQ-0023
func TestRun_HashMismatch(t *testing.T) {
	in := base(t)
	in.LockFiles = []verify.LockFile{added(t, lockSpec{tamper: func(l *lock.Lock) {
		l.Criteria[0].Footprint = append(l.Criteria[0].Footprint, "**") // widened by hand, hash not redone
	}})}
	in.Changed = modified("internal/report/report.go", "cmd/other/main.go")
	r := verify.Run(in)
	wantOutcome(t, r, verify.Findings, 1)
	if got := suspectOf(t, r, 0); !reflect.DeepEqual(got, []string{"hash-mismatch"}) {
		t.Errorf("suspect = %v, want [hash-mismatch]", got)
	}
	// every criterion of the lock is still reported, and the footprint check still runs
	if n := len(r.Locks[0].Criteria); n != 2 {
		t.Errorf("criteria = %d, want 2 (a suspect lock keeps its criteria)", n)
	}
	// the hand-widened `**` makes cmd/other/main.go inside the footprint, so no finding: it is
	// footprint-checked against the lock as written, and only the suspect marker raises the outcome
	if len(r.Findings) != 0 {
		t.Errorf("findings = %+v, want none (the tampered footprint covers every path)", r.Findings)
	}

	// a lock whose hash does not verify and a file outside its footprint: both are reported
	in.LockFiles = []verify.LockFile{added(t, lockSpec{tamper: func(l *lock.Lock) { l.Criteria[1].Text = "edited by hand" }})}
	r = verify.Run(in)
	wantOutcome(t, r, verify.Findings, 1)
	if got := suspectOf(t, r, 0); !reflect.DeepEqual(got, []string{"hash-mismatch"}) {
		t.Errorf("suspect = %v, want [hash-mismatch]", got)
	}
	if len(r.Findings) != 1 || r.Findings[0].Path != "cmd/other/main.go" {
		t.Errorf("findings = %+v, want cmd/other/main.go (suspect criteria are still footprint-checked)", r.Findings)
	}
}

func TestRun_Expired(t *testing.T) {
	tests := []struct {
		name string
		at   time.Time
		want []string
	}{
		{"one day old", ratifiedAt.Add(24 * time.Hour), nil},
		{"exactly 14 days", ratifiedAt.Add(14 * 24 * time.Hour), nil},
		{"14 days and a second", ratifiedAt.Add(14*24*time.Hour + time.Second), []string{"expired"}},
		{"before ratification", ratifiedAt.Add(-time.Second), []string{"expired"}},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			in := base(t)
			in.EvalTime = tc.at
			r := verify.Run(in)
			got := suspectOf(t, r, 0)
			if len(got) != len(tc.want) || (len(got) > 0 && !reflect.DeepEqual(got, tc.want)) {
				t.Fatalf("suspect = %v, want %v", got, tc.want)
			}
			if tc.want == nil {
				wantOutcome(t, r, verify.Clean, 0)
			} else {
				wantOutcome(t, r, verify.Findings, 1)
			}
		})
	}
}

func TestRun_SuspectReasonsSorted(t *testing.T) {
	in := base(t)
	in.BaseBranch = "develop"
	in.EvalTime = ratifiedAt.Add(30 * 24 * time.Hour)
	in.LockFiles = []verify.LockFile{added(t, lockSpec{tamper: func(l *lock.Lock) { l.Revision = 1; l.Criteria[1].Text = "x" }})}
	in.LockFiles[0].Status = footprint.Modified
	r := verify.Run(in)
	want := []string{"expired", "hash-mismatch", "modifies-existing-lock", "retargeted"}
	if got := suspectOf(t, r, 0); !reflect.DeepEqual(got, want) {
		t.Errorf("suspect = %v, want %v", got, want)
	}
}

func TestRun_ModifiesExistingLock(t *testing.T) {
	tests := []struct {
		name    string
		status  footprint.Status
		oldPath string
		want    []string
		out     verify.Outcome
	}{
		{"added", footprint.Added, "", nil, verify.Clean},
		{"modified", footprint.Modified, "", []string{"modifies-existing-lock"}, verify.Findings},
		{"renamed", footprint.Renamed, ".intent/old.yml", []string{"modifies-existing-lock"}, verify.Findings},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			in := base(t)
			in.LockFiles[0].Status = tc.status
			in.LockFiles[0].OldPath = tc.oldPath
			r := verify.Run(in)
			got := suspectOf(t, r, 0)
			if len(got) != len(tc.want) || (len(got) > 0 && !reflect.DeepEqual(got, tc.want)) {
				t.Fatalf("suspect = %v, want %v", got, tc.want)
			}
			wantOutcome(t, r, tc.out, map[verify.Outcome]int{verify.Clean: 0, verify.Findings: 1}[tc.out])
		})
	}
}

func TestRun_UnverifiableLock(t *testing.T) {
	zeroCriteria := func(l *lock.Lock) { l.Criteria = nil }
	tests := []struct {
		name string
		file verify.LockFile
		want string // a substring of one problem
	}{
		{"bad yaml", verify.LockFile{Path: ".intent/" + idA + ".yml", Status: footprint.Added, Data: []byte("schema: [unclosed\n")}, "lock:"},
		{"unknown key", verify.LockFile{Path: ".intent/" + idA + ".yml", Status: footprint.Added, Data: append(lockYAML(t, lockSpec{}), []byte("root_intent_text: secret\n")...)}, "root_intent_text"},
		{"validation problems", func() verify.LockFile { f := added(t, lockSpec{tamper: zeroCriteria}); return f }(), "criteria"},
		{"file name differs from id", verify.LockFile{Path: ".intent/other.yml", Status: footprint.Added, Data: lockYAML(t, lockSpec{})}, ".intent/" + idA + ".yml"},
		{"nested file", verify.LockFile{Path: ".intent/sub/" + idA + ".yml", Status: footprint.Added, Data: lockYAML(t, lockSpec{})}, ".intent/" + idA + ".yml"},
		{"empty file", verify.LockFile{Path: ".intent/" + idA + ".yml", Status: footprint.Added}, "empty"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			in := base(t)
			in.LockFiles = []verify.LockFile{tc.file}
			r := verify.Run(in)
			wantOutcome(t, r, verify.Unverifiable, 3)
			if len(r.Locks) != 1 {
				t.Fatalf("locks = %d, want 1", len(r.Locks))
			}
			lr := r.Locks[0]
			if lr.Path != tc.file.Path {
				t.Errorf("path = %q, want %q", lr.Path, tc.file.Path)
			}
			if len(lr.Criteria) != 0 {
				t.Errorf("an unverifiable lock contributes no criteria, got %+v", lr.Criteria)
			}
			found := false
			for _, p := range lr.Problems {
				if strings.Contains(p, tc.want) {
					found = true
				}
			}
			if !found {
				t.Errorf("problems = %q, want one containing %q", lr.Problems, tc.want)
			}
		})
	}

	t.Run("one bad lock does not hide a good one", func(t *testing.T) {
		in := base(t)
		bad := verify.LockFile{Path: ".intent/" + idB + ".yml", Status: footprint.Added, Data: []byte("{")}
		in.LockFiles = []verify.LockFile{bad, added(t, lockSpec{})} // given out of order
		r := verify.Run(in)
		wantOutcome(t, r, verify.Unverifiable, 3)
		if len(r.Locks) != 2 || r.Locks[0].Path != ".intent/"+idA+".yml" || r.Locks[1].Path != bad.Path {
			t.Fatalf("locks must be sorted by path: %+v", r.Locks)
		}
		if len(r.Locks[0].Criteria) != 2 || len(r.Locks[1].Criteria) != 0 {
			t.Errorf("criteria: %+v", r.Locks)
		}
	})
}

func TestRun_UnverifiableConfig(t *testing.T) {
	for name, cfg := range map[string]string{
		"bad yaml":    "version: [",
		"wrong key":   "version: 1\nnope: 1\n",
		"bad version": "version: 2\n",
		"bad skip":    "version: 1\nskip_authors:\n  - \"\"\n",
	} {
		t.Run(name, func(t *testing.T) {
			in := base(t)
			in.Config = []byte(cfg)
			r := verify.Run(in)
			wantOutcome(t, r, verify.Unverifiable, 3)
			if len(r.Problems) == 0 {
				t.Error("want the config problem listed in Problems")
			}
		})
	}
}

// REQ-0034
func TestRun_SkipsBots(t *testing.T) {
	tests := []struct {
		name, author, typ, config string
		skipped                   bool
	}{
		{"type Bot", "some-app", "Bot", "", true},
		{"dependabot login", "dependabot[bot]", "User", "", true},
		{"renovate login", "renovate[bot]", "User", "", true},
		{"skip_authors login", "my-release-bot", "User", "version: 1\nskip_authors:\n  - my-release-bot\n", true},
		{"login not in the list", "my-release-bot", "User", "version: 1\nskip_authors:\n  - someone-else\n", false},
		{"human", "octocat", "User", "", false},
		{"human, no type", "octocat", "", "", false},
		{"Bot type wins over a broken config", "x", "Bot", "version: [", true},
		{"login match is exact", "Dependabot[bot]", "User", "", false},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			in := base(t)
			in.Author, in.AuthorType, in.Config = tc.author, tc.typ, []byte(tc.config)
			in.Changed = append(in.Changed, footprint.ChangedFile{Path: "elsewhere/x.go", Status: footprint.Added})
			r := verify.Run(in)
			if tc.skipped {
				wantOutcome(t, r, verify.Skipped, 0)
				if len(r.Locks) != 0 || len(r.Findings) != 0 || len(r.Problems) != 0 {
					t.Errorf("a skipped run computes nothing else: %+v", r)
				}
				return
			}
			if r.Outcome == verify.Skipped {
				t.Fatalf("not skipped expected, got %+v", r)
			}
			wantOutcome(t, r, verify.Findings, 1) // the extra file is outside every footprint
		})
	}
}

// REQ-0032: no field of Input, at any depth, can carry a title, description or commit message.
func TestInput_HasNoDescriptionFields(t *testing.T) {
	banned := []string{"Title", "Body", "Description", "Commit", "Message"}
	seen := map[reflect.Type]bool{}
	var walk func(typ reflect.Type, path string)
	walk = func(typ reflect.Type, path string) {
		for typ.Kind() == reflect.Pointer || typ.Kind() == reflect.Slice || typ.Kind() == reflect.Array || typ.Kind() == reflect.Map {
			if typ.Kind() == reflect.Map {
				walk(typ.Key(), path)
			}
			typ = typ.Elem()
		}
		if typ.Kind() != reflect.Struct || seen[typ] {
			return
		}
		seen[typ] = true
		for i := 0; i < typ.NumField(); i++ {
			f := typ.Field(i)
			for _, b := range banned {
				if strings.Contains(f.Name, b) {
					t.Errorf("%s.%s: field names containing %q are forbidden", path, f.Name, b)
				}
			}
			walk(f.Type, path+"."+f.Name)
		}
	}
	typ := reflect.TypeOf(verify.Input{})
	walk(typ, "Input")
	if typ.NumField() != 8 {
		t.Errorf("Input has %d fields, want exactly 8 (spec S-07)", typ.NumField())
	}
}

func TestRun_DoesNotMutateInput(t *testing.T) {
	in := base(t)
	in.Changed = modified("z.go", "a.go", "internal/policy/p.go")
	in.LockFiles = []verify.LockFile{{Path: ".intent/" + idB + ".yml", Status: footprint.Added, Data: []byte("{")}, added(t, lockSpec{})}
	before := append([]footprint.ChangedFile(nil), in.Changed...)
	paths := []string{in.LockFiles[0].Path, in.LockFiles[1].Path}
	r := verify.Run(in)
	if r.Outcome != verify.Unverifiable {
		t.Fatalf("outcome = %q, want unverifiable", r.Outcome)
	}
	if !reflect.DeepEqual(in.Changed, before) || in.LockFiles[0].Path != paths[0] || in.LockFiles[1].Path != paths[1] {
		t.Error("Run reordered its input")
	}
}
