package claudecode

import (
	"encoding/json"
	"strings"
	"testing"
)

// The fixtures follow the structure of Claude Code's hook inputs and transcripts, with synthetic text.

const preInput = `{
  "session_id": "sess-1",
  "transcript_path": "/tmp/t.jsonl",
  "cwd": "/work/repo",
  "permission_mode": "plan",
  "hook_event_name": "PreToolUse",
  "tool_name": "ExitPlanMode",
  "tool_input": {"plan": "# Plan\nDo it.", "planFilePath": "/home/u/.claude/plans/x.md", "extra": [1, 2]},
  "tool_use_id": "toolu_01",
  "something_new": {"a": true}
}`

const postInput = `{
  "session_id": "sess-1",
  "transcript_path": "/tmp/t.jsonl",
  "cwd": "/work/repo",
  "hook_event_name": "PostToolUse",
  "tool_name": "ExitPlanMode",
  "tool_input": {"plan": "# Old plan"},
  "tool_response": {"plan": "# Approved plan", "filePath": "/home/u/.claude/plans/x.md", "isAgent": false},
  "tool_use_id": "toolu_01",
  "duration_ms": 12
}`

func TestParseInput(t *testing.T) {
	t.Run("PreToolUse", func(t *testing.T) {
		in, err := ParseInput([]byte(preInput))
		if err != nil {
			t.Fatal(err)
		}
		if in.HookEventName != "PreToolUse" || in.SessionID != "sess-1" || in.TranscriptPath != "/tmp/t.jsonl" ||
			in.CWD != "/work/repo" || in.ToolName != "ExitPlanMode" || in.ToolUseID != "toolu_01" {
			t.Errorf("fields: %+v", in)
		}
		if in.ToolInput.Plan != "# Plan\nDo it." || in.ToolResponse.Plan != "" {
			t.Errorf("plans: input %q, response %q", in.ToolInput.Plan, in.ToolResponse.Plan)
		}
	})
	t.Run("PostToolUse", func(t *testing.T) {
		in, err := ParseInput([]byte(postInput))
		if err != nil {
			t.Fatal(err)
		}
		if in.HookEventName != "PostToolUse" || in.ToolResponse.Plan != "# Approved plan" || in.ToolInput.Plan != "# Old plan" {
			t.Errorf("fields: %+v", in)
		}
	})
	t.Run("another tool's response may be a string or a list", func(t *testing.T) {
		for _, resp := range []string{`"done"`, `[1,2]`, `null`, `12`} {
			in, err := ParseInput([]byte(`{"hook_event_name":"PostToolUse","tool_name":"Bash","tool_input":"ls","tool_response":` + resp + `}`))
			if err != nil {
				t.Errorf("tool_response %s: %v", resp, err)
			}
			if in.ToolName != "Bash" {
				t.Errorf("tool_response %s: ToolName %q", resp, in.ToolName)
			}
		}
	})
	t.Run("not JSON, or not an object, is an error", func(t *testing.T) {
		for _, bad := range []string{``, `not json`, `[1]`, `"x"`, `{"session_id": `} {
			if _, err := ParseInput([]byte(bad)); err == nil {
				t.Errorf("ParseInput(%q): want an error", bad)
			}
		}
	})
}

func TestOutputRender(t *testing.T) {
	got := string(Output{SystemMessage: "IntentGate: \"x\" <y>\n"}.Render())
	if !strings.HasSuffix(got, "\n") || strings.Count(got, "\n") != 1 {
		t.Errorf("want one line ending in a newline, got %q", got)
	}
	var m map[string]string
	if err := json.Unmarshal([]byte(got), &m); err != nil || m["systemMessage"] != "IntentGate: \"x\" <y>\n" || len(m) != 1 {
		t.Errorf("Render = %q (%v), want {\"systemMessage\": ...}", got, err)
	}
	if empty := string(Output{}.Render()); empty != "{}\n" {
		t.Errorf("an empty Output renders %q, want {}", empty)
	}
}

func line(v any) string {
	b, err := json.Marshal(v)
	if err != nil {
		panic(err)
	}
	return string(b)
}

type m = map[string]any

func user(content any, extra m) string {
	e := m{"type": "user", "message": m{"role": "user", "content": content}}
	for k, v := range extra {
		e[k] = v
	}
	return line(e)
}

func assistant(model string, extra m) string {
	e := m{"type": "assistant", "message": m{"role": "assistant", "model": model, "content": []m{{"type": "text", "text": "ok"}}}}
	for k, v := range extra {
		e[k] = v
	}
	return line(e)
}

