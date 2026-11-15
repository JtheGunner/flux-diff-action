package guard

import (
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/JtheGunner/flux-diff-action/internal/model"
)

const fixtureDir = "../../testdata/secrets"

// fakeSecretValues are the plaintext fixture secrets. They must never appear
// in any output; tests report only the index of a leaked value.
var fakeSecretValues = []string{
	"FAKE-DO-NOT-USE-old-password",
	"FAKE-DO-NOT-USE-new-password",
	"FAKE-DO-NOT-USE-api-token",
	"FAKE-DO-NOT-USE-client-old",
	"FAKE-DO-NOT-USE-client-new",
	"FAKE-DO-NOT-USE-db-old",
}

func fixture(t *testing.T, name string) json.RawMessage {
	t.Helper()

	data, err := os.ReadFile(filepath.Join(fixtureDir, name))
	if err != nil {
		t.Fatalf("read fixture %s: %v", name, err)
	}

	return json.RawMessage(strings.TrimSpace(string(data)))
}

func secretChange(t *testing.T) model.Change {
	t.Helper()

	return model.Change{
		APIVersion: "v1", Kind: "Secret", Namespace: "apps", Name: "app-credentials",
		Status: model.StatusChanged,
		Old:    fixture(t, "secret-old.json"), New: fixture(t, "secret-new.json"),
	}
}

func configMapChange(t *testing.T) model.Change {
	t.Helper()

	return model.Change{
		APIVersion: "v1", Kind: "ConfigMap", Namespace: "apps", Name: "oidc",
		Status: model.StatusChanged,
		Old:    fixture(t, "configmap-old.json"), New: fixture(t, "configmap-new.json"),
	}
}

func newMasker(t *testing.T, cfg Config) *Masker {
	t.Helper()

	masker, err := New(cfg)
	if err != nil {
		t.Fatalf("New: %v", err)
	}

	return masker
}

func decode(t *testing.T, raw json.RawMessage) map[string]any {
	t.Helper()

	var out map[string]any
	if err := json.Unmarshal(raw, &out); err != nil {
		t.Fatalf("decode masked manifest: %v", err)
	}

	return out
}

func section(t *testing.T, manifest map[string]any, name string) map[string]any {
	t.Helper()

	got, ok := manifest[name].(map[string]any)
	if !ok {
		t.Fatalf("manifest has no %q map", name)
	}

	return got
}

func TestGuard_RejectsConfigRemovingSecretFromSkipKinds(t *testing.T) {
	cases := map[string][]string{
		"separate empty value": {"--skip-kinds", ""},
		"equals empty value":   {"--skip-kinds="},
		"separate other kind":  {"--skip-kinds", "CustomResourceDefinition"},
		"equals other kind":    {"--skip-kinds=ConfigMap,CustomResourceDefinition"},
		"second occurrence":    {"--skip-kinds=Secret", "--skip-kinds=ConfigMap"},
		"missing value":        {"--skip-kinds"},
	}

	for name, args := range cases {
		t.Run(name, func(t *testing.T) {
			if _, err := EnforceSkipKinds(args); !errors.Is(err, ErrSecretNotSkipped) {
				t.Errorf("err = %v, want ErrSecretNotSkipped", err)
			}
		})
	}
}

func TestEnforceSkipKinds_ForcesRequiredKinds(t *testing.T) {
	cases := map[string]struct {
		args []string
		want string
	}{
		"flag absent":        {[]string{"diff"}, "diff --skip-kinds=Secret,CustomResourceDefinition"},
		"forced flag first":  {[]string{"diff", "-p", "x"}, "diff --skip-kinds=Secret,CustomResourceDefinition -p x"},
		"case-insensitive":   {[]string{"--skip-kinds=secret"}, "--skip-kinds=Secret,CustomResourceDefinition"},
		"flag as value":      {[]string{"--path", "--skip-kinds="}, "--skip-kinds=Secret,CustomResourceDefinition --path --skip-kinds="},
		"adds missing CRD":   {[]string{"--skip-kinds", "Secret", "-p", "x"}, "--skip-kinds=Secret,CustomResourceDefinition -p x"},
		"keeps extra kinds":  {[]string{"--skip-kinds=Secret,Job"}, "--skip-kinds=Secret,CustomResourceDefinition,Job"},
		"merges repeated":    {[]string{"--skip-kinds=Secret,Job", "--skip-kinds=Secret,Pod"}, "--skip-kinds=Secret,CustomResourceDefinition,Job,Pod"},
		"trims whitespace":   {[]string{"--skip-kinds= Secret , Job"}, "--skip-kinds=Secret,CustomResourceDefinition,Job"},
		"nil args":           {nil, "--skip-kinds=Secret,CustomResourceDefinition"},
		"keeps other values": {[]string{"--other", "--skip-kinds=Secret"}, "--skip-kinds=Secret,CustomResourceDefinition --other"},
	}

	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			input := append([]string(nil), tc.args...)

			got, err := EnforceSkipKinds(tc.args)
			if err != nil {
				t.Fatalf("EnforceSkipKinds: %v", err)
			}
			if strings.Join(got, " ") != tc.want {
				t.Errorf("args = %q, want %q", strings.Join(got, " "), tc.want)
			}
			if strings.Join(tc.args, " ") != strings.Join(input, " ") {
				t.Error("input slice was modified")
			}
		})
	}
}

