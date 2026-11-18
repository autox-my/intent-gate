package main

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"time"

	"intentgate/internal/gitx"
	"intentgate/internal/hooks/claudecode"
	"intentgate/internal/ids"
	"intentgate/internal/ratify"
)

// Git is the repository facts the hook needs (internal/gitx behind an interface for tests).
type Git interface {
	TopLevel(dir string) (string, error)
	UserEmail(dir string) (string, error)
	BaseRef(dir string) (string, error)
}

// gitCLI is the real Git: internal/gitx.
type gitCLI struct{}

func (gitCLI) TopLevel(dir string) (string, error)  { return gitx.TopLevel(dir) }
func (gitCLI) UserEmail(dir string) (string, error) { return gitx.UserEmail(dir) }
func (gitCLI) BaseRef(dir string) (string, error)   { return gitx.BaseRef(dir) }

// Deps is everything Run takes from the outside world.
type Deps struct {
	Now      func() time.Time
	Rand     io.Reader
	StateDir string
	Git      Git
	Stderr   io.Writer // usage errors only; nil discards
}

const (
	exitPlanMode = "ExitPlanMode"
	pendingTTL   = 24 * time.Hour
	usage        = "usage: intentgate hook claude-code pre|post|print-config\n"
)

// Run executes the intentgate command line and returns the exit code. `hook claude-code pre|post`
// fail open: they always exit 0 and print exactly one JSON object (ratification.md §2).
func Run(args []string, stdin io.Reader, stdout io.Writer, deps Deps) int {
	if len(args) != 3 || args[0] != "hook" || args[1] != "claude-code" {
		return usageError(deps)
	}
	switch args[2] {
	case "pre", "post":
		out := safely(func() claudecode.Output { return runHook(args[2], stdin, deps) })
		_, _ = stdout.Write(out.Render())
		return 0
	case "print-config":
		_, _ = stdout.Write(printConfig())
		return 0
	}
	return usageError(deps)
}

// usageError exits 1, not 2: for a hook, exit code 2 would mean "block the tool call".
func usageError(deps Deps) int {
	if deps.Stderr != nil {
		_, _ = io.WriteString(deps.Stderr, usage)
	}
	return 1
}

// safely turns a panic into a "no lock" message: a hook never crashes the agent.
func safely(f func() claudecode.Output) (out claudecode.Output) {
	defer func() {
		if r := recover(); r != nil {
			out = noLock(fmt.Errorf("internal error: %v", r))
		}
	}()
	return f()
}

// noLock is the fail-open output: the message is "IntentGate: no lock: <reason>" (an error from
// ratify already starts "no lock: ").
func noLock(err error) claudecode.Output {
	msg := strings.TrimPrefix(err.Error(), "no lock: ")
	return claudecode.Output{SystemMessage: "IntentGate: no lock: " + msg}
}

func runHook(which string, stdin io.Reader, deps Deps) claudecode.Output {
	pending := pendingDir(deps.StateDir)
	if pending != "" {
		prunePending(pending, deps.Now())
	}
	data, err := io.ReadAll(stdin)
	if err != nil {
		return noLock(fmt.Errorf("reading the hook input: %w", err))
	}
	in, err := claudecode.ParseInput(data)
	if err != nil {
		return noLock(err)
	}
	event := map[string]string{"pre": "PreToolUse", "post": "PostToolUse"}[which]
	if in.ToolName != exitPlanMode {
		return claudecode.Output{}
	}
	if in.HookEventName != event {
		return noLock(fmt.Errorf("`hook claude-code %s` got a %q event, want %s", which, in.HookEventName, event))
	}
	if pending == "" {
		return noLock(errors.New("no state directory: set INTENTGATE_HOME"))
	}
	rec, err := pendingPath(pending, in)
	if err != nil {
		return noLock(err)
	}
	var out claudecode.Output
	if which == "pre" {
		out, err = pre(in, rec, deps)
	} else {
		out, err = post(in, rec, deps)
	}
	if err != nil {
		return noLock(err)
	}
	return out
}

// pendingRecord is what PreToolUse saves: when the criteria were shown, and a hash of the plan.
// It holds no root intent text.
type pendingRecord struct {
	FinishedAt string `json:"finished_at"`
	PlanSHA256 string `json:"plan_sha256"`
}

func pre(in claudecode.Input, recPath string, deps Deps) (claudecode.Output, error) {
	criteria, err := ratify.DraftCriteria(in.ToolInput.Plan)
	if err != nil {
		return claudecode.Output{}, err
	}
	sum := sha256.Sum256([]byte(in.ToolInput.Plan))
	data, err := json.Marshal(pendingRecord{
		FinishedAt: deps.Now().UTC().Format(time.RFC3339Nano),
		PlanSHA256: hex.EncodeToString(sum[:]),
	})
	if err != nil {
		return claudecode.Output{}, err
	}
	if err := os.MkdirAll(filepath.Dir(recPath), 0o700); err != nil {
		return claudecode.Output{}, err
	}
	if err := os.WriteFile(recPath, data, 0o600); err != nil {
		return claudecode.Output{}, err
	}
	var b strings.Builder
	b.WriteString("IntentGate will lock these criteria if you approve:")
	for _, c := range criteria {
		fmt.Fprintf(&b, "\n- %s: %s (footprint: %s)", c.ID, c.Text, strings.Join(c.Footprint, ", "))
	}
	return claudecode.Output{SystemMessage: b.String()}, nil
}

