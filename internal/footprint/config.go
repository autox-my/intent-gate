package footprint

import (
	"bytes"
	"errors"
	"fmt"
	"io"
	"strings"

	"github.com/bmatcuk/doublestar/v4"
	"gopkg.in/yaml.v3"
)

// Config is the parsed .intentgate.yml (REQ-0029). The zero value means "the defaults": test
// pairing is on unless NoTestPairing is set (`test_pairing: false`).
type Config struct {
	Add, Remove   []string
	NoTestPairing bool
}

var lockfileNames = []string{
	"go.sum", "package-lock.json", "pnpm-lock.yaml", "yarn.lock", "bun.lockb", "Cargo.lock",
	"poetry.lock", "uv.lock", "Pipfile.lock", "Gemfile.lock", "composer.lock",
}

// defaultGlobs are the default implicit globs (design §3), minus paired tests (a rule, not a glob)
// and the .gitattributes markers.
func defaultGlobs() []string {
	globs := []string{
		// fixtures and snapshots (REQ-0026)
		"**/testdata/**", "**/fixtures/**", "**/__fixtures__/**", "**/__snapshots__/**", "**/*.snap", "**/*.golden",
	}
	// lockfiles at any depth (REQ-0027)
	for _, n := range lockfileNames {
		globs = append(globs, "**/"+n)
	}
	// generated and vendored (REQ-0028)
	return append(globs,
		"vendor/**", "third_party/**", "**/node_modules/**", "**/*.pb.go", "**/*_gen.go", "**/*.generated.*", "**/dist/**")
}

type fileConfig struct {
	Version            int `yaml:"version"`
	ImplicitFootprints *struct {
		Add         []string `yaml:"add"`
		Remove      []string `yaml:"remove"`
		TestPairing *bool    `yaml:"test_pairing"`
	} `yaml:"implicit_footprints"`
}

// ParseConfig parses .intentgate.yml. nil or empty data means "no file" and yields the defaults.
// Anything else must be a valid version 1 document: unknown keys, a wrong version, an invalid
// `add` glob and a `remove` that is not exactly a default glob are errors, never "use the defaults".
func ParseConfig(data []byte) (Config, error) {
	cfg := Config{}
	if len(bytes.TrimSpace(data)) == 0 {
		return cfg, nil
	}
	dec := yaml.NewDecoder(bytes.NewReader(data))
	dec.KnownFields(true)
	var fc fileConfig
	if err := dec.Decode(&fc); errors.Is(err, io.EOF) {
		return Config{}, errors.New("footprint: .intentgate.yml: version is missing (the file has no keys; it must say `version: 1`)")
	} else if err != nil {
		return Config{}, fmt.Errorf("footprint: .intentgate.yml: %w", err)
	}
	var extra yaml.Node
	if err := dec.Decode(&extra); !errors.Is(err, io.EOF) {
		return Config{}, errors.New("footprint: .intentgate.yml: more than one YAML document")
	}
	if fc.Version != 1 {
		return Config{}, fmt.Errorf("footprint: .intentgate.yml: version must be 1, got %d", fc.Version)
	}
	if ifp := fc.ImplicitFootprints; ifp != nil {
		cfg.Add = ifp.Add
		cfg.Remove = ifp.Remove
		if ifp.TestPairing != nil {
			cfg.NoTestPairing = !*ifp.TestPairing
		}
	}
	if _, err := cfg.resolve(); err != nil {
		return Config{}, err
	}
	return cfg, nil
}

// resolve returns the default globs adjusted by Add and Remove, validating both.
func (cfg Config) resolve() ([]string, error) {
	globs := defaultGlobs()
	seen := map[string]bool{}
	for _, r := range cfg.Remove {
		if seen[r] {
			return nil, fmt.Errorf("footprint: .intentgate.yml: remove %q: duplicate entry", r)
		}
		seen[r] = true
		idx := indexOf(globs, r)
		if idx < 0 {
			return nil, fmt.Errorf("footprint: .intentgate.yml: remove %q is not one of the default globs", r)
		}
		globs = append(globs[:idx], globs[idx+1:]...)
	}
	for _, a := range cfg.Add {
		switch {
		case a == "":
			return nil, errors.New("footprint: .intentgate.yml: add: empty glob")
		case strings.HasPrefix(a, "!"):
			return nil, fmt.Errorf("footprint: .intentgate.yml: add %q: implicit footprints have no negation", a)
		case !doublestar.ValidatePattern(a):
			return nil, fmt.Errorf("footprint: .intentgate.yml: add %q: invalid glob", a)
		}
		globs = append(globs, a)
	}
	return globs, nil
}

func indexOf(ss []string, s string) int {
	for i, v := range ss {
		if v == s {
			return i
		}
	}
	return -1
}

// Implicit is the set of implicit footprints in force: the default and added globs, the
// .gitattributes rules and whether test pairing applies. Build it with NewImplicit.
type Implicit struct {
	globs       []string
	attrs       []attrRule
	testPairing bool
}

// NewImplicit builds the implicit footprints: the defaults (design §3) adjusted by cfg, plus the
// paths marked linguist-generated or linguist-vendored in gitattributes (REQ-0028, REQ-0029).
func NewImplicit(cfg Config, gitattributes []byte) (Implicit, error) {
	globs, err := cfg.resolve()
	if err != nil {
		return Implicit{}, err
	}
	rules, err := parseGitattributes(gitattributes)
	if err != nil {
		return Implicit{}, err
	}
	return Implicit{globs: globs, attrs: rules, testPairing: !cfg.NoTestPairing}, nil
}

// contains reports whether path is inside an implicit footprint. A paired test is inside when its
// subject is inside one of the criteria (REQ-0025).
func (im Implicit) contains(path string, criteria []Criterion) bool {
	for _, g := range im.globs {
		if matches(g, path) {
			return true
		}
	}
	if attrMarked(im.attrs, path) {
		return true
	}
	if im.testPairing {
		for _, subject := range subjectsOfTest(path) {
			if insideAnyCriterion(subject, criteria) {
				return true
			}
		}
	}
	return false
}
