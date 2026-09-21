// Package player plays audio files. It never decodes anything itself: mpv
// does the work, ffplay stands in when mpv is missing, and Go keeps the
// state a display needs.
package player

import (
	"context"
	"errors"
	"os/exec"
	"time"
)

// ErrNoEngine means neither mpv nor ffplay is installed.
var ErrNoEngine = errors.New("no audio player found: install mpv (recommended) or ffmpeg's ffplay")

// Status is what the display asks for on every tick.
type Status struct {
	Path     string
	Playing  bool          // false while paused
	Position time.Duration // where playback is
	Duration time.Duration // 0 until known
	Ended    bool          // the file played to its end
	Seekable bool          // false on the ffplay fallback
}

// Engine is one running player process.
type Engine interface {
	// Play starts a file, replacing whatever is playing.
	Play(ctx context.Context, path string) error
	// TogglePause pauses or resumes. Returns the new playing state.
	TogglePause() (playing bool, err error)
	// Seek moves by delta (negative rewinds). Engines that cannot seek
	// return ErrNotSeekable.
	Seek(delta time.Duration) error
	// Status is a snapshot; it never blocks on the player process.
	Status() Status
	// Close stops playback and reaps the process.
	Close() error
}

// ErrNotSeekable is returned by engines without seek support.
var ErrNotSeekable = errors.New("this player cannot seek")

// Name identifies an engine for `geet doctor` and error messages.
type Name string

const (
	MPV    Name = "mpv"
	FFPlay Name = "ffplay"
	Auto   Name = "auto"
)

// Open starts the engine the config asks for. Auto prefers mpv, because it
// can seek, pause and report an exact position; ffplay can only play.
func Open(ctx context.Context, want Name, mpvBin, ffplayBin string) (Engine, Name, error) {
	if mpvBin == "" {
		mpvBin = "mpv"
	}
	if ffplayBin == "" {
		ffplayBin = "ffplay"
	}
	tryMPV := want == Auto || want == MPV
	tryFFPlay := want == Auto || want == FFPlay

	if tryMPV && have(mpvBin) {
		e, err := newMPV(ctx, mpvBin)
		if err == nil {
			return e, MPV, nil
		}
		if want == MPV {
			return nil, MPV, err
		}
	}
	if tryFFPlay && have(ffplayBin) {
		return newFFPlay(ffplayBin), FFPlay, nil
	}
	return nil, "", ErrNoEngine
}

func have(bin string) bool {
	_, err := exec.LookPath(bin)
	return err == nil
}
