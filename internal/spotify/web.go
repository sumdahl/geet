package spotify

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"html"
	"io"
	"log/slog"
	"net/http"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"time"

	"golang.org/x/sync/errgroup"
)

const (
	defaultWebURL = "https://open.spotify.com"
	browserUA     = "Mozilla/5.0 (X11; Linux x86_64) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/130.0 Safari/537.36"
	// Spotify server-renders the music:* meta tags (album, track number,
	// release date) only for link-preview crawlers, not for browsers.
	crawlerUA = "facebookexternalhit/1.1"
	// The public playlist embed lists at most this many tracks.
	embedPlaylistCap = 100
	// Spotify separates artists in embed subtitles with a comma and a no-break
	// space, which keeps names like "Tyler, The Creator" intact.
	embedArtistSep = ", "
)

var (
	ErrPageFormat = errors.New("unexpected Spotify page format (did open.spotify.com change?)")

	nextDataRe = regexp.MustCompile(`(?s)<script id="__NEXT_DATA__" type="application/json">(.*?)</script>`)
	metaRe     = regexp.MustCompile(`<meta (?:name|property)="([^"]+)" content="([^"]*)"`)
)

// Web reads metadata from Spotify's public web pages, so it needs no
// credentials. It has no ISRC or disc number; see internal/deezer for those.
type Web struct {
	http    *http.Client
	baseURL string

	// OnProgress, if set, is called as a playlist's tracks are read, one
	// page lookup per track, which is the slow part of resolving a playlist.
	// Calls may come from several goroutines, but never at the same time.
	OnProgress func(done, total int)
	// Workers is how many playlist tracks are read at once (default 1).
	Workers int

	mu     sync.Mutex
	albums map[string]*webAlbum
}

type webAlbum struct {
	name   string
	artist string
	cover  string
	tracks []embedItem
}

func NewWeb(baseURL string) *Web {
	if baseURL == "" {
		baseURL = defaultWebURL
	}
	return &Web{
		http:    &http.Client{Timeout: 30 * time.Second},
		baseURL: strings.TrimRight(baseURL, "/"),
		albums:  make(map[string]*webAlbum),
	}
}

func (w *Web) Resolve(ctx context.Context, ref Ref) (Collection, error) {
	switch ref.Kind {
	case KindTrack:
		t, err := w.Track(ctx, ref.ID)
		if err != nil {
			return Collection{}, err
		}
		return collect(ref, "", []Track{t}), nil
	case KindAlbum:
		tracks, err := w.Album(ctx, ref.ID)
		return collect(ref, "", tracks), err
	case KindPlaylist:
		name, tracks, err := w.Playlist(ctx, ref.ID)
		return collect(ref, name, tracks), err
	default:
		return Collection{}, fmt.Errorf("%w: unsupported type %q", ErrInvalidURL, ref.Kind)
	}
}

func (w *Web) Track(ctx context.Context, id string) (Track, error) {
	meta, err := w.trackMeta(ctx, id)
	if err != nil {
		return Track{}, fmt.Errorf("fetching track %s: %w", id, err)
	}
	albumID := lastSegment(first(meta, "music:album"))
	if albumID == "" {
		return Track{}, fmt.Errorf("track %s: no album link: %w", id, ErrPageFormat)
	}
	alb, err := w.album(ctx, albumID)
	if err != nil {
		return Track{}, fmt.Errorf("fetching album of track %s: %w", id, err)
	}

	t := Track{
		ID:          id,
		Album:       alb.name,
		AlbumArtist: alb.artist,
		CoverURL:    alb.cover,
		Year:        year(first(meta, "music:release_date")),
	}
	t.TrackNumber, _ = strconv.Atoi(first(meta, "music:album:track"))

	if it, ok := findItem(alb.tracks, id); ok {
		t.Title = it.Title
		t.Artists = strings.Split(it.Subtitle, embedArtistSep)
		t.Duration = time.Duration(it.Duration) * time.Millisecond
		return t, nil
	}
	// The album embed didn't list this track; the meta tags are coarser
	// (whole seconds, comma-joined artists) but still usable.
	t.Title = first(meta, "og:title")
	t.Artists = strings.Split(first(meta, "music:musician_description"), ", ")
	secs, _ := strconv.Atoi(first(meta, "music:duration"))
	t.Duration = time.Duration(secs) * time.Second
	return t, nil
}

