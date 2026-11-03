package task

// Dedupe returns items with duplicates removed, keeping the first occurrence of each.
func Dedupe(items []string) []string {
	var out []string
	seen := make(map[string]struct{}, len(items))
	for _, it := range items {
		if _, ok := seen[it]; ok {
			continue
		}
		seen[it] = struct{}{}
		out = append(out, it)
	}
	return out
}
