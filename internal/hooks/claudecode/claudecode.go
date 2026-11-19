// Package claudecode is the pure part of the Claude Code hook: it reads Claude Code's hook input and
// transcript. The formats are Claude Code's, not ours, so unknown fields are ignored.
package claudecode

import (
	"bytes"
	"encoding/json"
	"fmt"
	"regexp"
	"strings"
)

// TurnSeparator joins the human turns of a transcript into the root intent.
const TurnSeparator = "\n\n---\n\n"

// Question is one question of an AskUserQuestion tool input.
type Question struct {
	Header   string
	Question string
}

// Input is the part of a PreToolUse or PostToolUse hook input the ExitPlanMode and AskUserQuestion
// hooks use. ToolInput.Plan is set by PreToolUse and ToolResponse.Plan by PostToolUse. For
// AskUserQuestion, ToolInput.Questions are the questions asked and ToolResponse.Answers (PostToolUse)
// maps a question's text to the label the human chose; a question with no string answer is absent.
type Input struct {
	HookEventName  string
	SessionID      string
	TranscriptPath string
	CWD            string
	ToolName       string
	ToolUseID      string
	ToolInput      struct {
		Plan      string
		Questions []Question
	}
	ToolResponse struct {
		Plan    string
		Answers map[string]string
	}
}

type rawInput struct {
	HookEventName  string          `json:"hook_event_name"`
	SessionID      string          `json:"session_id"`
	TranscriptPath string          `json:"transcript_path"`
	CWD            string          `json:"cwd"`
	ToolName       string          `json:"tool_name"`
	ToolUseID      string          `json:"tool_use_id"`
	ToolInput      json.RawMessage `json:"tool_input"`
	ToolResponse   json.RawMessage `json:"tool_response"`
}

// planOf returns the "plan" string of a JSON object; anything else (another tool's string or list,
// null, a plan that isn't a string) gives "".
func planOf(raw json.RawMessage) string {
	var v struct {
		Plan string `json:"plan"`
	}
	if json.Unmarshal(raw, &v) != nil {
		return ""
	}
	return v.Plan
}

// questionsOf returns the "questions" of a tool input that is an AskUserQuestion's; anything
// else gives none.
func questionsOf(raw json.RawMessage) []Question {
	var v struct {
		Questions []struct {
			Header   string `json:"header"`
			Question string `json:"question"`
		} `json:"questions"`
	}
	if json.Unmarshal(raw, &v) != nil {
		return nil
	}
	var qs []Question
	for _, q := range v.Questions {
		qs = append(qs, Question{Header: q.Header, Question: q.Question})
	}
	return qs
}

// answersOf returns the "answers" of an AskUserQuestion's output: question text to chosen label.
// Entries that are not strings are left out.
func answersOf(raw json.RawMessage) map[string]string {
	var v struct {
		Answers map[string]json.RawMessage `json:"answers"`
	}
	if json.Unmarshal(raw, &v) != nil {
		return nil
	}
	out := map[string]string{}
	for q, a := range v.Answers {
		var label string
		if json.Unmarshal(a, &label) == nil {
			out[q] = label
		}
	}
	return out
}

// ParseInput reads a hook input from JSON. Unknown fields are ignored; tool_input and
// tool_response of another tool may be any JSON and read as having no plan.
func ParseInput(data []byte) (Input, error) {
	var r rawInput
	if err := json.Unmarshal(data, &r); err != nil {
		return Input{}, fmt.Errorf("the hook input is not a JSON object: %w", err)
	}
	in := Input{
		HookEventName:  r.HookEventName,
		SessionID:      r.SessionID,
		TranscriptPath: r.TranscriptPath,
		CWD:            r.CWD,
		ToolName:       r.ToolName,
		ToolUseID:      r.ToolUseID,
	}
	in.ToolInput.Plan = planOf(r.ToolInput)
	in.ToolResponse.Plan = planOf(r.ToolResponse)
	if r.ToolName == askUserQuestion {
		in.ToolInput.Questions = questionsOf(r.ToolInput)
		in.ToolResponse.Answers = answersOf(r.ToolResponse)
	}
	return in, nil
}

