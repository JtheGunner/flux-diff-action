package guard

import (
	"encoding/base64"
	"encoding/json"
	"strings"
	"testing"

	"github.com/JtheGunner/flux-diff-action/internal/format"
	"github.com/JtheGunner/flux-diff-action/internal/model"
)

// renderAll returns the masked result in every output format.
func renderAll(t *testing.T, masker *Masker, change model.Change) map[string]string {
	t.Helper()

	got := masker.MaskResult(model.Result{Changes: []model.Change{change}})
	markdown, _ := format.Markdown(got, format.MarkdownOptions{})
	jsonOut, err := format.JSON(got)
	if err != nil {
		t.Fatalf("format.JSON: %v", err)
	}

	return map[string]string{"markdown": markdown, "text": format.Text(got), "json": jsonOut}
}

func assertNoneContain(t *testing.T, outputs map[string]string, forbidden ...string) {
	t.Helper()

	for name, out := range outputs {
		for _, text := range forbidden {
			if strings.Contains(out, text) {
				t.Errorf("%s output leaks %q:\n%s", name, text, out)
			}
		}
	}
}

func TestMask_KeyContainingSecretValue_IsScrubbedInEveryFormat(t *testing.T) {
	const secret = "FAKE-DO-NOT-USE-a"
	const quotedSecret = "FAKE-DO-NOT-USE\"q"

	masker := newMasker(t, Config{ExtraKeyPatterns: []string{"*token*"}})
	encodedSecret := base64.StdEncoding.EncodeToString([]byte(secret))
	manifest := map[string]any{"kind": "HelmRelease", "values": map[string]any{
		"token-" + secret:        secret,
		"token-" + encodedSecret: secret,
		// The key spells the quoted value the way JSON escapes it.
		"token-FAKE-DO-NOT-USE\\\"q": quotedSecret,
	}}
	raw, err := json.Marshal(manifest)
	if err != nil {
		t.Fatalf("marshal fixture: %v", err)
	}
	change := model.Change{Kind: "HelmRelease", Status: model.StatusAdded, New: raw}

	outputs := renderAll(t, masker, change)

	assertNoneContain(t, outputs, "FAKE-DO-NOT-USE", encodedSecret)
	if !strings.Contains(outputs["json"], "token-") {
		t.Errorf("the readable part of the key should survive: %s", outputs["json"])
	}
}

func TestMask_ShortKnownValueDoesNotMaskKeyName(t *testing.T) {
	masker := newMasker(t, Config{ExtraKeyPatterns: []string{"*password*"}})
	change := model.Change{
		Kind: "HelmRelease", Status: model.StatusAdded,
		New: json.RawMessage(`{"kind":"HelmRelease","values":{"password":"word","dbpassword":"FAKE-DO-NOT-USE-a"}}`),
	}

	got := masker.MaskResult(model.Result{Changes: []model.Change{change}})

	values := section(t, decode(t, got.Changes[0].New), "values")
	if values["password"] != Masked || values["dbpassword"] != Masked || len(values) != 2 {
		t.Errorf("values = %v, want both key names readable with masked values", values)
	}
}

func TestMask_KeyNameScrubBoundary(t *testing.T) {
	// "FAKE-DO" has 7 characters (below minKeyScrubLength), "FAKE-DO-" has 8.
	tests := []struct {
		name    string
		value   string
		wantKey string
	}{
		{"below minimum keeps the key", "FAKE-DO", "token-FAKE-DO-x"},
		{"at minimum scrubs the key", "FAKE-DO-", "token-<masked>x"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			masker := newMasker(t, Config{ExtraKeyPatterns: []string{"*token*"}})
			change := model.Change{
				Kind: "HelmRelease", Status: model.StatusAdded,
				New: json.RawMessage(`{"kind":"HelmRelease","values":{"token-FAKE-DO-x":"` + tt.value + `"}}`),
			}

			got := masker.MaskResult(model.Result{Changes: []model.Change{change}})

			values := section(t, decode(t, got.Changes[0].New), "values")
			if _, ok := values[tt.wantKey]; !ok || len(values) != 1 {
				t.Errorf("values = %v, want the single key %q", values, tt.wantKey)
			}
		})
	}
}

func TestMask_SecretObjectKeysStayStrict(t *testing.T) {
	const secret = "FAKE-DO-NOT-USE-a"

	masker := newMasker(t, Config{})
	encoded := base64.StdEncoding.EncodeToString([]byte(secret))
	change := model.Change{
		Kind: "Secret", Status: model.StatusAdded,
		New: json.RawMessage(`{"kind":"Secret","data":{"k-` + secret + `":"` + encoded + `","k-` + encoded + `":"` + encoded + `"}}`),
	}

	outputs := renderAll(t, masker, change)

	assertNoneContain(t, outputs, "FAKE-DO-NOT-USE", encoded)
}

func TestMask_NumericSecretIsMaskedWhereverItAppears(t *testing.T) {
	// 6 digits is below minKeyScrubLength, 12 digits is above it.
	for _, secret := range []string{"424242", "424242424242"} {
		t.Run(secret, func(t *testing.T) {
			masker := newMasker(t, Config{ExtraKeyPatterns: []string{"pin"}})
			change := model.Change{
				Kind: "HelmRelease", Status: model.StatusAdded,
				New: json.RawMessage(`{"kind":"HelmRelease","values":{"pin":` + secret + `,"replicas":` + secret + `,"list":[` + secret + `]}}`),
			}

			outputs := renderAll(t, masker, change)

			assertNoneContain(t, outputs, secret)
			if strings.Contains(outputs["json"], "unparseable") {
				t.Errorf("a numeric secret must not make the side unparseable: %s", outputs["json"])
			}
		})
	}
}
