package main

import (
	"os"
	"os/exec"
	"testing"
)

// hermeticChild marks the re-run below, so the child never recurses into this test.
const hermeticChild = "INTENTGATE_HERMETIC_CHILD"

// TestHermetic_PlanReviewEnv proves the tests in this package ignore a developer's plan-review environment:
// it re-runs the lock test as a subprocess whose environment holds a provider key pointing at a closed
// port (nothing leaves the machine) and expects the same result as a clean shell.
func TestHermetic_PlanReviewEnv(t *testing.T) {
	if os.Getenv(hermeticChild) != "" {
		t.Skip("child run of TestHermetic_PlanReviewEnv")
	}
	cmd := exec.Command(os.Args[0], "-test.run=^TestHook_ApprovedPlanWritesLock$", "-test.count=1")
	cmd.Env = append(os.Environ(),
		hermeticChild+"=1",
		"TYPESAFE_API_KEY=hermetic-test-key",
		"TYPESAFE_BASE_URL=http://127.0.0.1:9",
	)
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("TestHook_ApprovedPlanWritesLock fails when the shell exports the plan-review provider variables: %v\n%s", err, out)
	}
}
