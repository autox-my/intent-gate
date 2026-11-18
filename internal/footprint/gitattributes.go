package footprint

import (
	"bytes"
	"fmt"
	"strings"

	"github.com/bmatcuk/doublestar/v4"
)

// tri-state of one attribute on one .gitattributes line.
type attrState int8

const (
	attrUntouched attrState = iota
	attrSet
	attrUnset
)

// attrRule is one .gitattributes line that mentions linguist-generated or linguist-vendored.
type attrRule struct {
	glob              string // root-anchored glob
	dirOnly           bool   // pattern had a trailing slash: matches what is under the directory
	generated, vendor attrState
}

func (r attrRule) matches(path string) bool {
	if r.dirOnly {
		return matches(r.glob+"/**/*", path)
	}
	return matches(r.glob, path)
}

// parseGitattributes reads the rules that matter to the footprint check. Blank lines, `#` comments
// and `[attr]` macro lines are skipped. A pattern that is not a valid glob, or a quoted pattern, is
// an error: the file cannot be verified, so it is never silently skipped.
func parseGitattributes(data []byte) ([]attrRule, error) {
	var rules []attrRule
	for i, line := range strings.Split(string(bytes.ReplaceAll(data, []byte("\r\n"), []byte("\n"))), "\n") {
		line = strings.TrimSpace(line)
		if line == "" || strings.HasPrefix(line, "#") || strings.HasPrefix(line, "[attr]") {
			continue
		}
		fields := strings.Fields(line)
		pattern := fields[0]
		if strings.HasPrefix(pattern, `"`) {
			return nil, fmt.Errorf("footprint: .gitattributes line %d: quoted patterns are not supported", i+1)
		}
		rule, err := newAttrRule(pattern)
		if err != nil {
			return nil, fmt.Errorf("footprint: .gitattributes line %d: %w", i+1, err)
		}
		for _, a := range fields[1:] {
			switch a {
			case "linguist-generated", "linguist-generated=true":
				rule.generated = attrSet
			case "-linguist-generated", "!linguist-generated", "linguist-generated=false":
				rule.generated = attrUnset
			case "linguist-vendored", "linguist-vendored=true":
				rule.vendor = attrSet
			case "-linguist-vendored", "!linguist-vendored", "linguist-vendored=false":
				rule.vendor = attrUnset
			}
		}
		if rule.generated != attrUntouched || rule.vendor != attrUntouched {
			rules = append(rules, rule)
		}
	}
	return rules, nil
}

// newAttrRule turns a .gitattributes pattern into a root-anchored glob. A pattern with no `/`
// except a trailing one matches at any depth; otherwise it is anchored (a leading `/` is stripped).
func newAttrRule(pattern string) (attrRule, error) {
	if !doublestar.ValidatePattern(pattern) {
		return attrRule{}, fmt.Errorf("invalid pattern %q", pattern)
	}
	r := attrRule{}
	p := pattern
	if strings.HasSuffix(p, "/") {
		r.dirOnly = true
		p = strings.TrimSuffix(p, "/")
	}
	if p == "" || p == "/" {
		return attrRule{}, fmt.Errorf("invalid pattern %q", pattern)
	}
	if strings.Contains(p, "/") {
		p = strings.TrimPrefix(p, "/")
	} else {
		p = "**/" + p
	}
	if !doublestar.ValidatePattern(p) {
		return attrRule{}, fmt.Errorf("invalid pattern %q", pattern)
	}
	r.glob = p
	return r, nil
}

// attrMarked applies git's rule (the last matching line wins, per attribute) and reports whether
// path ends up marked linguist-generated or linguist-vendored.
func attrMarked(rules []attrRule, path string) bool {
	var generated, vendored attrState
	for _, r := range rules {
		if !r.matches(path) {
			continue
		}
		if r.generated != attrUntouched {
			generated = r.generated
		}
		if r.vendor != attrUntouched {
			vendored = r.vendor
		}
	}
	return generated == attrSet || vendored == attrSet
}
