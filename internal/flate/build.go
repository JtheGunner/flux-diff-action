package flate

import (
	"context"
	"errors"
	"fmt"
	"regexp"
	"strings"

	"github.com/JtheGunner/flux-diff-action/internal/guard"
	"github.com/JtheGunner/flux-diff-action/internal/model"
)

// ErrBuildFailureUnreadable means flate build reported failure but no failed
// unit could be read from its output. The message is fixed on purpose: it
// never carries output text.
var ErrBuildFailureUnreadable = errors.New("flate build exited 1 but reported no failure unit in a known format; update flux-diff or flate")

var (
	// "  ✗  Kind  ns/name  reason": `flate build` names no snapshot.
	buildFailurePattern = regexp.MustCompile(`^\s+✗\s+(\S+)\s+(?:([^/\s]+)/)?(\S+)\s+(.+)$`)
	// "         blocks ns/name": a unit that cannot render because the failure
	// above it did.
	blocksPattern = regexp.MustCompile(`^\s+blocks\s+(\S+)\s*$`)
)

// Build runs `flate build` on the head and returns the units it could not
// render. Only stderr is read: the rendered manifests on stdout are discarded
// unread, and the error text of each unit is dropped while parsing (see
// model.BrokenUnit). opts.Base is ignored and opts.Args must only hold flags that
// `flate build` accepts.
//
// Exit code 1 means some units failed; any other non-zero exit code is an
// error. Error messages never embed flate's stderr content.
func Build(ctx context.Context, opts Options) ([]model.BrokenUnit, error) {
	binary := opts.Binary
	if binary == "" {
		binary = defaultBinary
	}

	if err := checkVersion(ctx, binary, opts.timeout()); err != nil {
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

	_, stderr, exitCode, err := execute(ctx, binary, args, false, opts.timeout())
	if err != nil {
		return nil, err
	}

	if exitCode != 0 && exitCode != 1 {
		return nil, fmt.Errorf("flate build exited with unexpected code %d", exitCode)
	}

	units := parseBuildFailures(string(stderr))
	if exitCode == 1 && len(units) == 0 {
		return nil, ErrBuildFailureUnreadable
	}

	return units, nil
}

// parseBuildFailures reads the failed units, and the units each one blocks,
// from `flate build` stderr. It keeps no error text: the reason group of the
// failure line is matched but never copied.
func parseBuildFailures(stderr string) []model.BrokenUnit {
	var units []model.BrokenUnit

	for _, line := range strings.Split(stderr, "\n") {
		line = strings.TrimRight(line, "\r")

		if failedSummaryPattern.MatchString(line) {
			continue
		}

		if match := buildFailurePattern.FindStringSubmatch(line); match != nil {
			units = append(units, model.BrokenUnit{Kind: match[1], Namespace: match[2], Name: match[3]})
			continue
		}

		if match := blocksPattern.FindStringSubmatch(line); match != nil && len(units) > 0 {
			last := &units[len(units)-1]
			last.Blocks = append(last.Blocks, match[1])
		}
	}

	return units
}

// BrokenInBothSides picks, from the failed units of a head build, the units that
// flate's diff did not report on either side. flate stays silent for a unit
// that is broken identically in base and head, so those units are what
// remains. A unit the diff names, on any side, is a normal failure and stays
// out.
func BrokenInBothSides(diffFailures []model.Failure, built []model.BrokenUnit) []model.BrokenUnit {
	type unit struct{ kind, namespace, name string }

	reported := map[unit]bool{}
	for _, failure := range diffFailures {
		reported[unit{failure.Kind, failure.Namespace, failure.Name}] = true
	}

	var broken []model.BrokenUnit
	for _, candidate := range built {
		if !reported[unit{candidate.Kind, candidate.Namespace, candidate.Name}] {
			broken = append(broken, candidate)
		}
	}

	return broken
}
