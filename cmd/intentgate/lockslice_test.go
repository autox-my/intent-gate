package main

import (
	"os/exec"
	"testing"
)

// TestLockSliceScript runs scripts/lock_slice_test.sh, so scripts/check.sh covers scripts/lock-slice.sh.
func TestLockSliceScript(t *testing.T) {
	if _, err := exec.LookPath("bash"); err != nil {
		t.Skip("bash not found")
	}
	cmd := exec.Command("bash", "scripts/lock_slice_test.sh")
	cmd.Dir = "../.."
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("scripts/lock_slice_test.sh: %v\n%s", err, out)
	}
}
