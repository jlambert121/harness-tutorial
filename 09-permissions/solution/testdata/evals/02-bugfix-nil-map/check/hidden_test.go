package task

import "testing"

func TestHiddenCounterZeroValue(t *testing.T) {
	var c Counter
	c.Add("a")
	c.Add("a")
	c.Add("b")
	if c.Count("a") != 2 || c.Count("b") != 1 || c.Count("z") != 0 {
		t.Errorf("counts = %d %d %d", c.Count("a"), c.Count("b"), c.Count("z"))
	}
	var empty Counter
	if empty.Count("x") != 0 {
		t.Error("Count on zero value")
	}
}
