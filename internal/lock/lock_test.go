package lock

import (
	"bytes"
	"encoding/json"
	"fmt"
	"os"
	"reflect"
	"regexp"
	"strings"
	"testing"
	"time"
)

// goldenHash was recomputed outside Go for S-03 (redactions: 0 added to valid.yml): a Python script
// builds the same document as a dict, serialises it with json.dumps(sort_keys=True, separators=(",", ":"),
// ensure_ascii=False) (RFC 8785 for an ASCII, integer-only document) and takes SHA-256. The same script
// without redactions reproduces the S-01 golden, sha256:799b9e8f…028fff.
const goldenHash = "sha256:cb26e52bf91c8f6a41db7bbf1e170e73aee2795e16fc80c67aa0bb0d5b2927fb"

func validLock() Lock {
	return Lock{
		Schema:     "intentgate.lock/1",
		ID:         "01JD8Z6Q4M7R2T9X3B5N8K1C6F",
		Revision:   1,
		BaseRef:    "main",
		RootIntent: RootIntent{SHA256: strings.Repeat("ab", 32), Source: "claude-code-session", Redactions: intp(0)},
		Criteria: []Criterion{{
			ID: "C1", Text: "Footprint findings are published as SARIF.",
			Footprint: []string{"internal/report/**", "!internal/report/legacy/**"},
		}},
		Ratification: Ratification{Ratifier: "github:octocat", RatifiedAt: "2026-11-18T09:14:03Z", Method: "hook", ReviewDurationMS: intp(48210)},
		Amendments:   []Amendment{},
	}
}

func intp(n int) *int { return &n }

func criteria(n int) []Criterion {
	cs := make([]Criterion, n)
	for i := range cs {
		cs[i] = Criterion{ID: fmt.Sprintf("C%d", i+1), Text: fmt.Sprintf("criterion %d", i+1), Footprint: []string{"src/**"}}
	}
	return cs
}

func readFixture(t *testing.T, name string) []byte {
	t.Helper()
	b, err := os.ReadFile("testdata/" + name)
	if err != nil {
		t.Fatal(err)
	}
	return b
}

func hasProblem(ps []Problem, path string) bool {
	for _, p := range ps {
		if p.Path == path || strings.HasPrefix(p.Path, path+".") || strings.HasPrefix(p.Path, path+"[") {
			return true
		}
	}
	return false
}

func TestParse_ValidFixture(t *testing.T) {
	l, err := Parse(readFixture(t, "valid.yml"))
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	if l.ID != "01JD8Z6Q4M7R2T9X3B5N8K1C6F" || len(l.Criteria) != 2 || l.Ratification.Method != "hook" {
		t.Fatalf("parsed lock is wrong: %+v", l)
	}
	if ps := Validate(l); len(ps) != 0 {
		t.Fatalf("valid fixture has problems: %v", ps)
	}
}

func TestValidate_Baseline(t *testing.T) {
	if ps := Validate(validLock()); len(ps) != 0 {
		t.Fatalf("a valid lock has problems: %v", ps)
	}
}

// REQ-0007
func TestValidate_CriteriaCount(t *testing.T) {
	for _, tc := range []struct {
		n       int
		wantBad bool
	}{{0, true}, {1, false}, {8, false}, {9, true}} {
		t.Run(fmt.Sprintf("%d criteria", tc.n), func(t *testing.T) {
			l := validLock()
			l.Criteria = criteria(tc.n)
			ps := Validate(l)
			if got := hasProblem(ps, "criteria"); got != tc.wantBad {
				t.Fatalf("%d criteria: problem on criteria = %v, want %v (problems: %v)", tc.n, got, tc.wantBad, ps)
			}
			if !tc.wantBad && len(ps) != 0 {
				t.Fatalf("%d criteria should be valid, got %v", tc.n, ps)
			}
		})
	}
}

