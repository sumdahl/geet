package deezer

import (
	"context"
	"errors"
	"fmt"
	"net/url"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/sumdahl/geet/internal/spotify"
)

// RefPrefix marks a Deezer track ID in `geet search --json` refs.
const RefPrefix = "deezer:"

var ErrBadRef = errors.New("not a Deezer track reference")

// searchLimit is how many songs one search reads. Deezer ranks well, so the
// original is near the top; the rest are covers and tributes Rank sinks.
const searchLimit = 25

// Deezer's explicit_content_lyrics codes.
const (
	contentExplicit = 1
	contentEdited   = 3 // a clean edit
)

type catalogTrack struct {
	ID                    int64  `json:"id"`
	Title                 string `json:"title"`
	Duration              int    `json:"duration"`
	TrackPosition         int    `json:"track_position"`
	DiskNumber            int    `json:"disk_number"`
	ReleaseDate           string `json:"release_date"`
	ISRC                  string `json:"isrc"`
	ExplicitLyrics        bool   `json:"explicit_lyrics"`
	ExplicitContentLyrics int    `json:"explicit_content_lyrics"`
	Link                  string `json:"link"`
	Artist                struct {
		Name string `json:"name"`
	} `json:"artist"`
	Contributors []struct {
		Name string `json:"name"`
		Role string `json:"role"`
	} `json:"contributors"`
	Album struct {
		Title   string `json:"title"`
		CoverXL string `json:"cover_xl"`
	} `json:"album"`
}

// Search looks songs up in Deezer's keyless catalog. It lists explicit
// originals that the Apple catalog often only has as clean edits
// ("Tonight (I'm Fuckin' You)"), marked Explicit. Search results name only
// the main artist and no year; Lookup has the rest.
func (c *Client) Search(ctx context.Context, query string) ([]spotify.Track, error) {
	var page struct {
		Data []catalogTrack `json:"data"`
	}
	q := url.Values{"q": {query}, "limit": {strconv.Itoa(searchLimit)}}
	if err := c.get(ctx, "/search?"+q.Encode(), &page); err != nil {
		return nil, err
	}
	tracks := make([]spotify.Track, 0, len(page.Data))
	for _, d := range page.Data {
		tracks = append(tracks, d.toTrack())
	}
	return tracks, nil
}

// Lookup returns the song with the given Deezer track ID, with every artist,
// the release year, ISRC and track and disc numbers.
func (c *Client) Lookup(ctx context.Context, id string) (spotify.Track, error) {
	var d catalogTrack
	if err := c.get(ctx, "/track/"+id, &d); err != nil {
		return spotify.Track{}, err
	}
	if d.ID == 0 {
		return spotify.Track{}, fmt.Errorf("deezer track %s: %w", id, spotify.ErrNotFound)
	}
	return d.toTrack(), nil
}

func (d catalogTrack) toTrack() spotify.Track {
	t := spotify.Track{
		ID:          RefPrefix + strconv.FormatInt(d.ID, 10),
		Title:       d.Title,
		Artists:     []string{d.Artist.Name},
		AlbumArtist: d.Artist.Name,
		Album:       d.Album.Title,
		CoverURL:    d.Album.CoverXL,
		TrackNumber: d.TrackPosition,
		DiscNumber:  d.DiskNumber,
		Duration:    time.Duration(d.Duration) * time.Second,
		ISRC:        d.ISRC,
		SourceURL:   d.Link,
		Explicit:    d.ExplicitLyrics || d.ExplicitContentLyrics == contentExplicit,
		Clean:       d.ExplicitContentLyrics == contentEdited,
	}
	// Main artists first, then featured ones, as Spotify lists them.
	var main, featured []string
	for _, a := range d.Contributors {
		if a.Role == "Featured" {
			featured = append(featured, a.Name)
		} else {
			main = append(main, a.Name)
		}
	}
	if len(main) > 0 {
		t.Artists = append(main, featured...)
	}
	if len(d.ReleaseDate) >= 4 {
		t.Year, _ = strconv.Atoi(d.ReleaseDate[:4])
	}
	return t
}

var trackPath = regexp.MustCompile(`^(?:/[a-z]{2})?/track/(\d+)$`)

// ParseRef returns the Deezer track ID in a "deezer:<id>" reference or a
// Deezer track link (https://www.deezer.com/track/<id>, optionally with a
// language such as /en/).
func ParseRef(s string) (string, error) {
	s = strings.TrimSpace(s)
	if id, ok := strings.CutPrefix(s, RefPrefix); ok {
		if isDigits(id) {
			return id, nil
		}
		return "", fmt.Errorf("%w: %q", ErrBadRef, s)
	}
	u, err := url.Parse(s)
	if err != nil || (u.Scheme != "https" && u.Scheme != "http") || (u.Host != "www.deezer.com" && u.Host != "deezer.com") {
		return "", fmt.Errorf("%w: %q", ErrBadRef, s)
	}
	if m := trackPath.FindStringSubmatch(strings.TrimRight(u.Path, "/")); m != nil {
		return m[1], nil
	}
	return "", fmt.Errorf("%w: %q", ErrBadRef, s)
}

// IsRef reports whether s looks like something ParseRef handles.
func IsRef(s string) bool {
	s = strings.TrimSpace(s)
	return strings.HasPrefix(s, RefPrefix) || strings.Contains(s, "://www.deezer.com/") || strings.Contains(s, "://deezer.com/")
}

func isDigits(s string) bool {
	if s == "" || len(s) > 19 {
		return false
	}
	for _, r := range s {
		if r < '0' || r > '9' {
			return false
		}
	}
	return true
}
