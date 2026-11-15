// Package guard keeps secret values out of every output sink: it enforces
// flate's --skip-kinds, masks Secret objects and configurable patterns in the
// result model, and scrubs known values from free-form strings.
//
// Known limits: flate runs with Secrets skipped, so no Secret value is ever in
// the result and there is nothing to scrub from warning messages, failure
// reasons or unparsed stderr lines except values of configured patterns and
// ConfigMap-like objects. Those free-form texts are therefore only as safe as
// flate's own messages, which name keys and objects but not values. Stderr
// lines are truncated before masking, so a value split by the cut is not
// matched. A "kind: List" that wraps Secrets is not masked structurally;
// skipping the Secret kind in flate is what keeps those out.
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

// valueFlags are the flate diff flags that consume the next argument as their
// value. flate does not accept abbreviations of flag names, so --skip-kinds
// has no alias to recognise.
var valueFlags = map[string]bool{
	"--allow-missing-kinds": true, "-a": true, "--api-versions": true,
	"--base": true, "--cache-dir": true, "--concurrency": true,
	"--git-depth": true, "--kube-version": true, "-n": true,
	"--namespace": true, "-o": true, "--output": true, "-p": true,
	"--path": true, "-P": true, "--path-orig": true,
	"--registry-config": true, "--skip-kinds": true, "--strip-attr": true,
	"--strip-field": true, "--supply": true, "--log-level": true,
}

// ErrAmbiguousArgs means the arguments could be parsed differently by flate
// than by the guard, so the guard cannot prove that Secrets are skipped.
var ErrAmbiguousArgs = errors.New("ambiguous flate arguments")

// EnforceSkipKinds returns a copy of args whose --skip-kinds flag skips at
// least Secret and CustomResourceDefinition. The forced flag is placed first
// (right after a leading "diff" or "build" subcommand), before every other argument, so
// no user-supplied argument can shadow it. A user-supplied --skip-kinds (separate
// value or --skip-kinds=X form, repeated or empty) is merged into it; one that
// lacks Secret (compared case-insensitively) is rejected, a list lacking only
// CustomResourceDefinition is extended. A bare "--" terminator and a
// value-taking flag without a value are rejected. The input slice is not
// modified.
func EnforceSkipKinds(args []string) ([]string, error) {
	kinds := append([]string(nil), requiredSkipKinds...)
	rest := make([]string, 0, len(args)+1)

	for i := 0; i < len(args); i++ {
		arg := args[i]

		if arg == "--" {
			return nil, fmt.Errorf("%w: the \"--\" terminator is not allowed", ErrAmbiguousArgs)
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
			if valueFlags[arg] {
				if i+1 >= len(args) {
					return nil, fmt.Errorf("%w: %s has no value", ErrAmbiguousArgs, arg)
				}
				// The next argument is this flag's value, never a flag.
				i++
				rest = append(rest, args[i])
			}
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

	forced := skipKindsFlag + "=" + strings.Join(kinds, ",")

	insertAt := 0
	if len(rest) > 0 && (rest[0] == "diff" || rest[0] == "build") {
		insertAt = 1
	}

	out := make([]string, 0, len(rest)+1)
	out = append(out, rest[:insertAt]...)
	out = append(out, forced)

	return append(out, rest[insertAt:]...), nil
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
		if strings.EqualFold(candidate, item) {
			return true
		}
	}

	return false
}