func TestMask_SecretInResult_MaskedChanged(t *testing.T) {
	masker := newMasker(t, Config{})

	got := masker.MaskResult(model.Result{Changes: []model.Change{secretChange(t)}})

	change := got.Changes[0]
	oldData := section(t, decode(t, change.Old), "data")
	newData := section(t, decode(t, change.New), "data")

	if newData["password"] != MaskedChanged {
		t.Errorf("new password = %v, want %q", newData["password"], MaskedChanged)
	}
	if oldData["password"] == MaskedChanged || oldData["password"] == nil {
		t.Errorf("old password = %v, want a plain masked marker", oldData["password"])
	}

	stringData := section(t, decode(t, change.New), "stringData")
	if _, present := stringData["note"]; present {
		t.Error("unchanged stringData value should have no entry")
	}
}

func TestMask_UnchangedSecret_NoEntry(t *testing.T) {
	masker := newMasker(t, Config{})
	change := secretChange(t)
	change.New = change.Old

	got := masker.MaskResult(model.Result{Changes: []model.Change{change}})

	for side, raw := range map[string]json.RawMessage{"old": got.Changes[0].Old, "new": got.Changes[0].New} {
		data := section(t, decode(t, raw), "data")
		if len(data) != 0 {
			t.Errorf("%s data has entries %v, want none", side, data)
		}
	}
}

func TestMask_AddedAndRemovedSecretValues(t *testing.T) {
	masker := newMasker(t, Config{})

	added := secretChange(t)
	added.Status, added.Old = model.StatusAdded, nil
	removed := secretChange(t)
	removed.Status, removed.New = model.StatusRemoved, nil

	got := masker.MaskResult(model.Result{Changes: []model.Change{added, removed}})

	if data := section(t, decode(t, got.Changes[0].New), "data"); data["password"] != Masked {
		t.Errorf("added password = %v, want %q", data["password"], Masked)
	}
	if got.Changes[0].Old != nil {
		t.Error("absent old manifest must stay absent")
	}
	if data := section(t, decode(t, got.Changes[1].Old), "data"); data["password"] != Masked {
		t.Errorf("removed password = %v, want %q", data["password"], Masked)
	}
}

func TestMask_ExtraPatternsInConfigMap(t *testing.T) {
	masker := newMasker(t, Config{ExtraKeyPatterns: []string{"client_secret", "*password*"}})

	got := masker.MaskResult(model.Result{Changes: []model.Change{configMapChange(t)}})

	newData := section(t, decode(t, got.Changes[0].New), "data")

	if newData["client_secret"] != MaskedChanged {
		t.Errorf("client_secret = %v, want %q", newData["client_secret"], MaskedChanged)
	}
	if _, present := newData["DB_PASSWORD"]; present {
		t.Error("unchanged case-insensitive pattern match should have no entry")
	}
	if newData["issuer"] != "https://id.example.test" || newData["logLevel"] != "debug" {
		t.Errorf("non-matching keys were altered: %v", newData)
	}
}

