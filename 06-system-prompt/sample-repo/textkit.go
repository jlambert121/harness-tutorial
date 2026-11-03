// Package textkit provides small string utilities.
package textkit

import (
	"strings"
	"unicode"
)

// Title upper-cases the first letter of every word.
func Title(s string) string {
	prev := ' '
	return strings.Map(func(r rune) rune {
		defer func() { prev = r }()
		if unicode.IsSpace(prev) {
			return unicode.ToUpper(r)
		}
		return r
	}, s)
}

// WordCount returns the number of whitespace-separated words in s.
func WordCount(s string) int {
	return len(strings.Fields(s))
}
