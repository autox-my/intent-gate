package footprint

import (
	"path"
	"strings"
)

var jsExtensions = map[string]bool{"ts": true, "tsx": true, "js": true, "jsx": true, "mjs": true, "cjs": true}

// subjectsOfTest returns the subject paths a test file is paired with (design §3). A path that is
// not a recognised test file has no subjects. The subject need not exist or be in the diff.
func subjectsOfTest(p string) []string {
	dir, base := path.Split(p) // dir is "" or ends in "/"
	dir = strings.TrimSuffix(dir, "/")
	var out []string
	add := func(d, name string) {
		if d == "" {
			out = append(out, name)
		} else {
			out = append(out, d+"/"+name)
		}
	}
	parent := func(d string) (string, bool) { // d is ".../<last>"; returns the part before <last>
		last := path.Base(d)
		if d == "" {
			return "", false
		}
		if d == last {
			return "", true
		}
		return strings.TrimSuffix(d, "/"+last), true
	}

	// Go: d/x_test.go <-> d/x.go
	if stem, ok := strings.CutSuffix(base, "_test.go"); ok && stem != "" {
		add(dir, stem+".go")
	}

	// JS/TS: d/x.test.E, d/x.spec.E, d/__tests__/x.E <-> d/x.E (same extension on both sides)
	if i := strings.LastIndex(base, "."); i > 0 && jsExtensions[base[i+1:]] {
		ext, name := base[i:], base[:i]
		for _, marker := range []string{".test", ".spec"} {
			if stem, ok := strings.CutSuffix(name, marker); ok && stem != "" {
				add(dir, stem+ext)
			}
		}
		if path.Base(dir) == "__tests__" && dir != "" {
			if up, ok := parent(dir); ok {
				add(up, base)
			}
		}
	}

	// Python: d/test_x.py, d/x_test.py, d/tests/test_x.py <-> d/x.py
	if name, ok := strings.CutSuffix(base, ".py"); ok {
		var stems []string
		if stem, ok := strings.CutPrefix(name, "test_"); ok && stem != "" {
			stems = append(stems, stem)
		}
		if stem, ok := strings.CutSuffix(name, "_test"); ok && stem != "" {
			stems = append(stems, stem)
		}
		for _, stem := range stems {
			add(dir, stem+".py")
		}
		if len(stems) > 0 && dir != "" && path.Base(dir) == "tests" && strings.HasPrefix(name, "test_") {
			if up, ok := parent(dir); ok {
				add(up, strings.TrimPrefix(name, "test_")+".py")
			}
		}
	}

	// Rust: R/tests/x.rs <-> R/src/x.rs
	if name, ok := strings.CutSuffix(base, ".rs"); ok && name != "" && dir != "" && path.Base(dir) == "tests" {
		if up, ok := parent(dir); ok {
			add(up, "src/"+base)
		}
	}
	return out
}
