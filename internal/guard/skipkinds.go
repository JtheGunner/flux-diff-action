// Package guard keeps secret values out of every output sink: it enforces
// flate's --skip-kinds, masks Secret objects and configurable patterns in the
// result model, and scrubs known values from free-form strings.
package guard

import (
	"errors"
	"fmt"
	"strings"
)

const skipKindsFlag = "--skip-kinds"

// requiredSkipKinds must always be skipped by flate.
var requiredSkipKinds = []string{"Secret", "CustomResourceDefinition"}

// ErrSecretNotSkipped means a configuration tried to let flate render Secrets.
var ErrSecretNotSkipped = errors.New("--skip-kinds must include Secret")

// EnforceSkipKinds returns a copy of args whose --skip-kinds flag skips at
// least Secret and CustomResourceDefinition. A missing flag is added; a
// user-supplied list (separate value or --skip-kinds=X form, repeated or
// empty) that lacks Secret is rejected, a list lacking only
// CustomResourceDefinition is extended. The flag is placed before a bare
// "--" argument terminator, and anything after it is left untouched. The input slice is not modified.
func EnforceSkipKinds(args []string) ([]string, error) {
	kinds := append([]string(nil), requiredSkipKinds...)
	rest := make([]string, 0, len(args)+1)

	var tail []string

	for i := 0; i < len(args); i++ {
		arg := args[i]

		// Everything after a bare "--" is positional; the forced flag must
		// come before it to be parsed as a flag.
		if arg == "--" {
			tail = append(tail, args[i:]...)
			break
		}

		var value string
		switch {
		case arg == skipKindsFlag:
			if i+1 >= len(args) {
				return nil, fmt.Errorf("%w: flag has no value", ErrSecretNotSkipped)
			}
			i++
			value = args[i]
		case strings.HasPrefix(arg, skipKindsFlag+"="):
			value = strings.TrimPrefix(arg, skipKindsFlag+"=")
		default:
			rest = append(rest, arg)
			continue
		}

		listed := splitKinds(value)
		if !contains(listed, "Secret") {
			return nil, ErrSecretNotSkipped
		}
		for _, kind := range listed {
			if !contains(kinds, kind) {
				kinds = append(kinds, kind)
			}
		}
	}

	rest = append(rest, skipKindsFlag+"="+strings.Join(kinds, ","))

	return append(rest, tail...), nil
}

func splitKinds(value string) []string {
	var kinds []string
	for _, part := range strings.Split(value, ",") {
		if trimmed := strings.TrimSpace(part); trimmed != "" {
			kinds = append(kinds, trimmed)
		}
	}

	return kinds
}

func contains(list []string, item string) bool {
	for _, candidate := range list {
		if candidate == item {
			return true
		}
	}

	return false
}
