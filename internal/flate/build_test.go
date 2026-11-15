package flate

import (
	"context"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/JtheGunner/flux-diff-action/internal/model"
)

func TestParseBuildFailures_FailedUnit(t *testing.T) {
	got := parseBuildFailures(string(readFixture(t, "build-stderr-broken.txt")))

	want := []model.Failure{{
		Kind:      "Kustomization",
		Namespace: "flux-system",
		Name:      "broken-app",
		Reason:    "kustomize build ./apps/broken-app: accumulating resources: stat apps/broken-app/does-not-exist.yaml: file does not exist",
	}}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("failures = %+v, want %+v", got, want)
	}
}

func TestParseBuildFailures_BlockedUnitsAttachToTheirFailure(t *testing.T) {
	got := parseBuildFailures(string(readFixture(t, "build-stderr-blocked.txt")))

	if len(got) != 1 || !reflect.DeepEqual(got[0].Blocks, []string{"flux-system/good-app"}) {
		t.Errorf("failures = %+v, want one failure blocking flux-system/good-app", got)
	}
}

func TestParseBuildFailures_SummaryLineIsNotAFailure(t *testing.T) {
	if got := parseBuildFailures("\n  ✗ 3 failed · 2 blocked\n"); len(got) != 0 {
		t.Errorf("failures = %+v, want none", got)
	}
}

func TestBrokenInBothSides(t *testing.T) {
	broken := model.Failure{Kind: "Kustomization", Namespace: "flux-system", Name: "broken-app", Reason: "r"}
	other := model.Failure{Kind: "Kustomization", Namespace: "flux-system", Name: "other", Reason: "r"}

	cases := map[string]struct {
		diff  []model.Failure
		build []model.Failure
		want  []model.Failure
	}{
		"silent in diff":             {nil, []model.Failure{broken}, []model.Failure{broken}},
		"reported on current side":   {[]model.Failure{{Snapshot: "current", Kind: broken.Kind, Namespace: broken.Namespace, Name: broken.Name}}, []model.Failure{broken}, nil},
		"reported on orig side":      {[]model.Failure{{Snapshot: "orig", Kind: broken.Kind, Namespace: broken.Namespace, Name: broken.Name}}, []model.Failure{broken}, nil},
		"only other unit in diff":    {[]model.Failure{{Snapshot: "current", Kind: other.Kind, Namespace: other.Namespace, Name: other.Name}}, []model.Failure{broken}, []model.Failure{broken}},
		"build has no failures":      {[]model.Failure{{Snapshot: "orig", Kind: other.Kind, Namespace: other.Namespace, Name: other.Name}}, nil, nil},
		"same name, different kind":  {[]model.Failure{{Snapshot: "current", Kind: "HelmRelease", Namespace: broken.Namespace, Name: broken.Name}}, []model.Failure{broken}, []model.Failure{broken}},
		"same name, other namespace": {[]model.Failure{{Snapshot: "current", Kind: broken.Kind, Namespace: "apps", Name: broken.Name}}, []model.Failure{broken}, []model.Failure{broken}},
	}
	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			if got := BrokenInBothSides(tc.diff, tc.build); !reflect.DeepEqual(got, tc.want) {
				t.Errorf("got %+v, want %+v", got, tc.want)
			}
		})
	}
}

func TestBuild_ReturnsFailuresAndReadsOnlyStderr(t *testing.T) {
	binary := fakeFlate(t, "flate version "+MinVersion, "changes.json", "build-stderr-broken.txt", "1")

	failures, err := Build(context.Background(), Options{Binary: binary})
	if err != nil {
		t.Fatalf("Build: %v", err)
	}
	if len(failures) != 1 || failures[0].Name != "broken-app" {
		t.Errorf("failures = %+v", failures)
	}
}

func TestBuild_UnexpectedExitCode_ReturnsError(t *testing.T) {
	binary := fakeFlate(t, "flate version "+MinVersion, "", "", "2")

	if _, err := Build(context.Background(), Options{Binary: binary}); err == nil {
		t.Fatal("want an error for exit code 2")
	}
}

func TestBuild_ForcesSkipKindsAndUsesBuildSubcommand(t *testing.T) {
	binary := fakeFlate(t, "flate version "+MinVersion, "", "", "0")
	argsFile := filepath.Join(t.TempDir(), "args")
	script := "#!/bin/sh\nif [ \"$1\" = \"--version\" ]; then echo \"$FAKE_FLATE_VERSION\"; exit 0; fi\necho \"$@\" > " + argsFile + "\n"
	if err := os.WriteFile(binary, []byte(script), 0o700); err != nil {
		t.Fatalf("write fake binary: %v", err)
	}

	if _, err := Build(context.Background(), Options{Binary: binary, Path: "clusters/prod"}); err != nil {
		t.Fatalf("Build: %v", err)
	}

	recorded, err := os.ReadFile(argsFile)
	if err != nil {
		t.Fatalf("read recorded args: %v", err)
	}
	want := "build --skip-kinds=Secret,CustomResourceDefinition --no-progress -p clusters/prod"
	if strings.TrimSpace(string(recorded)) != want {
		t.Errorf("flate args = %q, want %q", recorded, want)
	}
}

func TestBuild_RejectsArgsRemovingSecretFromSkipKinds(t *testing.T) {
	binary := fakeFlate(t, "flate version "+MinVersion, "", "", "0")

	if _, err := Build(context.Background(), Options{Binary: binary, Args: []string{"--skip-kinds="}}); err == nil {
		t.Fatal("want error when --skip-kinds drops Secret")
	}
}
