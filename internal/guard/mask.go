package guard

import (
	"bytes"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"regexp"
	"strings"

	"github.com/JtheGunner/flux-diff-action/internal/model"
)

const (
	// MaskedChanged replaces a secret value that differs between base and head.
	// It carries no hash and no length.
	MaskedChanged = "<masked, changed>"
	// Masked replaces a secret value that was added, removed or is shown on the
	// old side of a change.
	Masked = "<masked>"

	maskedUnparseable = "<masked, unparseable>"
	secretKind        = "Secret"
	lastAppliedKey    = "kubectl.kubernetes.io/last-applied-configuration"
)

// Config holds the user-configurable masking rules.
type Config struct {
	// ExtraKeyPatterns are case-insensitive globs matched against every map
	// key at any depth, for example "client_secret" or "*password*". "*" and
	// "?" also match "/" and "." (see compileGlob).
	ExtraKeyPatterns []string
	// ExtraPaths are dot-separated paths whose segments may be globs, matched
	// case-insensitively from the object root, for example "spec.values.auth.*".
	// Write "\." for a literal dot inside a segment.
	ExtraPaths []string
}

// Masker masks secret values in the result model.
type Masker struct {
	keyPatterns []*regexp.Regexp
	paths       [][]*regexp.Regexp
}

// New validates cfg and returns a Masker.
func New(cfg Config) (*Masker, error) {
	masker := &Masker{}

	for _, pattern := range cfg.ExtraKeyPatterns {
		compiled, err := compileGlob(pattern)
		if err != nil {
			return nil, fmt.Errorf("invalid key pattern %q: %w", pattern, err)
		}
		masker.keyPatterns = append(masker.keyPatterns, compiled)
	}

	for _, extra := range cfg.ExtraPaths {
		var segments []*regexp.Regexp
		for _, segment := range splitPathPattern(extra) {
			compiled, err := compileGlob(segment)
			if err != nil {
				return nil, fmt.Errorf("invalid path %q: %w", extra, err)
			}
			segments = append(segments, compiled)
		}
		masker.paths = append(masker.paths, segments)
	}

	return masker, nil
}

// MaskResult returns a copy of res that is safe for every output sink: change
// objects are masked, and every secret value found in them is scrubbed from
// warning messages, failure reasons and unparsed lines. The input is not
// modified.
func (m *Masker) MaskResult(res model.Result) model.Result {
	known := m.KnownValues(res)

	out := res
	out.Changes = m.maskChanges(res.Changes, known)
	out.Suppressed = m.maskChanges(res.Suppressed, known)

	if res.Warnings != nil {
		out.Warnings = make([]model.Warning, len(res.Warnings))
		for i, warning := range res.Warnings {
			warning.Message = MaskString(warning.Message, known)
			warning.Keys = make([]string, len(res.Warnings[i].Keys))
			for k, key := range res.Warnings[i].Keys {
				warning.Keys[k] = MaskString(key, known)
			}
			out.Warnings[i] = warning
		}
	}

	if res.Failures != nil {
		out.Failures = make([]model.Failure, len(res.Failures))
		for i, failure := range res.Failures {
			failure.Reason = MaskString(failure.Reason, known)
			out.Failures[i] = failure
		}
	}

	if res.Unparsed != nil {
		out.Unparsed = make([]string, len(res.Unparsed))
		for i, line := range res.Unparsed {
			out.Unparsed[i] = MaskString(line, known)
		}
	}

	return out
}

// KnownValues returns every value the masker would hide in res, for use with
// MaskString on logs and error messages built outside the result model.
func (m *Masker) KnownValues(res model.Result) []string {
	var known []string
	for _, group := range [][]model.Change{res.Changes, res.Suppressed} {
		for _, change := range group {
			for _, raw := range []json.RawMessage{change.Old, change.New} {
				tree, ok := parse(raw)
				if !ok {
					continue
				}
				secret := change.Kind == secretKind || kindOf(tree) == secretKind
				for _, loc := range m.locations(tree, secret) {
					known = appendLeaves(known, loc.value, loc.base64Encoded)
				}
			}
		}
	}

	return known
}

