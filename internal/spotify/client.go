package spotify

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	"golang.org/x/oauth2"
	"golang.org/x/oauth2/clientcredentials"
)

const (
	defaultBaseURL  = "https://api.spotify.com/v1"
	defaultTokenURL = "https://accounts.spotify.com/api/token"
	maxTracksPerGet = 50
	maxRetryAfter   = 30 * time.Second
)

var (
	ErrAuth      = errors.New("spotify authentication failed")
	ErrForbidden = errors.New("spotify refused the request")
	ErrNotFound  = errors.New("spotify resource not found")
)

type Track struct {
	ID          string
	Title       string
	Artists     []string
	AlbumArtist string
	Album       string
	CoverURL    string
	TrackNumber int
	DiscNumber  int
	Year        int
	Duration    time.Duration
	ISRC        string
}

func (t Track) URL() string { return Ref{Kind: KindTrack, ID: t.ID}.URL() }

type Client struct {
	http    *http.Client
	baseURL string
}

type options struct {
	baseURL  string
	tokenURL string
}

type Option func(*options)

func WithBaseURL(u string) Option  { return func(o *options) { o.baseURL = u } }
func WithTokenURL(u string) Option { return func(o *options) { o.tokenURL = u } }

func New(clientID, clientSecret string, opts ...Option) *Client {
	o := options{baseURL: defaultBaseURL, tokenURL: defaultTokenURL}
	for _, opt := range opts {
		opt(&o)
	}
	cc := clientcredentials.Config{
		ClientID:     clientID,
		ClientSecret: clientSecret,
		TokenURL:     o.tokenURL,
	}
	// The token source outlives any single request, so it must not be bound
	// to a request context; per-request cancellation comes from req.Context().
	hc := cc.Client(context.Background())
	hc.Timeout = 30 * time.Second
	return &Client{http: hc, baseURL: strings.TrimRight(o.baseURL, "/")}
}

func (c *Client) Resolve(ctx context.Context, ref Ref) ([]Track, error) {
	switch ref.Kind {
	case KindTrack:
		t, err := c.Track(ctx, ref.ID)
		if err != nil {
			return nil, err
		}
		return []Track{t}, nil
	case KindAlbum:
		return c.Album(ctx, ref.ID)
	case KindPlaylist:
		return c.Playlist(ctx, ref.ID)
	default:
		return nil, fmt.Errorf("%w: unsupported type %q", ErrInvalidURL, ref.Kind)
	}
}

func (c *Client) Track(ctx context.Context, id string) (Track, error) {
	var t apiTrack
	if err := c.get(ctx, c.baseURL+"/tracks/"+url.PathEscape(id), &t); err != nil {
		return Track{}, fmt.Errorf("fetching track %s: %w", id, err)
	}
	return t.toTrack(), nil
}

// Album returns every track on the album. The album-tracks listing only has
// simplified track objects (no ISRC), so ISRCs are filled in from the batch
// tracks endpoint afterwards.
func (c *Client) Album(ctx context.Context, id string) ([]Track, error) {
	var a struct {
		apiAlbum
		Tracks page[apiTrack] `json:"tracks"`
	}
	if err := c.get(ctx, c.baseURL+"/albums/"+url.PathEscape(id), &a); err != nil {
		return nil, fmt.Errorf("fetching album %s: %w", id, err)
	}
	items := a.Tracks.Items
	for next := a.Tracks.Next; next != ""; {
		var p page[apiTrack]
		if err := c.get(ctx, next, &p); err != nil {
			return nil, fmt.Errorf("paging album %s: %w", id, err)
		}
		items = append(items, p.Items...)
		next = p.Next
	}

	tracks := make([]Track, 0, len(items))
	for _, it := range items {
		it.Album = a.apiAlbum
		tracks = append(tracks, it.toTrack())
	}
	if err := c.fillISRC(ctx, tracks); err != nil {
		return nil, fmt.Errorf("fetching ISRCs for album %s: %w", id, err)
	}
	return tracks, nil
}

func (c *Client) fillISRC(ctx context.Context, tracks []Track) error {
	for start := 0; start < len(tracks); start += maxTracksPerGet {
		batch := tracks[start:min(start+maxTracksPerGet, len(tracks))]
		ids := make([]string, len(batch))
		for i, t := range batch {
			ids[i] = t.ID
		}
		var resp struct {
			Tracks []*apiTrack `json:"tracks"`
		}
		u := c.baseURL + "/tracks?ids=" + url.QueryEscape(strings.Join(ids, ","))
		if err := c.get(ctx, u, &resp); err != nil {
			return err
		}
		isrc := make(map[string]string, len(resp.Tracks))
		for _, t := range resp.Tracks {
			if t != nil {
				isrc[t.ID] = t.ExternalIDs.ISRC
			}
		}
		for i := range batch {
			batch[i].ISRC = isrc[batch[i].ID]
		}
	}
	return nil
}

