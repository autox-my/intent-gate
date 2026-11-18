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
	methods  = []string{"hook", "cli-confirm", "issue"}              // REQ-0011
	sources  = []string{"claude-code-session", "issue", "plan-file"} //
	ulidRE   = regexp.MustCompile(`^[0-7][0-9A-HJKMNP-TV-Z]{25}$`)   // 26-char Crockford base32, 128 bits
	sha256RE = regexp.MustCompile(`^[0-9a-f]{64}$`)
)

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
	Amendments   []Amendment  `yaml:"amendments" json:"amendments"`
	Hash         string       `yaml:"hash" json:"hash,omitempty"`
}

// RootIntent binds the lock to the root intent by hash only.
type RootIntent struct {
	SHA256 string `yaml:"sha256" json:"sha256"`
	Source string `yaml:"source" json:"source"`
}

// Criterion is one acceptance criterion with its footprint globs.
type Criterion struct {
	ID        string   `yaml:"id" json:"id"`
	Text      string   `yaml:"text" json:"text"`
	Footprint []string `yaml:"footprint" json:"footprint"`
}

// Ratification records who locked the intent, when and how. RatifiedAt is kept as the RFC 3339
// text so that hashing never depends on re-formatting a time.
type Ratification struct {
	Ratifier         string `yaml:"ratifier" json:"ratifier"`
	RatifiedAt       string `yaml:"ratified_at" json:"ratified_at"`
	Method           string `yaml:"method" json:"method"`
	ReviewDurationMS int    `yaml:"review_duration_ms,omitempty" json:"review_duration_ms,omitempty"`
	DraftedBy        string `yaml:"drafted_by,omitempty" json:"drafted_by,omitempty"`
}

// Amendment is parsed as a free-form mapping; its shape is validated by a later slice.
type Amendment map[string]any

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

	sort.SliceStable(ps, func(i, j int) bool {
		if ps[i].Path != ps[j].Path {
			return ps[i].Path < ps[j].Path
		}
		return ps[i].Message < ps[j].Message
	})
	return ps
}

// Hash returns "sha256:" + hex of the canonical JSON (RFC 8785 subset) of the lock with its hash
// field removed (REQ-0012). Absent and empty lists hash the same.
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

// ValidAt reports whether the lock is still valid at the evaluation time at: false when at is
// more than 14 days after ratified_at, or when ratified_at doesn't parse (REQ-0022). The caller
// supplies at; this package never reads the clock.
func ValidAt(l Lock, at time.Time) bool {
	t, err := parseUTC(l.Ratification.RatifiedAt)
	if err != nil {
		return false
	}
	return !at.After(t.Add(MaxAge))
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
