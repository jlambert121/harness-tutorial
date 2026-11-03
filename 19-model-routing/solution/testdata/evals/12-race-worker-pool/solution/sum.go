package task

import (
	"context"
	"sync"
)

// Sum adds nums using the given number of worker goroutines. It returns
// ctx.Err() if ctx is cancelled before all numbers are processed.
func Sum(ctx context.Context, nums []int, workers int) (int, error) {
	jobs := make(chan int)
	var mu sync.Mutex
	total := 0
	var wg sync.WaitGroup
	for i := 0; i < workers; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			local := 0
			for n := range jobs {
				local += n
			}
			mu.Lock()
			total += local
			mu.Unlock()
		}()
	}
	var err error
feed:
	for _, n := range nums {
		select {
		case jobs <- n:
		case <-ctx.Done():
			err = ctx.Err()
			break feed
		}
	}
	close(jobs)
	wg.Wait()
	if err != nil {
		return 0, err
	}
	return total, nil
}
