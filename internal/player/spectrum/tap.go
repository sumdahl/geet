package spectrum

import (
	"bufio"
	"context"
	"encoding/binary"
	"io"
	"os/exec"
	"time"
)

// SampleRate is what the tap asks ffmpeg for. 22050 Hz covers everything a
// spectrum display can show and halves the work of the full rate.
const SampleRate = 22050

// Tap decodes a file to raw mono PCM with ffmpeg and hands frames of
// samples to an analyzer. Nothing here plays audio: the player owns that,
// and this reads the same file a second time. Decoding is cheap next to
// being wrong about what is currently audible.
type Tap struct {
	FFmpeg string // binary name or path
	Bands  int
}

// Frames starts ffmpeg at position `from` and returns a channel of band
// levels, one per ~46ms of audio, plus a stop function. The channel closes
// when the file ends, ffmpeg fails, or the context is done. A failure is
// silent: a visualizer is decoration, and losing it must never interrupt
// playback.
func (t Tap) Frames(ctx context.Context, path string, from time.Duration) (<-chan []float64, func()) {
	out := make(chan []float64, 4)
	ctx, cancel := context.WithCancel(ctx)

	bin := t.FFmpeg
	if bin == "" {
		bin = "ffmpeg"
	}
	args := []string{"-hide_banner", "-loglevel", "error", "-nostdin"}
	if from > 0 {
		args = append(args, "-ss", formatSeconds(from))
	}
	args = append(args,
		// Real time, not as fast as possible: the bars must match what the
		// ear hears, and an unthrottled ffmpeg would race minutes ahead.
		// -re reads the *input* at its native rate, so it belongs before
		// -i; after it, ffmpeg refuses to start at all.
		"-re",
		"-i", path,
		"-vn",
		"-f", "s16le", "-acodec", "pcm_s16le",
		"-ac", "1", "-ar", itoa(SampleRate),
		"-",
	)
	cmd := exec.CommandContext(ctx, bin, args...)
	pipe, err := cmd.StdoutPipe()
	if err != nil {
		close(out)
		cancel()
		return out, func() {}
	}
	if err := cmd.Start(); err != nil {
		close(out)
		cancel()
		return out, func() {}
	}

	go func() {
		defer close(out)
		defer func() {
			cancel()
			_ = cmd.Wait()
		}()
		an := NewAnalyzer(SampleRate, t.Bands)
		r := bufio.NewReaderSize(pipe, FrameSize*4)
		raw := make([]byte, FrameSize*2) // 16-bit mono
		buf := make([]float64, FrameSize)
		for {
			if _, err := io.ReadFull(r, raw); err != nil {
				return
			}
			for i := range buf {
				buf[i] = float64(int16(binary.LittleEndian.Uint16(raw[i*2:]))) / 32768
			}
			levels := append([]float64(nil), an.Feed(buf)...)
			select {
			case out <- levels:
			case <-ctx.Done():
				return
			default:
				// The UI is behind: drop this frame rather than let the
				// spectrum lag the music.
			}
		}
	}()

	return out, cancel
}

func formatSeconds(d time.Duration) string {
	return strconvFloat(d.Seconds())
}