func (c *Client) Playlist(ctx context.Context, id string) ([]Track, error) {
	type item struct {
		Track *apiTrack `json:"track"`
	}
	next := c.baseURL + "/playlists/" + url.PathEscape(id) + "/tracks?limit=100&additional_types=track"
	var tracks []Track
	for next != "" {
		var p page[item]
		if err := c.get(ctx, next, &p); err != nil {
			return nil, fmt.Errorf("fetching playlist %s: %w", id, err)
		}
		for _, it := range p.Items {
			switch {
			case it.Track == nil:
				slog.DebugContext(ctx, "skipping unavailable playlist item", "playlist", id)
			case it.Track.IsLocal:
				slog.DebugContext(ctx, "skipping local file", "playlist", id, "title", it.Track.Name)
			case it.Track.Type != "" && it.Track.Type != "track":
				slog.DebugContext(ctx, "skipping non-track item", "playlist", id, "type", it.Track.Type, "title", it.Track.Name)
			default:
				tracks = append(tracks, it.Track.toTrack())
			}
		}
		next = p.Next
	}
	return tracks, nil
}

func (c *Client) get(ctx context.Context, u string, v any) error {
	for attempt := 0; ; attempt++ {
		req, err := http.NewRequestWithContext(ctx, http.MethodGet, u, nil)
		if err != nil {
			return err
		}
		resp, err := c.http.Do(req)
		if err != nil {
			if _, ok := errors.AsType[*oauth2.RetrieveError](err); ok {
				return fmt.Errorf("%w: %w", ErrAuth, err)
			}
			return err
		}

		if resp.StatusCode == http.StatusTooManyRequests && attempt == 0 {
			wait := retryAfter(resp.Header.Get("Retry-After"))
			resp.Body.Close()
			slog.DebugContext(ctx, "spotify rate limited", "retry_after", wait)
			select {
			case <-time.After(wait):
				continue
			case <-ctx.Done():
				return ctx.Err()
			}
		}

		err = decode(resp, v)
		resp.Body.Close()
		return err
	}
}

func decode(resp *http.Response, v any) error {
	if resp.StatusCode/100 == 2 {
		if err := json.NewDecoder(resp.Body).Decode(v); err != nil {
			return fmt.Errorf("decoding %s: %w", resp.Request.URL.Path, err)
		}
		return nil
	}

	body, _ := io.ReadAll(io.LimitReader(resp.Body, 4096))
	var apiErr struct {
		Error struct {
			Message string `json:"message"`
		} `json:"error"`
	}
	msg := strings.TrimSpace(string(body))
	if json.Unmarshal(body, &apiErr) == nil && apiErr.Error.Message != "" {
		msg = apiErr.Error.Message
	}

	var sentinel error
	switch resp.StatusCode {
	case http.StatusUnauthorized:
		sentinel = ErrAuth
	case http.StatusForbidden:
		sentinel = ErrForbidden
	case http.StatusNotFound:
		sentinel = ErrNotFound
	default:
		return fmt.Errorf("spotify %s: HTTP %d: %s", resp.Request.URL.Path, resp.StatusCode, msg)
	}
	return fmt.Errorf("%w (HTTP %d): %s", sentinel, resp.StatusCode, msg)
}

func retryAfter(h string) time.Duration {
	secs, err := strconv.Atoi(h)
	if err != nil || secs < 1 {
		return time.Second
	}
	return min(time.Duration(secs)*time.Second, maxRetryAfter)
}

type page[T any] struct {
	Items []T    `json:"items"`
	Next  string `json:"next"`
}

type apiArtist struct {
	Name string `json:"name"`
}

type apiImage struct {
	URL   string `json:"url"`
	Width int    `json:"width"`
}

type apiAlbum struct {
	Name        string      `json:"name"`
	Artists     []apiArtist `json:"artists"`
	Images      []apiImage  `json:"images"`
	ReleaseDate string      `json:"release_date"`
}

type apiTrack struct {
	ID          string      `json:"id"`
	Name        string      `json:"name"`
	Type        string      `json:"type"`
	IsLocal     bool        `json:"is_local"`
	Artists     []apiArtist `json:"artists"`
	Album       apiAlbum    `json:"album"`
	TrackNumber int         `json:"track_number"`
	DiscNumber  int         `json:"disc_number"`
	DurationMS  int         `json:"duration_ms"`
	ExternalIDs struct {
		ISRC string `json:"isrc"`
	} `json:"external_ids"`
}

func (t apiTrack) toTrack() Track {
	return Track{
		ID:          t.ID,
		Title:       t.Name,
		Artists:     names(t.Artists),
		AlbumArtist: strings.Join(names(t.Album.Artists), ", "),
		Album:       t.Album.Name,
		CoverURL:    largest(t.Album.Images),
		TrackNumber: t.TrackNumber,
		DiscNumber:  t.DiscNumber,
		Year:        year(t.Album.ReleaseDate),
		Duration:    time.Duration(t.DurationMS) * time.Millisecond,
		ISRC:        t.ExternalIDs.ISRC,
	}
}

func names(as []apiArtist) []string {
	out := make([]string, len(as))
	for i, a := range as {
		out[i] = a.Name
	}
	return out
}

func largest(imgs []apiImage) string {
	best := -1
	var u string
	for _, img := range imgs {
		if img.Width > best {
			best, u = img.Width, img.URL
		}
	}
	return u
}

// release_date precision varies ("2011", "2011-03", "2011-03-14").
func year(date string) int {
	if len(date) < 4 {
		return 0
	}
	y, _ := strconv.Atoi(date[:4])
	return y
}
