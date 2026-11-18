package verify_test

import (
	"bytes"
	"encoding/json"
	"flag"
	"os"
	"path/filepath"
	"sort"
	"testing"
	"time"

	"intentgate/internal/footprint"
	"intentgate/internal/lock"
	"intentgate/internal/policy"
	"intentgate/internal/report"
	"intentgate/internal/verify"
)

var update = flag.Bool("update", false, "regenerate the golden files (never in CI)")

const sarifVersion = "0.0.0-golden"

type goldenCase struct {
	name string
	in   func(t *testing.T) verify.Input
}

func goldenCases() []goldenCase {
	return []goldenCase{
		{"clean", func(t *testing.T) verify.Input { return base(t) }},
		{"findings-suspect", func(t *testing.T) verify.Input {
			in := base(t)
			in.BaseBranch = "develop" // retargeted
			in.LockFiles = []verify.LockFile{added(t, lockSpec{})}
			in.Changed = []footprint.ChangedFile{
				{Path: "internal/report/report.go", Status: footprint.Modified},
				{Path: "internal/gitx/diff.go", Status: footprint.Modified},
				{Path: "cmd/new/main.go", Status: footprint.Added},
				{Path: "pkg/b.go", OldPath: "pkg/a.go", Status: footprint.Renamed},
				{Path: "internal/report/testdata/x.golden", Status: footprint.Added},
			}
			return in
		}},
		{"no-lock", func(t *testing.T) verify.Input {
			in := base(t)
			in.LockFiles = nil
			return in
		}},
		{"unverifiable", func(t *testing.T) verify.Input {
			in := base(t)
			in.LockFiles = []verify.LockFile{
				{Path: ".intent/" + idB + ".yml", Status: footprint.Added, Data: lockYAMLNoCriteria(t)},
				added(t, lockSpec{}),
			}
			in.Changed = modified("internal/report/report.go", "docs/readme.md")
			return in
		}},
		{"unverifiable-config", func(t *testing.T) verify.Input {
			in := base(t)
			in.Config = []byte("version: 3\n")
			return in
		}},
		{"skipped", func(t *testing.T) verify.Input {
			in := base(t)
			in.AuthorType = "Bot"
			return in
		}},
	}
}

func lockYAMLNoCriteria(t *testing.T) []byte {
	t.Helper()
	return lockYAML(t, lockSpec{id: idB, tamper: func(l *lock.Lock) { l.Criteria = nil }})
}

// render is every output of a run, keyed by golden file suffix.
func render(in verify.Input) map[string][]byte {
	r := verify.Run(in)
	d := policy.Decide(r)
	return map[string][]byte{
		".json":       report.JSON(r),
		".sarif.json": report.SARIF(r, sarifVersion),
		".summary.md": []byte(report.CheckSummary(r, d)),
		".comment.md": []byte(report.Comment(r, d)),
		".txt":        []byte(report.Text(r)),
	}
}

func suffixes() []string {
	return []string{".json", ".sarif.json", ".summary.md", ".comment.md", ".txt"}
}

// REQ-0040
func TestGolden_Deterministic(t *testing.T) {
	if got := len(goldenCases()); got < 4 {
		t.Fatalf("need at least 4 golden cases, have %d", got)
	}
	for _, tc := range goldenCases() {
		t.Run(tc.name, func(t *testing.T) {
			in := tc.in(t)
			first := render(in)
			for _, sfx := range suffixes() {
				path := filepath.Join("testdata", "golden", tc.name+sfx)
				if *update {
					if err := os.WriteFile(path, first[sfx], 0o644); err != nil {
						t.Fatal(err)
					}
					continue
				}
				want, err := os.ReadFile(path)
				if err != nil {
					t.Fatalf("missing golden file (run `go test ./internal/verify -update`): %v", err)
				}
				if !bytes.Equal(first[sfx], want) {
					t.Errorf("%s differs from its golden file.\n got:\n%s\nwant:\n%s", sfx, first[sfx], want)
				}
			}

			// the same input again, and with the lists reordered, gives the same bytes
			variants := map[string]verify.Input{"again": in, "reversed": reordered(in, true), "rotated": reordered(in, false)}
			names := make([]string, 0, len(variants))
			for n := range variants {
				names = append(names, n)
			}
			sort.Strings(names)
			for _, n := range names {
				got := render(variants[n])
				for _, sfx := range suffixes() {
					if !bytes.Equal(got[sfx], first[sfx]) {
						t.Errorf("%s output for the %s input differs:\n got:\n%s\nwant:\n%s", sfx, n, got[sfx], first[sfx])
					}
				}
			}
		})
	}
}

