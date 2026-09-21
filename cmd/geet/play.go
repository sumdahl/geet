package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"strings"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/sumdahl/geet/internal/config"
	"github.com/sumdahl/geet/internal/deezer"
	"github.com/sumdahl/geet/internal/index"
	"github.com/sumdahl/geet/internal/itunes"
	"github.com/sumdahl/geet/internal/lyrics"
	"github.com/sumdahl/geet/internal/player"
	"github.com/sumdahl/geet/internal/player/tui"
	"github.com/sumdahl/geet/internal/spotify"
	"github.com/sumdahl/geet/internal/youtube"
	"github.com/sumdahl/geet/internal/ytdlp"
)

// playEvent is one line of `geet play --json`. It follows the download
// contract's habits: a stage, the track it is about, and only additive
// fields (docs/03-communication-contract.md).
type playEvent struct {
	Track      string `json:"track"`
	Stage      string `json:"stage"` // track, playing, paused, position, stopped
	Path       string `json:"path,omitempty"`
	PositionMS int64  `json:"position_ms"`
	DurationMS int64  `json:"duration_ms,omitempty"`
	Index      int    `json:"index,omitempty"`
	Total      int    `json:"total,omitempty"`
}

func playCmd(ctx context.Context, args []string, stdout, stderr io.Writer) int {
	c := newCLI("play", "play [flags] [words… | link | file | folder]", stderr)
	positional, err := c.parse(args)
	if errors.Is(err, flag.ErrHelp) {
		return exitOK
	}
	if err != nil {
		return exitFatal
	}
	setupLogging(stderr, c.verbose)
	cfg, _, err := c.load()
	if err != nil {
		fmt.Fprintf(stderr, "geet: %v\n", err)
		return exitFatal
	}

	target := strings.TrimSpace(strings.Join(positional, " "))
	items, err := playQueue(ctx, c, cfg, positional, target, stdout, stderr)
	if err != nil {
		fmt.Fprintf(stderr, "geet: %v\n", err)
		return exitFatal
	}
	if len(items) == 0 {
		nothingToPlay(cfg, target, stderr)
		return exitPartial
	}
	return playItems(ctx, c, cfg, items, stdout, stderr)
}

// playTracks plays songs another command picked (the trending chart), with
// the same rules: from the library when they are there, streamed when they
// are not.
func playTracks(ctx context.Context, c *cli, cfg config.Config, tracks []spotify.Track, stdout, stderr io.Writer) int {
	items := queueFor(cfg, tracks)
	if len(items) == 0 {
		fmt.Fprintln(stderr, "Nothing to play.")
		return exitPartial
	}
	return playItems(ctx, c, cfg, items, stdout, stderr)
}

// playItems opens the player on a ready queue.
func playItems(ctx context.Context, c *cli, cfg config.Config, items []player.Item, stdout, stderr io.Writer) int {
	if cfg.Player.Shuffle {
		player.Shuffle(items)
	}

	engine, engineName, err := player.Open(ctx, player.Name(cfg.Player.Engine), cfg.Player.MPV, cfg.Player.FFplay)
	if err != nil {
		fmt.Fprintf(stderr, "geet: %v\n", err)
		return exitFatal
	}
	defer func() { _ = engine.Close() }()

	var lyr *lyrics.Client
	if cfg.Player.Lyrics {
		lyr = lyrics.New()
	}
	opts := tui.Options{
		Streamer: newStreamer(cfg),
		Save: func(ctx context.Context, t spotify.Track) (string, error) {
			return saveTrack(ctx, c, t, stdout, stderr)
		},
		Items:      items,
		Engine:     engine,
		EngineName: engineName,
		Lyrics:     lyr,
		FFmpeg:     cfg.Tools.FFmpeg,
		FFprobe:    cfg.Tools.FFprobe,
		Visualizer: cfg.Player.Visualizer,
		Repeat:     cfg.Player.Repeat,
	}

	if c.json {
		return playJSON(ctx, opts, stdout, stderr)
	}
	return playScreen(ctx, opts, stderr)
}

