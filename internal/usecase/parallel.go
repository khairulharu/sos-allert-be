package usecase

import (
	"context"
	"sync"
)

// runConcurrently runs every task in its own goroutine and returns the
// first error in declaration order. The first failure cancels the shared
// context so the remaining datastore calls stop early instead of finishing
// work whose result is about to be discarded.
//
// It exists for the read paths whose independent queries were serialised
// and therefore paid one round trip each (#57): the results are unrelated,
// so wall time should be one round trip, not the sum.
func runConcurrently(ctx context.Context, tasks ...func(context.Context) error) error {
	if len(tasks) == 0 {
		return nil
	}

	ctx, cancel := context.WithCancel(ctx)
	defer cancel()

	// Each goroutine writes a distinct index, so no mutex is needed.
	errs := make([]error, len(tasks))
	var wg sync.WaitGroup
	for i, task := range tasks {
		wg.Add(1)
		go func(i int, task func(context.Context) error) {
			defer wg.Done()
			if err := task(ctx); err != nil {
				errs[i] = err
				cancel()
			}
		}(i, task)
	}
	wg.Wait()

	for _, err := range errs {
		if err != nil {
			return err
		}
	}
	return nil
}
