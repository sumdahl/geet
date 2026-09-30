package main

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"os/exec"
	"strconv"
	"strings"
	"time"

	"github.com/sumdahl/geet/internal/lyrics"
	"github.com/sumdahl/geet/internal/spotify"
)

// lyricsResult is a song's lyrics as a front end draws them.
type lyricsResult struct {
	Path    string      `json:"path"`
	LRCPath string      `json:"lrc_path,omitempty"`
	Synced  bool        `json:"synced"`
	Lines   []lyricLine `json:"lines"`
}

// lyricsCmd finds the lyrics of a saved song and writes them beside it as a
// .lrc file: for songs saved before the lyrics setting existed, or whose
// lookup failed at download time. The song is named from its own tags.
func lyricsCmd(ctx context.Context, args []string, stdout, stderr io.Writer) int {
	c := newCLI("lyrics", "lyrics [flags] <song file>", stderr)
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
	song := rest[0]
	t, err := songTags(ctx, cfg.Tools.FFprobe, song)
	if err != nil {
		fmt.Fprintf(stderr, "geet: reading %s: %v\n", song, err)
		return exitFatal
	}
	lyr, err := lyrics.New().Fetch(ctx, t)
	res := lyricsResult{Path: song, Lines: []lyricLine{}}
	switch {
	case errors.Is(err, lyrics.ErrNotFound):
		fmt.Fprintf(stderr, "No lyrics for %s - %s.\n", strings.Join(t.Artists, ", "), t.Title)
	case err != nil:
		fmt.Fprintf(stderr, "geet: %v\n", err)
		return exitFatal
	default:
		res.LRCPath = saveLRC(ctx, song, lyr)
		res.Synced = lyr.Synced
		for _, l := range lyr.Lines {
			res.Lines = append(res.Lines, lyricLine{AtMS: l.At.Milliseconds(), Text: l.Text})
		}
	}
	if c.json {
		_ = newJSONEncoder(stdout).Encode(res)
	} else if res.LRCPath != "" {
		fmt.Fprintf(stdout, "%s (%d lines)\n", res.LRCPath, len(res.Lines))
	}
	if len(res.Lines) == 0 {
		return exitPartial
	}
	return exitOK
}

// songTags reads what LRCLIB matches on (title, artists, album, length)
// from a file's tags. Ogg keeps its tags on the audio stream, the other
// containers on the file, so both are read.
func songTags(ctx context.Context, ffprobe, path string) (spotify.Track, error) {
	out, err := exec.CommandContext(ctx, ffprobe, "-v", "error", "-select_streams", "a:0",
		"-show_entries", "format=duration:format_tags:stream_tags", "-of", "json", path).Output()
	if err != nil {
		return spotify.Track{}, err
	}
	var p struct {
		Streams []struct {
			Tags map[string]string `json:"tags"`
		} `json:"streams"`
		Format struct {
			Duration string            `json:"duration"`
			Tags     map[string]string `json:"tags"`
		} `json:"format"`
	}
	if err := json.Unmarshal(out, &p); err != nil {
		return spotify.Track{}, err
	}
	tags := map[string]string{}
	for k, v := range p.Format.Tags {
		tags[strings.ToLower(k)] = v
	}
	for _, s := range p.Streams {
		for k, v := range s.Tags {
			tags[strings.ToLower(k)] = v
		}
	}
	if tags["title"] == "" || tags["artist"] == "" {
		return spotify.Track{}, errors.New("no title or artist tag")
	}
	secs, _ := strconv.ParseFloat(p.Format.Duration, 64)
	return spotify.Track{
		Title:    tags["title"],
		Artists:  strings.Split(tags["artist"], ", "),
		Album:    tags["album"],
		Duration: time.Duration(secs * float64(time.Second)),
	}, nil
}
