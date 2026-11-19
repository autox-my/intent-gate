package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"strings"

	"github.com/autox-my/intent-gate/internal/forge/github"
	"github.com/autox-my/intent-gate/internal/policy"
	"github.com/autox-my/intent-gate/internal/report"
)

// `publish` sends what `verify --out-dir` wrote to GitHub: one check run, at most one comment
// (pr-check.md §5). The SARIF upload stays with the Action. It never exits 1, because findings were
// already reported by verify.
//
// Exit codes: 0 published, or nothing to publish; 2 usage or no GITHUB_TOKEN; 3 an unreadable or
// invalid out dir, or any API error.

const publishUsage = "usage: intentgate publish --out-dir <dir> --repo <owner/name> --pr <n> --head-sha <sha> [--bot-login <login>]\n"

const (
	checkName       = "IntentGate"
	defaultBotLogin = "github-actions[bot]"
)

var shaArg = regexp.MustCompile(`^[0-9a-fA-F]{40,64}$`)

func runPublish(args []string, stdout io.Writer, deps Deps) int {
	t := newTerm("publish", nil, stdout, deps)
	fs := newFlags("publish")
	outDir := fs.String("out-dir", "", "")
	repoFlag := fs.String("repo", "", "")
	pr := fs.Int("pr", 0, "")
	headSHA := fs.String("head-sha", "", "")
	botLogin := fs.String("bot-login", defaultBotLogin, "")
	if err := fs.Parse(args); err != nil || fs.NArg() > 0 {
		return t.usage(publishUsage, "")
	}
	owner, name, ok := splitRepo(*repoFlag)
	switch {
	case *outDir == "":
		return t.usage(publishUsage, "--out-dir is required")
	case !ok || !validRepo(*repoFlag):
		return t.usage(publishUsage, "--repo must be owner/name")
	case *pr <= 0:
		return t.usage(publishUsage, "--pr must be a pull request number")
	case !shaArg.MatchString(*headSHA):
		return t.usage(publishUsage, "--head-sha must be a full commit SHA")
	case *botLogin == "":
		return t.usage(publishUsage, "--bot-login must not be empty")
	}
	token := strings.TrimSpace(os.Getenv("GITHUB_TOKEN"))
	if token == "" {
		return t.fail(exitUsage, "intentgate publish needs a GitHub token in GITHUB_TOKEN")
	}
	dir := resolve(deps.Dir, *outDir)

	dec, err := loadDecision(dir)
	if err != nil {
		return t.fail(exitUnverifiable, "intentgate publish: %v", err)
	}
	if !dec.Publish {
		return 0
	}
	summary, err := readText(dir, "summary.md")
	if err != nil {
		return t.fail(exitUnverifiable, "intentgate publish: %v", err)
	}
	var comment string
	if dec.Comment != policy.None {
		if comment, err = readText(dir, "comment.md"); err != nil {
			return t.fail(exitUnverifiable, "intentgate publish: %v", err)
		}
	}

	api := os.Getenv("GITHUB_API_URL")
	if api == "" {
		api = defaultAPIURL
	}
	client := &github.Client{BaseURL: api, Token: token}
	ctx := context.Background()

	// The two writes are independent: a failure of one is reported and the other still runs.
	var failed bool
	report1 := func(err error) {
		failed = true
		fmt.Fprintf(t.errOut, "intentgate publish: %v\n", err)
	}
	cr := github.CheckRun{Name: checkName, HeadSHA: strings.ToLower(*headSHA), Conclusion: dec.Conclusion, Title: dec.Title, Summary: summary}
	if msg, err := publishCheckRun(ctx, client, owner, name, cr); err != nil {
		report1(err)
	} else {
		fmt.Fprintln(stdout, msg)
	}
	if dec.Comment != policy.None {
		if msg, err := publishComment(ctx, client, owner, name, *pr, dec.Comment, comment, *botLogin); err != nil {
			report1(err)
		} else if msg != "" {
			fmt.Fprintln(stdout, msg)
		}
	}
	if failed {
		return exitUnverifiable
	}
	return 0
}