// REQ-0008
func TestValidate_Footprint(t *testing.T) {
	for _, tc := range []struct {
		name    string
		fp      []string
		wantBad bool
	}{
		{"missing", nil, true},
		{"empty", []string{}, true},
		{"unparseable glob", []string{"src/[a-"}, true},
		{"unparseable negated glob", []string{"!src/[a-"}, true},
		{"empty glob", []string{""}, true},
		{"bare negation", []string{"!"}, true},
		{"plain glob", []string{"src/**"}, false},
		{"negated glob", []string{"src/**", "!src/gen/**"}, false},
		{"only negated glob", []string{"!src/gen/**"}, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			l := validLock()
			l.Criteria[0].Footprint = tc.fp
			ps := Validate(l)
			if got := hasProblem(ps, "criteria[0].footprint"); got != tc.wantBad {
				t.Fatalf("footprint %q: problem = %v, want %v (problems: %v)", tc.fp, got, tc.wantBad, ps)
			}
		})
	}
}

// REQ-0009, REQ-0010, REQ-0011
func TestValidate_Ratification(t *testing.T) {
	for _, tc := range []struct {
		name    string
		mutate  func(r *Ratification)
		field   string
		wantBad bool
	}{
		{"missing ratifier", func(r *Ratification) { r.Ratifier = "" }, "ratification.ratifier", true},
		{"missing ratified_at", func(r *Ratification) { r.RatifiedAt = "" }, "ratification.ratified_at", true},
		{"ratified_at not RFC 3339", func(r *Ratification) { r.RatifiedAt = "18 Nov 2026" }, "ratification.ratified_at", true},
		{"ratified_at with offset", func(r *Ratification) { r.RatifiedAt = "2026-11-18T09:14:03+07:00" }, "ratification.ratified_at", true},
		{"ratified_at UTC", func(r *Ratification) { r.RatifiedAt = "2026-11-18T09:14:03Z" }, "ratification.ratified_at", false},
		{"missing method", func(r *Ratification) { r.Method = "" }, "ratification.method", true},
		{"unknown method", func(r *Ratification) { r.Method = "telepathy" }, "ratification.method", true},
		{"method hook", func(r *Ratification) { r.Method = "hook" }, "ratification.method", false},
		{"method cli-confirm", func(r *Ratification) { r.Method = "cli-confirm" }, "ratification.method", false},
		{"method issue", func(r *Ratification) { r.Method = "issue" }, "ratification.method", false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			l := validLock()
			tc.mutate(&l.Ratification)
			ps := Validate(l)
			if got := hasProblem(ps, tc.field); got != tc.wantBad {
				t.Fatalf("%s: problem = %v, want %v (problems: %v)", tc.name, got, tc.wantBad, ps)
			}
		})
	}
}

func TestValidate_ReportsEverythingSorted(t *testing.T) {
	l := validLock()
	l.ID = "not-a-ulid"
	l.Criteria = nil
	l.Ratification = Ratification{}
	ps := Validate(l)
	for _, path := range []string{"id", "criteria", "ratification.ratifier", "ratification.ratified_at", "ratification.method"} {
		if !hasProblem(ps, path) {
			t.Errorf("no problem reported for %s (problems: %v)", path, ps)
		}
	}
	for i := 1; i < len(ps); i++ {
		if ps[i-1].Path > ps[i].Path || (ps[i-1].Path == ps[i].Path && ps[i-1].Message > ps[i].Message) {
			t.Fatalf("problems are not sorted: %v", ps)
		}
	}
}

func TestValidate_ULID(t *testing.T) {
	for id, wantBad := range map[string]bool{
		"01JD8Z6Q4M7R2T9X3B5N8K1C6F":  false,
		"01JD8Z6Q4M7R2T9X3B5N8K1C6":   true, // 25 chars
		"01JD8Z6Q4M7R2T9X3B5N8K1C6FF": true, // 27 chars
		"01JD8Z6Q4M7R2T9X3B5N8K1C6I":  true, // I is not Crockford base32
		"01jd8z6q4m7r2t9x3b5n8k1c6f":  true, // lower case
		"81JD8Z6Q4M7R2T9X3B5N8K1C6F":  true, // overflows 128 bits
	} {
		l := validLock()
		l.ID = id
		if got := hasProblem(Validate(l), "id"); got != wantBad {
			t.Errorf("id %q: problem = %v, want %v", id, got, wantBad)
		}
	}
}

