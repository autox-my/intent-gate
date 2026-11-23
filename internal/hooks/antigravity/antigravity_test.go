package antigravity

import (
	"encoding/json"
	"strings"
	"testing"
)

func testSHA(ch byte) string { return strings.Repeat(string(ch), 64) }

func cardJSONLine(step int, q string) string {
	b, _ := json.Marshal(map[string]any{
		"step_index": step,
		"source":     "MODEL",
		"type":       "PLANNER_RESPONSE",
		"status":     "DONE",
		"model":      "gemini-pro-orchestrator-neon",
		"tool_calls": []any{map[string]any{
			"name": "ask_question",
			"args": map[string]any{
				"questions": []any{map[string]any{
					"question":        q,
					"options":         []string{"Ratify", "Not yet"},
					"is_multi_select": false,
				}},
			},
		}},
	})
	return string(b)
}

func genericStepLine(step int, status, content string) string {
	b, _ := json.Marshal(map[string]any{
		"step_index": step,
		"source":     "MODEL",
		"type":       "GENERIC",
		"status":     status,
		"content":    content,
	})
	return string(b)
}

func userInputLine(step int, source, content string) string {
	b, _ := json.Marshal(map[string]any{
		"step_index": step,
		"source":     source,
		"type":       "USER_INPUT",
		"status":     "DONE",
		"content":    content,
	})
	return string(b)
}

func plannerLine(step int, model string) string {
	b, _ := json.Marshal(map[string]any{
		"step_index": step,
		"source":     "MODEL",
		"type":       "PLANNER_RESPONSE",
		"status":     "DONE",
		"model":      model,
	})
	return string(b)
}

func TestParseCard_AndRender(t *testing.T) {
	sha := testSHA('a')
	raw := RenderCard("docs/specs/S-26&more.md", sha)
	if !strings.HasSuffix(string(raw), "\n") {
		t.Fatalf("RenderCard must end with newline: %q", raw)
	}
	if strings.Contains(string(raw), `\u0026`) {
		t.Fatalf("RenderCard escaped HTML: %s", raw)
	}

	var decoded struct {
		Question    string   `json:"question"`
		Options     []string `json:"options"`
		MultiSelect bool     `json:"is_multi_select"`
	}
	dec := json.NewDecoder(strings.NewReader(string(raw)))
	dec.DisallowUnknownFields()
	if err := dec.Decode(&decoded); err != nil || dec.More() {
		t.Fatalf("RenderCard invalid JSON %q: %v", raw, err)
	}
	if decoded.Question != RatifyQuestionText("docs/specs/S-26&more.md", sha) {
		t.Errorf("question = %q", decoded.Question)
	}
	if decoded.MultiSelect || len(decoded.Options) != 2 || decoded.Options[0] != "Ratify" || decoded.Options[1] != "Not yet" {
		t.Errorf("decoded = %+v", decoded)
	}

	c, ok := ParseCard([]Question{{
		Question:    decoded.Question,
		Options:     decoded.Options,
		MultiSelect: decoded.MultiSelect,
	}})
	if !ok || c.Path != "docs/specs/S-26&more.md" || c.SHA256 != sha || c.Question != decoded.Question {
		t.Fatalf("ParseCard round-trip = (%+v, %v)", c, ok)
	}

	validQ := RatifyQuestionText("docs/plan.md", sha)
	for _, tc := range []struct {
		name string
		qs   []Question
	}{
		{"zero questions", nil},
		{"two questions", []Question{
			{Question: validQ, Options: []string{"Ratify", "Not yet"}},
			{Question: "Other?", Options: []string{"Yes", "No"}},
		}},
		{"wrong option label", []Question{{Question: validQ, Options: []string{"Ratify", "Later"}}}},
		{"single option", []Question{{Question: validQ, Options: []string{"Ratify"}}}},
		{"extra option", []Question{{Question: validQ, Options: []string{"Ratify", "Not yet", "Cancel"}}}},
		{"reversed order", []Question{{Question: validQ, Options: []string{"Not yet", "Ratify"}}}},
		{"multi_select true", []Question{{Question: validQ, Options: []string{"Ratify", "Not yet"}, MultiSelect: true}}},
		{"missing IntentGate prefix", []Question{{Question: "Ratify the plan in docs/plan.md (sha256 " + sha + ")?", Options: []string{"Ratify", "Not yet"}}}},
		{"bad sha256 length", []Question{{Question: "IntentGate: Ratify the plan in docs/plan.md (sha256 abcdef)?", Options: []string{"Ratify", "Not yet"}}}},
		{"uppercase sha256", []Question{{Question: "IntentGate: Ratify the plan in docs/plan.md (sha256 " + strings.ToUpper(sha) + ")?", Options: []string{"Ratify", "Not yet"}}}},
	} {
		if _, ok := ParseCard(tc.qs); ok {
			t.Errorf("%s: ParseCard returned true, want false", tc.name)
		}
	}
}

