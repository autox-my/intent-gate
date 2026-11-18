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

// Input is the part of a PreToolUse or PostToolUse hook input the ExitPlanMode hooks use.
// ToolInput.Plan is set by PreToolUse and ToolResponse.Plan by PostToolUse.
type Input struct {
	HookEventName  string
	SessionID      string
	TranscriptPath string
	CWD            string
	ToolName       string
	ToolUseID      string
	ToolInput      struct{ Plan string }
	ToolResponse   struct{ Plan string }
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

// RootIntent returns the human turns of a JSONL transcript joined with TurnSeparator, in file
// order, and "claude-code/<model>" from the last assistant entry (empty if there is none). Human
// turns are "user" entries whose content is a string or text blocks; meta, sidechain, compaction
// summary and transcript-only entries, entries carrying a tool_result, interrupt markers and
// slash-command or shell-output records are skipped, and <system-reminder> spans are removed (see
// humanText); each kept turn is trimmed. A malformed line is an error; no turns is "".
func RootIntent(transcript []byte) (text, draftedBy string, err error) {
	var turns []string
	for i, line := range bytes.Split(transcript, []byte("\n")) {
		if len(bytes.TrimSpace(line)) == 0 {
			continue
		}
		var e entry
		if err := json.Unmarshal(line, &e); err != nil {
			return "", "", fmt.Errorf("transcript line %d is not valid JSON: %w", i+1, err)
		}
		switch e.Type {
		case "user":
			if e.IsMeta || e.IsSidechain || e.IsCompactSummary || e.IsVisibleInTranscriptOnly {
				continue
			}
			if t, ok := turnText(e.Message.Content); ok {
				if t, ok := humanText(t); ok {
					turns = append(turns, t)
				}
			}
		case "assistant":
			if !e.IsSidechain {
				draftedBy = ""
				if e.Message.Model != "" {
					draftedBy = "claude-code/" + e.Message.Model
				}
			}
		}
	}
	return strings.Join(turns, TurnSeparator), draftedBy, nil
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
