// Package lock is IntentGate's pure intent-lock core: parse, validate, hash and validity.
// It does no file I/O, reads no clock, uses no randomness and makes no network calls.
package lock

import "time"

// Lock is the committed intent lock (schema intentgate.lock/1). It has no field for root intent text.
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

// Ratification records who locked the intent, when and how.
type Ratification struct {
	Ratifier         string `yaml:"ratifier" json:"ratifier"`
	RatifiedAt       string `yaml:"ratified_at" json:"ratified_at"`
	Method           string `yaml:"method" json:"method"`
	ReviewDurationMS int    `yaml:"review_duration_ms,omitempty" json:"review_duration_ms,omitempty"`
	DraftedBy        string `yaml:"drafted_by,omitempty" json:"drafted_by,omitempty"`
}

// Amendment is parsed as a free-form mapping; its shape is validated by a later slice.
type Amendment map[string]any

// Problem is one validation finding.
type Problem struct {
	Path    string
	Message string
}

// Parse reads a lock from YAML, rejecting unknown keys.
func Parse(data []byte) (Lock, error) { return Lock{}, nil }

// Validate returns every problem with the lock, sorted by path then message.
func Validate(l Lock) []Problem { return nil }

// Hash returns "sha256:" + hex over the canonical JSON of the lock with its hash removed.
func Hash(l Lock) (string, error) { return "", nil }

// VerifyHash reports whether l.Hash matches the computed hash.
func VerifyHash(l Lock) bool { return false }

// ValidAt reports whether the lock is still valid at the evaluation time at.
func ValidAt(l Lock, at time.Time) bool { return false }
