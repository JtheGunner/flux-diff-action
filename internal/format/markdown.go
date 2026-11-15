package format

import (
	"fmt"
	"html"
	"strings"
	"unicode/utf8"

	"github.com/JtheGunner/flux-diff-action/internal/model"
)

const (
	// DefaultMarker is the hidden first line that identifies the comment.
	DefaultMarker = "<!-- flux-diff -->"
	// DefaultMaxChars stays below GitHub's 65,536 character comment limit.
	DefaultMaxChars = 65000

	// noticeReserve is the room kept free for the truncation notice.
	noticeReserve = 300

	minFenceLength = 3
)

// MarkdownOptions configures Markdown.
type MarkdownOptions struct {
	// MaxChars is the maximum comment length in characters; DefaultMaxChars
	// when zero or negative.
	MaxChars int
	// Marker is the hidden first line; DefaultMarker when empty.
	Marker string
}

// entry is one object block, with its group heading on the first block of a
// group so a heading is never left without an object.
type entry struct {
	heading string
	block   string
	stub    string
}

// Markdown renders the result as a PR comment. The result must already be
// masked. The body never exceeds MaxChars characters and every <details> block
// is closed; truncated reports whether anything was left out.
func Markdown(r model.Result, o MarkdownOptions) (body string, truncated bool) {
	limit := o.MaxChars
	if limit <= 0 {
		limit = DefaultMaxChars
	}
	marker := o.Marker
	if marker == "" {
		marker = DefaultMarker
	}

	head := markdownHead(r, marker)
	partial, partialTruncated := partialSection(r, limit-runeCount(head)-noticeReserve)
	entries := buildEntries(r.Changes)

	total := runeCount(head) + runeCount(partial)
	for _, e := range entries {
		total += runeCount(e.heading + e.block)
	}

	if !partialTruncated && total <= limit {
		var out strings.Builder
		out.WriteString(head + partial)
		for _, e := range entries {
			out.WriteString(e.heading + e.block)
		}

		return out.String(), false
	}

	var shown strings.Builder
	shown.WriteString(head + partial)
	used := runeCount(head) + runeCount(partial) + noticeReserve

	withDiff := 0
	for _, e := range entries {
		text := e.heading + e.block
		if used+runeCount(text) <= limit {
			shown.WriteString(text)
			used += runeCount(text)
			withDiff++
			continue
		}

		if stub := e.heading + e.stub; used+runeCount(stub) <= limit {
			shown.WriteString(stub)
		}

		break
	}

	fmt.Fprintf(&shown, "\n> [!NOTE]\n> Output truncated at the comment size limit: full diffs shown for %d of %d changed objects. "+
		"Use `--format text` or `--format json` for the complete result.\n", withDiff, len(entries))

	return clampRunes(shown.String(), limit), true
}

func markdownHead(r model.Result, marker string) string {
	var out strings.Builder

	out.WriteString(marker + "\n")
	out.WriteString("## flux-diff: manifest changes\n\n")
	out.WriteString("**" + countChanges(r.Changes).summary() + "**")
	if len(r.Suppressed) > 0 {
		fmt.Fprintf(&out, " (%d more suppressed as noise)", len(r.Suppressed))
	}
	out.WriteString("\n\n")

	if text := verdict(r); text != "" {
		out.WriteString(text + "\n\n")
	}

	return out.String()
}

// partialSection lists everything that could not be fully rendered. It is
// placed before the object diffs so truncation never hides it. When the list
// does not fit budget, trailing items are replaced by a count.
func partialSection(r model.Result, budget int) (section string, truncated bool) {
	items := incompleteItems(r)
	if len(items) == 0 {
		return "", false
	}

	var out strings.Builder
	out.WriteString("### Not rendered / warnings\n\n")
	used := runeCount(out.String())

	for index, item := range items {
		line := fmt.Sprintf("- **%s:** %s\n", item.category, codeSpan(singleLine(item.text)))
		rest := fmt.Sprintf("- ... and %d more items not shown\n", len(items)-index)

		fits := used+runeCount(line) <= budget
		last := index == len(items)-1
		if !fits || (!last && used+runeCount(line)+runeCount(rest) > budget) {
			out.WriteString(rest + "\n")

			return out.String(), true
		}

		out.WriteString(line)
		used += runeCount(line)
	}
	out.WriteString("\n")

	return out.String(), false
}

func buildEntries(changes []model.Change) []entry {
	var entries []entry

	for _, grp := range groupByParent(changes) {
		heading := "### Other objects\n\n"
		if grp.label != "" {
			heading = "### " + codeSpan(grp.label) + "\n\n"
		}
		for index, change := range grp.changes {
			e := entry{block: objectBlock(change), stub: objectStub(change)}
			if index == 0 {
				e.heading = heading
			}
			entries = append(entries, e)
		}
	}

	return entries
}

func objectSummary(change model.Change) string {
	return fmt.Sprintf("<summary>%s <code>%s</code></summary>",
		html.EscapeString(string(change.Status)), html.EscapeString(singleLine(describeChange(change))))
}

func objectBlock(change model.Change) string {
	diff := unifiedDiff(manifestLines(change.Old), manifestLines(change.New))
	if diff == "" {
		diff = "(no textual difference)\n"
	}
	fence := strings.Repeat("`", max(minFenceLength, longestBacktickRun(diff)+1))

	return fmt.Sprintf("<details>\n%s\n\n%sdiff\n%s%s\n</details>\n\n", objectSummary(change), fence, diff, fence)
}

// objectStub is the fallback for a diff too large for the remaining room.
func objectStub(change model.Change) string {
	return fmt.Sprintf("<details>\n%s\n\nDiff omitted: too large for the comment size limit.\n\n</details>\n\n", objectSummary(change))
}

// codeSpan wraps untrusted text in an inline code span that the text cannot
// break out of.
func codeSpan(text string) string {
	text = singleLine(text)
	ticks := strings.Repeat("`", longestBacktickRun(text)+1)

	return ticks + " " + text + " " + ticks
}

func longestBacktickRun(text string) int {
	longest, run := 0, 0
	for _, r := range text {
		if r == '`' {
			run++
			longest = max(longest, run)
			continue
		}
		run = 0
	}

	return longest
}

func runeCount(text string) int {
	return utf8.RuneCountInString(text)
}

func clampRunes(text string, limit int) string {
	if runeCount(text) <= limit {
		return text
	}

	return string([]rune(text)[:limit])
}
