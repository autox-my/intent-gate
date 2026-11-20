package ratify

import (
	"errors"
	"fmt"
	"sort"
	"strings"
	"sync"

	"github.com/zricethezav/gitleaks/v8/config"
	"github.com/zricethezav/gitleaks/v8/detect"
)

// Masked is root intent text that has been through the secret scan. Only MaskWith (and so Mask and
// MaskAndBuild) can make a non-zero one, so Build and Store.Write, which take a Masked, can't be
// handed unscanned text by construction.
type Masked struct {
	text       string
	redactions int
}

// String is the masked text.
func (m Masked) String() string { return m.text }

// Redactions is the number of secrets masked out of the text.
func (m Masked) Redactions() int { return m.redactions }

// Finding is one secret a Detector found: the secret as it appears in the text, and the id of the
// rule that matched it.
type Finding struct {
	RuleID string
	Secret string
}

// Detector finds the secrets in a text. Mask uses gitleaks'; MaskAndBuild takes one as an argument
// so that a detector failure, and findings gitleaks wouldn't produce, can be tested.
type Detector func(text string) ([]Finding, error)

var (
	cfgOnce sync.Once
	cfg     config.Config
	cfgErr  error
)

// defaultConfig parses gitleaks' embedded default config once. It goes through gitleaks'
// NewDetectorDefaultConfig, which uses global viper state, so it must not run concurrently.
func defaultConfig() (config.Config, error) {
	cfgOnce.Do(func() {
		// NOTE: NewDetectorDefaultConfig reads gitleaks' embedded config through viper's global
		// instance: it sets and reads global viper state (config type and contents), so it is not
		// safe to run concurrently and it changes viper for anything else in the process that uses
		// the global. The sync.Once keeps it to one call; only the parsed config is kept.
		d, err := detect.NewDetectorDefaultConfig()
		if err != nil {
			cfgErr = err
			return
		}
		cfg = d.Config
	})
	return cfg, cfgErr
}

// gitleaksDetector is the production Detector: gitleaks' detection library with its default config.
func gitleaksDetector(text string) ([]Finding, error) {
	c, err := defaultConfig()
	if err != nil {
		return nil, fmt.Errorf("secret scan: %w", err)
	}
	var out []Finding
	for _, f := range detect.NewDetector(c).DetectString(text) {
		out = append(out, Finding{RuleID: f.RuleID, Secret: f.Secret})
	}
	return out, nil
}

// ErrSecretInIntent is the finding of ScanCommitted: a committed intent holds what looks like a
// secret. The file is already public, so masking it in the lock would hide nothing; the fix is in
// the file and its history (intent-lock.md §6).
var ErrSecretInIntent = errors.New("the committed intent contains what looks like a secret")

// ScanCommitted scans the text of a committed intent with d (nil means gitleaks') and returns it
// unchanged, as Masked text with no redactions. A finding is ErrSecretInIntent and a detector error
// is returned as it is: either way no Masked value exists.
func ScanCommitted(d Detector, text string) (Masked, error) {
	if d == nil {
		d = gitleaksDetector
	}
	findings, err := d(text)
	if err != nil {
		return Masked{}, err
	}
	if len(findings) > 0 {
		return Masked{}, ErrSecretInIntent
	}
	return Masked{text: text}, nil
}

// Mask scans text with gitleaks' detection library (default config) and replaces its findings'
// secrets with [REDACTED:<rule-id>]. See MaskWith for how spans are replaced and counted.
func Mask(text string) (Masked, int, error) { return MaskWith(gitleaksDetector, text) }

// span is the byte range [start, end) of a secret in the text, and the rule that found it.
type span struct {
	start, end int
	rule       string
}

// MaskWith scans text with d and replaces every occurrence of every finding's secret. Occurrences
// that overlap or touch are merged into one span, and each merged span is replaced once by
// [REDACTED:<rule-id>], the rule of the span's earliest-starting finding (ties by rule id). It returns
// the masked text and the number of merged spans. A finding whose secret is empty or isn't in the
// text is an error: the text is never passed on partly unmasked. The result is deterministic and
// doesn't depend on the order of d's findings.
func MaskWith(d Detector, text string) (Masked, int, error) {
	findings, err := d(text)
	if err != nil {
		return Masked{}, 0, err
	}
	var spans []span
	for _, f := range findings {
		if f.Secret == "" || !strings.Contains(text, f.Secret) {
			return Masked{}, 0, fmt.Errorf("secret scan: rule %s found a secret that is not in the text; refusing to continue unmasked", f.RuleID)
		}
		for from := 0; ; {
			k := strings.Index(text[from:], f.Secret)
			if k < 0 {
				break
			}
			start := from + k
			spans = append(spans, span{start, start + len(f.Secret), f.RuleID})
			from = start + 1 // overlapping occurrences of one secret count too
		}
	}
	sort.Slice(spans, func(i, j int) bool {
		if spans[i].start != spans[j].start {
			return spans[i].start < spans[j].start
		}
		if spans[i].rule != spans[j].rule {
			return spans[i].rule < spans[j].rule
		}
		return spans[i].end > spans[j].end
	})

	var out strings.Builder
	n, last := 0, 0
	for i := 0; i < len(spans); {
		cur := spans[i] // the earliest-starting span of the group names it
		end := cur.end
		for i++; i < len(spans) && spans[i].start <= end; i++ {
			end = max(end, spans[i].end)
		}
		out.WriteString(text[last:cur.start])
		out.WriteString("[REDACTED:" + cur.rule + "]")
		last = end
		n++
	}
	out.WriteString(text[last:])
	return Masked{text: out.String(), redactions: n}, n, nil
}
