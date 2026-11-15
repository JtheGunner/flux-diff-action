package flate

import (
	"context"
	"fmt"
	"regexp"
	"strings"

	"github.com/JtheGunner/flux-diff-action/internal/guard"
	"github.com/JtheGunner/flux-diff-action/internal/model"
)

var (
	// "  ✗  Kind  ns/name  reason": `flate build` names no snapshot.
	buildFailurePattern = regexp.MustCompile(`^\s+✗\s+(\S+)\s+(?:([^/\s]+)/)?(\S+)\s+(.+)$`)
	// "         blocks ns/name": a unit that cannot render because the failure
	// above it did.
	blocksPattern = regexp.MustCompile(`^\s+blocks\s+(\S+)\s*$`)
)

// Build runs `flate build` on the head and returns the units it could not
// render. Only stderr is read: the rendered manifests on stdout are discarded
// unread. opts.Base is ignored and opts.Args must only hold flags that
// `flate build` accepts.
//
// Exit code 1 means some units failed; any other non-zero exit code is an
// error. The failures are raw and unmasked, like the result of Run. Error
// messages never embed flate's stderr content.
func Build(ctx context.Context, opts Options) ([]model.Failure, error) {
	binary := opts.Binary
	if binary == "" {
		binary = defaultBinary
	}

	if err := checkVersion(ctx, binary); err != nil {
		return nil, err
	}

	args := []string{"build", "--no-progress"}
	if opts.Path != "" {
		args = append(args, "-p", opts.Path)
	}

	args, err := guard.EnforceSkipKinds(append(args, opts.Args...))
	if err != nil {
		return nil, fmt.Errorf("refusing to run flate: %w", err)
	}

	_, stderr, exitCode, err := execute(ctx, binary, args, false)
	if err != nil {
		return nil, err
	}

	if exitCode != 0 && exitCode != 1 {
		return nil, fmt.Errorf("flate build exited with unexpected code %d", exitCode)
	}

	return parseBuildFailures(string(stderr)), nil
}

// parseBuildFailures reads the failed units, and the units each one blocks,
// from `flate build` stderr.
func parseBuildFailures(stderr string) []model.Failure {
	var failures []model.Failure

	for _, line := range strings.Split(stderr, "\n") {
		line = strings.TrimRight(line, "\r")

		if failedSummaryPattern.MatchString(line) {
			continue
		}

		if match := buildFailurePattern.FindStringSubmatch(line); match != nil {
			failures = append(failures, model.Failure{
				Kind:      match[1],
				Namespace: match[2],
				Name:      match[3],
				Reason:    strings.TrimSpace(match[4]),
			})
			continue
		}

		if match := blocksPattern.FindStringSubmatch(line); match != nil && len(failures) > 0 {
			last := &failures[len(failures)-1]
			last.Blocks = append(last.Blocks, match[1])
		}
	}

	return failures
}

// BrokenInBothSides picks, from the failures of a head build, the units that
// flate's diff did not report on either side. flate stays silent for a unit
// that is broken identically in base and head, so those units are what
// remains. A unit the diff names, on any side, is a normal failure and stays
// out.
func BrokenInBothSides(diffFailures, buildFailures []model.Failure) []model.Failure {
	type unit struct{ kind, namespace, name string }

	reported := map[unit]bool{}
	for _, failure := range diffFailures {
		reported[unit{failure.Kind, failure.Namespace, failure.Name}] = true
	}

	var broken []model.Failure
	for _, failure := range buildFailures {
		if !reported[unit{failure.Kind, failure.Namespace, failure.Name}] {
			broken = append(broken, failure)
		}
	}

	return broken
}
