package ratify

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	"intentgate/internal/lock"
)

const testID = "01JD8Z6Q4M7R2T9X3B5N8K1C6F"

func sha(s string) string { sum := sha256.Sum256([]byte(s)); return hex.EncodeToString(sum[:]) }

func testInput() BuildInput {
	return BuildInput{
		ID:               testID,
		BaseRef:          "main",
		Source:           "claude-code-session",
		RootIntent:       "Add SARIF output to the report package.\n",
		Criteria:         []lock.Criterion{{ID: "C1", Text: "Findings are published as SARIF.", Footprint: []string{"internal/report/**"}}},
		Ratifier:         "git:dev@example.com",
		RatifiedAt:       time.Date(2026, 11, 18, 9, 14, 3, 0, time.UTC),
		Method:           "hook",
		ReviewDurationMS: 48210,
	}
}

func emptyDirs(t *testing.T) (repo, state string) {
	t.Helper()
	return t.TempDir(), t.TempDir()
}

func isEmpty(t *testing.T, dir string) bool {
	t.Helper()
	n := 0
	_ = filepath.WalkDir(dir, func(p string, d os.DirEntry, err error) error {
		if p != dir {
			n++
		}
		return nil
	})
	return n == 0
}

func TestBuild(t *testing.T) {
	t.Run("fields are set as designed", func(t *testing.T) {
		in := testInput()
		in.RootIntent = "masked text é\n"
		in.Redactions = 2
		in.DraftedBy = "claude-code/claude-opus-5-5"
		// 16:14:03.987 at +07:00 is 09:14:03 UTC once truncated to seconds
		in.RatifiedAt = time.Date(2026, 11, 18, 16, 14, 3, 987654321, time.FixedZone("ICT", 7*3600))
		l, err := Build(in)
		if err != nil {
			t.Fatalf("Build: %v", err)
		}
		if l.Schema != lock.SchemaID || l.ID != testID || l.Revision != 1 || l.BaseRef != "main" {
			t.Errorf("header wrong: %+v", l)
		}
		if l.RootIntent.SHA256 != sha("masked text é\n") || l.RootIntent.Source != "claude-code-session" ||
			l.RootIntent.Redactions == nil || *l.RootIntent.Redactions != 2 {
			t.Errorf("root_intent wrong: %+v", l.RootIntent)
		}
		if !reflect.DeepEqual(l.Criteria, in.Criteria) {
			t.Errorf("criteria wrong: %+v", l.Criteria)
		}
		r := l.Ratification
		if r.Ratifier != "git:dev@example.com" || r.RatifiedAt != "2026-11-18T09:14:03Z" || r.Method != "hook" ||
			r.ReviewDurationMS == nil || *r.ReviewDurationMS != 48210 || r.DraftedBy == nil || *r.DraftedBy != "claude-code/claude-opus-5-5" {
			t.Errorf("ratification wrong: %+v", r)
		}
		if l.Amendments == nil || len(l.Amendments) != 0 {
			t.Errorf("amendments should be empty, got %#v", l.Amendments)
		}
		if ps := lock.Validate(l); len(ps) != 0 {
			t.Errorf("Validate: %v", ps)
		}
		if !lock.VerifyHash(l) {
			t.Errorf("hash %q does not verify", l.Hash)
		}
	})
	t.Run("drafted_by is absent when not given, and review_duration_ms 0 is kept", func(t *testing.T) {
		in := testInput()
		in.ReviewDurationMS = 0
		l, err := Build(in)
		if err != nil {
			t.Fatal(err)
		}
		if l.Ratification.DraftedBy != nil || l.Ratification.ReviewDurationMS == nil || *l.Ratification.ReviewDurationMS != 0 {
			t.Fatalf("got %+v", l.Ratification)
		}
		if !lock.VerifyHash(l) {
			t.Fatal("hash does not verify")
		}
	})
	t.Run("the hash is stable", func(t *testing.T) {
		a, errA := Build(testInput())
		b, errB := Build(testInput())
		if errA != nil || errB != nil || a.Hash == "" || a.Hash != b.Hash {
			t.Fatalf("hashes %q %q (%v, %v)", a.Hash, b.Hash, errA, errB)
		}
	})
	for name, mutate := range map[string]func(*BuildInput){
		"zero criteria":    func(in *BuildInput) { in.Criteria = nil },
		"nine criteria":    func(in *BuildInput) { in.Criteria = make([]lock.Criterion, 9) },
		"bad id":           func(in *BuildInput) { in.ID = "not-a-ulid" },
		"bad method":       func(in *BuildInput) { in.Method = "telepathy" },
		"bad source":       func(in *BuildInput) { in.Source = "carrier-pigeon" },
		"no ratifier":      func(in *BuildInput) { in.Ratifier = "" },
		"negative redacts": func(in *BuildInput) { in.Redactions = -1 },
		"negative review":  func(in *BuildInput) { in.ReviewDurationMS = -1 },
		"bad footprint":    func(in *BuildInput) { in.Criteria[0].Footprint = []string{"src/[a-"} },
		"zero time":        func(in *BuildInput) { in.RatifiedAt = time.Time{} },
	} {
		t.Run("invalid input: "+name, func(t *testing.T) {
			in := testInput()
			mutate(&in)
			l, err := Build(in)
			if err == nil {
				t.Fatalf("want an error, got %+v", l)
			}
			if !reflect.DeepEqual(l, lock.Lock{}) {
				t.Fatalf("an error must come with the zero Lock, got %+v", l)
			}
		})
	}
}

