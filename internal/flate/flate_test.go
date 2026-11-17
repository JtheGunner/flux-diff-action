package flate

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/JtheGunner/flux-diff-action/internal/model"
)

const fixtureDir = "../../testdata/flate"

func readFixture(t *testing.T, name string) []byte {
	t.Helper()

	data, err := os.ReadFile(filepath.Join(fixtureDir, name))
	if err != nil {
		t.Fatalf("read fixture %s: %v", name, err)
	}

	return data
}

// fakeFlate writes a shell script that mimics the flate CLI. Its behaviour is
// driven by environment variables so no real flate is needed.
func fakeFlate(t *testing.T, version, stdoutFixture, stderrFixture string, exitCode string) string {
	t.Helper()

	script := `#!/bin/sh
if [ "$1" = "--version" ]; then
  echo "$FAKE_FLATE_VERSION"
  exit 0
fi
if [ -n "$FAKE_FLATE_STDOUT" ]; then cat "$FAKE_FLATE_STDOUT"; fi
if [ -n "$FAKE_FLATE_STDERR" ]; then cat "$FAKE_FLATE_STDERR" >&2; fi
exit "$FAKE_FLATE_EXIT"
`
	path := filepath.Join(t.TempDir(), "flate")
	if err := os.WriteFile(path, []byte(script), 0o700); err != nil {
		t.Fatalf("write fake binary: %v", err)
	}

	t.Setenv("FAKE_FLATE_VERSION", version)
	t.Setenv("FAKE_FLATE_EXIT", exitCode)
	t.Setenv("FAKE_FLATE_STDOUT", fixturePath(stdoutFixture))
	t.Setenv("FAKE_FLATE_STDERR", fixturePath(stderrFixture))

	return path
}

func fixturePath(name string) string {
	if name == "" {
		return ""
	}

	abs, err := filepath.Abs(filepath.Join(fixtureDir, name))
	if err != nil {
		return ""
	}

	return abs
}

func TestParse_ChangesWithParent(t *testing.T) {
	result, err := Parse(readFixture(t, "changes.json"), nil)
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}

	if len(result.Changes) != 3 {
		t.Fatalf("changes = %d, want 3", len(result.Changes))
	}

	changed := result.Changes[0]
	if changed.Status != model.StatusChanged || changed.Kind != "ConfigMap" || changed.Name != "demo-app" {
		t.Errorf("unexpected first change: %+v", changed)
	}
	if changed.Parent == nil || changed.Parent.Kind != "HelmRelease" || changed.Parent.Name != "demo-app" {
		t.Errorf("parent = %+v, want HelmRelease demo/demo-app", changed.Parent)
	}
	if len(changed.Old) == 0 || len(changed.New) == 0 {
		t.Error("old/new manifests must be kept")
	}

	if result.Changes[1].Status != model.StatusAdded || result.Changes[1].Parent != nil {
		t.Errorf("added change = %+v", result.Changes[1])
	}
	if result.Changes[2].Status != model.StatusRemoved {
		t.Errorf("removed change = %+v", result.Changes[2])
	}
	if len(result.Suppressed) != 1 || result.Suppressed[0].Name != "demo-noise" {
		t.Errorf("suppressed = %+v", result.Suppressed)
	}
}

func TestParse_StderrWarnings_SOPSUnreadable_ExternalSource(t *testing.T) {
	result, err := Parse([]byte(`{"changes":[],"suppressed":[]}`), readFixture(t, "stderr-warnings.txt"))
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}

	if len(result.Warnings) != 3 {
		t.Fatalf("warnings = %d, want 3: %+v", len(result.Warnings), result.Warnings)
	}

	sops := result.Warnings[0]
	if sops.Code != "SOPSUnreadable" || sops.Snapshot != "orig" || sops.Kind != "Secret" ||
		sops.Namespace != "demo" || sops.Name != "demo-credentials" {
		t.Errorf("sops warning = %+v", sops)
	}
	if len(sops.Keys) != 2 || sops.Keys[0] != "password" || sops.Keys[1] != "api-token" {
		t.Errorf("sops keys = %v", sops.Keys)
	}

	external := result.Warnings[1]
	if external.Code != "ExternalSource" || external.Snapshot != "current" || external.Kind != "Kustomization" {
		t.Errorf("external warning = %+v", external)
	}
	if len(external.Keys) != 0 {
		t.Errorf("external keys = %v, want none", external.Keys)
	}

	if result.Warnings[2].Code != "UnrelatedFailure" {
		t.Errorf("third warning code = %q", result.Warnings[2].Code)
	}
}

