package main

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"log/slog"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/sumdahl/spotify-dl/internal/audio"
	"github.com/sumdahl/spotify-dl/internal/config"
	"github.com/sumdahl/spotify-dl/internal/download"
	"github.com/sumdahl/spotify-dl/internal/index"
	"github.com/sumdahl/spotify-dl/internal/library"
	"github.com/sumdahl/spotify-dl/internal/spotify"
	"github.com/sumdahl/spotify-dl/internal/youtube"
	"github.com/sumdahl/spotify-dl/internal/ytdlp"
)

// event is one NDJSON line of the --json contract (docs/03-communication-
// contract.md). Fields are only ever added, never renamed or removed.
type event struct {
	Track      string  `json:"track"`
	Stage      string  `json:"stage"`          // reading|resolved|downloading|tagging|done|failed
	Step       string  `json:"step,omitempty"` // "reading" only: spotify, then tags
	Error      string  `json:"error,omitempty"`
	Fatal      bool    `json:"fatal,omitempty"` // the whole run stopped, not just this track
	SpotifyID  string  `json:"spotify_id,omitempty"`
	Index      int     `json:"index,omitempty"` // 1-based position in the request
	Total      int     `json:"total,omitempty"`
	YouTubeURL string  `json:"youtube_url,omitempty"`
	Path       string  `json:"path,omitempty"`
	Skipped    bool    `json:"skipped,omitempty"` // done without downloading: the file already existed
	Warning    string  `json:"warning,omitempty"`
	Progress   float64 `json:"progress,omitempty"` // repeated "downloading" events: 0.1 … 1.0
	// DuplicateOf is set on "done" when the track wasn't downloaded because
	// this file already had it; Linked tells a hard link from a copy.
	DuplicateOf string `json:"duplicate_of,omitempty"`
	Linked      bool   `json:"linked,omitempty"`
}

// reporter sends each event to the NDJSON stream (when --json) and to the
// human display. Safe for concurrent use.
type reporter struct {
	ui ui

	mu     sync.Mutex
	json   *json.Encoder
	warned map[string]bool
}

func (r *reporter) emit(tu trackUI, e event) {
	r.mu.Lock()
	if r.json != nil {
		if err := r.json.Encode(e); err != nil {
			slog.Error("writing progress", "err", err)
		}
	}
	// Every track carries its warning in the NDJSON, but a human needs to
	// read the same one only once per run.
	warn := e.Warning != "" && !r.warned[e.Warning]
	if warn {
		r.warned[e.Warning] = true
	}
	r.mu.Unlock()

	tu.stage(e)
	if warn {
		r.ui.log("warning: %s", e.Warning)
	}
}

// reading reports progress resolving the link, before any track starts.
func (r *reporter) reading(step string, done, total int) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.json != nil {
		r.json.Encode(event{Stage: "reading", Step: step, Index: done, Total: total})
	}
}

func (r *reporter) fatal(err error) int {
	r.ui.close(true)
	r.mu.Lock()
	if r.json != nil {
		r.json.Encode(event{Stage: "failed", Error: err.Error(), Fatal: true})
	}
	r.mu.Unlock()
	fmt.Fprintf(r.ui.writer(), "spotify-dl: %v\n", err)
	return exitFatal
}

