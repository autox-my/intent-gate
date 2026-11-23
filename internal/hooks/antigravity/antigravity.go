// Package antigravity is the pure part of the Antigravity hook: it reads Antigravity's hook input
// and JSONL transcript. The formats are Antigravity's, not ours, so unknown fields are ignored.
package antigravity

import (
	"bytes"
	"encoding/json"
	"fmt"
	"regexp"
	"slices"
	"strings"
)

// TurnSeparator joins the human turns of a transcript into the root intent.
const TurnSeparator = "\n\n---\n\n"

// Ratify card constants and lock source for Antigravity sessions.
const (
	RatifyLabel   = "Ratify"
	NotYetLabel   = "Not yet"
	SourceSession = "antigravity-session"
)

const askQuestionTool = "ask_question"

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

type rawInput struct {
	HookEventName         string `json:"hookEventName"`
	ConversationID        string `json:"conversationId"`
	TranscriptPath        string `json:"transcriptPath"`
	ArtifactDirectoryPath string `json:"artifactDirectoryPath"`
	CWD                   string `json:"cwd"`
	ModelName             string `json:"modelName"`
	Error                 string `json:"error"`
	StepIdx               *int   `json:"stepIdx"`
	InvocationNum         *int   `json:"invocationNum"`
	InitialNumSteps       *int   `json:"initialNumSteps"`
	ToolCall              struct {
		ID   string          `json:"id"`
		Name string          `json:"name"`
		Args json.RawMessage `json:"args"`
	} `json:"toolCall"`
}

type rawQuestion struct {
	Question    string   `json:"question"`
	Options     []string `json:"options"`
	MultiSelect bool     `json:"is_multi_select"`
}

func parseQuestionsArg(raw json.RawMessage) []Question {
	var args struct {
		Questions json.RawMessage `json:"questions"`
	}
	if len(raw) == 0 || json.Unmarshal(raw, &args) != nil || len(args.Questions) == 0 {
		return nil
	}
	var rqs []rawQuestion
	if json.Unmarshal(args.Questions, &rqs) != nil {
		var encoded string
		if json.Unmarshal(args.Questions, &encoded) != nil || json.Unmarshal([]byte(encoded), &rqs) != nil {
			return nil
		}
	}
	qs := make([]Question, len(rqs))
	for i, rq := range rqs {
		qs[i] = Question(rq)
	}
	return qs
}

// ParseInput parses an Antigravity hook JSON payload from stdin. Unknown fields are ignored.
func ParseInput(data []byte) (Input, error) {
	var r rawInput
	if err := json.Unmarshal(data, &r); err != nil {
		return Input{}, fmt.Errorf("the hook input is not a JSON object: %w", err)
	}
	in := Input{
		HookEventName:         r.HookEventName,
		ConversationID:        r.ConversationID,
		TranscriptPath:        r.TranscriptPath,
		ArtifactDirectoryPath: r.ArtifactDirectoryPath,
		CWD:                   r.CWD,
		ModelName:             r.ModelName,
		Error:                 r.Error,
		StepIdx:               r.StepIdx,
		InvocationNum:         r.InvocationNum,
		InitialNumSteps:       r.InitialNumSteps,
	}
	in.ToolCall.ID = r.ToolCall.ID
	in.ToolCall.Name = r.ToolCall.Name
	if r.ToolCall.Name == askQuestionTool {
		in.ToolCall.Questions = parseQuestionsArg(r.ToolCall.Args)
	}
	return in, nil
}

var ratifyQuestionRE = regexp.MustCompile(`^IntentGate: Ratify the plan in (.+) \(sha256 ([0-9a-f]{64})\)\?$`)

// RatifyQuestionText is the question of the card for a plan at path with this SHA-256.
func RatifyQuestionText(path, sha256hex string) string {
	return "IntentGate: Ratify the plan in " + path + " (sha256 " + sha256hex + ")?"
}

// RenderCard is the ask_question item JSON for the ratify card, ending in a newline.
func RenderCard(path, sha256hex string) []byte {
	card := rawQuestion{
		Question:    RatifyQuestionText(path, sha256hex),
		Options:     []string{RatifyLabel, NotYetLabel},
		MultiSelect: false,
	}
	var b bytes.Buffer
	enc := json.NewEncoder(&b)
	enc.SetEscapeHTML(false)
	_ = enc.Encode(card)
	return b.Bytes()
}

