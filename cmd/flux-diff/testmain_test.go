package main

import (
	"os"
	"testing"
)

// TestMain clears the variables a GitHub Actions runner sets, so the tests give
// the same result on a developer machine and in CI. Tests that need one set it
// with t.Setenv.
func TestMain(m *testing.M) {
	for _, name := range []string{
		"GITHUB_WORKSPACE", "GITHUB_EVENT_PATH", "GITHUB_EVENT_NAME", "GITHUB_OUTPUT",
		"GITHUB_STEP_SUMMARY", "GITHUB_TOKEN", "GITHUB_API_URL", "GITHUB_REPOSITORY",
	} {
		_ = os.Unsetenv(name)
	}

	os.Exit(m.Run())
}
