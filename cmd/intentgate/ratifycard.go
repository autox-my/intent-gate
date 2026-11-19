package main

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"

	"github.com/autox-my/intent-gate/internal/hooks/claudecode"
)

// The ratify card (ratification.md §2.1): in a harness that takes plan approval over, the agent
// writes the plan to a file and asks one AskUserQuestion; the human's `Ratify` is the approval.

const notRatified = "IntentGate: no lock (not ratified)"

var (
	errOutside = errors.New("no lock: plan path outside the repo and the plans folder")
	errChanged = errors.New("no lock: the plan changed since the question")
)

// runRatifyQuestion is `intentgate hook claude-code ratify-question <plan path>`: it prints the
// card's AskUserQuestion input for that plan. Exit 2 on a usage error or a plan file it can't read.
func runRatifyQuestion(args []string, stdout io.Writer, deps Deps) int {
	fail := func(format string, a ...any) int {
		if deps.Stderr != nil {
			fmt.Fprintf(deps.Stderr, "intentgate hook claude-code ratify-question: "+format+"\n", a...)
		}
		return 2
	}
	if len(args) != 1 || args[0] == "" {
		return usageError(deps)
	}
	dir := deps.Dir
	if dir == "" {
		dir = "."
	}
	path := args[0]
	if !filepath.IsAbs(path) {
		path = filepath.Join(dir, path)
	}
	abs, err := filepath.Abs(path)
	if err != nil {
		return fail("%v", err)
	}
	data, err := os.ReadFile(abs)
	if err != nil {
		return fail("cannot read the plan: %v", err)
	}
	shown := abs
	if deps.Git != nil {
		if top, err := deps.Git.TopLevel(dir); err == nil {
			if rel, ok := within(top, abs); ok {
				shown = rel
			}
		}
	}
	sum := sha256.Sum256(data)
	_, _ = stdout.Write(claudecode.RenderCard(filepath.ToSlash(shown), hex.EncodeToString(sum[:])))
	return 0
}

// resolveLoose is filepath.EvalSymlinks for a path that need not exist: the symbolic links of its
// longest existing prefix are resolved and the rest is kept as written. It never stats the path
// itself unless an ancestor lookup reaches it.
func resolveLoose(path string) (string, error) {
	path = filepath.Clean(path)
	if p, err := filepath.EvalSymlinks(path); err == nil {
		return p, nil
	} else if !errors.Is(err, os.ErrNotExist) {
		return "", err
	}
	parent, base := filepath.Split(path)
	if parent == "" || parent == path || filepath.Clean(parent) == path {
		return path, nil
	}
	p, err := resolveLoose(parent)
	if err != nil {
		return "", err
	}
	return filepath.Join(p, base), nil
}

// within reports whether path is inside root (symbolic links resolved on both sides; path may not
// exist) and, if so, its slash-separated path relative to root.
func within(root, path string) (string, bool) {
	r, err := filepath.EvalSymlinks(root)
	if err != nil {
		return "", false
	}
	p, err := resolveLoose(path)
	if err != nil {
		return "", false
	}
	rel, err := filepath.Rel(r, p)
	if err != nil || rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) || filepath.IsAbs(rel) {
		return "", false
	}
	return filepath.ToSlash(rel), true
}

// cardPlan resolves the card's plan path inside the repository or ~/.claude/plans/, reads the
// file and checks it still hashes to what the question says.
func cardPlan(in claudecode.Input, card claudecode.Card, deps Deps) (string, error) {
	if in.CWD == "" {
		return "", errors.New("the hook input has no cwd")
	}
	top, err := deps.Git.TopLevel(in.CWD)
	if err != nil {
		return "", err
	}
	path := card.Path
	if !filepath.IsAbs(path) {
		path = filepath.Join(top, filepath.FromSlash(path))
	}
	_, inRepo := within(top, path)
	inPlans := false
	if home, err := os.UserHomeDir(); err == nil && home != "" {
		_, inPlans = within(filepath.Join(home, ".claude", "plans"), path)
	}
	if !inRepo && !inPlans { // scope first: an out-of-scope path is never statted, read or hashed
		return "", errOutside
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return "", fmt.Errorf("cannot read the plan file: %w", err)
	}
	sum := sha256.Sum256(data)
	if hex.EncodeToString(sum[:]) != card.SHA256 {
		return "", errChanged
	}
	return string(data), nil
}

// preCard is PreToolUse on the card: the plan file is read and checked, then handled as a plan.
func preCard(in claudecode.Input, card claudecode.Card, recPath string, deps Deps) (claudecode.Output, error) {
	plan, err := cardPlan(in, card, deps)
	if err != nil {
		return claudecode.Output{}, err
	}
	return pre(in, plan, recPath, deps)
}

// postCard is PostToolUse on the card: only the answer Ratify, to a plan file that still hashes to
// the question's, writes a lock. The answer is tool_response's; if that has none for the question,
// the transcript's for the same tool_use_id (also written by the harness); anything else is "not
// ratified".
func postCard(in claudecode.Input, card claudecode.Card, recPath string, deps Deps) (claudecode.Output, error) {
	answers := in.ToolResponse.Answers
	if _, ok := answers[card.Question]; !ok { // tool_response has no answer: the transcript's, else fail closed
		if tdata, err := os.ReadFile(in.TranscriptPath); err == nil {
			answers = claudecode.TranscriptAnswers(tdata, in.ToolUseID)
		}
	}
	if !card.Ratified(answers) {
		_ = os.Remove(recPath)
		return claudecode.Output{SystemMessage: notRatified}, nil
	}
	plan, err := cardPlan(in, card, deps)
	if err != nil {
		return claudecode.Output{}, err
	}
	return post(in, plan, recPath, deps)
}
