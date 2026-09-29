package tui

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/sumdahl/geet/internal/lyrics"
	"github.com/sumdahl/geet/internal/player"
)

// fakeEngine records what a control did, so the panel's commands are tested
// without audio.
type fakeEngine struct {
	playing  bool
	seeked   time.Duration
	seekedTo time.Duration
	seeks    int
}

func (f *fakeEngine) Play(context.Context, string) error { f.playing = true; return nil }
func (f *fakeEngine) TogglePause() (bool, error)         { f.playing = !f.playing; return f.playing, nil }
func (f *fakeEngine) Seek(d time.Duration) error         { f.seeked, f.seeks = d, f.seeks+1; return nil }
func (f *fakeEngine) SeekTo(p time.Duration) error       { f.seekedTo, f.seeks = p, f.seeks+1; return nil }
func (f *fakeEngine) Status() player.Status              { return player.Status{Playing: f.playing} }
func (f *fakeEngine) Close() error                       { return nil }

func newTestModel(e *fakeEngine) *Model {
	m := New(context.Background(), Options{
		Items:  []player.Item{{Path: "/music/a.opus"}, {Path: "/music/b.opus"}},
		Engine: e,
	})
	m.status = e.Status()
	return m
}

func TestControlsDriveTheEngine(t *testing.T) {
	t.Parallel()
	e := &fakeEngine{playing: true}
	m := newTestModel(e)

	m.handleControl(Control{Action: "pause"})
	if e.playing {
		t.Fatal("pause did not pause")
	}
	// Already paused: pause again must not resume it.
	m.status = e.Status()
	m.handleControl(Control{Action: "pause"})
	if e.playing {
		t.Fatal("a second pause resumed playback")
	}
	m.handleControl(Control{Action: "play"})
	if !e.playing {
		t.Fatal("play did not resume")
	}

	m.status = player.Status{Playing: true, Position: 30 * time.Second}
	m.handleControl(Control{Action: "seekto", Value: 10_000})
	if e.seekedTo != 10*time.Second {
		t.Fatalf("seekto 10s landed at %v", e.seekedTo)
	}
	m.handleControl(Control{Action: "seek", Value: 5_000})
	if e.seeked != 5*time.Second {
		t.Fatalf("seek moved %v, want 5s", e.seeked)
	}

	// next walks the queue the same way the "n" key does.
	m.handleControl(Control{Action: "next"})
	if m.idx != 1 {
		t.Fatalf("next left the player on track %d", m.idx)
	}
}

// A song nobody has written down must be reported, not left pending: the
// panel says "no words for this one" instead of "looking…" forever.
func TestMissingLyricsAreReported(t *testing.T) {
	t.Parallel()
	var got []Event
	m := New(context.Background(), Options{
		Items:   []player.Item{{Path: "/music/a.opus"}},
		Engine:  &fakeEngine{playing: true},
		OnEvent: func(ev Event) { got = append(got, ev) },
	})
	m.Update(lyricsMsg{idx: 0, err: lyrics.ErrNotFound})
	if len(got) != 1 || got[0].Stage != "lyrics" {
		t.Fatalf("events = %+v, want one lyrics event", got)
	}
	if got[0].Lyrics == nil || len(got[0].Lyrics.Lines) != 0 {
		t.Fatalf("lyrics = %+v, want an empty set", got[0].Lyrics)
	}
}

func TestControlsReportToAFrontEnd(t *testing.T) {
	t.Parallel()
	var stages []string
	e := &fakeEngine{playing: true}
	m := New(context.Background(), Options{
		Items:   []player.Item{{Path: "/music/a.opus"}},
		Engine:  e,
		OnEvent: func(ev Event) { stages = append(stages, ev.Stage) },
	})
	m.status = e.Status()
	m.handleControl(Control{Action: "toggle"})
	m.emitLevels([]float64{0.1, 0.2})
	if len(stages) < 2 || stages[0] != "paused" || stages[len(stages)-1] != "levels" {
		t.Fatalf("stages = %v, want paused … levels", stages)
	}
}

// A failed lookup is reported too, with why, so the panel can stop waiting.
func TestFailedLyricsAreReported(t *testing.T) {
	t.Parallel()
	var got []Event
	m := New(context.Background(), Options{
		Items:   []player.Item{{Path: "/music/a.opus"}},
		Engine:  &fakeEngine{playing: true},
		OnEvent: func(ev Event) { got = append(got, ev) },
	})
	m.Update(lyricsMsg{idx: 0, err: errors.New("lrclib: timeout")})
	if len(got) != 1 || got[0].Stage != "lyrics" || got[0].Err != "lrclib: timeout" {
		t.Fatalf("events = %+v, want one lyrics event with the error", got)
	}
}
