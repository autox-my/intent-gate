package footprint

import (
	"encoding/json"
	"go/parser"
	"go/token"
	"path/filepath"
	"reflect"
	"strconv"
	"strings"
	"testing"
)

// mustConfig parses a config document or fails the test.
func mustConfig(t *testing.T, doc string) Config {
	t.Helper()
	cfg, err := ParseConfig([]byte(doc))
	if err != nil {
		t.Fatalf("ParseConfig(%q): %v", doc, err)
	}
	return cfg
}

// defaultImplicit is the implicit footprint set with no .intentgate.yml and no .gitattributes.
func defaultImplicit(t *testing.T) Implicit {
	t.Helper()
	return mustImplicit(t, mustConfig(t, ""), "")
}

func mustImplicit(t *testing.T, cfg Config, gitattributes string) Implicit {
	t.Helper()
	imp, err := NewImplicit(cfg, []byte(gitattributes))
	if err != nil {
		t.Fatalf("NewImplicit: %v", err)
	}
	return imp
}

func mod(paths ...string) []ChangedFile {
	cs := make([]ChangedFile, len(paths))
	for i, p := range paths {
		cs[i] = ChangedFile{Path: p, Status: Modified}
	}
	return cs
}

func findingPaths(fs []Finding) []string {
	out := make([]string, len(fs))
	for i, f := range fs {
		out[i] = f.Path
	}
	return out
}

func samePaths(a, b []string) bool {
	if len(a) == 0 && len(b) == 0 {
		return true
	}
	return reflect.DeepEqual(a, b)
}

// reported says whether path alone is reported against the given criteria and implicit footprints.
func reported(t *testing.T, path string, criteria []Criterion, imp Implicit) bool {
	t.Helper()
	return len(Check(mod(path), criteria, imp)) > 0
}

