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
	"slices"
	"strings"
	"sync"
	"sync/atomic"
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
	// geet watch only: which copied link an event belongs to. Job numbers
	// the links in the order they were copied; Source is the link (the first
	// one when several songs were copied together).
	Job    int     `json:"job,omitempty"`
	Source string  `json:"source,omitempty"`
	Name   string  `json:"name,omitempty"`   // "finished": the album, playlist or song
	Counts *counts `json:"counts,omitempty"` // "finished": how the link's tracks ended
}

type counts struct {
	Saved    int `json:"saved"`    // downloaded
	Existing int `json:"existing"` // already in the library (the file, or a duplicate reused)
	Failed   int `json:"failed"`
}

// reporter sends each event to the NDJSON stream (when --json) and to the
// human display. Safe for concurrent use.
type reporter struct {
	ui     ui
	stderr io.Writer // the terminal itself, still there after ui closes

	mu     sync.Mutex
	json   *json.Encoder
	warned map[string]bool
	job    int // geet watch: the link being downloaded, stamped on its events
	source string
}

// write sends e to the NDJSON stream, if any. The caller holds r.mu.
func (r *reporter) write(e event) {
	if r.json == nil {
		return
	}
	if e.Job == 0 {
		e.Job, e.Source = r.job, r.source
	}
	if err := r.json.Encode(e); err != nil {
		slog.Error("writing progress", "err", err)
	}
}

func (r *reporter) emit(tu trackUI, e event) {
	r.mu.Lock()
	r.write(e)
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

// notice tells the user something they should act on, once: as a
// "warning" line for humans and a "reading" event with a warning in NDJSON.
func (r *reporter) notice(msg string) {
	r.mu.Lock()
	r.write(event{Stage: "reading", Warning: msg})
	r.mu.Unlock()
	r.ui.log("%s", r.ui.highlight("warning: "+msg))
}

// reading reports progress resolving the link, before any track starts.
func (r *reporter) reading(step string, done, total int) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.write(event{Stage: "reading", Step: step, Index: done, Total: total})
}

// setUI switches the display. watch swaps it per link while its clipboard
// goroutine may be logging, hence the lock.
func (r *reporter) setUI(u ui) {
	r.mu.Lock()
	r.ui = u
	r.mu.Unlock()
}

// exit ends a download command: fatal on err, otherwise the exit code for
// how the tracks went.
func (r *reporter) exit(c *cli, res outcome, err error, stderr io.Writer) int {
	if err != nil {
		return r.fatal(err)
	}
	r.ui.close(false)
	setupLogging(stderr, c.verbose)
	if res.failed > 0 {
		fmt.Fprintf(stderr, "%d of %d track(s) failed\n", res.failed, res.total())
		return exitPartial
	}
	return exitOK
}

func (r *reporter) fatal(err error) int {
	r.ui.close(true)
	r.mu.Lock()
	r.write(event{Stage: "failed", Error: err.Error(), Fatal: true})
	r.mu.Unlock()
	// Not r.ui.writer(): once the progress display is shut down, writes to
	// it are dropped, and the one line saying why geet stopped was lost.
	fmt.Fprintf(r.stderr, "geet: %v\n", err)
	return exitFatal
}

func downloadCmd(ctx context.Context, args []string, stdout, stderr io.Writer) int {
	c := newCLI("download", "download [flags] <spotify-url | apple-music-url | deezer-url | itunes:<id> | deezer:<id>>", stderr)
	// --tracks alone reads the links from stdin; --tracks=FILE reads a file.
	// A bool-style flag, so the easily forgotten "-" isn't required.
	tracksFrom := new(string)
	c.fs.BoolFunc("tracks", "download song links instead of the link's own list, read from stdin (wl-paste | geet download <playlist> --tracks), or --tracks=FILE; for playlists over Spotify's 100-song public limit", func(v string) error {
		switch v {
		case "true", "-":
			*tracksFrom = "-"
		case "false":
			*tracksFrom = ""
		default:
			*tracksFrom = v
		}
		return nil
	})
	positional, err := c.parse(args)
	if errors.Is(err, flag.ErrHelp) {
		return exitOK
	}
	if err != nil {
		return exitFatal
	}
	// "--tracks -" (the form in older messages) parses as the bare flag and
	// a "-" argument; the "-" means stdin, which the bare flag already does.
	positional = slices.DeleteFunc(positional, func(a string) bool { return a == "-" && *tracksFrom == "-" })
	if len(positional) > 1 || (len(positional) == 0 && *tracksFrom == "") {
		c.fs.Usage()
		return exitFatal
	}
	rep, cfg, err := prepare(c, stdout, stderr)
	if err != nil {
		return rep.fatal(err)
	}
	link := ""
	if len(positional) == 1 {
		link = positional[0]
	}

	if *tracksFrom != "" {
		return downloadList(ctx, c, cfg, rep, link, *tracksFrom, stderr)
	}

	rep.setUI(chooseUI(cfg.Progress, stderr, c.json))
	setupLogging(rep.ui.writer(), c.verbose)
	col, lateTags, err := readLink(ctx, cfg, rep, link)
	if err != nil {
		return rep.fatal(err)
	}
	res, err := runDownload(ctx, cfg, rep, col, lateTags)
	return rep.exit(c, res, err, stderr)
}

