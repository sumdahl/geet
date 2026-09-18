// Package deezer fills in the tags Spotify's public pages don't expose (ISRC,
// disc number) by finding the same recording in Deezer's keyless public API.
// Enrichment is best effort: a track with no confident match is left as is.
package deezer

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"sync"
	"time"

	"golang.org/x/time/rate"

	"github.com/sumdahl/spotify-dl/internal/spotify"
	"github.com/sumdahl/spotify-dl/internal/textnorm"
)

const (
	defaultBaseURL = "https://api.deezer.com"
	durationSlack  = 3 * time.Second
	// Deezer's error code for its 50-requests-per-5-seconds quota.
	quotaExceeded = 4
	quotaWindow   = 5 * time.Second
)

type Client struct {
	http    *http.Client
	baseURL string
	limiter *rate.Limiter

	mu     sync.Mutex
	albums map[string][]dzTrack // by Spotify album artist + title; nil = no match
}

func New(baseURL string) *Client {
	if baseURL == "" {
		baseURL = defaultBaseURL
	}
	return &Client{
		http:    &http.Client{Timeout: 30 * time.Second},
		baseURL: strings.TrimRight(baseURL, "/"),
		limiter: rate.NewLimiter(rate.Every(quotaWindow/45), 5),
		albums:  make(map[string][]dzTrack),
	}
}

// EnrichAlbum matches the whole album at once, which also corrects the
// position-based track numbers on multi-disc albums. Tracks it can't pair up
// fall back to EnrichTrack.
func (c *Client) EnrichAlbum(ctx context.Context, tracks []spotify.Track) error {
	if len(tracks) == 0 {
		return nil
	}
	matched := make([]bool, len(tracks))
	id, ok, err := c.findAlbum(ctx, tracks[0].AlbumArtist, tracks[0].Album, len(tracks))
	if err != nil {
		return err
	}
	if ok {
		dz, err := c.albumTracks(ctx, id)
		if err != nil {
			return err
		}
		pairAlbum(tracks, dz, matched)
	} else {
		slog.DebugContext(ctx, "no matching Deezer album", "album", tracks[0].Album)
	}

	for i := range tracks {
		if matched[i] {
			continue
		}
		if err := c.EnrichTrack(ctx, &tracks[i]); err != nil {
			return err
		}
	}
	return nil
}

func pairAlbum(tracks []spotify.Track, dz []dzTrack, matched []bool) {
	if len(dz) == len(tracks) {
		aligned := true
		for i := range tracks {
			if !sameRecording(tracks[i], dz[i].Title, dz[i].Duration) {
				aligned = false
				break
			}
		}
		if aligned {
			for i := range tracks {
				dz[i].apply(&tracks[i], true)
				matched[i] = true
			}
			return
		}
	}

	used := make([]bool, len(dz))
	for i := range tracks {
		for j, d := range dz {
			if !used[j] && textnorm.Norm(d.Title) == textnorm.Norm(tracks[i].Title) && durationClose(tracks[i].Duration, d.Duration) {
				d.apply(&tracks[i], true)
				matched[i], used[j] = true, true
				break
			}
		}
	}
}

// EnrichTrack looks for the track on its own album first, so the ISRC and
// disc number come from the same release, then falls back to a track search.
func (c *Client) EnrichTrack(ctx context.Context, t *spotify.Track) error {
	if len(t.Artists) == 0 {
		return nil
	}
	if t.Album != "" {
		dz, err := c.cachedAlbum(ctx, t.AlbumArtist, t.Album)
		if err != nil {
			return err
		}
		for _, d := range dz {
			if textnorm.Norm(d.Title) == textnorm.Norm(t.Title) && durationClose(t.Duration, d.Duration) {
				d.apply(t, true)
				return nil
			}
		}
	}
	var res struct {
		Data []struct {
			ID       int64  `json:"id"`
			Title    string `json:"title"`
			Duration int    `json:"duration"`
			Artist   struct {
				Name string `json:"name"`
			} `json:"artist"`
			Album struct {
				Title string `json:"title"`
			} `json:"album"`
		} `json:"data"`
	}
	q := url.Values{"q": {t.Artists[0] + " " + t.Title}, "limit": {"10"}}
	if err := c.get(ctx, "/search?"+q.Encode(), &res); err != nil {
		return err
	}

	var bestID int64
	best, bestAlbum := -1, false
	for _, d := range res.Data {
		if !sameRecording(*t, d.Title, d.Duration) || !hasArtist(t.Artists, d.Artist.Name) {
			continue
		}
		score := 0
		if textnorm.Norm(d.Title) == textnorm.Norm(t.Title) {
			score += 2
		}
		sameAlbum := textnorm.Norm(d.Album.Title) == textnorm.Norm(t.Album)
		if sameAlbum {
			score += 4
		}
		if score > best {
			best, bestID, bestAlbum = score, d.ID, sameAlbum
		}
	}
	if best < 0 {
		slog.DebugContext(ctx, "no matching Deezer track", "title", t.Title, "artist", t.Artists[0])
		return nil
	}

	var d dzTrack
	if err := c.get(ctx, "/track/"+strconv.FormatInt(bestID, 10), &d); err != nil {
		return err
	}
	// Disc and track position only carry over from the same album release;
	// the ISRC identifies the recording, so it holds across compilations.
	d.apply(t, bestAlbum)
	return nil
}

