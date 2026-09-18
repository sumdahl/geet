// Package youtube finds the YouTube upload matching a Spotify track. Search
// goes through the yt-dlp binary; this package only builds the query and
// scores what comes back.
package youtube

import (
	"bufio"
	"bytes"
	"cmp"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/url"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/sumdahl/geet/internal/spotify"
	"github.com/sumdahl/geet/internal/textnorm"
	"github.com/sumdahl/geet/internal/ytdlp"
)

var ErrNoMatch = errors.New("no YouTube result matched")

type Options struct {
	YtDlp           ytdlp.Runner
	SearchQuery     string // with {artists} {artist} {title} {album} placeholders
	FallbackQuery   string // searched when nothing from SearchQuery matches; "" disables
	SearchResults   int
	MaxDurationDiff time.Duration
	// MusicFallback searches YouTube Music's songs when neither query
	// matches (see resolveMusic).
	MusicFallback bool
}

type Candidate struct {
	ID       string
	URL      string
	Title    string
	Channel  string
	Duration time.Duration
	Views    int64
	Verified bool
	Live     bool
}

type Resolver struct {
	opts Options
}

func New(opts Options) *Resolver {
	return &Resolver{opts: opts}
}

// Resolve searches YouTube for t and returns the best match, plus every
// candidate with its score or rejection reason. If nothing matches, it
// searches again with the fallback query: an official video with an intro
// can push the first page past the duration limit while the artist's
// separate "(Audio)" upload only turns up when asked for.
//
// Last, if still nothing matches, it tries YouTube Music (MusicFallback).
func (r *Resolver) Resolve(ctx context.Context, t spotify.Track) (Scored, []Scored, error) {
	best, all, err := r.resolve(ctx, t, r.opts.SearchQuery)
	if errors.Is(err, ErrNoMatch) && r.opts.FallbackQuery != "" {
		var more []Scored
		best, more, err = r.resolve(ctx, t, r.opts.FallbackQuery)
		all = append(all, more...)
	}
	if errors.Is(err, ErrNoMatch) && r.opts.MusicFallback {
		var more []Scored
		best, more, err = r.resolveMusic(ctx, t)
		all = append(all, more...)
	}
	return best, all, err
}

const (
	// musicResults is how many YouTube Music song results are listed.
	musicResults = 5
	// musicDetails caps how many of them are opened for their length and
	// channel, which the listing lacks and which cost about 2s each.
	musicDetails = 3
)

// resolveMusic searches YouTube Music's "Songs", which lists the official
// studio audio (the artist's "- Topic" channel) at the album's exact length.
// Regular search buries those under music videos with intros, lyric and fan
// uploads; songs with romanized titles ("Maayajastai" vs "Maayaajastai"),
// classical pieces and small artists are the usual misses. The listing has
// no length or channel, so only results whose title fits are opened, and
// they're scored like any other candidate.
func (r *Resolver) resolveMusic(ctx context.Context, t spotify.Track) (Scored, []Scored, error) {
	query := r.fill("{artist} {title}", t)
	u := "https://music.youtube.com/search?q=" + url.QueryEscape(query) + "#songs"
	out, err := r.opts.YtDlp.Run(ctx, "--flat-playlist", "--dump-json", "--no-warnings", "--no-progress",
		"--playlist-items", "1-"+strconv.Itoa(musicResults), u)
	if err != nil {
		return Scored{}, nil, fmt.Errorf("searching YouTube Music for %q: %w", query, err)
	}
	listed, err := parseCandidates(bytes.NewReader(out))
	if err != nil {
		return Scored{}, nil, err
	}
	var open []string
	want := titleWords(t)
	for _, c := range listed {
		if tokenCoverage(want, textnorm.Words(c.Title)) >= minTitleCoverage {
			open = append(open, c.URL)
		}
		if len(open) == musicDetails {
			break
		}
	}
	slog.DebugContext(ctx, "youtube music", "track", t.Title, "query", query, "listed", len(listed), "opening", len(open))
	if len(open) == 0 {
		return Scored{}, nil, fmt.Errorf("%w for %q", ErrNoMatch, t.Title)
	}

	// --ignore-errors: one unavailable result mustn't hide the others, so
	// the output is read even when yt-dlp reports a failure.
	out, err = r.opts.YtDlp.Run(ctx, append([]string{"--dump-json", "--skip-download", "--ignore-errors", "--no-warnings", "--no-progress"}, open...)...)
	if err != nil && (ctx.Err() != nil || errors.Is(err, ytdlp.ErrToolMissing) || errors.Is(err, ytdlp.ErrBotCheck) || len(out) == 0) {
		return Scored{}, nil, fmt.Errorf("reading YouTube Music results for %q: %w", query, err)
	}
	cands, err := parseCandidates(bytes.NewReader(out))
	if err != nil {
		return Scored{}, nil, err
	}
	best, all, err := Best(t, cands, r.opts.MaxDurationDiff)
	for _, s := range all {
		slog.DebugContext(ctx, "youtube candidate", "track", t.Title, "query", "youtube music: "+query, "id", s.ID, "title", s.Title,
			"channel", s.Channel, "score", fmt.Sprintf("%.1f", s.Score), "reject", s.Reject)
	}
	return best, all, err
}

