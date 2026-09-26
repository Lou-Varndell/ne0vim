package scraper

import (
	"context"
	"sync"
)

// runBounded calls fn once per item, running at most concurrency
// invocations at a time, and returns one result per item in input order.
// A canceled ctx is passed through to fn rather than short-circuiting the
// loop here, so each in-flight call fails (and is recorded) on its own
// terms.
func runBounded[T, R any](ctx context.Context, concurrency int, items []T, fn func(context.Context, T) R) []R {
	results := make([]R, len(items))
	if len(items) == 0 {
		return results
	}

	sem := make(chan struct{}, concurrency)
	var wg sync.WaitGroup

	for i, item := range items {
		wg.Add(1)
		sem <- struct{}{}

		go func(i int, item T) {
			defer wg.Done()
			defer func() { <-sem }()
			results[i] = fn(ctx, item)
		}(i, item)
	}

	wg.Wait()
	return results
}
