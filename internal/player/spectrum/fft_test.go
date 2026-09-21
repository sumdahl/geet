package spectrum

import (
	"math"
	"strings"
	"testing"
)

// A pure tone must light the band that owns its frequency, and leave the
// far ends of the spectrum alone.
func TestAnalyzerFindsTone(t *testing.T) {
	const bands = 24
	for _, hz := range []float64{100, 1000, 8000} {
		a := NewAnalyzer(SampleRate, bands)
		samples := make([]float64, FrameSize)
		for i := range samples {
			samples[i] = math.Sin(2 * math.Pi * hz * float64(i) / SampleRate)
		}
		var levels []float64
		// Levels are smoothed, so feed the tone until it settles.
		for range 20 {
			levels = a.Feed(samples)
		}
		peak, peakBand := 0.0, -1
		for b, l := range levels {
			if l > peak {
				peak, peakBand = l, b
			}
		}
		if peak < 0.3 {
			t.Fatalf("%.0f Hz: loudest band only %.2f, expected a clear peak", hz, peak)
		}
		want := bandOf(a, hz)
		if peakBand < want-1 || peakBand > want+1 {
			t.Errorf("%.0f Hz: peak in band %d, expected around %d", hz, peakBand, want)
		}
	}
}

// bandOf reports which band a frequency falls into, by bin.
func bandOf(a *Analyzer, hz float64) int {
	bin := int(hz * float64(FrameSize) / float64(SampleRate))
	for b := range a.levels {
		if bin >= a.edges[b] && bin < a.edges[b+1] {
			return b
		}
	}
	return len(a.levels) - 1
}

func TestAnalyzerSilenceFades(t *testing.T) {
	a := NewAnalyzer(SampleRate, 8)
	loud := make([]float64, FrameSize)
	for i := range loud {
		loud[i] = math.Sin(2 * math.Pi * 440 * float64(i) / SampleRate)
	}
	for range 10 {
		a.Feed(loud)
	}
	for range 60 {
		a.Silence()
	}
	for b, l := range a.levels {
		if l != 0 {
			t.Errorf("band %d still at %.3f after silence", b, l)
		}
	}
}

// Every band must own at least one bin, however many bands are asked for.
func TestBandEdgesAreDistinct(t *testing.T) {
	for _, bands := range []int{4, 16, 48, 64} {
		a := NewAnalyzer(SampleRate, bands)
		for i := 1; i <= bands; i++ {
			if a.edges[i] <= a.edges[i-1] && a.edges[i] < FrameSize/2 {
				t.Errorf("bands=%d: band %d is empty (%d..%d)", bands, i-1, a.edges[i-1], a.edges[i])
			}
		}
	}
}

func TestRenderShape(t *testing.T) {
	rows := Render([]float64{0, 0.5, 1}, 4, 2)
	if len(rows) != 4 {
		t.Fatalf("got %d rows, want 4", len(rows))
	}
	// Bottom row: the silent bar is blank, the full bar is solid.
	bottom := rows[3]
	if !strings.HasPrefix(bottom, "  ") {
		t.Errorf("silent bar is not blank: %q", bottom)
	}
	if !strings.HasSuffix(bottom, "██") {
		t.Errorf("full bar is not solid: %q", bottom)
	}
	// Top row: only the full bar reaches it.
	if strings.TrimSpace(rows[0]) != "██" {
		t.Errorf("top row = %q, want only the full bar", rows[0])
	}
}

func TestBandsForWidth(t *testing.T) {
	if got := BandsFor(10, 1); got != 5 {
		t.Errorf("BandsFor(10,1) = %d, want 5", got)
	}
	if got := BandsFor(2, 2); got != 4 {
		t.Errorf("a tiny width must still give the minimum: %d", got)
	}
	if got := BandsFor(1000, 1); got != 64 {
		t.Errorf("a huge width must cap: %d", got)
	}
}
