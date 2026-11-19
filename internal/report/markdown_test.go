package report_test

import (
	"strings"
	"testing"

	"github.com/autox-my/intent-gate/internal/footprint"
	"github.com/autox-my/intent-gate/internal/policy"
	"github.com/autox-my/intent-gate/internal/report"
	"github.com/autox-my/intent-gate/internal/verify"
)

// stripCodeSpans removes the inline code spans of s the way CommonMark reads them: a run of n
// backticks opens a span that the next run of exactly n backticks closes. A run with no closing
// run is literal text and stays. It also returns each span's content.
func stripCodeSpans(s string) (rest string, spans []string) {
	var out strings.Builder
	for i := 0; i < len(s); {
		if s[i] != '`' {
			out.WriteByte(s[i])
			i++
			continue
		}
		n := 0
		for i+n < len(s) && s[i+n] == '`' {
			n++
		}
		closeAt := -1
		for j := i + n; j < len(s); {
			if s[j] != '`' {
				j++
				continue
			}
			m := 0
			for j+m < len(s) && s[j+m] == '`' {
				m++
			}
			if m == n {
				closeAt = j
				break
			}
			j += m
		}
		if closeAt < 0 {
			out.WriteString(s[i : i+n])
			i += n
			continue
		}
		spans = append(spans, s[i+n:closeAt])
		i = closeAt + n
	}
	return out.String(), spans
}

const hostileText = "Do it.\n### Outside every footprint (0)\r\n--> <!-- intentgate:pr-comment --> `x` ``y`` ```z``` @everyone"

func hostileResult() verify.Result {
	hostileID := "@evil/team"
	return verify.Result{
		Outcome: verify.Unverifiable, ExitCode: 3,
		Problems: []string{"footprint: bad key `@evil/team`\n### Heading"},
		Locks: []verify.LockResult{
			{Path: ".intent/" + hostileID + ".yml", ID: hostileID, Revision: 1, Suspect: []string{"retargeted"},
				Problems: []string{"lock: \n## Problem `` ` ``\n-->"},
				Criteria: []verify.Criterion{
					{ID: hostileID, Text: hostileText, Footprint: []string{"`a/**`", "!b\n#"}},
					{ID: "`", Text: "``", Footprint: []string{}},
				}},
		},
		Findings: []footprint.Finding{
			{Path: "a`b/@evil.go\n## h", Status: footprint.Modified, NearestCriterion: hostileID, LockID: hostileID,
				Fix: "intentgate amend " + hostileID + " --criterion `x` --add '`'\n### h"},
		},
	}
}

// Review 1 of S-07: no lock- or path-derived text is rendered outside an inline code span, so it
// can't mention users, close the marker or add headings.
func TestMarkdown_LockTextStaysInCodeSpans(t *testing.T) {
	r := hostileResult()
	d := policy.Decide(r)
	for name, md := range map[string]string{"comment": report.Comment(r, d), "check summary": report.CheckSummary(r, d)} {
		t.Run(name, func(t *testing.T) {
			rest, spans := stripCodeSpans(md)
			rest = strings.TrimPrefix(rest, report.CommentMarker+"\n") // the one marker this report writes
			for _, leak := range []string{"@evil", "@everyone", "evil", "Outside every footprint (0)", "-->", "Do it.", "Heading", "## Problem", "a/**", "amend", "Problem"} {
				if strings.Contains(rest, leak) {
					t.Errorf("%q is rendered outside a code span:\n%s\n--- outside every span:\n%s", leak, md, rest)
				}
			}
			for _, sp := range spans {
				if strings.ContainsAny(sp, "\r\n") {
					t.Errorf("a code span holds a line break %q", sp)
				}
			}
			// the only headings are the ones the report itself writes
			for _, line := range strings.Split(md, "\n") {
				if strings.HasPrefix(line, "#") && !strings.HasPrefix(line, "## ") && !strings.HasPrefix(line, "### ") {
					t.Errorf("odd heading line %q", line)
				}
			}
			if n := strings.Count(rest, "### "); n > 3 {
				t.Errorf("%d heading lines outside code spans, want the report's own (at most 3):\n%s", n, rest)
			}
		})
	}

	c := report.Comment(r, d)
	if !strings.HasPrefix(c, report.CommentMarker+"\n") || strings.Count(c, report.CommentMarker) != 1 {
		t.Errorf("the marker must be first, intact and appear once:\n%s", c)
	}
	// the hostile strings are all still there, in spans
	_, spans := stripCodeSpans(c + "\n" + report.CheckSummary(r, d)) // criterion text is in the summary
	joined := strings.Join(spans, "\n")
	for _, want := range []string{"@evil/team", "Outside every footprint (0)", "-->", "@everyone"} {
		if !strings.Contains(joined, want) {
			t.Errorf("code spans lack %q:\n%s", want, c)
		}
	}
}

func TestMarkdown_CodeFenceIsLongerThanAnyBacktickRun(t *testing.T) {
	r := verify.Result{Outcome: verify.Findings, ExitCode: 1, Findings: []footprint.Finding{
		{Path: "a/b.go", Status: footprint.Added, NearestCriterion: "C1", LockID: lockID, Fix: "run ``x`` and ```y``` and `z`"},
	}}
	_, spans := stripCodeSpans(report.Comment(r, policy.Decide(r)))
	found := false
	for _, sp := range spans {
		if strings.Contains(sp, "run ``x`` and ```y``` and `z`") {
			found = true
		}
	}
	if !found {
		t.Errorf("the fix command is not one intact code span:\n%s", report.Comment(r, policy.Decide(r)))
	}
}