// readLink reads the tracks behind one link: a Spotify track, album or
// playlist, or an Apple Music or Deezer song (link, itunes:<id> or
// deezer:<id>). lateTags is as for
// resolveMetadata.
func readLink(ctx context.Context, cfg config.Config, rep *reporter, link string) (col spotify.Collection, lateTags bool, err error) {
	if itunes.IsRef(link) {
		col, err := lookupITunes(ctx, cfg, link)
		return col, true, err
	}
	if deezer.IsRef(link) {
		col, err := lookupDeezer(ctx, link)
		return col, false, err
	}
	ref, err := spotify.ParseURL(link)
	if err != nil {
		return spotify.Collection{}, false, err
	}
	steps := newSteps(rep, fmt.Sprintf("Reading %s from Spotify", ref.Kind))
	if ref.Kind != spotify.KindPlaylist {
		steps.show("spotify")
	}
	col, lateTags, err = resolveMetadata(ctx, cfg, ref, steps.report)
	steps.finish()
	if err != nil {
		return spotify.Collection{}, false, err
	}
	if col.Total > len(col.Tracks) {
		notice := fmt.Sprintf("Spotify's public page shows only %d of the %d songs in this playlist.", len(col.Tracks), col.Total)
		rep.notice(notice + "\nTo download all of them: in the Spotify app open the playlist, press Ctrl+A then Ctrl+C, then run:\n  wl-paste | geet download \"" + link + "\" --tracks")
	}
	return col, lateTags, nil
}

// downloadList downloads the song links read from `from` (a file, or - for
// stdin). A playlist or album link, if given, only names the folder and
// labels the run; its own (possibly truncated) track list isn't read.
func downloadList(ctx context.Context, c *cli, cfg config.Config, rep *reporter, link, from string, stderr io.Writer) int {
	in := io.Reader(os.Stdin)
	if from != "-" {
		// Shells don't expand ~ after "=", as in --tracks=~/links.txt.
		path, err := config.ExpandHome(from)
		if err != nil {
			return rep.fatal(err)
		}
		f, err := os.Open(path)
		if err != nil {
			return rep.fatal(err)
		}
		defer f.Close()
		in = f
	}
	links, err := readLinks(in)
	if err != nil {
		return rep.fatal(fmt.Errorf("reading --tracks: %w", err))
	}
	if len(links) == 0 {
		return rep.fatal(errors.New("--tracks: no song links found (select songs in Spotify and press Ctrl+C first)"))
	}

	col := spotify.Collection{Ref: spotify.Ref{Kind: kindList}, Name: "--tracks"}
	if link != "" {
		ref, err := spotify.ParseURL(link)
		if err != nil {
			return rep.fatal(err)
		}
		if ref.Kind == spotify.KindTrack {
			return rep.fatal(errors.New("with --tracks, give a playlist or album link (it names the folder), or no link"))
		}
		name, err := spotify.NewWeb("").Name(ctx, ref)
		if err != nil {
			return rep.fatal(err)
		}
		col.Ref, col.Name = ref, name
	}

	rep.setUI(chooseUI(cfg.Progress, stderr, c.json))
	setupLogging(rep.ui.writer(), c.verbose)
	col.Tracks, err = readList(ctx, cfg, rep, links, col.Ref)
	if err != nil {
		return rep.fatal(err)
	}
	res, err := runDownload(ctx, cfg, rep, col, true)
	return rep.exit(c, res, err, stderr)
}

