package main

import (
	"bytes"
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

var update = flag.Bool("update", false, "rewrite golden files")

const (
	flateFixtures = "../../testdata/flate"
	goldenDir     = "../../testdata/golden"
)

// fakeFlate writes a shell script that mimics the flate CLI and returns its path.
func fakeFlate(t *testing.T, stdoutFixture, stderrFixture, exitCode string) string {
	t.Helper()

	script := `#!/bin/sh
if [ "$1" = "--version" ]; then
  echo "flate v0.7.3"
  exit 0
fi
if [ "$1" = "build" ]; then
  if [ -n "$FAKE_FLATE_BUILD_STDERR" ]; then cat "$FAKE_FLATE_BUILD_STDERR" >&2; fi
  exit "${FAKE_FLATE_BUILD_EXIT:-0}"
fi
if [ -n "$FAKE_FLATE_ARGS" ]; then printf '%s\n' "$@" > "$FAKE_FLATE_ARGS"; fi
if [ -n "$FAKE_FLATE_STDOUT" ]; then cat "$FAKE_FLATE_STDOUT"; fi
if [ -n "$FAKE_FLATE_STDERR" ]; then cat "$FAKE_FLATE_STDERR" >&2; fi
exit "$FAKE_FLATE_EXIT"
`
	path := filepath.Join(t.TempDir(), "flate")
	if err := os.WriteFile(path, []byte(script), 0o700); err != nil {
		t.Fatalf("write fake binary: %v", err)
	}

	t.Setenv("FAKE_FLATE_EXIT", exitCode)
	t.Setenv("FAKE_FLATE_STDOUT", fixtureAbs(t, stdoutFixture))
	t.Setenv("FAKE_FLATE_STDERR", fixtureAbs(t, stderrFixture))

	return path
}

func fixtureAbs(t *testing.T, name string) string {
	t.Helper()

	if name == "" {
		return ""
	}
	abs, err := filepath.Abs(filepath.Join(flateFixtures, name))
	if err != nil {
		t.Fatalf("resolve fixture: %v", err)
	}

	return abs
}

func execute(t *testing.T, args ...string) (stdout string, err error) {
	t.Helper()

	var out bytes.Buffer
	cmd := newRootCommand()
	cmd.SetOut(&out)
	cmd.SetErr(&out)
	cmd.SetArgs(args)
	err = cmd.Execute()

	return out.String(), err
}

func assertGolden(t *testing.T, name, got string) {
	t.Helper()

	path := filepath.Join(goldenDir, name)
	if *update {
		if err := os.MkdirAll(goldenDir, 0o755); err != nil {
			t.Fatalf("create golden dir: %v", err)
		}
		if err := os.WriteFile(path, []byte(got), 0o644); err != nil {
			t.Fatalf("write golden: %v", err)
		}
	}

	want, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read golden %s (run with -update): %v", name, err)
	}
	if got != string(want) {
		t.Errorf("output differs from golden %s (run with -update and review the diff)\n--- got ---\n%s", name, got)
	}
}

func TestCLI_EndToEnd_Fixture(t *testing.T) {
	binary := fakeFlate(t, "changes.json", "stderr-warnings.txt", "0")

	out, err := execute(t, "--flate-binary", binary, "--base", "main", "--path", ".")
	if err != nil {
		t.Fatalf("run: %v", err)
	}

	assertGolden(t, "cli-markdown.md", out)
}

func TestCLI_EndToEnd_GroupsExpectedNotices(t *testing.T) {
	binary := fakeFlate(t, "changes.json", "stderr-grouped-warnings.txt", "0")

	out, err := execute(t, "--flate-binary", binary, "--base", "main", "--path", ".")
	if err != nil {
		t.Fatalf("run: %v", err)
	}

	assertGolden(t, "cli-markdown-grouped-notices.md", out)
}

func TestCLI_SecretFixture_NeverLeaksInOutputOrError(t *testing.T) {
	binary := fakeFlate(t, "secret-changes.json", "stderr-secret-failure.txt", "1")

	for _, outputFormat := range []string{"markdown", "text", "json"} {
		t.Run(outputFormat, func(t *testing.T) {
			out, err := execute(t, "--flate-binary", binary, "--format", outputFormat)
			if err == nil {
				t.Fatal("want an error for the failed unit")
			}
			if out == "" {
				t.Fatal("no output")
			}
			for _, text := range []string{out, err.Error()} {
				if strings.Contains(text, "FAKE-DO-NOT-USE") {
					t.Errorf("fake secret value leaked into %s output or error", outputFormat)
				}
			}
		})
	}
}

