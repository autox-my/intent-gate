// Package lock is IntentGate's pure intent-lock core: parse, validate, hash and validity.
// It does no file I/O, reads no clock, uses no randomness and makes no network calls.
package lock

import (
	"bytes"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"regexp"
	"sort"
	"strings"
	"time"

	"github.com/bmatcuk/doublestar/v4"
	"gopkg.in/yaml.v3"
)

// SchemaID is the value of the lock's schema field.
const SchemaID = "intentgate.lock/1"

// Limits and enums fixed by the design (docs/design/intent-lock.md).
const (
	MaxCriteria = 8                   // REQ-0007
	MaxAge      = 14 * 24 * time.Hour // REQ-0022
)

var (
	methods  = []string{"hook", "cli-confirm", "issue"}                                     // REQ-0011
	sources  = []string{"claude-code-session", "issue", "plan-file", SourceCommittedIntent} //
	ulidRE   = regexp.MustCompile(`^[0-7][0-9A-HJKMNP-TV-Z]{25}$`)                          // 26-char Crockford base32, 128 bits
	sha256RE = regexp.MustCompile(`^[0-9a-f]{64}$`)

	intentPathRE = regexp.MustCompile(`^intents/[a-z0-9][a-z0-9-]*\.md$`) // intent-lock.md §6
	blobRE       = regexp.MustCompile(`^([0-9a-f]{40}|[0-9a-f]{64})$`)    // a git object id, SHA-1 or SHA-256
)

// SourceCommittedIntent is the root_intent.source of a lock whose root intent is a committed
// intent file (ADR 0006, intent-lock.md §6): the lock then records the file's path and blob.
const SourceCommittedIntent = "committed-intent"

// ValidIntentPath reports whether s is a committed intent's path: intents/<slug>.md, <slug> being
// [a-z0-9][a-z0-9-]* (REQ-0043).
func ValidIntentPath(s string) bool { return intentPathRE.MatchString(s) }

// Lock is the committed intent lock (schema intentgate.lock/1). It has no field for root intent
// text, and Parse rejects unknown keys, so the text can't be committed (REQ-0014).
type Lock struct {
	Schema       string       `yaml:"schema" json:"schema"`
	ID           string       `yaml:"id" json:"id"`
	Revision     int          `yaml:"revision" json:"revision"`
	BaseRef      string       `yaml:"base_ref" json:"base_ref"`
	RootIntent   RootIntent   `yaml:"root_intent" json:"root_intent"`
	Criteria     []Criterion  `yaml:"criteria" json:"criteria"`
	Ratification Ratification `yaml:"ratification" json:"ratification"`
	PlanReview   *PlanReview  `yaml:"plan_review,omitempty" json:"plan_review,omitempty"`
	Amendments   []Amendment  `yaml:"amendments" json:"amendments"`
	Hash         string       `yaml:"hash" json:"hash,omitempty"`
}

// RootIntent binds the lock to the root intent by hash only. Redactions is the number of secrets
// masked before hashing (REQ-0015); it is a pointer so that a missing key (a problem) is distinct
// from 0, and the hash tells them apart. Path and Blob are set, and only set, for source
// committed-intent (REQ-0044): the intent file's path and its git blob id at HEAD.
type RootIntent struct {
	SHA256     string `yaml:"sha256" json:"sha256"`
	Source     string `yaml:"source" json:"source"`
	Redactions *int   `yaml:"redactions,omitempty" json:"redactions,omitempty"`
	Path       string `yaml:"path,omitempty" json:"path,omitempty"`
	Blob       string `yaml:"blob,omitempty" json:"blob,omitempty"`
}

// Criterion is one acceptance criterion with its footprint globs.
type Criterion struct {
	ID        string   `yaml:"id" json:"id"`
	Text      string   `yaml:"text" json:"text"`
	Footprint []string `yaml:"footprint" json:"footprint"`
}

// Ratification records who locked the intent, when and how. RatifiedAt is kept as the RFC 3339
// text so that hashing never depends on re-formatting a time. The optional scalars are pointers
// so that a key present with a zero value (0, "") is distinct from the key absent, and the hash
// tells them apart (omitempty only drops a nil pointer).
type Ratification struct {
	Ratifier         string  `yaml:"ratifier" json:"ratifier"`
	RatifiedAt       string  `yaml:"ratified_at" json:"ratified_at"`
	Method           string  `yaml:"method" json:"method"`
	ReviewDurationMS *int    `yaml:"review_duration_ms,omitempty" json:"review_duration_ms,omitempty"`
	DraftedBy        *string `yaml:"drafted_by,omitempty" json:"drafted_by,omitempty"`
}

