package main

import (
	"fmt"
	"io"
	"math"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/mattn/go-runewidth"
	"github.com/vbauerster/mpb/v8"
	"github.com/vbauerster/mpb/v8/decor"
	"golang.org/x/term"
)

// ui is the human-facing progress display on stderr. The NDJSON stream is
// separate (reporter) and never depends on which ui is active.
type ui interface {
	phase(label string) phaseUI // a step before downloads start, e.g. reading a playlist
	track(index, total int, name string) trackUI
	log(format string, args ...any) // a line above any live progress
	highlight(s string) string      // emphasis for warnings, where the display supports it
	writer() io.Writer              // for slog, so logs don't tear the bars
	close(abort bool)
}

type phaseUI interface {
	set(done, total int)
	finish()
}

type trackUI interface {
	stage(e event)
	progress(done, total int64)
}

// chooseUI picks animated bars only where they can render: a terminal, and
// not while --json output may be sharing that terminal.
func chooseUI(mode string, stderr io.Writer, jsonOut bool) ui {
	animate := mode == "always"
	if mode == "auto" {
		f, ok := stderr.(*os.File)
		animate = ok && term.IsTerminal(int(f.Fd())) && os.Getenv("TERM") != "dumb" && !jsonOut
	}
	if animate {
		return newBarUI(stderr)
	}
	return &plainUI{w: stderr}
}

// plainUI prints one line per event: the format for logs, pipes and the
// plugin's stderr.
type plainUI struct {
	mu sync.Mutex
	w  io.Writer
}

func (u *plainUI) phase(label string) phaseUI {
	u.log("%s…", label)
	return plainPhase{}
}

type plainPhase struct{}

func (plainPhase) set(int, int) {}
func (plainPhase) finish()      {}

func (u *plainUI) track(index, total int, name string) trackUI {
	// Tracks run concurrently, so every line carries its track number.
	t := plainTrack{u: u, tag: fmt.Sprintf("[%*d/%d]", len(fmt.Sprint(total)), index, total)}
	u.log("%s %s", t.tag, name)
	return t
}

func (u *plainUI) log(format string, args ...any) {
	u.mu.Lock()
	defer u.mu.Unlock()
	fmt.Fprintf(u.w, format+"\n", args...)
}

func (u *plainUI) writer() io.Writer         { return u.w }
func (u *plainUI) highlight(s string) string { return s }
func (u *plainUI) close(bool)                {}

type plainTrack struct {
	u   *plainUI
	tag string
}

func (t plainTrack) stage(e event) {
	switch e.Stage {
	case "resolved":
		t.u.log("%s match  %s", t.tag, e.YouTubeURL)
	case "done":
		switch {
		case e.DuplicateOf != "" && e.Skipped:
			t.u.log("%s have   %s (not added here: duplicates = skip)", t.tag, e.DuplicateOf)
		case e.DuplicateOf != "" && e.Linked:
			t.u.log("%s linked %s (same file as %s)", t.tag, e.Path, e.DuplicateOf)
		case e.DuplicateOf != "":
			t.u.log("%s copied %s (from %s)", t.tag, e.Path, e.DuplicateOf)
		case e.Skipped:
			t.u.log("%s exists %s", t.tag, e.Path)
		default:
			t.u.log("%s saved  %s", t.tag, e.Path)
		}
	case "failed":
		t.u.log("%s ✗ %s", t.tag, e.Error)
	}
}

func (plainTrack) progress(int64, int64) {}

// barUI draws animated progress with mpb.
//
// A finished track's bar is removed and replaced by a permanent result line
// printed above the live area: mpb never draws a bar that completes before
// its first refresh (an existing file, an instant failure), and this way
// every track still leaves exactly one line behind.
//
// In compact mode (runs of more than compactOver tracks) only tracks that
// are downloading or tagging (or waiting between the two) get a bar; the rest (still being found on
// YouTube, or queued for a download slot) are counted on one summary line at
// the bottom. With 16 downloads and 24 lookups at once, a bar per track
// would be taller than most terminals.
type barUI struct {
	p       *mpb.Progress
	color   bool
	started time.Time

	mu      sync.Mutex
	total   int
	counts  map[string]int // tracks per state: finding, queued, downloading, tagging, done, failed
	summary *mpb.Bar       // compact mode only
}

const (
	barTotal    = 1000
	barDownEnd  = 990 // download fills up to here; the rest is tagging
	nameWidth   = 42
	errWidth    = 70
	compactOver = 8
)

var spinner = []string{"⠋", "⠙", "⠹", "⠸", "⠼", "⠴", "⠦", "⠧", "⠇", "⠏"}

