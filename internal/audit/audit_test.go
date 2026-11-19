package audit

import (
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/autox-my/intent-gate/internal/lock"
)

const (
	idA = "01JD8Z6Q4M7R2T9X3B5N8K1C6F"
	idB = "01JD8Z6Q4M7R2T9X3B5N8K1C7G"

	secretText = "SECRET criterion text"
	secretGlob = "secret/footprint/**"
	secretRoot = "9f2b7e-root-intent-hash"
)

func intp(n int) *int { return &n }

// twoLocks are given in the wrong order on purpose: the event sorts them by id. Their criteria,
// footprints and root intent hash must never reach the event.
func twoLocks() (hook, cli lock.Lock) {
	crit := []lock.Criterion{{ID: "C1", Text: secretText, Footprint: []string{secretGlob}}}
	hook = lock.Lock{
		ID: idA, Revision: 2, Hash: "sha256:aaaa", Criteria: crit,
		RootIntent:   lock.RootIntent{SHA256: secretRoot, Source: "claude-code-session"},
		Ratification: lock.Ratification{Ratifier: "git:dev@example.com", Method: "hook", ReviewDurationMS: intp(48210)},
	}
	cli = lock.Lock{
		ID: idB, Revision: 1, Hash: "sha256:bbbb", Criteria: crit,
		RootIntent:   lock.RootIntent{SHA256: secretRoot, Source: "plan-file"},
		Ratification: lock.Ratification{Ratifier: "git:dev@example.com", Method: "cli-confirm"},
	}
	return hook, cli
}

var at = time.Date(2026, 11, 18, 9, 20, 11, 999, time.UTC)

func jsonKeys(t reflect.Type) []string {
	var keys []string
	for i := 0; i < t.NumField(); i++ {
		keys = append(keys, strings.Split(t.Field(i).Tag.Get("json"), ",")[0])
	}
	return keys
}

// REQ-0042
func TestAuditEvent_RatificationMethod(t *testing.T) {
	hook, cli := twoLocks()

	t.Run("a verify event lists each lock's ratification method, sorted by id", func(t *testing.T) {
		e := NewVerifyEvent(at, "0.1.0", "owner/name", 42, Verify{
			BaseSHA: "b1", HeadSHA: "h1", BaseBranch: "main", Author: "octocat", AuthorType: "User",
			ConfigSHA256: "c0ffee", VerifySHA256: "5ec0de",
			Locks: []lock.Lock{cli, hook}, UnverifiableLocks: 2,
			Outcome: "findings", ExitCode: 1, Conclusion: "neutral",
		})
		want := `{"schema":"intentgate.audit/1","event":"verify","at":"2026-11-18T09:20:11Z","repo":"owner/name","pr":42,` +
			`"base_sha":"b1","head_sha":"h1","base_branch":"main","author":"octocat","author_type":"User",` +
			`"intentgate_version":"0.1.0","config_sha256":"c0ffee","verify_sha256":"5ec0de","locks":[` +
			`{"id":"` + idA + `","revision":2,"hash":"sha256:aaaa","ratification_method":"hook","review_duration_ms":48210},` +
			`{"id":"` + idB + `","revision":1,"hash":"sha256:bbbb","ratification_method":"cli-confirm","review_duration_ms":null}` +
			`],"unverifiable_locks":2,"outcome":"findings","exit_code":1,"conclusion":"neutral"}` + "\n"
		if got := string(JSON(e)); got != want {
			t.Errorf("JSON:\n got %s\nwant %s", got, want)
		}
	})

	t.Run("a lock event is one lock, written", func(t *testing.T) {
		e := NewLockEvent(EventAmend, hook, at, "dev", "")
		want := `{"schema":"intentgate.audit/1","event":"amend","at":"2026-11-18T09:20:11Z","repo":"","pr":0,` +
			`"base_sha":"","head_sha":"","base_branch":"","author":"","author_type":"",` +
			`"intentgate_version":"dev","config_sha256":"","verify_sha256":"","locks":[` +
			`{"id":"` + idA + `","revision":2,"hash":"sha256:aaaa","ratification_method":"hook","review_duration_ms":48210}` +
			`],"unverifiable_locks":0,"outcome":"written","exit_code":0,"conclusion":""}` + "\n"
		if got := string(JSON(e)); got != want {
			t.Errorf("JSON:\n got %s\nwant %s", got, want)
		}
	})

	t.Run("no locks is an empty list, not null", func(t *testing.T) {
		got := string(JSON(NewVerifyEvent(at, "dev", "", 0, Verify{})))
		if !strings.Contains(got, `"locks":[]`) {
			t.Errorf("JSON %s lacks \"locks\":[]", got)
		}
	})

	t.Run("the fields are exactly the design's, so none can carry criteria, footprints or intent text", func(t *testing.T) {
		wantEvent := []string{"schema", "event", "at", "repo", "pr", "base_sha", "head_sha", "base_branch", "author",
			"author_type", "intentgate_version", "config_sha256", "verify_sha256", "locks", "unverifiable_locks", "outcome", "exit_code", "conclusion"}
		if got := jsonKeys(reflect.TypeOf(Event{})); !reflect.DeepEqual(got, wantEvent) {
			t.Errorf("Event keys = %v, want %v", got, wantEvent)
		}
		wantLock := []string{"id", "revision", "hash", "ratification_method", "review_duration_ms"}
		if got := jsonKeys(reflect.TypeOf(LockEntry{})); !reflect.DeepEqual(got, wantLock) {
			t.Errorf("lock entry keys = %v, want %v", got, wantLock)
		}
		for _, e := range []Event{
			NewVerifyEvent(at, "0.1.0", "o/n", 1, Verify{Locks: []lock.Lock{hook, cli}}),
			NewLockEvent(EventLock, cli, at, "0.1.0", "o/n"),
		} {
			out := string(JSON(e))
			for _, leak := range []string{secretText, secretGlob, secretRoot, "plan-file", "claude-code-session", "dev@example.com"} {
				if strings.Contains(out, leak) {
					t.Errorf("the event holds %q: %s", leak, out)
				}
			}
		}
	})
}
