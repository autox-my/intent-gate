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

	"intentgate/internal/audit"
	"intentgate/internal/gitx"
	"intentgate/internal/hooks/claudecode"
	"intentgate/internal/ids"
	"intentgate/internal/planreview"
	"intentgate/internal/ratify"
)

// Git is the repository facts the hook needs (internal/gitx behind an interface for tests).
type Git interface {
	TopLevel(dir string) (string, error)
	UserEmail(dir string) (string, error)
	BaseRef(dir string) (string, error)
	RemoteURL(dir, name string) (string, error)
}

// gitCLI is the real Git: internal/gitx.
type gitCLI struct{}

func (gitCLI) TopLevel(dir string) (string, error)  { return gitx.TopLevel(dir) }
func (gitCLI) UserEmail(dir string) (string, error) { return gitx.UserEmail(dir) }
func (gitCLI) BaseRef(dir string) (string, error)   { return gitx.BaseRef(dir) }
func (gitCLI) RemoteURL(dir, name string) (string, error) {
	return gitRemoteURL(dir, name)
}

// Deps is everything Run takes from the outside world.
type Deps struct {
	Now      func() time.Time
	Rand     io.Reader
	StateDir string
	Git      Git
	Stderr   io.Writer // usage errors only; nil discards

	// GHAuthToken returns the token `gh auth token` prints, for `lock --issue` when neither
	// $GITHUB_TOKEN nor $GH_TOKEN is set. Nil means there is no such fallback.
	GHAuthToken func() (string, error)

	// Dir is the working directory commands start from; relative paths and git are resolved here.
	Dir string
	// IsTerminal reports whether file descriptor fd (0 = stdin, 1 = stdout) is a terminal. A nil
	// IsTerminal means "no terminal", so ratification is refused (REQ-0005).
	IsTerminal func(fd int) bool

	// PlanReviewer is the plan review's provider; nil means planreview.Select(os.Getenv).
	PlanReviewer planreview.Reviewer
	// PlanReviewBudget is the review's time budget; 0 means 5 s.
	PlanReviewBudget time.Duration
}

const (
	exitPlanMode = "ExitPlanMode"
	pendingTTL   = 24 * time.Hour
	usage        = "usage: intentgate hook claude-code pre|post|print-config\n" + lockUsage + amendUsage + verifyUsage
)

// Run executes the intentgate command line (hook, lock, amend, verify) and returns the exit code. `hook claude-code pre|post`
// fail open: they always exit 0 and print exactly one JSON object (ratification.md §2), even on a
// bad command line. Every other usage error exits 2 with usage on stderr (core-contracts.md §3).
func Run(args []string, stdin io.Reader, stdout io.Writer, deps Deps) int {
	if len(args) > 0 {
		switch args[0] {
		case "lock":
			return runLock(args[1:], stdin, stdout, deps)
		case "amend":
			return runAmend(args[1:], stdin, stdout, deps)
		case "--version":
			if len(args) == 1 {
				_, _ = io.WriteString(stdout, "intentgate "+version+"\n")
				return 0
			}
		case "verify":
			return runVerify(args[1:], stdout, deps)
		case "publish":
			return runPublish(args[1:], stdout, deps)
		case "stats":
			return runStats(args[1:], stdout, deps)
		}
	}
	if len(args) < 3 || args[0] != "hook" || args[1] != "claude-code" {
		return usageError(deps)
	}
	switch args[2] {
	case "pre", "post":
		out := safely(func() claudecode.Output {
			if len(args) != 3 {
				return noLock(fmt.Errorf("unexpected arguments %q; usage: intentgate hook claude-code %s", args[3:], args[2]))
			}
			return runHook(args[2], stdin, deps)
		})
		_, _ = stdout.Write(out.Render())
		return 0
	case "print-config":
		if len(args) == 3 {
			_, _ = stdout.Write(printConfig())
			return 0
		}
	}
	return usageError(deps)
}

// usageError prints usage on stderr and returns 2, the usage-error code of every command.
func usageError(deps Deps) int {
	if deps.Stderr != nil {
		_, _ = io.WriteString(deps.Stderr, usage)
	}
	return 2
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
	criteria, err := draftMasked(in.ToolInput.Plan)
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
	if err := writeFileAtomic(recPath, data); err != nil {
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
	// No pending record, no lock: post only counts as an approval after pre showed the criteria.
	shownAt, ok := readPending(recPath)
	if !ok {
		return claudecode.Output{}, errors.New("no pending record; was the plan shown by the PreToolUse hook?")
	}
	plan := in.ToolResponse.Plan
	if plan == "" {
		return claudecode.Output{}, errors.New("the approved plan is missing from the hook input")
	}
	criteria, err := draftMasked(plan) // the approved plan wins over whatever pre saw
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
	if ms := now.Sub(shownAt).Milliseconds(); ms > 0 {
		durationMS = int(ms)
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
	auditLockWritten(deps, audit.EventLock, l, now, top)
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

// writeFileAtomic writes data (mode 0600) to a temp file next to path and renames it into place,
// so a reader never sees a half-written record.
func writeFileAtomic(path string, data []byte) (err error) {
	f, err := os.CreateTemp(filepath.Dir(path), ".tmp-*")
	if err != nil {
		return err
	}
	defer func() {
		if err != nil {
			_ = os.Remove(f.Name())
		}
	}()
	if err = f.Chmod(0o600); err != nil {
		_ = f.Close()
		return err
	}
	if _, err = f.Write(data); err != nil {
		_ = f.Close()
		return err
	}
	if err = f.Close(); err != nil {
		return err
	}
	return os.Rename(f.Name(), path)
}

// prunePending deletes stale pending records, then every empty session directory. A record is
// stale when |now − finished_at| > 24 h (so one stamped in the future goes too), judged by
// finished_at and not by mtime; a record that can't be parsed, and any other leftover file such as
// an interrupted temp file, is stale once its mtime is older than 24 h. Errors are ignored:
// pruning must never stop a hook.
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
			if r.IsDir() {
				continue
			}
			path := filepath.Join(dir, r.Name())
			if strings.HasSuffix(r.Name(), ".json") {
				if t, ok := readPending(path); ok {
					if d := now.Sub(t); d > pendingTTL || -d > pendingTTL {
						_ = os.Remove(path)
					}
					continue
				}
			}
			if info, err := r.Info(); err == nil && now.Sub(info.ModTime()) > pendingTTL {
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
