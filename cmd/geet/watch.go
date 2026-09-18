package main

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
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
	"time"

	"github.com/sumdahl/geet/internal/audio"
	"github.com/sumdahl/geet/internal/clipboard"
	"github.com/sumdahl/geet/internal/config"
	"github.com/sumdahl/geet/internal/deezer"
	"github.com/sumdahl/geet/internal/itunes"
	"github.com/sumdahl/geet/internal/notify"
	"github.com/sumdahl/geet/internal/spotify"
)

// queueSize caps links waiting behind the one downloading. Copying more
// than this in one sitting is almost certainly not meant as a request.
const queueSize = 32

// watchJob is one download started by a copy: a link, or several song
// links copied together (Spotify's Ctrl+C on selected songs).
type watchJob struct {
	id     int
	source string   // the link, or the first of links
	link   string   // a single link
	links  []string // song links copied together; set instead of link
}

// clipboardJobs finds the downloads in copied text: each album or playlist
// link is its own job, and song links make one job together, placed where
// the first of them was. Text with no link in it gives none.
func clipboardJobs(text string) []watchJob {
	var jobs []watchJob
	songs := -1 // index in jobs of the song job
	for _, f := range strings.Fields(text) {
		// Links pasted into prose come wrapped in brackets or followed by
		// punctuation.
		f = strings.Trim(f, `()[]<>"'.,;`)
		kind := spotify.KindTrack
		if !itunes.IsRef(f) && !deezer.IsRef(f) {
			ref, err := spotify.ParseURL(f)
			if err != nil {
				continue
			}
			kind = ref.Kind
		}
		if kind != spotify.KindTrack {
			jobs = append(jobs, watchJob{source: f, link: f})
			continue
		}
		if songs < 0 {
			songs = len(jobs)
			jobs = append(jobs, watchJob{source: f})
		}
		if !slices.Contains(jobs[songs].links, f) {
			jobs[songs].links = append(jobs[songs].links, f)
		}
	}
	if songs >= 0 && len(jobs[songs].links) == 1 {
		jobs[songs].link, jobs[songs].links = jobs[songs].links[0], nil
	}
	return jobs
}

type watcher struct {
	c        *cli
	cfg      config.Config
	rep      *reporter
	stderr   io.Writer
	notifier *notify.Notifier // nil: notifications off
	covers   string           // cache dir for cover art shown in notifications
	jobs     chan watchJob
	seq      int // last job id; only the clipboard goroutine touches it
}

func watchCmd(ctx context.Context, args []string, stdout, stderr io.Writer) int {
	c := newCLI("watch", "watch [flags]", stderr)
	positional, err := c.parse(args)
	if errors.Is(err, flag.ErrHelp) {
		return exitOK
	}
	if err != nil {
		return exitFatal
	}
	if len(positional) > 0 {
		c.fs.Usage()
		return exitFatal
	}
	rep, cfg, err := prepare(c, stdout, stderr)
	if err != nil {
		return rep.fatal(err)
	}
	// Fail now, not after the first copy: no wl-paste, or no Wayland session.
	if _, err := clipboard.Read(ctx, cfg.Tools.WlPaste); err != nil {
		return rep.fatal(err)
	}

	w := &watcher{c: c, cfg: cfg, rep: rep, stderr: stderr, jobs: make(chan watchJob, queueSize)}
	if cfg.Watch.Notify {
		if _, err := exec.LookPath(cfg.Tools.NotifySend); err != nil {
			slog.Warn("notify-send not found, so no notifications: install libnotify or set tools.notify_send", "tools.notify_send", cfg.Tools.NotifySend)
		} else {
			w.notifier = &notify.Notifier{Bin: cfg.Tools.NotifySend, App: "geet"}
		}
	}
	if dir, err := os.UserCacheDir(); err == nil {
		w.covers = filepath.Join(dir, "geet", "covers")
	}
	return w.run(ctx)
}

