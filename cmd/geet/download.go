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

	"github.com/sumdahl/geet/internal/audio"
	"github.com/sumdahl/geet/internal/config"
	"github.com/sumdahl/geet/internal/deezer"
	"github.com/sumdahl/geet/internal/download"
	"github.com/sumdahl/geet/internal/index"
	"github.com/sumdahl/geet/internal/itunes"
	"github.com/sumdahl/geet/internal/library"
	"github.com/sumdahl/geet/internal/pipeline"
	"github.com/sumdahl/geet/internal/spotify"
	"github.com/sumdahl/geet/internal/youtube"
	"github.com/sumdahl/geet/internal/ytdlp"
)

// event is one NDJSON line of the --json contract (docs/03-communication-
// contract.md). Fields are only ever added, never renamed or removed.
type event struct {
	Track      string  `json:"track"`
	Stage      string  `json:"stage"`          // reading|resolved|downloading|tagging|done|failed
	Step       string  `json:"step,omitempty"` // "reading" only: index, spotify, tags
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
	fmt.Fprintf(r.ui.writer(), "geet: %v\n", err)
	return exitFatal
}

func downloadCmd(ctx context.Context, args []string, stdout, stderr io.Writer) int {
	c := newCLI("download", "download [flags] <spotify-url | apple-music-url | itunes:<id>>", stderr)
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
	rep, cfg, err := prepare(c, stdout, stderr)
	if err != nil {
		return rep.fatal(err)
	}
	link := positional[0]

	if itunes.IsRef(link) {
		col, err := lookupITunes(ctx, cfg, link)
		if err != nil {
			return rep.fatal(err)
		}
		rep.ui = chooseUI(cfg.Progress, stderr, c.json)
		setupLogging(rep.ui.writer(), c.verbose)
		return runDownload(ctx, c, cfg, rep, col, true, stderr)
	}

	ref, err := spotify.ParseURL(link)
	if err != nil {
		return rep.fatal(err)
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
	col, lateTags, err := resolveMetadata(ctx, cfg, ref, func(step string, done, total int) {
		stepOf(step).set(done, total)
		rep.reading(step, done, total)
	})
	for _, ph := range phases {
		ph.finish()
	}
	if err != nil {
		return rep.fatal(err)
	}
	return runDownload(ctx, c, cfg, rep, col, lateTags, stderr)
}

// prepare loads the config and checks for the tools every download needs.
// The reporter prints plainly until the caller switches to the configured
// display, so config errors come out readable either way.
func prepare(c *cli, stdout, stderr io.Writer) (*reporter, config.Config, error) {
	rep := &reporter{ui: &plainUI{w: stderr}, warned: map[string]bool{}}
	if c.json {
		rep.json = newJSONEncoder(stdout)
	}
	setupLogging(stderr, c.verbose)

	cfg, _, err := c.load()
	if err != nil {
		return rep, cfg, err
	}
	for _, tool := range []struct{ name, bin string }{{"yt-dlp", cfg.Tools.YtDlp}, {"ffmpeg", cfg.Tools.FFmpeg}} {
		if _, err := exec.LookPath(tool.bin); err != nil {
			return rep, cfg, fmt.Errorf("%s not found (%q): install it or set tools.%s", tool.name, tool.bin, strings.ReplaceAll(tool.name, "-", "_"))
		}
	}
	return rep, cfg, nil
}

// lookupITunes resolves an "itunes:<id>" reference or Apple Music song link
// (what `geet search --json` hands out) to a one-track collection.
func lookupITunes(ctx context.Context, cfg config.Config, link string) (spotify.Collection, error) {
	id, err := itunes.ParseRef(link)
	if err != nil {
		return spotify.Collection{}, err
	}
	t, err := itunes.New("", cfg.Search.Country).Lookup(ctx, id)
	if err != nil {
		return spotify.Collection{}, err
	}
	return spotify.Collection{Ref: spotify.Ref{Kind: spotify.KindTrack, ID: t.ID}, Name: t.Title, Tracks: []spotify.Track{t}}, nil
}

// runDownload takes a resolved collection through the pipeline into the
// library and reports the outcome as an exit code. lateTags means ISRC and
// disc numbers are still to be looked up per track (see resolveMetadata).
func runDownload(ctx context.Context, c *cli, cfg config.Config, rep *reporter, col spotify.Collection, lateTags bool, stderr io.Writer) int {
	ref := col.Ref
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
	if lateTags {
		d.dz = deezer.New("")
	}
	// Per-track work dirs go inside one run dir in the library, so finished
	// files rename into place atomically and one RemoveAll cleans up even
	// after Ctrl+C.
	if err := os.MkdirAll(root, 0o755); err != nil {
		return rep.fatal(err)
	}
	if d.work, err = os.MkdirTemp(root, ".geet-"); err != nil {
		return rep.fatal(err)
	}
	defer os.RemoveAll(d.work)

	jobs := make([]*trackJob, len(tracks))
	for i, t := range tracks {
		jobs[i] = &trackJob{t: t, ev: event{Track: trackName(t), SpotifyID: t.ID, Index: i + 1, Total: len(tracks)}}
	}
	failed := 0
	err = pipeline.Run(ctx, jobs, []pipeline.Stage[*trackJob]{
		{Name: "resolve", Workers: cfg.ResolveJobs, Do: d.resolve},
		{Name: "download", Workers: cfg.Jobs, Do: d.download},
		{Name: "tag", Workers: tagWorkers, Do: d.tag},
	}, isFatal, func(j *trackJob, err error) {
		if err != nil {
			failed++
			j.ev.Error = err.Error()
			d.emit(j, "failed")
		}
	})
	switch {
	case ctx.Err() != nil:
		return rep.fatal(errors.New("interrupted"))
	case err != nil:
		return rep.fatal(err)
	}

	rep.ui.close(false)
	setupLogging(stderr, c.verbose)
	if failed > 0 {
		fmt.Fprintf(stderr, "%d of %d track(s) failed\n", failed, len(tracks))
		return exitPartial
	}
	return exitOK
}

// tagWorkers is fixed: tagging is a short ffmpeg run and never the
// bottleneck (docs/02-concurrency-pipeline.md).
const tagWorkers = 2

func isFatal(err error) bool {
	return errors.Is(err, ytdlp.ErrToolMissing) || errors.Is(err, audio.ErrToolMissing)
}

type downloader struct {
	cfg  config.Config
	root string // output, or the playlist's folder inside it
	work string // this run's scratch dir inside root
	idx  *index.Index
	dz   *deezer.Client // set when tags are looked up per track, in resolve
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
			FallbackQuery:   cfg.YouTube.FallbackQuery,
			SearchResults:   cfg.YouTube.SearchResults,
			MaxDurationDiff: cfg.YouTube.MaxDurationDiff.Duration,
		}),
	}
}

