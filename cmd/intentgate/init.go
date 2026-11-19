package main

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"reflect"
	"strings"

	"github.com/autox-my/intent-gate/internal/gitx"
)

// `intentgate init` (ratification.md §1.1 and §2): set a repository up for IntentGate, so sessions
// start in plan mode and agents write a well-formed `intentgate` block. It never needs a terminal
// and never writes a lock.
//
// Exit codes: 0; 2 usage, or not in a git repository; 3 an existing file is unreadable or invalid
// (nothing is written then).

const (
	initUsage = "usage: intentgate init [--agent claude-code|generic] [--dry-run]\n"

	settingsPath = ".claude/settings.json"
	configPath   = ".intentgate.yml"
	configBody   = "version: 1\n"

	sectionBegin = "<!-- intentgate:begin -->"
	sectionEnd   = "<!-- intentgate:end -->"

	defaultModeHint = "set it to plan to start sessions in plan mode"
)

// initFile is one planned file: what happens to it, and the bytes to write (nil: nothing to write).
type initFile struct {
	status  string // created, updated, unchanged or kept
	path    string // repo-relative, slash-separated
	target  string // absolute path to write: path, or what a symlink at path resolves to
	orig    []byte // the bytes an existing file had (updated files)
	notes   []string
	content []byte
}

func runInit(args []string, stdout io.Writer, deps Deps) int {
	t := newTerm("init", nil, stdout, deps)
	fs := newFlags("init")
	agent := fs.String("agent", "claude-code", "")
	dry := fs.Bool("dry-run", false, "")
	if err := fs.Parse(args); err != nil || fs.NArg() > 0 {
		return t.usage(initUsage, "")
	}
	if *agent != "claude-code" && *agent != "generic" {
		return t.usage(initUsage, fmt.Sprintf("unknown --agent %q", *agent))
	}
	top, err := gitx.TopLevel(deps.Dir)
	if err != nil {
		return t.usage(initUsage, "not in a git repository: "+err.Error())
	}

	var plan []initFile
	add := func(f initFile, err error) bool {
		if err != nil {
			t.fail(exitUnverifiable, "intentgate init: %v", err)
			return false
		}
		plan = append(plan, f)
		return true
	}
	if *agent == "claude-code" && !add(planSettings(top)) {
		return exitUnverifiable
	}
	if !add(planInstructions(top, *agent)) || !add(planConfig(top)) {
		return exitUnverifiable
	}

	var out strings.Builder
	for _, f := range plan {
		fmt.Fprintf(&out, "%s %s\n", f.status, f.path)
		for _, n := range f.notes {
			fmt.Fprintf(&out, "  %s\n", n)
		}
	}
	out.WriteString("next: " + nextStep(*agent) + "\n")

	if !*dry {
		for _, f := range plan {
			if f.content == nil {
				continue
			}
			p := filepath.Join(top, filepath.FromSlash(f.path))
			if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
				return t.fail(exitUnverifiable, "intentgate init: %s: %v", f.path, err)
			}
			if err := os.WriteFile(p, f.content, 0o644); err != nil {
				return t.fail(exitUnverifiable, "intentgate init: %s: %v", f.path, err)
			}
		}
	}
	_, _ = io.WriteString(stdout, out.String())
	return exitOK
}

func nextStep(agent string) string {
	if agent == "generic" {
		return "have the agent write its plan to a file, then run `intentgate lock --from-plan <file>` before it changes code."
	}
	return "start Claude Code in this repository; it opens in plan mode, and approving a plan writes the intent lock."
}

// readExisting returns the file's bytes, or exists=false if there is no such file. target is the
// absolute path a write must go to: the file itself, or, for a symlink, what it resolves to. A
// symlink that is dangling or resolves outside the repository's top level is an error.
func readExisting(top, rel string) (data []byte, exists bool, target string, err error) {
	target = filepath.Join(top, filepath.FromSlash(rel))
	fi, err := os.Lstat(target)
	if errors.Is(err, os.ErrNotExist) {
		return nil, false, target, nil
	}
	if err != nil {
		return nil, false, "", fmt.Errorf("%s: %v", rel, err)
	}
	if fi.Mode()&os.ModeSymlink != 0 {
		resolved, err := filepath.EvalSymlinks(target)
		if err != nil {
			return nil, false, "", fmt.Errorf("%s: a symlink that does not resolve (%v), left untouched", rel, err)
		}
		root, err := filepath.EvalSymlinks(top)
		if err != nil {
			return nil, false, "", fmt.Errorf("%s: %v", rel, err)
		}
		if r, err := filepath.Rel(root, resolved); err != nil || r == ".." || strings.HasPrefix(r, ".."+string(filepath.Separator)) {
			return nil, false, "", fmt.Errorf("%s: a symlink to %s, outside the repository, left untouched", rel, resolved)
		}
		target = resolved
	}
	data, err = os.ReadFile(target)
	if err != nil {
		return nil, false, "", fmt.Errorf("%s: %v", rel, err)
	}
	return data, true, target, nil
}

// ---- .intentgate.yml ----