// PlanReview is the advisory plan review's outcome: the status and the three counts, never any
// finding text, because findings quote the root intent (plan-review.md §4, REQ-0014). The counts
// are pointers so that a missing key (a problem) is distinct from 0.
type PlanReview struct {
	Status     string `yaml:"status" json:"status"`
	Omissions  *int   `yaml:"omissions" json:"omissions"`
	Additions  *int   `yaml:"additions" json:"additions"`
	Weakenings *int   `yaml:"weakenings" json:"weakenings"`
}

// Amendment kinds.
const (
	KindAddGlob  = "add-glob"
	KindReratify = "reratify"
)

var kinds = []string{KindAddGlob, KindReratify}

// Amendment is one entry of amendments: (intent-lock.md §2). It has no field for text, and Parse
// rejects unknown keys, so root intent text can't ride in it (REQ-0014). Criterion and Glob are
// set for add-glob only; At is RFC 3339 in UTC.
type Amendment struct {
	Revision  int    `yaml:"revision" json:"revision"`
	Kind      string `yaml:"kind" json:"kind"`
	Criterion string `yaml:"criterion,omitempty" json:"criterion,omitempty"`
	Glob      string `yaml:"glob,omitempty" json:"glob,omitempty"`
	By        string `yaml:"by" json:"by"`
	At        string `yaml:"at" json:"at"`
	Method    string `yaml:"method" json:"method"`
}

// Problem is one validation finding. Path is a dotted path such as criteria[0].footprint[1].
type Problem struct {
	Path    string
	Message string
}

func (p Problem) String() string { return p.Path + ": " + p.Message }

// Parse reads a lock from YAML. Unknown keys are errors (REQ-0014).
func Parse(data []byte) (Lock, error) {
	dec := yaml.NewDecoder(bytes.NewReader(data))
	dec.KnownFields(true)
	var l Lock
	if err := dec.Decode(&l); err != nil {
		if errors.Is(err, io.EOF) {
			return Lock{}, errors.New("lock: empty document")
		}
		return Lock{}, fmt.Errorf("lock: %w", err)
	}
	var extra any
	if err := dec.Decode(&extra); !errors.Is(err, io.EOF) {
		return Lock{}, errors.New("lock: more than one YAML document")
	}
	return l, nil
}

// Validate returns every problem with the lock, sorted by path then message. It does not check
// the hash (see VerifyHash) or the age (see ValidAt).
func Validate(l Lock) []Problem {
	var ps []Problem
	add := func(path, format string, args ...any) {
		ps = append(ps, Problem{Path: path, Message: fmt.Sprintf(format, args...)})
	}

	if l.Schema != SchemaID {
		add("schema", "must be %q, got %q", SchemaID, l.Schema)
	}
	if !ulidRE.MatchString(l.ID) {
		add("id", "must be a 26-character Crockford base32 ULID, got %q", l.ID)
	}
	if l.Revision < 1 {
		add("revision", "must be at least 1, got %d", l.Revision)
	}
	if l.BaseRef == "" {
		add("base_ref", "is required")
	}
	if !sha256RE.MatchString(l.RootIntent.SHA256) {
		add("root_intent.sha256", "must be 64 lower-case hex characters")
	}
	if !contains(sources, l.RootIntent.Source) {
		add("root_intent.source", "must be one of %s, got %q", strings.Join(sources, ", "), l.RootIntent.Source)
	}

	validateRootIntentFile(l.RootIntent, add) // REQ-0044

	// REQ-0015: recorded by the ratifier, so it must be there
	switch r := l.RootIntent.Redactions; {
	case r == nil:
		add("root_intent.redactions", "is required")
	case *r < 0:
		add("root_intent.redactions", "must be an integer of at least 0, got %d", *r)
	}

	// REQ-0007
	if n := len(l.Criteria); n < 1 || n > MaxCriteria {
		add("criteria", "must have 1 to %d criteria, has %d", MaxCriteria, n)
	}
	ids := map[string]bool{}
	for i, c := range l.Criteria {
		base := fmt.Sprintf("criteria[%d]", i)
		switch {
		case c.ID == "":
			add(base+".id", "is required")
		case ids[c.ID]:
			add(base+".id", "duplicate criterion id %q", c.ID)
		}
		ids[c.ID] = true
		if strings.TrimSpace(c.Text) == "" {
			add(base+".text", "is required")
		}
		// REQ-0008
		if len(c.Footprint) == 0 {
			add(base+".footprint", "is required and must not be empty")
		}
		for j, g := range c.Footprint {
			pat := strings.TrimPrefix(g, "!")
			if pat == "" {
				add(fmt.Sprintf("%s.footprint[%d]", base, j), "glob %q is empty", g)
			} else if !doublestar.ValidatePattern(pat) {
				add(fmt.Sprintf("%s.footprint[%d]", base, j), "glob %q does not parse", g)
			}
		}
	}

	// REQ-0009, REQ-0010, REQ-0011
	r := l.Ratification
	if strings.TrimSpace(r.Ratifier) == "" {
		add("ratification.ratifier", "is required")
	}
	if _, err := parseUTC(r.RatifiedAt); err != nil {
		add("ratification.ratified_at", "%v", err)
	}
	if !contains(methods, r.Method) {
		add("ratification.method", "must be one of %s, got %q", strings.Join(methods, ", "), r.Method)
	}
	// REQ-0012: recorded, never judged
	switch {
	case r.ReviewDurationMS == nil:
		add("ratification.review_duration_ms", "is required")
	case *r.ReviewDurationMS < 0:
		add("ratification.review_duration_ms", "must be an integer of at least 0, got %d", *r.ReviewDurationMS)
	}

	validatePlanReview(l.PlanReview, add)
	validateAmendments(l, add)

	sort.SliceStable(ps, func(i, j int) bool {
		if ps[i].Path != ps[j].Path {
			return ps[i].Path < ps[j].Path
		}
		return ps[i].Message < ps[j].Message
	})
	return ps
}