// parseVariant parses valid.yml after replacing old with new (old must occur in the fixture).
func parseVariant(t *testing.T, old, repl string) Lock {
	t.Helper()
	src := string(readFixture(t, "valid.yml"))
	if !strings.Contains(src, old) {
		t.Fatalf("fixture does not contain %q", old)
	}
	l, err := Parse([]byte(strings.Replace(src, old, repl, 1)))
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	return l
}

// REQ-0012
func TestValidate_ReviewDuration(t *testing.T) {
	const line = "  review_duration_ms: 48210\n"
	for _, tc := range []struct {
		name    string
		repl    string
		wantBad bool
	}{
		{"missing", "", true},
		{"negative", "  review_duration_ms: -5\n", true},
		{"zero", "  review_duration_ms: 0\n", false},
		{"positive", line, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			ps := Validate(parseVariant(t, line, tc.repl))
			if got := hasProblem(ps, "ratification.review_duration_ms"); got != tc.wantBad {
				t.Fatalf("problem = %v, want %v (problems: %v)", got, tc.wantBad, ps)
			}
			if !tc.wantBad && len(ps) != 0 {
				t.Fatalf("lock should be valid, got %v", ps)
			}
		})
	}
}

// REQ-0015: the redaction count is required and at least 0 (S-03).
func TestValidate_Redactions(t *testing.T) {
	const line = "  redactions: 0\n"
	for _, tc := range []struct {
		name    string
		repl    string
		wantBad bool
	}{
		{"missing", "", true},
		{"negative", "  redactions: -1\n", true},
		{"zero", line, false},
		{"positive", "  redactions: 3\n", false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			ps := Validate(parseVariant(t, line, tc.repl))
			if got := hasProblem(ps, "root_intent.redactions"); got != tc.wantBad {
				t.Fatalf("problem = %v, want %v (problems: %v)", got, tc.wantBad, ps)
			}
			if !tc.wantBad && len(ps) != 0 {
				t.Fatalf("lock should be valid, got %v", ps)
			}
		})
	}
}

// REQ-0013: a scalar key that is present (even zero-valued) is content and differs from the key
// absent; an absent list and an empty list hash the same (intent-lock.md §3).
func TestHash_Presence(t *testing.T) {
	mustHash := func(l Lock) string {
		t.Helper()
		h, err := Hash(l)
		if err != nil {
			t.Fatal(err)
		}
		return h
	}
	const dur = "  review_duration_ms: 48210\n"
	const drafted = "  drafted_by: \"claude-code/claude-opus-5-5\"\n"
	t.Run("review_duration_ms 0 differs from absent", func(t *testing.T) {
		if mustHash(parseVariant(t, dur, "  review_duration_ms: 0\n")) == mustHash(parseVariant(t, dur, "")) {
			t.Fatal("review_duration_ms: 0 hashes the same as the key absent")
		}
	})
	t.Run("redactions 0 differs from 1", func(t *testing.T) {
		a, b := validLock(), validLock()
		b.RootIntent.Redactions = intp(1)
		if mustHash(a) == mustHash(b) {
			t.Fatal("the redaction count is not counted in the hash")
		}
	})
	t.Run("drafted_by empty differs from absent", func(t *testing.T) {
		if mustHash(parseVariant(t, drafted, "  drafted_by: \"\"\n")) == mustHash(parseVariant(t, drafted, "")) {
			t.Fatal(`drafted_by: "" hashes the same as the key absent`)
		}
	})
	t.Run("nil and empty lists hash the same", func(t *testing.T) {
		a, b := validLock(), validLock()
		a.Amendments, b.Amendments = nil, []Amendment{}
		a.Criteria[0].Footprint, b.Criteria[0].Footprint = nil, []string{}
		if mustHash(a) != mustHash(b) {
			t.Fatal("nil lists hash differently from empty lists")
		}
		a.Criteria, b.Criteria = nil, []Criterion{}
		if mustHash(a) != mustHash(b) {
			t.Fatal("nil criteria hash differently from empty criteria")
		}
	})
}

