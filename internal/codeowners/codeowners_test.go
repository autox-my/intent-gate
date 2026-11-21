package codeowners

import (
	"reflect"
	"testing"
)

func names(os []Owner) []string {
	var out []string
	for _, o := range os {
		out = append(out, o.Name)
	}
	return out
}

// REQ-0045
func TestCodeowners(t *testing.T) {
	t.Run("the file is looked for in .github/, then the root, then docs/", func(t *testing.T) {
		want := []string{".github/CODEOWNERS", "CODEOWNERS", "docs/CODEOWNERS"}
		if !reflect.DeepEqual(Locations, want) {
			t.Errorf("Locations = %v, want %v", Locations, want)
		}
	})

	t.Run("the last matching line wins", func(t *testing.T) {
		f := Parse([]byte("* @a\n*.md @b\n/docs/ @c\n"))
		for path, want := range map[string][]string{
			"main.go":       {"a"},
			"README.md":     {"b"},
			"docs/guide.md": {"c"},
			"docs/run.sh":   {"c"},
		} {
			if got := names(f.Owners(path)); !reflect.DeepEqual(got, want) {
				t.Errorf("Owners(%q) = %v, want %v", path, got, want)
			}
		}
		g := Parse([]byte("/docs/ @c\n* @y\n"))
		if got := names(g.Owners("docs/guide.md")); !reflect.DeepEqual(got, []string{"y"}) {
			t.Errorf("a later * line must win over an earlier directory line, got %v", got)
		}
	})

	t.Run("patterns: *.md, /intents/, intents/**, docs/, docs/*, /README.md", func(t *testing.T) {
		for _, tc := range []struct {
			pattern string
			match   []string
			miss    []string
		}{
			{"*.md", []string{"a.md", "x/y/z.md"}, []string{"a.mdx", "md", "x/a.md.txt"}},
			{"/intents/", []string{"intents/a.md", "intents/sub/a.md"}, []string{"sub/intents/a.md", "intents", "intents.md", "myintents/a.md"}},
			{"intents/**", []string{"intents/a.md", "intents/sub/a.md"}, []string{"other/intents/a.md", "a.md"}},
			{"docs/", []string{"docs/a.md", "x/docs/a.md", "x/y/docs/z/a.md"}, []string{"docs.md", "mydocs/a.md", "docs"}},
			{"docs/*", []string{"docs/a.md"}, []string{"docs/sub/a.md", "x/docs/a.md"}},
			{"/README.md", []string{"README.md"}, []string{"sub/README.md", "README.md.bak"}},
			{"**/gen/*.go", []string{"gen/a.go", "x/y/gen/a.go"}, []string{"gen/sub/a.go", "x/gen.go"}},
			{"/src/?.go", []string{"src/a.go"}, []string{"src/ab.go", "src/sub/a.go"}},
		} {
			f := Parse([]byte(tc.pattern + " @owner\n"))
			for _, p := range tc.match {
				if got := names(f.Owners(p)); !reflect.DeepEqual(got, []string{"owner"}) {
					t.Errorf("pattern %q: Owners(%q) = %v, want [owner]", tc.pattern, p, got)
				}
			}
			for _, p := range tc.miss {
				if got := f.Owners(p); got != nil {
					t.Errorf("pattern %q: Owners(%q) = %v, want none", tc.pattern, p, got)
				}
			}
		}
	})

	t.Run("a line with a pattern and no owners un-owns the path", func(t *testing.T) {
		f := Parse([]byte("* @a\n/intents/\n"))
		if got := f.Owners("intents/x.md"); got != nil {
			t.Errorf("Owners(intents/x.md) = %v, want none", got)
		}
		if got := names(f.Owners("src/x.go")); !reflect.DeepEqual(got, []string{"a"}) {
			t.Errorf("Owners(src/x.go) = %v, want [a]", got)
		}
	})

	t.Run("users, teams and emails are told apart", func(t *testing.T) {
		f := Parse([]byte("* @alice @acme/reviewers dev@example.com\n"))
		want := []Owner{{"alice", User}, {"acme/reviewers", Team}, {"dev@example.com", Email}}
		if got := f.Owners("x"); !reflect.DeepEqual(got, want) {
			t.Errorf("Owners = %v, want %v", got, want)
		}
	})

	t.Run("comments, blank lines and CRLF are ignored", func(t *testing.T) {
		f := Parse([]byte("# * @nobody\r\n\r\n   \r\n* @a # trailing comment\r\n  # indented comment @b\r\n"))
		if got := names(f.Owners("x.go")); !reflect.DeepEqual(got, []string{"a"}) {
			t.Errorf("Owners = %v, want [a]", got)
		}
	})

	t.Run("a line GitHub treats as invalid is skipped: it neither owns nor un-owns", func(t *testing.T) {
		for _, tc := range []struct{ line, path string }{
			{`\docs/a.md @bad`, "docs/a.md"},        // a backslash: no leading one is stripped
			{`docs/[a].md @bad`, "docs/[a].md"},     // a range (here also a literal name)
			{`!docs/a.md @bad`, "!docs/a.md"},       // negation
			{`docs/a.md not-an-owner`, "docs/a.md"}, // an owner that is none of user, team, email
			{`docs/a.md @alice#note`, "docs/a.md"},  // a "#" glued to an owner is not a comment
			{`docs/a.md @good @`, "docs/a.md"},      // one bad owner spoils the line
		} {
			f := Parse([]byte("* @first\n" + tc.line + "\n"))
			if got := names(f.Owners(tc.path)); !reflect.DeepEqual(got, []string{"first"}) {
				t.Errorf("line %q: Owners(%q) = %v, want the earlier line's [first]", tc.line, tc.path, got)
			}
		}
		// an owner-less line is valid (it un-owns), unlike one with a bad owner
		g := Parse([]byte("* @first\n/docs/a.md\n"))
		if got := g.Owners("docs/a.md"); got != nil {
			t.Errorf("an owner-less line must un-own, got %v", got)
		}
		// no leading backslash is stripped: "\#file" is skipped, not a pattern for "#file"
		h := Parse([]byte("\\#file @bad\n"))
		if got := h.Owners("#file"); got != nil {
			t.Errorf("a line starting with a backslash must be skipped, got %v", got)
		}
	})

	t.Run("a directory pattern ending in ** covers only what is inside a directory below that point", func(t *testing.T) {
		for _, tc := range []struct {
			pattern string
			match   []string
			miss    []string
		}{
			{"/**/", []string{"docs/a.md", "a/b/c.md"}, []string{"README.md"}},
			{"**/", []string{"docs/a.md", "a/b/c.md"}, []string{"README.md"}},
			{"a/**/", []string{"a/b/c.md", "a/b/c/d.md"}, []string{"a/c.md", "a", "x/a/b/c.md"}},
		} {
			f := Parse([]byte(tc.pattern + " @owner\n"))
			for _, p := range tc.match {
				if got := names(f.Owners(p)); !reflect.DeepEqual(got, []string{"owner"}) {
					t.Errorf("pattern %q: Owners(%q) = %v, want [owner]", tc.pattern, p, got)
				}
			}
			for _, p := range tc.miss {
				if got := f.Owners(p); got != nil {
					t.Errorf("pattern %q: Owners(%q) = %v, want none", tc.pattern, p, got)
				}
			}
		}
	})
}
