package report

import (
	"fmt"
	"io"
)

// Text formats rows as aligned plain text.
type Text struct{}

// Format implements Formatter.
func (Text) Format(w io.Writer, rows []Row) error {
	for _, r := range rows {
		if _, err := fmt.Fprintf(w, "%-6s %d\n", r.Name, r.Count); err != nil {
			return err
		}
	}
	return nil
}
