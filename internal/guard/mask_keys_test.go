package guard

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/JtheGunner/flux-diff-action/internal/model"
)

func TestMask_PatternMatchedKeysStayReadable(t *testing.T) {
	masker := newMasker(t, Config{ExtraKeyPatterns: []string{"password", "token", "credentials"}})
	change := model.Change{
		Kind: "HelmRelease", Status: model.StatusChanged,
		Old: json.RawMessage(`{"kind":"HelmRelease","spec":{"values":{
			"top":{"password":"FAKE-DO-NOT-USE-old","token":"word"},
			"deep":{"a":{"b":{"token":"FAKE-DO-NOT-USE-t1"}}},
			"list":[{"token":"FAKE-DO-NOT-USE-l1","name":"x"},{"token":"FAKE-DO-NOT-USE-l2"}],
			"credentials":{"user":"FAKE-DO-NOT-USE-user","nested":["FAKE-DO-NOT-USE-n1"]}}}}`),
		New: json.RawMessage(`{"kind":"HelmRelease","spec":{"values":{
			"top":{"password":"FAKE-DO-NOT-USE-new","token":"word"},
			"deep":{"a":{"b":{"token":"FAKE-DO-NOT-USE-t1"}}},
			"list":[{"token":"FAKE-DO-NOT-USE-l1","name":"x"},{"token":"FAKE-DO-NOT-USE-l3"}],
			"credentials":{"user":"FAKE-DO-NOT-USE-user2","nested":["FAKE-DO-NOT-USE-n1"]}}}}`),
	}

	got := masker.MaskResult(model.Result{Changes: []model.Change{change}})

	for side, raw := range map[string]json.RawMessage{"old": got.Changes[0].Old, "new": got.Changes[0].New} {
		if strings.Contains(string(raw), "FAKE-DO-NOT-USE") {
			t.Errorf("%s side leaks a value: %s", side, raw)
		}
		if strings.Contains(string(raw), `"<masked>":`) {
			t.Errorf("%s side masks a key name: %s", side, raw)
		}
	}

	values := section(t, section(t, decode(t, got.Changes[0].New), "spec"), "values")
	oldValues := section(t, section(t, decode(t, got.Changes[0].Old), "spec"), "values")

	if top := section(t, values, "top"); top["password"] != MaskedChanged || len(top) != 1 {
		t.Errorf("top = %v, want only password %q", top, MaskedChanged)
	}
	if top := section(t, oldValues, "top"); top["password"] != Masked {
		t.Errorf("old top password = %v, want %q", top["password"], Masked)
	}
	if deepB := section(t, section(t, section(t, values, "deep"), "a"), "b"); len(deepB) != 0 {
		t.Errorf("unchanged deep token should have no entry: %v", deepB)
	}

	list, _ := values["list"].([]any)
	if len(list) != 2 {
		t.Fatalf("list = %v, want 2 items", list)
	}
	if first, _ := list[0].(map[string]any); first["name"] != "x" || len(first) != 1 {
		t.Errorf("unchanged list token should have no entry: %v", first)
	}
	if second, _ := list[1].(map[string]any); second["token"] != MaskedChanged {
		t.Errorf("list[1] = %v, want token %q", second, MaskedChanged)
	}

	if values["credentials"] != MaskedChanged {
		t.Errorf("credentials = %v, want the whole subtree replaced by %q", values["credentials"], MaskedChanged)
	}
	if oldValues["credentials"] != Masked {
		t.Errorf("old credentials = %v, want %q", oldValues["credentials"], Masked)
	}
}

func TestMask_KeyEqualToSecretValue_IsStillMasked(t *testing.T) {
	masker := newMasker(t, Config{ExtraKeyPatterns: []string{"password", "token"}})
	change := model.Change{
		Kind: "HelmRelease", Status: model.StatusAdded,
		New: json.RawMessage(`{"kind":"HelmRelease","values":{"password":"FAKE-DO-NOT-USE-a","token":"password"}}`),
	}

	got := masker.MaskResult(model.Result{Changes: []model.Change{change}})

	values := section(t, decode(t, got.Changes[0].New), "values")
	if values["token"] != Masked || values[Masked] != Masked || len(values) != 2 {
		t.Errorf("values = %v, want the key that equals a secret value masked", values)
	}
}
