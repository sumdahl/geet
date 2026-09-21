package player

import (
	"context"
	"os/exec"
	"sync"
	"syscall"
	"time"
)

// ffplayEngine is the fallback for machines without mpv. ffplay ships with
// ffmpeg, which geet already requires, so playback always works — but it
// has no control channel: pausing means stopping the process, and the
// position is a stopwatch rather than the truth. Lyrics still follow,
// within a fraction of a second.
type ffplayEngine struct {
	bin string

	mu      sync.Mutex
	cmd     *exec.Cmd
	path    string
	started time.Time
	offset  time.Duration // position when paused
	playing bool
	ended   bool
	dur     time.Duration
}

func newFFPlay(bin string) *ffplayEngine { return &ffplayEngine{bin: bin} }

func (e *ffplayEngine) Play(ctx context.Context, path string) error {
	e.stop()
	cmd := exec.Command(e.bin, "-nodisp", "-autoexit", "-loglevel", "quiet", path)
	if err := cmd.Start(); err != nil {
		return err
	}
	e.mu.Lock()
	e.cmd, e.path, e.started, e.offset = cmd, path, time.Now(), 0
	e.playing, e.ended, e.dur = true, false, 0
	e.mu.Unlock()

	go func() {
		_ = cmd.Wait()
		e.mu.Lock()
		// Only the current process ending means the song ended; an older
		// one exiting after a track change must not touch the state.
		if e.cmd == cmd {
			e.playing = false
			e.ended = true
		}
		e.mu.Unlock()
	}()
	return nil
}

// TogglePause suspends the process. SIGSTOP silences ffplay instantly and
// SIGCONT resumes it where it stopped, which is as close to a pause as this
// fallback gets.
func (e *ffplayEngine) TogglePause() (bool, error) {
	e.mu.Lock()
	defer e.mu.Unlock()
	if e.cmd == nil || e.cmd.Process == nil {
		return false, nil
	}
	if e.playing {
		if err := e.cmd.Process.Signal(syscall.SIGSTOP); err != nil {
			return true, err
		}
		e.offset += time.Since(e.started)
		e.playing = false
		return false, nil
	}
	if err := e.cmd.Process.Signal(syscall.SIGCONT); err != nil {
		return false, err
	}
	e.started = time.Now()
	e.playing = true
	return true, nil
}

func (e *ffplayEngine) Seek(time.Duration) error { return ErrNotSeekable }

// SetDuration lets the caller tell the engine how long the track is, since
// ffplay never says. mpv reports it itself.
func (e *ffplayEngine) SetDuration(d time.Duration) {
	e.mu.Lock()
	e.dur = d
	e.mu.Unlock()
}

func (e *ffplayEngine) Status() Status {
	e.mu.Lock()
	defer e.mu.Unlock()
	pos := e.offset
	if e.playing {
		pos += time.Since(e.started)
	}
	return Status{
		Path:     e.path,
		Playing:  e.playing,
		Position: pos,
		Duration: e.dur,
		Ended:    e.ended,
		Seekable: false,
	}
}

func (e *ffplayEngine) Close() error {
	e.stop()
	return nil
}

func (e *ffplayEngine) stop() {
	e.mu.Lock()
	cmd := e.cmd
	e.cmd, e.playing = nil, false
	e.mu.Unlock()
	if cmd == nil || cmd.Process == nil {
		return
	}
	// A stopped process ignores SIGTERM until it runs again.
	_ = cmd.Process.Signal(syscall.SIGCONT)
	_ = cmd.Process.Kill()
}