// REQ-0024
func TestCheck_OutsideEveryFootprint(t *testing.T) {
	imp := defaultImplicit(t)
	criteria := []Criterion{
		{LockID: "L1", ID: "C1", Footprint: []string{"internal/report/**", "!internal/report/legacy/**"}},
		{LockID: "L2", ID: "C2", Footprint: []string{"cmd/**/*.go", "docs/*.md"}},
	}

	t.Run("membership", func(t *testing.T) {
		cases := []struct {
			name, path string
			want       bool // reported
		}{
			{"outside every criterion", "a.txt", true},
			{"inside the first criterion", "internal/report/sarif.go", false},
			{"inside the first criterion, nested", "internal/report/deep/er/x.go", false},
			{"inside a criterion of another lock", "cmd/intentgate/main.go", false},
			{"** matches zero directories", "cmd/main.go", false},
			{"negated glob excludes", "internal/report/legacy/old.go", true},
			{"negated glob excludes deeply", "internal/report/legacy/a/b.go", true},
			{"single * does not cross directories", "docs/a/b.md", true},
			{"single * matches within a directory", "docs/a.md", false},
			{"case-sensitive directory", "internal/Report/x.go", true},
			{"case-sensitive file", "DOCS/a.md", true},
			{"case-sensitive extension", "cmd/main.GO", true},
		}
		for _, c := range cases {
			t.Run(c.name, func(t *testing.T) {
				if got := reported(t, c.path, criteria, imp); got != c.want {
					t.Errorf("reported(%q) = %v, want %v", c.path, got, c.want)
				}
			})
		}
	})

	t.Run("negation is per criterion", func(t *testing.T) {
		cs := append([]Criterion{}, criteria...)
		cs = append(cs, Criterion{LockID: "L3", ID: "C3", Footprint: []string{"internal/report/legacy/**"}})
		if reported(t, "internal/report/legacy/old.go", cs, imp) {
			t.Error("a path excluded by one criterion but included by another must not be reported")
		}
	})

	t.Run("sorted by path", func(t *testing.T) {
		got := findingPaths(Check(mod("z.txt", "b.txt", "a/b.txt", "a.txt", "internal/report/x.go"), criteria, imp))
		want := []string{"a.txt", "a/b.txt", "b.txt", "z.txt"}
		if !samePaths(got, want) {
			t.Errorf("paths = %v, want %v", got, want)
		}
	})

	t.Run("finding carries path and status", func(t *testing.T) {
		got := Check([]ChangedFile{{Path: "a.txt", Status: Added}}, criteria, imp)
		if len(got) != 1 || got[0].Path != "a.txt" || got[0].Status != Added {
			t.Errorf("findings = %+v, want one finding for a.txt with status added", got)
		}
	})

	t.Run("no findings is an empty, non-nil list", func(t *testing.T) {
		got := Check(mod("internal/report/x.go"), criteria, imp)
		if got == nil || len(got) != 0 {
			t.Errorf("Check = %#v, want an empty non-nil slice", got)
		}
	})

	t.Run("no criteria reports everything outside the implicit footprints", func(t *testing.T) {
		got := Check(mod("a.txt", "go.sum"), nil, imp)
		if !samePaths(findingPaths(got), []string{"a.txt"}) {
			t.Errorf("findings = %+v, want only a.txt", got)
		}
	})

	t.Run(".intent paths are skipped", func(t *testing.T) {
		got := findingPaths(Check(mod(".intent/01JD8Z6Q.yml", ".intent/sub/x.yml", ".intentx/file", "intent/x"), criteria, imp))
		want := []string{".intentx/file", "intent/x"}
		if !samePaths(got, want) {
			t.Errorf("paths = %v, want %v", got, want)
		}
	})

	t.Run("deletes are checked", func(t *testing.T) {
		got := Check([]ChangedFile{
			{Path: "gone.txt", Status: Deleted},
			{Path: "internal/report/gone.go", Status: Deleted},
		}, criteria, imp)
		if len(got) != 1 || got[0].Path != "gone.txt" || got[0].Status != Deleted {
			t.Errorf("findings = %+v, want one deleted finding for gone.txt", got)
		}
	})

	t.Run("renames", func(t *testing.T) {
		type pair struct{ old, new string }
		cases := []struct {
			name string
			pair pair
			want []string
		}{
			{"both outside", pair{"x/old.txt", "y/new.txt"}, []string{"x/old.txt", "y/new.txt"}},
			{"old outside, new inside", pair{"x/old.txt", "internal/report/new.go"}, []string{"x/old.txt"}},
			{"old inside, new outside", pair{"internal/report/old.go", "y/new.txt"}, []string{"y/new.txt"}},
			{"both inside", pair{"internal/report/old.go", "internal/report/new.go"}, nil},
			{"old under .intent is skipped", pair{".intent/a.yml", "y/new.txt"}, []string{"y/new.txt"}},
			{"new under .intent is skipped", pair{"x/old.txt", ".intent/a.yml"}, []string{"x/old.txt"}},
		}
		for _, c := range cases {
			t.Run(c.name, func(t *testing.T) {
				got := Check([]ChangedFile{{Path: c.pair.new, OldPath: c.pair.old, Status: Renamed}}, criteria, imp)
				if !samePaths(findingPaths(got), c.want) {
					t.Fatalf("paths = %v, want %v", findingPaths(got), c.want)
				}
				for _, f := range got {
					if f.Status != Renamed {
						t.Errorf("finding %q has status %q, want renamed", f.Path, f.Status)
					}
				}
			})
		}
	})
}