func TestParse_UnstructuredWarningLineIsKept(t *testing.T) {
	result, err := Parse([]byte(`{}`), []byte("    [NewKind] something unexpected\n"))
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}

	if len(result.Warnings) != 1 || result.Warnings[0].Code != "NewKind" {
		t.Fatalf("warnings = %+v", result.Warnings)
	}
}

func TestParse_Failures_ReportedWithUnitAndReason(t *testing.T) {
	result, err := Parse([]byte(`{"changes":[]}`), readFixture(t, "stderr-failures.txt"))
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}

	if len(result.Failures) != 2 {
		t.Fatalf("failures = %d, want 2: %+v", len(result.Failures), result.Failures)
	}

	failure := result.Failures[0]
	want := model.Failure{
		Snapshot:  "orig",
		Kind:      "HelmRelease",
		Namespace: "demo",
		Name:      "demo-app",
		Reason:    "valuesFrom Secret demo/demo-credentials not found",
	}
	if failure != want {
		t.Errorf("failure = %+v, want %+v", failure, want)
	}
	if result.Failures[1].Snapshot != "current" {
		t.Errorf("second failure snapshot = %q", result.Failures[1].Snapshot)
	}
}

func TestParse_InvalidJSON_ErrorDoesNotEchoPayload(t *testing.T) {
	_, err := Parse([]byte(`{"changes": [FAKE-DO-NOT-USE-payload`), nil)
	if err == nil {
		t.Fatal("expected error")
	}
	if strings.Contains(err.Error(), "FAKE-DO-NOT-USE") {
		t.Errorf("error leaks payload: %v", err)
	}
}

func TestParse_WrongTypeJSON_ErrorDoesNotEchoPayload(t *testing.T) {
	_, err := Parse([]byte(`{"changes": "FAKE-DO-NOT-USE-payload"}`), nil)
	if err == nil {
		t.Fatal("expected error")
	}
	if strings.Contains(err.Error(), "FAKE-DO-NOT-USE") {
		t.Errorf("error leaks payload: %v", err)
	}
}

func TestRun_Success(t *testing.T) {
	binary := fakeFlate(t, "flate version "+MinVersion, "changes.json", "stderr-warnings.txt", "0")

	result, err := Run(context.Background(), Options{Binary: binary, Path: "."})
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if result.ExitCode != 0 || len(result.Changes) != 3 || len(result.Warnings) != 3 {
		t.Errorf("result = exit %d, %d changes, %d warnings", result.ExitCode, len(result.Changes), len(result.Warnings))
	}
}

func TestRun_ExitCode1WithPartialJSON_ReturnsResultNotError(t *testing.T) {
	binary := fakeFlate(t, "flate version "+MinVersion, "changes.json", "stderr-failures.txt", "1")

	result, err := Run(context.Background(), Options{Binary: binary, Path: "."})
	if err != nil {
		t.Fatalf("Run returned error for exit 1 with JSON: %v", err)
	}
	if result.ExitCode != 1 {
		t.Errorf("ExitCode = %d, want 1", result.ExitCode)
	}
	if len(result.Changes) != 3 || len(result.Failures) != 2 {
		t.Errorf("changes = %d, failures = %d", len(result.Changes), len(result.Failures))
	}
}

func TestRun_ExitCode1WithoutJSON_ReturnsError(t *testing.T) {
	binary := fakeFlate(t, "flate version "+MinVersion, "", "stderr-failures.txt", "1")

	_, err := Run(context.Background(), Options{Binary: binary, Path: "."})
	if err == nil {
		t.Fatal("expected error when flate produced no JSON")
	}
	if strings.Contains(err.Error(), "demo-credentials") {
		t.Errorf("error leaks stderr content: %v", err)
	}
}

func TestRun_UnexpectedExitCode_ReturnsError(t *testing.T) {
	binary := fakeFlate(t, "flate version "+MinVersion, "changes.json", "", "2")

	if _, err := Run(context.Background(), Options{Binary: binary, Path: "."}); err == nil {
		t.Fatal("expected error for exit code 2")
	}
}

