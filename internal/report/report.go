// Package report renders a verify result as JSON, SARIF and Markdown. It is pure: no I/O, clock
// or randomness.
package report

import (
	"intentgate/internal/policy"
	"intentgate/internal/verify"
)

// JSON renders the intentgate.verify/1 document.
func JSON(r verify.Result) []byte { return nil }

// SARIF renders the findings as SARIF 2.1.0; version is IntentGate's version.
func SARIF(r verify.Result, version string) []byte { return nil }

// Text renders the content of the JSON document for a terminal.
func Text(r verify.Result) string { return "" }

// CheckSummary renders the check-run summary.
func CheckSummary(r verify.Result, d policy.Decision) string { return "" }

// Comment renders the pull request comment, or "" when the decision posts none.
func Comment(r verify.Result, d policy.Decision) string { return "" }
