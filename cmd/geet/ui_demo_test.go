package main

import (
	"fmt"
	"os"
	"sync"
	"testing"
	"time"
)

// TestUIDemo drives the real progress display with simulated downloads, for
// checking it in a terminal of a given size. It runs only on request, and
// from a test binary, since go test captures stderr:
//
//	go test -c -o uidemo.test ./cmd/geet
//	script -qefc "stty cols 150 rows 12; GEET_UI_DEMO=1 ./uidemo.test -test.run TestUIDemo" out.txt
func TestUIDemo(t *testing.T) {
	if os.Getenv("GEET_UI_DEMO") != "1" {
		t.Skip("set GEET_UI_DEMO=1 to run in a terminal")
	}
	const n, jobs = 40, 16
	u := newBarUI(os.Stderr)
	sem := make(chan struct{}, jobs)
	var wg sync.WaitGroup
	for i := 1; i <= n; i++ {
		tu := u.track(i, n, fmt.Sprintf("Artist %d - Song %d", i, i))
		tu.stage(event{Stage: "resolved"})
		wg.Add(1)
		go func() {
			defer wg.Done()
			sem <- struct{}{}
			defer func() { <-sem }()
			tu.stage(event{Stage: "downloading"})
			for p := int64(0); p <= 100; p += 10 {
				tu.progress(p, 100)
				time.Sleep(60 * time.Millisecond)
			}
			tu.stage(event{Stage: "downloaded"})
			tu.stage(event{Stage: "tagging"})
			time.Sleep(50 * time.Millisecond)
			tu.stage(event{Stage: "done", Path: fmt.Sprintf("/music/song%d.opus", i)})
		}()
	}
	wg.Wait()
	u.close(false)
}