// run downloads copied links one at a time, in the order they were copied,
// until ctx ends (Ctrl+C or SIGTERM, which is how a daemon is stopped, so
// it exits 0).
func (w *watcher) run(ctx context.Context) int {
	ctx, cancel := context.WithCancel(ctx)
	defer cancel()
	errc := make(chan error, 1)
	go func() { errc <- clipboard.Watch(ctx, w.cfg.Tools.WlPaste, w.cfg.Watch.Interval.Duration, w.copied) }()
	w.rep.ui.log("Watching the clipboard: copy a Spotify link to download it. Ctrl+C stops.")

	for {
		select {
		case err := <-errc:
			if err != nil {
				return w.rep.fatal(err)
			}
			return exitOK
		case j := <-w.jobs:
			if ctx.Err() != nil {
				continue // stopping: wait for the clipboard goroutine
			}
			w.process(ctx, j)
			if ctx.Err() == nil {
				w.rep.ui.log("Watching the clipboard.")
			}
		}
	}
}

// copied queues the downloads in newly copied text. It runs on the
// clipboard goroutine while a download may be in progress.
func (w *watcher) copied(text string) {
	for _, j := range clipboardJobs(text) {
		w.seq++
		j.id = w.seq
		w.rep.mu.Lock()
		w.rep.write(event{Stage: "queued", Job: j.id, Source: j.source})
		u := w.rep.ui
		w.rep.mu.Unlock()
		select {
		case w.jobs <- j:
			u.log("Copied %s", describeJob(j))
		default:
			w.rep.mu.Lock()
			w.rep.write(event{Stage: "finished", Job: j.id, Source: j.source, Error: "too many links waiting; copy it again later"})
			w.rep.mu.Unlock()
			u.log("warning: %d links are already waiting, so %s is ignored", queueSize, j.source)
		}
	}
}

func describeJob(j watchJob) string {
	if j.links != nil {
		return fmt.Sprintf("%d song links", len(j.links))
	}
	return j.link
}

// process downloads one job and reports how it ended, as a "finished"
// event and a notification. A failure ends only this job, never the daemon.
func (w *watcher) process(ctx context.Context, j watchJob) {
	w.rep.mu.Lock()
	w.rep.job, w.rep.source = j.id, j.source
	w.rep.warned = map[string]bool{}
	w.rep.mu.Unlock()
	u := chooseUI(w.cfg.Progress, w.stderr, w.c.json)
	w.rep.setUI(u)
	setupLogging(u.writer(), w.c.verbose)

	var col spotify.Collection
	var lateTags bool
	var err error
	if j.links != nil {
		col = spotify.Collection{Ref: spotify.Ref{Kind: kindList}, Name: fmt.Sprintf("%d copied songs", len(j.links))}
		col.Tracks, err = readList(ctx, w.cfg, w.rep, j.links)
		lateTags = true
	} else {
		col, lateTags, err = readLink(ctx, w.cfg, w.rep, j.link)
	}

	var res outcome
	var cover string
	var id uint32
	if err == nil {
		cover = w.cover(ctx, col)
		id = w.notify(ctx, startMessage(col, cover))
		res, err = runDownload(ctx, w.cfg, w.rep, col, lateTags)
	}
	if err != nil && ctx.Err() != nil {
		err = errInterrupted
	}

	u.close(err != nil)
	setupLogging(w.stderr, w.c.verbose)
	w.rep.setUI(&plainUI{w: w.stderr})

	e := event{Stage: "finished", Name: collectionTitle(col), Total: len(col.Tracks), Path: res.root,
		Counts: &counts{Saved: res.saved, Existing: res.existing, Failed: res.failed}}
	if err != nil {
		e.Error = err.Error()
	}
	w.rep.mu.Lock()
	w.rep.write(e)
	w.rep.job, w.rep.source = 0, ""
	w.rep.mu.Unlock()
	w.rep.ui.log("%s", finishedLine(j, col, res, err))

	msg := finishMessage(j, col, res, err)
	if msg.Icon == "" {
		msg.Icon = cover
	}
	msg.Replaces = id
	// The summary still matters when the download was stopped by Ctrl+C.
	sendCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 10*time.Second)
	defer cancel()
	w.notify(sendCtx, msg)
}

func (w *watcher) notify(ctx context.Context, m notify.Message) uint32 {
	if w.notifier == nil {
		return 0
	}
	id, err := w.notifier.Send(ctx, m)
	if err != nil && ctx.Err() == nil {
		slog.WarnContext(ctx, "notification failed", "err", err)
	}
	return id
}

