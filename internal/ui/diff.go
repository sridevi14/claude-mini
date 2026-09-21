package ui

import "strings"

type diffKind int

const (
	diffEqual diffKind = iota
	diffDel
	diffAdd
)

type diffOp struct {
	kind diffKind
	text string
}

// Diff renders a colored, context-collapsed line diff between old and new text.
func Diff(oldText, newText string) string { return diff(oldText, newText, true) }

// DiffPlain renders the same diff without ANSI colors. Tool results are handed to
// the model, and escape codes there are tokens it pays for and cannot use.
func DiffPlain(oldText, newText string) string { return diff(oldText, newText, false) }

func diff(oldText, newText string, color bool) string {
	a := splitLines(oldText)
	b := splitLines(newText)
	ops := lcsDiff(a, b)
	return render(ops, color)
}

func splitLines(s string) []string {
	if s == "" {
		return nil
	}
	return strings.Split(strings.ReplaceAll(s, "\r\n", "\n"), "\n")
}

// lcsDiff produces a minimal-ish edit script via the classic LCS DP table.
func lcsDiff(a, b []string) []diffOp {
	n, m := len(a), len(b)
	dp := make([][]int, n+1)
	for i := range dp {
		dp[i] = make([]int, m+1)
	}
	for i := n - 1; i >= 0; i-- {
		for j := m - 1; j >= 0; j-- {
			if a[i] == b[j] {
				dp[i][j] = dp[i+1][j+1] + 1
			} else if dp[i+1][j] >= dp[i][j+1] {
				dp[i][j] = dp[i+1][j]
			} else {
				dp[i][j] = dp[i][j+1]
			}
		}
	}
	var ops []diffOp
	i, j := 0, 0
	for i < n && j < m {
		switch {
		case a[i] == b[j]:
			ops = append(ops, diffOp{diffEqual, a[i]})
			i++
			j++
		case dp[i+1][j] >= dp[i][j+1]:
			ops = append(ops, diffOp{diffDel, a[i]})
			i++
		default:
			ops = append(ops, diffOp{diffAdd, b[j]})
			j++
		}
	}
	for ; i < n; i++ {
		ops = append(ops, diffOp{diffDel, a[i]})
	}
	for ; j < m; j++ {
		ops = append(ops, diffOp{diffAdd, b[j]})
	}
	return ops
}

// render collapses long runs of unchanged context to keep diffs readable. When
// color is false the markers alone carry the meaning, so the output stays legible
// in a tool result.
func render(ops []diffOp, color bool) string {
	const ctx = 3
	red, green, gray, reset := Red, Green, Gray, Reset
	if !color {
		red, green, gray, reset = "", "", "", ""
	}
	// mark which equal lines are near a change and should be shown.
	show := make([]bool, len(ops))
	for i, op := range ops {
		if op.kind != diffEqual {
			for k := i - ctx; k <= i+ctx; k++ {
				if k >= 0 && k < len(ops) {
					show[k] = true
				}
			}
		}
	}
	var sb strings.Builder
	skipping := false
	for i, op := range ops {
		if op.kind == diffEqual && !show[i] {
			if !skipping {
				sb.WriteString(gray + "  ⋮" + reset + "\n")
				skipping = true
			}
			continue
		}
		skipping = false
		switch op.kind {
		case diffEqual:
			sb.WriteString(gray + "  " + op.text + reset + "\n")
		case diffDel:
			sb.WriteString(red + "- " + op.text + reset + "\n")
		case diffAdd:
			sb.WriteString(green + "+ " + op.text + reset + "\n")
		}
	}
	return sb.String()
}
