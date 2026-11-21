// Package report renders a verify result as JSON, SARIF and Markdown. It is pure: no I/O, clock
// or randomness. Every list is sorted by a stable key, so identical results give identical bytes
// (REQ-0040).
package report

import (
	"bytes"
	"encoding/json"
	"sort"
	"strconv"
	"strings"

	"github.com/autox-my/intent-gate/internal/footprint"
	"github.com/autox-my/intent-gate/internal/policy"
	"github.com/autox-my/intent-gate/internal/verify"
)

// SchemaID is the value of the JSON document's schema key.
const SchemaID = "intentgate.verify/1"

// CommentMarker is the hidden first line of the pull request comment (pr-check.md §5). The Action
// finds its own comment by it.
const CommentMarker = "<!-- intentgate:pr-comment -->"

// document is the intentgate.verify/1 JSON document. The field order is the key order.
type document struct {
	Schema   string              `json:"schema"`
	Outcome  string              `json:"outcome"`
	ExitCode int                 `json:"exit_code"`
	Problems []string            `json:"problems"`
	Locks    []verify.LockResult `json:"locks"`
	Findings []footprint.Finding `json:"findings"`
}

// newDocument copies r into a document with every list non-nil and sorted. Criteria keep the
// lock's own order, which is significant (intent-lock.md §3).
func newDocument(r verify.Result) document {
	d := document{
		Schema:   SchemaID,
		Outcome:  string(r.Outcome),
		ExitCode: r.ExitCode,
		Problems: sortedStrings(r.Problems),
		Locks:    make([]verify.LockResult, 0, len(r.Locks)),
		Findings: sortedFindings(r.Findings),
	}
	for _, l := range r.Locks {
		l.Suspect = sortedStrings(l.Suspect)
		l.Problems = sortedStrings(l.Problems)
		crit := make([]verify.Criterion, 0, len(l.Criteria))
		for _, c := range l.Criteria {
			c.Footprint = append([]string{}, c.Footprint...)
			crit = append(crit, c)
		}
		l.Criteria = crit
		d.Locks = append(d.Locks, l)
	}
	// total order: the path, then the whole lock result (its JSON is the same bytes for equal
	// results), so entries that agree on the path never keep the input order
	keys := make(map[int][]byte, len(d.Locks))
	order := make([]int, len(d.Locks))
	for i := range d.Locks {
		keys[i], order[i] = marshal(d.Locks[i]), i
	}
	sort.SliceStable(order, func(a, b int) bool {
		x, y := d.Locks[order[a]], d.Locks[order[b]]
		if x.Path != y.Path {
			return x.Path < y.Path
		}
		return bytes.Compare(keys[order[a]], keys[order[b]]) < 0
	})
	sorted := make([]verify.LockResult, len(d.Locks))
	for i, o := range order {
		sorted[i] = d.Locks[o]
	}
	d.Locks = sorted
	return d
}

func sortedStrings(in []string) []string {
	out := append([]string{}, in...)
	sort.Strings(out)
	return out
}

func sortedFindings(in []footprint.Finding) []footprint.Finding {
	out := append([]footprint.Finding{}, in...)
	sort.SliceStable(out, func(i, j int) bool {
		a, b := out[i], out[j]
		switch {
		case a.Path != b.Path:
			return a.Path < b.Path
		case a.Status != b.Status:
			return a.Status < b.Status
		case a.NearestCriterion != b.NearestCriterion:
			return a.NearestCriterion < b.NearestCriterion
		case a.LockID != b.LockID:
			return a.LockID < b.LockID
		}
		return a.Fix < b.Fix
	})
	return out
}

// marshal is json.MarshalIndent with two spaces, no HTML escaping and a trailing newline.
func marshal(v any) []byte {
	var buf bytes.Buffer
	enc := json.NewEncoder(&buf)
	enc.SetEscapeHTML(false)
	enc.SetIndent("", "  ")
	if err := enc.Encode(v); err != nil {
		// the values here are strings, ints and slices of them: they always marshal
		panic("report: " + err.Error())
	}
	return buf.Bytes()
}

// JSON renders the intentgate.verify/1 document (core-contracts.md §4).
func JSON(r verify.Result) []byte { return marshal(newDocument(r)) }