func TestMask_ExtraPaths(t *testing.T) {
	masker := newMasker(t, Config{ExtraPaths: []string{"data.issuer", "spec.values.auth.*"}})
	change := model.Change{
		Kind: "HelmRelease", Status: model.StatusChanged,
		Old: json.RawMessage(`{"kind":"HelmRelease","data":{"issuer":"FAKE-DO-NOT-USE-a"},"spec":{"values":{"auth":{"user":"FAKE-DO-NOT-USE-u","other":1}}}}`),
		New: json.RawMessage(`{"kind":"HelmRelease","data":{"issuer":"FAKE-DO-NOT-USE-b"},"spec":{"values":{"auth":{"user":"FAKE-DO-NOT-USE-u","other":1}}}}`),
	}

	got := masker.MaskResult(model.Result{Changes: []model.Change{change}})

	if strings.Contains(string(got.Changes[0].Old)+string(got.Changes[0].New), "FAKE-DO-NOT-USE") {
		t.Error("masked path value still present")
	}
	if section(t, decode(t, got.Changes[0].New), "data")["issuer"] != MaskedChanged {
		t.Error("changed path value not marked as changed")
	}
}

func TestMask_DoesNotMutateInput(t *testing.T) {
	masker := newMasker(t, Config{})
	input := model.Result{
		Changes:  []model.Change{secretChange(t)},
		Warnings: []model.Warning{{Message: "x", Keys: []string{"a"}}},
	}
	before := string(input.Changes[0].New)

	masker.MaskResult(input)

	if string(input.Changes[0].New) != before {
		t.Error("MaskResult mutated the input change")
	}
}

func TestMask_UnparseableManifest_FailsClosed(t *testing.T) {
	masker := newMasker(t, Config{})
	change := model.Change{Kind: "ConfigMap", New: json.RawMessage(`{"data": FAKE-DO-NOT-USE-broken`)}

	got := masker.MaskResult(model.Result{Changes: []model.Change{change}})

	if strings.Contains(string(got.Changes[0].New), "FAKE-DO-NOT-USE") {
		t.Error("unparseable manifest was passed through")
	}
}

func TestNew_InvalidPattern(t *testing.T) {
	if _, err := New(Config{ExtraKeyPatterns: []string{"[unclosed"}}); err == nil {
		t.Error("want error for invalid key pattern")
	}
	if _, err := New(Config{ExtraPaths: []string{"a.[unclosed"}}); err == nil {
		t.Error("want error for invalid path")
	}
}

func TestMaskString_RemovesKnownValuesFromErrors(t *testing.T) {
	known := []string{"FAKE-DO-NOT-USE-api-token", "abc"}
	encoded := base64.StdEncoding.EncodeToString([]byte(known[0]))

	err := fmt.Errorf("render failed for token %s (b64 %s) abc", known[0], encoded)
	got := MaskString(err.Error(), known)

	if strings.Contains(got, known[0]) || strings.Contains(got, encoded) {
		t.Error("known value or its base64 form survived MaskString")
	}
	if !strings.Contains(got, "abc") {
		t.Error("values shorter than four characters must be ignored")
	}
}

// outputPath renders a masked result into one output sink.
type outputPath struct {
	name   string
	render func(t *testing.T, res model.Result, known []string) string
}

// outputPaths lists every sink the leak guard covers. Add the Markdown
// formatter here when it lands (FDA-8) so it is checked against the same
// fixtures.
var outputPaths = []outputPath{
	{"json", func(t *testing.T, res model.Result, _ []string) string {
		t.Helper()
		data, err := json.Marshal(res)
		if err != nil {
			t.Fatalf("marshal: %v", err)
		}

		return string(data)
	}},
	{"log line", func(_ *testing.T, res model.Result, _ []string) string {
		var line strings.Builder
		for _, change := range append(append([]model.Change{}, res.Changes...), res.Suppressed...) {
			fmt.Fprintf(&line, "change %s/%s old=%s new=%s\n", change.Kind, change.Name, change.Old, change.New)
		}
		fmt.Fprintf(&line, "%+v %+v %v", res.Warnings, res.Failures, res.Unparsed)

		return line.String()
	}},
	{"error strings", func(_ *testing.T, res model.Result, known []string) string {
		var parts []string
		for _, failure := range res.Failures {
			parts = append(parts, fmt.Errorf("unit %s failed: %s", failure.Name, failure.Reason).Error())
		}
		for _, warning := range res.Warnings {
			parts = append(parts, fmt.Errorf("warning %s: %s", warning.Code, warning.Message).Error())
		}
		parts = append(parts, MaskString(errors.New("raw: "+strings.Join(known, " ")).Error(), known))

		return strings.Join(parts, "\n")
	}},
}

