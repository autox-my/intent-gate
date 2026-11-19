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
		), "")
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
		), "")
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
		), "")
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
		), "")
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
		text, _, err := RootIntent(join(lines...), "")
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
		), "")
		if err != nil {
			t.Fatal(err)
		}
		if want := "Add SARIF.\n\n---\n\nKeep it small.\n\n---\n\nA  B C"; text != want {
			t.Errorf("text = %q, want %q", text, want)
		}
	})
	t.Run("draftedBy comes from the last assistant entry, empty when there is none", func(t *testing.T) {
		_, drafted, err := RootIntent(join(assistant("claude-sonnet-5-5", nil), user("x", nil), assistant("claude-opus-5-5", nil), user("y", nil)), "")
		if err != nil || drafted != "claude-code/claude-opus-5-5" {
			t.Errorf("draftedBy = %q, %v", drafted, err)
		}
		_, drafted, err = RootIntent(join(user("x", nil)), "")
		if err != nil || drafted != "" {
			t.Errorf("draftedBy = %q, %v; want empty", drafted, err)
		}
		_, drafted, err = RootIntent(join(assistant("", nil), user("x", nil)), "")
		if err != nil || drafted != "" {
			t.Errorf("an assistant entry with no model: draftedBy = %q, %v; want empty", drafted, err)
		}
	})
	t.Run("a malformed line is an error", func(t *testing.T) {
		if _, _, err := RootIntent(join(user("fine", nil), `{"type": "user", `, user("after", nil)), ""); err == nil {
			t.Error("want an error")
		}
	})
	t.Run("blank lines are fine and no turns is empty text", func(t *testing.T) {
		text, _, err := RootIntent([]byte("\n\n"+user("only", nil)+"\n\n"), "")
		if err != nil || text != "only" {
			t.Errorf("text = %q, %v", text, err)
		}
		text, _, err = RootIntent(nil, "")
		if err != nil || text != "" {
			t.Errorf("text = %q, %v; want empty", text, err)
		}
	})
}

func toolUse(id, name string) string {
	return line(m{"type": "assistant", "message": m{"role": "assistant", "model": "claude-opus-5-5",
		"content": []m{{"type": "tool_use", "id": id, "name": name, "input": m{}}}}})
}

func toolResult(id string, isError bool) string {
	return user([]m{{"type": "tool_result", "tool_use_id": id, "content": "x", "is_error": isError}}, nil)
}