// Text renders the content of the JSON document for a terminal. It reads the document back from
// JSON(r), so it is a rendering of it and not a second decision path.
func Text(r verify.Result) string {
	var d document
	if err := json.Unmarshal(JSON(r), &d); err != nil {
		panic("report: " + err.Error())
	}
	var b strings.Builder
	b.WriteString("outcome: " + d.Outcome + " (exit " + strconv.Itoa(d.ExitCode) + ")\n")
	for _, p := range d.Problems {
		b.WriteString("problem: " + p + "\n")
	}
	for _, l := range d.Locks {
		b.WriteString("\nlock " + l.Path)
		if l.ID != "" {
			b.WriteString(" (id " + l.ID + ", revision " + strconv.Itoa(l.Revision) + ")")
		}
		b.WriteString("\n")
		if len(l.Suspect) > 0 {
			b.WriteString("  suspect: " + strings.Join(l.Suspect, ", ") + "\n")
		}
		for _, p := range l.Problems {
			b.WriteString("  problem: " + p + "\n")
		}
		for _, c := range l.Criteria {
			b.WriteString("  " + c.ID + ": " + c.Text + "\n")
			b.WriteString("      footprint: " + strings.Join(c.Footprint, ", ") + "\n")
		}
	}
	if len(d.Findings) > 0 {
		b.WriteString("\noutside every footprint:\n")
		for _, f := range d.Findings {
			b.WriteString("  " + f.Path + " (" + string(f.Status) + ")\n")
			if f.Fix != "" {
				b.WriteString("      nearest: " + f.NearestCriterion + " of lock " + f.LockID + "\n")
				b.WriteString("      fix: " + f.Fix + "\n")
			}
		}
	}
	return b.String()
}

// reasonText explains a suspect reason in a sentence.
func reasonText(l verify.LockResult, reason string) string {
	switch reason {
	case verify.ReasonExpired:
		return "the lock was not valid at the time of this check (it lives 14 days from ratification)"
	case verify.ReasonHashMismatch:
		return "the file was edited outside `intentgate amend`: its content doesn't match its recorded hash"
	case verify.ReasonModifiesLock:
		return "this pull request changes a lock that was already on the base branch"
	case verify.ReasonRetargeted:
		return "the lock was ratified for a different base branch than this pull request's"
	case verify.ReasonRepoWide:
		return "a footprint glob matches every path, so the footprint check cannot flag anything for it: " + repoWideList(l)
	}
	return reason
}

// repoWideList names each criterion and repo-wide glob of a lock, in the lock's order.
func repoWideList(l verify.LockResult) string {
	var parts []string
	for _, c := range l.Criteria {
		for _, g := range c.Footprint {
			if footprint.RepoWide(g) {
				parts = append(parts, code(c.ID)+" "+code(g))
			}
		}
	}
	return strings.Join(parts, ", ")
}

// code renders s as an inline Markdown code span. Line breaks become spaces, the backtick fence is
// one longer than the longest backtick run in s, and a space pads the content when it starts or
// ends with a backtick. Every string that comes from a lock, a path or a config goes through it, so
// none of it can mention users, close the comment marker or start a heading.
func code(s string) string {
	s = strings.NewReplacer("\r\n", " ", "\r", " ", "\n", " ").Replace(s)
	longest, run := 0, 0
	for i := 0; i < len(s); i++ {
		if s[i] == '`' {
			run++
			if run > longest {
				longest = run
			}
		} else {
			run = 0
		}
	}
	fence := strings.Repeat("`", longest+1)
	if strings.HasPrefix(s, "`") || strings.HasSuffix(s, "`") {
		return fence + " " + s + " " + fence
	}
	return fence + s + fence
}

func lockLabel(l verify.LockResult) string {
	s := code(l.Path)
	if l.ID != "" {
		s += " (revision " + strconv.Itoa(l.Revision) + ")"
	}
	return s
}

