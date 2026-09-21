package player

import (
	"context"
	"fmt"
	"strings"

	"github.com/sumdahl/geet/internal/spotify"
	"github.com/sumdahl/geet/internal/youtube"
	"github.com/sumdahl/geet/internal/ytdlp"
)

// Streamer turns a song into something mpv can play straight away, without
// downloading it first: geet finds the same YouTube match a download would
// use, then asks yt-dlp for the audio stream's direct URL. Listening starts
// in a couple of seconds instead of after a whole download, and the song is
// only kept if the listener asks for it.
type Streamer struct {
	YouTube *youtube.Resolver
	Runner  ytdlp.Runner
}

// Stream is a playable URL for one song.
type Stream struct {
	// Direct is the audio URL mpv and ffmpeg read. It expires after a few
	// hours, which is far longer than a song.
	Direct string
	// Page is the YouTube watch URL the match came from, kept for the
	// download that follows if the listener keeps the song.
	Page string
	// Warning is a caveat about the match (a title-only match, say).
	Warning string
}

// Resolve finds a song on YouTube and returns its audio URL.
func (s *Streamer) Resolve(ctx context.Context, t spotify.Track) (Stream, error) {
	best, _, err := s.YouTube.Resolve(ctx, t)
	if err != nil {
		return Stream{}, err
	}
	direct, err := s.direct(ctx, best.URL)
	if err != nil {
		return Stream{}, err
	}
	out := Stream{Direct: direct, Page: best.URL}
	if best.TitleOnly {
		out.Warning = "matched by title alone: this may not be the right recording"
	}
	return out, nil
}

// direct asks yt-dlp for the media URL of the best audio-only format,
// falling back to a combined stream for uploads that have no audio-only
// one. -g prints URLs and downloads nothing.
func (s *Streamer) direct(ctx context.Context, page string) (string, error) {
	out, err := s.Runner.Run(ctx, "-g", "-f", "bestaudio/best", "--no-playlist", page)
	if err != nil {
		return "", err
	}
	// With a combined stream yt-dlp prints video and audio URLs, in that
	// order; the last one is the audio.
	var last string
	for _, line := range strings.Split(string(out), "\n") {
		if line = strings.TrimSpace(line); strings.HasPrefix(line, "http") {
			last = line
		}
	}
	if last == "" {
		return "", fmt.Errorf("yt-dlp gave no stream URL for %s", page)
	}
	return last, nil
}
