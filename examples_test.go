package fluxdiffaction_test

import (
	"os"
	"strings"
	"testing"

	"gopkg.in/yaml.v3"
)

type exampleWorkflow struct {
	On          any               `yaml:"on"`
	Permissions map[string]string `yaml:"permissions"`
	Jobs        map[string]struct {
		Steps []struct {
			Uses string         `yaml:"uses"`
			With map[string]any `yaml:"with"`
		} `yaml:"steps"`
	} `yaml:"jobs"`
}

func loadExample(t *testing.T) (exampleWorkflow, string) {
	t.Helper()

	data, err := os.ReadFile("examples/workflow.yaml")
	if err != nil {
		t.Fatalf("read examples/workflow.yaml: %v", err)
	}
	var wf exampleWorkflow
	if err := yaml.Unmarshal(data, &wf); err != nil {
		t.Fatalf("parse examples/workflow.yaml: %v", err)
	}

	return wf, string(data)
}

func TestExampleWorkflow_UsesSafeTriggerAndPermissions(t *testing.T) {
	wf, raw := loadExample(t)

	if on, ok := wf.On.(string); !ok || on != "pull_request" {
		t.Errorf("on = %v, want pull_request", wf.On)
	}
	for _, line := range strings.Split(raw, "\n") {
		if strings.Contains(line, "pull_request_target") && !strings.HasPrefix(strings.TrimSpace(line), "#") {
			t.Errorf("pull_request_target must only appear in a comment: %q", line)
		}
	}
	if wf.Permissions["pull-requests"] != "write" || wf.Permissions["contents"] != "read" || len(wf.Permissions) != 2 {
		t.Errorf("permissions = %v, want contents: read and pull-requests: write", wf.Permissions)
	}
}

func TestExampleWorkflow_CheckoutAndActionInputs(t *testing.T) {
	wf, _ := loadExample(t)
	action, _ := loadAction(t)

	sawCheckout, sawAction := false, false
	for _, job := range wf.Jobs {
		for _, step := range job.Steps {
			switch {
			case strings.HasPrefix(step.Uses, "actions/checkout@"):
				sawCheckout = true
				if step.With["fetch-depth"] != 0 {
					t.Errorf("fetch-depth = %v, want 0", step.With["fetch-depth"])
				}
				if step.With["persist-credentials"] != false {
					t.Errorf("persist-credentials = %v, want false", step.With["persist-credentials"])
				}
				if step.With["ref"] != "${{ github.event.pull_request.head.sha }}" {
					t.Errorf("ref = %v, want the pull request head SHA", step.With["ref"])
				}
			case strings.HasPrefix(step.Uses, "JtheGunner/flux-diff-action@"):
				sawAction = true
				if _, ok := step.With["version"]; !ok {
					t.Error("the example must pass the required version input")
				}
				for name := range step.With {
					if _, ok := action.Inputs[name]; !ok {
						t.Errorf("example passes input %q that action.yml does not define", name)
					}
				}
			}
		}
	}
	if !sawCheckout || !sawAction {
		t.Errorf("example must use actions/checkout and the action (checkout=%v action=%v)", sawCheckout, sawAction)
	}
}
