// Package spectrum turns a stream of audio samples into the bar heights a
// visualizer draws. The maths is ours; ffmpeg does the decoding.
package spectrum

import "math"

// fft computes the in-place radix-2 FFT of re/im, which must be the same
// length and a power of two. Only this file needs complex arithmetic, and a
// dependency for eighty lines of it would be silly.
func fft(re, im []float64) {
	n := len(re)
	if n <= 1 {
		return
	}
	// Bit-reversal permutation.
	for i, j := 1, 0; i < n; i++ {
		bit := n >> 1
		for ; j&bit != 0; bit >>= 1 {
			j ^= bit
		}
		j |= bit
		if i < j {
			re[i], re[j] = re[j], re[i]
			im[i], im[j] = im[j], im[i]
		}
	}
	for length := 2; length <= n; length <<= 1 {
		ang := -2 * math.Pi / float64(length)
		wRe, wIm := math.Cos(ang), math.Sin(ang)
		for i := 0; i < n; i += length {
			curRe, curIm := 1.0, 0.0
			for j := 0; j < length/2; j++ {
				uRe, uIm := re[i+j], im[i+j]
				vRe := re[i+j+length/2]*curRe - im[i+j+length/2]*curIm
				vIm := re[i+j+length/2]*curIm + im[i+j+length/2]*curRe
				re[i+j], im[i+j] = uRe+vRe, uIm+vIm
				re[i+j+length/2], im[i+j+length/2] = uRe-vRe, uIm-vIm
				curRe, curIm = curRe*wRe-curIm*wIm, curRe*wIm+curIm*wRe
			}
		}
	}
}

// hann is the window applied before the transform: without it, a frame's
// abrupt edges smear energy across every bin and the bars turn to mush.
func hann(n int) []float64 {
	w := make([]float64, n)
	for i := range w {
		w[i] = 0.5 * (1 - math.Cos(2*math.Pi*float64(i)/float64(n-1)))
	}
	return w
}

// Analyzer turns frames of samples into band levels between 0 and 1.
type Analyzer struct {
	window []float64
	re, im []float64
	edges  []int     // bin index where each band starts, plus the end
	levels []float64 // smoothed output, reused between frames
	powers []float64 // this frame's raw band energy
	peak   float64   // recent loudest band, for automatic gain
	rate   int
}

// FrameSize is how many samples one analysis frame holds. At 22050 Hz that
// is ~46ms: fast enough to feel live, long enough for bass resolution.
const FrameSize = 1024

// NewAnalyzer builds an analyzer for a sample rate and a number of bands.
// Bands are spaced logarithmically between 40 Hz and 16 kHz, because that is
// how hearing works: a linear split puts almost every bar in the treble,
// where music has little to show.
func NewAnalyzer(rate, bands int) *Analyzer {
	if bands < 4 {
		bands = 4
	}
	a := &Analyzer{
		window: hann(FrameSize),
		re:     make([]float64, FrameSize),
		im:     make([]float64, FrameSize),
		levels: make([]float64, bands),
		powers: make([]float64, bands),
		rate:   rate,
	}
	const lowHz, highHz = 40.0, 16000.0
	nyquistBin := FrameSize / 2
	a.edges = make([]int, bands+1)
	for i := 0; i <= bands; i++ {
		hz := lowHz * math.Pow(highHz/lowHz, float64(i)/float64(bands))
		bin := int(hz * float64(FrameSize) / float64(rate))
		if bin > nyquistBin {
			bin = nyquistBin
		}
		// Every band must own at least one bin, or the low bars stay dead.
		if i > 0 && bin <= a.edges[i-1] {
			bin = a.edges[i-1] + 1
		}
		if bin > nyquistBin {
			bin = nyquistBin
		}
		a.edges[i] = bin
	}
	return a
}

// Bands is how many bars the analyzer produces.
func (a *Analyzer) Bands() int { return len(a.levels) }

// Feed analyzes one frame of mono samples (-1..1) and returns the smoothed
// band levels. The slice is reused, so copy it to keep it. Levels rise
// instantly and fall gently, which is what makes bars look like music
// rather than noise.
func (a *Analyzer) Feed(samples []float64) []float64 {
	n := min(len(samples), FrameSize)
	for i := 0; i < n; i++ {
		a.re[i] = samples[i] * a.window[i]
		a.im[i] = 0
	}
	for i := n; i < FrameSize; i++ {
		a.re[i], a.im[i] = 0, 0
	}
	fft(a.re, a.im)

	// Band energy, with the high bands lifted: music carries far less
	// energy up there, and without this the right-hand bars never move.
	for b := range a.powers {
		lo, hi := a.edges[b], a.edges[b+1]
		if hi <= lo {
			hi = lo + 1
		}
		var sum float64
		for i := lo; i < hi && i < FrameSize/2; i++ {
			mag := math.Hypot(a.re[i], a.im[i]) / float64(FrameSize/2)
			sum += mag * mag
		}
		lift := 1 + 1.5*float64(b)/float64(len(a.powers))
		a.powers[b] = math.Sqrt(sum/float64(hi-lo)) * lift
	}

	// Automatic gain: scale against the loudest band heard recently, so a
	// quietly mastered song fills the display as well as a loud one. The
	// peak decays slowly, so a sudden loud moment doesn't flatten
	// everything that follows.
	frameMax := 0.0
	for _, p := range a.powers {
		frameMax = math.Max(frameMax, p)
	}
	a.peak = math.Max(a.peak*peakDecay, frameMax)
	if a.peak < minPeak {
		a.peak = minPeak
	}

	const attack, decay = 0.55, 0.12
	for b := range a.levels {
		// The curve gives quiet detail room to show without letting loud
		// bands saturate.
		level := math.Pow(math.Min(1, a.powers[b]/a.peak), 0.6)
		rate := decay
		if level > a.levels[b] {
			rate = attack
		}
		a.levels[b] += (level - a.levels[b]) * rate
	}
	return a.levels
}

// peakDecay and minPeak control the automatic gain: how fast the reference
// level falls, and the quietest reference worth scaling against (below it,
// the track is silence and the bars should stay down).
const (
	peakDecay = 0.997
	// Measured across real songs, a loud band sits around 0.03-0.11. A
	// floor near the lower end keeps quiet passages looking quiet instead
	// of amplifying them into a full display.
	minPeak = 0.02
)

// Silence fades every bar towards zero, for a paused or finished track.
func (a *Analyzer) Silence() []float64 {
	for b := range a.levels {
		a.levels[b] *= 0.8
		if a.levels[b] < 0.01 {
			a.levels[b] = 0
		}
	}
	return a.levels
}
