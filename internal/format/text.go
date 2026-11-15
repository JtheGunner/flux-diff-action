package format

import (
	"fmt"
	"strings"

	"github.com/JtheGunner/flux-diff-action/internal/model"
)

// Text renders the result as plain text for terminals and logs. The result
// must already be masked.
func Text(r model.Result) string {
	var out strings.Builder

	total := countChanges(r.Changes)
	fmt.Fprintf(&out, "flux-diff: %s\n", total.summary())
	if len(r.Suppressed) > 0 {
		fmt.Fprintf(&out, "%d suppressed as noise\n", len(r.Suppressed))
	}
	out.WriteString(verdict(r) + "\n")

	for _, grp := range groupByParent(r.Changes) {
		label := grp.label
		if label == "" {
			label = "(no parent)"
		}
		fmt.Fprintf(&out, "\n== %s ==\n", label)

		for _, change := range grp.changes {
			fmt.Fprintf(&out, "\n--- %s %s\n", change.Status, describeChange(change))
			out.WriteString(changeDiff(change))
		}
	}

	if items := incompleteItems(r); len(items) > 0 {
		out.WriteString("\nNot rendered / warnings:\n")
		for _, item := range items {
			fmt.Fprintf(&out, "- %s: %s\n", item.category, singleLine(item.text))
		}
	}

	return neutraliseWorkflowCommands(out.String())
}

func changeDiff(change model.Change) string {
	diff := unifiedDiff(manifestLines(change.Old), manifestLines(change.New))
	if diff == "" {
		return "(no textual difference)\n"
	}

	return diff
}

// verdict is the one-line statement about completeness. It never says
// "no changes" for an incomplete result.
func verdict(r model.Result) string {
	switch {
	case len(r.Changes) > 0 && isIncomplete(r):
		return "Result is incomplete: see the section on units that were not fully rendered."
	case len(r.Changes) > 0:
		return ""
	case isIncomplete(r):
		return "Result is incomplete: no changes could be confirmed, see the section on units that were not fully rendered."
	default:
		return "No manifest changes."
	}
}