// planConfig creates `version: 1` if the file is absent and never modifies an existing one.
func planConfig(top string) (initFile, error) {
	_, exists, target, err := readExisting(top, configPath)
	if err != nil {
		return initFile{}, err
	}
	if exists {
		return initFile{status: "kept", path: configPath}, nil
	}
	return initFile{status: "created", path: configPath, target: target, content: []byte(configBody)}, nil
}

// ---- .claude/settings.json ----

// planSettings merges the two hooks and permissions.defaultMode into .claude/settings.json.
func planSettings(top string) (initFile, error) {
	invalid := func(format string, args ...any) (initFile, error) {
		return initFile{}, fmt.Errorf("%s: %s", settingsPath, fmt.Sprintf(format, args...))
	}
	data, exists, target, err := readExisting(top, settingsPath)
	if err != nil {
		return initFile{}, err
	}
	doc := map[string]any{}
	if exists {
		if doc, err = decodeObject(data); err != nil {
			return invalid("not valid JSON, left untouched (%v)", err)
		}
	}
	before, err := encodeJSON(doc)
	if err != nil {
		return invalid("%v", err)
	}

	// The two hooks exactly as `hook claude-code print-config` prints them.
	// The entries stay raw, so their key order (`matcher` before `hooks`) is print-config's.
	var want struct {
		Hooks map[string][]json.RawMessage `json:"hooks"`
	}
	if err := json.Unmarshal(printConfig(), &want); err != nil {
		return invalid("internal error: %v", err)
	}

	hooks, err := objectAt(doc, "hooks")
	if err != nil {
		return invalid("%v, left untouched", err)
	}
	for _, ev := range []struct{ event, which string }{{"PreToolUse", "pre"}, {"PostToolUse", "post"}} {
		var entries []any
		switch v := hooks[ev.event].(type) {
		case nil:
		case []any:
			entries = v
		default:
			return invalid("hooks.%s is not an array, left untouched", ev.event)
		}
		if !runsIntentgate(entries, ev.which) {
			for _, e := range want.Hooks[ev.event] {
				entries = append(entries, e)
			}
		}
		hooks[ev.event] = entries
	}
	doc["hooks"] = hooks

	perms, err := objectAt(doc, "permissions")
	if err != nil {
		return invalid("%v, left untouched", err)
	}
	var notes []string
	switch mode := perms["defaultMode"]; mode {
	case nil:
		perms["defaultMode"] = "plan"
	case "plan":
	default:
		notes = append(notes, fmt.Sprintf("left defaultMode=%v as is; %s", mode, defaultModeHint))
	}
	doc["permissions"] = perms

	after, err := encodeJSON(doc)
	if err != nil {
		return invalid("%v", err)
	}
	switch {
	case !exists:
		return initFile{status: "created", path: settingsPath, target: target, content: after, notes: notes}, nil
	case bytes.Equal(before, after):
		return initFile{status: "unchanged", path: settingsPath, notes: notes}, nil
	}
	return initFile{status: "updated", path: settingsPath, target: target, orig: data, content: after, notes: notes}, nil
}

// decodeObject parses exactly one JSON object; numbers stay as written.
func decodeObject(data []byte) (map[string]any, error) {
	dec := json.NewDecoder(bytes.NewReader(data))
	dec.UseNumber()
	var v any
	if err := dec.Decode(&v); err != nil {
		return nil, err
	}
	if _, err := dec.Token(); err != io.EOF {
		return nil, errors.New("more than one JSON value")
	}
	m, ok := v.(map[string]any)
	if !ok {
		return nil, errors.New("not a JSON object")
	}
	return m, nil
}

// encodeJSON is 2-space JSON with a trailing newline.
func encodeJSON(v any) ([]byte, error) {
	var b bytes.Buffer
	enc := json.NewEncoder(&b)
	enc.SetEscapeHTML(false)
	enc.SetIndent("", "  ")
	if err := enc.Encode(v); err != nil {
		return nil, err
	}
	return b.Bytes(), nil
}

// objectAt returns doc[key] as an object, a new empty one if it is absent.
func objectAt(doc map[string]any, key string) (map[string]any, error) {
	switch v := doc[key].(type) {
	case nil:
		return map[string]any{}, nil
	case map[string]any:
		return v, nil
	}
	return nil, fmt.Errorf("%s is not an object", key)
}

// runsIntentgate reports whether an entry, under any matcher, already runs `intentgate hook claude-code <which>`.
func runsIntentgate(entries []any, which string) bool {
	for _, e := range entries {
		em, _ := e.(map[string]any)
		hs, _ := em["hooks"].([]any)
		for _, h := range hs {
			hm, _ := h.(map[string]any)
			cmd, _ := hm["command"].(string)
			if isIntentgateHook(cmd, which) {
				return true
			}
		}
	}
	return false
}

