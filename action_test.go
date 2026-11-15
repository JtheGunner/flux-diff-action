// Package fluxdiffaction_test holds static checks of the composite action
// definition, which cannot run outside GitHub Actions.
package fluxdiffaction_test

import (
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"testing"

	"gopkg.in/yaml.v3"
)

type actionFile struct {
	Runs struct {
		Using string `yaml:"using"`
		Steps []struct {
			Name string            `yaml:"name"`
			Uses string            `yaml:"uses"`
			Run  string            `yaml:"run"`
			Env  map[string]string `yaml:"env"`
		} `yaml:"steps"`
	} `yaml:"runs"`
	Inputs map[string]struct {
		Required bool   `yaml:"required"`
		Default  string `yaml:"default"`
	} `yaml:"inputs"`
}

func loadAction(t *testing.T) (actionFile, string) {
	t.Helper()

	data, err := os.ReadFile("action.yml")
	if err != nil {
		t.Fatalf("read action.yml: %v", err)
	}
	var action actionFile
	if err := yaml.Unmarshal(data, &action); err != nil {
		t.Fatalf("parse action.yml: %v", err)
	}

	return action, string(data)
}

func TestAction_RunScriptsHaveNoExpressions(t *testing.T) {
	action, _ := loadAction(t)

	if action.Runs.Using != "composite" || len(action.Runs.Steps) == 0 {
		t.Fatalf("action is not a composite action with steps")
	}
	for _, step := range action.Runs.Steps {
		if strings.Contains(step.Run, "${{") {
			t.Errorf("step %q interpolates an expression inside run:; pass it through env:", step.Name)
		}
	}
}

func TestAction_UsesArePinnedToCommitSHAs(t *testing.T) {
	action, _ := loadAction(t)
	pinned := regexp.MustCompile(`^[\w.-]+/[\w./-]+@[0-9a-f]{40}$`)

	for _, step := range action.Runs.Steps {
		if step.Uses == "" || step.Uses == "./" {
			continue
		}
		if !pinned.MatchString(step.Uses) {
			t.Errorf("step %q uses %q, want a full commit SHA", step.Name, step.Uses)
		}
	}
}

func TestWorkflows_UsesArePinnedToCommitSHAsWithVersionComment(t *testing.T) {
	files, err := filepath.Glob(".github/workflows/*.y*ml")
	if err != nil || len(files) == 0 {
		t.Fatalf("no workflow files found: %v", err)
	}
	usesLine := regexp.MustCompile(`^\s*(?:-\s+)?uses:\s*(\S+)(.*)$`)
	pinned := regexp.MustCompile(`^[\w.-]+/[\w./-]+@[0-9a-f]{40}$`)
	versionComment := regexp.MustCompile(`^\s+#\s*v\d+\.\d+\.\d+\s*$`)

	for _, file := range files {
		data, err := os.ReadFile(file)
		if err != nil {
			t.Fatal(err)
		}
		for number, line := range strings.Split(string(data), "\n") {
			match := usesLine.FindStringSubmatch(line)
			if match == nil || match[1] == "./" {
				continue
			}
			if !pinned.MatchString(match[1]) || !versionComment.MatchString(match[2]) {
				t.Errorf("%s:%d: %q must be pinned to a full commit SHA with a trailing # vX.Y.Z comment", file, number+1, strings.TrimSpace(line))
			}
		}
	}
}

func TestAction_TokenOnlyReachesTheCommentStep(t *testing.T) {
	action, raw := loadAction(t)

	for _, step := range action.Runs.Steps {
		_, hasToken := step.Env["GITHUB_TOKEN"]
		if hasToken != (step.Name == "Post comment") {
			t.Errorf("step %q: GITHUB_TOKEN in env = %v", step.Name, hasToken)
		}
	}
	if strings.Count(raw, "github.token")+strings.Count(raw, "secrets.") != 1 {
		t.Error("the token must be referenced exactly once")
	}
	if strings.Contains(raw, "pull_request_target") && !strings.HasPrefix(strings.TrimSpace(commentLineWith(raw, "pull_request_target")), "#") {
		t.Error("pull_request_target may only be mentioned in a comment")
	}
}

func TestAction_VersionIsRequiredAndFailOnErrorDefaultsOff(t *testing.T) {
	action, _ := loadAction(t)

	if !action.Inputs["version"].Required {
		t.Error("version must be required")
	}
	if action.Inputs["fail-on-error"].Default != "false" || action.Inputs["max-comment-size"].Default != "65000" {
		t.Error("unexpected defaults for fail-on-error or max-comment-size")
	}
}

func commentLineWith(text, needle string) string {
	for _, line := range strings.Split(text, "\n") {
		if strings.Contains(line, needle) {
			return line
		}
	}

	return ""
}

