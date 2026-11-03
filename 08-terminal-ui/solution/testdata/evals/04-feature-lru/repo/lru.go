package task

// LRU is a fixed-capacity least-recently-used cache.
type LRU struct {
	// TODO
}

// NewLRU returns an empty cache holding at most capacity entries.
func NewLRU(capacity int) *LRU {
	return &LRU{}
}

// Get returns the value for key and whether it was present.
func (c *LRU) Get(key string) (int, bool) {
	return 0, false
}

// Put sets key to value, evicting the least recently used entry if needed.
func (c *LRU) Put(key string, value int) {}

// Len returns the number of entries.
func (c *LRU) Len() int { return 0 }
