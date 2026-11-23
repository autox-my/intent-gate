package main

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/autox-my/intent-gate/internal/audit"
	"github.com/autox-my/intent-gate/internal/hooks/antigravity"
	"github.com/autox-my/intent-gate/internal/ids"
	"github.com/autox-my/intent-gate/internal/lock"
	"github.com/autox-my/intent-gate/internal/planreview"
	"github.com/autox-my/intent-gate/internal/ratify"
)

var errAgyOutside = errors.New("no lock: plan path outside the repo and the artifact directory")

type agyPendingRecord struct {
	FinishedAt     string           `json:"finished_at"`
	ConversationID string           `json:"conversation_id"`
	StepIdx        int              `json:"step_idx"`
	PlanPath       string           `json:"plan_path"`
	PlanSHA256     string           `json:"plan_sha256"`
	ArtifactDir    string           `json:"artifact_dir,omitempty"`
	Error          string           `json:"error,omitempty"`
	PlanReview     *lock.PlanReview `json:"plan_review,omitempty"`
}

func runAntigravityHookCmd(args []string, stdin io.Reader, stdout io.Writer, deps Deps) int {
	switch args[0] {
	case "pre":
		out := agyPreSafely(func() antigravity.PreOutput {
			if len(args) != 1 {
				return antigravity.PreOutput{}
			}
			return agyPre(stdin, deps)
		})
		_, _ = stdout.Write(out.Render())
		return 0
	case "post-invocation":
		out := agyPostSafely(func() antigravity.PostInvocationOutput {
			if len(args) != 1 {
				return antigravity.PostInvocationOutput{}
			}
			return agyPostInvocation(stdin, deps)
		})
		_, _ = stdout.Write(out.Render())
		return 0
	case "ratify-question":
		return runAntigravityRatifyQuestion(args[1:], stdout, deps)
	case "print-config":
		if len(args) == 1 {
			_, _ = stdout.Write(printAntigravityConfig())
			return 0
		}
	}
	return usageError(deps)
}

func agyPreSafely(f func() antigravity.PreOutput) (out antigravity.PreOutput) {
	defer func() {
		if recover() != nil {
			out = antigravity.PreOutput{}
		}
	}()
	return f()
}

func agyPostSafely(f func() antigravity.PostInvocationOutput) (out antigravity.PostInvocationOutput) {
	defer func() {
		if recover() != nil {
			out = antigravity.PostInvocationOutput{}
		}
	}()
	return f()
}

func agyPendingSessionDir(stateDir, convID string) (string, error) {
	if !safeComponent.MatchString(convID) || strings.Contains(convID, "..") {
		return "", errors.New("the hook input's conversationId is not a plain name")
	}
	return filepath.Join(pendingDir(stateDir), "agy-"+convID), nil
}

func agyPendingFilePath(sessionDir string, stepIdx int) string {
	return filepath.Join(sessionDir, fmt.Sprintf("step-%d.json", stepIdx))
}

func agyCheckPlan(cwd, artifactDir, cardPath, wantSHA string, deps Deps) (string, error) {
	if cwd == "" {
		return "", errors.New("the hook input has no cwd")
	}
	top, err := deps.Git.TopLevel(cwd)
	if err != nil {
		return "", err
	}
	path := cardPath
	if !filepath.IsAbs(path) {
		path = filepath.Join(top, filepath.FromSlash(path))
	}
	_, inRepo := within(top, path)
	inArt := false
	if artifactDir != "" && filepath.IsAbs(artifactDir) {
		if resolvedArt, err := filepath.EvalSymlinks(artifactDir); err == nil {
			cleanArt := filepath.Clean(resolvedArt)
			if cleanArt != filepath.Dir(cleanArt) { // refuse filesystem root (including via symlink)
				_, inArt = within(cleanArt, path)
			}
		}
	}
	if !inRepo && !inArt {
		return "", errAgyOutside
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return "", fmt.Errorf("cannot read the plan file: %w", err)
	}
	sum := sha256.Sum256(data)
	if hex.EncodeToString(sum[:]) != wantSHA {
		return "", errChanged
	}
	return string(data), nil
}