type entry struct {
	Type                      string `json:"type"`
	IsMeta                    bool   `json:"isMeta"`
	IsSidechain               bool   `json:"isSidechain"`
	IsCompactSummary          bool   `json:"isCompactSummary"`
	IsVisibleInTranscriptOnly bool   `json:"isVisibleInTranscriptOnly"`
	Message                   struct {
		Model   string          `json:"model"`
		Content json.RawMessage `json:"content"`
	} `json:"message"`
	ToolUseResult json.RawMessage `json:"toolUseResult"` // the structured output of the tool a user entry answers
}

// turnText returns the text of a human turn's content and false if the entry holds a tool_result
// (it is the harness answering the model, not a human) or has no text.
func turnText(content json.RawMessage) (string, bool) {
	var s string
	if json.Unmarshal(content, &s) == nil {
		return s, s != ""
	}
	var blocks []struct {
		Type string `json:"type"`
		Text string `json:"text"`
	}
	if json.Unmarshal(content, &blocks) != nil {
		return "", false
	}
	var texts []string
	for _, b := range blocks {
		switch b.Type {
		case "tool_result":
			return "", false
		case "text":
			texts = append(texts, b.Text)
		}
	}
	text := strings.Join(texts, "\n")
	return text, text != ""
}

var systemReminder = regexp.MustCompile(`(?s)<system-reminder>.*?</system-reminder>`)

// interruptMarkers are the whole text of an entry Claude Code writes when the human interrupts.
var interruptMarkers = map[string]bool{
	"[Request interrupted by user]":              true,
	"[Request interrupted by user for tool use]": true,
}

// harnessPrefixes start entries that record a slash command or shell output, not something the
// human asked the agent.
var harnessPrefixes = []string{
	"<command-name>", "<command-message>", "<command-args>",
	"<local-command-stdout>", "<local-command-stderr>", "<bash-stdout>", "<bash-stderr>",
}

// humanText cleans the text of a candidate human turn: <system-reminder> spans are removed and the
// result trimmed. It returns false for a turn that is then empty, an interrupt marker, or a
// slash-command or shell-output record.
func humanText(text string) (string, bool) {
	text = strings.TrimSpace(systemReminder.ReplaceAllString(text, ""))
	if text == "" || interruptMarkers[text] {
		return "", false
	}
	for _, p := range harnessPrefixes {
		if strings.HasPrefix(text, p) {
			return "", false
		}
	}
	return text, true
}

// exitPlanMode is the tool whose approval scopes the root intent; a ratified card
// (askUserQuestion) counts as one (ratification.md §2.1).
const (
	exitPlanMode    = "ExitPlanMode"
	askUserQuestion = "AskUserQuestion"
)

// The ratify card (ratification.md §2.1): one AskUserQuestion with this header, a question of the
// form `Ratify the plan in <path> (sha256 <hex>)?` and the options Ratify and Not yet.
const (
	RatifyHeader = "IntentGate"
	RatifyLabel  = "Ratify"
	NotYetLabel  = "Not yet"
)

var ratifyQuestion = regexp.MustCompile(`^Ratify the plan in (.+) \(sha256 ([0-9a-f]{64})\)\?$`)

// Card is a recognised ratify card: the plan path as asked and the SHA-256 it was asked with.
type Card struct {
	Question string
	Path     string
	SHA256   string
}

// ParseCard recognises a ratify card: exactly one question, header exactly RatifyHeader, text of the
// ratify form. Anything else is not a card and false.
func ParseCard(qs []Question) (Card, bool) {
	if len(qs) != 1 || qs[0].Header != RatifyHeader {
		return Card{}, false
	}
	m := ratifyQuestion.FindStringSubmatch(qs[0].Question)
	if m == nil {
		return Card{}, false
	}
	return Card{Question: qs[0].Question, Path: m[1], SHA256: m[2]}, true
}

// RatifyQuestionText is the question of the card for a plan at path with this SHA-256.
func RatifyQuestionText(path, sha256hex string) string {
	return "Ratify the plan in " + path + " (sha256 " + sha256hex + ")?"
}

