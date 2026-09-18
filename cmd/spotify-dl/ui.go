package main

import (
	"fmt"
	"io"
	"os"
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
	u.log("[%d/%d] %s", index, total, name)
	return plainTrack{u}
}

func (u *plainUI) log(format string, args ...any) {
	u.mu.Lock()
	defer u.mu.Unlock()
	fmt.Fprintf(u.w, format+"\n", args...)
}

func (u *plainUI) writer() io.Writer { return u.w }
func (u *plainUI) close(bool)        {}

type plainTrack struct{ u *plainUI }

func (t plainTrack) stage(e event) {
	switch e.Stage {
	case "resolved":
		t.u.log("       match  %s", e.YouTubeURL)
	case "done":
		if e.Skipped {
			t.u.log("       exists %s", e.Path)
		} else {
			t.u.log("       saved  %s", e.Path)
		}
	case "failed":
		t.u.log("       ✗ %s", e.Error)
	}
}

func (plainTrack) progress(int64, int64) {}

// barUI draws one animated line per track in progress with mpb. A finished
// track's bar is removed and replaced by a permanent result line printed
// above the live area: mpb never draws a bar that completes before its first
// refresh (an existing file, an instant failure), and this way every track
// still leaves exactly one line behind.
type barUI struct {
	p     *mpb.Progress
	color bool
}

const (
	barTotal   = 1000
	barDownEnd = 990 // download fills up to here; the rest is tagging
	nameWidth  = 42
	errWidth   = 70
)

var spinner = []string{"⠋", "⠙", "⠹", "⠸", "⠼", "⠴", "⠦", "⠧", "⠇", "⠏"}

func newBarUI(w io.Writer) *barUI {
	return &barUI{
		p: mpb.New(
			mpb.WithOutput(w),
			mpb.WithWidth(24),
			mpb.WithRefreshRate(100*time.Millisecond),
		),
		color: os.Getenv("NO_COLOR") == "",
	}
}

func (u *barUI) paint(code, s string) string {
	if !u.color {
		return s
	}
	return "\x1b[" + code + "m" + s + "\x1b[0m"
}

func (u *barUI) track(index, total int, name string) trackUI {
	label := fmt.Sprintf("[%*d/%d] %s", len(fmt.Sprint(total)), index, total, fit(name, nameWidth))
	t := &barTrack{ui: u, label: label, started: time.Now()}
	style := mpb.BarStyle().Lbound("").Rbound("").Filler("━").Tip("━").Padding("─").
		FillerMeta(func(s string) string { return u.paint("32", s) }).
		TipMeta(func(s string) string { return u.paint("32", s) }).
		PaddingMeta(func(s string) string { return u.paint("2", s) })
	t.bar = u.p.New(barTotal, style,
		mpb.PrependDecorators(decor.Name(label, decor.WCSyncSpaceR)),
		mpb.AppendDecorators(decor.Any(t.status, decor.WC{C: decor.DindentRight})),
		mpb.BarRemoveOnComplete(),
	)
	return t
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
			return u.paint("36", spinner[int(time.Since(ph.started)/(80*time.Millisecond))%len(spinner)]) + " " + label
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

func (u *barUI) close(abort bool) {
	if abort {
		u.p.Shutdown()
		return
	}
	u.p.Wait()
}

type barTrack struct {
	ui      *barUI
	bar     *mpb.Bar
	label   string
	started time.Time

	mu          sync.Mutex
	stageName   string
	done, total int64
}

func (t *barTrack) status(decor.Statistics) string {
	t.mu.Lock()
	defer t.mu.Unlock()
	spin := t.ui.paint("36", spinner[int(time.Since(t.started)/(80*time.Millisecond))%len(spinner)])
	switch t.stageName {
	case "", "resolved":
		return spin + " finding on YouTube"
	case "downloading":
		if t.total <= 0 {
			return spin + " downloading"
		}
		return fmt.Sprintf("%s downloading %3d%%  %s / %s", spin, t.done*100/t.total, mib(t.done), mib(t.total))
	case "tagging":
		return spin + " tagging & cover art"
	}
	return "" // done or failed: the bar is on its way out
}

// stage never calls into the bar while holding t.mu: mpb's render goroutine
// calls status, which takes t.mu, so doing both would deadlock.
func (t *barTrack) stage(e event) {
	t.mu.Lock()
	t.stageName = e.Stage
	t.mu.Unlock()

	switch e.Stage {
	case "tagging":
		t.bar.SetCurrent(barDownEnd)
	case "done":
		if e.Skipped {
			t.ui.log("%s %s", t.label, t.ui.paint("2", "• already downloaded"))
		} else {
			t.ui.log("%s %s", t.label, t.ui.paint("32", "✓ saved"))
		}
		t.bar.SetTotal(barTotal, true)
	case "failed":
		t.ui.log("%s %s", t.label, t.ui.paint("31", "✗ "+runewidth.Truncate(e.Error, errWidth, "…")))
		t.bar.Abort(true)
	}
}

func (t *barTrack) progress(done, total int64) {
	t.mu.Lock()
	t.done, t.total = done, total
	t.mu.Unlock()
	if total > 0 {
		t.bar.SetCurrent(min(done, total) * barDownEnd / total)
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
