package main

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/sumdahl/geet/internal/config"
	"github.com/sumdahl/geet/internal/deezer"
	"github.com/sumdahl/geet/internal/itunes"
	"github.com/sumdahl/geet/internal/spotify"
)

// trendingResult is one line of `geet trending --json`. It is the search
// result shape plus a rank, so a front end can reuse the same row it
// already draws for search.
type trendingResult struct {
	searchResult
	Rank int `json:"rank"`
}

// trendingDoc is what the cache holds: the songs, and when they were read.
type trendingDoc struct {
	Source    string           `json:"source"`
	Country   string           `json:"country"`
	FetchedAt time.Time        `json:"fetched_at"`
	Tracks    []spotify.Track  `json:"tracks"`
	Results   []trendingResult `json:"-"`
}

func trendingCmd(ctx context.Context, args []string, stdout, stderr io.Writer) int {
	c := newCLI("trending", "trending [flags]", stderr)
	limit := c.fs.Int("limit", 25, "how many songs to show (1-100)")
	source := c.fs.String("source", "auto", "where the chart comes from: auto, deezer or apple")
	country := c.fs.String("country", "", "Apple store country (two letters); defaults to search.country")
	refresh := c.fs.Bool("refresh", false, "read the chart again instead of using the cached copy")
	play := c.fs.String("play", "", "play these result numbers instead of showing the menu (1, 1,3 or 2-4)")
	pick := c.fs.String("pick", "", "download these result numbers instead of showing the menu")
	if _, err := c.parse(args); errors.Is(err, flag.ErrHelp) {
		return exitOK
	} else if err != nil {
		return exitFatal
	}
	setupLogging(stderr, c.verbose)
	cfg, _, err := c.load()
	if err != nil {
		fmt.Fprintf(stderr, "geet: %v\n", err)
		return exitFatal
	}
	if *country != "" {
		cfg.Search.Country = strings.ToUpper(*country)
	}

	doc, err := trendingNow(ctx, cfg, *source, *limit, *refresh)
	if err != nil {
		fmt.Fprintf(stderr, "geet: %v\n", err)
		return exitFatal
	}
	if len(doc.Results) == 0 {
		fmt.Fprintln(stderr, "No trending songs came back. Try again in a moment.")
		return exitPartial
	}

	if c.json && *play == "" && *pick == "" {
		if err := writeJSON(stdout, doc.Results); err != nil {
			fmt.Fprintf(stderr, "geet: %v\n", err)
			return exitFatal
		}
		return exitOK
	}

	results := make([]itunes.Result, len(doc.Tracks))
	for i, t := range doc.Tracks {
		results[i] = itunes.Result{Track: t}
	}

	// --play and --pick skip the menu; otherwise show the same picker
	// `geet search` uses, and play what was chosen.
	choice := *play
	wantPlay := true
	if *pick != "" {
		choice, wantPlay = *pick, false
	}
	var chosen []int
	if choice != "" {
		var quit bool
		chosen, quit, err = parseChoice(choice, len(results))
		if err == nil && quit {
			err = fmt.Errorf("expected result numbers, got %q", choice)
		}
	} else {
		fmt.Fprintf(stderr, "Trending now (%s%s)\n", doc.Source, agoLabel(doc.FetchedAt))
		chosen, err = pickResults(ctx, cfg.Search.Picker, "trending", results, os.Stdin, stderr)
	}
	if err != nil {
		fmt.Fprintf(stderr, "geet: %v\n", err)
		return exitFatal
	}
	if len(chosen) == 0 {
		fmt.Fprintln(stderr, "Nothing selected.")
		return exitOK
	}

	tracks := make([]spotify.Track, len(chosen))
	for i, p := range chosen {
		tracks[i] = fullTrack(ctx, doc.Tracks[p])
	}
	if wantPlay {
		return playTracks(ctx, c, cfg, tracks, stdout, stderr)
	}
	rep, cfg, err := prepare(c, stdout, stderr)
	if err != nil {
		return rep.fatal(err)
	}
	rep.setUI(chooseUI(cfg.Progress, stderr, c.json))
	setupLogging(rep.ui.writer(), c.verbose)
	col := spotify.Collection{Ref: spotify.Ref{Kind: kindSearch}, Name: "trending", Tracks: tracks}
	res, err := runDownload(ctx, cfg, rep, col, true)
	return rep.exit(c, res, err, stderr)
}

// trendingNow returns the chart, from the cache when it is fresh enough.
func trendingNow(ctx context.Context, cfg config.Config, source string, limit int, refresh bool) (trendingDoc, error) {
	path := trendingCachePath(cfg, source)
	if !refresh {
		if doc, ok := readTrendingCache(path, cfg.Trending.CacheFor.Duration, limit); ok {
			slog.DebugContext(ctx, "trending from cache", "path", path, "age", time.Since(doc.FetchedAt))
			return doc, nil
		}
	}
	doc, err := fetchTrending(ctx, cfg, source, limit)
	if err != nil {
		// A stale chart beats no chart: yesterday's hits are still hits.
		if stale, ok := readTrendingCache(path, 7*24*time.Hour, limit); ok {
			slog.WarnContext(ctx, "showing the cached chart; the live one failed", "err", err)
			return stale, nil
		}
		return trendingDoc{}, err
	}
	writeTrendingCache(path, doc)
	return doc, nil
}

