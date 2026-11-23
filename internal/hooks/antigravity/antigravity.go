// Package antigravity is the pure part of the Antigravity hook: it reads Antigravity's hook input
// and JSONL transcript. The formats are Antigravity's, not ours, so unknown fields are ignored.
package antigravity

import "errors"

// TurnSeparator joins the human turns of a transcript into the root intent.
const TurnSeparator = "\n\n---\n\n"

// Ratify card constants and lock source for Antigravity sessions.
const (
	RatifyLabel   = "Ratify"
	NotYetLabel   = "Not yet"
	SourceSession = "antigravity-session"
)

// Question is one question of an ask_question tool input.
type Question struct {
	Question    string
	Options     []string
	MultiSelect bool
}

// Card is a recognised ratify card: the plan path as asked and the SHA-256 it was asked with.
type Card struct {
	Question string
	Path     string
	SHA256   string
}

// Input is the parsed Antigravity PreToolUse or PostInvocation hook input.
type Input struct {
	HookEventName         string
	ConversationID        string
	TranscriptPath        string
	ArtifactDirectoryPath string
	CWD                   string
	ModelName             string
	Error                 string
	StepIdx               *int
	InvocationNum         *int
	InitialNumSteps       *int
	ToolCall              struct {
		ID        string
		Name      string
		Questions []Question
	}
}

// ScopeKind says how RootIntentScope chose its turns.
type ScopeKind string

// The scopes: the turns since the previous ratified card, the previous card's own turns (no new
// ones since), or the whole session (no earlier ratified card).
const (
	ScopeSince  ScopeKind = "since"
	ScopeReused ScopeKind = "reused"
	ScopeWhole  ScopeKind = "whole"
)

// Scope is a root intent with how it was scoped. Turns is the number of human turns in Text.
type Scope struct {
	Text      string
	DraftedBy string
	Turns     int
	Kind      ScopeKind
}

// Describe is the line the hooks print to say which scope was used.
func (s Scope) Describe() string { return "" }

// PreOutput is what PreToolUse prints: always {"decision":"allow"}.
type PreOutput struct{}

// Render returns the PreToolUse JSON output and a newline.
func (PreOutput) Render() []byte { return nil }

// PostInvocationOutput is what PostInvocation prints: {} when Message is empty, else injectSteps.
type PostInvocationOutput struct {
	Message string
}

// Render returns the PostInvocation JSON output and a newline.
func (PostInvocationOutput) Render() []byte { return nil }

// RatifyQuestionText is the question of the card for a plan at path with this SHA-256.
func RatifyQuestionText(path, sha256hex string) string { return "" }

// RenderCard is the ask_question item JSON for the ratify card, ending in a newline.
func RenderCard(path, sha256hex string) []byte { return nil }

// ParseCard recognises an exact ratify card.
func ParseCard(qs []Question) (Card, bool) { return Card{}, false }

// ParseInput parses an Antigravity hook JSON payload from stdin.
func ParseInput(data []byte) (Input, error) { return Input{}, errors.New("not implemented") }

// StepAnswer finds the transcript entry at stepIdx and returns its trimmed A1 answer, whether the
// step was found, and whether status is DONE.
func StepAnswer(transcript []byte, stepIdx int) (answer string, found bool, done bool) {
	return "", false, false
}

// Ratified reports whether an ask_question answer counts as ratification.
func Ratified(answer string) bool { return false }

// RootIntentScope extracts and scopes human turns and drafted_by from an Antigravity transcript.
func RootIntentScope(transcript []byte, currentStepIdx int) (Scope, error) {
	return Scope{}, errors.New("not implemented")
}