// ParseCard recognises an exact ratify card: one question, not multi-select, options strictly
// ["Ratify", "Not yet"], and question matching the IntentGate ratify card pattern.
func ParseCard(qs []Question) (Card, bool) {
	if len(qs) != 1 || qs[0].MultiSelect || !slices.Equal(qs[0].Options, []string{RatifyLabel, NotYetLabel}) {
		return Card{}, false
	}
	m := ratifyQuestionRE.FindStringSubmatch(qs[0].Question)
	if m == nil {
		return Card{}, false
	}
	return Card{Question: qs[0].Question, Path: m[1], SHA256: m[2]}, true
}

// extractAnswer pulls the trimmed answer after "A1: " from a GENERIC step's content.
func extractAnswer(content string) string {
	norm := strings.ReplaceAll(content, "\r\n", "\n")
	for _, line := range strings.Split(norm, "\n") {
		if rest, ok := strings.CutPrefix(line, "A1: "); ok {
			return strings.TrimSpace(rest)
		}
	}
	return ""
}

// StepAnswer finds the GENERIC transcript entry at stepIdx and returns its trimmed A1 answer,
// whether the step was found, and whether status is DONE.
func StepAnswer(transcript []byte, stepIdx int) (answer string, found bool, done bool) {
	for _, line := range bytes.Split(transcript, []byte("\n")) {
		if len(bytes.TrimSpace(line)) == 0 {
			continue
		}
		var e transcriptEntry
		if json.Unmarshal(line, &e) != nil || e.Type != "GENERIC" || e.StepIndex == nil || *e.StepIndex != stepIdx {
			continue
		}
		return extractAnswer(e.Content), true, e.Status == "DONE"
	}
	return "", false, false
}

// Ratified reports whether an ask_question answer counts as ratification ("Ratify" or "Ratify, <note>").
func Ratified(answer string) bool {
	return answer == RatifyLabel || strings.HasPrefix(answer, RatifyLabel+", ")
}

type toolCallEntry struct {
	Name string          `json:"name"`
	Args json.RawMessage `json:"args"`
}

type transcriptEntry struct {
	StepIndex *int            `json:"step_index"`
	Source    string          `json:"source"`
	Type      string          `json:"type"`
	Status    string          `json:"status"`
	Model     string          `json:"model"`
	Content   string          `json:"content"`
	ToolCalls []toolCallEntry `json:"tool_calls"`
}

var wrapperREs = []*regexp.Regexp{
	regexp.MustCompile(`(?s)<t3_code_instructions>.*?</t3_code_instructions>`),
	regexp.MustCompile(`(?s)<runtime_info>.*?</runtime_info>`),
	regexp.MustCompile(`(?s)<ADDITIONAL_METADATA>.*?</ADDITIONAL_METADATA>`),
	regexp.MustCompile(`(?s)<USER_SETTINGS_CHANGE>.*?</USER_SETTINGS_CHANGE>`),
}