// RenderCard is the AskUserQuestion input for the card, as one JSON object and a newline.
func RenderCard(path, sha256hex string) []byte {
	type option struct {
		Label       string `json:"label"`
		Description string `json:"description"`
	}
	card := struct {
		Header      string   `json:"header"`
		Question    string   `json:"question"`
		Options     []option `json:"options"`
		MultiSelect bool     `json:"multiSelect"`
	}{RatifyHeader, RatifyQuestionText(path, sha256hex),
		[]option{{RatifyLabel, "Write the intent lock for this plan"}, {NotYetLabel, "No lock"}}, false}
	var b bytes.Buffer
	enc := json.NewEncoder(&b)
	enc.SetEscapeHTML(false)
	_ = enc.Encode(card) // a struct of strings can't fail
	return b.Bytes()
}

// Ratified reports whether the human chose exactly Ratify for the card in a PostToolUse input.
func (c Card) Ratified(answers map[string]string) bool {
	return answers[c.Question] == RatifyLabel
}

// planBlock is the part of a content block the plan scoping reads: an ExitPlanMode tool_use, or the
// tool_result that answers one (is_error is true when the human rejected the plan).
type planBlock struct {
	Type      string          `json:"type"`
	Name      string          `json:"name"`
	ID        string          `json:"id"`
	ToolUseID string          `json:"tool_use_id"`
	IsError   bool            `json:"is_error"`
	Content   json.RawMessage `json:"content"`
	Input     json.RawMessage `json:"input"`
}

func planBlocks(content json.RawMessage) []planBlock {
	var blocks []planBlock
	if json.Unmarshal(content, &blocks) != nil { // a string, or anything else: no blocks
		return nil
	}
	return blocks
}

// denialMarkers are the texts Claude Code puts in the result of a plan the human turned down, even
// when it does not set is_error. They are matched case-insensitively.
var denialMarkers = []string{"the user doesn't want to proceed", "user rejected", "rejected the plan"}

// resultText is the text of a tool_result's content: a string, or the text blocks of a list.
func resultText(content json.RawMessage) string {
	var s string
	if json.Unmarshal(content, &s) == nil {
		return s
	}
	var blocks []struct {
		Text string `json:"text"`
	}
	if json.Unmarshal(content, &blocks) != nil {
		return ""
	}
	var texts []string
	for _, b := range blocks {
		texts = append(texts, b.Text)
	}
	return strings.Join(texts, "\n")
}

// cardOf is the ratify card a tool_use block asks, if it is one.
func cardOf(b planBlock) (Card, bool) {
	if b.Type != "tool_use" || b.Name != askUserQuestion {
		return Card{}, false
	}
	return ParseCard(questionsOf(b.Input))
}

// cardRatified reports whether the result of a card is the human choosing Ratify: the structured
// output (toolUseResult) maps the question to Ratify, or the result text says so.
func cardRatified(c Card, b planBlock, toolUseResult json.RawMessage) bool {
	if b.IsError {
		return false
	}
	if c.Ratified(answersOf(toolUseResult)) {
		return true
	}
	return strings.Contains(resultText(b.Content), `"`+c.Question+`"="`+RatifyLabel+`"`)
}

// approved reports whether a plan's result is an approval: not an error and with no denial marker.
func approved(b planBlock) bool {
	if b.IsError {
		return false
	}
	text := strings.ToLower(resultText(b.Content))
	for _, m := range denialMarkers {
		if strings.Contains(text, m) {
			return false
		}
	}
	return true
}

// ScopeKind says how RootIntentScope chose its turns.
type ScopeKind string

// The scopes: the turns since the previous approved plan, the previous plan's own turns (no new
// ones since), or the whole session (no earlier approved plan).
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
func (s Scope) Describe() string {
	why := map[ScopeKind]string{
		ScopeSince:  "since the previous approved plan",
		ScopeReused: "reused from the previous plan (no new turns)",
		ScopeWhole:  "whole session (no earlier approved plan)",
	}[s.Kind]
	return fmt.Sprintf("root intent: %d human turn(s) %s", s.Turns, why)
}

// RootIntent returns the root intent text and "claude-code/<model>"; see RootIntentScope.
func RootIntent(transcript []byte, currentToolUseID string) (text, draftedBy string, err error) {
	s, err := RootIntentScope(transcript, currentToolUseID)
	return s.Text, s.DraftedBy, err
}