// playScreen runs the full-screen player.
func playScreen(ctx context.Context, opts tui.Options, stderr io.Writer) int {
	m := tui.New(ctx, opts)
	defer m.Close()
	// geet already catches SIGINT and SIGTERM (see run()) and cancels ctx.
	// Letting bubbletea install a second handler deadlocks its shutdown:
	// its signal goroutine blocks sending a quit message that the closing
	// program will never read, and Run waits for that goroutine forever.
	p := tea.NewProgram(m, tea.WithAltScreen(), tea.WithContext(ctx), tea.WithoutSignalHandler())
	if _, err := p.Run(); err != nil && !errors.Is(err, tea.ErrProgramKilled) && !errors.Is(err, context.Canceled) {
		fmt.Fprintf(stderr, "geet: %v\n", err)
		return exitFatal
	}
	return exitOK
}

// playJSON plays without a screen and streams events, for the plugin. The
// same model runs it, so both modes behave identically; only the display is
// missing.
func playJSON(ctx context.Context, opts tui.Options, stdout, stderr io.Writer) int {
	enc := newJSONEncoder(stdout)
	total := len(opts.Items)
	position := map[string]int{}
	for i, it := range opts.Items {
		position[it.Path] = i + 1
	}
	opts.OnEvent = func(e tui.Event) {
		_ = enc.Encode(playEvent{
			Track:      e.Item.Name(),
			Stage:      e.Stage,
			Path:       e.Item.Path,
			PositionMS: e.Position.Milliseconds(),
			DurationMS: e.Duration.Milliseconds(),
			Index:      position[e.Item.Path],
			Total:      total,
		})
	}
	m := tui.New(ctx, opts)
	defer m.Close()
	// tea.WithoutRenderer runs the same model with no output: the queue,
	// lyrics and events all work, and nothing is drawn.
	p := tea.NewProgram(m, tea.WithoutRenderer(), tea.WithInput(nil), tea.WithContext(ctx), tea.WithoutSignalHandler())
	if _, err := p.Run(); err != nil && !errors.Is(err, tea.ErrProgramKilled) && !errors.Is(err, context.Canceled) {
		fmt.Fprintf(stderr, "geet: %v\n", err)
		return exitFatal
	}
	return exitOK
}

// playQueue turns what the user typed into files to play.
func playQueue(ctx context.Context, c *cli, cfg config.Config, positional []string, target string, stdout, stderr io.Writer) ([]player.Item, error) {
	// Nothing typed: the whole library, newest first.
	if target == "" {
		return player.Scan(cfg.Output)
	}

	// A file or folder on disk.
	if info, err := os.Stat(expandPath(target)); err == nil {
		path := expandPath(target)
		if info.IsDir() {
			return player.Scan(path)
		}
		if !player.IsAudio(path) {
			return nil, fmt.Errorf("%s is not an audio file", filepath.Base(path))
		}
		return []player.Item{{Path: path, Added: info.ModTime()}}, nil
	}

	// One or more links or catalogue references. Several make a queue, so
	// a front end (the Omarchy panel) can hand over a whole chart and
	// next/previous walk it.
	if refs := playableRefs(positional); len(refs) > 0 {
		return playRefs(ctx, c, refs, stdout, stderr)
	}

	// Words: the library first, since playing must not need the network.
	all, err := player.Scan(cfg.Output)
	if err != nil {
		return nil, err
	}
	if found := player.Match(all, target); len(found) > 0 {
		return found, nil
	}

	// Nothing in the library: offer the catalogue, exactly as `geet search`
	// does, and play what gets downloaded.
	fmt.Fprintf(stderr, "Nothing in your library matches %q. Searching…\n", target)
	return playSearch(ctx, c, target, stdout, stderr)
}

