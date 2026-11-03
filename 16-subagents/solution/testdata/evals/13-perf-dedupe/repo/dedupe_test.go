package task

import (
	"slices"
	"testing"
)

func TestDedupe(t *testing.T) {
	got := Dedupe([]string{"b", "a", "b", "c", "a"})
	if !slices.Equal(got, []string{"b", "a", "c"}) {
		t.Errorf("got %v", got)
	}
}