func TestRootIntent(t *testing.T) {
	join := func(lines ...string) []byte { return []byte(strings.Join(lines, "\n") + "\n") }

	t.Run("string and block content, in file order", func(t *testing.T) {
		text, drafted, err := RootIntent(join(
			user("First ask.", nil),
			assistant("claude-opus-5-5", nil),
			user([]m{{"type": "text", "text": "Second"}, {"type": "text", "text": "ask."}}, nil),
		))
		if err != nil {
			t.Fatal(err)
		}
		if want := "First ask.\n\n---\n\nSecond\nask."; text != want {
			t.Errorf("text = %q, want %q", text, want)
		}
		if drafted != "claude-code/claude-opus-5-5" {
			t.Errorf("draftedBy = %q", drafted)
		}
	})
	t.Run("meta, sidechain and tool_result entries are skipped", func(t *testing.T) {
		text, _, err := RootIntent(join(
			user("kept one", nil),
			user("meta caveat", m{"isMeta": true}),
			user("subagent prompt", m{"isSidechain": true}),
			user([]m{{"type": "tool_result", "tool_use_id": "t1", "content": "output"}}, nil),
			user([]m{{"type": "text", "text": "mixed"}, {"type": "tool_result", "tool_use_id": "t2", "content": "x"}}, nil),
			line(m{"type": "attachment", "attachment": m{"type": "x"}}),
			line(m{"type": "last-prompt", "lastPrompt": "not a turn"}),
			user("kept two", m{"isMeta": false, "isSidechain": false}),
		))
		if err != nil {
			t.Fatal(err)
		}
		if want := "kept one\n\n---\n\nkept two"; text != want {
			t.Errorf("text = %q, want %q", text, want)
		}
	})
	t.Run("review 1: compaction summaries and transcript-only entries are skipped", func(t *testing.T) {
		text, _, err := RootIntent(join(
			user("kept one", nil),
			user("This session is being continued from a previous conversation...", m{"isCompactSummary": true}),
			user("shown in the transcript only", m{"isVisibleInTranscriptOnly": true}),
			user("kept two", m{"isCompactSummary": false, "isVisibleInTranscriptOnly": false}),
		))
		if err != nil {
			t.Fatal(err)
		}
		if want := "kept one\n\n---\n\nkept two"; text != want {
			t.Errorf("text = %q, want %q", text, want)
		}
	})
	t.Run("review 1: interrupt markers are skipped, but only when they are the whole text", func(t *testing.T) {
		text, _, err := RootIntent(join(
			user("kept one", nil),
			user("[Request interrupted by user]", nil),
			user([]m{{"type": "text", "text": "[Request interrupted by user for tool use]"}}, nil),
			user("  [Request interrupted by user]\n", nil),
			user("I wrote [Request interrupted by user] on purpose", nil),
		))
		if err != nil {
			t.Fatal(err)
		}
		if want := "kept one\n\n---\n\nI wrote [Request interrupted by user] on purpose"; text != want {
			t.Errorf("text = %q, want %q", text, want)
		}
	})
	t.Run("review 1: command and shell-output entries are skipped", func(t *testing.T) {
		in := [][]string{}
		for _, prefix := range []string{"<command-name>", "<command-message>", "<command-args>", "<local-command-stdout>",
			"<local-command-stderr>", "<bash-stdout>", "<bash-stderr>"} {
			in = append(in, []string{prefix + "payload"})
		}
		lines := []string{user("kept one", nil)}
		for _, c := range in {
			lines = append(lines, user(c[0], nil))
			lines = append(lines, user([]m{{"type": "text", "text": "\n" + c[0]}}, nil))
		}
		lines = append(lines, user("mentions <command-name> mid-sentence", nil), user("<bash-input>ls</bash-input>", nil))
		text, _, err := RootIntent(join(lines...))
		if err != nil {
			t.Fatal(err)
		}
		// <bash-input> is not on the list: it is the human's own typed command, so it stays.
		if want := "kept one\n\n---\n\nmentions <command-name> mid-sentence\n\n---\n\n<bash-input>ls</bash-input>"; text != want {
			t.Errorf("text = %q, want %q", text, want)
		}
	})
	t.Run("review 1: system-reminder spans are removed and emptied turns dropped", func(t *testing.T) {
		text, _, err := RootIntent(join(
			user("Add SARIF.\n<system-reminder>\nthe date is X\nmore\n</system-reminder>", nil),
			user("<system-reminder>only a reminder</system-reminder>", nil),
			user([]m{{"type": "text", "text": "<system-reminder>a</system-reminder>"}, {"type": "text", "text": "Keep it small."}, {"type": "text", "text": "<system-reminder>b</system-reminder>"}}, nil),
			user("   \n  ", nil),
			user("A <system-reminder>x</system-reminder> B <system-reminder>y</system-reminder>C", nil),
		))
		if err != nil {
			t.Fatal(err)
		}
		if want := "Add SARIF.\n\n---\n\nKeep it small.\n\n---\n\nA  B C"; text != want {
			t.Errorf("text = %q, want %q", text, want)
		}
	})
	t.Run("draftedBy comes from the last assistant entry, empty when there is none", func(t *testing.T) {
		_, drafted, err := RootIntent(join(assistant("claude-sonnet-5-5", nil), user("x", nil), assistant("claude-opus-5-5", nil), user("y", nil)))
		if err != nil || drafted != "claude-code/claude-opus-5-5" {
			t.Errorf("draftedBy = %q, %v", drafted, err)
		}
		_, drafted, err = RootIntent(join(user("x", nil)))
		if err != nil || drafted != "" {
			t.Errorf("draftedBy = %q, %v; want empty", drafted, err)
		}
		_, drafted, err = RootIntent(join(assistant("", nil), user("x", nil)))
		if err != nil || drafted != "" {
			t.Errorf("an assistant entry with no model: draftedBy = %q, %v; want empty", drafted, err)
		}
	})
	t.Run("a malformed line is an error", func(t *testing.T) {
		if _, _, err := RootIntent(join(user("fine", nil), `{"type": "user", `, user("after", nil))); err == nil {
			t.Error("want an error")
		}
	})
	t.Run("blank lines are fine and no turns is empty text", func(t *testing.T) {
		text, _, err := RootIntent([]byte("\n\n" + user("only", nil) + "\n\n"))
		if err != nil || text != "only" {
			t.Errorf("text = %q, %v", text, err)
		}
		text, _, err = RootIntent(nil)
		if err != nil || text != "" {
			t.Errorf("text = %q, %v; want empty", text, err)
		}
	})
}