func TestCLI_ExitCodes(t *testing.T) {
	cases := []struct {
		name    string
		stderr  string
		exit    string
		args    []string
		wantErr string
	}{
		{"completed run with warnings", "stderr-warnings.txt", "0", nil, ""},
		{"strict turns partial into failure", "stderr-warnings.txt", "0", []string{"--strict"}, "result is partial"},
		{"failed unit", "stderr-failures.txt", "1", nil, "unit(s) failed"},
		{"clean run", "", "0", []string{"--strict"}, ""},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			binary := fakeFlate(t, "changes.json", tc.stderr, tc.exit)

			out, err := execute(t, append([]string{"--flate-binary", binary}, tc.args...)...)
			if out == "" {
				t.Error("the comment must be written even when the run fails")
			}
			switch {
			case tc.wantErr == "" && err != nil:
				t.Errorf("unexpected error: %v", err)
			case tc.wantErr != "" && (err == nil || !strings.Contains(err.Error(), tc.wantErr)):
				t.Errorf("error = %v, want containing %q", err, tc.wantErr)
			}
		})
	}
}

// buildFlate points the fake flate's build subcommand at a stderr fixture.
func buildFlate(t *testing.T, diffStderr, diffExit, buildStderr, buildExit string) string {
	t.Helper()

	binary := fakeFlate(t, "changes.json", diffStderr, diffExit)
	t.Setenv("FAKE_FLATE_BUILD_STDERR", fixtureAbs(t, buildStderr))
	t.Setenv("FAKE_FLATE_BUILD_EXIT", buildExit)

	return binary
}

func TestCLI_BrokenInBothSides_IsNamedWithReasonAndKeepsExitCode(t *testing.T) {
	binary := buildFlate(t, "", "0", "build-stderr-blocked.txt", "1")

	out, err := execute(t, "--flate-binary", binary)
	if err != nil {
		t.Fatalf("a unit broken in base and head must not fail the run: %v", err)
	}

	for _, want := range []string{
		"Already broken in base and head",
		"Kustomization flux-system/broken-app",
		"blocks: flux-system/good-app",
		"reason not shown",
		"Result is incomplete",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("output lacks %q:\n%s", want, out)
		}
	}
}

func TestCLI_BrokenInBothSides_StrictFailsTheRun(t *testing.T) {
	binary := buildFlate(t, "", "0", "build-stderr-broken.txt", "1")

	_, err := execute(t, "--flate-binary", binary, "--strict")
	if err == nil || !strings.Contains(err.Error(), "Already broken in base and head") {
		t.Errorf("--strict error = %v, want it to name the broken unit", err)
	}
}

func TestCLI_BrokenOnOneSideOnly_StaysANormalFailure(t *testing.T) {
	binary := buildFlate(t, "stderr-one-sided-failure.txt", "1", "build-stderr-broken.txt", "1")

	out, err := execute(t, "--flate-binary", binary)
	if err == nil || !strings.Contains(err.Error(), "unit(s) failed") {
		t.Errorf("error = %v, want a normal unit failure", err)
	}
	if strings.Contains(out, "Already broken in base and head") {
		t.Errorf("a unit the diff reports must not be listed as broken in both sides:\n%s", out)
	}
}

// The reason of a unit broken in base and head is never shown: the diff holds
// no objects for it, so a value quoted in the reason could not be masked. The
// value below is deliberately not among the values of the diff's changes.
func TestCLI_BrokenInBothSides_ReasonNeverReachesAnyOutput(t *testing.T) {
	const reasonOnlyValue = "FAKE-DO-NOT-USE-build-only-value"

	binary := buildFlate(t, "", "0", "build-stderr-secret-failure.txt", "1")

	for _, outputFormat := range []string{"markdown", "text", "json"} {
		for _, strict := range []bool{false, true} {
			t.Run(fmt.Sprintf("%s strict=%t", outputFormat, strict), func(t *testing.T) {
				args := []string{"--flate-binary", binary, "--format", outputFormat}
				if strict {
					args = append(args, "--strict")
				}

				out, err := execute(t, args...)
				if !strings.Contains(out, "broken-app") {
					t.Fatalf("broken unit missing from output:\n%s", out)
				}
				if strict && (err == nil || !strings.Contains(err.Error(), "broken-app")) {
					t.Errorf("--strict error = %v, want it to name the unit", err)
				}
				for _, text := range []string{out, fmt.Sprint(err)} {
					if strings.Contains(text, reasonOnlyValue) || strings.Contains(text, "rendering failed") {
						t.Errorf("the reason leaked into %s output or error: %s", outputFormat, text)
					}
				}
			})
		}
	}
}