func TestParseInput(t *testing.T) {
	t.Run("PreToolUse ask_question with array questions and stepIdx 0", func(t *testing.T) {
		payload := `{
			"hookEventName": "PreToolUse",
			"conversationId": "conv-1",
			"transcriptPath": "/tmp/transcript_full.jsonl",
			"artifactDirectoryPath": "/tmp/artifacts",
			"cwd": "/repo",
			"modelName": "gemini-pro-orchestrator-neon",
			"stepIdx": 0,
			"toolCall": {
				"id": "call_1",
				"name": "ask_question",
				"args": {
					"questions": [
						{"question": "Q?", "options": ["Ratify", "Not yet"], "is_multi_select": false}
					]
				}
			}
		}`
		in, err := ParseInput([]byte(payload))
		if err != nil {
			t.Fatalf("ParseInput: %v", err)
		}
		if in.HookEventName != "PreToolUse" || in.ConversationID != "conv-1" || in.CWD != "/repo" || in.ModelName != "gemini-pro-orchestrator-neon" {
			t.Errorf("parsed fields = %+v", in)
		}
		if in.StepIdx == nil || *in.StepIdx != 0 {
			t.Fatalf("StepIdx = %v, want 0", in.StepIdx)
		}
		if in.ToolCall.ID != "call_1" || in.ToolCall.Name != "ask_question" || len(in.ToolCall.Questions) != 1 {
			t.Fatalf("ToolCall = %+v", in.ToolCall)
		}
		if in.ToolCall.Questions[0].Question != "Q?" || len(in.ToolCall.Questions[0].Options) != 2 {
			t.Errorf("Questions = %+v", in.ToolCall.Questions)
		}
	})

	t.Run("PreToolUse ask_question with string-encoded questions", func(t *testing.T) {
		payload := `{
			"hookEventName": "PreToolUse",
			"conversationId": "conv-1",
			"stepIdx": 78,
			"toolCall": {
				"id": "call_2",
				"name": "ask_question",
				"args": {
					"questions": "[{\"question\":\"Q2?\",\"options\":[\"Ratify\",\"Not yet\"],\"is_multi_select\":false}]"
				}
			}
		}`
		in, err := ParseInput([]byte(payload))
		if err != nil {
			t.Fatalf("ParseInput: %v", err)
		}
		if len(in.ToolCall.Questions) != 1 || in.ToolCall.Questions[0].Question != "Q2?" {
			t.Errorf("Questions = %+v", in.ToolCall.Questions)
		}
	})

	t.Run("PostInvocation fields", func(t *testing.T) {
		payload := `{
			"hookEventName": "PostInvocation",
			"conversationId": "conv-1",
			"transcriptPath": "/tmp/transcript_full.jsonl",
			"cwd": "/repo",
			"modelName": "gemini-flash-worker-agy",
			"invocationNum": 10,
			"initialNumSteps": 85
		}`
		in, err := ParseInput([]byte(payload))
		if err != nil {
			t.Fatalf("ParseInput: %v", err)
		}
		if in.HookEventName != "PostInvocation" || in.StepIdx != nil {
			t.Errorf("parsed = %+v", in)
		}
		if in.InvocationNum == nil || *in.InvocationNum != 10 || in.InitialNumSteps == nil || *in.InitialNumSteps != 85 {
			t.Errorf("InvocationNum=%v InitialNumSteps=%v", in.InvocationNum, in.InitialNumSteps)
		}
	})

	t.Run("malformed JSON returns error", func(t *testing.T) {
		if _, err := ParseInput([]byte("{bad")); err == nil {
			t.Error("expected error for malformed JSON")
		}
	})
}

