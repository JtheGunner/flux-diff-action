package format

import (
	"bytes"
	"encoding/json"
	"fmt"
	"strings"
)

const (
	diffContextLines = 3
	// maxDiffCells bounds the LCS table; larger inputs fall back to a full
	// replacement diff.
	maxDiffCells = 4_000_000
)

type diffOp struct {
	kind byte // ' ', '-' or '+'
	text string
}

// manifestLines renders a raw manifest as stable, indented JSON lines. An
// empty manifest yields no lines. Unparseable input is kept verbatim: the
// guard has already masked it.
func manifestLines(raw json.RawMessage) []string {
	if len(bytes.TrimSpace(raw)) == 0 {
		return nil
	}

	var tree any
	if err := json.Unmarshal(raw, &tree); err != nil {
		return strings.Split(strings.TrimSpace(string(raw)), "\n")
	}

	var buf bytes.Buffer
	encoder := json.NewEncoder(&buf)
	encoder.SetEscapeHTML(false)
	encoder.SetIndent("", "  ")
	if err := encoder.Encode(tree); err != nil {
		return strings.Split(strings.TrimSpace(string(raw)), "\n")
	}

	return strings.Split(strings.TrimRight(buf.String(), "\n"), "\n")
}

// unifiedDiff returns a unified diff with hunk headers, or "" when the sides
// are identical.
func unifiedDiff(oldLines, newLines []string) string {
	ops := diffOps(oldLines, newLines)

	changed := false
	for _, op := range ops {
		if op.kind != ' ' {
			changed = true
			break
		}
	}
	if !changed {
		return ""
	}

	var out strings.Builder
	for _, hunk := range hunks(ops) {
		out.WriteString(hunk)
	}

	return out.String()
}

func diffOps(oldLines, newLines []string) []diffOp {
	rows, cols := len(oldLines), len(newLines)

	if (rows+1)*(cols+1) > maxDiffCells {
		ops := make([]diffOp, 0, rows+cols)
		for _, line := range oldLines {
			ops = append(ops, diffOp{'-', line})
		}
		for _, line := range newLines {
			ops = append(ops, diffOp{'+', line})
		}

		return ops
	}

	// lcs[i][j] is the LCS length of oldLines[i:] and newLines[j:].
	lcs := make([][]int32, rows+1)
	for i := range lcs {
		lcs[i] = make([]int32, cols+1)
	}
	for i := rows - 1; i >= 0; i-- {
		for j := cols - 1; j >= 0; j-- {
			if oldLines[i] == newLines[j] {
				lcs[i][j] = lcs[i+1][j+1] + 1
			} else {
				lcs[i][j] = max(lcs[i+1][j], lcs[i][j+1])
			}
		}
	}

	ops := make([]diffOp, 0, rows+cols)
	i, j := 0, 0
	for i < rows && j < cols {
		switch {
		case oldLines[i] == newLines[j]:
			ops = append(ops, diffOp{' ', oldLines[i]})
			i++
			j++
		case lcs[i+1][j] >= lcs[i][j+1]:
			ops = append(ops, diffOp{'-', oldLines[i]})
			i++
		default:
			ops = append(ops, diffOp{'+', newLines[j]})
			j++
		}
	}
	for ; i < rows; i++ {
		ops = append(ops, diffOp{'-', oldLines[i]})
	}
	for ; j < cols; j++ {
		ops = append(ops, diffOp{'+', newLines[j]})
	}

	return ops
}

// hunks groups ops into unified-diff hunks with context lines.
func hunks(ops []diffOp) []string {
	keep := make([]bool, len(ops))
	for index, op := range ops {
		if op.kind == ' ' {
			continue
		}
		for k := max(0, index-diffContextLines); k <= min(len(ops)-1, index+diffContextLines); k++ {
			keep[k] = true
		}
	}

	var result []string
	oldLine, newLine := 1, 1
	for index := 0; index < len(ops); {
		if !keep[index] {
			oldLine, newLine = advance(ops[index], oldLine, newLine)
			index++
			continue
		}

		startOld, startNew := oldLine, newLine
		var body strings.Builder
		oldCount, newCount := 0, 0
		for ; index < len(ops) && keep[index]; index++ {
			op := ops[index]
			body.WriteByte(op.kind)
			body.WriteString(op.text)
			body.WriteByte('\n')
			if op.kind != '+' {
				oldCount++
			}
			if op.kind != '-' {
				newCount++
			}
			oldLine, newLine = advance(op, oldLine, newLine)
		}

		if oldCount == 0 {
			startOld--
		}
		if newCount == 0 {
			startNew--
		}
		result = append(result, fmt.Sprintf("@@ -%d,%d +%d,%d @@\n%s", startOld, oldCount, startNew, newCount, body.String()))
	}

	return result
}

func advance(op diffOp, oldLine, newLine int) (int, int) {
	if op.kind != '+' {
		oldLine++
	}
	if op.kind != '-' {
		newLine++
	}

	return oldLine, newLine
}
