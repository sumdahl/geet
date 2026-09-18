// Package download fetches a YouTube upload's best audio stream as-is.
// Converting and tagging it is internal/audio's job, done in one ffmpeg pass,
// because yt-dlp's own conversion ignores the requested bitrate whenever the
// source is already in the target codec.
package download

import (
	"context"
	"errors"
	"fmt"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/sumdahl/spotify-dl/internal/ytdlp"
)

// Source is the downloaded stream, with what YouTube reports about it.
type Source struct {
	Path  string
	Codec string  // e.g. "opus", "mp4a.40.2"
	Kbps  float64 // 0 when YouTube doesn't say
}

// Fetch downloads url's best audio into dir. Opus is preferred: it is
// YouTube's highest-quality audio (~150 kbps) and can be kept without
// re-encoding when the target is opus too.
func Fetch(ctx context.Context, y ytdlp.Runner, url, dir string) (Source, error) {
	out, err := y.Run(ctx,
		"--format", "bestaudio[acodec=opus]/bestaudio",
		"--no-playlist", "--no-warnings", "--no-progress",
		"--output", filepath.Join(dir, "source.%(ext)s"),
		"--print", "after_move:%(filepath)s\t%(acodec)s\t%(abr)s",
		"--", url)
	if err != nil {
		return Source{}, fmt.Errorf("downloading %s: %w", url, err)
	}
	return parsePrint(string(out))
}

func parsePrint(out string) (Source, error) {
	line := strings.TrimSpace(out)
	if i := strings.LastIndexByte(line, '\n'); i >= 0 {
		line = line[i+1:]
	}
	fields := strings.Split(line, "\t")
	if len(fields) != 3 || fields[0] == "" {
		return Source{}, errors.New("yt-dlp did not report the downloaded file")
	}
	s := Source{Path: fields[0], Codec: fields[1]}
	if s.Codec == "NA" {
		s.Codec = ""
	}
	s.Kbps, _ = strconv.ParseFloat(fields[2], 64) // "NA" when unknown
	return s, nil
}
