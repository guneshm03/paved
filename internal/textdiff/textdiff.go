// Package textdiff computes line diffs with Myers' O(ND) algorithm and renders
// them in unified diff format.
package textdiff

import (
	"fmt"
	"strings"
)

// OpKind is the kind of an edit.
type OpKind int

// Edit kinds.
const (
	Equal OpKind = iota
	Delete
	Insert
)

// Op is one line-level edit.
type Op struct {
	Kind OpKind
	Line string
}

// SplitLines splits text into lines, keeping a final line without trailing
// newline as its own element. An empty string has zero lines.
func SplitLines(s string) []string {
	if s == "" {
		return nil
	}
	lines := strings.SplitAfter(s, "\n")
	if lines[len(lines)-1] == "" {
		lines = lines[:len(lines)-1]
	}
	return lines
}

// Lines computes a shortest edit script turning a into b. It runs in
// O((N+M)·D) time and O(D²) space for the stored trace, where D is the edit
// distance — small for the near-identical files a dry run usually compares.
func Lines(a, b []string) []Op {
	n, m := len(a), len(b)
	maxD := n + m
	if maxD == 0 {
		return nil
	}
	offset := maxD
	v := make([]int, 2*maxD+2)
	var trace [][]int
	found := false
	for d := 0; d <= maxD && !found; d++ {
		snapshot := make([]int, len(v))
		copy(snapshot, v)
		trace = append(trace, snapshot)
		for k := -d; k <= d; k += 2 {
			var x int
			if k == -d || (k != d && v[offset+k-1] < v[offset+k+1]) {
				x = v[offset+k+1] // move down: insertion from b
			} else {
				x = v[offset+k-1] + 1 // move right: deletion from a
			}
			y := x - k
			for x < n && y < m && a[x] == b[y] {
				x++
				y++
			}
			v[offset+k] = x
			if x >= n && y >= m {
				found = true
				break
			}
		}
	}
	return backtrack(trace, a, b, offset)
}

func backtrack(trace [][]int, a, b []string, offset int) []Op {
	x, y := len(a), len(b)
	var rev []Op
	for d := len(trace) - 1; d >= 0; d-- {
		v := trace[d]
		k := x - y
		var prevK int
		if k == -d || (k != d && v[offset+k-1] < v[offset+k+1]) {
			prevK = k + 1
		} else {
			prevK = k - 1
		}
		prevX := v[offset+prevK]
		prevY := prevX - prevK
		for x > prevX && y > prevY {
			x--
			y--
			rev = append(rev, Op{Kind: Equal, Line: a[x]})
		}
		if d > 0 {
			if x == prevX {
				y--
				rev = append(rev, Op{Kind: Insert, Line: b[y]})
			} else {
				x--
				rev = append(rev, Op{Kind: Delete, Line: a[x]})
			}
		}
	}
	ops := make([]Op, len(rev))
	for i := range rev {
		ops[i] = rev[len(rev)-1-i]
	}
	return ops
}

// Unified renders a unified diff between old and new text with the given
// number of context lines. It returns "" when the texts are equal.
func Unified(oldName, newName, oldText, newText string, context int) string {
	if oldText == newText {
		return ""
	}
	ops := Lines(SplitLines(oldText), SplitLines(newText))
	var b strings.Builder
	fmt.Fprintf(&b, "--- %s\n+++ %s\n", oldName, newName)
	for _, h := range hunks(ops, context) {
		b.WriteString(h)
	}
	return b.String()
}

// hunks groups ops into unified-diff hunks.
func hunks(ops []Op, context int) []string {
	type pos struct{ a, b int }
	// Position of each op in old/new files (0-based, before applying op).
	at := make([]pos, len(ops)+1)
	for i, op := range ops {
		at[i+1] = at[i]
		switch op.Kind {
		case Equal:
			at[i+1].a++
			at[i+1].b++
		case Delete:
			at[i+1].a++
		case Insert:
			at[i+1].b++
		}
	}
	var out []string
	i := 0
	for i < len(ops) {
		for i < len(ops) && ops[i].Kind == Equal {
			i++
		}
		if i == len(ops) {
			break
		}
		start := max(i-context, 0)
		end := i
		// Extend the hunk while changes are within 2*context equal lines.
		for end < len(ops) {
			if ops[end].Kind != Equal {
				end++
				continue
			}
			run := end
			for run < len(ops) && ops[run].Kind == Equal {
				run++
			}
			if run == len(ops) || run-end > 2*context {
				end = min(end+context, len(ops))
				break
			}
			end = run
		}
		var b strings.Builder
		oldStart, newStart := at[start].a, at[start].b
		oldLen, newLen := at[end].a-oldStart, at[end].b-newStart
		fmt.Fprintf(&b, "@@ -%s +%s @@\n", hunkRange(oldStart, oldLen), hunkRange(newStart, newLen))
		for _, op := range ops[start:end] {
			prefix := " "
			switch op.Kind {
			case Delete:
				prefix = "-"
			case Insert:
				prefix = "+"
			}
			b.WriteString(prefix + op.Line)
			if !strings.HasSuffix(op.Line, "\n") {
				b.WriteString("\n\\ No newline at end of file\n")
			}
		}
		out = append(out, b.String())
		i = end
	}
	return out
}

func hunkRange(start, length int) string {
	if length == 0 {
		return fmt.Sprintf("%d,0", start)
	}
	if length == 1 {
		return fmt.Sprintf("%d", start+1)
	}
	return fmt.Sprintf("%d,%d", start+1, length)
}

// Stats counts inserted and deleted lines.
func Stats(ops []Op) (added, deleted int) {
	for _, op := range ops {
		switch op.Kind {
		case Insert:
			added++
		case Delete:
			deleted++
		}
	}
	return added, deleted
}