// validateRootIntentFile checks path and blob: both present, and well formed, iff the source is
// committed-intent (REQ-0044).
func validateRootIntentFile(ri RootIntent, add func(path, format string, args ...any)) {
	if ri.Source != SourceCommittedIntent {
		if ri.Path != "" {
			add("root_intent.path", "is only for source %s, got %q", SourceCommittedIntent, ri.Path)
		}
		if ri.Blob != "" {
			add("root_intent.blob", "is only for source %s, got %q", SourceCommittedIntent, ri.Blob)
		}
		return
	}
	switch {
	case ri.Path == "":
		add("root_intent.path", "is required for source %s", SourceCommittedIntent)
	case !intentPathRE.MatchString(ri.Path):
		add("root_intent.path", "must be intents/<slug>.md with a slug of [a-z0-9][a-z0-9-]*, got %q", ri.Path)
	}
	switch {
	case ri.Blob == "":
		add("root_intent.blob", "is required for source %s", SourceCommittedIntent)
	case !blobRE.MatchString(ri.Blob):
		add("root_intent.blob", "must be a git object id of 40 or 64 lower-case hex characters, got %q", ri.Blob)
	}
}

// planReviewStatuses is the enum of plan_review.status (plan-review.md §3, §4).
var planReviewStatuses = []string{"complete", "partial", "unavailable", "error"}

// validatePlanReview checks the optional plan_review record: a status from the enum and all
// three counts present and at least 0. Absent is fine.
func validatePlanReview(p *PlanReview, add func(path, format string, args ...any)) {
	if p == nil {
		return
	}
	if !contains(planReviewStatuses, p.Status) {
		add("plan_review.status", "must be one of %s, got %q", strings.Join(planReviewStatuses, ", "), p.Status)
	}
	for _, c := range []struct {
		name string
		n    *int
	}{{"omissions", p.Omissions}, {"additions", p.Additions}, {"weakenings", p.Weakenings}} {
		switch {
		case c.n == nil:
			add("plan_review."+c.name, "is required when plan_review is present")
		case *c.n < 0:
			add("plan_review."+c.name, "must be an integer of at least 0, got %d", *c.n)
		}
	}
}

// validateAmendments checks the amendment history: revisions are 2, 3, … in order and end at the
// lock's revision, and each entry is well formed against the lock's criteria.
func validateAmendments(l Lock, add func(path, format string, args ...any)) {
	if want := len(l.Amendments) + 1; l.Revision >= 1 && l.Revision != want {
		add("revision", "must be %d (1 + the %d amendments), got %d", want, len(l.Amendments), l.Revision)
	}
	for i, a := range l.Amendments {
		base := fmt.Sprintf("amendments[%d]", i)
		if want := i + 2; a.Revision != want {
			add(base+".revision", "must be %d, got %d", want, a.Revision)
		}
		if strings.TrimSpace(a.By) == "" {
			add(base+".by", "is required")
		}
		if _, err := parseUTC(a.At); err != nil {
			add(base+".at", "%v", err)
		}
		if !contains(methods, a.Method) {
			add(base+".method", "must be one of %s, got %q", strings.Join(methods, ", "), a.Method)
		}
		switch a.Kind {
		case KindAddGlob:
			validateAddGlob(l, a, base, add)
		case KindReratify:
			if a.Criterion != "" {
				add(base+".criterion", "is only for add-glob, got %q", a.Criterion)
			}
			if a.Glob != "" {
				add(base+".glob", "is only for add-glob, got %q", a.Glob)
			}
		default:
			add(base+".kind", "must be one of %s, got %q", strings.Join(kinds, ", "), a.Kind)
		}
	}
}

