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
	"os/exec"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/sumdahl/spotify-dl/internal/spotify"
)

var (
	ErrNoMatch     = errors.New("no YouTube result matched")
	ErrToolMissing = errors.New("yt-dlp not found")
)

type Options struct {
	Binary             string // yt-dlp executable
	SearchQuery        string // with {artists} {artist} {title} {album} placeholders
	SearchResults      int
	MaxDurationDiff    time.Duration
	CookiesFile        string
	CookiesFromBrowser string
	ExtraArgs          []string
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
// candidate with its score or rejection reason.
func (r *Resolver) Resolve(ctx context.Context, t spotify.Track) (Scored, []Scored, error) {
	cands, err := r.Search(ctx, r.Query(t))
	if err != nil {
		return Scored{}, nil, err
	}
	best, all, err := Best(t, cands, r.opts.MaxDurationDiff)
	for _, s := range all {
		slog.DebugContext(ctx, "youtube candidate", "track", t.Title, "id", s.ID, "title", s.Title,
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

func (r *Resolver) Query(t spotify.Track) string {
	artist := ""
	if len(t.Artists) > 0 {
		artist = t.Artists[0]
	}
	return strings.NewReplacer(
		"{artists}", strings.Join(t.Artists, ", "),
		"{artist}", artist,
		"{title}", t.Title,
		"{album}", t.Album,
	).Replace(r.opts.SearchQuery)
}

func (r *Resolver) Search(ctx context.Context, query string) ([]Candidate, error) {
	// --flat-playlist reads the result list without opening each video: about
	// 1.5s instead of several seconds per result, and it still carries the
	// title, channel, duration and view count scoring needs.
	args := []string{"--flat-playlist", "--dump-json", "--no-warnings", "--no-progress"}
	args = append(args, r.cookieArgs()...)
	args = append(args, r.opts.ExtraArgs...)
	args = append(args, "ytsearch"+strconv.Itoa(r.opts.SearchResults)+":"+query)

	var stdout, stderr bytes.Buffer
	cmd := exec.CommandContext(ctx, r.opts.Binary, args...)
	cmd.Stdout, cmd.Stderr = &stdout, &stderr
	if err := cmd.Run(); err != nil {
		if errors.Is(err, exec.ErrNotFound) {
			return nil, fmt.Errorf("%w: %q (install yt-dlp or set tools.yt_dlp)", ErrToolMissing, r.opts.Binary)
		}
		if ctx.Err() != nil {
			return nil, ctx.Err()
		}
		return nil, fmt.Errorf("yt-dlp search %q: %w: %s", query, err, lastLine(stderr.String()))
	}
	return parseCandidates(&stdout)
}

func (r *Resolver) cookieArgs() []string {
	switch {
	case r.opts.CookiesFile != "":
		return []string{"--cookies", r.opts.CookiesFile}
	case r.opts.CookiesFromBrowser != "":
		return []string{"--cookies-from-browser", r.opts.CookiesFromBrowser}
	}
	return nil
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
		if c.URL == "" {
			c.URL = "https://www.youtube.com/watch?v=" + e.ID
		}
		out = append(out, c)
	}
	return out, sc.Err()
}

func lastLine(s string) string {
	s = strings.TrimSpace(s)
	if i := strings.LastIndexByte(s, '\n'); i >= 0 {
		return s[i+1:]
	}
	return s
}