func TestStepAnswer_AndRatified(t *testing.T) {
	transcript := strings.Join([]string{
		genericStepLine(10, "DONE", "Created At: x\nCompleted At: y\nA1: Ratify"),
		genericStepLine(11, "DONE", "Created At: x\r\nCompleted At: y\r\nA1: Ratify, yes\r\n"),
		genericStepLine(12, "DONE", "A1: Ratify, looks good"),
		genericStepLine(13, "DONE", "A1: Not yet"),
		genericStepLine(14, "DONE", "A1: Not yet, Ratify later"),
		genericStepLine(15, "DONE", "A1: Ratify later"),
		genericStepLine(16, "DONE", "A1: Ratify?"),
		genericStepLine(17, "RUNNING", "A1: Ratify"),
		userInputLine(18, "USER_EXPLICIT", "A1: Ratify"),
	}, "\n") + "\n"

	for _, tc := range []struct {
		step     int
		wantAns  string
		wantFind bool
		wantDone bool
		wantRat  bool
	}{
		{10, "Ratify", true, true, true},
		{11, "Ratify, yes", true, true, true},
		{12, "Ratify, looks good", true, true, true},
		{13, "Not yet", true, true, false},
		{14, "Not yet, Ratify later", true, true, false},
		{15, "Ratify later", true, true, false},
		{16, "Ratify?", true, true, false},
		{17, "Ratify", true, false, true},
		{18, "", false, false, false},
		{99, "", false, false, false},
	} {
		ans, found, done := StepAnswer([]byte(transcript), tc.step)
		if ans != tc.wantAns || found != tc.wantFind || done != tc.wantDone {
			t.Errorf("step %d: got (%q, %v, %v), want (%q, %v, %v)", tc.step, ans, found, done, tc.wantAns, tc.wantFind, tc.wantDone)
		}
		if found && done {
			if got := Ratified(ans); got != tc.wantRat {
				t.Errorf("step %d Ratified(%q) = %v, want %v", tc.step, ans, got, tc.wantRat)
			}
		} else if tc.step == 17 && !Ratified(ans) {
			t.Errorf("Ratified(%q) = false, want true", ans)
		}
	}
}

