// Package itunes searches Apple's public iTunes catalog, which needs no key
// and answers from anywhere (the store country is a parameter), for
// `geet search`. Results come back as spotify.Track so they flow through the
// same download pipeline as Spotify links.
//
// Two catalog traits shape this package: the same recording is listed once
// per album edition (Rank merges them), and explicit songs are often only
// available as clean edits whose titles are censored ("umean" for
// "fukumean"); those are marked Track.Clean.
package itunes

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/url"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/sumdahl/geet/internal/spotify"
)

const (
	defaultBaseURL = "https://itunes.apple.com"
	// RefPrefix marks an iTunes track reference: "itunes:1499378607".
	RefPrefix = "itunes:"
	// maxFetch is what Search asks the catalog for before ranking; the
	// relevant result is often well down the raw list.
	maxFetch = 50
)

var (
	ErrNotFound = errors.New("not found in the iTunes catalog")
	ErrBadRef   = errors.New("not an iTunes track reference or Apple Music song link")
)

type Client struct {
	http    *http.Client
	baseURL string
	country string
}

// New returns a client for the given store country (two letters, e.g. "US").
func New(baseURL, country string) *Client {
	if baseURL == "" {
		baseURL = defaultBaseURL
	}
	return &Client{
		http:    &http.Client{Timeout: 20 * time.Second},
		baseURL: strings.TrimRight(baseURL, "/"),
		country: strings.ToUpper(country),
	}
}

// Search returns up to maxFetch songs matching term, in the catalog's order.
func (c *Client) Search(ctx context.Context, term string) ([]spotify.Track, error) {
	q := url.Values{
		"term":    {term},
		"media":   {"music"},
		"entity":  {"song"},
		"limit":   {strconv.Itoa(maxFetch)},
		"country": {c.country},
	}
	return c.get(ctx, "/search?"+q.Encode())
}

// Lookup returns the song with the given iTunes track ID.
func (c *Client) Lookup(ctx context.Context, id string) (spotify.Track, error) {
	q := url.Values{"id": {id}, "country": {c.country}}
	tracks, err := c.get(ctx, "/lookup?"+q.Encode())
	if err != nil {
		return spotify.Track{}, err
	}
	if len(tracks) == 0 {
		return spotify.Track{}, fmt.Errorf("track %s: %w", id, ErrNotFound)
	}
	return tracks[0], nil
}

type result struct {
	Kind                 string `json:"kind"`
	TrackID              int64  `json:"trackId"`
	TrackName            string `json:"trackName"`
	ArtistName           string `json:"artistName"`
	CollectionName       string `json:"collectionName"`
	CollectionArtistName string `json:"collectionArtistName"`
	ArtworkURL100        string `json:"artworkUrl100"`
	ReleaseDate          string `json:"releaseDate"`
	TrackTimeMillis      int64  `json:"trackTimeMillis"`
	TrackNumber          int    `json:"trackNumber"`
	DiscNumber           int    `json:"discNumber"`
	TrackViewURL         string `json:"trackViewUrl"`
	TrackExplicitness    string `json:"trackExplicitness"`
}

func (c *Client) get(ctx context.Context, path string) ([]spotify.Track, error) {
	body, err := c.fetch(ctx, c.baseURL+path)
	if err != nil {
		return nil, err
	}
	var doc struct {
		Results []result `json:"results"`
	}
	if err := json.Unmarshal(body, &doc); err != nil {
		return nil, fmt.Errorf("itunes: decoding response: %w", err)
	}
	var out []spotify.Track
	for _, r := range doc.Results {
		if r.Kind == "song" && r.TrackID != 0 {
			out = append(out, r.toTrack())
		}
	}
	return out, nil
}

