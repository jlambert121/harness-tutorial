package task

import "testing"

func TestHiddenLRU(t *testing.T) {
	c := NewLRU(2)
	c.Put("a", 1)
	c.Put("b", 2)
	if v, ok := c.Get("a"); !ok || v != 1 {
		t.Fatalf("Get(a) = %d, %v", v, ok)
	}
	c.Put("c", 3)
	if _, ok := c.Get("b"); ok {
		t.Error("b should have been evicted (least recently used)")
	}
	c.Put("a", 10)
	if v, _ := c.Get("a"); v != 10 {
		t.Errorf("update failed: %d", v)
	}
	if c.Len() != 2 {
		t.Errorf("Len = %d", c.Len())
	}
	c.Put("d", 4)
	if _, ok := c.Get("c"); ok {
		t.Error("c should have been evicted")
	}
	one := NewLRU(1)
	one.Put("x", 1)
	one.Put("y", 2)
	if _, ok := one.Get("x"); ok || one.Len() != 1 {
		t.Error("capacity 1")
	}
}
