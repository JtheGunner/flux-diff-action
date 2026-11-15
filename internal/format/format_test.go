package format

import (
	"encoding/json"
	"fmt"
	"reflect"
	"strings"
	"testing"
	"unicode/utf8"

	"github.com/JtheGunner/flux-diff-action/internal/model"
)

func manifest(kind, name, value string) json.RawMessage {
	return json.RawMessage(fmt.Sprintf(`{"apiVersion":"v1","kind":%q,"metadata":{"name":%q},"data":{"key":%q}}`, kind, name, value))
}

func change(status model.Status, name string, parent *model.Ref) model.Change {
	c := model.Change{Parent: parent, APIVersion: "v1", Kind: "ConfigMap", Namespace: "demo", Name: name, Status: status}
	if status != model.StatusAdded {
		c.Old = manifest("ConfigMap", name, "old")
	}
	if status != model.StatusRemoved {
		c.New = manifest("ConfigMap", name, "new")
	}

	return c
}

func helmParent(name string) *model.Ref {
	return &model.Ref{Group: "helm.toolkit.fluxcd.io", Kind: "HelmRelease", Namespace: "demo", Name: name}
}

func kustomizationParent(name string) *model.Ref {
	return &model.Ref{Group: "kustomize.toolkit.fluxcd.io", Kind: "Kustomization", Namespace: "flux-system", Name: name}
}

func sampleResult() model.Result {
	return model.Result{
		Changes: []model.Change{
			change(model.StatusChanged, "a", helmParent("app")),
			change(model.StatusAdded, "b", kustomizationParent("apps")),
			change(model.StatusAdded, "c", helmParent("app")),
		},
	}
}

func TestMarkdown_SummaryLine(t *testing.T) {
	body, _ := Markdown(sampleResult(), MarkdownOptions{})

	if !strings.Contains(body, "**2 added, 1 changed, 0 removed**") {
		t.Errorf("summary line missing:\n%s", body)
	}
}

func TestMarkdown_StartsWithHiddenMarker(t *testing.T) {
	body, _ := Markdown(sampleResult(), MarkdownOptions{})
	if !strings.HasPrefix(body, DefaultMarker+"\n") {
		t.Errorf("default marker missing: %q", body[:40])
	}

	body, _ = Markdown(sampleResult(), MarkdownOptions{Marker: "<!-- custom -->"})
	if !strings.HasPrefix(body, "<!-- custom -->\n") {
		t.Errorf("custom marker missing: %q", body[:40])
	}
}

func TestMarkdown_GroupsByParentKustomizationAndHelmRelease(t *testing.T) {
	body, _ := Markdown(sampleResult(), MarkdownOptions{})

	helm := strings.Index(body, "### ` HelmRelease demo/app `")
	kustomization := strings.Index(body, "### ` Kustomization flux-system/apps `")
	if helm < 0 || kustomization < 0 {
		t.Fatalf("group headings missing:\n%s", body)
	}
	if strings.Count(body, "HelmRelease demo/app `") != 1 {
		t.Errorf("HelmRelease group must appear once:\n%s", body)
	}
	if helm > strings.Index(body, "<code>ConfigMap demo/c</code>") || kustomization < strings.Index(body, "<code>ConfigMap demo/a</code>") {
		t.Errorf("objects are not under their parent heading:\n%s", body)
	}
}

func TestMarkdown_DiffShowsOldAndNew(t *testing.T) {
	body, _ := Markdown(model.Result{Changes: []model.Change{change(model.StatusChanged, "a", nil)}}, MarkdownOptions{})

	for _, want := range []string{"### Other objects", "```diff", `-    "key": "old"`, `+    "key": "new"`, "@@ "} {
		if !strings.Contains(body, want) {
			t.Errorf("missing %q in:\n%s", want, body)
		}
	}
}

func TestMarkdown_PartialSectionListsFailuresAndSopsWarnings(t *testing.T) {
	r := model.Result{
		Failures: []model.Failure{{Snapshot: "current", Kind: "HelmRelease", Namespace: "demo", Name: "app", Reason: "valuesFrom Secret missing"}},
		Warnings: []model.Warning{
			{Code: "SOPSUnreadable", Snapshot: "orig", Kind: "Secret", Namespace: "demo", Name: "creds", Message: "SOPS-encrypted", Keys: []string{"password"}},
			{Code: "ExternalSource", Kind: "Kustomization", Namespace: "flux-system", Name: "crds", Message: "outside the local tree"},
		},
		Unparsed: []string{"strange line"},
		ExitCode: 1,
	}
	body, _ := Markdown(r, MarkdownOptions{})

	for _, want := range []string{
		"### Not rendered / warnings",
		"**Failed:**", "HelmRelease demo/app (current snapshot): valuesFrom Secret missing",
		"**SOPS (partial):**", "(keys: password)",
		"**External source (not rendered):**",
		"**Unparsed flate output:** ` strange line `",
	} {
		if !strings.Contains(body, want) {
			t.Errorf("missing %q in:\n%s", want, body)
		}
	}
	if strings.Contains(body, "No manifest changes") {
		t.Error("incomplete result must not claim there are no changes")
	}
}

