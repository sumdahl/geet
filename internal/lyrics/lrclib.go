package lyrics

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"math"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/sumdahl/geet/internal/spotify"
	"github.com/sumdahl/geet/internal/textnorm"
)

// ErrNotFound is returned when no lyrics exist for a track. It is an
// ordinary outcome, not a failure: instrumentals, brand-new releases and
// most local-language songs have none. Callers show the song without a
// lyrics pane rather than reporting an error.
var ErrNotFound = errors.New("no lyrics found")

// lrclibBase is the public LRCLIB instance. It needs no key and no account.
const lrclibBase = "https://lrclib.net"

// Client fetches lyrics, remembering what it found (and what it didn't) on
// disk so a song played twice needs no network.
type Client struct {
	HTTP     *http.Client
	BaseURL  string
	CacheDir string // empty disables caching
}

// New returns a client caching under ~/.cache/geet/lyrics.
func New() *Client {
	return &Client{
		HTTP:     &http.Client{Timeout: 10 * time.Second},
		BaseURL:  lrclibBase,
		CacheDir: defaultCacheDir(),
	}
}

func defaultCacheDir() string {
	dir, err := os.UserCacheDir()
	if err != nil {
		return ""
	}
	return filepath.Join(dir, "geet", "lyrics")
}

// Fetch returns a track's lyrics, from the cache when possible. A track
// without lyrics anywhere gives ErrNotFound, which is cached too: asking
// LRCLIB the same hopeless question on every replay is rude and slow.
func (c *Client) Fetch(ctx context.Context, t spotify.Track) (Lyrics, error) {
	key := cacheKey(t)
	if cached, miss, ok := c.readCache(key); ok {
		if miss {
			return Lyrics{}, ErrNotFound
		}
		return ParseLRC(cached), nil
	}

	text, err := c.get(ctx, t)
	if errors.Is(err, ErrNotFound) {
		text, err = c.search(ctx, t)
	}
	switch {
	case err == nil:
		c.writeCache(key, text)
		return ParseLRC(text), nil
	case errors.Is(err, ErrNotFound):
		c.writeCache(key, missMarker)
		return Lyrics{}, ErrNotFound
	default:
		// A network failure must not be cached as "no lyrics".
		slog.DebugContext(ctx, "lyrics lookup failed", "track", t.Title, "err", err)
		return Lyrics{}, err
	}
}

// lrclibRecord is the subset of LRCLIB's JSON that matters here.
type lrclibRecord struct {
	TrackName    string  `json:"trackName"`
	ArtistName   string  `json:"artistName"`
	Duration     float64 `json:"duration"`
	Instrumental bool    `json:"instrumental"`
	PlainLyrics  string  `json:"plainLyrics"`
	SyncedLyrics string  `json:"syncedLyrics"`
}

func (r lrclibRecord) text() (string, bool) {
	switch {
	case r.Instrumental:
		return "", false
	case strings.TrimSpace(r.SyncedLyrics) != "":
		return r.SyncedLyrics, true
	case strings.TrimSpace(r.PlainLyrics) != "":
		return r.PlainLyrics, true
	}
	return "", false
}

// get asks for the exact recording: LRCLIB matches on artist, title, album
// and length together, so it never hands back another song's words.
func (c *Client) get(ctx context.Context, t spotify.Track) (string, error) {
	q := url.Values{}
	q.Set("artist_name", primaryArtist(t))
	q.Set("track_name", textnorm.StripFeat(t.Title))
	if t.Album != "" {
		q.Set("album_name", t.Album)
	}
	if t.Duration > 0 {
		q.Set("duration", fmt.Sprintf("%d", int(math.Round(t.Duration.Seconds()))))
	}
	var rec lrclibRecord
	if err := c.do(ctx, "/api/get?"+q.Encode(), &rec); err != nil {
		return "", err
	}
	text, ok := rec.text()
	if !ok {
		return "", ErrNotFound
	}
	return text, nil
}

// search is the fallback for a tagging mismatch (a remaster, a different
// album edition). A result is only accepted when it is the same artist and
// title and within 5s of the length, so a cover version never slips in.
func (c *Client) search(ctx context.Context, t spotify.Track) (string, error) {
	q := url.Values{}
	q.Set("track_name", textnorm.StripFeat(t.Title))
	q.Set("artist_name", primaryArtist(t))
	var found []lrclibRecord
	if err := c.do(ctx, "/api/search?"+q.Encode(), &found); err != nil {
		return "", err
	}
	for _, rec := range found {
		if !sameSong(t, rec) {
			continue
		}
		if text, ok := rec.text(); ok {
			return text, nil
		}
	}
	return "", ErrNotFound
}

func sameSong(t spotify.Track, rec lrclibRecord) bool {
	if textnorm.Norm(textnorm.StripFeat(t.Title)) != textnorm.Norm(textnorm.StripFeat(rec.TrackName)) {
		return false
	}
	if textnorm.Norm(primaryArtist(t)) != textnorm.Norm(rec.ArtistName) {
		return false
	}
	if t.Duration > 0 && rec.Duration > 0 {
		if math.Abs(rec.Duration-t.Duration.Seconds()) > 5 {
			return false
		}
	}
	return true
}

func (c *Client) do(ctx context.Context, path string, out any) error {
	base := c.BaseURL
	if base == "" {
		base = lrclibBase
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, base+path, nil)
	if err != nil {
		return err
	}
	// LRCLIB asks clients to identify themselves.
	req.Header.Set("User-Agent", "geet (https://github.com/sumdahl/geet)")
	client := c.HTTP
	if client == nil {
		client = http.DefaultClient
	}
	resp, err := client.Do(req)
	if err != nil {
		return err
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode == http.StatusNotFound {
		return ErrNotFound
	}
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("lrclib: %s", resp.Status)
	}
	return json.NewDecoder(resp.Body).Decode(out)
}

func primaryArtist(t spotify.Track) string {
	if len(t.Artists) == 0 {
		return ""
	}
	return t.Artists[0]
}

// missMarker is what a "no lyrics" answer looks like in the cache. An empty
// file would be indistinguishable from a half-written one.
const missMarker = "\x00geet:none\n"

func cacheKey(t spotify.Track) string {
	if t.ISRC != "" {
		return "isrc-" + sanitize(t.ISRC)
	}
	if t.ID != "" {
		return "id-" + sanitize(t.ID)
	}
	return "q-" + sanitize(textnorm.Norm(primaryArtist(t)+"-"+t.Title))
}

func sanitize(s string) string {
	return strings.Map(func(r rune) rune {
		switch {
		case r >= 'a' && r <= 'z', r >= 'A' && r <= 'Z', r >= '0' && r <= '9', r == '-', r == '_':
			return r
		}
		return '_'
	}, s)
}

func (c *Client) readCache(key string) (text string, miss, ok bool) {
	if c.CacheDir == "" || key == "" {
		return "", false, false
	}
	b, err := os.ReadFile(filepath.Join(c.CacheDir, key+".lrc"))
	if err != nil {
		return "", false, false
	}
	if string(b) == missMarker {
		return "", true, true
	}
	return string(b), false, true
}

func (c *Client) writeCache(key, text string) {
	if c.CacheDir == "" || key == "" {
		return
	}
	if err := os.MkdirAll(c.CacheDir, 0o755); err != nil {
		return
	}
	_ = os.WriteFile(filepath.Join(c.CacheDir, key+".lrc"), []byte(text), 0o644)
}
