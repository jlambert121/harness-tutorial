package report

import (
	"encoding/json"
	"io"
	"math"
)

// JSON formats rows as a JSON array.
type JSON struct{}

// Format implements Formatter.
func (JSON) Format(w io.Writer, rows []Row) error {
	total := 0
	for _, r := range rows {
		total += r.Count
	}
	type item struct {
		Name  string  `json:"name"`
		Count int     `json:"count"`
		Share float64 `json:"share"`
	}
	items := []item{}
	for _, r := range rows {
		share := 0.0
		if total > 0 {
			share = math.Round(float64(r.Count)/float64(total)*100) / 100
		}
		items = append(items, item{r.Name, r.Count, share})
	}
	return json.NewEncoder(w).Encode(items)
}