func downloadCmd(ctx context.Context, args []string, stdout, stderr io.Writer) int {
	c := newCLI("download", "download [flags] <spotify-url>", stderr)
	positional, err := c.parse(args)
	if errors.Is(err, flag.ErrHelp) {
		return exitOK
	}
	if err != nil {
		return exitFatal
	}
	if len(positional) != 1 {
		c.fs.Usage()
		return exitFatal
	}
	// The display depends on config, so config errors go out plainly.
	rep := &reporter{ui: &plainUI{w: stderr}, warned: map[string]bool{}}
	if c.json {
		rep.json = json.NewEncoder(stdout)
	}
	setupLogging(stderr, c.verbose)

	cfg, _, err := c.load()
	if err != nil {
		return rep.fatal(err)
	}
	ref, err := spotify.ParseURL(positional[0])
	if err != nil {
		return rep.fatal(err)
	}
	for _, tool := range []struct{ name, bin string }{{"yt-dlp", cfg.Tools.YtDlp}, {"ffmpeg", cfg.Tools.FFmpeg}} {
		if _, err := exec.LookPath(tool.bin); err != nil {
			return rep.fatal(fmt.Errorf("%s not found (%q): install it or set tools.%s", tool.name, tool.bin, strings.ReplaceAll(tool.name, "-", "_")))
		}
	}

	rep.ui = chooseUI(cfg.Progress, stderr, c.json)
	setupLogging(rep.ui.writer(), c.verbose)

	// Resolving a playlist reads every track's page and then looks up tags,
	// which takes a while; show both steps rather than a silent terminal.
	labels := map[string]string{
		"spotify": fmt.Sprintf("Reading %s from Spotify", ref.Kind),
		"tags":    "Looking up tags on Deezer",
	}
	phases := map[string]phaseUI{}
	stepOf := func(step string) phaseUI {
		ph, ok := phases[step]
		if !ok {
			for _, prev := range phases {
				prev.finish()
			}
			ph = rep.ui.phase(labels[step])
			phases[step] = ph
		}
		return ph
	}
	if ref.Kind != spotify.KindPlaylist {
		stepOf("spotify")
	}
	col, err := resolveMetadata(ctx, cfg, ref, func(step string, done, total int) {
		stepOf(step).set(done, total)
		rep.reading(step, done, total)
	})
	for _, ph := range phases {
		ph.finish()
	}
	if err != nil {
		return rep.fatal(err)
	}
	tracks := col.Tracks
	root := cfg.Output
	if ref.Kind == spotify.KindPlaylist && cfg.PlaylistFolder {
		root = filepath.Join(cfg.Output, library.FolderName(col.Name, cfg.PlaylistFolderCase))
	}
	rep.ui.log("%s %q: %d track(s) → %s", ref.Kind, col.Name, len(tracks), root)

	idx, err := openIndex(ctx, cfg, rep)
	if err != nil {
		return rep.fatal(err)
	}
	d := newDownloader(cfg, root, rep, idx)
	failed := 0
	for i, t := range tracks {
		err := d.track(ctx, t, i+1, len(tracks))
		switch {
		case err == nil:
		case ctx.Err() != nil:
			return rep.fatal(errors.New("interrupted"))
		case errors.Is(err, ytdlp.ErrToolMissing), errors.Is(err, audio.ErrToolMissing):
			return rep.fatal(err)
		default:
			failed++
		}
	}

	rep.ui.close(false)
	setupLogging(stderr, c.verbose)
	if failed > 0 {
		fmt.Fprintf(stderr, "%d of %d track(s) failed\n", failed, len(tracks))
		return exitPartial
	}
	return exitOK
}

type downloader struct {
	cfg  config.Config
	root string // output, or the playlist's folder inside it
	idx  *index.Index
	yt   *youtube.Resolver
	ytd  ytdlp.Runner
	rep  *reporter
}

func newDownloader(cfg config.Config, root string, rep *reporter, idx *index.Index) *downloader {
	ytd := ytdlp.Runner{
		Binary:             cfg.Tools.YtDlp,
		CookiesFile:        cfg.YouTube.CookiesFile,
		CookiesFromBrowser: cfg.YouTube.CookiesFromBrowser,
		ExtraArgs:          cfg.YouTube.ExtraArgs,
	}
	return &downloader{
		cfg:  cfg,
		root: root,
		idx:  idx,
		ytd:  ytd,
		rep:  rep,
		yt: youtube.New(youtube.Options{
			YtDlp:           ytd,
			SearchQuery:     cfg.YouTube.SearchQuery,
			SearchResults:   cfg.YouTube.SearchResults,
			MaxDurationDiff: cfg.YouTube.MaxDurationDiff.Duration,
		}),
	}
}

// track takes one track through match → download → tag → move into place,
// reporting each stage. A returned error has already been reported.
func (d *downloader) track(ctx context.Context, t spotify.Track, index, total int) (err error) {
	ev := event{Track: trackName(t), SpotifyID: t.ID, Index: index, Total: total}
	tu := d.rep.ui.track(index, total, ev.Track)
	emit := func(stage string) {
		e := ev
		e.Stage = stage
		d.rep.emit(tu, e)
	}
	defer func() {
		if err != nil && ctx.Err() == nil {
			ev.Error = err.Error()
			emit("failed")
		}
	}()

	dest := library.Path(d.root, d.cfg.OutputTemplate, t, d.cfg.Format)
	ev.Path = dest
	if !d.cfg.Overwrite {
		if _, err := os.Stat(dest); err == nil {
			d.remember(ctx, t, dest)
			ev.Skipped = true
			emit("done")
			return nil
		}
		if done, err := d.reuse(ctx, t, dest, &ev); done || err != nil {
			if err == nil {
				emit("done")
			}
			return err
		}
	}

	best, _, err := d.yt.Resolve(ctx, t)
	if err != nil {
		return err
	}
	ev.YouTubeURL = best.URL
	emit("resolved")

	// The work directory sits inside the library so the finished file can be
	// renamed into place atomically instead of copied across filesystems.
	if err := os.MkdirAll(d.root, 0o755); err != nil {
		return err
	}
	work, err := os.MkdirTemp(d.root, ".spotify-dl-")
	if err != nil {
		return err
	}
	defer os.RemoveAll(work)

	emit("downloading")
	src, err := d.fetch(ctx, best.URL, work, tu, ev)
	if err != nil {
		return err
	}

	emit("tagging")
	cover, err := audio.FetchCover(ctx, t.CoverURL)
	if err != nil {
		slog.WarnContext(ctx, "no cover art", "track", ev.Track, "err", err)
	}
	out := filepath.Join(work, "out."+d.cfg.Format)
	err = audio.Encode(ctx, d.cfg.Tools.FFmpeg, audio.Job{
		Source: src.Path, SourceCodec: src.Codec, Dest: out,
		Format: d.cfg.Format, Bitrate: d.cfg.Bitrate, Track: t, Cover: cover,
	})
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(dest), 0o755); err != nil {
		return err
	}
	if err := os.Rename(out, dest); err != nil {
		return err
	}

	d.remember(ctx, t, dest)
	ev.Warning = audio.QualityWarning(d.cfg.Format, d.cfg.Bitrate, src.Kbps, src.Codec)
	emit("done")
	return nil
}

