package flate

import (
	"errors"
	"fmt"
	"regexp"
	"strconv"
)

// PinnedVersion is the flate release this project is tested against. CI and
// the Action download exactly this version.
const PinnedVersion = "v0.7.3"

// MinVersion is the oldest flate release whose JSON and stderr formats are
// supported.
const MinVersion = "v0.7.3"

var versionPattern = regexp.MustCompile(`v?(\d+)\.(\d+)\.(\d+)`)

var errUnparseableVersion = errors.New("unparseable version")

type semver [3]int

func parseVersion(text string) (semver, error) {
	match := versionPattern.FindStringSubmatch(text)
	if match == nil {
		return semver{}, errUnparseableVersion
	}

	var version semver
	for i := range version {
		number, err := strconv.Atoi(match[i+1])
		if err != nil {
			return semver{}, errUnparseableVersion
		}
		version[i] = number
	}

	return version, nil
}

// compareVersions returns -1, 0 or 1 when a is older than, equal to or newer
// than b. Pre-release and build suffixes are ignored.
func compareVersions(a, b string) (int, error) {
	left, err := parseVersion(a)
	if err != nil {
		return 0, fmt.Errorf("compare %q: %w", a, err)
	}

	right, err := parseVersion(b)
	if err != nil {
		return 0, fmt.Errorf("compare %q: %w", b, err)
	}

	for i := range left {
		switch {
		case left[i] < right[i]:
			return -1, nil
		case left[i] > right[i]:
			return 1, nil
		}
	}

	return 0, nil
}