// REQ-0013
func TestHash_StableUnderReformatting(t *testing.T) {
	a, err := Parse(readFixture(t, "valid.yml"))
	if err != nil {
		t.Fatal(err)
	}
	b, err := Parse(readFixture(t, "reformatted.yml"))
	if err != nil {
		t.Fatal(err)
	}
	ha, err := Hash(a)
	if err != nil {
		t.Fatal(err)
	}
	hb, err := Hash(b)
	if err != nil {
		t.Fatal(err)
	}
	if ha != goldenHash {
		t.Errorf("hash of valid.yml = %q, want %q", ha, goldenHash)
	}
	if ha != hb {
		t.Errorf("reformatting changed the hash: %q vs %q", ha, hb)
	}
}

// REQ-0013
func TestHash_ChangesWithContent(t *testing.T) {
	base := validLock()
	h0, err := Hash(base)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(h0, "sha256:") || len(h0) != len("sha256:")+64 {
		t.Fatalf("hash %q is not sha256:<64 hex>", h0)
	}
	for _, tc := range []struct {
		name   string
		mutate func(l *Lock)
	}{
		{"root intent hash", func(l *Lock) { l.RootIntent.SHA256 = strings.Repeat("cd", 32) }},
		{"criterion text", func(l *Lock) { l.Criteria[0].Text += " " }},
		{"footprint glob", func(l *Lock) { l.Criteria[0].Footprint[0] = "internal/other/**" }},
		{"ratifier", func(l *Lock) { l.Ratification.Ratifier = "github:someone-else" }},
		{"method", func(l *Lock) { l.Ratification.Method = "issue" }},
	} {
		t.Run(tc.name, func(t *testing.T) {
			l := validLock()
			tc.mutate(&l)
			h, err := Hash(l)
			if err != nil {
				t.Fatal(err)
			}
			if h == h0 {
				t.Fatalf("changing the %s did not change the hash", tc.name)
			}
		})
	}
	t.Run("the hash field itself is not covered", func(t *testing.T) {
		l := validLock()
		l.Hash = "sha256:" + strings.Repeat("0", 64)
		h, err := Hash(l)
		if err != nil {
			t.Fatal(err)
		}
		if h != h0 {
			t.Fatalf("setting the hash field changed the hash: %q vs %q", h, h0)
		}
	})
}

func TestVerifyHash(t *testing.T) {
	l := validLock()
	h, err := Hash(l)
	if err != nil {
		t.Fatal(err)
	}
	l.Hash = h
	if !VerifyHash(l) {
		t.Error("a lock carrying its correct hash does not verify")
	}
	l.Criteria[0].Text = "edited after ratification"
	if VerifyHash(l) {
		t.Error("an edited lock verifies")
	}
	l2 := validLock()
	if VerifyHash(l2) {
		t.Error("a lock with no hash verifies")
	}
}

// REQ-0014
func TestParse_RejectsUnknownKeys(t *testing.T) {
	if _, err := Parse(readFixture(t, "root_intent_text.yml")); err == nil {
		t.Fatal("a lock carrying root_intent.text parsed without error")
	}
	if _, err := Parse([]byte("schema: intentgate.lock/1\nsurprise: 1\n")); err == nil {
		t.Fatal("a lock with an unknown top-level key parsed without error")
	}
	if _, err := Parse(readFixture(t, "valid.yml")); err != nil {
		t.Fatalf("the valid fixture does not parse: %v", err)
	}
}