func TestRun_BinaryMissing_ReturnsClearError(t *testing.T) {
	missing := filepath.Join(t.TempDir(), "no-such-flate")

	_, err := Run(context.Background(), Options{Binary: missing, Path: "."})
	if !errors.Is(err, ErrBinaryNotFound) {
		t.Fatalf("err = %v, want ErrBinaryNotFound", err)
	}
	if !strings.Contains(err.Error(), "no-such-flate") {
		t.Errorf("error should name the binary: %v", err)
	}
}

func TestRun_VersionBelowMinimum_ReturnsError(t *testing.T) {
	binary := fakeFlate(t, "flate version v0.1.0", "changes.json", "", "0")

	_, err := Run(context.Background(), Options{Binary: binary, Path: "."})
	if !errors.Is(err, ErrVersionTooOld) {
		t.Fatalf("err = %v, want ErrVersionTooOld", err)
	}
	if !strings.Contains(err.Error(), "v0.1.0") || !strings.Contains(err.Error(), MinVersion) {
		t.Errorf("error should name found and minimum version: %v", err)
	}
}

func TestRun_UnparseableVersion_ReturnsError(t *testing.T) {
	binary := fakeFlate(t, "FAKE-DO-NOT-USE garbage", "changes.json", "", "0")

	_, err := Run(context.Background(), Options{Binary: binary, Path: "."})
	if err == nil {
		t.Fatal("expected error")
	}
	if strings.Contains(err.Error(), "FAKE-DO-NOT-USE") {
		t.Errorf("error leaks version output: %v", err)
	}
}

func TestBuildArgs(t *testing.T) {
	got := buildArgs(Options{Path: "clusters/prod", Base: "origin/main", Args: []string{"--strict-substitutions"}})
	want := []string{"diff", "-o", "json", "--no-progress", "-p", "clusters/prod", "--base", "origin/main", "--strict-substitutions"}

	if strings.Join(got, " ") != strings.Join(want, " ") {
		t.Errorf("args = %v, want %v", got, want)
	}

	minimal := buildArgs(Options{})
	if strings.Join(minimal, " ") != "diff -o json --no-progress" {
		t.Errorf("minimal args = %v", minimal)
	}
}

func TestCompareVersions(t *testing.T) {
	cases := []struct {
		a, b string
		want int
	}{
		{"v0.7.3", "v0.7.3", 0},
		{"v0.7.2", "v0.7.3", -1},
		{"v0.10.0", "v0.9.9", 1},
		{"v1.0.0", "v0.99.99", 1},
	}
	for _, c := range cases {
		got, err := compareVersions(c.a, c.b)
		if err != nil || got != c.want {
			t.Errorf("compareVersions(%s, %s) = %d, %v; want %d", c.a, c.b, got, err, c.want)
		}
	}
}

func TestRun_ForcesSkipKindsOnTheProcess(t *testing.T) {
	binary := fakeFlate(t, "flate version "+MinVersion, "changes.json", "", "0")
	argsFile := filepath.Join(t.TempDir(), "args")
	script := "#!/bin/sh\nif [ \"$1\" = \"--version\" ]; then echo \"$FAKE_FLATE_VERSION\"; exit 0; fi\necho \"$@\" > " + argsFile + "\ncat \"$FAKE_FLATE_STDOUT\"\n"
	if err := os.WriteFile(binary, []byte(script), 0o700); err != nil {
		t.Fatalf("write fake binary: %v", err)
	}

	if _, err := Run(context.Background(), Options{Binary: binary}); err != nil {
		t.Fatalf("Run: %v", err)
	}

	recorded, err := os.ReadFile(argsFile)
	if err != nil {
		t.Fatalf("read recorded args: %v", err)
	}
	if !strings.Contains(string(recorded), "--skip-kinds=Secret,CustomResourceDefinition") {
		t.Errorf("flate args = %q, want forced --skip-kinds", recorded)
	}
}

func TestRun_RejectsArgsRemovingSecretFromSkipKinds(t *testing.T) {
	binary := fakeFlate(t, "flate version "+MinVersion, "changes.json", "", "0")

	_, err := Run(context.Background(), Options{Binary: binary, Args: []string{"--skip-kinds="}})
	if err == nil {
		t.Fatal("want error when --skip-kinds drops Secret")
	}
}