func (m *Masker) maskChanges(changes []model.Change, known []string) []model.Change {
	if changes == nil {
		return nil
	}

	out := make([]model.Change, len(changes))
	for i, change := range changes {
		out[i] = m.maskChange(change, known)
	}

	return out
}

func (m *Masker) maskChange(change model.Change, known []string) model.Change {
	oldTree, oldOK := parse(change.Old)
	newTree, newOK := parse(change.New)

	secret := change.Kind == secretKind ||
		(oldOK && kindOf(oldTree) == secretKind) ||
		(newOK && kindOf(newTree) == secretKind)

	oldLocs := map[string]any{}
	newLocs := map[string]any{}
	for key, loc := range m.locations(oldTree, secret) {
		oldLocs[key] = loc.value
	}
	for key, loc := range m.locations(newTree, secret) {
		newLocs[key] = loc.value
	}

	change.Old = m.maskRaw(change.Old, oldTree, oldOK, secret, oldLocs, newLocs, false, known)
	change.New = m.maskRaw(change.New, newTree, newOK, secret, newLocs, oldLocs, true, known)

	return change
}

func (m *Masker) maskRaw(
	raw json.RawMessage, tree any, ok, secret bool,
	own, other map[string]any, isNew bool, known []string,
) json.RawMessage {
	if len(raw) == 0 {
		return raw
	}
	if !ok {
		return json.RawMessage(`"` + maskedUnparseable + `"`)
	}

	masked := scrubTree(m.redact(tree, nil, secret, own, other, isNew), known)

	encoded, err := marshalNoEscape(masked)
	if err != nil {
		return json.RawMessage(`"` + maskedUnparseable + `"`)
	}

	// Defence in depth: the tree was already scrubbed value by value, so this
	// only catches forms that survive in the encoded bytes.
	scrubbed := []byte(MaskString(string(encoded), known))
	if !json.Valid(scrubbed) {
		return json.RawMessage(`"` + maskedUnparseable + `"`)
	}

	return scrubbed
}

// scrubTree applies MaskString to every string value and map key of node, so
// known values are removed from their decoded form rather than from the
// JSON-escaped bytes. It returns a new tree.
func scrubTree(node any, known []string) any {
	switch typed := node.(type) {
	case string:
		return MaskString(typed, known)
	case map[string]any:
		out := make(map[string]any, len(typed))
		for key, child := range typed {
			out[MaskString(key, known)] = scrubTree(child, known)
		}

		return out
	case []any:
		out := make([]any, len(typed))
		for i, child := range typed {
			out[i] = scrubTree(child, known)
		}

		return out
	default:
		return node
	}
}

// location is one masked place in an object.
type location struct {
	value         any
	base64Encoded bool // value is base64 of the real secret (Secret data)
}

// locations returns the masked values of tree keyed by joined path.
func (m *Masker) locations(tree any, secret bool) map[string]location {
	found := map[string]location{}
	if tree == nil {
		return found
	}

	m.walk(tree, nil, secret, func(p []string, value any, fromSecretData bool) {
		found[joinPath(p)] = location{value: value, base64Encoded: fromSecretData}
	})

	return found
}

// walk calls visit for each masked value and does not descend into it.
func (m *Masker) walk(node any, p []string, secret bool, visit func([]string, any, bool)) {
	switch typed := node.(type) {
	case map[string]any:
		for key, child := range typed {
			childPath := appendPath(p, key)
			if masked, fromData := m.isMasked(childPath, child, secret); masked {
				visit(childPath, child, fromData)
				continue
			}
			m.walk(child, childPath, secret, visit)
		}
	case []any:
		for i, child := range typed {
			childPath := appendPath(p, fmt.Sprint(i))
			if masked, fromData := m.isMasked(childPath, child, secret); masked {
				visit(childPath, child, fromData)
				continue
			}
			m.walk(child, childPath, secret, visit)
		}
	}
}