// readList reads the tracks behind a list of song links, showing progress.
func readList(ctx context.Context, cfg config.Config, rep *reporter, links []string, hintFrom spotify.Ref) ([]spotify.Track, error) {
	steps := newSteps(rep, fmt.Sprintf("Reading %d songs from Spotify", len(links)))
	defer steps.finish()
	return resolveList(ctx, cfg, links, hintFrom, steps.report)
}

// kindList labels a collection read from --tracks without a playlist link:
// it downloads like single tracks (no playlist folder).
const kindList spotify.Kind = "list"

// steps shows the slow metadata steps as progress lines ("spotify" while
// reading tracks, "tags" during Deezer lookups) and reports them as NDJSON
// "reading" events, so a long playlist never looks stuck.
type steps struct {
	rep    *reporter
	labels map[string]string
	phases map[string]phaseUI
}

func newSteps(rep *reporter, readingLabel string) *steps {
	return &steps{
		rep:    rep,
		labels: map[string]string{"spotify": readingLabel, "tags": "Looking up tags on Deezer"},
		phases: map[string]phaseUI{},
	}
}

func (s *steps) show(step string) phaseUI {
	ph, ok := s.phases[step]
	if !ok {
		for _, prev := range s.phases {
			prev.finish()
		}
		ph = s.rep.ui.phase(s.labels[step])
		s.phases[step] = ph
	}
	return ph
}

func (s *steps) report(step string, done, total int) {
	s.show(step).set(done, total)
	s.rep.reading(step, done, total)
}

func (s *steps) finish() {
	for _, ph := range s.phases {
		ph.finish()
	}
}

