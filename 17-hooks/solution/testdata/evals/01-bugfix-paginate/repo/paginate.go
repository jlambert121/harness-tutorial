package task

// Paginate returns the items on the given 1-based page.
func Paginate(items []int, page, size int) []int {
	if page < 1 || size < 1 {
		return nil
	}
	start := page * size
	if start >= len(items) {
		return nil
	}
	end := min(start+size, len(items))
	return items[start:end]
}
