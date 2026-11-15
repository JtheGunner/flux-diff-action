// Package format turns a flate result into Markdown, text and JSON output.
//
// Every function here expects a result that already went through
// guard.MaskResult. The package does not mask on its own: masking before any
// sink is the caller's responsibility, and the guard tests exercise each
// formatter with a masked result.
package format

import (
	"fmt"
	"strings"
	"unicode"

	"github.com/JtheGunner/flux-diff-action/internal/model"
)

const (
	codeSOPSUnreadable = "SOPSUnreadable"
	codeExternalSource = "ExternalSource"
)

type counts struct {
	added, changed, removed int
}

func countChanges(changes []model.Change) counts {
	var total counts
	for _, change := range changes {
		switch change.Status {
		case model.StatusAdded:
			total.added++
		case model.StatusChanged:
			total.changed++
		case model.StatusRemoved:
			total.removed++
		}
	}

	return total
}

func (c counts) summary() string {
	return fmt.Sprintf("%d added, %d changed, %d removed", c.added, c.changed, c.removed)
}

// incompleteItem is one entry of the "Not rendered / warnings" section.
type incompleteItem struct {
	category string
	text     string
	// expected marks a notice that is identical on every run (SOPS, external
	// source). The Markdown output folds several of them into one block.
	expected bool
}

// isIncomplete reports whether the result must not be presented as clean.
func isIncomplete(r model.Result) bool {
	return len(r.Failures) > 0 || len(r.Warnings) > 0 || len(r.Unparsed) > 0 || r.ExitCode != 0
}

func incompleteItems(r model.Result) []incompleteItem {
	var items []incompleteItem

	for _, failure := range r.Failures {
		text := describeRef(failure.Kind, failure.Namespace, failure.Name)
		if failure.Snapshot != "" {
			text += " (" + failure.Snapshot + " snapshot)"
		}
		items = append(items, incompleteItem{category: "Failed", text: text + ": " + failure.Reason})
	}

	for _, warning := range r.Warnings {
		items = append(items, incompleteItem{category: warningCategory(warning.Code), text: describeWarning(warning), expected: isExpectedNotice(warning.Code)})
	}

	for _, line := range r.Unparsed {
		items = append(items, incompleteItem{category: "Unparsed flate output", text: line})
	}

	if r.ExitCode != 0 && len(r.Failures) == 0 {
		items = append(items, incompleteItem{category: "Failed", text: fmt.Sprintf("flate exited with code %d without naming a failed unit", r.ExitCode)})
	}

	return items
}

// isExpectedNotice reports whether a warning code describes a known, stable
// limit rather than a problem that needs attention on its own.
func isExpectedNotice(code string) bool {
	return code == codeSOPSUnreadable || code == codeExternalSource
}

func warningCategory(code string) string {
	switch code {
	case codeSOPSUnreadable:
		return "SOPS (partial)"
	case codeExternalSource:
		return "External source (not rendered)"
	default:
		return "Warning"
	}
}

func describeWarning(warning model.Warning) string {
	text := "[" + warning.Code + "]"
	if warning.Snapshot != "" {
		text += " " + warning.Snapshot + " snapshot:"
	}
	if ref := describeRef(warning.Kind, warning.Namespace, warning.Name); ref != "" {
		text += " " + ref + ":"
	}
	text += " " + warning.Message
	if len(warning.Keys) > 0 {
		text += " (keys: " + strings.Join(warning.Keys, ", ") + ")"
	}

	return text
}

// describeRef renders "Kind namespace/name", omitting empty parts.
func describeRef(kind, namespace, name string) string {
	identity := name
	if namespace != "" {
		identity = namespace + "/" + name
	}

	return singleLine(kind + " " + identity)
}

func describeChange(change model.Change) string {
	return describeRef(change.Kind, change.Namespace, change.Name)
}

func describeParent(ref *model.Ref) string {
	if ref == nil {
		return ""
	}

	return describeRef(ref.Kind, ref.Namespace, ref.Name)
}

// group is a set of changes sharing one parent Kustomization or HelmRelease.
type group struct {
	label   string // empty for changes without a parent
	changes []model.Change
}

// groupByParent keeps the first-seen order of parents and puts changes
// without a parent last.
func groupByParent(changes []model.Change) []group {
	var groups []group
	index := map[string]int{}
	var ungrouped []model.Change

	for _, change := range changes {
		label := describeParent(change.Parent)
		if label == "" {
			ungrouped = append(ungrouped, change)
			continue
		}
		position, seen := index[label]
		if !seen {
			position = len(groups)
			index[label] = position
			groups = append(groups, group{label: label})
		}
		groups[position].changes = append(groups[position].changes, change)
	}

	if len(ungrouped) > 0 {
		groups = append(groups, group{changes: ungrouped})
	}

	return groups
}

// singleLine collapses line breaks so untrusted text cannot start new
// Markdown blocks.
func singleLine(text string) string {
	return strings.Join(strings.Fields(text), " ")
}

// neutraliseWorkflowCommands keeps GitHub Actions from reading a line of the
// rendered text as a workflow command ("::set-output ...", "##[...]") when the
// output is printed to a job log. The runner ignores leading whitespace before
// it looks for the marker (any Unicode space), so such lines get a visible "| " prefix instead of
// a space. Diff context lines start with a space, so manifest content from a
// PR can reach this check indented.
func neutraliseWorkflowCommands(text string) string {
	// A bare carriage return starts a new line for the runner's log parser.
	text = strings.ReplaceAll(strings.ReplaceAll(text, "\r\n", "\n"), "\r", "\n")

	lines := strings.Split(text, "\n")
	for i, line := range lines {
		trimmed := strings.TrimLeftFunc(line, unicode.IsSpace)
		if strings.HasPrefix(trimmed, "::") || strings.HasPrefix(trimmed, "##[") {
			lines[i] = "| " + line
		}
	}

	return strings.Join(lines, "\n")
}
