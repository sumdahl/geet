package tui

import (
	"fmt"
	"strings"
	"time"

	"github.com/charmbracelet/lipgloss"

	"github.com/sumdahl/geet/internal/player/spectrum"
)

// The palette follows the terminal's own colours: geet never fights a
// user's theme. Only the accent is chosen, and it is one colour, used
// sparingly.
var (
	accent    = lipgloss.AdaptiveColor{Light: "63", Dark: "111"}
	dim       = lipgloss.AdaptiveColor{Light: "245", Dark: "243"}
	titleSt   = lipgloss.NewStyle().Bold(true)
	subSt     = lipgloss.NewStyle().Foreground(dim)
	accentSt  = lipgloss.NewStyle().Foreground(accent)
	currentSt = lipgloss.NewStyle().Foreground(accent).Bold(true)
	noteSt    = lipgloss.NewStyle().Foreground(lipgloss.AdaptiveColor{Light: "130", Dark: "214"})
)

// Layout constants. The screen is mostly air: one blank line between
// sections, a two-column gutter at the edges, and nothing packed tight.
const (
	gutter       = 2
	minVisWidth  = 34 // below this, a spectrum is a smear, so it goes
	minLyrWidth  = 26
	noteLifetime = 6 * time.Second
)

func (m *Model) View() string {
	if m.quitting {
		return ""
	}
	if m.width < 20 || m.height < 8 {
		return "geet needs a slightly larger window\n"
	}
	inner := m.width - 2*gutter

	var b strings.Builder
	b.WriteString("\n")
	b.WriteString(pad(m.header(inner)))
	b.WriteString("\n")
	b.WriteString(pad(m.progress(inner)))
	b.WriteString("\n")

	if note := m.noteLine(inner); note != "" {
		b.WriteString(pad(note))
		b.WriteString("\n")
	}

	body := m.body(inner, m.bodyHeight())
	if body != "" {
		b.WriteString(pad(body))
		b.WriteString("\n")
	}
	b.WriteString(pad(m.footer(inner)))
	return b.String()
}

// bodyHeight is what is left for the spectrum and lyrics once the fixed
// furniture has its rows. The body only appears when there is real room
// for it: a short window shows the song and its progress, and nothing that
// would be cramped.
func (m *Model) bodyHeight() int {
	used := 1 + 2 + 1 + 1 + 2 + 1 // top air, header, progress, footer air, footer, tail
	if m.noteLine(1) != "" {
		used += 2
	}
	h := m.height - used
	if h < 3 {
		return 0
	}
	if h > 18 {
		h = 18
	}
	return h
}

func (m *Model) header(width int) string {
	it := m.items[m.idx]
	right := m.timeLabel()
	title := Truncate(it.Track.Title, max(1, width-Width(right)-2), "…")
	if it.Track.Title == "" {
		title = Truncate(it.Name(), max(1, width-Width(right)-2), "…")
	}

	line1 := lipgloss.JoinHorizontal(lipgloss.Top,
		titleSt.Render(title),
		strings.Repeat(" ", max(1, width-Width(title)-Width(right))),
		accentSt.Render(right),
	)

	sub := strings.Join(it.Track.Artists, ", ")
	if it.Track.Album != "" {
		if sub != "" {
			sub += "  ·  "
		}
		sub += it.Track.Album
	}
	if sub == "" {
		sub = shortPath(it.Path)
	}
	return line1 + "\n" + subSt.Render(Truncate(sub, width, "…"))
}

// timeLabel is the state and the clock: "▶  1:42 / 5:12".
func (m *Model) timeLabel() string {
	icon := "▶"
	if !m.status.Playing {
		icon = "❚❚"
	}
	pos := clock(m.status.Position)
	if m.status.Duration > 0 {
		return fmt.Sprintf("%s  %s / %s", icon, pos, clock(m.status.Duration))
	}
	return fmt.Sprintf("%s  %s", icon, pos)
}

func (m *Model) progress(width int) string {
	frac := 0.0
	if m.status.Duration > 0 {
		frac = float64(m.status.Position) / float64(m.status.Duration)
	}
	frac = clamp01(frac)
	done := int(frac*float64(width) + 0.5)
	return accentSt.Render(strings.Repeat("━", done)) +
		subSt.Render(strings.Repeat("─", max(0, width-done)))
}

func (m *Model) noteLine(width int) string {
	if m.note == "" || time.Since(m.noteAt) > noteLifetime {
		return ""
	}
	return noteSt.Render(Truncate(m.note, width, "…"))
}

// body lays out the spectrum and the lyrics. Either can have the whole
// width to itself: a song with no lyrics gets a wide spectrum rather than
// an empty box, which is the difference between a screen that looks
// finished and one that looks broken.
func (m *Model) body(width, height int) string {
	if height <= 0 {
		return ""
	}
	wantVis := m.showVis && len(m.levels) > 0
	wantLyr := m.showLyr && m.lyrState != lyricsIdle

	switch {
	case wantVis && wantLyr && m.lyricsAreComplex() && height >= 7:
		// Devanagari and its relatives cannot be trusted to stay inside a
		// fixed-width column (see width.go), so they get the full width and
		// the spectrum moves above them. Overlapping text is unreadable;
		// a shorter spectrum is not.
		visH := height / 3
		if visH < 2 {
			visH = 2
		}
		return m.visualizer(width, visH) + "\n\n" + m.lyricsPane(width, height-visH-1)

	case wantVis && wantLyr && width >= minVisWidth+minLyrWidth+4:
		visW := width/2 - 2
		lyrW := width - visW - 4
		left := m.visualizer(visW, height)
		right := m.lyricsPane(lyrW, height)
		return lipgloss.JoinHorizontal(lipgloss.Top,
			lipgloss.NewStyle().Width(visW).Render(left),
			"    ",
			lipgloss.NewStyle().Width(lyrW).Render(right),
		)
	case wantLyr && m.lyrState == lyricsReady:
		// Words beat bars when there is only room for one.
		return m.lyricsPane(width, height)
	case wantVis:
		return m.visualizer(width, height)
	case wantLyr:
		return m.lyricsPane(width, height)
	}
	return ""
}

