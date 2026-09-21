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
	// Streamer plays songs that are not downloaded. Without it, such a
	// song is skipped.
	Streamer *player.Streamer
	// Save keeps a streaming song: it downloads the track and returns the
	// file. It runs off the display goroutine and may take a while.
	Save func(context.Context, spotify.Track) (string, error)
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
	finding  bool   // resolving a stream for the current song
	saveErr  string // why the last "keep this song" failed

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
type streamMsg struct {
	idx    int
	stream player.Stream
	err    error
}
type savedMsg struct {
	idx  int
	path string
	err  error
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
	m.saveErr = ""

	it := m.items[i]

	// Not downloaded and not resolved yet: find it on YouTube first. The
	// song plays a couple of seconds later, rather than after a download.
	if !it.Downloaded() && it.Stream.Direct == "" {
		if m.opts.Streamer == nil {
			m.setNote(it.Name() + " isn't downloaded, and streaming is off")
			return m.skip(1)
		}
		m.finding = true
		cmds := []tea.Cmd{m.resolveStream(i, it.Track)}
		if it.Track.Title != "" && m.showLyr {
			m.lyrState = lyricsLoading
			cmds = append(cmds, m.fetchLyrics(i, it.Track))
		}
		return tea.Batch(cmds...)
	}

	return m.playCurrent()
}

// playCurrent starts whatever the current item points at, a file or a
// stream, and kicks off the work that decorates it.
func (m *Model) playCurrent() tea.Cmd {
	m.finding = false
	it := m.items[m.idx]
	if err := m.opts.Engine.Play(m.ctx, it.Source()); err != nil {
		m.setNote("could not play " + it.Name() + ": " + err.Error())
		return m.skip(1)
	}
	m.emit("track", 0)

	var cmds []tea.Cmd
	if c := m.readTags(m.idx, it); c != nil {
		cmds = append(cmds, c)
	}
	if m.showVis {
		cmds = append(cmds, m.startSpectrum(it.Source(), 0))
	}
	if it.Track.Title != "" && m.showLyr && m.lyrState == lyricsIdle {
		m.lyrState = lyricsLoading
		cmds = append(cmds, m.fetchLyrics(m.idx, it.Track))
	}
	// Find the next song while this one plays, so the queue runs on
	// without a pause between songs.
	if c := m.prefetchNext(); c != nil {
		cmds = append(cmds, c)
	}
	return tea.Batch(cmds...)
}

func (m *Model) resolveStream(i int, t spotify.Track) tea.Cmd {
	streamer := m.opts.Streamer
	if streamer == nil {
		return nil
	}
	return func() tea.Msg {
		st, err := streamer.Resolve(m.ctx, t)
		return streamMsg{idx: i, stream: st, err: err}
	}
}

// prefetchNext resolves the next song's stream ahead of time. A failure is
// silent here: it is reported when that song actually comes up.
func (m *Model) prefetchNext() tea.Cmd {
	next := m.idx + 1
	if next >= len(m.items) || m.opts.Streamer == nil {
		return nil
	}
	it := m.items[next]
	if it.Downloaded() || it.Stream.Direct != "" || it.Track.Title == "" {
		return nil
	}
	return m.resolveStream(next, it.Track)
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

	case streamMsg:
		if msg.idx >= len(m.items) {
			return m, nil
		}
		if msg.err != nil {
			if msg.idx != m.idx {
				return m, nil // a prefetch: report it when the song comes up
			}
			m.finding = false
			m.setNote("couldn't play " + m.items[msg.idx].Name() + ": " + msg.err.Error())
			return m, m.skip(1)
		}
		m.items[msg.idx].Stream = msg.stream
		if msg.idx != m.idx {
			return m, nil // prefetched, ready for later
		}
		if msg.stream.Warning != "" {
			m.setNote(msg.stream.Warning)
		}
		return m, m.playCurrent()

	case savedMsg:
		if msg.idx >= len(m.items) {
			return m, nil
		}
		m.items[msg.idx].Saving = false
		if msg.err != nil {
			m.saveErr = msg.err.Error()
			m.setNote("couldn't save " + m.items[msg.idx].Name() + ": " + msg.err.Error())
			return m, nil
		}
		m.items[msg.idx].Path = msg.path
		m.setNote("saved " + m.items[msg.idx].Name())
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
				return m, m.startSpectrum(m.items[m.idx].Source(), m.status.Position)
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

	case "d", "s":
		return m, m.saveCurrent()

	case "v":
		m.showVis = !m.showVis
		if m.showVis {
			return m, m.startSpectrum(m.items[m.idx].Source(), m.status.Position)
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

// saveCurrent downloads the song that is playing, so it stays in the
// library. Playback carries on from the stream while it downloads.
func (m *Model) saveCurrent() tea.Cmd {
	it := m.items[m.idx]
	switch {
	case m.opts.Save == nil:
		m.setNote("saving isn't available here")
		return nil
	case it.Downloaded():
		m.setNote("already in your library")
		return nil
	case it.Saving:
		m.setNote("already saving…")
		return nil
	case it.Track.Title == "":
		m.setNote("nothing to save: this song has no details")
		return nil
	}
	m.items[m.idx].Saving = true
	m.setNote("saving " + it.Name() + "…")
	idx, track, save := m.idx, it.Track, m.opts.Save
	return func() tea.Msg {
		path, err := save(m.ctx, track)
		return savedMsg{idx: idx, path: path, err: err}
	}
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
	return m.startSpectrum(m.items[m.idx].Source(), at)
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