func TestRootIntent_ScopedToCurrentPlan(t *testing.T) {
	join := func(lines ...string) []byte { return []byte(strings.Join(lines, "\n") + "\n") }
	const sep = TurnSeparator

	t.Run("only the turns after the last approved earlier plan, around a rejected one", func(t *testing.T) {
		text, _, err := RootIntent(join(
			user("ask A", nil),
			user("ask B", nil),
			toolUse("toolu_a", "ExitPlanMode"),
			toolResult("toolu_a", false),
			user("ask C", nil),
			toolUse("toolu_r", "ExitPlanMode"),
			toolResult("toolu_r", true), // rejected: not a boundary
			user("ask D", nil),
			toolUse("toolu_cur", "ExitPlanMode"),
		), "toolu_cur")
		if err != nil {
			t.Fatal(err)
		}
		if want := "ask C" + sep + "ask D"; text != want {
			t.Errorf("text = %q, want %q", text, want)
		}
	})
	t.Run("the last approved plan is the boundary, not the first", func(t *testing.T) {
		text, _, err := RootIntent(join(
			user("ask A", nil),
			toolUse("toolu_1", "ExitPlanMode"), toolResult("toolu_1", false),
			user("ask B", nil),
			toolUse("toolu_2", "ExitPlanMode"), toolResult("toolu_2", false),
			user("ask C", nil),
			toolUse("toolu_cur", "ExitPlanMode"),
		), "toolu_cur")
		if err != nil || text != "ask C" {
			t.Errorf("text = %q, %v; want %q", text, err, "ask C")
		}
	})
	t.Run("no earlier approved plan: every turn", func(t *testing.T) {
		text, _, err := RootIntent(join(
			user("ask A", nil),
			toolUse("toolu_r", "ExitPlanMode"), toolResult("toolu_r", true),
			user("ask B", nil),
			toolUse("toolu_cur", "ExitPlanMode"),
		), "toolu_cur")
		if err != nil || text != "ask A"+sep+"ask B" {
			t.Errorf("text = %q, %v", text, err)
		}
	})
	t.Run("the current tool use id is never a boundary, even once its result is in the transcript", func(t *testing.T) {
		text, _, err := RootIntent(join(
			user("ask A", nil),
			toolUse("toolu_cur", "ExitPlanMode"), toolResult("toolu_cur", false),
			user("ask B", nil),
		), "toolu_cur")
		if err != nil || text != "ask A"+sep+"ask B" {
			t.Errorf("text = %q, %v", text, err)
		}
	})
	t.Run("another tool's approved use is not a boundary", func(t *testing.T) {
		text, _, err := RootIntent(join(
			user("ask A", nil),
			toolUse("toolu_b", "Bash"), toolResult("toolu_b", false),
			user("ask B", nil),
			toolUse("toolu_cur", "ExitPlanMode"),
		), "toolu_cur")
		if err != nil || text != "ask A"+sep+"ask B" {
			t.Errorf("text = %q, %v", text, err)
		}
	})
	t.Run("a sidechain plan is not a boundary", func(t *testing.T) {
		sub := line(m{"type": "assistant", "isSidechain": true, "message": m{"role": "assistant",
			"content": []m{{"type": "tool_use", "id": "toolu_s", "name": "ExitPlanMode", "input": m{}}}}})
		text, _, err := RootIntent(join(
			user("ask A", nil), sub,
			user([]m{{"type": "tool_result", "tool_use_id": "toolu_s", "content": "x"}}, m{"isSidechain": true}),
			user("ask B", nil),
		), "toolu_cur")
		if err != nil || text != "ask A"+sep+"ask B" {
			t.Errorf("text = %q, %v", text, err)
		}
	})
	t.Run("the existing filters still apply after the boundary", func(t *testing.T) {
		text, drafted, err := RootIntent(join(
			user("old ask", nil),
			toolUse("toolu_a", "ExitPlanMode"), toolResult("toolu_a", false),
			user("meta", m{"isMeta": true}),
			user("[Request interrupted by user]", nil),
			user("new ask\n<system-reminder>x</system-reminder>", nil),
			toolUse("toolu_cur", "ExitPlanMode"),
		), "toolu_cur")
		if err != nil || text != "new ask" || drafted != "claude-code/claude-opus-5-5" {
			t.Errorf("text = %q, draftedBy = %q, %v", text, drafted, err)
		}
	})
}

func toolUses(ids ...string) string {
	var blocks []m
	for _, id := range ids {
		blocks = append(blocks, m{"type": "tool_use", "id": id, "name": "ExitPlanMode", "input": m{}})
	}
	return line(m{"type": "assistant", "message": m{"role": "assistant", "model": "claude-opus-5-5", "content": blocks}})
}

// toolResultText is a tool_result that is not an error, carrying text (a string, or text blocks).
func toolResultText(id string, content any) string {
	return user([]m{{"type": "tool_result", "tool_use_id": id, "content": content, "is_error": false}}, nil)
}