func TestNoSecretValueInAnyOutput(t *testing.T) {
	masker := newMasker(t, Config{ExtraKeyPatterns: []string{"client_secret", "*password*"}})

	leaky := model.Result{
		Changes:    []model.Change{secretChange(t), configMapChange(t)},
		Suppressed: []model.Change{secretChange(t)},
		Warnings: []model.Warning{
			{Code: "SOPSUnreadable", Message: "cannot read " + fakeSecretValues[0], Keys: []string{"password"}},
		},
		Failures: []model.Failure{
			{Name: "oidc", Reason: "bad value " + base64.StdEncoding.EncodeToString([]byte(fakeSecretValues[4]))},
		},
		Unparsed: []string{"stderr: " + fakeSecretValues[2]},
	}

	masked := masker.MaskResult(leaky)
	known := masker.KnownValues(leaky)

	var forbidden []string
	for _, value := range fakeSecretValues {
		forbidden = append(forbidden, value, base64.StdEncoding.EncodeToString([]byte(value)))
	}

	for _, sink := range outputPaths {
		t.Run(sink.name, func(t *testing.T) {
			output := sink.render(t, masked, known)
			if output == "" {
				t.Fatal("sink rendered nothing")
			}
			for i, value := range forbidden {
				if strings.Contains(output, value) {
					t.Errorf("forbidden value #%d leaked into %s output", i, sink.name)
				}
			}
		})
	}

	t.Run("control: unmasked result does leak", func(t *testing.T) {
		output := outputPaths[0].render(t, leaky, known)
		if !strings.Contains(output, base64.StdEncoding.EncodeToString([]byte(fakeSecretValues[0]))) {
			t.Error("fixtures no longer contain the secrets the guard test looks for")
		}
	})
}

func TestEnforceSkipKinds_RejectsAmbiguousArgs(t *testing.T) {
	cases := map[string][]string{
		"terminator":             {"diff", "--", "extra"},
		"terminator then flag":   {"--", "--skip-kinds="},
		"trailing value flag":    {"diff", "--path"},
		"trailing short flag":    {"diff", "-o"},
		"trailing skip-kinds":    {"diff", "--skip-kinds"},
		"lowercase kind missing": {"--skip-kinds=secrets"},
	}

	for name, args := range cases {
		t.Run(name, func(t *testing.T) {
			if got, err := EnforceSkipKinds(args); err == nil {
				t.Errorf("want error, got args %q", got)
			}
		})
	}
}

func TestMask_ValueWithJSONEscapedCharacters_NeverSurvives(t *testing.T) {
	const value = "FAKE-DO-NOT-USE \"q\" back\\slash\nline <tag> & ünï"

	secretObject := map[string]any{
		"apiVersion": "v1",
		"kind":       "Secret",
		"metadata":   map[string]any{"name": "fake"},
		"stringData": map[string]any{"token": value},
	}
	lastApplied, err := json.Marshal(secretObject)
	if err != nil {
		t.Fatalf("marshal last-applied: %v", err)
	}
	configMapObject := map[string]any{
		"apiVersion": "v1",
		"kind":       "ConfigMap",
		"metadata": map[string]any{
			"name":        "copy",
			"annotations": map[string]any{"note": "see " + value, "kubectl.kubernetes.io/last-applied-configuration": string(lastApplied)},
		},
		"data": map[string]any{"copied": value, value: "key"},
	}

	marshal := func(object map[string]any) json.RawMessage {
		raw, marshalErr := json.Marshal(object)
		if marshalErr != nil {
			t.Fatalf("marshal: %v", marshalErr)
		}

		return raw
	}

	leaky := model.Result{Changes: []model.Change{
		{Kind: "Secret", Name: "fake", New: marshal(secretObject)},
		{Kind: "ConfigMap", Name: "copy", New: marshal(configMapObject)},
	}}

	masker := newMasker(t, Config{})
	masked := masker.MaskResult(leaky)
	known := masker.KnownValues(leaky)

	escapedHTML, _ := json.Marshal(value)
	forbidden := []string{
		value,
		string(escapedHTML[1 : len(escapedHTML)-1]),
		"\\\"q\\\"",
		"ünï",
		"\\u00fcn",
	}

	for _, sink := range outputPaths {
		t.Run(sink.name, func(t *testing.T) {
			output := sink.render(t, masked, known)
			for _, needle := range forbidden {
				if strings.Contains(output, needle) {
					t.Errorf("escaped secret fragment %q leaked into %s output", needle, sink.name)
				}
			}
		})
	}
}