// REQ-0025
func TestCheck_PairedTests(t *testing.T) {
	imp := defaultImplicit(t)
	off := mustImplicit(t, mustConfig(t, "version: 1\nimplicit_footprints:\n  test_pairing: false\n"), "")

	paired := []struct{ test, subject string }{
		{"d/x_test.go", "d/x.go"},
		{"x_test.go", "x.go"},
		{"web/src/a.test.ts", "web/src/a.ts"},
		{"web/a.spec.tsx", "web/a.tsx"},
		{"web/a.test.js", "web/a.js"},
		{"web/a.spec.jsx", "web/a.jsx"},
		{"web/a.test.mjs", "web/a.mjs"},
		{"web/a.spec.cjs", "web/a.cjs"},
		{"web/__tests__/a.ts", "web/a.ts"},
		{"web/__tests__/a.js", "web/a.js"},
		{"__tests__/a.mjs", "a.mjs"},
		{"py/test_x.py", "py/x.py"},
		{"py/x_test.py", "py/x.py"},
		{"py/tests/test_x.py", "py/x.py"},
		{"crate/tests/x.rs", "crate/src/x.rs"},
		{"tests/x.rs", "src/x.rs"},
	}
	for _, c := range paired {
		t.Run("paired "+c.test, func(t *testing.T) {
			inside := []Criterion{{LockID: "L", ID: "C1", Footprint: []string{c.subject}}}
			if reported(t, c.test, inside, imp) {
				t.Errorf("%s is paired with %s, which is inside a footprint, but it was reported", c.test, c.subject)
			}
			outside := []Criterion{{LockID: "L", ID: "C1", Footprint: []string{"unrelated/**"}}}
			if !reported(t, c.test, outside, imp) {
				t.Errorf("%s: subject %s is outside every footprint, but it was not reported", c.test, c.subject)
			}
			if !reported(t, c.test, inside, off) {
				t.Errorf("%s: test_pairing is false, but it was not reported", c.test)
			}
			excluded := []Criterion{{LockID: "L", ID: "C1", Footprint: []string{c.subject, "!" + c.subject}}}
			if !reported(t, c.test, excluded, imp) {
				t.Errorf("%s: its subject %s is excluded by a negated glob, but it was not reported", c.test, c.subject)
			}
		})
	}

	notPaired := []struct{ test, subject string }{
		{"d/x_test.go", "d/y.go"},
		{"d/x_test.go", "e/x.go"},
		{"web/a.test.ts", "web/a.js"},
		{"web/a.test.ts", "web/b.ts"},
		{"web/__tests__/sub/a.ts", "web/a.ts"},
		{"py/tests/x.py", "py/x.py"},
		{"crate/tests/x.rs", "crate/x.rs"},
		{"crate/tests/sub/x.rs", "crate/src/x.rs"},
		{"d/x.go", "d/x_test.go"},
	}
	for _, c := range notPaired {
		t.Run("not paired "+c.test+" with "+c.subject, func(t *testing.T) {
			inside := []Criterion{{LockID: "L", ID: "C1", Footprint: []string{c.subject}}}
			if !reported(t, c.test, inside, imp) {
				t.Errorf("%s is not paired with %s but it was not reported", c.test, c.subject)
			}
		})
	}

	t.Run("a subject need not be in the diff", func(t *testing.T) {
		cs := []Criterion{{LockID: "L", ID: "C1", Footprint: []string{"d/**"}}}
		if got := Check(mod("d/x_test.go"), cs, imp); len(got) != 0 {
			t.Errorf("findings = %+v, want none", got)
		}
	})
}

// REQ-0026
func TestImplicit_Fixtures(t *testing.T) {
	imp := defaultImplicit(t)
	inside := []string{
		"testdata/a.json", "pkg/testdata/a.json", "pkg/testdata/deep/er/a.json",
		"fixtures/a.txt", "a/b/fixtures/x.txt",
		"__fixtures__/x.js", "src/__fixtures__/x.js",
		"__snapshots__/a.txt", "src/__snapshots__/a.test.js.snap",
		"a.snap", "pkg/b.snap",
		"x.golden", "pkg/sub/x.golden",
	}
	for _, p := range inside {
		t.Run(p, func(t *testing.T) {
			if reported(t, p, nil, imp) {
				t.Errorf("%s is a fixture or snapshot but it was reported", p)
			}
		})
	}
	outside := []string{"testdata.txt", "pkg/fixture/a.txt", "a.snapshot", "pkg/golden", "mytestdata/a.json"}
	for _, p := range outside {
		t.Run("control "+p, func(t *testing.T) {
			if !reported(t, p, nil, imp) {
				t.Errorf("%s is not a fixture or snapshot but it was not reported", p)
			}
		})
	}
}

// REQ-0027
func TestImplicit_Lockfiles(t *testing.T) {
	imp := defaultImplicit(t)
	names := []string{
		"go.sum", "package-lock.json", "pnpm-lock.yaml", "yarn.lock", "bun.lockb", "Cargo.lock",
		"poetry.lock", "uv.lock", "Pipfile.lock", "Gemfile.lock", "composer.lock",
	}
	for _, n := range names {
		t.Run(n, func(t *testing.T) {
			for _, p := range []string{n, "a/b/" + n} {
				if reported(t, p, nil, imp) {
					t.Errorf("%s is a lockfile but it was reported", p)
				}
			}
		})
	}
	for _, p := range []string{"go.mod", "sub/go.mod", "go.sum.bak", "my-go.sum", "package.json", "lock.json"} {
		t.Run("control "+p, func(t *testing.T) {
			if !reported(t, p, nil, imp) {
				t.Errorf("%s is not a lockfile but it was not reported", p)
			}
		})
	}
}