func (c *Client) cachedAlbum(ctx context.Context, artist, album string) ([]dzTrack, error) {
	key := textnorm.Norm(artist) + "\x00" + textnorm.Norm(album)
	c.mu.Lock()
	dz, ok := c.albums[key]
	c.mu.Unlock()
	if ok {
		return dz, nil
	}

	id, found, err := c.findAlbum(ctx, artist, album, 0)
	if err != nil {
		return nil, err
	}
	if found {
		if dz, err = c.albumTracks(ctx, id); err != nil {
			return nil, err
		}
	}
	c.mu.Lock()
	c.albums[key] = dz
	c.mu.Unlock()
	return dz, nil
}

// findAlbum prefers a same-titled album with n tracks; n = 0 accepts the
// first same-titled one.
func (c *Client) findAlbum(ctx context.Context, artist, album string, n int) (int64, bool, error) {
	var res struct {
		Data []struct {
			ID       int64  `json:"id"`
			Title    string `json:"title"`
			NbTracks int    `json:"nb_tracks"`
			Artist   struct {
				Name string `json:"name"`
			} `json:"artist"`
		} `json:"data"`
	}
	q := url.Values{"q": {artist + " " + album}, "limit": {"10"}}
	if err := c.get(ctx, "/search/album?"+q.Encode(), &res); err != nil {
		return 0, false, err
	}
	var fallback int64
	for _, a := range res.Data {
		if textnorm.Norm(a.Title) != textnorm.Norm(album) || !strings.Contains(textnorm.Norm(artist), textnorm.Norm(a.Artist.Name)) {
			continue
		}
		if n == 0 || a.NbTracks == n {
			return a.ID, true, nil
		}
		if fallback == 0 {
			fallback = a.ID
		}
	}
	return fallback, fallback != 0, nil
}

func (c *Client) albumTracks(ctx context.Context, id int64) ([]dzTrack, error) {
	var all []dzTrack
	next := "/album/" + strconv.FormatInt(id, 10) + "/tracks?limit=100"
	for next != "" {
		var page struct {
			Data []dzTrack `json:"data"`
			Next string    `json:"next"`
		}
		if err := c.get(ctx, next, &page); err != nil {
			return nil, err
		}
		all = append(all, page.Data...)
		next = page.Next
	}
	return all, nil
}

type dzTrack struct {
	Title         string `json:"title"`
	ISRC          string `json:"isrc"`
	Duration      int    `json:"duration"`
	TrackPosition int    `json:"track_position"`
	DiskNumber    int    `json:"disk_number"`
}

func (d dzTrack) apply(t *spotify.Track, sameRelease bool) {
	if d.ISRC != "" {
		t.ISRC = d.ISRC
	}
	if sameRelease && d.DiskNumber > 0 && d.TrackPosition > 0 {
		t.DiscNumber, t.TrackNumber = d.DiskNumber, d.TrackPosition
	}
}

// get treats path as relative to baseURL, or absolute if it already is.
func (c *Client) get(ctx context.Context, path string, v any) error {
	u := path
	if !strings.HasPrefix(u, "http") {
		u = c.baseURL + path
	}
	for attempt := 0; ; attempt++ {
		if err := c.limiter.Wait(ctx); err != nil {
			return err
		}
		body, err := c.fetch(ctx, u)
		if err != nil {
			return err
		}
		// Deezer reports errors, quota included, as HTTP 200 with an error body.
		var e struct {
			Error *struct {
				Code    int    `json:"code"`
				Message string `json:"message"`
			} `json:"error"`
		}
		if err := json.Unmarshal(body, &e); err != nil {
			return fmt.Errorf("deezer %s: decoding: %w", path, err)
		}
		if e.Error != nil {
			if e.Error.Code == quotaExceeded && attempt == 0 {
				select {
				case <-time.After(quotaWindow):
					continue
				case <-ctx.Done():
					return ctx.Err()
				}
			}
			return fmt.Errorf("deezer %s: %s (code %d)", path, e.Error.Message, e.Error.Code)
		}
		if err := json.Unmarshal(body, v); err != nil {
			return fmt.Errorf("deezer %s: decoding: %w", path, err)
		}
		return nil
	}
}

func (c *Client) fetch(ctx context.Context, u string) ([]byte, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, u, nil)
	if err != nil {
		return nil, err
	}
	resp, err := c.http.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode/100 != 2 {
		return nil, fmt.Errorf("deezer %s: HTTP %d", req.URL.Path, resp.StatusCode)
	}
	return io.ReadAll(io.LimitReader(resp.Body, 4<<20))
}

// sameRecording accepts title variants such as Spotify's "Song - Remastered
// 2009" against Deezer's "Song (Remastered 2009)", but relies on duration to
// tell a remix or live cut apart from the original.
func sameRecording(t spotify.Track, dzTitle string, dzSeconds int) bool {
	return durationClose(t.Duration, dzSeconds) && (textnorm.Norm(dzTitle) == textnorm.Norm(t.Title) || textnorm.Base(dzTitle) == textnorm.Base(t.Title))
}

func durationClose(d time.Duration, dzSeconds int) bool {
	diff := d - time.Duration(dzSeconds)*time.Second
	return diff.Abs() <= durationSlack
}

func hasArtist(artists []string, name string) bool {
	n := textnorm.Norm(name)
	for _, a := range artists {
		if textnorm.Norm(a) == n {
			return true
		}
	}
	return false
}