// cover saves the collection's cover art for notify-send, which only takes
// a file. Covers are cached by URL: an album's is the same for every song.
func (w *watcher) cover(ctx context.Context, col spotify.Collection) string {
	if w.notifier == nil || w.covers == "" || len(col.Tracks) == 0 || col.Tracks[0].CoverURL == "" {
		return ""
	}
	url := col.Tracks[0].CoverURL
	sum := sha256.Sum256([]byte(url))
	path := filepath.Join(w.covers, hex.EncodeToString(sum[:8])+".jpg")
	if _, err := os.Stat(path); err == nil {
		return path
	}
	img, err := audio.FetchCover(ctx, url)
	if err == nil {
		err = writeFileAtomic(path, img)
	}
	if err != nil {
		slog.DebugContext(ctx, "no cover for the notification", "err", err)
		return ""
	}
	return path
}

func writeFileAtomic(path string, data []byte) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	f, err := os.CreateTemp(filepath.Dir(path), ".cover-")
	if err != nil {
		return err
	}
	defer os.Remove(f.Name())
	_, err = f.Write(data)
	if cerr := f.Close(); err == nil {
		err = cerr
	}
	if err != nil {
		return err
	}
	return os.Rename(f.Name(), path)
}

// collectionTitle names what was copied: "Artists - Title" for one song,
// otherwise the album or playlist name.
func collectionTitle(col spotify.Collection) string {
	if col.Ref.Kind == spotify.KindTrack && len(col.Tracks) == 1 {
		return trackName(col.Tracks[0])
	}
	return col.Name
}

func songs(n int) string {
	if n == 1 {
		return "1 song"
	}
	return fmt.Sprintf("%d songs", n)
}

func startMessage(col spotify.Collection, cover string) notify.Message {
	body := collectionTitle(col)
	if col.Ref.Kind != spotify.KindTrack {
		body += " · " + songs(len(col.Tracks))
	}
	return notify.Message{Summary: "Downloading", Body: body, Icon: cover, Urgency: notify.Low}
}

func finishMessage(j watchJob, col spotify.Collection, res outcome, err error) notify.Message {
	title := collectionTitle(col)
	if title == "" {
		title = j.source
	}
	switch {
	case errors.Is(err, errInterrupted):
		return notify.Message{Summary: "Download stopped", Body: title + " · " + progressSummary(res), Urgency: notify.Normal}
	case err != nil:
		return notify.Message{Summary: "Download failed", Body: title + "\n" + err.Error(), Icon: "dialog-error", Urgency: notify.Critical}
	case res.total() == 0:
		return notify.Message{Summary: "Nothing to download", Body: title + " has no songs", Urgency: notify.Normal}
	case res.failed == res.total():
		return notify.Message{Summary: "Download failed", Body: title + " · " + progressSummary(res), Icon: "dialog-error", Urgency: notify.Critical}
	case res.failed > 0:
		return notify.Message{Summary: "Downloaded with failures", Body: title + " · " + progressSummary(res), Urgency: notify.Normal}
	case res.saved == 0:
		return notify.Message{Summary: "Already in your library", Body: title, Urgency: notify.Low}
	case col.Ref.Kind == spotify.KindTrack:
		return notify.Message{Summary: "Saved", Body: title, Urgency: notify.Normal}
	}
	return notify.Message{Summary: "Saved", Body: title + " · " + progressSummary(res), Urgency: notify.Normal}
}

// progressSummary is "12 saved, 3 already there, 1 failed", leaving out
// zero counts.
func progressSummary(res outcome) string {
	var parts []string
	if res.saved > 0 {
		parts = append(parts, fmt.Sprintf("%d saved", res.saved))
	}
	if res.existing > 0 {
		parts = append(parts, fmt.Sprintf("%d already there", res.existing))
	}
	if res.failed > 0 {
		parts = append(parts, fmt.Sprintf("%d failed", res.failed))
	}
	if len(parts) == 0 {
		return "nothing done"
	}
	return strings.Join(parts, ", ")
}

func finishedLine(j watchJob, col spotify.Collection, res outcome, err error) string {
	title := collectionTitle(col)
	if title == "" {
		title = j.source
	}
	if err != nil {
		return fmt.Sprintf("✗ %s: %v", title, err)
	}
	return fmt.Sprintf("✓ %s: %s → %s", title, progressSummary(res), res.root)
}
