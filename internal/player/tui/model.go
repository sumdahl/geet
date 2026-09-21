// Package tui is the full-screen player: what is playing, how far along it
// is, a spectrum of the sound, and the words when anyone has written them
// down.
package tui

import (
	"context"
	"errors"
	"time"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/sumdahl/geet/internal/lyrics"
	"github.com/sumdahl/geet/internal/player"
	"github.com/sumdahl/geet/internal/player/spectrum"
	"github.com/sumdahl/geet/internal/spotify"
)

// Options is everything the player screen needs from its caller.
type Options struct {
	Items      []player.Item
	Engine     player.Engine
	EngineName player.Name
	Lyrics     *lyrics.Client // nil turns lyrics off
	FFmpeg     string
	FFprobe    string
	Visualizer bool
	Repeat     bool
	// OnEvent reports playback for `--json`. It may be nil.
	OnEvent func(Event)
}

// Event is a playback change worth telling a consumer about.
type Event struct {
	Stage    string // track, playing, paused, position, stopped
	Item     player.Item
	Position time.Duration
	Duration time.Duration
}

// lyricsState is what the lyrics pane is doing. "None" is an ordinary
// resting state, not an error: plenty of songs have no lyrics anywhere.
type lyricsState int

const (
	lyricsIdle lyricsState = iota
	lyricsLoading
	lyricsReady
	lyricsNone
	lyricsFailed
)

const (
	tickEvery  = 200 * time.Millisecond
	seekStep   = 5 * time.Second
	seekBigger = 30 * time.Second
)

// Model is the bubbletea model for the player screen.
type Model struct {
	opts  Options
	ctx   context.Context
	items []player.Item
	idx   int

	status   player.Status
	levels   []float64
	frames   <-chan []float64
	stopTap  func()
	tapGen   int
	bands    int
	showVis  bool
	showLyr  bool
	lyrState lyricsState
	lyr      lyrics.Lyrics
	lyrErr   string
	note     string // a transient line under the header
	noteAt   time.Time

	width, height int
	quitting      bool
	lastPos       time.Duration
}

// New builds the model. The first track starts when bubbletea runs Init.
func New(ctx context.Context, opts Options) *Model {
	return &Model{
		opts:    opts,
		ctx:     ctx,
		items:   opts.Items,
		showVis: opts.Visualizer,
		showLyr: opts.Lyrics != nil,
		bands:   24,
	}
}

func (m *Model) Init() tea.Cmd {
	return tea.Batch(m.startTrack(0), tick())
}

type tickMsg time.Time

// Each spectrum tap gets a generation number. A seek or a pause starts a
// new tap, and the old one's last messages can still be in flight: without
// the number, a stale "channel closed" would cancel the new tap's
// subscription and freeze the bars.
type frameMsg struct {
	gen    int
	levels []float64
}
type framesDoneMsg struct{ gen int }
type tagsMsg struct {
	idx   int
	track spotify.Track
}
type lyricsMsg struct {
	idx int
	lyr lyrics.Lyrics
	err error
}

func tick() tea.Cmd {
	return tea.Tick(tickEvery, func(t time.Time) tea.Msg { return tickMsg(t) })
}

// waitForFrame turns the spectrum channel into messages, one at a time, so
// the model never blocks on audio analysis.
func waitForFrame(ch <-chan []float64, gen int) tea.Cmd {
	if ch == nil {
		return nil
	}
	return func() tea.Msg {
		levels, ok := <-ch
		if !ok {
			return framesDoneMsg{gen: gen}
		}
		return frameMsg{gen: gen, levels: levels}
	}
}

// startTrack plays item i and kicks off the work that decorates it: tags,
// lyrics and the spectrum tap.
func (m *Model) startTrack(i int) tea.Cmd {
	if i < 0 || i >= len(m.items) {
		m.quitting = true
		return tea.Quit
	}
	m.idx = i
	m.stopSpectrum()
	m.lyr = lyrics.Lyrics{}
	m.lyrErr = ""
	m.lyrState = lyricsIdle
	m.levels = nil

	it := m.items[i]
	if err := m.opts.Engine.Play(m.ctx, it.Path); err != nil {
		m.note = "could not play " + it.Name() + ": " + err.Error()
		m.noteAt = time.Now()
		return m.skip(1)
	}
	m.emit("track", 0)

	cmds := []tea.Cmd{m.readTags(i, it)}
	if m.showVis {
		cmds = append(cmds, m.startSpectrum(it.Path, 0))
	}
	if it.Track.Title != "" && m.showLyr {
		m.lyrState = lyricsLoading
		cmds = append(cmds, m.fetchLyrics(i, it.Track))
	}
	return tea.Batch(cmds...)
}

func (m *Model) readTags(i int, it player.Item) tea.Cmd {
	if it.Track.Title != "" {
		return nil
	}
	ffprobe := m.opts.FFprobe
	return func() tea.Msg {
		return tagsMsg{idx: i, track: player.ReadTags(m.ctx, ffprobe, it.Path)}
	}
}

func (m *Model) fetchLyrics(i int, t spotify.Track) tea.Cmd {
	client := m.opts.Lyrics
	if client == nil {
		return nil
	}
	return func() tea.Msg {
		l, err := client.Fetch(m.ctx, t)
		return lyricsMsg{idx: i, lyr: l, err: err}
	}
}

func (m *Model) startSpectrum(path string, from time.Duration) tea.Cmd {
	m.stopSpectrum()
	m.tapGen++
	tap := spectrum.Tap{FFmpeg: m.opts.FFmpeg, Bands: m.bands}
	frames, stop := tap.Frames(m.ctx, path, from)
	m.frames, m.stopTap = frames, stop
	return waitForFrame(frames, m.tapGen)
}