// playableRefs returns the arguments when every one of them is a link or a
// catalogue reference. Mixed input ("play the beatles") is a search, not a
// queue.
func playableRefs(args []string) []string {
	if len(args) == 0 {
		return nil
	}
	for _, a := range args {
		if !isPlayableLink(a) {
			return nil
		}
	}
	return args
}

func isPlayableLink(s string) bool {
	if itunes.IsRef(s) || deezer.IsRef(s) {
		return true
	}
	_, err := spotify.ParseURL(s)
	return err == nil
}

// playLink reads a link's tracks and queues them: whatever is downloaded
// plays from the library, the rest streams.
func playRefs(ctx context.Context, c *cli, refs []string, stdout, stderr io.Writer) ([]player.Item, error) {
	rep, cfg, err := prepare(c, stdout, stderr)
	if err != nil {
		return nil, err
	}
	setupLogging(stderr, c.verbose)

	var col spotify.Collection
	for i, ref := range refs {
		one, _, err := readLink(ctx, cfg, rep, ref)
		if err != nil {
			// One bad reference in a queue of twenty is not a reason to
			// play nothing.
			if len(refs) == 1 {
				return nil, err
			}
			slog.WarnContext(ctx, "skipping a song that couldn't be read", "ref", ref, "err", err)
			continue
		}
		if i == 0 {
			col = one
		} else {
			col.Tracks = append(col.Tracks, one.Tracks...)
		}
	}
	if len(col.Tracks) == 0 {
		return nil, fmt.Errorf("none of those songs could be read")
	}
	if !cfg.Player.Stream {
		// Download-first, the old behaviour, for anyone who wants the
		// files before the music.
		rep.setUI(chooseUI(cfg.Progress, stderr, c.json))
		if _, err := runDownload(ctx, cfg, rep, col, true); err != nil {
			return nil, err
		}
		return itemsFor(cfg, col.Tracks), nil
	}
	return queueFor(cfg, col.Tracks), nil
}

// playSearch shows the search picker and plays what was picked, streaming
// anything not already downloaded.
func playSearch(ctx context.Context, c *cli, query string, stdout, stderr io.Writer) ([]player.Item, error) {
	rep, cfg, err := prepare(c, stdout, stderr)
	if err != nil {
		return nil, err
	}
	results, err := searchCatalog(ctx, cfg, query)
	if err != nil {
		return nil, err
	}
	if len(results) == 0 {
		return nil, nil
	}
	picked, err := pickResults(ctx, cfg.Search.Picker, query, results, os.Stdin, stderr)
	if err != nil {
		return nil, err
	}
	if len(picked) == 0 {
		return nil, nil
	}
	tracks := make([]spotify.Track, len(picked))
	for i, p := range picked {
		tracks[i] = results[p].Track
	}
	// A Deezer result lacks the year, ISRC and featured artists its full
	// entry has, and lyrics matching wants them.
	dz := deezer.New("")
	for i, t := range tracks {
		if id, ok := strings.CutPrefix(t.ID, deezer.RefPrefix); ok {
			if full, err := dz.Lookup(ctx, id); err == nil {
				tracks[i] = full
			}
		}
	}
	if !cfg.Player.Stream {
		rep.setUI(chooseUI(cfg.Progress, stderr, c.json))
		setupLogging(rep.ui.writer(), c.verbose)
		col := spotify.Collection{Ref: spotify.Ref{Kind: kindSearch}, Name: query, Tracks: tracks}
		if _, err := runDownload(ctx, cfg, rep, col, true); err != nil {
			return nil, err
		}
		return itemsFor(cfg, tracks), nil
	}
	return queueFor(cfg, tracks), nil
}