// REQ-0028
func TestImplicit_GeneratedVendored(t *testing.T) {
	t.Run("default globs", func(t *testing.T) {
		imp := defaultImplicit(t)
		inside := []string{
			"vendor/x/y.go", "third_party/a/b.c", "node_modules/a.js", "web/node_modules/p/i.js",
			"api/x.pb.go", "x.pb.go", "a/b/x_gen.go", "x_gen.go", "a/x.generated.ts", "a.generated.go",
			"dist/a.js", "web/dist/x.js",
		}
		for _, p := range inside {
			if reported(t, p, nil, imp) {
				t.Errorf("%s is generated or vendored but it was reported", p)
			}
		}
		outside := []string{"vendors/x.go", "src/vendor/x.go", "x.pb.gox", "x_gen.gox", "x.generated", "distro/a.js"}
		for _, p := range outside {
			if !reported(t, p, nil, imp) {
				t.Errorf("%s is not generated or vendored but it was not reported", p)
			}
		}
	})

	attrCases := []struct {
		name, attrs, path string
		inside            bool
	}{
		{"generated set", "gen/** linguist-generated", "gen/a.go", true},
		{"generated =true", "gen/** linguist-generated=true", "gen/a.go", true},
		{"vendored set", "ext/** linguist-vendored", "ext/a.go", true},
		{"vendored =true", "ext/** linguist-vendored=true", "ext/a.go", true},
		{"unset with -", "gen/** -linguist-generated", "gen/a.go", false},
		{"unset with =false", "gen/** linguist-generated=false", "gen/a.go", false},
		{"vendored unset with -", "ext/** -linguist-vendored", "ext/a.go", false},
		{"vendored unset with =false", "ext/** linguist-vendored=false", "ext/a.go", false},
		{"unspecified with !", "gen/** linguist-generated\ngen/** !linguist-generated", "gen/a.go", false},
		{"unrelated attributes", "gen/** text eol=lf", "gen/a.go", false},
		{"other value is not a marker", "gen/** linguist-generated=maybe", "gen/a.go", false},
		{"=no unmarks", "gen/** linguist-generated\ngen/** linguist-generated=no", "gen/a.go", false},
		{"=0 unmarks", "gen/** linguist-generated\ngen/** linguist-generated=0", "gen/a.go", false},
		{"=maybe unmarks an earlier set", "gen/** linguist-generated\ngen/** linguist-generated=maybe", "gen/a.go", false},
		{"vendored =no unmarks", "ext/** linguist-vendored\next/** linguist-vendored=no", "ext/a.go", false},
		{"=true after =no marks again", "gen/** linguist-generated=no\ngen/** linguist-generated=true", "gen/a.go", true},
		{"pattern with no attributes", "gen/**", "gen/a.go", false},
		{"unset after set, later line wins", "gen/** linguist-generated\ngen/keep/** -linguist-generated", "gen/keep/a.go", false},
		{"set line still applies elsewhere", "gen/** linguist-generated\ngen/keep/** -linguist-generated", "gen/a.go", true},
		{"set after unset, later line wins", "gen/keep/** -linguist-generated\ngen/** linguist-generated", "gen/keep/a.go", true},
		{"attributes are independent", "gen/** linguist-generated\ngen/** -linguist-vendored", "gen/a.go", true},
		{"vendored survives generated unset", "gen/** linguist-vendored\ngen/** -linguist-generated", "gen/a.go", true},
		{"no slash matches at any depth", "*.pb.txt linguist-generated", "a/b/x.pb.txt", true},
		{"no slash matches at the root", "*.pb.txt linguist-generated", "x.pb.txt", true},
		{"no slash name at any depth", "gen.txt linguist-generated", "a/b/gen.txt", true},
		{"trailing slash matches nothing at any depth", "generated/ linguist-generated", "x/generated/a.go", false},
		{"trailing slash matches nothing at the root", "generated/ linguist-generated", "generated/a.go", false},
		{"trailing slash with a slash inside matches nothing", "src/gen/ linguist-vendored", "src/gen/a.go", false},
		{"trailing slash does not unmark", "gen/** linguist-generated\ngen/ -linguist-generated", "gen/a.go", true},
		{"trailing slash does not match a file", "generated/ linguist-generated", "x/generated", false},
		{"slash in the middle anchors", "docs/gen/*.md linguist-generated", "docs/gen/a.md", true},
		{"anchored does not match deeper prefix", "docs/gen/*.md linguist-generated", "other/docs/gen/a.md", false},
		{"anchored * does not cross directories", "docs/gen/*.md linguist-generated", "docs/gen/sub/a.md", false},
		{"leading slash anchors", "/gen.txt linguist-generated", "gen.txt", true},
		{"leading slash does not match deeper", "/gen.txt linguist-generated", "sub/gen.txt", false},
		{"** in an anchored pattern", "docs/**/gen.md linguist-generated", "docs/a/b/gen.md", true},
		{"macro line is skipped", "[attr]mymacro linguist-generated\nfoo.txt mymacro", "foo.txt", false},
		{"macro does not mark its own name", "[attr]gen linguist-generated", "gen", false},
		{"comment is skipped", "# gen/** linguist-generated", "gen/a.go", false},
		{"blank lines are skipped", "\n\n  \ngen/** linguist-generated\n\n", "gen/a.go", true},
		{"CRLF line endings", "gen/** linguist-generated\r\next/** linguist-vendored\r\n", "ext/a.go", true},
		{"tab separator", "gen/**\tlinguist-generated", "gen/a.go", true},
		{"attribute among others", "gen/** text linguist-generated eol=lf", "gen/a.go", true},
		{"case-sensitive pattern", "gen/** linguist-generated", "Gen/a.go", false},
		{"no trailing newline", "gen/** linguist-generated", "gen/a.go", true},
		{"empty file", "", "gen/a.go", false},
	}
	t.Run("trailing slash dist/ is reported once the dist default is removed", func(t *testing.T) {
		cfg := mustConfig(t, "version: 1\nimplicit_footprints:\n  remove: [\"**/dist/**\"]\n")
		imp := mustImplicit(t, cfg, "dist/ linguist-generated")
		if !reported(t, "dist/bundle.js", nil, imp) {
			t.Error("dist/bundle.js: a trailing-slash pattern matches nothing, but it was not reported")
		}
	})

	for _, c := range attrCases {
		t.Run(c.name, func(t *testing.T) {
			imp := mustImplicit(t, mustConfig(t, ""), c.attrs)
			if got := !reported(t, c.path, nil, imp); got != c.inside {
				t.Errorf("inside(%q) with %q = %v, want %v", c.path, c.attrs, got, c.inside)
			}
		})
	}

	errCases := []struct{ name, attrs string }{
		{"negative pattern", "!gen/** linguist-generated"},
		{"negative pattern with no attributes", "!gen/**"},
		{"negative pattern after valid lines", "gen/** linguist-generated\n!ext/** linguist-vendored"},
		{"unclosed bracket", "[unclosed linguist-generated"},
		{"unclosed brace", "{a linguist-vendored"},
		{"invalid pattern in a path", "a/[b linguist-generated"},
		{"quoted pattern", `"quoted path" linguist-generated`},
		{"invalid pattern after valid lines", "gen/** linguist-generated\n[bad linguist-vendored"},
		{"invalid pattern with no attributes", "[bad"},
	}
	for _, c := range errCases {
		t.Run("error "+c.name, func(t *testing.T) {
			if _, err := NewImplicit(mustConfig(t, ""), []byte(c.attrs)); err == nil {
				t.Errorf("NewImplicit(%q) succeeded, want an error", c.attrs)
			}
		})
	}
}

