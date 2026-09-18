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

	"github.com/sumdahl/spotify-dl/internal/audio"
	"github.com/sumdahl/spotify-dl/internal/config"
	"github.com/sumdahl/spotify-dl/internal/download"
	"github.com/sumdahl/spotify-dl/internal/library"
	"github.com/sumdahl/spotify-dl/internal/spotify"
	"github.com/sumdahl/spotify-dl/internal/youtube"
	"github.com/sumdahl/spotify-dl/internal/ytdlp"
)

// event is one NDJSON line of the --json contract (docs/03-communication-
// contract.md). Fields are only ever added, never renamed or removed.
type event struct {
	Track      string  `json:"track"`
	Stage      string  `json:"stage"` // resolved|downloading|tagging|done|failed
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

	fmt.Fprintf(stderr, "Reading %s %s from Spotify…\n", ref.Kind, ref.ID)
	tracks, err := resolveMetadata(ctx, cfg, ref)
	if err != nil {
		return rep.fatal(err)
	}
	fmt.Fprintf(stderr, "%d track(s) → %s\n", len(tracks), cfg.Output)

	rep.ui = chooseUI(cfg.Progress, stderr, c.json)
	setupLogging(rep.ui.writer(), c.verbose)
	d := newDownloader(cfg, rep)
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
	cfg config.Config
	yt  *youtube.Resolver
	ytd ytdlp.Runner
	rep *reporter
}

func newDownloader(cfg config.Config, rep *reporter) *downloader {
	ytd := ytdlp.Runner{
		Binary:             cfg.Tools.YtDlp,
		CookiesFile:        cfg.YouTube.CookiesFile,
		CookiesFromBrowser: cfg.YouTube.CookiesFromBrowser,
		ExtraArgs:          cfg.YouTube.ExtraArgs,
	}
	return &downloader{
		cfg: cfg,
		ytd: ytd,
		rep: rep,
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

	dest := library.Path(d.cfg.Output, d.cfg.OutputTemplate, t, d.cfg.Format)
	ev.Path = dest
	if !d.cfg.Overwrite {
		if _, err := os.Stat(dest); err == nil {
			ev.Skipped = true
			emit("done")
			return nil
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
	if err := os.MkdirAll(d.cfg.Output, 0o755); err != nil {
		return err
	}
	work, err := os.MkdirTemp(d.cfg.Output, ".spotify-dl-")
	if err != nil {
		return err
	}
	defer os.RemoveAll(work)

	emit("downloading")
	// NDJSON gets a "downloading" event per 10% step: enough for a
	// consumer's progress bar without flooding the pipe.
	lastStep := 0
	src, err := download.Fetch(ctx, d.ytd, best.URL, work, func(done, size int64) {
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

	ev.Warning = audio.QualityWarning(d.cfg.Format, d.cfg.Bitrate, src.Kbps, src.Codec)
	emit("done")
	return nil
}

func trackName(t spotify.Track) string {
	return strings.Join(t.Artists, ", ") + " - " + t.Title
}
