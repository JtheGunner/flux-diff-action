package flate

import (
	"encoding/json"
	"errors"
	"fmt"
	"regexp"
	"strings"

	"github.com/JtheGunner/flux-diff-action/internal/model"
)

var (
	// "    [Code] orig snapshot: Kind ns/name: message"; the namespace is absent
	// for cluster-scoped kinds.
	warningPattern = regexp.MustCompile(`^\s+\[(\w+)\]\s+(orig|current) snapshot: (\S+) (?:([^/\s]+)/)?([^:\s]+): (.*)$`)
	// "    [Code] anything else": keeps warnings of a shape we do not know yet.
	looseWarningPattern = regexp.MustCompile(`^\s+\[(\w+)\]\s*(.*)$`)
	// "  ✗  orig snapshot: Kind  ns/name  reason"
	failurePattern = regexp.MustCompile(`^\s+✗\s+(orig|current) snapshot: (\S+)\s+(?:([^/\s]+)/)?(\S+)\s+(.+)$`)
	// Detail lines under a warning are indented deeper than the warning itself.
	detailPattern = regexp.MustCompile(`^\s{6,}\S`)
)

// rawOutput mirrors the JSON document flate prints on stdout.
type rawOutput struct {
	Changes    []model.Change `json:"changes"`
	Suppressed []model.Change `json:"suppressed"`
}

// Parse turns flate's stdout (JSON) and stderr (warnings, failures) into a
// Result. ExitCode is left for the caller to fill in.
//
// Errors never embed stdout or stderr content, since both may carry secret
// values.
func Parse(stdout, stderr []byte) (Result, error) {
	var raw rawOutput
	if err := json.Unmarshal(stdout, &raw); err != nil {
		return Result{}, describeDecodeError(err)
	}

	warnings, failures := parseStderr(string(stderr))

	return Result{
		Changes:    raw.Changes,
		Suppressed: raw.Suppressed,
		Warnings:   warnings,
		Failures:   failures,
	}, nil
}

// describeDecodeError reports where decoding failed without quoting input.
func describeDecodeError(err error) error {
	var syntaxErr *json.SyntaxError
	if errors.As(err, &syntaxErr) {
		return fmt.Errorf("flate output is not valid JSON (syntax error at byte %d)", syntaxErr.Offset)
	}

	var typeErr *json.UnmarshalTypeError
	if errors.As(err, &typeErr) {
		return fmt.Errorf("flate output has an unexpected JSON structure (field %q, expected %s)", typeErr.Field, typeErr.Type)
	}

	return errors.New("flate output could not be decoded as JSON")
}

func parseStderr(stderr string) ([]model.Warning, []model.Failure) {
	var (
		warnings []model.Warning
		failures []model.Failure
	)

	for _, line := range strings.Split(stderr, "\n") {
		line = strings.TrimRight(line, "\r")

		if match := failurePattern.FindStringSubmatch(line); match != nil {
			failures = append(failures, model.Failure{
				Snapshot:  match[1],
				Kind:      match[2],
				Namespace: match[3],
				Name:      match[4],
				Reason:    strings.TrimSpace(match[5]),
			})
			continue
		}

		if match := warningPattern.FindStringSubmatch(line); match != nil {
			warnings = append(warnings, model.Warning{
				Code:      match[1],
				Snapshot:  match[2],
				Kind:      match[3],
				Namespace: match[4],
				Name:      match[5],
				Message:   strings.TrimSpace(match[6]),
			})
			continue
		}

		if match := looseWarningPattern.FindStringSubmatch(line); match != nil {
			warnings = append(warnings, model.Warning{Code: match[1], Message: strings.TrimSpace(match[2])})
			continue
		}

		if len(warnings) > 0 && detailPattern.MatchString(line) {
			last := &warnings[len(warnings)-1]
			last.Keys = append(last.Keys, splitKeys(line)...)
		}
	}

	return warnings, failures
}

func splitKeys(line string) []string {
	var keys []string
	for _, part := range strings.Split(line, ",") {
		if key := strings.TrimSpace(part); key != "" {
			keys = append(keys, key)
		}
	}

	return keys
}