func agyPre(stdin io.Reader, deps Deps) antigravity.PreOutput {
	if p := pendingDir(deps.StateDir); p != "" {
		prunePending(p, deps.Now())
	}
	data, err := io.ReadAll(stdin)
	if err != nil {
		return antigravity.PreOutput{}
	}
	in, err := antigravity.ParseInput(data)
	if err != nil || in.HookEventName != "PreToolUse" || in.ToolCall.Name != "ask_question" {
		return antigravity.PreOutput{}
	}
	card, ok := antigravity.ParseCard(in.ToolCall.Questions)
	if !ok || in.StepIdx == nil || *in.StepIdx < 0 || deps.StateDir == "" {
		return antigravity.PreOutput{}
	}
	sessDir, err := agyPendingSessionDir(deps.StateDir, in.ConversationID)
	if err != nil {
		return antigravity.PreOutput{}
	}

	rec := agyPendingRecord{
		FinishedAt:     deps.Now().UTC().Format(time.RFC3339Nano),
		ConversationID: in.ConversationID,
		StepIdx:        *in.StepIdx,
		PlanPath:       card.Path,
		PlanSHA256:     card.SHA256,
		ArtifactDir:    in.ArtifactDirectoryPath,
	}
	if plan, err := agyCheckPlan(in.CWD, in.ArtifactDirectoryPath, card.Path, card.SHA256, deps); err != nil {
		rec.Error = strings.TrimPrefix(err.Error(), "no lock: ")
	} else if drafted, maskedPlan, err := draftPlanMasked(plan); err != nil {
		rec.Error = strings.TrimPrefix(err.Error(), "no lock: ")
	} else if drafted.Intent != "" {
		top, err := deps.Git.TopLevel(in.CWD)
		if err != nil {
			rec.Error = strings.TrimPrefix(err.Error(), "no lock: ")
		} else if ci, err := loadCommittedIntent(top, drafted.Intent); err != nil {
			rec.Error = strings.TrimPrefix(err.Error(), "no lock: ")
		} else {
			res := reviewStep(deps, maskedPlan, func() (string, error) { return ci.Text, nil })
			rec.PlanReview = planReviewRecord(res)
		}
	} else {
		res := agyPlanReview(in, maskedPlan, deps)
		rec.PlanReview = planReviewRecord(res)
	}
	rec.FinishedAt = deps.Now().UTC().Format(time.RFC3339Nano)

	if encoded, err := json.Marshal(rec); err == nil {
		if os.MkdirAll(sessDir, 0o700) == nil {
			_ = writeFileAtomic(agyPendingFilePath(sessDir, *in.StepIdx), encoded)
		}
	}
	return antigravity.PreOutput{}
}

func agyPlanReview(in antigravity.Input, maskedPlan string, deps Deps) planreview.Result {
	stepIdx := *in.StepIdx
	return reviewStep(deps, maskedPlan, func() (string, error) {
		data, err := os.ReadFile(in.TranscriptPath)
		if err != nil {
			return "", errors.New("cannot read the transcript")
		}
		scope, err := antigravity.RootIntentScope(data, stepIdx)
		if err != nil {
			return "", errors.New("cannot read the human turns from the transcript")
		}
		if strings.TrimSpace(scope.Text) == "" {
			return "", errors.New("the transcript has no human turns")
		}
		return scope.Text, nil
	})
}

func agyPostInvocation(stdin io.Reader, deps Deps) antigravity.PostInvocationOutput {
	if p := pendingDir(deps.StateDir); p != "" {
		prunePending(p, deps.Now())
	}
	data, err := io.ReadAll(stdin)
	if err != nil {
		return antigravity.PostInvocationOutput{}
	}
	in, err := antigravity.ParseInput(data)
	if err != nil || in.HookEventName != "PostInvocation" || deps.StateDir == "" {
		return antigravity.PostInvocationOutput{}
	}
	sessDir, err := agyPendingSessionDir(deps.StateDir, in.ConversationID)
	if err != nil {
		return antigravity.PostInvocationOutput{}
	}
	entries, err := os.ReadDir(sessDir)
	if err != nil || len(entries) == 0 {
		return antigravity.PostInvocationOutput{}
	}

	type item struct {
		path string
		rec  agyPendingRecord
	}
	var items []item
	for _, ent := range entries {
		name := ent.Name()
		if ent.IsDir() || !strings.HasPrefix(name, "step-") || !strings.HasSuffix(name, ".json") {
			continue
		}
		p := filepath.Join(sessDir, name)
		raw, rerr := os.ReadFile(p)
		if rerr != nil {
			continue
		}
		var rec agyPendingRecord
		if json.Unmarshal(raw, &rec) == nil {
			items = append(items, item{path: p, rec: rec})
		}
	}
	sort.Slice(items, func(i, j int) bool { return items[i].rec.StepIdx < items[j].rec.StepIdx })
	if len(items) == 0 {
		return antigravity.PostInvocationOutput{}
	}

	tdata, tErr := os.ReadFile(in.TranscriptPath)
	if tErr != nil {
		return antigravity.PostInvocationOutput{}
	}
	var msgs []string
	for _, it := range items {
		ans, found, done := antigravity.StepAnswer(tdata, it.rec.StepIdx)
		if !found {
			if in.InitialNumSteps != nil && it.rec.StepIdx < *in.InitialNumSteps {
				_ = os.Remove(it.path)
			}
			continue
		}
		_ = os.Remove(it.path)
		if !done || !antigravity.Ratified(ans) {
			msgs = append(msgs, notRatified)
			continue
		}
		if it.rec.Error != "" {
			msgs = append(msgs, "IntentGate: no lock: "+it.rec.Error)
			continue
		}
		msg, err := agyWriteLock(in, it.rec, tdata, deps)
		if err != nil {
			msgs = append(msgs, "IntentGate: no lock: "+strings.TrimPrefix(err.Error(), "no lock: "))
			continue
		}
		msgs = append(msgs, msg)
	}
	_ = os.Remove(sessDir) // fails harmlessly unless empty
	return antigravity.PostInvocationOutput{Message: strings.Join(msgs, "\n\n")}
}