// redact returns a masked copy of node.
func (m *Masker) redact(node any, p []string, secret bool, own, other map[string]any, isNew bool) any {
	switch typed := node.(type) {
	case map[string]any:
		out := make(map[string]any, len(typed))
		for key, child := range typed {
			childPath := appendPath(p, key)
			if masked, _ := m.isMasked(childPath, child, secret); masked {
				if replacement, keep := replacementFor(childPath, own, other, isNew, false); keep {
					out[key] = replacement
				}
				continue
			}
			out[key] = m.redact(child, childPath, secret, own, other, isNew)
		}

		return out
	case []any:
		out := make([]any, len(typed))
		for i, child := range typed {
			childPath := appendPath(p, fmt.Sprint(i))
			if masked, _ := m.isMasked(childPath, child, secret); masked {
				out[i], _ = replacementFor(childPath, own, other, isNew, true)
				continue
			}
			out[i] = m.redact(child, childPath, secret, own, other, isNew)
		}

		return out
	default:
		return node
	}
}

// replacementFor decides what replaces the masked value at p. keep is false
// when the value is unchanged and the entry is dropped; array items are never
// dropped because that would shift indexes.
func replacementFor(p []string, own, other map[string]any, isNew, inArray bool) (replacement any, keep bool) {
	key := joinPath(p)
	counterpart, existsOther := other[key]
	if !existsOther {
		return Masked, true
	}
	if equalValues(own[key], counterpart) {
		if inArray {
			return Masked, true
		}

		return nil, false
	}
	if isNew {
		return MaskedChanged, true
	}

	return Masked, true
}

// isMasked reports whether the value at p must be masked. fromSecretData is
// true for the values of a Secret's data/stringData.
func (m *Masker) isMasked(p []string, value any, secret bool) (masked, fromSecretData bool) {
	if secret {
		switch {
		case len(p) == 2 && (p[0] == "data" || p[0] == "stringData"):
			return true, p[0] == "data"
		case len(p) == 1 && (p[0] == "data" || p[0] == "stringData"):
			if _, isMap := value.(map[string]any); !isMap {
				return true, false
			}
		case len(p) == 3 && p[0] == "metadata" && p[1] == "annotations" && p[2] == lastAppliedKey:
			return true, false
		}
	}

	last := p[len(p)-1]
	for _, pattern := range m.keyPatterns {
		if pattern.MatchString(last) {
			return true, false
		}
	}

	for _, segments := range m.paths {
		if matchesPath(segments, p) {
			return true, false
		}
	}

	return false, false
}

func matchesPath(segments []*regexp.Regexp, p []string) bool {
	if len(segments) != len(p) {
		return false
	}
	for i, segment := range segments {
		if !segment.MatchString(p[i]) {
			return false
		}
	}

	return true
}

func appendLeaves(known []string, value any, base64Encoded bool) []string {
	switch typed := value.(type) {
	case string:
		known = append(known, typed)
		if base64Encoded {
			if decoded, err := base64.StdEncoding.DecodeString(typed); err == nil {
				known = append(known, string(decoded))
			}
		}
	case json.Number:
		known = append(known, typed.String())
	case map[string]any:
		for _, child := range typed {
			known = appendLeaves(known, child, false)
		}
	case []any:
		for _, child := range typed {
			known = appendLeaves(known, child, false)
		}
	}

	return known
}

func parse(raw json.RawMessage) (any, bool) {
	if len(raw) == 0 {
		return nil, false
	}

	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.UseNumber()

	var tree any
	if err := decoder.Decode(&tree); err != nil {
		return nil, false
	}

	return tree, true
}

func kindOf(tree any) string {
	object, ok := tree.(map[string]any)
	if !ok {
		return ""
	}
	kind, _ := object["kind"].(string)

	return kind
}

func equalValues(a, b any) bool {
	left, errLeft := json.Marshal(a)
	right, errRight := json.Marshal(b)

	return errLeft == nil && errRight == nil && bytes.Equal(left, right)
}

func marshalNoEscape(value any) ([]byte, error) {
	var buf bytes.Buffer
	encoder := json.NewEncoder(&buf)
	encoder.SetEscapeHTML(false)
	if err := encoder.Encode(value); err != nil {
		return nil, err
	}

	return bytes.TrimRight(buf.Bytes(), "\n"), nil
}

func appendPath(p []string, segment string) []string {
	out := make([]string, len(p)+1)
	copy(out, p)
	out[len(p)] = segment

	return out
}

func joinPath(p []string) string {
	return strings.Join(p, "\x00")
}