// lyricsAreComplex reports whether the words on screen belong to a script
// whose width a terminal may render differently from Unicode's count.
func (m *Model) lyricsAreComplex() bool {
	if m.lyrState != lyricsReady {
		return false
	}
	// A handful of lines is enough to tell, and it avoids walking a long
	// song's lyrics on every frame.
	for i, line := range m.lyr.Lines {
		if i == 8 {
			break
		}
		if IsComplexScript(line.Text) {
			return true
		}
	}
	return false
}

func (m *Model) visualizer(width, height int) string {
	bars := spectrum.BandsFor(width, 2)
	levels := m.levels
	if len(levels) > bars {
		levels = levels[:bars]
	}
	rows := spectrum.Render(levels, height, 2)
	for i, r := range rows {
		rows[i] = accentSt.Render(r)
	}
	return strings.Join(rows, "\n")
}

// lyricsPane shows the line being sung, with what came before and after
// around it. Unsynced lyrics scroll slowly instead of jumping.
func (m *Model) lyricsPane(width, height int) string {
	switch m.lyrState {
	case lyricsLoading:
		return subSt.Render("looking for the words…")
	case lyricsNone:
		return subSt.Render("no lyrics for this one")
	case lyricsFailed:
		return subSt.Render("lyrics unavailable  ·  " + Truncate(m.lyrErr, max(10, width-24), "…"))
	case lyricsReady:
	default:
		return ""
	}

	cur := m.lyr.At(m.status.Position)
	if !m.lyr.Synced {
		// No timings: page through slowly so the words still move with the
		// song rather than sitting still.
		if m.status.Duration > 0 {
			cur = int(float64(len(m.lyr.Lines)) * clamp01(float64(m.status.Position)/float64(m.status.Duration)))
		} else {
			cur = 0
		}
	}

	// Keep the current line a third of the way down: enough of what is
	// coming to read ahead, enough behind to keep the place.
	above := height / 3
	start := cur - above
	if start < 0 {
		start = 0
	}
	var out []string
	if !m.lyr.Synced {
		// Say why the words are not following the beat, once, quietly.
		out = append(out, subSt.Render("lyrics · no timings for this one"), "")
		height -= 2
	}
	// The marker takes two columns, and a complex script needs room for a
	// terminal that draws wider than Unicode says. cur is -1 until the
	// first line is due, so it is no use for sampling the script.
	room := width - 2
	if m.lyricsAreComplex() {
		room = width - 6
	}
	for i := start; i < start+height && i < len(m.lyr.Lines); i++ {
		text := Truncate(m.lyr.Lines[i].Text, max(4, room), "…")
		if text == "" {
			out = append(out, "")
			continue
		}
		switch {
		case i == cur && m.lyr.Synced:
			out = append(out, currentSt.Render("▸ "+text))
		case i == cur:
			// Unsynced: the line is an estimate, so it is marked but not
			// claimed as exact.
			out = append(out, currentSt.Render("· "+text))
		default:
			out = append(out, subSt.Render("  "+text))
		}
	}
	for len(out) < height {
		out = append(out, "")
	}
	return strings.Join(out, "\n")
}

func (m *Model) footer(width int) string {
	left := ""
	if len(m.items) > 1 {
		left = fmt.Sprintf("%d of %d", m.idx+1, len(m.items))
	}
	keys := []string{"space pause", "←/→ seek", "n next", "q quit"}
	if width < 60 {
		keys = []string{"space", "←/→", "n", "q"}
	}
	right := strings.Join(keys, "  ·  ")
	gap := max(1, width-Width(left)-Width(right))
	return subSt.Render(left + strings.Repeat(" ", gap) + right)
}

// pad indents a block by the side gutter, so nothing touches the edge of
// the terminal.
func pad(block string) string {
	lines := strings.Split(block, "\n")
	for i, l := range lines {
		lines[i] = strings.Repeat(" ", gutter) + l
	}
	return strings.Join(lines, "\n")
}

func clock(d time.Duration) string {
	if d < 0 {
		d = 0
	}
	total := int(d.Seconds())
	if h := total / 3600; h > 0 {
		return fmt.Sprintf("%d:%02d:%02d", h, (total%3600)/60, total%60)
	}
	return fmt.Sprintf("%d:%02d", total/60, total%60)
}

func shortPath(p string) string {
	if i := strings.LastIndex(p, "/"); i >= 0 {
		return p[i+1:]
	}
	return p
}

func clamp01(f float64) float64 {
	if f < 0 {
		return 0
	}
	if f > 1 {
		return 1
	}
	return f
}
