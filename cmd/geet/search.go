package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"strings"

	"github.com/sumdahl/geet/internal/config"
	"github.com/sumdahl/geet/internal/itunes"
	"github.com/sumdahl/geet/internal/spotify"
)

// kindSearch labels a collection built from search picks: it downloads like
// single tracks (no playlist folder).
const kindSearch spotify.Kind = "search"

// searchResult is one entry of `geet search --json`: what a front end (the
// Omarchy plugin) needs to show a menu and then run `geet download <ref>`.
type searchResult struct {
	Index       int      `json:"index"` // 1-based, as --pick takes it
	Ref         string   `json:"ref"`   // pass to `geet download`
	Title       string   `json:"title"`
	Artists     []string `json:"artists"`
	Album       string   `json:"album"`
	AlbumArtist string   `json:"album_artist"`
	Year        int      `json:"year,omitempty"`
	DurationMS  int64    `json:"duration_ms"`
	CoverURL    string   `json:"cover_url"`
	URL         string   `json:"url"`
	Clean       bool     `json:"clean,omitempty"`
	Editions    int      `json:"editions"`
	Label       string   `json:"label"` // the picker's one-line description
}

func searchCmd(ctx context.Context, args []string, stdout, stderr io.Writer) int {
	c := newCLI("search", "search [flags] <words…>", stderr)
	pick := c.fs.String("pick", "", "choose without a menu: result numbers such as 1, 1,3 or 2-4")
	var yes bool
	c.fs.BoolVar(&yes, "yes", false, "download menu picks without asking to confirm")
	c.fs.BoolVar(&yes, "y", false, "short for --yes")
	positional, err := c.parse(args)
	if errors.Is(err, flag.ErrHelp) {
		return exitOK
	}
	if err != nil {
		return exitFatal
	}
	query := strings.TrimSpace(strings.Join(positional, " "))
	if query == "" {
		c.fs.Usage()
		return exitFatal
	}

	// Listing needs neither yt-dlp nor ffmpeg; only downloading does.
	if c.json && *pick == "" {
		setupLogging(stderr, c.verbose)
		cfg, _, err := c.load()
		if err != nil {
			fmt.Fprintf(stderr, "geet: %v\n", err)
			return exitFatal
		}
		results, err := searchCatalog(ctx, cfg, query)
		if err != nil {
			fmt.Fprintf(stderr, "geet: %v\n", err)
			return exitFatal
		}
		out := make([]searchResult, len(results))
		for i, r := range results {
			out[i] = searchResult{
				Index: i + 1, Ref: r.ID, Title: r.Title, Artists: r.Artists, Album: r.Album,
				AlbumArtist: r.AlbumArtist, Year: r.Year, DurationMS: r.Duration.Milliseconds(),
				CoverURL: r.CoverURL, URL: r.URL(), Clean: r.Clean, Editions: r.Editions, Label: resultLabel(r),
			}
		}
		if err := writeJSON(stdout, out); err != nil {
			fmt.Fprintf(stderr, "geet: %v\n", err)
			return exitFatal
		}
		return exitOK
	}

	rep, cfg, err := prepare(c, stdout, stderr)
	if err != nil {
		return rep.fatal(err)
	}
	results, err := searchCatalog(ctx, cfg, query)
	if err != nil {
		return rep.fatal(err)
	}
	if len(results) == 0 {
		fmt.Fprintf(stderr, "No songs found for %q in the iTunes %s store.\n", query, strings.ToUpper(cfg.Search.Country))
		return exitPartial
	}

	var picked []int
	if *pick != "" {
		var quit bool
		picked, quit, err = parseChoice(*pick, len(results))
		if err == nil && quit {
			err = fmt.Errorf("--pick %q: expected result numbers", *pick)
		}
	} else {
		picked, err = pickResults(ctx, cfg.Search.Picker, query, results, os.Stdin, stderr)
	}
	if err != nil {
		return rep.fatal(err)
	}
	if len(picked) == 0 {
		fmt.Fprintln(stderr, "Nothing selected.")
		return exitOK
	}
	// A stray Enter in the menu shouldn't start a download: show the picks
	// and ask once. --pick is already deliberate, so it never asks.
	if *pick == "" && cfg.Search.Confirm && !yes {
		labels := make([]string, len(picked))
		for i, p := range picked {
			labels[i] = resultLabel(results[p])
		}
		if !confirmDownload(os.Stdin, stderr, labels) {
			fmt.Fprintln(stderr, "Cancelled: nothing downloaded.")
			return exitOK
		}
	}

	tracks := make([]spotify.Track, len(picked))
	for i, p := range picked {
		tracks[i] = results[p].Track
	}
	rep.ui = chooseUI(cfg.Progress, stderr, c.json)
	setupLogging(rep.ui.writer(), c.verbose)
	col := spotify.Collection{Ref: spotify.Ref{Kind: kindSearch}, Name: query, Tracks: tracks}
	return runDownload(ctx, c, cfg, rep, col, true, stderr)
}

// searchCatalog searches iTunes and returns the best results, ranked and
// with album editions merged, capped at search.limit.
func searchCatalog(ctx context.Context, cfg config.Config, query string) ([]itunes.Result, error) {
	tracks, err := itunes.New("", cfg.Search.Country).Search(ctx, query)
	if err != nil {
		return nil, fmt.Errorf("searching %q: %w", query, err)
	}
	results := itunes.Rank(query, tracks)
	return results[:min(len(results), cfg.Search.Limit)], nil
}
