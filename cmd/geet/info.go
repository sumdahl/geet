package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"strings"

	"github.com/sumdahl/geet/internal/config"
	"github.com/sumdahl/geet/internal/deezer"
	"github.com/sumdahl/geet/internal/itunes"
	"github.com/sumdahl/geet/internal/spotify"
)

// infoResult is what a link is, before anything downloads: what a front end
// shows when a link is shared to it.
type infoResult struct {
	Kind     string      `json:"kind"`   // track, album or playlist
	Source   string      `json:"source"` // spotify, youtube, apple or deezer
	Name     string      `json:"name"`
	CoverURL string      `json:"cover_url,omitempty"`
	Total    int         `json:"total"` // songs, counting any the list below leaves out
	Tracks   []infoTrack `json:"tracks"`
}

type infoTrack struct {
	Ref        string   `json:"ref"`
	Title      string   `json:"title"`
	Artists    []string `json:"artists"`
	DurationMS int64    `json:"duration_ms"`
	Explicit   bool     `json:"explicit,omitempty"`
	CoverURL   string   `json:"cover_url,omitempty"`
}

func infoCmd(ctx context.Context, args []string, stdout, stderr io.Writer) int {
	c := newCLI("info", "info [flags] <link>", stderr)
	rest, err := c.parse(args)
	if errors.Is(err, flag.ErrHelp) {
		return exitOK
	} else if err != nil {
		return exitFatal
	}
	if len(rest) != 1 {
		c.fs.Usage()
		return exitFatal
	}
	setupLogging(stderr, c.verbose)
	cfg, _, err := c.load()
	if err != nil {
		fmt.Fprintf(stderr, "geet: %v\n", err)
		return exitFatal
	}
	res, err := linkInfo(ctx, cfg, &reporter{ui: &plainUI{w: stderr}, stderr: stderr, warned: map[string]bool{}}, rest[0])
	if err != nil {
		fmt.Fprintf(stderr, "geet: %v\n", err)
		return exitFatal
	}
	if c.json {
		if err := newJSONEncoder(stdout).Encode(res); err != nil {
			return exitFatal
		}
		return exitOK
	}
	fmt.Fprintf(stdout, "%s %q from %s: %d song(s)\n", res.Kind, res.Name, res.Source, res.Total)
	for i, t := range res.Tracks {
		fmt.Fprintf(stdout, "%3d. %s - %s\n", i+1, strings.Join(t.Artists, ", "), t.Title)
	}
	return exitOK
}

// linkInfo reads a link cheaply: one embed page for Spotify (not a page per
// song, which is what downloading needs), the flat listing for YouTube, and
// the one catalog entry for Apple and Deezer.
func linkInfo(ctx context.Context, cfg config.Config, rep *reporter, link string) (infoResult, error) {
	var col spotify.Collection
	var source string
	var err error
	switch {
	case itunes.IsRef(link):
		source = "apple"
		col, err = lookupITunes(ctx, cfg, link)
	case deezer.IsRef(link):
		source = "deezer"
		col, err = lookupDeezer(ctx, link)
	case isYouTube(link):
		source = "youtube"
		col, err = readYouTube(ctx, cfg, rep, link)
	default:
		source = "spotify"
		var ref spotify.Ref
		if ref, err = spotify.ParseURL(link); err == nil {
			col, err = newWeb(ctx, cfg, func(string, int, int) {}).Preview(ctx, ref)
		}
	}
	if err != nil {
		return infoResult{}, err
	}
	res := infoResult{Kind: string(col.Ref.Kind), Source: source, Name: col.Name, Total: max(col.Total, len(col.Tracks)), Tracks: []infoTrack{}}
	for _, t := range col.Tracks {
		if res.CoverURL == "" {
			res.CoverURL = t.CoverURL
		}
		res.Tracks = append(res.Tracks, infoTrack{
			Ref: t.URL(), Title: t.Title, Artists: t.Artists, DurationMS: t.Duration.Milliseconds(), Explicit: t.Explicit, CoverURL: t.CoverURL,
		})
	}
	return res, nil
}
