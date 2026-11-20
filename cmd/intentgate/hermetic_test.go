package main

import (
	"os"
	"os/exec"
	"strings"
	"testing"
)

// hermeticChild marks the re-run below, so the child never recurses into this test.
const hermeticChild = "INTENTGATE_HERMETIC_CHILD"

// TestHermetic_PlanReviewEnv proves the tests in this package ignore a developer's plan-review environment:
// it re-runs the lock test (and TestHermetic_ProviderEnvCleared) as a subprocess whose environment sets all
// four provider variables, with the base URL on a closed port (nothing leaves the machine), and expects both
// to pass. -test.v and the output check stop a renamed or missing test from passing vacuously.
func TestHermetic_PlanReviewEnv(t *testing.T) {
	if os.Getenv(hermeticChild) != "" {
		t.Skip("child run of TestHermetic_PlanReviewEnv")
	}
	cmd := exec.Command(os.Args[0],
		"-test.v",
		"-test.run=^(TestHook_ApprovedPlanWritesLock|TestHermetic_ProviderEnvCleared)$",
		"-test.count=1")
	cmd.Env = append(os.Environ(),
		hermeticChild+"=1",
		"TYPESAFE_API_KEY=hermetic-test-key",
		"TYPESAFE_BASE_URL=http://127.0.0.1:9",
		"TYPESAFE_MODEL=hermetic-test-model",
		"INTENTGATE_PLAN_REVIEW=on",
	)
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("child run fails when the shell exports the plan-review provider variables: %v\n%s", err, out)
	}
	for _, want := range []string{"--- PASS: TestHook_ApprovedPlanWritesLock", "--- PASS: TestHermetic_ProviderEnvCleared"} {
		if !strings.Contains(string(out), want) {
			t.Fatalf("child output lacks %q (test renamed, missing or skipped?)\n%s", want, out)
		}
	}
}

// TestHermetic_ProviderEnvCleared asserts TestMain removed every plan-review provider variable: it runs in
// the child above, where all four were set, so dropping a name from TestMain's list fails it.
func TestHermetic_ProviderEnvCleared(t *testing.T) {
	// Deliberately its own list, not shared with TestMain: dropping a name there must fail here.
	for _, k := range []string{"TYPESAFE_API_KEY", "TYPESAFE_BASE_URL", "TYPESAFE_MODEL", "INTENTGATE_PLAN_REVIEW"} {
		if v, ok := os.LookupEnv(k); ok {
			t.Errorf("%s is set (%q); TestMain must unset it", k, v)
		}
	}
}

// TestMain unsets the plan-review provider variables before any test runs, so every test here (and every
// subprocess it starts) sees no provider unless it injects Deps.PlanReviewer or sets them with t.Setenv.
func TestMain(m *testing.M) {
	for _, k := range []string{"TYPESAFE_API_KEY", "TYPESAFE_BASE_URL", "TYPESAFE_MODEL", "INTENTGATE_PLAN_REVIEW"} {
		os.Unsetenv(k)
	}
	os.Exit(m.Run())
}