// fetchTrending reads the chart. Deezer is the default because it is
// localised by where the request comes from, which puts local songs in the
// list; Apple's feed is per-country and stands in when Deezer fails.
func fetchTrending(ctx context.Context, cfg config.Config, source string, limit int) (trendingDoc, error) {
	var dz, apple []spotify.Track
	var dzErr, appleErr error
	var wg sync.WaitGroup
	if source == "auto" || source == "deezer" {
		wg.Go(func() { dz, dzErr = deezer.New("").Chart(ctx, limit) })
	}
	if source == "auto" || source == "apple" {
		wg.Go(func() { apple, appleErr = itunes.New("", cfg.Search.Country).TopSongs(ctx, limit) })
	}
	wg.Wait()

	switch {
	case source == "deezer" && dzErr != nil:
		return trendingDoc{}, fmt.Errorf("reading Deezer's chart: %w", dzErr)
	case source == "apple" && appleErr != nil:
		return trendingDoc{}, fmt.Errorf("reading Apple's chart: %w", appleErr)
	case len(dz) == 0 && len(apple) == 0:
		return trendingDoc{}, fmt.Errorf("no chart came back: %w", errors.Join(dzErr, appleErr))
	}

	tracks, from := dz, "Deezer"
	if len(tracks) == 0 {
		tracks, from = apple, "Apple Music"
	} else if len(apple) > 0 && source == "auto" {
		tracks = appendNew(tracks, apple, limit)
		from = "Deezer and Apple Music"
	}
	if len(tracks) > limit {
		tracks = tracks[:limit]
	}

	doc := trendingDoc{Source: from, Country: cfg.Search.Country, FetchedAt: time.Now(), Tracks: tracks}
	doc.Results = make([]trendingResult, len(tracks))
	for i, t := range tracks {
		doc.Results[i] = trendingResult{
			Rank: i + 1,
			searchResult: searchResult{
				Index: i + 1, Ref: t.ID, Title: t.Title, Artists: t.Artists, Album: t.Album,
				AlbumArtist: t.AlbumArtist, Year: t.Year, DurationMS: t.Duration.Milliseconds(),
				CoverURL: t.CoverURL, URL: t.URL(), Clean: t.Clean, Explicit: t.Explicit,
				Label: resultLabel(itunes.Result{Track: t}),
			},
		}
	}
	return doc, nil
}

// appendNew adds songs from the second chart that the first doesn't
// already have, so one song never appears twice from two sources.
func appendNew(have, more []spotify.Track, limit int) []spotify.Track {
	seen := map[string]bool{}
	for _, t := range have {
		seen[songKey(t)] = true
	}
	for _, t := range more {
		if len(have) >= limit {
			break
		}
		if k := songKey(t); !seen[k] {
			seen[k] = true
			have = append(have, t)
		}
	}
	return have
}

func songKey(t spotify.Track) string {
	artist := ""
	if len(t.Artists) > 0 {
		artist = t.Artists[0]
	}
	return strings.ToLower(strings.TrimSpace(artist + " - " + t.Title))
}

// fullTrack fills in what a chart entry leaves out (year, ISRC, featured
// artists), which matching and lyrics both want.
func fullTrack(ctx context.Context, t spotify.Track) spotify.Track {
	id, ok := strings.CutPrefix(t.ID, deezer.RefPrefix)
	if !ok {
		return t
	}
	full, err := deezer.New("").Lookup(ctx, id)
	if err != nil {
		slog.DebugContext(ctx, "couldn't read the song's full details", "track", t.Title, "err", err)
		return t
	}
	return full
}

// ---------------------------------------------------------------- cache

func trendingCachePath(cfg config.Config, source string) string {
	dir, err := os.UserCacheDir()
	if err != nil {
		return ""
	}
	name := "trending-" + source
	if source != "deezer" {
		name += "-" + strings.ToLower(cfg.Search.Country)
	}
	return filepath.Join(dir, "geet", name+".json")
}

func readTrendingCache(path string, maxAge time.Duration, limit int) (trendingDoc, bool) {
	if path == "" {
		return trendingDoc{}, false
	}
	raw, err := os.ReadFile(path)
	if err != nil {
		return trendingDoc{}, false
	}
	var doc trendingDoc
	if err := json.Unmarshal(raw, &doc); err != nil {
		return trendingDoc{}, false
	}
	if time.Since(doc.FetchedAt) > maxAge || len(doc.Tracks) == 0 {
		return trendingDoc{}, false
	}
	if len(doc.Tracks) > limit {
		doc.Tracks = doc.Tracks[:limit]
	}
	doc.Results = make([]trendingResult, len(doc.Tracks))
	for i, t := range doc.Tracks {
		doc.Results[i] = trendingResult{
			Rank: i + 1,
			searchResult: searchResult{
				Index: i + 1, Ref: t.ID, Title: t.Title, Artists: t.Artists, Album: t.Album,
				AlbumArtist: t.AlbumArtist, Year: t.Year, DurationMS: t.Duration.Milliseconds(),
				CoverURL: t.CoverURL, URL: t.URL(), Clean: t.Clean, Explicit: t.Explicit,
				Label: resultLabel(itunes.Result{Track: t}),
			},
		}
	}
	return doc, true
}

func writeTrendingCache(path string, doc trendingDoc) {
	if path == "" {
		return
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return
	}
	raw, err := json.Marshal(doc)
	if err != nil {
		return
	}
	// Written whole, then renamed: a half-written chart must never be read.
	tmp := path + ".tmp"
	if os.WriteFile(tmp, raw, 0o644) == nil {
		_ = os.Rename(tmp, path)
	}
}

// agoLabel says how old a cached chart is, in words.
func agoLabel(at time.Time) string {
	if at.IsZero() {
		return ""
	}
	d := time.Since(at)
	switch {
	case d < 2*time.Minute:
		return ", just now"
	case d < time.Hour:
		return fmt.Sprintf(", %d minutes ago", int(d.Minutes()))
	case d < 2*time.Hour:
		return ", an hour ago"
	default:
		return fmt.Sprintf(", %d hours ago", int(d.Hours()))
	}
}