func (m *Model) stopSpectrum() {
	if m.stopTap != nil {
		m.stopTap()
		m.stopTap = nil
	}
	m.frames = nil
}

// skip moves by delta tracks, stopping at the end unless repeating.
func (m *Model) skip(delta int) tea.Cmd {
	next := m.idx + delta
	if next >= len(m.items) {
		if !m.opts.Repeat {
			m.quitting = true
			m.emit("stopped", m.status.Position)
			return tea.Quit
		}
		next = 0
	}
	if next < 0 {
		next = 0
	}
	return m.startTrack(next)
}

func (m *Model) emit(stage string, pos time.Duration) {
	if m.opts.OnEvent == nil || m.idx >= len(m.items) {
		return
	}
	m.opts.OnEvent(Event{
		Stage:    stage,
		Item:     m.items[m.idx],
		Position: pos,
		Duration: m.status.Duration,
	})
}

func (m *Model) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
		m.width, m.height = msg.Width, msg.Height
		return m, nil

	case tea.KeyMsg:
		return m.handleKey(msg)

	case tickMsg:
		m.status = m.opts.Engine.Status()
		if m.status.Duration == 0 && m.items[m.idx].Track.Duration > 0 {
			m.status.Duration = m.items[m.idx].Track.Duration
		}
		// Report position about twice a second, not on every tick.
		if m.status.Position/500e6 != m.lastPos/500e6 {
			m.emit("position", m.status.Position)
			m.lastPos = m.status.Position
		}
		if m.status.Ended {
			return m, tea.Batch(m.skip(1), tick())
		}
		if !m.status.Playing && m.levels != nil {
			m.levels = fade(m.levels)
		}
		return m, tick()

	case frameMsg:
		if msg.gen != m.tapGen {
			return m, nil // an older tap, already replaced
		}
		m.levels = msg.levels
		return m, waitForFrame(m.frames, m.tapGen)

	case framesDoneMsg:
		if msg.gen == m.tapGen {
			m.frames = nil
		}
		return m, nil

	case tagsMsg:
		if msg.idx == m.idx {
			m.items[msg.idx].Track = msg.track
			if m.showLyr && m.lyrState == lyricsIdle {
				m.lyrState = lyricsLoading
				return m, m.fetchLyrics(msg.idx, msg.track)
			}
		}
		return m, nil

	case lyricsMsg:
		if msg.idx != m.idx {
			return m, nil
		}
		switch {
		case errors.Is(msg.err, lyrics.ErrNotFound):
			m.lyrState = lyricsNone
		case msg.err != nil:
			m.lyrState, m.lyrErr = lyricsFailed, msg.err.Error()
		case msg.lyr.Empty():
			m.lyrState = lyricsNone
		default:
			m.lyrState, m.lyr = lyricsReady, msg.lyr
		}
		return m, nil
	}
	return m, nil
}

func (m *Model) handleKey(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	switch msg.String() {
	case "q", "esc", "ctrl+c":
		m.quitting = true
		m.emit("stopped", m.status.Position)
		return m, tea.Quit

	case " ", "k":
		playing, err := m.opts.Engine.TogglePause()
		if err != nil {
			m.setNote(err.Error())
			return m, nil
		}
		if playing {
			m.emit("playing", m.status.Position)
			// The tap stopped with the pause; restart it where we are.
			if m.showVis {
				return m, m.startSpectrum(m.items[m.idx].Path, m.status.Position)
			}
		} else {
			m.emit("paused", m.status.Position)
			m.stopSpectrum()
		}
		return m, nil

	case "right", "l":
		return m, m.seek(seekStep)
	case "left", "h":
		return m, m.seek(-seekStep)
	case "shift+right", "L":
		return m, m.seek(seekBigger)
	case "shift+left", "H":
		return m, m.seek(-seekBigger)

	case "n", "down", "j":
		return m, m.skip(1)
	case "p", "up":
		// Restart this track first, the way every music player does.
		if m.status.Position > 3*time.Second {
			return m, m.restart()
		}
		return m, m.skip(-1)

	case "v":
		m.showVis = !m.showVis
		if m.showVis {
			return m, m.startSpectrum(m.items[m.idx].Path, m.status.Position)
		}
		m.stopSpectrum()
		m.levels = nil
		return m, nil

	case "y":
		m.showLyr = !m.showLyr
		if m.showLyr && m.lyrState == lyricsIdle {
			m.lyrState = lyricsLoading
			return m, m.fetchLyrics(m.idx, m.items[m.idx].Track)
		}
		return m, nil
	}
	return m, nil
}

func (m *Model) seek(delta time.Duration) tea.Cmd {
	if err := m.opts.Engine.Seek(delta); err != nil {
		if errors.Is(err, player.ErrNotSeekable) {
			m.setNote("this player can't seek — install mpv for seeking")
		} else {
			m.setNote(err.Error())
		}
		return nil
	}
	if !m.showVis {
		return nil
	}
	// The spectrum reads the file separately, so it has to jump too.
	at := m.status.Position + delta
	if at < 0 {
		at = 0
	}
	return m.startSpectrum(m.items[m.idx].Path, at)
}

func (m *Model) restart() tea.Cmd {
	return m.startTrack(m.idx)
}

func (m *Model) setNote(s string) {
	m.note, m.noteAt = s, time.Now()
}

// Close stops the spectrum tap. The engine belongs to the caller.
func (m *Model) Close() { m.stopSpectrum() }

func fade(levels []float64) []float64 {
	out := make([]float64, len(levels))
	for i, l := range levels {
		out[i] = l * 0.85
	}
	return out
}
