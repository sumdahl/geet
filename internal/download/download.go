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

	"github.com/sumdahl/geet/internal/ytdlp"
)

// Source is the downloaded stream, with what YouTube reports about it.
type Source struct {
	Path  string
	Codec string  // e.g. "opus", "mp4a.40.2"
	Kbps  float64 // 0 when YouTube doesn't say
}

// progressPrefix marks yt-dlp's progress lines on stdout, where the final
// --print line also goes.
const progressPrefix = "GEET-PROGRESS "

// Fetch downloads url's best audio into dir. Opus is preferred: it is
// YouTube's highest-quality audio (~150 kbps) and can be kept without
// re-encoding when the target is opus too. onProgress, if set, gets bytes
// done and the total (0 when unknown) as the download runs.
func Fetch(ctx context.Context, y ytdlp.Runner, url, dir string, onProgress func(done, total int64)) (Source, error) {
	var result string
	err := y.RunLines(ctx, func(line string) {
		rest, ok := strings.CutPrefix(line, progressPrefix)
		if !ok {
			result = line
			return
		}
		if onProgress != nil {
			if done, total, ok := parseProgress(rest); ok {
				onProgress(done, total)
			}
		}
	},
		"--format", "bestaudio[acodec=opus]/bestaudio",
		"--no-playlist", "--no-warnings",
		"--progress", "--newline",
		"--progress-template", "download:"+progressPrefix+"%(progress.downloaded_bytes)s %(progress.total_bytes)s %(progress.total_bytes_estimate)s",
		"--output", filepath.Join(dir, "source.%(ext)s"),
		"--print", "after_move:%(filepath)s\t%(acodec)s\t%(abr)s",
		"--", url)
	if err != nil {
		return Source{}, fmt.Errorf("download failed: %w", err)
	}
	return parsePrint(result)
}

// parseProgress reads "downloaded total estimate"; yt-dlp prints "NA" for
// whichever total it doesn't know.
func parseProgress(s string) (done, total int64, ok bool) {
	f := strings.Fields(s)
	if len(f) != 3 {
		return 0, 0, false
	}
	done, err := strconv.ParseInt(f[0], 10, 64)
	if err != nil {
		return 0, 0, false
	}
	for _, t := range f[1:] {
		if v, err := strconv.ParseFloat(t, 64); err == nil && v > 0 {
			return done, int64(v), true
		}
	}
	return done, 0, true
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
