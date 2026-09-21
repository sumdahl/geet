package spectrum

import (
	"strconv"
	"strings"
)

// blocks are the eighth-height glyphs, from almost nothing to full. They
// give a bar eight steps per terminal row.
var blocks = []rune{' ', '▁', '▂', '▃', '▄', '▅', '▆', '▇', '█'}

// Render draws levels as rows of block glyphs, tallest row first, so the
// caller can print them top to bottom. Each bar is `barWidth` columns wide
// with a column of air between bars, which is what keeps the display from
// looking like a solid wall.
func Render(levels []float64, height, barWidth int) []string {
	if height < 1 {
		height = 1
	}
	if barWidth < 1 {
		barWidth = 1
	}
	rows := make([]string, height)
	for row := 0; row < height; row++ {
		// Row 0 is the top of the display.
		fromTop := height - row
		var b strings.Builder
		for i, level := range levels {
			if i > 0 {
				b.WriteByte(' ')
			}
			eighths := int(level*float64(height)*8 + 0.5)
			cell := eighths - (fromTop-1)*8
			switch {
			case cell <= 0:
				b.WriteString(strings.Repeat(" ", barWidth))
			case cell >= 8:
				b.WriteString(strings.Repeat(string(blocks[8]), barWidth))
			default:
				b.WriteString(strings.Repeat(string(blocks[cell]), barWidth))
			}
		}
		rows[row] = b.String()
	}
	return rows
}

// BandsFor picks how many bars fit a width, given one space between bars.
// Narrow terminals get fewer, wider bars rather than a smear of slivers.
func BandsFor(width, barWidth int) int {
	if barWidth < 1 {
		barWidth = 1
	}
	n := (width + 1) / (barWidth + 1)
	if n < 4 {
		return 4
	}
	if n > 64 {
		return 64
	}
	return n
}

func itoa(n int) string { return strconv.Itoa(n) }

func strconvFloat(f float64) string { return strconv.FormatFloat(f, 'f', 3, 64) }
