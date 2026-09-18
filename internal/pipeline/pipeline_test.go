package pipeline

import (
	"context"
	"errors"
	"runtime"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

type item struct {
	n     int
	trail []string
}

func stage(name string, workers int, fn func(it *item) (bool, error)) Stage[*item] {
	return Stage[*item]{Name: name, Workers: workers, Do: func(ctx context.Context, it *item) (*item, bool, error) {
		it.trail = append(it.trail, name)
		done, err := fn(it)
		return it, done, err
	}}
}

func items(n int) []*item {
	out := make([]*item, n)
	for i := range out {
		out[i] = &item{n: i}
	}
	return out
}

func never(error) bool { return false }

func TestRunRoutesEveryItem(t *testing.T) {
	errOdd := errors.New("odd")
	stages := []Stage[*item]{
		stage("resolve", 3, func(it *item) (bool, error) { return it.n%10 == 0, nil }), // every 10th: done early
		stage("download", 2, func(it *item) (bool, error) {
			if it.n%7 == 3 {
				return false, errOdd
			}
			return false, nil
		}),
		stage("tag", 1, func(*item) (bool, error) { return false, nil }),
	}
	var mu sync.Mutex
	seen := map[int]error{}
	err := Run(context.Background(), items(50), stages, never, func(it *item, err error) {
		mu.Lock()
		defer mu.Unlock()
		if _, dup := seen[it.n]; dup {
			t.Errorf("item %d finished twice", it.n)
		}
		seen[it.n] = err
		var want []string
		switch {
		case it.n%10 == 0:
			want = []string{"resolve"}
		case it.n%7 == 3:
			want = []string{"resolve", "download"}
		default:
			want = []string{"resolve", "download", "tag"}
		}
		if len(it.trail) != len(want) {
			t.Errorf("item %d went through %v, want %v", it.n, it.trail, want)
		}
		if (it.n%7 == 3 && it.n%10 != 0) != (err != nil) {
			t.Errorf("item %d err = %v", it.n, err)
		}
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(seen) != 50 {
		t.Errorf("%d items finished, want 50", len(seen))
	}
}

func TestRunBoundsEachStage(t *testing.T) {
	var cur, peak [2]atomic.Int32
	busy := func(i int) func(*item) (bool, error) {
		return func(*item) (bool, error) {
			n := cur[i].Add(1)
			for {
				p := peak[i].Load()
				if n <= p || peak[i].CompareAndSwap(p, n) {
					break
				}
			}
			time.Sleep(2 * time.Millisecond)
			cur[i].Add(-1)
			return false, nil
		}
	}
	stages := []Stage[*item]{stage("wide", 8, busy(0)), stage("narrow", 2, busy(1))}
	if err := Run(context.Background(), items(60), stages, never, func(*item, error) {}); err != nil {
		t.Fatal(err)
	}
	if p := peak[0].Load(); p > 8 || p < 2 {
		t.Errorf("wide stage peaked at %d workers, want 2-8", p)
	}
	if p := peak[1].Load(); p != 2 {
		t.Errorf("narrow stage peaked at %d workers, want 2", p)
	}
}

func TestRunStopsOnFatal(t *testing.T) {
	errFatal := errors.New("tool missing")
	var ran atomic.Int32
	stages := []Stage[*item]{stage("s", 2, func(it *item) (bool, error) {
		ran.Add(1)
		if it.n == 3 {
			return false, errFatal
		}
		time.Sleep(time.Millisecond)
		return false, nil
	})}
	err := Run(context.Background(), items(1000), stages, func(err error) bool { return errors.Is(err, errFatal) }, func(*item, error) {})
	if !errors.Is(err, errFatal) {
		t.Fatalf("err = %v, want the fatal error", err)
	}
	if n := ran.Load(); n > 50 {
		t.Errorf("%d items still ran after the fatal error", n)
	}
}

// Cancelling mid-flight, with items blocked in every stage and in every
// channel, must stop all workers without leaking a goroutine.
func TestRunCancelMidFlightLeaksNothing(t *testing.T) {
	before := runtime.NumGoroutine()
	ctx, cancel := context.WithCancel(context.Background())
	started := make(chan struct{}, 100)
	block := func(ctx context.Context, it *item) (*item, bool, error) {
		started <- struct{}{}
		<-ctx.Done()
		return it, false, ctx.Err()
	}
	stages := []Stage[*item]{
		{Name: "resolve", Workers: 8, Do: func(ctx context.Context, it *item) (*item, bool, error) { return it, false, nil }},
		{Name: "download", Workers: 4, Do: block},
		{Name: "tag", Workers: 2, Do: block},
	}
	finished := 0
	done := make(chan error)
	go func() {
		done <- Run(ctx, items(200), stages, never, func(*item, error) { finished++ })
	}()
	for range 4 {
		<-started // all download workers are busy and the channels have filled up
	}
	cancel()
	select {
	case err := <-done:
		if !errors.Is(err, context.Canceled) {
			t.Errorf("err = %v, want context.Canceled", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("Run did not return after cancel")
	}
	if finished != 0 {
		t.Errorf("finish called %d times for interrupted items", finished)
	}

	deadline := time.Now().Add(2 * time.Second)
	for runtime.NumGoroutine() > before && time.Now().Before(deadline) {
		time.Sleep(10 * time.Millisecond)
	}
	if n := runtime.NumGoroutine(); n > before {
		buf := make([]byte, 1<<16)
		t.Fatalf("%d goroutines leaked:\n%s", n-before, buf[:runtime.Stack(buf, true)])
	}
}

func TestRunEmpty(t *testing.T) {
	if err := Run(context.Background(), nil, []Stage[*item]{stage("s", 2, func(*item) (bool, error) { return false, nil })}, never, func(*item, error) {
		t.Error("finish called")
	}); err != nil {
		t.Fatal(err)
	}
}