func isIntentgateHook(command, which string) bool {
	want := []string{"intentgate", "hook", "claude-code", which}
	f := strings.Fields(command)
	for i := 0; i+len(want) <= len(f); i++ {
		// The binary may be given as a path (/usr/local/bin/intentgate).
		got := append([]string{filepath.Base(f[i])}, f[i+1:i+len(want)]...)
		if reflect.DeepEqual(got, want) {
			return true
		}
	}
	return false
}

// ---- the agent instructions ----

// planInstructions puts the marked section in AGENTS.md, else CLAUDE.md, else a new AGENTS.md.
func planInstructions(top, agent string) (initFile, error) {
	rel := "AGENTS.md"
	var data []byte
	exists := false
	target := filepath.Join(top, rel)
	for _, name := range []string{"AGENTS.md", "CLAUDE.md"} {
		d, ok, tgt, err := readExisting(top, name)
		if err != nil {
			return initFile{}, err
		}
		if ok {
			rel, data, exists, target = name, d, true, tgt
			break
		}
	}
	text := string(data)
	eol := "\n"
	if i := strings.Index(text, "\n"); i > 0 && text[i-1] == '\r' {
		eol = "\r\n"
	}
	section := strings.ReplaceAll(instructionsSection(agent), "\n", eol)
	var out string
	begins, ends := markerOffsets(text, sectionBegin), markerOffsets(text, sectionEnd)
	switch {
	case len(begins) == 0 && len(ends) == 0:
		out = text
		if out != "" {
			if !strings.HasSuffix(out, "\n") {
				out += eol
			}
			out += eol
		}
		out += section + eol
	case len(begins) == 0 || len(ends) == 0 || ends[0] < begins[0]:
		return initFile{}, fmt.Errorf("%s: the %s / %s markers are unpaired or out of order, left untouched", rel, sectionBegin, sectionEnd)
	case len(begins) > 1 || len(ends) > 1:
		return initFile{}, fmt.Errorf("%s: more than one IntentGate section, left untouched", rel)
	default:
		out = text[:begins[0]] + section + text[ends[0]+len(sectionEnd):]
	}
	switch {
	case !exists:
		return initFile{status: "created", path: rel, target: target, content: []byte(out)}, nil
	case out == text:
		return initFile{status: "unchanged", path: rel}, nil
	}
	return initFile{status: "updated", path: rel, target: target, orig: data, content: []byte(out)}, nil
}

// markerOffsets lists where marker occurs in text outside fenced code blocks: a marker inside a
// fence (a documented example) is text, not a marker.
func markerOffsets(text, marker string) []int {
	fenced := fencedRanges(text)
	var offs []int
	for from := 0; ; {
		i := strings.Index(text[from:], marker)
		if i < 0 {
			return offs
		}
		at := from + i
		from = at + len(marker)
		inFence := false
		for _, r := range fenced {
			if at >= r[0] && at < r[1] {
				inFence = true
				break
			}
		}
		if !inFence {
			offs = append(offs, at)
		}
	}
}

// fencedRanges returns the byte ranges of the fenced code blocks (``` or ~~~, closed by a fence of
// the same character at least as long; an unclosed fence runs to the end of the text).
func fencedRanges(text string) [][2]int {
	var ranges [][2]int
	var open byte // the opening fence character, 0 outside a fence
	var openLen, start int
	for pos := 0; pos < len(text); {
		end := len(text)
		if i := strings.IndexByte(text[pos:], '\n'); i >= 0 {
			end = pos + i + 1
		}
		line := strings.TrimRight(text[pos:end], "\r\n")
		trimmed := strings.TrimLeft(line, " ")
		if len(line)-len(trimmed) <= 3 && len(trimmed) >= 3 && (trimmed[0] == '`' || trimmed[0] == '~') {
			c := trimmed[0]
			run := len(trimmed) - len(strings.TrimLeft(trimmed, string(c)))
			rest := trimmed[run:]
			switch {
			case open == 0 && run >= 3 && !(c == '`' && strings.Contains(rest, "`")):
				open, openLen, start = c, run, pos
			case open == c && run >= openLen && strings.TrimSpace(rest) == "":
				ranges = append(ranges, [2]int{start, end})
				open = 0
			}
		}
		pos = end
	}
	if open != 0 {
		ranges = append(ranges, [2]int{start, len(text)})
	}
	return ranges
}

// instructionsSection is the text between the markers, markers included, without a trailing newline.
func instructionsSection(agent string) string {
	mode := "Start work in plan mode; approving the plan writes the intent lock."
	if agent == "generic" {
		mode = "Write the plan to a file, then ask the human to run `intentgate lock --from-plan <file>` before you change code."
	}
	return sectionBegin + "\n" +
		"## IntentGate\n\n" +
		"End every plan with exactly one fenced `intentgate` block. Each criterion is a checkable outcome, " +
		"and its footprint lists the repo-relative globs it may touch. Use this shape exactly " +
		"(2-space indents, `footprint` inside each criterion):\n\n" +
		"```intentgate\n" +
		"criteria:\n" +
		"  - text: \"<a checkable outcome>\"\n" +
		"    footprint: [\"<repo-relative glob>\"]\n" +
		"```\n\n" +
		mode + "\n" +
		sectionEnd
}