// REQ-0022
func TestValidAt(t *testing.T) {
	l := validLock()
	ratified := time.Date(2026, 11, 18, 9, 14, 3, 0, time.UTC)
	day := 24 * time.Hour
	for _, tc := range []struct {
		name string
		at   time.Time
		want bool
	}{
		{"at ratification", ratified, true},
		{"after 13 days", ratified.Add(13 * day), true},
		{"exactly 14 days", ratified.Add(14 * day), true},
		{"14 days and 1 second", ratified.Add(14*day + time.Second), false},
		{"1 second before ratification", ratified.Add(-time.Second), false},
		{"a year before ratification", ratified.Add(-365 * day), false},
		{"a year later", ratified.Add(365 * day), false},
		{"14 days in another zone", ratified.Add(14 * day).In(time.FixedZone("x", 7*3600)), true},
	} {
		if got := ValidAt(l, tc.at); got != tc.want {
			t.Errorf("%s: ValidAt = %v, want %v", tc.name, got, tc.want)
		}
	}
	bad := validLock()
	bad.Ratification.RatifiedAt = "garbage"
	if ValidAt(bad, ratified) {
		t.Error("a lock with an unparseable ratified_at is valid")
	}
}

func TestCanonicalJSON(t *testing.T) {
	for _, tc := range []struct {
		name, in, want string
	}{
		{"key order and whitespace", ` { "b" : 1, "a" : [ 1 , 2 ] , "c": { "z": true, "y": null } } `, `{"a":[1,2],"b":1,"c":{"y":null,"z":true}}`},
		{"non-ASCII key order", `{"é":1,"z":2,"a":3,"€":4}`, `{"a":3,"z":2,"é":1,"€":4}`},
		{"UTF-16 code unit order beats code point order", "{\"\U0001F600\":1,\"ﬁ\":2}", "{\"\U0001F600\":1,\"ﬁ\":2}"},
		{"string escapes", `{"s":"a\"b\\c\/d\u0008\u000c\n\r\t\u0001\u001f\u007f é"}`, "{\"s\":\"a\\\"b\\\\c/d\\b\\f\\n\\r\\t\\u0001\\u001f\u007f é\"}"},
		{"no HTML escaping", `{"s":"<a href=\"x\">&</a>"}`, `{"s":"<a href=\"x\">&</a>"}`},
		{"integers", `[0,-5,12345678901]`, `[0,-5,12345678901]`},
		{"empty containers", `{"a":[],"b":{}}`, `{"a":[],"b":{}}`},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got, err := CanonicalJSON([]byte(tc.in))
			if err != nil {
				t.Fatalf("CanonicalJSON(%s): %v", tc.in, err)
			}
			if string(got) != tc.want {
				t.Fatalf("CanonicalJSON(%s)\n got %s\nwant %s", tc.in, got, tc.want)
			}
		})
	}
	for _, in := range []string{"\"\xff\"", "{\"a\":\"\xff\"}", "{\"\xff\":1}", `{"a":1.5}`, `[1.0]`, `[1e3]`, `{"a":-0}`, `{"a":`, `{"a":1,"a":2}`, ``} {
		if _, err := CanonicalJSON([]byte(in)); err == nil {
			t.Errorf("CanonicalJSON(%q) accepted input that must be rejected", in)
		}
	}
}

// The JSON Schema is the public contract; Go is the enforcement. This pins the parts of the schema
// that must agree with Validate (REQ-0012, REQ-0010) and the documented gap (duplicate ids).
func TestSchema_AgreesWithValidation(t *testing.T) {
	raw, err := os.ReadFile("../../schema/intentgate.lock.v1.json")
	if err != nil {
		t.Fatal(err)
	}
	var doc map[string]any
	if err := json.Unmarshal(raw, &doc); err != nil {
		t.Fatal(err)
	}
	props := doc["properties"].(map[string]any)
	rat := props["ratification"].(map[string]any)
	ratProps := rat["properties"].(map[string]any)

	required := map[string]bool{}
	for _, r := range rat["required"].([]any) {
		required[r.(string)] = true
	}
	if !required["review_duration_ms"] {
		t.Error("schema: ratification.required lacks review_duration_ms")
	}
	rd := ratProps["review_duration_ms"].(map[string]any)
	if rd["type"] != "integer" || rd["minimum"] != float64(0) {
		t.Errorf("schema: review_duration_ms = %v, want integer with minimum 0", rd)
	}

	pat, _ := ratProps["ratified_at"].(map[string]any)["pattern"].(string)
	re, err := regexp.Compile(pat)
	if err != nil {
		t.Fatalf("schema: ratified_at pattern %q: %v", pat, err)
	}
	for in, want := range map[string]bool{
		"2026-11-18T09:14:03Z":      true,
		"2026-11-18T09:14:03.5Z":    true,
		"2026-99-99T99:99:99Z":      false,
		"2026-13-01T00:00:00Z":      false,
		"2026-11-32T00:00:00Z":      false,
		"2026-11-18T24:00:00Z":      false,
		"2026-11-18T09:14:03+07:00": false,
	} {
		if got := re.MatchString(in); got != want {
			t.Errorf("schema: ratified_at pattern matches %q = %v, want %v", in, got, want)
		}
	}

	if d, _ := doc["description"].(string); !strings.Contains(d, "duplicate criterion id") {
		t.Error("schema: description does not say duplicate criterion ids are rejected by the Go validator")
	}
}

