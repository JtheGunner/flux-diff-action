package flate

import (
	"encoding/json"
	"errors"
	"fmt"
	"regexp"
	"strconv"
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
	// "  ✗ 2 failed · 0 blocked" and "  ⚠ warnings (3)": summary counts used to
	// cross-check what was parsed.
	failedSummaryPattern  = regexp.MustCompile(`^\s*✗\s+(\d+) failed\b`)
	warningsHeaderPattern = regexp.MustCompile(`^\s*⚠\s+warnings \((\d+)\)\s*$`)
	// Detail lines under a warning are indented deeper than the warning itself.
	detailPattern = regexp.MustCompile(`^\s{6,}\S`)
	// keyNamePattern is what a Secret data key looks like. Only lines made of
	// such names are accepted as key lists, so a detail line that carries a
	// value (spaces, quotes, base64 padding) is kept as an unparsed line, where
	// the secret guard scrubs it, instead of being listed as a key.
	keyNamePattern = regexp.MustCompile(`^[-._a-zA-Z0-9]+$`)
)

// keyListingCodes are the warning codes whose detail lines list key names.
var keyListingCodes = map[string]bool{
	"SOPSUnreadable": true,
}

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

	found := parseStderr(string(stderr))

	return Result{
		Changes:    raw.Changes,
		Suppressed: raw.Suppressed,
		Warnings:   found.warnings,
		Failures:   found.failures,
		Unparsed:   found.unparsed,
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

// stderrFindings is everything parseStderr extracted from flate's stderr.
type stderrFindings struct {
	warnings []model.Warning
	failures []model.Failure
	unparsed []string
}

func parseStderr(stderr string) stderrFindings {
	var (
		found          stderrFindings
		unparsed       unparsedCollector
		reportedFailed = -1
		reportedWarned = -1
	)

	for _, line := range strings.Split(stderr, "\n") {
		line = strings.TrimRight(line, "\r")
		if strings.TrimSpace(line) == "" {
			continue
		}

		if match := failedSummaryPattern.FindStringSubmatch(line); match != nil {
			reportedFailed = atoiOrNegative(match[1])
			continue
		}

		if match := warningsHeaderPattern.FindStringSubmatch(line); match != nil {
			reportedWarned = atoiOrNegative(match[1])
			continue
		}

		if match := failurePattern.FindStringSubmatch(line); match != nil {
			found.failures = append(found.failures, model.Failure{
				Snapshot:  match[1],
				Kind:      match[2],
				Namespace: match[3],
				Name:      match[4],
				Reason:    strings.TrimSpace(match[5]),
			})
			continue
		}

		if match := warningPattern.FindStringSubmatch(line); match != nil {
			found.warnings = append(found.warnings, model.Warning{
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
			found.warnings = append(found.warnings, model.Warning{Code: match[1], Message: strings.TrimSpace(match[2])})
			continue
		}

		if len(found.warnings) > 0 && detailPattern.MatchString(line) {
			last := &found.warnings[len(found.warnings)-1]
			if keys, ok := keyList(last.Code, line); ok {
				last.Keys = append(last.Keys, keys...)
				continue
			}
		}

		unparsed.add(line)
	}

	if reportedFailed >= 0 && reportedFailed != len(found.failures) {
		unparsed.addNote(fmt.Sprintf("flate reported %d failures but %d were parsed", reportedFailed, len(found.failures)))
	}
	if reportedWarned >= 0 && reportedWarned != len(found.warnings) {
		unparsed.addNote(fmt.Sprintf("flate reported %d warnings but %d were parsed", reportedWarned, len(found.warnings)))
	}

	found.unparsed = unparsed.lines()

	return found
}

func atoiOrNegative(value string) int {
	number, err := strconv.Atoi(value)
	if err != nil {
		return -1
	}

	return number
}

// unparsedCollector keeps a bounded number of truncated stderr lines and
// counts the ones it had to drop.
type unparsedCollector struct {
	kept    []string
	notes   []string
	dropped int
}

func (c *unparsedCollector) add(line string) {
	if len(c.kept) >= model.MaxUnparsedLines {
		c.dropped++
		return
	}

	line = strings.TrimSpace(line)
	if runes := []rune(line); len(runes) > model.MaxUnparsedLineLength {
		line = string(runes[:model.MaxUnparsedLineLength]) + "..."
	}
	c.kept = append(c.kept, line)
}

// addNote records a message generated by this package, not flate output.
func (c *unparsedCollector) addNote(note string) {
	c.notes = append(c.notes, note)
}

func (c *unparsedCollector) lines() []string {
	result := append([]string(nil), c.kept...)
	if c.dropped > 0 {
		result = append(result, fmt.Sprintf("%d more unparsed stderr lines omitted", c.dropped))
	}

	return append(result, c.notes...)
}

// keyList reads a detail line as a comma-separated list of key names. It
// reports false unless the warning code lists keys and every entry looks like
// a key name.
func keyList(code, line string) ([]string, bool) {
	if !keyListingCodes[code] {
		return nil, false
	}

	var keys []string
	for _, part := range strings.Split(line, ",") {
		key := strings.TrimSpace(part)
		if key == "" {
			continue
		}
		if !keyNamePattern.MatchString(key) {
			return nil, false
		}
		keys = append(keys, key)
	}

	return keys, len(keys) > 0
}
