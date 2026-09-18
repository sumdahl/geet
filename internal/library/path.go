// Package library decides where a track's file goes inside the music
// library, from a user-configurable path template.
package library

import (
	"errors"
	"fmt"
	"path/filepath"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"unicode"
	"unicode/utf8"

	"github.com/sumdahl/spotify-dl/internal/spotify"
)

const DefaultTemplate = "{title} - {artists}"

var Placeholders = []string{
	"{title}", "{artist}", "{artists}", "{album}", "{album_artist}",
	"{track}", "{disc}", "{year}", "{isrc}", "{spotify_id}",
}

const (
	unknown = "Unknown"
	// Leaves room for the extension under the common 255-byte name limit.
	maxSegmentBytes = 200
)

var placeholderRe = regexp.MustCompile(`\{[^{}]*\}`)

func ValidateTemplate(tmpl string) error {
	if strings.TrimSpace(tmpl) == "" {
		return errors.New("must not be empty")
	}
	if strings.HasPrefix(tmpl, "/") {
		return errors.New("must be relative to output, not start with /")
	}
	for _, seg := range strings.Split(tmpl, "/") {
		if seg == ".." || seg == "." {
			return fmt.Errorf("must not contain a %q path segment", seg)
		}
	}
	for _, p := range placeholderRe.FindAllString(tmpl, -1) {
		if !slices.Contains(Placeholders, p) {
			return fmt.Errorf("unknown placeholder %s (known: %s)", p, strings.Join(Placeholders, " "))
		}
	}
	if !strings.Contains(tmpl, "{title}") {
		return errors.New("must contain {title}, or tracks would overwrite each other")
	}
	return nil
}

// Path returns root/<rendered template>.<ext>. Each "/"-separated template
// segment becomes exactly one path component: values are sanitized so a
// title like "AC/DC" can't add directories or climb out of root. tmpl must
// have passed ValidateTemplate.
func Path(root, tmpl string, t spotify.Track, ext string) string {
	vals := values(t)
	segs := strings.Split(tmpl, "/")
	parts := make([]string, 0, len(segs)+1)
	parts = append(parts, root)
	for i, seg := range segs {
		s := placeholderRe.ReplaceAllStringFunc(seg, func(p string) string {
			return sanitize(vals[p])
		})
		s = clean(s)
		if s == "" {
			s = unknown
		}
		if i == len(segs)-1 {
			s = truncate(s, maxSegmentBytes-len(ext)-1) + "." + ext
		} else {
			s = truncate(s, maxSegmentBytes)
		}
		parts = append(parts, s)
	}
	return filepath.Join(parts...)
}

func values(t spotify.Track) map[string]string {
	artist := ""
	if len(t.Artists) > 0 {
		artist = t.Artists[0]
	}
	albumArtist := t.AlbumArtist
	if albumArtist == "" {
		albumArtist = artist
	}
	track := ""
	if t.TrackNumber > 0 {
		track = fmt.Sprintf("%02d", t.TrackNumber)
	}
	// Disc is unknown for many keyless lookups; nearly every such track is
	// on disc 1, and an empty value would leave stray separators in names.
	disc := "1"
	if t.DiscNumber > 0 {
		disc = strconv.Itoa(t.DiscNumber)
	}
	year := ""
	if t.Year > 0 {
		year = strconv.Itoa(t.Year)
	}
	return map[string]string{
		"{title}":        t.Title,
		"{artist}":       artist,
		"{artists}":      strings.Join(t.Artists, ", "),
		"{album}":        t.Album,
		"{album_artist}": albumArtist,
		"{track}":        track,
		"{disc}":         disc,
		"{year}":         year,
		"{isrc}":         t.ISRC,
		"{spotify_id}":   t.ID,
	}
}

// sanitize makes a value safe inside one path component, also on FAT/exFAT
// (phones, SD cards), which reject <>:"|?* and backslashes.
func sanitize(v string) string {
	return strings.Map(func(r rune) rune {
		switch {
		case r == '/' || r == '\\':
			return '-'
		case strings.ContainsRune(`<>:"|?*`, r), unicode.IsControl(r):
			return -1
		}
		return r
	}, v)
}

// clean collapses whitespace and trims the spaces and dots that are invalid
// or hidden-file markers at the ends of a name.
func clean(s string) string {
	return strings.Trim(strings.Join(strings.Fields(s), " "), " .")
}

func truncate(s string, max int) string {
	if len(s) <= max {
		return s
	}
	for max > 0 && !utf8.RuneStart(s[max]) {
		max--
	}
	return strings.TrimRight(s[:max], " .")
}

// FolderCases are the letter-case styles FolderName accepts.
var FolderCases = []string{"lower", "capitalize", "title"}

const maxFolderBytes = 100

// FolderName turns a playlist name into a directory name without spaces:
// words joined by hyphens, apostrophes dropped ("Spotify's" → "spotifys"),
// all other punctuation, symbols and emoji removed. Letters of every script
// are kept. letterCase is lower ("road-trip-mix"), capitalize
// ("Road-trip-mix") or title ("Road-Trip-Mix").
func FolderName(name, letterCase string) string {
	var words []string
	var word strings.Builder
	flush := func() {
		if word.Len() > 0 {
			words = append(words, strings.ToLower(word.String()))
			word.Reset()
		}
	}
	for _, r := range name {
		switch {
		case r == '\'' || r == '’' || r == '`':
		case unicode.IsLetter(r) || unicode.IsDigit(r) || unicode.IsMark(r):
			word.WriteRune(r)
		default:
			flush()
		}
	}
	flush()
	if len(words) == 0 {
		return "playlist"
	}

	for i, w := range words {
		if letterCase == "title" || (letterCase == "capitalize" && i == 0) {
			words[i] = upperFirst(w)
		}
	}

	out := words[0]
	for _, w := range words[1:] {
		if len(out)+1+len(w) > maxFolderBytes {
			break
		}
		out += "-" + w
	}
	return truncate(out, maxFolderBytes)
}

func upperFirst(s string) string {
	r, size := utf8.DecodeRuneInString(s)
	return string(unicode.ToUpper(r)) + s[size:]
}