func TestMarshal_RoundTrip(t *testing.T) {
	drafted := "claude-code/claude-opus-5-5"
	full := validLock()
	full.Criteria = append(full.Criteria, Criterion{ID: "C2", Text: "Quotes \"and\" colons: # and\nnewlines, non-ASCII é 日本", Footprint: []string{"a/**", "!a/b/**"}})
	full.Ratification.DraftedBy = &drafted
	full.RootIntent.Redactions = intp(2)
	h, err := Hash(full)
	if err != nil {
		t.Fatal(err)
	}
	full.Hash = h
	zero := validLock()
	zero.Ratification.ReviewDurationMS = intp(0)
	empty := ""
	zero.Ratification.DraftedBy = &empty
	zero.Hash = h

	fixture, err := Parse(readFixture(t, "valid.yml"))
	if err != nil {
		t.Fatal(err)
	}
	for name, l := range map[string]Lock{"full": full, "zero-valued optionals": zero, "valid.yml": fixture} {
		t.Run(name, func(t *testing.T) {
			out, err := Marshal(l)
			if err != nil {
				t.Fatalf("Marshal: %v", err)
			}
			got, err := Parse(out)
			if err != nil {
				t.Fatalf("Parse(Marshal(l)): %v\n%s", err, out)
			}
			if !reflect.DeepEqual(got, l) {
				t.Fatalf("round trip changed the lock\n got: %+v\nwant: %+v\nyaml:\n%s", got, l, out)
			}
			h0, _ := Hash(l)
			h1, _ := Hash(got)
			if h0 != h1 {
				t.Fatalf("round trip changed the hash: %s vs %s", h0, h1)
			}
			again, err := Marshal(got)
			if err != nil || !bytes.Equal(again, out) {
				t.Fatalf("Marshal is not deterministic or not stable under Parse: %v\n%s\n%s", err, out, again)
			}
		})
	}
}

// intent-lock.md §2: the field order of the committed file is fixed.
func TestMarshal_FieldOrder(t *testing.T) {
	l := validLock()
	drafted := "x"
	l.Ratification.DraftedBy = &drafted
	out, err := Marshal(l)
	if err != nil {
		t.Fatal(err)
	}
	last := -1
	for _, key := range []string{"schema:", "\nid:", "\nrevision:", "\nbase_ref:", "\nroot_intent:", "\n  sha256:", "\n  source:", "\n  redactions:",
		"\ncriteria:", "\nratification:", "\n  ratifier:", "\n  ratified_at:", "\n  method:", "\n  review_duration_ms:", "\n  drafted_by:", "\namendments:", "\nhash:"} {
		i := strings.Index(string(out), key)
		if i < 0 || i < last {
			t.Fatalf("key %q is missing or out of order (at %d, after %d) in:\n%s", key, i, last, out)
		}
		last = i
	}
}

