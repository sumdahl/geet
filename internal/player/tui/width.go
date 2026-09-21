package tui

import (
	"strings"
	"unicode"

	"github.com/mattn/go-runewidth"
	"github.com/rivo/uniseg"
)

// Terminals disagree about scripts that combine marks with their letters.
// "सम्झनामा" is 24 runes, 15 grapheme clusters, and Unicode calls it 19
// columns wide: a terminal that shapes it draws 15 cells, one that doesn't
// draws up to 24. Laying such text out in a fixed-width column is therefore
// guesswork, and guessing short makes lines overlap their neighbour.
//
// geet handles it in two ways: it measures pessimistically (below), and it
// gives these lyrics the full width instead of a column beside the
// spectrum (see body in view.go).

// complexRanges are the scripts whose rendered width a terminal cannot be
// trusted to match Unicode's: the Indic family, Thai, Lao, Tibetan,
// Myanmar, Khmer, and the combining-mark blocks.
var complexRanges = &unicode.RangeTable{
	R16: []unicode.Range16{
		{Lo: 0x0300, Hi: 0x036F, Stride: 1}, // combining diacritics
		{Lo: 0x0900, Hi: 0x0DFF, Stride: 1}, // Devanagari … Sinhala
		{Lo: 0x0E00, Hi: 0x0FFF, Stride: 1}, // Thai, Lao, Tibetan
		{Lo: 0x1000, Hi: 0x109F, Stride: 1}, // Myanmar
		{Lo: 0x1780, Hi: 0x17FF, Stride: 1}, // Khmer
		{Lo: 0x1AB0, Hi: 0x1AFF, Stride: 1}, // combining extended
		{Lo: 0x20D0, Hi: 0x20FF, Stride: 1}, // combining for symbols
		{Lo: 0xFE20, Hi: 0xFE2F, Stride: 1}, // combining half marks
	},
}

// IsComplexScript reports whether s holds text whose width a terminal may
// render differently from Unicode's measurement.
func IsComplexScript(s string) bool {
	for _, r := range s {
		if unicode.Is(complexRanges, r) {
			return true
		}
	}
	return false
}

// Width is how many columns s may occupy at worst. For plain text it is
// Unicode's answer; for a complex script it is the rune count, which is
// what an unshaping terminal draws. Reserving the larger of the two keeps
// text inside its box whichever way the terminal renders it.
func Width(s string) int {
	w := runewidth.StringWidth(s)
	if !IsComplexScript(s) {
		return w
	}
	if runes := len([]rune(s)); runes > w {
		return runes
	}
	return w
}

// Truncate shortens s to at most max columns, cutting only between grapheme
// clusters: splitting inside one would leave a vowel sign with no letter to
// attach to, which renders as a stray mark.
func Truncate(s string, max int, tail string) string {
	if max <= 0 {
		return ""
	}
	if Width(s) <= max {
		return s
	}
	limit := max - Width(tail)
	if limit < 0 {
		limit = 0
	}
	var b strings.Builder
	used := 0
	g := uniseg.NewGraphemes(s)
	for g.Next() {
		cluster := g.Str()
		w := Width(cluster)
		if used+w > limit {
			break
		}
		b.WriteString(cluster)
		used += w
	}
	return b.String() + tail
}

// Wrap breaks s into lines of at most width columns, on spaces where it
// can and between clusters where it must. Lyrics read better wrapped than
// cut off.
func Wrap(s string, width int) []string {
	if width <= 0 || s == "" {
		return []string{s}
	}
	if Width(s) <= width {
		return []string{s}
	}
	var lines []string
	var line strings.Builder
	lineWidth := 0
	flush := func() {
		if line.Len() > 0 {
			lines = append(lines, line.String())
			line.Reset()
			lineWidth = 0
		}
	}
	for _, word := range strings.Fields(s) {
		w := Width(word)
		switch {
		case w > width:
			// A single word too long for the line: break it by cluster.
			flush()
			for Width(word) > width {
				part := Truncate(word, width, "")
				if part == "" {
					break
				}
				lines = append(lines, part)
				word = strings.TrimPrefix(word, part)
			}
			if word != "" {
				line.WriteString(word)
				lineWidth = Width(word)
			}
		case lineWidth == 0:
			line.WriteString(word)
			lineWidth = w
		case lineWidth+1+w <= width:
			line.WriteString(" " + word)
			lineWidth += 1 + w
		default:
			flush()
			line.WriteString(word)
			lineWidth = w
		}
	}
	flush()
	return lines
}