func TestCLI_UnexpectedBuildExitCodeFailsTheRun(t *testing.T) {
	binary := buildFlate(t, "", "0", "", "2")

	if _, err := execute(t, "--flate-binary", binary); err == nil {
		t.Error("an unexpected flate build exit code must fail the run")
	}
}

func TestCLI_UnsupportedFlagValues(t *testing.T) {
	binary := fakeFlate(t, "changes.json", "", "0")

	cases := map[string]struct {
		args []string
		want string
	}{
		"head":    {[]string{"--head", "feature"}, "--head other than the working tree is not supported yet"},
		"format":  {[]string{"--format", "xml"}, "unsupported --format"},
		"nothing": {nil, ""},
	}
	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			_, err := execute(t, append([]string{"--flate-binary", binary}, tc.args...)...)
			if tc.want == "" {
				if err != nil {
					t.Errorf("unexpected error: %v", err)
				}

				return
			}
			if err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Errorf("error = %v, want containing %q", err, tc.want)
			}
		})
	}
}

// hangingFlate answers --version and then hangs.
func hangingFlate(t *testing.T) string {
	t.Helper()

	script := `#!/bin/sh
if [ "$1" = "--version" ]; then
  echo "flate v0.7.3"
  exit 0
fi
exec sleep 30
`
	path := filepath.Join(t.TempDir(), "flate")
	if err := os.WriteFile(path, []byte(script), 0o700); err != nil {
		t.Fatalf("write fake binary: %v", err)
	}

	return path
}

func TestCLI_HangingFlate_ReportsFailedResultAndStillWritesTheComment(t *testing.T) {
	binary := hangingFlate(t)
	output := filepath.Join(t.TempDir(), "comment.md")

	started := time.Now()
	_, err := execute(t, "--flate-binary", binary, "--flate-timeout", "300ms", "--output", output)

	if err == nil || !strings.Contains(err.Error(), "timed out") {
		t.Fatalf("error = %v, want a timeout failure", err)
	}
	if elapsed := time.Since(started); elapsed > 10*time.Second {
		t.Errorf("run took %s, want it to stop shortly after the timeout", elapsed)
	}

	comment, readErr := os.ReadFile(output)
	if readErr != nil {
		t.Fatalf("the comment must be written when flate times out: %v", readErr)
	}
	for _, want := range []string{"timed out after 300ms", "flate"} {
		if !strings.Contains(string(comment), want) {
			t.Errorf("comment does not contain %q:\n%s", want, comment)
		}
	}
	if strings.Contains(string(comment), "No changes") {
		t.Errorf("a timed-out run must not read as clean:\n%s", comment)
	}
}

func TestCLI_HangingBuild_IsReportedAndKeepsTheDiff(t *testing.T) {
	binary := fakeFlate(t, "changes.json", "", "0")
	script := "#!/bin/sh\nif [ \"$1\" = \"--version\" ]; then echo 'flate v0.7.3'; exit 0; fi\n" +
		"if [ \"$1\" = \"build\" ]; then exec sleep 30; fi\n" +
		"cat \"$FAKE_FLATE_STDOUT\"\n"
	if err := os.WriteFile(binary, []byte(script), 0o700); err != nil {
		t.Fatalf("write fake binary: %v", err)
	}
	output := filepath.Join(t.TempDir(), "comment.md")

	_, err := execute(t, "--flate-binary", binary, "--flate-timeout", "300ms", "--output", output)
	if err == nil || !strings.Contains(err.Error(), "timed out") {
		t.Fatalf("error = %v, want a timeout failure", err)
	}

	comment, readErr := os.ReadFile(output)
	if readErr != nil {
		t.Fatalf("read comment: %v", readErr)
	}
	if !strings.Contains(string(comment), "timed out after 300ms") {
		t.Errorf("comment misses the timeout:\n%s", comment)
	}
}

func TestCLI_FlateTimeoutMustBePositive(t *testing.T) {
	binary := fakeFlate(t, "changes.json", "", "0")

	for _, value := range []string{"0s", "-1m"} {
		_, err := execute(t, "--flate-binary", binary, "--flate-timeout", value)
		if err == nil || !strings.Contains(err.Error(), "--flate-timeout") {
			t.Errorf("--flate-timeout %s: error = %v, want a validation error", value, err)
		}
	}
}
