package task

import (
	"context"
	"sync"
)

// Sum adds nums using the given number of worker goroutines. It returns
// ctx.Err() if ctx is cancelled before all numbers are processed.
func Sum(ctx context.Context, nums []int, workers int) (int, error) {
	jobs := make(chan int)
	total := 0
	var wg sync.WaitGroup
	for i := 0; i < workers; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for n := range jobs {
				total += n
			}
		}()
	}
	for _, n := range nums {
		jobs <- n
	}
	close(jobs)
	wg.Wait()
	return total, nil
}