func newBarUI(w io.Writer) *barUI {
	return &barUI{
		p: mpb.New(
			mpb.WithOutput(w),
			mpb.WithWidth(24),
			mpb.WithRefreshRate(100*time.Millisecond),
		),
		color:   os.Getenv("NO_COLOR") == "",
		started: time.Now(),
		counts:  map[string]int{},
	}
}

func (u *barUI) paint(code, s string) string {
	if !u.color {
		return s
	}
	return "\x1b[" + code + "m" + s + "\x1b[0m"
}

func (u *barUI) spin(since time.Time) string {
	return u.paint("36", spinner[int(time.Since(since)/(80*time.Millisecond))%len(spinner)])
}

func (u *barUI) track(index, total int, name string) trackUI {
	label := fmt.Sprintf("[%*d/%d] %s", len(fmt.Sprint(total)), index, total, fit(name, nameWidth))
	t := &barTrack{ui: u, label: label, started: time.Now(), state: "finding"}

	u.mu.Lock()
	u.counts["finding"]++
	compact := total > compactOver
	startSummary := compact && u.summary == nil && u.total == 0
	u.total = total
	u.mu.Unlock()

	if startSummary {
		// Highest priority: mpb draws it below every track bar.
		bar := u.p.New(0, mpb.NopStyle(),
			mpb.PrependDecorators(decor.Any(u.summaryLine)),
			mpb.BarPriority(math.MaxInt32),
		)
		u.mu.Lock()
		u.summary = bar
		u.mu.Unlock()
	}
	if !compact {
		t.showBar()
	}
	return t
}

// summaryLine is the compact mode's bottom line, e.g.
// "⠹ 20 finding on YouTube · 7 queued · 16 downloading · 12/48 done".
func (u *barUI) summaryLine(decor.Statistics) string {
	u.mu.Lock()
	defer u.mu.Unlock()
	var parts []string
	add := func(n int, what string) {
		if n > 0 {
			parts = append(parts, fmt.Sprintf("%d %s", n, what))
		}
	}
	add(u.counts["finding"], "finding on YouTube")
	add(u.counts["queued"], "queued")
	add(u.counts["downloading"], "downloading")
	add(u.counts["waiting"], "waiting to tag")
	add(u.counts["tagging"], "tagging")
	parts = append(parts, fmt.Sprintf("%d/%d done", u.counts["done"], u.total))
	if n := u.counts["failed"]; n > 0 {
		parts = append(parts, u.paint("31", fmt.Sprintf("%d failed", n)))
	}
	return u.spin(u.started) + " " + strings.Join(parts, u.paint("2", " · "))
}

func (u *barUI) move(from, to string) {
	u.mu.Lock()
	u.counts[from]--
	u.counts[to]++
	u.mu.Unlock()
}

func (u *barUI) phase(label string) phaseUI {
	ph := &barPhase{started: time.Now()}
	style := mpb.BarStyle().Lbound("").Rbound("").Filler("━").Tip("━").Padding("─").
		FillerMeta(func(s string) string { return u.paint("36", s) }).
		TipMeta(func(s string) string { return u.paint("36", s) }).
		PaddingMeta(func(s string) string { return u.paint("2", s) })
	// Total is unknown until the first set; the spinner shows life meanwhile.
	ph.bar = u.p.New(0, style,
		mpb.PrependDecorators(decor.Any(func(decor.Statistics) string {
			return u.spin(ph.started) + " " + label
		}, decor.WCSyncSpaceR)),
		mpb.AppendDecorators(decor.Any(func(s decor.Statistics) string {
			if s.Total <= 0 {
				return ""
			}
			return fmt.Sprintf("%d/%d", s.Current, s.Total)
		})),
		mpb.BarRemoveOnComplete(),
	)
	return ph
}

type barPhase struct {
	bar     *mpb.Bar
	started time.Time
}

func (p *barPhase) set(done, total int) {
	p.bar.SetTotal(int64(total), false)
	p.bar.SetCurrent(int64(done))
}

func (p *barPhase) finish() {
	if !p.bar.Completed() {
		p.bar.Abort(true)
	}
}

func (u *barUI) log(format string, args ...any) {
	fmt.Fprintf(u.p, format+"\n", args...)
}

func (u *barUI) writer() io.Writer { return u.p }

func (u *barUI) highlight(s string) string { return u.paint("33", s) }

func (u *barUI) close(abort bool) {
	if abort {
		u.p.Shutdown()
		return
	}
	// The summary never completes on its own; Wait would block on it.
	u.mu.Lock()
	summary := u.summary
	u.mu.Unlock()
	if summary != nil {
		summary.Abort(true)
	}
	u.p.Wait()
}