func validateAddGlob(l Lock, a Amendment, base string, add func(path, format string, args ...any)) {
	var crit *Criterion
	switch {
	case a.Criterion == "":
		add(base+".criterion", "is required for add-glob")
	default:
		for i := range l.Criteria {
			if l.Criteria[i].ID == a.Criterion {
				crit = &l.Criteria[i]
			}
		}
		if crit == nil {
			add(base+".criterion", "no criterion %q in this lock", a.Criterion)
		}
	}
	if a.Glob == "" {
		add(base+".glob", "is required for add-glob")
		return
	}
	if err := CheckGlob(a.Glob); err != nil {
		add(base+".glob", "%v", err)
	} else if crit != nil && !contains(crit.Footprint, a.Glob) {
		add(base+".glob", "%q is not in the footprint of %s", a.Glob, crit.ID)
	}
}

// CheckGlob applies the footprint rules to one glob: a leading ! excludes, and what follows must
// be a non-empty pattern that parses.
func CheckGlob(g string) error {
	pat := strings.TrimPrefix(g, "!")
	if pat == "" {
		return fmt.Errorf("glob %q is empty", g)
	}
	if !doublestar.ValidatePattern(pat) {
		return fmt.Errorf("glob %q does not parse", g)
	}
	return nil
}

// ValidID reports whether s is a well-formed lock id (a 26-character Crockford base32 ULID).
func ValidID(s string) bool { return ulidRE.MatchString(s) }

// Hash returns "sha256:" + hex of the canonical JSON (RFC 8785 subset) of the lock with its hash
// field removed (REQ-0013). Absent and empty lists hash the same; a scalar key present with a
// zero value hashes differently from the key absent.
func Hash(l Lock) (string, error) {
	l.Hash = ""
	if l.Amendments == nil {
		l.Amendments = []Amendment{}
	}
	if l.Criteria == nil {
		l.Criteria = []Criterion{}
	} else {
		cs := make([]Criterion, len(l.Criteria))
		copy(cs, l.Criteria)
		for i := range cs {
			if cs[i].Footprint == nil {
				cs[i].Footprint = []string{}
			}
		}
		l.Criteria = cs
	}
	raw, err := json.Marshal(l)
	if err != nil {
		return "", fmt.Errorf("lock: %w", err)
	}
	canon, err := CanonicalJSON(raw)
	if err != nil {
		return "", err
	}
	sum := sha256.Sum256(canon)
	return "sha256:" + hex.EncodeToString(sum[:]), nil
}

// VerifyHash reports whether l.Hash equals the hash computed from the rest of the lock.
func VerifyHash(l Lock) bool {
	want, err := Hash(l)
	if err != nil || l.Hash == "" {
		return false
	}
	return subtle.ConstantTimeCompare([]byte(want), []byte(l.Hash)) == 1
}

// ValidAt reports whether the lock is valid at the evaluation time at: true only when
// ratified_at <= at <= ratified_at + 14 days (REQ-0022). False before ratified_at (so a
// future-dated lock can't outlive 14 days), after the window, or when ratified_at doesn't parse.
// The caller supplies at; this package never reads the clock.
func ValidAt(l Lock, at time.Time) bool {
	t, err := parseUTC(l.Ratification.RatifiedAt)
	if err != nil {
		return false
	}
	return !at.Before(t) && !at.After(t.Add(MaxAge))
}

// parseUTC parses an RFC 3339 timestamp that must be written in UTC (a "Z" suffix).
func parseUTC(s string) (time.Time, error) {
	if s == "" {
		return time.Time{}, errors.New("is required")
	}
	t, err := time.Parse(time.RFC3339, s)
	if err != nil {
		return time.Time{}, fmt.Errorf("must be RFC 3339, got %q", s)
	}
	if !strings.HasSuffix(s, "Z") {
		return time.Time{}, fmt.Errorf("must be UTC (a Z suffix), got %q", s)
	}
	return t.UTC(), nil
}

func contains(set []string, s string) bool {
	for _, v := range set {
		if v == s {
			return true
		}
	}
	return false
}

// Marshal renders the lock as YAML in the field order of intent-lock.md §2 (the order of the
// struct fields), two-space indented. It is deterministic and does not validate or hash: Parse of
// the output gives back an equal Lock.
func Marshal(l Lock) ([]byte, error) {
	if l.Amendments == nil {
		l.Amendments = []Amendment{}
	}
	var buf bytes.Buffer
	enc := yaml.NewEncoder(&buf)
	enc.SetIndent(2)
	if err := enc.Encode(l); err != nil {
		return nil, fmt.Errorf("lock: %w", err)
	}
	if err := enc.Close(); err != nil {
		return nil, fmt.Errorf("lock: %w", err)
	}
	return buf.Bytes(), nil
}
