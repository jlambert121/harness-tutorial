package task

import (
	"fmt"
	"slices"
	"testing"
	"time"
)

func TestHiddenDedupeFast(t *testing.T) {
	items := make([]string, 200000)
	for i := range items {
		items[i] = fmt.Sprint(i % 150000)
	}
	done := make(chan []string, 1)
	go func() { done <- Dedupe(items) }()
	var got []string
	select {
	case got = <-done:
	case <-time.After(500 * time.Millisecond):
		t.Fatal("Dedupe took longer than 500ms for 200,000 items")
	}
	if len(got) != 150000 || got[0] != "0" || got[149999] != "149999" {
		t.Errorf("wrong output: len=%d", len(got))
	}
	if Dedupe(nil) != nil && len(Dedupe(nil)) != 0 {
		t.Error("nil input")
	}
	if !slices.Equal(Dedupe([]string{"x", "x", ""}), []string{"x", ""}) {
		t.Error("empty string handling")
	}
}