func TestMarkdown_NoChanges_SaysNoManifestChanges(t *testing.T) {
	body, truncated := Markdown(model.Result{}, MarkdownOptions{})

	if !strings.Contains(body, "No manifest changes.") || truncated {
		t.Errorf("clean empty result: truncated=%v body:\n%s", truncated, body)
	}
}

func TestMarkdown_NoChangesButUnparsedOrExit_IsNeverClean(t *testing.T) {
	cases := map[string]model.Result{
		"unparsed": {Unparsed: []string{"x"}},
		"failures": {Failures: []model.Failure{{Name: "a", Reason: "r"}}},
		"exit":     {ExitCode: 1},
		"warnings": {Warnings: []model.Warning{{Code: "SOPSUnreadable", Message: "m"}}},
	}
	for name, r := range cases {
		t.Run(name, func(t *testing.T) {
			for _, out := range []string{markdownOf(r), Text(r)} {
				if strings.Contains(out, "No manifest changes") {
					t.Errorf("shown as clean:\n%s", out)
				}
				if !strings.Contains(out, "incomplete") {
					t.Errorf("incompleteness not stated:\n%s", out)
				}
			}
		})
	}
}

func markdownOf(r model.Result) string {
	body, _ := Markdown(r, MarkdownOptions{})

	return body
}

func TestMarkdown_SuppressedCountShown(t *testing.T) {
	r := sampleResult()
	r.Suppressed = []model.Change{change(model.StatusChanged, "noise", nil), change(model.StatusChanged, "noise2", nil)}

	if body := markdownOf(r); !strings.Contains(body, "2 more suppressed as noise") {
		t.Errorf("suppressed count missing:\n%s", body)
	}
}

func bigResult(objects int) model.Result {
	var r model.Result
	for i := range objects {
		c := change(model.StatusChanged, fmt.Sprintf("obj-%d", i), helmParent("app"))
		c.Old = manifest("ConfigMap", c.Name, strings.Repeat("old ", 100))
		c.New = manifest("ConfigMap", c.Name, strings.Repeat("new ", 100))
		r.Changes = append(r.Changes, c)
	}
	r.Warnings = []model.Warning{{Code: "SOPSUnreadable", Kind: "Secret", Name: "creds", Message: "SOPS-encrypted"}}

	return r
}

func TestMarkdown_OverLimit_TruncatesAtObjectBoundaryWithNotice(t *testing.T) {
	const limit = 3000

	body, truncated := Markdown(bigResult(30), MarkdownOptions{MaxChars: limit})

	if !truncated {
		t.Fatal("truncated = false, want true")
	}
	if got := utf8.RuneCountInString(body); got > limit {
		t.Errorf("body has %d chars, limit %d", got, limit)
	}
	if opens, closes := strings.Count(body, "<details>"), strings.Count(body, "</details>"); opens != closes || opens == 0 {
		t.Errorf("<details> unbalanced: %d open, %d close", opens, closes)
	}
	if strings.Count(body, "```")%2 != 0 {
		t.Error("code fences unbalanced")
	}
	if !strings.Contains(body, "Output truncated") {
		t.Error("truncation notice missing")
	}
	if !strings.Contains(body, "SOPS (partial)") {
		t.Error("partial section must survive truncation")
	}
	noticeAt := strings.Index(body, "\n> [!NOTE]")
	if noticeAt < 0 || !strings.HasSuffix(strings.TrimRight(body[:noticeAt], "\n"), "</details>") {
		t.Error("cut is not at an object boundary")
	}
}

func TestMarkdown_OverLimit_TinyLimitStillRespected(t *testing.T) {
	for _, limit := range []int{50, 400, 900} {
		body, truncated := Markdown(bigResult(5), MarkdownOptions{MaxChars: limit})
		if utf8.RuneCountInString(body) > limit || !truncated {
			t.Errorf("limit %d: %d chars, truncated=%v", limit, utf8.RuneCountInString(body), truncated)
		}
	}
}

