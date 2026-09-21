// Package lyrics fetches song lyrics from LRCLIB and follows them in time.
package lyrics

import (
	"sort"
	"strconv"
	"strings"
	"time"
)

// Line is one lyric line. At is where it starts; it is zero for lyrics that
// came without timings.
type Line struct {
	At   time.Duration
	Text string
}

// Lyrics is what a track's lyrics look like once parsed. Synced lyrics
// follow playback; plain ones are just read.
type Lyrics struct {
	Lines  []Line
	Synced bool
}

// Empty reports whether there is nothing to show. Plenty of songs have no
// lyrics anywhere (instrumentals, new releases, local recordings), so
// callers must treat this as ordinary, not as an error.
func (l Lyrics) Empty() bool { return len(l.Lines) == 0 }

// At returns the index of the line playing at position d, or -1 before the
// first line. Callers ask on every tick, so this binary-searches rather
// than scanning.
func (l Lyrics) At(d time.Duration) int {
	if !l.Synced || len(l.Lines) == 0 {
		return -1
	}
	i := sort.Search(len(l.Lines), func(i int) bool { return l.Lines[i].At > d })
	return i - 1
}

// ParseLRC reads an LRC file: lines of "[mm:ss.xx] text", where one line can
// carry several timestamps ("[00:12.00][01:30.00] chorus"). Metadata tags
// ([ar:], [length:] …) are skipped. Text without any timestamp is kept as
// plain lyrics, which is what LRCLIB returns for songs nobody has synced.
func ParseLRC(s string) Lyrics {
	var synced []Line
	var plain []Line
	for _, raw := range strings.Split(s, "\n") {
		line := strings.TrimRight(raw, "\r")
		stamps, text := splitStamps(line)
		if len(stamps) == 0 {
			if t := strings.TrimSpace(line); t != "" && !isTag(line) {
				plain = append(plain, Line{Text: t})
			}
			continue
		}
		for _, at := range stamps {
			synced = append(synced, Line{At: at, Text: strings.TrimSpace(text)})
		}
	}
	if len(synced) > 0 {
		sort.SliceStable(synced, func(i, j int) bool { return synced[i].At < synced[j].At })
		return Lyrics{Lines: synced, Synced: true}
	}
	return Lyrics{Lines: plain}
}

// splitStamps peels the leading [..] groups off a line, returning the ones
// that are timestamps and whatever text follows.
func splitStamps(line string) ([]time.Duration, string) {
	var out []time.Duration
	rest := strings.TrimLeft(line, " \t")
	for strings.HasPrefix(rest, "[") {
		end := strings.IndexByte(rest, ']')
		if end < 0 {
			break
		}
		at, ok := parseStamp(rest[1:end])
		if !ok {
			// A metadata tag: it ends the timestamps, and what follows is
			// not a lyric line.
			if len(out) == 0 {
				return nil, ""
			}
			break
		}
		out = append(out, at)
		rest = rest[end+1:]
	}
	return out, rest
}

// parseStamp reads "mm:ss.xx", "mm:ss", or "hh:mm:ss.xx".
func parseStamp(s string) (time.Duration, bool) {
	parts := strings.Split(s, ":")
	if len(parts) < 2 || len(parts) > 3 {
		return 0, false
	}
	var total time.Duration
	for _, p := range parts[:len(parts)-1] {
		n, err := strconv.Atoi(strings.TrimSpace(p))
		if err != nil || n < 0 {
			return 0, false
		}
		total = total*60 + time.Duration(n)*time.Minute
	}
	secs, err := strconv.ParseFloat(strings.Replace(strings.TrimSpace(parts[len(parts)-1]), ",", ".", 1), 64)
	if err != nil || secs < 0 {
		return 0, false
	}
	return total + time.Duration(secs*float64(time.Second)), true
}

func isTag(line string) bool {
	line = strings.TrimSpace(line)
	return strings.HasPrefix(line, "[") && strings.HasSuffix(line, "]")
}