// trackJob is one track's trip through the pipeline. Only one stage holds it
// at a time.
type trackJob struct {
	t    spotify.Track
	ev   event
	tu   trackUI
	dest string
	url  string
	work string
	src  download.Source
}

func (d *downloader) emit(j *trackJob, stage string) {
	e := j.ev
	e.Stage = stage
	d.rep.emit(j.tu, e)
}

// resolve decides what the track needs: nothing (the file exists), a link to
// a copy downloaded elsewhere, or a download of the YouTube upload it finds.
func (d *downloader) resolve(ctx context.Context, j *trackJob) (*trackJob, bool, error) {
	j.tu = d.rep.ui.track(j.ev.Index, j.ev.Total, j.ev.Track)
	j.dest = library.Path(d.root, d.cfg.OutputTemplate, j.t, d.cfg.Format)
	j.ev.Path = j.dest
	if !d.cfg.Overwrite {
		if _, err := os.Stat(j.dest); err == nil {
			d.remember(ctx, j.t, j.dest)
			j.ev.Skipped = true
			d.emit(j, "done")
			return j, true, nil
		}
	}
	// Looked up here rather than up front, so an existing file costs no
	// lookup, and before the duplicate check, which can match on ISRC.
	if d.dz != nil {
		if err := d.dz.EnrichTrack(ctx, &j.t); err != nil {
			if ctx.Err() != nil {
				return j, false, ctx.Err()
			}
			slog.WarnContext(ctx, "Deezer lookup failed; ISRC and disc number may be missing", "track", j.ev.Track, "err", err)
		}
	}
	if !d.cfg.Overwrite {
		if done, err := d.reuse(ctx, j.t, j.dest, &j.ev); done || err != nil {
			if err == nil {
				d.emit(j, "done")
			}
			return j, done, err
		}
	}

	best, _, err := d.yt.Resolve(ctx, j.t)
	if err != nil {
		return j, false, err
	}
	j.url = best.URL
	j.ev.YouTubeURL = best.URL
	d.emit(j, "resolved")
	return j, false, nil
}

func (d *downloader) download(ctx context.Context, j *trackJob) (*trackJob, bool, error) {
	work, err := os.MkdirTemp(d.work, "track-")
	if err != nil {
		return j, false, err
	}
	j.work = work
	d.emit(j, "downloading")
	j.src, err = d.fetch(ctx, j.url, j.work, j.tu, j.ev)
	return j, false, err
}

func (d *downloader) tag(ctx context.Context, j *trackJob) (*trackJob, bool, error) {
	defer os.RemoveAll(j.work)
	d.emit(j, "tagging")
	cover, err := audio.FetchCover(ctx, j.t.CoverURL)
	if err != nil {
		slog.WarnContext(ctx, "no cover art", "track", j.ev.Track, "err", err)
	}
	out := filepath.Join(j.work, "out."+d.cfg.Format)
	err = audio.Encode(ctx, d.cfg.Tools.FFmpeg, audio.Job{
		Source: j.src.Path, SourceCodec: j.src.Codec, Dest: out,
		Format: d.cfg.Format, Bitrate: d.cfg.Bitrate, Track: j.t, Cover: cover,
	})
	if err != nil {
		return j, false, err
	}
	if err := os.MkdirAll(filepath.Dir(j.dest), 0o755); err != nil {
		return j, false, err
	}
	if err := os.Rename(out, j.dest); err != nil {
		return j, false, err
	}
	d.remember(ctx, j.t, j.dest)
	j.ev.Warning = audio.QualityWarning(d.cfg.Format, d.cfg.Bitrate, j.src.Kbps, j.src.Codec)
	d.emit(j, "done")
	return j, false, nil
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
