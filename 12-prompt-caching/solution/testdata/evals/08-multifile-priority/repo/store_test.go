package task

import "testing"

func TestSeed(t *testing.T) {
	var s Store
	Seed(&s)
	if len(s.List()) != 3 {
		t.Error("Seed")
	}
}