func TestRun_DoesNotPassFlateEnvironmentToTheProcess(t *testing.T) {
	binary := fakeFlate(t, "flate version "+MinVersion, "changes.json", "", "0")
	envFile := filepath.Join(t.TempDir(), "env")
	script := "#!/bin/sh\nif [ \"$1\" = \"--version\" ]; then echo \"$FAKE_FLATE_VERSION\"; exit 0; fi\nenv > " + envFile + "\ncat \"$FAKE_FLATE_STDOUT\"\n"
	if err := os.WriteFile(binary, []byte(script), 0o700); err != nil {
		t.Fatalf("write fake binary: %v", err)
	}
	t.Setenv("FLATE_SKIP_KINDS", "ConfigMap")
	t.Setenv("FLATE_LOG_LEVEL", "debug")
	t.Setenv("FAKE_KEEP_ME", "kept")

	if _, err := Run(context.Background(), Options{Binary: binary}); err != nil {
		t.Fatalf("Run: %v", err)
	}

	recorded, err := os.ReadFile(envFile)
	if err != nil {
		t.Fatalf("read recorded env: %v", err)
	}
	var sawKept, sawPath bool
	for _, line := range strings.Split(string(recorded), "\n") {
		name, _, _ := strings.Cut(line, "=")
		switch {
		case strings.HasPrefix(name, "FLATE_"):
			t.Errorf("flate process received %s", name)
		case name == "FAKE_KEEP_ME":
			sawKept = true
		case name == "PATH":
			sawPath = true
		}
	}
	if !sawKept || !sawPath {
		t.Error("non-FLATE variables (FAKE_KEEP_ME, PATH) were dropped")
	}
}

// hangingFlate writes a fake flate that answers --version and then hangs. With
// orphan, the hang is a child process that keeps flate's pipes open after
// flate itself was killed, like a stuck helper.
func hangingFlate(t *testing.T, orphan bool) string {
	t.Helper()

	hang := "exec sleep 30"
	if orphan {
		hang = "sleep 30"
	}
	script := `#!/bin/sh
if [ "$1" = "--version" ]; then
  echo "flate version ` + MinVersion + `"
  exit 0
fi
` + hang + "\n"
	path := filepath.Join(t.TempDir(), "flate")
	if err := os.WriteFile(path, []byte(script), 0o700); err != nil {
		t.Fatalf("write fake binary: %v", err)
	}

	return path
}

func TestRun_HangingFlate_FailsWithTimeoutError(t *testing.T) {
	binary := hangingFlate(t, false)

	started := time.Now()
	_, err := Run(context.Background(), Options{Binary: binary, Path: ".", Timeout: 200 * time.Millisecond})

	if !errors.Is(err, ErrTimeout) {
		t.Fatalf("error = %v, want ErrTimeout", err)
	}
	if !strings.Contains(err.Error(), "200ms") {
		t.Errorf("error %q does not name the timeout", err)
	}
	if elapsed := time.Since(started); elapsed > 10*time.Second {
		t.Errorf("Run took %s, want it to stop shortly after the timeout", elapsed)
	}
}

func TestBuild_HangingFlate_FailsWithTimeoutError(t *testing.T) {
	binary := hangingFlate(t, false)

	_, err := Build(context.Background(), Options{Binary: binary, Path: ".", Timeout: 200 * time.Millisecond})
	if !errors.Is(err, ErrTimeout) {
		t.Fatalf("error = %v, want ErrTimeout", err)
	}
}

func TestRun_OrphanedChildHoldingPipes_DoesNotBlockTheTimeout(t *testing.T) {
	binary := hangingFlate(t, true)

	started := time.Now()
	_, err := Run(context.Background(), Options{Binary: binary, Path: ".", Timeout: 200 * time.Millisecond})

	if !errors.Is(err, ErrTimeout) {
		t.Fatalf("error = %v, want ErrTimeout", err)
	}
	if elapsed := time.Since(started); elapsed > 15*time.Second {
		t.Errorf("Run took %s, want it to stop within the pipe grace period", elapsed)
	}
}

func TestRun_CancelledContext_IsNotReportedAsTimeout(t *testing.T) {
	binary := hangingFlate(t, false)
	ctx, cancel := context.WithCancel(context.Background())
	time.AfterFunc(200*time.Millisecond, cancel)

	_, err := Run(ctx, Options{Binary: binary, Path: ".", Timeout: time.Minute})
	if err == nil || errors.Is(err, ErrTimeout) {
		t.Fatalf("error = %v, want a cancellation that is not ErrTimeout", err)
	}
}

func TestRun_DefaultTimeoutApplies(t *testing.T) {
	if DefaultTimeout != 10*time.Minute {
		t.Errorf("DefaultTimeout = %s, want 10m", DefaultTimeout)
	}
}
