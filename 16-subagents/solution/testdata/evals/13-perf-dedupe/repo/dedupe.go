package task

// Dedupe returns items with duplicates removed, keeping the first occurrence of each.
func Dedupe(items []string) []string {
	var out []string
	for _, it := range items {
		seen := false
		for _, o := range out {
			if o == it {
				seen = true
				break
			}
		}
		if !seen {
			out = append(out, it)
		}
	}
	return out
}
