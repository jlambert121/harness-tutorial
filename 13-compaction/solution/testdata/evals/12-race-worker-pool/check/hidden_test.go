package task

import (
	"context"
	"errors"
	"testing"
	"time"
)

func TestHiddenSumCorrect(t *testing.T) {
	nums := make([]int, 20000)
	want := 0
	for i := range nums {
		nums[i] = i
		want += i
	}
	for _, w := range []int{1, 4, 16} {
		got, err := Sum(context.Background(), nums, w)
		if err != nil || got != want {
			t.Errorf("workers=%d: Sum = %d, %v; want %d", w, got, err, want)
		}
	}
}

func TestHiddenSumCancel(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	nums := make([]int, 200_000)
	start := time.Now()
	_, err := Sum(ctx, nums, 2)
	if !errors.Is(err, context.Canceled) {
		t.Errorf("err = %v, want context.Canceled", err)
	}
	if time.Since(start) > 200*time.Millisecond {
		t.Errorf("cancelled Sum took %v", time.Since(start))
	}
}