func TestMarkdown_UntrustedTextCannotBreakStructure(t *testing.T) {
	r := model.Result{
		Failures: []model.Failure{{Name: "x", Reason: "bad `` </details>\n## injected"}},
		Changes:  []model.Change{{Kind: "ConfigMap", Name: "</summary><b>", Status: model.StatusAdded, New: manifest("ConfigMap", "x", "```\n</details>")}},
	}
	body, _ := Markdown(r, MarkdownOptions{})

	if strings.Contains(body, "\n## injected") {
		t.Error("newline in reason started a new block")
	}
	if strings.Contains(body, "<b>") {
		t.Error("object name was not HTML-escaped")
	}
	if strings.Count(body, "<details>") != strings.Count(body, "</details>") {
		// the diff content holds a literal </details> inside a fence, which is inert;
		// only the structural tags matter, so compare line-start occurrences.
		starts := 0
		for _, line := range strings.Split(body, "\n") {
			if line == "</details>" {
				starts++
			}
		}
		if starts != strings.Count(body, "<details>") {
			t.Errorf("structure unbalanced:\n%s", body)
		}
	}
}

func TestJSON_RoundTrips(t *testing.T) {
	r := sampleResult()
	r.Suppressed = []model.Change{change(model.StatusChanged, "noise", nil)}
	r.Warnings = []model.Warning{{Code: "SOPSUnreadable", Message: "m", Keys: []string{"k"}}}
	r.Failures = []model.Failure{{Name: "a", Reason: "r"}}
	r.Unparsed = []string{"u"}
	r.ExitCode = 1

	out, err := JSON(r)
	if err != nil {
		t.Fatalf("JSON: %v", err)
	}

	var back model.Result
	if err := json.Unmarshal([]byte(out), &back); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}

	want, _ := json.Marshal(r)
	got, _ := json.Marshal(back)
	if !reflect.DeepEqual(want, got) {
		t.Errorf("round trip differs:\nwant %s\ngot  %s", want, got)
	}
}

func TestText_ContainsSummaryGroupsAndDiff(t *testing.T) {
	out := Text(sampleResult())

	for _, want := range []string{"2 added, 1 changed, 0 removed", "== HelmRelease demo/app ==", "--- changed ConfigMap demo/a", `+    "key": "new"`} {
		if !strings.Contains(out, want) {
			t.Errorf("missing %q in:\n%s", want, out)
		}
	}
}

func TestIsPartial(t *testing.T) {
	if IsPartial(model.Result{Warnings: []model.Warning{{Code: "UnrelatedFailure"}}}) {
		t.Error("UnrelatedFailure alone is not partial")
	}
	for _, r := range []model.Result{
		{Unparsed: []string{"x"}},
		{Warnings: []model.Warning{{Code: "SOPSUnreadable"}}},
		{Warnings: []model.Warning{{Code: "ExternalSource"}}},
	} {
		if !IsPartial(r) {
			t.Errorf("want partial: %+v", r)
		}
	}
}

func TestFailureSummary_BoundedAndSingleLine(t *testing.T) {
	var r model.Result
	for i := range 8 {
		r.Failures = append(r.Failures, model.Failure{Name: fmt.Sprintf("u%d", i), Reason: "bad\nreason"})
	}
	msg := FailureSummary(r)

	if strings.Contains(msg, "\n") || !strings.Contains(msg, "8 unit(s) failed") || !strings.Contains(msg, "and 3 more") {
		t.Errorf("summary = %q", msg)
	}
}

func TestUnifiedDiff_IdenticalIsEmpty(t *testing.T) {
	if got := unifiedDiff([]string{"a", "b"}, []string{"a", "b"}); got != "" {
		t.Errorf("diff = %q", got)
	}
}

func TestMarkdownAndText_ParentNameCannotInjectLines(t *testing.T) {
	evil := &model.Ref{Kind: "HelmRelease", Namespace: "demo", Name: "x`\n</details>\n::set-output name=pwn::1\n## injected"}
	r := model.Result{Changes: []model.Change{change(model.StatusChanged, "a", evil)}}

	body, _ := Markdown(r, MarkdownOptions{})
	for _, line := range strings.Split(body, "\n") {
		if line == "</details>" && !strings.Contains(body, "<details>") {
			t.Errorf("injected closing tag on its own line:\n%s", body)
		}
		if strings.HasPrefix(line, "## injected") || strings.HasPrefix(line, "::") {
			t.Errorf("parent name started a new line %q in Markdown", line)
		}
	}

	text := Text(r)
	for _, line := range strings.Split(text, "\n") {
		if strings.HasPrefix(line, "::") || strings.HasPrefix(line, "## injected") {
			t.Errorf("parent name started a new line %q in text", line)
		}
	}
}

func TestText_LinesThatLookLikeWorkflowCommandsAreNeutralised(t *testing.T) {
	got := neutraliseWorkflowCommands("ok\n::error::x\n##[group]y\n  ::fine")

	for _, line := range strings.Split(got, "\n") {
		if strings.HasPrefix(line, "::") || strings.HasPrefix(line, "##[") {
			t.Errorf("line %q still reads as a workflow command", line)
		}
	}
}