// REQ-0029
func TestParseConfig(t *testing.T) {
	t.Run("no file yields the defaults", func(t *testing.T) {
		for name, data := range map[string][]byte{"nil": nil, "empty": {}, "blank": []byte("  \n\n")} {
			cfg, err := ParseConfig(data)
			if err != nil {
				t.Errorf("%s: ParseConfig: %v", name, err)
				continue
			}
			if len(cfg.Add) != 0 || len(cfg.Remove) != 0 || cfg.NoTestPairing {
				t.Errorf("%s: cfg = %+v, want no add, no remove, test_pairing on", name, cfg)
			}
		}
	})

	t.Run("version only keeps the defaults", func(t *testing.T) {
		cfg := mustConfig(t, "version: 1\n")
		if len(cfg.Add) != 0 || len(cfg.Remove) != 0 || cfg.NoTestPairing {
			t.Errorf("cfg = %+v, want the defaults", cfg)
		}
	})

	t.Run("full document", func(t *testing.T) {
		cfg := mustConfig(t, `# .intentgate.yml
version: 1
implicit_footprints:
  add:
    - "docs/**"
  remove:
    - "**/dist/**"
  test_pairing: false
`)
		if !reflect.DeepEqual(cfg.Add, []string{"docs/**"}) || !reflect.DeepEqual(cfg.Remove, []string{"**/dist/**"}) || !cfg.NoTestPairing {
			t.Errorf("cfg = %+v", cfg)
		}
	})

	t.Run("test_pairing defaults to true when omitted", func(t *testing.T) {
		cfg := mustConfig(t, "version: 1\nimplicit_footprints:\n  add: [\"docs/**\"]\n")
		if cfg.NoTestPairing {
			t.Error("test_pairing = false, want true")
		}
	})

	t.Run("add extends the defaults", func(t *testing.T) {
		imp := mustImplicit(t, mustConfig(t, "version: 1\nimplicit_footprints:\n  add: [\"docs/**\", \"*.md\"]\n"), "")
		for _, p := range []string{"docs/a.md", "docs/x/y.txt", "README.md"} {
			if reported(t, p, nil, imp) {
				t.Errorf("%s matches an added glob but it was reported", p)
			}
		}
		if reported(t, "go.sum", nil, imp) {
			t.Error("the defaults must still apply after add")
		}
		if !reported(t, "src/a.txt", nil, imp) {
			t.Error("src/a.txt matches nothing but it was not reported")
		}
	})

	t.Run("remove drops a default", func(t *testing.T) {
		imp := mustImplicit(t, mustConfig(t, "version: 1\nimplicit_footprints:\n  remove: [\"**/dist/**\"]\n"), "")
		if !reported(t, "web/dist/x.js", nil, imp) {
			t.Error("dist/ was removed from the defaults but it was not reported")
		}
		if reported(t, "vendor/x.go", nil, imp) {
			t.Error("the other defaults must still apply after a remove")
		}
	})

	t.Run("remove drops a lockfile default", func(t *testing.T) {
		imp := mustImplicit(t, mustConfig(t, "version: 1\nimplicit_footprints:\n  remove: [\"**/go.sum\"]\n"), "")
		if !reported(t, "go.sum", nil, imp) || !reported(t, "a/go.sum", nil, imp) {
			t.Error("go.sum was removed from the defaults but it was not reported")
		}
		if reported(t, "yarn.lock", nil, imp) {
			t.Error("other lockfiles must still apply")
		}
	})

	t.Run("test_pairing false turns pairing off", func(t *testing.T) {
		imp := mustImplicit(t, mustConfig(t, "version: 1\nimplicit_footprints:\n  test_pairing: false\n"), "")
		cs := []Criterion{{LockID: "L", ID: "C1", Footprint: []string{"d/x.go"}}}
		if !reported(t, "d/x_test.go", cs, imp) {
			t.Error("pairing is off but d/x_test.go was not reported")
		}
	})

	t.Run("a zero-value Config means the defaults, test pairing on", func(t *testing.T) {
		cs := []Criterion{{LockID: "L", ID: "C1", Footprint: []string{"d/x.go"}}}
		for name, cfg := range map[string]Config{"empty": {}, "add only": {Add: []string{"docs/**"}}} {
			imp, err := NewImplicit(cfg, nil)
			if err != nil {
				t.Fatalf("%s: NewImplicit: %v", name, err)
			}
			if reported(t, "d/x_test.go", cs, imp) {
				t.Errorf("%s: pairing is on by default but d/x_test.go was reported", name)
			}
		}
	})

	errCases := []struct{ name, doc string }{
		{"remove of a non-default", "version: 1\nimplicit_footprints:\n  remove: [\"src/**\"]\n"},
		{"remove typo", "version: 1\nimplicit_footprints:\n  remove: [\"**/dst/**\"]\n"},
		{"unknown top-level key", "version: 1\nfoo: 1\n"},
		{"unknown nested key", "version: 1\nimplicit_footprints:\n  adds: [\"docs/**\"]\n"},
		{"version 2", "version: 2\n"},
		{"version 0", "version: 0\n"},
		{"version missing", "implicit_footprints:\n  add: [\"docs/**\"]\n"},
		{"version as a string", "version: \"1\"\n"},
		{"invalid add glob", "version: 1\nimplicit_footprints:\n  add: [\"[bad\"]\n"},
		{"negated add glob", "version: 1\nimplicit_footprints:\n  add: [\"!docs/**\"]\n"},
		{"empty add glob", "version: 1\nimplicit_footprints:\n  add: [\"\"]\n"},
		{"malformed yaml", "version: [1\n"},
		{"not a mapping", "- a\n- b\n"},
		{"two documents", "version: 1\n---\nversion: 1\n"},
	}
	for _, c := range errCases {
		t.Run("error "+c.name, func(t *testing.T) {
			if cfg, err := ParseConfig([]byte(c.doc)); err == nil {
				t.Errorf("ParseConfig(%q) = %+v, want an error", c.doc, cfg)
			}
		})
	}

	t.Run("NewImplicit rejects a config that was not parsed", func(t *testing.T) {
		if _, err := NewImplicit(Config{Add: []string{"[bad"}}, nil); err == nil {
			t.Error("an invalid add glob in a hand-built Config must be an error")
		}
		if _, err := NewImplicit(Config{Remove: []string{"src/**"}}, nil); err == nil {
			t.Error("a remove of a non-default in a hand-built Config must be an error")
		}
	})
}