// newStreamer plays songs that aren't downloaded, unless the config says
// to download first.
func newStreamer(cfg config.Config) *player.Streamer {
	if !cfg.Player.Stream {
		return nil
	}
	runner := ytdlp.Runner{
		Binary:             cfg.Tools.YtDlp,
		CookiesFile:        cfg.YouTube.CookiesFile,
		CookiesFromBrowser: cfg.YouTube.CookiesFromBrowser,
		ExtraArgs:          cfg.YouTube.ExtraArgs,
	}
	return &player.Streamer{
		Runner: runner,
		YouTube: youtube.New(youtube.Options{
			YtDlp:           runner,
			SearchQuery:     cfg.YouTube.SearchQuery,
			FallbackQuery:   cfg.YouTube.FallbackQuery,
			SearchResults:   cfg.YouTube.SearchResults,
			MaxDurationDiff: cfg.YouTube.MaxDurationDiff.Duration,
			MusicFallback:   cfg.YouTube.MusicFallback,
			TitleFallback:   cfg.YouTube.TitleFallback,
		}),
	}
}

// saveTrack downloads the song that is playing and returns its file, so
// "keep this" is the same download the rest of geet does — same matching,
// tags and cover art.
func saveTrack(ctx context.Context, c *cli, t spotify.Track, stdout, stderr io.Writer) (string, error) {
	rep, cfg, err := prepare(c, stdout, io.Discard)
	if err != nil {
		return "", err
	}
	// The player owns the screen: the download must not draw on it.
	rep.setUI(&plainUI{w: io.Discard})
	col := spotify.Collection{Ref: spotify.Ref{Kind: kindSearch}, Name: t.Title, Tracks: []spotify.Track{t}}
	if _, err := runDownload(ctx, cfg, rep, col, true); err != nil {
		return "", err
	}
	items := itemsFor(cfg, []spotify.Track{t})
	if len(items) == 0 {
		return "", fmt.Errorf("the download finished but the file wasn't found")
	}
	return items[0].Path, nil
}

// queueFor turns tracks into a queue: each song plays from the library when
// it is there, and streams when it isn't.
func queueFor(cfg config.Config, tracks []spotify.Track) []player.Item {
	idx, _, err := index.Open(cfg.IndexPath)
	ext := "." + cfg.Format
	items := make([]player.Item, 0, len(tracks))
	for _, t := range tracks {
		item := player.Item{Track: t}
		if err == nil {
			if path, ok := idx.Lookup(t.ID, t.ISRC, ext, cfg.Output); ok {
				item.Path = path
				if info, statErr := os.Stat(path); statErr == nil {
					item.Added = info.ModTime()
				}
			}
		}
		items = append(items, item)
	}
	return items
}

// itemsFor finds each track's file through the download index. A track
// whose download failed simply isn't in the queue.
func itemsFor(cfg config.Config, tracks []spotify.Track) []player.Item {
	idx, _, err := index.Open(cfg.IndexPath)
	if err != nil {
		return nil
	}
	ext := "." + cfg.Format
	var items []player.Item
	for _, t := range tracks {
		path, ok := idx.Lookup(t.ID, t.ISRC, ext, cfg.Output)
		if !ok {
			continue
		}
		item := player.Item{Path: path, Track: t}
		if info, err := os.Stat(path); err == nil {
			item.Added = info.ModTime()
		}
		items = append(items, item)
	}
	return items
}

// nothingToPlay explains the empty case in the terms the user is in: an
// empty library, a search that found nothing, or a folder with no audio.
func nothingToPlay(cfg config.Config, target string, stderr io.Writer) {
	switch target {
	case "":
		fmt.Fprintf(stderr, "Nothing to play yet: %s has no songs.\n", cfg.Output)
		fmt.Fprintln(stderr, "Download one first, for example:")
		fmt.Fprintln(stderr, "  geet search blinding lights")
	default:
		fmt.Fprintf(stderr, "Nothing to play for %q.\n", target)
	}
}

func expandPath(p string) string {
	if strings.HasPrefix(p, "~/") {
		if home, err := os.UserHomeDir(); err == nil {
			return filepath.Join(home, p[2:])
		}
	}
	return p
}
