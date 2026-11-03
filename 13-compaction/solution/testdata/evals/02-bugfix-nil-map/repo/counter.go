package task

// Counter counts occurrences of strings. The zero value should be ready to use.
type Counter struct {
	counts map[string]int
}

// Add records one occurrence of key.
func (c *Counter) Add(key string) {
	c.counts[key]++
}

// Count returns how many times key was added.
func (c *Counter) Count(key string) int {
	return c.counts[key]
}
