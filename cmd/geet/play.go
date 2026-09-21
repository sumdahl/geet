package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
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
	items, err := playQueue(ctx, c, cfg, target, stdout, stderr)
	if err != nil {
		fmt.Fprintf(stderr, "geet: %v\n", err)
		return exitFatal
	}
	if len(items) == 0 {
		nothingToPlay(cfg, target, stderr)
		return exitPartial
	}
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
func playQueue(ctx context.Context, c *cli, cfg config.Config, target string, stdout, stderr io.Writer) ([]player.Item, error) {
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

	// A link or a catalogue reference: play what is already downloaded, and
	// download what isn't.
	if isPlayableLink(target) {
		return playLink(ctx, c, target, stdout, stderr)
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

func isPlayableLink(s string) bool {
	if itunes.IsRef(s) || deezer.IsRef(s) {
		return true
	}
	_, err := spotify.ParseURL(s)
	return err == nil
}

// playLink downloads a link's tracks (skipping what the index already has)
// and returns their files in order.
func playLink(ctx context.Context, c *cli, link string, stdout, stderr io.Writer) ([]player.Item, error) {
	rep, cfg, err := prepare(c, stdout, stderr)
	if err != nil {
		return nil, err
	}
	rep.setUI(chooseUI(cfg.Progress, stderr, c.json))
	setupLogging(rep.ui.writer(), c.verbose)

	col, lateTags, err := readLink(ctx, cfg, rep, link)
	if err != nil {
		return nil, err
	}
	if _, err := runDownload(ctx, cfg, rep, col, lateTags); err != nil {
		return nil, err
	}
	return itemsFor(cfg, col.Tracks), nil
}

// playSearch shows the search picker and plays what it downloads.
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
	rep.setUI(chooseUI(cfg.Progress, stderr, c.json))
	setupLogging(rep.ui.writer(), c.verbose)
	col := spotify.Collection{Ref: spotify.Ref{Kind: kindSearch}, Name: query, Tracks: tracks}
	if _, err := runDownload(ctx, cfg, rep, col, true); err != nil {
		return nil, err
	}
	return itemsFor(cfg, tracks), nil
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