// reuse satisfies t from a file already downloaded under another name (the
// same song in another playlist, or the same recording on another release)
// per the duplicates setting. done is false when t must be downloaded.
func (d *downloader) reuse(ctx context.Context, t spotify.Track, dest string, ev *event) (done bool, err error) {
	if d.cfg.Duplicates == "download" {
		return false, nil
	}
	src, ok := d.idx.Lookup(t.ID, t.ISRC, d.cfg.Format)
	if !ok || src == dest {
		return false, nil
	}
	ev.DuplicateOf = src
	if d.cfg.Duplicates == "skip" {
		ev.Skipped = true
		return true, nil
	}
	linked, err := index.Place(src, dest, d.cfg.Duplicates == "copy")
	if err != nil {
		return false, fmt.Errorf("reusing %s: %w", src, err)
	}
	ev.Linked = linked
	d.remember(ctx, t, dest)
	return true, nil
}

// remember records dest in the index and saves it right away, so an
// interrupted run keeps what it did.
func (d *downloader) remember(ctx context.Context, t spotify.Track, dest string) {
	d.idx.Add(t.ID, t.ISRC, dest)
	if err := d.idx.Save(); err != nil {
		slog.WarnContext(ctx, "saving the download index", "err", err)
	}
}

// openIndex loads the download index. The first time (no index yet) it
// builds one from the existing library, so songs downloaded before the index
// existed aren't downloaded again; that needs ffprobe, and without it the
// index just starts empty.
func openIndex(ctx context.Context, cfg config.Config, rep *reporter) (*index.Index, error) {
	idx, fresh, err := index.Open(cfg.IndexPath)
	if err != nil {
		return nil, err
	}
	if !fresh {
		return idx, nil
	}
	if _, err := exec.LookPath(cfg.Tools.FFprobe); err != nil {
		slog.WarnContext(ctx, "ffprobe not found; not indexing existing downloads", "tools.ffprobe", cfg.Tools.FFprobe)
		return idx, nil
	}
	ph := rep.ui.phase("Indexing your existing downloads (first run only)")
	err = idx.Scan(ctx, cfg.Tools.FFprobe, cfg.Output, func(done, total int) {
		ph.set(done, total)
		rep.reading("index", done, total)
	})
	ph.finish()
	if err != nil {
		return nil, err
	}
	if err := idx.Save(); err != nil {
		return nil, err
	}
	if n := idx.Len(); n > 0 {
		rep.ui.log("Indexed %d existing download(s) in %s", n, cfg.Output)
	}
	return idx, nil
}

// fetch downloads with retries: YouTube fails downloads now and then (a 403,
// throttling) that work on the next try.
func (d *downloader) fetch(ctx context.Context, url, work string, tu trackUI, ev event) (download.Source, error) {
	for attempt := 0; ; attempt++ {
		// NDJSON gets a "downloading" event per 10% step: enough for a
		// consumer's progress bar without flooding the pipe.
		lastStep := 0
		src, err := download.Fetch(ctx, d.ytd, url, work, func(done, size int64) {
			tu.progress(done, size)
			if size <= 0 {
				return
			}
			if step := int(done * 10 / size); step > lastStep {
				lastStep = step
				e := ev
				e.Stage = "downloading"
				e.Progress = float64(step) / 10
				d.rep.emit(tu, e)
			}
		})
		if err == nil || ctx.Err() != nil || errors.Is(err, ytdlp.ErrToolMissing) {
			return src, err
		}
		if attempt >= d.cfg.DownloadRetries {
			if attempt > 0 {
				err = fmt.Errorf("%w (after %d attempts)", err, attempt+1)
			}
			return src, err
		}
		slog.DebugContext(ctx, "retrying download", "track", ev.Track, "attempt", attempt+2, "err", err)
		tu.progress(0, 0)
		select {
		case <-time.After(time.Duration(attempt+1) * 2 * time.Second):
		case <-ctx.Done():
			return src, ctx.Err()
		}
	}
}

func trackName(t spotify.Track) string {
	return strings.Join(t.Artists, ", ") + " - " + t.Title
}
