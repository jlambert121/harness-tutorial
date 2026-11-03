package tools

import (
	"errors"
	"fmt"
	"strings"
)

type Strategy string

// Strategies in the order Codex's apply_patch tries them (seek_sequence.rs).
const (
	StrategyExact      Strategy = "exact"
	StrategyTrailingWS Strategy = "trailing_ws" // ignore trailing whitespace per line
	StrategyTrimmed    Strategy = "trimmed"     // ignore leading and trailing whitespace per line
	StrategyUnicode    Strategy = "unicode"     // also map smart quotes, dashes, odd spaces to ASCII
)

var AllStrategies = []Strategy{StrategyExact, StrategyTrailingWS, StrategyTrimmed, StrategyUnicode}

var ErrNoMatch = errors.New("no match")

type AmbiguousError struct {
	Strategy Strategy
	Count    int
}

func (e *AmbiguousError) Error() string {
	return fmt.Sprintf("%d matches (strategy %s)", e.Count, e.Strategy)
}

// FindMatch locates old in content using the first strategy that produces
// exactly one match. A strategy that produces several matches stops the
// search: guessing which one was meant is how fuzzy matchers corrupt files.
// Both strings must already use "\n" line endings.
func FindMatch(content, old string, strategies []Strategy) (start, end int, used Strategy, err error) {
	for _, s := range strategies {
		var spans [][2]int
		if s == StrategyExact {
			spans = exactSpans(content, old)
		} else {
			spans = lineSpans(content, old, normalizer(s))
		}
		switch len(spans) {
		case 0:
			continue
		case 1:
			return spans[0][0], spans[0][1], s, nil
		default:
			return 0, 0, s, &AmbiguousError{Strategy: s, Count: len(spans)}
		}
	}
	return 0, 0, "", ErrNoMatch
}

func exactSpans(content, old string) [][2]int {
	var spans [][2]int
	for i := 0; ; {
		j := strings.Index(content[i:], old)
		if j < 0 {
			return spans
		}
		spans = append(spans, [2]int{i + j, i + j + len(old)})
		i += j + 1
	}
}

// lineSpans compares old against every window of the same number of content
// lines after normalizing each line. A match spans whole lines; the final
// newline is included only if old ends with one.
func lineSpans(content, old string, norm func(string) string) [][2]int {
	want := strings.Split(strings.TrimSuffix(old, "\n"), "\n")
	for i := range want {
		want[i] = norm(want[i])
	}
	lines := strings.SplitAfter(content, "\n")
	offsets := make([]int, len(lines)+1)
	for i, l := range lines {
		offsets[i+1] = offsets[i] + len(l)
	}
	var spans [][2]int
	for i := 0; i+len(want) <= len(lines); i++ {
		ok := true
		for k := range want {
			if norm(strings.TrimSuffix(lines[i+k], "\n")) != want[k] {
				ok = false
				break
			}
		}
		if !ok {
			continue
		}
		end := offsets[i+len(want)]
		if !strings.HasSuffix(old, "\n") && strings.HasSuffix(lines[i+len(want)-1], "\n") {
			end--
		}
		spans = append(spans, [2]int{offsets[i], end})
	}
	return spans
}

var unicodeASCII = strings.NewReplacer(
	"‘", "'", "’", "'", "‚", "'", "‛", "'",
	"“", `"`, "”", `"`, "„", `"`, "‟", `"`,
	"‐", "-", "‑", "-", "‒", "-", "–", "-", "—", "-", "−", "-",
	" ", " ", " ", " ", " ", " ", " ", " ", " ", " ",
	"…", "...",
)

func normalizer(s Strategy) func(string) string {
	switch s {
	case StrategyTrailingWS:
		return func(l string) string { return strings.TrimRight(l, " \t\r") }
	case StrategyTrimmed:
		return func(l string) string { return strings.TrimSpace(l) }
	default:
		return func(l string) string { return strings.TrimSpace(unicodeASCII.Replace(l)) }
	}
}
