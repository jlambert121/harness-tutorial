package task

import (
	"testing"
	"time"
)

func TestHiddenLimiterSpec(t *testing.T) {
	t0 := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	l := NewLimiter(3, 2, t0)
	for i := 0; i < 3; i++ {
		if !l.Allow(t0) {
			t.Fatalf("starts full: call %d denied", i)
		}
	}
	if l.Allow(t0) {
		t.Fatal("empty bucket allowed")
	}
	if l.Allow(t0.Add(250 * time.Millisecond)) {
		t.Fatal("half a token is not a whole token")
	}
	if !l.Allow(t0.Add(500 * time.Millisecond)) {
		t.Fatal("fractions must accumulate: 0.5 + 0.5 = 1 token at 500ms")
	}
	if l.Allow(t0.Add(100 * time.Millisecond)) {
		t.Fatal("clock went backwards: no refill")
	}
	if !l.Allow(t0.Add(time.Second)) {
		t.Fatal("after backwards time, refill continues from the latest time seen (500ms→1s = 1 token)")
	}
	if !l.Allow(t0.Add(time.Hour)) || !l.Allow(t0.Add(time.Hour)) || !l.Allow(t0.Add(time.Hour)) || l.Allow(t0.Add(time.Hour)) {
		t.Fatal("capacity caps refill at 3 tokens")
	}
}