// CheckSummary renders the check-run summary: the criteria with suspect markers, the findings and,
// for an unverifiable result, the reasons.
func CheckSummary(r verify.Result, d policy.Decision) string {
	doc := newDocument(r)
	var b strings.Builder
	b.WriteString("## " + summaryTitle(doc, d) + "\n")
	if doc.Outcome == string(verify.Skipped) {
		b.WriteString("\nThis pull request was skipped: its author is a bot.\n")
		return b.String()
	}
	if len(doc.Problems) > 0 || hasLockProblems(doc) {
		b.WriteString("\n### Could not verify\n\n")
		for _, p := range doc.Problems {
			b.WriteString("- configuration: " + code(p) + "\n")
		}
		for _, l := range doc.Locks {
			for _, p := range l.Problems {
				b.WriteString("- " + code(l.Path) + ": " + code(p) + "\n")
			}
		}
	}
	for _, l := range doc.Locks {
		if len(l.Criteria) == 0 {
			continue
		}
		b.WriteString("\n### Lock " + lockLabel(l) + "\n\n")
		if len(l.Suspect) > 0 {
			b.WriteString("Suspect (" + strings.Join(l.Suspect, ", ") + "):\n")
			for _, s := range l.Suspect {
				b.WriteString("- " + s + ": " + reasonText(l, s) + "\n")
			}
			b.WriteString("\n")
		}
		for _, c := range l.Criteria {
			marker := ""
			if len(l.Suspect) > 0 {
				marker = " ⚠ suspect"
			}
			b.WriteString("- " + code(c.ID) + marker + ": " + code(c.Text) + "\n")
			b.WriteString("  - footprint: " + codeList(c.Footprint) + "\n")
		}
	}
	writeFindings(&b, doc.Findings)
	return b.String()
}

func summaryTitle(doc document, d policy.Decision) string {
	if d.Title != "" {
		return d.Title
	}
	return "IntentGate: " + doc.Outcome
}

func hasLockProblems(doc document) bool {
	for _, l := range doc.Locks {
		if len(l.Problems) > 0 {
			return true
		}
	}
	return false
}

func codeList(ss []string) string {
	parts := make([]string, len(ss))
	for i, s := range ss {
		parts[i] = code(s)
	}
	return strings.Join(parts, ", ")
}

func writeFindings(b *strings.Builder, fs []footprint.Finding) {
	if len(fs) == 0 {
		return
	}
	b.WriteString("\n### Outside every footprint (" + strconv.Itoa(len(fs)) + ")\n\n")
	for _, f := range fs {
		b.WriteString("- " + code(f.Path) + " (" + string(f.Status) + ")")
		if f.Fix != "" {
			b.WriteString(": nearest criterion " + code(f.NearestCriterion) + " of lock " + code(f.LockID) + ". If the change is intended, run " + code(f.Fix))
		}
		b.WriteString("\n")
	}
}

// Comment renders the pull request comment: the hidden marker, then the suspect locks with their
// reasons, the unverifiable problems and the findings with their fix commands. It returns "" when
// the decision posts no comment (REQ-0033).
func Comment(r verify.Result, d policy.Decision) string {
	if d.Comment == policy.None {
		return ""
	}
	doc := newDocument(r)
	var b strings.Builder
	b.WriteString(CommentMarker + "\n")
	b.WriteString("### IntentGate: " + summaryTitle(doc, d) + "\n")
	if doc.Outcome == string(verify.Clean) {
		return b.String()
	}
	b.WriteString("\nAdvisory only: this check never blocks a merge.\n")
	if len(doc.Problems) > 0 || hasLockProblems(doc) {
		b.WriteString("\n**Could not verify**\n\n")
		for _, p := range doc.Problems {
			b.WriteString("- configuration: " + code(p) + "\n")
		}
		for _, l := range doc.Locks {
			for _, p := range l.Problems {
				b.WriteString("- " + code(l.Path) + ": " + code(p) + "\n")
			}
		}
	}
	var suspect []verify.LockResult
	for _, l := range doc.Locks {
		if len(l.Suspect) > 0 {
			suspect = append(suspect, l)
		}
	}
	if len(suspect) > 0 {
		b.WriteString("\n**Suspect locks** (their criteria are still checked, but trust them less)\n\n")
		for _, l := range suspect {
			b.WriteString("- " + lockLabel(l) + ": " + strings.Join(l.Suspect, ", ") + "\n")
			for _, s := range l.Suspect {
				b.WriteString("  - " + s + ": " + reasonText(l, s) + "\n")
			}
		}
	}
	writeFindings(&b, doc.Findings)
	return b.String()
}
