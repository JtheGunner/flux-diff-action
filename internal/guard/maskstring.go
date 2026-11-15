package guard

import (
	"encoding/base64"
	"sort"
	"strings"
)

// minKnownValueLength avoids over-replacing: very short values would mangle
// unrelated text.
const minKnownValueLength = 4

// maskedPlaceholder replaces a value in free-form text.
const maskedPlaceholder = "<masked>"

// MaskString removes every known secret value, and its standard and URL-safe
// base64 forms, from s. Values shorter than four characters are ignored.
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
	}

	sort.SliceStable(needles, func(i, j int) bool { return len(needles[i]) > len(needles[j]) })

	for _, needle := range needles {
		s = strings.ReplaceAll(s, needle, maskedPlaceholder)
	}

	return s
}