func agyWriteLock(in antigravity.Input, rec agyPendingRecord, tdata []byte, deps Deps) (string, error) {
	now := deps.Now()
	plan, err := agyCheckPlan(in.CWD, rec.ArtifactDir, rec.PlanPath, rec.PlanSHA256, deps)
	if err != nil {
		return "", err
	}
	drafted, _, err := draftPlanMasked(plan)
	if err != nil {
		return "", err
	}
	top, err := deps.Git.TopLevel(in.CWD)
	if err != nil {
		return "", err
	}

	var (
		rootIntent, draftedBy, scopeLine string
		source                           = antigravity.SourceSession
		committed                        *committedIntent
	)
	scope, scopeErr := antigravity.RootIntentScope(tdata, rec.StepIdx)
	if scopeErr == nil {
		draftedBy = scope.DraftedBy
	}
	if draftedBy == "" && in.ModelName != "" {
		draftedBy = "antigravity/" + in.ModelName
	}

	if drafted.Intent != "" {
		ci, err := loadCommittedIntent(top, drafted.Intent)
		if err != nil {
			return "", err
		}
		committed, rootIntent, source, scopeLine = &ci, ci.Text, lock.SourceCommittedIntent, ci.describe()
	} else {
		if scopeErr != nil {
			return "", scopeErr
		}
		rootIntent, scopeLine = scope.Text, scope.Describe()
		if strings.TrimSpace(rootIntent) == "" {
			return "", errors.New("the transcript has no human turns to lock")
		}
	}

	email, err := deps.Git.UserEmail(in.CWD)
	if err != nil {
		return "", err
	}
	baseRef, err := deps.Git.BaseRef(in.CWD)
	if err != nil {
		return "", err
	}
	id, err := ids.NewULID(now, deps.Rand)
	if err != nil {
		return "", err
	}

	var durationMS int
	if shownAt, err := time.Parse(time.RFC3339Nano, rec.FinishedAt); err == nil {
		if ms := now.Sub(shownAt).Milliseconds(); ms > 0 {
			durationMS = int(ms)
		}
	}
	var planReview *lock.PlanReview
	if usablePlanReview(rec.PlanReview) {
		planReview = rec.PlanReview
	}

	l, masked, err := ratify.MaskAndBuild(nil, rootIntent, ratify.BuildInput{
		ID:               id,
		BaseRef:          baseRef,
		Source:           source,
		Committed:        committed.record(),
		Criteria:         drafted.Criteria,
		Ratifier:         "git:" + email,
		RatifiedAt:       now,
		Method:           "hook",
		ReviewDurationMS: durationMS,
		DraftedBy:        draftedBy,
		PlanReview:       planReview,
	})
	if err != nil {
		return "", err
	}
	if err := (ratify.Store{RepoRoot: top, StateDir: deps.StateDir}).Write(l, masked); err != nil {
		return "", err
	}
	auditLockWritten(deps, audit.EventLock, l, now, top)

	msg := "IntentGate: intent lock written: .intent/" + l.ID + ".yml"
	if pr := l.PlanReview; pr != nil {
		msg += fmt.Sprintf(" (plan review: %s; %d omissions, %d additions, %d weakenings)", pr.Status, *pr.Omissions, *pr.Additions, *pr.Weakenings)
	}
	msg += "\n" + scopeLine
	for _, w := range repoWideWarnings(drafted.Criteria) {
		msg += "\n" + w
	}
	return msg, nil
}