// publishCheckRun updates the check run of this name on this head SHA, else creates it: one write.
func publishCheckRun(ctx context.Context, c *github.Client, owner, repo string, cr github.CheckRun) (string, error) {
	id, found, err := c.FindCheckRun(ctx, owner, repo, cr.HeadSHA, cr.Name)
	if err != nil {
		return "", err
	}
	if found {
		if err := c.UpdateCheckRun(ctx, owner, repo, id, cr); err != nil {
			return "", err
		}
		return fmt.Sprintf("updated check run %d", id), nil
	}
	if id, err = c.CreateCheckRun(ctx, owner, repo, cr); err != nil {
		return "", err
	}
	return fmt.Sprintf("created check run %d", id), nil
}

// publishComment edits the bot's marker comment; with none it creates one for upsert and does
// nothing for update-if-exists. Two runs that race can each create one: after a create it lists
// again, and whenever more than one bot comment starts with the marker it keeps the oldest (lowest
// id), updates it and deletes the others, so the pull request converges to one comment.
func publishComment(ctx context.Context, c *github.Client, owner, repo string, pr int, mode policy.CommentMode, body, bot string) (string, error) {
	ids, err := c.FindComments(ctx, owner, repo, pr, report.CommentMarker, bot)
	if err != nil {
		return "", err
	}
	verb := "updated"
	if len(ids) == 0 {
		if mode != policy.Upsert {
			return "", nil
		}
		if _, err := c.CreateComment(ctx, owner, repo, pr, body); err != nil {
			return "", err
		}
		verb = "created"
		if ids, err = c.FindComments(ctx, owner, repo, pr, report.CommentMarker, bot); err != nil {
			return "", err
		}
		if len(ids) == 0 {
			return "", fmt.Errorf("github: the comment just created is not in the list of comments of #%d", pr)
		}
	}
	keep := ids[0]
	if verb == "updated" || len(ids) > 1 {
		if err := c.UpdateComment(ctx, owner, repo, keep, body); err != nil {
			return "", err
		}
	}
	for _, id := range ids[1:] {
		if err := c.DeleteComment(ctx, owner, repo, id); err != nil {
			return "", err
		}
	}
	msg := fmt.Sprintf("%s comment %d", verb, keep)
	if len(ids) > 1 {
		msg += fmt.Sprintf(" (deleted %d duplicate)", len(ids)-1)
	}
	return msg, nil
}

// loadDecision reads and checks decision.json in dir.
func loadDecision(dir string) (policy.Decision, error) {
	data, err := os.ReadFile(filepath.Join(dir, "decision.json"))
	if err != nil {
		return policy.Decision{}, fmt.Errorf("cannot read decision.json: %w", err)
	}
	var d decisionFile
	dec := json.NewDecoder(bytes.NewReader(data))
	if err := dec.Decode(&d); err != nil {
		return policy.Decision{}, fmt.Errorf("decision.json is not valid: %v", err)
	}
	out := policy.Decision{Publish: d.Publish, Conclusion: d.Conclusion, Comment: policy.CommentMode(d.Comment), Title: d.Title}
	if !d.Publish {
		return out, nil
	}
	switch out.Comment {
	case policy.None, policy.Upsert, policy.UpdateIfExists:
	default:
		return policy.Decision{}, fmt.Errorf("decision.json: unknown comment mode %q", d.Comment)
	}
	if d.Title == "" {
		return policy.Decision{}, errors.New("decision.json: a decision to publish needs a title")
	}
	// Phase 0 is advisory: a check run may only conclude success or neutral, never block.
	if d.Conclusion != "success" && d.Conclusion != "neutral" {
		return policy.Decision{}, fmt.Errorf("decision.json: conclusion %q must be success or neutral", d.Conclusion)
	}
	return out, nil
}

func readText(dir, name string) (string, error) {
	data, err := os.ReadFile(filepath.Join(dir, name))
	if err != nil {
		return "", fmt.Errorf("cannot read %s: %w", name, errors.Unwrap(err))
	}
	return string(data), nil
}
