// Package pipeline runs items through a chain of stages, each with its own
// bounded worker pool, connected by small buffered channels. A slow stage
// backpressures the one before it instead of letting it race ahead and
// buffer everything in memory.
package pipeline

import (
	"context"
	"sync"

	"golang.org/x/sync/errgroup"
)

// Stage processes one item. Returning done ends the item's trip early (it
// needs no further stages, e.g. the file already exists); an error ends it
// as failed. Neither affects other items. Only an error that isFatal (see
// Run) stops the whole pipeline.
type Stage[T any] struct {
	Name    string
	Workers int
	Do      func(ctx context.Context, item T) (next T, done bool, err error)
}

type job[T any] struct {
	item T
	done bool
	err  error
}

// Run feeds items through stages in order and calls finish once per item
// that completes, successfully or not, from a single goroutine. If a stage
// returns an error for which isFatal is true, or ctx is cancelled, every
// stage stops, finish is not called for items still in flight, and Run
// returns that error once all goroutines have exited.
func Run[T any](parent context.Context, items []T, stages []Stage[T], isFatal func(error) bool, finish func(T, error)) error {
	g, ctx := errgroup.WithContext(parent)

	// Each channel holds about as many items as the stage reading it has
	// workers: enough to keep them busy, little enough to backpressure.
	chans := make([]chan job[T], len(stages)+1)
	for i := range stages {
		chans[i] = make(chan job[T], stages[i].Workers)
	}
	chans[len(stages)] = make(chan job[T], 1)

	send := func(ch chan<- job[T], j job[T]) error {
		select {
		case ch <- j:
			return nil
		case <-ctx.Done():
			return ctx.Err()
		}
	}

	g.Go(func() error {
		defer close(chans[0])
		for _, it := range items {
			if err := send(chans[0], job[T]{item: it}); err != nil {
				return err
			}
		}
		return nil
	})

	for i, st := range stages {
		in, out := chans[i], chans[i+1]
		var wg sync.WaitGroup
		for range max(st.Workers, 1) {
			wg.Add(1)
			g.Go(func() error {
				defer wg.Done()
				for j := range in {
					if j.err == nil && !j.done {
						next, done, err := st.Do(ctx, j.item)
						if err != nil && (ctx.Err() != nil || isFatal(err)) {
							return err
						}
						j = job[T]{item: next, done: done, err: err}
					}
					if err := send(out, j); err != nil {
						return err
					}
				}
				return nil
			})
		}
		g.Go(func() error {
			wg.Wait()
			close(out)
			return nil
		})
	}

	g.Go(func() error {
		for j := range chans[len(stages)] {
			finish(j.item, j.err)
		}
		return nil
	})

	// ctx itself is always cancelled once Wait returns; only the caller's
	// context says whether the run was interrupted.
	if err := g.Wait(); err != nil {
		return err
	}
	return parent.Err()
}
