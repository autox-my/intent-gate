// Package codeowners parses a CODEOWNERS file and says who owns a path, by GitHub's rules. It is
// pure: no I/O.
package codeowners

import "strings"

// Locations are the places GitHub looks for the file, in the order it uses: the first that exists
// at a revision is the one that counts.
var Locations = []string{".github/CODEOWNERS", "CODEOWNERS", "docs/CODEOWNERS"}

// Kind says what an owner is.
type Kind int

// The kinds of owner.
const (
	User  Kind = iota + 1 // @login
	Team                  // @org/team
	Email                 // an email address
	Other                 // anything GitHub would not accept as an owner
)

// Owner is one owner of a path. Name is the login, the team as org/team (no @) or the email.
type Owner struct {
	Name string
	Kind Kind
}

// File is a parsed CODEOWNERS file.
type File struct{ rules []rule }

type rule struct {
	segs     []string // the pattern's path segments, with "**" kept as a segment of its own
	dirOnly  bool     // the pattern ended with "/": it names a directory, and so what is inside it
	anchored bool     // a leading "/" or a "/" inside the pattern ties it to the root
	owners   []Owner
}

// Parse reads a CODEOWNERS file. A line is a pattern followed by owners; "#" starts a comment
// (at the start of a line, or after whitespace); a blank line is skipped.
func Parse(data []byte) *File {
	f := &File{}
	for _, line := range strings.Split(string(data), "\n") {
		fields := fieldsOf(line)
		if len(fields) == 0 {
			continue
		}
		r := newRule(fields[0])
		for _, o := range fields[1:] {
			r.owners = append(r.owners, classify(o))
		}
		f.rules = append(f.rules, r)
	}
	return f
}

// fieldsOf splits a line on whitespace and stops at the first field that starts a comment. A
// leading backslash lets a pattern start with "#" ("\#file").
func fieldsOf(line string) []string {
	var out []string
	for i, tok := range strings.Fields(line) {
		if strings.HasPrefix(tok, "#") {
			break
		}
		if i == 0 {
			tok = strings.TrimPrefix(tok, `\`)
		}
		out = append(out, tok)
	}
	return out
}

func newRule(pattern string) rule {
	r := rule{dirOnly: strings.HasSuffix(pattern, "/")}
	p := strings.Trim(pattern, "/")
	r.anchored = strings.HasPrefix(pattern, "/") || strings.Contains(p, "/")
	r.segs = strings.Split(p, "/")
	if !r.anchored {
		r.segs = append([]string{"**"}, r.segs...)
	}
	return r
}

func classify(tok string) Owner {
	switch {
	case strings.HasPrefix(tok, "@") && len(tok) > 1:
		name := tok[1:]
		if strings.Contains(name, "/") {
			return Owner{name, Team}
		}
		return Owner{name, User}
	case strings.Contains(tok, "@") && !strings.HasPrefix(tok, "@") && !strings.HasSuffix(tok, "@"):
		return Owner{tok, Email}
	}
	return Owner{tok, Other}
}

// Owners returns the owners of path (slash separated, relative to the repository root): those of
// the last line whose pattern matches. No matching line, and a matching line without owners, both
// give nil.
func (f *File) Owners(path string) []Owner {
	segs := strings.Split(strings.Trim(path, "/"), "/")
	for i := len(f.rules) - 1; i >= 0; i-- {
		if f.rules[i].matches(segs) {
			if len(f.rules[i].owners) == 0 {
				return nil
			}
			return append([]Owner(nil), f.rules[i].owners...)
		}
	}
	return nil
}

func (r rule) matches(path []string) bool {
	last := r.segs[len(r.segs)-1]
	// What may be left of the path when the pattern is used up: a directory pattern, and a plain
	// name (which may be a directory), cover what is inside; a pattern ending in a wildcard
	// segment covers exactly what it matches, and "/**" needs at least one thing inside.
	minRest, anyRest := 0, true
	switch {
	case last == "**":
		minRest = 1
	case r.dirOnly:
		minRest = 1
	case strings.ContainsAny(last, "*?"):
		anyRest = false
	}
	return match(r.segs, path, minRest, anyRest)
}

// match matches the pattern segments against the path segments; "**" takes zero or more.
func match(pat, path []string, minRest int, anyRest bool) bool {
	if len(pat) == 0 {
		if len(path) < minRest {
			return false
		}
		return anyRest || len(path) == 0
	}
	if pat[0] == "**" {
		for skip := 0; skip <= len(path); skip++ {
			if len(pat) == 1 {
				// a trailing ** is the rest of the path: at least minRest segments
				return len(path) >= minRest
			}
			if match(pat[1:], path[skip:], minRest, anyRest) {
				return true
			}
		}
		return false
	}
	if len(path) == 0 || !glob(pat[0], path[0]) {
		return false
	}
	return match(pat[1:], path[1:], minRest, anyRest)
}

// glob matches one path segment against a pattern segment with * (any run) and ? (one character).
func glob(p, s string) bool {
	for len(p) > 0 {
		switch p[0] {
		case '*':
			for k := 0; k <= len(s); k++ {
				if glob(p[1:], s[k:]) {
					return true
				}
			}
			return false
		case '?':
			if len(s) == 0 {
				return false
			}
		default:
			if len(s) == 0 || s[0] != p[0] {
				return false
			}
		}
		p, s = p[1:], s[1:]
	}
	return len(s) == 0
}
