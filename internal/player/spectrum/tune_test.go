package spectrum

import (
	"encoding/binary"
	"os"
	"os/exec"
	"testing"
)

// decodeSeconds decodes n seconds of a real song, starting at `at`, the way
// the tap does. These tests run only with GEET_TAP_FILE set: a unit test
// must not depend on this machine's music library.
func decodeSeconds(t *testing.T, at, n string) []byte {
	t.Helper()
	path := os.Getenv("GEET_TAP_FILE")
	if path == "" {
		t.Skip("set GEET_TAP_FILE to a song to run this")
	}
	out, err := exec.Command("ffmpeg", "-hide_banner", "-loglevel", "error", "-nostdin",
		"-ss", at, "-i", path, "-vn", "-f", "s16le", "-acodec", "pcm_s16le",
		"-ac", "1", "-ar", "22050", "-t", n, "-").Output()
	if err != nil {
		t.Fatal(err)
	}
	return out
}

func feedAll(a *Analyzer, pcm []byte, each func(levels []float64)) {
	buf := make([]float64, FrameSize)
	for f := 0; (f+1)*FrameSize*2 < len(pcm); f++ {
		off := f * FrameSize * 2
		for i := range buf {
			buf[i] = float64(int16(binary.LittleEndian.Uint16(pcm[off+i*2:]))) / 32768
		}
		each(a.Feed(buf))
	}
}

// TestBandPowerRange reports the raw band energy of real music, which is
// where minPeak's value comes from.
func TestBandPowerRange(t *testing.T) {
	for _, at := range []string{"2", "30", "90"} {
		a := NewAnalyzer(SampleRate, 24)
		pcm := decodeSeconds(t, at, "3")
		var loudest float64
		feedAll(a, pcm, func([]float64) {
			for _, p := range a.powers {
				if p > loudest {
					loudest = p
				}
			}
		})
		t.Logf("at %ss: loudest band power %.4f", at, loudest)
	}
}

// TestLevelSpread checks that the bars vary: a display where every band
// sits at the top says nothing about the music.
func TestLevelSpread(t *testing.T) {
	a := NewAnalyzer(SampleRate, 24)
	pcm := decodeSeconds(t, "30", "5")
	var full, quiet, cells int
	feedAll(a, pcm, func(levels []float64) {
		for _, l := range levels {
			cells++
			switch {
			case l > 0.95:
				full++
			case l < 0.05:
				quiet++
			}
		}
	})
	if cells == 0 {
		t.Fatal("no frames decoded")
	}
	saturated := 100 * float64(full) / float64(cells)
	t.Logf("cells=%d saturated=%.0f%% near-silent=%.0f%%", cells, saturated, 100*float64(quiet)/float64(cells))
	if saturated > 35 {
		t.Errorf("%.0f%% of bars are pinned at the top: the display has no dynamics", saturated)
	}
}
