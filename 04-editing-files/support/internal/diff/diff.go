// Package diff renders line-based unified diffs.
package diff

import (
	"fmt"
	"strings"
)

// Unified returns a unified diff of a → b with the given lines of context,
// or "" if they are equal. Inputs above maxCells line pairs fall back to a summary.
func Unified(a, b, name string, context int) string {
	if a == b {
		return ""
	}
	x, y := split(a), split(b)
	const maxCells = 4_000_000
	if len(x)*len(y) > maxCells {
		return fmt.Sprintf("--- %s\n+++ %s\n(file too large to diff: %d → %d lines)\n", name, name, len(x), len(y))
	}
	ops := lcsOps(x, y)
	var out strings.Builder
	fmt.Fprintf(&out, "--- %s\n+++ %s\n", name, name)
	for i := 0; i < len(ops); {
		if ops[i].kind == ' ' {
			i++
			continue
		}
		start := max(0, i-context)
		end := i
		for end < len(ops) {
			if ops[end].kind != ' ' {
				end++
				continue
			}
			run := end
			for run < len(ops) && ops[run].kind == ' ' {
				run++
			}
			if run-end > 2*context || run == len(ops) {
				end = min(len(ops), end+context)
				break
			}
			end = run
		}
		hunk := ops[start:end]
		aStart, bStart := hunk[0].ai+1, hunk[0].bi+1
		var aLen, bLen int
		for _, o := range hunk {
			if o.kind != '+' {
				aLen++
			}
			if o.kind != '-' {
				bLen++
			}
		}
		fmt.Fprintf(&out, "@@ -%d,%d +%d,%d @@\n", aStart, aLen, bStart, bLen)
		for _, o := range hunk {
			out.WriteByte(o.kind)
			out.WriteString(o.text)
			out.WriteByte('\n')
		}
		i = end
	}
	return out.String()
}

type op struct {
	kind   byte
	text   string
	ai, bi int
}

func split(s string) []string {
	if s == "" {
		return nil
	}
	return strings.Split(strings.TrimSuffix(s, "\n"), "\n")
}

func lcsOps(x, y []string) []op {
	n, m := len(x), len(y)
	dp := make([][]int, n+1)
	for i := range dp {
		dp[i] = make([]int, m+1)
	}
	for i := n - 1; i >= 0; i-- {
		for j := m - 1; j >= 0; j-- {
			if x[i] == y[j] {
				dp[i][j] = dp[i+1][j+1] + 1
			} else {
				dp[i][j] = max(dp[i+1][j], dp[i][j+1])
			}
		}
	}
	var ops []op
	i, j := 0, 0
	for i < n || j < m {
		switch {
		case i < n && j < m && x[i] == y[j]:
			ops = append(ops, op{' ', x[i], i, j})
			i++
			j++
		case i < n && (j == m || dp[i+1][j] >= dp[i][j+1]):
			ops = append(ops, op{'-', x[i], i, j})
			i++
		default:
			ops = append(ops, op{'+', y[j], i, j})
			j++
		}
	}
	return ops
}
