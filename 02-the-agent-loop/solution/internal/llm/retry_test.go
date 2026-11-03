package llm

import (
	"testing"
	"time"
)

func TestBackoffNeverExceedsCap(t *testing.T) {
	p := retryPolicy{max: 10, initial: 2 * time.Second, cap: 30 * time.Second}
	for attempt := 0; attempt < 10; attempt++ {
		for i := 0; i < 200; i++ {
			if d := p.backoff(attempt, nil); d > p.cap || d <= 0 {
				t.Fatalf("attempt %d: backoff %v outside (0, %v]", attempt, d, p.cap)
			}
		}
	}
}