// Review 1 of S-17: boundary order, denial markers, missing results, empty scope, the scope line.
func TestRootIntent_ReviewOne(t *testing.T) {
	join := func(lines ...string) []byte { return []byte(strings.Join(lines, "\n") + "\n") }
	const sep = TurnSeparator
	check := func(t *testing.T, transcript []byte, want string, kind ScopeKind, turns int) {
		t.Helper()
		s, err := RootIntentScope(transcript, "toolu_cur")
		if err != nil {
			t.Fatal(err)
		}
		if s.Text != want || s.Kind != kind || s.Turns != turns {
			t.Errorf("scope = {%q, %s, %d turns}, want {%q, %s, %d turns}", s.Text, s.Kind, s.Turns, want, kind, turns)
		}
		if text, _, _ := RootIntent(transcript, "toolu_cur"); text != want {
			t.Errorf("RootIntent = %q, want %q", text, want)
		}
	}

	t.Run("a plan after the current one is not a boundary", func(t *testing.T) {
		check(t, join(
			user("ask A", nil),
			toolUse("toolu_cur", "ExitPlanMode"),
			user("ask B", nil),
			toolUse("toolu_later", "ExitPlanMode"), toolResult("toolu_later", false),
			user("ask C", nil),
		), "ask A"+sep+"ask B"+sep+"ask C", ScopeWhole, 3)
	})
	t.Run("same message: a plan after the current one is ignored", func(t *testing.T) {
		check(t, join(
			user("ask A", nil),
			toolUse("toolu_0", "ExitPlanMode"), toolResult("toolu_0", false),
			user("ask B", nil),
			toolUses("toolu_cur", "toolu_later"), toolResult("toolu_later", false),
		), "ask B", ScopeSince, 1)
	})
	t.Run("same message: a plan before the current one counts", func(t *testing.T) {
		// Its boundary leaves no turns after it, so the previous plan's turns are reused.
		check(t, join(
			user("ask A", nil),
			toolUse("toolu_0", "ExitPlanMode"), toolResult("toolu_0", false),
			user("ask B", nil),
			toolUses("toolu_first", "toolu_cur"), toolResult("toolu_first", false),
		), "ask B", ScopeReused, 1)
	})
	t.Run("a denial marker in a non-error result does not reset", func(t *testing.T) {
		for _, text := range []string{"The user doesn't want to proceed with this tool use.", "User rejected tool use", "The user REJECTED THE PLAN", "x"} {
			for _, content := range []any{text, []m{{"type": "text", "text": text}}} {
				want, kind := "ask A"+sep+"ask B", ScopeWhole
				if text == "x" { // no marker: approved, a boundary
					want, kind = "ask B", ScopeSince
				}
				check(t, join(
					user("ask A", nil),
					toolUse("toolu_d", "ExitPlanMode"), toolResultText("toolu_d", content),
					user("ask B", nil),
					toolUse("toolu_cur", "ExitPlanMode"),
				), want, kind, strings.Count(want, sep)+1)
			}
		}
	})
	t.Run("an error result with an ordinary text is still a rejection", func(t *testing.T) {
		check(t, join(
			user("ask A", nil),
			toolUse("toolu_r", "ExitPlanMode"), toolResult("toolu_r", true),
			user("ask B", nil),
			toolUse("toolu_cur", "ExitPlanMode"),
		), "ask A"+sep+"ask B", ScopeWhole, 2)
	})
	t.Run("a plan with no result is a boundary", func(t *testing.T) {
		check(t, join(
			user("ask A", nil),
			toolUse("toolu_gone", "ExitPlanMode"),
			user("ask B", nil),
			toolUse("toolu_cur", "ExitPlanMode"),
		), "ask B", ScopeSince, 1)
	})
	t.Run("a result that only comes after the current use does not count", func(t *testing.T) {
		check(t, join(
			user("ask A", nil),
			toolUse("toolu_late", "ExitPlanMode"),
			toolUse("toolu_cur", "ExitPlanMode"),
			toolResult("toolu_late", true),
			user("ask B", nil),
		), "ask A"+sep+"ask B", ScopeReused, 2)
	})
	t.Run("no turn since the boundary: the previous plan's turns are reused", func(t *testing.T) {
		check(t, join(
			user("ask A", nil),
			toolUse("toolu_1", "ExitPlanMode"), toolResult("toolu_1", false),
			user("ask B", nil), user("ask C", nil),
			toolUse("toolu_2", "ExitPlanMode"), toolResult("toolu_2", false),
			toolUse("toolu_cur", "ExitPlanMode"),
		), "ask B"+sep+"ask C", ScopeReused, 2)
	})
	t.Run("no turn since the only boundary: the turns before it are reused", func(t *testing.T) {
		check(t, join(
			user("ask A", nil), user("ask B", nil),
			toolUse("toolu_1", "ExitPlanMode"), toolResult("toolu_1", false),
			toolUse("toolu_cur", "ExitPlanMode"),
		), "ask A"+sep+"ask B", ScopeReused, 2)
	})
	t.Run("turns since the boundary: the scope is since", func(t *testing.T) {
		check(t, join(
			user("ask A", nil),
			toolUse("toolu_1", "ExitPlanMode"), toolResult("toolu_1", false),
			user("ask B", nil),
			toolUse("toolu_cur", "ExitPlanMode"),
		), "ask B", ScopeSince, 1)
	})
}

func TestScope_Describe(t *testing.T) {
	for _, c := range []struct {
		s    Scope
		want string
	}{
		{Scope{Kind: ScopeSince, Turns: 2}, "root intent: 2 human turn(s) since the previous approved plan"},
		{Scope{Kind: ScopeReused, Turns: 1}, "root intent: 1 human turn(s) reused from the previous plan (no new turns)"},
		{Scope{Kind: ScopeWhole, Turns: 3}, "root intent: 3 human turn(s) whole session (no earlier approved plan)"},
	} {
		if got := c.s.Describe(); got != c.want {
			t.Errorf("Describe() = %q, want %q", got, c.want)
		}
	}
}