// REQ-0030
func TestFinding_FixCommand(t *testing.T) {
	imp := defaultImplicit(t)
	criteria := []Criterion{
		{LockID: "L1", ID: "C1", Footprint: []string{"internal/report/**"}},
		{LockID: "L2", ID: "C2", Footprint: []string{"internal/gitx/*.go", "cmd/**/*.go"}},
		{LockID: "L3", ID: "C3", Footprint: []string{"docs/**", "!docs/private/**"}},
	}

	cases := []struct {
		name, path    string
		criteria      []Criterion
		wantNearest   string
		wantLock      string
		wantFix       string
		wantNoFinding bool
	}{
		{
			name: "nearest criterion by shared directory prefix", path: "internal/gitx/sub/x.go", criteria: criteria,
			wantNearest: "C2", wantLock: "L2", wantFix: `intentgate amend L2 --criterion C2 --add 'internal/gitx/sub/**'`,
		},
		{
			name: "deeper shared prefix wins over a shorter one", path: "cmd/intentgate/sub/main.txt", criteria: criteria,
			wantNearest: "C2", wantLock: "L2", wantFix: `intentgate amend L2 --criterion C2 --add 'cmd/intentgate/sub/**'`,
		},
		{
			name: "tie goes to the earliest criterion", path: "internal/zzz/x.go", criteria: criteria,
			wantNearest: "C1", wantLock: "L1", wantFix: `intentgate amend L1 --criterion C1 --add 'internal/zzz/**'`,
		},
		{
			name: "no shared prefix goes to the earliest criterion", path: "other/x.txt", criteria: criteria,
			wantNearest: "C1", wantLock: "L1", wantFix: `intentgate amend L1 --criterion C1 --add 'other/**'`,
		},
		{
			name: "root-level file uses the path itself", path: "Makefile", criteria: criteria,
			wantNearest: "C1", wantLock: "L1", wantFix: `intentgate amend L1 --criterion C1 --add 'Makefile'`,
		},
		{
			name: "negated globs do not attract", path: "docs/private/x.txt", criteria: criteria,
			wantNearest: "C3", wantLock: "L3", wantFix: `intentgate amend L3 --criterion C3 --add 'docs/private/**'`,
		},
		{
			name: "nearest glob of a criterion is the best of its globs", path: "cmd/x/y.txt",
			criteria: []Criterion{
				{LockID: "LA", ID: "C1", Footprint: []string{"cmd/*.go"}},
				{LockID: "LB", ID: "C2", Footprint: []string{"cmd/x/*.go", "zzz/**"}},
			},
			wantNearest: "C2", wantLock: "LB", wantFix: `intentgate amend LB --criterion C2 --add 'cmd/x/**'`,
		},
		{
			name: "metacharacters in a directory are escaped", path: "weird/a[1]/{x}/f.go", criteria: criteria,
			wantNearest: "C1", wantLock: "L1", wantFix: `intentgate amend L1 --criterion C1 --add 'weird/a\[1\]/\{x\}/**'`,
		},
		{
			name: "metacharacters in a root-level file are escaped", path: "we*ir?d.txt", criteria: criteria,
			wantNearest: "C1", wantLock: "L1", wantFix: `intentgate amend L1 --criterion C1 --add 'we\*ir\?d.txt'`,
		},
		{
			name: "backslash is escaped", path: `a\b/c.go`, criteria: criteria,
			wantNearest: "C1", wantLock: "L1", wantFix: `intentgate amend L1 --criterion C1 --add 'a\\b/**'`,
		},
		{
			name: "single quote is closed, escaped and reopened", path: "it's/dir/f.go", criteria: criteria,
			wantNearest: "C1", wantLock: "L1", wantFix: `intentgate amend L1 --criterion C1 --add 'it'\''s/dir/**'`,
		},
		{
			name: "no criteria means an empty fix", path: "a/b.txt", criteria: nil,
		},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got := Check(mod(c.path), c.criteria, imp)
			if len(got) != 1 {
				t.Fatalf("findings = %+v, want exactly one", got)
			}
			f := got[0]
			if f.Path != c.path || f.NearestCriterion != c.wantNearest || f.LockID != c.wantLock || f.Fix != c.wantFix {
				t.Errorf("finding = %+v\nwant nearest %q lock %q\nfix %q", f, c.wantNearest, c.wantLock, c.wantFix)
			}
		})
	}

	t.Run("a rename finding carries a fix for the path it reports", func(t *testing.T) {
		got := Check([]ChangedFile{{Path: "internal/report/new.go", OldPath: "oldplace/x.go", Status: Renamed}}, criteria, imp)
		if len(got) != 1 || got[0].Path != "oldplace/x.go" ||
			got[0].Fix != `intentgate amend L1 --criterion C1 --add 'oldplace/**'` {
			t.Errorf("findings = %+v", got)
		}
	})

	t.Run("json field names", func(t *testing.T) {
		got := Check(mod("internal/zzz/x.go"), criteria, imp)
		if len(got) != 1 {
			t.Fatalf("findings = %+v, want one", got)
		}
		raw, err := json.Marshal(got[0])
		if err != nil {
			t.Fatal(err)
		}
		var m map[string]any
		if err := json.Unmarshal(raw, &m); err != nil {
			t.Fatal(err)
		}
		want := map[string]any{
			"path": "internal/zzz/x.go", "status": "modified", "nearest_criterion": "C1", "lock_id": "L1",
			"fix": `intentgate amend L1 --criterion C1 --add 'internal/zzz/**'`,
		}
		if !reflect.DeepEqual(m, want) {
			t.Errorf("json = %s, want keys %v", raw, want)
		}
	})
}

