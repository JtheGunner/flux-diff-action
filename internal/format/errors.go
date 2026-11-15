package format

import (
	"fmt"
	"strings"

	"github.com/JtheGunner/flux-diff-action/internal/model"
)

const maxErrorItems = 5

// FailureSummary builds the one-line message used when a run ends with failed
// units. It lists at most a few failures. The result must already be masked.
func FailureSummary(r model.Result) string {
	items := incompleteItems(model.Result{Failures: r.Failures, ExitCode: r.ExitCode})
	if len(items) == 0 {
		return "no unit failed"
	}

	shown := items
	if len(shown) > maxErrorItems {
		shown = shown[:maxErrorItems]
	}

	parts := make([]string, len(shown))
	for i, item := range shown {
		parts[i] = singleLine(item.text)
	}

	message := fmt.Sprintf("%d unit(s) failed: %s", len(items), strings.Join(parts, "; "))
	if hidden := len(items) - len(shown); hidden > 0 {
		message += fmt.Sprintf("; and %d more", hidden)
	}

	return message
}

// PartialSummary builds the one-line message used for --strict when units are
// only partially rendered. The result must already be masked.
func PartialSummary(r model.Result) string {
	var parts []string
	for _, item := range incompleteItems(model.Result{Warnings: r.Warnings, Unparsed: r.Unparsed}) {
		if item.category == "Warning" {
			continue
		}
		parts = append(parts, item.category+": "+singleLine(item.text))
		if len(parts) == maxErrorItems {
			break
		}
	}

	return fmt.Sprintf("result is partial: %s", strings.Join(parts, "; "))
}

// IsPartial reports whether some unit was only partially rendered: a SOPS or
// external-source warning, or flate output that could not be parsed.
func IsPartial(r model.Result) bool {
	if len(r.Unparsed) > 0 {
		return true
	}
	for _, warning := range r.Warnings {
		if warning.Code == codeSOPSUnreadable || warning.Code == codeExternalSource {
			return true
		}
	}

	return false
}
