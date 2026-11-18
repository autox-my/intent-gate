package report

import (
	"strings"

	"intentgate/internal/footprint"
	"intentgate/internal/verify"
)

// RuleID is the SARIF rule of a footprint finding (REQ-0036).
const RuleID = "intentgate/outside-footprint"

type sarifDoc struct {
	Schema  string     `json:"$schema"`
	Version string     `json:"version"`
	Runs    []sarifRun `json:"runs"`
}

type sarifRun struct {
	Tool    sarifTool     `json:"tool"`
	Results []sarifResult `json:"results"`
}

type sarifTool struct {
	Driver sarifDriver `json:"driver"`
}

type sarifDriver struct {
	Name    string      `json:"name"`
	Version string      `json:"version"`
	Rules   []sarifRule `json:"rules"`
}

type sarifRule struct {
	ID               string    `json:"id"`
	ShortDescription sarifText `json:"shortDescription"`
}

type sarifText struct {
	Text string `json:"text"`
}

type sarifResult struct {
	RuleID    string          `json:"ruleId"`
	Level     string          `json:"level"`
	Message   sarifText       `json:"message"`
	Locations []sarifLocation `json:"locations"`
}

type sarifLocation struct {
	PhysicalLocation struct {
		ArtifactLocation struct {
			URI       string `json:"uri"`
			URIBaseID string `json:"uriBaseId"`
		} `json:"artifactLocation"`
	} `json:"physicalLocation"`
}

// uriBaseID is the base every artifact uri is relative to: the repository root.
const uriBaseID = "%SRCROOT%"

// encodePath percent-encodes a repo-relative path for a SARIF uri: each segment per RFC 3986 (only
// unreserved characters stay as they are, so a ":" in the first segment can't read as a scheme),
// the "/" separators kept.
func encodePath(p string) string {
	const hex = "0123456789ABCDEF"
	var b strings.Builder
	for i := 0; i < len(p); i++ {
		c := p[i]
		switch {
		case c == '/', c >= 'a' && c <= 'z', c >= 'A' && c <= 'Z', c >= '0' && c <= '9', c == '-', c == '.', c == '_', c == '~':
			b.WriteByte(c)
		default:
			b.WriteByte('%')
			b.WriteByte(hex[c>>4])
			b.WriteByte(hex[c&15])
		}
	}
	return b.String()
}

// SARIF renders the footprint findings as SARIF 2.1.0 (REQ-0036): one run, one result per finding
// in path order, located at the file (percent-encoded, relative to %SRCROOT%) with no region. version is IntentGate's version, supplied by
// the caller.
func SARIF(r verify.Result, version string) []byte {
	results := make([]sarifResult, 0, len(r.Findings))
	for _, f := range sortedFindings(r.Findings) {
		var loc sarifLocation
		loc.PhysicalLocation.ArtifactLocation.URI = encodePath(f.Path)
		loc.PhysicalLocation.ArtifactLocation.URIBaseID = uriBaseID
		results = append(results, sarifResult{
			RuleID:    RuleID,
			Level:     "warning",
			Message:   sarifText{Text: sarifMessage(f)},
			Locations: []sarifLocation{loc},
		})
	}
	return marshal(sarifDoc{
		Schema:  "https://json.schemastore.org/sarif-2.1.0.json",
		Version: "2.1.0",
		Runs: []sarifRun{{
			Tool: sarifTool{Driver: sarifDriver{
				Name:    "IntentGate",
				Version: version,
				Rules: []sarifRule{{
					ID:               RuleID,
					ShortDescription: sarifText{Text: "A changed file is outside every criterion's footprint"},
				}},
			}},
			Results: results,
		}},
	})
}

func sarifMessage(f footprint.Finding) string {
	var b strings.Builder
	b.WriteString(f.Path + " is outside every criterion's footprint")
	if f.NearestCriterion != "" {
		b.WriteString(" (nearest criterion: " + f.NearestCriterion + " of lock " + f.LockID + ")")
	}
	if f.Fix != "" {
		b.WriteString(". If the change is intended, run: " + f.Fix)
	}
	return b.String()
}
