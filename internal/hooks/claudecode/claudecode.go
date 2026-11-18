// Package claudecode is the pure part of the Claude Code hook: it reads Claude Code's hook input and
// transcript. The formats are Claude Code's, not ours, so unknown fields are ignored.
package claudecode

// Input is the part of a PreToolUse or PostToolUse hook input the ExitPlanMode hooks use.
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

// ParseInput reads a hook input from JSON.
func ParseInput(data []byte) (Input, error) { return Input{}, nil }

// RootIntent returns the human turns of a JSONL transcript, joined, and who drafted the plan.
func RootIntent(transcript []byte) (text, draftedBy string, err error) { return "", "", nil }

// Output is what a hook prints: one JSON object.
type Output struct{ SystemMessage string }

// Render returns the JSON object and a newline.
func (o Output) Render() []byte { return nil }
