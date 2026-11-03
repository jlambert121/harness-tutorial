package task

import "container/list"

// LRU is a fixed-capacity least-recently-used cache.
type LRU struct {
	cap   int
	order *list.List
	items map[string]*list.Element
}

type entry struct {
	key   string
	value int
}

// NewLRU returns an empty cache holding at most capacity entries.
func NewLRU(capacity int) *LRU {
	return &LRU{cap: capacity, order: list.New(), items: map[string]*list.Element{}}
}

// Get returns the value for key and whether it was present.
func (c *LRU) Get(key string) (int, bool) {
	e, ok := c.items[key]
	if !ok {
		return 0, false
	}
	c.order.MoveToFront(e)
	return e.Value.(*entry).value, true
}

// Put sets key to value, evicting the least recently used entry if needed.
func (c *LRU) Put(key string, value int) {
	if e, ok := c.items[key]; ok {
		e.Value.(*entry).value = value
		c.order.MoveToFront(e)
		return
	}
	c.items[key] = c.order.PushFront(&entry{key, value})
	if c.order.Len() > c.cap {
		last := c.order.Back()
		c.order.Remove(last)
		delete(c.items, last.Value.(*entry).key)
	}
}

// Len returns the number of entries.
func (c *LRU) Len() int { return c.order.Len() }
