package flate

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/JtheGunner/flux-diff-action/internal/model"
)

func TestParseBuildFailures_FailedUnit(t *testing.T) {
	got := parseBuildFailures(string(readFixture(t, "build-stderr-broken.txt")))

	want := []model.BrokenUnit{{Kind: "Kustomization", Namespace: "flux-system", Name: "broken-app"}}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("units = %+v, want %+v", got, want)
	}
}

func TestParseBuildFailures_UnitIdentityMatchesTheDiffParser(t *testing.T) {
	cases := map[string]struct{ diffLine, buildLine string }{
		"namespaced": {
			"  ✗  current snapshot: Kustomization  flux-system/app  boom",
			"  ✗  Kustomization  flux-system/app  boom",
		},
		"cluster-scoped": {
			"  ✗  current snapshot: ClusterIssuer  issuer  boom",
			"  ✗  ClusterIssuer  issuer  boom",
		},
	}
	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			diff := parseStderr(tc.diffLine + "\n").failures
			built := parseBuildFailures(tc.buildLine + "\n")

			if len(diff) != 1 || len(built) != 1 {
				t.Fatalf("parsed %d diff failures and %d build units, want 1 each", len(diff), len(built))
			}
			if diff[0].Kind != built[0].Kind || diff[0].Namespace != built[0].Namespace || diff[0].Name != built[0].Name {
				t.Errorf("diff identity %+v differs from build identity %+v", diff[0], built[0])
			}
		})
	}
}

func TestParseBuildFailures_KeepsNoErrorText(t *testing.T) {
	got := parseBuildFailures("  ✗  Kustomization  flux-system/app  value FAKE-DO-NOT-USE-reason-only is invalid\n")

	if strings.Contains(strings.Join([]string{got[0].Kind, got[0].Namespace, got[0].Name}, " "), "FAKE-DO-NOT-USE") {
		t.Errorf("unit carries error text: %+v", got[0])
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
	broken := model.BrokenUnit{Kind: "Kustomization", Namespace: "flux-system", Name: "broken-app"}
	other := model.Failure{Kind: "Kustomization", Namespace: "flux-system", Name: "other"}
	sameUnit := func(snapshot string) model.Failure {
		return model.Failure{Snapshot: snapshot, Kind: broken.Kind, Namespace: broken.Namespace, Name: broken.Name}
	}

	cases := map[string]struct {
		diff  []model.Failure
		build []model.BrokenUnit
		want  []model.BrokenUnit
	}{
		"silent in diff":             {nil, []model.BrokenUnit{broken}, []model.BrokenUnit{broken}},
		"reported on current side":   {[]model.Failure{sameUnit("current")}, []model.BrokenUnit{broken}, nil},
		"reported on orig side":      {[]model.Failure{sameUnit("orig")}, []model.BrokenUnit{broken}, nil},
		"only other unit in diff":    {[]model.Failure{other}, []model.BrokenUnit{broken}, []model.BrokenUnit{broken}},
		"build has no failures":      {[]model.Failure{other}, nil, nil},
		"same name, different kind":  {[]model.Failure{{Kind: "HelmRelease", Namespace: broken.Namespace, Name: broken.Name}}, []model.BrokenUnit{broken}, []model.BrokenUnit{broken}},
		"same name, other namespace": {[]model.Failure{{Kind: broken.Kind, Namespace: "apps", Name: broken.Name}}, []model.BrokenUnit{broken}, []model.BrokenUnit{broken}},
	}
	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			if got := BrokenInBothSides(tc.diff, tc.build); !reflect.DeepEqual(got, tc.want) {
				t.Errorf("got %+v, want %+v", got, tc.want)
			}
		})
	}
}

func TestBuild_ExitOneWithoutReadableFailure_FailsWithoutLeakingOutput(t *testing.T) {
	binary := fakeFlate(t, "flate version "+MinVersion, "", "build-stderr-unrecognised.txt", "1")

	units, err := Build(context.Background(), Options{Binary: binary})
	if !errors.Is(err, ErrBuildFailureUnreadable) {
		t.Fatalf("error = %v, want ErrBuildFailureUnreadable", err)
	}
	if len(units) != 0 {
		t.Errorf("units = %+v, want none", units)
	}
	if strings.Contains(err.Error(), "FAKE-DO-NOT-USE") {
		t.Errorf("error carries stderr text: %v", err)
	}
}

func TestBuild_ExitZeroIsClean(t *testing.T) {
	binary := fakeFlate(t, "flate version "+MinVersion, "", "", "0")

	units, err := Build(context.Background(), Options{Binary: binary})
	if err != nil || len(units) != 0 {
		t.Errorf("units = %+v, err = %v, want a clean result", units, err)
	}
}

func TestBuild_ReturnsFailuresAndReadsOnlyStderr(t *testing.T) {
	binary := fakeFlate(t, "flate version "+MinVersion, "changes.json", "build-stderr-broken.txt", "1")

	units, err := Build(context.Background(), Options{Binary: binary})
	if err != nil {
		t.Fatalf("Build: %v", err)
	}
	if len(units) != 1 || units[0].Name != "broken-app" {
		t.Errorf("units = %+v", units)
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