// RootIntentScope returns the human turns of a JSONL transcript joined with TurnSeparator, in file
// order, how they were scoped, and "claude-code/<model>" from the last assistant entry (empty if
// there is none). Human turns are "user" entries whose content is a string or text blocks; meta,
// sidechain, compaction summary and transcript-only entries, entries carrying a tool_result,
// interrupt markers and slash-command or shell-output records are skipped, and <system-reminder>
// spans are removed (see humanText); each kept turn is trimmed. A malformed line is an error; no
// turns is "".
//
// The turns are scoped to one plan (ratification.md §2, "Root intent scope"). A boundary is an
// ExitPlanMode tool use, or a ratify card (§2.1) asked with AskUserQuestion, that comes before currentToolUseID in transcript order (a use after it, or
// the current one, never is) and that was approved: its result, seen before the current use, is not
// an error and holds no denial marker. A use with no such result is a boundary too, so a truncated
// transcript cannot leak earlier slices' asks. The text is the turns after the last boundary; if
// there are none, the turns the previous boundary's plan had (those between the boundary before it
// and it). With no boundary every turn is kept.
func RootIntentScope(transcript []byte, currentToolUseID string) (Scope, error) {
	type use struct {
		turns  int   // the human turns before this tool use
		denied bool  // its result, seen before the current use, is a rejection
		card   *Card // set when the use is a ratify card
	}
	var (
		turns     []string
		uses      []use // the ExitPlanMode tool uses before the current one, in transcript order
		useByID   = map[string]int{}
		seenCur   bool
		draftedBy string
	)
	for i, line := range bytes.Split(transcript, []byte("\n")) {
		if len(bytes.TrimSpace(line)) == 0 {
			continue
		}
		var e entry
		if err := json.Unmarshal(line, &e); err != nil {
			return Scope{}, fmt.Errorf("transcript line %d is not valid JSON: %w", i+1, err)
		}
		switch e.Type {
		case "user":
			if e.IsSidechain {
				continue
			}
			if !seenCur {
				for _, b := range planBlocks(e.Message.Content) {
					if k, ok := useByID[b.ToolUseID]; ok && b.Type == "tool_result" {
						if c := uses[k].card; c != nil {
							uses[k].denied = !cardRatified(*c, b, e.ToolUseResult)
						} else {
							uses[k].denied = !approved(b)
						}
						delete(useByID, b.ToolUseID) // the first result is the answer
					}
				}
			}
			if e.IsMeta || e.IsCompactSummary || e.IsVisibleInTranscriptOnly {
				continue
			}
			if t, ok := turnText(e.Message.Content); ok {
				if t, ok := humanText(t); ok {
					turns = append(turns, t)
				}
			}
		case "assistant":
			if e.IsSidechain {
				continue
			}
			draftedBy = ""
			if e.Message.Model != "" {
				draftedBy = "claude-code/" + e.Message.Model
			}
			for _, b := range planBlocks(e.Message.Content) {
				card, isCard := cardOf(b)
				if b.Type != "tool_use" || b.ID == "" || (b.Name != exitPlanMode && !isCard) {
					continue
				}
				if b.ID == currentToolUseID {
					seenCur = true
				}
				if !seenCur {
					useByID[b.ID] = len(uses)
					u := use{turns: len(turns)}
					if isCard {
						u.card = &card
					}
					uses = append(uses, u)
				}
			}
		}
	}
	var bounds []int // the turn counts at the boundaries: a use not denied is approved or unanswered
	for _, u := range uses {
		if !u.denied {
			bounds = append(bounds, u.turns)
		}
	}
	s := Scope{DraftedBy: draftedBy, Kind: ScopeWhole}
	picked := turns
	if n := len(bounds); n > 0 {
		from := bounds[n-1]
		picked, s.Kind = turns[from:], ScopeSince
		if len(picked) == 0 { // nothing since the boundary: the previous plan's turns
			prev := 0
			if n > 1 {
				prev = bounds[n-2]
			}
			picked, s.Kind = turns[prev:from], ScopeReused
		}
	}
	s.Text, s.Turns = strings.Join(picked, TurnSeparator), len(picked)
	return s, nil
}

// Output is what a hook prints: one JSON object. An empty SystemMessage renders {}.
type Output struct {
	SystemMessage string `json:"systemMessage,omitempty"`
}

// Render returns the JSON object and a newline.
func (o Output) Render() []byte {
	b, err := json.Marshal(o)
	if err != nil { // a struct of one string can't fail
		return []byte("{}\n")
	}
	return append(b, '\n')
}
