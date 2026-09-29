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

// Event is a playback change worth telling a consumer about. A front end
// with no screen of its own (the Omarchy panel) draws from these alone, so
// the decoration the TUI paints itself — the spectrum, the lyrics, the
// transient note — is reported here too.
type Event struct {
	Stage    string // track, playing, paused, position, stopped, levels, lyrics, note, saved
	Item     player.Item
	Position time.Duration
	Duration time.Duration
	Index    int // 1-based place in the queue
	Total    int
	Levels   []float64      // stage "levels": one value per spectrum band, 0..1
	Lyrics   *lyrics.Lyrics // stage "lyrics": the whole song's words, once
	Note     string         // stage "note": the line the TUI shows under the header
	Err      string         // stage "lyrics": why the lookup failed; Lyrics is then empty
}

// Control is a command from a front end that has no keyboard of its own.
// Everything but SeekTo runs the very key handler the terminal player runs,
// so the two modes cannot drift apart.
type Control struct {
	Action string // toggle, play, pause, next, prev, seek, seekto, save, visualizer, lyrics, stop
	Value  int64  // seekto: milliseconds from the start; seek: milliseconds to move
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
// skip moves through the queue when a song ends. Running off the end stops
// the player, which is what a queue finishing means.
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

// jump is the listener pressing next or previous. Unlike a song ending, it
// must never close the player: asking for a song that isn't there is not a
// reason to quit, it is a reason to say so.
func (m *Model) jump(delta int) tea.Cmd {
	next := m.idx + delta
	switch {
	case next >= len(m.items):
		if m.opts.Repeat {
			return m.startTrack(0)
		}
		m.setNote(lastSongNote(len(m.items)))
		return nil
	case next < 0:
		m.setNote("this is the first song")
		return nil
	}
	return m.startTrack(next)
}

func lastSongNote(queued int) string {
	if queued == 1 {
		return "nothing else queued — play a playlist or the trending list for more"
	}
	return "that was the last song in the queue"
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
		Index:    m.idx + 1,
		Total:    len(m.items),
	})
}

// emitLevels reports one spectrum frame. Frames arrive about twenty times a
// second, which is the rate a bar display wants and small enough to send as
// it comes rather than buffer.
func (m *Model) emitLevels(levels []float64) {
	if m.opts.OnEvent == nil {
		return
	}
	m.opts.OnEvent(Event{Stage: "levels", Levels: levels})
}

// emitLyrics reports a song's words once, when they arrive, or why they
// could not be read. A front end follows them with the position events, the
// same way the screen does.
func (m *Model) emitLyrics(l lyrics.Lyrics, lookupErr string) {
	if m.opts.OnEvent == nil || m.idx >= len(m.items) {
		return
	}
	got := l
	m.opts.OnEvent(Event{
		Stage:  "lyrics",
		Item:   m.items[m.idx],
		Index:  m.idx + 1,
		Total:  len(m.items),
		Lyrics: &got,
		Err:    lookupErr,
	})
}

func (m *Model) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
		m.width, m.height = msg.Width, msg.Height
		return m, nil

	case tea.KeyMsg:
		return m.handleKey(msg)

	case Control:
		return m.handleControl(msg)

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
		m.emitLevels(msg.levels)
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
		m.emit("saved", m.status.Position)
		return m, nil

	case lyricsMsg:
		if msg.idx != m.idx {
			return m, nil
		}
		switch {
		case errors.Is(msg.err, lyrics.ErrNotFound):
			m.lyrState = lyricsNone
			// A front end is told there are none, rather than left
			// waiting: plenty of songs have no words anywhere.
			m.emitLyrics(lyrics.Lyrics{}, "")
		case msg.err != nil:
			m.lyrState, m.lyrErr = lyricsFailed, msg.err.Error()
			// Also reported: a front end left on "looking…" can't tell
			// a timeout from a lookup still under way.
			m.emitLyrics(lyrics.Lyrics{}, m.lyrErr)
		case msg.lyr.Empty():
			m.lyrState = lyricsNone
			m.emitLyrics(lyrics.Lyrics{}, "")
		default:
			m.lyrState, m.lyr = lyricsReady, msg.lyr
			m.emitLyrics(msg.lyr, "")
		}
		return m, nil
	}
	return m, nil
}

// handleControl runs a front end's command. Everything that has a key runs
// through that key's handler, so a change to the terminal player changes
// the panel with it; only seeking to an exact position has no key, because
// no keyboard can point at a spot in a song.
func (m *Model) handleControl(c Control) (tea.Model, tea.Cmd) {
	switch c.Action {
	case "toggle":
		return m.press(" ")
	case "play":
		if m.status.Playing {
			return m, nil
		}
		return m.press(" ")
	case "pause":
		if !m.status.Playing {
			return m, nil
		}
		return m.press(" ")
	case "next":
		return m.press("n")
	case "prev":
		return m.press("p")
	case "save":
		return m.press("d")
	case "visualizer":
		return m.press("v")
	case "lyrics":
		return m.press("y")
	case "stop":
		return m.press("q")
	case "seek":
		return m, m.seek(time.Duration(c.Value) * time.Millisecond)
	case "seekto":
		return m, m.seekTo(time.Duration(c.Value) * time.Millisecond)
	}
	return m, nil
}

// press replays a key through the ordinary handler.
func (m *Model) press(key string) (tea.Model, tea.Cmd) {
	if key == " " {
		return m.handleKey(tea.KeyMsg{Type: tea.KeySpace, Runes: []rune{' '}})
	}
	return m.handleKey(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune(key)})
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
		return m, m.jump(1)
	case "p", "up", "b":
		// Restart this song first, the way every music player does: press
		// again within three seconds to go back.
		if m.status.Position > 3*time.Second {
			return m, m.restart()
		}
		return m, m.jump(-1)

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

// seekTo jumps to an exact position, which only a front end with a
// progress bar can ask for.
func (m *Model) seekTo(at time.Duration) tea.Cmd {
	if at < 0 {
		at = 0
	}
	if err := m.opts.Engine.SeekTo(at); err != nil {
		if errors.Is(err, player.ErrNotSeekable) {
			m.setNote("this player can't seek — install mpv for seeking")
		} else {
			m.setNote(err.Error())
		}
		return nil
	}
	m.status.Position = at
	if !m.showVis {
		return nil
	}
	return m.startSpectrum(m.items[m.idx].Source(), at)
}

func (m *Model) restart() tea.Cmd {
	return m.startTrack(m.idx)
}

func (m *Model) setNote(s string) {
	m.note, m.noteAt = s, time.Now()
	if m.opts.OnEvent != nil {
		m.opts.OnEvent(Event{Stage: "note", Note: s})
	}
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