func (r *Resolver) resolve(ctx context.Context, t spotify.Track, query string) (Scored, []Scored, error) {
	cands, err := r.Search(ctx, r.fill(query, t))
	if err != nil {
		return Scored{}, nil, err
	}
	best, all, err := Best(t, cands, r.opts.MaxDurationDiff)
	for _, s := range all {
		slog.DebugContext(ctx, "youtube candidate", "track", t.Title, "query", query, "id", s.ID, "title", s.Title,
			"channel", s.Channel, "score", fmt.Sprintf("%.1f", s.Score), "reject", s.Reject)
	}
	return best, all, err
}

// Best picks the highest-scoring candidate that wasn't rejected. cands must be
// in search-rank order, which breaks near-ties.
func Best(t spotify.Track, cands []Candidate, maxDiff time.Duration) (Scored, []Scored, error) {
	all := make([]Scored, len(cands))
	for i, c := range cands {
		all[i] = score(t, c, i, len(cands), maxDiff)
	}
	ok := slices.DeleteFunc(slices.Clone(all), func(s Scored) bool { return s.Reject != "" })
	if len(ok) == 0 {
		return Scored{}, all, fmt.Errorf("%w for %q", ErrNoMatch, t.Title)
	}
	best := slices.MaxFunc(ok, func(a, b Scored) int { return cmp.Compare(a.Score, b.Score) })
	return best, all, nil
}

// Query is the first search text for t.
func (r *Resolver) Query(t spotify.Track) string {
	return r.fill(r.opts.SearchQuery, t)
}

func (r *Resolver) fill(query string, t spotify.Track) string {
	artist := ""
	if len(t.Artists) > 0 {
		artist = t.Artists[0]
	}
	return strings.NewReplacer(
		"{artists}", strings.Join(t.Artists, ", "),
		"{artist}", artist,
		"{title}", t.Title,
		"{album}", t.Album,
	).Replace(query)
}

func (r *Resolver) Search(ctx context.Context, query string) ([]Candidate, error) {
	// --flat-playlist reads the result list without opening each video: about
	// 1.5s instead of several seconds per result, and it still carries the
	// title, channel, duration and view count scoring needs.
	out, err := r.opts.YtDlp.Run(ctx, "--flat-playlist", "--dump-json", "--no-warnings", "--no-progress",
		"ytsearch"+strconv.Itoa(r.opts.SearchResults)+":"+query)
	if err != nil {
		return nil, fmt.Errorf("searching %q: %w", query, err)
	}
	return parseCandidates(bytes.NewReader(out))
}

func parseCandidates(r io.Reader) ([]Candidate, error) {
	var out []Candidate
	sc := bufio.NewScanner(r)
	sc.Buffer(make([]byte, 0, 64<<10), 4<<20)
	for sc.Scan() {
		line := bytes.TrimSpace(sc.Bytes())
		if len(line) == 0 || line[0] != '{' {
			continue
		}
		var e struct {
			ID         string  `json:"id"`
			URL        string  `json:"url"`
			WebpageURL string  `json:"webpage_url"`
			Title      string  `json:"title"`
			Channel    string  `json:"channel"`
			Uploader   string  `json:"uploader"`
			Duration   float64 `json:"duration"`
			ViewCount  int64   `json:"view_count"`
			Verified   bool    `json:"channel_is_verified"`
			LiveStatus string  `json:"live_status"`
		}
		if err := json.Unmarshal(line, &e); err != nil {
			return nil, fmt.Errorf("parsing yt-dlp output: %w", err)
		}
		if e.ID == "" {
			continue
		}
		c := Candidate{
			ID:       e.ID,
			URL:      e.URL,
			Title:    e.Title,
			Channel:  cmp.Or(e.Channel, e.Uploader),
			Duration: time.Duration(e.Duration * float64(time.Second)),
			Views:    e.ViewCount,
			Verified: e.Verified,
			Live:     e.LiveStatus == "is_live" || e.LiveStatus == "is_upcoming",
		}
		// A single video's full details put a media stream in "url", which
		// expires; the watch page is what the downloader needs.
		switch {
		case e.WebpageURL != "":
			c.URL = e.WebpageURL
		case !strings.Contains(c.URL, "watch?v="):
			c.URL = "https://www.youtube.com/watch?v=" + e.ID
		}
		out = append(out, c)
	}
	return out, sc.Err()
}
