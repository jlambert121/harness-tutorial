package report

import (
	"io"
	"sort"
)

// Row is one line of the report.
type Row struct {
	Name  string
	Count int
}

// Formatter writes rows in some output format.
type Formatter interface {
	Format(w io.Writer, rows []Row) error
}

// Collect counts names, most frequent first, ties by name.
func Collect(names []string) []Row {
	counts := map[string]int{}
	for _, n := range names {
		counts[n]++
	}
	var rows []Row
	for n, c := range counts {
		rows = append(rows, Row{n, c})
	}
	sort.Slice(rows, func(i, j int) bool {
		if rows[i].Count != rows[j].Count {
			return rows[i].Count > rows[j].Count
		}
		return rows[i].Name < rows[j].Name
	})
	return rows
}
