package guard

import (
	"bytes"
	"encoding/base64"
	"encoding/json"
	"sort"
	"strings"
)

// minKnownValueLength avoids over-replacing: very short values would mangle
// unrelated text.
const minKnownValueLength = 4

// maskedPlaceholder replaces a value in free-form text.
const maskedPlaceholder = "<masked>"

// MaskString removes every known secret value, and its standard and URL-safe
// base64 forms and its JSON-escaped forms, from s. Values shorter than four characters are ignored.
// Longer needles are replaced first so a value containing another is fully
// removed.
func MaskString(s string, knownValues []string) string {
	var needles []string
	for _, value := range knownValues {
		if len(value) < minKnownValueLength {
			continue
		}
		raw := []byte(value)
		needles = append(needles,
			value,
			base64.StdEncoding.EncodeToString(raw),
			base64.RawStdEncoding.EncodeToString(raw),
			base64.URLEncoding.EncodeToString(raw),
			base64.RawURLEncoding.EncodeToString(raw),
		)
		needles = append(needles, jsonEscapedForms(value)...)
	}

	sort.SliceStable(needles, func(i, j int) bool { return len(needles[i]) > len(needles[j]) })

	for _, needle := range needles {
		s = strings.ReplaceAll(s, needle, maskedPlaceholder)
	}

	return s
}

// jsonEscapedForms returns value as it appears inside a JSON string, with and
// without HTML escaping, when that differs from value itself.
func jsonEscapedForms(value string) []string {
	var forms []string

	if escaped, err := json.Marshal(value); err == nil {
		forms = append(forms, string(escaped[1:len(escaped)-1]))
	}

	var buf bytes.Buffer
	encoder := json.NewEncoder(&buf)
	encoder.SetEscapeHTML(false)
	if err := encoder.Encode(value); err == nil {
		text := strings.TrimRight(buf.String(), "\n")
		forms = append(forms, text[1:len(text)-1])
	}

	out := forms[:0]
	for _, form := range forms {
		if form != value {
			out = append(out, form)
		}
	}

	return out
}
