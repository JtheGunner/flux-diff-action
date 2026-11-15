// Package fluxdiffaction_test holds static checks of the composite action
// definition, which cannot run outside GitHub Actions.
package fluxdiffaction_test

import (
	"os"
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
