package tui

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/sumdahl/geet/internal/lyrics"
	"github.com/sumdahl/geet/internal/player"
	"github.com/sumdahl/geet/internal/spotify"
)

// stillEngine is a player that reports a fixed position and does nothing.
type stillEngine struct{ status player.Status }

func (e *stillEngine) Play(context.Context, string) error { return nil }
func (e *stillEngine) TogglePause() (bool, error)         { return true, nil }
func (e *stillEngine) Seek(time.Duration) error           { return nil }
func (e *stillEngine) Status() player.Status              { return e.status }
func (e *stillEngine) Close() error                       { return nil }

func testModel(t *testing.T, lrc string, at time.Duration) *Model {
	t.Helper()
	eng := &stillEngine{status: player.Status{Playing: true, Position: at, Duration: 4 * time.Minute}}
	m := New(context.Background(), Options{
		Items: []player.Item{{
			Path:  "/music/song.opus",
			Track: spotify.Track{Title: "Song", Artists: []string{"Someone"}},
		}},
		Engine:     eng,
		Visualizer: true,
	})
	m.width, m.height = 100, 28
	m.status = eng.status
	m.lyr = ParseLRCForTest(lrc)
	m.lyrState = lyricsReady
	m.showLyr = true
	m.levels = []float64{0.2, 0.9, 0.4, 0.7, 0.1, 0.5}
	return m
}

// ParseLRCForTest keeps the test readable without exporting anything from
// the lyrics package that only tests need.
func ParseLRCForTest(s string) lyrics.Lyrics { return lyrics.ParseLRC(s) }

// Before the first synced line arrives there is no current line, which used
// to be an index of -1 into the lyrics — and a panic in the middle of a
// song.
func TestViewBeforeFirstLyricLine(t *testing.T) {
	m := testModel(t, "[00:30.00]first line\n[01:00.00]second line\n", 2*time.Second)
	out := m.View()
	if !strings.Contains(out, "first line") {
		t.Errorf("the lyrics are missing from the screen:\n%s", out)
	}
}

func TestViewWithComplexScriptLyrics(t *testing.T) {
	m := testModel(t, devanagari+"\n"+devanagari+"\n", 30*time.Second)
	out := m.View()
	if !strings.Contains(out, "no timings") {
		t.Errorf("unsynced lyrics should say so:\n%s", out)
	}
	// Devanagari must get the full width: nothing may share a line with it,
	// since a terminal can draw those clusters wider than Unicode counts
	// and the two would then overlap.
	for _, line := range strings.Split(out, "\n") {
		plain := stripANSI(line)
		if !strings.Contains(plain, "सम्झ") {
			continue
		}
		if strings.ContainsAny(plain, "▁▂▃▄▅▆▇█") {
			t.Errorf("spectrum and complex-script lyrics share a line: %q", plain)
		}
		if col := strings.Index(plain, "सम्झ"); col > 8 {
			t.Errorf("complex-script lyrics start at column %d, expected the full width", col)
		}
	}
}

// Latin lyrics keep the side-by-side layout, which fits more on screen:
// the words sit in the right-hand column, not at the left margin.
func TestViewKeepsColumnsForLatinLyrics(t *testing.T) {
	m := testModel(t, "[00:10.00]hello there\n[00:20.00]second line\n", 15*time.Second)
	out := m.View()
	col := lyricColumn(out, "hello there")
	if col < 0 {
		t.Fatalf("the lyrics are missing from the screen:\n%s", out)
	}
	if col < m.width/3 {
		t.Errorf("lyrics start at column %d, expected the right-hand column:\n%s", col, out)
	}
}

// lyricColumn is where a line of words begins on screen, or -1.
func lyricColumn(view, text string) int {
	for _, line := range strings.Split(view, "\n") {
		if i := strings.Index(stripANSI(line), text); i >= 0 {
			return i
		}
	}
	return -1
}

// stripANSI removes styling so a column count means what it looks like.
func stripANSI(s string) string {
	var b strings.Builder
	for i := 0; i < len(s); i++ {
		if s[i] == 0x1b {
			for i < len(s) && s[i] != 'm' {
				i++
			}
			continue
		}
		b.WriteByte(s[i])
	}
	return b.String()
}

// A narrow window must still render something sensible rather than panic.
func TestViewTinyWindow(t *testing.T) {
	m := testModel(t, "[00:10.00]hello\n", 15*time.Second)
	for _, size := range [][2]int{{10, 5}, {30, 9}, {60, 12}, {200, 50}} {
		m.width, m.height = size[0], size[1]
		if out := m.View(); out == "" {
			t.Errorf("%dx%d rendered nothing", size[0], size[1])
		}
	}
}

// Pressing "next" on the last song must never close the player. It used to
// call the same path a finished queue does, so a song played from the
// Omarchy panel (a queue of one) quit — and the terminal it ran in closed.
func TestNextOnLastSongKeepsPlaying(t *testing.T) {
	m := testModel(t, "[00:10.00]hello\n", 15*time.Second)
	if len(m.items) != 1 {
		t.Fatalf("this test wants a single-song queue, got %d", len(m.items))
	}
	if cmd := m.jump(1); cmd != nil {
		t.Error("next at the end of the queue should do nothing, not run a command")
	}
	if m.quitting {
		t.Error("the player quit on next")
	}
	if !strings.Contains(m.note, "nothing else queued") {
		t.Errorf("the listener was not told why nothing happened: %q", m.note)
	}

	m.note = ""
	if cmd := m.jump(-1); cmd != nil {
		t.Error("previous at the first song should do nothing")
	}
	if m.quitting {
		t.Error("the player quit on previous")
	}
	if !strings.Contains(m.note, "first song") {
		t.Errorf("note = %q", m.note)
	}
}

// A queue that ends on its own does stop the player: that is what a
// finished queue means.
func TestQueueEndingStopsThePlayer(t *testing.T) {
	m := testModel(t, "", 0)
	if cmd := m.skip(1); cmd == nil {
		t.Fatal("a finished queue should quit")
	}
	if !m.quitting {
		t.Error("the player should be quitting after its last song ended")
	}
}