// amended is validLock with two criteria and a revision 3 history: a glob added, then a re-ratify.
func amended() Lock {
	l := validLock()
	l.Criteria = append(l.Criteria, Criterion{ID: "C2", Text: "The report package has golden tests.", Footprint: []string{"internal/report/testdata/**", "docs/api/**"}})
	l.Revision = 3
	l.Amendments = []Amendment{
		{Revision: 2, Kind: "add-glob", Criterion: "C2", Glob: "docs/api/**", By: "git:dev@example.com", At: "2026-11-19T10:00:00Z", Method: "cli-confirm"},
		{Revision: 3, Kind: "reratify", By: "git:dev@example.com", At: "2026-12-05T10:00:00Z", Method: "cli-confirm"},
	}
	return l
}

// REQ-0014: an amendment is typed, so root intent text can't ride in it.
func TestParse_RejectsUnknownAmendmentKeys(t *testing.T) {
	doc := func(extra string) []byte {
		fixture := strings.Replace(string(readFixture(t, "valid.yml")), "amendments: []\n",
			"amendments:\n  - revision: 2\n    kind: reratify\n    by: \"git:dev@example.com\"\n    at: 2026-11-19T10:00:00Z\n    method: cli-confirm\n"+extra, 1)
		if !strings.Contains(fixture, "kind: reratify") {
			t.Fatal("the fixture has no amendments: [] line to replace")
		}
		return []byte(fixture)
	}
	if _, err := Parse(doc("")); err != nil {
		t.Fatalf("a well-formed amendment does not parse: %v", err)
	}
	for _, key := range []string{"root_intent_text", "text", "prompt"} {
		if _, err := Parse(doc("    " + key + ": \"please add SARIF output\"\n")); err == nil {
			t.Errorf("an amendment carrying %s parsed without error", key)
		}
	}
}

func TestValidate_Amendments(t *testing.T) {
	if ps := Validate(amended()); len(ps) != 0 {
		t.Fatalf("the well-formed amended lock has problems: %v", ps)
	}
	mutate := func(f func(*Lock)) Lock {
		l := amended()
		l.Criteria = append([]Criterion(nil), l.Criteria...)
		l.Amendments = append([]Amendment(nil), l.Amendments...)
		f(&l)
		return l
	}
	cases := []struct {
		name string
		l    Lock
		path string // a problem must be reported at or under this path
	}{
		{"revision not 1 + amendments", mutate(func(l *Lock) { l.Revision = 2 }), "revision"},
		{"amendments without a revision bump", mutate(func(l *Lock) { l.Amendments = nil; l.Revision = 3 }), "revision"},
		{"revision bumped without an amendment", mutate(func(l *Lock) { l.Amendments = nil; l.Revision = 2 }), "revision"},
		{"first amendment is revision 3", mutate(func(l *Lock) { l.Amendments[0].Revision = 3 }), "amendments[0].revision"},
		{"revisions out of order", mutate(func(l *Lock) { l.Amendments[0].Revision, l.Amendments[1].Revision = 3, 2 }), "amendments[0].revision"},
		{"revision repeated", mutate(func(l *Lock) { l.Amendments[1].Revision = 2 }), "amendments[1].revision"},
		{"revision below 2", mutate(func(l *Lock) { l.Amendments[0].Revision = 1 }), "amendments[0].revision"},
		{"unknown kind", mutate(func(l *Lock) { l.Amendments[0].Kind = "remove-glob" }), "amendments[0].kind"},
		{"empty kind", mutate(func(l *Lock) { l.Amendments[0].Kind = "" }), "amendments[0].kind"},
		{"add-glob without a criterion", mutate(func(l *Lock) { l.Amendments[0].Criterion = "" }), "amendments[0].criterion"},
		{"add-glob for a criterion that does not exist", mutate(func(l *Lock) { l.Amendments[0].Criterion = "C9" }), "amendments[0].criterion"},
		{"add-glob without a glob", mutate(func(l *Lock) { l.Amendments[0].Glob = "" }), "amendments[0].glob"},
		{"add-glob with a glob that does not parse", mutate(func(l *Lock) {
			l.Amendments[0].Glob = "docs/[api"
			l.Criteria[1].Footprint[1] = "docs/[api"
		}), "amendments[0].glob"},
		{"add-glob with a bare !", mutate(func(l *Lock) { l.Amendments[0].Glob = "!" }), "amendments[0].glob"},
		{"add-glob whose glob is not in the footprint", mutate(func(l *Lock) { l.Amendments[0].Glob = "docs/other/**" }), "amendments[0].glob"},
		{"add-glob whose glob is in another criterion's footprint", mutate(func(l *Lock) { l.Amendments[0].Criterion = "C1" }), "amendments[0].glob"},
		{"reratify with a criterion", mutate(func(l *Lock) { l.Amendments[1].Criterion = "C1" }), "amendments[1].criterion"},
		{"reratify with a glob", mutate(func(l *Lock) { l.Amendments[1].Glob = "docs/api/**" }), "amendments[1].glob"},
		{"empty by", mutate(func(l *Lock) { l.Amendments[0].By = " " }), "amendments[0].by"},
		{"at not UTC", mutate(func(l *Lock) { l.Amendments[0].At = "2026-11-19T17:00:00+07:00" }), "amendments[0].at"},
		{"empty at", mutate(func(l *Lock) { l.Amendments[1].At = "" }), "amendments[1].at"},
		{"unknown method", mutate(func(l *Lock) { l.Amendments[1].Method = "email" }), "amendments[1].method"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if ps := Validate(c.l); !hasProblem(ps, c.path) {
				t.Errorf("want a problem at %s, got %v", c.path, ps)
			}
		})
	}
	t.Run("all three methods are accepted", func(t *testing.T) {
		for _, m := range []string{"hook", "cli-confirm", "issue"} {
			l := mutate(func(l *Lock) { l.Amendments[0].Method = m })
			if ps := Validate(l); len(ps) != 0 {
				t.Errorf("method %s: %v", m, ps)
			}
		}
	})
	t.Run("a negated glob can be added", func(t *testing.T) {
		l := mutate(func(l *Lock) {
			l.Amendments[0].Glob = "!docs/api/private/**"
			l.Criteria[1].Footprint[1] = "!docs/api/private/**"
		})
		if ps := Validate(l); len(ps) != 0 {
			t.Errorf("%v", ps)
		}
	})
}

