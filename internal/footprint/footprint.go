// Package footprint is the pure footprint check: given changed files, criteria and implicit
// footprints it reports each changed file that lies outside all of them. It does no file I/O,
// reads no clock and makes no network or model call (REQ-0031); adapters pass in bytes and values.
package footprint

import (
	"sort"
	"strings"

	"github.com/bmatcuk/doublestar/v4"
)

// Status is how a file changed in the diff.
type Status string

const (
	Added    Status = "added"
	Modified Status = "modified"
	Deleted  Status = "deleted"
	Renamed  Status = "renamed"
)

// ChangedFile is one entry of the diff. OldPath is set only for renamed files.
type ChangedFile struct {
	Path, OldPath string
	Status        Status
}

// Criterion is one criterion of one lock. Footprint holds positive globs and `!` negated globs.
type Criterion struct {
	LockID, ID string
	Footprint  []string
}

// Finding is one changed path outside every footprint.
type Finding struct {
	Path             string `json:"path"`
	Status           Status `json:"status"`
	NearestCriterion string `json:"nearest_criterion"`
	LockID           string `json:"lock_id"`
	Fix              string `json:"fix"`
}

// intentDir is where lock files live; they are checked by the PR check, not here.
const intentDir = ".intent/"

// Check reports each changed file that is inside no criterion's footprint and no implicit
// footprint (REQ-0024). A rename checks both paths; each one outside is its own finding. Findings
// are sorted by path (then status), so the output is deterministic. The result is never nil.
func Check(changed []ChangedFile, criteria []Criterion, implicit Implicit) []Finding {
	findings := []Finding{}
	for _, cf := range changed {
		paths := []string{cf.Path}
		if cf.Status == Renamed && cf.OldPath != "" {
			paths = append(paths, cf.OldPath)
		}
		for _, p := range paths {
			if p == "" || strings.HasPrefix(p, intentDir) {
				continue
			}
			if insideAnyCriterion(p, criteria) || implicit.contains(p, criteria) {
				continue
			}
			findings = append(findings, newFinding(p, cf.Status, criteria))
		}
	}
	sort.SliceStable(findings, func(i, j int) bool {
		if findings[i].Path != findings[j].Path {
			return findings[i].Path < findings[j].Path
		}
		return findings[i].Status < findings[j].Status
	})
	return findings
}

// insideAnyCriterion reports whether path is inside the footprint of at least one criterion.
func insideAnyCriterion(path string, criteria []Criterion) bool {
	for _, c := range criteria {
		if c.contains(path) {
			return true
		}
	}
	return false
}

// contains: a path is inside a criterion's footprint when it matches at least one positive glob
// and none of the `!` globs. A glob that is not a valid pattern matches nothing.
func (c Criterion) contains(path string) bool {
	positive := false
	for _, g := range c.Footprint {
		if neg, ok := strings.CutPrefix(g, "!"); ok {
			if matches(neg, path) {
				return false
			}
			continue
		}
		if !positive && matches(g, path) {
			positive = true
		}
	}
	return positive
}

// matches is doublestar.Match: case-sensitive, `/` separators, `**` crosses directories.
func matches(glob, path string) bool {
	ok, err := doublestar.Match(glob, path)
	return err == nil && ok
}