// cleanUserText strips T3 Code wrapper sections and tags from a USER_INPUT step's content.
func cleanUserText(raw string) string {
	s := raw
	for _, re := range wrapperREs {
		s = re.ReplaceAllString(s, "")
	}
	if start := strings.Index(s, "<user_request>"); start >= 0 {
		rest := s[start+len("<user_request>"):]
		if end := strings.LastIndex(rest, "</user_request>"); end >= 0 {
			s = rest[:end]
		}
	}
	s = strings.ReplaceAll(s, "<USER_REQUEST>", "")
	s = strings.ReplaceAll(s, "</USER_REQUEST>", "")
	return strings.TrimSpace(s)
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
func (s Scope) Describe() string {
	why := map[ScopeKind]string{
		ScopeSince:  "since the previous approved plan",
		ScopeReused: "reused from the previous plan (no new turns)",
		ScopeWhole:  "whole session (no earlier approved plan)",
	}[s.Kind]
	return fmt.Sprintf("root intent: %d human turn(s) %s", s.Turns, why)
}

// RootIntentScope extracts and scopes human turns and drafted_by from an Antigravity transcript.
func RootIntentScope(transcript []byte, currentStepIdx int) (Scope, error) {
	type cardUse struct {
		turns      int
		cardStep   int
		pairedStep int
		hasResult  bool
		ratified   bool
		inFlight   bool
	}
	var (
		turns        []string
		uses         []cardUse
		pendingSlots []int
		nextSlot     int
		draftedBy    string
	)
	closePending := func() {
		for _, s := range pendingSlots {
			if s >= 0 {
				uses[s].inFlight = false
			}
		}
		pendingSlots, nextSlot = nil, 0
	}
	for i, line := range bytes.Split(transcript, []byte("\n")) {
		if len(bytes.TrimSpace(line)) == 0 {
			continue
		}
		var e transcriptEntry
		if err := json.Unmarshal(line, &e); err != nil {
			return Scope{}, fmt.Errorf("transcript line %d is not valid JSON: %w", i+1, err)
		}
		stepIdx := -1
		hasStep := e.StepIndex != nil
		if hasStep {
			stepIdx = *e.StepIndex
		}
		if hasStep && stepIdx >= currentStepIdx {
			continue
		}
		switch e.Type {
		case "USER_INPUT":
			closePending()
			if e.Source == "USER_EXPLICIT" && (!hasStep || stepIdx < currentStepIdx) {
				if t := cleanUserText(e.Content); t != "" {
					turns = append(turns, t)
				}
			}
		case "PLANNER_RESPONSE":
			closePending()
			if !hasStep || stepIdx < currentStepIdx {
				if e.Model != "" {
					draftedBy = "antigravity/" + e.Model
				}
			}
			for _, tc := range e.ToolCalls {
				if tc.Name == askQuestionTool {
					if _, isCard := ParseCard(parseQuestionsArg(tc.Args)); isCard {
						pendingSlots = append(pendingSlots, len(uses))
						uses = append(uses, cardUse{turns: len(turns), cardStep: stepIdx, inFlight: true})
						continue
					}
				}
				pendingSlots = append(pendingSlots, -1)
			}
		case "GENERIC":
			if nextSlot < len(pendingSlots) {
				slot := pendingSlots[nextSlot]
				nextSlot++
				if slot >= 0 {
					uses[slot].pairedStep = stepIdx
					uses[slot].hasResult = true
					uses[slot].inFlight = false
					uses[slot].ratified = e.Status == "DONE" && Ratified(extractAnswer(e.Content))
				}
			}
		}
	}

	var bounds []int
	for _, u := range uses {
		if u.cardStep >= currentStepIdx {
			continue
		}
		if u.hasResult && u.pairedStep >= currentStepIdx {
			continue
		}
		if !u.hasResult && u.inFlight {
			continue
		}
		if u.hasResult && !u.ratified {
			continue
		}
		bounds = append(bounds, u.turns)
	}

	s := Scope{DraftedBy: draftedBy, Kind: ScopeWhole}
	picked := turns
	if n := len(bounds); n > 0 {
		from := bounds[n-1]
		picked, s.Kind = turns[from:], ScopeSince
		if len(picked) == 0 {
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

// PreOutput is what PreToolUse prints: always {"decision":"allow"}.
type PreOutput struct{}

// Render returns the PreToolUse JSON output and a newline.
func (PreOutput) Render() []byte {
	return []byte("{\"decision\":\"allow\"}\n")
}

// PostInvocationOutput is what PostInvocation prints: {} when Message is empty, else injectSteps.
type PostInvocationOutput struct {
	Message string
}

// Render returns the PostInvocation JSON output and a newline.
func (o PostInvocationOutput) Render() []byte {
	if o.Message == "" {
		return []byte("{}\n")
	}
	type msgBody struct {
		SystemMessage string            `json:"systemMessage"`
		Metadata      map[string]string `json:"metadata"`
	}
	type step struct {
		SystemMessage msgBody `json:"systemMessage"`
	}
	doc := struct {
		InjectSteps []step `json:"injectSteps"`
	}{
		InjectSteps: []step{{
			SystemMessage: msgBody{
				SystemMessage: o.Message,
				Metadata:      map[string]string{"user_markdown": o.Message},
			},
		}},
	}
	var b bytes.Buffer
	enc := json.NewEncoder(&b)
	enc.SetEscapeHTML(false)
	if enc.Encode(doc) != nil {
		return []byte("{}\n")
	}
	return b.Bytes()
}