// reordered returns a copy of in whose Changed and LockFiles are reversed or rotated by one.
func reordered(in verify.Input, reverse bool) verify.Input {
	out := in
	out.Changed = append([]footprint.ChangedFile(nil), in.Changed...)
	out.LockFiles = append([]verify.LockFile(nil), in.LockFiles...)
	if reverse {
		for i, j := 0, len(out.Changed)-1; i < j; i, j = i+1, j-1 {
			out.Changed[i], out.Changed[j] = out.Changed[j], out.Changed[i]
		}
		for i, j := 0, len(out.LockFiles)-1; i < j; i, j = i+1, j-1 {
			out.LockFiles[i], out.LockFiles[j] = out.LockFiles[j], out.LockFiles[i]
		}
		return out
	}
	if n := len(out.Changed); n > 1 {
		out.Changed = append(out.Changed[1:], out.Changed[0])
	}
	if n := len(out.LockFiles); n > 1 {
		out.LockFiles = append(out.LockFiles[1:], out.LockFiles[0])
	}
	return out
}

func TestGolden_NoClockOrHostInOutput(t *testing.T) {
	in := base(t)
	a := report.JSON(verify.Run(in))
	in.EvalTime = in.EvalTime.Add(time.Hour) // still valid: a different clock must not change the bytes
	b := report.JSON(verify.Run(in))
	if !bytes.Equal(a, b) {
		t.Error("the JSON must not contain the evaluation time")
	}
}

// schemaNode is the part of a JSON Schema this test follows.
type schemaNode struct {
	Type       string                `json:"type"`
	Properties map[string]schemaNode `json:"properties"`
	Required   []string              `json:"required"`
	Items      *schemaNode           `json:"items"`
	Additional *bool                 `json:"additionalProperties"`
}

func TestJSONSchema(t *testing.T) {
	raw, err := os.ReadFile(filepath.Join("..", "..", "schema", "intentgate.verify.v1.json"))
	if err != nil {
		t.Fatalf("schema: %v", err)
	}
	var root struct {
		Dialect string `json:"$schema"`
		schemaNode
	}
	if err := json.Unmarshal(raw, &root); err != nil {
		t.Fatal(err)
	}
	if root.Dialect != "https://json-schema.org/draft/2020-12/schema" {
		t.Errorf("$schema = %q, want draft 2020-12", root.Dialect)
	}
	for _, tc := range goldenCases() {
		t.Run(tc.name, func(t *testing.T) {
			var doc any
			if err := json.Unmarshal(report.JSON(verify.Run(tc.in(t))), &doc); err != nil {
				t.Fatalf("JSON output is not JSON: %v", err)
			}
			checkKeys(t, "$", doc, root.schemaNode)
		})
	}
}

// checkKeys fails when an object in doc has keys other than the schema's properties, or when the
// schema doesn't mark every property required (the output always writes every key).
func checkKeys(t *testing.T, path string, doc any, s schemaNode) {
	t.Helper()
	switch v := doc.(type) {
	case map[string]any:
		if s.Type != "object" || s.Additional == nil || *s.Additional {
			t.Errorf("%s: schema must say type object with additionalProperties false", path)
		}
		keys := make([]string, 0, len(v))
		for k := range v {
			keys = append(keys, k)
		}
		sort.Strings(keys)
		want := make([]string, 0, len(s.Properties))
		for k := range s.Properties {
			want = append(want, k)
		}
		sort.Strings(want)
		req := append([]string(nil), s.Required...)
		sort.Strings(req)
		if !equalStrings(keys, want) || !equalStrings(req, want) {
			t.Errorf("%s: output keys %v, schema properties %v, required %v; all three must be equal", path, keys, want, req)
		}
		for _, k := range keys {
			if sub, ok := s.Properties[k]; ok {
				checkKeys(t, path+"."+k, v[k], sub)
			}
		}
	case []any:
		if s.Type != "array" || s.Items == nil {
			t.Errorf("%s: schema must say type array with items", path)
			return
		}
		for i, e := range v {
			checkKeys(t, path+"["+string(rune('0'+i%10))+"]", e, *s.Items)
		}
	}
}

func equalStrings(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}