func post(in claudecode.Input, recPath string, deps Deps) (claudecode.Output, error) {
	now := deps.Now()
	plan := in.ToolResponse.Plan
	if plan == "" {
		return claudecode.Output{}, errors.New("the approved plan is missing from the hook input")
	}
	criteria, err := ratify.DraftCriteria(plan) // the approved plan wins over whatever pre saw
	if err != nil {
		return claudecode.Output{}, err
	}
	tdata, err := os.ReadFile(in.TranscriptPath)
	if err != nil {
		return claudecode.Output{}, fmt.Errorf("reading the transcript: %w", err)
	}
	rootIntent, draftedBy, err := claudecode.RootIntent(tdata)
	if err != nil {
		return claudecode.Output{}, err
	}
	if strings.TrimSpace(rootIntent) == "" {
		return claudecode.Output{}, errors.New("the transcript has no human turns to lock")
	}
	if in.CWD == "" {
		return claudecode.Output{}, errors.New("the hook input has no cwd")
	}
	top, err := deps.Git.TopLevel(in.CWD)
	if err != nil {
		return claudecode.Output{}, err
	}
	email, err := deps.Git.UserEmail(in.CWD)
	if err != nil {
		return claudecode.Output{}, err
	}
	baseRef, err := deps.Git.BaseRef(in.CWD)
	if err != nil {
		return claudecode.Output{}, err
	}
	id, err := ids.NewULID(now, deps.Rand)
	if err != nil {
		return claudecode.Output{}, err
	}

	var durationMS int
	if rec, ok := readPending(recPath); ok {
		if ms := now.Sub(rec).Milliseconds(); ms > 0 {
			durationMS = int(ms)
		}
	}
	l, masked, err := ratify.MaskAndBuild(nil, rootIntent, ratify.BuildInput{
		ID:               id,
		BaseRef:          baseRef,
		Source:           "claude-code-session",
		Criteria:         criteria,
		Ratifier:         "git:" + email,
		RatifiedAt:       now,
		Method:           "hook",
		ReviewDurationMS: durationMS,
		DraftedBy:        draftedBy,
	})
	if err != nil {
		return claudecode.Output{}, err
	}
	if err := (ratify.Store{RepoRoot: top, StateDir: deps.StateDir}).Write(l, masked); err != nil {
		return claudecode.Output{}, err
	}
	_ = os.Remove(recPath)
	return claudecode.Output{SystemMessage: "IntentGate: intent lock written: .intent/" + l.ID + ".yml"}, nil
}

var safeComponent = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9_.-]*$`)

// pendingDir is where pending records live, or "" when there is no state directory.
func pendingDir(state string) string {
	if state == "" {
		return ""
	}
	return filepath.Join(state, "pending")
}

// pendingPath is the record's path for this session and tool use. Both ids come from the hook
// input and become path components, so anything but a plain name is refused.
func pendingPath(pending string, in claudecode.Input) (string, error) {
	for _, c := range []struct{ name, v string }{{"session_id", in.SessionID}, {"tool_use_id", in.ToolUseID}} {
		if !safeComponent.MatchString(c.v) || strings.Contains(c.v, "..") {
			return "", fmt.Errorf("the hook input's %s is not a plain name", c.name)
		}
	}
	return filepath.Join(pending, in.SessionID, in.ToolUseID+".json"), nil
}

// readPending returns the time the criteria were shown; false if there is no usable record.
func readPending(path string) (time.Time, bool) {
	data, err := os.ReadFile(path)
	if err != nil {
		return time.Time{}, false
	}
	var rec pendingRecord
	if json.Unmarshal(data, &rec) != nil {
		return time.Time{}, false
	}
	t, err := time.Parse(time.RFC3339Nano, rec.FinishedAt)
	return t, err == nil
}

// prunePending deletes pending records older than 24 h by their finished_at (not file mtime), and
// then every empty session directory. Errors are ignored: pruning must never stop a hook.
func prunePending(pending string, now time.Time) {
	sessions, err := os.ReadDir(pending)
	if err != nil {
		return
	}
	for _, s := range sessions {
		if !s.IsDir() {
			continue
		}
		dir := filepath.Join(pending, s.Name())
		recs, _ := os.ReadDir(dir)
		for _, r := range recs {
			if r.IsDir() || !strings.HasSuffix(r.Name(), ".json") {
				continue
			}
			path := filepath.Join(dir, r.Name())
			if t, ok := readPending(path); !ok || now.Sub(t) > pendingTTL {
				_ = os.Remove(path)
			}
		}
		_ = os.Remove(dir) // fails, harmlessly, unless the directory is empty
	}
}

// printConfig is the .claude/settings.json `hooks` fragment that wires the two hooks.
func printConfig() []byte {
	type hook struct {
		Type    string `json:"type"`
		Command string `json:"command"`
		Timeout int    `json:"timeout"`
	}
	type matcher struct {
		Matcher string `json:"matcher"`
		Hooks   []hook `json:"hooks"`
	}
	cfg := map[string]map[string][]matcher{"hooks": {
		"PreToolUse":  {{Matcher: exitPlanMode, Hooks: []hook{{"command", "intentgate hook claude-code pre", 60}}}},
		"PostToolUse": {{Matcher: exitPlanMode, Hooks: []hook{{"command", "intentgate hook claude-code post", 30}}}},
	}}
	b, _ := json.MarshalIndent(cfg, "", "  ")
	return append(b, '\n')
}
