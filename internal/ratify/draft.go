// Package ratify is the shared ratification pipeline (docs/design/ratification.md §1): draft the
// criteria from a plan, mask secrets in the root intent, build and hash the lock, and write the lock
// plus the root intent record. Everything is pure except Store, which takes its directories as
// arguments; ids and times are passed in (no clock, no randomness, no home directory in here).
package ratify

import (
	"errors"
	"fmt"
	"io"
	"strings"

	"gopkg.in/yaml.v3"

	"github.com/autox-my/intent-gate/internal/lock"
)

// infoString is the fence info string that marks the criteria block (ratification.md §1.1).
const infoString = "intentgate"

// draftBlock is the YAML inside the block. Unknown keys are errors (KnownFields).
type draftBlock struct {
	Intent   yaml.Node        `yaml:"intent"` // optional: the committed intent file (intent-lock.md §6)
	Criteria []draftCriterion `yaml:"criteria"`
}

// draftCriterion keeps the YAML nodes, not strings: yaml.v3 would turn `text: 1` or `footprint: [1]`
// into "1", and a criterion is only what the author wrote as text.
type draftCriterion struct {
	Text      yaml.Node   `yaml:"text"`
	Footprint []yaml.Node `yaml:"footprint"`
}

// str returns the string a node holds. An absent node is "" (the lock's validation reports it); a
// number, bool, null, list or map is an error.
func str(n yaml.Node) (string, bool) {
	if n.Kind == 0 {
		return "", true
	}
	if n.Kind != yaml.ScalarNode || n.Tag != "!!str" {
		return "", false
	}
	return n.Value, true
}

// Drafted is what a plan's block gives: the criteria (ids C1…Cn) and the committed intent file the
// block's intent: key names, "" when it has none.
type Drafted struct {
	Criteria []lock.Criterion
	Intent   string
}

// ErrIntent marks the error of an intent: value that breaks the path rule (intent-lock.md §6): a
// drafting error that the lock command reports as a usage error. Use errors.Is.
var ErrIntent = errors.New("bad intent: key")

// intentError is an ErrIntent with its message, which starts "no lock: " like every drafting error.
type intentError struct{ msg string }

func (e intentError) Error() string        { return "no lock: " + e.msg }
func (e intentError) Is(target error) bool { return target == ErrIntent }

// DraftCriteria is DraftPlan without the intent: it returns the plan's criteria only.
func DraftCriteria(plan string) ([]lock.Criterion, error) {
	d, err := DraftPlan(plan)
	if err != nil {
		return nil, err
	}
	return d.Criteria, nil
}

// DraftPlan reads the plan's one ```intentgate block and returns its criteria with ids C1…Cn
// (ratification.md §1.1) and its optional top-level intent: key, a committed intent path that must
// be intents/<slug>.md (an error that is errors.Is ErrIntent otherwise). A missing or repeated
// block, invalid YAML, an unknown key, no criteria, more than lock.MaxCriteria criteria, a text or
// footprint item that isn't a string, or an invalid footprint is an error starting "no lock: ";
// nothing is truncated or repaired, and an error comes with a zero Drafted.
func DraftPlan(plan string) (Drafted, error) {
	blocks := fencedBlocks(plan, infoString)
	switch len(blocks) {
	case 0:
		return Drafted{}, fail("the plan has no ```%s block", infoString)
	case 1:
	default:
		return Drafted{}, fail("the plan has %d ```%s blocks, want exactly one", len(blocks), infoString)
	}

	dec := yaml.NewDecoder(strings.NewReader(blocks[0]))
	dec.KnownFields(true)
	var b draftBlock
	if err := dec.Decode(&b); err != nil && !errors.Is(err, io.EOF) {
		return Drafted{}, fail("the %s block is not valid: %v", infoString, err)
	}
	var extra any
	if err := dec.Decode(&extra); !errors.Is(err, io.EOF) {
		return Drafted{}, fail("the %s block has more than one YAML document", infoString)
	}

	var intent string
	if b.Intent.Kind != 0 {
		v, ok := str(b.Intent)
		if !ok || !lock.ValidIntentPath(v) {
			return Drafted{}, intentError{"intent: must be a path of the form intents/<slug>.md, where <slug> is lower-case letters, digits and -"}
		}
		intent = v
	}

	n := len(b.Criteria)
	switch {
	case n == 0:
		return Drafted{}, fail("the %s block has no criteria", infoString)
	case n > lock.MaxCriteria:
		return Drafted{}, fail("the plan has %d criteria and the limit is %d; split the plan", n, lock.MaxCriteria)
	}
	cs := make([]lock.Criterion, n)
	for i, c := range b.Criteria {
		id := fmt.Sprintf("C%d", i+1)
		text, ok := str(c.Text)
		if !ok {
			return Drafted{}, fail("%s: text must be a string", id)
		}
		var fp []string
		for _, item := range c.Footprint {
			g, ok := str(item)
			if !ok || item.Kind == 0 {
				return Drafted{}, fail("%s: every footprint item must be a string", id)
			}
			fp = append(fp, g)
		}
		cs[i] = lock.Criterion{ID: id, Text: text, Footprint: fp}
	}
	// The same rules as the lock's own validation; keep only the findings about the criteria.
	for _, p := range lock.Validate(lock.Lock{Criteria: cs}) {
		if p.Path == "criteria" || strings.HasPrefix(p.Path, "criteria[") {
			return Drafted{}, fail("%s", p)
		}
	}
	return Drafted{Criteria: cs, Intent: intent}, nil
}

func fail(format string, args ...any) error {
	return errors.New("no lock: " + fmt.Sprintf(format, args...))
}

// fencedBlocks returns the bodies of the top-level fenced code blocks whose info string is info.
// A fence is three or more backticks or tildes at the start of a line (up to three spaces of
// indent), closed by a line of at least as many of the same character. A block inside another
// block is just text, and a block left open at the end of the plan is not a block.
func fencedBlocks(md, info string) []string {
	var out []string
	var (
		open  bool
		ch    byte
		width int
		match bool
		body  []string
	)
	for _, line := range strings.Split(strings.ReplaceAll(md, "\r\n", "\n"), "\n") {
		trimmed := strings.TrimLeft(line, " ")
		indented := len(line)-len(trimmed) <= 3
		c, w := fenceRun(trimmed)
		if !open {
			if indented && w >= 3 {
				rest := strings.TrimSpace(trimmed[w:])
				if c == '`' && strings.Contains(rest, "`") {
					continue // a backtick fence's info string can't contain backticks: inline code
				}
				open, ch, width, match, body = true, c, w, rest == info, nil
			}
			continue
		}
		if indented && c == ch && w >= width && strings.TrimSpace(trimmed[w:]) == "" {
			if match {
				out = append(out, strings.Join(body, "\n")+"\n")
			}
			open = false
			continue
		}
		body = append(body, line)
	}
	return out
}

// fenceRun returns the fence character and the length of the run of it at the start of s.
func fenceRun(s string) (byte, int) {
	if s == "" || (s[0] != '`' && s[0] != '~') {
		return 0, 0
	}
	n := 0
	for n < len(s) && s[n] == s[0] {
		n++
	}
	return s[0], n
}