// stepRun is the observable result of executing one action step script.
type stepRun struct {
	exitCode int
	stdout   string
	output   string // content of $GITHUB_OUTPUT
	summary  string // content of $GITHUB_STEP_SUMMARY
}

// runStep executes the run: script of the named step with bash. The runner
// temp directory holds a stub flux-diff that exits with stubExit and a
// comment.md body.
func runStep(t *testing.T, name string, stubExit int, env map[string]string) stepRun {
	t.Helper()

	action, _ := loadAction(t)
	script := ""
	for _, step := range action.Runs.Steps {
		if step.Name == name {
			script = step.Run
		}
	}
	if script == "" {
		t.Fatalf("no step named %q", name)
	}

	temp := t.TempDir()
	dir := filepath.Join(temp, "flux-diff-action")
	if err := os.MkdirAll(filepath.Join(dir, "bin"), 0o750); err != nil {
		t.Fatal(err)
	}
	stub := fmt.Sprintf("#!/bin/sh\nexit %d\n", stubExit)
	if err := os.WriteFile(filepath.Join(dir, "bin", "flux-diff"), []byte(stub), 0o700); err != nil { //nolint:gosec // test stub must be executable
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "comment.md"), []byte("<!-- flux-diff -->\nFAKE-BODY\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	outputFile, summaryFile := filepath.Join(temp, "output"), filepath.Join(temp, "summary")

	cmd := exec.Command("bash", "-c", script)
	cmd.Env = []string{
		"PATH=" + os.Getenv("PATH"), "RUNNER_TEMP=" + temp,
		"GITHUB_OUTPUT=" + outputFile, "GITHUB_STEP_SUMMARY=" + summaryFile,
	}
	for key, value := range env {
		cmd.Env = append(cmd.Env, key+"="+value)
	}
	out, err := cmd.Output()

	run := stepRun{stdout: string(out)}
	var exitErr *exec.ExitError
	switch {
	case errors.As(err, &exitErr):
		run.exitCode = exitErr.ExitCode()
	case err != nil:
		t.Fatalf("run step %q: %v", name, err)
	}
	output, _ := os.ReadFile(outputFile)
	summary, _ := os.ReadFile(summaryFile)
	run.output, run.summary = string(output), string(summary)

	return run
}

func TestAction_PostCommentFailureDoesNotFailTheJobByDefault(t *testing.T) {
	env := map[string]string{"PR_NUMBER": "3", "IS_FORK": "false", "MAX_SIZE": "65000", "FAIL_ON_ERROR": "false"}

	run := runStep(t, "Post comment", 1, env)
	if run.exitCode != 0 {
		t.Errorf("exit code = %d, want 0 when fail-on-error is false", run.exitCode)
	}
	if !strings.Contains(run.stdout, "::warning::") || !strings.Contains(run.summary, "FAKE-BODY") ||
		!strings.Contains(run.output, "outcome=step-summary") {
		t.Errorf("want a warning, the body in the step summary and the outcome: %+v", run)
	}
}

func TestAction_PostCommentFailureFailsWhenAskedTo(t *testing.T) {
	env := map[string]string{"PR_NUMBER": "3", "IS_FORK": "false", "MAX_SIZE": "65000", "FAIL_ON_ERROR": "true"}

	run := runStep(t, "Post comment", 1, env)
	if run.exitCode == 0 || !strings.Contains(run.summary, "FAKE-BODY") {
		t.Errorf("want a non-zero exit and the body in the step summary: %+v", run)
	}
}

func TestAction_PostCommentSuccessRecordsOutputs(t *testing.T) {
	env := map[string]string{"PR_NUMBER": "3", "IS_FORK": "true", "MAX_SIZE": "65000", "FAIL_ON_ERROR": "true"}

	run := runStep(t, "Post comment", 0, env)
	if run.exitCode != 0 || run.summary != "" {
		t.Errorf("unexpected failure handling on success: %+v", run)
	}
}

func TestAction_ReportResultWarnsByDefaultAndFailsOnRequest(t *testing.T) {
	warn := runStep(t, "Report result", 0, map[string]string{"EXIT_CODE": "1", "FAIL_ON_ERROR": "false"})
	if warn.exitCode != 0 || !strings.Contains(warn.stdout, "::warning::") {
		t.Errorf("default: want exit 0 with a warning: %+v", warn)
	}

	fail := runStep(t, "Report result", 0, map[string]string{"EXIT_CODE": "1", "FAIL_ON_ERROR": "true"})
	if fail.exitCode == 0 || !strings.Contains(fail.stdout, "::error::") {
		t.Errorf("fail-on-error: want a non-zero exit: %+v", fail)
	}
}
