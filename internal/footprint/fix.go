package footprint

import (
	"path"
	"strings"
)

// newFinding builds the finding for p, with the nearest criterion and the exact amendment command
// that would bring p into that criterion's footprint (REQ-0030). With no criteria the criterion,
// lock and fix are empty.
func newFinding(p string, status Status, criteria []Criterion) Finding {
	f := Finding{Path: p, Status: status}
	best := nearest(p, criteria)
	if best < 0 {
		return f
	}
	c := criteria[best]
	f.NearestCriterion, f.LockID = c.ID, c.LockID
	f.Fix = "intentgate amend " + c.LockID + " --criterion " + c.ID + " --add " + shellQuote(amendGlob(p))
	return f
}

// nearest returns the index of the criterion whose positive globs share the most leading directory
// segments with p's directory; ties go to the earliest. It returns -1 with no criteria.
func nearest(p string, criteria []Criterion) int {
	if len(criteria) == 0 {
		return -1
	}
	dirSegs := segments(path.Dir(p))
	best, bestScore := 0, -1
	for i, c := range criteria {
		score := 0
		for _, g := range c.Footprint {
			if strings.HasPrefix(g, "!") {
				continue
			}
			if s := sharedSegments(dirSegs, segments(staticDir(g))); s > score {
				score = s
			}
		}
		if score > bestScore {
			best, bestScore = i, score
		}
	}
	return best
}

// staticDir is the glob's static prefix (before the first `*`, `?`, `[` or `{`), cut back to the
// last `/`.
func staticDir(glob string) string {
	prefix := glob
	if i := strings.IndexAny(glob, "*?[{"); i >= 0 {
		prefix = glob[:i]
	}
	if i := strings.LastIndex(prefix, "/"); i >= 0 {
		return prefix[:i]
	}
	return ""
}

func segments(dir string) []string {
	if dir == "" || dir == "." {
		return nil
	}
	return strings.Split(dir, "/")
}

func sharedSegments(a, b []string) int {
	n := 0
	for n < len(a) && n < len(b) && a[n] == b[n] {
		n++
	}
	return n
}

// amendGlob is the glob an amendment would add: the directory of p plus `/**`, or p itself for a
// root-level file, with glob metacharacters escaped.
func amendGlob(p string) string {
	dir := path.Dir(p)
	if dir == "." {
		return escapeGlob(p)
	}
	return escapeGlob(dir) + "/**"
}

func escapeGlob(s string) string {
	var b strings.Builder
	for _, r := range s {
		if strings.ContainsRune(`*?[]{}\`, r) {
			b.WriteByte('\\')
		}
		b.WriteRune(r)
	}
	return b.String()
}

// shellQuote single-quotes s for a POSIX shell.
func shellQuote(s string) string {
	return "'" + strings.ReplaceAll(s, "'", `'\''`) + "'"
}
