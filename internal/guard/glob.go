package guard

import (
	"errors"
	"regexp"
	"strings"
)

// compileGlob turns a case-insensitive glob into an anchored regular
// expression. Unlike path.Match, "*" and "?" also match "/" and ".", because
// Kubernetes keys such as "example.com/client-secret" contain both and a
// pattern like "*secret*" must still cover them.
//
// Supported syntax: "*" (any run), "?" (one character), "[...]" classes with
// optional leading "!" or "^" negation, and "\" to take the next character
// literally.
func compileGlob(pattern string) (*regexp.Regexp, error) {
	var expr strings.Builder
	expr.WriteString("(?is)^")

	runes := []rune(pattern)
	for i := 0; i < len(runes); i++ {
		switch r := runes[i]; r {
		case '*':
			expr.WriteString(".*")
		case '?':
			expr.WriteString(".")
		case '\\':
			i++
			if i >= len(runes) {
				return nil, errors.New("trailing backslash")
			}
			expr.WriteString(regexp.QuoteMeta(string(runes[i])))
		case '[':
			end, class, err := globClass(runes, i)
			if err != nil {
				return nil, err
			}
			expr.WriteString(class)
			i = end
		default:
			expr.WriteString(regexp.QuoteMeta(string(r)))
		}
	}
	expr.WriteString("$")

	return regexp.Compile(expr.String())
}

// globClass converts the "[...]" class starting at runes[start] and returns
// the index of its closing bracket.
func globClass(runes []rune, start int) (int, string, error) {
	var class strings.Builder
	class.WriteString("[")

	i := start + 1
	if i < len(runes) && (runes[i] == '!' || runes[i] == '^') {
		class.WriteString("^")
		i++
	}

	members := 0
	for ; i < len(runes); i++ {
		switch r := runes[i]; r {
		case ']':
			if members == 0 {
				return 0, "", errors.New("empty character class")
			}
			class.WriteString("]")

			return i, class.String(), nil
		case '\\':
			i++
			if i >= len(runes) {
				return 0, "", errors.New("trailing backslash")
			}
			class.WriteString(regexp.QuoteMeta(string(runes[i])))
			members++
		case '-':
			class.WriteString("-")
			members++
		default:
			class.WriteString(regexp.QuoteMeta(string(r)))
			members++
		}
	}

	return 0, "", errors.New("unclosed character class")
}

// splitPathPattern splits a dot-separated path pattern into segments. "\."
// keeps a literal dot inside a segment, so keys like "app.kubernetes.io/name"
// can be addressed; the backslash stays for compileGlob to consume.
func splitPathPattern(pattern string) []string {
	var segments []string
	var current strings.Builder

	runes := []rune(pattern)
	for i := 0; i < len(runes); i++ {
		switch runes[i] {
		case '\\':
			current.WriteRune(runes[i])
			if i+1 < len(runes) {
				i++
				current.WriteRune(runes[i])
			}
		case '.':
			segments = append(segments, current.String())
			current.Reset()
		default:
			current.WriteRune(runes[i])
		}
	}

	return append(segments, current.String())
}