// Album numbers tracks by their position in the listing, which is wrong from
// the second disc on; internal/deezer corrects disc and track numbers when
// it finds the same album.
func (w *Web) Album(ctx context.Context, id string) ([]Track, error) {
	alb, err := w.album(ctx, id)
	if err != nil {
		return nil, fmt.Errorf("fetching album %s: %w", id, err)
	}
	if len(alb.tracks) == 0 {
		return nil, fmt.Errorf("album %s: empty track list: %w", id, ErrPageFormat)
	}

	// The album embed has no release date, so read it from one track's page.
	var yr int
	if meta, err := w.trackMeta(ctx, itemID(alb.tracks[0])); err == nil {
		yr = year(first(meta, "music:release_date"))
	} else {
		slog.WarnContext(ctx, "could not read album release date", "album", id, "err", err)
	}

	tracks := make([]Track, 0, len(alb.tracks))
	for i, it := range alb.tracks {
		tracks = append(tracks, Track{
			ID:          itemID(it),
			Title:       it.Title,
			Artists:     strings.Split(it.Subtitle, embedArtistSep),
			AlbumArtist: alb.artist,
			Album:       alb.name,
			CoverURL:    alb.cover,
			TrackNumber: i + 1,
			Year:        yr,
			Duration:    time.Duration(it.Duration) * time.Millisecond,
		})
	}
	return tracks, nil
}

func (w *Web) Playlist(ctx context.Context, id string) (string, []Track, error) {
	e, err := w.embed(ctx, KindPlaylist, id)
	if err != nil {
		return "", nil, fmt.Errorf("fetching playlist %s: %w", id, err)
	}
	if len(e.TrackList) >= embedPlaylistCap {
		slog.WarnContext(ctx, "Spotify's public playlist view lists at most 100 tracks; any after that are skipped", "playlist", id)
	}

	// Read tracks in parallel, keeping playlist order: results[i] stays nil
	// for an item that is skipped.
	results := make([]*Track, len(e.TrackList))
	var mu sync.Mutex
	done := 0
	progress := func() {
		mu.Lock()
		defer mu.Unlock()
		done++
		if w.OnProgress != nil {
			w.OnProgress(done, len(e.TrackList))
		}
	}
	if w.OnProgress != nil {
		w.OnProgress(0, len(e.TrackList))
	}
	g, gctx := errgroup.WithContext(ctx)
	g.SetLimit(max(w.Workers, 1))
	for i, it := range e.TrackList {
		g.Go(func() error {
			defer progress()
			if it.EntityType != "" && it.EntityType != "track" {
				slog.DebugContext(gctx, "skipping non-track item", "playlist", id, "type", it.EntityType, "title", it.Title)
				return nil
			}
			t, err := w.Track(gctx, itemID(it))
			if errors.Is(err, ErrNotFound) {
				slog.WarnContext(gctx, "skipping unavailable track", "playlist", id, "title", it.Title)
				return nil
			}
			if err != nil {
				return err
			}
			results[i] = &t
			return nil
		})
	}
	if err := g.Wait(); err != nil {
		return "", nil, err
	}

	var tracks []Track
	for _, t := range results {
		if t != nil {
			tracks = append(tracks, *t)
		}
	}
	return e.Name, tracks, nil
}

func (w *Web) album(ctx context.Context, id string) (*webAlbum, error) {
	w.mu.Lock()
	alb, ok := w.albums[id]
	w.mu.Unlock()
	if ok {
		return alb, nil
	}

	e, err := w.embed(ctx, KindAlbum, id)
	if err != nil {
		return nil, err
	}
	alb = &webAlbum{
		name:   e.Name,
		artist: strings.ReplaceAll(e.Subtitle, embedArtistSep, ", "),
		cover:  e.cover(),
		tracks: e.TrackList,
	}
	w.mu.Lock()
	w.albums[id] = alb
	w.mu.Unlock()
	return alb, nil
}

type embedItem struct {
	URI        string `json:"uri"`
	Title      string `json:"title"`
	Subtitle   string `json:"subtitle"`
	Duration   int    `json:"duration"`
	EntityType string `json:"entityType"`
}

type embedImage struct {
	URL      string `json:"url"`
	MaxWidth int    `json:"maxWidth"`
}

