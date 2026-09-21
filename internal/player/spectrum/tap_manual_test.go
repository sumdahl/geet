package spectrum

import (
	"context"
	"os"
	"testing"
	"time"
)

// TestTapOnRealFile checks the ffmpeg tap against an actual song. It runs
// only when GEET_TAP_FILE points at one, since a test must not depend on
// this machine's music library.
func TestTapOnRealFile(t *testing.T) {
	path := os.Getenv("GEET_TAP_FILE")
	if path == "" {
		t.Skip("set GEET_TAP_FILE to a song to run this")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 6*time.Second)
	defer cancel()
	// Start well inside the song: the first seconds of many tracks are
	// near silence, and silent bars are not a failure.
	frames, stop := Tap{Bands: 16}.Frames(ctx, path, 30*time.Second)
	defer stop()

	var got int
	var loudest float64
	for levels := range frames {
		got++
		for _, l := range levels {
			if l > loudest {
				loudest = l
			}
		}
		if got >= 40 {
			break
		}
	}
	t.Logf("frames=%d loudest=%.3f", got, loudest)
	if got == 0 {
		t.Fatal("the tap produced no frames")
	}
	if loudest < 0.1 {
		t.Errorf("every band stayed near zero (loudest %.3f): the music is not reaching the analyzer", loudest)
	}
}