// prepare loads the config and checks for the tools every download needs.
// The reporter prints plainly until the caller switches to the configured
// display, so config errors come out readable either way.
func prepare(c *cli, stdout, stderr io.Writer) (*reporter, config.Config, error) {
	rep := &reporter{ui: &plainUI{w: stderr}, stderr: stderr, warned: map[string]bool{}}
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

// lookupDeezer resolves a "deezer:<id>" reference (what `geet search --json`
// hands out for Deezer results) or a Deezer track link to a one-track
// collection. Deezer's entry has the ISRC and disc number already.
func lookupDeezer(ctx context.Context, link string) (spotify.Collection, error) {
	id, err := deezer.ParseRef(link)
	if err != nil {
		return spotify.Collection{}, err
	}
	t, err := deezer.New("").Lookup(ctx, id)
	if err != nil {
		return spotify.Collection{}, err
	}
	return spotify.Collection{Ref: spotify.Ref{Kind: spotify.KindTrack, ID: t.ID}, Name: t.Title, Tracks: []spotify.Track{t}}, nil
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

// outcome is how a run's tracks ended.
type outcome struct {
	root     string // where the files went: output, or the playlist's folder
	saved    int    // downloaded
	existing int    // already in the library: the file existed, or a duplicate was reused
	failed   int
}

func (o outcome) total() int { return o.saved + o.existing + o.failed }

var errInterrupted = errors.New("interrupted")

// runDownload takes a resolved collection through the pipeline into the
// library. An error means the whole run stopped; failed tracks only count in
// the outcome. lateTags means ISRC and disc numbers are still to be looked
// up per track (see resolveMetadata).
func runDownload(ctx context.Context, cfg config.Config, rep *reporter, col spotify.Collection, lateTags bool) (outcome, error) {
	var res outcome
	// "auto" and a missing keyring are resolved here, once, for every yt-dlp
	// run that follows (see ytdlp.CookieSource).
	src, err := ytdlp.CookieSource(cfg.YouTube.CookiesFromBrowser, ytdlp.SystemProbes())
	if err != nil {
		return res, fmt.Errorf("youtube.cookies_from_browser: %w", err)
	}
	if src != cfg.YouTube.CookiesFromBrowser {
		slog.DebugContext(ctx, "cookies", "from", src)
	}
	cfg.YouTube.CookiesFromBrowser = src
	ref := col.Ref
	tracks := col.Tracks
	root := cfg.Output
	if ref.Kind == spotify.KindPlaylist && cfg.PlaylistFolder {
		root = filepath.Join(cfg.Output, library.FolderName(col.Name, cfg.PlaylistFolderCase))
	}
	res.root = root
	rep.ui.log("%s %q: %d track(s) → %s", ref.Kind, col.Name, len(tracks), root)

	idx, err := openIndex(ctx, cfg, rep)
	if err != nil {
		return res, err
	}
	d := newDownloader(cfg, root, rep, idx)
	if lateTags {
		d.dz = deezer.New("")
	}
	// Per-track work dirs go inside one run dir in the library, so finished
	// files rename into place atomically and one RemoveAll cleans up even
	// after Ctrl+C.
	if err := os.MkdirAll(root, 0o755); err != nil {
		return res, err
	}
	if d.work, err = os.MkdirTemp(root, ".geet-"); err != nil {
		return res, err
	}
	defer os.RemoveAll(d.work)

	jobs := make([]*trackJob, len(tracks))
	for i, t := range tracks {
		jobs[i] = &trackJob{t: t, ev: event{Track: trackName(t), SpotifyID: t.ID, Index: i + 1, Total: len(tracks)}}
	}
	botChecked := false
	err = pipeline.Run(ctx, jobs, []pipeline.Stage[*trackJob]{
		{Name: "resolve", Workers: cfg.ResolveJobs, Do: d.resolve},
		{Name: "download", Workers: cfg.Jobs, Do: d.download},
		{Name: "tag", Workers: tagWorkers, Do: d.tag},
	}, isFatal, func(j *trackJob, err error) {
		if err == nil {
			if j.ev.Skipped || j.ev.DuplicateOf != "" {
				res.existing++
			} else {
				res.saved++
			}
			return
		}
		res.failed++
		j.ev.Error = err.Error()
		d.emit(j, "failed")
		// Every blocked track fails the same way; say how to fix it once.
		if errors.Is(err, ytdlp.ErrBotCheck) && !botChecked {
			botChecked = true
			rep.notice(ytdlp.BotCheckAdvice(cfg.YouTube.CookiesFromBrowser, err))
		}
		if errors.Is(err, ytdlp.ErrAgeRestricted) {
			d.noticeAge(err)
		}
	})
	if ctx.Err() != nil {
		return res, errInterrupted
	}
	return res, err
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

	ageNoticed atomic.Bool // the age-restriction advice is shown once a run
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
			MusicFallback:   cfg.YouTube.MusicFallback,
			TitleFallback:   cfg.YouTube.TitleFallback,
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
	alts []youtube.Scored // what to try if YouTube age-restricts url
	// cleanEdit is the clean edit's title when the explicit version couldn't
	// be had and the clean one was saved instead.
	cleanEdit string
	titleOnly bool // matched by title and length alone (youtube.Scored.TitleOnly)
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

	best, all, err := d.yt.Resolve(ctx, j.t)
	if err != nil {
		return j, false, err
	}
	j.alts = youtube.Alternatives(j.t, all, best)
	j.titleOnly = best.TitleOnly
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
	if errors.Is(err, ytdlp.ErrAgeRestricted) || errors.Is(err, ytdlp.ErrUnplayable) {
		j.src, err = d.tryOtherUploads(ctx, j, err)
	}
	if err == nil {
		// Display only (not an NDJSON stage): the download slot is free
		// while the track waits for a tagging worker.
		j.tu.stage(event{Stage: "downloaded"})
	}
	return j, false, err
}

// maxAlternatives caps the other uploads tried for one that failed.
const maxAlternatives = 3

// tryOtherUploads gets the song some other way when YouTube won't serve the
// upload chosen for it (age-restricted, no format, removed), in this order:
// the next uploads that passed matching and exact re-uploads
// (youtube.Alternatives); then, for an explicit song, its clean edit,
// found in the Apple catalog. The explicit version is the default and the
// clean edit only a fallback, so the file says so: " (Clean)" in the title
// tag and a warning. cause is returned if nothing works.
//
// A signed-in account that isn't age-verified gets ErrUnplayable, not
// ErrAgeRestricted, for an age-restricted video: yt-dlp's download says
// only that no format is available.
func (d *downloader) tryOtherUploads(ctx context.Context, j *trackJob, cause error) (download.Source, error) {
	why := "unavailable"
	if errors.Is(cause, ytdlp.ErrAgeRestricted) {
		why = "age-restricted"
		d.noticeAge(cause)
	}
	// failed is what to report if nothing works: the first cause, unless a
	// fallback ran into YouTube's bot check, which blocks every upload and
	// has a fix of its own.
	failed := cause
	note := func(err error) {
		if errors.Is(err, ytdlp.ErrBotCheck) {
			failed = err
		}
	}
	try := func(cands []youtube.Scored, what string) (download.Source, bool) {
		for _, c := range cands[:min(len(cands), maxAlternatives)] {
			d.rep.ui.log("%s: %s on YouTube, trying %s %q (%s)", j.ev.Track, why, what, c.Title, c.URL)
			src, err := d.fetch(ctx, c.URL, j.work, j.tu, j.ev)
			if err == nil {
				j.url, j.ev.YouTubeURL = c.URL, c.URL
				return src, true
			}
			if ctx.Err() != nil {
				return download.Source{}, false
			}
			note(err)
			slog.DebugContext(ctx, "alternative failed", "track", j.ev.Track, "url", c.URL, "err", err)
		}
		return download.Source{}, false
	}
	slog.DebugContext(ctx, "upload failed; alternatives", "track", j.ev.Track, "cause", cause, "count", len(j.alts))
	if src, ok := try(j.alts, "another upload"); ok {
		return src, nil
	}
	// The song's own version first: search wider before any clean edit.
	if ctx.Err() == nil && !errors.Is(failed, ytdlp.ErrBotCheck) {
		tried := map[string]bool{youtubeID(j.url): true}
		for _, a := range j.alts[:min(len(j.alts), maxAlternatives)] {
			tried[a.ID] = true
		}
		more, err := d.yt.MoreAlternatives(ctx, j.t, tried)
		note(err)
		slog.DebugContext(ctx, "wider search", "track", j.ev.Track, "count", len(more), "err", err)
		if src, ok := try(more, "another upload"); ok {
			return src, nil
		}
	}
	if !j.t.Explicit || ctx.Err() != nil {
		return download.Source{}, failed
	}

	it := itunes.New("", d.cfg.Search.Country)
	results, err := it.Search(ctx, itunes.CleanEditTerm(j.t))
	if err != nil {
		slog.DebugContext(ctx, "looking for a clean edit", "track", j.ev.Track, "err", err)
		return download.Source{}, failed
	}
	clean, ok := itunes.CleanEdit(j.t, results)
	if !ok {
		slog.DebugContext(ctx, "no clean edit in the Apple catalog", "track", j.ev.Track)
		return download.Source{}, failed
	}
	best, all, err := d.yt.Resolve(ctx, clean)
	if err != nil {
		note(err)
		slog.DebugContext(ctx, "matching the clean edit", "track", j.ev.Track, "clean", clean.Title, "err", err)
		return download.Source{}, failed
	}
	if src, ok := try(append([]youtube.Scored{best}, youtube.Alternatives(clean, all, best)...), "the clean edit"); ok {
		j.cleanEdit = clean.Title
		return src, nil
	}
	return download.Source{}, failed
}

// youtubeID is the video ID in a watch URL.
func youtubeID(u string) string {
	if _, id, ok := strings.Cut(u, "v="); ok {
		id, _, _ = strings.Cut(id, "&")
		return id
	}
	return u
}

// noticeAge shows how to get age-restricted songs, once a run: whether the
// song then failed or came as a clean edit, the fix is the same.
func (d *downloader) noticeAge(err error) {
	if d.ageNoticed.CompareAndSwap(false, true) {
		d.rep.notice(ytdlp.AgeRestrictedAdvice(err))
	}
}

func (d *downloader) tag(ctx context.Context, j *trackJob) (*trackJob, bool, error) {
	defer os.RemoveAll(j.work)
	d.emit(j, "tagging")
	cover, err := audio.FetchCover(ctx, j.t.CoverURL)
	if err != nil {
		slog.WarnContext(ctx, "no cover art", "track", j.ev.Track, "err", err)
	}
	out := filepath.Join(j.work, "out."+d.cfg.Format)
	if j.cleanEdit != "" {
		j.t.Title += " (Clean)"
	}
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
	if j.cleanEdit != "" {
		warn := fmt.Sprintf("YouTube wouldn't serve the explicit version, so this is the clean edit (%q)", j.cleanEdit)
		j.ev.Warning = strings.TrimPrefix(j.ev.Warning+"; "+warn, "; ")
	}
	if j.titleOnly {
		warn := fmt.Sprintf("matched by title and length only: the YouTube upload (%s) doesn't name the artist", j.url)
		j.ev.Warning = strings.TrimPrefix(j.ev.Warning+"; "+warn, "; ")
	}
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
	src, ok := d.idx.Lookup(t.ID, t.ISRC, d.cfg.Format, filepath.Dir(dest))
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
		if err == nil || ctx.Err() != nil || errors.Is(err, ytdlp.ErrToolMissing) || errors.Is(err, ytdlp.ErrBotCheck) || errors.Is(err, ytdlp.ErrAgeRestricted) || errors.Is(err, ytdlp.ErrUnplayable) {
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