// REQ-0015
func TestMaskAndBuild_ScanBeforeWrite(t *testing.T) {
	t.Run("a scanner failure returns nothing and a store stays empty", func(t *testing.T) {
		repo, state := emptyDirs(t)
		boom := errors.New("scanner exploded")
		scan := func(string) (string, int, error) { return "", 0, boom }
		l, masked, err := MaskAndBuild(scan, testInput())
		if !errors.Is(err, boom) {
			t.Fatalf("error = %v, want it to wrap the scanner's", err)
		}
		if !reflect.DeepEqual(l, lock.Lock{}) || masked != "" {
			t.Fatalf("nothing may be returned, got %+v and %q", l, masked)
		}
		if !isEmpty(t, repo) || !isEmpty(t, state) {
			t.Fatal("the store was touched")
		}
	})
	t.Run("the scanner output is what is hashed and returned, never the raw text", func(t *testing.T) {
		scan := func(s string) (string, int, error) { return "MASKED:" + strings.ToUpper(s[:3]), 3, nil }
		in := testInput()
		in.RootIntent = "raw secret text"
		in.Redactions = 99 // MaskAndBuild owns the count
		l, masked, err := MaskAndBuild(scan, in)
		if err != nil {
			t.Fatal(err)
		}
		if masked != "MASKED:RAW" || l.RootIntent.SHA256 != sha("MASKED:RAW") || l.RootIntent.Redactions == nil || *l.RootIntent.Redactions != 3 {
			t.Fatalf("got %q and %+v", masked, l.RootIntent)
		}
	})
	t.Run("with real secrets the stored text and the hashed bytes are the masked text", func(t *testing.T) {
		repo, state := emptyDirs(t)
		in := testInput()
		in.RootIntent = "Ship it. Token " + fakeToken + " goes in CI.\n"
		l, masked, err := MaskAndBuild(nil, in) // nil scanner = Mask
		if err != nil {
			t.Fatal(err)
		}
		const want = "Ship it. Token [REDACTED:github-pat] goes in CI.\n"
		if masked != want {
			t.Fatalf("masked = %q, want %q", masked, want)
		}
		if l.RootIntent.SHA256 != sha(want) || l.RootIntent.Redactions == nil || *l.RootIntent.Redactions != 1 {
			t.Fatalf("root_intent = %+v", l.RootIntent)
		}
		s := Store{RepoRoot: repo, StateDir: state}
		if err := s.Write(l, masked); err != nil {
			t.Fatal(err)
		}
		rec, err := os.ReadFile(filepath.Join(state, "locks", testID, "root-intent.md"))
		if err != nil || string(rec) != want {
			t.Fatalf("root intent record = %q, %v", rec, err)
		}
		committed, err := os.ReadFile(filepath.Join(repo, ".intent", testID+".yml"))
		if err != nil {
			t.Fatal(err)
		}
		if strings.Contains(string(committed), fakeToken) || strings.Contains(string(rec), fakeToken) {
			t.Fatal("the secret reached the store")
		}
		got, err := lock.Parse(committed)
		if err != nil || !reflect.DeepEqual(got, l) || !lock.VerifyHash(got) {
			t.Fatalf("committed lock does not round-trip: %v", err)
		}
	})
}