func TestRootIntentScope(t *testing.T) {
	sha := testSHA('b')
	q1 := RatifyQuestionText("docs/specs/S-25.md", sha)
	q2 := RatifyQuestionText("docs/specs/S-26.md", sha)

	t.Run("whole session with T3 wrapper stripping and model tracking", func(t *testing.T) {
		wrapped := "<t3_code_instructions>\nrules\n</t3_code_instructions>\n<USER_REQUEST>\n<t3_code_instructions>dup</t3_code_instructions>\n<user_request>\nBuild adapter\n</user_request><runtime_info>info</runtime_info>\n</USER_REQUEST>\n<ADDITIONAL_METADATA>\ntime\n</ADDITIONAL_METADATA>"
		wrappedNoInner := "<USER_REQUEST>\n<t3_code_instructions>\nrules\n</t3_code_instructions>\n<runtime_info>info</runtime_info>\nKeep it minimal\n</USER_REQUEST><USER_SETTINGS_CHANGE>x</USER_SETTINGS_CHANGE>"
		lines := []string{
			userInputLine(0, "USER_EXPLICIT", wrapped),
			userInputLine(1, "SYSTEM", "ignored system input"),
			plannerLine(2, "gemini-pro-orchestrator-neon"),
			userInputLine(3, "USER_EXPLICIT", wrappedNoInner),
			cardJSONLine(4, q2), // current card at currentStepIdx-1 (PreToolUse) must not be a boundary
		}
		sc, err := RootIntentScope([]byte(strings.Join(lines, "\n")+"\n"), 5)
		if err != nil {
			t.Fatalf("RootIntentScope: %v", err)
		}
		want := "Build adapter" + TurnSeparator + "Keep it minimal"
		if sc.Text != want || sc.Turns != 2 || sc.Kind != ScopeWhole || sc.DraftedBy != "antigravity/gemini-pro-orchestrator-neon" {
			t.Errorf("got %+v, want text %q", sc, want)
		}
	})

	t.Run("since previous ratified card, ignoring rejected cards and current card", func(t *testing.T) {
		lines := []string{
			userInputLine(0, "USER_EXPLICIT", "First slice ask"),
			cardJSONLine(1, q1),
			genericStepLine(2, "DONE", "A1: Ratify, yes"),
			userInputLine(3, "USER_EXPLICIT", "Second slice ask"),
			cardJSONLine(4, q2),
			genericStepLine(5, "DONE", "A1: Not yet"), // rejected card: must NOT reset scope
			userInputLine(6, "USER_EXPLICIT", "Clarified ask"),
			cardJSONLine(7, q2), // current card paired with step 8
			genericStepLine(8, "DONE", "A1: Ratify"),
		}
		sc, err := RootIntentScope([]byte(strings.Join(lines, "\n")+"\n"), 8)
		if err != nil {
			t.Fatalf("RootIntentScope: %v", err)
		}
		want := "Second slice ask" + TurnSeparator + "Clarified ask"
		if sc.Text != want || sc.Turns != 2 || sc.Kind != ScopeSince {
			t.Errorf("got %+v, want text %q, ScopeSince", sc, want)
		}
	})

	t.Run("reused when no new turns after previous ratified card", func(t *testing.T) {
		lines := []string{
			userInputLine(0, "USER_EXPLICIT", "First ask"),
			cardJSONLine(1, q1),
			genericStepLine(2, "DONE", "A1: Ratify"),
			cardJSONLine(3, q2),
			genericStepLine(4, "DONE", "A1: Ratify"),
		}
		sc, err := RootIntentScope([]byte(strings.Join(lines, "\n")+"\n"), 4)
		if err != nil {
			t.Fatalf("RootIntentScope: %v", err)
		}
		if sc.Text != "First ask" || sc.Turns != 1 || sc.Kind != ScopeReused {
			t.Errorf("got %+v, want reused 'First ask'", sc)
		}
	})

	t.Run("truncated earlier card without result acts as boundary", func(t *testing.T) {
		lines := []string{
			userInputLine(0, "USER_EXPLICIT", "Old ask"),
			cardJSONLine(1, q1), // unanswered before next turn/ask -> boundary
			userInputLine(2, "USER_EXPLICIT", "New ask"),
			cardJSONLine(3, q2),
			genericStepLine(4, "DONE", "A1: Ratify"),
		}
		sc, err := RootIntentScope([]byte(strings.Join(lines, "\n")+"\n"), 4)
		if err != nil {
			t.Fatalf("RootIntentScope: %v", err)
		}
		if sc.Text != "New ask" || sc.Turns != 1 || sc.Kind != ScopeSince {
			t.Errorf("got %+v, want 'New ask'", sc)
		}
	})

	t.Run("parallel tool call with in-flight card in PreToolUse is not a prior boundary", func(t *testing.T) {
		multi, _ := json.Marshal(map[string]any{
			"step_index": 3,
			"source":     "MODEL",
			"type":       "PLANNER_RESPONSE",
			"status":     "DONE",
			"model":      "gemini-pro-orchestrator-neon",
			"tool_calls": []any{
				map[string]any{"name": "read_file", "args": map[string]any{"Path": "go.mod"}},
				map[string]any{"name": "ask_question", "args": map[string]any{"questions": []any{map[string]any{
					"question": q2, "options": []string{"Ratify", "Not yet"}, "is_multi_select": false,
				}}}},
			},
		})
		lines := []string{
			userInputLine(0, "USER_EXPLICIT", "First ask"),
			cardJSONLine(1, q1),
			genericStepLine(2, "DONE", "A1: Ratify"),
			string(multi),
			genericStepLine(4, "DONE", "read_file output"),
		}
		sc, err := RootIntentScope([]byte(strings.Join(lines, "\n")+"\n"), 5)
		if err != nil {
			t.Fatalf("RootIntentScope: %v", err)
		}
		if sc.Text != "First ask" || sc.Turns != 1 || sc.Kind != ScopeReused {
			t.Errorf("got %+v, want reused 'First ask'", sc)
		}
	})

	t.Run("malformed JSONL line returns error", func(t *testing.T) {
		if _, err := RootIntentScope([]byte("{not json\n"), 1); err == nil {
			t.Error("expected error for malformed JSONL")
		}
	})
}

func TestOutputs(t *testing.T) {
	if got := string(PreOutput{}.Render()); got != "{\"decision\":\"allow\"}\n" {
		t.Errorf("PreOutput = %q", got)
	}
	if got := string(PostInvocationOutput{}.Render()); got != "{}\n" {
		t.Errorf("empty PostInvocationOutput = %q", got)
	}
	msg := "IntentGate: intent lock written: .intent/01.yml <tag>"
	out := string(PostInvocationOutput{Message: msg}.Render())
	if strings.Contains(out, `\u003c`) {
		t.Fatalf("PostInvocationOutput escaped HTML: %s", out)
	}
	var parsed struct {
		InjectSteps []struct {
			SystemMessage struct {
				SystemMessage string            `json:"systemMessage"`
				Metadata      map[string]string `json:"metadata"`
			} `json:"systemMessage"`
		} `json:"injectSteps"`
	}
	if err := json.Unmarshal([]byte(out), &parsed); err != nil {
		t.Fatalf("unmarshal PostInvocationOutput %q: %v", out, err)
	}
	if len(parsed.InjectSteps) != 1 || parsed.InjectSteps[0].SystemMessage.SystemMessage != msg ||
		parsed.InjectSteps[0].SystemMessage.Metadata["user_markdown"] != msg {
		t.Errorf("parsed = %+v", parsed)
	}
}