type embedEntity struct {
	Name           string      `json:"name"`
	Subtitle       string      `json:"subtitle"`
	TrackList      []embedItem `json:"trackList"`
	VisualIdentity struct {
		Image []embedImage `json:"image"`
	} `json:"visualIdentity"`
}

func (e embedEntity) cover() string {
	best := -1
	var u string
	for _, img := range e.VisualIdentity.Image {
		if img.MaxWidth > best {
			best, u = img.MaxWidth, img.URL
		}
	}
	return u
}

func (w *Web) embed(ctx context.Context, kind Kind, id string) (embedEntity, error) {
	body, err := w.fetch(ctx, w.baseURL+"/embed/"+string(kind)+"/"+id, browserUA)
	if err != nil {
		return embedEntity{}, err
	}
	m := nextDataRe.FindSubmatch(body)
	if m == nil {
		return embedEntity{}, fmt.Errorf("%s %s: no __NEXT_DATA__: %w", kind, id, ErrPageFormat)
	}
	var doc struct {
		Props struct {
			PageProps struct {
				State struct {
					Data struct {
						Entity *embedEntity `json:"entity"`
					} `json:"data"`
				} `json:"state"`
			} `json:"pageProps"`
		} `json:"props"`
	}
	if err := json.Unmarshal(m[1], &doc); err != nil {
		return embedEntity{}, fmt.Errorf("%s %s: %w: %w", kind, id, ErrPageFormat, err)
	}
	e := doc.Props.PageProps.State.Data.Entity
	if e == nil {
		// A removed or private item still returns 200 with an empty entity.
		return embedEntity{}, fmt.Errorf("%s %s: %w", kind, id, ErrNotFound)
	}
	return *e, nil
}

func (w *Web) trackMeta(ctx context.Context, id string) (map[string][]string, error) {
	body, err := w.fetch(ctx, w.baseURL+"/track/"+id, crawlerUA)
	if err != nil {
		return nil, err
	}
	meta := make(map[string][]string)
	for _, m := range metaRe.FindAllSubmatch(body, -1) {
		k := string(m[1])
		meta[k] = append(meta[k], html.UnescapeString(string(m[2])))
	}
	if len(meta["og:title"]) == 0 {
		return nil, fmt.Errorf("track %s: no metadata tags: %w", id, ErrPageFormat)
	}
	return meta, nil
}

// fetch GETs u, retrying a few times when Spotify rate-limits (HTTP 429),
// which parallel playlist reads can trigger.
func (w *Web) fetch(ctx context.Context, u, userAgent string) ([]byte, error) {
	const attempts = 4
	for attempt := 1; ; attempt++ {
		req, err := http.NewRequestWithContext(ctx, http.MethodGet, u, nil)
		if err != nil {
			return nil, err
		}
		req.Header.Set("User-Agent", userAgent)
		req.Header.Set("Accept-Language", "en")
		resp, err := w.http.Do(req)
		if err != nil {
			return nil, err
		}
		if resp.StatusCode == http.StatusTooManyRequests && attempt < attempts {
			wait := retryAfter(resp.Header.Get("Retry-After")) * time.Duration(attempt)
			resp.Body.Close()
			slog.DebugContext(ctx, "spotify rate limited", "url", u, "retry_in", wait)
			select {
			case <-time.After(wait):
				continue
			case <-ctx.Done():
				return nil, ctx.Err()
			}
		}
		defer resp.Body.Close()
		switch {
		case resp.StatusCode == http.StatusNotFound:
			return nil, fmt.Errorf("%s: %w", u, ErrNotFound)
		case resp.StatusCode/100 != 2:
			return nil, fmt.Errorf("%s: HTTP %d", u, resp.StatusCode)
		}
		return io.ReadAll(io.LimitReader(resp.Body, 8<<20))
	}
}

func first(meta map[string][]string, k string) string {
	if v := meta[k]; len(v) > 0 {
		return v[0]
	}
	return ""
}

func lastSegment(u string) string {
	u = strings.TrimRight(u, "/")
	if i := strings.LastIndexByte(u, '/'); i >= 0 {
		return u[i+1:]
	}
	return ""
}

func itemID(it embedItem) string {
	return strings.TrimPrefix(it.URI, "spotify:track:")
}

func findItem(items []embedItem, id string) (embedItem, bool) {
	for _, it := range items {
		if itemID(it) == id {
			return it, true
		}
	}
	return embedItem{}, false
}