// fetch GETs u, retrying when Apple rate-limits: the catalog allows about
// 20 requests a minute and answers 429 or 403 past that.
func (c *Client) fetch(ctx context.Context, u string) ([]byte, error) {
	const attempts = 3
	for attempt := 1; ; attempt++ {
		req, err := http.NewRequestWithContext(ctx, http.MethodGet, u, nil)
		if err != nil {
			return nil, err
		}
		resp, err := c.http.Do(req)
		if err != nil {
			return nil, fmt.Errorf("itunes: %w", err)
		}
		body, err := io.ReadAll(io.LimitReader(resp.Body, 8<<20))
		resp.Body.Close()
		if err != nil {
			return nil, err
		}
		limited := resp.StatusCode == http.StatusTooManyRequests || resp.StatusCode == http.StatusForbidden
		if limited && attempt < attempts {
			wait := time.Duration(attempt) * 2 * time.Second
			slog.DebugContext(ctx, "itunes rate limited", "retry_in", wait)
			select {
			case <-time.After(wait):
				continue
			case <-ctx.Done():
				return nil, ctx.Err()
			}
		}
		if resp.StatusCode/100 != 2 {
			return nil, fmt.Errorf("itunes: HTTP %d", resp.StatusCode)
		}
		return body, nil
	}
}

func (r result) toTrack() spotify.Track {
	t := spotify.Track{
		ID:          RefPrefix + strconv.FormatInt(r.TrackID, 10),
		Title:       r.TrackName,
		Artists:     splitArtists(r.ArtistName),
		AlbumArtist: r.CollectionArtistName,
		Album:       r.CollectionName,
		CoverURL:    strings.Replace(r.ArtworkURL100, "100x100bb", "600x600bb", 1),
		TrackNumber: r.TrackNumber,
		DiscNumber:  r.DiscNumber,
		Duration:    time.Duration(r.TrackTimeMillis) * time.Millisecond,
		SourceURL:   r.TrackViewURL,
		Clean:       r.TrackExplicitness == "cleaned",
	}
	if t.AlbumArtist == "" {
		t.AlbumArtist = r.ArtistName
	}
	if len(r.ReleaseDate) >= 4 {
		t.Year, _ = strconv.Atoi(r.ReleaseDate[:4])
	}
	return t
}

var artistSep = regexp.MustCompile(`\s*(?:,|&| feat\. | ft\. | x )\s*`)

// splitArtists turns iTunes' single artist string ("The Weeknd & ROSALÍA")
// into names. YouTube uploads often name only the primary artist, so
// matching needs them separately; a duo such as "Simon & Garfunkel" comes
// out as two names, which still matches.
func splitArtists(s string) []string {
	var out []string
	for _, a := range artistSep.Split(s, -1) {
		if a = strings.TrimSpace(a); a != "" {
			out = append(out, a)
		}
	}
	if len(out) == 0 {
		return []string{s}
	}
	return out
}

var songPathID = regexp.MustCompile(`^/[a-z]{2}/song/(?:[^/]+/)?(\d+)$`)

// ParseRef returns the iTunes track ID in an "itunes:<id>" reference or an
// Apple Music song link: https://music.apple.com/us/album/<slug>/<album>?i=<id>
// or https://music.apple.com/us/song/<slug>/<id>.
func ParseRef(s string) (string, error) {
	s = strings.TrimSpace(s)
	if id, ok := strings.CutPrefix(s, RefPrefix); ok {
		if isDigits(id) {
			return id, nil
		}
		return "", fmt.Errorf("%w: %q", ErrBadRef, s)
	}
	u, err := url.Parse(s)
	if err != nil || (u.Scheme != "https" && u.Scheme != "http") ||
		(u.Host != "music.apple.com" && u.Host != "itunes.apple.com") {
		return "", fmt.Errorf("%w: %q", ErrBadRef, s)
	}
	if id := u.Query().Get("i"); isDigits(id) {
		return id, nil
	}
	if m := songPathID.FindStringSubmatch(strings.TrimRight(u.Path, "/")); m != nil {
		return m[1], nil
	}
	return "", fmt.Errorf("%w: %q", ErrBadRef, s)
}

// IsRef reports whether s looks like something ParseRef handles, so callers
// can route it here instead of to the Spotify parser.
func IsRef(s string) bool {
	s = strings.TrimSpace(s)
	return strings.HasPrefix(s, RefPrefix) ||
		strings.Contains(s, "://music.apple.com/") || strings.Contains(s, "://itunes.apple.com/")
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