type barTrack struct {
	ui      *barUI
	label   string
	started time.Time

	mu          sync.Mutex
	bar         *mpb.Bar // nil until shown
	state       string   // finding, queued, downloading, tagging, done, failed
	done, total int64
}

// showBar gives the track its animated line, once.
func (t *barTrack) showBar() *mpb.Bar {
	t.mu.Lock()
	bar := t.bar
	t.mu.Unlock()
	if bar != nil {
		return bar
	}
	u := t.ui
	style := mpb.BarStyle().Lbound("").Rbound("").Filler("━").Tip("━").Padding("─").
		FillerMeta(func(s string) string { return u.paint("32", s) }).
		TipMeta(func(s string) string { return u.paint("32", s) }).
		PaddingMeta(func(s string) string { return u.paint("2", s) })
	bar = u.p.New(barTotal, style,
		mpb.PrependDecorators(decor.Name(t.label, decor.WCSyncSpaceR)),
		mpb.AppendDecorators(decor.Any(t.status, decor.WC{C: decor.DindentRight})),
		mpb.BarRemoveOnComplete(),
	)
	t.mu.Lock()
	t.bar = bar
	t.mu.Unlock()
	return bar
}

func (t *barTrack) status(decor.Statistics) string {
	t.mu.Lock()
	defer t.mu.Unlock()
	spin := t.ui.spin(t.started)
	switch t.state {
	case "finding":
		return spin + " finding on YouTube"
	case "queued":
		return t.ui.paint("2", "· queued for download")
	case "downloading":
		if t.total <= 0 {
			return spin + " downloading"
		}
		return fmt.Sprintf("%s downloading %3d%%  %s / %s", spin, t.done*100/t.total, mib(t.done), mib(t.total))
	case "waiting":
		return t.ui.paint("2", "· downloaded, waiting to tag")
	case "tagging":
		return spin + " tagging & cover art"
	}
	return "" // done or failed: the bar is on its way out
}

var stateOf = map[string]string{
	"resolved": "queued", "downloading": "downloading", "downloaded": "waiting",
	"tagging": "tagging", "done": "done", "failed": "failed",
}

// stage never calls into a bar or the UI's counters while holding t.mu:
// mpb's render goroutine calls status and summaryLine, which take those
// locks, so holding one while waiting on a bar would deadlock.
func (t *barTrack) stage(e event) {
	next, ok := stateOf[e.Stage]
	if !ok {
		return
	}
	t.mu.Lock()
	prev := t.state
	t.state = next
	bar := t.bar
	t.mu.Unlock()
	if prev == next {
		return // repeated "downloading" progress events
	}
	t.ui.move(prev, next)

	switch next {
	case "downloading":
		t.showBar()
	case "tagging":
		t.showBar().SetCurrent(barDownEnd)
	case "done":
		from := filepath.Base(filepath.Dir(e.DuplicateOf))
		switch {
		case e.DuplicateOf != "" && e.Skipped:
			t.ui.log("%s %s", t.label, t.ui.paint("2", "• already have it in "+from))
		case e.DuplicateOf != "" && e.Linked:
			t.ui.log("%s %s", t.label, t.ui.paint("36", "⧉ linked from "+from+" (no download)"))
		case e.DuplicateOf != "":
			t.ui.log("%s %s", t.label, t.ui.paint("36", "⧉ copied from "+from+" (no download)"))
		case e.Skipped:
			t.ui.log("%s %s", t.label, t.ui.paint("2", "• already downloaded"))
		default:
			t.ui.log("%s %s", t.label, t.ui.paint("32", "✓ saved"))
		}
		if bar != nil {
			bar.SetTotal(barTotal, true)
		}
	case "failed":
		t.ui.log("%s %s", t.label, t.ui.paint("31", "✗ "+runewidth.Truncate(e.Error, errWidth, "…")))
		if bar != nil {
			bar.Abort(true)
		}
	}
}

func (t *barTrack) progress(done, total int64) {
	t.mu.Lock()
	t.done, t.total = done, total
	bar := t.bar
	t.mu.Unlock()
	if bar != nil && total > 0 {
		bar.SetCurrent(min(done, total) * barDownEnd / total)
	}
}

// fit pads or truncates s to exactly width terminal cells, so columns line
// up even with wide (CJK) or combining (Devanagari) characters.
func fit(s string, width int) string {
	s = strings.Join(strings.Fields(s), " ")
	if runewidth.StringWidth(s) > width {
		s = runewidth.Truncate(s, width, "…")
	}
	return runewidth.FillRight(s, width)
}

func mib(n int64) string {
	return fmt.Sprintf("%.1f MiB", float64(n)/(1<<20))
}