func runAntigravityRatifyQuestion(args []string, stdout io.Writer, deps Deps) int {
	fail := func(format string, a ...any) int {
		if deps.Stderr != nil {
			fmt.Fprintf(deps.Stderr, "intentgate hook antigravity ratify-question: "+format+"\n", a...)
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
	_, _ = stdout.Write(antigravity.RenderCard(filepath.ToSlash(shown), hex.EncodeToString(sum[:])))
	return 0
}

func printAntigravityConfig() []byte {
	type hook struct {
		Type    string `json:"type"`
		Command string `json:"command"`
		Timeout int    `json:"timeout"`
	}
	type matcher struct {
		Matcher string `json:"matcher"`
		Hooks   []hook `json:"hooks"`
	}
	type entry struct {
		Enabled        bool      `json:"enabled"`
		PreToolUse     []matcher `json:"PreToolUse"`
		PostInvocation []hook    `json:"PostInvocation"`
	}
	cfg := struct {
		IntentGate entry `json:"intentgate"`
	}{
		IntentGate: entry{
			Enabled: true,
			PreToolUse: []matcher{{
				Matcher: "ask_question",
				Hooks:   []hook{{"command", "intentgate hook antigravity pre", 60}},
			}},
			PostInvocation: []hook{{"command", "intentgate hook antigravity post-invocation", 30}},
		},
	}
	var b bytes.Buffer
	enc := json.NewEncoder(&b)
	enc.SetEscapeHTML(false)
	enc.SetIndent("", "  ")
	_ = enc.Encode(cfg)
	return b.Bytes()
}

func planAntigravityHooks(top string) (initFile, error) {
	invalid := func(format string, args ...any) (initFile, error) {
		return initFile{}, fmt.Errorf("%s: %s", antigravityHooksPath, fmt.Sprintf(format, args...))
	}
	data, exists, target, err := readExisting(top, antigravityHooksPath)
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

	var wantDoc struct {
		IntentGate map[string]json.RawMessage `json:"intentgate"`
	}
	if err := json.Unmarshal(printAntigravityConfig(), &wantDoc); err != nil {
		return invalid("internal error: %v", err)
	}

	ig, err := objectAt(doc, "intentgate")
	if err != nil {
		return invalid("%v, left untouched", err)
	}
	if err := mergeAntigravityEnabled(ig); err != nil {
		return invalid("%v, left untouched", err)
	}
	if err := mergeAntigravityPre(ig, wantDoc.IntentGate["PreToolUse"]); err != nil {
		return invalid("%v, left untouched", err)
	}
	if err := mergeAntigravityPostInv(ig, wantDoc.IntentGate["PostInvocation"]); err != nil {
		return invalid("%v, left untouched", err)
	}
	doc["intentgate"] = ig

	after, err := encodeJSON(doc)
	if err != nil {
		return invalid("%v", err)
	}
	switch {
	case !exists:
		return initFile{status: "created", path: antigravityHooksPath, target: target, content: after}, nil
	case bytes.Equal(before, after):
		return initFile{status: "unchanged", path: antigravityHooksPath}, nil
	}
	return initFile{status: "updated", path: antigravityHooksPath, target: target, orig: data, content: after}, nil
}

func mergeAntigravityEnabled(ig map[string]any) error {
	v, ok := ig["enabled"]
	if !ok || v == nil {
		ig["enabled"] = true
		return nil
	}
	if _, isBool := v.(bool); !isBool {
		return errors.New("intentgate.enabled is not a boolean")
	}
	return nil
}

func mergeAntigravityPre(ig map[string]any, raw json.RawMessage) error {
	var entries []any
	switch v := ig["PreToolUse"].(type) {
	case nil:
	case []any:
		entries = v
	default:
		return errors.New("intentgate.PreToolUse is not an array")
	}
	var want []json.RawMessage
	if err := json.Unmarshal(raw, &want); err != nil {
		return fmt.Errorf("internal error: %v", err)
	}
	if !runsAntigravityPre(entries, "ask_question") {
		for _, w := range want {
			entries = append(entries, w)
		}
	}
	ig["PreToolUse"] = entries
	return nil
}

func mergeAntigravityPostInv(ig map[string]any, raw json.RawMessage) error {
	var entries []any
	switch v := ig["PostInvocation"].(type) {
	case nil:
	case []any:
		entries = v
	default:
		return errors.New("intentgate.PostInvocation is not an array")
	}
	var want []json.RawMessage
	if err := json.Unmarshal(raw, &want); err != nil {
		return fmt.Errorf("internal error: %v", err)
	}
	if !runsAntigravityPostInv(entries) {
		for _, w := range want {
			entries = append(entries, w)
		}
	}
	ig["PostInvocation"] = entries
	return nil
}

func runsAntigravityPre(entries []any, wanted string) bool {
	for _, e := range entries {
		em, _ := e.(map[string]any)
		matcher, _ := em["matcher"].(string)
		if !matcherCovers(matcher, wanted) {
			continue
		}
		hs, _ := em["hooks"].([]any)
		for _, h := range hs {
			hm, _ := h.(map[string]any)
			if cmd, _ := hm["command"].(string); isHookCommand(cmd, "intentgate", "hook", "antigravity", "pre") {
				return true
			}
		}
	}
	return false
}

func runsAntigravityPostInv(entries []any) bool {
	for _, h := range entries {
		hm, _ := h.(map[string]any)
		if cmd, _ := hm["command"].(string); isHookCommand(cmd, "intentgate", "hook", "antigravity", "post-invocation") {
			return true
		}
	}
	return false
}