func TestHash_AmendmentsAreHashed(t *testing.T) {
	a := amended()
	b := amended()
	b.Amendments = append([]Amendment(nil), b.Amendments...)
	b.Amendments[0].Glob = "docs/other/**"
	ha, err1 := Hash(a)
	hb, err2 := Hash(b)
	if err1 != nil || err2 != nil || ha == hb {
		t.Errorf("an amendment's glob does not change the hash: %v %v %s %s", err1, err2, ha, hb)
	}
}

// The schema is the public contract for amendments too.
func TestSchema_Amendments(t *testing.T) {
	raw, err := os.ReadFile("../../schema/intentgate.lock.v1.json")
	if err != nil {
		t.Fatal(err)
	}
	var doc map[string]any
	if err := json.Unmarshal(raw, &doc); err != nil {
		t.Fatal(err)
	}
	am, _ := doc["properties"].(map[string]any)["amendments"].(map[string]any)
	items, _ := am["items"].(map[string]any)
	if items["additionalProperties"] != false {
		t.Errorf("schema: amendments.items.additionalProperties = %v, want false", items["additionalProperties"])
	}
	props, _ := items["properties"].(map[string]any)
	for _, k := range []string{"revision", "kind", "criterion", "glob", "by", "at", "method"} {
		if _, ok := props[k]; !ok {
			t.Errorf("schema: amendments.items.properties lacks %s", k)
		}
	}
	if _, ok := props["root_intent_text"]; ok {
		t.Error("schema: amendments.items.properties has root_intent_text")
	}
	if rev, _ := props["revision"].(map[string]any); rev["minimum"] != float64(2) {
		t.Errorf("schema: amendment revision minimum = %v, want 2", rev["minimum"])
	}
	kind, _ := props["kind"].(map[string]any)
	if got, _ := json.Marshal(kind["enum"]); string(got) != `["add-glob","reratify"]` {
		t.Errorf("schema: amendment kind enum = %s", got)
	}
}
