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

func TestParse_UnrecognisedStderrLines_AreKept(t *testing.T) {
	stderr := "Error: something broke\n" +
		"  ✗ strange failure shape\n" +
		"[Unindented] warning\n" +
		"        orphan detail line\n" +
		"\n"

	result, err := Parse([]byte("{}"), []byte(stderr))
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}

	want := []string{"Error: something broke", "✗ strange failure shape", "[Unindented] warning", "orphan detail line"}
	if !reflect.DeepEqual(result.Unparsed, want) {
		t.Errorf("unparsed = %q, want %q", result.Unparsed, want)
	}
}

func TestParse_KnownFixtures_LeaveNothingUnparsed(t *testing.T) {
	for _, name := range []string{"stderr-warnings.txt", "stderr-failures.txt"} {
		result, err := Parse([]byte("{}"), readFixture(t, name))
		if err != nil {
			t.Fatalf("Parse %s: %v", name, err)
		}
		if len(result.Unparsed) != 0 {
			t.Errorf("%s: unparsed = %q, want none", name, result.Unparsed)
		}
	}
}

func TestParse_Unparsed_IsBounded(t *testing.T) {
	var builder strings.Builder
	for i := 0; i < model.MaxUnparsedLines+5; i++ {
		builder.WriteString(strings.Repeat("x", model.MaxUnparsedLineLength+100) + "\n")
	}

	result, err := Parse([]byte("{}"), []byte(builder.String()))
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}

	if len(result.Unparsed) != model.MaxUnparsedLines+1 {
		t.Fatalf("unparsed = %d entries, want %d", len(result.Unparsed), model.MaxUnparsedLines+1)
	}
	if got := len([]rune(result.Unparsed[0])); got != model.MaxUnparsedLineLength+3 {
		t.Errorf("line length = %d, want %d", got, model.MaxUnparsedLineLength+3)
	}
	if last := result.Unparsed[len(result.Unparsed)-1]; !strings.Contains(last, "5 more") {
		t.Errorf("last entry = %q, want omitted-count note", last)
	}
}

func TestParse_SummaryCountMismatch_IsReported(t *testing.T) {
	stderr := "  ✗ 2 failed · 0 blocked\n  ⚠ warnings (1)\n"

	result, err := Parse([]byte("{}"), []byte(stderr))
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}

	want := []string{
		"flate reported 2 failures but 0 were parsed",
		"flate reported 1 warnings but 0 were parsed",
	}
	if !reflect.DeepEqual(result.Unparsed, want) {
		t.Errorf("unparsed = %q, want %q", result.Unparsed, want)
	}
}

func TestRun_ExitCode1WithOnlyUnparsableStderr_SignalsUnparsed(t *testing.T) {
	stderrFile := filepath.Join(t.TempDir(), "stderr.txt")
	if err := os.WriteFile(stderrFile, []byte("Error: FAKE-DO-NOT-USE unexpected\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	binary := fakeFlate(t, "flate version "+MinVersion, "changes.json", "", "1")
	t.Setenv("FAKE_FLATE_STDERR", stderrFile)

	result, err := Run(context.Background(), Options{Binary: binary})
	if err != nil {
		t.Fatalf("Run: %v", err)
	}

	if result.ExitCode != 1 || len(result.Failures) != 0 || len(result.Warnings) != 0 {
		t.Fatalf("result = %+v", result)
	}
	if len(result.Unparsed) != 1 {
		t.Errorf("unparsed = %q, want the unrecognised line", result.Unparsed)
	}
}

func TestParse_DetailLines_OnlyKeyNamesUnderKeyListingCodes(t *testing.T) {
	const warning = "    [SOPSUnreadable] orig snapshot: Secret ns/app: cannot read\n"
	const other = "    [ExternalSource] orig snapshot: HelmRelease ns/app: not fetched\n"

	cases := map[string]struct {
		stderr     string
		wantKeys   []string
		wantUnpars []string
	}{
		"key names":       {warning + "          password, api-token\n", []string{"password", "api-token"}, nil},
		"value as key":    {warning + "          token: FAKE-DO-NOT-USE-x\n", nil, []string{"token: FAKE-DO-NOT-USE-x"}},
		"padded base64":   {warning + "          RkFLRS1ETy1OT1QtVVNF==\n", nil, []string{"RkFLRS1ETy1OT1QtVVNF=="}},
		"mixed line":      {warning + "          password, two words\n", nil, []string{"password, two words"}},
		"non-key-listing": {other + "          password\n", nil, []string{"password"}},
	}
	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			result, err := Parse([]byte("{}"), []byte(tc.stderr))
			if err != nil {
				t.Fatalf("Parse: %v", err)
			}
			if got := result.Warnings[0].Keys; !reflect.DeepEqual(got, tc.wantKeys) {
				t.Errorf("keys = %q, want %q", got, tc.wantKeys)
			}
			if !reflect.DeepEqual(result.Unparsed, tc.wantUnpars) {
				t.Errorf("unparsed = %q, want %q", result.Unparsed, tc.wantUnpars)
			}
		})
	}
}