// REQ-0031
func TestNoModelCall_Imports(t *testing.T) {
	allowed := map[string]bool{
		// standard library packages the package needs; deliberately no net, net/*, os, os/exec, io/fs, syscall
		"bytes": true, "errors": true, "fmt": true, "io": true, "path": true, "sort": true,
		"strings": true, "unicode/utf8": true,
		"github.com/bmatcuk/doublestar/v4": true,
		"gopkg.in/yaml.v3":                 true,
	}
	files, err := filepath.Glob("*.go")
	if err != nil {
		t.Fatal(err)
	}
	checked := 0
	for _, name := range files {
		if strings.HasSuffix(name, "_test.go") {
			continue
		}
		f, err := parser.ParseFile(token.NewFileSet(), name, nil, parser.ImportsOnly)
		if err != nil {
			t.Fatalf("parse %s: %v", name, err)
		}
		checked++
		for _, imp := range f.Imports {
			path, err := strconv.Unquote(imp.Path.Value)
			if err != nil {
				t.Fatalf("%s: bad import %s", name, imp.Path.Value)
			}
			if !allowed[path] {
				t.Errorf("%s imports %q, which is not in the allowlist", name, path)
			}
		}
	}
	if checked == 0 {
		t.Fatal("no non-test .go files found to check")
	}
}
